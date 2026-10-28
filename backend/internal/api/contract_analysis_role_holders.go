package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// Who holds each AccessControl role: RoleGranted/RoleRevoked events up to the snapshot block are replayed in
// order and every resulting member is checked with hasRole at that block. Event replay alone is history, the
// hasRole check is the current-state evidence; neither proves what the role can do.

const (
	contractRoleLogPages     = 3
	contractRoleLogPageSize  = 1000
	contractRoleMaxChecks    = 48
	contractRoleMaxHistory   = 64
	contractRoleTopicAccount = 2
	contractRoleTopicSender  = 3
)

var (
	contractTopicRoleGranted = method.Topic0Of("RoleGranted(bytes32,address,address)")
	contractTopicRoleRevoked = method.Topic0Of("RoleRevoked(bytes32,address,address)")
)

type contractLog struct {
	Block, Index uint64
	Tx           string
	Topics       []string
}

// contractLogsFunc returns the target's logs matching topics up to the snapshot block; complete is false when
// paging stopped before the end.
type contractLogsFunc func(topics [][]string) (logs []contractLog, complete bool, err error)

func (s *Server) contractRoleLogs(ctx context.Context, q ca.Request, result *ca.Result) contractLogsFunc {
	return func(topics [][]string) ([]contractLog, bool, error) {
		if s.deps.Prov == nil {
			return nil, false, fmt.Errorf("log provider unavailable")
		}
		from, to := uint64(0), result.Snapshot.BlockNumber
		var logs []contractLog
		token := ""
		for page := 0; page < contractRoleLogPages; page++ {
			res, e := s.deps.Prov.Fetch(ctx, provider.FetchRequest{Chain: q.Chain, Dataset: "logs", Address: contractProbeTarget(result.Snapshot),
				FromBlock: &from, ToBlock: &to, PageSize: contractRoleLogPageSize, PageToken: token, Topics: topics, Priority: "background"})
			if e != nil {
				return logs, false, e
			}
			for _, raw := range res.Rows {
				var row struct {
					BlockNumber, LogIndex, TransactionHash string
					Topics                                 []string
				}
				if json.Unmarshal(raw, &row) != nil {
					continue
				}
				block, e1 := strconv.ParseUint(strings.TrimPrefix(row.BlockNumber, "0x"), 16, 64)
				index, e2 := strconv.ParseUint(strings.TrimPrefix(row.LogIndex, "0x"), 16, 64)
				if e1 != nil || e2 != nil {
					continue
				}
				logs = append(logs, contractLog{Block: block, Index: index, Tx: strings.ToLower(row.TransactionHash), Topics: row.Topics})
			}
			if !res.HasMore || res.NextPageToken == "" {
				return logs, true, nil
			}
			token = res.NextPageToken
		}
		return logs, false, nil
	}
}

func contractTopicAddress(topic string) string {
	topic = strings.ToLower(strings.TrimPrefix(topic, "0x"))
	if len(topic) != 64 {
		return ""
	}
	return "0x" + topic[24:]
}

// contractApplyRoleHolders adds the role_holders section and attaches members to role-gated probe rows. It runs
// only for contracts that look like AccessControl (a hasRole entry or an AccessControl revert).
func contractApplyRoleHolders(result *ca.Result, fetch contractLogsFunc, call contractCallFunc) {
	hasRole := false
	for _, raw := range result.Sections["methods"] {
		if sel, _ := rowMap(raw)["selector"].(string); strings.EqualFold(sel, contractSelHasRole) {
			hasRole = true
		}
	}
	gated := hasRole
	for _, raw := range result.Sections["caller_access_probes"] {
		if rowMap(raw)["revert_signal"] == "access_control_role" {
			gated = true
		}
	}
	if !gated {
		return
	}
	target := contractProbeTarget(result.Snapshot)
	coverage := map[string]any{"dimension": "role_holders", "complete": false, "target_address": target,
		"basis": "RoleGranted/RoleRevoked events up to the snapshot block, members checked with hasRole at the snapshot block"}
	logs, complete, err := fetch([][]string{{contractTopicRoleGranted, contractTopicRoleRevoked}})
	if err != nil {
		coverage["reason"] = "role_events_unavailable"
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(coverage))
		return
	}
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].Block < logs[j].Block || logs[i].Block == logs[j].Block && logs[i].Index < logs[j].Index
	})
	type member struct {
		address, grantedBy, grantedTx string
		grantedBlock                  uint64
	}
	members := map[string]map[string]*member{}
	history := map[string][]map[string]any{}
	events := 0
	var roles []string
	for _, l := range logs {
		if l.Block > result.Snapshot.BlockNumber || len(l.Topics) < 4 {
			continue
		}
		kind := map[string]string{contractTopicRoleGranted: "RoleGranted", contractTopicRoleRevoked: "RoleRevoked"}[strings.ToLower(l.Topics[0])]
		account, sender := contractTopicAddress(l.Topics[contractRoleTopicAccount]), contractTopicAddress(l.Topics[contractRoleTopicSender])
		if kind == "" || account == "" || len(l.Topics[1]) != 66 {
			continue
		}
		role := strings.ToLower(l.Topics[1])
		if members[role] == nil {
			members[role] = map[string]*member{}
			roles = append(roles, role)
		}
		events++
		if kind == "RoleGranted" {
			members[role][account] = &member{address: account, grantedBy: sender, grantedTx: l.Tx, grantedBlock: l.Block}
		} else {
			delete(members[role], account)
		}
		if len(history[role]) < contractRoleMaxHistory {
			history[role] = append(history[role], map[string]any{"event": kind, "account": account, "sender": sender, "block_number": l.Block, "tx_hash": l.Tx})
		}
	}
	p := &contractProber{call: call}
	byRole := map[string][]string{}
	rows := []json.RawMessage{}
	for _, role := range roles {
		roleWord, _ := hex.DecodeString(role[2:])
		list := []map[string]any{}
		addresses := make([]string, 0, len(members[role]))
		for a := range members[role] {
			addresses = append(addresses, a)
		}
		sort.Strings(addresses)
		for _, a := range addresses {
			m := members[role][a]
			entry := map[string]any{"address": a, "granted_by": m.grantedBy, "granted_tx": m.grantedTx, "granted_block": m.grantedBlock}
			if hasRole && p.calls < contractRoleMaxChecks {
				if r, ok := p.do(contractProbeCaller, contractProbeData(contractSelHasRole, roleWord, contractAddressWord(a))); ok && r.Success && len(r.Data) == 32 {
					entry["has_role_at_snapshot"] = r.Data[31] == 1
				}
			}
			if v, checked := entry["has_role_at_snapshot"]; !checked || v == true {
				byRole[role] = append(byRole[role], a)
			}
			list = append(list, entry)
		}
		rows = append(rows, ca.Row(map[string]any{"role_holder_id": ca.ID(target, role, "role-holders"), "target_address": target,
			"role": role, "role_name": contractRoleNames[role], "members": list, "event_history": history[role],
			"history_truncated": len(history[role]) >= contractRoleMaxHistory, "events_complete": complete,
			"block_number": result.Snapshot.BlockNumber, "authorization_sufficiency_verified": false}))
	}
	result.Sections["role_holders"] = rows
	for i, raw := range result.Sections["caller_access_probes"] {
		r := rowMap(raw)
		if role, _ := r["required_role"].(string); byRole[role] != nil {
			r["role_members"] = byRole[role]
			result.Sections["caller_access_probes"][i] = ca.Row(r)
		}
	}
	for i, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		summary, ok := m["caller_access_probe"].(map[string]any)
		if !ok {
			continue
		}
		if role, _ := summary["required_role"].(string); byRole[role] != nil {
			summary["role_members"] = byRole[role]
			m["caller_access_probe"] = summary
			result.Sections["methods"][i] = ca.Row(m)
		}
	}
	coverage["complete"], coverage["roles"], coverage["events"], coverage["has_role_checks"] = complete, len(roles), events, p.calls
	result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(coverage))
}
