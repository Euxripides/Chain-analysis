package fundtrace

import (
	"fmt"
	"github.com/shopspring/decimal"
	"math/big"
	"sort"
)

func Compare(a, b Result, decimals int) map[string]any {
	amount := func(raw *big.Int) string { return decimal.NewFromBigInt(raw, -int32(decimals)).String() }
	keys := map[string]bool{}
	left := map[string]string{}
	right := map[string]string{}
	for _, e := range a.Edges {
		k := fmt.Sprintf("%s:%d:%d", e.Transfer.Hash, e.Transfer.Index, e.Hops)
		keys[k] = true
		left[k] = e.AttributedRaw
	}
	for _, e := range b.Edges {
		k := fmt.Sprintf("%s:%d:%d", e.Transfer.Hash, e.Transfer.Index, e.Hops)
		keys[k] = true
		right[k] = e.AttributedRaw
	}
	ordered := []string{}
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	equal, different := 0, 0
	differences := []any{}
	parse := func(s string) *big.Int {
		if s == "" {
			return big.NewInt(0)
		}
		n, _ := integer(s)
		return n
	}
	for _, k := range ordered {
		x, y := parse(left[k]), parse(right[k])
		if x.Cmp(y) == 0 {
			equal++
			continue
		}
		different++
		d := new(big.Int).Sub(x, y)
		d.Abs(d)
		differences = append(differences, map[string]any{"transfer_key": k, "proportional_raw": x.String(), "fifo_raw": y.String(), "difference_amount_raw": d.String(), "difference_amount": amount(d)})
	}
	terminalKeys := map[string]bool{}
	for k := range a.Remaining {
		terminalKeys[k] = true
	}
	for k := range b.Remaining {
		terminalKeys[k] = true
	}
	l1 := big.NewInt(0)
	for k := range terminalKeys {
		d := new(big.Int).Sub(parse(a.Remaining[k]), parse(b.Remaining[k]))
		l1.Add(l1, d.Abs(d))
	}
	frontier := new(big.Int).Sub(parse(a.FrontierRaw), parse(b.FrontierRaw))
	l1.Add(l1, frontier.Abs(frontier))
	// Both conserved distributions have the same sum, hence L1 is even.
	redistributed := new(big.Int).Quo(l1, big.NewInt(2))
	consistency := "consistent"
	if different > 0 || redistributed.Sign() > 0 {
		consistency = "divergent"
		if equal > 0 {
			consistency = "partially_consistent"
		}
	}
	summary := func(r Result) map[string]any {
		return map[string]any{"remaining_attributed_raw": r.Remaining, "unexpanded_frontier_raw": r.FrontierRaw, "accounted_amount_raw": r.AccountedRaw}
	}
	return map[string]any{"model_consistency": consistency, "proportional_result": summary(a), "fifo_result": summary(b), "difference_amount_raw": redistributed.String(), "difference_amount": amount(redistributed), "difference_ratio": ratio(redistributed, parse(a.SeedRaw)), "difference_basis": "Terminal distribution total variation including unexpanded frontier; per-transfer differences are separately listed and must not be summed across hops", "transfer_differences": differences, "equal_transfer_count": equal, "different_transfer_count": different, "comparison_scope": "Same supplied ledger and archive baselines; consistency does not prove completeness or fund identity"}
}
