package store

import (
	"testing"
	"time"
)

func TestSplitTimeIntervalIsContiguousWithoutOverlap(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour).Add(-time.Millisecond)
	first, second, ok := splitTimeInterval(from, to)
	if !ok {
		t.Fatal("valid interval was not split")
	}
	if !first[0].Equal(from) || !second[1].Equal(to) {
		t.Fatalf("outer bounds changed: first=%v second=%v", first, second)
	}
	if !first[1].Add(time.Millisecond).Equal(second[0]) {
		t.Fatalf("split has a gap or overlap: first=%v second=%v", first, second)
	}
}

func TestSplitTimeIntervalRejectsSubMillisecondWindow(t *testing.T) {
	from := time.Now().UTC()
	if _, _, ok := splitTimeInterval(from, from.Add(time.Microsecond)); ok {
		t.Fatal("sub-millisecond interval must not be split")
	}
}
