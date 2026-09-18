-- 0067_identity_deletion.sql
-- 删除（离职）用户的持久化基础（计划 §3.2 / 决策 D6）。
--
-- 关键约束：**绝不物理删除 identities**。审计链（audit_events_v2）、登录事件（login_events）与
-- 大量业务对象通过外键引用 identities(id)，物理删除会破坏 append-only 语义与历史可回溯性
-- （见 AD-005 / ADR-018）。因此「删除」= 软删 + 匿名化：
--   * status 增加 'deleted'；
--   * deleted_at 记录删除时刻；
--   * email/display_name 由应用层改写为不可识别值（唯一索引 identities_email_lower_uidx
--     要求改写后的邮箱仍然唯一，因此使用 deleted+<uuid>@invalid 形式）。
--
-- 幂等与安全：只加列与放宽 CHECK，不改写既有数据。

ALTER TABLE identities ADD COLUMN IF NOT EXISTS deleted_at timestamptz;

ALTER TABLE identities DROP CONSTRAINT IF EXISTS identities_status_check;
ALTER TABLE identities ADD CONSTRAINT identities_status_check
    CHECK (status IN ('invited', 'active', 'disabled', 'locked', 'deleted'));

CREATE INDEX IF NOT EXISTS identities_deleted_idx
    ON identities (deleted_at) WHERE deleted_at IS NOT NULL;

-- 自检：状态与时间戳必须一致（不允许「已删除但没有时间」或「有时间却没标删除」）。
DO $$
DECLARE
    bad integer;
BEGIN
    SELECT count(*) INTO bad
      FROM identities
     WHERE (status = 'deleted' AND deleted_at IS NULL)
        OR (status <> 'deleted' AND deleted_at IS NOT NULL);
    IF bad > 0 THEN
        RAISE EXCEPTION '0067 self-check failed: % identities rows have inconsistent deleted_at/status', bad;
    END IF;
END $$;
