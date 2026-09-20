package api

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestChainDatasetsIsPerChain(t *testing.T) {
	// Solana 暴露且只暴露已实现的数据集（ADR-023 D9）：transactions / token_transfers / CPI 树。
	solana := model.ChainDatasets("solana")
	wantSolana := map[string]bool{"transactions": true, "token_transfers": true, "internal_transactions": true, "logs": true, "balances": true}
	if len(solana) != len(wantSolana) {
		t.Fatalf("unexpected solana datasets: %v", solana)
	}
	for _, dataset := range solana {
		if !wantSolana[dataset] {
			t.Fatalf("solana advertises unimplemented dataset %s", dataset)
		}
	}

	tron := model.ChainDatasets("tron")
	if len(tron) != 3 {
		t.Fatalf("tron dataset list changed unexpectedly: %v", tron)
	}
	for _, dataset := range tron {
		if dataset == "nft_transfers" || dataset == "logs" || dataset == "balances" {
			t.Fatalf("tron must not advertise %s", dataset)
		}
	}
	for _, dataset := range model.ChainDatasets("bsc") {
		if dataset == "internal_transactions" {
			t.Fatal("EVM chains must not advertise internal_transactions (Ankr Advanced API has no such method)")
		}
	}
	// 空链保持向后兼容：返回全量。
	if len(model.ChainDatasets("")) != len(model.AllDatasets) {
		t.Fatalf("empty chain must keep the legacy full list, got %v", model.ChainDatasets(""))
	}
}
