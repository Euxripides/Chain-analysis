package api

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/canvas"
)

func TestCursorRoundTripRejectsTampering(t *testing.T) {
	raw := encodeCursor(timeIDCursor{TS: "2026-09-12T00:00:00Z", ID: "11111111-1111-1111-1111-111111111111"})
	var out timeIDCursor
	if err := decodeCursor(raw, &out); err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if out.ID != "11111111-1111-1111-1111-111111111111" || out.TS == "" {
		t.Fatalf("cursor round trip mismatch: %+v", out)
	}
	if err := decodeCursor("not-base64!", &out); err == nil {
		t.Fatal("tampered cursor must fail")
	}
	// 未知字段必须拒绝，避免客户端伪造内部游标结构。
	if err := decodeCursor(encodeCursor(map[string]any{"o": 1, "extra": true}), &offsetCursor{}); err == nil {
		t.Fatal("cursor with unknown fields must fail")
	}
}

func TestCanvasEvidenceRefsExtraction(t *testing.T) {
	state := canvas.State{
		Annotations: []map[string]any{
			{"text": "see evidence:11111111-1111-1111-1111-111111111111"},
			{"ref": map[string]any{"type": "evidence", "id": "22222222-2222-2222-2222-222222222222"}},
			{"text": "evidence/33333333-3333-3333-3333-333333333333"},
			{"note": "no reference here"},
		},
	}
	refs := canvasEvidenceRefs(state)
	if len(refs) != 3 {
		t.Fatalf("refs = %v, want 3", refs)
	}
	seen := map[string]bool{}
	for _, r := range refs {
		seen[r] = true
	}
	for _, want := range []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
	} {
		if !seen[want] {
			t.Fatalf("missing ref %s in %v", want, refs)
		}
	}
}

func TestCanvasStateDiffReportsOnlyChangedFields(t *testing.T) {
	current := canvas.Canvas{Name: "A", NodeRefs: []string{"a"}, Layout: map[string]any{"x": 1}}
	name := "B"
	req := canvas.UpdateRequest{Name: &name, State: canvas.State{NodeRefs: []string{"a", "b"}, Layout: map[string]any{"x": 1}}}
	diff := canvasStateDiff(req, current)
	fields := map[string]bool{}
	for _, d := range diff {
		fields[d["field"].(string)] = true
	}
	if !fields["name"] || !fields["node_refs"] || fields["layout"] {
		t.Fatalf("unexpected diff: %#v", diff)
	}
}

func TestJobFailureKindIsUserSafe(t *testing.T) {
	if kind := jobFailureKind("dial tcp provider.ankr.com: i/o timeout"); kind != "timeout" {
		t.Fatalf("timeout kind = %q", kind)
	}
	if kind := jobFailureKind("provider returned 500"); kind != "provider" {
		t.Fatalf("provider kind = %q", kind)
	}
	if msg := safeJobError("dial tcp provider.ankr.com: i/o timeout"); msg == "" || msg == "dial tcp provider.ankr.com: i/o timeout" {
		t.Fatalf("safeJobError leaked raw provider error: %q", msg)
	}
}
