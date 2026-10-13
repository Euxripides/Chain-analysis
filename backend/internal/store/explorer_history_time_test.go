package store

import (
	"testing"
	"time"
)

func TestTimeHistoryRequiresContiguousIngestedRequestRanges(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	start := now.Add(-30 * 24 * time.Hour)
	mid := now.Add(-14 * 24 * time.Hour)
	cutoff := now.Add(-time.Hour)
	good := []historyTimeRange{{start, mid, mid}, {mid.Add(time.Millisecond), cutoff, cutoff}}
	if !assessTimeHistory("transactions", start, good, now).Usable {
		t.Fatal("contiguous recent history rejected")
	}
	gap := []historyTimeRange{{start, mid, mid}, {mid.Add(time.Hour), cutoff, cutoff}}
	if got := assessTimeHistory("transactions", start, gap, now); got.Usable || got.Reason != "historical_gap" {
		t.Fatalf("gap accepted %+v", got)
	}
	if got := assessTimeHistory("transactions", start, good[1:], now); got.HistoryComplete {
		t.Fatal("tail alone accepted as full history")
	}
}
