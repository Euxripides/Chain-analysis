package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func TestTokenHoldersShareAndCodeAndProfile(t *testing.T) {
	vault, pair, wallet := "0x2796a26e723d7a260d628705960c6580837e44ae", "0x1d26b5168f33d9bdf8d43887612c1b68bc27b121", "0x627194d91fba22f889c554eade784b2ad08170b2"
	result := &ca.Result{Sections: map[string][]json.RawMessage{"caller_access_probes": {
		ca.Row(map[string]any{"signature_candidate": "totalSupply()", "returned_uint": "120000000000000000000000000"}),
		ca.Row(map[string]any{"signature": "decimals()", "returned_uint": "18"}),
	}}}
	contractApplyTokenHolders(result, func(limit int) ([]contractHolder, int64, error) {
		if limit != contractTopHolders {
			t.Fatalf("limit = %d", limit)
		}
		return []contractHolder{{vault, "107120161715413684419022930"}, {pair, "7983509688584586440441002"}, {wallet, "998573256884976005256036"}}, 11189, nil
	}, func(address string) (int, error) {
		if address == wallet {
			return 0, nil
		}
		return 4096, nil
	})
	h := rowMap(result.Sections["token_holders"][0])
	top := h["top_holders"].([]any)
	first := top[0].(map[string]any)
	if fmt.Sprint(h["holders_count"]) != "11189" || h["decimals"] != "18" || first["share_percent"] != "89.2668" || first["is_contract"] != true || top[2].(map[string]any)["is_contract"] != false {
		t.Fatalf("holders = %v", h)
	}
	contractBuildSchemeProfile(result)
	p := rowMap(result.Sections["scheme_profile"][0])
	if holders := p["top_holders"].([]any); len(holders) != 3 || holders[0].(map[string]any)["address"] != vault {
		t.Fatalf("profile top holders = %v", p["top_holders"])
	}
	found := false
	for _, r := range p["related_addresses"].([]any) {
		m := r.(map[string]any)
		if m["address"] == vault && contains(stringList(m["relations"]), "top holder #1 (89.2668% of supply)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("related = %v", p["related_addresses"])
	}
}

func TestTokenHoldersSkipNonTokensAndReportIndexFailure(t *testing.T) {
	result := &ca.Result{Sections: map[string][]json.RawMessage{}}
	contractApplyTokenHolders(result, func(int) ([]contractHolder, int64, error) { t.Fatal("non-token fetched holders"); return nil, 0, nil }, nil)
	if len(result.Sections["coverage"]) != 0 {
		t.Fatal("non-token produced coverage")
	}
	result.Sections["caller_access_probes"] = []json.RawMessage{ca.Row(map[string]any{"signature": "totalSupply()", "returned_uint": "5"})}
	contractApplyTokenHolders(result, func(int) ([]contractHolder, int64, error) { return nil, 0, errors.New("down") }, nil)
	if c := rowMap(result.Sections["coverage"][0]); c["reason"] != "holder_index_unavailable" {
		t.Fatalf("coverage = %v", c)
	}
}

func TestProfileGateReportsRoleWithoutHolders(t *testing.T) {
	result := &ca.Result{Sections: map[string][]json.RawMessage{
		"methods":              {ca.Row(map[string]any{"method_id": "tax", "selector": "0x78e3079e", "signature": "setTaxRecipient(address)"}), ca.Row(map[string]any{"method_id": "up", "selector": "0x4f1ef286", "signature": "upgradeToAndCall(address,bytes)"})},
		"caller_access_probes": {ca.Row(map[string]any{"method_ids": []string{"tax"}, "classification": "caller_restricted", "required_role": "0xfee"}), ca.Row(map[string]any{"method_ids": []string{"up"}, "classification": "caller_restricted", "required_role": "0x00"})},
		"role_holders":         {ca.Row(map[string]any{"role": "0xfee", "members": []any{}})},
	}}
	contractBuildSchemeProfile(result)
	gates := rowMap(result.Sections["scheme_profile"][0])["caller_gated_functions"].([]any)
	status := map[string]any{}
	for _, g := range gates {
		status[g.(map[string]any)["function"].(string)] = g.(map[string]any)["holder_status"]
	}
	if status["setTaxRecipient(address)"] != "no_current_holder" || status["upgradeToAndCall(address,bytes)"] != "unknown_no_role_events" {
		t.Fatalf("gate status = %v", status)
	}
}
