package api

import (
	"encoding/json"
	"testing"
)

func TestInvestigationReadScopesDoNotGrantAnalysisOrExport(t *testing.T) {
	for _, op := range investigationOperations {
		path := "/api/v1/analysis/" + op
		want := "read"
		switch op {
		case "aggregate_transfers", "trace_funds", "trace_fund_batch", "detect_exchange_deposit", "trace_to_exchange":
			want = "analysis"
		case "get_export_file", "download_export":
			path = "/api/v1/export/" + op
			want = "export"
		}
		if got := actionScopeForPath(path); got != want {
			t.Fatalf("%s scope=%s want=%s", op, got, want)
		}
	}
	if actionScopeForPath("/api/v1/analysis/path") != "analysis" || actionScopeForPath("/api/v1/analysis/get_native_balance/extra") != "analysis" {
		t.Fatal("existing/unknown analysis scope bypass")
	}
}
func TestSnapshotRequestRejectsConflictingStateSelectors(t *testing.T) {
	for _, q := range []investigationRequest{{Block: json.RawMessage(`"latest"`), Timestamp: "2026-09-30T00:00:00Z"}, {Block: json.RawMessage(`123`), BlockTag: "latest"}, {Block: json.RawMessage(`1.2`)}, {Timestamp: "wrong"}} {
		if _, _, e := requestRef(q); e == nil {
			t.Fatal("invalid snapshot request accepted", q)
		}
	}
	r, at, e := requestRef(investigationRequest{Block: json.RawMessage(`"0x123"`)})
	if e != nil || at != nil || r.Number == nil || *r.Number != 291 {
		t.Fatal(r, at, e)
	}
}
