package solana

import (
	"encoding/json"
	"testing"
)

func TestNormalizeInternalCPI(t *testing.T) {
	raw := json.RawMessage(`{"txHash":"SIG","callIndex":256,"blockNumber":"448611120","timestamp":1789875226,
		"from":"ParentProgram","to":"ChildProgram","valueRaw":"0","assetType":"PROGRAM","callType":"cpi",
		"depth":2,"tracePath":"0.0","methodId":"","failed":true,"error":"custom program error: 0x1"}`)
	row, err := NormalizeInternal(raw, "drpc")
	if err != nil {
		t.Fatalf("normalize CPI: %v", err)
	}
	if row.Chain != "solana" || row.Provider != "drpc" || row.TxHash != "SIG" {
		t.Fatalf("unexpected identity: %+v", row)
	}
	if row.From != "ParentProgram" || row.To != "ChildProgram" || row.CallType != "cpi" || row.AssetType != "PROGRAM" {
		t.Fatalf("unexpected call fields: %+v", row)
	}
	if row.Depth != 2 || row.TracePath != "0.0" || row.CallIndex != 256 || row.BlockNumber != 448611120 {
		t.Fatalf("unexpected tree fields: %+v", row)
	}
	if !row.Failed || row.ErrorText == "" {
		t.Fatalf("failure must be preserved: %+v", row)
	}
}

func TestNormalizeInternalRejectsMalformedRows(t *testing.T) {
	cases := []string{
		`{"callIndex":1,"blockNumber":"1","valueRaw":"0"}`,
		`{"txHash":"SIG","callIndex":1,"blockNumber":"1","valueRaw":"not-a-number"}`,
		`{"txHash":"SIG","callIndex":1,"blockNumber":"x","valueRaw":"0"}`,
	}
	for _, raw := range cases {
		if _, err := NormalizeInternal(json.RawMessage(raw), "drpc"); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}

// 空来源回退 "solana"：CPI 行不因 provider 缺省而丢失来源（规范 §0）。
func TestNormalizeInternalProviderFallsBackToSolana(t *testing.T) {
	raw := json.RawMessage(`{"txHash":"SIG","callIndex":0,"blockNumber":"1","timestamp":1,"from":"A","to":"B","valueRaw":"0"}`)
	row, err := NormalizeInternal(raw, "")
	if err != nil {
		t.Fatalf("normalize CPI: %v", err)
	}
	if row.Provider != "solana" {
		t.Fatalf("empty provider must fall back to solana, got %q", row.Provider)
	}
}
