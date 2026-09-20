-- 0072_solana_chain.sql
-- 目的：把 Solana 登记为**可下载链**（链能力矩阵 + 治理视图 + 显式路由策略行）。
--
-- 背景：平台此前只有两条取数路径——EVM（Ankr Advanced API）与 TRON（TronGrid/Ankr）。
--   Solana 完全不存在：注册表无条目、chain_capabilities 无行、Provider 无实现，
--   于是 /api/v2/meta/chains 不会返回它，前端自然也看不到。
--   2026-09-20 新增 internal/provider/solana（标准 JSON-RPC：
--   getSignaturesForAddress + getTransaction）与 chain.Registry 条目后，
--   链级静态元数据必须同步落库，否则 /api/v1/chains/capabilities 与 Count Oracle
--   部署矩阵仍然认为这条链不存在。
--
-- 语义边界（诚实标注）：ankr_transactions / ankr_token_transfers 描述的是
--   **Ankr Advanced API 的取数能力**，不是"这条链能不能下载"。Ankr 的 Advanced API
--   明确拒绝 solana（"invalid blockchain name: solana is not allowed"，2026-09-20 实测），
--   因此这两列必须保持 false；Solana 的取数能力由 provider_descriptors 的 solana-rpc 行表达。
--   当前只实现 transactions，链级 Dataset 白名单见
--   internal/api/sprint5_meta.go 的 chainDatasetsByChain。
--
-- 幂等：ON CONFLICT DO UPDATE / DO NOTHING，重复执行不改变结果。

INSERT INTO chain_capabilities (chain, evm_chain_id, ankr_alias, chainbase_tx_count, chainbase_token_count,
                                ankr_transactions, ankr_token_transfers, range_supported, enabled,
                                chain_type, native_symbol)
VALUES ('solana', 0, 'solana', false, false, false, false, true, true, 'solana', 'SOL')
ON CONFLICT (chain) DO UPDATE SET
    ankr_alias = EXCLUDED.ankr_alias,
    range_supported = EXCLUDED.range_supported,
    enabled = EXCLUDED.enabled,
    chain_type = EXCLUDED.chain_type,
    native_symbol = EXCLUDED.native_symbol,
    updated_at = now();

INSERT INTO provider_descriptors (provider_key, display_name, kind, chains, datasets, capabilities,
                                  endpoint, status, config, concurrency, rate_limit_per_minute, priority)
VALUES ('solana-rpc', 'Solana JSON-RPC', 'rpc', ARRAY['solana'], ARRAY['transactions'],
        '[{"key":"fetch","state":"declared"},{"key":"head","state":"declared"}]'::jsonb,
        'https://rpc.ankr.com/solana', 'connected', '{"mode":"config"}'::jsonb, 4, 60, 40)
ON CONFLICT (provider_key) DO NOTHING;

-- 显式策略行：Solana 的 transactions 只由 solana-rpc 承载，绝不回落到 Ankr Advanced API
--   （后者会直接拒绝 solana）。下载引擎当前按链选择 Provider，这一行是治理视图的事实来源。
INSERT INTO routing_policies (policy_key, description, chain, dataset, enabled, primary_provider,
                              fallback_providers, max_concurrency, rate_limit_per_minute,
                              circuit_error_rate, circuit_p95_ms, circuit_cooldown_seconds,
                              rollout_percent, revision)
VALUES ('solana:transactions', 'Solana 交易拉取路由（标准 JSON-RPC）', 'solana', 'transactions',
        true, 'solana-rpc', ARRAY[]::text[], 4, 60, 0.25, 8000, 60, 100, 1)
ON CONFLICT (policy_key) DO NOTHING;

-- 自检：Solana 必须真的可下载（能力行存在且 enabled），否则整个迁移回滚。
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM chain_capabilities
         WHERE chain = 'solana' AND enabled AND chain_type = 'solana'
    ) THEN
        RAISE EXCEPTION '0072 self-check failed: chain_capabilities 缺少可用的 solana 行';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM provider_descriptors WHERE provider_key = 'solana-rpc') THEN
        RAISE EXCEPTION '0072 self-check failed: provider_descriptors 缺少 solana-rpc';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM routing_policies WHERE policy_key = 'solana:transactions' AND enabled) THEN
        RAISE EXCEPTION '0072 self-check failed: routing_policies 缺少启用的 solana:transactions';
    END IF;
END $$;
