package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// V1.5 RPC 控制面存储（§191-§195）。
// 明文 Secret 永不入库：这里只保存能力、健康快照与计价。

// capabilityFlags 把 CapabilitySet 拆成可单独覆盖的能力位（§194）。
func capabilityFlags(caps rpc.CapabilitySet) map[string]bool {
	return map[string]bool{
		"standard":  caps.StandardRPC,
		"archive":   caps.Archive,
		"batch":     caps.Batch,
		"trace":     caps.Trace,
		"debug":     caps.Debug,
		"websocket": caps.WebSocket,
		"grpc":      caps.GRPC,
	}
}

// SaveProviderCapabilities 持久化探测/静态能力。
func (s *Store) SaveProviderCapabilities(ctx context.Context, records []rpc.CapabilityRecord) error {
	if len(records) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, record := range records {
		detail, _ := json.Marshal(map[string]any{
			"max_batch_size":        record.Capability.MaxBatchSize,
			"max_log_block_range":   record.Capability.MaxLogBlockRange,
			"max_trace_block_range": record.Capability.MaxTraceBlockRange,
		})
		source := record.Capability.Source
		if source == "" {
			source = rpc.CapSourceStatic
		}
		for capability, supported := range capabilityFlags(record.Capability) {
			batch.Queue("INSERT INTO rpc_provider_capabilities "+
				"(provider, chain, capability, supported, source, detail, checked_at) "+
				"VALUES ($1,$2,$3,$4,$5,$6,now()) "+
				"ON CONFLICT (provider, chain, capability) DO UPDATE SET "+
				"supported = EXCLUDED.supported, source = EXCLUDED.source, "+
				"detail = EXCLUDED.detail, checked_at = now()",
				strings.ToLower(record.Provider), strings.ToLower(record.Chain), capability, supported, source, detail)
		}
	}
	results := s.Pool.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	return results.Close()
}

// LoadProviderCapabilities 读取管理员覆盖过的能力（admin 优先级最高，§20）。
func (s *Store) LoadProviderCapabilities(ctx context.Context) ([]rpc.CapabilityRecord, error) {
	rows, err := s.Pool.Query(ctx,
		"SELECT provider, chain, capability, supported, source, detail FROM rpc_provider_capabilities "+
			"WHERE source = 'admin'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byKey := map[string]*rpc.CapabilityRecord{}
	for rows.Next() {
		var provider, chainSlug, capability, source string
		var supported bool
		var detail []byte
		if err := rows.Scan(&provider, &chainSlug, &capability, &supported, &source, &detail); err != nil {
			return nil, err
		}
		key := provider + "|" + chainSlug
		record, ok := byKey[key]
		if !ok {
			record = &rpc.CapabilityRecord{Provider: provider, Chain: chainSlug}
			record.Capability.Source = rpc.CapSourceAdmin
			byKey[key] = record
		}
		switch capability {
		case "standard":
			record.Capability.StandardRPC = supported
		case "archive":
			record.Capability.Archive = supported
		case "batch":
			record.Capability.Batch = supported
		case "trace":
			record.Capability.Trace = supported
		case "debug":
			record.Capability.Debug = supported
		case "websocket":
			record.Capability.WebSocket = supported
		case "grpc":
			record.Capability.GRPC = supported
		}
		var limits struct {
			MaxBatchSize       int    `json:"max_batch_size"`
			MaxLogBlockRange   uint64 `json:"max_log_block_range"`
			MaxTraceBlockRange uint64 `json:"max_trace_block_range"`
		}
		if err := json.Unmarshal(detail, &limits); err == nil {
			if limits.MaxBatchSize > 0 {
				record.Capability.MaxBatchSize = limits.MaxBatchSize
			}
			if limits.MaxLogBlockRange > 0 {
				record.Capability.MaxLogBlockRange = limits.MaxLogBlockRange
			}
			if limits.MaxTraceBlockRange > 0 {
				record.Capability.MaxTraceBlockRange = limits.MaxTraceBlockRange
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]rpc.CapabilityRecord, 0, len(byKey))
	for _, record := range byKey {
		out = append(out, *record)
	}
	return out, nil
}

// SetProviderCapability 管理员覆盖单个能力位（§20 admin override）。
func (s *Store) SetProviderCapability(ctx context.Context, provider, chainSlug, capability string, supported bool) error {
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO rpc_provider_capabilities (provider, chain, capability, supported, source, checked_at) "+
			"VALUES ($1,$2,$3,$4,'admin',now()) "+
			"ON CONFLICT (provider, chain, capability) DO UPDATE SET supported = EXCLUDED.supported, "+
			"source = 'admin', checked_at = now()",
		strings.ToLower(provider), strings.ToLower(chainSlug), capability, supported)
	return err
}

// SaveProviderHealth 周期性健康快照（§195：实时指标不写库，周期快照才写）。
func (s *Store) SaveProviderHealth(ctx context.Context, snapshots []rpc.ProviderHealth) error {
	if len(snapshots) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, item := range snapshots {
		batch.Queue("INSERT INTO rpc_provider_health "+
			"(provider, chain, state, success_rate, p50_ms, p95_ms, p99_ms, timeout_rate, head_block, head_lag, "+
			" in_flight, requests, last_error, captured_at) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''),now())",
			strings.ToLower(item.Provider), strings.ToLower(item.Chain), string(item.State),
			item.SuccessRate, item.P50Ms, item.P95Ms, item.P99Ms, item.TimeoutRate,
			int64(item.HeadBlock), int64(item.HeadLag), item.InFlight, item.Requests, item.LastError)
	}
	results := s.Pool.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	return results.Close()
}

// ProviderPricing 读取当前生效计价（§173：价格变动不污染历史报表）。
func (s *Store) ProviderPricing(ctx context.Context, provider, apiType string) (rpc.Pricing, error) {
	var pricing rpc.Pricing
	err := s.Pool.QueryRow(ctx,
		"SELECT credits_per_call, cost_per_call_usd FROM provider_pricing "+
			"WHERE provider=$1 AND api_type=$2 AND effective_from <= now() "+
			"AND (effective_to IS NULL OR effective_to > now()) "+
			"ORDER BY effective_from DESC LIMIT 1",
		strings.ToLower(provider), apiType).Scan(&pricing.CreditsPerCall, &pricing.CostPerCallUSD)
	if err == pgx.ErrNoRows {
		return rpc.Pricing{}, nil
	}
	return pricing, err
}

// RPCUsageSummary 按用途统计 RPC 用量（Admin Dashboard，§144/§174）。
type RPCUsageSummary struct {
	UsageType    string  `json:"usage_type"`
	MethodClass  string  `json:"method_class"`
	HTTPRequests int64   `json:"http_requests"`
	MethodCalls  int64   `json:"method_calls"`
	CacheHits    int64   `json:"cache_hits"`
	Errors       int64   `json:"errors"`
	Credits      float64 `json:"credits"`
	CostUSD      float64 `json:"cost_usd"`
}

// RPCUsage 汇总最近一段时间的 RPC 用量。
func (s *Store) RPCUsage(ctx context.Context, since time.Time) ([]RPCUsageSummary, error) {
	rows, err := s.Pool.Query(ctx,
		"SELECT usage_type, method_class, COUNT(*), COALESCE(SUM(method_calls),0), "+
			"COUNT(*) FILTER (WHERE cache_hit), COUNT(*) FILTER (WHERE status <> 'ok'), "+
			"COALESCE(SUM(credits),0), COALESCE(SUM(estimated_cost_usd),0) "+
			"FROM provider_usage WHERE api_type='rpc' AND request_time >= $1 "+
			"GROUP BY usage_type, method_class ORDER BY SUM(method_calls) DESC", since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RPCUsageSummary{}
	for rows.Next() {
		var item RPCUsageSummary
		if err := rows.Scan(&item.UsageType, &item.MethodClass, &item.HTTPRequests, &item.MethodCalls,
			&item.CacheHits, &item.Errors, &item.Credits, &item.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
