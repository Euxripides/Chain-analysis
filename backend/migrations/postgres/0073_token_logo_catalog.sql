-- 0073_token_logo_catalog.sql
-- 目的：把两类**外部代币目录**落库，作为图标与跨源映射的唯一事实来源。
--
-- 背景（2026-09-20）：地址详情的代币列已经要显示"图标 + 符号 + 全称"，但图标只在
--   token_metadata.logo_object_key 有对象时才出图；没有对象的代币只能退回首字母。
--   图标对象由 internal/tokenmeta/logo_worker.go 从 logo_url 自托管抓取，
--   所以缺的其实是"logo_url 从哪来"。
--
-- 两张目录表（都是外部事实，与链上事实分开存）：
--   1) token_logo_assets —— Trust Wallet assets 全量下载的索引：
--      每个 <chain>/assets/<address>/ 一行，保留源站目录名（EVM 即 EIP-55 校验和地址，
--      拼 raw.githubusercontent.com URL 必须原样使用）与 logo.png 的 sha256/字节数。
--   2) coingecko_coins / coingecko_platforms —— CoinGecko /coins/list?include_platform=true
--      的导入结果：CoinGecko ID ↔ 平台键 ↔ 合约地址，platform_key 映射到平台链 slug。
--
-- 幂等：全部 ON CONFLICT DO UPDATE，重复导入只更新字段与时间戳。

CREATE TABLE IF NOT EXISTS token_logo_assets (
    source             VARCHAR(32)  NOT NULL,
    chain              VARCHAR(32)  NOT NULL,
    normalized_address VARCHAR(128) NOT NULL,
    source_chain       VARCHAR(64)  NOT NULL,
    source_address     VARCHAR(128) NOT NULL,
    symbol             VARCHAR(64),
    name               TEXT,
    token_type         VARCHAR(32),
    decimals           INTEGER,
    status             VARCHAR(32),
    logo_url           TEXT,
    logo_sha256        CHAR(64),
    logo_bytes         BIGINT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source, chain, normalized_address)
);
CREATE INDEX IF NOT EXISTS idx_token_logo_assets_chain ON token_logo_assets(chain, normalized_address);
CREATE INDEX IF NOT EXISTS idx_token_logo_assets_symbol ON token_logo_assets(chain, lower(symbol));

CREATE TABLE IF NOT EXISTS coingecko_coins (
    id         TEXT PRIMARY KEY,
    symbol     VARCHAR(64),
    name       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS coingecko_platforms (
    coin_id            TEXT NOT NULL REFERENCES coingecko_coins(id) ON DELETE CASCADE,
    platform_key       VARCHAR(64) NOT NULL,
    contract           VARCHAR(160) NOT NULL,
    chain              VARCHAR(32),
    normalized_address VARCHAR(128),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (coin_id, platform_key, contract)
);
CREATE INDEX IF NOT EXISTS idx_coingecko_platforms_chain ON coingecko_platforms(chain, normalized_address);
CREATE INDEX IF NOT EXISTS idx_coingecko_platforms_symbol ON coingecko_platforms(coin_id);

-- 自检：三张表必须存在且主键/唯一列齐备，否则导入工具会静默失败。
DO $$
DECLARE missing TEXT;
BEGIN
    SELECT string_agg(t, ', ') INTO missing
    FROM (VALUES ('token_logo_assets'), ('coingecko_coins'), ('coingecko_platforms')) AS v(t)
    WHERE NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = v.t);
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION '0073 缺少表: %', missing;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'token_logo_assets' AND column_name = 'logo_sha256'
    ) THEN
        RAISE EXCEPTION '0073 token_logo_assets.logo_sha256 缺失';
    END IF;
END $$;
