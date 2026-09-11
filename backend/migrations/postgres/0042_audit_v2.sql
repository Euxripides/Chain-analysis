-- 0042 审计 v2：不可抵赖审计事件 + 事务 Outbox + 每日锚点
-- 对应 docs/dual-system-development-plan.md §6.5 / §6.6 / §22 / Sprint S3-DB-01（S3-BE-02）。
--
-- 设计要点：
--   * append-only：应用运行账号无 UPDATE/DELETE 权限（见 0042 末尾 REVOKE 说明）。
--   * 顺序哈希链：seq 单调递增，event_hash = sha256(chain|seq|prev_hash|canonical_payload)。
--   * 高风险事件保存 before/after（敏感字段只存掩码或摘要）。
--   * audit_events（旧表）保留，双写观察期内由应用同时写入，切读后再评估下线。

CREATE TABLE IF NOT EXISTS audit_events_v2 (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    chain              text NOT NULL DEFAULT 'audit',
    seq                bigint NOT NULL,
    occurred_at        timestamptz NOT NULL DEFAULT now(),
    event_version      integer NOT NULL DEFAULT 1,
    environment        text NOT NULL DEFAULT '',

    -- 谁
    actor_type         text NOT NULL DEFAULT 'user' CHECK (actor_type IN ('user', 'system', 'api_client', 'anonymous')),
    actor_id           uuid,
    actor_email        text NOT NULL DEFAULT '',
    actor_display_name text NOT NULL DEFAULT '',
    actor_roles        text[] NOT NULL DEFAULT '{}',

    -- 哪里
    organization_id    uuid,
    workspace_id       uuid,
    session_id         uuid,
    device_id          uuid,

    -- 做了什么
    action             text NOT NULL,
    category           text NOT NULL,
    resource_type      text NOT NULL DEFAULT '',
    resource_id        text NOT NULL DEFAULT '',
    case_id            uuid,
    investigation_id   uuid,
    job_id             uuid,
    report_id          uuid,
    http_method        text NOT NULL DEFAULT '',
    route_template     text NOT NULL DEFAULT '',
    request_id         text NOT NULL DEFAULT '',
    trace_id           text NOT NULL DEFAULT '',
    idempotency_key_hash text NOT NULL DEFAULT '',
    client_version     text NOT NULL DEFAULT '',

    -- 结果
    result             text NOT NULL CHECK (result IN ('allow', 'deny', 'success', 'failure', 'error')),
    http_status        integer NOT NULL DEFAULT 0,
    error_code         text NOT NULL DEFAULT '',
    duration_ms        integer NOT NULL DEFAULT 0,
    result_count       bigint NOT NULL DEFAULT 0,
    affected_rows      integer NOT NULL DEFAULT 0,

    -- 授权与风险
    permission_key     text,
    policy_decision    text NOT NULL DEFAULT '',
    deny_reason        text NOT NULL DEFAULT '',
    sensitive          boolean NOT NULL DEFAULT false,
    risk_level         text NOT NULL DEFAULT 'low' CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
    approval_id        uuid,
    second_confirm     boolean NOT NULL DEFAULT false,
    requires_review    boolean NOT NULL DEFAULT false,

    -- 变化与上下文
    before             jsonb,
    after              jsonb,
    changes            jsonb,
    detail             jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- 来源与位置
    peer_ip            inet,
    client_ip          inet,
    proxy_chain        text[] NOT NULL DEFAULT '{}',
    user_agent         text NOT NULL DEFAULT '',
    data_source        text NOT NULL DEFAULT '',
    content_hash       text NOT NULL DEFAULT '',
    object_ref         text NOT NULL DEFAULT '',

    -- 防篡改
    prev_hash          text NOT NULL,
    event_hash         text NOT NULL,
    UNIQUE (chain, seq)
);
CREATE INDEX IF NOT EXISTS audit_v2_time_idx ON audit_events_v2 (occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_v2_actor_idx ON audit_events_v2 (actor_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_v2_action_idx ON audit_events_v2 (action, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_v2_resource_idx ON audit_events_v2 (resource_type, resource_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_v2_trace_idx ON audit_events_v2 (trace_id) WHERE trace_id <> '';
CREATE INDEX IF NOT EXISTS audit_v2_session_idx ON audit_events_v2 (session_id) WHERE session_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS audit_v2_category_idx ON audit_events_v2 (category, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_v2_org_idx ON audit_events_v2 (organization_id, occurred_at DESC);

-- 高频用户行为遥测（页面进入、搜索、筛选等）。与法定审计分表，默认 90 天保留。
CREATE TABLE IF NOT EXISTS user_activity_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    occurred_at     timestamptz NOT NULL DEFAULT now(),
    identity_id     uuid REFERENCES identities(id) ON DELETE SET NULL,
    organization_id uuid,
    workspace_id    uuid,
    session_id      uuid,
    category        text NOT NULL,
    action          text NOT NULL,
    route           text NOT NULL DEFAULT '',
    object_type     text NOT NULL DEFAULT '',
    object_id       text NOT NULL DEFAULT '',
    params_digest   text NOT NULL DEFAULT '',
    result_count    bigint NOT NULL DEFAULT 0,
    duration_ms     integer NOT NULL DEFAULT 0,
    data_source     text NOT NULL DEFAULT '',
    detail          jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS user_activity_time_idx ON user_activity_events (occurred_at DESC);
CREATE INDEX IF NOT EXISTS user_activity_identity_idx ON user_activity_events (identity_id, occurred_at DESC);

-- 事务 Outbox：关键审计/管理事件与业务变更在同一事务内落库（§6.5）。
-- 表本身由 0028_investigation_phase0.sql 建立并已被 investigation service 使用；
-- 这里只做 expand-compatible 的增量扩展，不重建、不改语义。
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS dedupe_key text NOT NULL DEFAULT '';
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS locked_until timestamptz;

-- 去重键只对显式提供 dedupe_key 的事件生效（空串不参与唯一性）。
CREATE UNIQUE INDEX IF NOT EXISTS outbox_events_dedupe_uidx
    ON outbox_events (dedupe_key) WHERE dedupe_key <> '';
CREATE INDEX IF NOT EXISTS outbox_events_topic_idx ON outbox_events (subject, created_at DESC);

-- 每日哈希链锚点：归档到启用对象锁的 MinIO 桶（§6.6.5）。
CREATE TABLE IF NOT EXISTS audit_chain_anchors (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    chain        text NOT NULL,
    anchor_date  date NOT NULL,
    from_seq     bigint NOT NULL,
    to_seq       bigint NOT NULL,
    head_hash    text NOT NULL,
    event_count  bigint NOT NULL,
    object_ref   text NOT NULL DEFAULT '',
    content_hash text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (chain, anchor_date)
);

-- 高风险操作审批（双人复核）。0045 会扩展配置版本，这里先建立最小可用的编排表。
CREATE TABLE IF NOT EXISTS approval_requests (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organizations(id),
    operation      text NOT NULL,
    target_type    text NOT NULL DEFAULT '',
    target_id      text NOT NULL DEFAULT '',
    payload        jsonb NOT NULL DEFAULT '{}'::jsonb,
    requested_by   uuid NOT NULL REFERENCES identities(id),
    requested_at   timestamptz NOT NULL DEFAULT now(),
    status         text NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'executed', 'failed', 'cancelled')),
    decided_by     uuid REFERENCES identities(id),
    decided_at     timestamptz,
    decision_note  text NOT NULL DEFAULT '',
    executed_at    timestamptz,
    expires_at     timestamptz,
    result         jsonb
);
CREATE INDEX IF NOT EXISTS approval_requests_status_idx ON approval_requests (status, requested_at DESC);
CREATE INDEX IF NOT EXISTS approval_requests_requester_idx ON approval_requests (requested_by, requested_at DESC);
