package download

import (
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func TestTronIncrementalPlanningUsesAddressDatasetWatermark(t *testing.T) {
	now := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	through := now.Add(-2 * time.Hour)
	job := &model.DownloadJob{Chain: "tron", RangeType: "full"}
	planned, current := tronIncrementalPlanningJob(job, &store.AddressDatasetSyncState{CompleteThrough: through}, now)
	if current {
		t.Fatal("two-hour tail was incorrectly treated as current")
	}
	if got, want := *planned.FromTimestamp, through.Add(time.Millisecond); !got.Equal(want) {
		t.Fatalf("tail starts at %s, want %s", got, want)
	}
	if got, want := *planned.ToTimestamp, now.Add(time.Minute); !got.Equal(want) {
		t.Fatalf("tail ends at %s, want %s", got, want)
	}
	if planned.RangeType != "full" {
		t.Fatalf("range type changed to %q", planned.RangeType)
	}
}

func TestTronIncrementalPlanningKeepsNewAddressOnFullHistory(t *testing.T) {
	job := &model.DownloadJob{Chain: "tron", RangeType: "full"}
	planned, current := tronIncrementalPlanningJob(job, nil, time.Now())
	if current || planned != job || planned.FromTimestamp != nil || planned.ToTimestamp != nil {
		t.Fatal("new address must retain full-history planning")
	}
}

func TestLocalTRONQuantilesRequireCompleteSyncWatermarkForFullHistory(t *testing.T) {
	job := &model.DownloadJob{Chain: "tron", RangeType: "full", ReusePolicy: "prefer_local", Source: "chatgpt_mcp"}
	if localTRONQuantilesTrusted(job, nil) {
		t.Fatal("partial local rows without a sync watermark must not shape full-history shards")
	}
	if !localTRONQuantilesTrusted(job, &store.AddressDatasetSyncState{}) {
		t.Fatal("a durable sync watermark should allow local quantile planning")
	}
	job.RangeType = "time"
	if !localTRONQuantilesTrusted(job, nil) {
		t.Fatal("explicit time ranges may use their bounded local distribution")
	}
}

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

func TestTronProfileShardCountKeepsExactSparseHistorySmall(t *testing.T) {
	first := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	latest := first.Add(300 * 24 * time.Hour)
	job := &model.DownloadJob{EffectiveMode: "fast"}
	profile := provider.TRC20ActivityProfile{First: first, Latest: latest, ExactRows: 120, PeakRowsPerDay: 50}
	if got := tronProfileShardCount(job, profile); got != 1 {
		t.Fatalf("exact sparse shards=%d want 1", got)
	}
}

func TestTronProfileShardCountExpandsDenseColdHistory(t *testing.T) {
	first := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	latest := first.Add(365 * 24 * time.Hour)
	job := &model.DownloadJob{EffectiveMode: "turbo"}
	profile := provider.TRC20ActivityProfile{First: first, Latest: latest, PeakRowsPerDay: 30_000, HasMore: true}
	if got := tronProfileShardCount(job, profile); got != 127 {
		t.Fatalf("dense shards=%d want 127", got)
	}
}

func TestTronProfileShardCountDoesNotOvershardOrdinaryColdHistory(t *testing.T) {
	first := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	latest := first.Add(365 * 24 * time.Hour)
	job := &model.DownloadJob{EffectiveMode: "fast"}
	profile := provider.TRC20ActivityProfile{First: first, Latest: latest, PeakRowsPerDay: 40, HasMore: true}
	if got := tronProfileShardCount(job, profile); got != 16 {
		t.Fatalf("ordinary shards=%d want fast baseline 16", got)
	}
}

func TestTronParallelPlanningOnlyAcceptsIndependentTimeDatasets(t *testing.T) {
	subjects := []model.Subject{{ID: "a"}, {ID: "b"}}
	if !tronParallelPlanningEligible(&model.DownloadJob{Chain: "tron", Datasets: []string{"transactions", "token_transfers"}}, subjects) {
		t.Fatal("TRON address streams should be eligible for bounded parallel planning")
	}
	if tronParallelPlanningEligible(&model.DownloadJob{Chain: "tron", Datasets: []string{"balances"}}, subjects) {
		t.Fatal("mixed planner paths must remain sequential")
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
