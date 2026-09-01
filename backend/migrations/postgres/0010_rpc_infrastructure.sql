-- 0010_rpc_infrastructure.sql — V1.5 RPC Infrastructure 控制面（§191-§195）
-- 说明：凭证只保存引用（credential_ref），明文 Secret 永不入库/入日志（§30/§190）。

CREATE TABLE IF NOT EXISTS rpc_providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(64) NOT NULL UNIQUE,
    provider_type VARCHAR(32) NOT NULL DEFAULT 'hosted',
    enabled BOOLEAN NOT NULL DEFAULT true,
    priority INTEGER NOT NULL DEFAULT 100,
    credential_ref VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rpc_provider_chains (
    provider VARCHAR(64) NOT NULL,
    chain VARCHAR(32) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    endpoint_template TEXT,
    archive_enabled BOOLEAN NOT NULL DEFAULT false,
    weight INTEGER NOT NULL DEFAULT 100,
    priority INTEGER NOT NULL DEFAULT 100,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, chain)
);

-- 能力按 provider + chain 管理（§19）；source 记录 static/probe/runtime/admin（§20）
CREATE TABLE IF NOT EXISTS rpc_provider_capabilities (
    provider VARCHAR(64) NOT NULL,
    chain VARCHAR(32) NOT NULL,
    capability VARCHAR(32) NOT NULL,
    supported BOOLEAN NOT NULL,
    source VARCHAR(16) NOT NULL DEFAULT 'static',
    detail JSONB NOT NULL DEFAULT '{}',
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    PRIMARY KEY (provider, chain, capability)
);

-- 健康快照：实时指标在内存/Prometheus，这里只做周期快照（§195）
CREATE TABLE IF NOT EXISTS rpc_provider_health (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider VARCHAR(64) NOT NULL,
    chain VARCHAR(32) NOT NULL,
    state VARCHAR(16) NOT NULL,
    success_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    p50_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    p95_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    p99_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    timeout_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    head_block BIGINT NOT NULL DEFAULT 0,
    head_lag BIGINT NOT NULL DEFAULT 0,
    in_flight INTEGER NOT NULL DEFAULT 0,
    requests BIGINT NOT NULL DEFAULT 0,
    last_error TEXT,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_rpc_provider_health_recent
    ON rpc_provider_health (provider, chain, captured_at DESC);

-- 计价带生效区间，价格变动不会污染历史报表（§172/§173）
CREATE TABLE IF NOT EXISTS provider_pricing (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider VARCHAR(64) NOT NULL,
    api_type VARCHAR(16) NOT NULL,
    plan VARCHAR(32) NOT NULL DEFAULT 'payg',
    credits_per_call DOUBLE PRECISION NOT NULL,
    cost_per_call_usd DOUBLE PRECISION NOT NULL,
    effective_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_to TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_provider_pricing_window
    ON provider_pricing (provider, api_type, effective_from DESC);

-- 用量按天/用途汇总，避免报表全表扫 provider_usage
CREATE TABLE IF NOT EXISTS rpc_usage_rollups (
    day DATE NOT NULL,
    provider VARCHAR(64) NOT NULL,
    chain VARCHAR(32) NOT NULL,
    method_class VARCHAR(32) NOT NULL,
    usage_type VARCHAR(32) NOT NULL,
    http_requests BIGINT NOT NULL DEFAULT 0,
    method_calls BIGINT NOT NULL DEFAULT 0,
    cache_hits BIGINT NOT NULL DEFAULT 0,
    errors BIGINT NOT NULL DEFAULT 0,
    credits DOUBLE PRECISION NOT NULL DEFAULT 0,
    cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (day, provider, chain, method_class, usage_type)
);

-- provider_usage 扩展（§39/§40）：api_type 区分 Advanced 与 RPC；
-- method_calls 与 http 请求数分开，Batch 不得被记成一次调用。
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS api_type VARCHAR(16) NOT NULL DEFAULT 'advanced';
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS chain VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS method_class VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS method_calls INTEGER NOT NULL DEFAULT 1;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS batch_size INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS singleflight_hit BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS investigation_id UUID;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS priority VARCHAR(16) NOT NULL DEFAULT 'normal';
CREATE INDEX IF NOT EXISTS idx_provider_usage_api_type
    ON provider_usage (api_type, request_time DESC);
CREATE INDEX IF NOT EXISTS idx_provider_usage_chain_method
    ON provider_usage (chain, method_class, request_time DESC);

-- 首期 Provider 与链登记（幂等）
INSERT INTO rpc_providers (name, provider_type, enabled, priority, credential_ref)
VALUES ('ankr', 'hosted', true, 10, 'env:ANKR_API_KEY')
ON CONFLICT (name) DO NOTHING;

INSERT INTO provider_pricing (provider, api_type, plan, credits_per_call, cost_per_call_usd)
SELECT 'ankr', 'rpc', 'payg', 200, 0.00002
WHERE NOT EXISTS (SELECT 1 FROM provider_pricing WHERE provider='ankr' AND api_type='rpc');

INSERT INTO provider_pricing (provider, api_type, plan, credits_per_call, cost_per_call_usd)
SELECT 'ankr', 'advanced', 'payg', 700, 0.00007
WHERE NOT EXISTS (SELECT 1 FROM provider_pricing WHERE provider='ankr' AND api_type='advanced');
