-- 0005_method_abi.sql — V1.3：Method Signature / Contract ABI / 解析队列 / Backfill 任务
-- Selector 不是唯一标识（§10）：唯一约束是 (selector, signature)，一个 selector 允许多个候选。
CREATE TABLE IF NOT EXISTS method_signatures (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    selector VARCHAR(10) NOT NULL,
    method_name VARCHAR(255) NOT NULL,
    signature TEXT NOT NULL,
    source VARCHAR(32) NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT false,
    occurrence_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (selector, signature)
);
CREATE INDEX IF NOT EXISTS idx_method_signatures_selector ON method_signatures (selector);
CREATE INDEX IF NOT EXISTS idx_method_signatures_name ON method_signatures (lower(method_name));

-- Contract ABI（§13）：ABI 精确匹配优先级高于公共签名库（§62-5）
CREATE TABLE IF NOT EXISTS contract_abis (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain VARCHAR(32) NOT NULL,
    contract_address VARCHAR(128) NOT NULL,
    abi_json JSONB NOT NULL,
    source VARCHAR(32) NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT false,
    implementation_address VARCHAR(128),
    fetched_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain, contract_address, source)
);
CREATE INDEX IF NOT EXISTS idx_contract_abis_lookup ON contract_abis (chain, contract_address);

-- Event Signature（§29）
CREATE TABLE IF NOT EXISTS event_signatures (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic0 VARCHAR(66) NOT NULL,
    event_name VARCHAR(255) NOT NULL,
    signature TEXT NOT NULL,
    source VARCHAR(32) NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (topic0, signature)
);
CREATE INDEX IF NOT EXISTS idx_event_signatures_topic0 ON event_signatures (topic0);

-- 未知 Selector 异步补全队列（§18/§19）
-- 去重键 chain + contract_address + method_id：百万笔同 selector 交易只产生一条 Job。
CREATE TABLE IF NOT EXISTS method_resolve_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain VARCHAR(32) NOT NULL,
    contract_address VARCHAR(128) NOT NULL DEFAULT '',
    method_id VARCHAR(10) NOT NULL,
    sample_tx_hash VARCHAR(80) NOT NULL DEFAULT '',
    input_length INTEGER NOT NULL DEFAULT 0,
    occurrence_count BIGINT NOT NULL DEFAULT 1,
    status VARCHAR(24) NOT NULL DEFAULT 'PENDING',
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain, contract_address, method_id)
);
CREATE INDEX IF NOT EXISTS idx_method_resolve_queue_status ON method_resolve_queue (status, updated_at);

-- 历史数据 Backfill 任务（§34/§35）：checkpoint / pause / resume / cancel / progress
CREATE TABLE IF NOT EXISTS method_backfill_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain VARCHAR(32) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'PENDING',
    priority VARCHAR(16) NOT NULL DEFAULT 'background',
    batch_size INTEGER NOT NULL DEFAULT 20000,
    checkpoint_timestamp TIMESTAMPTZ,
    checkpoint_tx_hash VARCHAR(80) NOT NULL DEFAULT '',
    total_rows BIGINT NOT NULL DEFAULT 0,
    scanned_rows BIGINT NOT NULL DEFAULT 0,
    rewritten_rows BIGINT NOT NULL DEFAULT 0,
    resolved_rows BIGINT NOT NULL DEFAULT 0,
    unknown_rows BIGINT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_method_backfill_status ON method_backfill_jobs (status, created_at);

-- Deep Decode 用量归属（§49/§50）：usage_type = tx_deep_decode，预留 decode 预算字段
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS usage_type VARCHAR(32) NOT NULL DEFAULT 'dataset_download';
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS tx_hash VARCHAR(80) NOT NULL DEFAULT '';
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS cache_hit BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS decode_priority VARCHAR(16) NOT NULL DEFAULT 'normal';
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS decode_budget INTEGER;
CREATE INDEX IF NOT EXISTS idx_provider_usage_usage_type ON provider_usage (usage_type, request_time DESC);
