-- Ankr 极速采集引擎 V1.1：单地址多 Range 极限性能 + Benchmark 持久化 + Sweet Spot 画像。
-- 文档 V1.1 §20/§24/§41-§44/§48。

ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'balanced';

-- Benchmark 运行记录（§48）：固定 target_block 的 1/2/4/8 Range 极限性能实测。
CREATE TABLE IF NOT EXISTS ankr_benchmark_runs (
    id UUID PRIMARY KEY,
    chain TEXT NOT NULL,
    dataset TEXT NOT NULL,
    address_hash TEXT NOT NULL DEFAULT '',   -- 不落明文地址（前端/日志只展示 hash）
    target_block BIGINT NOT NULL DEFAULT 0,
    shard_count INT NOT NULL DEFAULT 1,
    page_size INT NOT NULL DEFAULT 10000,
    job_id UUID,
    status TEXT NOT NULL DEFAULT 'RUNNING',   -- RUNNING|COMPLETED|FAILED
    wall_clock_ms BIGINT NOT NULL DEFAULT 0,
    rows BIGINT NOT NULL DEFAULT 0,
    unique_rows BIGINT NOT NULL DEFAULT 0,
    requests INT NOT NULL DEFAULT 0,
    attempts INT NOT NULL DEFAULT 0,
    credits REAL NOT NULL DEFAULT 0,
    cost REAL NOT NULL DEFAULT 0,
    p50_ms INT NOT NULL DEFAULT 0,
    p95_ms INT NOT NULL DEFAULT 0,
    p99_ms INT NOT NULL DEFAULT 0,
    page_gap_avg_ms BIGINT NOT NULL DEFAULT 0,
    page_gap_p95_ms BIGINT NOT NULL DEFAULT 0,
    peak_active_shards INT NOT NULL DEFAULT 0,
    retry_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    timeout_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    rate_limit_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    error_429 INT NOT NULL DEFAULT 0,
    error_conn_limit INT NOT NULL DEFAULT 0,
    error_timeout INT NOT NULL DEFAULT 0,
    error_oversized INT NOT NULL DEFAULT 0,
    peak_rss BIGINT NOT NULL DEFAULT 0,
    spool_peak_bytes BIGINT NOT NULL DEFAULT 0,
    ch_p95_ms BIGINT NOT NULL DEFAULT 0,
    raw_overlap_rows BIGINT NOT NULL DEFAULT 0,
    boundary_missing BIGINT NOT NULL DEFAULT 0,
    boundary_evidence JSONB NOT NULL DEFAULT '{}',
    integrity_status TEXT NOT NULL DEFAULT 'PENDING', -- PENDING|PASS|FAIL
    excluded_from_learning BOOLEAN NOT NULL DEFAULT false,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_benchmark_lookup ON ankr_benchmark_runs (chain, dataset, created_at DESC);

-- Shard Profile 扩展（§20/§21）：Sweet Spot 学习所需的密度分级与统计。
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS query_shape TEXT NOT NULL DEFAULT '';
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS density_class TEXT NOT NULL DEFAULT '';
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS preferred_shard_count INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS avg_wall_clock_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS provider_latency_avg_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS provider_latency_p95_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS retry_rate DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS timeout_rate DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS rate_limit_rate DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE ankr_shard_profiles ADD COLUMN IF NOT EXISTS sample_count INT NOT NULL DEFAULT 0;

-- Sweet Spot 画像独立表（按 chain+dataset+query_shape+density_class 聚合，§20/§24）。
CREATE TABLE IF NOT EXISTS ankr_sweet_spot_profiles (
    chain TEXT NOT NULL,
    dataset TEXT NOT NULL,
    query_shape TEXT NOT NULL DEFAULT '',
    density_class TEXT NOT NULL DEFAULT '',
    preferred_shard_count INT NOT NULL DEFAULT 0,
    avg_wall_clock_ms BIGINT NOT NULL DEFAULT 0,
    avg_rows BIGINT NOT NULL DEFAULT 0,
    avg_pages BIGINT NOT NULL DEFAULT 0,
    provider_latency_avg_ms BIGINT NOT NULL DEFAULT 0,
    provider_latency_p95_ms BIGINT NOT NULL DEFAULT 0,
    retry_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    timeout_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    rate_limit_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    sample_count INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, dataset, query_shape, density_class)
);
