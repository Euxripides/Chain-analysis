CREATE TABLE IF NOT EXISTS gpt_action_api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    key_prefix TEXT NOT NULL,
    key_hash BYTEA NOT NULL UNIQUE,
    scopes TEXT[] NOT NULL DEFAULT ARRAY['read','analysis','export']::TEXT[],
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 300 CHECK (rate_limit_per_minute > 0),
    allowed_ips TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    expires_at TIMESTAMPTZ,
    disabled_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by TEXT NOT NULL DEFAULT 'admin@local'
);

CREATE TABLE IF NOT EXISTS gpt_action_request_logs (
    id BIGSERIAL PRIMARY KEY,
    api_key_id UUID REFERENCES gpt_action_api_keys(id) ON DELETE SET NULL,
    request_id TEXT NOT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    status INTEGER NOT NULL,
    duration_ms BIGINT NOT NULL,
    remote_ip TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_gpt_action_request_logs_key_time
    ON gpt_action_request_logs(api_key_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_gpt_action_request_logs_time
    ON gpt_action_request_logs(created_at DESC);
