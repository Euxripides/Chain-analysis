-- 0060：MFA/TOTP（计划 §19.9 / AUTH-004）。此前只有 501 占位与 identities.mfa_required 列。
--
-- 设计要点：
--   * 一个身份一行共享密钥（identity_mfa），confirm 之前不生效（confirmed_at 为空）；
--   * 防重放靠 last_used_step：同一个时间步的验证码只能用一次；
--   * 恢复码只存摘要（bytea 32 字节），用掉即写 used_at，绝不回显；
--   * 全部为可加性变更，不改动既有表与既有行。
CREATE TABLE IF NOT EXISTS identity_mfa (
    identity_id    uuid PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
    secret         text        NOT NULL,
    confirmed_at   timestamptz,
    last_used_step bigint      NOT NULL DEFAULT -1,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN identity_mfa.secret IS 'base32 TOTP 共享密钥；confirm 之前视为未启用';
COMMENT ON COLUMN identity_mfa.last_used_step IS '最近一次成功校验的时间步（防重放：同一步只能成功一次）';

CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id uuid        NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    digest      bytea       NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (identity_id, digest)
);

CREATE INDEX IF NOT EXISTS idx_mfa_recovery_codes_identity_unused
    ON mfa_recovery_codes (identity_id) WHERE used_at IS NULL;
