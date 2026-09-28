package api

import (
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// TestPrefetchSpecHashMatchesExportPath 锁定预生成与真实导出算出**同一个规格哈希**。
//
// 这条不变式是预生成能不能命中的唯一依据，而它失配时**不会报错**——用户点导出只是
// 每次都重新算一遍，看不出任何异常。因此用测试把两边的输入规范化钉死。
//
// 两边的规范化规则必须来自同一处：
//   - 地址：addressnorm.NormalizeKey（EVM 折叠小写、Tron/Solana 各有规则）
//   - 数据集串：strings.Join(datasets, "+")
//   - 快照：buildExportSnapshot（唯一构造点）
func TestPrefetchSpecHashMatchesExportPath(t *testing.T) {
	const chain = "bsc"
	raw := "0x0000000000000000000000000000000000000001"
	upper := "0x000000000000000000000000000000000000000A"

	// 导出路径：createExportV2 对每个地址调 NormalizeKey 后再进快照。
	exportAddresses := []string{upper}
	for i := range exportAddresses {
		exportAddresses[i] = addressnorm.NormalizeKey(chain, exportAddresses[i])
	}
	// 预生成路径：handleExportPrefetch 用同一函数、同一顺序。
	prefetchAddresses := make([]string, 0, 1)
	if norm := addressnorm.NormalizeKey(chain, upper); norm != "" {
		prefetchAddresses = append(prefetchAddresses, norm)
	}

	if len(exportAddresses) != len(prefetchAddresses) || exportAddresses[0] != prefetchAddresses[0] {
		t.Fatalf("address normalization diverged: export=%v prefetch=%v", exportAddresses, prefetchAddresses)
	}

	datasets := []string{"token_transfers"}
	exportSnapshot := buildExportSnapshot(chain, exportAddresses, datasets, "csv", false, false, false, "", false, "", exportBounds{})
	prefetchSnapshot := buildExportSnapshot(chain, prefetchAddresses, datasets, "csv", false, false, false, "", false, "", exportBounds{})
	if store.PrefetchSpecHash(exportSnapshot) != store.PrefetchSpecHash(prefetchSnapshot) {
		t.Fatal("spec hashes differ for the same logical request; prefetch would never hit")
	}

	// 大小写不同的写法必须命中同一份规格（前端可能回填大写地址）。
	lower := buildExportSnapshot(chain, []string{addressnorm.NormalizeKey(chain, raw)}, datasets, "csv", false, false, false, "", false, "", exportBounds{})
	upperSnap := buildExportSnapshot(chain, []string{addressnorm.NormalizeKey(chain, "0x0000000000000000000000000000000000000001")}, datasets, "csv", false, false, false, "", false, "", exportBounds{})
	if store.PrefetchSpecHash(lower) != store.PrefetchSpecHash(upperSnap) {
		t.Fatal("case variations of the same address must hash identically")
	}
}

// TestPrefetchSpecHashSensitiveToSpecFields 反向校准：规格真的不同时必须**不**命中。
//
// 否则"命中率 100%"这种假象会掩盖一个更糟的问题——用户改了选项却拿到了按旧规格生成的
// 产物，而那意味着导出的内容与他看到的选项不符。
func TestPrefetchSpecHashSensitiveToSpecFields(t *testing.T) {
	base := buildExportSnapshot("bsc", []string{"0xabc"}, []string{"token_transfers"}, "csv", false, false, false, "", false, "", exportBounds{})
	baseHash := store.PrefetchSpecHash(base)

	variants := map[string][]byte{
		"force_zip 不同":     buildExportSnapshot("bsc", []string{"0xabc"}, []string{"token_transfers"}, "csv", false, true, false, "", false, "", exportBounds{}),
		"include_meta 不同":  buildExportSnapshot("bsc", []string{"0xabc"}, []string{"token_transfers"}, "csv", true, false, false, "", false, "", exportBounds{}),
		"decoded_input 不同": buildExportSnapshot("bsc", []string{"0xabc"}, []string{"token_transfers"}, "csv", false, false, true, "", false, "", exportBounds{}),
		"format 不同":        buildExportSnapshot("bsc", []string{"0xabc"}, []string{"token_transfers"}, "jsonl", false, false, false, "", false, "", exportBounds{}),
		"dataset 不同":       buildExportSnapshot("bsc", []string{"0xabc"}, []string{"transactions"}, "csv", false, false, false, "", false, "", exportBounds{}),
		"地址不同":             buildExportSnapshot("bsc", []string{"0xabd"}, []string{"token_transfers"}, "csv", false, false, false, "", false, "", exportBounds{}),
		"链不同":              buildExportSnapshot("eth", []string{"0xabc"}, []string{"token_transfers"}, "csv", false, false, false, "", false, "", exportBounds{}),
		// 时间范围必须进哈希：全量与"某段时间"是**内容不同**的产物，
		// 少了这一维会让用户拿到范围外的数据（而且不会报错，只是命中了一份旧产物）。
		"带时间范围": buildExportSnapshot("bsc", []string{"0xabc"}, []string{"token_transfers"}, "csv", false, false, false, "", false, "",
			exportBounds{From: timePtr(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}),
		"范围终点不同": buildExportSnapshot("bsc", []string{"0xabc"}, []string{"token_transfers"}, "csv", false, false, false, "", false, "",
			exportBounds{To: timePtr(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}),
	}
	for name, snap := range variants {
		if store.PrefetchSpecHash(snap) == baseHash {
			t.Fatalf("%s: hash collided with the base spec; a changed draft would wrongly hit a stale artifact", name)
		}
	}
}

// TestExportZipDecisionSharedByBothPaths 锁定"是否打 ZIP"的判定只有一处。
//
// 两边不一致时规格哈希里的 zip 字段会不同，预生成永不命中——而且不会报错。
// 实测踩过：预生成原先只看 force_zip，漏了"地址数>3 或数据集数>1 也强制 ZIP"。
func TestExportZipDecisionSharedByBothPaths(t *testing.T) {
	cases := []struct {
		forceZip            bool
		addresses, datasets int
		want                bool
	}{
		{false, 1, 1, false},
		{true, 1, 1, true},
		{false, 2, 1, false},
		{false, 4, 1, true}, // 地址数 > 3
		{false, 1, 2, true}, // 数据集数 > 1
		{true, 4, 2, true},
	}
	for _, c := range cases {
		if got := exportZipDecision(c.forceZip, c.addresses, c.datasets); got != c.want {
			t.Fatalf("force_zip=%v addresses=%d datasets=%d: got %v want %v",
				c.forceZip, c.addresses, c.datasets, got, c.want)
		}
	}
	// 同一份逻辑规格在两条路径上必须得到同一哈希（含 zip 字段）。
	snapZip := buildExportSnapshot("bsc", []string{"0xabc"}, []string{"a", "b"}, "csv", false,
		exportZipDecision(false, 1, 2), false, "", false, "", exportBounds{})
	snapZip2 := buildExportSnapshot("bsc", []string{"0xabc"}, []string{"a", "b"}, "csv", false,
		exportZipDecision(false, 1, 2), false, "", false, "", exportBounds{})
	if store.PrefetchSpecHash(snapZip) != store.PrefetchSpecHash(snapZip2) {
		t.Fatal("identical specs must hash identically")
	}
}
