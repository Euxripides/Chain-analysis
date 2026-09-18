package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// serverMemoryCeiling is the measured max_server_memory_usage of the real
// instance (20,686,173,408 bytes = 19.28 GiB). The export scan must always run
// with a budget strictly below it.
const serverMemoryCeiling = int64(19) << 30

// TestExportScanBudgetIsBounded freezes the 2026-09-15 capacity fix. The export
// shape "SELECT * ... FINAL ... ORDER BY timestamp DESC LIMIT 2000000" needed
// 15.26 GiB and aborted with Code 241 against the 19.28 GiB server ceiling, so a
// legitimate multi-dataset ZIP produced no object at all. Removing the sort
// spill or raising the per-query cap above the ceiling must fail this test.
func TestExportScanBudgetIsBounded(t *testing.T) {
	sortSpill, ok := exportScanSettings["max_bytes_before_external_sort"].(int)
	if !ok || sortSpill <= 0 {
		t.Fatalf("the export sort must spill to disk, got %v", exportScanSettings["max_bytes_before_external_sort"])
	}
	groupSpill, ok := exportScanSettings["max_bytes_before_external_group_by"].(int)
	if !ok || groupSpill <= 0 {
		t.Fatalf("the export aggregation must spill to disk, got %v", exportScanSettings["max_bytes_before_external_group_by"])
	}
	perQueryCap, ok := exportScanSettings["max_memory_usage"].(int)
	if !ok || perQueryCap <= 0 {
		t.Fatalf("the export scan needs a positive per-query cap, got %v", exportScanSettings["max_memory_usage"])
	}
	if int64(perQueryCap) >= serverMemoryCeiling {
		t.Fatalf("per-query cap %d must stay below the server ceiling %d", perQueryCap, serverMemoryCeiling)
	}
	// The cap has to clear the real requirement measured on 2026-09-15 (7.19 GiB
	// peak for 2,000,000 token_transfers rows). A cap at or below it would make
	// the heavy multi-dataset ZIP fail again, which is the defect being fixed.
	const measuredRequirement = int64(7) << 30
	if int64(perQueryCap) <= measuredRequirement {
		t.Fatalf("per-query cap %d leaves no headroom over the measured %d requirement", perQueryCap, measuredRequirement)
	}
	if int64(perQueryCap) <= int64(sortSpill) {
		t.Fatalf("per-query cap %d must exceed the sort spill threshold %d", perQueryCap, sortSpill)
	}
	threads, ok := exportScanSettings["max_threads"].(int)
	if !ok || threads <= 0 {
		t.Fatalf("the export scan needs a bounded thread count, got %v", exportScanSettings["max_threads"])
	}
}

// TestExportScanContextKeepsRequestCancellation proves the bounded context is
// still derived from the request context: a cancelled request must cancel the
// export scan rather than leaving it running against the shared server.
func TestExportScanContextKeepsRequestCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	child := ExportScanContext(parent)
	if child == nil {
		t.Fatal("ExportScanContext returned nil")
	}
	cancel()
	select {
	case <-child.Done():
	default:
		t.Fatal("cancelling the request context must cancel the export scan")
	}
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatalf("cancelled scan should report context.Canceled, got %v", child.Err())
	}
}

// TestDescribeScanErrorTypesOnlyMemoryRefusals checks the typed, user-visible
// outcome: a real Code 241 refusal becomes ErrScanTooLarge (so the export row
// and the log name the capacity limit), while every other error passes through
// unchanged and is NOT reported as a memory refusal.
func TestDescribeScanErrorTypesOnlyMemoryRefusals(t *testing.T) {
	raw := fmt.Errorf("code: 241, message: (total) memory limit exceeded: would use 15.26 GiB, current RSS: 19.28 GiB, maximum: 19.28 GiB. OvercommitTracker decision: Query was selected to stop by OvercommitTracker")
	if !IsMemoryLimitError(raw) {
		t.Fatal("a Code 241 refusal must be recognised")
	}
	typed := DescribeScanError(raw)
	if !errors.Is(typed, ErrScanTooLarge) {
		t.Fatalf("a Code 241 refusal must become ErrScanTooLarge, got %v", typed)
	}
	if !strings.Contains(typed.Error(), "Code 241") {
		t.Fatalf("the typed error must name the ClickHouse code, got %v", typed)
	}

	benign := fmt.Errorf("code: 60, message: Table blockchain.token_transfers does not exist")
	if IsMemoryLimitError(benign) {
		t.Fatal("a non-memory error must not be classified as a memory refusal")
	}
	if got := DescribeScanError(benign); got != benign {
		t.Fatalf("a non-memory error must pass through unchanged, got %v", got)
	}
	if DescribeScanError(nil) != nil {
		t.Fatal("a nil error must stay nil")
	}
	if IsMemoryLimitError(nil) {
		t.Fatal("a nil error is not a memory refusal")
	}
}
