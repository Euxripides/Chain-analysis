package api

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
)

func TestNextReadScopeCannotQueueAnalysesOrMutateRules(t *testing.T) {
	for _, op := range nextOperations {
		prefix := "/api/v1/analysis/"
		want := "analysis"
		switch op {
		case "get_investigation_capabilities", "get_analysis_status", "list_watch_rules", "get_watch_events":
			want = "read"
		case "create_evidence_bundle", "get_evidence_bundle", "verify_evidence_bundle":
			prefix = "/api/v1/export/"
			want = "export"
		}
		if got := actionScopeForPath(prefix + op); got != want {
			t.Fatalf("%s requires %s, got %s", op, want, got)
		}
	}
}

func TestNextCursorBindsScopeSnapshotSectionAndPosition(t *testing.T) {
	job := nextJob{ID: "workspace:A", ResultSHA: "shaA", CursorKey: bytes.Repeat([]byte{2}, 32)}
	token := signNextCursor(nextCursor{job.ID, "edges", job.ResultSHA, 123}, job.CursorKey)
	n, e := parseNextCursor(token, job, "edges")
	if e != nil || n != 123 {
		t.Fatal(n, e)
	}
	for _, other := range []nextJob{{ID: "workspace:B", ResultSHA: job.ResultSHA, CursorKey: job.CursorKey}, {ID: job.ID, ResultSHA: "shaB", CursorKey: job.CursorKey}} {
		if _, e = parseNextCursor(token, other, "edges"); e == nil {
			t.Fatal("cross-snapshot/scope cursor accepted")
		}
	}
	if _, e = parseNextCursor(token, job, "addresses"); e == nil {
		t.Fatal("cross-section cursor accepted")
	}
	if _, e = parseNextCursor(token+"x", job, "edges"); e == nil {
		t.Fatal("tampered cursor accepted")
	}
}
func TestNextEvidenceDeterministicAndTamperDetection(t *testing.T) {
	job := nextJob{ID: "analysis", Version: "v2", Operation: "trace_source_set"}
	files := func() map[string][]byte {
		return map[string][]byte{"request.json": []byte(`{"sources":[]}`), "result.json": []byte(`{"amount_raw":"123456789012345678901234567890"}`)}
	}
	a, _, e := buildEvidenceZip(job, files())
	if e != nil {
		t.Fatal(e)
	}
	b, _, e := buildEvidenceZip(job, files())
	if e != nil || !bytes.Equal(a, b) {
		t.Fatal("bundle is not deterministic")
	}
	if _, e = verifyEvidenceZip(a); e != nil {
		t.Fatal(e)
	}
	a[len(a)/3] ^= 1
	if _, e = verifyEvidenceZip(a); e == nil {
		t.Fatal("tampered ZIP accepted")
	}
}
func TestNextStrictRequestAndUnsignedAmounts(t *testing.T) {
	for _, raw := range []string{`{"unknown":1}`, `{} {}`, `{"max_records":1.1}`} {
		if _, e := decodeNext([]byte(raw)); e == nil {
			t.Fatal("invalid body accepted", raw)
		}
	}
	for _, s := range []string{"1e18", "-1", "+2", "1.2", new(big.Int).Lsh(big.NewInt(1), 256).String()} {
		if _, e := nextRaw(s); e == nil {
			t.Fatal("invalid raw accepted", s)
		}
	}
}
func TestNextTraceExcludesDelegateCallsAndRevertedAncestors(t *testing.T) {
	a := chain.Address("0x1111111111111111111111111111111111111111")
	b := chain.Address("0x2222222222222222222222222222222222222222")
	root := &chaintrace.CallFrame{Type: "CALL", From: a, To: &b, Value: "10", Children: []chaintrace.CallFrame{{Type: "DELEGATECALL", From: a, To: &b, Value: "10"}, {Type: "CALL", From: a, To: &b, Value: "4", Error: "revert", Children: []chaintrace.CallFrame{{Type: "CALL", From: a, To: &b, Value: "4"}}}, {Type: "CALL", From: a, To: &b, Value: "2"}}}
	legs := nativeTraceLegs(root)
	if len(legs) != 2 || legs[0].Raw != "10" || legs[1].Raw != "2" {
		t.Fatal(legs)
	}
}
func TestNextFlattenPreservesRawValuesAndAllRows(t *testing.T) {
	rows := []any{}
	for i := 0; i < 1001; i++ {
		rows = append(rows, map[string]any{"amount_raw": "123456789012345678901234567890"})
	}
	meta, sections, e := flattenNextResult(map[string]any{"evidence": rows, "total_raw": "123456789012345678901234567890"})
	if e != nil || len(sections["evidence"]) != 1001 || meta["total_raw"] != "123456789012345678901234567890" {
		t.Fatal(e, meta)
	}
	raw, _ := json.Marshal(sections["evidence"][0])
	if !bytes.Contains(raw, []byte(`"123456789012345678901234567890"`)) {
		t.Fatal(string(raw))
	}
}
