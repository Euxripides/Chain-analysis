package download

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestRangeCoverageMissingUsesShardRanges(t *testing.T) {
	contiguous := []model.Shard{
		{FromBlock: 0, ToBlock: 9},
		{FromBlock: 10, ToBlock: 19},
		{FromBlock: 20, ToBlock: 30},
	}
	if got := rangeCoverageMissing(contiguous, 30); got != 0 {
		t.Fatalf("contiguous shard ranges reported missing=%d", got)
	}

	withGap := []model.Shard{
		{FromBlock: 0, ToBlock: 9},
		{FromBlock: 13, ToBlock: 20},
	}
	if got := rangeCoverageMissing(withGap, 20); got != 3 {
		t.Fatalf("range gap missing=%d want 3", got)
	}

	withTail := []model.Shard{{FromBlock: 0, ToBlock: 9}}
	if got := rangeCoverageMissing(withTail, 12); got != 3 {
		t.Fatalf("tail gap missing=%d want 3", got)
	}
}
