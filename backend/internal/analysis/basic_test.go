package analysis

import (
	"context"
	"testing"
	"time"
)

type fakeRows struct{ rows []map[string]any }

func (f fakeRows) RowsForExport(context.Context, string, string, string, int, int) ([]map[string]any, error) {
	return f.rows, nil
}

// FlowTokenTransfers 是 P1-1 之后 Analyze 真正的读路径，测试桩必须同样实现它。
func (f fakeRows) FlowTokenTransfers(context.Context, string, string, int) ([]map[string]any, error) {
	return f.rows, nil
}
func TestAnalyzeAggregatesDirectionAndEvidence(t *testing.T) {
	now := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	svc := &Service{query: fakeRows{rows: []map[string]any{
		{"from_address": "alice", "to_address": "target", "token_symbol": "USDT", "amount": "10.5", "tx_hash": "in-1", "timestamp": now.Add(-time.Hour)},
		{"from_address": "target", "to_address": "bob", "token_symbol": "USDT", "amount": "4", "tx_hash": "out-1", "timestamp": now},
		{"from_address": "target", "to_address": "bob", "token_symbol": "USDT", "amount": "3", "tx_hash": "out-2", "timestamp": now.Add(time.Minute)},
	}}}
	got, err := svc.Analyze(context.Background(), "tron", "target")
	if err != nil {
		t.Fatal(err)
	}
	if got.TransactionCount != 3 || got.UniqueCounterparties != 2 || len(got.Edges) != 2 {
		t.Fatalf("unexpected profile: %+v", got)
	}
	if got.FirstFunder == nil || got.FirstFunder.Counterparty != "alice" || got.FirstFunder.Amount != "10.5" {
		t.Fatalf("unexpected first funder: %+v", got.FirstFunder)
	}
	if len(got.CashoutCandidates) != 1 || got.CashoutCandidates[0].Counterparty != "bob" || got.CashoutCandidates[0].Amount != "7" || len(got.CashoutCandidates[0].Evidence) != 2 || got.CashoutCandidates[0].FlowRatio != "1.00000000" {
		t.Fatalf("unexpected cashout candidates: %+v", got.CashoutCandidates)
	}
}
