-- 0047 协作与审批底座（Sprint 4 / S4-DB-01）
--
-- 编号偏差说明（§32 约束 9）：计划 §22.1 原定 0044 承载本迁移，但 S3-BE-03 已用
-- 0044_tenant_scope / 0045_session_family / 0046_session_device_fk 处理租户收敛与会话族。
-- 本迁移按真实顺序追加为 0047，内容与 §22.1 的「case members、comments、reviews、
-- report approvals」一致；计划为 0047/0048 预留的 scope 回填与索引已在 0044/本文件完成。
--
-- 原则：全部 additive。不删除/重命名现有列；新增状态列均带默认值，旧行可读。

-- ================================================================ 案件协作

ALTER TABLE cases ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE cases ADD COLUMN IF NOT EXISTS classification VARCHAR(24) NOT NULL DEFAULT 'internal'
    CHECK (classification IN ('public','internal','confidential','restricted'));
ALTER TABLE cases ADD COLUMN IF NOT EXISTS priority VARCHAR(16) NOT NULL DEFAULT 'medium'
    CHECK (priority IN ('low','medium','high','critical'));
ALTER TABLE cases ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;
ALTER TABLE cases ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS case_members (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id      UUID NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    identity_id  UUID NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    role         VARCHAR(24) NOT NULL DEFAULT 'investigator'
                 CHECK (role IN ('lead','investigator','reviewer','viewer')),
    added_by     UUID REFERENCES identities(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (case_id, identity_id)
);
CREATE INDEX IF NOT EXISTS idx_case_members_case ON case_members (case_id, created_at);
CREATE INDEX IF NOT EXISTS idx_case_members_identity ON case_members (identity_id, case_id);

CREATE TABLE IF NOT EXISTS case_comments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id         UUID NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    thread_id       UUID,
    parent_id       UUID REFERENCES case_comments(id) ON DELETE CASCADE,
    body_markdown   TEXT NOT NULL,
    body_hash       VARCHAR(64) NOT NULL,
    created_by      UUID REFERENCES identities(id),
    edited_revision BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_case_comments_case ON case_comments (case_id, created_at DESC);

CREATE TABLE IF NOT EXISTS case_subjects (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id           UUID NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    subject_type      VARCHAR(24) NOT NULL
                      CHECK (subject_type IN ('ADDRESS','TRANSACTION','DOMAIN','IP','APK','ENTITY','OTHER')),
    chain             VARCHAR(32),
    value             TEXT NOT NULL,
    normalized_value  TEXT NOT NULL,
    label             VARCHAR(255) NOT NULL DEFAULT '',
    properties        JSONB NOT NULL DEFAULT '{}',
    revision          BIGINT NOT NULL DEFAULT 1,
    created_by        UUID REFERENCES identities(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (case_id, subject_type, chain, normalized_value)
);
CREATE INDEX IF NOT EXISTS idx_case_subjects_case ON case_subjects (case_id, created_at);

CREATE TABLE IF NOT EXISTS case_activity (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id           UUID NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    event_type        VARCHAR(64) NOT NULL,
    actor_identity_id UUID REFERENCES identities(id),
    actor_display     VARCHAR(255) NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '',
    object_type       VARCHAR(48) NOT NULL DEFAULT '',
    object_id         TEXT NOT NULL DEFAULT '',
    detail            JSONB NOT NULL DEFAULT '{}',
    trace_id          VARCHAR(64) NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_case_activity_case ON case_activity (case_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_case_activity_actor ON case_activity (actor_identity_id, created_at DESC);

-- ================================================================ 复核请求

CREATE TABLE IF NOT EXISTS review_requests (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID NOT NULL REFERENCES organizations(id),
    workspace_id      UUID NOT NULL REFERENCES workspaces(id),
    case_id           UUID REFERENCES cases(id) ON DELETE SET NULL,
    investigation_id  UUID REFERENCES investigations(id) ON DELETE SET NULL,
    resource_type     VARCHAR(32) NOT NULL CHECK (resource_type IN ('investigation','finding','evidence','report')),
    resource_id       UUID NOT NULL,
    requested_by      UUID REFERENCES identities(id),
    reviewer_id       UUID REFERENCES identities(id),
    status            VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
    reason            TEXT NOT NULL DEFAULT '',
    decision_note     TEXT NOT NULL DEFAULT '',
    revision          BIGINT NOT NULL DEFAULT 1,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at        TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_review_requests_reviewer ON review_requests (reviewer_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_review_requests_resource ON review_requests (resource_type, resource_id, status);
CREATE INDEX IF NOT EXISTS idx_review_requests_scope ON review_requests (organization_id, workspace_id, status, created_at DESC);

-- ================================================================ Finding / Evidence 不可变复核模型

ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS nature VARCHAR(16) NOT NULL DEFAULT 'DETERMINISTIC'
    CHECK (nature IN ('CONFIRMED','INFERRED','HEURISTIC','DETERMINISTIC'));
ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS reviewed_by UUID REFERENCES identities(id);
ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;
ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS review_note TEXT NOT NULL DEFAULT '';
ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS supersedes_id UUID REFERENCES investigation_findings(id);
ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS superseded_by UUID REFERENCES investigation_findings(id);
ALTER TABLE investigation_findings ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES identities(id);
UPDATE investigation_findings SET nature = CASE
        WHEN algorithm = 'analysis.advanced_patterns' AND status = 'candidate' THEN 'INFERRED'
        WHEN status = 'confirmed' THEN 'CONFIRMED'
        ELSE 'DETERMINISTIC' END
WHERE nature = 'DETERMINISTIC' AND (algorithm = 'analysis.advanced_patterns' OR status = 'confirmed');
CREATE INDEX IF NOT EXISTS idx_findings_investigation_status ON investigation_findings (investigation_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_findings_supersedes ON investigation_findings (supersedes_id) WHERE supersedes_id IS NOT NULL;

ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS status VARCHAR(24) NOT NULL DEFAULT 'collected'
    CHECK (status IN ('collected','verified','disputed','superseded'));
ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS nature VARCHAR(16) NOT NULL DEFAULT 'DETERMINISTIC'
    CHECK (nature IN ('CONFIRMED','INFERRED','HEURISTIC','DETERMINISTIC'));
ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS supersedes_id UUID REFERENCES investigation_evidence(id);
ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS superseded_by UUID REFERENCES investigation_evidence(id);
ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES identities(id);
ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS verified_by UUID REFERENCES identities(id);
ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS verified_at TIMESTAMPTZ;
ALTER TABLE investigation_evidence ADD COLUMN IF NOT EXISTS collected_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS idx_evidence_investigation_status ON investigation_evidence (investigation_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_evidence_supersedes ON investigation_evidence (supersedes_id) WHERE supersedes_id IS NOT NULL;

-- ================================================================ 报告审批与下载票据

ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS status VARCHAR(24) NOT NULL DEFAULT 'generated'
    CHECK (status IN ('generated','in_review','approved','rejected','signed','archived'));
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS status_revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS submitted_at TIMESTAMPTZ;
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS submitted_by UUID REFERENCES identities(id);
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ;
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS approved_by UUID REFERENCES identities(id);
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS rejected_at TIMESTAMPTZ;
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS rejected_by UUID REFERENCES identities(id);
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS reject_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS signed_at TIMESTAMPTZ;
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS signed_by UUID REFERENCES identities(id);
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS signature_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE investigation_reports ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_reports_case_status ON investigation_reports (case_id, status, generated_at DESC);

CREATE TABLE IF NOT EXISTS report_approvals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id       UUID NOT NULL REFERENCES investigation_reports(id) ON DELETE CASCADE,
    report_revision BIGINT NOT NULL,
    reviewer_id     UUID REFERENCES identities(id),
    decision        VARCHAR(16) NOT NULL CHECK (decision IN ('approve','reject')),
    content_hash    VARCHAR(64) NOT NULL DEFAULT '',
    note            TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (report_id, report_revision, reviewer_id, decision)
);
CREATE INDEX IF NOT EXISTS idx_report_approvals_report ON report_approvals (report_id, report_revision, created_at DESC);

CREATE TABLE IF NOT EXISTS report_download_tickets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id       UUID NOT NULL REFERENCES investigation_reports(id) ON DELETE CASCADE,
    token_hash      VARCHAR(64) NOT NULL UNIQUE,
    user_id         UUID REFERENCES identities(id),
    organization_id UUID REFERENCES organizations(id),
    workspace_id    UUID REFERENCES workspaces(id),
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_report_tickets_expiry ON report_download_tickets (expires_at) WHERE used_at IS NULL;

-- ================================================================ 通知（§22.5 / USER-010）

CREATE TABLE IF NOT EXISTS notifications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id),
    workspace_id    UUID REFERENCES workspaces(id),
    type            VARCHAR(48) NOT NULL,
    title_key       VARCHAR(128) NOT NULL,
    title           TEXT NOT NULL DEFAULT '',
    body            TEXT NOT NULL DEFAULT '',
    object_type     VARCHAR(48) NOT NULL DEFAULT '',
    object_id       TEXT NOT NULL DEFAULT '',
    payload         JSONB NOT NULL DEFAULT '{}',
    read_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications (user_id, read_at, created_at DESC);

CREATE TABLE IF NOT EXISTS notification_delivery_attempts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    notification_id     UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    channel             VARCHAR(24) NOT NULL DEFAULT 'inbox',
    status              VARCHAR(24) NOT NULL DEFAULT 'delivered',
    attempts            INTEGER NOT NULL DEFAULT 1,
    provider_message_id TEXT NOT NULL DEFAULT '',
    error_redacted      TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ================================================================ 权限补齐（Sprint 4 路由实际用到的键）

INSERT INTO permissions (key, category, description, risk_level, sensitive) VALUES
    ('investigation:start',         'investigation', '启动调查', 'medium', false),
    ('investigation:pause',         'investigation', '暂停调查', 'medium', false),
    ('investigation:resume',        'investigation', '恢复调查', 'medium', false),
    ('investigation:retry',         'investigation', '重试调查', 'medium', false),
    ('investigation:submit_review', 'investigation', '提交调查复核', 'medium', false),
    ('subject:list',                'case',          '查看案件对象', 'low', false),
    ('subject:create',              'case',          '新增案件对象', 'medium', false),
    ('subject:update',              'case',          '修改案件对象', 'medium', false),
    ('subject:remove',              'case',          '移除案件对象', 'high', false),
    ('evidence:list',               'evidence',      '列出证据', 'low', false),
    ('report:review',               'report',        '复核报告', 'high', false)
ON CONFLICT (key) DO UPDATE SET category = EXCLUDED.category, description = EXCLUDED.description,
    risk_level = EXCLUDED.risk_level, sensitive = EXCLUDED.sensitive;

-- Owner 在 0041 已获得当时的全量权限；新增权限必须显式补授，否则业务闭环会以 403 收尾。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r CROSS JOIN permissions p
WHERE r.key = 'owner' AND r.is_system
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'investigation:start','investigation:pause','investigation:resume','investigation:retry',
    'investigation:submit_review','subject:list','subject:create','subject:update','subject:remove',
    'evidence:list','report:review'
]) AS k WHERE r.key = 'investigator' AND r.is_system
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'investigation:start','investigation:pause','investigation:resume','investigation:retry',
    'subject:list','subject:create','subject:update','evidence:list'
]) AS k WHERE r.key = 'analyst' AND r.is_system
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'investigation:submit_review','subject:list','evidence:list','report:review'
]) AS k WHERE r.key = 'reviewer' AND r.is_system
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY['subject:list','evidence:list']) AS k
WHERE r.key IN ('viewer','auditor') AND r.is_system
ON CONFLICT DO NOTHING;

-- ================================================================ 数据资产登记（§32 约束 4）

INSERT INTO data_asset_registry (table_name, owner_module, sensitivity, retention, notes) VALUES
    ('case_members',                  'case',         'internal',     '随案件归档',       '案件成员与角色'),
    ('case_comments',                 'case',         'confidential', '随案件归档',       '协作评论，正文不写秘密凭证'),
    ('case_subjects',                 'case',         'confidential', '随案件归档',       '案件调查对象'),
    ('case_activity',                 'case',         'internal',     '在线 365 天',       '用户可理解的业务时间线'),
    ('review_requests',               'review',       'confidential', '随对象归档',       'Finding/Evidence/报告复核请求'),
    ('report_approvals',              'report',       'restricted',   '长期保留（证据）',   '报告 revision 审批记录'),
    ('report_download_tickets',       'report',       'restricted',   '票据过期后 30 天',   '只保存 token 摘要'),
    ('notifications',                 'notification', 'internal',     '90 天',            '站内通知'),
    ('notification_delivery_attempts','notification', 'internal',     '90 天',            '送达尝试（脱敏错误）')
ON CONFLICT (table_name) DO UPDATE
    SET owner_module = EXCLUDED.owner_module, sensitivity = EXCLUDED.sensitivity,
        retention = EXCLUDED.retention, notes = EXCLUDED.notes, updated_at = now();
