-- Ankr 极速采集引擎 V1.2：Chainbase Count Oracle + 600 全局并发调度 + 多地址极速采集。
-- 文档 V1.2 §86-§88/§23/§42/§55/§56/§61。

-- 1) Provider 用量拆分 TTFB / Body（§56：ankr_ttfb_seconds / ankr_body_download_seconds 落库）。
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS ttfb_ms INT NOT NULL DEFAULT 0;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS body_ms INT NOT NULL DEFAULT 0;

-- 2) Benchmark Provider Latency Context + Normalization（§55/§103-§105）。
ALTER TABLE ankr_benchmark_runs ADD COLUMN IF NOT EXISTS provider_latency_p50_ms INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_benchmark_runs ADD COLUMN IF NOT EXISTS provider_latency_p95_ms INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_benchmark_runs ADD COLUMN IF NOT EXISTS provider_ttfb_p50_ms INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_benchmark_runs ADD COLUMN IF NOT EXISTS provider_ttfb_p95_ms INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_benchmark_runs ADD COLUMN IF NOT EXISTS provider_body_p50_ms INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_benchmark_runs ADD COLUMN IF NOT EXISTS provider_body_p95_ms INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_benchmark_runs ADD COLUMN IF NOT EXISTS normalized_wall_clock_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE ankr_benchmark_runs ADD COLUMN IF NOT EXISTS provider_window_ratio DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE ankr_benchmark_runs ADD COLUMN IF NOT EXISTS excluded_reason TEXT NOT NULL DEFAULT '';

-- 3) Count Oracle Observations（§87）。
CREATE TABLE IF NOT EXISTS count_oracle_observations (
    id UUID PRIMARY KEY,
    provider TEXT NOT NULL DEFAULT 'chainbase',
    chain TEXT NOT NULL,
    dataset TEXT NOT NULL,
    address_hash TEXT NOT NULL DEFAULT '',
    from_block BIGINT NOT NULL DEFAULT 0,
    to_block BIGINT NOT NULL DEFAULT 0,
    raw_count BIGINT NOT NULL DEFAULT -1,
    adjusted_count BIGINT NOT NULL DEFAULT -1,
    latency_ms INT NOT NULL DEFAULT 0,
    cache_hit BOOLEAN NOT NULL DEFAULT false,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT '',
    error_class TEXT NOT NULL DEFAULT '',
    job_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_count_obs_lookup ON count_oracle_observations (chain, dataset, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_count_obs_job ON count_oracle_observations (job_id);

-- 4) Calibration samples（§23 聚合来源：每个任务完成后 predicted vs actual 样本）。
CREATE TABLE IF NOT EXISTS count_oracle_calibration_samples (
    id UUID PRIMARY KEY,
    chain TEXT NOT NULL,
    dataset TEXT NOT NULL,
    address_hash TEXT NOT NULL DEFAULT '',
    predicted_count BIGINT NOT NULL DEFAULT 0,
    actual_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_calib_samples_lookup ON count_oracle_calibration_samples (chain, dataset, created_at DESC);

-- 5) Count Calibration 聚合（§22/§23：chain + dataset 独立维护，只用于 Planning）。
CREATE TABLE IF NOT EXISTS count_oracle_calibration (
    chain TEXT NOT NULL,
    dataset TEXT NOT NULL,
    sample_count INT NOT NULL DEFAULT 0,
    ratio_p50 DOUBLE PRECISION NOT NULL DEFAULT 1,
    ratio_p95 DOUBLE PRECISION NOT NULL DEFAULT 1,
    absolute_error_p50 DOUBLE PRECISION NOT NULL DEFAULT 0,
    absolute_error_p95 DOUBLE PRECISION NOT NULL DEFAULT 0,
    false_zero_count INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, dataset)
);

-- 6) Planner Decision Log（§88：为什么该地址 initial=n shards）。
CREATE TABLE IF NOT EXISTS download_planning_decisions (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL,
    subject_id UUID NOT NULL,
    dataset TEXT NOT NULL,
    count_source TEXT NOT NULL DEFAULT '',
    predicted_rows BIGINT NOT NULL DEFAULT -1,
    predicted_pages BIGINT NOT NULL DEFAULT -1,
    profile_hit BOOLEAN NOT NULL DEFAULT false,
    preferred_shards INT NOT NULL DEFAULT 0,
    initial_shards INT NOT NULL DEFAULT 0,
    provider_safe_slots INT NOT NULL DEFAULT 0,
    global_active_slots INT NOT NULL DEFAULT 0,
    decision_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_planning_decisions_job ON download_planning_decisions (job_id);
CREATE INDEX IF NOT EXISTS idx_planning_decisions_time ON download_planning_decisions (created_at DESC);

-- 7) Global Concurrency Profile（§42：chain mix + dataset mix 聚合）。
CREATE TABLE IF NOT EXISTS ankr_global_concurrency_profiles (
    chain_mix TEXT NOT NULL DEFAULT 'all',
    dataset_mix TEXT NOT NULL DEFAULT 'all',
    preferred_global_concurrency INT NOT NULL DEFAULT 0,
    p50_ms INT NOT NULL DEFAULT 0,
    p95_ms INT NOT NULL DEFAULT 0,
    goodput DOUBLE PRECISION NOT NULL DEFAULT 0,
    retry_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    timeout_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    rate_limit_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_mix, dataset_mix)
);

-- 8) Scheduler 实时状态（worker 心跳 → API / 前端 Global Concurrency Card）。
CREATE TABLE IF NOT EXISTS ankr_scheduler_state (
    worker_id TEXT PRIMARY KEY,
    active_slots INT NOT NULL DEFAULT 0,
    weighted_slots INT NOT NULL DEFAULT 0,
    concurrency_target INT NOT NULL DEFAULT 0,
    hard_ceiling INT NOT NULL DEFAULT 600,
    ramp_level INT NOT NULL DEFAULT 0,
    ramp_phase TEXT NOT NULL DEFAULT '',
    control_state TEXT NOT NULL DEFAULT 'NORMAL',
    control_reason TEXT NOT NULL DEFAULT '',
    goodput DOUBLE PRECISION NOT NULL DEFAULT 0,
    ttfb_p50_ms INT NOT NULL DEFAULT 0,
    mem_used_bytes BIGINT NOT NULL DEFAULT 0,
    spool_used_bytes BIGINT NOT NULL DEFAULT 0,
    time_to_full_safe_concurrency_ms BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 9) Count Oracle Health 快照（worker 周期性写入 → API Control Center）。
CREATE TABLE IF NOT EXISTS count_oracle_status (
    provider TEXT PRIMARY KEY,
    circuit_state TEXT NOT NULL DEFAULT 'CLOSED',
    requests BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    p50_ms INT NOT NULL DEFAULT 0,
    p95_ms INT NOT NULL DEFAULT 0,
    success_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    cache_hit_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    quota_exhausted BOOLEAN NOT NULL DEFAULT false,
    last_error TEXT NOT NULL DEFAULT '',
    false_zero_count INT NOT NULL DEFAULT 0,
    prediction_error_p50 DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 10) Chain Capability Registry（§61：Chainbase / Ankr 能力矩阵，首期 7 链）。
CREATE TABLE IF NOT EXISTS chain_capabilities (
    chain TEXT PRIMARY KEY,
    evm_chain_id BIGINT NOT NULL DEFAULT 0,
    ankr_alias TEXT NOT NULL DEFAULT '',
    chainbase_tx_count BOOLEAN NOT NULL DEFAULT false,
    chainbase_token_count BOOLEAN NOT NULL DEFAULT false,
    ankr_transactions BOOLEAN NOT NULL DEFAULT false,
    ankr_token_transfers BOOLEAN NOT NULL DEFAULT false,
    range_supported BOOLEAN NOT NULL DEFAULT false,
    enabled BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO chain_capabilities (chain, evm_chain_id, ankr_alias, chainbase_tx_count, chainbase_token_count, ankr_transactions, ankr_token_transfers, range_supported, enabled) VALUES
    ('bsc', 56, 'bsc', true, true, true, true, true, true),
    ('eth', 1, 'eth', true, true, true, true, true, true),
    ('polygon', 137, 'polygon', true, true, true, true, true, true),
    ('arbitrum', 42161, 'arbitrum', true, true, true, true, true, true),
    ('optimism', 10, 'optimism', true, true, true, true, true, true),
    ('base', 8453, 'base', true, true, true, true, true, true),
    ('avalanche', 43114, 'avalanche', true, true, true, true, true, true)
ON CONFLICT (chain) DO UPDATE SET
    evm_chain_id = EXCLUDED.evm_chain_id,
    ankr_alias = EXCLUDED.ankr_alias,
    chainbase_tx_count = EXCLUDED.chainbase_tx_count,
    chainbase_token_count = EXCLUDED.chainbase_token_count,
    ankr_transactions = EXCLUDED.ankr_transactions,
    ankr_token_transfers = EXCLUDED.ankr_token_transfers,
    range_supported = EXCLUDED.range_supported,
    enabled = EXCLUDED.enabled,
    updated_at = now();
