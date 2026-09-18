-- 0063_audit_integrity_declarations.sql
-- 审计完整性：把「稀疏空洞」与「内容哈希不可复算行」纳入 0055 的不可变声明模型。
--
-- 背景（两起真实缺陷，均已定位根因并修复写入侧）：
--
--   1) 登录链存在 8 处**稀疏空洞**（seq 3791 / 3799 / 5911 / 5914 / 5915 / 5918 / 5919 / 5920）。
--      根因：链头推进（audit_chain_heads）与写行不在同一事务，插入被约束拒绝时链头已前进。
--      2026-09-16 已在写入侧关闭（internal/iam/loginevent.go 的 result 白名单 + 同事务写入 +
--      失败必留痕）。但空洞区间内夹着真实数据行，0055 的「连续缺口」边界无法表达。
--
--   2) 审计链存在 2 处**内容哈希不可复算行**（seq 52394 / 52399）。
--      根因：写入侧把不可落库的 UUID 文本（workspace_id='not-a-uuid'）放进了哈希载荷，
--      而 uuid 列落库为 NULL、校验时读回 COALESCE(col::text,'')=''，两端不再一致。
--      2026-09-16 已在写入侧关闭（audit.UUIDPayloadValue）。但既有两行的 event_hash
--      永远无法由其行内容复算——这是不可逆的事实，不是可以"重算修好"的状态。
--
-- 设计立场（与 ADR-018 一致，见 ADR-019）：
--   * 不改写、不回填、不删除任何审计行（append-only 与可检测性是审计的根）；
--   * 也不允许静默忽略：把事实**逐条声明**为不可变记录，附链上的哈希证据；
--   * 声明走 0055 的封存审批链（step-up + 双人审批 + 自身哈希链）；
--   * 校验器据此跳过这些**已声明**的 seq 的内容复算，但继续校验 prev_hash 链接，
--     并把声明明细原样回报——「已声明」绝不等于「完整」。
--
-- 本迁移不写入任何 audit_events_v2 / login_events 行，不改写 audit_chain_heads，
-- 不修改任何既有锚点或既有完整性事件。

-- ---------------------------------------------------------------------------
-- 1. 允许第二个 kind：declared_integrity_events（稀疏声明）
-- ---------------------------------------------------------------------------
ALTER TABLE audit_integrity_incidents
    DROP CONSTRAINT IF EXISTS audit_integrity_incidents_kind_check;
ALTER TABLE audit_integrity_incidents
    ADD CONSTRAINT audit_integrity_incidents_kind_check
    CHECK (kind IN ('unrecoverable_gap', 'declared_integrity_events'));

-- 连续缺口的 trusted_from_seq 必须紧接缺口末端；稀疏声明同样是
-- max(seq)+1（因为 gap_to_seq 存的就是 max(seq)），因此原约束仍然成立，无需放宽。

-- ---------------------------------------------------------------------------
-- 2. 逐条声明表：每个受影响的 seq 一行，附链上哈希证据
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_integrity_declared_events (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id         uuid NOT NULL REFERENCES audit_integrity_incidents(id),
    chain               text NOT NULL,
    seq                 bigint NOT NULL,
    disposition         text NOT NULL CHECK (disposition IN ('lost', 'content_anomaly')),
    -- 链上唯一留存的哈希证据：
    --   lost            -> 后继行的 prev_hash（= 被丢事件的 event_hash）
    --   content_anomaly -> 该行实际存储的 event_hash
    observed_event_hash text NOT NULL,
    -- 证据来源（'successor_prev_hash' / 'stored_event_hash'）与可复算说明
    evidence            jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_integrity_declared_events_hash_len CHECK (length(observed_event_hash) = 64),
    UNIQUE (incident_id, chain, seq)
);
CREATE INDEX IF NOT EXISTS audit_integrity_declared_events_chain_idx
    ON audit_integrity_declared_events (chain, seq);

DROP TRIGGER IF EXISTS audit_integrity_declared_events_immutable ON audit_integrity_declared_events;
CREATE TRIGGER audit_integrity_declared_events_immutable
    BEFORE UPDATE OR DELETE ON audit_integrity_declared_events
    FOR EACH ROW EXECUTE FUNCTION audit_integrity_immutable_guard();

-- ---------------------------------------------------------------------------
-- 3. 运行角色权限：只读 + 只写，绝不 UPDATE/DELETE/TRUNCATE，且不得成为 owner
-- ---------------------------------------------------------------------------
GRANT SELECT, INSERT ON audit_integrity_declared_events TO blockchain_app;
REVOKE UPDATE, DELETE, TRUNCATE ON audit_integrity_declared_events FROM blockchain_app;

-- ---------------------------------------------------------------------------
-- 4. 自检：权限矩阵不符即迁移失败（不允许静默降级）
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    problems text[] := ARRAY[]::text[];
    t text := 'audit_integrity_declared_events';
BEGIN
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
    IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_roles r ON r.oid = c.relowner
               WHERE c.relname = t AND r.rolname = 'blockchain_app') THEN
        problems := array_append(problems, t || ':OWNED_BY_RUNTIME_ROLE');
    END IF;

    IF array_length(problems, 1) IS NOT NULL THEN
        RAISE EXCEPTION 'audit_integrity_declared_events permission self-check failed: %',
            array_to_string(problems, ', ');
    END IF;
END
$$;
