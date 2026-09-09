package download

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
	"github.com/blockchain-platform/blockchain-platform/internal/spool"
)

// 文档 §72/§93（Disk Full 模拟）：Spool 水位 95% → 停止新 Fetch，页面不丢。
func TestSpoolWatermarkBackpressure(t *testing.T) {
	cfg := &config.Config{Ankr: config.AnkrConfig{
		SpoolMaxBytes:          1 << 20,
		SpoolWatermarkMild:     0.6,
		SpoolWatermarkReduce:   0.75,
		SpoolWatermarkPauseBG:  0.85,
		SpoolWatermarkStop:     0.95,
		CHBatchMaxBytes:        16 << 20,
		CHInsertP95Thresh:      5 * time.Second,
		FetchMemoryBudgetBytes: 1 << 30,
	}}
	sp, err := spool.New(spool.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	prov := ankr.NewMock(1000, nil)
	bp := newBackpressure(cfg, sp, newByteBudget(1<<30), prov)
	bp.Refresh(context.Background(), nil)
	if !bp.FetchAllowed() {
		t.Fatal("must start allowed")
	}
	// 写入 1.1MB 页面 → 超过 95% 水位
	big := json.RawMessage("\"" + strings.Repeat("a", 1_100_000) + "\"")
	_, _, err = sp.WritePage(spool.PageMeta{JobID: "j", Dataset: "logs", ShardID: "s", PageNo: 1}, []json.RawMessage{big})
	if err != nil {
		t.Fatal(err)
	}
	bp.lastUsedAt = time.Time{} // 强制下一轮重新统计
	bp.Refresh(context.Background(), nil)
	if bp.FetchAllowed() {
		t.Fatalf("fetch must be stopped at >=95%% watermark, ratio=%.3f", sp.UsedRatio())
	}
	if bp.State() != stateProtected {
		t.Fatalf("expected PROTECTED state, got %s", bp.State().String())
	}
	// 释放后：恢复迟滞（RECOVERING），但仍允许继续派发（<PROTECTED）
	if err := sp.RemoveShard("j", "logs", "s"); err != nil {
		t.Fatal(err)
	}
	bp.lastUsedAt = time.Time{} // 强制重新统计
	bp.Refresh(context.Background(), nil)
	if bp.State() != stateRecovering {
		t.Fatalf("expected RECOVERING (hysteresis), got %s", bp.State().String())
	}
	if !bp.FetchAllowed() {
		t.Fatal("fetch must be allowed again after watermark drops")
	}
}

// 文档 §47：全局内存预算必须有界。
func TestByteBudgetBlocking(t *testing.T) {
	b := newByteBudget(100)
	b.Reserve(60)
	if b.Used() != 60 {
		t.Fatalf("used must be 60, got %d", b.Used())
	}
	done := make(chan struct{})
	go func() {
		b.Reserve(50) // 60+50 > 100 → 阻塞直到 Release
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("reserve must block when budget exhausted")
	case <-time.After(100 * time.Millisecond):
	}
	b.Release(60)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reserve must unblock after release")
	}
	b.Release(50)
	if b.Used() != 0 {
		t.Fatalf("used must return to 0, got %d", b.Used())
	}
}
