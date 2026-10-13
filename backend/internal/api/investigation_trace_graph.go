package api

import (
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"math/big"
	"strings"
)

type tracePosition struct{ Block, Index uint64 }

// Deduplication only controls retained evidence and record budget. An existing
// transfer must still be reconsidered when an earlier arrival opens its path.
func retainTraceTransfer(ledger map[string]query.LedgerTransfer, tx query.LedgerTransfer, limit int) bool {
	key := fmt.Sprintf("%s:%d", tx.Hash, tx.Index)
	if _, exists := ledger[key]; exists {
		return true
	}
	if len(ledger) >= limit {
		return false
	}
	ledger[key] = tx
	return true
}
func (p tracePosition) before(other tracePosition) bool {
	return p.Block < other.Block || p.Block == other.Block && p.Index < other.Index
}

// Reachability is only a candidate for accounting. Pre-arrival transactions
// remain in the ledger but cannot open downstream branches for the source lot.
func extendTraceFrontier(tx query.LedgerTransfer, front, seen map[string]bool, arrivals map[string]tracePosition, maxNodes int, allow bool) (bool, bool) {
	if len(tx.Raw) > 78 {
		return false, false
	}
	raw, ok := new(big.Int).SetString(tx.Raw, 10)
	arrival, arrived := arrivals[tx.From]
	pos := tracePosition{Block: tx.Block, Index: tx.Index}
	if !ok || raw.Sign() <= 0 || raw.BitLen() > 256 || tx.From == tx.To || !front[tx.From] || !arrived || !arrival.before(pos) || tx.To == "0x0000000000000000000000000000000000000000" {
		return false, false
	}
	if previous, ok := arrivals[tx.To]; ok && !pos.before(previous) {
		return false, false
	}
	if !allow || !seen[tx.To] && len(seen) >= maxNodes {
		return false, true
	}
	seen[tx.To] = true
	arrivals[tx.To] = pos
	return true, false
}
func traceModels(requested string) []string {
	if requested == "both" || requested == "" {
		return []string{"proportional", "fifo"}
	}
	return []string{requested}
}

func validateTraceSource(receipt *chainread.Receipt, block *chainread.Block, expected chain.Hash) error {
	if receipt == nil || block == nil || receipt.BlockNumber == nil || receipt.TxHash != expected || block.Number != *receipt.BlockNumber || receipt.BlockHash != "" && !strings.EqualFold(receipt.BlockHash, block.Hash) {
		return summaryError(503, "SOURCE_STATE_CHANGED", "Receipt and source block are inconsistent; retry against a stable chain state")
	}
	return nil
}
