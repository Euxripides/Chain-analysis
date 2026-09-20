-- 0074_coingecko_platform_provenance.sql
-- 目的：给 CoinGecko 映射标注**来源口径**。
--
-- 背景（2026-09-20）：官方 /coins/list?include_platform=true 在本机被上游封堵
--   （DNS 投毒 + IP 层拦截：本机解析 107.181.166.244、AliDNS 52.58.1.161、
--   DNSPod 69.50.221.20，443 全部超时；8.8.8.8/1.1.1.1 的 53 也不通）。
--   为了让映射先可用，先用 GitHub 上可达的基础列表（id/symbol/name，3,981 条）
--   与 CoinGecko 聚合 tokenlist（链 + 合约 + 名称 + 符号）做**名称+符号完全匹配**推导。
--
--   推导结果与官方结果混在一张表里，必须能区分，否则后续无法判断"这条映射能不能信"。
--   官方接口可达后跑 cmd/tokenindex -coingecko，会把同一主键的行标回 official。
--
-- 幂等：ADD COLUMN IF NOT EXISTS + 默认值回填。

ALTER TABLE coingecko_platforms
    ADD COLUMN IF NOT EXISTS match_method VARCHAR(32) NOT NULL DEFAULT 'official';
ALTER TABLE coingecko_platforms
    ADD COLUMN IF NOT EXISTS matched_at TIMESTAMPTZ;

UPDATE coingecko_platforms SET match_method = 'derived-name-symbol'
WHERE match_method = 'official' AND matched_at IS NULL AND contract <> '';

CREATE INDEX IF NOT EXISTS idx_coingecko_platforms_method ON coingecko_platforms(match_method);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'coingecko_platforms' AND column_name = 'match_method'
    ) THEN
        RAISE EXCEPTION '0074 coingecko_platforms.match_method 缺失';
    END IF;
END $$;
