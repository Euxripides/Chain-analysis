package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

// One compact row per analyzed code that an investigating agent reads first: every entry function with its name,
// fund effects, external calls and caller gate, the getter configuration, related addresses and fee formulas.
// Name-based hints are search aids for the agent, not findings.

const (
	contractProfileMaxFunctions = 160
	contractProfileMaxFormulas  = 24
	contractProfileMaxRelated   = 80
)

var (
	contractHexAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{1,40}$`)
	// Each hint matches a word of the camelCase/snake_case name by prefix, so "emergencyWithdraw" is not "draw".
	contractProfileHints = map[string][]string{
		"referral":         {"refer", "invit", "upline", "downline", "parent", "sponsor", "recommend", "leader", "team", "relation", "bind", "inviter"},
		"distribution":     {"reward", "dividend", "bonus", "commission", "share", "profit", "static", "dynamic", "level", "node", "partner", "genesis", "super", "rebate", "airdrop", "distribut", "pending"},
		"deposit":          {"deposit", "invest", "stake", "buy", "join", "register", "subscribe", "purchase", "participat", "enter"},
		"withdrawal":       {"withdraw", "claim", "redeem", "release", "unstake", "exit", "sell", "harvest", "payout", "cashout"},
		"gambling":         {"bet", "lottery", "draw", "random", "jackpot", "prize", "game", "round", "winner", "dice", "roll", "spin", "ticket", "vrf"},
		"privileged_funds": {"emergency", "rescue", "recover", "sweep", "drain", "skim", "migrate", "upgrade", "signer"},
	}
	contractNameWord = regexp.MustCompile(`[A-Z]+[a-z0-9]*|[a-z0-9]+`)
)

// contractNormalizeAddress left-pads a hex literal that lost leading zeros; other text is returned unchanged.
func contractNormalizeAddress(text string) string {
	text = strings.TrimSpace(text)
	if !contractHexAddress.MatchString(text) || len(text) < 34 {
		return text
	}
	return "0x" + strings.Repeat("0", 42-len(text)) + strings.ToLower(text[2:])
}

// contractNameMatches reports whether any word of a camelCase or snake_case name starts with one of the prefixes.
func contractNameMatches(name string, prefixes []string) bool {
	if strings.ToUpper(name) == name {
		name = strings.ToLower(name)
	}
	for _, word := range contractNameWord.FindAllString(name, -1) {
		word = strings.ToLower(word)
		for _, p := range prefixes {
			if strings.HasPrefix(word, p) {
				return true
			}
		}
	}
	return false
}

func contractProfileName(m map[string]any) (string, string) {
	if sig, _ := m["signature"].(string); sig != "" {
		return sig, "verified"
	}
	if list, _ := m["signature_candidates"].([]any); len(list) > 0 {
		if sig, _ := list[0].(map[string]any)["signature"].(string); sig != "" {
			return sig, "public_candidate"
		}
	}
	if name, _ := m["name"].(string); name != "" {
		return name, "engine_label"
	}
	return fmt.Sprint(m["selector"]), "selector_only"
}

func contractBuildSchemeProfile(result *ca.Result) {
	rowsOf := func(section string) []map[string]any {
		out := []map[string]any{}
		for _, raw := range result.Sections[section] {
			out = append(out, rowMap(raw))
		}
		return out
	}
	funds, calls, probes, roles, semantics := map[string][]map[string]any{}, map[string][]map[string]any{}, map[string]map[string]any{}, rowsOf("role_holders"), rowsOf("semantics")
	for _, f := range rowsOf("funds") {
		funds[fmt.Sprint(f["method_id"])] = append(funds[fmt.Sprint(f["method_id"])], f)
	}
	for _, c := range rowsOf("calls") {
		calls[fmt.Sprint(c["method_id"])] = append(calls[fmt.Sprint(c["method_id"])], c)
	}
	for _, p := range rowsOf("caller_access_probes") {
		for _, id := range stringList(p["method_ids"]) {
			probes[id] = p
		}
	}
	roleHeld := map[string]int{}
	for _, r := range roles {
		held := 0
		for _, m := range r["members"].([]any) {
			if member, _ := m.(map[string]any); member["has_role_at_snapshot"] != false {
				held++
			}
		}
		roleHeld[fmt.Sprint(r["role"])] = held
	}
	related := map[string][]string{}
	relate := func(address, relation string) {
		address = contractNormalizeAddress(address)
		if len(address) != 42 || !contractHexAddress.MatchString(address) || strings.Trim(address[2:], "0") == "" || contains(related[address], relation) {
			return
		}
		related[address] = append(related[address], relation)
	}
	basis := "bytecode"
	for _, c := range rowsOf("coverage") {
		if c["dimension"] == "source" && (c["complete"] == true || strings.HasPrefix(fmt.Sprint(c["basis"]), "recompiled")) {
			basis = "source"
		}
	}
	functions, config := []map[string]any{}, []map[string]any{}
	hints := map[string][]string{}
	inflow, outflow, admin := []string{}, []string{}, []map[string]any{}
	for _, m := range rowsOf("methods") {
		selector, _ := m["selector"].(string)
		entryName, _ := m["name"].(string)
		if selector == "" && entryName != "fallback / dispatcher" && entryName != "fallback()" && entryName != "receive()" && !strings.HasPrefix(fmt.Sprint(m["signature"]), "fallback") && !strings.HasPrefix(fmt.Sprint(m["signature"]), "receive") {
			continue
		}
		id := fmt.Sprint(m["method_id"])
		name, nameBasis := contractProfileName(m)
		short := strings.SplitN(name, "(", 2)[0]
		fn := map[string]any{"method_id": id, "selector": selector, "name": name, "name_basis": nameBasis, "behaviors": m["behaviors"]}
		kinds := []string{}
		for _, f := range funds[id] {
			kind := fmt.Sprint(f["kind"])
			if !contains(kinds, kind) {
				kinds = append(kinds, kind)
			}
			to, from := fmt.Sprint(f["to_expression"]), fmt.Sprint(f["from_expression"])
			switch {
			case kind == "native_received_intent" || kind == "token_transfer_from_intent" && strings.Contains(to, "this"):
				if !contains(inflow, name) {
					inflow = append(inflow, name)
				}
			case kind == "native_payment" || kind == "token_transfer_intent" || kind == "token_transfer_from_intent":
				if !contains(outflow, name) {
					outflow = append(outflow, name)
				}
				fn["outflow_to"] = append(stringList(fn["outflow_to"]), to)
				_ = from
			}
			relate(to, "fund recipient in "+short)
		}
		if len(kinds) > 0 {
			fn["fund_effects"] = kinds
		}
		targets := []string{}
		for _, c := range calls[id] {
			t := fmt.Sprint(c["resolved_target_address"])
			if t == "" || t == "<nil>" {
				t = fmt.Sprint(c["target_expression"])
			}
			t = contractNormalizeAddress(t)
			if !contains(targets, t) && len(targets) < 8 {
				targets = append(targets, t)
			}
			relate(t, fmt.Sprint(c["call_type"])+" target in "+short)
		}
		if len(targets) > 0 {
			fn["external_calls"] = targets
		}
		if p := probes[id]; p != nil {
			access := map[string]any{"classification": p["classification"]}
			for _, k := range []string{"required_role_name", "required_role", "revert_reason", "role_members", "confirmed_callers"} {
				if v := p[k]; v != nil && fmt.Sprint(v) != "" && fmt.Sprint(v) != "[]" {
					access[k] = v
				}
			}
			fn["access"] = access
			if c := p["classification"]; c == "caller_restricted" || c == "caller_restricted_by_differential" {
				gate := map[string]any{"function": name}
				for _, k := range []string{"required_role_name", "required_role", "revert_reason", "role_members", "confirmed_callers"} {
					if v, ok := access[k]; ok {
						gate[k] = v
					}
				}
				if role, _ := p["required_role"].(string); role != "" {
					switch held, known := roleHeld[role]; {
					case !known:
						gate["holder_status"] = "unknown_no_role_events"
					case held == 0:
						gate["holder_status"] = "no_current_holder"
					default:
						gate["holder_status"] = "held"
					}
				}
				admin = append(admin, gate)
				for _, a := range append(stringList(p["role_members"]), stringList(p["confirmed_callers"])...) {
					relate(a, "can call "+short)
				}
			}
			for _, k := range []string{"returned_address", "returned_uint", "returned_text"} {
				if v, ok := p[k]; ok {
					fn["value"] = v
					config = append(config, map[string]any{"getter": name, "value": v, "kind": strings.TrimPrefix(k, "returned_")})
					if k == "returned_address" {
						relate(fmt.Sprint(v), "returned by "+short)
					}
					break
				}
			}
		}
		if nameBasis != "selector_only" {
			for hint, words := range contractProfileHints {
				if contractNameMatches(short, words) {
					hints[hint] = append(hints[hint], name)
				}
			}
		}
		if len(functions) < contractProfileMaxFunctions {
			functions = append(functions, fn)
		}
	}
	for _, r := range roles {
		for _, m := range r["members"].([]any) {
			member, _ := m.(map[string]any)
			name := fmt.Sprint(r["role_name"])
			if name == "" || name == "<nil>" {
				name = fmt.Sprint(r["role"])
			}
			if member["has_role_at_snapshot"] != false {
				relate(fmt.Sprint(member["address"]), "holds "+name)
			}
		}
	}
	topHolders := []map[string]any{}
	for _, h := range rowsOf("token_holders") {
		profileHolders, _ := h["top_holders"].([]any)
		for _, raw := range profileHolders {
			entry, _ := raw.(map[string]any)
			if len(topHolders) < 10 {
				topHolders = append(topHolders, map[string]any{"rank": entry["rank"], "address": entry["address"], "share_percent": entry["share_percent"], "is_contract": entry["is_contract"]})
			}
			relate(fmt.Sprint(entry["address"]), fmt.Sprintf("top holder #%v (%v%% of supply)", entry["rank"], entry["share_percent"]))
		}
	}
	formulas := []map[string]any{}
	for _, sem := range semantics {
		if sem["behavior"] == "比例与取整计算" && len(formulas) < contractProfileMaxFormulas {
			text := fmt.Sprint(sem["text"])
			if len(text) > 240 {
				text = text[:240] + "…"
			}
			formulas = append(formulas, map[string]any{"method_id": sem["method_id"], "text": text})
		}
	}
	addresses := make([]string, 0, len(related))
	for a := range related {
		addresses = append(addresses, a)
	}
	sort.Strings(addresses)
	relatedRows := []map[string]any{}
	for _, a := range addresses {
		if len(relatedRows) >= contractProfileMaxRelated {
			break
		}
		relatedRows = append(relatedRows, map[string]any{"address": a, "relations": related[a]})
	}
	for k := range hints {
		sort.Strings(hints[k])
	}
	target := contractProbeTarget(result.Snapshot)
	profile := map[string]any{"profile_id": ca.ID(target, result.Snapshot.CodeAddress, "scheme-profile"), "target_address": target,
		"code_address": result.Snapshot.CodeAddress, "entry_address": result.Snapshot.EntryAddress, "chain": result.Snapshot.Chain,
		"block_number": result.Snapshot.BlockNumber, "analysis_basis": basis, "proxy": result.Snapshot.Proxy,
		"function_count": len(functions), "functions": functions, "inflow_functions": inflow, "outflow_functions": outflow,
		"caller_gated_functions": admin, "configuration_values": config, "top_holders": topHolders, "related_addresses": relatedRows, "fee_and_ratio_formulas": formulas,
		"name_hints": hints, "hint_basis": "function names (verified or public candidates) only; confirm with fund effects, storage and transactions",
		"read_next": []string{"caller_access_probes", "role_holders", "token_holders", "funds", "calls", "semantics", "methods"}}
	result.Sections["scheme_profile"] = append(result.Sections["scheme_profile"], json.RawMessage(ca.Row(profile)))
}
