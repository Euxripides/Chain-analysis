package download

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

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
