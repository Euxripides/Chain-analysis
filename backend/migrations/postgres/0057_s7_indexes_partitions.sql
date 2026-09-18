-- 0057_s7_indexes_partitions.sql
-- S7-DB-01（计划 §22.1 中「0048 查询索引、分区和归档任务」的真实语义）。
--
-- 本迁移只做三件事，且全部可回滚（见 backend/migrations/rollback/rollback-0057.sql，
-- §22.1：可删除新索引，不回退数据）：
--   1) 依据真实查询谓词补齐缺失索引（每条都给出代码位置）；
--   2) 把 §22.4 明确要求「按月分区 + 定期归档」的 user_activity_events 改造成范围分区表，
--      改造方式为「旧表整体 ATTACH 为历史分区」——不复制行、不删除行、不改写任何业务数据；
--   3) 为 login_events / audit_events_v2 提供机器可读的分区就绪门禁，
--      把"为什么现在不分区"变成可复查结论，而不是口头说明。
--
-- 实测基线（2026-09-12）：pg_stat_user_tables 中 download_pages seq_scan=4435982、checkpoints seq_scan=876；
--   provider_usage 414663 行、download_pages 165469 行、checkpoints 19962 行、user_activity_events 0 行。
-- 详见 acceptance-evidence/dual-system-v1/sprint7/db/index-partition-verification.md。

-- ---------------------------------------------------------------------------
-- 1. 缺失索引（每条都对应一个真实查询）
-- ---------------------------------------------------------------------------

-- engine.go:387  SELECT COALESCE(SUM(rows_inserted),0) FROM download_pages WHERE job_id=$1 AND status IN ('INGESTED','VALIDATED')
CREATE INDEX IF NOT EXISTS idx_download_pages_job_status
    ON public.download_pages (job_id, status);

-- sprint5_jobs.go:127 / :194  按 job_id 读取 checkpoint（PK 只有 shard_id）
CREATE INDEX IF NOT EXISTS idx_checkpoints_job
    ON public.checkpoints (job_id);

-- sprint5_jobs.go:273  FROM exports WHERE job_id=$1 AND organization_id=$2 ORDER BY created_at DESC LIMIT 20
CREATE INDEX IF NOT EXISTS idx_exports_job_org_created
    ON public.exports (job_id, organization_id, created_at DESC);

-- adminv1.go:609-616  后台审计列表 ORDER BY seq DESC LIMIT/OFFSET（无 chain 过滤，用不上 (chain,seq) 唯一索引）
CREATE INDEX IF NOT EXISTS idx_audit_v2_seq_desc
    ON public.audit_events_v2 (seq DESC);

-- adminv1.go:583-585  审计列表按 result 过滤 + ORDER BY seq DESC
CREATE INDEX IF NOT EXISTS idx_audit_v2_result_seq
    ON public.audit_events_v2 (result, seq DESC);

-- loginevent.go:296-305  登录事件列表 ORDER BY seq DESC LIMIT/OFFSET
CREATE INDEX IF NOT EXISTS idx_login_events_seq_desc
    ON public.login_events (seq DESC);

-- loginevent.go:261-263  按 organization_id 过滤 + ORDER BY seq DESC
CREATE INDEX IF NOT EXISTS idx_login_events_org_seq
    ON public.login_events (organization_id, seq DESC);

-- loginevent.go:269-271  按 event_type 过滤 + ORDER BY seq DESC
CREATE INDEX IF NOT EXISTS idx_login_events_type_seq
    ON public.login_events (event_type, seq DESC);

-- cases.go:371  SELECT count(*) FROM investigations WHERE case_id=$1 AND status NOT IN (...)
CREATE INDEX IF NOT EXISTS idx_investigations_case_status
    ON public.investigations (case_id, status);

-- session.go:576-578  后台会话列表按 s.organization_id 过滤（现有索引都以 identity_id 开头）
CREATE INDEX IF NOT EXISTS idx_sessions_org_last_seen
    ON public.sessions (organization_id, last_seen_at DESC);

-- ---------------------------------------------------------------------------
-- 2. 分区维护基础设施
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.partition_maintenance_log (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    table_name     text NOT NULL,
    action         text NOT NULL,
    partition_name text NOT NULL DEFAULT '',
    detail         jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at     timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE public.partition_maintenance_log IS
  'S7-DB-01：分区创建/归档候选计算的审计轨迹（不含删除动作；删除需走 S6 保留策略审批）。';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'blockchain_app') THEN
        GRANT SELECT ON public.partition_maintenance_log TO blockchain_app;
        REVOKE UPDATE, DELETE, TRUNCATE ON public.partition_maintenance_log FROM blockchain_app;
    END IF;
END
$$;

-- 幂等地为 user_activity_events 建立从 p_start（默认当月）起、含未来 N 个月的分区。
CREATE OR REPLACE FUNCTION public.s7_ensure_user_activity_partitions(
    p_months_ahead int DEFAULT 2,
    p_start date DEFAULT NULL
) RETURNS text[] LANGUAGE plpgsql AS $$
DECLARE
    created text[] := ARRAY[]::text[];
    base date;
    m date;
    part text;
    i int;
BEGIN
    base := COALESCE(p_start, date_trunc('month', (now() AT TIME ZONE 'UTC'))::date);
    base := date_trunc('month', base)::date;
    FOR i IN 0..GREATEST(p_months_ahead, 0) LOOP
        m := (base + make_interval(months => i))::date;
        part := 'user_activity_events_' || to_char(m, 'YYYYMM');
        IF NOT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
                        WHERE n.nspname='public' AND c.relname=part) THEN
            EXECUTE format(
                'CREATE TABLE public.%I PARTITION OF public.user_activity_events FOR VALUES FROM (%L) TO (%L)',
                part, m, (m + interval '1 month')::date);
            IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'blockchain_app') THEN
                EXECUTE format('GRANT SELECT, INSERT ON public.%I TO blockchain_app', part);
                EXECUTE format('REVOKE UPDATE, DELETE, TRUNCATE ON public.%I FROM blockchain_app', part);
            END IF;
            created := array_append(created, part);
            INSERT INTO public.partition_maintenance_log(table_name, action, partition_name, detail)
            VALUES ('user_activity_events', 'create_partition', part,
                    jsonb_build_object('range_start', m, 'range_end', (m + interval '1 month')::date));
        END IF;
    END LOOP;
    RETURN created;
END
$$;
COMMENT ON FUNCTION public.s7_ensure_user_activity_partitions(int, date) IS
  'S7-DB-01：幂等创建 user_activity_events 月度分区（含权限）。p_start 用于避开历史分区边界。';

-- 归档候选（只读）：整体早于保留期的分区，可进入「导出 → 双人审批 → DETACH」流程。
-- 本函数不执行任何 DETACH/DROP —— 实际删除在 S6 保留策略开关打开前保持关闭。
CREATE OR REPLACE FUNCTION public.s7_partition_archive_candidates(p_retention_days int DEFAULT 90)
RETURNS TABLE(partition_name text, bound_expr text, rows bigint, size_bytes bigint, max_occurred_at timestamptz)
LANGUAGE plpgsql STABLE AS $$
DECLARE
    threshold timestamptz;
    r record;
    n bigint;
    sz bigint;
    newest timestamptz;
BEGIN
    threshold := now() - make_interval(days => GREATEST(p_retention_days, 1));
    FOR r IN
        SELECT c.relname AS part, pg_get_expr(c.relpartbound, c.oid) AS bound
          FROM pg_inherits i
          JOIN pg_class c ON c.oid = i.inhrelid
          JOIN pg_class p ON p.oid = i.inhparent
          JOIN pg_namespace n ON n.oid = p.relnamespace
         WHERE n.nspname = 'public' AND p.relname = 'user_activity_events'
         ORDER BY c.relname
    LOOP
        CONTINUE WHEN r.bound LIKE '%DEFAULT%';
        EXECUTE format('SELECT count(*), pg_total_relation_size(%L), max(occurred_at) FROM public.%I', r.part, r.part)
           INTO n, sz, newest;
        IF n > 0 AND newest IS NOT NULL AND newest < threshold THEN
            partition_name := r.part;
            bound_expr := r.bound;
            rows := n;
            size_bytes := sz;
            max_occurred_at := newest;
            RETURN NEXT;
        END IF;
    END LOOP;
END
$$;
COMMENT ON FUNCTION public.s7_partition_archive_candidates(int) IS
  'S7-DB-01：只读返回已整体超出保留期、可进入归档审批流程的 user_activity_events 分区。不执行删除。';

-- 分区状态（只读）：供运行手册与验收报告直接引用。
CREATE OR REPLACE FUNCTION public.s7_partition_status()
RETURNS TABLE(parent_table text, partition_name text, bound_expr text, rows bigint, size_bytes bigint)
LANGUAGE plpgsql STABLE AS $$
DECLARE
    r record;
    n bigint;
    sz bigint;
BEGIN
    FOR r IN
        SELECT p.relname AS parent, c.relname AS part, pg_get_expr(c.relpartbound, c.oid) AS bound
          FROM pg_inherits i
          JOIN pg_class c ON c.oid = i.inhrelid
          JOIN pg_class p ON p.oid = i.inhparent
          JOIN pg_namespace n ON n.oid = p.relnamespace
         WHERE n.nspname='public' AND p.relkind='p'
         ORDER BY p.relname, c.relname
    LOOP
        EXECUTE format('SELECT count(*), pg_total_relation_size(%L) FROM public.%I', r.part, r.part) INTO n, sz;
        parent_table := r.parent;
        partition_name := r.part;
        bound_expr := r.bound;
        rows := n;
        size_bytes := sz;
        RETURN NEXT;
    END LOOP;
END
$$;
COMMENT ON FUNCTION public.s7_partition_status() IS 'S7-DB-01：所有分区表的分区清单与行数（只读）。';

-- login_events / audit_events_v2 分区就绪门禁（机器可读），见文件末尾说明。
CREATE OR REPLACE FUNCTION public.s7_partition_readiness()
RETURNS TABLE(table_name text, ready boolean, blockers text[])
LANGUAGE plpgsql STABLE AS $$
BEGIN
    table_name := 'login_events';
    SELECT NOT EXISTS (
        SELECT 1 FROM pg_constraint c
         WHERE c.conrelid = 'public.login_events'::regclass AND c.contype IN ('u','p')
           AND pg_get_constraintdef(c.oid) LIKE '%chain%seq%'
    ) INTO ready;
    blockers := ARRAY[
        'UNIQUE(chain,seq) 需要全局唯一，PostgreSQL 分区表不支持全局唯一索引',
        'organization_id 参与审计哈希链规范化，重写数据会破坏 AUDIT-004 链校验',
        'append-only 行触发器 audit_append_only_guard 属 Sprint 6 完整性边界（0054/0055），不得绕过'
    ];
    RETURN NEXT;

    table_name := 'audit_events_v2';
    SELECT NOT EXISTS (
        SELECT 1 FROM pg_constraint c
         WHERE c.conrelid = 'public.audit_events_v2'::regclass AND c.contype IN ('u','p')
           AND pg_get_constraintdef(c.oid) LIKE '%chain%seq%'
    ) INTO ready;
    blockers := ARRAY[
        'UNIQUE(chain,seq) 需要全局唯一，PostgreSQL 分区表不支持全局唯一索引',
        'organization_id 参与审计哈希链规范化，重写数据会破坏 AUDIT-004 链校验',
        '包含 ADR-018 不可恢复缺口的封存边界行，任何重写都会造成 boundary_conflict'
    ];
    RETURN NEXT;
END
$$;
COMMENT ON FUNCTION public.s7_partition_readiness() IS
  'S7-DB-01：分区就绪门禁。ready=false 表示在保持链完整性前提下禁止分区，blockers 为实测阻塞点。';
-- ---------------------------------------------------------------------------
-- 3. user_activity_events：按月分区（§22.4）
--    历史边界取「实际数据的最大月份」而非硬编码日期：表非空时按数据推导，
--    空表时取当月起点，保证既不与月分区重叠、也不丢任何一行。
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    is_partitioned boolean;
    hist_exists boolean;
    hist_max timestamptz;
    hist_upper timestamptz;
BEGIN
    SELECT c.relkind = 'p' INTO is_partitioned
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'public' AND c.relname = 'user_activity_events';

    IF is_partitioned IS NULL THEN
        RAISE EXCEPTION '0057：user_activity_events 不存在，拒绝分区改造';
    END IF;

    SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
                    WHERE n.nspname='public' AND c.relname='user_activity_events_hist') INTO hist_exists;

    IF is_partitioned OR hist_exists THEN
        RETURN; -- 已改造（重放安全）
    END IF;

    -- 3.1 旧表原样保留为历史分区（ACL 随表保留：INSERT/SELECT 放行、UPDATE/DELETE 已回收）。
    ALTER TABLE public.user_activity_events RENAME TO user_activity_events_hist;

    -- 3.2 去掉旧表主键与二级索引：ATTACH 时 PostgreSQL 会按父表索引在分区上重建等价索引，
    --     旧表自带 PK(id) 与父表 PK(id, occurred_at) 冲突（42P16），必须先移除。
    ALTER TABLE public.user_activity_events_hist DROP CONSTRAINT IF EXISTS user_activity_events_pkey;
    DROP INDEX IF EXISTS public.user_activity_identity_idx;
    DROP INDEX IF EXISTS public.user_activity_time_idx;

    -- 3.3 历史分区上界：按真实数据推导（不猜日期）。
    EXECUTE 'SELECT max(occurred_at) FROM public.user_activity_events_hist' INTO hist_max;
    IF hist_max IS NULL THEN
        hist_upper := date_trunc('month', (now() AT TIME ZONE 'UTC'));
    ELSE
        hist_upper := date_trunc('month', hist_max) + interval '1 month';
    END IF;

    -- 3.4 分区父表：结构同旧表，按 occurred_at 月分区。
    CREATE TABLE public.user_activity_events (
        LIKE public.user_activity_events_hist INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING STORAGE
    ) PARTITION BY RANGE (occurred_at);
    ALTER TABLE public.user_activity_events ADD PRIMARY KEY (id, occurred_at);

    EXECUTE format(
        'ALTER TABLE public.user_activity_events_hist ADD CONSTRAINT user_activity_events_hist_bounds CHECK (occurred_at < %L::timestamptz)',
        hist_upper);

    EXECUTE format(
        'ALTER TABLE public.user_activity_events ATTACH PARTITION public.user_activity_events_hist FOR VALUES FROM (MINVALUE) TO (%L::timestamptz)',
        hist_upper);

    -- 3.5 DEFAULT 分区兜底：运维漏建分区时写入不失败、不静默丢弃。
    CREATE TABLE public.user_activity_events_default PARTITION OF public.user_activity_events DEFAULT;

    -- 3.6 父表索引（自动下发到所有分区）。
    CREATE INDEX user_activity_identity_idx ON public.user_activity_events (identity_id, occurred_at DESC);
    CREATE INDEX user_activity_time_idx     ON public.user_activity_events (occurred_at DESC);
    CREATE INDEX user_activity_org_time_idx ON public.user_activity_events (organization_id, occurred_at DESC);

    -- 3.7 权限：新建对象默认无授权，必须显式补齐，并保持 0054 收紧语义
    --     （事件表只允许 INSERT/SELECT，禁止 UPDATE/DELETE/TRUNCATE）。
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'blockchain_app') THEN
        GRANT SELECT, INSERT ON public.user_activity_events TO blockchain_app;
        GRANT SELECT, INSERT ON public.user_activity_events_default TO blockchain_app;
        GRANT SELECT, INSERT ON public.user_activity_events_hist TO blockchain_app;
        REVOKE UPDATE, DELETE, TRUNCATE ON public.user_activity_events FROM blockchain_app;
        REVOKE UPDATE, DELETE, TRUNCATE ON public.user_activity_events_default FROM blockchain_app;
        REVOKE UPDATE, DELETE, TRUNCATE ON public.user_activity_events_hist FROM blockchain_app;
    END IF;
    REVOKE TRUNCATE ON public.user_activity_events, public.user_activity_events_default,
                       public.user_activity_events_hist FROM PUBLIC;

    -- 3.8 建立当月与未来两个月分区（从历史边界之后开始，避免与历史分区重叠）。
    PERFORM public.s7_ensure_user_activity_partitions(
        2, GREATEST(date_trunc('month', (now() AT TIME ZONE 'UTC'))::date, hist_upper::date));
END
$$;

-- 补建当月与未来两个月分区（重放安全；已存在则跳过）。
SELECT public.s7_ensure_user_activity_partitions(2);

-- ---------------------------------------------------------------------------
-- 4. 自检
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    problems text[] := ARRAY[]::text[];
    missing text[] := ARRAY[]::text[];
    want text[] := ARRAY[
        'idx_download_pages_job_status','idx_checkpoints_job','idx_exports_job_org_created',
        'idx_audit_v2_seq_desc','idx_audit_v2_result_seq','idx_login_events_seq_desc',
        'idx_login_events_org_seq','idx_login_events_type_seq','idx_investigations_case_status',
        'idx_sessions_org_last_seen'];
    app_exists boolean;
BEGIN
    SELECT array_agg(w) INTO missing FROM unnest(want) AS w
     WHERE NOT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
                        WHERE n.nspname='public' AND c.relname=w);
    IF missing IS NOT NULL THEN
        problems := array_append(problems, 'index_missing:' || array_to_string(missing, '|'));
    END IF;

    IF (SELECT relkind FROM pg_class WHERE oid='public.user_activity_events'::regclass) <> 'p' THEN
        problems := array_append(problems, 'user_activity_events:NOT_PARTITIONED');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid
                    WHERE i.inhparent='public.user_activity_events'::regclass
                      AND c.relname='user_activity_events_hist') THEN
        problems := array_append(problems, 'user_activity_events_hist:NOT_ATTACHED');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid
                    WHERE i.inhparent='public.user_activity_events'::regclass
                      AND c.relname='user_activity_events_default') THEN
        problems := array_append(problems, 'user_activity_events_default:MISSING');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid
                    WHERE i.inhparent='public.user_activity_events'::regclass
                      AND pg_get_expr(c.relpartbound, c.oid) NOT LIKE '%DEFAULT%') THEN
        problems := array_append(problems, 'user_activity_events:NO_MONTH_PARTITION');
    END IF;

    -- 分区重建后必须仍然禁止运行角色改写事件行（0054 语义不能因重建而丢失）。
    SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'blockchain_app') INTO app_exists;
    IF app_exists THEN
        IF has_table_privilege('blockchain_app','user_activity_events','UPDATE')
           OR has_table_privilege('blockchain_app','user_activity_events','DELETE')
           OR has_table_privilege('blockchain_app','user_activity_events','TRUNCATE') THEN
            problems := array_append(problems, 'user_activity_events:APPEND_ONLY_REGRESSED');
        END IF;
        IF NOT has_table_privilege('blockchain_app','user_activity_events','INSERT')
           OR NOT has_table_privilege('blockchain_app','user_activity_events','SELECT') THEN
            problems := array_append(problems, 'user_activity_events:WRITE_PRIVILEGE_MISSING');
        END IF;
        IF has_table_privilege('blockchain_app','user_activity_events_hist','UPDATE')
           OR has_table_privilege('blockchain_app','user_activity_events_hist','DELETE') THEN
            problems := array_append(problems, 'user_activity_events_hist:APPEND_ONLY_REGRESSED');
        END IF;
        IF has_table_privilege('blockchain_app','user_activity_events_default','UPDATE')
           OR has_table_privilege('blockchain_app','user_activity_events_default','DELETE') THEN
            problems := array_append(problems, 'user_activity_events_default:APPEND_ONLY_REGRESSED');
        END IF;
    END IF;

    IF array_length(problems, 1) IS NOT NULL THEN
        RAISE EXCEPTION '0057 索引/分区自检失败: %', array_to_string(problems, ', ');
    END IF;
END
$$;
