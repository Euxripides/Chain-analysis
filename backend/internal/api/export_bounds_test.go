package api

import (
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestNewExportBoundsValidation(t *testing.T) {
	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if _, err := newExportBounds(&from, &to); err == nil {
		t.Fatal("start_time 晚于 end_time 必须被拒绝")
	}
	for _, tc := range []struct {
		name       string
		from, to   *time.Time
		wantZero   bool
		wantFromOK bool
	}{
		{name: "都不给", wantZero: true},
		{name: "只给起点", from: &from, wantFromOK: true},
		{name: "只给终点", to: &to},
		{name: "两端都给", from: &to, to: &from},
	} {
		got, err := newExportBounds(tc.from, tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.wantZero && !got.isZero() {
			t.Fatalf("%s: want zero bounds, got %+v", tc.name, got)
		}
		if tc.wantFromOK && got.From == nil {
			t.Fatalf("%s: from must be kept", tc.name)
		}
	}
}

func TestExportBoundsIntersect(t *testing.T) {
	a := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	c := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// 请求窗口 [a,c] 与作业窗口 [b,c] ⇒ [b,c]：作业窗口更晚的起点必须生效。
	got := exportBounds{From: &a, To: &c}.intersect(exportBounds{From: &b, To: &c})
	if got.From == nil || !got.From.Equal(b) {
		t.Fatalf("intersect from = %v, want %v", got.From, b)
	}
	// 空交集不报错，而是留一个取不到行的窗口（"该范围没有数据"）。
	empty := exportBounds{From: &b, To: &c}.intersect(exportBounds{From: &a, To: &a})
	if empty.From == nil || empty.To == nil || !empty.From.After(*empty.To) {
		t.Fatalf("empty intersection must stay inverted, got %+v", empty)
	}
	// 任一侧为 nil 表示不限，取另一侧。
	only := exportBounds{}.intersect(exportBounds{From: &a, To: &c})
	if only.From == nil || only.To == nil || !only.From.Equal(a) || !only.To.Equal(c) {
		t.Fatalf("nil bounds must adopt the other side, got %+v", only)
	}
}

// TestExportStreamQueryPartsPlaceholdersMatchArgs 钉住"占位符个数 == 参数个数"。
//
// logs/balances 的条件只有两个占位符，而历史的 exportStreamArgs 恒返回三个参数；
// 多传参数会被驱动直接拒绝（导出失败），少传则更糟——会把条件错位绑定。
func TestExportStreamQueryPartsPlaceholdersMatchArgs(t *testing.T) {
	bounds := exportBounds{
		From: timePtr(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		To:   timePtr(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)),
	}
	for _, dataset := range []string{"transactions", "token_transfers", "nft_transfers", "logs", "balances", "internal_transactions"} {
		for _, b := range []exportBounds{{}, bounds, {From: bounds.From}} {
			table, where, args := exportStreamQueryParts(dataset, "bsc", "0xabc")
			where, args = appendExportTimeBounds(where, args, b)
			if table == "" {
				t.Fatalf("%s: empty table", dataset)
			}
			if got, want := strings.Count(where, "?"), len(args); got != want {
				t.Fatalf("%s (bounds=%+v): placeholders=%d args=%d (%s)", dataset, b, got, want, where)
			}
			sql := exportStreamSQLUnbounded(table, where, 0)
			if strings.Contains(sql, "LIMIT") {
				t.Fatalf("%s: unbounded SQL must not carry LIMIT: %s", dataset, sql)
			}
			sql = exportStreamSQLUnbounded(table, where, 10)
			// LIMIT ? OFFSET ? 的两个参数由调用方追加，这里只校验语句形状。
			if !strings.HasSuffix(sql, "LIMIT ? OFFSET ?") {
				t.Fatalf("%s: bounded SQL must end with LIMIT ? OFFSET ?: %s", dataset, sql)
			}
		}
	}
}

// TestColumnarTokenFilterDecision 钉住"哪些作业能用列式路径表达"。
//
// 判据错一边的后果都是导出内容不同：太宽会把范围/过滤条件丢掉一部分（导出范围外
// 的数据），太窄会让 Actions 的真实路径重新落回 2 百万行的内存上限。
func TestColumnarTokenFilterDecision(t *testing.T) {
	if _, ok := columnarTokenFilter(nil); !ok {
		t.Fatal("无作业导出必须支持列式路径")
	}
	tokenJob := &model.DownloadJob{RangeType: "time", Stats: map[string]any{"gpt_actions_token_filter": "usdt"}}
	if token, ok := columnarTokenFilter(tokenJob); !ok || token != "USDT" {
		t.Fatalf("token 过滤作业应支持且归一化为大写，got (%q,%v)", token, ok)
	}
	blank := &model.DownloadJob{RangeType: "time", Stats: map[string]any{"gpt_actions_token_filter": "  "}}
	if token, ok := columnarTokenFilter(blank); !ok || token != "" {
		t.Fatalf("空白 token 过滤等价于不过滤，got (%q,%v)", token, ok)
	}
	from := int64(1)
	blockJob := &model.DownloadJob{RangeType: "block", FromBlock: &from}
	if _, ok := columnarTokenFilter(blockJob); ok {
		t.Fatal("区块范围作业不能走列式路径（列式只实现了时间范围）")
	}
	otherFilter := &model.DownloadJob{RangeType: "time", Stats: map[string]any{"some_other_filter": "x"}}
	if _, ok := columnarTokenFilter(otherFilter); ok {
		t.Fatal("未知过滤器必须拒绝列式路径")
	}
}

func timePtr(t time.Time) *time.Time { return &t }
