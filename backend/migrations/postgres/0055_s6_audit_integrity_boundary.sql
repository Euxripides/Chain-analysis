-- 0055_s6_audit_integrity_boundary.sql
-- Sprint 6 审计修复（阻断项）：不可恢复审计缺口的「不可变完整性事件边界」。
--
-- 背景（真实事件，见 docs/implementation/sprint6-report.md §6）：
--   AUDIT-003 的负向用例在验收环境上真实执行了 TRUNCATE audit_events_v2，
--   清空了 chain='audit' 的 seq 1..2343。该区间没有任何可用恢复源：
--     - 无 pg_dump / 物理基备（archive_mode=off，无 WAL 归档，无 LVM/VM 快照）；
--     - MinIO blockchain-audit 桶只保存每日 anchor.json（不含事件本体）；
--     - ClickHouse 无审计镜像表；audit_exports 全部晚于事件发生时间（from_seq>=2344）。
--   因此历史审计行不可恢复，且**不允许**伪造或回填（那会破坏 append-only 与可检测性本身）。
--
-- 本迁移建立「完整性事件边界」的数据结构，用于：
--   1. 显式检测并封存不可恢复缺口（缺口范围 / 幸存起点 / 封存尾哈希）；
--   2. 引用保留的旧锚点（不删除、不改写 audit_chain_anchors 任何既有行）；
--   3. 明确「旧链不连续 + 新链可信起点」，使边界之后的链与锚点可以逐项验证；
--   4. 让边界创建、审批与哈希证据对应用运行角色完全不可 UPDATE/DELETE/TRUNCATE/DROP。
--
-- 重要：本迁移不写入任何 audit_events_v2 行，不改写 audit_chain_heads，
--       不修改 audit_chain_anchors 既有内容；封存动作由后台 API 走双人审批执行。

-- ---------------------------------------------------------------------------
-- 1. 不可变守卫：用于完整性事件边界三张表。
--    TRUNCATE 不触发行级触发器，因此运行角色侧另有 REVOKE（见第 4 节）。
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION audit_integrity_immutable_guard() RETURNS trigger AS $$
BEGIN
    IF current_setting('app.audit_maintenance', true) = 'on' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'audit integrity record % is immutable; % is not permitted', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

-- ---------------------------------------------------------------------------
-- 2. 完整性事件（不可恢复缺口）主表
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_integrity_incidents (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_ref        text NOT NULL UNIQUE,
    chain               text NOT NULL,
    kind                text NOT NULL DEFAULT 'unrecoverable_gap'
                        CHECK (kind IN ('unrecoverable_gap')),
    -- 已确认不可恢复的缺口区间（含端点）
    gap_from_seq        bigint NOT NULL,
    gap_to_seq          bigint NOT NULL,
    gap_event_count     bigint NOT NULL,
    -- 新链可信起点：从该 seq 起可用 sealed_tail_hash 逐行复算
    trusted_from_seq    bigint NOT NULL,
    -- sealed_tail_hash = 幸存首行的 prev_hash，即被销毁的最后一条事件的 event_hash。
    -- 这是被销毁链尾的唯一保留密码学证据，也是新链与旧链的衔接点。
    sealed_tail_hash    text NOT NULL,
    sealed_tail_source  text NOT NULL DEFAULT '',
    detection_method    text NOT NULL DEFAULT '',
    detected_at         timestamptz NOT NULL DEFAULT now(),
    detected_by_email   text NOT NULL DEFAULT '',
    sealed_at           timestamptz NOT NULL DEFAULT now(),
    sealed_by           uuid REFERENCES identities(id),
    sealed_by_email     text NOT NULL DEFAULT '',
    reason              text NOT NULL,
    approval_request_id uuid REFERENCES approval_requests(id),
    -- 恢复尝试与观测证据（含保留锚点引用、恢复源排查结论）
    evidence            jsonb NOT NULL DEFAULT '{}'::jsonb,
    evidence_hash       text NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_integrity_incidents_gap_order CHECK (gap_to_seq >= gap_from_seq),
    CONSTRAINT audit_integrity_incidents_trusted_after_gap CHECK (trusted_from_seq = gap_to_seq + 1),
    CONSTRAINT audit_integrity_incidents_head_hash_len CHECK (length(sealed_tail_hash) = 64),
    CONSTRAINT audit_integrity_incidents_evidence_hash_len CHECK (length(evidence_hash) = 64),
    UNIQUE (chain, gap_from_seq, gap_to_seq)
);
CREATE INDEX IF NOT EXISTS audit_integrity_incidents_chain_idx
    ON audit_integrity_incidents (chain, sealed_at DESC);

DROP TRIGGER IF EXISTS audit_integrity_incidents_immutable ON audit_integrity_incidents;
CREATE TRIGGER audit_integrity_incidents_immutable
    BEFORE UPDATE OR DELETE ON audit_integrity_incidents
    FOR EACH ROW EXECUTE FUNCTION audit_integrity_immutable_guard();

-- ---------------------------------------------------------------------------
-- 3. 边界引用的保留锚点（旧锚点原样保留，本表只做只读引用与归档一致性证据）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_integrity_incident_anchors (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id    uuid NOT NULL REFERENCES audit_integrity_incidents(id),
    chain          text NOT NULL,
    anchor_date    date NOT NULL,
    from_seq       bigint NOT NULL,
    to_seq         bigint NOT NULL,
    head_hash      text NOT NULL,
    event_count    bigint NOT NULL,
    object_ref     text NOT NULL DEFAULT '',
    content_hash   text NOT NULL DEFAULT '',
    -- 封存时刻重新读取 MinIO 归档对象算出的 SHA-256，用于证明归档件未被替换
    archive_hash   text NOT NULL DEFAULT '',
    archive_intact boolean NOT NULL DEFAULT false,
    disposition    text NOT NULL DEFAULT 'sealed_unrecoverable'
                   CHECK (disposition IN ('sealed_unrecoverable')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (incident_id, chain, anchor_date)
);

DROP TRIGGER IF EXISTS audit_integrity_incident_anchors_immutable ON audit_integrity_incident_anchors;
CREATE TRIGGER audit_integrity_incident_anchors_immutable
    BEFORE UPDATE OR DELETE ON audit_integrity_incident_anchors
    FOR EACH ROW EXECUTE FUNCTION audit_integrity_immutable_guard();

-- ---------------------------------------------------------------------------
-- 4. 边界创建 / 审批 / 哈希证据事件链（自身也是顺序哈希链，可逐条复算）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_integrity_incident_events (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_ref        text NOT NULL,
    incident_id         uuid REFERENCES audit_integrity_incidents(id),
    approval_request_id uuid REFERENCES approval_requests(id),
    chain               text NOT NULL,
    seq                 bigint NOT NULL,
    event               text NOT NULL
                        CHECK (event IN ('detected','requested','approved','rejected','sealed','failed')),
    actor_id            uuid,
    actor_email         text NOT NULL DEFAULT '',
    note                text NOT NULL DEFAULT '',
    detail              jsonb NOT NULL DEFAULT '{}'::jsonb,
    prev_hash           text NOT NULL,
    event_hash          text NOT NULL,
    occurred_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (incident_ref, seq),
    CONSTRAINT audit_integrity_incident_events_hash_len CHECK (length(event_hash) = 64)
);
CREATE INDEX IF NOT EXISTS audit_integrity_incident_events_ref_idx
    ON audit_integrity_incident_events (incident_ref, seq);

DROP TRIGGER IF EXISTS audit_integrity_incident_events_immutable ON audit_integrity_incident_events;
CREATE TRIGGER audit_integrity_incident_events_immutable
    BEFORE UPDATE OR DELETE ON audit_integrity_incident_events
    FOR EACH ROW EXECUTE FUNCTION audit_integrity_immutable_guard();

-- ---------------------------------------------------------------------------
-- 5. 既有每日锚点永久不可改写
--    此前 audit_chain_anchors 允许 ON CONFLICT DO UPDATE（归档器幂等写入）。
--    这里把「锚点内容」冻结：重复写入必须逐字节相同才允许（等价于幂等 no-op），
--    任何真实改写或删除都会被数据库拒绝（不依赖应用代码自律）。
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION audit_chain_anchors_immutable() RETURNS trigger AS $$
BEGIN
    IF current_setting('app.audit_maintenance', true) = 'on' THEN
        RETURN NEW;
    END IF;
    IF NEW.chain <> OLD.chain OR NEW.anchor_date <> OLD.anchor_date
       OR NEW.from_seq <> OLD.from_seq OR NEW.to_seq <> OLD.to_seq
       OR NEW.head_hash <> OLD.head_hash OR NEW.event_count <> OLD.event_count
       OR NEW.object_ref <> OLD.object_ref OR NEW.content_hash <> OLD.content_hash THEN
        RAISE EXCEPTION 'audit anchor %.% is immutable; content rewrite is not permitted',
            OLD.chain, OLD.anchor_date
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_chain_anchors_immutable_trg ON audit_chain_anchors;
CREATE TRIGGER audit_chain_anchors_immutable_trg
    BEFORE UPDATE ON audit_chain_anchors
    FOR EACH ROW EXECUTE FUNCTION audit_chain_anchors_immutable();

DROP TRIGGER IF EXISTS audit_chain_anchors_no_delete ON audit_chain_anchors;
CREATE TRIGGER audit_chain_anchors_no_delete
    BEFORE DELETE ON audit_chain_anchors
    FOR EACH ROW EXECUTE FUNCTION audit_append_only_guard();

-- ---------------------------------------------------------------------------
-- 6. 权限目录：封存边界是 critical 操作，需独立权限键（与 policy/catalog.go 一致）
-- ---------------------------------------------------------------------------
INSERT INTO permissions (key, category, description, risk_level, sensitive) VALUES
    ('admin:audit:seal', 'admin.security', '封存审计链完整性缺口边界', 'critical', false)
ON CONFLICT (key) DO UPDATE SET category = EXCLUDED.category, description = EXCLUDED.description,
    risk_level = EXCLUDED.risk_level, sensitive = EXCLUDED.sensitive;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r
CROSS JOIN (VALUES ('admin:audit:seal')) AS p(key)
WHERE r.key IN ('owner', 'security_admin') AND r.is_system
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- 7. 运行角色权限收紧（§27.6 AUDIT-003）
--    0054 的 ALTER DEFAULT PRIVILEGES 会给新表授予 UPDATE/DELETE，
--    这里必须显式回收；TRUNCATE 已由 0054 的全库 REVOKE 覆盖，仍逐表重申。
-- ---------------------------------------------------------------------------
GRANT SELECT, INSERT ON audit_integrity_incidents TO blockchain_app;
GRANT SELECT, INSERT ON audit_integrity_incident_anchors TO blockchain_app;
GRANT SELECT, INSERT ON audit_integrity_incident_events TO blockchain_app;

REVOKE UPDATE, DELETE, TRUNCATE ON audit_integrity_incidents FROM blockchain_app;
REVOKE UPDATE, DELETE, TRUNCATE ON audit_integrity_incident_anchors FROM blockchain_app;
REVOKE UPDATE, DELETE, TRUNCATE ON audit_integrity_incident_events FROM blockchain_app;
-- 归档器自 0055 起改用 ON CONFLICT DO NOTHING，不再需要 UPDATE；锚点对运行角色彻底 append-only。
REVOKE UPDATE, DELETE, TRUNCATE ON audit_chain_anchors FROM blockchain_app;

-- ---------------------------------------------------------------------------
-- 8. 自检：运行角色权限矩阵与不可变性必须与声明一致，否则迁移失败（不允许静默降级）
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    problems text[] := ARRAY[]::text[];
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['audit_integrity_incidents','audit_integrity_incident_anchors','audit_integrity_incident_events'] LOOP
        IF has_table_privilege('blockchain_app', t, 'UPDATE') THEN
            problems := array_append(problems, t || ':UPDATE_GRANTED');
        END IF;
        IF has_table_privilege('blockchain_app', t, 'DELETE') THEN
            problems := array_append(problems, t || ':DELETE_GRANTED');
        END IF;
        IF has_table_privilege('blockchain_app', t, 'TRUNCATE') THEN
            problems := array_append(problems, t || ':TRUNCATE_GRANTED');
        END IF;
        IF NOT has_table_privilege('blockchain_app', t, 'INSERT') THEN
            problems := array_append(problems, t || ':INSERT_MISSING');
        END IF;
        IF NOT has_table_privilege('blockchain_app', t, 'SELECT') THEN
            problems := array_append(problems, t || ':SELECT_MISSING');
        END IF;
        -- DROP 需要 owner 身份；运行角色不得是这三张表的 owner。
        IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_roles r ON r.oid = c.relowner
                   WHERE c.relname = t AND r.rolname = 'blockchain_app') THEN
            problems := array_append(problems, t || ':OWNED_BY_RUNTIME_ROLE');
        END IF;
    END LOOP;

    IF has_table_privilege('blockchain_app', 'audit_chain_anchors', 'TRUNCATE') THEN
        problems := array_append(problems, 'audit_chain_anchors:TRUNCATE_GRANTED');
    END IF;
    IF has_table_privilege('blockchain_app', 'audit_chain_anchors', 'UPDATE') THEN
        problems := array_append(problems, 'audit_chain_anchors:UPDATE_GRANTED');
    END IF;
    IF NOT has_table_privilege('blockchain_app', 'audit_chain_anchors', 'INSERT') THEN
        problems := array_append(problems, 'audit_chain_anchors:INSERT_MISSING');
    END IF;
    IF has_table_privilege('blockchain_app', 'audit_chain_anchors', 'DELETE') THEN
        problems := array_append(problems, 'audit_chain_anchors:DELETE_GRANTED');
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'audit_chain_anchors_immutable_trg' AND NOT tgisinternal) THEN
        problems := array_append(problems, 'audit_chain_anchors:UPDATE_GUARD_MISSING');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'audit_chain_anchors_no_delete' AND NOT tgisinternal) THEN
        problems := array_append(problems, 'audit_chain_anchors:DELETE_GUARD_MISSING');
    END IF;

    IF array_length(problems, 1) IS NOT NULL THEN
        RAISE EXCEPTION '0055 完整性边界权限自检失败: %', array_to_string(problems, ', ');
    END IF;
END
$$;
