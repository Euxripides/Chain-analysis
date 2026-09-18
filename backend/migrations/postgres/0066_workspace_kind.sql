-- 0066_workspace_kind.sql
-- 用户级数据隔离的基础（计划 docs/multiuser-login-and-isolation-plan.md §3.3 / 决策 D1、D6）。
--
-- 背景：此前 workspaces 只有 (id, organization_id, name, slug, created_at)，无法表达「个人空间」；
--   管理员新建用户时若未指定工作区，代码会回退到「该组织最早创建的工作区」，于是所有用户
--   落进同一个公共工作区（线上 default 工作区 30 名成员 / 26 个案件），互相可见。
--
-- 本迁移引入：
--   * kind = 'personal' | 'shared'：个人空间与共享工作区；
--   * owner_identity_id：个人空间的归属人（1:1，唯一索引保证）；
--   * archived_at：删除（离职）用户时其个人空间归档为只读（决策 D6）；
--   * created_by_identity：谁建的这个工作区（审计与回溯）。
--
-- 幂等与安全：只加列加约束，不改写既有数据；现有工作区一律保持 kind='shared'（语义不变）。

ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'shared';
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS owner_identity_id uuid REFERENCES identities(id);
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS created_by_identity uuid REFERENCES identities(id);
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS archived_at timestamptz;

ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_kind_check;
ALTER TABLE workspaces ADD CONSTRAINT workspaces_kind_check CHECK (kind IN ('personal', 'shared'));

-- 个人空间必须有归属人；共享工作区不需要。
ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_personal_owner_check;
ALTER TABLE workspaces ADD CONSTRAINT workspaces_personal_owner_check
    CHECK (kind = 'shared' OR owner_identity_id IS NOT NULL);

-- 一个用户最多一个个人空间。
CREATE UNIQUE INDEX IF NOT EXISTS workspaces_personal_owner_uidx
    ON workspaces (owner_identity_id) WHERE kind = 'personal';

CREATE INDEX IF NOT EXISTS workspaces_archived_idx
    ON workspaces (archived_at) WHERE archived_at IS NOT NULL;

-- 自检：不允许出现既非 personal/shared、又缺归属人的个人空间。
DO $$
DECLARE
    bad integer;
BEGIN
    SELECT count(*) INTO bad
      FROM workspaces
     WHERE kind NOT IN ('personal', 'shared')
        OR (kind = 'personal' AND owner_identity_id IS NULL);
    IF bad > 0 THEN
        RAISE EXCEPTION '0066 self-check failed: % workspace rows violate kind/owner invariants', bad;
    END IF;
END $$;
