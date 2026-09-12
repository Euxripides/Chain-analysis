package dataset

import (
	"context"
	"testing"
)

// TestResolveRangeLocalNeverProbesHead 保证 v2 本地预览不会调用 HeadFn。
// Coverage HIT 必须实证 0 Provider 请求（§28.7 验收）。
func TestResolveRangeLocalNeverProbesHead(t *testing.T) {
	calls := 0
	svc := New(nil, func(ctx context.Context, chain string) (int64, error) {
		calls++
		return 123456, nil
	})
	rng, err := svc.ResolveRangeLocal("bsc", RangeSpec{Type: "full"})
	if err != nil {
		t.Fatalf("ResolveRangeLocal full: %v", err)
	}
	if calls != 0 {
		t.Fatalf("ResolveRangeLocal called head %d times, want 0", calls)
	}
	if rng.RangeType != "full" {
		t.Fatalf("range type = %q, want full", rng.RangeType)
	}
	if rng.FromBlock != nil || rng.ToBlock != nil {
		t.Fatalf("local full range must not invent block bounds: from=%v to=%v", rng.FromBlock, rng.ToBlock)
	}
	// 对照：传统 ResolveRange 仍会探测 head（保留旧调用方行为）。
	if _, err := svc.ResolveRange(context.Background(), "bsc", RangeSpec{Type: "full"}); err != nil {
		t.Fatalf("ResolveRange full: %v", err)
	}
	if calls != 1 {
		t.Fatalf("ResolveRange head calls = %d, want 1", calls)
	}
}

func TestResolveRangeLocalBoundedRanges(t *testing.T) {
	svc := New(nil, nil)
	from, to := int64(10), int64(20)
	rng, err := svc.ResolveRangeLocal("bsc", RangeSpec{Type: "block", FromBlock: &from, ToBlock: &to})
	if err != nil || rng.FromBlock == nil || rng.ToBlock == nil || *rng.FromBlock != 10 || *rng.ToBlock != 20 {
		t.Fatalf("block range resolved incorrectly: %+v err=%v", rng, err)
	}
	if _, err := svc.ResolveRangeLocal("bsc", RangeSpec{Type: "block", FromBlock: &to, ToBlock: &from}); err == nil {
		t.Fatal("reversed block range must fail")
	}
}
