package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

func TestGetterValueDecodesAddressesNumbersAndStrings(t *testing.T) {
	address := contractAddressWord("0x15b45354ea61b0923ae96c4ced40b84bcf2ecaf6")
	if v := contractGetterValue("", "registry()", address); v["returned_address"] != "0x15b45354ea61b0923ae96c4ced40b84bcf2ecaf6" {
		t.Fatalf("address getter = %v", v)
	}
	small := make([]byte, 32)
	small[31] = 44
	if v := contractGetterValue("SELL_TAX_RATE()", "", small); v["returned_uint"] != "44" || v["returned_address"] != nil {
		t.Fatalf("number getter = %v", v)
	}
	text, _ := hex.DecodeString("0000000000000000000000000000000000000000000000000000000000000020" + "0000000000000000000000000000000000000000000000000000000000000002" + "4d43000000000000000000000000000000000000000000000000000000000000")
	if v := contractGetterValue("symbol()", "", text); v["returned_text"] != "MC" {
		t.Fatalf("string getter = %v", v)
	}
	if v := contractGetterValue("balanceOf(address)", "", address); v != nil {
		t.Fatalf("argument getter decoded: %v", v)
	}
}

func TestNameHintsMatchWordsNotSubstrings(t *testing.T) {
	cases := map[string]map[string]bool{
		"emergencyWithdraw":     {"privileged_funds": true, "withdrawal": true, "gambling": false},
		"executeAMO":            {"referral": false},
		"bindReferrer":          {"referral": true},
		"drawWinner":            {"gambling": true},
		"lastMintDay":           {"deposit": false},
		"accStaticPerShare":     {"distribution": true},
		"MAX_SINGLE_TRADE_USDT": {"gambling": false},
		"withdrawToken":         {"gambling": false, "withdrawal": true},
	}
	for name, want := range cases {
		for hint, expected := range want {
			if got := contractNameMatches(name, contractProfileHints[hint]); got != expected {
				t.Fatalf("%s %s = %v, want %v", name, hint, got, expected)
			}
		}
	}
}

func TestSchemeProfileSummarisesFunctionsFlowsGatesAndAddresses(t *testing.T) {
	admin, router := "0x00000000000000000000000000000000000000a1", "0x3cb658cc8f58dcc2adffed1e8f074a4a659b0222"
	row := func(v map[string]any) json.RawMessage { return ca.Row(v) }
	result := &ca.Result{Sections: map[string][]json.RawMessage{
		"methods": {
			row(map[string]any{"method_id": "dep", "selector": method.SelectorOf("deposit(uint256,address)"), "signature": "", "signature_candidates": []any{map[string]any{"signature": "deposit(uint256,address)"}}, "behaviors": []string{"修改合约状态"}}),
			row(map[string]any{"method_id": "wd", "selector": method.SelectorOf("withdraw(uint256)"), "signature": "withdraw(uint256)"}),
			row(map[string]any{"method_id": "em", "selector": method.SelectorOf("emergencyWithdraw(address,uint256,address)"), "signature": "", "signature_candidates": []any{map[string]any{"signature": "emergencyWithdraw(address,uint256,address)"}}}),
			row(map[string]any{"method_id": "reg", "selector": method.SelectorOf("registry()"), "signature": "registry()"}),
			row(map[string]any{"method_id": "anon", "selector": "0x01453f9e", "signature": ""}),
			row(map[string]any{"method_id": "internal", "selector": "", "name": "", "visibility": "internal"}),
		},
		"funds": {
			row(map[string]any{"method_id": "dep", "kind": "token_transfer_from_intent", "to_expression": "address(this)", "from_expression": "msg.sender"}),
			row(map[string]any{"method_id": "wd", "kind": "token_transfer_intent", "to_expression": "msg.sender", "from_expression": "address(this)"}),
		},
		"calls": {row(map[string]any{"method_id": "dep", "call_type": "CALL", "target_expression": "0xa4d87f0b646f29aeaed09a2573ad1ff74bbba62"})},
		"caller_access_probes": {
			row(map[string]any{"method_ids": []string{"em"}, "classification": "caller_restricted", "required_role_name": "DEFAULT_ADMIN_ROLE", "role_members": []string{admin}}),
			row(map[string]any{"method_ids": []string{"reg"}, "classification": "callable_by_probe_caller", "returned_address": router}),
		},
		"role_holders": {row(map[string]any{"role": "0x" + "00", "role_name": "DEFAULT_ADMIN_ROLE", "members": []any{map[string]any{"address": admin, "has_role_at_snapshot": true}}})},
		"semantics":    {row(map[string]any{"method_id": "dep", "behavior": "比例与取整计算", "text": "amount * 10 / 100"})},
		"coverage":     {row(map[string]any{"dimension": "source", "complete": false, "reason": "verified_source_unavailable"})},
	}}
	result.Snapshot.EntryAddress, result.Snapshot.StorageAddress, result.Snapshot.CodeAddress = "0xe1", "0xe1", "0xc1"
	contractBuildSchemeProfile(result)
	p := rowMap(result.Sections["scheme_profile"][0])
	if p["analysis_basis"] != "bytecode" || p["function_count"] != json.Number("5") {
		t.Fatalf("profile header = %v", p)
	}
	if got := stringList(p["inflow_functions"]); len(got) != 1 || got[0] != "deposit(uint256,address)" {
		t.Fatalf("inflow = %v", got)
	}
	if got := stringList(p["outflow_functions"]); len(got) != 1 || got[0] != "withdraw(uint256)" {
		t.Fatalf("outflow = %v", got)
	}
	gates := p["caller_gated_functions"].([]any)
	if len(gates) != 1 || gates[0].(map[string]any)["required_role_name"] != "DEFAULT_ADMIN_ROLE" {
		t.Fatalf("gates = %v", gates)
	}
	related := map[string][]string{}
	for _, r := range p["related_addresses"].([]any) {
		m := r.(map[string]any)
		related[m["address"].(string)] = stringList(m["relations"])
	}
	if len(related["0x0a4d87f0b646f29aeaed09a2573ad1ff74bbba62"]) == 0 {
		t.Fatalf("leading-zero call target not normalised: %v", related)
	}
	if !contains(related[admin], "holds DEFAULT_ADMIN_ROLE") || !contains(related[admin], "can call emergencyWithdraw") || !contains(related[router], "returned by registry") {
		t.Fatalf("related = %v", related)
	}
	hints := p["name_hints"].(map[string]any)
	if !contains(stringList(hints["deposit"]), "deposit(uint256,address)") || !contains(stringList(hints["privileged_funds"]), "emergencyWithdraw(address,uint256,address)") || !contains(stringList(hints["withdrawal"]), "withdraw(uint256)") {
		t.Fatalf("hints = %v", hints)
	}
	for _, fn := range p["functions"].([]any) {
		f := fn.(map[string]any)
		if f["method_id"] == "anon" && f["name_basis"] != "selector_only" {
			t.Fatalf("unnamed function = %v", f)
		}
		if f["method_id"] == "reg" && f["value"] != router {
			t.Fatalf("getter value = %v", f)
		}
	}
	if len(p["fee_and_ratio_formulas"].([]any)) != 1 {
		t.Fatalf("formulas = %v", p["fee_and_ratio_formulas"])
	}
}
