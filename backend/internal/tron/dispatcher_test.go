package tron

import (
	"context"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func TestDatasetRouterRoutesOnlyTransactionsToTronGrid(t *testing.T) {
	ankr := &fakeProvider{name: "ankr"}
	grid := &fakeProvider{name: "trongrid"}
	router := &DatasetRouter{Default: ankr, Transactions: grid, TRC20: ankr, Internal: ankr, ReceiptProvider: ankr, TxDetailProvider: ankr}
	for _, dataset := range []string{"transactions", "token_transfers", "internal_transactions"} {
		if _, err := router.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: dataset}); err != nil {
			t.Fatalf("dataset %s: %v", dataset, err)
		}
	}
	if grid.calls != 1 || ankr.calls != 2 {
		t.Fatalf("unexpected calls: ankr=%d trongrid=%d", ankr.calls, grid.calls)
	}
	if _, err := router.TxDetail(context.Background(), "tron", "tx", true, true); err != nil {
		t.Fatal(err)
	}
	if ankr.detailCalls != 1 || grid.detailCalls != 0 {
		t.Fatalf("unexpected detail calls: ankr=%d trongrid=%d", ankr.detailCalls, grid.detailCalls)
	}
	if _, err := router.TxReceipt(context.Background(), "tron", "tx"); err != nil {
		t.Fatal(err)
	}
	if ankr.receiptCalls != 1 || grid.receiptCalls != 0 {
		t.Fatalf("unexpected receipt calls: ankr=%d trongrid=%d", ankr.receiptCalls, grid.receiptCalls)
	}
	if got := router.providerForCapability("receipt"); got == nil || got.Name() != "ankr" {
		t.Fatalf("receipt provider=%v", got)
	}
}

func TestDatasetRouterFallsBackToDefault(t *testing.T) {
	defaultProvider := &fakeProvider{name: "ankr"}
	router := &DatasetRouter{Default: defaultProvider}
	if _, err := router.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions"}); err != nil {
		t.Fatal(err)
	}
	if defaultProvider.calls != 1 {
		t.Fatalf("default provider calls=%d", defaultProvider.calls)
	}
}
