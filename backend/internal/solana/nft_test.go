package solana

import (
	"encoding/json"
	"testing"
)

func TestNormalizeNftTransfer(t *testing.T) {
	raw := json.RawMessage(`{"blockNumber":"448700000","transactionHash":"SIG","logIndex":768,"contractAddress":"NftMint111111111111111111111111111111111111","tokenId":"","fromAddress":"FromWallet1111111111111111111111111111111","toAddress":"ToWallet111111111111111111111111111111111","value":"1","timestamp":1789875226}`)
	row, err := NormalizeNftTransfer(raw)
	if err != nil {
		t.Fatalf("normalize nft transfer: %v", err)
	}
	if row.Chain != "solana" || row.Provider != "solana" || row.TxHash != "SIG" || row.BlockNumber != 448700000 {
		t.Fatalf("unexpected identity: %+v", row)
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
		if _, err := NormalizeNftTransfer(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}
