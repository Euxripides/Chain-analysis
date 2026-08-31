package ankr

import (
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func testPolicy() *PagePolicy {
	cfg := config.Load()
	cfg.Ankr.PageLevels = []int{1000, 2000, 5000, 10000}
	cfg.Ankr.PageUpSuccessCount = 3
	cfg.Ankr.PageUpMaxDuration = 30 * time.Second
	cfg.Ankr.PageUpMaxBodyBytes = 16 << 20
	cfg.Ankr.PageDownCooldownSuccess = 5
	cfg.Ankr.RangeSplitOnMinPageFailures = 3
	cfg.Ankr.PageSizeLogs = 5000
	return NewPagePolicy(cfg)
}

// §42：初始 10000；连续 TooLarge → 10000→5000→2000→1000，最小不再降
func TestDowngradeLadder(t *testing.T) {
	p := testPolicy()
	key := p.Key("bsc", "token_transfers", "address", "0x1")
	if got := p.Current(key, "token_transfers"); got != 10000 {
		t.Fatalf("initial = %d, want 10000", got)
	}
	want := []int{5000, 2000, 1000, 1000}
	for i, w := range want {
		dec := p.OnError(key, "token_transfers",
			provider.NewResponseTooLarge("too large", p.Current(key, "token_transfers"), 70<<20), p.Current(key, "token_transfers"))
		if dec.NextPageSize != w {
			t.Fatalf("step %d: next=%d want %d", i, dec.NextPageSize, w)
		}
		if i == 3 && dec.SplitRange {
			// 第 4 次才算 min-page 失败计数
		}
	}
}

// §42：min page 连续失败达阈值 → SplitRange
func TestSplitRangeAtMinPage(t *testing.T) {
	p := testPolicy()
	key := p.Key("bsc", "token_transfers", "address", "0x1")
	p.OnError(key, "token_transfers", provider.NewResponseTooLarge("", 10000, 0), 10000)
	p.OnError(key, "token_transfers", provider.NewResponseTooLarge("", 5000, 0), 5000)
	p.OnError(key, "token_transfers", provider.NewResponseTooLarge("", 2000, 0), 2000)
	dec := p.OnError(key, "token_transfers", provider.NewResponseTooLarge("", 1000, 0), 1000)
	if dec.SplitRange {
		t.Fatal("split only after 3 failures at min page")
	}
	dec = p.OnError(key, "token_transfers", provider.NewResponseTooLarge("", 1000, 0), 1000)
	if dec.SplitRange {
		t.Fatal("split only after 3 failures at min page")
	}
	dec = p.OnError(key, "token_transfers", provider.NewResponseTooLarge("", 1000, 0), 1000)
	if !dec.SplitRange {
		t.Fatal("want SplitRange=true after 3 min-page failures")
	}
}

// §42：连续快成功升档一次一级；降档后有 cooldown（防振荡）
func TestUpgradeOneStepWithCooldown(t *testing.T) {
	p := testPolicy()
	key := p.Key("bsc", "transactions", "address", "0x1")
	// 先降到 5000
	dec := p.OnError(key, "transactions", provider.NewResponseTooLarge("", 10000, 0), 10000)
	if dec.NextPageSize != 5000 {
		t.Fatal("expect downgrade to 5000")
	}
	cur := 5000
	// 降档后 cooldown=5：前 4 次快成功仍不允许升档
	for i := 0; i < 4; i++ {
		cur = p.OnSuccess(key, "transactions", int64(cur), 1<<20, time.Second)
	}
	if cur != 5000 {
		t.Fatalf("cooldown should prevent upgrade, got %d", cur)
	}
	// 第 5 次快成功：cooldown 清零 + 快成功累计>=3 → 升一级
	cur = p.OnSuccess(key, "transactions", int64(cur), 1<<20, time.Second)
	if cur != 10000 {
		t.Fatalf("want upgrade to 10000, got %d", cur)
	}
}

// §44：Resume 继承（Snapshot/Restore 往返）
func TestRestoreKeepsState(t *testing.T) {
	p := testPolicy()
	key := p.Key("bsc", "token_transfers", "address", "0x1")
	p.OnError(key, "token_transfers", provider.NewResponseTooLarge("", 10000, 0), 10000)
	snap := p.Snapshot(key)
	if snap.CurrentPageSize != 5000 {
		t.Fatalf("snapshot page = %d", snap.CurrentPageSize)
	}
	p2 := testPolicy()
	p2.Restore(key, snap)
	if p2.Current(key, "token_transfers") != 5000 {
		t.Fatalf("restored page = %d, want 5000", p2.Current(key, "token_transfers"))
	}
}
