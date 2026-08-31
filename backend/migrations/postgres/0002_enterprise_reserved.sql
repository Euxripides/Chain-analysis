-- 0002_enterprise_reserved.sql — 企业化预留 + 全量解析元数据（文档 §88/§92）
CREATE TABLE IF NOT EXISTS workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, slug)
);

CREATE TABLE IF NOT EXISTS projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Provider 配额（后期企业计费直接复用）
CREATE TABLE IF NOT EXISTS quotas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    provider TEXT NOT NULL DEFAULT 'ankr',
    requests_per_day BIGINT NOT NULL DEFAULT 0,
    credits_per_day BIGINT NOT NULL DEFAULT 0,
    cost_per_day_usd NUMERIC(14,4) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, provider)
);

-- 任务表预留组织/工作区/项目（§70：公共链数据不按租户复制，只挂归属）
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id);
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id);
-- “全量”解析元数据：链头/解析时刻可审计、可恢复（§92）
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ;

-- 默认工作区/项目（V1 单用户）
INSERT INTO workspaces (organization_id, name, slug)
SELECT id, 'default', 'default' FROM organizations WHERE slug='default'
ON CONFLICT (organization_id, slug) DO NOTHING;
INSERT INTO projects (workspace_id, name)
SELECT id, 'default' FROM workspaces WHERE slug='default'
ON CONFLICT DO NOTHING;
