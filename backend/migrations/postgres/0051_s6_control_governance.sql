-- 0051_s6_control_governance.sql
-- Sprint 6 / S6-DB-01（§22.1 原 0045 语义，编号顺延至 0051，详见 ADR-016 与 sprint6-report.md）
-- 内容：配置项与配置版本、功能开关、Kill Switch 双人审批编排扩展、告警生命周期扩展、
--       Provider 描述符 / 密钥摘要 / 路由策略。
-- 原则：全部为新增表或新增列，不删除既有列；旧二进制仍可读取旧列（expand-compatible，AD-006）。

-- ---------------------------------------------------------------------------
-- 1. 配置项（当前值）与配置版本（append-only 历史）
-- ---------------------------------------------------------------------------
-- 秘密值永不落库：只保存 secret_ref（环境变量/密钥库引用）与布尔标记。
CREATE TABLE IF NOT EXISTS configuration_settings (
    key          text PRIMARY KEY,
    category     text NOT NULL DEFAULT 'general',
    value_type   text NOT NULL DEFAULT 'string'
                 CHECK (value_type IN ('string', 'int', 'bool', 'json', 'secret-ref')),
    value        jsonb NOT NULL DEFAULT 'null'::jsonb,
    secret_ref   text NOT NULL DEFAULT '',
    is_secret    boolean NOT NULL DEFAULT false,
    description  text NOT NULL DEFAULT '',
    revision     bigint NOT NULL DEFAULT 1,
    updated_by   uuid REFERENCES identities(id),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS configuration_settings_category_idx
    ON configuration_settings (category, key);

CREATE TABLE IF NOT EXISTS configuration_revisions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key                 text NOT NULL,
    revision            bigint NOT NULL,
    value               jsonb,
    secret_ref          text NOT NULL DEFAULT '',
    is_secret           boolean NOT NULL DEFAULT false,
    before_hash         text NOT NULL DEFAULT '',
    after_hash          text NOT NULL DEFAULT '',
    change_note         text NOT NULL DEFAULT '',
    actor_id            uuid REFERENCES identities(id),
    actor_email         text NOT NULL DEFAULT '',
    request_id          text NOT NULL DEFAULT '',
    trace_id            text NOT NULL DEFAULT '',
    approval_request_id uuid REFERENCES approval_requests(id),
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (key, revision)
);
CREATE INDEX IF NOT EXISTS configuration_revisions_key_idx
    ON configuration_revisions (key, revision DESC);

-- ---------------------------------------------------------------------------
-- 2. 功能开关（环境 / 组织 / 工作区 / 百分比灰度，评估可重现）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS feature_flags (
    key                 text PRIMARY KEY,
    description         text NOT NULL DEFAULT '',
    environment         text NOT NULL DEFAULT 'all',
    enabled             boolean NOT NULL DEFAULT false,
    default_value       boolean NOT NULL DEFAULT false,
    percentage          integer NOT NULL DEFAULT 0 CHECK (percentage BETWEEN 0 AND 100),
    scope_type          text NOT NULL DEFAULT 'global'
                        CHECK (scope_type IN ('global', 'organization', 'workspace', 'user')),
    scope_org_id        uuid REFERENCES organizations(id),
    scope_workspace_id  uuid REFERENCES workspaces(id),
    rules               jsonb NOT NULL DEFAULT '[]'::jsonb,
    revision            bigint NOT NULL DEFAULT 1,
    updated_by          uuid REFERENCES identities(id),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS feature_flags_scope_idx
    ON feature_flags (environment, scope_type);

-- 评估记录：ADMIN-010 需要「只影响目标 scope」的运行时证据（两名用户对照）。
CREATE TABLE IF NOT EXISTS feature_flag_evaluations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    flag_key        text NOT NULL,
    flag_revision   bigint NOT NULL,
    environment     text NOT NULL DEFAULT '',
    subject_type    text NOT NULL DEFAULT 'user',
    subject_id      text NOT NULL DEFAULT '',
    organization_id uuid,
    workspace_id    uuid,
    result          boolean NOT NULL,
    reason          text NOT NULL DEFAULT '',
    bucket          integer NOT NULL DEFAULT 0,
    evaluated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS feature_flag_evaluations_key_idx
    ON feature_flag_evaluations (flag_key, evaluated_at DESC);
CREATE INDEX IF NOT EXISTS feature_flag_evaluations_subject_idx
    ON feature_flag_evaluations (subject_id, flag_key, evaluated_at DESC);

-- ---------------------------------------------------------------------------
-- 3. Kill Switch 编排（双人审批 + 恢复 + 健康验证；默认不删任务、不清队列）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS kill_switch_engagements (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mode                text NOT NULL CHECK (mode IN ('engage', 'restore')),
    status              text NOT NULL DEFAULT 'pending_approval'
                        CHECK (status IN ('pending_approval', 'approved', 'rejected',
                                          'executing', 'active', 'restored', 'failed', 'cancelled')),
    reason              text NOT NULL,
    scope               jsonb NOT NULL DEFAULT '{}'::jsonb,
    impact_preview      jsonb NOT NULL DEFAULT '{}'::jsonb,
    approval_request_id uuid REFERENCES approval_requests(id),
    step_up_at          timestamptz,
    step_up_method      text NOT NULL DEFAULT 'password',
    requested_by        uuid REFERENCES identities(id),
    approved_by         uuid REFERENCES identities(id),
    engaged_at          timestamptz,
    restored_at         timestamptz,
    health_check        jsonb,
    break_glass         boolean NOT NULL DEFAULT false,
    review_required_by  timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS kill_switch_engagements_status_idx
    ON kill_switch_engagements (status, created_at DESC);

-- ---------------------------------------------------------------------------
-- 4. 告警生命周期扩展（A-ALERT-01：认领 / 抑制 / 解决 / 关联对象）
--    关联链用于「从告警定位到 request / provider / shard / checkpoint / object」。
-- ---------------------------------------------------------------------------
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS acknowledged_at   timestamptz;
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS acknowledged_by   uuid;
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS acknowledge_note  text NOT NULL DEFAULT '';
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS resolved_by       uuid;
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS resolution_note   text NOT NULL DEFAULT '';
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS muted_by          uuid;
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS mute_reason       text NOT NULL DEFAULT '';
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS occurrence_count  bigint NOT NULL DEFAULT 1;
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS source            text NOT NULL DEFAULT 'api-control';
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS slo_key           text NOT NULL DEFAULT '';
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS trace_id          text NOT NULL DEFAULT '';
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS links             jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS labels            jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS updated_at        timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS api_control_alerts_status_idx
    ON api_control_alerts (status, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS api_control_alerts_severity_idx
    ON api_control_alerts (severity, last_seen_at DESC);

-- ---------------------------------------------------------------------------
-- 5. 审批编排：把「请求人与审批人不能是同一人」固化为数据库约束（ADMIN-006）
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'approval_requests_two_person_chk') THEN
        ALTER TABLE approval_requests
            ADD CONSTRAINT approval_requests_two_person_chk
            CHECK (decided_by IS NULL OR decided_by <> requested_by);
    END IF;
END
$$;

-- 影响预览与执行结果需要独立留痕（审批表只保存 payload，执行前后差异在这里）。
CREATE TABLE IF NOT EXISTS approval_request_events (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    approval_request_id uuid NOT NULL REFERENCES approval_requests(id) ON DELETE CASCADE,
    event               text NOT NULL,
    actor_id            uuid REFERENCES identities(id),
    actor_email         text NOT NULL DEFAULT '',
    note                text NOT NULL DEFAULT '',
    detail              jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS approval_request_events_request_idx
    ON approval_request_events (approval_request_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- 6. Provider 描述符、密钥摘要与路由策略
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_descriptors (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_key          text NOT NULL UNIQUE,
    display_name          text NOT NULL,
    kind                  text NOT NULL DEFAULT 'rpc'
                          CHECK (kind IN ('rpc', 'count', 'management', 'mock', 'local')),
    chains                text[] NOT NULL DEFAULT '{}',
    datasets              text[] NOT NULL DEFAULT '{}',
    capabilities          jsonb NOT NULL DEFAULT '[]'::jsonb,
    endpoint              text NOT NULL DEFAULT '',
    status                text NOT NULL DEFAULT 'disconnected'
                          CHECK (status IN ('connected', 'disconnected', 'degraded', 'disabled')),
    config                jsonb NOT NULL DEFAULT '{}'::jsonb,
    concurrency           integer NOT NULL DEFAULT 0,
    rate_limit_per_minute integer NOT NULL DEFAULT 0,
    priority              integer NOT NULL DEFAULT 100,
    revision              bigint NOT NULL DEFAULT 1,
    capability_drift      jsonb,
    last_test_at          timestamptz,
    last_test_result      jsonb,
    updated_by            uuid REFERENCES identities(id),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

-- 只保存摘要：prefix（前 6 位）+ sha256 fingerprint + 引用名。永不保存明文。
CREATE TABLE IF NOT EXISTS provider_secrets (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_key        text NOT NULL REFERENCES provider_descriptors(provider_key) ON DELETE CASCADE,
    secret_ref          text NOT NULL,
    prefix              text NOT NULL DEFAULT '',
    fingerprint         text NOT NULL,
    status              text NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'retiring', 'revoked')),
    note                text NOT NULL DEFAULT '',
    created_by          uuid REFERENCES identities(id),
    rotated_by          uuid REFERENCES identities(id),
    rotated_at          timestamptz NOT NULL DEFAULT now(),
    last_used_at        timestamptz,
    expires_at          timestamptz,
    approval_request_id uuid REFERENCES approval_requests(id)
);
CREATE INDEX IF NOT EXISTS provider_secrets_provider_idx
    ON provider_secrets (provider_key, rotated_at DESC);

CREATE TABLE IF NOT EXISTS routing_policies (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_key               text NOT NULL UNIQUE,
    description              text NOT NULL DEFAULT '',
    chain                    text NOT NULL DEFAULT '',
    dataset                  text NOT NULL DEFAULT '',
    enabled                  boolean NOT NULL DEFAULT true,
    primary_provider         text NOT NULL DEFAULT '',
    fallback_providers       text[] NOT NULL DEFAULT '{}',
    max_concurrency          integer NOT NULL DEFAULT 0,
    rate_limit_per_minute    integer NOT NULL DEFAULT 0,
    circuit_error_rate       double precision NOT NULL DEFAULT 0,
    circuit_p95_ms           integer NOT NULL DEFAULT 0,
    circuit_cooldown_seconds integer NOT NULL DEFAULT 60,
    rollout_percent          integer NOT NULL DEFAULT 100 CHECK (rollout_percent BETWEEN 0 AND 100),
    revision                 bigint NOT NULL DEFAULT 1,
    updated_by               uuid REFERENCES identities(id),
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS routing_policy_revisions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_key          text NOT NULL,
    revision            bigint NOT NULL,
    before              jsonb NOT NULL DEFAULT '{}'::jsonb,
    after               jsonb NOT NULL DEFAULT '{}'::jsonb,
    change_note         text NOT NULL DEFAULT '',
    actor_id            uuid REFERENCES identities(id),
    actor_email         text NOT NULL DEFAULT '',
    approval_request_id uuid REFERENCES approval_requests(id),
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (policy_key, revision)
);

-- ---------------------------------------------------------------------------
-- 7. API / MCP Key 轮换与吊销留痕（A-KEY-01）
--    明文仅在创建/轮换响应中返回一次，库里只有 key_prefix 与 key_hash。
-- ---------------------------------------------------------------------------
ALTER TABLE gpt_action_api_keys ADD COLUMN IF NOT EXISTS rotated_from       uuid;
ALTER TABLE gpt_action_api_keys ADD COLUMN IF NOT EXISTS rotated_at         timestamptz;
ALTER TABLE gpt_action_api_keys ADD COLUMN IF NOT EXISTS revoked_at         timestamptz;
ALTER TABLE gpt_action_api_keys ADD COLUMN IF NOT EXISTS revoked_by         uuid;
ALTER TABLE gpt_action_api_keys ADD COLUMN IF NOT EXISTS revoke_reason      text NOT NULL DEFAULT '';
ALTER TABLE gpt_action_api_keys ADD COLUMN IF NOT EXISTS created_by_identity uuid;
ALTER TABLE gpt_action_api_keys ADD COLUMN IF NOT EXISTS revision           bigint NOT NULL DEFAULT 1;
ALTER TABLE gpt_action_api_keys ADD COLUMN IF NOT EXISTS description        text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS gpt_action_api_keys_prefix_idx ON gpt_action_api_keys (key_prefix);


-- ---------------------------------------------------------------------------
-- 7b. 预算扩展（A-BUDGET-01：周期、维度、阈值、动作、版本）
-- ---------------------------------------------------------------------------
ALTER TABLE api_control_budgets ADD COLUMN IF NOT EXISTS dimension        text NOT NULL DEFAULT 'provider';
ALTER TABLE api_control_budgets ADD COLUMN IF NOT EXISTS scope_key        text NOT NULL DEFAULT '';
ALTER TABLE api_control_budgets ADD COLUMN IF NOT EXISTS action           text NOT NULL DEFAULT 'alert';
ALTER TABLE api_control_budgets ADD COLUMN IF NOT EXISTS action_threshold integer NOT NULL DEFAULT 100;
ALTER TABLE api_control_budgets ADD COLUMN IF NOT EXISTS currency         text NOT NULL DEFAULT 'USD';
ALTER TABLE api_control_budgets ADD COLUMN IF NOT EXISTS notes            text NOT NULL DEFAULT '';
ALTER TABLE api_control_budgets ADD COLUMN IF NOT EXISTS revision         bigint NOT NULL DEFAULT 1;
ALTER TABLE api_control_budgets ADD COLUMN IF NOT EXISTS updated_by       uuid REFERENCES identities(id);

-- ---------------------------------------------------------------------------
-- 7c. 告警优先级（§25.4 P0–P3）。severity 保留给既有写入方，priority 为 S6 生命周期字段。
-- ---------------------------------------------------------------------------
ALTER TABLE api_control_alerts ADD COLUMN IF NOT EXISTS priority text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS api_control_alerts_priority_idx
    ON api_control_alerts (priority, last_seen_at DESC);

-- ---------------------------------------------------------------------------
-- 8. 基线数据（幂等）。仅包含系统自身默认值，不含任何秘密。
-- ---------------------------------------------------------------------------
INSERT INTO configuration_settings (key, category, value_type, value, description) VALUES
    ('platform.environment',            'platform', 'string', '"production"'::jsonb,  '运行环境标识（development/staging/production）'),
    ('control.kill_switch.dual_approval', 'control', 'bool', 'true'::jsonb,         'Kill Switch 与恢复必须经双人审批'),
    ('control.kill_switch.default_scope', 'control', 'json', '{"apis":true,"jobs":true,"worker_poll":true,"actions_mcp":true,"delete_jobs":false,"purge_queue":false,"drop_checkpoints":false}'::jsonb, 'Kill Switch 默认影响范围（默认不删除任务、不清空队列、不丢弃 Checkpoint）'),
    ('governance.deletion.execution_enabled', 'governance', 'bool', 'false'::jsonb, '实际删除执行开关；专项验收前保持关闭'),
    ('governance.retention.scheduler_enabled', 'governance', 'bool', 'false'::jsonb, '保留策略调度器开关'),
    ('provider.test.probe_method',      'provider', 'string', '"getBlockNumber"'::jsonb, 'Provider 连接测试使用的受控低成本探针方法'),
    ('provider.key.rotation_days',      'provider', 'int',    '90'::jsonb,          'Provider 密钥建议轮换周期（天）'),
    ('audit.export.max_events',         'audit',    'int',    '200000'::jsonb,      '单次审计导出的最大事件数')
ON CONFLICT (key) DO NOTHING;

INSERT INTO configuration_revisions (key, revision, value, before_hash, after_hash, change_note, actor_email)
SELECT key, 1, value, '', encode(sha256(convert_to(coalesce(value::text, 'null'), 'UTF8')), 'hex'), 'migration 0051 基线值', 'system:migration'
FROM configuration_settings
ON CONFLICT (key, revision) DO NOTHING;

INSERT INTO feature_flags (key, description, environment, enabled, default_value, percentage, scope_type, rules, revision) VALUES
    ('admin.control_plane_v2',   '后台控制面 v2（/api/admin/v1 control）',        'all', true,  false, 100, 'global', '[]'::jsonb, 1),
    ('admin.provider_registry',  'Provider 描述符与路由策略页面',                  'all', true,  false, 100, 'global', '[]'::jsonb, 1),
    ('admin.alert_center',       '告警中心与生命周期操作',                        'all', true,  false, 100, 'global', '[]'::jsonb, 1),
    ('admin.backup_restore',     '备份与恢复演练页面（只读预览）',                'all', true,  false, 100, 'global', '[]'::jsonb, 1),
    ('user.explorer_v2',         '用户层 Explorer v2',                            'all', true,  false, 100, 'global', '[]'::jsonb, 1),
    ('user.gauge_rollout',       '灰度发布对照开关（ADMIN-010 使用）',            'all', true,  false,   0, 'global', '[]'::jsonb, 1)
ON CONFLICT (key) DO NOTHING;

INSERT INTO provider_descriptors (provider_key, display_name, kind, chains, datasets, capabilities, endpoint, status, config, concurrency, rate_limit_per_minute, priority) VALUES
    ('ankr-multichain', 'Ankr Multichain', 'rpc',    ARRAY['ethereum','bsc','polygon','tron'], ARRAY['transactions','token_transfers','internal_transactions'],
     '[{"key":"fetch","state":"declared"},{"key":"count","state":"declared"},{"key":"address_type","state":"declared"}]'::jsonb, 'https://rpc.ankr.com/multichain', 'connected', '{"mode":"config"}'::jsonb, 32, 900, 10),
    ('tron-grid',       'TRON Grid',       'rpc',    ARRAY['tron'], ARRAY['transactions','token_transfers','internal_transactions'],
     '[{"key":"fetch","state":"declared"},{"key":"address_type","state":"declared"}]'::jsonb, '', 'connected', '{"mode":"config"}'::jsonb, 32, 600, 20),
    ('rpc-pool',        'RPC Pool',        'rpc',    ARRAY['ethereum','bsc','polygon'], ARRAY['transactions'],
     '[{"key":"fetch","state":"declared"}]'::jsonb, '', 'disconnected', '{"mode":"config"}'::jsonb, 8, 300, 30),
    ('local-cache',     'Local Cache',     'local',  ARRAY['*'], ARRAY['*'],
     '[{"key":"fetch","state":"declared"},{"key":"projection","state":"declared"}]'::jsonb, '', 'connected', '{"mode":"internal"}'::jsonb, 64, 0, 1)
ON CONFLICT (provider_key) DO NOTHING;

INSERT INTO routing_policies (policy_key, description, chain, dataset, enabled, primary_provider, fallback_providers, max_concurrency, rate_limit_per_minute, circuit_error_rate, circuit_p95_ms, circuit_cooldown_seconds, rollout_percent, revision) VALUES
    ('tron:transactions',          'TRON 交易拉取路由',      'tron',     'transactions',          true, 'tron-grid',       ARRAY['ankr-multichain'], 32, 600, 0.25, 8000, 60, 100, 1),
    ('tron:token_transfers',       'TRON TRC20 转账路由',    'tron',     'token_transfers',       true, 'tron-grid',       ARRAY['ankr-multichain'], 32, 600, 0.25, 8000, 60, 100, 1),
    ('tron:internal_transactions', 'TRON 内部交易路由',      'tron',     'internal_transactions', true, 'tron-grid',       ARRAY['ankr-multichain'], 16, 300, 0.25, 8000, 60, 100, 1),
    ('evm:transactions',           'EVM 交易拉取路由',       'ethereum', 'transactions',          true, 'ankr-multichain', ARRAY['rpc-pool'],        32, 900, 0.25, 8000, 60, 100, 1)
ON CONFLICT (policy_key) DO NOTHING;