-- 0052_s6_data_governance.sql
-- Sprint 6 / S6-DB-01（§22.1 原 0046 语义，编号顺延至 0052）
-- 内容：保留策略、删除请求、备份/恢复/校验元数据。
-- 安全默认：实际删除执行默认关闭，由 configuration_settings.governance.deletion.execution_enabled 控制（S6-BE-04）。
-- 原则：全部为新增表与新增列，旧二进制可继续运行（AD-006）。

-- ---------------------------------------------------------------------------
-- 1. 保留策略
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS retention_policies (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_key        text NOT NULL UNIQUE,
    description       text NOT NULL DEFAULT '',
    resource_type     text NOT NULL
                      CHECK (resource_type IN ('raw_object', 'export_object', 'user_activity',
                                               'audit_archive', 'clickhouse_partition', 'cache_object',
                                               'job_artifact')),
    scope_type        text NOT NULL DEFAULT 'global'
                      CHECK (scope_type IN ('global', 'organization', 'workspace')),
    scope_org_id      uuid REFERENCES organizations(id),
    scope_workspace_id uuid REFERENCES workspaces(id),
    retain_days       integer NOT NULL DEFAULT 365 CHECK (retain_days >= 0),
    archive_after_days integer CHECK (archive_after_days IS NULL OR archive_after_days >= 0),
    action            text NOT NULL DEFAULT 'archive'
                      CHECK (action IN ('archive', 'delete', 'none')),
    grace_days        integer NOT NULL DEFAULT 7 CHECK (grace_days >= 0),
    legal_hold        boolean NOT NULL DEFAULT false,
    enabled           boolean NOT NULL DEFAULT false,
    last_run_at       timestamptz,
    next_run_at       timestamptz,
    revision          bigint NOT NULL DEFAULT 1,
    updated_by        uuid REFERENCES identities(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS retention_policies_resource_idx
    ON retention_policies (resource_type, enabled);

-- 影响预览与执行记录：preview 不改数据，只在 dry_run 下统计。
CREATE TABLE IF NOT EXISTS retention_runs (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id           uuid REFERENCES retention_policies(id) ON DELETE SET NULL,
    policy_key          text NOT NULL DEFAULT '',
    mode                text NOT NULL DEFAULT 'preview'
                        CHECK (mode IN ('preview', 'execute')),
    status              text NOT NULL DEFAULT 'preview'
                        CHECK (status IN ('preview', 'pending_approval', 'approved', 'executing',
                                          'completed', 'failed', 'blocked_disabled', 'rejected')),
    dry_run             boolean NOT NULL DEFAULT true,
    scanned_objects     bigint NOT NULL DEFAULT 0,
    matched_objects     bigint NOT NULL DEFAULT 0,
    matched_bytes       bigint NOT NULL DEFAULT 0,
    sample              jsonb NOT NULL DEFAULT '[]'::jsonb,
    impact              jsonb NOT NULL DEFAULT '{}'::jsonb,
    block_reason        text NOT NULL DEFAULT '',
    approval_request_id uuid REFERENCES approval_requests(id),
    requested_by        uuid REFERENCES identities(id),
    requested_by_email  text NOT NULL DEFAULT '',
    started_at          timestamptz,
    finished_at         timestamptz,
    result              jsonb,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS retention_runs_policy_idx
    ON retention_runs (policy_key, created_at DESC);

-- ---------------------------------------------------------------------------
-- 2. 删除请求（双人审批 + 可恢复窗口；执行默认关闭）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS deletion_requests (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type        text NOT NULL,
    policy_id            uuid REFERENCES retention_policies(id) ON DELETE SET NULL,
    target_scope         jsonb NOT NULL DEFAULT '{}'::jsonb,
    reason               text NOT NULL DEFAULT '',
    status               text NOT NULL DEFAULT 'previewed'
                         CHECK (status IN ('previewed', 'pending_approval', 'approved', 'rejected',
                                           'executing', 'completed', 'failed', 'cancelled', 'blocked_disabled')),
    dry_run              boolean NOT NULL DEFAULT true,
    execution_enabled    boolean NOT NULL DEFAULT false,
    matched_objects      bigint NOT NULL DEFAULT 0,
    matched_bytes        bigint NOT NULL DEFAULT 0,
    preview              jsonb NOT NULL DEFAULT '{}'::jsonb,
    approval_request_id  uuid REFERENCES approval_requests(id),
    requested_by         uuid REFERENCES identities(id),
    requested_by_email   text NOT NULL DEFAULT '',
    approved_by          uuid REFERENCES identities(id),
    restore_window_until timestamptz,
    executed_at          timestamptz,
    result               jsonb,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS deletion_requests_status_idx
    ON deletion_requests (status, created_at DESC);

-- ---------------------------------------------------------------------------
-- 3. 备份 / 恢复 / 校验元数据（A-BACKUP-01、DATA-009）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS backup_jobs (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind                text NOT NULL CHECK (kind IN ('backup', 'restore', 'verify')),
    target              text NOT NULL CHECK (target IN ('postgres', 'clickhouse', 'minio', 'config', 'all')),
    status              text NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'running', 'completed', 'failed', 'verified',
                                          'blocked', 'rejected', 'previewed')),
    drill               boolean NOT NULL DEFAULT false,
    requested_by        uuid REFERENCES identities(id),
    requested_by_email  text NOT NULL DEFAULT '',
    approval_request_id uuid REFERENCES approval_requests(id),
    backup_id           uuid REFERENCES backup_jobs(id) ON DELETE SET NULL,
    started_at          timestamptz,
    finished_at         timestamptz,
    size_bytes          bigint NOT NULL DEFAULT 0,
    object_ref          text NOT NULL DEFAULT '',
    checksum            text NOT NULL DEFAULT '',
    schema_version      text NOT NULL DEFAULT '',
    build_sha           text NOT NULL DEFAULT '',
    verify_status       text NOT NULL DEFAULT '',
    verify_detail       jsonb NOT NULL DEFAULT '{}'::jsonb,
    rpo_seconds         bigint,
    rto_seconds         bigint,
    maintenance_window  text NOT NULL DEFAULT '',
    notes               text NOT NULL DEFAULT '',
    result              jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS backup_jobs_kind_idx ON backup_jobs (kind, created_at DESC);

-- 恢复影响预览：列出会被覆盖/合并的对象、冲突数与维护窗口要求。
CREATE TABLE IF NOT EXISTS restore_previews (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    backup_id           uuid REFERENCES backup_jobs(id) ON DELETE SET NULL,
    target              text NOT NULL DEFAULT 'all',
    scope               jsonb NOT NULL DEFAULT '{}'::jsonb,
    impact              jsonb NOT NULL DEFAULT '{}'::jsonb,
    conflict_count      bigint NOT NULL DEFAULT 0,
    verified            boolean NOT NULL DEFAULT false,
    verify_detail       jsonb NOT NULL DEFAULT '{}'::jsonb,
    maintenance_window  text NOT NULL DEFAULT '',
    approval_request_id uuid REFERENCES approval_requests(id),
    requested_by        uuid REFERENCES identities(id),
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS restore_previews_backup_idx
    ON restore_previews (backup_id, created_at DESC);


-- ---------------------------------------------------------------------------
-- 5. 审计导出任务（A-AUDIT-03 / ADMIN-008）：条件、行数、manifest、SHA-256 可校验。
--    审计事件本身仍为 append-only，这里只保存导出产物元数据。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_exports (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    format            text NOT NULL DEFAULT 'jsonl' CHECK (format IN ('jsonl', 'csv')),
    chain             text NOT NULL DEFAULT 'audit',
    filters           jsonb NOT NULL DEFAULT '{}'::jsonb,
    status            text NOT NULL DEFAULT 'queued'
                      CHECK (status IN ('queued', 'running', 'completed', 'failed', 'expired')),
    requested_by      uuid REFERENCES identities(id),
    requested_by_email text NOT NULL DEFAULT '',
    approval_request_id uuid REFERENCES approval_requests(id),
    event_count       bigint NOT NULL DEFAULT 0,
    from_seq          bigint,
    to_seq            bigint,
    first_event_at    timestamptz,
    last_event_at     timestamptz,
    object_ref        text NOT NULL DEFAULT '',
    manifest_ref      text NOT NULL DEFAULT '',
    size_bytes        bigint NOT NULL DEFAULT 0,
    sha256            text NOT NULL DEFAULT '',
    manifest          jsonb NOT NULL DEFAULT '{}'::jsonb,
    error             text NOT NULL DEFAULT '',
    started_at        timestamptz,
    completed_at      timestamptz,
    expires_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_exports_created_idx ON audit_exports (created_at DESC);

-- ---------------------------------------------------------------------------
-- 4. 基线数据（幂等）：策略默认全部 enabled=false，删除动作默认关闭。
-- ---------------------------------------------------------------------------
INSERT INTO retention_policies (policy_key, description, resource_type, retain_days, archive_after_days, action, grace_days, legal_hold, enabled) VALUES
    ('raw-objects-default',      '原始 Provider 响应对象（MinIO raw bucket）', 'raw_object',          365,  90,   'archive', 7,  false, false),
    ('export-objects-default',   '用户导出产物（MinIO export bucket）',        'export_object',        90,  NULL, 'delete',  7,  false, false),
    ('user-activity-default',    '用户高频行为事件（§22.4，90 天归档）',        'user_activity',        90,  90,   'archive', 7,  false, false),
    ('audit-archive-default',    '审计归档（合规留存，禁止自动删除）',          'audit_archive',      2555,  365,  'archive', 30, true,  false),
    ('clickhouse-partition-default', 'ClickHouse 明细分区（超期转冷）',         'clickhouse_partition', 730, 365,  'archive', 30, false, false),
    ('cache-objects-default',    '可重建缓存对象',                              'cache_object',         30,  NULL, 'delete',  3,  false, false)
ON CONFLICT (policy_key) DO NOTHING;