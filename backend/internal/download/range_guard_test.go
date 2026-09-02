package download

import (
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// BUG-002 回归：历史区间必须被完整覆盖；区间无法满足或可能被 Provider 截断时
// 禁止以 0 行 COMPLETED 静默收敛。

func shardFor(dataset string, from, to int64) model.Shard {
	return model.Shard{Dataset: dataset, FromBlock: from, ToBlock: to}
}

func TestRangeCoverageVerdictAllowsFullyScannedResults(t *testing.T) {
	// 区间内有行 → 允许
	if err := rangeCoverageVerdict(shardFor("token_transfers", 114147137, 114147137), true, false, false, 1, true); err != nil {
		t.Fatalf("in-range rows must pass: %v", err)
	}
	// 已横跨区间（升序枚举先见下界行，后见上界行）→ 允许（区间内确无数据的空结果）
	if err := rangeCoverageVerdict(shardFor("token_transfers", 98000716, 98000717), true, true, true, 0, false); err != nil {
		t.Fatalf("spanned range must pass: %v", err)
	}
	// Provider 从未返回任何行（地址无历史）→ 允许 0 行
	if err := rangeCoverageVerdict(shardFor("transactions", 114147137, 114147137), false, false, false, 0, false); err != nil {
		t.Fatalf("empty provider must pass: %v", err)
	}
	// 未横跨但末页未饱和（枚举到索引自然尽头；区间整体在索引之外）→ 允许 0 行
	if err := rangeCoverageVerdict(shardFor("token_transfers", 98000716, 98000717), true, true, false, 0, false); err != nil {
		t.Fatalf("exhausted unsaturated tail must pass: %v", err)
	}
	if err := rangeCoverageVerdict(shardFor("token_transfers", 98000716, 98000717), true, false, false, 0, false); err != nil {
		t.Fatalf("exhausted unsaturated tail (below-only) must pass: %v", err)
	}
	// 全量（无界）请求 → 守卫不适用（Coverage 记录观测跨度）
	if err := rangeCoverageVerdict(shardFor("token_transfers", 0, 0), true, false, false, 0, true); err != nil {
		t.Fatalf("unbounded must pass: %v", err)
	}
	// 时间边界且横跨 → 允许
	now := time.Now()
	from := now.Add(-24 * time.Hour)
	sh := shardFor("token_transfers", 0, 0)
	sh.FromTimestamp = &from
	if err := rangeCoverageVerdict(sh, true, true, true, 0, true); err != nil {
		t.Fatalf("timestamp spanned must pass: %v", err)
	}
}

func TestRangeCoverageVerdictFailsOnPotentiallyTruncatedScan(t *testing.T) {
	// 见过行、0 区间内行、从未横跨、末页仍饱和 → 可能被截断，必须失败
	verdict := rangeCoverageVerdict(shardFor("token_transfers", 114147137, 114147137), true, false, false, 0, true)
	if verdict == nil {
		t.Fatal("potentially truncated scan must fail, not silently complete with 0 rows")
	}
	if !strings.Contains(verdict.Error(), "[range_not_reached]") {
		t.Fatalf("error must carry range_not_reached marker: %v", verdict)
	}
	if !strings.Contains(verdict.Error(), "114147137") {
		t.Fatalf("error must name the requested range: %v", verdict)
	}
	for _, ds := range []string{"transactions", "nft_transfers"} {
		if err := rangeCoverageVerdict(shardFor(ds, 100, 200), true, true, false, 0, true); err == nil {
			t.Fatalf("%s one-sided saturated scan must fail", ds)
		}
	}
}

func TestRangeCoverageVerdictGuardScope(t *testing.T) {
	// 只有带界的 transactions/token_transfers/nft_transfers 受守卫约束
	if err := rangeCoverageVerdict(shardFor("logs", 60001001, 60001200), true, false, false, 0, true); err != nil {
		t.Fatalf("logs honors provider bounds; guard must not apply: %v", err)
	}
	if err := rangeCoverageVerdict(shardFor("balances", 0, 115394565), true, false, false, 0, true); err != nil {
		t.Fatalf("balances is a snapshot; guard must not apply: %v", err)
	}
	// 单块时间范围也受守卫约束
	now := time.Now()
	sh := shardFor("transactions", 0, 0)
	sh.FromTimestamp = &now
	sh.ToTimestamp = &now
	if err := rangeCoverageVerdict(sh, true, false, false, 0, true); err == nil {
		t.Fatal("timestamp-bounded one-sided saturated scan must fail")
	}
}
