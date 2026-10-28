package api

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// Behavioural evidence for caller restrictions that static guards miss (closed-source bytecode, shared
// internal access routines). Each entry selector is simulated with eth_call at the pinned block, first from a
// fixed unprivileged caller and, when that reverts, from on-chain controller candidates. These are
// observations at one block with zeroed arguments; they never prove authorization sufficiency.

const (
	contractProbeMaxSelectors  = 96
	contractProbeMaxCalls      = 160
	contractProbeMaxCandidates = 4
	contractProbeArgWords      = 8
)

type contractCallFunc func(from string, data []byte) ([]byte, error)

var contractProbeCaller = "0x" + hex.EncodeToString(method.Keccak256([]byte("contract-analysis/access-probe/v1"))[12:])

var (
	contractSelOwnable       = method.SelectorOf("OwnableUnauthorizedAccount(address)")
	contractSelAccessControl = method.SelectorOf("AccessControlUnauthorizedAccount(address,bytes32)")
	contractSelAccessManaged = method.SelectorOf("AccessManagedUnauthorized(address)")
	contractSelErrorString   = method.SelectorOf("Error(string)")
	contractSelPanic         = method.SelectorOf("Panic(uint256)")
	contractSelRoleMember    = method.SelectorOf("getRoleMember(bytes32,uint256)")
	contractSelHasRole       = method.SelectorOf("hasRole(bytes32,address)")
	contractAccessErrorNames = map[string]string{}
	contractRoleNames        = map[string]string{"0x" + strings.Repeat("0", 64): "DEFAULT_ADMIN_ROLE"}
	contractAccessText       = regexp.MustCompile(`(?i)caller is not|not (the )?owner|only ?owner|unauthori[sz]ed|not authori[sz]ed|access denied|permission denied|no permission|missing role|forbidden|only ?admin|not ?admin\b|only ?operator|not ?operator\b|only ?governance|only ?(the )?manager|!owner`)
	contractRoleText         = regexp.MustCompile(`(?i)is missing role (0x[0-9a-f]{64})`)
	// Getters whose non-zero address result is a controller candidate, in probe order.
	contractControllerGetters = []string{"owner()", "getOwner()", "admin()", "governance()", "operator()", "manager()"}
)

func init() {
	for _, sig := range []string{"Unauthorized()", "Unauthorized(address)", "NotOwner()", "OnlyOwner()", "NotAuthorized()", "NotAuthorized(address)",
		"CallerNotOwner()", "CallerIsNotOwner()", "NotAdmin()", "OnlyAdmin()", "NotOperator()", "OnlyOperator()", "Forbidden()", "AccessDenied()",
		"NotGovernance()", "OnlyGovernance()", "NotManager()", "OnlyManager()", "InvalidCaller()", "CallerNotAllowed()"} {
		contractAccessErrorNames[method.SelectorOf(sig)] = sig
	}
	for _, name := range []string{"MINTER_ROLE", "PAUSER_ROLE", "BURNER_ROLE", "ADMIN_ROLE", "OPERATOR_ROLE", "MANAGER_ROLE", "UPGRADER_ROLE",
		"GOVERNOR_ROLE", "GOVERNANCE_ROLE", "KEEPER_ROLE", "ROUTER_ROLE", "EXECUTOR_ROLE", "PROPOSER_ROLE", "CANCELLER_ROLE", "TREASURER_ROLE",
		"FEE_MANAGER_ROLE", "AMO_MANAGER_ROLE", "DISTRIBUTOR_ROLE", "SNAPSHOT_ROLE", "WHITELIST_ROLE", "BLACKLIST_ROLE", "REWARD_MANAGER_ROLE",
		"VAULT_ROLE", "BRIDGE_ROLE", "ORACLE_ROLE", "CONTROLLER_ROLE", "STRATEGIST_ROLE", "GUARDIAN_ROLE", "SIGNER_ROLE", "RELAYER_ROLE",
		"MIGRATOR_ROLE", "LIQUIDITY_ROLE", "OWNER_ROLE"} {
		contractRoleNames[method.Topic0Of(name)] = name
	}
}

type contractRevert struct{ Selector, Signal, Reason, Role string }

func (r contractRevert) access() bool {
	return r.Signal == "ownable_unauthorized" || r.Signal == "access_control_role" || r.Signal == "access_manager" ||
		r.Signal == "access_error_name" || r.Signal == "access_error_text"
}

// contractClassifyRevert names a revert payload. message is the provider text, used only when there is no payload.
func contractClassifyRevert(data []byte, message string) contractRevert {
	if len(data) < 4 {
		reason := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(message), "execution reverted"))
		reason = strings.TrimSpace(strings.TrimPrefix(reason, ":"))
		if m := contractRoleText.FindStringSubmatch(reason); m != nil {
			return contractRevert{Signal: "access_control_role", Reason: reason, Role: strings.ToLower(m[1])}
		}
		if reason != "" && contractAccessText.MatchString(reason) {
			return contractRevert{Signal: "access_error_text", Reason: reason}
		}
		return contractRevert{Signal: "empty", Reason: reason}
	}
	r := contractRevert{Selector: "0x" + hex.EncodeToString(data[:4])}
	switch r.Selector {
	case contractSelOwnable:
		r.Signal, r.Reason = "ownable_unauthorized", "OwnableUnauthorizedAccount(address)"
	case contractSelAccessControl:
		r.Signal, r.Reason = "access_control_role", "AccessControlUnauthorizedAccount(address,bytes32)"
		if len(data) >= 68 {
			r.Role = "0x" + hex.EncodeToString(data[36:68])
		}
	case contractSelAccessManaged:
		r.Signal, r.Reason = "access_manager", "AccessManagedUnauthorized(address)"
	case contractSelPanic:
		r.Signal, r.Reason = "panic", "Panic(uint256)"
	case contractSelErrorString:
		r.Signal, r.Reason = "error_string", contractDecodeErrorString(data)
		if m := contractRoleText.FindStringSubmatch(r.Reason); m != nil {
			r.Signal, r.Role = "access_control_role", strings.ToLower(m[1])
		} else if contractAccessText.MatchString(r.Reason) {
			r.Signal = "access_error_text"
		}
	default:
		if name, ok := contractAccessErrorNames[r.Selector]; ok {
			r.Signal, r.Reason = "access_error_name", name
		} else {
			r.Signal = "custom_error"
		}
	}
	return r
}

func contractDecodeErrorString(data []byte) string {
	if len(data) < 68 {
		return ""
	}
	offset, length := contractWord(data[4:36]), contractWord(data[36:68])
	if offset != 32 || length > uint64(len(data)-68) {
		return ""
	}
	return string(data[68 : 68+length])
}

// contractWord reads a 32-byte big-endian word that fits in uint64, else MaxUint64.
func contractWord(w []byte) uint64 {
	for _, b := range w[:24] {
		if b != 0 {
			return ^uint64(0)
		}
	}
	return binary.BigEndian.Uint64(w[24:])
}

type contractProbeCall struct {
	Success bool
	Revert  contractRevert
	Data    []byte
	Err     string
}

type contractProber struct {
	call  contractCallFunc
	calls int
	stop  string
}

func (p *contractProber) do(from string, data []byte) (contractProbeCall, bool) {
	if p.stop != "" || p.calls >= contractProbeMaxCalls {
		return contractProbeCall{}, false
	}
	p.calls++
	out, e := p.call(from, data)
	if e == nil {
		return contractProbeCall{Success: true, Data: out}, true
	}
	var re *rpc.Error
	if errors.As(e, &re) && re.Class == rpc.ClassExecutionReverted {
		return contractProbeCall{Revert: contractClassifyRevert(re.RevertData, re.Message), Data: re.RevertData}, true
	}
	reason := "rpc_error"
	if re != nil {
		reason = string(re.Class)
	}
	if re != nil && (re.Class == rpc.ClassBudgetExceeded || re.Class == rpc.ClassArchiveRequired) {
		p.stop = reason
	}
	return contractProbeCall{Err: reason}, true
}

func contractProbeData(selector string, words ...[]byte) []byte {
	sel, _ := hex.DecodeString(strings.TrimPrefix(selector, "0x"))
	data := append([]byte{}, sel...)
	for _, w := range words {
		data = append(data, w...)
	}
	for len(data) < 4+32*contractProbeArgWords {
		data = append(data, make([]byte, 32)...)
	}
	return data
}

func contractAddressWord(address string) []byte {
	b, _ := hex.DecodeString(strings.TrimPrefix(strings.ToLower(address), "0x"))
	return append(make([]byte, 12), b...)
}

// contractGetterValue decodes the return of a no-argument function (signature or public candidate ending in "()"):
// an address, a uint256 word or a short ABI string. Calls with arguments ran with zeros and are not decoded.
func contractGetterValue(signature, candidate string, out []byte) map[string]any {
	name := signature
	if name == "" {
		name = candidate
	}
	if !strings.HasSuffix(name, "()") || len(out) < 32 {
		return nil
	}
	value := map[string]any{"returned_word": "0x" + hex.EncodeToString(out[:32])}
	if len(out) == 32 {
		// A left-padded word above 2^128 is read as an address; small values are numbers.
		if n := new(big.Int).SetBytes(out); n.BitLen() <= 160 {
			value["returned_uint"] = n.String()
			if n.BitLen() > 128 {
				value["returned_address"] = contractProbeAddress(out)
			}
		}
		return value
	}
	if contractWord(out[:32]) == 32 && len(out) >= 64 {
		if n := contractWord(out[32:64]); n <= 256 && uint64(len(out)) >= 64+n {
			value["returned_text"] = string(out[64 : 64+n])
		}
	}
	return value
}

func contractProbeAddress(out []byte) string {
	if len(out) != 32 {
		return ""
	}
	return strings.ToLower(nonzeroAddress(out))
}

// contractProbeTarget is the account whose storage runs this code when called: the proxy for an implementation
// reached by DELEGATECALL, the dependency itself for a CALL dependency, else the entry.
func contractProbeTarget(snapshot ca.Snapshot) string {
	if snapshot.StorageAddress != "" {
		return snapshot.StorageAddress
	}
	return snapshot.EntryAddress
}

func (s *Server) contractProbeCallerAccess(ctx context.Context, q ca.Request, result *ca.Result) {
	if result.Snapshot.StateGranularity != "block_end" {
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "caller_access_probes", "complete": false, "reason": "probes_require_block_end_state"}))
		return
	}
	to, block := chain.Address(contractProbeTarget(result.Snapshot)), chain.AtBlock(result.Snapshot.BlockNumber)
	call := func(from string, data []byte) ([]byte, error) {
		return s.deps.ChainRead.CallContract(ctx, chainread.ContractCall{Chain: q.Chain, To: to, From: chain.Address(from), Data: data, BlockRef: block}, contractOptions(ctx, q))
	}
	contractApplyAccessProbes(result, call)
	holders, code := s.contractHolderSources(ctx, q, result)
	contractApplyTokenHolders(result, holders, code)
	contractApplyRoleHolders(result, s.contractRoleLogs(ctx, q, result), call)
}

// contractApplyAccessProbes adds the caller_access_probes section, a coverage row and a per-method summary.
func contractApplyAccessProbes(result *ca.Result, call contractCallFunc) {
	type entry struct {
		selector, signature, candidate string
		methodIDs                      []string
		state                          bool
	}
	var entries []*entry
	bySelector := map[string]*entry{}
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		sel, _ := m["selector"].(string)
		if len(sel) != 10 {
			continue
		}
		sel = strings.ToLower(sel)
		e := bySelector[sel]
		if e == nil {
			e = &entry{selector: sel}
			bySelector[sel] = e
			entries = append(entries, e)
		}
		e.methodIDs = append(e.methodIDs, fmt.Sprint(m["method_id"]))
		if sig, _ := m["signature"].(string); sig != "" {
			e.signature = sig
		} else if c, _ := m["candidate_name"].(string); c != "" && e.candidate == "" {
			if list, _ := m["signature_candidates"].([]any); len(list) > 0 {
				e.candidate, _ = list[0].(map[string]any)["signature"].(string)
			}
		}
		for _, b := range stringList(m["behaviors"]) {
			if b == "修改合约状态" || b == "可接收原生币" || b == "外部合约交互" || strings.HasSuffix(b, "支付") || strings.HasSuffix(b, "调用") {
				e.state = true
			}
		}
	}
	p, target := &contractProber{call: call}, contractProbeTarget(result.Snapshot)
	type candidate struct{ Address, Source string }
	var candidates []candidate
	addCandidate := func(address, source string) {
		if address == "" || address == contractProbeCaller || len(candidates) >= contractProbeMaxCandidates {
			return
		}
		for _, c := range candidates {
			if c.Address == address {
				return
			}
		}
		candidates = append(candidates, candidate{address, source})
	}
	for _, sig := range contractControllerGetters {
		if bySelector[method.SelectorOf(sig)] == nil {
			continue
		}
		if r, ok := p.do(contractProbeCaller, contractProbeData(method.SelectorOf(sig))); ok && r.Success {
			addCandidate(contractProbeAddress(r.Data), sig)
		}
	}
	type outcome struct {
		e     *entry
		first contractProbeCall
		ok    bool
	}
	var outcomes []outcome
	for i, e := range entries {
		if i >= contractProbeMaxSelectors {
			outcomes = append(outcomes, outcome{e: e})
			continue
		}
		r, ok := p.do(contractProbeCaller, contractProbeData(e.selector))
		outcomes = append(outcomes, outcome{e, r, ok})
	}
	roles := []string{}
	for _, o := range outcomes {
		if role := o.first.Revert.Role; role != "" && !contains(roles, role) {
			roles = append(roles, role)
		}
	}
	if bySelector[contractSelRoleMember] != nil {
		for _, role := range append(roles, "0x"+strings.Repeat("0", 64)) {
			roleWord, _ := hex.DecodeString(role[2:])
			if r, ok := p.do(contractProbeCaller, contractProbeData(contractSelRoleMember, roleWord, make([]byte, 32))); ok && r.Success {
				name := contractRoleNames[role]
				if name == "" {
					name = role
				}
				addCandidate(contractProbeAddress(r.Data), "getRoleMember("+name+",0)")
			}
		}
	}
	roleHolders := map[string][]map[string]any{}
	if bySelector[contractSelHasRole] != nil {
		for _, role := range roles {
			roleWord, _ := hex.DecodeString(role[2:])
			for _, c := range candidates {
				if r, ok := p.do(contractProbeCaller, contractProbeData(contractSelHasRole, roleWord, contractAddressWord(c.Address))); ok && r.Success && len(r.Data) == 32 {
					roleHolders[role] = append(roleHolders[role], map[string]any{"address": c.Address, "holds_role": r.Data[31] == 1, "source": "hasRole(bytes32,address)"})
				}
			}
		}
	}
	counts := map[string]int{}
	rows := make([]json.RawMessage, 0, len(outcomes))
	summaries := map[string]map[string]any{}
	for _, o := range outcomes {
		probeID := ca.ID(target, result.Snapshot.CodeAddress, o.e.selector, "caller-access-probe")
		row := map[string]any{"probe_id": probeID, "method_ids": o.e.methodIDs, "selector": o.e.selector, "signature": o.e.signature, "signature_candidate": o.e.candidate,
			"target_address": target, "code_address": result.Snapshot.CodeAddress, "entry_address": result.Snapshot.EntryAddress, "block_number": result.Snapshot.BlockNumber, "block_hash": result.Snapshot.BlockHash,
			"probe_caller": contractProbeCaller, "zeroed_argument_words": contractProbeArgWords, "state_changing_hint": o.e.state,
			"basis": "eth_call_simulation_at_snapshot_block", "authorization_sufficiency_verified": false}
		classification := "not_probed"
		confirmed := []string{}
		if o.ok {
			switch {
			case o.first.Err != "":
				row["outcome"], row["error_class"] = "error", o.first.Err
				classification = "not_probed"
			case o.first.Success:
				row["outcome"] = "success"
				classification = "callable_by_probe_caller"
				for k, v := range contractGetterValue(o.e.signature, o.e.candidate, o.first.Data) {
					row[k] = v
				}
			default:
				row["outcome"] = "reverted"
				row["revert_selector"], row["revert_signal"], row["revert_reason"] = o.first.Revert.Selector, o.first.Revert.Signal, o.first.Revert.Reason
				if len(o.first.Data) > 0 {
					row["revert_data"] = "0x" + hex.EncodeToString(o.first.Data)
				}
				if o.first.Revert.Role != "" {
					row["required_role"], row["required_role_name"] = o.first.Revert.Role, contractRoleNames[o.first.Revert.Role]
					if holders := roleHolders[o.first.Revert.Role]; len(holders) > 0 {
						row["role_holder_checks"] = holders
					}
				}
				checks := []map[string]any{}
				for _, c := range candidates {
					r, ok := p.do(c.Address, contractProbeData(o.e.selector))
					if !ok {
						break
					}
					check := map[string]any{"candidate": c.Address, "candidate_source": c.Source}
					passes := false
					switch {
					case r.Err != "":
						check["outcome"], check["error_class"] = "error", r.Err
					case r.Success:
						check["outcome"], passes = "success", true
					default:
						check["outcome"], check["revert_selector"], check["revert_signal"], check["revert_reason"] = "reverted", r.Revert.Selector, r.Revert.Signal, r.Revert.Reason
						passes = !r.Revert.access() && (r.Revert.Selector != o.first.Revert.Selector || r.Revert.Reason != o.first.Revert.Reason || r.Revert.Signal != o.first.Revert.Signal)
					}
					check["passes_caller_check"] = passes
					if passes {
						confirmed = append(confirmed, c.Address)
					}
					checks = append(checks, check)
				}
				row["controller_checks"] = checks
				switch {
				case o.first.Revert.access():
					classification = "caller_restricted"
				case len(confirmed) > 0:
					classification = "caller_restricted_by_differential"
				default:
					classification = "inconclusive"
				}
			}
		}
		row["classification"], row["confirmed_callers"] = classification, confirmed
		counts[classification]++
		rows = append(rows, ca.Row(row))
		summary := map[string]any{"probe_id": probeID, "classification": classification, "confirmed_callers": confirmed}
		if role, ok := row["required_role"]; ok {
			summary["required_role"], summary["required_role_name"] = role, row["required_role_name"]
		}
		for _, id := range o.e.methodIDs {
			summaries[id] = summary
		}
	}
	for i, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		summary := summaries[fmt.Sprint(m["method_id"])]
		if summary == nil {
			continue
		}
		m["caller_access_probe"] = summary
		if c := summary["classification"]; (c == "caller_restricted" || c == "caller_restricted_by_differential") && !contains(stringList(m["behaviors"]), "调用者条件") {
			m["behaviors"] = append(stringList(m["behaviors"]), "调用者条件（链上模拟）")
		}
		result.Sections["methods"][i] = ca.Row(m)
	}
	result.Sections["caller_access_probes"] = rows
	candidateRows := []map[string]any{}
	for _, c := range candidates {
		candidateRows = append(candidateRows, map[string]any{"address": c.Address, "source": c.Source})
	}
	coverage := map[string]any{"dimension": "caller_access_probes", "complete": false, "basis": "eth_call_simulation_at_snapshot_block",
		"probe_caller": contractProbeCaller, "target_address": target, "code_address": result.Snapshot.CodeAddress, "selectors": len(entries), "calls": p.calls, "controller_candidates": candidateRows,
		"caller_restricted": counts["caller_restricted"], "caller_restricted_by_differential": counts["caller_restricted_by_differential"],
		"callable_by_probe_caller": counts["callable_by_probe_caller"], "inconclusive": counts["inconclusive"], "not_probed": counts["not_probed"],
		"limitations": "zeroed arguments at one block: checks after an argument revert stay hidden, and success does not show the call is harmless or will succeed with other arguments"}
	if p.stop != "" {
		coverage["stopped"] = p.stop
	}
	result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(coverage))
	if result.Metrics != nil {
		result.Metrics["caller_access_probe_calls"] = p.calls
	}
}
