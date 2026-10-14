package api

import (
	"context"
	"encoding/json"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"time"
)

func (s *Server) contractUsageMetrics(ctx context.Context, analysisID string, result *ca.Result) error {
	var requests, calls, caches, retries, bytes, failures int64
	var credits, cost string
	err := s.deps.Store.Pool.QueryRow(ctx, `SELECT
	 COALESCE(SUM(http_requests),0)::bigint,COALESCE(SUM(method_calls),0)::bigint,
	 COUNT(*) FILTER(WHERE cache_hit OR singleflight_hit),COUNT(*) FILTER(WHERE retry_attempt>0),
	 COALESCE(SUM(response_bytes),0)::bigint,
	 COUNT(*) FILTER(WHERE status NOT IN ('ok','success','cache_hit','singleflight_hit')),
	 COALESCE(SUM(credits) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0)::numeric::text,
	 COALESCE(SUM(estimated_cost_usd) FILTER(WHERE NOT cache_hit AND NOT singleflight_hit),0)::numeric::text
	 FROM provider_usage WHERE job_id=$1 AND usage_type='contract_analysis'`, analysisID).Scan(&requests, &calls, &caches, &retries, &bytes, &failures, &credits, &cost)
	if err != nil {
		return err
	}
	result.Metrics["rpc_http_requests"], result.Metrics["rpc_method_calls"] = requests, calls
	result.Metrics["rpc_cache_hits"], result.Metrics["rpc_retry_attempts"] = caches, retries
	result.Metrics["rpc_response_bytes"], result.Metrics["rpc_failed_attempts"] = bytes, failures
	result.Metrics["credits"], result.Metrics["estimated_usd"] = json.Number(credits), json.Number(cost)
	result.Metrics["cost_basis"] = "persisted_provider_usage_pricing_estimate"
	result.Metrics["accounting_as_of"] = time.Now().UTC()
	var raw []byte
	if err = s.deps.Store.Pool.QueryRow(ctx, `SELECT jsonb_build_object('reserved_rpc_http_requests',reserved_requests,'reserved_credits',reserved_credits::text,'state_read_attempts',state_reads,'processing_ms',processing_ms,'max_rpc_http_requests',max_requests,'max_credits',max_credits,'max_state_reads',max_state_reads) FROM contract_analysis_budgets WHERE analysis_id=$1`, analysisID).Scan(&raw); err != nil {
		return err
	}
	result.Metrics["durable_budget"] = json.RawMessage(raw)
	return nil
}
