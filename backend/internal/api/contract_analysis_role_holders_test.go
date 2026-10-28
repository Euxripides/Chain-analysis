package api

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

func roleTopic(address string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(address, "0x")
}

func roleLog(block, index uint64, kind, role, account, sender string) contractLog {
	topic := contractTopicRoleGranted
	if kind == "revoke" {
		topic = contractTopicRoleRevoked
	}
	return contractLog{Block: block, Index: index, Tx: fmt.Sprintf("0x%064x", block*100+index), Topics: []string{topic, role, roleTopic(account), roleTopic(sender)}}
}

func TestRoleHoldersReplayEventsAndCheckHasRole(t *testing.T) {
	admin, oldAdmin, minter, stale := "0x00000000000000000000000000000000000000a1", "0x00000000000000000000000000000000000000a0", "0x00000000000000000000000000000000000000b2", "0x00000000000000000000000000000000000000b9"
	adminRole, minterRole := "0x"+strings.Repeat("0", 64), method.Topic0Of("MINTER_ROLE")
	result := probeResult("hasRole(bytes32,address)", "mint(address,uint256)")
	contractApplyAccessProbes(result, func(from string, data []byte) ([]byte, error) {
		if "0x"+hex.EncodeToString(data[:4]) == method.SelectorOf("mint(address,uint256)") {
			role, _ := hex.DecodeString(strings.TrimPrefix(minterRole, "0x"))
			return nil, probeRevert(append(append(mustHex(contractSelAccessControl), contractAddressWord(from)...), role...), "execution reverted")
		}
		return make([]byte, 32), nil
	})
	logs := []contractLog{
		// Out of order on purpose: replay must follow block and log index.
		roleLog(30, 2, "revoke", adminRole, oldAdmin, oldAdmin),
		roleLog(10, 0, "grant", adminRole, oldAdmin, oldAdmin),
		roleLog(30, 1, "grant", adminRole, admin, oldAdmin),
		roleLog(20, 0, "grant", minterRole, minter, oldAdmin),
		roleLog(21, 0, "grant", minterRole, stale, oldAdmin),
		roleLog(99, 0, "grant", minterRole, oldAdmin, admin), // after the snapshot block
	}
	result.Snapshot.BlockNumber = 50
	checks := 0
	contractApplyRoleHolders(result, func(topics [][]string) ([]contractLog, bool, error) {
		if len(topics) != 1 || topics[0][0] != contractTopicRoleGranted || topics[0][1] != contractTopicRoleRevoked {
			t.Fatalf("topics = %v", topics)
		}
		return logs, true, nil
	}, func(from string, data []byte) ([]byte, error) {
		checks++
		w := make([]byte, 32)
		if !strings.HasSuffix(hex.EncodeToString(data[36:68]), strings.TrimPrefix(stale, "0x")) {
			w[31] = 1
		}
		return w, nil
	})
	holders := map[string]map[string]any{}
	for _, raw := range result.Sections["role_holders"] {
		r := rowMap(raw)
		holders[r["role"].(string)] = r
	}
	adminMembers := holders[adminRole]["members"].([]any)
	if len(adminMembers) != 1 || adminMembers[0].(map[string]any)["address"] != admin || adminMembers[0].(map[string]any)["granted_by"] != oldAdmin {
		t.Fatalf("admin members = %v", adminMembers)
	}
	if holders[adminRole]["role_name"] != "DEFAULT_ADMIN_ROLE" || len(holders[adminRole]["event_history"].([]any)) != 3 {
		t.Fatalf("admin row = %v", holders[adminRole])
	}
	if checks != 3 {
		t.Fatalf("hasRole checks = %d", checks)
	}
	for _, raw := range result.Sections["caller_access_probes"] {
		r := rowMap(raw)
		if r["selector"] == method.SelectorOf("mint(address,uint256)") {
			if got := stringList(r["role_members"]); len(got) != 1 || got[0] != minter {
				t.Fatalf("mint role members = %v (stale holder must be dropped by hasRole)", got)
			}
		}
	}
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		if m["method_id"] == "m-mint(address,uint256)" {
			if got := stringList(m["caller_access_probe"].(map[string]any)["role_members"]); len(got) != 1 || got[0] != minter {
				t.Fatalf("method summary role members = %v", got)
			}
		}
	}
}

func TestRoleHoldersSkipNonAccessControlAndReportFailures(t *testing.T) {
	result := probeResult("owner()")
	contractApplyRoleHolders(result, func([][]string) ([]contractLog, bool, error) {
		t.Fatal("fetched logs for a non-AccessControl contract")
		return nil, false, nil
	}, nil)
	if len(result.Sections["role_holders"]) != 0 || len(result.Sections["coverage"]) != 0 {
		t.Fatal("non-AccessControl contract produced role output")
	}
	result = probeResult("hasRole(bytes32,address)")
	contractApplyRoleHolders(result, func([][]string) ([]contractLog, bool, error) { return nil, false, errors.New("down") }, nil)
	cov := rowMap(result.Sections["coverage"][0])
	if cov["dimension"] != "role_holders" || cov["reason"] != "role_events_unavailable" || cov["complete"] != false {
		t.Fatalf("coverage = %v", cov)
	}
}
