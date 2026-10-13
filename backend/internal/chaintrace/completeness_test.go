package chaintrace

import (
	"encoding/json"
	"testing"
)

func TestParityBranchGrowthPreservesEveryDescendant(t *testing.T) {
	// Provider order is deliberately scrambled, with several root siblings and
	// grandchildren. The old append/slice-pointer implementation loses nodes.
	raw := json.RawMessage(`[
 {"traceAddress":[2,0],"action":{"value":"0x23"}},
 {"traceAddress":[],"subtraces":3},
 {"traceAddress":[1],"subtraces":1},
 {"traceAddress":[0,0,0],"action":{"value":"0x11"}},
 {"traceAddress":[0],"subtraces":1},
 {"traceAddress":[2],"subtraces":1},
 {"traceAddress":[1,0],"action":{"value":"0x19"}},
 {"traceAddress":[0,0],"subtraces":1}]`)
	root, _, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	frames, depth := Summarize(root)
	if frames != 8 || depth != 4 || root.Children[0].Children[0].Children[0].Value != "17" || root.Children[1].Children[0].Value != "25" || root.Children[2].Children[0].Value != "35" {
		t.Fatalf("lost/reordered call tree: %+v", root)
	}
}

func TestParityIncompleteTreesAreRejected(t *testing.T) {
	for _, raw := range []string{
		`[{"traceAddress":[]},{"traceAddress":[0,0]}]`,
		`[{"traceAddress":[0]}]`,
		`[{"traceAddress":[]},{"traceAddress":[]}]`,
		`[{"traceAddress":[]},{"traceAddress":[0]},{"traceAddress":[0]}]`,
		`[{"traceAddress":[]},{"traceAddress":[-1]}]`,
		`[{"traceAddress":[]},{"traceAddress":[1]}]`,
		`[{"traceAddress":[],"subtraces":1}]`,
	} {
		if _, _, err := Normalize(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted incomplete trace %s", raw)
		}
	}
}

func TestOversizedTreesAreRejectedInsteadOfTruncated(t *testing.T) {
	items := make([]map[string]any, maxFrames+1)
	for i := range items {
		p := []int{}
		if i > 0 {
			p = []int{i - 1}
		}
		items[i] = map[string]any{"traceAddress": p}
	}
	raw, _ := json.Marshal(items)
	if _, _, err := Normalize(raw); err == nil {
		t.Fatal("truncated parity tree accepted")
	}
	root := gethFrame{Type: "CALL", Calls: make([]gethFrame, maxFrames)}
	raw, _ = json.Marshal(root)
	if _, _, err := Normalize(raw); err == nil {
		t.Fatal("truncated geth tree accepted")
	}
}

func TestLegacyNormalizedCacheIsNotTrusted(t *testing.T) {
	r := &TraceResult{Root: &CallFrame{}, FrameCount: 1, MaxDepth: 1}
	if currentNormalizedTrace(r) {
		t.Fatal("legacy trace accepted")
	}
	r.Root.NormalizationVersion = NormalizationVersion
	if !currentNormalizedTrace(r) {
		t.Fatal("repaired trace rejected")
	}
	r.FrameCount = 2
	if currentNormalizedTrace(r) {
		t.Fatal("inconsistent cache accepted")
	}
}

func TestNativeFundsExcludeRevertedDescendantsAndDelegatedValue(t *testing.T) {
	root, _, err := Normalize(json.RawMessage(`{"type":"CALL","value":"0x0","calls":[
	{"type":"CALL","value":"0x10","error":"reverted","calls":[{"type":"CALL","value":"0x20"}]},
	{"type":"DELEGATECALL","value":"0x30"},
	{"type":"CALLCODE","value":"0x40"},
	{"type":"STATICCALL","value":"0x50"},
	{"type":"CALL","value":"0x60"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := ValueTransfers(ExtractInternalActivity(&TraceResult{Root: root}))
	if len(rows) != 1 || rows[0].Value != "96" {
		t.Fatalf("non-executed native funding returned: %+v", rows)
	}
}
