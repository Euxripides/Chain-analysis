// V1.2 Planner Count-aware 纯逻辑测试：shard 推荐 / 模式映射 / hash。
package download

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestRecommendInitialShardsMapping(t *testing.T) {
	cases := []struct {
		pages int64
		want  int
	}{
		{0, 1}, {1, 1},
		{2, 2}, {5, 2},
		{6, 2}, {15, 2}, {20, 2},
		{21, 4}, {80, 4},
		{81, 8}, {2000, 8},
	}
	for _, c := range cases {
		got := recommendInitialShards(c.pages)
		if got != c.want {
			t.Errorf("pages=%d recommend=%d want=%d", c.pages, got, c.want)
		}
	}
}

func TestModeShardsEconomyTurbo(t *testing.T) {
	p := &Planner{}
	if got := p.modeShards("economy", 4); got != 2 {
		t.Fatalf("economy(4)=%d want 2", got)
	}
	if got := p.modeShards("economy", 1); got != 1 {
		t.Fatalf("economy(1)=%d want 1 (floor)", got)
	}
	if got := p.modeShards("turbo", 8); got != 16 {
		t.Fatalf("turbo(8)=%d want 16", got)
	}
	if got := p.modeShards("turbo", 16); got != 16 {
		t.Fatalf("turbo(16)=%d want capped 16", got)
	}
	if got := p.modeShards("balanced", 2); got != 2 {
		t.Fatalf("balanced(2)=%d want 2", got)
	}
}

func TestSubjectHashStable(t *testing.T) {
	a := subjectHash("0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28")
	b := subjectHash("0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28")
	if a != b || len(a) != 16 {
		t.Fatalf("subjectHash unstable or wrong length: %s", a)
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("short", 100); got != "short" {
		t.Fatalf("short got %q", got)
	}
	got := truncateStr("0123456789", 6)
	if len(got) != 9 { // 6 chars + "..."
		t.Fatalf("truncate length wrong: %q", got)
	}
}

func TestTransactionsCertificationBenchmarkBypassesProductionGate(t *testing.T) {
	p := &Planner{transactionsRange: false, txCertRequired: true}
	bench := &model.DownloadJob{
		Chain:  "bsc",
		Source: "benchmark",
		Stats:  map[string]any{"benchmark_shard_count": float64(4)},
	}
	if !p.multiRangeEligibleForJob(bench, "transactions") {
		t.Fatal("fixed-shard transactions benchmark must be allowed before certification")
	}
	ordinary := &model.DownloadJob{Chain: "bsc", Source: "user_download", Stats: bench.Stats}
	if p.multiRangeEligibleForJob(ordinary, "transactions") {
		t.Fatal("ordinary transactions job must remain blocked before certification")
	}
	bench.Stats["benchmark_shard_count"] = float64(256)
	if !p.multiRangeEligibleForJob(bench, "transactions") {
		t.Fatal("256-shard benchmark must be allowed for final concurrency qualification")
	}
	bench.Stats["benchmark_shard_count"] = float64(257)
	if p.multiRangeEligibleForJob(bench, "transactions") {
		t.Fatal("out-of-range benchmark shard count must not bypass the gate")
	}
}
