package store

import (
	"testing"
	"time"
)

func TestExplorerHistoryPolicy(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		ranges           []BlockInterval
		age              time.Duration
		head             int64
		usable, complete bool
		reason           string
	}{
		{"fresh", []BlockInterval{{0, 100}}, 6 * 24 * time.Hour, 100, true, true, "fresh_enough"},
		{"one_nanosecond_before_week", []BlockInterval{{0, 100}}, 7*24*time.Hour - time.Nanosecond, 100, true, true, "fresh_enough"},
		{"exact_week", []BlockInterval{{0, 100}}, 7 * 24 * time.Hour, 100, false, true, "stale_tail"},
		{"middle_gap_even_recent", []BlockInterval{{0, 40}, {42, 100}}, time.Hour, 100, false, false, "historical_gap"},
		{"no_genesis_anchor", []BlockInterval{{10, 100}}, time.Hour, 100, false, false, "history_unknown"},
		{"latest_transaction_does_not_prove_scope", []BlockInterval{{0, 90}}, time.Hour, 100, false, false, "history_unknown"},
		{"adjacent_merge", []BlockInterval{{51, 100}, {0, 50}}, time.Hour, 100, true, true, "fresh_enough"},
		{"future_cutoff", []BlockInterval{{0, 100}}, -time.Second, 100, false, false, "history_unknown"},
		{"empty_proven_query", []BlockInterval{{0, 100}}, time.Hour, 100, true, true, "fresh_enough"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := assessHistory("transactions", tc.ranges, []historyCutoff{{Head: tc.head, At: now.Add(-tc.age)}}, now)
			if got.Usable != tc.usable || got.HistoryComplete != tc.complete || got.Reason != tc.reason {
				t.Fatalf("got %+v", got)
			}
		})
	}
	got := assessHistory("transactions", []BlockInterval{{0, 100}}, nil, now)
	if got.Usable {
		t.Fatal("missing request cutoff treated as complete")
	}
}
