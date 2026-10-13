package api

import (
	"context"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"golang.org/x/sync/errgroup"
	"strings"
	"time"
)

func (s *Server) investigationBalanceBatch(ctx context.Context, q investigationRequest, op string) (any, map[string]any, error) {
	if len(q.Addresses) < 1 || len(q.Addresses) > 200 {
		return nil, nil, fmt.Errorf("addresses must contain 1-200 complete addresses")
	}
	if op == "batch_get_token_balances" && q.TokenContract == "" {
		return nil, nil, fmt.Errorf("token_contract is required")
	}
	if q.MaxConcurrent == 0 {
		q.MaxConcurrent = 4
	}
	if q.MaxConcurrent < 1 || q.MaxConcurrent > 8 {
		return nil, nil, fmt.Errorf("max_concurrent must be 1-8")
	}
	addresses := []string{}
	seen := map[string]bool{}
	for _, raw := range q.Addresses {
		a, e := addressnorm.Normalize(q.Chain, raw)
		if e != nil {
			return nil, nil, fmt.Errorf("invalid complete batch address")
		}
		if !seen[a] {
			seen[a] = true
			addresses = append(addresses, a)
		}
	}
	p, e := s.statePoint(ctx, q)
	if e != nil {
		return nil, nil, e
	}
	token := q.TokenContract
	if op == "batch_get_native_balances" {
		token = ""
	}
	items := make([]map[string]any, len(addresses))
	g := new(errgroup.Group)
	g.SetLimit(q.MaxConcurrent)
	for i, address := range addresses {
		index, a := i, address
		g.Go(func() error {
			item := map[string]any{"index": index, "address": a, "success": false}
			result, err := s.deps.ChainRead.SnapshotBalance(ctx, q.Chain, chain.Address(a), chain.Address(token), p, investigationOptions())
			if err != nil {
				item["error"] = map[string]any{"code": "BALANCE_READ_FAILED", "message": "RPC balance unavailable; retry this address"}
			} else {
				item["success"] = true
				item["response"] = actionEnvelope{Success: true, Data: result, Meta: investigationMeta(&p)}
			}
			items[index] = item
			return nil
		})
	}
	_ = g.Wait()
	succeeded := 0
	for _, item := range items {
		if item["success"] == true {
			succeeded++
		}
	}
	return map[string]any{"results": items, "requested": len(addresses), "succeeded": succeeded, "failed": len(addresses) - succeeded, "as_of_block": p.Block, "is_current_balance": p.Current, "snapshot_shared": true}, investigationMeta(&p), nil
}

func (s *Server) investigationSelectSource(ctx context.Context, q investigationRequest) (any, map[string]any, error) {
	if q.Source == "" || q.TokenContract == "" || q.StartTime == "" {
		return nil, nil, fmt.Errorf("without source_tx_hash, provide source_address, exact token_contract and start_time; ambiguous sources require explicit hash/log_index")
	}
	start, e := time.Parse(time.RFC3339, q.StartTime)
	if e != nil {
		return nil, nil, fmt.Errorf("invalid start_time")
	}
	end := start.Add(7 * 24 * time.Hour)
	if q.EndTime != "" {
		end, e = time.Parse(time.RFC3339, q.EndTime)
	}
	if e != nil || end.Before(start) || end.Sub(start) > 366*24*time.Hour {
		return nil, nil, fmt.Errorf("invalid source selection time range")
	}
	first, e := s.deps.ChainRead.ResolveState(ctx, q.Chain, chain.Latest(), &start, investigationOptions())
	if e != nil {
		return nil, nil, e
	}
	head, e := s.deps.ChainRead.ResolveState(ctx, q.Chain, chain.Latest(), nil, investigationOptions())
	if e != nil {
		return nil, nil, e
	}
	last := head
	if end.Before(head.Time) {
		last, e = s.deps.ChainRead.ResolveState(ctx, q.Chain, chain.Latest(), &end, investigationOptions())
		if e != nil {
			return nil, nil, e
		}
	}
	rows, more, e := s.query.InvestigationLedger(ctx, q.Chain, q.TokenContract, []string{q.Source}, first.Block, last.Block, 1000)
	if e != nil {
		return nil, nil, e
	}
	candidates := []any{}
	var selectedHash string
	var selectedIndex uint64
	for _, row := range rows {
		if row.From == q.Source && !row.Time.Before(start) && !row.Time.After(end) && row.Raw != "0" && row.To != row.From && row.To != "0x"+strings.Repeat("0", 40) {
			candidates = append(candidates, row)
			selectedHash = row.Hash
			selectedIndex = row.Index
		}
	}
	if len(candidates) != 1 || more {
		selectionTruncated := more || len(candidates) > 100
		if len(candidates) > 100 {
			candidates = candidates[:100]
		}
		return map[string]any{"source_address": q.Source, "token_contract": q.TokenContract, "requires_source_selection": true, "candidate_transfers": candidates, "selection_truncated": selectionTruncated, "reason": "No unique outgoing source Transfer; choose an explicit source_tx_hash and log_index. amount selects a fraction of that Transfer, not an address history total"}, map[string]any{"source": "indexed_history", "coverage": map[string]any{"complete": false}}, nil
	}
	q.SourceTx = selectedHash
	q.LogIndex = &selectedIndex
	q.EndTime = end.Format(time.RFC3339)
	return s.investigationTrace(ctx, q)
}
