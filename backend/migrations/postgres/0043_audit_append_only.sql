-- 0043 审计表 append-only 强制（§6.6.5 "应用运行账号无 UPDATE/DELETE 权限"）
--
-- 背景：当前部署中应用数据库账号是这些表的 owner（PostgreSQL 中 owner 会绕过
-- REVOKE），因此单纯 REVOKE 无法真正阻止改写。这里改用 BEFORE UPDATE/DELETE
-- 触发器，无论账号权限如何都会拒绝，构成真正的纵深防御。
--
-- 受影响的表：
--   audit_events_v2  —— 业务与管理审计链
--   login_events     —— 登录安全链
--   audit_chain_heads —— 链头（同样不允许手工回退，只能由 ChainAppend 推进）
--
-- 维护逃生舱：保留受控入口给未来的保留期删除/归档流程（§6.6.5、S6 保留策略）。
--   BEGIN; SET LOCAL app.audit_maintenance = 'on'; DELETE ...; COMMIT;
-- 该开关只能由持有数据库凭据的运维流程设置，且必须走审批（admin:deletion:approve）。

CREATE OR REPLACE FUNCTION audit_append_only_guard() RETURNS trigger AS $$
BEGIN
    IF current_setting('app.audit_maintenance', true) = 'on' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'audit table % is append-only; % is not permitted (set app.audit_maintenance=on for an approved retention job)', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_events_v2_append_only ON audit_events_v2;
CREATE TRIGGER audit_events_v2_append_only
    BEFORE UPDATE OR DELETE ON audit_events_v2
    FOR EACH ROW EXECUTE FUNCTION audit_append_only_guard();

DROP TRIGGER IF EXISTS login_events_append_only ON login_events;
CREATE TRIGGER login_events_append_only
    BEFORE UPDATE OR DELETE ON login_events
    FOR EACH ROW EXECUTE FUNCTION audit_append_only_guard();

DROP TRIGGER IF EXISTS audit_chain_heads_no_rewind ON audit_chain_heads;
CREATE TRIGGER audit_chain_heads_no_rewind
    BEFORE DELETE ON audit_chain_heads
    FOR EACH ROW EXECUTE FUNCTION audit_append_only_guard();

-- 链头只允许单调推进：禁止 last_seq 回退或更换历史哈希（除非处于维护模式）。
CREATE OR REPLACE FUNCTION audit_chain_heads_monotonic() RETURNS trigger AS $$
BEGIN
    IF current_setting('app.audit_maintenance', true) = 'on' THEN
        RETURN NEW;
    END IF;
    IF NEW.last_seq < OLD.last_seq THEN
        RAISE EXCEPTION 'audit chain % last_seq must not decrease (% -> %)', OLD.chain, OLD.last_seq, NEW.last_seq
            USING ERRCODE = 'restrict_violation';
    END IF;
    IF NEW.last_seq = OLD.last_seq AND NEW.last_hash <> OLD.last_hash THEN
        RAISE EXCEPTION 'audit chain % head hash must not be replaced at the same seq', OLD.chain
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_chain_heads_monotonic_trg ON audit_chain_heads;
CREATE TRIGGER audit_chain_heads_monotonic_trg
    BEFORE UPDATE ON audit_chain_heads
    FOR EACH ROW EXECUTE FUNCTION audit_chain_heads_monotonic();
