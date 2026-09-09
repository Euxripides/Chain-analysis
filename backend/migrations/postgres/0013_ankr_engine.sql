-- Ankr 极速采集引擎（下载 → Coverage → Range Sharding → Fetch → Response Guard
-- → Local Durable Spool → Parse → Batch Ingest → ClickHouse → Coverage Finalize）
-- 页面级状态机 + Shard/Page 画像 + 用量字节拆分。文档 §39/§80。

ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS fetch_started_at TIMESTAMPTZ;
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS fetch_completed_at TIMESTAMPTZ;
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS ingest_started_at TIMESTAMPTZ;
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS ingest_completed_at TIMESTAMPTZ;
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS validation_completed_at TIMESTAMPTZ;

ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS fetched_at TIMESTAMPTZ;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS ingested_at TIMESTAMPTZ;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS validated_at TIMESTAMPTZ;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS fetch_duration_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS ingest_duration_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS wire_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS decoded_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS page_count INT NOT NULL DEFAULT 0;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS page_gap_avg_ms BIGINT NOT NULL DEFAULT 0;

ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS wire_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS decoded_bytes BIGINT NOT NULL DEFAULT 0;

-- 页面级状态机（§80）：DURABLE → INGESTED → VALIDATED；FAILED 可重放。
CREATE TABLE IF NOT EXISTS download_pages (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL,
    shard_id UUID NOT NULL REFERENCES download_shards(id) ON DELETE CASCADE,
    dataset TEXT NOT NULL,
    page_no INT NOT NULL,
    query_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'DURABLE',
    page_size INT NOT NULL DEFAULT 0,
    wire_bytes BIGINT NOT NULL DEFAULT 0,
    decoded_bytes BIGINT NOT NULL DEFAULT 0,
    raw_path TEXT NOT NULL DEFAULT '',
    raw_sha256 TEXT NOT NULL DEFAULT '',
    rows_fetched INT NOT NULL DEFAULT 0,
    rows_parsed INT NOT NULL DEFAULT 0,
    rows_inserted INT NOT NULL DEFAULT 0,
    unique_rows INT NOT NULL DEFAULT 0,
    next_page_token_encrypted TEXT NOT NULL DEFAULT '',
    next_page_token_hash TEXT NOT NULL DEFAULT '',
    from_block BIGINT NOT NULL DEFAULT 0,
    to_block BIGINT NOT NULL DEFAULT 0,
    fetch_started_at TIMESTAMPTZ,
    body_completed_at TIMESTAMPTZ,
    durable_at TIMESTAMPTZ,
    ingested_at TIMESTAMPTZ,
    validated_at TIMESTAMPTZ,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (shard_id, page_no)
);
CREATE INDEX IF NOT EXISTS idx_pages_shard ON download_pages (shard_id, page_no);
CREATE INDEX IF NOT EXISTS idx_pages_status ON download_pages (status) WHERE status IN ('DURABLE','FAILED');

-- Shard 历史画像（§12）：Density-aware Sharding 的长期数据。
CREATE TABLE IF NOT EXISTS ankr_shard_profiles (
    id UUID PRIMARY KEY,
    chain TEXT NOT NULL,
    dataset TEXT NOT NULL,
    subject_class TEXT NOT NULL DEFAULT 'address',
    block_from BIGINT NOT NULL DEFAULT 0,
    block_to BIGINT NOT NULL DEFAULT 0,
    rows BIGINT NOT NULL DEFAULT 0,
    pages INT NOT NULL DEFAULT 0,
    wire_bytes BIGINT NOT NULL DEFAULT 0,
    decoded_bytes BIGINT NOT NULL DEFAULT 0,
    fetch_duration_ms BIGINT NOT NULL DEFAULT 0,
    ingest_duration_ms BIGINT NOT NULL DEFAULT 0,
    rows_per_block DOUBLE PRECISION NOT NULL DEFAULT 0,
    bytes_per_row DOUBLE PRECISION NOT NULL DEFAULT 0,
    page_latency_p50_ms BIGINT NOT NULL DEFAULT 0,
    page_latency_p95_ms BIGINT NOT NULL DEFAULT 0,
    page_gap_avg_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_shard_profiles_lookup ON ankr_shard_profiles (chain, dataset, subject_class, block_from);

-- PageSize 画像（§32/§33）：按 query_shape 持久化最佳页大小与体量特征。
CREATE TABLE IF NOT EXISTS ankr_page_profiles (
    chain TEXT NOT NULL,
    dataset TEXT NOT NULL,
    query_shape TEXT NOT NULL,
    preferred_page_size INT NOT NULL DEFAULT 10000,
    avg_rows INT NOT NULL DEFAULT 0,
    avg_wire_bytes BIGINT NOT NULL DEFAULT 0,
    avg_decoded_bytes BIGINT NOT NULL DEFAULT 0,
    avg_latency_ms BIGINT NOT NULL DEFAULT 0,
    p95_latency_ms BIGINT NOT NULL DEFAULT 0,
    timeout_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    oversized_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    pages_observed BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, dataset, query_shape)
);
