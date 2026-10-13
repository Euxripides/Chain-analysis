package api

import (
	"context"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

type bridgeAnchorReader interface {
	Receipt(context.Context, string, chain.Hash, chainread.Options) (*chainread.Receipt, error)
	Block(context.Context, string, chain.BlockRef, bool, chainread.Options) (*chainread.Block, error)
}

// A missing event time is filled in memory from its exact successful receipt
// and matching block. This performs two bounded reads and never repairs stored
// datasets or substitutes a protocol-estimated time for chain block time.
func enrichBridgeAnchorTime(ctx context.Context, reader bridgeAnchorReader, a crosschain.Anchor, rows []crosschain.Movement) {
	if a.Chain == "solana" || a.Chain == "tron" || len(rows) == 0 {
		return
	}
	missing := false
	for _, row := range rows {
		if row.Timestamp.Unix() <= 0 {
			missing = true
		}
	}
	if !missing {
		return
	}
	hash, e := chain.ParseHash(a.TxHash)
	if e != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	options := chainread.Options{Purpose: rpc.PurposeReceipt, Priority: rpc.PriorityInteractive}
	receipt, e := reader.Receipt(ctx, a.Chain, hash, options)
	if e != nil || receipt == nil || receipt.Status == nil || receipt.BlockNumber == nil || receipt.BlockHash == "" || receipt.TxHash != hash {
		return
	}
	if _, e = chain.ParseHash(receipt.BlockHash); e != nil {
		return
	}
	if *receipt.Status != 1 {
		for i := range rows {
			rows[i].Status = "failed"
		}
		return
	}
	block, e := reader.Block(ctx, a.Chain, chain.BlockRef{Number: receipt.BlockNumber}, false, options)
	if e != nil || block == nil || block.Number != *receipt.BlockNumber || !strings.EqualFold(block.Hash, receipt.BlockHash) || block.Timestamp == 0 || block.Timestamp > uint64(time.Now().Add(time.Hour).Unix()) {
		return
	}
	for i := range rows {
		if rows[i].Timestamp.Unix() <= 0 {
			rows[i].Timestamp = time.Unix(int64(block.Timestamp), 0).UTC()
			rows[i].TimestampSource = "chain_rpc_receipt_and_block"
			if rows[i].Status != "failed" {
				rows[i].Status = "success"
			}
		}
	}
}
