package ingest

import (
	"encoding/json"
	"testing"
)

func TestPickAddressForChainKeepsBase58Case(t *testing.T) {
	const from = "FromPubkeyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	const to = "ToPubkeyBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	raw := json.RawMessage(`{"blockNumber":"100","hash":"SIG","from":"FromPubkeyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","to":"ToPubkeyBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB","value":"95000","timestamp":1700000000,"status":"0x1"}`)

	row, err := NormalizeTransaction(raw, "solana", "solana")
	if err != nil {
		t.Fatalf("normalize solana transaction: %v", err)
	}
	if row.From != from || row.To != to {
		t.Fatalf("solana base58 addresses must not be folded: from=%s to=%s", row.From, row.To)
	}
	if row.Status != 1 || row.BlockNumber != 100 {
		t.Fatalf("unexpected row: %+v", row)
	}

	evmRaw := json.RawMessage(`{"hash":"0xabc","from":"0xABCDEF0123456789ABCDEF0123456789ABCDEF01","to":"0xABCDEF0123456789ABCDEF0123456789ABCDEF02","value":"1","blockNumber":"7"}`)
	evmRow, err := NormalizeTransaction(evmRaw, "bsc", "ankr")
	if err != nil {
		t.Fatalf("normalize evm transaction: %v", err)
	}
	if evmRow.From != "0xabcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("evm addresses must still be lowercased, got %s", evmRow.From)
	}
}

// §2.2/§3 行契约：Solana dRPC 证据列必须逐列读出，且数组元素不得折叠大小写。
func TestNormalizeTransactionReadsSolanaExtensions(t *testing.T) {
	raw := json.RawMessage(`{"blockNumber":"310000000","hash":"5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tjF3ZpRzrFmBV6UjKdiSZkQUW","from":"5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9","to":"CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs","timestamp":1789875226,"status":"1","value":"","feeRaw":"5000","signers":["SignerAAAhbXZxdw7gTjjD1mBwuoFbhUvuAi9Signature","SignerBBBBhbXZxdw7gTjjD1mBwuoFbhUvuAi9Signat"],"accountKeys":["5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9","CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs"],"programIds":["11111111111111111111111111111111"],"sourceApi":"data_api","sourceMethod":"get_transactions_history","sourceRequestId":"req-1","chainPayloadJson":{"memo":"x"}}`)
	row, err := NormalizeTransaction(raw, "solana", "drpc")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if row.FeeRaw != "5000" {
		t.Fatalf("fee_raw = %q", row.FeeRaw)
	}
	if len(row.Signers) != 2 || row.Signers[1] != "SignerBBBBhbXZxdw7gTjjD1mBwuoFbhUvuAi9Signat" {
		t.Fatalf("signers = %v", row.Signers)
	}
	if len(row.AccountKeys) != 2 || row.AccountKeys[0] != "5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9" {
		t.Fatalf("account keys must keep their case: %v", row.AccountKeys)
	}
	if len(row.ProgramIds) != 1 || row.ProgramIds[0] != "11111111111111111111111111111111" {
		t.Fatalf("program ids = %v", row.ProgramIds)
	}
	if row.SourceAPI != "data_api" || row.SourceMethod != "get_transactions_history" || row.SourceRequestID != "req-1" {
		t.Fatalf("source evidence = %q/%q/%q", row.SourceAPI, row.SourceMethod, row.SourceRequestID)
	}
	// 对象形式的 chainPayloadJson 必须序列化保存，不能丢。
	if row.ChainPayloadJSON != `{"memo":"x"}` {
		t.Fatalf("chain payload = %q", row.ChainPayloadJSON)
	}
}

// EVM/TRON 行没有这些键：全部保持零值，不能被默认成任何"证据"。
func TestNormalizeTransactionLeavesSolanaExtensionsEmptyForEVM(t *testing.T) {
	raw := json.RawMessage(`{"blockHeight":"1","transactionHash":"0x1","fromAddress":"0xa","toAddress":"0xb","value":"0x1"}`)
	row, err := NormalizeTransaction(raw, "bsc", "ankr")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if row.FeeRaw != "" || row.Signers != nil || row.AccountKeys != nil || row.ProgramIds != nil ||
		row.SourceAPI != "" || row.SourceMethod != "" || row.SourceRequestID != "" || row.ChainPayloadJSON != "" {
		t.Fatalf("evm rows must leave solana columns empty: %+v", row)
	}
}
