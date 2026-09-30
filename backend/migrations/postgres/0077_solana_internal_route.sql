-- Solana native SOL movements and CPI calls share internal_transactions.
-- 0075 seeded the other Solana datasets but omitted this route, causing the
-- download worker to fall through to Ankr and fail with "unknown dataset".
INSERT INTO routing_policies (
    policy_key, description, chain, dataset, enabled, primary_provider,
    fallback_providers, max_concurrency, rate_limit_per_minute,
    circuit_error_rate, circuit_p95_ms, circuit_cooldown_seconds,
    rollout_percent, revision, config
)
VALUES (
    'solana:internal_transactions', 'Solana 原生 SOL 转账与 CPI 调用路由',
    'solana', 'internal_transactions', true, 'drpc',
    ARRAY['solana-rpc'], 48, 0,
    0.25, 8000, 60, 100, 1,
    '{"api_channel":"solana_rpc","operation_class":"solana_history"}'::jsonb
)
ON CONFLICT (policy_key) DO NOTHING;
