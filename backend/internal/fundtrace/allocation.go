// Package fundtrace attributes a fungible token lot under explicit accounting
// assumptions. Its results are hypotheses over evidence, not token identity.
package fundtrace

import (
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"math/big"
	"sort"
)

type Lot struct {
	Amount *big.Int
	Tagged bool
	Hops   int
}
type Edge struct {
	Transfer      query.LedgerTransfer `json:"transfer"`
	AttributedRaw string               `json:"attributed_amount_raw"`
	Hops          int                  `json:"hops"`
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
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("invalid unsigned raw amount")
	}
	return n, nil
}
func clone(n *big.Int) *big.Int { return new(big.Int).Set(n) }
func Allocate(model string, transfers []query.LedgerTransfer, baselines map[string]string, sourceHash string, sourceIndex uint64, seedRaw string, maxHops int) (Result, error) {
	result := Result{Model: model, Edges: []Edge{}, Remaining: map[string]string{}, Anomalies: []string{}, Assumptions: []string{"Balances before the source block are untagged older funds", "Only supplied indexed token transfers affect the ledger; rebases, fees or missing transfers create uncertainty", "Raw integer allocation conserves the tracked amount; proportional rounding uses largest remainders"}}
	if model != "fifo" && model != "proportional" {
		return result, fmt.Errorf("unsupported allocation model")
	}
	seed, e := integer(seedRaw)
	if e != nil || seed.Sign() == 0 {
		return result, fmt.Errorf("seed amount must be positive")
	}
	result.SeedRaw = seed.String()
	frontier := big.NewInt(0)
	accounts := map[string][]Lot{}
	for address, raw := range baselines {
		n, e := integer(raw)
		if e != nil {
			return result, e
		}
		if n.Sign() > 0 {
			accounts[address] = []Lot{{Amount: n}}
		}
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
		amount, e := integer(tx.Raw)
		if e != nil {
			return result, e
		}
		if tx.From == tx.To {
			if tx.Hash == sourceHash && tx.Index == sourceIndex {
				return result, fmt.Errorf("source lot cannot be a self transfer")
			}
			continue
		}
		lots := accounts[tx.From]
		total := big.NewInt(0)
		for _, l := range lots {
			total.Add(total, l.Amount)
		}
		if total.Cmp(amount) < 0 {
			gap := new(big.Int).Sub(amount, total)
			lots = append(lots, Lot{Amount: gap})
			result.Anomalies = append(result.Anomalies, fmt.Sprintf("unexplained balance deficit at %s in %s:%d", tx.From, tx.Hash, tx.Index))
			total.Add(total, gap)
		}
		consumed := make([]*big.Int, len(lots))
		for i := range consumed {
			consumed[i] = big.NewInt(0)
		}
		if model == "fifo" {
			left := clone(amount)
			for i, l := range lots {
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
				product := new(big.Int).Mul(amount, l.Amount)
				rem := big.NewInt(0)
				consumed[i].QuoRem(product, total, rem)
				assigned.Add(assigned, consumed[i])
				rs = append(rs, remainder{i, rem})
			}
			sort.SliceStable(rs, func(i, j int) bool { return rs[i].Value.Cmp(rs[j].Value) > 0 })
			left := new(big.Int).Sub(amount, assigned)
			for i := 0; left.Sign() > 0 && i < len(rs); i++ {
				consumed[rs[i].Index].Add(consumed[rs[i].Index], big.NewInt(1))
				left.Sub(left, big.NewInt(1))
			}
		}
		remaining := []Lot{}
		incoming := []Lot{}
		attributions := map[int]*big.Int{}
		for i, l := range lots {
			take := consumed[i]
			rest := new(big.Int).Sub(l.Amount, take)
			if rest.Sign() > 0 {
				remaining = append(remaining, Lot{Amount: rest, Tagged: l.Tagged, Hops: l.Hops})
			}
			if take.Sign() == 0 {
				continue
			}
			next := Lot{Amount: clone(take), Tagged: l.Tagged, Hops: l.Hops}
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
			incoming = append(incoming, next)
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
				incoming = append(incoming, Lot{Amount: untagged})
			}
			incoming = append(incoming, Lot{Amount: clone(seed), Tagged: true, Hops: 0})
			seeded = true
		}
		accounts[tx.From] = remaining
		accounts[tx.To] = append(accounts[tx.To], incoming...)
		for hops := 1; hops <= maxHops; hops++ {
			if n := attributions[hops]; n != nil && n.Sign() > 0 {
				result.Edges = append(result.Edges, Edge{Transfer: tx, AttributedRaw: n.String(), Hops: hops})
			}
		}
	}
	if !seeded {
		return result, fmt.Errorf("source transfer is missing from the ledger")
	}
	accounted := clone(frontier)
	for address, lots := range accounts {
		amount := big.NewInt(0)
		for _, l := range lots {
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
