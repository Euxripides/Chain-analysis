package dataset

import (
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// ComputeGaps 计算 want 中未被 covered 覆盖的区间。
func ComputeGaps(covered []store.BlockInterval, want store.BlockInterval) []store.BlockInterval {
	merged := store.MergeBlockRanges(covered)
	var gaps []store.BlockInterval
	cur := want.From
	for _, iv := range merged {
		if iv.To < cur || iv.From > want.To {
			continue
		}
		if iv.From > cur {
			gaps = append(gaps, store.BlockInterval{From: cur, To: min64(iv.From-1, want.To)})
		}
		if iv.To >= want.To {
			cur = want.To + 1
			break
		}
		cur = iv.To + 1
		if cur > want.To {
			break
		}
	}
	if cur <= want.To {
		gaps = append(gaps, store.BlockInterval{From: cur, To: want.To})
	}
	return gaps
}

func CoveredBlocks(covered []store.BlockInterval, want store.BlockInterval) int64 {
	merged := store.MergeBlockRanges(covered)
	var sum int64
	for _, iv := range merged {
		if iv.To < want.From || iv.From > want.To {
			continue
		}
		f := max64(iv.From, want.From)
		t := min64(iv.To, want.To)
		if t >= f {
			sum += t - f + 1
		}
	}
	return sum
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
