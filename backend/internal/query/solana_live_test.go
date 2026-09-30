package query

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

// Exercise the actual ClickHouse schema and SQL, not merely a mock that accepts
// any statement. The address has one finalized signature on the dRPC archive.
func TestLiveSolanaAnalysisQueries(t *testing.T) {
	if os.Getenv("SOLANA_QUERY_LIVE") != "1" {
		t.Skip("explicit live ClickHouse query test only")
	}
	dsn := os.Getenv("CLICKHOUSE_URL")
	if dsn == "" {
		t.Fatal("CLICKHOUSE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ch, err := clickhouse.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	q := New(ch)
	var precise string
	if err := ch.DB().QueryRow(ctx, `SELECT toString(divideDecimal(toDecimal256OrZero('4970008000',0),toDecimal256(1000000000,0),9))`).Scan(&precise); err != nil {
		t.Fatalf("precise lamport conversion: %v", err)
	}
	if precise != "4.970008" {
		t.Fatalf("precise lamport conversion = %q", precise)
	}
	const address = "5x3ENUr1P1DRXHqPLZ5LeGwccDXnNUxBR1UMCrq2aZ3e"
	if batch, err := q.ActionBatchOverview(ctx, "solana", []string{address}); err != nil {
		t.Fatalf("batch overview: %v", err)
	} else if len(batch) != 1 || batch[0].NativeBalanceSource != "balance_snapshot" || batch[0].NativeBalance == "0" {
		t.Fatalf("batch overview did not include stored SOL snapshot: %+v", batch)
	}
	flow, err := q.ActionFlowSummary(ctx, "solana", address, "SOL", "", nil, nil)
	if err != nil {
		t.Fatalf("native flow: %v", err)
	}
	t.Logf("native in=%s out=%s", flow.TotalIn, flow.TotalOut)
	if _, err := q.ActionCounterparties(ctx, ActionFilter{Chain: "solana", Address: address, Token: "SOL"}, "", 0, 5, "total_amount"); err != nil {
		t.Fatalf("native counterparties: %v", err)
	}
	if _, err := q.ActionFlowSummary(ctx, "solana", address, "USDC", "", nil, nil); err != nil {
		t.Fatalf("SPL flow: %v", err)
	}
	if _, err := q.ActionCounterparties(ctx, ActionFilter{Chain: "solana", Address: address, Token: "USDC"}, "", 0, 5, "total_amount"); err != nil {
		t.Fatalf("SPL counterparties: %v", err)
	}
	if _, err := q.ActionEdges(ctx, "solana", []string{address}, "SOL", "", "", nil, nil, 5); err != nil {
		t.Fatalf("native path edges: %v", err)
	}
	if first, err := q.ActionFirstFunding(ctx, "solana", address); err != nil {
		t.Fatalf("first funding exact amount: %v", err)
	} else if native, ok := first["first_native_funding"].(map[string]any); !ok || native["raw_value"] != "4970008000" || native["amount"] != "4.970008" {
		t.Fatalf("first funding lost lamport precision: %+v", first)
	}
	if page, err := q.ActionRows(ctx, "transactions", ActionFilter{Chain: "solana", Address: address, Page: 1, PageSize: 5}); err != nil {
		t.Fatalf("signature activity rows: %v", err)
	} else if len(page.Items) > 0 {
		t.Logf("stored_signature=%v stored_slot=%v", page.Items[0]["tx_hash"], page.Items[0]["block_number"])
	}
	if page, err := q.ActionRows(ctx, "internal_transactions", ActionFilter{Chain: "solana", Address: address, MinAmount: "4.9", MaxAmount: "5.0", Page: 1, PageSize: 5}); err != nil || len(page.Items) != 1 {
		t.Fatalf("exact SOL movement amount filter: items=%d err=%v", len(page.Items), err)
	}
	if _, err := q.ActionRows(ctx, "transactions", ActionFilter{Chain: "solana", Address: address, MinAmount: "1"}); err == nil {
		t.Fatal("a Solana signature has no single amount")
	}
}
