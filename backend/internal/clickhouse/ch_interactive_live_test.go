//go:build live

package clickhouse

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveInteractiveSettingsReachTheServer asks the real ClickHouse instance
// what it actually received, instead of trusting query_log inference. Run with:
//
//	CLICKHOUSE_URL=... go test -tags live ./internal/clickhouse/ -run TestLiveInteractive -v
func TestLiveInteractiveSettingsReachTheServer(t *testing.T) {
	url := os.Getenv("CLICKHOUSE_URL")
	if url == "" {
		t.Skip("CLICKHOUSE_URL is not set")
	}
	ctx := context.Background()
	conn, err := New(ctx, url)
	if err != nil {
		t.Fatalf("clickhouse open: %v", err)
	}
	defer conn.Close()

	// getSetting() reports every setting as Float64 (ClickHouse SettingFieldFloat),
	// which is exactly why the value must be read back instead of assumed.
	effective := func(queryCtx context.Context, label string) (float64, float64, float64) {
		var seconds, memory, threads float64
		if err := conn.DB().QueryRow(queryCtx,
			"SELECT toFloat64(getSetting('max_execution_time')), toFloat64(getSetting('max_memory_usage')), toFloat64(getSetting('max_threads'))").
			Scan(&seconds, &memory, &threads); err != nil {
			t.Fatalf("%s: read effective settings: %v", label, err)
		}
		return seconds, memory, threads
	}

	plain, plainMemory, plainThreads := effective(ctx, "plain")
	t.Logf("plain    max_execution_time=%v max_memory_usage=%v max_threads=%v", plain, plainMemory, plainThreads)

	interactive, memory, threads := effective(InteractiveScanContext(ctx), "interactive")
	t.Logf("interactive max_execution_time=%v max_memory_usage=%v max_threads=%v", interactive, memory, threads)
	if budget := float64(InteractiveScanTimeout / time.Second); interactive != budget || memory != float64(int(2)<<30) || threads != 4 {
		t.Errorf("interactiveScanSettings did not reach the server: got execution=%v memory=%v threads=%v",
			interactive, memory, threads)
	}

	// A request-shaped context carries the 60 s router middleware deadline.
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	derived, derivedMemory, derivedThreads := effective(InteractiveScanContext(requestCtx), "settings under a 60s request deadline")
	t.Logf("settings under a 60s request deadline max_execution_time=%v max_memory_usage=%v max_threads=%v",
		derived, derivedMemory, derivedThreads)
	// clickhouse-go derives max_execution_time from the deadline and that value
	// wins over the setting, which is why the query layer ALSO applies
	// InteractiveScanTimeout as a deadline. This records the derived value
	// (measured 64 s for a 60 s deadline) so the trap cannot be forgotten.
	if derived < 60 {
		t.Logf("note: the driver no longer raises the explicit bound under a 60s deadline (got %v)", derived)
	}

	// The real request path: the 60 s router deadline with the interactive budget
	// applied on top of it.
	budgetCtx, budgetCancel := context.WithTimeout(ctx, 60*time.Second)
	scanCtx, scanCancel := context.WithTimeout(budgetCtx, InteractiveScanTimeout)
	scanned, scannedMemory, scannedThreads := effective(InteractiveScanContext(scanCtx), "interactive budget on a 60s request")
	scanCancel()
	budgetCancel()
	t.Logf("interactive budget on a 60s request max_execution_time=%v max_memory_usage=%v max_threads=%v",
		scanned, scannedMemory, scannedThreads)
	if scanned >= 60 {
		t.Errorf("the interactive budget must keep the server bound below the 60 s router middleware, got %v", scanned)
	}
	if scannedMemory != float64(int(2)<<30) || scannedThreads != 4 {
		t.Errorf("the interactive memory/thread bound did not reach the server: memory=%v threads=%v", scannedMemory, scannedThreads)
	}
}
