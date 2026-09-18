-- 0058_export_object_migrations.sql
-- F-R10-BUCKET / ADR-019 §2.3：导出产物桶迁移账本（**additive**：只新增表与索引）。
--
-- 背景（实测 inventory f-r10-bucket-inventory-20260913T121059Z）：
--   PG exports 277 行（COMPLETED 270 / FAILED 7），object_ref 非空且唯一 270 条；
--   这 270 个对象 100% 落在 RAW_BUCKET（blockchain-raw）的 exports/ 前缀，共 2,749,993,808 bytes；
--   EXPORT_BUCKET（blockchain-exports）只有 28 个既有 audit-exports/* 治理审计对象，业务 ref 命中 0。
--   270 条中 PG sha256 有值 204 条、为空 66 条。
--
-- 本表只记录"某个导出对象被复制到哪个桶、源/目标内容 SHA-256 是否逐字一致"，用途：
--   1) 幂等：主键 (export_id, to_bucket) 保证重复运行不产生重复行；
--   2) 可续跑：status 让中断后的重跑只处理未 VERIFIED 的对象；
--   3) 可审计：source_sha256 / destination_sha256 / pg_sha256 三个值同时落库，可离线复核。
--
-- 明确不做的事（ADR-019 §2.3）：
--   * 不删除 RAW 对象；* 不移动对象；* 不更新 exports.object_ref；
--   * 不回填/改写 exports 表的历史行（66 条缺 sha 的行保持原样）；
--   * 不触碰 blockchain-exports 里既有的 28 个 audit-exports/* 审计对象。

CREATE TABLE IF NOT EXISTS public.export_object_migrations (
    export_id          UUID        NOT NULL REFERENCES public.exports(id),
    object_ref         TEXT        NOT NULL,
    from_bucket        TEXT        NOT NULL,
    to_bucket          TEXT        NOT NULL,
    bytes              BIGINT      NOT NULL DEFAULT 0,
    -- 源对象 GET 后复算的内容 SHA-256（**这是唯一可信的字节证明**，不是 ETag/size）
    source_sha256      TEXT        NOT NULL DEFAULT '',
    -- 目标对象 GET 后复算的内容 SHA-256
    destination_sha256 TEXT        NOT NULL DEFAULT '',
    -- exports.sha256 的历史值（204 条有值 / 66 条为空）；空值表示历史行没有记录，不是校验通过
    pg_sha256          TEXT        NOT NULL DEFAULT '',
    status             TEXT        NOT NULL DEFAULT 'PENDING',
    attempts           INTEGER     NOT NULL DEFAULT 0,
    last_error         TEXT        NOT NULL DEFAULT '',
    verified_at        TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 幂等：同一导出对象迁往同一目标桶只有一行
    CONSTRAINT export_object_migrations_pk PRIMARY KEY (export_id, to_bucket),
    -- 状态机：PENDING（待处理/干跑计划）→ COPIED（已复制待验证）→ VERIFIED；
    -- ALREADY_VERIFIED 表示目标已存在且内容逐字相同（不覆盖）；CONFLICT/FAILED 必须 fail closed。
    CONSTRAINT export_object_migrations_status_ck CHECK (
        status IN ('PENDING', 'COPIED', 'VERIFIED', 'ALREADY_VERIFIED', 'CONFLICT', 'FAILED')
    ),
    -- 原地"迁移"无意义，必须跨桶
    CONSTRAINT export_object_migrations_bucket_ck CHECK (from_bucket <> to_bucket),
    -- VERIFIED/ALREADY_VERIFIED 必须有非空的目标 SHA 与可追溯的源 SHA
    CONSTRAINT export_object_migrations_verified_ck CHECK (
        status NOT IN ('VERIFIED', 'ALREADY_VERIFIED')
        OR (source_sha256 <> '' AND destination_sha256 <> '' AND source_sha256 = destination_sha256)
    )
);

CREATE INDEX IF NOT EXISTS idx_export_object_migrations_status
    ON public.export_object_migrations (to_bucket, status);

CREATE INDEX IF NOT EXISTS idx_export_object_migrations_updated
    ON public.export_object_migrations (updated_at DESC);

-- 权限：0054 的 ALTER DEFAULT PRIVILEGES 已给 blockchain_app 授予 SELECT/INSERT/UPDATE/DELETE，
-- 这里显式重申，保证账本可写可读（本表是运行期账本，不是 append-only 审计表）。
GRANT SELECT, INSERT, UPDATE, DELETE ON public.export_object_migrations TO blockchain_app;
REVOKE TRUNCATE ON public.export_object_migrations FROM blockchain_app;
REVOKE TRUNCATE ON public.export_object_migrations FROM PUBLIC;
