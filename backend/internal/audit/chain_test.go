package audit

import (
	"strings"
	"testing"
)

func TestCanonicalizeIsStable(t *testing.T) {
	a, err := Canonicalize(map[string]any{"b": 1, "a": map[string]any{"y": 2, "x": 3}, "c": []any{1, "two", true}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Canonicalize(map[string]any{"c": []any{1, "two", true}, "a": map[string]any{"x": 3, "y": 2}, "b": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("canonicalization is order dependent:\n%s\n%s", a, b)
	}
	if !strings.Contains(string(a), `"a":{`) {
		t.Fatalf("unexpected canonical form: %s", a)
	}
}

func TestHashEventDetectsChange(t *testing.T) {
	payload := []byte(`{"action":"case:read"}`)
	base := HashEvent(ChainAudit, 1, "", payload)
	if base != HashEvent(ChainAudit, 1, "", payload) {
		t.Fatal("hash must be deterministic")
	}
	if base == HashEvent(ChainAudit, 2, "", payload) {
		t.Fatal("seq must participate in the hash")
	}
	if base == HashEvent(ChainAudit, 1, "deadbeef", payload) {
		t.Fatal("prev hash must participate in the hash")
	}
	if base == HashEvent(ChainAudit, 1, "", []byte(`{"action":"case:delete"}`)) {
		t.Fatal("payload change must change the hash")
	}
	if base == HashEvent(ChainLogin, 1, "", payload) {
		t.Fatal("chain name must participate in the hash")
	}
}

func TestVerifyChainResultHelpers(t *testing.T) {
	r := VerifyChainResult{Valid: true}
	got := r.broken(7, "event_hash_mismatch", "boom")
	if got.Valid || got.Reason != "event_hash_mismatch" || got.BrokenAtSeq == nil || *got.BrokenAtSeq != 7 {
		t.Fatalf("unexpected broken result: %+v", got)
	}
	if len(got.Errors) != 1 {
		t.Fatalf("expected one error message, got %v", got.Errors)
	}
}
