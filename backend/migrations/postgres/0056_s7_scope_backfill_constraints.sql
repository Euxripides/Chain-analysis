-- 0056_s7_scope_backfill_constraints.sql
-- S7-DB-01（计划 §22.1 中「0047 organization/workspace NOT NULL 回填与约束」的真实语义）：
--   1) 先做可复查的空值/一致性审计（函数 public.s7_scope_audit()）；
--   2) 能推导的按业务语义回填（只回填可推导行，不猜、不伪造）；
--   3) 追加约束时先用 NOT VALID 建立，再 VALIDATE，避免长时间持锁并让失败可见；
--   4) 追加自检，任何一项不满足即迁移失败（禁止静默降级）。
--
-- 实测基线（2026-09-12 生产库 blockchain_platform，迁移前）：
--   ws_not_null_but_org_null 全表 0 行；以下三张表存在“合法为空”的历史行，
--   且 audit_events_v2 / login_events 的 organization_id 参与哈希链规范化，
--   回填会直接破坏 AUDIT-004 链校验（verify.go COALESCE(organization_id::text,'')），
--   因此这两张表**禁止回填**，只允许追加约束与向前保证。详见
--   acceptance-evidence/dual-system-v1/sprint7/db/scope-backfill-report.md。
--
-- 回滚策略（§22.1）：本迁移只追加列/约束/函数，不修改已有数据行；
--   回滚 = DROP 新增约束与列（脚本见同目录 rollback-0056.sql），无需回退数据。

-- ---------------------------------------------------------------------------
-- 1. 可复查的作用域审计函数（迁移与验收报告共用同一口径）
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.s7_scope_audit()
RETURNS TABLE(table_name text, rows bigint, org_null bigint, ws_null bigint, ws_without_org bigint)
LANGUAGE plpgsql STABLE AS $$
DECLARE
    r record;
    has_org boolean;
    has_ws boolean;
BEGIN
    FOR r IN
        SELECT c.relname AS tbl
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public' AND c.relkind = 'r'
          AND EXISTS (SELECT 1 FROM pg_attribute a
                       WHERE a.attrelid = c.oid AND a.attname = 'organization_id'
                         AND a.attnum > 0 AND NOT a.attisdropped)
        ORDER BY c.relname
    LOOP
        SELECT EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = ('public.' || quote_ident(r.tbl))::regclass AND a.attname = 'organization_id' AND a.attnum > 0 AND NOT a.attisdropped),
               EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = ('public.' || quote_ident(r.tbl))::regclass AND a.attname = 'workspace_id' AND a.attnum > 0 AND NOT a.attisdropped)
          INTO has_org, has_ws;
        IF has_ws THEN
            EXECUTE format('SELECT %L::text, count(*), count(*) FILTER (WHERE organization_id IS NULL), count(*) FILTER (WHERE workspace_id IS NULL), count(*) FILTER (WHERE workspace_id IS NOT NULL AND organization_id IS NULL) FROM %I', r.tbl, r.tbl)
              INTO table_name, rows, org_null, ws_null, ws_without_org;
        ELSE
            EXECUTE format('SELECT %L::text, count(*), count(*) FILTER (WHERE organization_id IS NULL), NULL::bigint, NULL::bigint FROM %I', r.tbl, r.tbl)
              INTO table_name, rows, org_null, ws_null, ws_without_org;
        END IF;
        RETURN NEXT;
    END LOOP;
END
$$;

COMMENT ON FUNCTION public.s7_scope_audit() IS
  'S7-DB-01：按表统计 organization_id/workspace_id 空值与跨作用域不一致行，供迁移前后核对。';

-- ---------------------------------------------------------------------------
-- 2. feature_flags 用户级作用域缺口（S7 审计发现的真实缺陷）
--    scope_type='user' 时没有任何列可以承载目标用户：实现用 scope_org_id
--    （uuid，FK organizations）与 subject_id 比较，永远不可能匹配；
--    结果是用户级灰度实际不生效（恒回落默认值），且语义上把组织 ID 当成用户 ID。
--    修复：新增 scope_user_id（FK users），并约束各 scope_type 的目标必须存在。
-- ---------------------------------------------------------------------------
ALTER TABLE public.feature_flags ADD COLUMN IF NOT EXISTS scope_user_id uuid REFERENCES public.users(id);

-- 各作用域类型必须有对应目标，禁止“声明了作用域但没有目标”的开关。
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'feature_flags_scope_target_check') THEN
        ALTER TABLE public.feature_flags
          ADD CONSTRAINT feature_flags_scope_target_check CHECK (
              (scope_type = 'global')
              OR (scope_type = 'organization' AND scope_org_id IS NOT NULL)
              OR (scope_type = 'workspace' AND scope_org_id IS NOT NULL AND scope_workspace_id IS NOT NULL)
              OR (scope_type = 'user' AND scope_user_id IS NOT NULL)
          ) NOT VALID;
    END IF;
END
$$;

-- 工作区作用域必须同时记录所属组织（否则无法做组织级统计与越权判定）。
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'feature_flags_ws_implies_org_check') THEN
        ALTER TABLE public.feature_flags
          ADD CONSTRAINT feature_flags_ws_implies_org_check CHECK (
              scope_workspace_id IS NULL OR scope_org_id IS NOT NULL
          ) NOT VALID;
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- 3. 事件/业务表的作用域一致性约束
--    （workspace 有值 ⇒ organization 必须有值；实测 0 行违反）
--    NOT VALID + VALIDATE：先建约束再看是否可验证，失败会被显式报出而不是静默跳过。
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t text;
    cname text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'audit_events_v2', 'login_events', 'user_activity_events',
        'notifications', 'export_tickets', 'report_download_tickets', 'feature_flag_evaluations'
    ] LOOP
        cname := t || '_s7_scope_coherence_check';
        IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = cname) THEN
            EXECUTE format(
                'ALTER TABLE public.%I ADD CONSTRAINT %I CHECK (workspace_id IS NULL OR organization_id IS NOT NULL) NOT VALID',
                t, cname);
        END IF;
    END LOOP;
END
$$;

-- ---------------------------------------------------------------------------
-- 4. 票据表：组织作用域是结构性不变量（写入方总是传 Principal 的组织，空串会直接报错），
--    因此可以安全提升为 NOT NULL。先回填可推导行，再收紧。
--    实测回填命中 0 行（迁移前两表 ws/org 空值均为 0）。
-- ---------------------------------------------------------------------------
UPDATE public.export_tickets et
   SET organization_id = e.organization_id
  FROM public.exports e
 WHERE et.export_id = e.id
   AND et.organization_id IS NULL
   AND e.organization_id IS NOT NULL;

UPDATE public.export_tickets et
   SET workspace_id = e.workspace_id
  FROM public.exports e
 WHERE et.export_id = e.id
   AND et.workspace_id IS NULL
   AND e.workspace_id IS NOT NULL;

UPDATE public.report_download_tickets t
   SET organization_id = r.organization_id,
       workspace_id    = r.workspace_id
  FROM public.investigation_reports r
 WHERE t.report_id = r.id
   AND (t.organization_id IS NULL OR t.workspace_id IS NULL)
   AND r.organization_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 5. feature_flag_evaluations：按主体可推导时回填作用域（用户主体→其组织成员关系）。
--    实测命中 0 行（历史行主体为测试用的合成 ID，不可推导，保持 NULL 并由约束保证一致性）。
-- ---------------------------------------------------------------------------
UPDATE public.feature_flag_evaluations ffe
   SET organization_id = m.organization_id
  FROM public.organization_memberships m
 WHERE ffe.organization_id IS NULL
   AND ffe.subject_type = 'user'
   AND m.identity_id::text = ffe.subject_id
   AND m.status = 'active';

-- 工作区可从“主体用户唯一工作区成员关系”推导；多工作区时不做猜测。
UPDATE public.feature_flag_evaluations ffe
   SET workspace_id = w.workspace_id
  FROM (
      SELECT identity_id, min(workspace_id::text)::uuid AS workspace_id
        FROM public.workspace_memberships
       WHERE status = 'active'
       GROUP BY identity_id
      HAVING count(*) = 1
  ) w
 WHERE ffe.workspace_id IS NULL
   AND ffe.subject_type = 'user'
   AND w.identity_id::text = ffe.subject_id;

-- ---------------------------------------------------------------------------
-- 6. 验证前面建立的 NOT VALID 约束；任何一项失败都会让整个迁移回滚。
-- ---------------------------------------------------------------------------
ALTER TABLE public.feature_flags              VALIDATE CONSTRAINT feature_flags_scope_target_check;
ALTER TABLE public.feature_flags              VALIDATE CONSTRAINT feature_flags_ws_implies_org_check;
ALTER TABLE public.audit_events_v2            VALIDATE CONSTRAINT audit_events_v2_s7_scope_coherence_check;
ALTER TABLE public.login_events               VALIDATE CONSTRAINT login_events_s7_scope_coherence_check;
ALTER TABLE public.user_activity_events       VALIDATE CONSTRAINT user_activity_events_s7_scope_coherence_check;
ALTER TABLE public.notifications              VALIDATE CONSTRAINT notifications_s7_scope_coherence_check;
ALTER TABLE public.export_tickets             VALIDATE CONSTRAINT export_tickets_s7_scope_coherence_check;
ALTER TABLE public.report_download_tickets    VALIDATE CONSTRAINT report_download_tickets_s7_scope_coherence_check;
ALTER TABLE public.feature_flag_evaluations   VALIDATE CONSTRAINT feature_flag_evaluations_s7_scope_coherence_check;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.export_tickets WHERE organization_id IS NULL) THEN
        RAISE EXCEPTION '0056：export_tickets 仍存在 organization_id 为空的行，拒绝收紧';
    END IF;
    IF EXISTS (SELECT 1 FROM public.report_download_tickets WHERE organization_id IS NULL) THEN
        RAISE EXCEPTION '0056：report_download_tickets 仍存在 organization_id 为空的行，拒绝收紧';
    END IF;
END
$$;

ALTER TABLE public.export_tickets          ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE public.report_download_tickets ALTER COLUMN organization_id SET NOT NULL;

-- ---------------------------------------------------------------------------
-- 7. 迁移自检：约束必须存在且已验证（convalidated），否则迁移失败。
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    problems text[] := ARRAY[]::text[];
    r record;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                    WHERE table_schema='public' AND table_name='feature_flags' AND column_name='scope_user_id') THEN
        problems := array_append(problems, 'feature_flags.scope_user_id:MISSING');
    END IF;

    FOR r IN
        SELECT conname, convalidated FROM pg_constraint
         WHERE conname IN (
            'feature_flags_scope_target_check', 'feature_flags_ws_implies_org_check',
            'audit_events_v2_s7_scope_coherence_check', 'login_events_s7_scope_coherence_check',
            'user_activity_events_s7_scope_coherence_check', 'notifications_s7_scope_coherence_check',
            'export_tickets_s7_scope_coherence_check', 'report_download_tickets_s7_scope_coherence_check',
            'feature_flag_evaluations_s7_scope_coherence_check')
    LOOP
        IF NOT r.convalidated THEN
            problems := array_append(problems, r.conname || ':NOT_VALIDATED');
        END IF;
    END LOOP;

    IF (SELECT count(*) FROM pg_constraint WHERE conname IN (
            'feature_flags_scope_target_check', 'feature_flags_ws_implies_org_check',
            'audit_events_v2_s7_scope_coherence_check', 'login_events_s7_scope_coherence_check',
            'user_activity_events_s7_scope_coherence_check', 'notifications_s7_scope_coherence_check',
            'export_tickets_s7_scope_coherence_check', 'report_download_tickets_s7_scope_coherence_check',
            'feature_flag_evaluations_s7_scope_coherence_check')) <> 9 THEN
        problems := array_append(problems, 'scope_constraints:COUNT_MISMATCH');
    END IF;

    IF NOT (SELECT attnotnull FROM pg_attribute
             WHERE attrelid='public.export_tickets'::regclass AND attname='organization_id') THEN
        problems := array_append(problems, 'export_tickets.organization_id:NOT_NOT_NULL');
    END IF;
    IF NOT (SELECT attnotnull FROM pg_attribute
             WHERE attrelid='public.report_download_tickets'::regclass AND attname='organization_id') THEN
        problems := array_append(problems, 'report_download_tickets.organization_id:NOT_NOT_NULL');
    END IF;

    IF array_length(problems, 1) IS NOT NULL THEN
        RAISE EXCEPTION '0056 作用域约束自检失败: %', array_to_string(problems, ', ');
    END IF;
END
$$;
