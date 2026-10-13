package api

import (
	"encoding/json"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"strings"
	"testing"
)

func TestAnkrMainnetDownloadContracts(t *testing.T) {
	registry := chain.DefaultRegistry()
	if len(chain.AnkrAdvancedMainnets()) != 17 {
		t.Fatal("unexpected official mainnet count")
	}
	for _, slug := range chain.AnkrAdvancedMainnets() {
		t.Run(slug, func(t *testing.T) {
			cfg, err := registry.Require(slug)
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.IsEVM() || cfg.ChainID == 0 || cfg.Native.Decimals != 18 || cfg.DefaultProvider != "ankr" {
				t.Fatalf("invalid registry: %+v", cfg)
			}
			for _, fn := range []func(string) (string, error){canonicalActionChain, canonicalActionDownloadChain} {
				if got, err := fn(" " + strings.ToUpper(slug) + " "); err != nil || got != slug {
					t.Fatalf("Actions rejects %s: %s %v", slug, got, err)
				}
			}
			caps := model.ChainDatasetCapabilities(slug)
			available := 0
			for _, cap := range caps {
				if cap.Status == model.DatasetAvailable {
					available++
					if cap.ID == "internal_transactions" {
						t.Fatal("nonexistent internal transaction method exposed")
					}
					if cap.Provider != "ankr" || cap.APIChannel != "ankr_advanced" {
						t.Fatalf("wrong route: %+v", cap)
					}
				}
			}
			if available != 5 {
				t.Fatalf("wrong dataset count: %d", available)
			}
			if nativeSymbol(slug) != cfg.Native.Symbol {
				t.Fatalf("native symbol mismatch")
			}
		})
	}
	for _, slug := range []string{"not-a-chain", "stellar", "solana_testnet", "eth_sepolia", "story_aeneid_testnet", "somnia_testnet"} {
		if _, err := canonicalActionDownloadChain(slug); err == nil {
			t.Fatalf("out-of-scope chain accepted: %s", slug)
		}
		for _, ds := range model.ChainDatasetCapabilities(slug) {
			if ds.Status == model.DatasetAvailable {
				t.Fatalf("unknown chain %s advertises %s", slug, ds.ID)
			}
		}
	}
	if !model.ChainSupportsDataset("tron", "internal_transactions") || !model.ChainSupportsDataset("solana", "token_transfers") {
		t.Fatal("existing non-EVM support lost")
	}
}

func TestAnkrMainnetsInActionsOpenAPI(t *testing.T) {
	encoded, err := json.Marshal(actionParameters("getTransactions"))
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range chain.AnkrAdvancedMainnets() {
		if !strings.Contains(string(encoded), `"`+slug+`"`) {
			t.Fatalf("schema omits %s", slug)
		}
	}
}
