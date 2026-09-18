-- 0061：MFA 登录第二步的一次性挑战（计划 §21.4 / AUTH-004）。
--
-- 契约：需要 MFA 时登录返回 202，challenge_id 仅可使用一次且 5 分钟过期。
--
-- 设计要点：
--   * 明文 challenge token 只在签发响应里出现一次，库里只存 SHA-256 摘要；
--   * 身份、受众与签发时的请求上下文由服务端落库，第二步不信任客户端自报字段；
--   * attempts 为提交次数（含成功的一次），并发下用「先自增再校验」保证计数原子；
--   * consumed_at 一次性消费，重复提交必然影响 0 行；
--   * 全部为可加性变更，不改动既有表与既有行。
CREATE TABLE IF NOT EXISTS mfa_login_challenges (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash      text        NOT NULL UNIQUE,
    identity_id     uuid        NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    audience        text        NOT NULL DEFAULT 'user',
    organization_id uuid,
    workspace_id    uuid,
    client_ip       text        NOT NULL DEFAULT '',
    peer_ip         text        NOT NULL DEFAULT '',
    proxy_chain     text[]      NOT NULL DEFAULT '{}',
    user_agent      text        NOT NULL DEFAULT '',
    entry_host      text        NOT NULL DEFAULT '',
    scheme          text        NOT NULL DEFAULT '',
    request_id      text        NOT NULL DEFAULT '',
    trace_id        text        NOT NULL DEFAULT '',
    attempts        int         NOT NULL DEFAULT 0,
    consumed_at     timestamptz,
    expires_at      timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE mfa_login_challenges IS '登录第二步的一次性挑战；明文 token 不落库，只存 SHA-256 摘要';
COMMENT ON COLUMN mfa_login_challenges.attempts IS '该挑战已提交的验证码次数（含成功的一次），上限由应用层控制';
COMMENT ON COLUMN mfa_login_challenges.consumed_at IS '一次性消费时间；非空表示该挑战已作废（无论成功或失败）';

CREATE INDEX IF NOT EXISTS idx_mfa_login_challenges_identity
    ON mfa_login_challenges (identity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mfa_login_challenges_pending
    ON mfa_login_challenges (expires_at) WHERE consumed_at IS NULL;
