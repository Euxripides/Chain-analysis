package query

import (
	"context"
	"testing"
	"time"
)

// TestExportScansAreSerialised freezes the other half of the 2026-09-15
// capacity fix: the export query is the only shape in this service that can
// hold gigabytes on the ClickHouse server, so at most one may run at a time.
// Removing the bound (a larger buffer) or leaking a slot would fail here.
func TestExportScansAreSerialised(t *testing.T) {
	if got := cap(exportScanSlots); got != 1 {
		t.Fatalf("export scans must be serialised to one slot, got %d", got)
	}
	release, err := acquireExportScanSlot(context.Background())
	if err != nil {
		t.Fatalf("the first export scan must acquire the slot: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := acquireExportScanSlot(ctx); err == nil {
		t.Fatal("a second concurrent export scan must not acquire the slot")
	}
	release()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	release2, err := acquireExportScanSlot(ctx2)
	if err != nil {
		t.Fatalf("the slot must be reusable once released: %v", err)
	}
	release2()
	if len(exportScanSlots) != 0 {
		t.Fatalf("the slot must be empty after release, got %d", len(exportScanSlots))
	}
}

// TestExportScanSlotRespectsCancellation proves a queued export gives up with
// its caller instead of blocking forever behind a long scan.
func TestExportScanSlotRespectsCancellation(t *testing.T) {
	release, err := acquireExportScanSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := acquireExportScanSlot(cancelled); err != context.Canceled {
		t.Fatalf("a cancelled caller must get context.Canceled, got %v", err)
	}
}

// TestSpeculativeScanYieldsInsteadOfQueueing 锁定预生成（投机扫描）的资源边界。
//
// 导出扫描槽容量是 1。预生成是"用户还没点导出"的投机任务，**绝不能排队占住槽位**：
// 否则一个 20 秒的预生成会把正在等结果的用户挡在后面。带投机标记的扫描抢不到槽
// 必须立刻返回 ErrSpeculativeScanBusy，而普通导出仍然排队等待。
func TestSpeculativeScanYieldsInsteadOfQueueing(t *testing.T) {
	release1, err := acquireExportScanSlot(context.Background())
	if err != nil {
		t.Fatalf("hold slot: %v", err)
	}

	// 投机扫描：槽被占时立即让路，不阻塞。
	start := time.Now()
	if _, serr := acquireExportScanSlotFor(WithSpeculativeScan(context.Background())); serr != ErrSpeculativeScanBusy {
		t.Fatalf("want ErrSpeculativeScanBusy, got %v", serr)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("speculative scan blocked for %s; it must yield immediately", elapsed)
	}

	// 普通扫描：排队等槽，释放后必须拿到。
	queued := make(chan error, 1)
	go func() {
		rel, qerr := acquireExportScanSlotFor(context.Background())
		if qerr == nil {
			rel()
		}
		queued <- qerr
	}()
	select {
	case err := <-queued:
		t.Fatalf("normal scan acquired the slot while it was held (err=%v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	release1()
	select {
	case err := <-queued:
		if err != nil {
			t.Fatalf("normal scan failed after the slot was released: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("normal scan never acquired the slot after it was released")
	}
}
