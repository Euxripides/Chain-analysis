package download

import (
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// BUG-002 回归：历史区间无法满足时禁止以 0 行 COMPLETED 静默收敛。

func shardFor(dataset string, from, to int64) model.Shard {
	return model.Shard{Dataset: dataset, FromBlock: from, ToBlock: to}
}

func TestRangeCoverageVerdictAllowsFullyScannedResults(t *testing.T) {
	// 区间内有行 → 允许
	if err := rangeCoverageVerdict(shardFor("token_transfers", 114147137, 114147137), true, false, 1); err != nil {
		t.Fatalf("in-range rows must pass: %v", err)
	}
	// 已越过下边界（降序扫描穿过整个区间）→ 允许（区间内确无数据的空结果）
	if err := rangeCoverageVerdict(shardFor("token_transfers", 98000716, 98000717), true, true, 0); err != nil {
		t.Fatalf("belowSeen must pass: %v", err)
	}
	// Provider 从未返回任何行（地址无历史）→ 允许 0 行
	if err := rangeCoverageVerdict(shardFor("transactions", 114147137, 114147137), false, false, 0); err != nil {
		t.Fatalf("empty provider must pass: %v", err)
	}
	// 全量（无界）请求 → 守卫不适用（Coverage 记录观测跨度）
	if err := rangeCoverageVerdict(shardFor("token_transfers", 0, 0), true, false, 0); err != nil {
		t.Fatalf("unbounded must pass: %v", err)
	}
	// 时间边界且越过下时间界 → 允许
	now := time.Now()
	from := now.Add(-24 * time.Hour)
	sh := shardFor("token_transfers", 0, 0)
	sh.FromTimestamp = &from
	if err := rangeCoverageVerdict(sh, true, true, 0); err != nil {
		t.Fatalf("timestamp belowSeen must pass: %v", err)
	}
}

func TestRangeCoverageVerdictFailsOnUnreachedRange(t *testing.T) {
	// BUG-002 现场：provider 返回过大量行、区间内 0 行、从未越过下界 → 必须失败
	verdict := rangeCoverageVerdict(shardFor("token_transfers", 114147137, 114147137), true, false, 0)
	if verdict == nil {
		t.Fatal("unreached range must fail, not silently complete with 0 rows")
	}
	if !strings.Contains(verdict.Error(), "[range_not_reached]") {
		t.Fatalf("error must carry range_not_reached marker: %v", verdict)
	}
	if !strings.Contains(verdict.Error(), "114147137") {
		t.Fatalf("error must name the requested range: %v", verdict)
	}
	for _, ds := range []string{"transactions", "nft_transfers"} {
		if err := rangeCoverageVerdict(shardFor(ds, 100, 200), true, false, 0); err == nil {
			t.Fatalf("%s unreached range must fail", ds)
		}
	}
}

func TestRangeCoverageVerdictGuardScope(t *testing.T) {
	// 只有带界的 transactions/token_transfers/nft_transfers 受守卫约束
	if err := rangeCoverageVerdict(shardFor("logs", 60001001, 60001200), true, false, 0); err != nil {
		t.Fatalf("logs honors provider bounds; guard must not apply: %v", err)
	}
	if err := rangeCoverageVerdict(shardFor("balances", 0, 115394565), true, false, 0); err != nil {
		t.Fatalf("balances is a snapshot; guard must not apply: %v", err)
	}
	// 单块时间范围也受守卫约束
	now := time.Now()
	sh := shardFor("transactions", 0, 0)
	sh.FromTimestamp = &now
	sh.ToTimestamp = &now
	if err := rangeCoverageVerdict(sh, true, false, 0); err == nil {
		t.Fatal("timestamp-bounded unreached range must fail")
	}
}
