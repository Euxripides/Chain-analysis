package tron

import (
	"context"
	"testing"
)

func TestDailyQuotaThresholdsAndPriority(t *testing.T) {
	q := newDailyQuota(10)
	for i := 0; i < 9; i++ {
		if _, err := q.allow(context.Background(), "background"); err != nil {
			t.Fatalf("request %d unexpectedly blocked: %v", i, err)
		}
	}
	if level := func() string { _, _, l := q.snapshot(); return l }(); level != "pause_background" {
		t.Fatalf("level after 90%% usage = %q", level)
	}
	if _, err := q.allow(context.Background(), "background"); err == nil {
		t.Fatal("background request should be blocked at 90%%")
	}
	if _, err := q.allow(context.Background(), "manual"); err != nil {
		t.Fatalf("manual request should remain allowed below hard limit: %v", err)
	}
	if _, err := q.allow(context.Background(), "manual"); err == nil {
		t.Fatal("request should be blocked at the hard daily budget")
	}
}

func TestQuotaPriorityAtManualOnlyThreshold(t *testing.T) {
	q := newDailyQuota(20)
	for i := 0; i < 19; i++ {
		if _, err := q.allow(context.Background(), "manual"); err != nil {
			t.Fatalf("request %d unexpectedly blocked: %v", i, err)
		}
	}
	if _, err := q.allow(context.Background(), "background"); err == nil {
		t.Fatal("background request should be blocked at manual-only threshold")
	}
	if _, err := q.allow(context.Background(), "interactive"); err != nil {
		t.Fatalf("interactive request should be allowed at 95%%: %v", err)
	}
	if _, err := q.allow(context.Background(), "manual"); err == nil {
		t.Fatal("request should be blocked at exhausted budget")
	}
}
