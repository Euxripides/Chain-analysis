package ingest

import (
	"encoding/json"
	"testing"
)

func TestPickAddressForChainKeepsBase58Case(t *testing.T) {
	const from = "FromPubkeyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	const to = "ToPubkeyBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	raw := json.RawMessage(`{"blockNumber":"100","hash":"SIG","from":"` + from + `","to":"` + to + `","value":"95000","timestamp":1700000000,"status":"0x1"}`)

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
