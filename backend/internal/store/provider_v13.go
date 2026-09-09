// V1.3 Count Provider Port：DB 层（§66-§70：registry/capabilities/runtime_profiles/attempts/decisions）。
package store

import (
	"context"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/google/uuid"
)

// UpsertCountProviderRegistry §67。
func (s *Store) UpsertCountProviderRegistry(ctx context.Context, p *model.CountProviderRegistryRow) error {
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO count_provider_registry (provider_id, display_name, enabled, adapter_type, updated_at) "+
			"VALUES ($1,$2,$3,$4, now()) "+
			"ON CONFLICT (provider_id) DO UPDATE SET display_name=$2, enabled=$3, adapter_type=$4, updated_at=now()",
		p.ProviderID, p.DisplayName, p.Enabled, p.AdapterType)
	return err
}

// UpsertCountProviderCapability §68（按 provider+chain+dataset）。
func (s *Store) UpsertCountProviderCapability(ctx context.Context, p *model.CountProviderCapabilityRow) error {
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO count_provider_capabilities (provider_id, chain_id, dataset, enabled, runtime_status, supports_range, range_verified, supports_batch, max_batch_size, semantics, accuracy, reason, updated_at) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12, now()) "+
			"ON CONFLICT (provider_id, chain_id, dataset) DO UPDATE SET enabled=$4, runtime_status=$5, supports_range=$6, range_verified=$7, supports_batch=$8, max_batch_size=$9, semantics=$10, accuracy=$11, reason=$12, updated_at=now()",
		p.ProviderID, p.ChainID, p.Dataset, p.Enabled, p.RuntimeStatus, p.SupportsRange, p.RangeVerified,
		p.SupportsBatch, p.MaxBatchSize, p.Semantics, p.Accuracy, p.Reason)
	return err
}

// UpsertCountProviderRuntimeProfile §57。
func (s *Store) UpsertCountProviderRuntimeProfile(ctx context.Context, p *model.CountProviderRuntimeProfile) error {
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO count_provider_runtime_profiles "+
			"(provider_id, chain_id, dataset, state, active, queue_depth, safe_concurrency, hard_ceiling, success_rps, valid_count_rps, p50_ms, p95_ms, success_rate, rate_limit_rate, timeout_rate, cost_per_1k_counts, requests, success_count, last_error, updated_at) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19, now()) "+
			"ON CONFLICT (provider_id, chain_id, dataset) DO UPDATE SET "+
			"state=$4, active=$5, queue_depth=$6, safe_concurrency=$7, hard_ceiling=$8, success_rps=$9, valid_count_rps=$10, p50_ms=$11, p95_ms=$12, success_rate=$13, rate_limit_rate=$14, timeout_rate=$15, cost_per_1k_counts=$16, requests=$17, success_count=$18, last_error=$19, updated_at=now()",
		p.ProviderID, p.ChainID, p.Dataset, p.State, p.Active, p.QueueDepth, p.SafeConcurrency, p.HardCeiling,
		p.SuccessRPS, p.ValidCountRPS, p.P50Ms, p.P95Ms, p.SuccessRate, p.RateLimitRate, p.TimeoutRate,
		p.CostPer1KCounts, p.Requests, p.SuccessCount, truncateText(p.LastError, 300))
	return err
}

// InsertCountProviderAttempt §69：每次真实 Attempt（异步写入）。
func (s *Store) InsertCountProviderAttempt(ctx context.Context, a *model.CountProviderAttempt) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO count_provider_attempts (id, request_hash, provider_id, credential_slot_id, chain_id, dataset, endpoint, status, error_class, latency_ms, count, requests_cost, credits_cost, usd_cost) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)",
		a.ID, a.RequestHash, a.ProviderID, a.CredentialSlot, a.ChainID, a.Dataset, a.Endpoint,
		a.Status, a.ErrorClass, a.LatencyMS, a.Count, a.RequestsCost, a.CreditsCost, a.USDCost)
	return err
}

// InsertResolutionDecision count_resolution_decisions（§66/§98：resolution + shadow diff 证据）。
func (s *Store) InsertResolutionDecision(ctx context.Context, d *model.ShadowDiff) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO count_resolution_decisions "+
			"(id, request_hash, chain_id, dataset, subject_hash, range_start, range_end, status, reason, provider_id, count, latency_ms, fallback_recommended, old_source, new_source, old_count, new_count, old_initial, new_initial, decision_diff_reason) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)",
		d.ID, d.RequestHash, d.ChainID, d.Dataset, d.SubjectHash, d.RangeStart, d.RangeEnd,
		d.Status, truncateText(d.Reason, 300), d.ProviderID, d.Count, d.LatencyMS, d.FallbackRecommended,
		d.OldSource, d.NewSource, d.OldCount, d.NewCount, d.OldInitial, d.NewInitial,
		truncateText(d.DecisionDiffReason, 300))
	return err
}

// ListCountProviderRuntimeProfiles 全部 runtime 行（API Count Providers UI）。
func (s *Store) ListCountProviderRuntimeProfiles(ctx context.Context) ([]model.CountProviderRuntimeProfile, error) {
	rows, err := s.Pool.Query(ctx,
		"SELECT provider_id, chain_id, dataset, state, active, queue_depth, safe_concurrency, hard_ceiling, success_rps, valid_count_rps, p50_ms, p95_ms, success_rate, rate_limit_rate, timeout_rate, cost_per_1k_counts, requests, success_count, last_error, updated_at "+
			"FROM count_provider_runtime_profiles ORDER BY provider_id, chain_id, dataset")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CountProviderRuntimeProfile
	for rows.Next() {
		var p model.CountProviderRuntimeProfile
		if err := rows.Scan(&p.ProviderID, &p.ChainID, &p.Dataset, &p.State, &p.Active, &p.QueueDepth,
			&p.SafeConcurrency, &p.HardCeiling, &p.SuccessRPS, &p.ValidCountRPS, &p.P50Ms, &p.P95Ms,
			&p.SuccessRate, &p.RateLimitRate, &p.TimeoutRate, &p.CostPer1KCounts, &p.Requests, &p.SuccessCount,
			&p.LastError, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListCountProviderRegistries 已注册 Provider（API 展示）。
func (s *Store) ListCountProviderRegistries(ctx context.Context) ([]model.CountProviderRegistryRow, error) {
	rows, err := s.Pool.Query(ctx, "SELECT provider_id, display_name, enabled, adapter_type, created_at, updated_at FROM count_provider_registry ORDER BY provider_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CountProviderRegistryRow
	for rows.Next() {
		var p model.CountProviderRegistryRow
		if err := rows.Scan(&p.ProviderID, &p.DisplayName, &p.Enabled, &p.AdapterType, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListCountProviderCapabilities 能力行。
func (s *Store) ListCountProviderCapabilities(ctx context.Context) ([]model.CountProviderCapabilityRow, error) {
	rows, err := s.Pool.Query(ctx,
		"SELECT provider_id, chain_id, dataset, enabled, runtime_status, supports_range, range_verified, supports_batch, max_batch_size, semantics, accuracy, reason, updated_at "+
			"FROM count_provider_capabilities ORDER BY provider_id, chain_id, dataset")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CountProviderCapabilityRow
	for rows.Next() {
		var p model.CountProviderCapabilityRow
		if err := rows.Scan(&p.ProviderID, &p.ChainID, &p.Dataset, &p.Enabled, &p.RuntimeStatus, &p.SupportsRange,
			&p.RangeVerified, &p.SupportsBatch, &p.MaxBatchSize, &p.Semantics, &p.Accuracy, &p.Reason, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListResolutionDecisions 最近 resolution/shadow diff（API）。
func (s *Store) ListResolutionDecisions(ctx context.Context, limit int) ([]model.ShadowDiff, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx,
		"SELECT id, request_hash, chain_id, dataset, subject_hash, range_start, range_end, status, reason, provider_id, count, latency_ms, fallback_recommended, old_source, new_source, old_count, new_count, old_initial, new_initial, decision_diff_reason, created_at "+
			"FROM count_resolution_decisions ORDER BY created_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ShadowDiff
	for rows.Next() {
		var d model.ShadowDiff
		if err := rows.Scan(&d.ID, &d.RequestHash, &d.ChainID, &d.Dataset, &d.SubjectHash, &d.RangeStart, &d.RangeEnd,
			&d.Status, &d.Reason, &d.ProviderID, &d.Count, &d.LatencyMS, &d.FallbackRecommended,
			&d.OldSource, &d.NewSource, &d.OldCount, &d.NewCount, &d.OldInitial, &d.NewInitial,
			&d.DecisionDiffReason, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

var _ = time.Now
