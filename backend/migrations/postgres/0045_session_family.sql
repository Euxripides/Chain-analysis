-- 0045 会话令牌族与重放检测（AUTH-005）
-- 对应 docs/dual-system-development-plan.md §21.4 "旋转 Session token，检测重放"
-- 与 §27.1 AUTH-005 "旧 token 再用触发 family revoke"。
--
-- 背景：原先的"轮换"只是把同一行会话的 token_hash 改掉。这有两个问题：
--   1. 旧令牌被重放时只会得到 401，无法区分"过期"和"有人拿着被偷的旧令牌"，
--      因而无法触发安全响应；
--   2. 没有家族概念，攻击者与合法用户会各自持有互不相干的有效会话。
--
-- 新模型（refresh token rotation with reuse detection）：
--   * 一次登录 = 一个 session family，family_id 指向家族首个会话；
--   * 每次 refresh 生成**新的一行**会话，沿用同一 family_id，
--      旧行标记 revoked_reason='rotated' 并记录 replaced_by；
--   * 若有人拿已被 rotation 淘汰的令牌来认证，视为令牌泄露 -> 撤销整个家族，
--      合法用户也被强制重新登录，攻击者拿到的令牌同时失效。
--   * logout / 强制下线 使用不同的 revoked_reason，不会被误判为重放。

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS family_id uuid;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS parent_id uuid REFERENCES sessions(id);
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS replaced_by uuid REFERENCES sessions(id);
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS rotated_at timestamptz;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS rotation_count integer NOT NULL DEFAULT 0;

-- 既有会话自成一个家族（family_id 指向自己），保证回填后语义一致。
UPDATE sessions SET family_id = id WHERE family_id IS NULL;
ALTER TABLE sessions ALTER COLUMN family_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS sessions_family_idx ON sessions (family_id, created_at DESC);
CREATE INDEX IF NOT EXISTS sessions_parent_idx ON sessions (parent_id) WHERE parent_id IS NOT NULL;

-- 令牌族重放事件。与 login_events 分开存储：
-- 重放检测发生在**每个请求**的认证路径上，写入 login 链会造成高频噪声，
-- 因此只在这里记录去重后的事件，并同步写一条 audit_events_v2（风险等级 high）。
CREATE TABLE IF NOT EXISTS session_reuse_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    family_id      uuid NOT NULL,
    identity_id    uuid NOT NULL,
    organization_id uuid,
    presented_session_id uuid NOT NULL,
    revoked_at     timestamptz,
    revoked_reason text NOT NULL DEFAULT '',
    replaced_by    uuid,
    sessions_revoked integer NOT NULL DEFAULT 0,
    client_ip      inet,
    peer_ip        inet,
    user_agent     text NOT NULL DEFAULT '',
    request_id     text NOT NULL DEFAULT '',
    trace_id       text NOT NULL DEFAULT '',
    detail         jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS session_reuse_events_time_idx ON session_reuse_events (occurred_at DESC);
CREATE INDEX IF NOT EXISTS session_reuse_events_family_idx ON session_reuse_events (family_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS session_reuse_events_identity_idx ON session_reuse_events (identity_id, occurred_at DESC);
