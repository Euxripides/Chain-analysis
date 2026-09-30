package download

import (
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestSolanaEmptyCoverageBoundsOnlyForExhaustedBlockScope(t *testing.T) {
	base := model.Shard{Dataset: "transactions", FromBlock: 0, ToBlock: 451606404,
		PageState: map[string]any{"query_mode": "full"}}
	if from, to, ok := solanaEmptyCoverageBounds(base, "solana"); !ok || from != 0 || to != base.ToBlock {
		t.Fatalf("full empty history must retain the pinned head: %d..%d ok=%t", from, to, ok)
	}
	base.PageState = map[string]any{"current_page_size": float64(100)}
	if _, _, ok := solanaEmptyCoverageBounds(base, "solana"); !ok {
		t.Fatal("adaptive checkpoint must not erase a finalized empty coverage assertion")
	}
	block := base
	block.FromBlock, block.ToBlock = 100, 200
	block.PageState = map[string]any{"query_mode": "block"}
	if from, to, ok := solanaEmptyCoverageBounds(block, "solana"); !ok || from != 100 || to != 200 {
		t.Fatalf("empty slot range must remain bounded: %d..%d ok=%t", from, to, ok)
	}
	timeRange := base
	timeRange.PageState = map[string]any{"query_mode": "time"}
	now := time.Now()
	timeRange.FromTimestamp = &now
	for name, tc := range map[string]struct {
		shard model.Shard
		chain string
	}{
		"time":             {timeRange, "solana"},
		"other chain":      {base, "tron"},
		"unbounded":        {model.Shard{Dataset: "transactions", PageState: map[string]any{"query_mode": "full"}}, "solana"},
		"balance snapshot": {model.Shard{Dataset: "balances", ToBlock: 12, PageState: map[string]any{"query_mode": "full"}}, "solana"},
	} {
		if _, _, ok := solanaEmptyCoverageBounds(tc.shard, tc.chain); ok {
			t.Errorf("%s must not assert an empty full block interval", name)
		}
	}
}
