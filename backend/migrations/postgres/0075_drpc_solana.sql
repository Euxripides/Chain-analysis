-- 0075_drpc_solana.sql
-- 目的：Solana dRPC 双通道（Data API + Solana JSON-RPC）接入的 PostgreSQL 契约（规范 §2.1）。
--
-- 背景：Solana 此前只有 internal/provider/solana 一条标准 JSON-RPC 通道，性能档位只有一套
--   Ankr 全局状态。dRPC 同时提供 Data API（地址历史页）与 Solana JSON-RPC（明细/余额），
--   两条通道的并发、速率、请求权重和 CU 口径都不同，因此需要「Provider x 通道 x 操作类 x 模式」
--   的档位表、把路由决策固化到 Shard 上，并把一个 Provider 的三类凭据分开管理。
--
-- 原则：只新增表/列与种子行，不删除既有列，旧二进制仍可读取旧列（expand-compatible）。
-- 幂等：ADD COLUMN IF NOT EXISTS / CREATE TABLE IF NOT EXISTS / ON CONFLICT。
--   执行器每个文件一个事务并持有 pg_advisory_lock，所以这里禁止 CONCURRENTLY。

-- ---------------------------------------------------------------------------
-- 1. provider_secrets：区分三类凭据（文档 §11.1）
--    runtime_api_key（Data API + JSON-RPC）、statistics_api_token（账户余额/聚合用量）、
--    keys_api_token（Key 状态与当日 CU）。此前一张表只能表达「Provider 级唯一 active 密钥」，
--    轮换 runtime key 会连带停用两个管理 Token，所以必须按 kind 分组。
-- ---------------------------------------------------------------------------
ALTER TABLE provider_secrets ADD COLUMN IF NOT EXISTS secret_kind text NOT NULL DEFAULT 'runtime_api_key';

-- 既有行由 DEFAULT 回填为 runtime_api_key（文档 §19 第 2 步）；约束单独加，避免重复执行报错。
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'provider_secrets_secret_kind_chk') THEN
        ALTER TABLE provider_secrets ADD CONSTRAINT provider_secrets_secret_kind_chk
            CHECK (secret_kind IN ('runtime_api_key', 'statistics_api_token', 'keys_api_token'));
    END IF;
END
$$;

-- 旧表只在代码层约定「一个 Provider 一个 active 密钥」，没有唯一性约束，库里可能残留同组多条 active；
-- 先按 rotated_at 只保留最新一条（其余转 retiring），否则下面的 partial unique index 建不起来。
-- 这也正是轮换语义：同 Provider 同 kind 的旧密钥一律 retiring（文档 §11.1）。
UPDATE provider_secrets s SET status = 'retiring'
 WHERE s.status = 'active'
   AND EXISTS (
       SELECT 1 FROM provider_secrets n
        WHERE n.provider_key = s.provider_key
          AND n.secret_kind = s.secret_kind
          AND n.status = 'active'
          AND (n.rotated_at, n.id) > (s.rotated_at, s.id)
   );

-- 同一 Provider 同一 kind 只允许一个 active；retiring/revoked 保留历史但不参与唯一性。
CREATE UNIQUE INDEX IF NOT EXISTS provider_secrets_active_kind_uniq
    ON provider_secrets (provider_key, secret_kind) WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- 2. routing_policies：显式表达通道与操作类
--    此前只能靠 policy_key 命名约定推断 api_channel/operation_class，而运行时路由解析必须按值取用。
-- ---------------------------------------------------------------------------
ALTER TABLE routing_policies ADD COLUMN IF NOT EXISTS config jsonb NOT NULL DEFAULT '{}'::jsonb;

-- ---------------------------------------------------------------------------
-- 3. download_shards：固化创建时的路由与档位（文档 §9.1）
--    后台改路由/改档位只影响新 Shard；恢复旧 Shard 必须沿用固化值，否则页 Token 与字段语义
--    可能在换源后漂移，Checkpoint 无法安全续跑。既有 Shard 取默认值，由旧路由解析兜底。
-- ---------------------------------------------------------------------------
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS provider_key text NOT NULL DEFAULT '';
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS api_channel text NOT NULL DEFAULT '';
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS operation_class text NOT NULL DEFAULT '';
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS routing_policy_key text NOT NULL DEFAULT '';
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS routing_policy_revision bigint NOT NULL DEFAULT 0;
ALTER TABLE download_shards ADD COLUMN IF NOT EXISTS profile_revision bigint NOT NULL DEFAULT 0;

-- ---------------------------------------------------------------------------
-- 4. provider_usage：按通道核对用量（文档 §15.1）
--    真实列名是 provider（不是 provider_key）、时间列是 request_time（0001 原始定义）。
--    Ankr/TronGrid 等单通道 Provider 保持空串，只有 dRPC 需要区分 data_api 与 solana_rpc。
-- ---------------------------------------------------------------------------
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS api_channel text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS provider_usage_channel_idx
    ON provider_usage (provider, api_channel, request_time DESC);

-- ---------------------------------------------------------------------------
-- 5. provider_performance_profiles：按「Provider x 链 x 数据集 x 操作类 x 模式」保存并发/速率/CU（文档 §10.1）
--    dataset='*' 表示该 Provider 的多个数据集共享同一档位；操作类决定实际通道。
--    旧表 ankr_production_profiles / ankr_performance_control 保留：解析器优先读本表，
--    Ankr 没有新档位时回退旧表，待一轮发布与回滚演练后再决定停用。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_performance_profiles (
    provider_key            text NOT NULL REFERENCES provider_descriptors(provider_key),
    chain                   text NOT NULL,
    dataset                 text NOT NULL DEFAULT '*',
    operation_class         text NOT NULL,
    mode                    text NOT NULL CHECK (mode IN ('safe', 'fast', 'turbo')),
    shard_concurrency       integer NOT NULL,
    request_concurrency     integer NOT NULL,
    requests_per_minute     numeric(18,6) NOT NULL DEFAULT 0,
    requests_per_second     numeric(18,6) NOT NULL DEFAULT 0,
    cu_per_minute           bigint NOT NULL DEFAULT 0,
    page_size               integer NOT NULL DEFAULT 0,
    batch_size              integer NOT NULL DEFAULT 1,
    request_weight          integer NOT NULL DEFAULT 1,
    request_timeout_seconds integer NOT NULL,
    hard_ceiling            integer NOT NULL,
    verified_at             timestamptz,
    verification_source     text NOT NULL DEFAULT 'initial',
    revision                bigint NOT NULL DEFAULT 1,
    updated_by              uuid REFERENCES identities(id),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_key, chain, dataset, operation_class, mode)
);

-- ---------------------------------------------------------------------------
-- 6. provider_runtime_states：每个 Provider 通道的实时档位（规范 §1.3 / 文档 §10.1）
--    与 provider_performance_profiles 的区别：档位表是配置（revision 可审计），本表是运行结果
--    （min(任务请求档, 健康允许档, 额度允许档, 机器背压档) 算出来的实际档位与降级原因）。
--    不用 FK：运行态由心跳写入，Provider 描述符被禁用时仍需保留最后的降级原因供后台展示。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_runtime_states (
    provider_key        text NOT NULL,
    chain               text NOT NULL DEFAULT '',
    operation_class     text NOT NULL DEFAULT '',
    requested_mode      text NOT NULL DEFAULT '',
    effective_mode      text NOT NULL DEFAULT '',
    target_concurrency  integer NOT NULL DEFAULT 0,
    active_concurrency  integer NOT NULL DEFAULT 0,
    requests_per_minute numeric(18,6) NOT NULL DEFAULT 0,
    requests_per_second numeric(18,6) NOT NULL DEFAULT 0,
    cu_per_minute       bigint NOT NULL DEFAULT 0,
    degrade_reason      text NOT NULL DEFAULT '',
    channel_states      jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_key, chain, operation_class)
);

-- ---------------------------------------------------------------------------
-- 7. provider_account_snapshots：dRPC 管理同步的额度快照（文档 §11.2）
--    append-only 历史：后台额度曲线与对账都按时间点回溯，不能只保留最新值。
--    account_balance 单位由 account_balance_unit 单独保存：官方只定义 balance 字段，
--    未确认货币单位前不得在代码里固定按 USD 展示。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_account_snapshots (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_key        text NOT NULL REFERENCES provider_descriptors(provider_key),
    account_tier        text NOT NULL DEFAULT 'unknown',
    account_balance     numeric(38,18),
    account_balance_unit text NOT NULL DEFAULT 'provider_unit',
    key_id              text NOT NULL DEFAULT '',
    key_name            text NOT NULL DEFAULT '',
    key_enabled         boolean,
    cu_spent_today      bigint,
    cu_day_limit        bigint,
    cu_remaining_today  bigint,
    rate_limit          numeric(18,6),
    requests_hour       bigint NOT NULL DEFAULT 0,
    requests_day        bigint NOT NULL DEFAULT 0,
    cu_hour             bigint NOT NULL DEFAULT 0,
    cu_day              bigint NOT NULL DEFAULT 0,
    source              text NOT NULL,
    source_updated_at   timestamptz,
    synced_at           timestamptz NOT NULL DEFAULT now(),
    sync_status         text NOT NULL,
    sync_error_class    text NOT NULL DEFAULT '',
    raw_hash            text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS provider_account_snapshots_recent_idx
    ON provider_account_snapshots (provider_key, synced_at DESC);

-- ---------------------------------------------------------------------------
-- 8. 种子数据（幂等）
--    只登记 dRPC 的静态事实与初始档位；status 保持 disconnected：
--    配置了 runtime key 且探针成功之前，不能声称可用（文档 §11.5）。
-- ---------------------------------------------------------------------------
INSERT INTO provider_descriptors (provider_key, display_name, kind, chains, datasets, capabilities,
                                  endpoint, status, config, concurrency, rate_limit_per_minute, priority)
VALUES ('drpc', 'dRPC Solana', 'rpc', ARRAY['solana'],
        ARRAY['transactions', 'token_transfers', 'nft_transfers', 'balances', 'logs'],
        '[{"key":"fetch","state":"declared"},{"key":"head","state":"declared"}]'::jsonb,
        'https://lb.drpc.live/solana', 'disconnected', '{"mode":"config"}'::jsonb, 4, 60, 20)
ON CONFLICT (provider_key) DO NOTHING;

-- 计价（文档 §15.1：1,000,000 CU = 0.30 USD）：Data History 1837 CU/请求，
-- Solana RPC 20 CU/方法调用。写入 provider_pricing 而不是散落在代码里，成本估算与对账共用同一来源。
INSERT INTO provider_pricing (provider, api_type, plan, credits_per_call, cost_per_call_usd)
SELECT 'drpc', 'data_api', 'payg', 1837, 0.0005511
WHERE NOT EXISTS (SELECT 1 FROM provider_pricing WHERE provider = 'drpc' AND api_type = 'data_api');
INSERT INTO provider_pricing (provider, api_type, plan, credits_per_call, cost_per_call_usd)
SELECT 'drpc', 'rpc', 'payg', 20, 0.000006
WHERE NOT EXISTS (SELECT 1 FROM provider_pricing WHERE provider = 'drpc' AND api_type = 'rpc');

-- 5 条 Solana 路由策略。api_channel/operation_class 写入 config，供运行时路由解析直接取值。
-- 5 行里只有 solana:transactions 已由 0072 创建并指向旧 Provider（solana-rpc），
-- 其余 4 行是本次新增；前者必须单独改挂 dRPC 并留 revision 记录，不能靠 DO NOTHING 静默保留。
-- max_concurrency 取 TURBO 硬上限（data_api 8 / solana_rpc 48）；solana_rpc 的速率由 Profile 的
-- RPS 控制，rate_limit_per_minute 留 0 表示不由策略行表达。
INSERT INTO routing_policies (policy_key, description, chain, dataset, enabled, primary_provider,
                              fallback_providers, max_concurrency, rate_limit_per_minute,
                              circuit_error_rate, circuit_p95_ms, circuit_cooldown_seconds,
                              rollout_percent, revision, config) VALUES
    ('solana:transactions',    'Solana 交易历史路由（dRPC Data API）', 'solana', 'transactions',
     true, 'drpc', ARRAY['solana-rpc'], 8, 60, 0.25, 8000, 60, 100, 1,
     '{"api_channel":"data_api","operation_class":"solana_history"}'::jsonb),
    ('solana:token_transfers', 'Solana SPL 转账路由（dRPC Data API）', 'solana', 'token_transfers',
     true, 'drpc', ARRAY[]::text[], 8, 60, 0.25, 8000, 60, 100, 1,
     '{"api_channel":"data_api","operation_class":"solana_history"}'::jsonb),
    ('solana:nft_transfers',   'Solana NFT 转账路由（dRPC Data API）', 'solana', 'nft_transfers',
     true, 'drpc', ARRAY[]::text[], 8, 60, 0.25, 8000, 60, 100, 1,
     '{"api_channel":"data_api","operation_class":"solana_history"}'::jsonb),
    ('solana:balances',        'Solana 余额快照路由（dRPC Solana JSON-RPC）', 'solana', 'balances',
     true, 'drpc', ARRAY[]::text[], 48, 0, 0.25, 8000, 60, 100, 1,
     '{"api_channel":"solana_rpc","operation_class":"solana_balance"}'::jsonb),
    ('solana:logs',            'Solana 程序日志路由（dRPC Solana JSON-RPC）', 'solana', 'logs',
     true, 'drpc', ARRAY[]::text[], 48, 0, 0.25, 8000, 60, 100, 1,
     '{"api_channel":"solana_rpc","operation_class":"solana_detail"}'::jsonb)
ON CONFLICT (policy_key) DO NOTHING;

-- 0072 的 solana:transactions 指向 solana-rpc（旧 Provider 只能取 transactions）。
-- 这里改挂 dRPC，并把旧值写入 routing_policy_revisions，与后台改路由走同一条留痕路径。
-- WHERE 守卫保证幂等：管理员后来手工改过（primary_provider 已不是 solana-rpc）就绝不覆盖。
DO $$
DECLARE
    before_row routing_policies%ROWTYPE;
    after_row  routing_policies%ROWTYPE;
BEGIN
    SELECT * INTO before_row FROM routing_policies WHERE policy_key = 'solana:transactions' FOR UPDATE;
    IF NOT FOUND OR before_row.primary_provider IS DISTINCT FROM 'solana-rpc' THEN
        RETURN;
    END IF;
    UPDATE routing_policies SET
        description = 'Solana 交易历史路由（dRPC Data API）',
        primary_provider = 'drpc',
        fallback_providers = ARRAY['solana-rpc'],
        max_concurrency = 8,
        rate_limit_per_minute = 60,
        config = '{"api_channel":"data_api","operation_class":"solana_history"}'::jsonb,
        revision = revision + 1,
        updated_at = now()
    WHERE policy_key = 'solana:transactions'
    RETURNING * INTO after_row;
    INSERT INTO routing_policy_revisions (policy_key, revision, before, after, change_note, actor_email)
    VALUES ('solana:transactions', after_row.revision, to_jsonb(before_row), to_jsonb(after_row),
            '0075：Solana transactions 主路由切换到 dRPC Data API；未配置 dRPC 时由运行时回退旧 provider/solana',
            'system:migration')
    ON CONFLICT (policy_key, revision) DO NOTHING;
END
$$;

-- 初始档位矩阵（文档 §6.2）。dataset='*'：transactions/token_transfers/nft_transfers 共享同一
-- Data API 历史页档位；操作类与通道绑定，故 solana_history 只有 data_api 行、
-- solana_detail（logs）与 solana_balance（balances）只有 solana_rpc 行。
-- 数值口径必须与 internal/provider/route.DefaultProfiles() 的内置兜底表一致，
-- 否则"库里有行"与"库里没行"会给出两种档位：
--   * requests_per_minute 与 requests_per_second 同时给出且互相一致（data_api 按 RPM、
--     solana_rpc 按 RPS），消费方只读其中一个字段时不会退化成不限流；
--   * cu_per_minute 是该档的**名义额度** = 单次 CU x 每分钟调用数（Data API 1837 CU/页、
--     JSON-RPC 20 CU/调用）。层级未知/免费层的 45,000 CU/分钟是运行时闸门
--     （DRPC_UNKNOWN_TIER_CU_PER_MINUTE），不能写进档位：否则付费账户的 FAST/TURBO
--     会被错误地压到 24 RPM；
--   * solana_rpc 的 shard_concurrency=0 表示"不适用"（逐签名/逐账户补全没有历史 Shard 这一层），
--     限流由 request_concurrency + RPS 决定；
--   * page_size=100 对应 DRPC_SOLANA_HISTORY_PAGE_SIZE；request_weight 4/1 用于全局加权并发
--     （Data API 单请求更贵）；request_timeout_seconds 60/30 对应 DATA/RPC 两个超时配置。
INSERT INTO provider_performance_profiles
    (provider_key, chain, dataset, operation_class, mode, shard_concurrency, request_concurrency,
     requests_per_minute, requests_per_second, cu_per_minute, page_size, batch_size, request_weight,
     request_timeout_seconds, hard_ceiling, verification_source, revision) VALUES
    ('drpc', 'solana', '*', 'solana_history', 'safe',  1,  2,   10, 0.166667,  18370, 100, 1, 4, 60,  8, 'initial', 1),
    ('drpc', 'solana', '*', 'solana_history', 'fast',  2,  4,   30, 0.5,       55110, 100, 1, 4, 60,  8, 'initial', 1),
    ('drpc', 'solana', '*', 'solana_history', 'turbo', 4,  8,   60, 1,        110220, 100, 1, 4, 60,  8, 'initial', 1),
    ('drpc', 'solana', '*', 'solana_detail',  'safe',  0,  8, 1200, 20,        24000, 100, 1, 1, 30, 48, 'initial', 1),
    ('drpc', 'solana', '*', 'solana_detail',  'fast',  0, 16, 2400, 40,        48000, 100, 1, 1, 30, 48, 'initial', 1),
    ('drpc', 'solana', '*', 'solana_detail',  'turbo', 0, 32, 4800, 80,        96000, 100, 1, 1, 30, 48, 'initial', 1),
    ('drpc', 'solana', '*', 'solana_balance', 'safe',  0,  8, 1200, 20,        24000, 100, 1, 1, 30, 48, 'initial', 1),
    ('drpc', 'solana', '*', 'solana_balance', 'fast',  0, 16, 2400, 40,        48000, 100, 1, 1, 30, 48, 'initial', 1),
    ('drpc', 'solana', '*', 'solana_balance', 'turbo', 0, 32, 4800, 80,        96000, 100, 1, 1, 30, 48, 'initial', 1)
ON CONFLICT (provider_key, chain, dataset, operation_class, mode) DO NOTHING;

-- 自检：任何一项缺失都 RAISE，让整个迁移事务回滚，而不是留下半可用的路由。
DO $$
DECLARE
    problems text[] := ARRAY[]::text[];
    n bigint;
BEGIN
    -- 表
    IF to_regclass('provider_performance_profiles') IS NULL THEN
        problems := array_append(problems, 'provider_performance_profiles 表缺失');
    END IF;
    IF to_regclass('provider_runtime_states') IS NULL THEN
        problems := array_append(problems, 'provider_runtime_states 表缺失');
    END IF;
    IF to_regclass('provider_account_snapshots') IS NULL THEN
        problems := array_append(problems, 'provider_account_snapshots 表缺失');
    END IF;

    -- 列
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                    WHERE table_name = 'provider_secrets' AND column_name = 'secret_kind') THEN
        problems := array_append(problems, 'provider_secrets.secret_kind 列缺失');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                    WHERE table_name = 'routing_policies' AND column_name = 'config') THEN
        problems := array_append(problems, 'routing_policies.config 列缺失');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                    WHERE table_name = 'provider_usage' AND column_name = 'api_channel') THEN
        problems := array_append(problems, 'provider_usage.api_channel 列缺失');
    END IF;
    SELECT count(*) INTO n FROM information_schema.columns
     WHERE table_name = 'download_shards'
       AND column_name IN ('provider_key', 'api_channel', 'operation_class',
                           'routing_policy_key', 'routing_policy_revision', 'profile_revision');
    IF n <> 6 THEN
        problems := array_append(problems, 'download_shards 路由固化列不完整');
    END IF;

    -- 约束与索引
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'provider_secrets_secret_kind_chk') THEN
        problems := array_append(problems, 'provider_secrets_secret_kind_chk 约束缺失');
    END IF;
    IF to_regclass('provider_secrets_active_kind_uniq') IS NULL THEN
        problems := array_append(problems, 'provider_secrets_active_kind_uniq 索引缺失');
    END IF;

    -- 种子：Provider 描述符与计价
    IF NOT EXISTS (SELECT 1 FROM provider_descriptors WHERE provider_key = 'drpc') THEN
        problems := array_append(problems, 'provider_descriptors 缺少 drpc 行');
    END IF;
    SELECT count(*) INTO n FROM provider_pricing WHERE provider = 'drpc';
    IF n < 2 THEN
        problems := array_append(problems, 'provider_pricing 缺少 drpc 计价（data_api/rpc）');
    END IF;

    -- 种子：5 条 Solana 路由策略必须都指向 drpc 且带 config
    SELECT count(*) INTO n FROM routing_policies
     WHERE chain = 'solana' AND primary_provider = 'drpc' AND enabled
       AND coalesce(config->>'api_channel', '') <> ''
       AND coalesce(config->>'operation_class', '') <> '';
    IF n <> 5 THEN
        problems := array_append(problems, format('routing_policies 中可用的 drpc solana 策略应为 5 条，实际 %s 条', n));
    END IF;

    -- 种子：3 个操作类 x 3 档 = 9 行初始档位
    SELECT count(*) INTO n FROM provider_performance_profiles
     WHERE provider_key = 'drpc' AND chain = 'solana' AND dataset = '*'
       AND operation_class IN ('solana_history', 'solana_detail', 'solana_balance')
       AND mode IN ('safe', 'fast', 'turbo');
    IF n <> 9 THEN
        problems := array_append(problems, format('provider_performance_profiles 初始档位应为 9 行，实际 %s 行', n));
    END IF;

    IF array_length(problems, 1) IS NOT NULL THEN
        RAISE EXCEPTION '0075 自检失败: %', array_to_string(problems, '; ');
    END IF;
END
$$;
