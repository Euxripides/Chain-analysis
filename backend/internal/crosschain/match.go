package crosschain

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

func SelectAnchor(selector Anchor, movements []Movement) (Movement, error) {
	selected := []Movement{}
	for _, m := range movements {
		if m.Dataset == selector.Dataset && (selector.EventIndex == nil || m.Index == *selector.EventIndex) {
			amount, e := Amount(m.Amount)
			if e == nil && amount.IsPositive() && m.Status != "failed" {
				selected = append(selected, m)
			}
		}
	}
	if len(selected) == 0 {
		return Movement{}, fmt.Errorf("no positive successful/observed anchor movement in the requested dataset/event")
	}
	if len(selected) != 1 {
		return Movement{}, fmt.Errorf("anchor is ambiguous: %d movements; provide event_index", len(selected))
	}
	if selected[0].Timestamp.IsZero() || selected[0].Timestamp.Unix() <= 0 {
		return Movement{}, fmt.Errorf("anchor timestamp is unavailable; indexed event and parent transaction must have a real block time")
	}
	return selected[0], nil
}

func Match(ctx context.Context, r CandidateRequest, anchor Movement, movements []Movement, scanTruncated bool) (MatchResult, error) {
	if e := r.Validate(); e != nil {
		return MatchResult{}, e
	}
	if anchor.Chain != r.Anchor.Chain || !Same(anchor.Chain, anchor.TxHash, r.Anchor.TxHash) || anchor.Dataset != r.Anchor.Dataset || r.Anchor.EventIndex != nil && anchor.Index != *r.Anchor.EventIndex {
		return MatchResult{}, fmt.Errorf("anchor identity does not match request")
	}
	if anchor.Status == "failed" || anchor.Timestamp.IsZero() || anchor.Timestamp.Unix() <= 0 {
		return MatchResult{}, fmt.Errorf("anchor must have an observed timestamp and must not be failed")
	}
	ref, e := Amount(anchor.Amount)
	if e != nil || !ref.IsPositive() {
		return MatchResult{}, fmt.Errorf("anchor amount is unavailable")
	}
	basis := "anchor_observed_amount"
	if r.ExpectedAmount != "" {
		ref, _ = Amount(r.ExpectedAmount)
		basis = "explicit_expected_amount"
	}
	abs, _ := Amount(r.AbsoluteTolerance)
	relative := ref.Mul(decimal.NewFromInt(int64(r.RelativeToleranceBPS))).Shift(-4)
	tol := abs
	if relative.GreaterThan(tol) {
		tol = relative
	}
	out := MatchResult{Anchor: anchor, Candidates: []Candidate{}, ReferenceAmount: ref.String(), AmountBasis: basis, AssetRelation: r.AssetRelation, Scanned: len(movements), ScanTruncated: scanTruncated}
	type entry struct {
		movement Movement
		amount   decimal.Decimal
		delta    int64
	}
	groups := map[string][]entry{}
	seen := map[string]bool{}
	for _, m := range movements {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if m.Chain != r.SearchChain || m.Timestamp.Before(r.StartTime) || m.Timestamp.After(r.EndTime) || m.Timestamp.IsZero() || m.Timestamp.Unix() <= 0 || m.Status == "failed" {
			continue
		}
		if r.AssetKind == "token" {
			if m.Dataset != "token_transfers" || !Same(m.Chain, m.TokenContract, r.TokenContract) {
				continue
			}
		} else if m.Dataset == "token_transfers" {
			continue
		}
		searchAddress := m.To
		opposite := m.From
		if r.SearchDirection == "out" {
			searchAddress = m.From
			opposite = m.To
		}
		found := false
		for _, a := range r.SearchAddresses {
			if Same(m.Chain, a, searchAddress) {
				found = true
				break
			}
		}
		if !found || Same(m.Chain, m.From, m.To) {
			continue
		}
		if r.Counterparty != "" && !Same(m.Chain, r.Counterparty, opposite) {
			continue
		}
		// The explicit range controls pre-funding and delayed settlement; signed delta exposes chronology.
		delta := m.Timestamp.Unix() - anchor.Timestamp.Unix()
		if seen[m.Identity()] {
			continue
		}
		seen[m.Identity()] = true
		n, e := Amount(m.Amount)
		if e != nil || !n.IsPositive() || n.GreaterThan(ref.Add(tol)) {
			continue
		}
		group := strings.Join([]string{m.Chain, strings.ToLower(m.Token), fmt.Sprint(m.Decimals), m.TokenContract, m.From, m.To}, "|")
		groups[group] = append(groups[group], entry{m, n, delta})
	}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	stop := false
	for _, key := range keys {
		entries := groups[key]
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].movement.Timestamp.Equal(entries[j].movement.Timestamp) {
				return entries[i].movement.Identity() < entries[j].movement.Identity()
			}
			return entries[i].movement.Timestamp.Before(entries[j].movement.Timestamp)
		})
		var walk func(int, []entry, decimal.Decimal)
		walk = func(start int, parts []entry, total decimal.Decimal) {
			if stop {
				return
			}
			if ctx.Err() != nil {
				stop = true
				return
			}
			if len(parts) > 0 {
				diff := total.Sub(ref).Abs()
				if diff.LessThanOrEqual(tol) {
					rows := []Movement{}
					delta := parts[0].delta
					reasons := []string{"asset filter and explicit address/time range matched", "cross-chain asset relationship declared by caller"}
					unknown := false
					for _, p := range parts {
						rows = append(rows, p.movement)
						if absInt(p.delta) > absInt(delta) {
							delta = p.delta
						}
						if p.movement.Status != "success" {
							unknown = true
						}
					}
					score := 30
					if diff.IsZero() {
						score += 40
						reasons = append(reasons, "exact decimal amount match")
					} else {
						score += 25
						reasons = append(reasons, "amount difference within explicit tolerance; fee is not independently established")
					}
					if absInt(delta) <= 60 {
						score += 20
					} else if absInt(delta) <= 600 {
						score += 15
					} else {
						score += 5
					}
					if r.Counterparty != "" {
						score += 5
						reasons = append(reasons, "explicit counterparty matched")
					}
					if len(parts) > 1 {
						score -= 5 * (len(parts) - 1)
						reasons = append(reasons, "sum of same-asset movements between the same address pair within five minutes")
					}
					if unknown {
						score -= 10
						reasons = append(reasons, "one or more parent transaction statuses are unavailable")
					}
					if r.Direction == "forward" && delta < 0 || r.Direction == "backward" && delta > 0 {
						score -= 10
						reasons = append(reasons, "chronology is opposite to the requested tracing direction")
					}
					out.Candidates = append(out.Candidates, Candidate{rows, total.String(), diff.String(), delta, score, reasons, "candidate"})
				}
			}
			if len(parts) >= r.MaxParts {
				return
			}
			for i := start; i < len(entries); i++ {
				if len(parts) > 0 && entries[i].movement.Timestamp.Sub(parts[0].movement.Timestamp) > 5*time.Minute {
					break
				}
				if out.CombinationsTested >= r.MaxCombinations {
					out.CombinationsTruncated = true
					stop = true
					return
				}
				out.CombinationsTested++
				n := total.Add(entries[i].amount)
				if n.GreaterThan(ref.Add(tol)) {
					continue
				}
				walk(i+1, append(parts, entries[i]), n)
				if stop {
					return
				}
			}
		}
		walk(0, nil, decimal.Zero)
		if stop {
			break
		}
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	sort.Slice(out.Candidates, func(i, j int) bool {
		a, b := out.Candidates[i], out.Candidates[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.AmountDifference != b.AmountDifference {
			x, _ := Amount(a.AmountDifference)
			y, _ := Amount(b.AmountDifference)
			return x.LessThan(y)
		}
		if absInt(a.TimeDifferenceSeconds) != absInt(b.TimeDifferenceSeconds) {
			return absInt(a.TimeDifferenceSeconds) < absInt(b.TimeDifferenceSeconds)
		}
		return a.Movements[0].Identity() < b.Movements[0].Identity()
	})
	if len(out.Candidates) > r.MaxResults {
		out.Candidates = out.Candidates[:r.MaxResults]
		out.ResultTruncated = true
	}
	return out, nil
}
func absInt(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
