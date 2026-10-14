package api

import (
	"context"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"time"
)

func (s *Server) contractHistory(ctx context.Context, q ca.Request, result *ca.Result) {
	if q.FromBlock == nil {
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "historical_calls", "complete": false, "reason": "range_not_requested"}))
		return
	}
	coverage, err := s.deps.Store.ListCoverage(ctx, q.Chain, q.Address, "transactions")
	complete := err == nil
	position := int64(*q.FromBlock)
	for _, interval := range coverage {
		if interval.FromBlock <= position && interval.ToBlock >= position && !interval.CommittedAt.IsZero() {
			position = interval.ToBlock + 1
		}
	}
	complete = complete && position > int64(*q.ToBlock)
	if s.deps.CH == nil {
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "historical_calls", "complete": false, "reason": "index_unavailable"}))
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := s.deps.CH.DB().Query(clickhouse.InteractiveScanContext(bounded), `SELECT substring(input,1,10) AS selector,count(),countIf(status=1),countIf(status=0),uniqExact(from_address),min(timestamp),max(timestamp) FROM blockchain.transactions FINAL WHERE chain=? AND to_address=? AND block_number>=? AND block_number<=? GROUP BY selector ORDER BY selector LIMIT ?`, q.Chain, q.Address, *q.FromBlock, *q.ToBlock, q.Budget.MaxRecords+1)
	if err != nil {
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "historical_calls", "complete": false, "reason": "bounded_index_query_unavailable", "from_block": q.FromBlock, "to_block": q.ToBlock}))
		return
	}
	defer rows.Close()
	aggregates := map[string]map[string]any{}
	count := 0
	for rows.Next() {
		var selector string
		var calls, successful, failed, callers uint64
		var first, last time.Time
		if err = rows.Scan(&selector, &calls, &successful, &failed, &callers, &first, &last); err != nil {
			break
		}
		count++
		if count > q.Budget.MaxRecords {
			complete = false
			break
		}
		a := map[string]any{"selector": selector, "count": calls, "success": successful, "failed": failed, "callers": callers, "first_time": first, "last_time": last, "from_block": q.FromBlock, "to_block": q.ToBlock, "coverage_complete": complete, "basis": "indexed_top_level_calls", "internal_calls_complete": false, "entry_address": q.Address, "method_identity_status": "selector_candidates_not_historical_code_attribution"}
		aggregates[selector] = a
		result.Sections["history"] = append(result.Sections["history"], ca.Row(a))
	}
	if err != nil || rows.Err() != nil {
		complete = false
	}
	for i, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		selector, _ := m["selector"].(string)
		if selector == "" || m["dispatch_role"] == "overridden_source_definition" {
			m["historical_calls"] = map[string]any{"count": nil, "status": "not_a_unique_external_selector_entry"}
			result.Sections["methods"][i] = ca.Row(m)
			continue
		}
		if aggregate, ok := aggregates[selector]; ok {
			aggregate["coverage_complete"] = complete
			m["historical_calls"] = aggregate
		} else {
			m["historical_calls"] = map[string]any{"count": nil, "status": "no_indexed_calls_in_range", "coverage_complete": complete}
		}
		result.Sections["methods"][i] = ca.Row(m)
	}
	result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "historical_calls", "complete": complete, "from_block": q.FromBlock, "to_block": q.ToBlock, "methods_observed": count, "basis": "committed_transaction_intervals_and_exact_index_aggregation", "internal_calls_complete": false}))
}
