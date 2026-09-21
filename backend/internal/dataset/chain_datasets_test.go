package dataset

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// 链级 Dataset 白名单是唯一事实来源（model.ChainDatasets），下载入参校验必须读它：
// 否则 Solana 的 CPI 调用树（ADR-023 D11）会在预览阶段被误拒。
//
// Solana 的白名单随实际取数通道变化（开发文档 §5.2）：
//   - 旧 JSON-RPC 路径：nft_transfers 靠 decimals=0 猜测，按 ADR-023 D9 排除；
//   - dRPC Data API 路径：transfer.token.nft 是明确对象，nft_transfers 可用。
//
// 因此这里显式覆盖两种模式，而不是把某一时刻的开关状态写死进断言。
func TestValidateChainDatasetSupportUsesChainWhitelist(t *testing.T) {
	defer model.SetSolanaDataAPI(false)

	allowed := []struct {
		chain   string
		dataset string
	}{
		{"solana", "internal_transactions"},
		{"solana", "token_transfers"},
		{"solana", "logs"},
		{"solana", "balances"},
		{"tron", "internal_transactions"},
		{"bsc", "nft_transfers"},
	}
	rejected := []struct {
		chain   string
		dataset string
	}{
		{"bsc", "internal_transactions"},
		{"eth", "internal_transactions"},
		{"polygon", "internal_transactions"},
		{"tron", "balances"},
	}

	for _, mode := range []bool{false, true} {
		model.SetSolanaDataAPI(mode)
		for _, item := range allowed {
			if err := validateChainDatasetSupport(item.chain, []string{item.dataset}); err != nil {
				t.Fatalf("solana_data_api=%v: %s/%s must be allowed: %v", mode, item.chain, item.dataset, err)
			}
		}
		for _, item := range rejected {
			if err := validateChainDatasetSupport(item.chain, []string{item.dataset}); err == nil {
				t.Fatalf("solana_data_api=%v: %s/%s must be rejected", mode, item.chain, item.dataset)
			}
		}
		// nft_transfers 只在 Data API 通道下可用。
		err := validateChainDatasetSupport("solana", []string{"nft_transfers"})
		if mode && err != nil {
			t.Fatalf("solana_data_api=true: solana/nft_transfers must be allowed: %v", err)
		}
		if !mode && err == nil {
			t.Fatal("solana_data_api=false: solana/nft_transfers must be rejected (ADR-023 D9)")
		}
	}
}
