package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// routerTimeoutSeconds is the chi middleware.Timeout applied to every request in
// internal/api. An interactive scan bound must stay strictly inside it: a scan
// the transport cancels can no longer be answered with a diagnosable code, which
// is exactly the P4-FLOW-01 defect (a 60 s transport kill collapsed into a 500).
const routerTimeoutSeconds = 60

// TestInteractiveScanBudgetIsBounded freezes the 2026-09-15 flow-summary fix.
//
// Measured on the real instance: the flow-summary aggregate with FINAL read
// 85,074,805 rows and held 6.87-10.62 GiB per call (12 real calls in 30 minutes,
// 23.4 s to 52.3 s each) and was refused with Code 241 at the 19.60 GiB server
// ceiling. The rewritten, explicitly deduplicated shape returns the same answer
// from 1.0-1.4 M rows in 0.3-3.3 s and 11-36 MiB. Deleting the bound, raising it
// to the export footprint, or letting it run past the router timeout must fail
// this test.
func TestInteractiveScanBudgetIsBounded(t *testing.T) {
	perQueryCap, ok := interactiveScanSettings["max_memory_usage"].(int)
	if !ok || perQueryCap <= 0 {
		t.Fatalf("an interactive scan needs a positive per-query cap, got %v", interactiveScanSettings["max_memory_usage"])
	}
	exportCap, ok := exportScanSettings["max_memory_usage"].(int)
	if !ok || exportCap <= 0 {
		t.Fatalf("the export cap is unreadable, got %v", exportScanSettings["max_memory_usage"])
	}
	if perQueryCap >= exportCap {
		t.Fatalf("interactive cap %d must stay below the export cap %d so several request-path reads fit where one export fits", perQueryCap, exportCap)
	}
	if int64(perQueryCap) >= serverMemoryCeiling {
		t.Fatalf("interactive cap %d must stay below the server ceiling %d", perQueryCap, serverMemoryCeiling)
	}
	// The cap has to clear the measured requirement (36 MiB worst observed) with
	// real headroom for a pathological address, otherwise the endpoint starts
	// refusing legitimate busy addresses again.
	const measuredRequirement = int64(36) << 20
	if int64(perQueryCap) <= 8*measuredRequirement {
		t.Fatalf("interactive cap %d leaves no headroom over the measured %d requirement", perQueryCap, measuredRequirement)
	}
	groupSpill, ok := interactiveScanSettings["max_bytes_before_external_group_by"].(int)
	if !ok || groupSpill <= 0 {
		t.Fatalf("an interactive aggregation must spill to disk, got %v", interactiveScanSettings["max_bytes_before_external_group_by"])
	}
	if groupSpill >= perQueryCap {
		t.Fatalf("the group-by spill threshold %d must stay below the per-query cap %d", groupSpill, perQueryCap)
	}
	seconds, ok := interactiveScanSettings["max_execution_time"].(int)
	if !ok || seconds <= 0 {
		t.Fatalf("an interactive scan needs a bounded execution time, got %v", interactiveScanSettings["max_execution_time"])
	}
	if seconds >= routerTimeoutSeconds {
		t.Fatalf("interactive execution time %d s must stay strictly inside the %d s router timeout, otherwise the transport kills the request before the handler can answer", seconds, routerTimeoutSeconds)
	}
	// The deadline and the per-query setting must be the same budget: the query
	// layer applies InteractiveScanTimeout, and the setting is what the server
	// sees when no deadline is present.
	if budget := int(InteractiveScanTimeout / time.Second); seconds != budget {
		t.Fatalf("the per-query execution bound %d s must equal InteractiveScanTimeout (%d s)", seconds, budget)
	}
	if InteractiveScanTimeout <= 0 || InteractiveScanTimeout >= routerTimeoutSeconds*time.Second {
		t.Fatalf("InteractiveScanTimeout %v must stay strictly inside the %d s router middleware", InteractiveScanTimeout, routerTimeoutSeconds)
	}
	threads, ok := interactiveScanSettings["max_threads"].(int)
	if !ok || threads <= 0 {
		t.Fatalf("an interactive scan needs a bounded thread count, got %v", interactiveScanSettings["max_threads"])
	}
}

// TestInteractiveScanContextKeepsRequestCancellation proves the bounded context
// is still derived from the request context: a cancelled request must cancel the
// scan instead of leaving it running against the shared server.
func TestInteractiveScanContextKeepsRequestCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	child := InteractiveScanContext(parent)
	if child == nil {
		t.Fatal("InteractiveScanContext returned nil")
	}
	cancel()
	select {
	case <-child.Done():
	default:
		t.Fatal("cancelling the request context must cancel the interactive scan")
	}
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatalf("cancelled scan should report context.Canceled, got %v", child.Err())
	}
}

// TestDescribeInteractiveScanErrorTypesRefusals checks the typed, user-visible
// outcome: a Code 241 memory refusal becomes ErrScanTooLarge, a Code 159
// execution-time refusal (or an expired request deadline) becomes
// ErrScanTimeout, and every other error passes through unchanged so a real
// defect is still a 500.
func TestDescribeInteractiveScanErrorTypesRefusals(t *testing.T) {
	memory := fmt.Errorf("code: 241, message: (total) memory limit exceeded: would use 2.10 GiB, current RSS: 19.63 GiB, maximum: 19.60 GiB")
	if !errors.Is(DescribeInteractiveScanError(memory), ErrScanTooLarge) {
		t.Fatalf("a Code 241 refusal must become ErrScanTooLarge, got %v", DescribeInteractiveScanError(memory))
	}

	timeout := fmt.Errorf("code: 159, message: Timeout exceeded: elapsed 25.002 seconds, maximum: 25")
	if !IsExecutionTimeoutError(timeout) {
		t.Fatal("a Code 159 refusal must be recognised")
	}
	typedTimeout := DescribeInteractiveScanError(timeout)
	if !errors.Is(typedTimeout, ErrScanTimeout) {
		t.Fatalf("a Code 159 refusal must become ErrScanTimeout, got %v", typedTimeout)
	}
	if !strings.Contains(typedTimeout.Error(), "Code 159") {
		t.Fatalf("the typed error must name the ClickHouse code, got %v", typedTimeout)
	}

	symbolic := fmt.Errorf("DB::Exception: TIMEOUT_EXCEEDED: max_execution_time exceeded")
	if !errors.Is(DescribeInteractiveScanError(symbolic), ErrScanTimeout) {
		t.Fatalf("the symbolic form must be typed too, got %v", DescribeInteractiveScanError(symbolic))
	}

	deadline := fmt.Errorf("read tcp: %w", context.DeadlineExceeded)
	if !errors.Is(DescribeInteractiveScanError(deadline), ErrScanTimeout) {
		t.Fatalf("an expired request deadline must become ErrScanTimeout, got %v", DescribeInteractiveScanError(deadline))
	}

	benign := fmt.Errorf("code: 60, message: Table blockchain.token_transfers does not exist")
	if IsExecutionTimeoutError(benign) {
		t.Fatal("a non-timeout error must not be classified as a timeout")
	}
	if got := DescribeInteractiveScanError(benign); got != benign {
		t.Fatalf("a benign error must pass through unchanged, got %v", got)
	}
	if DescribeInteractiveScanError(nil) != nil {
		t.Fatal("a nil error must stay nil")
	}
	if IsExecutionTimeoutError(nil) {
		t.Fatal("a nil error is not a timeout")
	}
}
