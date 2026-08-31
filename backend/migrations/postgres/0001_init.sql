-- 0001_init.sql — 控制面 Schema (PostgreSQL 18)
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- 租户预留（V1 单用户）
CREATE TABLE IF NOT EXISTS organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID REFERENCES organizations(id),
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'owner',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 默认组织与管理员（V1 单用户，鉴权预留）
INSERT INTO organizations (name, slug) VALUES ('default', 'default') ON CONFLICT (slug) DO NOTHING;
INSERT INTO users (organization_id, email, display_name, role)
SELECT id, 'admin@local', 'Admin', 'owner' FROM organizations WHERE slug='default'
ON CONFLICT (email) DO NOTHING;

-- 下载任务（四级模型第一级）
CREATE TABLE IF NOT EXISTS download_jobs (
    id UUID PRIMARY KEY,
    job_no TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL,
    priority INT NOT NULL DEFAULT 100,
    source TEXT NOT NULL DEFAULT 'user_download',
    chain TEXT NOT NULL,
    range_type TEXT NOT NULL,
    from_block BIGINT,
    to_block BIGINT,
    from_timestamp TIMESTAMPTZ,
    to_timestamp TIMESTAMPTZ,
    datasets TEXT[] NOT NULL,
    reuse_policy TEXT NOT NULL DEFAULT 'prefer_local',
    head_block BIGINT NOT NULL DEFAULT 0,
    created_by UUID REFERENCES users(id),
    error TEXT NOT NULL DEFAULT '',
    stats JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 第二级：Subject（地址）
CREATE TABLE IF NOT EXISTS download_subjects (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL DEFAULT 'address',
    subject TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    progress REAL NOT NULL DEFAULT 0,
    stats JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (job_id, subject)
);
CREATE INDEX IF NOT EXISTS idx_subjects_job ON download_subjects (job_id);

-- 第三级：Dataset
CREATE TABLE IF NOT EXISTS download_datasets (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
    subject_id UUID NOT NULL REFERENCES download_subjects(id) ON DELETE CASCADE,
    dataset TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    progress REAL NOT NULL DEFAULT 0,
    weight REAL NOT NULL DEFAULT 1,
    stats JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (subject_id, dataset)
);
CREATE INDEX IF NOT EXISTS idx_datasets_subject ON download_datasets (subject_id);

-- 第四级：Shard（含 Checkpoint 字段，保证暂停/恢复）
CREATE TABLE IF NOT EXISTS download_shards (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
    subject_id UUID NOT NULL REFERENCES download_subjects(id) ON DELETE CASCADE,
    dataset_id UUID NOT NULL REFERENCES download_datasets(id) ON DELETE CASCADE,
    dataset TEXT NOT NULL,
    from_block BIGINT NOT NULL,
    to_block BIGINT NOT NULL,
    from_timestamp TIMESTAMPTZ,
    to_timestamp TIMESTAMPTZ,
    page_token TEXT NOT NULL DEFAULT '',
    page_no INT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'PENDING',
    priority INT NOT NULL DEFAULT 100,
    retries INT NOT NULL DEFAULT 0,
    max_retries INT NOT NULL DEFAULT 5,
    lease_owner TEXT NOT NULL DEFAULT '',
    leased_until TIMESTAMPTZ,
    downloaded_rows BIGINT NOT NULL DEFAULT 0,
    downloaded_bytes BIGINT NOT NULL DEFAULT 0,
    raw_object TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_shards_dispatch ON download_shards (priority DESC, created_at) WHERE status IN ('PENDING','FAILED_RETRYABLE');
CREATE INDEX IF NOT EXISTS idx_shards_job ON download_shards (job_id);
CREATE INDEX IF NOT EXISTS idx_shards_dataset ON download_shards (dataset_id);

-- Coverage：本地已有数据区间（核心复用机制）
CREATE TABLE IF NOT EXISTS dataset_coverage (
    id UUID PRIMARY KEY,
    chain TEXT NOT NULL,
    subject_type TEXT NOT NULL DEFAULT 'address',
    subject TEXT NOT NULL,
    dataset TEXT NOT NULL,
    from_block BIGINT NOT NULL,
    to_block BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'COMPLETE',
    provider TEXT NOT NULL DEFAULT 'ankr',
    row_count BIGINT NOT NULL DEFAULT 0,
    raw_bytes BIGINT NOT NULL DEFAULT 0,
    head_block BIGINT NOT NULL DEFAULT 0,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_coverage_lookup ON dataset_coverage (chain, subject_type, subject, dataset, status);
CREATE INDEX IF NOT EXISTS idx_coverage_blocks ON dataset_coverage (chain, subject_type, subject, dataset, from_block, to_block);

-- API 用量（成本核算基础）
CREATE TABLE IF NOT EXISTS provider_usage (
    id UUID PRIMARY KEY,
    provider TEXT NOT NULL,
    method TEXT NOT NULL,
    dataset TEXT NOT NULL DEFAULT '',
    job_id UUID,
    shard_id UUID,
    tenant_id TEXT NOT NULL DEFAULT '',
    request_time TIMESTAMPTZ NOT NULL DEFAULT now(),
    duration_ms INT NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    http_status INT NOT NULL DEFAULT 0,
    api_error TEXT NOT NULL DEFAULT '',
    rows INT NOT NULL DEFAULT 0,
    bytes INT NOT NULL DEFAULT 0,
    credits REAL NOT NULL DEFAULT 0,
    estimated_cost_usd REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_usage_time ON provider_usage (request_time DESC);
CREATE INDEX IF NOT EXISTS idx_usage_provider ON provider_usage (provider, request_time DESC);

-- Checkpoint（暂停/恢复）
CREATE TABLE IF NOT EXISTS checkpoints (
    shard_id UUID PRIMARY KEY,
    job_id UUID NOT NULL,
    range_start BIGINT NOT NULL,
    range_end BIGINT NOT NULL,
    next_page_token TEXT NOT NULL DEFAULT '',
    page_no INT NOT NULL DEFAULT 0,
    downloaded_rows BIGINT NOT NULL DEFAULT 0,
    downloaded_bytes BIGINT NOT NULL DEFAULT 0,
    last_success_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 审计
CREATE TABLE IF NOT EXISTS audit_events (
    id UUID PRIMARY KEY,
    user_id UUID,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_time ON audit_events (created_at DESC);

-- 导出任务
CREATE TABLE IF NOT EXISTS exports (
    id UUID PRIMARY KEY,
    job_id UUID,
    dataset TEXT NOT NULL DEFAULT '',
    format TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    object_ref TEXT NOT NULL DEFAULT '',
    rows BIGINT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
