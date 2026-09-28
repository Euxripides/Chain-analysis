-- 0076: 导出预生成（prefetch）
--
-- 背景：用户从点击导出到拿到产物要等 10-20 秒（173 万行 csv+zip，实测 17 s）。
-- 但用户在填导出表单上通常要花 30-60 秒，这段时间足够把产物先生成好——点下载时
-- 直接命中已存在的对象，等待接近零。
--
-- 设计取舍：
--   * **独立于 exports 表**：预生成是"可能用不上"的投机产物，不应该出现在用户的
--     导出列表里、也不该计入导出审计。因此单独一张表，用户点导出时如果命中才把它
--     提升成一条真正的 exports 行。
--   * **按规格快照去重**：spec_hash 是 (chain, addresses, datasets, format, zip,
--     include_meta, include_decoded_input) 的稳定哈希。同一份草稿反复触发预生成
--     （表单每变一次都会触发）只会命中同一行，不会堆产物。
--   * **TENANT 作用域**：预生成产物同样只在生成它的组织/工作区内可见，避免跨租户
--     命中别人的文件。
--   * **过期清理**：expires_at 之后的行由维护任务删除（对象一并删）。预生成是投机
--     产物，过期即弃，不需要保留审计价值。
CREATE TABLE IF NOT EXISTS export_prefetch (
    id UUID PRIMARY KEY,
    spec_hash TEXT NOT NULL,
    organization_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    created_by UUID,
    -- 与 exports 同语义，便于命中后直接提升。
    dataset TEXT NOT NULL DEFAULT '',
    format TEXT NOT NULL,
    zip BOOLEAN NOT NULL DEFAULT FALSE,
    status TEXT NOT NULL DEFAULT 'PENDING',
    object_ref TEXT NOT NULL DEFAULT '',
    object_bucket TEXT NOT NULL DEFAULT '',
    object_sha256 TEXT NOT NULL DEFAULT '',
    rows BIGINT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '1 hour'
);

-- 命中查询的唯一形状：同租户 + 同规格 + 未过期，取最新一条。
CREATE INDEX IF NOT EXISTS idx_export_prefetch_hit
    ON export_prefetch (organization_id, workspace_id, spec_hash, created_at DESC);

-- 清理查询的形状：按过期时间扫。
CREATE INDEX IF NOT EXISTS idx_export_prefetch_expiry
    ON export_prefetch (expires_at);
