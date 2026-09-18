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
