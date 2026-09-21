package solana

import (
	"encoding/json"
	"testing"
)

func TestNormalizeBalance(t *testing.T) {
	raw := json.RawMessage(`{"holderAddress":"Holder1111111111111111111111111111111111","contractAddress":"Mint1111111111111111111111111111111111111","tokenType":"SPL","tokenDecimals":6,"balanceRawInteger":"42","snapshotBlock":448700000,"timestamp":1789875226}`)
	row, err := NormalizeBalance(raw, "drpc")
	if err != nil {
		t.Fatalf("normalize balance: %v", err)
	}
	// provider 必须透传真实来源（dRPC），不能再硬编码 "solana"。
	if row.Chain != "solana" || row.Provider != "drpc" {
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
		if _, err := NormalizeBalance(json.RawMessage(raw), "drpc"); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}

// 空来源回退 "solana"：未配置 dRPC 时仍走旧 Provider，source_provider 语义不变（规范 §0）。
func TestNormalizeBalanceProviderFallsBackToSolana(t *testing.T) {
	raw := json.RawMessage(`{"holderAddress":"Holder","tokenType":"native","tokenDecimals":9,"balanceRawInteger":"1","snapshotBlock":1,"timestamp":1}`)
	row, err := NormalizeBalance(raw, "")
	if err != nil {
		t.Fatalf("normalize balance: %v", err)
	}
	if row.Provider != "solana" {
		t.Fatalf("empty provider must fall back to solana, got %q", row.Provider)
	}
}
