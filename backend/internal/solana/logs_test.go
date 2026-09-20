package solana

import (
	"encoding/json"
	"testing"
)

func TestNormalizeProgramLog(t *testing.T) {
	raw := json.RawMessage(`{"blockNumber":"448611120","transactionHash":"SIG","logIndex":3,"address":"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA","topics":[],"data":"hello","removed":false,"timestamp":1789875226,"eventName":"","eventSource":"solana_program_log","eventSignature":"","eventVerified":false}`)
	row, err := NormalizeProgramLog(raw)
	if err != nil {
		t.Fatalf("normalize program log: %v", err)
	}
	if row.Chain != "solana" || row.Provider != "solana" || row.TxHash != "SIG" || row.LogIndex != 3 {
		t.Fatalf("unexpected identity: %+v", row)
	}
	if row.Address != "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA" || row.Data != "hello" {
		t.Fatalf("unexpected payload: %+v", row)
	}
	// 关键口径（ADR-024 D1）：没有 topics、没有 EVM 签名、未经验证。
	if len(row.Topics) != 0 {
		t.Fatalf("solana rows must not carry topics: %+v", row.Topics)
	}
	if row.EventSignature != "" || row.EventVerified != 0 {
		t.Fatalf("solana rows must not claim EVM verification: %+v", row)
	}
	if row.EventSource != "solana_program_log" {
		t.Fatalf("event source must be preserved: %+v", row)
	}
}

func TestNormalizeProgramLogRejectsMalformedRows(t *testing.T) {
	cases := []string{
		`{"blockNumber":"1","logIndex":1,"data":"x"}`,
		`{"transactionHash":"SIG","blockNumber":"x","logIndex":1,"data":"x"}`,
	}
	for _, raw := range cases {
		if _, err := NormalizeProgramLog(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}
