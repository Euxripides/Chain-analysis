package download

import (
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestTronTimeIntervalsAreContiguousNonOverlappingAndModeBounded(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(160 * 24 * time.Hour)
	job := &model.DownloadJob{Chain: "tron", RangeType: "time", FromTimestamp: &from, ToTimestamp: &to, EffectiveMode: "fast"}
	intervals := tronTimeIntervals(job, to)
	if len(intervals) != 16 {
		t.Fatalf("intervals=%d want 16 for fast mode", len(intervals))
	}
	if !intervals[0].From.Equal(from) || !intervals[len(intervals)-1].To.Equal(to) {
		t.Fatalf("bounds=%s..%s want %s..%s", intervals[0].From, intervals[len(intervals)-1].To, from, to)
	}
	for i := 1; i < len(intervals); i++ {
		if got := intervals[i-1].To.Add(time.Millisecond); !got.Equal(intervals[i].From) {
			t.Fatalf("gap/overlap at %d: previous=%s next=%s", i, intervals[i-1].To, intervals[i].From)
		}
	}
}

func TestTronTurboShortRangeKeepsFingerprintParallelism(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(8 * 24 * time.Hour)
	job := &model.DownloadJob{Chain: "tron", RangeType: "time", FromTimestamp: &from, ToTimestamp: &to, EffectiveMode: "turbo"}
	intervals := tronTimeIntervals(job, to)
	if len(intervals) != 32 {
		t.Fatalf("intervals=%d want 32 for eight-day Turbo range", len(intervals))
	}
	for i := 1; i < len(intervals); i++ {
		if got := intervals[i-1].To.Add(time.Millisecond); !got.Equal(intervals[i].From) {
			t.Fatalf("gap/overlap at %d: previous=%s next=%s", i, intervals[i-1].To, intervals[i].From)
		}
	}
}

func TestTronColdTimeIntervalsUseMonthlyScaleAndRemainContiguous(t *testing.T) {
	from := time.Date(2018, 6, 25, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	job := &model.DownloadJob{Chain: "tron", RangeType: "time", FromTimestamp: &from, ToTimestamp: &to, EffectiveMode: "fast"}
	intervals := tronColdTimeIntervals(job, to)
	if len(intervals) <= 12 || len(intervals) > 128 {
		t.Fatalf("cold intervals=%d want >12 and <=128", len(intervals))
	}
	if !intervals[0].From.Equal(from) || !intervals[len(intervals)-1].To.Equal(to) {
		t.Fatalf("cold bounds=%s..%s want %s..%s", intervals[0].From, intervals[len(intervals)-1].To, from, to)
	}
	for i := 1; i < len(intervals); i++ {
		if got := intervals[i-1].To.Add(time.Millisecond); !got.Equal(intervals[i].From) {
			t.Fatalf("cold gap/overlap at %d: previous=%s next=%s", i, intervals[i-1].To, intervals[i].From)
		}
	}
	for _, interval := range intervals {
		if span := interval.To.Sub(interval.From) + time.Millisecond; span > 90*24*time.Hour {
			t.Fatalf("cold shard span=%s exceeds quarterly tail bound", span)
		}
	}
}

func TestTronColdActivityIntervalsUseTurboParallelismAndRemainContiguous(t *testing.T) {
	first := time.Date(2026, 7, 16, 8, 55, 39, 0, time.UTC)
	latest := time.Date(2026, 9, 7, 9, 30, 0, 0, time.UTC)
	to := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	job := &model.DownloadJob{Chain: "tron", RangeType: "time", FromTimestamp: nil, ToTimestamp: &to, EffectiveMode: "turbo"}
	intervals := tronColdActivityIntervals(job, to, first, latest)
	if len(intervals) != 128 {
		t.Fatalf("activity intervals=%d want 128", len(intervals))
	}
	if !intervals[0].From.Equal(first) || !intervals[len(intervals)-1].To.Equal(to) {
		t.Fatalf("bounds=%s..%s want %s..%s", intervals[0].From, intervals[len(intervals)-1].To, first, to)
	}
	for i := 1; i < len(intervals); i++ {
		if got := intervals[i-1].To.Add(time.Millisecond); !got.Equal(intervals[i].From) {
			t.Fatalf("gap/overlap at %d: previous=%s next=%s", i, intervals[i-1].To, intervals[i].From)
		}
	}
	if !intervals[len(intervals)-1].From.Equal(latest.Add(time.Millisecond)) {
		t.Fatalf("catch-up starts=%s want %s", intervals[len(intervals)-1].From, latest.Add(time.Millisecond))
	}
}

func TestRequestedBlockIntervalKeepsSingleBlock(t *testing.T) {
	from, to := int64(115394565), int64(115394565)
	got, bounded := requestedBlockInterval(&model.DownloadJob{RangeType: "block", FromBlock: &from, ToBlock: &to})
	if !bounded || got.From != from || got.To != to {
		t.Fatalf("single-block range was lost: bounded=%v interval=%+v", bounded, got)
	}
}

func TestRequestedBlockIntervalKeepsGenesisBlock(t *testing.T) {
	zero := int64(0)
	got, bounded := requestedBlockInterval(&model.DownloadJob{RangeType: "block", FromBlock: &zero, ToBlock: &zero})
	if !bounded || got.From != 0 || got.To != 0 {
		t.Fatalf("genesis range was confused with full range: bounded=%v interval=%+v", bounded, got)
	}
}

func TestRequestedBlockIntervalRejectsNonBlockSentinel(t *testing.T) {
	zero := int64(0)
	_, bounded := requestedBlockInterval(&model.DownloadJob{RangeType: "time", FromBlock: &zero, ToBlock: &zero})
	if bounded {
		t.Fatal("time range must not be planned as an exact block range")
	}
}
