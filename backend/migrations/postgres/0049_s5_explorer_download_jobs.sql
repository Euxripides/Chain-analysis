-- 0049 Sprint 5：Explorer/下载/任务/导出基础增量（§28.7 S5-BE-01..03、S5-ADMIN-01）
--
-- 全部 additive：
--   1. 补齐 S5 路由所需的权限键与角色映射（不修改既有键语义）；
--   2. job_event_log：为 SSE Last-Event-ID 提供可重放的单调事件序号；
--   3. export_tickets 增加一次性使用字段（60 秒票据在下载后立即失效）。
-- 数据所有者：下载/导出元数据属 organization+workspace（0044 已设 NOT NULL）。
-- 保留期：job_event_log 365 天（与 download_jobs 同级）；导出票据按 expires_at 清理。

-- ---------------------------------------------------------------- 权限补齐
INSERT INTO permissions (key, category, description, risk_level, sensitive) VALUES
    ('download:refresh', 'download', '显式刷新并新增 Provider 取数', 'high', false),
    ('job:list:workspace', 'job', '列出工作区内任务', 'low', false),
    ('admin:job:list', 'admin.data', '列出全局任务', 'low', false),
    ('admin:export:list', 'admin.data', '列出全局导出', 'medium', true)
ON CONFLICT (key) DO UPDATE SET category = EXCLUDED.category, description = EXCLUDED.description,
    risk_level = EXCLUDED.risk_level, sensitive = EXCLUDED.sensitive;

-- Owner 永远拥有全部显式权限。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r CROSS JOIN permissions p
WHERE r.key = 'owner' AND r.is_system
ON CONFLICT DO NOTHING;

-- Investigator/Analyst：可显式 refresh；可查看工作区内任务。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY['download:refresh', 'job:list:workspace']) AS k
WHERE r.key IN ('investigator', 'analyst') AND r.is_system
ON CONFLICT DO NOTHING;

-- 平台管理员/审计员：全局任务只读；审计员不控制任务。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, 'admin:job:list' FROM roles r
WHERE r.key IN ('platform_admin', 'auditor') AND r.is_system
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, 'admin:export:list' FROM roles r
WHERE r.key = 'platform_admin' AND r.is_system
ON CONFLICT DO NOTHING;

-- 内置 security_admin 保持既定边界：有 admin:job:read 但本次不追加 list，
-- 避免在未审计的迁移里扩大安全管理员的数据面可见范围。

-- ---------------------------------------------------------------- SSE 事件日志
CREATE TABLE IF NOT EXISTS job_event_log (
    id          BIGSERIAL PRIMARY KEY,
    job_id      UUID NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
    event_type  VARCHAR(64) NOT NULL DEFAULT 'update',
    payload     JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS job_event_log_job_id_id_idx ON job_event_log (job_id, id);

COMMENT ON TABLE job_event_log IS '§21.5 SSE Last-Event-ID 重放日志；只保留 task 事件，数据本体在 MinIO/ClickHouse。';

-- ---------------------------------------------------------------- 导出票据一次性
ALTER TABLE export_tickets ADD COLUMN IF NOT EXISTS used_at TIMESTAMPTZ;
ALTER TABLE export_tickets ADD COLUMN IF NOT EXISTS used_by UUID;
CREATE INDEX IF NOT EXISTS idx_export_tickets_active ON export_tickets (export_id) WHERE used_at IS NULL;
