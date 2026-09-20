package solana

import (
	"encoding/json"
	"testing"
)

func TestNormalizeBalance(t *testing.T) {
	raw := json.RawMessage(`{"holderAddress":"Holder1111111111111111111111111111111111","contractAddress":"Mint1111111111111111111111111111111111111","tokenType":"SPL","tokenDecimals":6,"balanceRawInteger":"42","snapshotBlock":448700000,"timestamp":1789875226}`)
	row, err := NormalizeBalance(raw)
	if err != nil {
		t.Fatalf("normalize balance: %v", err)
	}
	if row.Chain != "solana" || row.Provider != "solana" {
		t.Fatalf("unexpected provenance: %+v", row)
	}
	if row.Address != "Holder1111111111111111111111111111111111" || row.TokenAddress != "Mint1111111111111111111111111111111111111" {
		t.Fatalf("unexpected addresses: %+v", row)
	}
	if row.Balance == nil || row.Balance.String() != "42" || row.TokenDecimals != 6 || row.SnapshotBlock != 448700000 {
		t.Fatalf("unexpected balance fields: %+v", row)
	}
	// 没有来源就不填（ADR-024 D2）：不用 mint 冒充 symbol，不编价格。
	if row.TokenSymbol != "" || row.TokenName != "" || row.TokenPrice != "" || row.BalanceUSD != "" {
		t.Fatalf("metadata must stay empty: %+v", row)
	}
}

func TestNormalizeBalanceRejectsMalformedRows(t *testing.T) {
	cases := []string{
		`{"contractAddress":"M","tokenDecimals":6,"balanceRawInteger":"1"}`,
		`{"holderAddress":"H","tokenDecimals":6,"balanceRawInteger":"not-a-number"}`,
		`{"holderAddress":"H","tokenDecimals":"x","balanceRawInteger":"1"}`,
	}
	for _, raw := range cases {
		if _, err := NormalizeBalance(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}
