// V1.2 Store：Count Oracle 观测/校准/决策日志 + Scheduler 状态 + 能力矩阵 + 全局并发画像。
package store

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// ---- Count Oracle Observations（§87）----

func (s *Store) InsertCountObservation(ctx context.Context, o *model.CountObservation) error {
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	var jobID any
	if o.JobID != "" {
		jobID = o.JobID
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO count_oracle_observations "+
		"(id, provider, chain, dataset, address_hash, from_block, to_block, raw_count, adjusted_count, latency_ms, cache_hit, confidence, status, error_class, job_id) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)",
		o.ID, o.Provider, o.Chain, o.Dataset, o.AddressHash, o.FromBlock, o.ToBlock,
		o.RawCount, o.AdjustedCount, o.LatencyMS, o.CacheHit, o.Confidence, o.Status, o.ErrorClass, jobID)
	return err
}

func (s *Store) ListCountObservations(ctx context.Context, chain, dataset string, hours, limit int) ([]model.CountObservation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	q := "SELECT id, provider, chain, dataset, address_hash, from_block, to_block, raw_count, adjusted_count, latency_ms, cache_hit, confidence, status, error_class, job_id, created_at " +
		"FROM count_oracle_observations WHERE created_at > now() - make_interval(hours => $1)"
	args := []any{hours}
	if chain != "" {
		args = append(args, chain)
		q += " AND chain=$" + strconv.Itoa(len(args))
	}
	if dataset != "" {
		args = append(args, dataset)
		q += " AND dataset=$" + strconv.Itoa(len(args))
	}
	args = append(args, limit)
	q += " ORDER BY created_at DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CountObservation
	for rows.Next() {
		var o model.CountObservation
		var jobID pgtype.UUID
		if err := rows.Scan(&o.ID, &o.Provider, &o.Chain, &o.Dataset, &o.AddressHash, &o.FromBlock, &o.ToBlock,
			&o.RawCount, &o.AdjustedCount, &o.LatencyMS, &o.CacheHit, &o.Confidence, &o.Status, &o.ErrorClass, &jobID, &o.CreatedAt); err != nil {
			return nil, err
		}
		if jobID.Valid {
			o.JobID = jobID.String()
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ---- Calibration（§22/§23）----

func (s *Store) InsertCalibrationSample(ctx context.Context, c *model.CalibrationSample) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Provider == "" {
		c.Provider = "chainbase"
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO count_oracle_calibration_samples "+
		"(id, provider, chain, dataset, address_hash, predicted_count, actual_count) VALUES ($1,$2,$3,$4,$5,$6,$7)",
		c.ID, c.Provider, c.Chain, c.Dataset, c.AddressHash, c.PredictedCount, c.ActualCount)
	return err
}

// RefreshCalibration 用最近样本重算 provider+chain+dataset 聚合（§23/§51；只用于 Planning）。
func (s *Store) RefreshCalibration(ctx context.Context, provider, chain, dataset string, sampleLimit int) (*model.CountCalibration, error) {
	if provider == "" {
		provider = "chainbase"
	}
	if sampleLimit <= 0 {
		sampleLimit = 5000
	}
	var cal model.CountCalibration
	cal.Provider, cal.Chain, cal.Dataset = provider, chain, dataset
	err := s.Pool.QueryRow(ctx,
		"SELECT COUNT(*), "+
			"COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY actual::float8 / NULLIF(predicted,0)), 1), "+
			"COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY actual::float8 / NULLIF(predicted,0)), 1), "+
			"COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY ABS(actual - predicted)::float8), 0), "+
			"COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY ABS(actual - predicted)::float8), 0), "+
			"COUNT(*) FILTER (WHERE predicted = 0 AND actual > 0) "+
			"FROM (SELECT predicted_count AS predicted, actual_count AS actual FROM count_oracle_calibration_samples "+
			"WHERE provider=$1 AND chain=$2 AND dataset=$3 ORDER BY created_at DESC LIMIT $4) t",
		provider, chain, dataset, sampleLimit).
		Scan(&cal.SampleCount, &cal.RatioP50, &cal.RatioP95, &cal.AbsoluteErrorP50, &cal.AbsoluteErrorP95, &cal.FalseZeroCount)
	if err != nil {
		return nil, err
	}
	if _, err := s.Pool.Exec(ctx, "INSERT INTO count_oracle_calibration (provider, chain, dataset, sample_count, ratio_p50, ratio_p95, absolute_error_p50, absolute_error_p95, false_zero_count, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now()) "+
		"ON CONFLICT (provider, chain, dataset) DO UPDATE SET sample_count=$4, ratio_p50=$5, ratio_p95=$6, absolute_error_p50=$7, absolute_error_p95=$8, false_zero_count=$9, updated_at=now()",
		cal.Provider, cal.Chain, cal.Dataset, cal.SampleCount, cal.RatioP50, cal.RatioP95, cal.AbsoluteErrorP50, cal.AbsoluteErrorP95, cal.FalseZeroCount); err != nil {
		return nil, err
	}
	cal.UpdatedAt = time.Now()
	return &cal, nil
}

// GetCalibration 读取聚合（不存在 → nil, nil）。
func (s *Store) GetCalibration(ctx context.Context, provider, chain, dataset string) (*model.CountCalibration, error) {
	if provider == "" {
		provider = "chainbase"
	}
	var cal model.CountCalibration
	err := s.Pool.QueryRow(ctx, "SELECT provider, chain, dataset, sample_count, ratio_p50, ratio_p95, absolute_error_p50, absolute_error_p95, false_zero_count, updated_at "+
		"FROM count_oracle_calibration WHERE provider=$1 AND chain=$2 AND dataset=$3", provider, chain, dataset).
		Scan(&cal.Provider, &cal.Chain, &cal.Dataset, &cal.SampleCount, &cal.RatioP50, &cal.RatioP95, &cal.AbsoluteErrorP50, &cal.AbsoluteErrorP95, &cal.FalseZeroCount, &cal.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cal, nil
}

// ListCalibrations 全部聚合（API 展示）。
func (s *Store) ListCalibrations(ctx context.Context) ([]model.CountCalibration, error) {
	rows, err := s.Pool.Query(ctx, "SELECT provider, chain, dataset, sample_count, ratio_p50, ratio_p95, absolute_error_p50, absolute_error_p95, false_zero_count, updated_at "+
		"FROM count_oracle_calibration ORDER BY provider, chain, dataset")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CountCalibration
	for rows.Next() {
		var cal model.CountCalibration
		if err := rows.Scan(&cal.Provider, &cal.Chain, &cal.Dataset, &cal.SampleCount, &cal.RatioP50, &cal.RatioP95, &cal.AbsoluteErrorP50, &cal.AbsoluteErrorP95, &cal.FalseZeroCount, &cal.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, cal)
	}
	return out, rows.Err()
}

// ---- Planning Decision Log（§88）----

func (s *Store) InsertPlanningDecision(ctx context.Context, d *model.PlanningDecision) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO download_planning_decisions "+
		"(id, job_id, subject_id, dataset, count_source, predicted_rows, predicted_pages, profile_hit, preferred_shards, initial_shards, provider_safe_slots, global_active_slots, decision_reason) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)",
		d.ID, d.JobID, d.SubjectID, d.Dataset, d.CountSource, d.PredictedRows, d.PredictedPages, d.ProfileHit,
		d.PreferredShards, d.InitialShards, d.ProviderSafeSlots, d.GlobalActiveSlots, d.DecisionReason)
	return err
}

func (s *Store) ListPlanningDecisions(ctx context.Context, jobID string, limit int) ([]model.PlanningDecision, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := "SELECT id, job_id, subject_id, dataset, count_source, predicted_rows, predicted_pages, profile_hit, preferred_shards, initial_shards, provider_safe_slots, global_active_slots, decision_reason, created_at " +
		"FROM download_planning_decisions"
	args := []any{}
	if jobID != "" {
		q += " WHERE job_id=$1"
		args = append(args, jobID)
	}
	args = append(args, limit)
	q += " ORDER BY created_at DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PlanningDecision
	for rows.Next() {
		var d model.PlanningDecision
		var jobIDv, subjID pgtype.UUID
		if err := rows.Scan(&d.ID, &jobIDv, &subjID, &d.Dataset, &d.CountSource, &d.PredictedRows, &d.PredictedPages,
			&d.ProfileHit, &d.PreferredShards, &d.InitialShards, &d.ProviderSafeSlots, &d.GlobalActiveSlots, &d.DecisionReason, &d.CreatedAt); err != nil {
			return nil, err
		}
		if jobIDv.Valid {
			d.JobID = jobIDv.String()
		}
		if subjID.Valid {
			d.SubjectID = subjID.String()
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- Chain Capability Registry（§61）----

func (s *Store) GetChainCapability(ctx context.Context, chain string) (*model.ChainCapability, error) {
	var c model.ChainCapability
	err := s.Pool.QueryRow(ctx, "SELECT chain, evm_chain_id, ankr_alias, chain_type, native_symbol, chainbase_tx_count, chainbase_token_count, ankr_transactions, ankr_token_transfers, range_supported, enabled, updated_at "+
		"FROM chain_capabilities WHERE chain=$1", chain).
		Scan(&c.Chain, &c.EVMChainID, &c.AnkrAlias, &c.ChainType, &c.NativeSymbol, &c.ChainbaseTxCount, &c.ChainbaseTokenCount,
			&c.AnkrTransactions, &c.AnkrTokenTransfers, &c.RangeSupported, &c.Enabled, &c.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) ListChainCapabilities(ctx context.Context) ([]model.ChainCapability, error) {
	rows, err := s.Pool.Query(ctx, "SELECT chain, evm_chain_id, ankr_alias, chain_type, native_symbol, chainbase_tx_count, chainbase_token_count, ankr_transactions, ankr_token_transfers, range_supported, enabled, updated_at "+
		"FROM chain_capabilities ORDER BY evm_chain_id NULLS LAST")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ChainCapability
	for rows.Next() {
		var c model.ChainCapability
		if err := rows.Scan(&c.Chain, &c.EVMChainID, &c.AnkrAlias, &c.ChainType, &c.NativeSymbol, &c.ChainbaseTxCount, &c.ChainbaseTokenCount,
			&c.AnkrTransactions, &c.AnkrTokenTransfers, &c.RangeSupported, &c.Enabled, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- Scheduler / Oracle 心跳（§80/§82）----

// UpsertSchedulerState 写 ankr_scheduler_state 心跳。
//
// WAL 优化（2026-09-17）：**有意不加值变化守卫**，理由如下（也写在
// acceptance-evidence/.../wal-reduction-20260917.md 的“有意不改”一节）：
//
//   - worker_id 的取值是 dataserver-<pid>，即**每个 worker 进程一份新行**；线上实测
//     ankr_scheduler_state 已有 **397 行 / 397 个不同 worker_id**，其中只有当前进程那一行是活的。
//   - 读取方 GetSchedulerState 用的是 ORDER BY updated_at DESC LIMIT 1（见本文件下方），
//     它的正确性**依赖当前 worker 的 updated_at 持续前进**。一旦改成“值不变就不写、updated_at
//     只在变化时推进”，当前 worker 长时间无变化时，这个查询会选到**上一个已退出 worker 的陈旧行**，
//     调度状态会被读错。
//   - 而这张表的 WAL 占比极小：实测 44 次 UPDATE/分钟、行宽约 400 B ≈ **18 KB/min**，
//     相对当时基线 15.6 MB/min 约 **0.1%**。
//
// 用 0.1% 的收益去换“调度状态可能读错”的风险不划算。若确实要优化，正确做法是先让读取端
// 变成确定性定位（例如按 worker_id 查询），那是语义变更，需要单独批准。
func (s *Store) UpsertSchedulerState(ctx context.Context, st *model.SchedulerState) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_scheduler_state (worker_id, configured_initial_concurrency, configured_max_concurrency, global_hard_ceiling, ready_queue_enabled, ready_queue_shadow, ready_queue_mode, auto_tune_enabled, controller_mode, active_slots, weighted_slots, concurrency_target, hard_ceiling, ramp_level, ramp_phase, control_state, control_reason, goodput, ttfb_p50_ms, mem_used_bytes, spool_used_bytes, time_to_full_safe_concurrency_ms, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,now()) "+
		"ON CONFLICT (worker_id) DO UPDATE SET configured_initial_concurrency=$2, configured_max_concurrency=$3, global_hard_ceiling=$4, ready_queue_enabled=$5, ready_queue_shadow=$6, ready_queue_mode=$7, auto_tune_enabled=$8, controller_mode=$9, active_slots=$10, weighted_slots=$11, concurrency_target=$12, hard_ceiling=$13, ramp_level=$14, ramp_phase=$15, control_state=$16, control_reason=$17, goodput=$18, ttfb_p50_ms=$19, mem_used_bytes=$20, spool_used_bytes=$21, time_to_full_safe_concurrency_ms=$22, updated_at=now()",
		st.WorkerID, st.ConfiguredInitialConcurrency, st.ConfiguredMaxConcurrency, st.GlobalHardCeiling, st.ReadyQueueEnabled, st.ReadyQueueShadow, st.ReadyQueueMode, st.AutoTuneEnabled, st.ControllerMode,
		st.ActiveSlots, st.WeightedSlots, st.ConcurrencyTarget, st.HardCeiling, st.RampLevel, st.RampPhase,
		st.ControlState, st.ControlReason, st.Goodput, st.TTFBP50Ms, st.MemUsedBytes, st.SpoolUsedBytes, st.TimeToFullSafeConcurrencyMs)
	return err
}

func (s *Store) GetSchedulerState(ctx context.Context) (*model.SchedulerState, error) {
	var st model.SchedulerState
	err := s.Pool.QueryRow(ctx, "SELECT worker_id, configured_initial_concurrency, configured_max_concurrency, global_hard_ceiling, ready_queue_enabled, ready_queue_shadow, ready_queue_mode, auto_tune_enabled, controller_mode, active_slots, weighted_slots, concurrency_target, hard_ceiling, ramp_level, ramp_phase, control_state, control_reason, goodput, ttfb_p50_ms, mem_used_bytes, spool_used_bytes, time_to_full_safe_concurrency_ms, updated_at "+
		"FROM ankr_scheduler_state ORDER BY updated_at DESC LIMIT 1").
		Scan(&st.WorkerID, &st.ConfiguredInitialConcurrency, &st.ConfiguredMaxConcurrency, &st.GlobalHardCeiling, &st.ReadyQueueEnabled, &st.ReadyQueueShadow, &st.ReadyQueueMode, &st.AutoTuneEnabled, &st.ControllerMode,
			&st.ActiveSlots, &st.WeightedSlots, &st.ConcurrencyTarget, &st.HardCeiling, &st.RampLevel, &st.RampPhase,
			&st.ControlState, &st.ControlReason, &st.Goodput, &st.TTFBP50Ms, &st.MemUsedBytes, &st.SpoolUsedBytes, &st.TimeToFullSafeConcurrencyMs, &st.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// UpsertCountOracleHealth 写 count_oracle_status（countoracle 5 秒心跳的一部分，
// internal/countoracle/service.go:870-873，实测 12 次 UPDATE/分钟）。
//
// WAL 优化（2026-09-17）：加 WHERE 守卫，只有业务值真的变化时才写新版本行。
// 读取方 GetCountOracleHealth 按 provider 精确查询，不依赖写入次数或 updated_at 的新鲜度，
// 因此“值没变就不写”对它是不可见的。
// updated_at 有意不参与比较（它在 SET 里被赋成 now()，参与比较会让守卫恒真、完全失效）。
func (s *Store) UpsertCountOracleHealth(ctx context.Context, h *model.CountOracleHealth) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO count_oracle_status (provider, circuit_state, requests, success_count, p50_ms, p95_ms, success_rate, cache_hit_rate, quota_exhausted, last_error, false_zero_count, prediction_error_p50, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now()) "+
		"ON CONFLICT (provider) DO UPDATE SET circuit_state=$2, requests=$3, success_count=$4, p50_ms=$5, p95_ms=$6, success_rate=$7, cache_hit_rate=$8, quota_exhausted=$9, last_error=$10, false_zero_count=$11, prediction_error_p50=$12, updated_at=now() "+
		"WHERE count_oracle_status.circuit_state IS DISTINCT FROM EXCLUDED.circuit_state "+
		"OR count_oracle_status.requests IS DISTINCT FROM EXCLUDED.requests "+
		"OR count_oracle_status.success_count IS DISTINCT FROM EXCLUDED.success_count "+
		"OR count_oracle_status.p50_ms IS DISTINCT FROM EXCLUDED.p50_ms "+
		"OR count_oracle_status.p95_ms IS DISTINCT FROM EXCLUDED.p95_ms "+
		"OR count_oracle_status.success_rate IS DISTINCT FROM EXCLUDED.success_rate "+
		"OR count_oracle_status.cache_hit_rate IS DISTINCT FROM EXCLUDED.cache_hit_rate "+
		"OR count_oracle_status.quota_exhausted IS DISTINCT FROM EXCLUDED.quota_exhausted "+
		"OR count_oracle_status.last_error IS DISTINCT FROM EXCLUDED.last_error "+
		"OR count_oracle_status.false_zero_count IS DISTINCT FROM EXCLUDED.false_zero_count "+
		"OR count_oracle_status.prediction_error_p50 IS DISTINCT FROM EXCLUDED.prediction_error_p50",
		h.Provider, h.CircuitState, h.Requests, h.SuccessCount, h.P50Ms, h.P95Ms, h.SuccessRate, h.CacheHitRate, h.QuotaExhausted, h.LastError, h.FalseZeroCount, h.PredictionErrorP50)
	return err
}

func (s *Store) GetCountOracleHealth(ctx context.Context, providerName string) (*model.CountOracleHealth, error) {
	var h model.CountOracleHealth
	err := s.Pool.QueryRow(ctx, "SELECT provider, circuit_state, requests, success_count, p50_ms, p95_ms, success_rate, cache_hit_rate, quota_exhausted, last_error, false_zero_count, prediction_error_p50, updated_at "+
		"FROM count_oracle_status WHERE provider=$1", providerName).
		Scan(&h.Provider, &h.CircuitState, &h.Requests, &h.SuccessCount, &h.P50Ms, &h.P95Ms, &h.SuccessRate, &h.CacheHitRate, &h.QuotaExhausted, &h.LastError, &h.FalseZeroCount, &h.PredictionErrorP50, &h.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// ---- Global Concurrency Profile（§42，P1 学习目标表）----

func (s *Store) UpsertGlobalConcurrencyProfile(ctx context.Context, p *model.GlobalConcurrencyProfile) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_global_concurrency_profiles (chain_mix, dataset_mix, preferred_global_concurrency, p50_ms, p95_ms, goodput, retry_rate, timeout_rate, rate_limit_rate, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now()) "+
		"ON CONFLICT (chain_mix, dataset_mix) DO UPDATE SET preferred_global_concurrency=$3, p50_ms=$4, p95_ms=$5, goodput=$6, retry_rate=$7, timeout_rate=$8, rate_limit_rate=$9, updated_at=now()",
		p.ChainMix, p.DatasetMix, p.PreferredGlobalConcurrency, p.P50Ms, p.P95Ms, p.Goodput, p.RetryRate, p.TimeoutRate, p.RateLimitRate)
	return err
}

func (s *Store) GetGlobalConcurrencyProfile(ctx context.Context, chainMix, datasetMix string) (*model.GlobalConcurrencyProfile, error) {
	var p model.GlobalConcurrencyProfile
	err := s.Pool.QueryRow(ctx, "SELECT chain_mix, dataset_mix, preferred_global_concurrency, p50_ms, p95_ms, goodput, retry_rate, timeout_rate, rate_limit_rate, updated_at "+
		"FROM ankr_global_concurrency_profiles WHERE chain_mix=$1 AND dataset_mix=$2", chainMix, datasetMix).
		Scan(&p.ChainMix, &p.DatasetMix, &p.PreferredGlobalConcurrency, &p.P50Ms, &p.P95Ms, &p.Goodput, &p.RetryRate, &p.TimeoutRate, &p.RateLimitRate, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
