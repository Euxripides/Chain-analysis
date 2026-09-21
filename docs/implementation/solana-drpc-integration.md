# Solana dRPC 接入实施规范（冻结接口）

本文件是《Solana dRPC 数据源接入开发文档》的实施契约。所有并行开发必须严格遵守此处的包接口、
表结构、字段名与 row 契约；任何偏离都会在集成时被当作缺陷。

## 0. 现状与迁移策略

- 线上已有 Solana 下载：internal/provider/solana（标准 JSON-RPC，getSignaturesForAddress + getTransaction），
  经 provider.Router 按链注册，sourceProviderForDataset 对 solana 固定返回 "solana"。
- 本次新增 dRPC（Data API + Solana JSON-RPC 双通道），**不删除** internal/provider/solana：
  未配置 dRPC 时继续用旧 Provider（回退，不回归）。
- sourceProviderForDataset 改为读取 Runtime Route Resolver；TRON 分支保持原样。

## 1. 冻结的 Go 接口

### 1.1 internal/secret（新包）

    package secret

    // Resolver 解析受控密钥引用。
    type Resolver interface {
        Resolve(ctx context.Context, ref string) (string, error)
        Describe(ctx context.Context, ref string) RefStatus
    }

    type RefStatus struct {
        Ref         string `json:"ref"`
        Kind        string `json:"kind"`        // env | store | empty
        Name        string `json:"name"`
        Configured  bool   `json:"configured"`
        Prefix      string `json:"prefix"`      // 明文前 6 位
        Fingerprint string `json:"fingerprint"` // sha256 前 16 hex
    }

    var ErrNotConfigured = errors.New("secret not configured")

    type ManagerOptions struct {
        FilePath string // 受管密钥文件（0600，JSON map[ref]plaintext）
        Log      *slog.Logger
    }
    func NewManager(opts ManagerOptions) *Manager
    func (m *Manager) Resolve(ctx context.Context, ref string) (string, error)
    func (m *Manager) Describe(ctx context.Context, ref string) RefStatus
    // Put 写入受管 store 并返回规范化引用 store://<provider>/<kind>。
    // 明文不落 PostgreSQL、不进日志、不进审计。
    func (m *Manager) Put(providerKey, kind, plaintext string) (ref string, status RefStatus, err error)
    func (m *Manager) Delete(providerKey, kind string) error

支持的引用形式：env://NAME、store://<provider_key>/<kind>、空字符串（未配置）。

### 1.2 internal/provider/route（新包）

    package route

    type Route struct {
        ProviderKey    string `json:"provider_key"`
        APIChannel     string `json:"api_channel"`
        OperationClass string `json:"operation_class"`
        PolicyKey      string `json:"routing_policy_key"`
        PolicyRevision int64  `json:"routing_policy_revision"`
    }

    type RuntimeProvider struct {
        Key         string
        DisplayName string
        Channels    []string
        Provider    provider.Provider
    }

    type Registry struct{ /* ... */ }
    func NewRegistry(log *slog.Logger) *Registry
    func (r *Registry) Register(key, displayName string, channels []string, p provider.Provider)
    // Get 按 provider_key 取运行时实现（后台探针用）。
    func (r *Registry) Get(key string) (provider.Provider, bool)
    func (r *Registry) List() []RuntimeProvider
    // Resolve 解析 (chain, dataset) -> Route。
    // 优先级：1) 数据库 routing_policies（LoadPolicies 注入）2) 内置默认表
    // 未命中返回 ok=false，调用方回退旧的链级特判。
    func (r *Registry) Resolve(chain, dataset string) (Route, bool)

    type PolicyRow struct {
        PolicyKey       string
        Chain           string
        Dataset         string
        Enabled         bool
        PrimaryProvider string
        APIChannel      string
        OperationClass  string
        Revision        int64
    }
    func (r *Registry) LoadPolicies(rows []PolicyRow)

    // Registry 自身实现 provider.Provider：按 (chain,dataset) 分派。
    func (r *Registry) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error)
    func (r *Registry) Name() string   // "route"
    func (r *Registry) Capabilities() provider.Capabilities
    func (r *Registry) Health(ctx context.Context) provider.HealthStatus

内置默认路由表（优先级 2）：

| chain | dataset | provider_key | api_channel | operation_class |
|---|---|---|---|---|
| solana | transactions | drpc | data_api | solana_history |
| solana | token_transfers | drpc | data_api | solana_history |
| solana | nft_transfers | drpc | data_api | solana_history |
| solana | balances | drpc | solana_rpc | solana_balance |
| solana | logs | drpc | solana_rpc | solana_detail |
| 其它非 tron | * | ankr | ankr_advanced | evm_history |
| tron | transactions | cfg.Tron.ProviderForDataset | trongrid/ankr_tron_node | tron_history |
| tron | token_transfers | cfg.Tron.ProviderForDataset | 同上 | tron_history |
| tron | internal_transactions | cfg.Tron.ProviderForDataset | 同上 | tron_history |

**Solana 路由仅在 cfg.DRPC.Enabled 为真时命中**；否则 Resolve 对 solana 返回未命中。

### 1.3 internal/provider/route：性能档位

    type Profile struct {
        ProviderKey, Chain, Dataset, OperationClass, Mode string
        ShardConcurrency      int
        RequestConcurrency    int
        RequestsPerMinute     float64
        RequestsPerSecond     float64
        CUPerMinute           int64
        PageSize              int
        BatchSize             int
        RequestWeight         int
        RequestTimeoutSeconds int
        HardCeiling           int
        Revision              int64
        VerifiedAt            *time.Time
        VerificationSource    string
    }

    type ProfileSource interface {
        PerformanceProfile(ctx context.Context, providerKey, chain, dataset, operation, mode string) (Profile, bool)
    }
    func (r *Registry) SetProfileSource(src ProfileSource)
    func (r *Registry) Profile(ctx context.Context, providerKey, chain, dataset, operation, mode string) (Profile, bool)
    func DefaultProfiles() []Profile

### 1.4 internal/provider/drpc（新包）

    package drpc

    const (
        ProviderKey      = "drpc"
        ChannelDataAPI   = "data_api"
        ChannelSolanaRPC = "solana_rpc"
    )

    type Options struct {
        Enabled bool
        Tier    string // paid | free | unknown
        // BaseURL 形如 https://lb.drpc.live/solana（不含 key）
        BaseURL            string
        RuntimeKeyRef      string
        StatisticsTokenRef string
        KeysTokenRef       string
        Secrets            secret.Resolver
        HistoryPageSize        int
        MaxTxVersion           int
        RPCTimeout             time.Duration
        DataTimeout            time.Duration
        SlotTimeSearchLimit    int
        SlotTimeMargin         time.Duration
        UnknownTierCUPerMinute int64
        QuotaWarnPercent       int
        QuotaSafeOnlyPercent   int
        QuotaStopPercent       int
        BalanceWarn            string
        ProfileSource          ProfileSource
        UsageHook              provider.UsageHook
        Log                    *slog.Logger
    }

    // 为避免包循环 drpc 不 import route：
    type Profile struct { /* 与 route.Profile 同名字段子集 + OperationClass/Mode */ }
    type ProfileSource interface {
        Profile(ctx context.Context, providerKey, chain, dataset, operation, mode string) (Profile, bool)
    }

    type Client struct{ /* ... */ }
    func New(opts Options, hook provider.UsageHook) *Client // 永不返回 nil
    func (c *Client) Name() string { return ProviderKey }
    func (c *Client) Capabilities() provider.Capabilities
    func (c *Client) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error)
    func (c *Client) Health(ctx context.Context) provider.HealthStatus
    // ProbeChannels 真实探针：Data API 低成本调用 + JSON-RPC getHealth。
    func (c *Client) ProbeChannels(ctx context.Context) map[string]any
    // QuotaStatus 供后台展示（不含秘密）。
    func (c *Client) QuotaStatus() map[string]any
    // SetQuotaSnapshot 由 drpcmgmt 同步后注入（跨通道共享闸门）。
    func (c *Client) SetQuotaSnapshot(s QuotaSnapshot)

    type QuotaSnapshot struct {
        Tier         string
        Balance      string
        BalanceUnit  string
        CUSpentToday *int64
        CUDayLimit   *int64
        RateLimit    *float64
        SyncedAt     time.Time
        KeyEnabled   *bool
        Source       string
    }

Channel 语义：

- Fetch 按 req.Dataset 选择通道：
  - transactions / token_transfers / nft_transfers -> Data API 历史页；
  - logs / balances -> Solana JSON-RPC；
  - internal_transactions -> provider.Error{Class: ClassUnsupported}，消息含 SOLANA_DATASET_UNSUPPORTED。
- Data API 页：GET {base}/{key}/lambda/v1/transactions/{address}/history?start&end&limit&page_token。
  start/end 为毫秒。limit 取 req.PageSize（钳制到 [1, HistoryPageSize]）。
- JSON-RPC 页：POST {base}/{key}。logs 用 getSignaturesForAddress + 每签名 getTransaction；
  balances 用 getBalance + getTokenAccountsByOwner + getSlot。
- 每个请求前依次通过：通道信号量（RequestConcurrency）-> CU Token Bucket -> RPM/RPS Token Bucket
  -> quotaGate（账户级共享：CU 用尽/Key 停用时拒绝并返回 ClassQuota）。

错误码（provider.Error.Message 前缀，前端可见）：DRPC_NOT_CONFIGURED、DRPC_AUTH_FAILED、
DRPC_QUOTA_EXHAUSTED、DRPC_RATE_LIMITED、DRPC_MANAGEMENT_STALE、DRPC_PAGINATION_INVALID、
SOLANA_TRANSACTION_VERSION_UNSUPPORTED、SOLANA_SLOT_TIME_UNAVAILABLE。

用量记录：每次 HTTP 请求写一条 model.UsageRow（Provider="drpc"、
APIType=data_api|rpc、Chain="solana"、Method、UsageType=dataset_download|tx_detail|
balance_snapshot|provider_test|quota_sync、MethodCalls、Credits=CU、CostUSD、
Status、HTTPStatus、ErrorClass、RequestID）。

### 1.5 internal/provider/drpcmgmt（新包）

    package drpcmgmt

    type Options struct {
        Enabled            bool
        StatisticsTokenRef string
        KeysTokenRef       string
        Secrets            secret.Resolver
        BaseURL            string // https://drpc.org
        SyncInterval       time.Duration
        StaleAfter         time.Duration
        SafeOnlyAfter      time.Duration
        HTTPTimeout        time.Duration
        Log                *slog.Logger
    }

    type Snapshot struct {
        Tier             string
        Balance          *decimal.Decimal
        BalanceUnit      string
        KeyID, KeyName   string
        KeyEnabled       *bool
        CUSpentToday     *int64
        CUDayLimit       *int64
        CURemainingToday *int64
        RateLimit        *float64
        RequestsHour, RequestsDay, CUHour, CUDay int64
        Source           string
        SourceUpdatedAt  *time.Time
        SyncedAt         time.Time
        SyncStatus       string // ok | ok_no_management_token | error
        SyncErrorClass   string
        RawHash          string
    }

    type Service struct{ /* ... */ }
    func New(opts Options, st *store.Store, log *slog.Logger) *Service
    // Sync 拉取 Statistics API(range=hour/day) 与 Keys API，合并成 Snapshot 并落库。
    func (s *Service) Sync(ctx context.Context) (Snapshot, error)
    func (s *Service) Run(ctx context.Context)
    func (s *Service) LastSnapshot(ctx context.Context) (*Snapshot, error)

- Statistics API：GET {base}/api/metrics/json?range=day，Bearer statistics_api_token。
- Keys API：GET {base}/api/team/keys/list，Bearer keys_api_token。
- 同步频率下限 5 分钟；后台手工同步 60 秒内返回上次快照。
- cu_day_limit 空或 0 -> 不计算剩余，前端显示未设置日上限。

## 2. 数据库契约（迁移文件）

### 2.1 PostgreSQL 0075_drpc_solana.sql

必须幂等（ADD COLUMN IF NOT EXISTS / CREATE TABLE IF NOT EXISTS / ON CONFLICT），
末尾带 DO $$ 自检块（缺少新表/新列/种子行即 RAISE EXCEPTION 回滚）。

1. provider_secrets 增加 secret_kind text NOT NULL DEFAULT 'runtime_api_key'
   + CHECK in ('runtime_api_key','statistics_api_token','keys_api_token')
   + partial unique index (provider_key, secret_kind) WHERE status='active'。
2. download_shards 增加 provider_key/api_channel/operation_class（text NOT NULL DEFAULT ''）、
   routing_policy_key text NOT NULL DEFAULT ''、routing_policy_revision bigint NOT NULL DEFAULT 0、
   profile_revision bigint NOT NULL DEFAULT 0。
3. provider_usage 增加 api_channel text NOT NULL DEFAULT '' + 索引（真实列名先读现有迁移）。
4. provider_performance_profiles（主键 provider_key+chain+dataset+operation_class+mode），列同开发文档 10.1。
5. provider_runtime_states：主键 (provider_key, chain, operation_class)，列：requested_mode、
   effective_mode、target_concurrency、active_concurrency、requests_per_minute numeric、
   requests_per_second numeric、cu_per_minute bigint、degrade_reason text、channel_states jsonb、updated_at。
6. provider_account_snapshots：列同开发文档 11.2 + 索引 (provider_key, synced_at DESC)。
7. 种子：
   - provider_descriptors 插入 drpc 行（chains=ARRAY['solana']，datasets 五项，
     endpoint https://lb.drpc.live/solana，status disconnected，concurrency 4，rate_limit 60，priority 20），
     ON CONFLICT (provider_key) DO NOTHING。
   - routing_policies 5 行（solana:transactions / token_transfers / nft_transfers / balances / logs），
     primary_provider='drpc'；若表无 config 列则新增 config jsonb NOT NULL DEFAULT '{}'::jsonb，
     行内写 api_channel + operation_class。
   - provider_performance_profiles 按 6.2 矩阵插入 dRPC 两通道 x 三档，ON CONFLICT DO NOTHING。

### 2.2 ClickHouse 0010_solana_drpc.sql

    ALTER TABLE blockchain.transactions
        ADD COLUMN IF NOT EXISTS fee_raw String DEFAULT '',
        ADD COLUMN IF NOT EXISTS signers Array(String) DEFAULT [],
        ADD COLUMN IF NOT EXISTS account_keys Array(String) DEFAULT [],
        ADD COLUMN IF NOT EXISTS program_ids Array(String) DEFAULT [],
        ADD COLUMN IF NOT EXISTS source_api LowCardinality(String) DEFAULT '',
        ADD COLUMN IF NOT EXISTS source_method LowCardinality(String) DEFAULT '',
        ADD COLUMN IF NOT EXISTS source_request_id String DEFAULT '',
        ADD COLUMN IF NOT EXISTS chain_payload_json String DEFAULT '';

    ALTER TABLE blockchain.token_transfers
        ADD COLUMN IF NOT EXISTS instruction_index UInt32 DEFAULT 0,
        ADD COLUMN IF NOT EXISTS inner_instruction_index Int32 DEFAULT -1,
        ADD COLUMN IF NOT EXISTS source_api LowCardinality(String) DEFAULT '',
        ADD COLUMN IF NOT EXISTS source_request_id String DEFAULT '';

    ALTER TABLE blockchain.nft_transfers
        ADD COLUMN IF NOT EXISTS instruction_index UInt32 DEFAULT 0,
        ADD COLUMN IF NOT EXISTS inner_instruction_index Int32 DEFAULT -1,
        ADD COLUMN IF NOT EXISTS source_api LowCardinality(String) DEFAULT '',
        ADD COLUMN IF NOT EXISTS source_request_id String DEFAULT '';

    CREATE TABLE IF NOT EXISTS blockchain.solana_program_logs ( 同文档 8.4 );

现有 token_transfers 已有 from_token_account/to_token_account（0008 迁移），不要重复添加。

### 2.3 solana_program_logs 的写入与查询

采用**双写**：dataset=logs 且 chain=solana 时同时写 blockchain.logs
（event_source=solana_program_log）与 blockchain.solana_program_logs，避免读路径分叉导致 500。
提供 ingest.Writer.WriteSolanaProgramLogs(ctx, []SolanaProgramLogRow)。

## 3. Row 契约（Provider -> ingest）

dRPC Provider 输出的每一行必须是 Ankr 同构 JSON，字段名固定。

transactions（Data API 行 / RPC 补全行）：

    {"blockNumber":"<slot>","blockHash":"","hash":"<signature>","timestamp":<unix秒>,
     "from":"<fee payer>","to":"<primary recipient 或空>","value":"<lamports 或空>",
     "nonce":"0","gasLimit":"0","gasUsed":"0","gasPrice":"0","type":"<activity type>",
     "status":"1|0","input":"","feeRaw":"<lamports>","signers":["..."],
     "accountKeys":["..."],"programIds":["..."],"sourceApi":"data_api",
     "sourceMethod":"get_transactions_history","sourceRequestId":"...",
     "chainPayloadJson":"{...}"}

from/to 不转小写（pickAddressForChain 已按链处理）；value 只在存在单笔原生转账时填写，否则空串。

token_transfers：

    {"blockHeight":"<slot>","timestamp":<unix秒>,"transactionHash":"<sig>","logIndex":<本地稳定序号>,
     "contractAddress":"<mint>","tokenSymbol":"","tokenDecimals":<n>,"fromAddress":"<owner>",
     "toAddress":"<owner>","valueRawInteger":"<amount>","value":"<amount>",
     "fromTokenAccount":"","toTokenAccount":"","instructionIndex":n,"innerInstructionIndex":n,
     "sourceApi":"data_api","sourceRequestId":"..."}

nft_transfers：

    {"blockNumber":"<slot>","timestamp":<unix秒>,"transactionHash":"<sig>","logIndex":n,
     "contractAddress":"<mint>","fromAddress":"","toAddress":"","value":"1",
     "tokenStandard":"SPL","sourceApi":"data_api","sourceRequestId":"..."}

logs（程序日志；solana.NormalizeProgramLog 读取）：

    {"transactionHash":"<sig>","blockNumber":"<slot>","timestamp":<unix秒>,"logIndex":n,
     "address":"<programId>","data":"<log message>","eventSource":"solana_program_log",
     "programId":"","invokeDepth":n,"success":true,"removed":false,
     "sourceApi":"solana_rpc","sourceRequestId":"..."}

balances（solana.NormalizeBalance 读取）：

    {"holderAddress":"<owner>","contractAddress":"<mint 或空=原生 SOL>",
     "tokenType":"native|spl|spl-token-2022","tokenDecimals":<n>,
     "balanceRawInteger":"<raw>","snapshotBlock":"<slot>","timestamp":<unix秒>}

## 4. 配置项（config.DRPCConfig）

    DRPC_ENABLED=false
    DRPC_TIER=unknown
    DRPC_BASE_URL=https://lb.drpc.live/solana
    DRPC_RUNTIME_KEY_REF=store://drpc/runtime_api_key
    DRPC_STATISTICS_TOKEN_REF=store://drpc/statistics_api_token
    DRPC_KEYS_TOKEN_REF=store://drpc/keys_api_token
    DRPC_MANAGEMENT_BASE_URL=https://drpc.org
    DRPC_MANAGEMENT_SYNC_INTERVAL_SECONDS=300
    DRPC_MANAGEMENT_STALE_AFTER_SECONDS=900
    DRPC_MANAGEMENT_SAFE_ONLY_AFTER_SECONDS=3600
    DRPC_SOLANA_HISTORY_PAGE_SIZE=100
    DRPC_SOLANA_MAX_TX_VERSION=0
    DRPC_SOLANA_RPC_REQUEST_TIMEOUT_SECONDS=30
    DRPC_SOLANA_DATA_REQUEST_TIMEOUT_SECONDS=60
    DRPC_SOLANA_SLOT_TIME_SEARCH_LIMIT=64
    DRPC_SOLANA_SLOT_TIME_MARGIN_SECONDS=120
    DRPC_UNKNOWN_TIER_CU_PER_MINUTE=45000
    DRPC_QUOTA_WARN_PERCENT=80
    DRPC_QUOTA_SAFE_ONLY_PERCENT=95
    DRPC_QUOTA_STOP_PERCENT=100
    DRPC_BALANCE_WARN=
    PROVIDER_SECRETS_FILE=

SOLANA_ENABLED、SOLANA_PROVIDER=drpc|solana-rpc 保留：SOLANA_PROVIDER=drpc 且 DRPC_ENABLED=true
时注册 dRPC；否则沿用旧 provider/solana。

## 5. 后台 API 契约

    GET   /providers/{id}/quota                                  admin:cost:read
    GET   /providers/{id}/quota-history?range=day                admin:cost:read
    GET   /providers/{id}/performance-profiles                   admin:provider:read
    PATCH /providers/{id}/performance-profiles/{chain}/{operation}/{mode}
                                                                 admin:performance:update (If-Match + reason + Step-up)
    GET   /providers/{id}/runtime-states                         admin:provider:read
    POST  /providers/{id}/commands/sync-quota                    admin:provider:update
    POST  /providers/{id}/commands/probe-channels                admin:provider:update
    PUT   /providers/{id}/secrets/{kind}                         admin:provider:update (+ Step-up)
          body: {"secret_ref":"env://X"} 或 {"value":"<明文>"}（明文只写受管 store）

GET /providers/{id} 详情响应新增（只增不改）：runtime_health.channels、
quota{tier,balance,balance_unit,cu_spent_today,cu_day_limit,cu_remaining_today,rate_limit,
synced_at,stale,source}、performance_profiles[]、secrets[] 每项含 secret_kind。

s6ProviderProbe 改为经 deps.Routes.Get(key) 取运行时实现；找不到才返回 not_runtime_provider。

## 6. 前端契约

- /api/v2/meta/datasets?chain=<slug> 返回 {items:[{id,label,status,provider,api_channel,
  supports_full,supports_time_range,supports_block_range}],source:"capability_registry"}；
  status 为 available|unsupported。前端只允许勾选 available。
- 预览响应新增 provider_plan[]：{provider,api_channel,estimated_pages,estimated_rpc_calls,
  estimated_cu,estimated_cost_usd}。
- 任务详情新增 provider_runtime_states[]：{provider,api_channel,operation_class,requested_mode,
  effective_mode,request_concurrency,requests_per_minute,shard_concurrency,cu_per_minute,
  used_requests,used_cu,estimated_cost_usd,reason}。
- Shard 详情新增 provider_key,api_channel,operation_class,profile_revision。
- 所有提示（警告/确认/成功/错误）必须用 Dialog，禁止页面内联横幅（AGENTS.md 硬约束）。

## 7. 并行分工与文件归属（避免写冲突）

| 工作流 | 独占文件/目录 |
|---|---|
| A 路由与档位 | backend/internal/provider/route/**、backend/internal/secret/** |
| B dRPC Provider | backend/internal/provider/drpc/** |
| C 迁移与存储 | backend/migrations/postgres/0075_*.sql、backend/migrations/clickhouse/0010_*.sql、backend/internal/store/drpc.go |
| D 管理同步 | backend/internal/provider/drpcmgmt/** |
| E 后台 API | backend/internal/api/sprint6_drpc.go（新文件）+ sprint6_provider.go 最小改动 |
| F 数据面 | backend/internal/solana/**、backend/internal/ingest/writer.go、backend/internal/ingest/normalizer.go |
| G 接线 | backend/internal/app/app.go、backend/internal/config/config.go、backend/internal/download/*（主控执行） |
| H 用户端前端 | frontend/apps/user-console/** |
| I 管理台前端 | frontend/apps/admin-console/** |


---

## 8. 实施结果（2026-09-21）

### 8.1 已落地

| 工作流 | 交付物 |
|---|---|
| 路由与档位 | internal/provider/route（Registry/Route/PolicyRow/Profile/DefaultProfiles/Provider 实现）|
| 密钥 | internal/secret（Resolver/Manager：env:// 与 store://，0600 原子写，Describe 只回前缀+指纹）|
| dRPC Provider | internal/provider/drpc（双通道、限流、额度闸门、分页守卫、错误分类、用量；83 测试 / 80.7% 覆盖 / -race 通过）|
| 管理同步 | internal/provider/drpcmgmt（Statistics + Keys API，60s 防抖，快照落库，阈值告警）|
| 数据库 | migrations/postgres/0075_drpc_solana.sql、migrations/clickhouse/0010_solana_drpc.sql（已在生产库执行并验证）|
| 存储 | internal/store/drpc.go（档位 CRUD、运行时状态、账户快照、按 kind 密钥、按通道用量）|
| 数据面 | internal/solana（tx/transfers/程序日志/provider 参数化）、ingest 新列与 solana_program_logs 双写 |
| 地址 | internal/addressnorm（全链路按链规范化，Solana 不再被小写化）|
| 后台 API | internal/api/sprint6_drpc.go（额度/档位/运行时/密钥/命令）+ 探针改走 Runtime Registry |
| 用户端 | 数据集能力、Slot 标签、provider_plan、任务 Provider Runtime；Dialogs 化内联提示 |
| 管理台 | 列表额度列、Provider 详情三个新 Tab、路由页通道/操作类列 |

### 8.2 与开发文档的差异（均为有意取舍）

1. **Solana 的 internal_transactions 继续可用**：文档 §5.2 把它列为不支持（dRPC 不提供），
   但旧 JSON-RPC 解码器已能产出 CPI 调用树。删掉一个可用能力不属于本次接入范围，
   因此它保留在链白名单里并继续由 solana-rpc 承载；dRPC 负责其余五个数据集。
2. **nft_transfers 的可用性随通道变化**：dRPC Data API 的 transfer.token.nft 是明确对象，
   因此 Data API 就绪时 nft_transfers 进入白名单；回退到旧 JSON-RPC 时按 ADR-023 D9 排除。
   这是进程级开关（model.SetSolanaDataAPI），保证白名单、路由、前端三方一致。
3. **部署顺序与策略投影**：0075 把 solana:* 策略行指向 drpc（配置事实）。
   但部署是「先上代码、后录 Key」，因此运行期按密钥可解析性把 Solana 策略投影回 solana-rpc，
   Key 到位后 10 秒内自动切回 drpc（app.projectSolanaPolicies + 单测固定该行为）。
4. **生产层级校验不放行**：文档 §12 要求生产 DRPC_TIER=paid。
   实现改为「不拒绝启动 + 按 CU 上限钳制 + 页面标注 cu_cap_unknown_tier」，
   否则「先部署、后录 Key」的正常上线路会被启动校验挡住。发布验收仍要求 paid。
5. **档位 cu_per_minute 是名义额度**（单次 CU × 每分钟调用数），不是 45000；
   45000 是未知/免费层级的**运行时闸门**（DRPC_UNKNOWN_TIER_CU_PER_MINUTE）。
6. **程序日志双写**：同时写 blockchain.logs（event_source=solana_program_log）与
   blockchain.solana_program_logs，避免为一条链把查询层分叉；扩展表不计入 fact 行数。
7. **未接入项**：文档 §17 的 Prometheus 指标（dRPC 部分）、§6.3 的调度器级 Provider 执行池
   （并发/速率由 Provider 内部按档位强制，全局加权槽位沿用既有实现）、
   §5.3 的 Slot 范围边界扩展已实现但需真实 Key 验证。

### 8.3 验证状态

- 后端：go build ./... 通过；go test ./... 全绿（含 drpc 83 项、route 13 项、secret 15 项、drpcmgmt 16 项）。
- 契约：python3 scripts/check_openapi.py 通过（202 路径一致）。
- 数据库：0075 与 0010 已在生产库执行，幂等复跑通过；种子（descriptor/5 策略/9 档位/2 定价）已核对。
- 前端：typecheck + uc-contract-freeze-check + uc-v4-render-check。
- 待真实 Key 验证：真实 dRPC 三档并发、真实地址完整性、CU 与官方对账（文档 §21.3）。

