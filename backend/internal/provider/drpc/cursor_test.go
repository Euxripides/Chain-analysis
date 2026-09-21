package drpc

// 本文件覆盖分页游标：hash 摘要、重复/循环 Token、连续两页完全相同、页边界重复行，
// 以及调用方 PageState 优先于进程内记忆。
import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func rowsOf(t *testing.T, values ...string) []json.RawMessage {
	t.Helper()
	rows := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		rows = append(rows, json.RawMessage(value))
	}
	return rows
}

func TestTokenHashIsSHA256AndHidesToken(t *testing.T) {
	hash := TokenHash("real-page-token")
	if !strings.HasPrefix(hash, "sha256:") || len(hash) != len("sha256:")+64 {
		t.Fatalf("TokenHash 必须是 sha256:<64 hex>，得到 %s", hash)
	}
	if strings.Contains(hash, "real-page-token") {
		t.Fatalf("TokenHash 不得包含原 token")
	}
	if TokenHash("") != "" {
		t.Fatalf("空 token 应返回空摘要")
	}
	if TokenHash("a") == TokenHash("b") {
		t.Fatalf("不同 token 的摘要必须不同")
	}
}

func TestPageDigestIsContentSensitive(t *testing.T) {
	first := PageDigest(rowsOf(t, `{"a":1}`))
	if first != PageDigest(rowsOf(t, `{"a":1}`)) {
		t.Fatalf("同样内容必须得到同样摘要")
	}
	if first == PageDigest(rowsOf(t, `{"a":2}`)) {
		t.Fatalf("内容不同摘要必须不同")
	}
}

func TestCursorGuardRejectsRepeatAndLoop(t *testing.T) {
	guard := newCursorGuard()
	rows := rowsOf(t, `{"a":1}`)

	if _, err := guard.validate("k", "tok-1", "tok-1", PageDigest(rows), rows, PageState{}, false); err == nil {
		t.Fatalf("同一 token 原样返回必须报错")
	}
	if _, err := guard.validate("k", "tok-1", "tok-2", PageDigest(rows), rows, PageState{}, false); err != nil {
		t.Fatalf("正常推进不应报错: %v", err)
	}
	other := rowsOf(t, `{"a":2}`)
	if _, err := guard.validate("k", "tok-2", "tok-1", PageDigest(other), other, PageState{}, false); err == nil {
		t.Fatalf("回到已用过的 token 必须报循环")
	}
}

func TestCursorGuardRejectsIdenticalConsecutivePages(t *testing.T) {
	guard := newCursorGuard()
	rows := rowsOf(t, `{"a":1}`)
	if _, err := guard.validate("k", "", "tok-1", PageDigest(rows), rows, PageState{}, false); err != nil {
		t.Fatalf("第一页不应报错: %v", err)
	}
	if _, err := guard.validate("k", "tok-1", "tok-2", PageDigest(rows), rows, PageState{}, false); err == nil {
		t.Fatalf("连续两页完全相同必须报错")
	}
}

func TestCursorGuardEmptyTokenEndsPagination(t *testing.T) {
	guard := newCursorGuard()
	outcome, err := guard.validate("k", "tok-1", "", PageDigest(rowsOf(t, `{"a":1}`)), rowsOf(t, `{"a":1}`), PageState{}, false)
	if err != nil {
		t.Fatalf("空 token 表示结束，不应报错: %v", err)
	}
	if outcome.NextPageTokenHash != "" {
		t.Fatalf("空 token 的摘要应为空: %#v", outcome)
	}
}

func TestCursorGuardCountsBoundaryOverlap(t *testing.T) {
	guard := newCursorGuard()
	shared := `{"sig":"same"}`
	first := rowsOf(t, shared, `{"sig":"first"}`)
	if _, err := guard.validate("k", "", "tok-1", PageDigest(first), first, PageState{}, false); err != nil {
		t.Fatalf("第一页不应报错: %v", err)
	}
	second := rowsOf(t, shared, `{"sig":"second"}`)
	outcome, err := guard.validate("k", "tok-1", "tok-2", PageDigest(second), second, PageState{}, false)
	if err != nil {
		t.Fatalf("包含式分页不应报错（去重由下载层幂等键负责）: %v", err)
	}
	if outcome.OverlapRows != 1 {
		t.Fatalf("应统计出 1 行页边界重复，得到 %d", outcome.OverlapRows)
	}
}

func TestCursorGuardPrefersCallerPageState(t *testing.T) {
	// 进程内记忆为空，但调用方提供了 seen_token_hashes：必须据此判定循环。
	guard := newCursorGuard()
	state := PageState{
		NextPageTokenHash: TokenHash("tok-x"),
		SeenTokenHashes:   []string{TokenHash("tok-old")},
		LastPageDigest:    PageDigest(rowsOf(t, `{"a":1}`)),
	}
	if _, err := guard.validate("k", "tok-1", "tok-x", PageDigest(rowsOf(t, `{"a":2}`)), rowsOf(t, `{"a":2}`), state, true); err == nil {
		t.Fatalf("调用方 PageState 的上一页 token 必须被识别为重复")
	}
	state.NextPageTokenHash = ""
	if _, err := guard.validate("k", "tok-1", "tok-old", PageDigest(rowsOf(t, `{"a":2}`)), rowsOf(t, `{"a":2}`), state, true); err == nil {
		t.Fatalf("调用方 seen_token_hashes 必须能识别循环")
	}
	state.SeenTokenHashes = nil
	if _, err := guard.validate("k", "tok-1", "tok-new", PageDigest(rowsOf(t, `{"a":1}`)), rowsOf(t, `{"a":1}`), state, true); err == nil {
		t.Fatalf("调用方 LastPageDigest 必须能识别连续两页相同")
	}
}

func TestWithPageStateRoundTrip(t *testing.T) {
	state := PageState{NextPageTokenHash: "sha256:abc", SeenTokenHashes: []string{"sha256:def"}}
	ctx := WithPageState(context.Background(), state)
	got, ok := PageStateFromContext(ctx)
	if !ok || got.NextPageTokenHash != state.NextPageTokenHash || len(got.SeenTokenHashes) != 1 {
		t.Fatalf("PageState 往返失败: %#v %v", got, ok)
	}
	if _, ok := PageStateFromContext(context.Background()); ok {
		t.Fatalf("未注入时不应返回 PageState")
	}
}

func TestCursorKeySeparatesRanges(t *testing.T) {
	from, to := uint64(1), uint64(2)
	base := provider.FetchRequest{Dataset: DatasetTransactions, Address: testAddress}
	withRange := provider.FetchRequest{Dataset: DatasetTransactions, Address: testAddress, FromBlock: &from, ToBlock: &to}
	if cursorKey(base) == cursorKey(withRange) {
		t.Fatalf("不同区间的游标记忆必须隔离，避免跨 Shard 误判")
	}
	if cursorKey(base) != cursorKey(provider.FetchRequest{Dataset: DatasetTransactions, Address: testAddress}) {
		t.Fatalf("同一区间必须得到同一个 key")
	}
}
