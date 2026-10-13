// Package fundtrace attributes a fungible token lot under explicit accounting
// assumptions. Its results are hypotheses over evidence, not token identity.
package fundtrace

import (
	"context"
	"errors"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/shopspring/decimal"
	"math/big"
	"math/bits"
	"sort"
	"time"
)

// ErrAllocationBudget distinguishes a bounded investigation from a complete
// attribution. Callers must not return a budget-limited Result as complete.
var ErrAllocationBudget = errors.New("fund allocation budget exceeded")

const (
	MaxAllocationWork         = 2000000
	MaxAllocationLots         = 50000
	MaxAllocationConsumptions = 100000
	MaxAllocationTransfers    = 20001 // API ledger budget plus its confirmed seed.
)

type AllocationBudgetError struct {
	Dimension string
	Used      int64
	Limit     int64
}

func (e *AllocationBudgetError) Error() string {
	return fmt.Sprintf("%s: %s %d exceeds %d; narrow the trace window or record budget", ErrAllocationBudget, e.Dimension, e.Used, e.Limit)
}
func (e *AllocationBudgetError) Unwrap() error { return ErrAllocationBudget }

type allocationBudget struct {
	ctx          context.Context
	work         int64
	consumptions int64
}

func (b *allocationBudget) charge(work int64) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if work > MaxAllocationWork-b.work {
		return &AllocationBudgetError{Dimension: "work", Used: b.work + work, Limit: MaxAllocationWork}
	}
	b.work += work
	return nil
}
func (b *allocationBudget) lots(n int) error {
	if n > MaxAllocationLots {
		return &AllocationBudgetError{Dimension: "active_lots", Used: int64(n), Limit: MaxAllocationLots}
	}
	return b.ctx.Err()
}
func (b *allocationBudget) consumption() error {
	if b.consumptions >= MaxAllocationConsumptions {
		return &AllocationBudgetError{Dimension: "consumptions", Used: b.consumptions + 1, Limit: MaxAllocationConsumptions}
	}
	b.consumptions++
	return b.ctx.Err()
}

// Only untagged lots have no source-batch audit identity. Consolidating adjacent
// untagged fragments preserves their position relative to every tagged FIFO
// lot. Tagged fragments, including their IDs and originals, are never merged.
func appendCompact(lots []Lot, next Lot) []Lot {
	if len(lots) > 0 && !next.Tagged && !lots[len(lots)-1].Tagged {
		last := &lots[len(lots)-1]
		merged := new(big.Int).Add(last.Amount, next.Amount)
		last.Amount = merged
		last.Original = clone(merged)
		last.Hops = 0
		return lots
	}
	return append(lots, next)
}

type Lot struct {
	Amount   *big.Int
	Tagged   bool
	Hops     int
	ID       string
	Original *big.Int
}
type Consumption struct {
	LotID               string `json:"lot_id"`
	SourceBatchID       string `json:"source_batch_id"`
	OriginalRaw         string `json:"batch_original_amount_raw"`
	FragmentOriginalRaw string `json:"fragment_original_amount_raw"`
	BeforeRaw           string `json:"batch_remaining_before_raw"`
	ConsumedRaw         string `json:"consumed_amount_raw"`
	AfterRaw            string `json:"batch_remaining_after_raw"`
	OutgoingHash        string `json:"outgoing_tx_hash"`
	Original            string `json:"batch_original_amount"`
	Before              string `json:"batch_remaining_before"`
	Consumed            string `json:"consumed_amount"`
	After               string `json:"batch_remaining_after"`
}
type Edge struct {
	Hash             string               `json:"tx_hash"`
	Block            uint64               `json:"block_number"`
	Index            uint64               `json:"log_index"`
	Time             time.Time            `json:"timestamp"`
	Token            string               `json:"token_contract"`
	GapRaw           string               `json:"balance_gap_injected_raw"`
	Gap              string               `json:"balance_gap_injected"`
	Transfer         query.LedgerTransfer `json:"transfer"`
	AttributedRaw    string               `json:"attributed_amount_raw"`
	Hops             int                  `json:"hops"`
	From             string               `json:"from"`
	To               string               `json:"to"`
	ActualRaw        string               `json:"actual_transfer_amount_raw"`
	ActualAmount     string               `json:"actual_transfer_amount"`
	AttributedAmount string               `json:"attributed_amount"`
	Model            string               `json:"attribution_model"`
	Type             string               `json:"attribution_type"`
	BatchID          string               `json:"source_batch_id"`
	SourceBeforeRaw  string               `json:"source_balance_before_transfer_raw"`
	SourceBefore     string               `json:"source_balance_before_transfer"`
	TotalBeforeRaw   string               `json:"total_balance_before_transfer_raw"`
	TotalBefore      string               `json:"total_balance_before_transfer"`
	Share            string               `json:"source_share_before_transfer"`
	ShareFraction    string               `json:"source_share_fraction"`
	Confidence       *float64             `json:"confidence"`
	ConfidenceBasis  string               `json:"confidence_basis"`
	BalanceBasis     string               `json:"balance_basis"`
	Consumptions     []Consumption        `json:"batch_consumptions"`
}
type Result struct {
	Model        string            `json:"model"`
	Edges        []Edge            `json:"edges"`
	Remaining    map[string]string `json:"remaining_attributed_raw"`
	FrontierRaw  string            `json:"unexpanded_frontier_raw"`
	SeedRaw      string            `json:"seed_amount_raw"`
	AccountedRaw string            `json:"accounted_amount_raw"`
	Anomalies    []string          `json:"anomalies"`
	Assumptions  []string          `json:"assumptions"`
}

func integer(s string) (*big.Int, error) {
	// Model totals may exceed one uint256 when incomplete balances are padded,
	// but every total is still bounded by the supplied bounded ledger. Keep a
	// little headroom for those totals without ever parsing an unbounded string.
	if len(s) == 0 || len(s) > 128 {
		return nil, fmt.Errorf("invalid bounded unsigned raw amount")
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("invalid unsigned raw amount")
	}
	return n, nil
}

func ledgerInteger(s string) (*big.Int, error) {
	if len(s) > 78 {
		return nil, fmt.Errorf("raw amount exceeds uint256 precision")
	}
	n, err := integer(s)
	if err != nil {
		return nil, err
	}
	if n.BitLen() > 256 {
		return nil, fmt.Errorf("raw amount exceeds uint256 precision")
	}
	return n, nil
}
func clone(n *big.Int) *big.Int { return new(big.Int).Set(n) }
func Allocate(model string, transfers []query.LedgerTransfer, baselines map[string]string, sourceHash string, sourceIndex uint64, seedRaw string, maxHops int, batchIDs ...string) (Result, error) {
	return AllocateContext(context.Background(), model, transfers, baselines, sourceHash, sourceIndex, seedRaw, maxHops, batchIDs...)
}

func AllocateContext(ctx context.Context, model string, transfers []query.LedgerTransfer, baselines map[string]string, sourceHash string, sourceIndex uint64, seedRaw string, maxHops int, batchIDs ...string) (Result, error) {
	result := Result{Model: model, Edges: []Edge{}, Remaining: map[string]string{}, Anomalies: []string{}, Assumptions: []string{"Balances before the source block are untagged older funds", "Only supplied indexed token transfers affect the ledger; rebases, fees or missing transfers create uncertainty", "Raw integer allocation conserves the tracked amount; proportional rounding uses largest remainders", "Adjacent untagged fragments are consolidated without crossing tagged FIFO lots; proportional integer remainders apply to the consolidated lots, so smallest-token-unit allocation can differ from per-transfer-fragment rounding"}}
	budget := allocationBudget{ctx: ctx}
	if err := budget.charge(0); err != nil {
		return result, err
	}
	if len(transfers) > MaxAllocationTransfers {
		return result, &AllocationBudgetError{Dimension: "transfers", Used: int64(len(transfers)), Limit: MaxAllocationTransfers}
	}
	if maxHops < 1 || maxHops > 5 {
		return result, fmt.Errorf("max_hops must be 1-5")
	}
	if model != "fifo" && model != "proportional" {
		return result, fmt.Errorf("unsupported allocation model")
	}
	seed, e := ledgerInteger(seedRaw)
	if e != nil || seed.Sign() == 0 {
		return result, fmt.Errorf("seed amount must be positive")
	}
	result.SeedRaw = seed.String()
	batchID := fmt.Sprintf("%s:%d:%s", sourceHash, sourceIndex, seedRaw)
	if len(batchIDs) > 0 {
		batchID = batchIDs[0]
	}
	frontier := big.NewInt(0)
	accounts := map[string][]Lot{}
	activeLots := 0
	for address, raw := range baselines {
		if err := budget.charge(1); err != nil {
			return result, err
		}
		n, e := ledgerInteger(raw)
		if e != nil {
			return result, e
		}
		if n.Sign() > 0 {
			accounts[address] = []Lot{{Amount: n, ID: "baseline:" + address, Original: clone(n)}}
			activeLots++
			if err := budget.lots(activeLots); err != nil {
				return result, err
			}
		}
	}
	if err := budget.charge(int64(len(transfers) * bits.Len(uint(len(transfers))))); err != nil {
		return result, err
	}
	ordered := append([]query.LedgerTransfer(nil), transfers...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Block != ordered[j].Block {
			return ordered[i].Block < ordered[j].Block
		}
		if ordered[i].Index != ordered[j].Index {
			return ordered[i].Index < ordered[j].Index
		}
		return ordered[i].Hash < ordered[j].Hash
	})
	seeded := false
	for _, tx := range ordered {
		if err := budget.charge(1); err != nil {
			return result, err
		}
		amount, e := ledgerInteger(tx.Raw)
		if e != nil {
			return result, e
		}
		if tx.From == tx.To {
			if tx.Hash == sourceHash && tx.Index == sourceIndex {
				return result, fmt.Errorf("source lot cannot be a self transfer")
			}
			continue
		}
		if amount.Sign() == 0 {
			continue
		}
		lots := accounts[tx.From]
		previousSenderLots := len(lots)
		total := big.NewInt(0)
		for _, l := range lots {
			if err := budget.charge(1); err != nil {
				return result, err
			}
			total.Add(total, l.Amount)
		}
		gapInjected := big.NewInt(0)
		if total.Cmp(amount) < 0 {
			gap := new(big.Int).Sub(amount, total)
			gapInjected.Set(gap)
			lots = append(lots, Lot{Amount: gap, ID: fmt.Sprintf("gap:%s:%d", tx.Hash, tx.Index), Original: clone(gap)})
			result.Anomalies = append(result.Anomalies, fmt.Sprintf("unexplained balance deficit at %s in %s:%d", tx.From, tx.Hash, tx.Index))
			total.Add(total, gap)
		}
		consumed := make([]*big.Int, len(lots))
		for i := range consumed {
			if err := budget.charge(1); err != nil {
				return result, err
			}
			consumed[i] = big.NewInt(0)
		}
		if model == "fifo" {
			left := clone(amount)
			for i, l := range lots {
				if err := budget.charge(1); err != nil {
					return result, err
				}
				take := clone(l.Amount)
				if take.Cmp(left) > 0 {
					take.Set(left)
				}
				consumed[i] = take
				left.Sub(left, take)
				if left.Sign() == 0 {
					break
				}
			}
		} else if total.Sign() > 0 {
			type remainder struct {
				Index int
				Value *big.Int
			}
			rs := []remainder{}
			assigned := big.NewInt(0)
			for i, l := range lots {
				if err := budget.charge(1); err != nil {
					return result, err
				}
				product := new(big.Int).Mul(amount, l.Amount)
				rem := big.NewInt(0)
				consumed[i].QuoRem(product, total, rem)
				assigned.Add(assigned, consumed[i])
				if rem.Sign() > 0 {
					rs = append(rs, remainder{i, rem})
				}
			}
			left := new(big.Int).Sub(amount, assigned)
			if left.Sign() > 0 {
				if err := budget.charge(int64(len(rs) * bits.Len(uint(len(rs))))); err != nil {
					return result, err
				}
				sort.SliceStable(rs, func(i, j int) bool { return rs[i].Value.Cmp(rs[j].Value) > 0 })
				if err := budget.charge(0); err != nil {
					return result, err
				}
			}
			for i := 0; left.Sign() > 0 && i < len(rs); i++ {
				consumed[rs[i].Index].Add(consumed[rs[i].Index], big.NewInt(1))
				left.Sub(left, big.NewInt(1))
			}
		}
		remaining := []Lot{}
		incoming := []Lot{}
		attributions := map[int]*big.Int{}
		sourceBefore := map[int]*big.Int{}
		consumptions := map[int][]Consumption{}
		for i, l := range lots {
			if err := budget.charge(1); err != nil {
				return result, err
			}
			if l.Tagged {
				h := l.Hops + 1
				if sourceBefore[h] == nil {
					sourceBefore[h] = big.NewInt(0)
				}
				sourceBefore[h].Add(sourceBefore[h], l.Amount)
			}
			take := consumed[i]
			rest := new(big.Int).Sub(l.Amount, take)
			if rest.Sign() > 0 {
				remaining = appendCompact(remaining, Lot{Amount: rest, Tagged: l.Tagged, Hops: l.Hops, ID: l.ID, Original: l.Original})
			}
			if l.Tagged && l.Hops+1 <= maxHops {
				if err := budget.consumption(); err != nil {
					return result, err
				}
				original := l.Original
				if original == nil {
					original = l.Amount
				}
				consumptions[l.Hops+1] = append(consumptions[l.Hops+1], Consumption{LotID: l.ID, SourceBatchID: batchID, OriginalRaw: seed.String(), FragmentOriginalRaw: original.String(), BeforeRaw: l.Amount.String(), ConsumedRaw: take.String(), AfterRaw: rest.String(), OutgoingHash: tx.Hash})
			}
			if take.Sign() == 0 {
				continue
			}
			next := Lot{Amount: clone(take), Tagged: l.Tagged, Hops: l.Hops, ID: fmt.Sprintf("%s:%d:%d:%s", tx.Hash, tx.Index, i, model), Original: clone(take)}
			if l.Tagged {
				next.Hops++
				if next.Hops <= maxHops {
					if attributions[next.Hops] == nil {
						attributions[next.Hops] = big.NewInt(0)
					}
					attributions[next.Hops].Add(attributions[next.Hops], take)
				}
				if next.Hops > maxHops {
					frontier.Add(frontier, take)
					next.Tagged = false
				}
			}
			incoming = appendCompact(incoming, next)
		}
		if tx.Hash == sourceHash && tx.Index == sourceIndex {
			if seeded {
				return result, fmt.Errorf("duplicate source transfer")
			}
			if amount.Cmp(seed) < 0 {
				return result, fmt.Errorf("seed exceeds source transfer")
			}
			incoming = []Lot{}
			untagged := new(big.Int).Sub(amount, seed)
			if untagged.Sign() > 0 {
				incoming = append(incoming, Lot{Amount: untagged, ID: fmt.Sprintf("%s:%d:other", tx.Hash, tx.Index), Original: clone(untagged)})
			}
			incoming = append(incoming, Lot{Amount: clone(seed), Tagged: true, Hops: 0, ID: batchID, Original: clone(seed)})
			seeded = true
		}
		accounts[tx.From] = remaining
		previousReceiverLots := len(accounts[tx.To])
		receiver := accounts[tx.To]
		for _, lot := range incoming {
			if err := budget.charge(1); err != nil {
				return result, err
			}
			receiver = appendCompact(receiver, lot)
		}
		activeLots += len(remaining) + len(receiver) - previousSenderLots - previousReceiverLots
		if err := budget.lots(activeLots); err != nil {
			return result, err
		}
		accounts[tx.To] = receiver
		for hops := 1; hops <= maxHops; hops++ {
			if err := budget.charge(1); err != nil {
				return result, err
			}
			if before := sourceBefore[hops]; before != nil && before.Sign() > 0 {
				n := attributions[hops]
				if n == nil {
					n = big.NewInt(0)
				}
				result.Edges = append(result.Edges, Edge{Hash: tx.Hash, Block: tx.Block, Index: tx.Index, Time: tx.Time, Token: tx.Token, GapRaw: gapInjected.String(), Transfer: tx, AttributedRaw: n.String(), Hops: hops, From: tx.From, To: tx.To, ActualRaw: tx.Raw, Model: model, Type: "inferred", BatchID: batchID, SourceBeforeRaw: before.String(), TotalBeforeRaw: total.String(), Share: ratio(before, total), ShareFraction: before.String() + "/" + total.String(), ConfidenceBasis: "Unrated accounting inference; not a statistically calibrated ownership probability", BalanceBasis: "Archive state baseline plus supplied indexed transfers; modeled mid-block balances, not a new RPC read at each transfer", Consumptions: consumptions[hops]})
			}
		}
	}
	if !seeded {
		return result, fmt.Errorf("source transfer is missing from the ledger")
	}
	accounted := clone(frontier)
	for address, lots := range accounts {
		if err := budget.charge(1); err != nil {
			return result, err
		}
		amount := big.NewInt(0)
		for _, l := range lots {
			if err := budget.charge(1); err != nil {
				return result, err
			}
			if l.Tagged {
				amount.Add(amount, l.Amount)
			}
		}
		if amount.Sign() > 0 {
			result.Remaining[address] = amount.String()
			accounted.Add(accounted, amount)
		}
	}
	result.FrontierRaw = frontier.String()
	result.AccountedRaw = accounted.String()
	if accounted.Cmp(seed) != 0 {
		return result, fmt.Errorf("allocation conservation invariant failed")
	}
	return result, nil
}

// ratio is a display decimal rounded to 80 places. The fraction carries exactness.
func ratio(n, d *big.Int) string {
	if d.Sign() == 0 {
		return "0"
	}
	return decimal.NewFromBigInt(n, 0).DivRound(decimal.NewFromBigInt(d, 0), 80).String()
}

func Format(r *Result, decimals int) {
	value := func(raw string) string {
		n, _ := integer(raw)
		return decimal.NewFromBigInt(n, -int32(decimals)).String()
	}
	for i := range r.Edges {
		e := &r.Edges[i]
		e.ActualAmount = value(e.ActualRaw)
		e.AttributedAmount = value(e.AttributedRaw)
		e.SourceBefore = value(e.SourceBeforeRaw)
		e.TotalBefore = value(e.TotalBeforeRaw)
		e.Gap = value(e.GapRaw)
		for j := range e.Consumptions {
			c := &e.Consumptions[j]
			c.Original = value(c.OriginalRaw)
			c.Before = value(c.BeforeRaw)
			c.Consumed = value(c.ConsumedRaw)
			c.After = value(c.AfterRaw)
		}
	}
}
