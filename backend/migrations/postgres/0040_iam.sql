-- 0040 IAM：身份、凭据、会话、登录事件、可信设备
-- 对应 docs/dual-system-development-plan.md §6.4 / §22.2 / Sprint S2-DB-01。
--
-- 迁移策略（expand-compatible）：
--   * 只新增表，不修改、不删除 users / audit_events。
--   * identities.id 与既有 users.id 一一对应（回填时沿用原 UUID），
--     因此 download_jobs.created_by、investigations.created_by、audit_events.user_id
--     等既有外键无需改写即可继续解析到同一主体。
--   * users.role 仅作为迁移来源；成员关系与角色绑定的回填在 0041。

CREATE TABLE IF NOT EXISTS identities (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      uuid NOT NULL REFERENCES organizations(id),
    email                text NOT NULL,
    display_name         text NOT NULL DEFAULT '',
    status               text NOT NULL DEFAULT 'active'
                         CHECK (status IN ('invited', 'active', 'disabled', 'locked')),
    is_platform_admin    boolean NOT NULL DEFAULT false,
    mfa_required         boolean NOT NULL DEFAULT false,
    must_change_password boolean NOT NULL DEFAULT false,
    failed_login_count   integer NOT NULL DEFAULT 0,
    locked_until         timestamptz,
    last_login_at        timestamptz,
    last_login_ip        inet,
    password_changed_at  timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    -- 指向迁移来源 users 行；保留用于核对与回滚，不参与运行时授权。
    legacy_user_id       uuid REFERENCES users(id)
);

CREATE UNIQUE INDEX IF NOT EXISTS identities_email_lower_uidx ON identities (lower(email));
CREATE UNIQUE INDEX IF NOT EXISTS identities_legacy_user_uidx ON identities (legacy_user_id) WHERE legacy_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS identities_org_status_idx ON identities (organization_id, status);

-- 凭据：密码 / TOTP / 恢复码 / OIDC 绑定。secret_hash 只存哈希或密文。
CREATE TABLE IF NOT EXISTS credentials (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id  uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    kind         text NOT NULL CHECK (kind IN ('password', 'totp', 'recovery_code', 'oidc')),
    algorithm    text NOT NULL DEFAULT 'argon2id',
    secret_hash  text NOT NULL,
    params       jsonb NOT NULL DEFAULT '{}'::jsonb,
    label        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz,
    revoked_at   timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS credentials_password_uidx
    ON credentials (identity_id) WHERE kind = 'password' AND revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS credentials_identity_idx ON credentials (identity_id, kind);

-- 可信设备：设备 ID 随机生成，不采集硬件指纹。
CREATE TABLE IF NOT EXISTS trusted_devices (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id   uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    device_label  text NOT NULL DEFAULT '',
    platform      text NOT NULL DEFAULT '',
    browser       text NOT NULL DEFAULT '',
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    trusted_until timestamptz,
    revoked_at    timestamptz,
    created_ip    inet,
    last_ip       inet
);
CREATE INDEX IF NOT EXISTS trusted_devices_identity_idx ON trusted_devices (identity_id, last_seen_at DESC);

-- 会话：服务端可撤销。Cookie 中只放随机不透明令牌，DB 只存 SHA-256。
CREATE TABLE IF NOT EXISTS sessions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id         uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    organization_id     uuid NOT NULL REFERENCES organizations(id),
    workspace_id        uuid REFERENCES workspaces(id),
    audience            text NOT NULL CHECK (audience IN ('user', 'admin')),
    token_hash          text NOT NULL,
    csrf_hash           text NOT NULL,
    auth_method         text NOT NULL DEFAULT 'password',
    mfa_satisfied       boolean NOT NULL DEFAULT false,
    device_id           uuid REFERENCES trusted_devices(id),
    user_agent          text NOT NULL DEFAULT '',
    client_hints        jsonb NOT NULL DEFAULT '{}'::jsonb,
    device              jsonb NOT NULL DEFAULT '{}'::jsonb,
    peer_ip             inet,
    client_ip           inet,
    proxy_chain         text[] NOT NULL DEFAULT '{}',
    created_at          timestamptz NOT NULL DEFAULT now(),
    last_seen_at        timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    revoked_at          timestamptz,
    revoked_reason      text NOT NULL DEFAULT '',
    revoked_by          uuid REFERENCES identities(id)
);
CREATE UNIQUE INDEX IF NOT EXISTS sessions_token_uidx ON sessions (token_hash);
CREATE INDEX IF NOT EXISTS sessions_identity_idx ON sessions (identity_id, audience, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS sessions_active_idx ON sessions (expires_at) WHERE revoked_at IS NULL;

-- 登录事件：每次登录尝试都记录，含失败。append-only。
CREATE TABLE IF NOT EXISTS login_events (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    seq               bigint NOT NULL,
    chain             text NOT NULL DEFAULT 'login',
    occurred_at       timestamptz NOT NULL DEFAULT now(),
    event_version     integer NOT NULL DEFAULT 1,
    environment       text NOT NULL DEFAULT '',
    event_type        text NOT NULL,
    result            text NOT NULL CHECK (result IN ('success', 'failure', 'denied', 'blocked')),
    reason_code       text NOT NULL DEFAULT '',
    risk_level        text NOT NULL DEFAULT 'low',
    risk_action       text NOT NULL DEFAULT '',
    identity_id       uuid REFERENCES identities(id),
    subject_digest    text NOT NULL DEFAULT '',
    subject_masked    text NOT NULL DEFAULT '',
    organization_id   uuid,
    workspace_id      uuid,
    audience          text NOT NULL DEFAULT 'user',
    auth_method       text NOT NULL DEFAULT 'password',
    identity_provider text NOT NULL DEFAULT 'local',
    mfa_method        text NOT NULL DEFAULT '',
    mfa_result        text NOT NULL DEFAULT '',
    session_id        uuid,
    device_id         uuid,
    is_new_device     boolean NOT NULL DEFAULT false,
    device_trusted    boolean NOT NULL DEFAULT false,
    device_label      text NOT NULL DEFAULT '',
    device_type       text NOT NULL DEFAULT '',
    device_vendor     text NOT NULL DEFAULT '',
    device_model      text NOT NULL DEFAULT '',
    os_name           text NOT NULL DEFAULT '',
    os_version        text NOT NULL DEFAULT '',
    browser_name      text NOT NULL DEFAULT '',
    browser_version   text NOT NULL DEFAULT '',
    user_agent        text NOT NULL DEFAULT '',
    client_hints      jsonb NOT NULL DEFAULT '{}'::jsonb,
    peer_ip           inet,
    client_ip         inet,
    proxy_chain       text[] NOT NULL DEFAULT '{}',
    entry_host        text NOT NULL DEFAULT '',
    scheme            text NOT NULL DEFAULT '',
    request_id        text NOT NULL DEFAULT '',
    trace_id          text NOT NULL DEFAULT '',
    duration_ms       integer NOT NULL DEFAULT 0,
    rate_limited      boolean NOT NULL DEFAULT false,
    alert_id          uuid,
    ip_intel          jsonb NOT NULL DEFAULT '{}'::jsonb,
    detail            jsonb NOT NULL DEFAULT '{}'::jsonb,
    prev_hash         text NOT NULL,
    event_hash        text NOT NULL,
    UNIQUE (chain, seq)
);
CREATE INDEX IF NOT EXISTS login_events_time_idx ON login_events (occurred_at DESC);
CREATE INDEX IF NOT EXISTS login_events_identity_idx ON login_events (identity_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS login_events_ip_idx ON login_events (client_ip, occurred_at DESC);
CREATE INDEX IF NOT EXISTS login_events_result_idx ON login_events (result, occurred_at DESC);
CREATE INDEX IF NOT EXISTS login_events_session_idx ON login_events (session_id);

-- 哈希链头：审计链与登录链共用的单调序号 + 前序哈希。
CREATE TABLE IF NOT EXISTS audit_chain_heads (
    chain      text PRIMARY KEY,
    last_seq   bigint NOT NULL DEFAULT 0,
    last_hash  text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO audit_chain_heads (chain, last_seq, last_hash) VALUES
    ('audit', 0, ''), ('login', 0, '')
ON CONFLICT (chain) DO NOTHING;

-- ---- 回填：既有 users → identities（保持 UUID 不变）----
INSERT INTO identities (id, organization_id, email, display_name, status, is_platform_admin, legacy_user_id, created_at, updated_at)
SELECT u.id,
       COALESCE(u.organization_id, (SELECT id FROM organizations WHERE slug = 'default' LIMIT 1)),
       u.email,
       u.display_name,
       'active',
       (u.role = 'owner'),
       u.id,
       u.created_at,
       now()
FROM users u
WHERE NOT EXISTS (SELECT 1 FROM identities i WHERE i.id = u.id);
