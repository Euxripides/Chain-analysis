package solana

import (
	"encoding/json"
	"testing"
)

func TestNormalizeProgramLog(t *testing.T) {
	raw := json.RawMessage(`{"blockNumber":"448611120","transactionHash":"SIG","logIndex":3,"address":"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA","topics":[],"data":"hello","removed":false,"timestamp":1789875226,"eventName":"","eventSource":"solana_program_log","eventSignature":"","eventVerified":false}`)
	row, err := NormalizeProgramLog(raw, "drpc")
	if err != nil {
		t.Fatalf("normalize program log: %v", err)
	}
	if row.Chain != "solana" || row.Provider != "drpc" || row.TxHash != "SIG" || row.LogIndex != 3 {
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
		if _, err := NormalizeProgramLog(json.RawMessage(raw), "drpc"); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}

// 空来源回退 "solana"（规范 §0）。
func TestNormalizeProgramLogProviderFallsBackToSolana(t *testing.T) {
	raw := json.RawMessage(`{"blockNumber":"1","transactionHash":"SIG","logIndex":0,"address":"P","data":"x","timestamp":1}`)
	row, err := NormalizeProgramLog(raw, "")
	if err != nil {
		t.Fatalf("normalize program log: %v", err)
	}
	if row.Provider != "solana" {
		t.Fatalf("empty provider must fall back to solana, got %q", row.Provider)
	}
}

// solana_program_logs 原生列映射（规范 §8.4）：slot/signature/program_id/message/invoke_depth。
func TestNormalizeSolanaProgramLogMapsNativeColumns(t *testing.T) {
	raw := json.RawMessage(`{"blockNumber":"448611120","transactionHash":"SIG","logIndex":3,"address":"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA","programId":"","data":"Program log: hello","invokeDepth":2,"success":false,"removed":false,"timestamp":1789875226,"eventSource":"solana_program_log","sourceApi":"solana_rpc","sourceRequestId":"req-1"}`)
	row, err := NormalizeSolanaProgramLog(raw, "drpc")
	if err != nil {
		t.Fatalf("normalize solana program log: %v", err)
	}
	if row.Chain != "solana" || row.Slot != 448611120 || row.Signature != "SIG" || row.LogIndex != 3 {
		t.Fatalf("unexpected identity: %+v", row)
	}
	// §3 行契约把 programId 放在 address 上（programId 为空），必须回落到 address。
	if row.ProgramID != "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA" {
		t.Fatalf("program id must fall back to address: %+v", row)
	}
	if row.Message != "Program log: hello" || row.InvokeDepth != 2 || row.Success != 0 {
		t.Fatalf("unexpected payload: %+v", row)
	}
	if row.SourceProvider != "drpc" || row.SourceAPI != "solana_rpc" || row.SourceRequestID != "req-1" {
		t.Fatalf("unexpected provenance: %+v", row)
	}
}

// success 缺失不判失败（否则整表日志都会被标成失败）；malformed 行仍要拒绝。
func TestNormalizeSolanaProgramLogDefaultsSuccess(t *testing.T) {
	raw := json.RawMessage(`{"blockNumber":"7","transactionHash":"SIG","logIndex":1,"address":"P","data":"x","timestamp":1}`)
	row, err := NormalizeSolanaProgramLog(raw, "")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if row.Success != 1 || row.SourceProvider != "solana" {
		t.Fatalf("unexpected defaults: %+v", row)
	}
	bad := []string{
		`{"blockNumber":"7","logIndex":1,"data":"x"}`,
		`{"transactionHash":"SIG","blockNumber":"x","logIndex":1,"data":"x"}`,
	}
	for _, raw := range bad {
		if _, err := NormalizeSolanaProgramLog(json.RawMessage(raw), "drpc"); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}
