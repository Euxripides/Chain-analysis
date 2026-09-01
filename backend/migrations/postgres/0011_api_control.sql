-- 0011_api_control.sql — V1.6 API Control Center

CREATE TABLE IF NOT EXISTS api_control_settings (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    automatic_protection BOOLEAN NOT NULL DEFAULT true,
    kill_switch BOOLEAN NOT NULL DEFAULT false,
    background_paused BOOLEAN NOT NULL DEFAULT false,
    state VARCHAR(24) NOT NULL DEFAULT 'NORMAL',
    state_reason TEXT NOT NULL DEFAULT '指标正常',
    state_since TIMESTAMPTZ NOT NULL DEFAULT now(),
    manual_until TIMESTAMPTZ,
    manual_mode VARCHAR(24),
    manual_reason TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO api_control_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS api_control_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(96) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    warning_error_rate DOUBLE PRECISION NOT NULL DEFAULT 0.03,
    critical_error_rate DOUBLE PRECISION NOT NULL DEFAULT 0.12,
    warning_p95_ms INTEGER NOT NULL DEFAULT 5000,
    critical_p95_ms INTEGER NOT NULL DEFAULT 15000,
    cooldown_seconds INTEGER NOT NULL DEFAULT 300,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO api_control_policies (name)
SELECT '默认异常保护策略'
WHERE NOT EXISTS (SELECT 1 FROM api_control_policies);

CREATE TABLE IF NOT EXISTS api_control_actions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    action VARCHAR(64) NOT NULL,
    source VARCHAR(24) NOT NULL DEFAULT 'automatic',
    from_state VARCHAR(24) NOT NULL DEFAULT '',
    to_state VARCHAR(24) NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    detail JSONB NOT NULL DEFAULT '{}',
    actor VARCHAR(128) NOT NULL DEFAULT 'system',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_api_control_actions_time ON api_control_actions (created_at DESC);

CREATE TABLE IF NOT EXISTS api_control_budgets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(96) NOT NULL,
    provider VARCHAR(64) NOT NULL DEFAULT 'all',
    period VARCHAR(16) NOT NULL DEFAULT 'monthly',
    limit_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
    warn_percent INTEGER NOT NULL DEFAULT 80,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_control_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    severity VARCHAR(16) NOT NULL,
    title VARCHAR(160) NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'OPEN',
    fingerprint VARCHAR(160),
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    muted_until TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    UNIQUE (fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_api_control_alerts_status ON api_control_alerts (status, last_seen_at DESC);

