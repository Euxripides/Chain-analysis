-- 0050 Sprint 5 修正：download_jobs 需要按 IAM identity 记录创建者。
--
-- 背景：download_jobs.created_by 的外键指向 legacy users(id)，而 Sprint 2 之后
-- 新建的 fixture/自定义 IAM identity 并不保证有 legacy user 行（fixture-* 账号
-- 的 identities.legacy_user_id 就是 NULL）。S5 v2 下载若把 identity.id 写进
-- created_by，会被 FK 拒绝，导致所有新下载创建失败（真实缺陷，已在 E2E 暴露）。
--
-- 修复方式 additive：
--   1. 新增 created_by_identity REFERENCES identities(id)，用于 v2 用户作用域；
--   2. 保留 created_by 及其 FK，legacy/MCP 调用方行为不变；
--   3. 能从 legacy_user_id 推导的旧任务做一次幂等回填。
ALTER TABLE download_jobs
    ADD COLUMN IF NOT EXISTS created_by_identity UUID REFERENCES identities(id);

UPDATE download_jobs j
SET created_by_identity = i.id
FROM identities i
WHERE j.created_by_identity IS NULL
  AND j.created_by IS NOT NULL
  AND i.legacy_user_id = j.created_by;

CREATE INDEX IF NOT EXISTS download_jobs_identity_scope_idx
    ON download_jobs (organization_id, workspace_id, created_by_identity, updated_at DESC);
