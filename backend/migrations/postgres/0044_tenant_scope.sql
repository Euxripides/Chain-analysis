-- 0044 租户作用域收敛（S3-BE-03）
-- 对应 docs/dual-system-development-plan.md §6.4 / §28.5 S3-BE-03。
--
-- 目标：cases / investigations / canvases / jobs / exports / reports 的
-- organization_id 与 workspace_id 必须非空且可索引，使"查询强制作用域"
-- 与"跨租户负向测试"成为可实现、可验证的约束。
--
-- 数据现状（迁移前实测）：
--   cases                 org/ws 均 NOT NULL                 —— 已合规
--   investigation_canvases org/ws 均 NOT NULL                —— 已合规
--   investigations        org 已填、ws 24/24 为 NULL
--   download_jobs         org/ws 2347/2347 为 NULL
--   exports               org/ws 88/129 为 NULL
--   investigation_reports 完全没有作用域列
--
-- 回填规则（确定性，不猜）：
--   1. 能从父对象推导的，用父对象推导（report → investigation，job → 创建人）。
--   2. 推导不出的，落到该组织（或 default 组织）的 default 工作区。
--   3. 回填完成后才加 NOT NULL，保证迁移可在存量数据上重复执行。

-- ---------------------------------------------------------------- 回填

-- investigations：workspace 从 case 推导，否则落到组织的 default 工作区
UPDATE investigations i
SET workspace_id = COALESCE(
        (SELECT c.workspace_id FROM cases c WHERE c.id = i.case_id),
        (SELECT w.id FROM workspaces w
          WHERE w.organization_id = COALESCE(i.organization_id,
                (SELECT id FROM organizations WHERE slug = 'default' LIMIT 1))
          ORDER BY (w.slug = 'default') DESC, w.slug LIMIT 1)
    )
WHERE i.workspace_id IS NULL;

UPDATE investigations i
SET organization_id = COALESCE(i.organization_id,
        (SELECT w.organization_id FROM workspaces w WHERE w.id = i.workspace_id),
        (SELECT id FROM organizations WHERE slug = 'default' LIMIT 1))
WHERE i.organization_id IS NULL;

-- download_jobs：优先取创建人所属组织，其次 default
UPDATE download_jobs j
SET organization_id = COALESCE(
        (SELECT ident.organization_id FROM identities ident WHERE ident.id = j.created_by),
        (SELECT id FROM organizations WHERE slug = 'default' LIMIT 1))
WHERE j.organization_id IS NULL;

UPDATE download_jobs j
SET workspace_id = (SELECT w.id FROM workspaces w
                     WHERE w.organization_id = j.organization_id
                     ORDER BY (w.slug = 'default') DESC, w.slug LIMIT 1)
WHERE j.workspace_id IS NULL;

-- exports：从所属 job 推导，其次创建人组织
UPDATE exports e
SET organization_id = COALESCE(
        (SELECT j.organization_id FROM download_jobs j WHERE j.id = e.job_id),
        (SELECT ident.organization_id FROM identities ident WHERE ident.id = e.created_by),
        (SELECT id FROM organizations WHERE slug = 'default' LIMIT 1))
WHERE e.organization_id IS NULL;

UPDATE exports e
SET workspace_id = COALESCE(
        (SELECT j.workspace_id FROM download_jobs j WHERE j.id = e.job_id),
        (SELECT w.id FROM workspaces w
          WHERE w.organization_id = e.organization_id
          ORDER BY (w.slug = 'default') DESC, w.slug LIMIT 1))
WHERE e.workspace_id IS NULL;

-- investigation_reports：新增作用域列，从 investigation 推导
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS organization_id uuid;
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS workspace_id uuid;
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS case_id uuid;
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS generated_by uuid;

UPDATE investigation_reports r
SET organization_id = i.organization_id,
    workspace_id    = i.workspace_id,
    case_id         = i.case_id
FROM investigations i
WHERE i.id = r.investigation_id
  AND (r.organization_id IS NULL OR r.workspace_id IS NULL);

UPDATE investigation_reports r
SET organization_id = COALESCE(r.organization_id,
        (SELECT id FROM organizations WHERE slug = 'default' LIMIT 1))
WHERE r.organization_id IS NULL;

UPDATE investigation_reports r
SET workspace_id = (SELECT w.id FROM workspaces w
                     WHERE w.organization_id = r.organization_id
                     ORDER BY (w.slug = 'default') DESC, w.slug LIMIT 1)
WHERE r.workspace_id IS NULL;

-- ---------------------------------------------------------------- 约束

-- 先确认没有残留 NULL，否则 NOT NULL 会失败并回滚整个迁移（fail loud 优于静默跳过）。
DO $$
DECLARE
    bad text;
BEGIN
    SELECT string_agg(t, ', ') INTO bad FROM (
        SELECT 'investigations' AS t WHERE EXISTS (SELECT 1 FROM investigations WHERE organization_id IS NULL OR workspace_id IS NULL)
        UNION ALL
        SELECT 'download_jobs' WHERE EXISTS (SELECT 1 FROM download_jobs WHERE organization_id IS NULL OR workspace_id IS NULL)
        UNION ALL
        SELECT 'exports' WHERE EXISTS (SELECT 1 FROM exports WHERE organization_id IS NULL OR workspace_id IS NULL)
        UNION ALL
        SELECT 'investigation_reports' WHERE EXISTS (SELECT 1 FROM investigation_reports WHERE organization_id IS NULL OR workspace_id IS NULL)
    ) s;
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'tenant scope backfill incomplete for: %', bad;
    END IF;
END $$;

ALTER TABLE investigations        ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE investigations        ALTER COLUMN workspace_id    SET NOT NULL;
ALTER TABLE download_jobs         ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE download_jobs         ALTER COLUMN workspace_id    SET NOT NULL;
ALTER TABLE exports               ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE exports               ALTER COLUMN workspace_id    SET NOT NULL;
ALTER TABLE investigation_reports ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE investigation_reports ALTER COLUMN workspace_id    SET NOT NULL;

-- 外键（此前缺省，跨租户写入缺少数据库层兜底）
DO $$ BEGIN
    ALTER TABLE investigation_reports
        ADD CONSTRAINT investigation_reports_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE investigation_reports
        ADD CONSTRAINT investigation_reports_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ---------------------------------------------------------------- 索引
-- 列表查询一律是"先按作用域过滤，再按时间倒序"，因此建复合索引。
CREATE INDEX IF NOT EXISTS investigations_scope_time_idx
    ON investigations (organization_id, workspace_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS download_jobs_scope_time_idx
    ON download_jobs (organization_id, workspace_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS exports_scope_time_idx
    ON exports (organization_id, workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS investigation_reports_scope_time_idx
    ON investigation_reports (organization_id, workspace_id, generated_at DESC);
CREATE INDEX IF NOT EXISTS investigation_reports_investigation_idx
    ON investigation_reports (investigation_id, revision DESC);
CREATE INDEX IF NOT EXISTS cases_scope_time_idx
    ON cases (organization_id, workspace_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS investigation_canvases_scope_time_idx
    ON investigation_canvases (organization_id, workspace_id, updated_at DESC);

-- 数据所有者 / 保留期 / 敏感级别（§32 约束 4）：登记到数据字典表，供后台"数据治理"页读取。
CREATE TABLE IF NOT EXISTS data_asset_registry (
    table_name   text PRIMARY KEY,
    owner_module text NOT NULL,
    scope_column text NOT NULL DEFAULT 'organization_id',
    sensitivity  text NOT NULL DEFAULT 'internal'
                 CHECK (sensitivity IN ('public', 'internal', 'confidential', 'restricted')),
    retention    text NOT NULL DEFAULT '',
    notes        text NOT NULL DEFAULT '',
    updated_at   timestamptz NOT NULL DEFAULT now()
);

INSERT INTO data_asset_registry (table_name, owner_module, sensitivity, retention, notes) VALUES
    ('cases',                 'case',          'confidential', '案件归档后 3 年',   '案件元数据，含密级与成员'),
    ('investigations',        'investigation', 'confidential', '随案件归档',        '调查目标、预算与状态'),
    ('investigation_canvases','canvas',        'confidential', '随调查归档',        '图谱画布与快照引用'),
    ('investigation_reports', 'report',        'restricted',   '长期保留（证据）',   '报告对象引用与内容哈希'),
    ('download_jobs',         'download',      'internal',     '365 天',            '下载任务与游标'),
    ('exports',               'export',        'confidential', '按导出策略',         '导出文件引用与 SHA-256'),
    ('audit_events_v2',       'audit',         'restricted',   '在线 365 天 / 归档 3 年', 'append-only，触发器保护'),
    ('login_events',          'audit',         'restricted',   '在线 365 天 / 归档 3 年', 'append-only，触发器保护')
ON CONFLICT (table_name) DO UPDATE
    SET owner_module = EXCLUDED.owner_module,
        sensitivity  = EXCLUDED.sensitivity,
        retention    = EXCLUDED.retention,
        notes        = EXCLUDED.notes,
        updated_at   = now();
