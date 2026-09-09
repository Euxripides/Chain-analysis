-- Ankr 极速采集引擎 V1.4：Transactions Multi-Range Certification + Global Ready Queue + 全局 Sweet Spot（§14/§33/§44/§53/§56）。
-- 不删除任何 V1.1/V1.2/V1.3 表（§54）。

-- 1) Shard：Lease Epoch（§33 CAS 防 split-brain）+ Ready Queue 排序字段（§8/§9）
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS lease_epoch BIGINT NOT NULL DEFAULT 0;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS work_score DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS tail_boost BOOLEAN NOT NULL DEFAULT false;

-- 2) Range Certification（§14）：Range 语义必须实机认证后才允许 Sharding。
CREATE TABLE IF NOT EXISTS ankr_range_certifications (
    chain TEXT NOT NULL,
    dataset TEXT NOT NULL,
    range_supported BOOLEAN NOT NULL DEFAULT false,
    range_verified BOOLEAN NOT NULL DEFAULT false,
    reference_run TEXT NOT NULL DEFAULT '',
    missing BIGINT NOT NULL DEFAULT 0,
    real_duplicates BIGINT NOT NULL DEFAULT 0,
    boundary_violations BIGINT NOT NULL DEFAULT 0,
    union_rows BIGINT NOT NULL DEFAULT -1,
    verification_version INT NOT NULL DEFAULT 1,
    verified_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, dataset)
);

-- 3) Global Fetch Profiles（§44）：并发 Sweet Spot；旧样本按衰减权重更新。
CREATE TABLE IF NOT EXISTS ankr_global_fetch_profiles (
    chain TEXT NOT NULL DEFAULT 'all',
    dataset_mix TEXT NOT NULL DEFAULT 'all',
    page_size_class INT NOT NULL DEFAULT 10000,
    safe_concurrency INT NOT NULL DEFAULT 0,
    best_goodput DOUBLE PRECISION NOT NULL DEFAULT 0,
    p50_ms INT NOT NULL DEFAULT 0,
    p95_ms INT NOT NULL DEFAULT 0,
    rate_limit_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    timeout_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    retry_amp DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_wire_bytes BIGINT NOT NULL DEFAULT 0,
    avg_decoded_bytes BIGINT NOT NULL DEFAULT 0,
    samples INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, dataset_mix, page_size_class)
);

-- 4) Fetch Concurrency Runs（§53）：每一级并发的实验记录。
CREATE TABLE IF NOT EXISTS ankr_fetch_concurrency_runs (
    id UUID PRIMARY KEY,
    run_id TEXT NOT NULL DEFAULT '',
    chain TEXT NOT NULL DEFAULT '',
    dataset_mix TEXT NOT NULL DEFAULT '',
    concurrency_target INT NOT NULL DEFAULT 0,
    wall_ms BIGINT NOT NULL DEFAULT 0,
    rows BIGINT NOT NULL DEFAULT 0,
    unique_rows BIGINT NOT NULL DEFAULT 0,
    requests INT NOT NULL DEFAULT 0,
    credits DOUBLE PRECISION NOT NULL DEFAULT 0,
    ttfb_p50_ms INT NOT NULL DEFAULT 0,
    ttfb_p95_ms INT NOT NULL DEFAULT 0,
    body_p50_ms INT NOT NULL DEFAULT 0,
    body_p95_ms INT NOT NULL DEFAULT 0,
    retry_amp DOUBLE PRECISION NOT NULL DEFAULT 0,
    r429_count BIGINT NOT NULL DEFAULT 0,
    timeout_count BIGINT NOT NULL DEFAULT 0,
    wire_bytes BIGINT NOT NULL DEFAULT 0,
    decoded_bytes BIGINT NOT NULL DEFAULT 0,
    max_memory BIGINT NOT NULL DEFAULT 0,
    max_spool BIGINT NOT NULL DEFAULT 0,
    pass BOOLEAN NOT NULL DEFAULT false,
    exclude_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_fetch_concurrency_runs_time ON ankr_fetch_concurrency_runs (created_at DESC);

-- 5) Global Ready Queue Shadow Diff（§56）：旧顺序 vs V1.4 顺序对照（放量 Gate 证据）。
CREATE TABLE IF NOT EXISTS ankr_fetch_queue_diffs (
    id UUID PRIMARY KEY,
    sampled_ready INT NOT NULL DEFAULT 0,
    actual_first_shard TEXT NOT NULL DEFAULT '',
    v14_first_shard TEXT NOT NULL DEFAULT '',
    order_mismatch BOOLEAN NOT NULL DEFAULT false,
    mismatch_rank INT NOT NULL DEFAULT -1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_fetch_queue_diffs_time ON ankr_fetch_queue_diffs (created_at DESC);
