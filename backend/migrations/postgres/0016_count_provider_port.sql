-- Ankr 极速采集引擎 V1.3：Count Oracle Provider Port（§66-§70/§51/§57/§98）。
-- 不删除 V1.2 表；优先 ALTER + 新表，保证快速回滚（§99）。

-- 0) Calibration 增加 Provider 维度（§51：不能让 Provider B 的偏差污染 Provider C）。
ALTER TABLE count_oracle_calibration_samples ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'chainbase';
CREATE INDEX IF NOT EXISTS idx_calib_samples_provider ON count_oracle_calibration_samples (provider, chain, dataset, created_at DESC);

ALTER TABLE count_oracle_calibration DROP CONSTRAINT IF EXISTS count_oracle_calibration_pkey;
ALTER TABLE count_oracle_calibration ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'chainbase';
ALTER TABLE count_oracle_calibration ADD PRIMARY KEY (provider, chain, dataset);

-- 1) Provider Registry（§67）。
CREATE TABLE IF NOT EXISTS count_provider_registry (
    provider_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    adapter_type TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2) Provider Capabilities（§68：细到 chain+dataset）。
CREATE TABLE IF NOT EXISTS count_provider_capabilities (
    provider_id TEXT NOT NULL,
    chain_id BIGINT NOT NULL DEFAULT 0,
    dataset TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    runtime_status TEXT NOT NULL DEFAULT 'AVAILABLE',
    supports_range BOOLEAN NOT NULL DEFAULT false,
    range_verified BOOLEAN NOT NULL DEFAULT false,
    supports_batch BOOLEAN NOT NULL DEFAULT false,
    max_batch_size INT NOT NULL DEFAULT 0,
    semantics TEXT NOT NULL DEFAULT 'TOTAL_ROWS',
    accuracy TEXT NOT NULL DEFAULT 'EXACT',
    reason TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_id, chain_id, dataset)
);

-- 3) Runtime Profiles（§57：Provider/Chain/Dataset 实时状态，worker 心跳写入）。
CREATE TABLE IF NOT EXISTS count_provider_runtime_profiles (
    provider_id TEXT NOT NULL,
    chain_id BIGINT NOT NULL DEFAULT 0,
    dataset TEXT NOT NULL DEFAULT '_all',
    state TEXT NOT NULL DEFAULT 'UNKNOWN',
    active INT NOT NULL DEFAULT 0,
    queue_depth INT NOT NULL DEFAULT 0,
    safe_concurrency INT NOT NULL DEFAULT 0,
    hard_ceiling INT NOT NULL DEFAULT 0,
    success_rps DOUBLE PRECISION NOT NULL DEFAULT 0,
    valid_count_rps DOUBLE PRECISION NOT NULL DEFAULT 0,
    p50_ms INT NOT NULL DEFAULT 0,
    p95_ms INT NOT NULL DEFAULT 0,
    success_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    rate_limit_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    timeout_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    cost_per_1k_counts DOUBLE PRECISION NOT NULL DEFAULT 0,
    requests BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_id, chain_id, dataset)
);

-- 4) Provider Attempts（§69：每次真实 Attempt；不含任何 secret）。
CREATE TABLE IF NOT EXISTS count_provider_attempts (
    id UUID PRIMARY KEY,
    request_hash TEXT NOT NULL DEFAULT '',
    provider_id TEXT NOT NULL,
    credential_slot_id TEXT NOT NULL DEFAULT '',
    chain_id BIGINT NOT NULL DEFAULT 0,
    dataset TEXT NOT NULL DEFAULT '',
    endpoint TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    error_class TEXT NOT NULL DEFAULT '',
    latency_ms INT NOT NULL DEFAULT 0,
    count BIGINT NOT NULL DEFAULT -1,
    requests_cost BIGINT NOT NULL DEFAULT 0,
    credits_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    usd_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_count_provider_attempts_lookup ON count_provider_attempts (provider_id, dataset, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_count_provider_attempts_hash ON count_provider_attempts (request_hash);

-- 5) Resolution Decisions（§98：shadow diff 放量 Gate 证据；old/new 对照）。
CREATE TABLE IF NOT EXISTS count_resolution_decisions (
    id UUID PRIMARY KEY,
    request_hash TEXT NOT NULL DEFAULT '',
    chain_id BIGINT NOT NULL DEFAULT 0,
    dataset TEXT NOT NULL DEFAULT '',
    subject_hash TEXT NOT NULL DEFAULT '',
    range_start BIGINT NOT NULL DEFAULT 0,
    range_end BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    provider_id TEXT NOT NULL DEFAULT '',
    count BIGINT NOT NULL DEFAULT -1,
    latency_ms INT NOT NULL DEFAULT 0,
    fallback_recommended BOOLEAN NOT NULL DEFAULT false,
    old_source TEXT NOT NULL DEFAULT '',
    new_source TEXT NOT NULL DEFAULT '',
    old_count BIGINT NOT NULL DEFAULT -1,
    new_count BIGINT NOT NULL DEFAULT -1,
    old_initial INT NOT NULL DEFAULT 0,
    new_initial INT NOT NULL DEFAULT 0,
    decision_diff_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_count_resolution_decisions_time ON count_resolution_decisions (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_count_resolution_decisions_hash ON count_resolution_decisions (request_hash);
