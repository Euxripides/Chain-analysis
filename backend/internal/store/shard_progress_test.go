package store

import "testing"

func TestIsSupersededTailSplit(t *testing.T) {
	if !IsSupersededTailSplit("CANCELLED", TailSplitSupersededError) {
		t.Fatal("tail-split parent must be excluded from progress")
	}
	for _, tc := range []struct{ status, reason string }{
		{"CANCELLED", "cancelled by user"},
		{"COMPLETED", TailSplitSupersededError},
		{"PENDING", ""},
	} {
		if IsSupersededTailSplit(tc.status, tc.reason) {
			t.Fatalf("ordinary shard was treated as superseded: %#v", tc)
		}
	}
}

func TestEffectiveShardSQL(t *testing.T) {
	if got, want := EffectiveShardSQL(""), "NOT (status='CANCELLED' AND error IN ('split for tail acceleration','forced address completion'))"; got != want {
		t.Fatalf("unqualified predicate=%q want %q", got, want)
	}
	if got, want := EffectiveShardSQL("sh."), "NOT (sh.status='CANCELLED' AND sh.error IN ('split for tail acceleration','forced address completion'))"; got != want {
		t.Fatalf("qualified predicate=%q want %q", got, want)
	}
}
