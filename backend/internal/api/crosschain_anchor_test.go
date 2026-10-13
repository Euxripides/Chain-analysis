package api

import (
	"context"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
)

type anchorReadFixture struct {
	receipt *chainread.Receipt
	block   *chainread.Block
	calls   int
}

func (r *anchorReadFixture) Receipt(context.Context, string, chain.Hash, chainread.Options) (*chainread.Receipt, error) {
	r.calls++
	return r.receipt, nil
}
func (r *anchorReadFixture) Block(context.Context, string, chain.BlockRef, bool, chainread.Options) (*chainread.Block, error) {
	r.calls++
	return r.block, nil
}
func TestBridgeAnchorTimeRequiresExactReceiptAndBlock(t *testing.T) {
	hash := chain.Hash("0x1111111111111111111111111111111111111111111111111111111111111111")
	height, status := uint64(100), uint64(1)
	a := crosschain.Anchor{Chain: "bsc", TxHash: hash.String(), Dataset: "internal_transactions"}
	blockHash := "0x2222222222222222222222222222222222222222222222222222222222222222"
	r := &anchorReadFixture{receipt: &chainread.Receipt{TxHash: hash, Status: &status, BlockNumber: &height, BlockHash: blockHash}, block: &chainread.Block{Number: height, Hash: blockHash, Timestamp: 1779035253}}
	rows := []crosschain.Movement{{Timestamp: time.Unix(0, 0), Status: "unknown"}}
	enrichBridgeAnchorTime(context.Background(), r, a, rows)
	if r.calls != 2 || rows[0].Timestamp.Unix() != 1779035253 || rows[0].Status != "success" || rows[0].TimestampSource != "chain_rpc_receipt_and_block" {
		t.Fatal(r, rows)
	}
	r.calls = 0
	enrichBridgeAnchorTime(context.Background(), r, a, rows)
	if r.calls != 0 {
		t.Fatal("valid indexed time triggered redundant reads")
	}
	rows[0].Timestamp = time.Unix(0, 0)
	r.block.Hash = "different"
	enrichBridgeAnchorTime(context.Background(), r, a, rows)
	if rows[0].Timestamp.Unix() != 0 {
		t.Fatal("mismatched block supplied a time")
	}
	status = 0
	enrichBridgeAnchorTime(context.Background(), r, a, rows)
	if rows[0].Status != "failed" {
		t.Fatal("failed parent accepted")
	}
}
