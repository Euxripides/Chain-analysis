package dataset

import "testing"

// 链级 Dataset 白名单是唯一事实来源（model.ChainDatasets），下载入参校验必须读它：
// 否则 Solana 的 CPI 调用树（ADR-023 D11）会在预览阶段被误拒。
func TestValidateChainDatasetSupportUsesChainWhitelist(t *testing.T) {
	allowed := []struct {
		chain   string
		dataset string
	}{
		{"solana", "internal_transactions"},
		{"solana", "token_transfers"},
		{"tron", "internal_transactions"},
		{"bsc", "nft_transfers"},
	}
	for _, item := range allowed {
		if err := validateChainDatasetSupport(item.chain, []string{item.dataset}); err != nil {
			t.Fatalf("%s/%s must be allowed: %v", item.chain, item.dataset, err)
		}
	}
	rejected := []struct {
		chain   string
		dataset string
	}{
		{"bsc", "internal_transactions"},
		{"eth", "internal_transactions"},
		{"polygon", "internal_transactions"},
		{"solana", "nft_transfers"},
		{"solana", "logs"},
		{"tron", "balances"},
	}
	for _, item := range rejected {
		if err := validateChainDatasetSupport(item.chain, []string{item.dataset}); err == nil {
			t.Fatalf("%s/%s must be rejected", item.chain, item.dataset)
		}
	}
}
