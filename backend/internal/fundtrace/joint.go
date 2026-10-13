package fundtrace

import (
	"context"
	"fmt"
	"math/big"
	"math/bits"
	"sort"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/query"
)

// JointSeed is activated at its receipt log, never at the start of the window.
type JointSeed struct {
	ID    string `json:"source_id"`
	Hash  string `json:"tx_hash"`
	Index uint64 `json:"log_index"`
	Raw   string `json:"amount_raw"`
}
type JointSource struct {
	SeedRaw      string            `json:"seed_amount_raw"`
	Remaining    map[string]string `json:"remaining_attributed_raw"`
	FrontierRaw  string            `json:"unexpanded_frontier_raw"`
	BurnedRaw    string            `json:"burned_raw"`
	AccountedRaw string            `json:"accounted_amount_raw"`
}
type JointEdge struct {
	ExternalFunding bool                 `json:"external_funding_sender"`
	Transfer        query.LedgerTransfer `json:"transfer"`
	AttributedRaw   string               `json:"total_attributed_raw"`
	BySource        map[string]string    `json:"attributed_by_source_raw"`
	GapRaw          string               `json:"balance_gap_injected_raw"`
}
type JointResult struct {
	Model       string                 `json:"model"`
	Sources     map[string]JointSource `json:"sources"`
	Edges       []JointEdge            `json:"edges"`
	Anomalies   []string               `json:"anomalies"`
	Assumptions []string               `json:"assumptions"`
}
type jointLot struct {
	amount *big.Int
	source string
	hops   int
}
type SourceOverlapError struct {
	Source, PriorSource, Hash string
	Index                     uint64
}

func (e *SourceOverlapError) Error() string {
	return fmt.Sprintf("SOURCE_OVERLAP: source %s consumes source %s in %s:%d; select independent source receipts", e.Source, e.PriorSource, e.Hash, e.Index)
}
func compactJoint(dst []jointLot, lot jointLot) []jointLot {
	if lot.amount.Sign() == 0 {
		return dst
	}
	if lot.source == "" && len(dst) > 0 && dst[len(dst)-1].source == "" {
		dst[len(dst)-1].amount = new(big.Int).Add(dst[len(dst)-1].amount, lot.amount)
		return dst
	}
	return append(dst, lot)
}
func eventKey(tx query.LedgerTransfer) string {
	return fmt.Sprintf("%s:%d", strings.ToLower(tx.Hash), tx.Index)
}

// AllocateJoint uses ONE physical ledger for all sources. A unit has at most
// one source ID. Proportional rounding is performed over all physical lots in
// one apportionment; independently running/clipping single-source models is
// deliberately impossible here.
func AllocateJoint(ctx context.Context, model string, transfers []query.LedgerTransfer, baselines map[string]string, seeds []JointSeed, maxHops int, externalSets ...map[string]bool) (JointResult, error) {
	r := JointResult{Model: model, Sources: map[string]JointSource{}, Edges: []JointEdge{}, Anomalies: []string{}, Assumptions: []string{
		"One shared ledger; seeds activate only at their confirmed receipt positions", "Source overlap is rejected, never retagged or counted twice", "Baseline funds are untagged; missing transfers, fee tokens and rebases can invalidate modeled balances", "FIFO preserves lot chronology; proportional integer rounding uses largest remainders with stable FIFO ties", "Adjacent untagged fragments are consolidated; models are accounting inference, not identity of fungible units", "Frontier and burn are terminal accounted amounts, not current wallet balances"}}
	b := allocationBudget{ctx: ctx}
	external := map[string]bool{}
	if len(externalSets) > 1 {
		return r, fmt.Errorf("one external sender set allowed")
	}
	if len(externalSets) == 1 {
		external = externalSets[0]
	}
	for a := range external {
		if _, ok := baselines[a]; ok {
			return r, fmt.Errorf("external sender also has tracked-account baseline")
		}
	}
	if len(external) > 0 {
		r.Assumptions = append(r.Assumptions, "Senders outside the tracked graph contribute confirmed untagged external inflows; their unrelated balances/history cannot affect source allocation", "Tagged flows into senders without an archive baseline terminate as unexpanded frontier")
	}
	if err := b.charge(0); err != nil {
		return r, err
	}
	if (model != "fifo" && model != "proportional") || maxHops < 1 || maxHops > 5 || len(seeds) < 1 || len(seeds) > 100 {
		return r, fmt.Errorf("invalid joint model, hops or source count")
	}
	if len(transfers) > MaxAllocationTransfers {
		return r, &AllocationBudgetError{Dimension: "transfers", Used: int64(len(transfers)), Limit: MaxAllocationTransfers}
	}
	byEvent := map[string]JointSeed{}
	frontier := map[string]*big.Int{}
	burned := map[string]*big.Int{}
	activated := map[string]bool{}
	for _, seed := range seeds {
		amount, err := ledgerInteger(seed.Raw)
		if err != nil || amount.Sign() == 0 || seed.ID == "" || len(seed.ID) > 128 {
			return r, fmt.Errorf("invalid source ID or amount")
		}
		key := fmt.Sprintf("%s:%d", strings.ToLower(seed.Hash), seed.Index)
		if _, ok := byEvent[key]; ok {
			return r, fmt.Errorf("duplicate source event")
		}
		if _, ok := r.Sources[seed.ID]; ok {
			return r, fmt.Errorf("duplicate source ID")
		}
		byEvent[key] = seed
		r.Sources[seed.ID] = JointSource{SeedRaw: amount.String(), Remaining: map[string]string{}}
		frontier[seed.ID] = new(big.Int)
		burned[seed.ID] = new(big.Int)
	}
	accounts := map[string][]jointLot{}
	active := 0
	for address, raw := range baselines {
		v, err := ledgerInteger(raw)
		if err != nil {
			return r, err
		}
		if v.Sign() > 0 {
			accounts[address] = []jointLot{{amount: v}}
			active++
		}
	}
	if err := b.lots(active); err != nil {
		return r, err
	}
	ordered := append([]query.LedgerTransfer(nil), transfers...)
	if err := b.charge(int64(len(ordered) * bits.Len(uint(len(ordered))))); err != nil {
		return r, err
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Block != ordered[j].Block {
			return ordered[i].Block < ordered[j].Block
		}
		if ordered[i].Index != ordered[j].Index {
			return ordered[i].Index < ordered[j].Index
		}
		return ordered[i].Hash < ordered[j].Hash
	})
	visited := map[string]bool{}
	zero := "0x" + strings.Repeat("0", 40)
	for _, tx := range ordered {
		if err := b.charge(1); err != nil {
			return r, err
		}
		key := eventKey(tx)
		if visited[key] {
			return r, fmt.Errorf("duplicate ledger event %s", key)
		}
		visited[key] = true
		amount, err := ledgerInteger(tx.Raw)
		if err != nil {
			return r, err
		}
		seed, isSeed := byEvent[key]
		if tx.From == tx.To || amount.Sign() == 0 {
			if isSeed {
				return r, fmt.Errorf("source must be a nonzero non-self transfer")
			}
			continue
		}
		lots := accounts[tx.From]
		oldFrom := len(lots)
		total := new(big.Int)
		for _, lot := range lots {
			if external[tx.From] && lot.source != "" {
				return r, fmt.Errorf("tracked sender lacks required archive baseline")
			}
			total.Add(total, lot.amount)
		}
		if err := b.charge(int64(len(lots))); err != nil {
			return r, err
		}
		gap := new(big.Int)
		if total.Cmp(amount) < 0 {
			gap.Sub(amount, total)
			lots = compactJoint(lots, jointLot{amount: clone(gap)})
			total.Add(total, gap)
			if tx.From != zero && !external[tx.From] {
				r.Anomalies = append(r.Anomalies, fmt.Sprintf("unexplained deficit %s at %s", gap, key))
			}
		}
		takes := make([]*big.Int, len(lots))
		for i := range takes {
			takes[i] = new(big.Int)
		}
		if model == "fifo" {
			left := clone(amount)
			for i, lot := range lots {
				takes[i].Set(lot.amount)
				if takes[i].Cmp(left) > 0 {
					takes[i].Set(left)
				}
				left.Sub(left, takes[i])
				if left.Sign() == 0 {
					break
				}
			}
		} else {
			type rem struct {
				i int
				v *big.Int
			}
			rs := []rem{}
			assigned := new(big.Int)
			for i, lot := range lots {
				remainder := new(big.Int)
				takes[i].QuoRem(new(big.Int).Mul(amount, lot.amount), total, remainder)
				assigned.Add(assigned, takes[i])
				if remainder.Sign() > 0 {
					rs = append(rs, rem{i, remainder})
				}
			}
			sort.SliceStable(rs, func(i, j int) bool { return rs[i].v.Cmp(rs[j].v) > 0 })
			if err := b.charge(int64(len(rs) * bits.Len(uint(len(rs))))); err != nil {
				return r, err
			}
			left := new(big.Int).Sub(amount, assigned)
			for i := 0; left.Sign() > 0 && i < len(rs); i++ {
				takes[rs[i].i].Add(takes[rs[i].i], big.NewInt(1))
				left.Sub(left, big.NewInt(1))
			}
			if left.Sign() != 0 {
				return r, fmt.Errorf("joint rounding invariant failed")
			}
		}
		if err := b.charge(int64(len(lots) * 4)); err != nil {
			return r, err
		}
		if isSeed {
			for i, lot := range lots {
				if lot.source != "" && takes[i].Sign() > 0 {
					return r, &SourceOverlapError{seed.ID, lot.source, tx.Hash, tx.Index}
				}
			}
			v, _ := ledgerInteger(seed.Raw)
			if v.Cmp(amount) > 0 {
				return r, fmt.Errorf("seed exceeds receipt amount")
			}
		}
		rest := []jointLot{}
		incoming := []jointLot{}
		attributed := new(big.Int)
		sourceAmounts := map[string]*big.Int{}
		for i, lot := range lots {
			remaining := new(big.Int).Sub(lot.amount, takes[i])
			rest = compactJoint(rest, jointLot{remaining, lot.source, lot.hops})
			if takes[i].Sign() == 0 {
				continue
			}
			next := jointLot{clone(takes[i]), lot.source, lot.hops}
			if lot.source != "" {
				next.hops++
				if sourceAmounts[lot.source] == nil {
					sourceAmounts[lot.source] = new(big.Int)
				}
				sourceAmounts[lot.source].Add(sourceAmounts[lot.source], takes[i])
				attributed.Add(attributed, takes[i])
				if tx.To == zero {
					burned[lot.source].Add(burned[lot.source], takes[i])
					next.source = ""
				} else if next.hops > maxHops || external[tx.To] {
					frontier[lot.source].Add(frontier[lot.source], takes[i])
					next.source = ""
				}
			}
			incoming = compactJoint(incoming, next)
		}
		if isSeed {
			v, _ := ledgerInteger(seed.Raw)
			incoming = []jointLot{}
			other := new(big.Int).Sub(amount, v)
			incoming = compactJoint(incoming, jointLot{amount: other})
			incoming = compactJoint(incoming, jointLot{amount: v, source: seed.ID})
			activated[seed.ID] = true
			sourceAmounts[seed.ID] = clone(v)
			attributed.Set(v)
			if tx.To == zero {
				burned[seed.ID].Add(burned[seed.ID], v)
			}
		}
		accounts[tx.From] = rest
		oldTo := len(accounts[tx.To])
		receiver := accounts[tx.To]
		if tx.To == zero {
			incoming = nil
		} else {
			for _, lot := range incoming {
				receiver = compactJoint(receiver, lot)
			}
			accounts[tx.To] = receiver
		}
		active += len(rest) + len(receiver) - oldFrom - oldTo
		if err := b.lots(active); err != nil {
			return r, err
		}
		if attributed.Cmp(amount) > 0 {
			return r, fmt.Errorf("per-edge attribution exceeds actual transfer")
		}
		if attributed.Sign() > 0 {
			if err := b.consumption(); err != nil {
				return r, err
			}
			values := map[string]string{}
			for id, v := range sourceAmounts {
				values[id] = v.String()
			}
			r.Edges = append(r.Edges, JointEdge{Transfer: tx, AttributedRaw: attributed.String(), BySource: values, GapRaw: gap.String(), ExternalFunding: external[tx.From]})
		}
	}
	for address, lots := range accounts {
		if err := b.charge(int64(len(lots))); err != nil {
			return r, err
		}
		for _, lot := range lots {
			if lot.source == "" {
				continue
			}
			source := r.Sources[lot.source]
			v, _ := integer("0")
			if raw, ok := source.Remaining[address]; ok {
				v, _ = integer(raw)
			}
			v.Add(v, lot.amount)
			source.Remaining[address] = v.String()
			r.Sources[lot.source] = source
		}
	}
	for id, source := range r.Sources {
		if !activated[id] {
			return r, fmt.Errorf("source %s is absent from ledger", id)
		}
		accounted := new(big.Int).Add(frontier[id], burned[id])
		for _, raw := range source.Remaining {
			v, _ := integer(raw)
			accounted.Add(accounted, v)
		}
		if accounted.String() != source.SeedRaw {
			return r, fmt.Errorf("source conservation failed for %s", id)
		}
		source.FrontierRaw = frontier[id].String()
		source.BurnedRaw = burned[id].String()
		source.AccountedRaw = accounted.String()
		r.Sources[id] = source
	}
	return r, nil
}
