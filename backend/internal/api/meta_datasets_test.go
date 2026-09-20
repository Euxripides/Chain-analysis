package api

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestChainDatasetsIsPerChain(t *testing.T) {
	solana := chainDatasets("solana")
	if len(solana) != 1 || solana[0] != "transactions" {
		t.Fatalf("solana must only expose transactions, got %v", solana)
	}
	tron := chainDatasets("tron")
	if len(tron) != 3 {
		t.Fatalf("tron dataset list changed unexpectedly: %v", tron)
	}
	for _, dataset := range tron {
		if dataset == "nft_transfers" || dataset == "logs" || dataset == "balances" {
			t.Fatalf("tron must not advertise %s", dataset)
		}
	}
	for _, dataset := range chainDatasets("bsc") {
		if dataset == "internal_transactions" {
			t.Fatal("EVM chains must not advertise internal_transactions (Ankr Advanced API has no such method)")
		}
	}
	// 空链保持向后兼容：返回全量。
	if len(chainDatasets("")) != len(model.AllDatasets) {
		t.Fatalf("empty chain must keep the legacy full list, got %v", chainDatasets(""))
	}
}
