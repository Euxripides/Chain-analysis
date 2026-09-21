package solana

import (
	"encoding/json"
	"testing"
)

func TestNormalizeNftTransfer(t *testing.T) {
	raw := json.RawMessage(`{"blockNumber":"448700000","transactionHash":"SIG","logIndex":768,"contractAddress":"NftMint111111111111111111111111111111111111","tokenId":"","fromAddress":"FromWallet1111111111111111111111111111111","toAddress":"ToWallet111111111111111111111111111111111","value":"1","timestamp":1789875226,"instructionIndex":7,"innerInstructionIndex":2,"sourceApi":"data_api","sourceRequestId":"req-nft"}`)
	row, err := NormalizeNftTransfer(raw, "drpc")
	if err != nil {
		t.Fatalf("normalize nft transfer: %v", err)
	}
	if row.Chain != "solana" || row.Provider != "drpc" || row.TxHash != "SIG" || row.BlockNumber != 448700000 {
		t.Fatalf("unexpected identity: %+v", row)
	}
	// 指令定位与来源证据随行透传（规范 §2.2），log_index 不被当成 EVM log index。
	if row.InstructionIndex != 7 || row.InnerInstructionIndex != 2 {
		t.Fatalf("instruction position must be preserved: %+v", row)
	}
	if row.SourceAPI != "data_api" || row.SourceRequestID != "req-nft" {
		t.Fatalf("source evidence must be preserved: %+v", row)
	}
	if row.Contract != "NftMint111111111111111111111111111111111111" {
		t.Fatalf("mint must stay the contract: %+v", row)
	}
	// Solana 没有 tokenId：必须留空，绝不把 mint 塞进来（ADR-024 D3）。
	if row.TokenID != "" {
		t.Fatalf("solana NFT rows must leave tokenId empty, got %q", row.TokenID)
	}
	if row.TokenName != "" || row.TokenSymbol != "" {
		t.Fatalf("metadata must stay empty: %+v", row)
	}
	if row.From != "FromWallet1111111111111111111111111111111" || row.To != "ToWallet111111111111111111111111111111111" {
		t.Fatalf("solana addresses must keep their case: %+v", row)
	}
}

func TestNormalizeNftTransferRejectsMalformedRows(t *testing.T) {
	cases := []string{
		`{"blockNumber":"1","contractAddress":"M","value":"1"}`,
		`{"blockNumber":"1","transactionHash":"SIG","value":"1"}`,
		`{"blockNumber":"x","transactionHash":"SIG","contractAddress":"M","value":"1"}`,
	}
	for _, raw := range cases {
		if _, err := NormalizeNftTransfer(json.RawMessage(raw), "drpc"); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}

// 缺 innerInstructionIndex 按 -1（非内层指令）落库，不能伪装成第 0 条内层指令；
// 空来源回退 "solana"（规范 §0/§2.2）。
func TestNormalizeNftTransferDefaultsInnerIndexAndProvider(t *testing.T) {
	raw := json.RawMessage(`{"blockNumber":"1","transactionHash":"SIG","contractAddress":"Mint","fromAddress":"A","toAddress":"B","value":"1","timestamp":1}`)
	row, err := NormalizeNftTransfer(raw, "")
	if err != nil {
		t.Fatalf("normalize nft transfer: %v", err)
	}
	if row.InnerInstructionIndex != -1 || row.InstructionIndex != 0 {
		t.Fatalf("missing inner index must stay -1: %+v", row)
	}
	if row.Provider != "solana" {
		t.Fatalf("empty provider must fall back to solana, got %q", row.Provider)
	}
}
