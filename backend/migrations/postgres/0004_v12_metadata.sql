-- 0004_v12_metadata.sql — V1.2：案件/标签/地址目录/Token 元数据/更新预览/导出增强
CREATE TABLE IF NOT EXISTS cases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    case_no VARCHAR(128),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    color VARCHAR(32),
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE IF NOT EXISTS tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    name VARCHAR(128) NOT NULL,
    description TEXT,
    color VARCHAR(32),
    source VARCHAR(32) NOT NULL DEFAULT 'user',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE IF NOT EXISTS workspace_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    chain VARCHAR(32) NOT NULL,
    address VARCHAR(128) NOT NULL,
    normalized_address VARCHAR(128) NOT NULL,
    display_name VARCHAR(255),
    primary_case_id UUID,
    transaction_count BIGINT NOT NULL DEFAULT 0,
    token_transfer_count BIGINT NOT NULL DEFAULT 0,
    nft_transfer_count BIGINT NOT NULL DEFAULT 0,
    log_count BIGINT NOT NULL DEFAULT 0,
    total_activity_count BIGINT NOT NULL DEFAULT 0,
    first_data_block BIGINT,
    last_data_block BIGINT,
    first_data_at TIMESTAMPTZ,
    last_data_at TIMESTAMPTZ,
    last_sync_at TIMESTAMPTZ,
    sync_status VARCHAR(32) NOT NULL DEFAULT 'unknown',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, chain, normalized_address)
);
CREATE INDEX IF NOT EXISTS idx_workspace_addresses_updated ON workspace_addresses (workspace_id, last_sync_at DESC);

CREATE TABLE IF NOT EXISTS address_case_links (
    workspace_id UUID NOT NULL,
    chain VARCHAR(32) NOT NULL,
    normalized_address VARCHAR(128) NOT NULL,
    case_id UUID NOT NULL,
    assigned_by UUID,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, chain, normalized_address, case_id)
);

CREATE TABLE IF NOT EXISTS address_tag_links (
    workspace_id UUID NOT NULL,
    chain VARCHAR(32) NOT NULL,
    normalized_address VARCHAR(128) NOT NULL,
    tag_id UUID NOT NULL,
    assigned_by UUID,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, chain, normalized_address, tag_id)
);

CREATE TABLE IF NOT EXISTS token_metadata (
    chain VARCHAR(32) NOT NULL,
    contract_address VARCHAR(128) NOT NULL,
    name VARCHAR(255),
    symbol VARCHAR(128),
    decimals INTEGER,
    logo_url TEXT,
    token_type VARCHAR(32),
    verified BOOLEAN NOT NULL DEFAULT false,
    metadata_source VARCHAR(32) NOT NULL DEFAULT 'ankr',
    last_verified_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, contract_address)
);

CREATE TABLE IF NOT EXISTS update_previews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    created_by UUID,
    chain VARCHAR(32) NOT NULL,
    target_block BIGINT NOT NULL,
    request_json JSONB NOT NULL,
    result_json JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 导出任务增强（§53）
ALTER TABLE exports ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
ALTER TABLE exports ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id);
ALTER TABLE exports ADD COLUMN IF NOT EXISTS created_by UUID;
ALTER TABLE exports ADD COLUMN IF NOT EXISTS format VARCHAR(16) NOT NULL DEFAULT 'csv';
ALTER TABLE exports ADD COLUMN IF NOT EXISTS zip BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE exports ADD COLUMN IF NOT EXISTS query_snapshot JSONB NOT NULL DEFAULT '{}';
ALTER TABLE exports ADD COLUMN IF NOT EXISTS sha256 VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE exports ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
ALTER TABLE exports ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
ALTER TABLE exports ADD COLUMN IF NOT EXISTS finished_at TIMESTAMPTZ;
ALTER TABLE exports ADD COLUMN IF NOT EXISTS format_old VARCHAR(16);

CREATE TABLE IF NOT EXISTS export_tickets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    export_id UUID NOT NULL REFERENCES exports(id),
    user_id UUID,
    organization_id UUID,
    workspace_id UUID,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_export_tickets_expiry ON export_tickets (expires_at);
