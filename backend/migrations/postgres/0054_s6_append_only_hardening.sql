-- 0054_s6_append_only_hardening.sql
-- Sprint 6 / AUDIT-003 发现并修复（真实缺陷，不是理论风险）：
--   0043 用行级触发器阻止了审计表的 UPDATE / DELETE，但
--   (a) PostgreSQL 不支持对 TRUNCATE 使用行级触发器，事件触发器也不覆盖 TRUNCATE；
--   (b) 应用连接账号同时是这些表的 owner（本部署中还是超级用户），
--       因此 REVOKE 对 owner 无效（pg_class_aclcheck 对 owner 恒定放行）。
--   结果：应用角色仍可 TRUNCATE 整张审计表。S6 验收在执行 AUDIT-003c 时真实触发，
--   详见 docs/implementation/sprint6-report.md 的事件记录。
--
-- 修复策略（§27.6 AUDIT-003 要求的「应用 DB 角色拒绝」）：
--   引入受限运行角色 blockchain_app，应用连接建立后 SET ROLE 到该角色；
--   该角色只持有 SELECT/INSERT/UPDATE/DELETE，没有 TRUNCATE / DROP / TRIGGER / REFERENCES。
--   行级触发器保留为纵深防御（覆盖 owner 直连路径）。
--
-- 部署侧：backend/internal/store 在 DB_RUNTIME_ROLE 非空时对每个连接执行 SET ROLE；
-- 迁移与运维工具（cmd/migrate、cmd/admin-init、cmd/admin-fixture）不使用该角色。

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'blockchain_app') THEN
        CREATE ROLE blockchain_app NOLOGIN;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO blockchain_app;

-- 运行角色：只读放行 + 写入放行，显式排除 TRUNCATE/DROP/TRIGGER/REFERENCES。
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO blockchain_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO blockchain_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO blockchain_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO blockchain_app;

-- 审计与治理关键表收紧。
--   - 事件表（audit_events_v2 / login_events / user_activity_events / approval_request_events）：
--     写入路径只允许 INSERT + SELECT，UPDATE / DELETE 一律拒绝。
--   - audit_chain_heads 必须保留 UPDATE：哈希链推进需要更新链头（last_seq/last_hash），
--     该列的改写由链校验与每日锚点共同检测，不能靠权限锁死，否则审计无法写入。
--   - audit_chain_anchors 保留 INSERT/UPDATE：归档器按 (chain, anchor_date) 幂等 upsert。
--   - 所有表均回收 TRUNCATE。
REVOKE UPDATE, DELETE, TRUNCATE ON audit_events_v2 FROM blockchain_app;
REVOKE UPDATE, DELETE, TRUNCATE ON login_events FROM blockchain_app;
REVOKE DELETE, TRUNCATE ON audit_chain_heads FROM blockchain_app;
REVOKE DELETE, TRUNCATE ON audit_chain_anchors FROM blockchain_app;
REVOKE UPDATE, DELETE, TRUNCATE ON approval_request_events FROM blockchain_app;
REVOKE UPDATE, DELETE, TRUNCATE ON user_activity_events FROM blockchain_app;

REVOKE TRUNCATE ON ALL TABLES IN SCHEMA public FROM PUBLIC;

-- 自检：运行角色的权限必须与上面声明一致，否则迁移失败（不允许静默降级）。
DO $$
DECLARE
    problems text[] := ARRAY[]::text[];
BEGIN
    IF has_table_privilege('blockchain_app', 'audit_events_v2', 'TRUNCATE') THEN
        problems := array_append(problems, 'audit_events_v2:TRUNCATE_GRANTED');
    END IF;
    IF has_table_privilege('blockchain_app', 'audit_events_v2', 'UPDATE') THEN
        problems := array_append(problems, 'audit_events_v2:UPDATE_GRANTED');
    END IF;
    IF has_table_privilege('blockchain_app', 'audit_events_v2', 'DELETE') THEN
        problems := array_append(problems, 'audit_events_v2:DELETE_GRANTED');
    END IF;
    IF NOT has_table_privilege('blockchain_app', 'audit_events_v2', 'INSERT') THEN
        problems := array_append(problems, 'audit_events_v2:INSERT_MISSING');
    END IF;
    IF NOT has_table_privilege('blockchain_app', 'audit_events_v2', 'SELECT') THEN
        problems := array_append(problems, 'audit_events_v2:SELECT_MISSING');
    END IF;
    IF has_table_privilege('blockchain_app', 'download_jobs', 'TRUNCATE') THEN
        problems := array_append(problems, 'download_jobs:TRUNCATE_GRANTED');
    END IF;
    -- 链头推进必须可用，否则审计写入会整体失败。
    IF NOT has_table_privilege('blockchain_app', 'audit_chain_heads', 'UPDATE') THEN
        problems := array_append(problems, 'audit_chain_heads:UPDATE_MISSING');
    END IF;
    IF NOT has_table_privilege('blockchain_app', 'audit_chain_heads', 'SELECT') THEN
        problems := array_append(problems, 'audit_chain_heads:SELECT_MISSING');
    END IF;
    IF NOT has_table_privilege('blockchain_app', 'download_jobs', 'INSERT') THEN
        problems := array_append(problems, 'download_jobs:INSERT_MISSING');
    END IF;
    IF array_length(problems, 1) IS NOT NULL THEN
        RAISE EXCEPTION '0054 运行角色权限自检失败: %', array_to_string(problems, ', ');
    END IF;
END
$$;