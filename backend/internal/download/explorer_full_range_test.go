package download

import (
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"testing"
	"time"
)

func TestFullEVMHistoryUsesFixedHeadForGapPlanning(t *testing.T) {
	for _, tc := range []struct {
		chain   string
		head    int64
		bounded bool
	}{{"bsc", 100, true}, {"eth", 100, true}, {"bsc", 0, false}, {"tron", 100, false}, {"solana", 100, false}} {
		iv, bounded := requestedBlockInterval(&model.DownloadJob{Chain: tc.chain, RangeType: "full", HeadBlock: tc.head})
		if bounded != tc.bounded {
			t.Fatalf("%s head %d bounded=%v", tc.chain, tc.head, bounded)
		}
		if bounded && (iv.From != 0 || iv.To != tc.head) {
			t.Fatalf("wrong range %+v", iv)
		}
	}
}
func TestTronFullHistoryUsesResolvedCutoff(t *testing.T) {
	cutoff := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	job := &model.DownloadJob{Chain: "tron", RangeType: "full", ResolvedAt: &cutoff}
	intervals := tronTimeIntervals(job, cutoff.Add(time.Hour))
	if len(intervals) == 0 || !intervals[len(intervals)-1].To.Equal(cutoff) {
		t.Fatal("full range moved after confirmation")
	}
}
