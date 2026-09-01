-- V1.4 Token Metadata Enrichment：公共元数据、原始观察、目录、去重队列与可恢复回填。
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS normalized_address VARCHAR(128);
UPDATE token_metadata SET normalized_address=lower(contract_address) WHERE normalized_address IS NULL;
ALTER TABLE token_metadata ALTER COLUMN normalized_address SET NOT NULL;
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS logo_object_key TEXT;
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS logo_mime_type VARCHAR(128);
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS confidence VARCHAR(32) NOT NULL DEFAULT 'unknown';
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS metadata_status VARCHAR(32) NOT NULL DEFAULT 'unknown';
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS first_seen_block BIGINT;
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS last_seen_block BIGINT;
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ;
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS last_resolved_at TIMESTAMPTZ;
ALTER TABLE token_metadata ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE token_metadata ALTER COLUMN metadata_source DROP NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_token_metadata_normalized ON token_metadata(chain, normalized_address);

CREATE TABLE IF NOT EXISTS token_metadata_observations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain VARCHAR(32) NOT NULL,
    contract_address VARCHAR(128) NOT NULL,
    normalized_address VARCHAR(128) NOT NULL,
    name VARCHAR(255),
    symbol VARCHAR(128),
    decimals INTEGER,
    token_type VARCHAR(32),
    logo_url TEXT,
    source VARCHAR(32) NOT NULL,
    source_ref VARCHAR(255),
    block_number BIGINT,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    raw_hash VARCHAR(128),
    valid BOOLEAN NOT NULL DEFAULT true,
    validation_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(chain, normalized_address, source, raw_hash)
);
CREATE INDEX IF NOT EXISTS idx_token_metadata_obs_lookup
ON token_metadata_observations(chain, normalized_address, observed_at DESC);

CREATE TABLE IF NOT EXISTS token_metadata_overrides (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain VARCHAR(32) NOT NULL,
    normalized_address VARCHAR(128) NOT NULL,
    name VARCHAR(255),
    symbol VARCHAR(128),
    decimals INTEGER,
    token_type VARCHAR(32),
    logo_object_key TEXT,
    reason TEXT,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(chain, normalized_address)
);

CREATE TABLE IF NOT EXISTS token_contract_catalog (
    chain VARCHAR(32) NOT NULL,
    normalized_address VARCHAR(128) NOT NULL,
    first_seen_block BIGINT,
    last_seen_block BIGINT,
    first_seen_at TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ,
    transfer_count BIGINT NOT NULL DEFAULT 0,
    metadata_status VARCHAR(32) NOT NULL DEFAULT 'unknown',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(chain, normalized_address)
);
CREATE INDEX IF NOT EXISTS idx_token_catalog_status ON token_contract_catalog(metadata_status, chain, normalized_address);

CREATE TABLE IF NOT EXISTS token_metadata_resolve_queue (
    chain VARCHAR(32) NOT NULL,
    normalized_address VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    priority SMALLINT NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(chain, normalized_address)
);
CREATE INDEX IF NOT EXISTS idx_token_resolve_runnable ON token_metadata_resolve_queue(status, next_retry_at, updated_at);

CREATE TABLE IF NOT EXISTS token_metadata_backfill_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain VARCHAR(32),
    status VARCHAR(32) NOT NULL,
    cursor_json JSONB NOT NULL DEFAULT '{}',
    scanned_tokens BIGINT NOT NULL DEFAULT 0,
    completed_tokens BIGINT NOT NULL DEFAULT 0,
    partial_tokens BIGINT NOT NULL DEFAULT 0,
    failed_tokens BIGINT NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    last_error TEXT
);
CREATE INDEX IF NOT EXISTS idx_token_backfill_status ON token_metadata_backfill_jobs(status, updated_at);
