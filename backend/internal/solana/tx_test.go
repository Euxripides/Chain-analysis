package solana

import (
	"encoding/json"
	"testing"
)

const solanaTxRow = `{"blockNumber":"310000000","blockHash":"","hash":"5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tjF3ZpRzrFmBV6UjKdiSZkQUW","timestamp":1789875226,"from":"5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9","to":"CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs","value":"95000","nonce":"0","gasLimit":"0","gasUsed":"0","gasPrice":"0","type":"transfer","status":"1","input":"","feeRaw":"5000","signers":["SignerAAAhbXZxdw7gTjjD1mBwuoFbhUvuAi9Signature","SignerBBBBhbXZxdw7gTjjD1mBwuoFbhUvuAi9Signat"],"accountKeys":["5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9","CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs"],"programIds":["11111111111111111111111111111111"],"sourceApi":"data_api","sourceMethod":"get_transactions_history","sourceRequestId":"req-9","chainPayloadJson":{"memo":"hello"}}`

func TestNormalizeSolanaTransactionCarriesExtensions(t *testing.T) {
	row, err := NormalizeSolanaTransaction(json.RawMessage(solanaTxRow), "drpc")
	if err != nil {
		t.Fatalf("normalize solana transaction: %v", err)
	}
	if row.Chain != "solana" || row.Provider != "drpc" {
		t.Fatalf("unexpected provenance: %+v", row)
	}
	// base58 大小写必须原样保留（折叠等于换一个地址）。
	if row.From != "5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9" || row.To != "CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs" {
		t.Fatalf("base58 addresses must keep their case: %+v", row)
	}
	if row.BlockNumber != 310000000 || row.Status != 1 || row.ValueRaw != "95000" {
		t.Fatalf("unexpected identity: %+v", row)
	}
	// dRPC 证据列（规范 §2.2/§3）逐列断言。
	if row.FeeRaw != "5000" {
		t.Fatalf("fee_raw must be carried: %+v", row)
	}
	if len(row.Signers) != 2 || row.Signers[0] != "SignerAAAhbXZxdw7gTjjD1mBwuoFbhUvuAi9Signature" {
		t.Fatalf("signers must be carried in order: %+v", row.Signers)
	}
	if len(row.AccountKeys) != 2 || row.AccountKeys[1] != "CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs" {
		t.Fatalf("account_keys must keep their case: %+v", row.AccountKeys)
	}
	if len(row.ProgramIds) != 1 || row.ProgramIds[0] != "11111111111111111111111111111111" {
		t.Fatalf("program_ids must be carried: %+v", row.ProgramIds)
	}
	if row.SourceAPI != "data_api" || row.SourceMethod != "get_transactions_history" || row.SourceRequestID != "req-9" {
		t.Fatalf("source evidence must be carried: %+v", row)
	}
	if row.ChainPayloadJSON != `{"memo":"hello"}` {
		t.Fatalf("chain payload must be carried: %q", row.ChainPayloadJSON)
	}
	// Solana 语义字段：原生单位、无"确认数"概念。
	if row.NativeSymbol != "SOL" || row.Confirmation != "finalized" {
		t.Fatalf("unexpected solana semantics: %+v", row)
	}
}

// 缺失字段一律零值：value 不能变成 "0"，gas/nonce/input 不能凭空出现 EVM 语义。
func TestNormalizeSolanaTransactionDoesNotInventValues(t *testing.T) {
	raw := json.RawMessage(`{"hash":"SIG","from":"A","to":"B","timestamp":1789875226}`)
	row, err := NormalizeSolanaTransaction(raw, "")
	if err != nil {
		t.Fatalf("normalize solana transaction: %v", err)
	}
	if row.ValueRaw != "" {
		t.Fatalf("missing value must stay empty, got %q", row.ValueRaw)
	}
	if row.Nonce != 0 || row.Gas != 0 || row.GasUsed != 0 || row.GasPrice != "" || row.Input != "" {
		t.Fatalf("EVM fields must stay zero/empty: %+v", row)
	}
	if row.FeeRaw != "" || row.Signers != nil || row.AccountKeys != nil || row.ProgramIds != nil ||
		row.SourceAPI != "" || row.SourceMethod != "" || row.SourceRequestID != "" || row.ChainPayloadJSON != "" {
		t.Fatalf("missing evidence must stay empty: %+v", row)
	}
	if row.NativeSymbol != "SOL" || row.Confirmation != "finalized" {
		t.Fatalf("solana semantics must still apply: %+v", row)
	}
	if row.Provider != "solana" {
		t.Fatalf("empty provider must fall back to solana, got %q", row.Provider)
	}
}

// RPC 风格行：blockNumber 缺失时用 slot，commitment 覆盖默认 confirmation。
func TestNormalizeSolanaTransactionReadsSlotAndCommitment(t *testing.T) {
	raw := json.RawMessage(`{"slot":"310000123","hash":"SIG","from":"A","to":"B","timestamp":1,"commitment":"confirmed","confirmation":""}`)
	row, err := NormalizeSolanaTransaction(raw, "drpc")
	if err != nil {
		t.Fatalf("normalize solana transaction: %v", err)
	}
	if row.BlockNumber != 310000123 {
		t.Fatalf("slot must be used when blockNumber is absent: %+v", row)
	}
	if row.Confirmation != "confirmed" {
		t.Fatalf("commitment must override the default: %+v", row)
	}
}

// Provider 万一回传 EVM 字段，也不能被下游当成 gas/nonce 用（规范 §8.2）。
func TestNormalizeSolanaTransactionDropsEVMGasSemantics(t *testing.T) {
	raw := json.RawMessage(`{"hash":"SIG","from":"A","to":"B","timestamp":1,"nonce":"7","gasLimit":"21000","gasUsed":"21000","gasPrice":"0x5"}`)
	row, err := NormalizeSolanaTransaction(raw, "drpc")
	if err != nil {
		t.Fatalf("normalize solana transaction: %v", err)
	}
	if row.Nonce != 0 || row.Gas != 0 || row.GasUsed != 0 || row.GasPrice != "" {
		t.Fatalf("EVM gas fields must be zeroed for solana: %+v", row)
	}
}
