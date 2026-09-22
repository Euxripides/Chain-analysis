package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

type AnkrConfig struct {
	Mode                    string
	APIKey                  string
	Endpoint                string
	MaxPageSize             int
	InitialConcurrency      int
	MaxConcurrency          int
	PerformanceModesEnabled bool
	ProdSafeConcurrency     int
	ProdFastConcurrency     int
	ProdTurboConcurrency    int
	ProdHardCeiling         int
	ProdRejectBoundary      int
	RequestsPerMinute       int
	RequestTimeout          time.Duration
	ResponseHeaderTimeout   time.Duration
	CreditsPerRequest       float64
	CostPerRequestUSD       float64

	// V1.1：响应体保护（§8/§32）
	ResponseSoftLimitBytes int64
	ResponseHardLimitBytes int64

	// V1.1：自适应 PageSize（§12-§15/§32）
	PageLevels                  []int
	PageSizeTransactions        int
	PageSizeTokenTransfers      int
	PageSizeNftTransfers        int
	PageSizeLogs                int
	PageUpSuccessCount          int
	PageUpMaxDuration           time.Duration
	PageUpMaxBodyBytes          int64
	PageDownCooldownSuccess     int
	RangeSplitOnMinPageFailures int

	// Ankr 引擎（§21/§22/§26/§98）：超时分层、响应上限、Feature Flags
	ConnectTimeout      time.Duration
	TLSHandshakeTimeout time.Duration
	BodyIdleTimeout     time.Duration

	// Local Durable Spool（§37/§38/§72）
	SpoolDir              string
	SpoolMaxBytes         int64
	SpoolWatermarkMild    float64 // 60% → mild throttle
	SpoolWatermarkReduce  float64 // 75% → reduce fetch
	SpoolWatermarkPauseBG float64 // 85% → pause background
	SpoolWatermarkStop    float64 // 95% → stop new fetch

	// Async Pipeline（§55/§56/§58/§70）
	FetchSlots        int
	ParserSlots       int
	CHWriterSlots     int
	CHBatchMaxRows    int
	CHBatchMaxBytes   int64
	CHBatchFlush      time.Duration
	CHInsertP95Thresh time.Duration

	// Global Memory Budget（§47/§48）
	FetchMemoryBudgetBytes     int64
	WorkerRSSHardMaxBytes      int64
	VMMemAvailableWarnBytes    int64
	VMMemAvailableProtectBytes int64
	SystemMemoryGuardEnabled   bool
	SwapGrowthSamples          int

	// Feature Flags（§98）
	AsyncIngestEnabled         bool
	LocalSpoolEnabled          bool
	RangeShardingEnabled       bool
	AdaptivePageEnabled        bool
	TailAccelerationEnabled    bool
	WeightedConcurrencyEnabled bool
	ValidateFinalEnabled       bool

	// V1.4（§55）：Global Ready Queue / 自适应并发 / Range Certification / Retry Budget
	V14Enabled                    bool   // ANKR_V14_ENABLED（主控；默认 false → 一键回旧调度路径）
	GlobalReadyQueueEnabled       bool   // ANKR_GLOBAL_READY_QUEUE_ENABLED（LPT/权重排序的全局队列）
	GlobalReadyQueueShadow        bool   // ANKR_GLOBAL_READY_QUEUE_SHADOW（§56：记录 diff 不改顺序）
	TransactionsRangeCertRequired bool   // ANKR_TRANSACTIONS_RANGE_CERT_REQUIRED（§14：无 Certification 不允许多 Range）
	PageRetryMaxAttempts          int    // ANKR_PAGE_RETRY_MAX_ATTEMPTS（§27 Retry Budget）
	PageRetryBudgetSeconds        int    // ANKR_PAGE_RETRY_BUDGET_SECONDS（§27 时间预算）
	ReadyQueueMode                string // ANKR_READY_QUEUE_MODE: fifo|lpt|weighted（default fifo 兼容 V1.1-V1.3）
	TailBoostRemainingShards      int    // ANKR_TAIL_BOOST_REMAINING_SHARDS（§9：剩 N 个 shard 进 tail boost）
	ReadyAgingWindowSeconds       int    // ANKR_READY_AGING_WINDOW_SECONDS（§9 aging 起始窗口）
	LeaseReaperGraceSeconds       int    // ANKR_LEASE_REAPER_GRACE_SECONDS（过期后的显式宽限，默认 0）
	AllowSourceDuplicates         bool   // ANKR_ALLOW_SOURCE_DUPLICATES（仅在已分类且 canonical set 一致后开启）
	DuplicateDiagnosticsEnabled   bool   // ANKR_DUPLICATE_DIAGNOSTICS_ENABLED（benchmark 诊断原始重复）

	// Tail Acceleration（§15）
	TailBoostConcurrency int
	TailSplitWindowMin   int64
}

type TronConfig struct {
	Enabled     bool
	VolumeGuard VolumeGuardConfig
	// Provider is the legacy global fallback. Dataset-specific providers take
	// precedence and keep the TRON routing decision local to each dataset.
	Provider                        string
	DefaultProvider                 string
	TransactionsProvider            string
	TRC20Provider                   string
	TRC20PrimaryProvider            string
	TRC20FallbackProvider           string
	TRC20AnkrFallbackEnabled        bool
	TRC20FallbackMaxWindows         uint64
	InternalProvider                string
	ReceiptProvider                 string
	TxDetailProvider                string
	TronGridEnabled                 bool
	BaseURL                         string
	APIKey                          string
	TronGridPageSize                int
	TronGridInitialConcurrency      int
	TronGridNormalConcurrency       int
	TronGridMaxConcurrency          int
	TronGridRequestTimeout          time.Duration
	TronGridMinRequestInterval      time.Duration
	TronGridMaxRetries              int
	TronGridDailyRequestBudget      int64
	TronGridTXPageSize              int
	TronGridTXInitialConcurrency    int
	TronGridTXNormalConcurrency     int
	TronGridTXMaxConcurrency        int
	TronGridTXRequestTimeout        time.Duration
	TronGridTXMaxRetries            int
	TronGridTRC20PageSize           int
	TronGridTRC20InitialConcurrency int
	TronGridTRC20NormalConcurrency  int
	TronGridTRC20MaxConcurrency     int
	TronGridTRC20RequestTimeout     time.Duration
	TronGridTRC20MaxRetries         int
	InitialConcurrency              int
	MaxConcurrency                  int
	RequestTimeout                  time.Duration
	ConnectTimeout                  time.Duration
	MaxRetries                      int
	IncrementalOverlap              time.Duration
	AnkrConcurrency                 int
	LogInitialSpan                  uint64
	LogMinSpan                      uint64
	LogMaxSpan                      uint64
	BlockSpan                       uint64
	ReceiptSpan                     uint64
	MaxScanBlocks                   uint64
	OverlapBlocks                   uint64
	MaxTxAutoScan                   uint64
	MaxInternalAutoScan             uint64
	TimeBlockCacheTTL               time.Duration
	TimeBlockProbeRetry             int
	ReceiptCacheMaxEntries          int
	ReceiptCacheTTL                 time.Duration
	ReceiptInitialConcurrency       int
	ReceiptMaxConcurrency           int
	ReceiptAdaptiveEnabled          bool
	ReceiptAdaptiveHardMax          int
	TRC20AdaptiveEnrichment         bool
	TRC20LogEnrichmentRatio         float64
	TRC20EnrichLogMaxWindows        int
	TRC20EnrichLogSpan              uint64
	TRC20BatchReceipts              bool
	TRC20LocalIdentityEnabled       bool
	TRC20PrefetchPages              int
	TRC20PageQueueMax               int
}

type VolumeGuardConfig struct {
	Enabled              bool
	WarnRows             int64
	HardActualRows       int64
	HardProjectedRows    int64
	ExtremeRows          int64
	SuperExtremeRows     int64
	ProjectedConfidence  float64
	ExtremeConfidence    float64
	PreflightMaxRequests int
	PreflightTimeout     time.Duration
}

// ProviderForCapability keeps TRON node/enrichment capabilities independent
// from dataset routing. Receipt and transaction detail default to Ankr even
// when transaction history is explicitly routed to TronGrid.
func (t TronConfig) ProviderForCapability(capability string) string {
	var configured string
	switch strings.ToLower(strings.TrimSpace(capability)) {
	case "receipt":
		configured = t.ReceiptProvider
	case "tx_detail", "transaction_detail":
		configured = t.TxDetailProvider
	default:
		configured = t.DefaultProvider
	}
	if provider := strings.ToLower(strings.TrimSpace(configured)); provider != "" {
		return provider
	}
	return "ankr"
}

// ProviderForDataset resolves the dataset-level TRON provider without
// changing the legacy TRON_PROVIDER fallback semantics. Empty dataset
// overrides intentionally fall back to DefaultProvider and then Provider.
func (t TronConfig) ProviderForDataset(dataset string) string {
	defaultProvider := strings.ToLower(strings.TrimSpace(t.DefaultProvider))
	if defaultProvider == "" {
		defaultProvider = strings.ToLower(strings.TrimSpace(t.Provider))
		if defaultProvider == "trongrid" {
			// Never let the legacy global switch silently route TRC20 or
			// internal datasets through the indexer. Callers must opt in with
			// TRON_DEFAULT_PROVIDER or a dataset-specific override.
			defaultProvider = "ankr"
		}
	}
	if defaultProvider == "" {
		defaultProvider = "ankr"
	}
	var override string
	switch dataset {
	case "transactions":
		override = t.TransactionsProvider
	case "token_transfers":
		override = t.TRC20PrimaryProvider
		if strings.TrimSpace(override) == "" {
			override = t.TRC20Provider
		}
	case "internal_transactions":
		override = t.InternalProvider
	case "internal":
		override = t.InternalProvider
	}
	if p := strings.ToLower(strings.TrimSpace(override)); p != "" {
		return p
	}
	return defaultProvider
}

func (c *Config) AnkrProductionProfile() model.DownloadPerformanceProfile {
	return model.DownloadPerformanceProfile{
		Scope: "global", DefaultMode: model.PerformanceFast,
		SafeConcurrency: c.Ankr.ProdSafeConcurrency, FastConcurrency: c.Ankr.ProdFastConcurrency,
		TurboConcurrency: c.Ankr.ProdTurboConcurrency, HardCeiling: c.Ankr.ProdHardCeiling,
		RejectBoundary: c.Ankr.ProdRejectBoundary, MemoryBudgetBytes: c.Ankr.FetchMemoryBudgetBytes,
		AbsoluteRSSLimitBytes: c.Ankr.WorkerRSSHardMaxBytes, PageSizeDefault: c.Ankr.PageSizeTransactions,
		RequestTimeoutSeconds: int(c.Ankr.RequestTimeout / time.Second), RangeShardingEnabled: c.Ankr.RangeShardingEnabled,
		Version: "1.4.2", VerifiedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), UpdatedBy: "environment",
	}
}

// MethodConfig V1.3 Method / ABI 解析（§17-§20/§31/§35）。
type MethodConfig struct {
	Enabled                 bool
	SeedOnStart             bool
	RegistryRefresh         time.Duration
	ResolverInterval        time.Duration
	ResolverBatch           int
	PublicSignatureEnabled  bool
	PublicSignatureEndpoint string
	PublicSignatureTimeout  time.Duration
	BackfillInterval        time.Duration
	BackfillBatchSize       int
	BackfillBatchIdle       time.Duration
	MaxCachedABIs           int
}

type TokenMetadataConfig struct {
	Enabled           bool
	BackfillEnabled   bool
	BackfillBatchSize int
	WorkerInterval    time.Duration
	BatchIdle         time.Duration
	RPCMaxRetries     int
	RPCWorkers        int
	RPCMinInterval    time.Duration
	LogoEnabled       bool
	LogoMaxBytes      int64
	LogoMaxWidth      int
	LogoMaxHeight     int
	// LogoMirrorBase / LogoMirrorHosts：某些图床（实测 assets.coingecko.com）在这台机器上
	// 出网被切断，直接抓会连超时；配置一个公网图片代理后，命中 MirrorHosts 的源站改走代理抓取。
	// logo_url 本身仍保存**原始事实 URL**，代理只是抓取通道（可随时关掉）。
	LogoMirrorBase  string
	LogoMirrorHosts []string
}

// RPCConfig V1.5 统一链 RPC 基础设施（§188/§189）。
// 所有阈值都可配置：Provider 价格、限额、能力不得硬编码为业务常量（§254）。
type RPCConfig struct {
	Enabled         bool
	DefaultProvider string
	Chains          []string
	ChainRegistry   string

	AnkrEnabled          bool
	AnkrEndpointTemplate string
	AnkrAPIKey           string

	HTTPMaxIdleConns        int
	HTTPMaxIdleConnsPerHost int
	HTTPMaxConnsPerHost     int

	GlobalConcurrency int
	RequestsPerSecond float64

	ChainHeadTimeout time.Duration
	StandardTimeout  time.Duration
	LogsTimeout      time.Duration
	TraceTimeout     time.Duration
	DebugTimeout     time.Duration

	ResponseHardLimitBytes int64
	MaxRequestBytes        int

	BatchEnabled bool
	BatchMaxSize int
	BatchMaxWait time.Duration

	CacheEnabled  bool
	CacheCapacity int

	TraceEnabled    bool
	DebugEnabled    bool
	WriteEnabled    bool
	TraceRawPersist bool

	ProbeOnStart    bool
	HealthSnapshot  time.Duration
	RetryMaxAttempt int

	BudgetEnabled            bool
	BudgetWindow             time.Duration
	TraceMaxRequests         int64
	InvestigationMaxRequests int64
	InvestigationMaxCredits  int64

	CreditsPerCall float64
	CostPerCallUSD float64
}

// ChainbaseConfig V1.2 Count Oracle Provider 配置（§12/§16/§69/§122）。
type ChainbaseConfig struct {
	Enabled                bool   // CHAINBASE_COUNT_ORACLE_ENABLED
	APIKey                 string // 只允许 env / secret store（§12），禁止进入前端/日志/源码/指标 labels
	Endpoint               string
	CountTimeout           time.Duration
	MaxRetries             int
	Workers                int // CHAINBASE_COUNT_WORKERS：独立于 Ankr Fetch Slots（§69）
	MaxConcurrency         int
	RequestsPerMinute      int
	ResponseHardLimitBytes int64
	ConnectTimeout         time.Duration
	TLSHandshakeTimeout    time.Duration
	BodyIdleTimeout        time.Duration
	CreditsPerRequest      float64
	CostPerRequestUSD      float64
	ProbeMaxRequests       int
	NearHeadWindowBlocks   int64
}

// CountOracleConfig V1.2 Count 行为开关（§16/§89/§95/§121）。
type CountOracleConfig struct {
	Enabled                   bool // COUNT_ORACLE_ENABLED（§121 别名）
	CountAwareShardingEnabled bool // COUNT_AWARE_SHARDING_ENABLED（false = Shadow Mode §89）
	CacheEnabled              bool
	SingleflightEnabled       bool
	CalibrationEnabled        bool
	ZeroVerifyEnabled         bool
	PlanningDeadline          time.Duration // COUNT_PLANNING_DEADLINE_MS：Count 不能拖慢下载（§16）
	CacheTTLHistoric          time.Duration // 历史固定区间长 TTL（§20）
	CacheTTLNearHead          time.Duration // 接近链头短 TTL（§20）
	LPTEnabled                bool          // MULTI_ADDRESS_LPT_ENABLED（§97：LPT 排序）
}

// CountProviderConfig V1.3 Provider Port 配置（§76/§84/§97）。
// 关键：Core 不写死 Chainbase 1 RPS —— Chainbase 低速率只是该 Provider 的 Runtime Profile（§26-§29/§73）。
type CountProviderConfig struct {
	// PortEnabled Count Provider Port 总开关（§97）
	PortEnabled bool // COUNT_PROVIDER_PORT_ENABLED
	// Shadow Port Shadow（§97：Port 计算，旧 Planner 仍做最终决策）
	Shadow bool // COUNT_PROVIDER_PORT_SHADOW
	// RouterEnabled 规则 Router（§93）
	RouterEnabled bool // COUNT_PROVIDER_ROUTER_ENABLED
	// GlobalMaxInflight 全局 Count 预算（§28/§97）
	GlobalMaxInflight int // COUNT_PROVIDER_GLOBAL_MAX_INFLIGHT
	// RecordAttempts 落 count_provider_attempts（§69）
	RecordAttempts bool // COUNT_PROVIDER_RECORD_ATTEMPTS

	ChainbaseEnabled        bool    // COUNT_PROVIDER_CHAINBASE_ENABLED
	ChainbaseTXEnabled      bool    // COUNT_PROVIDER_CHAINBASE_TX_ENABLED
	ChainbaseTokenEnabled   bool    // COUNT_PROVIDER_CHAINBASE_TOKEN_ENABLED（V1.3 实测 BLOCKED → 默认 false）
	ChainbaseMaxConcurrency int     // COUNT_PROVIDER_CHAINBASE_MAX_CONCURRENCY
	ChainbaseMaxRPS         float64 // COUNT_PROVIDER_CHAINBASE_MAX_RPS
	ChainbaseQueueCapacity  int     // COUNT_PROVIDER_CHAINBASE_QUEUE_CAPACITY
	ChainbaseRateCooldownMS int     // COUNT_PROVIDER_CHAINBASE_RATE_COOLDOWN_MS
	ChainbaseDisplayName    string  // COUNT_PROVIDER_CHAINBASE_DISPLAY_NAME

	MockParallelEnabled        bool    // COUNT_PROVIDER_MOCKPARALLEL_ENABLED
	MockParallelMaxConcurrency int     // COUNT_PROVIDER_MOCKPARALLEL_MAX_CONCURRENCY
	MockParallelLatencyMS      int     // COUNT_PROVIDER_MOCKPARALLEL_LATENCY_MS
	MockParallelQueueCapacity  int     // COUNT_PROVIDER_MOCKPARALLEL_QUEUE_CAPACITY
	MockParallelMaxRPS         float64 // COUNT_PROVIDER_MOCKPARALLEL_MAX_RPS（0=不限）
}

// GlobalConcurrencyConfig V1.2 600 全局并发（§33-§38/§98-§102/§121/§122）。
type GlobalConcurrencyConfig struct {
	MaxInflight                      int // ANKR_GLOBAL_MAX_INFLIGHT：全局硬 Ceiling（§33）
	AutoTuneEnabled                  bool
	FixedConcurrencyEnabled          bool // 正式 benchmark cell：固定 target，仍服从 backpressure hard gates
	LatencyNormalizationEnabled      bool
	ProfileLearnEnabled              bool // §42（P1）
	TransactionsRangeShardingEnabled bool // V1.2：transactions 多 Range 闸门（默认关）
	RampSteps                        []int
	RampSettleSamples                int
	GoodputMinGainPct                float64
	RollbackCooldown                 time.Duration
}

type Config struct {
	Env         string
	Timezone    string
	LogLevel    string
	HTTPPort    int
	DatabaseURL string
	// MigrationDatabaseURL 供迁移与初始化工具使用（需要 DDL 权限，通常是 owner 账号）。
	MigrationDatabaseURL string
	// DatabaseRuntimeRole 非空时，API/Worker/MCP 的每个数据库连接都会先 SET ROLE 到该角色，
	// 从而把运行期权限与迁移/运维权限分离（§27.6 AUDIT-003：应用 DB 角色拒绝危险操作）。
	DatabaseRuntimeRole string
	// LEGACY-CONSOLE-01：老控制台（"/" 与局域网入口上的那套前端）自身没有登录页，
	// 它只能借用 /admin/login 在"同一个域名"下签发的会话；换了 host（例如局域网 IP）
	// 就没有会话，而 2026-09-13 S7-SEC-LEGACY-V1 之后 /api/v1 的读路由一律要求已认证，
	// 于是老控制台看不到数据。下面四项给老线一个**自己的**服务凭据，由入口层注入，
	// 使两条线不再互相依赖：老前端代码不改，新线（会话/工作区/CSRF/提交门）也不受影响。
	// 哈希为空 = 整条路径关闭（fail closed）。
	LegacyConsoleTokenSHA256    string
	LegacyConsolePermissions    []string
	LegacyConsoleOrganizationID string
	LegacyConsoleWorkspaceID    string
	// IdentityID 是该服务主体的审计主体（audit_events_v2.actor_id）：老控制台的每一次
	// 请求都会以这个身份写审计，因此它必须是一个真实存在的 identities.id。留空 =
	// 整条路径关闭（不编造主体）。
	LegacyConsoleIdentityID string
	// Audience 决定该主体属于哪一类会话受众：user（默认）或 admin。后台权限键
	// （admin:*）只有 admin 受众才可能通过，因此配了 admin:* 却写成 user 会启动即关闭。
	LegacyConsoleAudience string
	ClickHouseURL         string
	MinioEndpoint         string
	MinioSecure           bool
	MinioAccessKey        string
	MinioSecretKey        string
	NatsURL               string
	RawBucket             string
	ExportBucket          string
	BackupsBucket         string
	// F-R10-BUCKET（P2）：导出产物历史上落在 RAW_BUCKET（rawstore 默认桶）。
	// ExportWriteBucket 决定**新写入**落在哪个桶："raw"（默认，保持既有行为）或 "export"。
	// ExportReadOrder 是读取回退顺序（逗号分隔），默认 "export,raw"：先找新桶再回退旧桶，
	// 从而在迁移/切写/回滚的任何阶段都能读到对象（双读兼容）。
	ExportWriteBucket string
	ExportReadOrder   string
	WorkerMetricsPort int
	Ankr              AnkrConfig
	Solana            SolanaConfig
	DRPC              DRPCConfig
	Tron              TronConfig
	Method            MethodConfig
	TokenMetadata     TokenMetadataConfig
	RPC               RPCConfig
	AnkrManagement    AnkrManagementConfig
	Auth              AuthConfig

	// V1.2（§12/§16/§69/§33/§121）：Chainbase Count Oracle 平面 + 全局并发。
	Chainbase   ChainbaseConfig
	CountOracle CountOracleConfig
	Global      GlobalConcurrencyConfig

	// V1.3：Count Provider Port（§2/§76/§84/§97）。
	CountProvider CountProviderConfig
}

// AuthConfig 是 §6.3 / §28.4 的认证与会话策略。
//
// 生产环境必须设置 AUTH_MODE=session，应用将不再接受任何客户端自报身份头。
type AuthConfig struct {
	Mode               string // session | hybrid | legacy
	CookieSecure       bool
	CookieSecureMode   string // auto / always / never（S7-SEC-01 R-05）
	CookiePrefix       string // 部署在 TLS 终止后可设置为 __Host-
	CookieDomain       string
	SessionIdleTTL     time.Duration
	SessionAbsoluteTTL time.Duration
	MaxFailedLogins    int
	LockDuration       time.Duration
	LoginRatePerMinute int
	MinPasswordLength  int
	RequireAdminMFA    bool
	TrustedProxies     []string
	BootstrapEmail     string
	OIDC               OIDCConfig
}

// OIDCConfig 只在完整配置且显式启用时生效；生产环境没有真实 IdP 时必须保持禁用。
type OIDCConfig struct {
	Enabled      bool
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

// SolanaConfig：Solana 走标准 JSON-RPC（Ankr Advanced API 明确拒绝 solana）。
//
// 默认复用 Ankr 的 Solana RPC 入口（https://rpc.ankr.com/solana/<API_KEY>）与同一把密钥；
// 也可以指向任意标准 Solana JSON-RPC（此时 APIKey 通常为空）。
type SolanaConfig struct {
	Enabled        bool
	Endpoint       string
	APIKey         string
	MaxPageSize    int
	Concurrency    int
	RequestTimeout time.Duration
}

// solanaEndpointFromEnv 推导 Solana RPC 端点：显式 SOLANA_RPC_ENDPOINT 优先，
// 否则取 ANKR_ENDPOINT 的 scheme://host 并替换路径为 /solana。
func solanaEndpointFromEnv() string {
	if explicit := strings.TrimSpace(os.Getenv("SOLANA_RPC_ENDPOINT")); explicit != "" {
		return explicit
	}
	base := env("ANKR_ENDPOINT", "https://rpc.ankr.com/multichain")
	scheme := "https://"
	rest := base
	if strings.HasPrefix(rest, "https://") {
		rest = strings.TrimPrefix(rest, "https://")
	} else if strings.HasPrefix(rest, "http://") {
		scheme = "http://"
		rest = strings.TrimPrefix(rest, "http://")
	}
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		rest = rest[:idx]
	}
	if rest == "" {
		return ""
	}
	return scheme + rest + "/solana"
}

// solanaConfigFromEnv 组装 Solana Provider 配置。
func solanaConfigFromEnv() SolanaConfig {
	endpoint := solanaEndpointFromEnv()
	key := env("SOLANA_API_KEY", os.Getenv("ANKR_API_KEY"))
	// 没有凭据的 Ankr 端点运行期必然 401：宁可不开，也不注册一个注定失败的 Provider。
	ready := endpoint != "" && (key != "" || !strings.Contains(endpoint, "rpc.ankr.com"))
	return SolanaConfig{
		Enabled:        envBool("SOLANA_ENABLED", true) && ready,
		Endpoint:       endpoint,
		APIKey:         key,
		MaxPageSize:    envInt("SOLANA_MAX_PAGE_SIZE", 20),
		Concurrency:    envInt("SOLANA_CONCURRENCY", 4),
		RequestTimeout: time.Duration(envInt("SOLANA_REQUEST_TIMEOUT_SECONDS", 30)) * time.Second,
	}
}

// DRPCConfig 是 dRPC（Solana Data API + Solana JSON-RPC）的配置。
//
// 与 Ankr 的关键差别：dRPC 的两条通道属于**同一个账户**，因此额度闸门（CU/余额/Key 状态）
// 在数据面共享，而并发与速率按通道分别解析（见 provider_performance_profiles）。
// 凭据只以受控引用形式出现（env:// 或 store://），配置里永远不出现明文 Key。
type DRPCConfig struct {
	Enabled bool
	// Tier 为 paid|free|unknown；unknown/free 时数据面按 DRPC_UNKNOWN_TIER_CU_PER_MINUTE 钳制。
	Tier string
	// BaseURL 形如 https://lb.drpc.live/solana —— 不含 API Key，Key 在请求时由 secret.Resolver 解析。
	BaseURL            string
	ManagementBaseURL  string
	RuntimeKeyRef      string
	StatisticsTokenRef string
	KeysTokenRef       string

	HistoryPageSize     int
	MaxTxVersion        int
	RPCTimeout          time.Duration
	DataTimeout         time.Duration
	SlotTimeSearchLimit int
	SlotTimeMargin      time.Duration

	UnknownTierCUPerMinute int64
	QuotaWarnPercent       int
	QuotaSafeOnlyPercent   int
	QuotaStopPercent       int
	BalanceWarn            string

	ManagementSyncInterval  time.Duration
	ManagementStaleAfter    time.Duration
	ManagementSafeOnlyAfter time.Duration

	// SecretsFile 是受管密钥文件（后台录入明文后写入此处，0600）。
	SecretsFile string
}

// drpcConfigFromEnv 组装 dRPC 配置。
//
// 默认保持关闭：Solana 当前仍由既有的 Ankr Solana JSON-RPC Provider 承载，
// 只有在显式开启并且凭据引用可解析时才切到 dRPC，避免把线上 Solana 下载打断。
func drpcConfigFromEnv() DRPCConfig {
	return DRPCConfig{
		Enabled:            envBool("DRPC_ENABLED", false),
		Tier:               strings.ToLower(env("DRPC_TIER", "unknown")),
		BaseURL:            strings.TrimRight(env("DRPC_BASE_URL", "https://lb.drpc.live/solana"), "/"),
		ManagementBaseURL:  strings.TrimRight(env("DRPC_MANAGEMENT_BASE_URL", "https://drpc.org"), "/"),
		RuntimeKeyRef:      env("DRPC_RUNTIME_KEY_REF", "store://drpc/runtime_api_key"),
		StatisticsTokenRef: env("DRPC_STATISTICS_TOKEN_REF", "store://drpc/statistics_api_token"),
		KeysTokenRef:       env("DRPC_KEYS_TOKEN_REF", "store://drpc/keys_api_token"),

		HistoryPageSize:     envInt("DRPC_SOLANA_HISTORY_PAGE_SIZE", 100),
		MaxTxVersion:        envInt("DRPC_SOLANA_MAX_TX_VERSION", 0),
		RPCTimeout:          time.Duration(envInt("DRPC_SOLANA_RPC_REQUEST_TIMEOUT_SECONDS", 30)) * time.Second,
		DataTimeout:         time.Duration(envInt("DRPC_SOLANA_DATA_REQUEST_TIMEOUT_SECONDS", 60)) * time.Second,
		SlotTimeSearchLimit: envInt("DRPC_SOLANA_SLOT_TIME_SEARCH_LIMIT", 64),
		SlotTimeMargin:      time.Duration(envInt("DRPC_SOLANA_SLOT_TIME_MARGIN_SECONDS", 120)) * time.Second,

		UnknownTierCUPerMinute: int64(envInt("DRPC_UNKNOWN_TIER_CU_PER_MINUTE", 45000)),
		QuotaWarnPercent:       envInt("DRPC_QUOTA_WARN_PERCENT", 80),
		QuotaSafeOnlyPercent:   envInt("DRPC_QUOTA_SAFE_ONLY_PERCENT", 95),
		QuotaStopPercent:       envInt("DRPC_QUOTA_STOP_PERCENT", 100),
		BalanceWarn:            os.Getenv("DRPC_BALANCE_WARN"),

		ManagementSyncInterval:  time.Duration(envInt("DRPC_MANAGEMENT_SYNC_INTERVAL_SECONDS", 300)) * time.Second,
		ManagementStaleAfter:    time.Duration(envInt("DRPC_MANAGEMENT_STALE_AFTER_SECONDS", 900)) * time.Second,
		ManagementSafeOnlyAfter: time.Duration(envInt("DRPC_MANAGEMENT_SAFE_ONLY_AFTER_SECONDS", 3600)) * time.Second,

		SecretsFile: strings.TrimSpace(os.Getenv("PROVIDER_SECRETS_FILE")),
	}
}

// ProviderSecretsFilePath 返回受管密钥文件的最终路径。
//
// 默认落在 $HOME/.blockchain-platform/provider-secrets.json：systemd 单元以该用户运行，
// 因此不需要额外写权限，也不会污染仓库目录。
func (c DRPCConfig) ProviderSecretsFilePath() string {
	if c.SecretsFile != "" {
		return c.SecretsFile
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "provider-secrets.json"
	}
	return filepath.Join(home, ".blockchain-platform", "provider-secrets.json")
}

// AnkrManagementConfig controls the account-scoped Management MCP connection.
// The RPC API key is intentionally not accepted here: Management MCP uses OAuth.
type AnkrManagementConfig struct {
	Enabled       bool
	MCPURL        string
	IssuerURL     string
	RedirectURI   string
	FrontendURL   string
	EncryptionKey string
	SyncTTL       time.Duration
}

// splitList 解析逗号分隔配置。
// defaultAuthMode 让生产环境默认使用最严格模式。
func defaultAuthMode(appEnv string) string {
	if appEnv == "production" {
		return "session"
	}
	return "hybrid"
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// parsePageLevels 解析 "1000,2000,5000,10000"。
// parseRampSteps 解析 "4,8,16,32,...,600"（§37/§99）；非法输入退化为默认序列。
func parseRampSteps(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return []int{4, 8, 16, 32, 64, 96, 128, 192, 256, 384, 512, 600}
	}
	return out
}

func parsePageLevels(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return []int{1000, 2000, 5000, 10000}
	}
	return out
}

// drpcAllowedHosts 是 dRPC Endpoint 的允许列表（开发文档 §16-7：防止管理员把 Endpoint
// 改成内网或本机地址造成 SSRF）。
var drpcAllowedHosts = map[string]bool{
	"lb.drpc.live": true,
	"drpc.org":     true,
}

// validateDRPC 校验 dRPC 配置。配置错误在启动期失败，而不是等到第一次下载。
func (c *Config) validateDRPC() error {
	cfg := c.DRPC
	switch cfg.Tier {
	case "paid", "free", "unknown":
	default:
		return fmt.Errorf("DRPC_TIER must be paid|free|unknown, got %q", cfg.Tier)
	}
	if cfg.BaseURL != "" {
		u, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return fmt.Errorf("DRPC_BASE_URL invalid: %w", err)
		}
		// 只允许 HTTPS：生产与测试都不接受明文传输密钥。
		if u.Scheme != "https" {
			return fmt.Errorf("DRPC_BASE_URL must use https, got %q", u.Scheme)
		}
		if !drpcAllowedHosts[strings.ToLower(u.Hostname())] {
			return fmt.Errorf("DRPC_BASE_URL host %q is not in the allowed list", u.Hostname())
		}
		// Endpoint 里不允许直接内嵌明文 Key（唯一合法形态是不带 key 的 base URL）。
		if strings.Contains(u.Path, "/lambda") {
			return fmt.Errorf("DRPC_BASE_URL must not include the /lambda path segment")
		}
	}
	if cfg.ManagementBaseURL != "" {
		u, err := url.Parse(cfg.ManagementBaseURL)
		if err != nil || u.Scheme != "https" || !drpcAllowedHosts[strings.ToLower(u.Hostname())] {
			return fmt.Errorf("DRPC_MANAGEMENT_BASE_URL must be https on an allowed dRPC host")
		}
	}
	if cfg.HistoryPageSize <= 0 || cfg.HistoryPageSize > 1000 {
		return fmt.Errorf("DRPC_SOLANA_HISTORY_PAGE_SIZE must be within 1..1000")
	}
	if cfg.MaxTxVersion < 0 {
		return fmt.Errorf("DRPC_SOLANA_MAX_TX_VERSION must not be negative")
	}
	if cfg.RPCTimeout <= 0 || cfg.DataTimeout <= 0 {
		return fmt.Errorf("dRPC request timeouts must be positive")
	}
	if cfg.SlotTimeSearchLimit <= 0 {
		return fmt.Errorf("DRPC_SOLANA_SLOT_TIME_SEARCH_LIMIT must be positive")
	}
	if cfg.UnknownTierCUPerMinute <= 0 {
		return fmt.Errorf("DRPC_UNKNOWN_TIER_CU_PER_MINUTE must be positive")
	}
	if !(0 < cfg.QuotaWarnPercent && cfg.QuotaWarnPercent <= cfg.QuotaSafeOnlyPercent && cfg.QuotaSafeOnlyPercent <= cfg.QuotaStopPercent) {
		return fmt.Errorf("DRPC_QUOTA_*_PERCENT must satisfy 0 < warn <= safe_only <= stop")
	}
	if cfg.ManagementSyncInterval < time.Minute {
		return fmt.Errorf("DRPC_MANAGEMENT_SYNC_INTERVAL_SECONDS must be at least 60 (official management API limit is 10/min)")
	}
	if cfg.ManagementStaleAfter < cfg.ManagementSyncInterval {
		return fmt.Errorf("DRPC_MANAGEMENT_STALE_AFTER_SECONDS must be >= the sync interval")
	}
	// 生产环境开启 dRPC 但层级未知/免费时**不拒绝启动**：层级只有在后台录入凭据并完成一次
	// 管理同步之后才能确定，启动期硬失败会把"先部署、后补 Key"的正常上线路堵死。
	// 数据面按 DRPC_UNKNOWN_TIER_CU_PER_MINUTE 钳制（开发文档 §3.4/§6.2），
	// 并由 Provider 页面显式标注 degraded_reason=cu_cap_unknown_tier。
	// 发布验收仍要求 DRPC_TIER=paid（见开发文档 §12），但那是验收条件而非启动条件。
	// Solana 显式指定 dRPC 时，dRPC 必须真的开启；否则运行期会静默回落到旧 Provider。
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SOLANA_PROVIDER")), "drpc") && !cfg.Enabled {
		return fmt.Errorf("SOLANA_PROVIDER=drpc requires DRPC_ENABLED=true")
	}
	return nil
}

// Validate 启动校验（§32：错误配置直接启动失败）。
func (c *Config) Validate() error {
	if err := c.validateDRPC(); err != nil {
		return err
	}
	// [FIX 2026-09-13 / P0-密钥] 凭据必须由环境提供：缺失即快速失败。
	// 历史实现在编译期内置了与生产同值的默认凭据（PG/CH/MinIO 各一组，值已按 §23.8 于 2026-09-13 轮换，
	// 明文不再出现在任何文件），导致发行二进制内嵌可用凭据（§23.8 FAIL），且生产从未轮换默认值。
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return fmt.Errorf("DATABASE_URL is required (credential defaults are no longer compiled in)")
	}
	if strings.TrimSpace(c.MigrationDatabaseURL) == "" {
		return fmt.Errorf("MIGRATION_DATABASE_URL or DATABASE_URL is required")
	}
	if strings.TrimSpace(c.ClickHouseURL) == "" {
		return fmt.Errorf("CLICKHOUSE_URL is required (credential defaults are no longer compiled in)")
	}
	if strings.TrimSpace(c.MinioSecretKey) == "" {
		return fmt.Errorf("MINIO_ROOT_PASSWORD is required (credential defaults are no longer compiled in)")
	}
	// F-R10-BUCKET / ADR-019：导出写桶、读顺序与两个桶名必须是显式合法值。
	// 非法值若被静默接受，会造成“以为切了写桶其实还在写旧桶”“以为在双读其实只在读旧桶”
	// 这类伪证据，因此一律启动失败（fail closed），绝不回落默认值。
	if raw := strings.TrimSpace(c.RawBucket); raw == "" {
		return fmt.Errorf("RAW_BUCKET is required")
	}
	if exportBucket := strings.TrimSpace(c.ExportBucket); exportBucket == "" {
		return fmt.Errorf("EXPORT_BUCKET is required")
	}
	if strings.TrimSpace(c.ExportBucket) == strings.TrimSpace(c.RawBucket) {
		return fmt.Errorf("EXPORT_BUCKET must differ from RAW_BUCKET (both are %q)", strings.TrimSpace(c.RawBucket))
	}
	// 只接受规范字面量：区分大小写，且**不允许前后空白**。大小写变体或带空格的取值语义虽可
	// 推断，但会让部署证据里出现与规范不同的字符串，审计时无法一眼确认，因此一律拒绝。
	switch c.ExportWriteBucket {
	case "raw", "export":
	default:
		return fmt.Errorf("EXPORT_WRITE_BUCKET must be raw or export (got %q)", c.ExportWriteBucket)
	}
	// 读顺序必须显式包含两个桶（顺序可变），未知项、重复项与带空格的项均为配置错误：
	// 只配一个桶会让迁移/回滚期间的对象读不到，必须让它在启动期就暴露。
	seen := map[string]bool{}
	for _, part := range strings.Split(c.ExportReadOrder, ",") {
		token := part
		switch token {
		case "raw", "export":
		default:
			return fmt.Errorf("EXPORT_READ_ORDER entries must be raw or export (got %q)", part)
		}
		if seen[token] {
			return fmt.Errorf("EXPORT_READ_ORDER contains duplicate %q", token)
		}
		seen[token] = true
	}
	if !seen["raw"] || !seen["export"] {
		return fmt.Errorf("EXPORT_READ_ORDER must contain both raw and export (got %q)", c.ExportReadOrder)
	}

	a := &c.Ankr
	if c.Tron.Enabled {
		vg := c.Tron.VolumeGuard
		if vg.Enabled && (vg.WarnRows <= 0 || vg.HardActualRows <= 0 || vg.HardProjectedRows <= vg.WarnRows ||
			vg.ExtremeRows <= vg.HardProjectedRows || vg.SuperExtremeRows <= vg.ExtremeRows ||
			vg.ProjectedConfidence <= 0 || vg.ProjectedConfidence > 1 || vg.ExtremeConfidence <= 0 || vg.ExtremeConfidence > 1 ||
			vg.PreflightMaxRequests < 1 || vg.PreflightMaxRequests > 32 || vg.PreflightTimeout <= 0) {
			return fmt.Errorf("TRON volume guard thresholds, confidence, preflight budget, and timeout are invalid")
		}
		defaultProvider := strings.ToLower(strings.TrimSpace(c.Tron.DefaultProvider))
		if defaultProvider == "" {
			defaultProvider = strings.ToLower(strings.TrimSpace(c.Tron.Provider))
		}
		if defaultProvider == "" {
			defaultProvider = "ankr"
		}
		if defaultProvider != "ankr" && defaultProvider != "trongrid" {
			return fmt.Errorf("TRON_DEFAULT_PROVIDER must be ankr or trongrid")
		}
		if strings.EqualFold(strings.TrimSpace(c.Env), "production") && strings.ToLower(strings.TrimSpace(c.Tron.TransactionsProvider)) != "trongrid" {
			return fmt.Errorf("TRON_TRANSACTIONS_PROVIDER=trongrid is required in production")
		}
		providers := map[string]bool{defaultProvider: true}
		for _, dataset := range []string{"transactions", "token_transfers", "internal_transactions"} {
			p := c.Tron.ProviderForDataset(dataset)
			if p != "ankr" && p != "trongrid" {
				return fmt.Errorf("TRON provider for %s must be ankr or trongrid", dataset)
			}
			providers[p] = true
		}
		if c.Tron.TRC20AnkrFallbackEnabled {
			if strings.ToLower(strings.TrimSpace(c.Tron.TRC20FallbackProvider)) != "ankr" {
				return fmt.Errorf("TRON_TRC20_FALLBACK_PROVIDER must be ankr when fallback is enabled")
			}
			providers["ankr"] = true
			if c.Tron.TRC20FallbackMaxWindows < 1 {
				return fmt.Errorf("TRON_TRC20_FALLBACK_MAX_WINDOWS must be positive")
			}
		}
		for _, capability := range []string{"receipt", "tx_detail"} {
			p := c.Tron.ProviderForCapability(capability)
			if p != "ankr" {
				return fmt.Errorf("TRON %s provider must be ankr", capability)
			}
			providers[p] = true
		}
		if c.Tron.ProviderForDataset("token_transfers") != "trongrid" || c.Tron.ProviderForDataset("internal_transactions") != "ankr" {
			return fmt.Errorf("TRON TRC20 primary must be trongrid and internal provider must be ankr")
		}
		if c.Tron.ReceiptCacheMaxEntries < 1 || c.Tron.ReceiptCacheTTL <= 0 {
			return fmt.Errorf("TRON receipt cache must be bounded and have a positive TTL")
		}
		if c.Tron.ReceiptInitialConcurrency < 1 || c.Tron.ReceiptMaxConcurrency < c.Tron.ReceiptInitialConcurrency || c.Tron.ReceiptAdaptiveHardMax < c.Tron.ReceiptMaxConcurrency {
			return fmt.Errorf("TRON receipt concurrency must be positive and max must be >= initial")
		}
		if c.Tron.TRC20LogEnrichmentRatio <= 0 || c.Tron.TRC20LogEnrichmentRatio > 1 || c.Tron.TRC20EnrichLogMaxWindows < 1 || c.Tron.TRC20EnrichLogSpan < 1 {
			return fmt.Errorf("TRON adaptive enrichment settings are invalid")
		}
		if c.Tron.TRC20PrefetchPages < 1 || c.Tron.TRC20PrefetchPages > 16 ||
			c.Tron.TRC20PageQueueMax < c.Tron.TRC20PrefetchPages || c.Tron.TRC20PageQueueMax > 2048 {
			return fmt.Errorf("TRON TRC20 prefetch queue settings are invalid")
		}
		if providers["ankr"] && strings.TrimSpace(c.Ankr.APIKey) == "" {
			return fmt.Errorf("ANKR_API_KEY is required for TRON datasets using Ankr")
		}
		if providers["trongrid"] {
			if !c.Tron.TronGridEnabled {
				return fmt.Errorf("TRONGRID_ENABLED=true is required for TRON datasets using TronGrid")
			}
			if c.Tron.BaseURL == "" || strings.TrimSpace(c.Tron.APIKey) == "" {
				return fmt.Errorf("TRONGRID_BASE_URL and TRONGRID_API_KEY are required for TRON datasets using TronGrid")
			}
			validPool := func(pageSize, initial, normal, maximum int, timeout time.Duration, retries int) bool {
				return pageSize >= 1 && pageSize <= 200 && initial >= 1 && initial <= normal && normal <= maximum && maximum <= 32 && timeout > 0 && retries >= 0
			}
			if !validPool(c.Tron.TronGridTXPageSize, c.Tron.TronGridTXInitialConcurrency, c.Tron.TronGridTXNormalConcurrency, c.Tron.TronGridTXMaxConcurrency, c.Tron.TronGridTXRequestTimeout, c.Tron.TronGridTXMaxRetries) ||
				!validPool(c.Tron.TronGridTRC20PageSize, c.Tron.TronGridTRC20InitialConcurrency, c.Tron.TronGridTRC20NormalConcurrency, c.Tron.TronGridTRC20MaxConcurrency, c.Tron.TronGridTRC20RequestTimeout, c.Tron.TronGridTRC20MaxRetries) {
				return fmt.Errorf("TronGrid settings must satisfy page_size 1..200 and 1 <= initial <= normal <= max <= 32")
			}
			if c.Tron.TronGridRequestTimeout <= 0 || c.Tron.TronGridMinRequestInterval <= 0 || c.Tron.TronGridMaxRetries < 0 || c.Tron.TronGridDailyRequestBudget < 1 || c.Tron.TronGridDailyRequestBudget > 100000 {
				return fmt.Errorf("TronGrid timeout/retry/daily budget settings are invalid")
			}
		}
		if providers["ankr"] {
			if c.Tron.InitialConcurrency < 1 || c.Tron.MaxConcurrency < c.Tron.InitialConcurrency {
				return fmt.Errorf("TRON Ankr concurrency must satisfy 1 <= initial <= max")
			}
			if c.Tron.AnkrConcurrency < 1 || c.Tron.LogMinSpan < 1 || c.Tron.LogInitialSpan < c.Tron.LogMinSpan || c.Tron.LogMaxSpan < c.Tron.LogInitialSpan || c.Tron.LogMaxSpan > 5000 || c.Tron.BlockSpan < 1 || c.Tron.ReceiptSpan < 1 || c.Tron.MaxScanBlocks < 1 || c.Tron.MaxTxAutoScan < 1 || c.Tron.MaxInternalAutoScan < 1 || c.Tron.TimeBlockCacheTTL <= 0 || c.Tron.TimeBlockProbeRetry < 0 {
				return fmt.Errorf("TRON Ankr concurrency/span settings are invalid")
			}
			if c.Tron.RequestTimeout <= 0 || c.Tron.ConnectTimeout <= 0 || c.Tron.MaxRetries < 0 {
				return fmt.Errorf("TRON timeout/retry settings are invalid")
			}
		}
	}
	if a.PerformanceModesEnabled {
		if a.ProdHardCeiling < 1 || a.ProdHardCeiling > 64 {
			return fmt.Errorf("ANKR_PROD_HARD_CEILING must be between 1 and 64")
		}
		if a.ProdSafeConcurrency < 1 || a.ProdSafeConcurrency > a.ProdFastConcurrency || a.ProdFastConcurrency > a.ProdTurboConcurrency || a.ProdTurboConcurrency > a.ProdHardCeiling {
			return fmt.Errorf("Ankr production modes must be ordered SAFE <= FAST <= TURBO <= hard ceiling")
		}
		if a.ProdRejectBoundary <= a.ProdHardCeiling {
			return fmt.Errorf("ANKR_PROD_REJECT_BOUNDARY must exceed the production hard ceiling")
		}
		if a.InitialConcurrency > a.ProdHardCeiling || a.MaxConcurrency > a.ProdHardCeiling || c.Global.MaxInflight > a.ProdHardCeiling {
			return fmt.Errorf("Ankr production concurrency cannot exceed hard ceiling")
		}
	}
	if a.ResponseHardLimitBytes <= 0 || a.ResponseSoftLimitBytes <= 0 {
		return fmt.Errorf("ankr response limits must be positive")
	}
	if a.ResponseSoftLimitBytes >= a.ResponseHardLimitBytes {
		return fmt.Errorf("ankr response soft limit must be < hard limit")
	}
	if a.BodyIdleTimeout <= 0 || a.ConnectTimeout <= 0 {
		return fmt.Errorf("ankr connect/body-idle timeouts must be positive")
	}
	if a.SpoolDir == "" || a.SpoolMaxBytes <= 0 {
		return fmt.Errorf("ankr spool dir and max bytes must be set")
	}
	if a.ParserSlots <= 0 || a.CHWriterSlots <= 0 || a.CHBatchMaxRows <= 0 || a.CHBatchMaxBytes <= 0 {
		return fmt.Errorf("ankr pipeline slots and batch limits must be positive")
	}
	if a.FetchMemoryBudgetBytes <= 0 {
		return fmt.Errorf("ankr fetch memory budget must be positive")
	}
	if a.SystemMemoryGuardEnabled {
		if a.WorkerRSSHardMaxBytes <= 0 || a.VMMemAvailableWarnBytes <= 0 || a.VMMemAvailableProtectBytes <= 0 {
			return fmt.Errorf("ankr system memory guard thresholds must be positive")
		}
		if a.VMMemAvailableProtectBytes >= a.VMMemAvailableWarnBytes {
			return fmt.Errorf("ANKR_VM_MEM_AVAILABLE_PROTECT_MB must be below ANKR_VM_MEM_AVAILABLE_WARN_MB")
		}
		if a.SwapGrowthSamples < 2 {
			return fmt.Errorf("ANKR_SWAP_GROWTH_SAMPLES must be >= 2")
		}
	}
	if len(a.PageLevels) < 2 {
		return fmt.Errorf("ankr page levels need at least 2 levels")
	}
	for i := 1; i < len(a.PageLevels); i++ {
		if a.PageLevels[i] <= a.PageLevels[i-1] {
			return fmt.Errorf("ankr page levels must be strictly increasing")
		}
	}
	if a.PageLevels[len(a.PageLevels)-1] > 10000 {
		return fmt.Errorf("ankr max page size must be <= 10000")
	}
	if a.PageLevels[0] <= 0 {
		return fmt.Errorf("ankr min page size must be > 0")
	}
	if a.MaxPageSize > 10000 {
		return fmt.Errorf("ANKR_MAX_PAGE_SIZE must be <= 10000")
	}
	if c.RPC.Enabled {
		if c.RPC.ResponseHardLimitBytes <= 0 {
			return fmt.Errorf("RPC_RESPONSE_HARD_LIMIT_MB must be positive")
		}
		if c.RPC.GlobalConcurrency <= 0 {
			return fmt.Errorf("RPC_GLOBAL_CONCURRENCY must be positive")
		}
		if c.RPC.BatchMaxSize <= 0 {
			return fmt.Errorf("RPC_BATCH_MAX_SIZE must be positive")
		}
		if len(c.RPC.Chains) == 0 {
			return fmt.Errorf("RPC_CHAINS must list at least one chain")
		}
	}
	if c.AnkrManagement.Enabled && len(c.AnkrManagement.EncryptionKey) < 32 {
		return fmt.Errorf("ANKR_MANAGEMENT_ENCRYPTION_KEY must contain at least 32 characters when management integration is enabled")
	}
	if c.CountOracle.Enabled {
		if c.Chainbase.CountTimeout <= 0 || c.Chainbase.MaxRetries < 0 || c.Chainbase.Workers <= 0 {
			return fmt.Errorf("chainbase count timeout / retries / workers must be valid when count oracle is enabled")
		}
		if c.Chainbase.ResponseHardLimitBytes <= 0 {
			return fmt.Errorf("CHAINBASE_RESPONSE_HARD_LIMIT_MB must be positive when count oracle is enabled")
		}
	}
	if c.Global.MaxInflight <= 0 {
		return fmt.Errorf("ANKR_GLOBAL_MAX_INFLIGHT must be positive")
	}
	if c.Global.AutoTuneEnabled {
		if len(c.Global.RampSteps) < 2 || c.Global.RampSteps[0] <= 0 {
			return fmt.Errorf("ANKR_CONCURRENCY_RAMP_STEPS must be an increasing positive list")
		}
		for i := 1; i < len(c.Global.RampSteps); i++ {
			if c.Global.RampSteps[i] <= c.Global.RampSteps[i-1] {
				return fmt.Errorf("ANKR_CONCURRENCY_RAMP_STEPS must be strictly increasing")
			}
		}
		if c.Global.RampSteps[len(c.Global.RampSteps)-1] > c.Global.MaxInflight {
			return fmt.Errorf("ramp top step must not exceed ANKR_GLOBAL_MAX_INFLIGHT")
		}
		if c.Global.RampSettleSamples <= 0 {
			return fmt.Errorf("ANKR_RAMP_SETTLE_SAMPLES must be positive")
		}
	}
	return nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// envBoolStrict 与 envBool 相同，但把 "auto" 视为"未设置"，回落到默认值。
// 用于 AUTH_COOKIE_SECURE 这类支持三态的开关（auto/true/false）。
func envBoolStrict(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" && !strings.EqualFold(v, "auto") {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// cookieSecureMode 把 AUTH_COOKIE_SECURE 归一为 auto / always / never。
//
// 生产默认策略（S7-SEC-01 R-05）：APP_ENV=production 且未显式配置时使用 auto ——
// HTTPS 入口（Cloudflare/Nginx TLS 终止，X-Forwarded-Proto=https）写 Secure + __Host-；
// 非生产环境默认 auto 亦如此，本地 HTTP 调试仍然可用。
// 显式 AUTH_COOKIE_SECURE=false 会被接受，但生产环境应视为配置错误（启动日志会告警）。
func cookieSecureMode(raw, appEnv string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return "always"
	case "false", "0", "no", "off":
		return "never"
	case "auto", "":
		return "auto"
	default:
		return "auto"
	}
}

func parseDurationEnv(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	// Accept a plain integer as seconds for deployment systems that only
	// support scalar values, while retaining the documented "15s" form.
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func Load() *Config {
	return &Config{
		Env:      env("APP_ENV", "development"),
		Timezone: env("APP_TIMEZONE", "Asia/Bangkok"),
		LogLevel: env("API_LOG_LEVEL", "info"),
		HTTPPort: envInt("API_HTTP_PORT", 8080),
		// DATABASE_URL 是**应用运行账号**（api/worker）：S7 起应为非超级用户的受限登录角色，
		// 连接后再 SET ROLE 到 DB_RUNTIME_ROLE（ADR-017 §5）。
		// [FIX 2026-09-13 / P0-密钥] 不再编译期内置凭据默认值：缺 DATABASE_URL 时必须快速失败
		// （历史默认值与生产同值并被编译进发行二进制 → §23.8 FAIL）。
		DatabaseURL: env("DATABASE_URL", ""),
		// MigrationDatabaseURL 是**迁移/运维账号**（cmd/migrate、admin-init、admin-fixture）：
		// 需要 DDL 权限，因此仍使用 owner；未配置时回落 DATABASE_URL（向后兼容）。
		MigrationDatabaseURL: env("MIGRATION_DATABASE_URL", env("DATABASE_URL", "")),
		DatabaseRuntimeRole:  env("DB_RUNTIME_ROLE", ""),
		// LEGACY-CONSOLE-01：入口层注入的服务凭据（SHA-256 十六进制）+ 它被授予的
		// 权限键 + 它所属的组织/工作区。任何一项缺失都会让这条路径保持关闭。
		LegacyConsoleTokenSHA256:    env("LEGACY_CONSOLE_TOKEN_SHA256", ""),
		LegacyConsolePermissions:    splitList(env("LEGACY_CONSOLE_PERMISSIONS", "")),
		LegacyConsoleOrganizationID: env("LEGACY_CONSOLE_ORGANIZATION_ID", ""),
		LegacyConsoleWorkspaceID:    env("LEGACY_CONSOLE_WORKSPACE_ID", ""),
		LegacyConsoleIdentityID:     env("LEGACY_CONSOLE_IDENTITY_ID", ""),
		LegacyConsoleAudience:       env("LEGACY_CONSOLE_AUDIENCE", "user"),
		ClickHouseURL:               env("CLICKHOUSE_URL", ""),
		MinioEndpoint:               env("MINIO_ENDPOINT", "127.0.0.1:9001"),
		MinioSecure:                 envBool("MINIO_SECURE", false),
		MinioAccessKey:              env("MINIO_ROOT_USER", "blockchain_admin"),
		MinioSecretKey:              env("MINIO_ROOT_PASSWORD", ""),
		NatsURL:                     env("NATS_URL", "nats://127.0.0.1:4222"),
		RawBucket:                   env("RAW_BUCKET", "blockchain-raw"),
		ExportBucket:                env("EXPORT_BUCKET", "blockchain-exports"),
		ExportWriteBucket:           env("EXPORT_WRITE_BUCKET", "raw"),
		ExportReadOrder:             env("EXPORT_READ_ORDER", "export,raw"),
		BackupsBucket:               env("BACKUPS_BUCKET", "blockchain-backups"),
		WorkerMetricsPort:           envInt("WORKER_METRICS_PORT", 8081),
		Auth: AuthConfig{
			// 未配置时按环境决定：production 强制 session，其它环境 hybrid 便于平滑迁移。
			Mode: env("AUTH_MODE", defaultAuthMode(env("APP_ENV", "development"))),
			// AUTH_COOKIE_SECURE 支持三态（S7-SEC-01 R-05）：
			//   auto（默认）—— 按入口协议：HTTPS 入口才写 Secure + __Host- 前缀；
			//   true / false —— 固定行为（false 仅限本机 HTTP 调试，生产禁止）。
			CookieSecure:       envBoolStrict("AUTH_COOKIE_SECURE", env("APP_ENV", "development") == "production"),
			CookieSecureMode:   cookieSecureMode(env("AUTH_COOKIE_SECURE", "auto"), env("APP_ENV", "development")),
			CookiePrefix:       env("AUTH_COOKIE_PREFIX", ""),
			CookieDomain:       env("AUTH_COOKIE_DOMAIN", ""),
			SessionIdleTTL:     time.Duration(envInt("AUTH_SESSION_IDLE_TTL_SECONDS", 43200)) * time.Second,
			SessionAbsoluteTTL: time.Duration(envInt("AUTH_SESSION_ABSOLUTE_TTL_SECONDS", 604800)) * time.Second,
			MaxFailedLogins:    envInt("AUTH_MAX_FAILED_LOGINS", 10),
			LockDuration:       time.Duration(envInt("AUTH_LOCK_DURATION_SECONDS", 900)) * time.Second,
			LoginRatePerMinute: envInt("AUTH_LOGIN_RATE_PER_MINUTE", 20),
			MinPasswordLength:  envInt("AUTH_MIN_PASSWORD_LENGTH", 12),
			RequireAdminMFA:    envBool("AUTH_REQUIRE_ADMIN_MFA", false),
			TrustedProxies:     splitList(env("AUTH_TRUSTED_PROXIES", "127.0.0.1/32,::1/128")),
			BootstrapEmail:     env("AUTH_BOOTSTRAP_EMAIL", ""),
			OIDC: OIDCConfig{
				Enabled:      envBool("AUTH_OIDC_ENABLED", false),
				Issuer:       strings.TrimSpace(env("AUTH_OIDC_ISSUER", "")),
				ClientID:     strings.TrimSpace(env("AUTH_OIDC_CLIENT_ID", "")),
				ClientSecret: strings.TrimSpace(env("AUTH_OIDC_CLIENT_SECRET", "")),
				RedirectURL:  strings.TrimSpace(env("AUTH_OIDC_REDIRECT_URL", "")),
				Scopes:       splitList(env("AUTH_OIDC_SCOPES", "openid,email,profile")),
			},
		},
		Tron: TronConfig{
			Enabled: envBool("TRON_ENABLED", false),
			VolumeGuard: VolumeGuardConfig{
				Enabled: envBool("TRON_VOLUME_GUARD_ENABLED", true),
				// 2026-09-15 产品决定：单地址单 dataset 超过 100 万行就停下来等人工确认
				// （"超过百万的地址就不下载了"）。人工确认门槛由 500 万降到 100 万，
				// WarnRows 相应降到 50 万以保持阈值阶梯单调。
				WarnRows:             int64(envInt("TRON_VOLUME_WARN_ROWS", 500000)),
				HardActualRows:       int64(envInt("TRON_VOLUME_HARD_ACTUAL_ROWS", 1000000)),
				HardProjectedRows:    int64(envInt("TRON_VOLUME_HARD_PROJECTED_ROWS", 1000000)),
				ExtremeRows:          int64(envInt("TRON_VOLUME_EXTREME_ROWS", 10000000)),
				SuperExtremeRows:     int64(envInt("TRON_VOLUME_SUPER_EXTREME_ROWS", 50000000)),
				ProjectedConfidence:  envFloat("TRON_VOLUME_PROJECTED_CONFIDENCE", 0.80),
				ExtremeConfidence:    envFloat("TRON_VOLUME_EXTREME_CONFIDENCE", 0.70),
				PreflightMaxRequests: envInt("TRON_VOLUME_PREFLIGHT_MAX_REQUESTS", 32),
				PreflightTimeout:     time.Duration(envInt("TRON_VOLUME_PREFLIGHT_TIMEOUT_SECONDS", 5)) * time.Second,
			},
			Provider: strings.ToLower(env("TRON_PROVIDER", "ankr")),
			// V3 deliberately does not inherit TRON_PROVIDER=trongrid as a
			// global switch. The legacy variable remains a compatibility hint for
			// Ankr deployments; dataset routing is explicit below.
			DefaultProvider:                 strings.ToLower(env("TRON_DEFAULT_PROVIDER", "ankr")),
			TransactionsProvider:            strings.ToLower(strings.TrimSpace(os.Getenv("TRON_TRANSACTIONS_PROVIDER"))),
			TRC20Provider:                   strings.ToLower(strings.TrimSpace(os.Getenv("TRON_TRC20_PROVIDER"))),
			TRC20PrimaryProvider:            strings.ToLower(env("TRON_TRC20_PRIMARY_PROVIDER", "trongrid")),
			TRC20FallbackProvider:           strings.ToLower(env("TRON_TRC20_FALLBACK_PROVIDER", "ankr")),
			TRC20AnkrFallbackEnabled:        envBool("TRON_TRC20_ANKR_FALLBACK_ENABLED", true),
			TRC20FallbackMaxWindows:         uint64(envInt("TRON_TRC20_FALLBACK_MAX_WINDOWS", 5000)),
			InternalProvider:                strings.ToLower(strings.TrimSpace(os.Getenv("TRON_INTERNAL_PROVIDER"))),
			ReceiptProvider:                 strings.ToLower(env("TRON_RECEIPT_PROVIDER", "ankr")),
			TxDetailProvider:                strings.ToLower(env("TRON_TX_DETAIL_PROVIDER", "ankr")),
			TronGridEnabled:                 envBool("TRONGRID_ENABLED", false),
			BaseURL:                         env("TRONGRID_BASE_URL", "https://api.trongrid.io"),
			APIKey:                          strings.TrimSpace(os.Getenv("TRONGRID_API_KEY")),
			TronGridPageSize:                envInt("TRONGRID_TRC20_PAGE_SIZE", envInt("TRONGRID_TX_PAGE_SIZE", 200)),
			TronGridInitialConcurrency:      envInt("TRONGRID_TRC20_INITIAL_CONCURRENCY", envInt("TRONGRID_TX_INITIAL_CONCURRENCY", 4)),
			TronGridNormalConcurrency:       envInt("TRONGRID_TRC20_NORMAL_CONCURRENCY", envInt("TRONGRID_TX_NORMAL_CONCURRENCY", 6)),
			TronGridMaxConcurrency:          envInt("TRONGRID_TRC20_MAX_CONCURRENCY", envInt("TRONGRID_TX_MAX_CONCURRENCY", 8)),
			TronGridRequestTimeout:          parseDurationEnv("TRONGRID_TRC20_REQUEST_TIMEOUT", parseDurationEnv("TRONGRID_TX_REQUEST_TIMEOUT", 15*time.Second)),
			TronGridMinRequestInterval:      time.Duration(envInt("TRONGRID_MIN_REQUEST_INTERVAL_MS", 100)) * time.Millisecond,
			TronGridMaxRetries:              envInt("TRONGRID_TRC20_MAX_RETRIES", envInt("TRONGRID_TX_MAX_RETRIES", 3)),
			TronGridDailyRequestBudget:      int64(envInt("TRONGRID_DAILY_REQUEST_BUDGET", 80000)),
			TronGridTXPageSize:              envInt("TRONGRID_TX_PAGE_SIZE", 200),
			TronGridTXInitialConcurrency:    envInt("TRONGRID_TX_INITIAL_CONCURRENCY", 4),
			TronGridTXNormalConcurrency:     envInt("TRONGRID_TX_NORMAL_CONCURRENCY", 6),
			TronGridTXMaxConcurrency:        envInt("TRONGRID_TX_MAX_CONCURRENCY", 8),
			TronGridTXRequestTimeout:        parseDurationEnv("TRONGRID_TX_REQUEST_TIMEOUT", 15*time.Second),
			TronGridTXMaxRetries:            envInt("TRONGRID_TX_MAX_RETRIES", 3),
			TronGridTRC20PageSize:           envInt("TRONGRID_TRC20_PAGE_SIZE", 200),
			TronGridTRC20InitialConcurrency: envInt("TRONGRID_TRC20_INITIAL_CONCURRENCY", 4),
			TronGridTRC20NormalConcurrency:  envInt("TRONGRID_TRC20_NORMAL_CONCURRENCY", 6),
			TronGridTRC20MaxConcurrency:     envInt("TRONGRID_TRC20_MAX_CONCURRENCY", 8),
			TronGridTRC20RequestTimeout:     parseDurationEnv("TRONGRID_TRC20_REQUEST_TIMEOUT", 15*time.Second),
			TronGridTRC20MaxRetries:         envInt("TRONGRID_TRC20_MAX_RETRIES", 3),
			InitialConcurrency:              envInt("TRON_ANKR_INITIAL_CONCURRENCY", envInt("TRON_INITIAL_CONCURRENCY", 2)),
			MaxConcurrency:                  envInt("TRON_ANKR_MAX_CONCURRENCY", envInt("TRON_MAX_CONCURRENCY", 4)),
			RequestTimeout:                  time.Duration(envInt("TRON_REQUEST_TIMEOUT_SECONDS", 180)) * time.Second,
			ConnectTimeout:                  time.Duration(envInt("TRON_CONNECT_TIMEOUT_SECONDS", 5)) * time.Second,
			MaxRetries:                      envInt("TRON_MAX_RETRIES", 4),
			IncrementalOverlap:              time.Duration(envInt("TRON_INCREMENTAL_OVERLAP_SECONDS", 180)) * time.Second,
			AnkrConcurrency:                 envInt("TRON_ANKR_CONCURRENCY", 2),
			LogInitialSpan:                  uint64(envInt("TRON_ANKR_LOG_INITIAL_SPAN", 5000)),
			LogMinSpan:                      uint64(envInt("TRON_ANKR_LOG_MIN_SPAN", 10)),
			LogMaxSpan:                      uint64(envInt("TRON_LOG_MAX_SPAN", envInt("TRON_ANKR_LOG_MAX_SPAN", 5000))),
			BlockSpan:                       uint64(envInt("TRON_ANKR_BLOCK_SPAN", 1)),
			ReceiptSpan:                     uint64(envInt("TRON_ANKR_RECEIPT_SPAN", 4)),
			MaxScanBlocks:                   uint64(envInt("TRON_ANKR_MAX_SCAN_BLOCKS", 2000000)),
			OverlapBlocks:                   uint64(envInt("TRON_ANKR_OVERLAP_BLOCKS", 20)),
			MaxTxAutoScan:                   uint64(envInt("MAX_TRON_TX_AUTO_SCAN", 1000)),
			MaxInternalAutoScan:             uint64(envInt("MAX_TRON_INTERNAL_AUTO_SCAN", 5000)),
			TimeBlockCacheTTL:               time.Duration(envInt("TIME_BLOCK_CACHE_TTL_SECONDS", 86400)) * time.Second,
			TimeBlockProbeRetry:             envInt("TIME_BLOCK_PROBE_RETRY", 2),
			ReceiptCacheMaxEntries:          envInt("TRON_RECEIPT_CACHE_MAX_ENTRIES", 50000),
			ReceiptCacheTTL:                 parseDurationEnv("TRON_RECEIPT_CACHE_TTL", 30*time.Minute),
			ReceiptInitialConcurrency:       envInt("TRON_RECEIPT_INITIAL_CONCURRENCY", 6),
			ReceiptMaxConcurrency:           envInt("TRON_RECEIPT_MAX_CONCURRENCY", 12),
			ReceiptAdaptiveEnabled:          envBool("TRON_RECEIPT_ADAPTIVE_ENABLED", true),
			ReceiptAdaptiveHardMax:          envInt("TRON_RECEIPT_ADAPTIVE_HARD_MAX", 16),
			TRC20AdaptiveEnrichment:         envBool("TRON_TRC20_ADAPTIVE_ENRICHMENT_ENABLED", true),
			TRC20LogEnrichmentRatio:         envFloat("TRON_TRC20_LOG_ENRICHMENT_RATIO", 1.00),
			TRC20EnrichLogMaxWindows:        envInt("TRON_TRC20_ENRICH_LOG_MAX_WINDOWS", 500),
			TRC20EnrichLogSpan:              uint64(envInt("TRON_TRC20_ENRICH_LOG_SPAN", 5000)),
			TRC20BatchReceipts:              envBool("TRON_TRC20_BATCH_RECEIPTS_ENABLED", true),
			TRC20LocalIdentityEnabled:       envBool("TRON_TRC20_LOCAL_IDENTITY_ENABLED", true),
			TRC20PrefetchPages:              envInt("TRON_TRC20_PREFETCH_PAGES", 16),
			TRC20PageQueueMax:               envInt("TRON_TRC20_PAGE_QUEUE_MAX", 512),
		},
		Chainbase: ChainbaseConfig{
			Enabled:                envBool("CHAINBASE_COUNT_ORACLE_ENABLED", true),
			APIKey:                 strings.TrimSpace(os.Getenv("CHAINBASE_API_KEY")),
			Endpoint:               env("CHAINBASE_ENDPOINT", "https://api.chainbase.com"),
			CountTimeout:           time.Duration(envInt("CHAINBASE_COUNT_TIMEOUT_MS", 3000)) * time.Millisecond,
			MaxRetries:             envInt("CHAINBASE_COUNT_MAX_RETRIES", 1),
			Workers:                envInt("CHAINBASE_COUNT_WORKERS", 8),
			MaxConcurrency:         envInt("CHAINBASE_MAX_CONCURRENCY", 16),
			RequestsPerMinute:      envInt("CHAINBASE_REQUESTS_PER_MINUTE", 300),
			ResponseHardLimitBytes: int64(envInt("CHAINBASE_RESPONSE_HARD_LIMIT_MB", 4)) << 20,
			ConnectTimeout:         time.Duration(envInt("CHAINBASE_CONNECT_TIMEOUT_SECONDS", 5)) * time.Second,
			TLSHandshakeTimeout:    time.Duration(envInt("CHAINBASE_TLS_TIMEOUT_SECONDS", 5)) * time.Second,
			BodyIdleTimeout:        time.Duration(envInt("CHAINBASE_BODY_IDLE_TIMEOUT_SECONDS", 10)) * time.Second,
			CreditsPerRequest:      envFloat("CHAINBASE_CREDITS_PER_REQUEST", 0),
			CostPerRequestUSD:      envFloat("CHAINBASE_COST_PER_REQUEST_USD", 0),
			ProbeMaxRequests:       envInt("CHAINBASE_COUNT_PROBE_MAX_REQUESTS", 22),
			NearHeadWindowBlocks:   int64(envInt("CHAINBASE_NEAR_HEAD_WINDOW_BLOCKS", 50000)),
		},
		CountOracle: CountOracleConfig{
			Enabled:                   envBool("COUNT_ORACLE_ENABLED", true), // §121 别名
			CountAwareShardingEnabled: envBool("COUNT_AWARE_SHARDING_ENABLED", false),
			CacheEnabled:              envBool("COUNT_ORACLE_CACHE_ENABLED", true),
			SingleflightEnabled:       envBool("COUNT_ORACLE_SINGLEFLIGHT_ENABLED", true),
			CalibrationEnabled:        envBool("COUNT_ORACLE_CALIBRATION_ENABLED", true),
			ZeroVerifyEnabled:         envBool("COUNT_ORACLE_ZERO_VERIFY_ENABLED", true),
			PlanningDeadline:          time.Duration(envInt("COUNT_PLANNING_DEADLINE_MS", 5000)) * time.Millisecond,
			CacheTTLHistoric:          time.Duration(envInt("CHAINBASE_CACHE_TTL_HISTORIC_HOURS", 24)) * time.Hour,
			CacheTTLNearHead:          time.Duration(envInt("CHAINBASE_CACHE_TTL_NEAR_HEAD_SECONDS", 60)) * time.Second,
			LPTEnabled:                envBool("MULTI_ADDRESS_LPT_ENABLED", false),
		},
		CountProvider: CountProviderConfig{
			PortEnabled:       envBool("COUNT_PROVIDER_PORT_ENABLED", true),
			Shadow:            envBool("COUNT_PROVIDER_PORT_SHADOW", true),
			RouterEnabled:     envBool("COUNT_PROVIDER_ROUTER_ENABLED", true),
			GlobalMaxInflight: envInt("COUNT_PROVIDER_GLOBAL_MAX_INFLIGHT", 256),
			RecordAttempts:    envBool("COUNT_PROVIDER_RECORD_ATTEMPTS", true),

			ChainbaseEnabled:        envBool("COUNT_PROVIDER_CHAINBASE_ENABLED", true),
			ChainbaseTXEnabled:      envBool("COUNT_PROVIDER_CHAINBASE_TX_ENABLED", true),
			ChainbaseTokenEnabled:   envBool("COUNT_PROVIDER_CHAINBASE_TOKEN_ENABLED", false),
			ChainbaseMaxConcurrency: envInt("COUNT_PROVIDER_CHAINBASE_MAX_CONCURRENCY", 1),
			ChainbaseMaxRPS:         envFloat("COUNT_PROVIDER_CHAINBASE_MAX_RPS", 0.9),
			ChainbaseQueueCapacity:  envInt("COUNT_PROVIDER_CHAINBASE_QUEUE_CAPACITY", 128),
			ChainbaseRateCooldownMS: envInt("COUNT_PROVIDER_CHAINBASE_RATE_COOLDOWN_MS", 1800),
			ChainbaseDisplayName:    env("COUNT_PROVIDER_CHAINBASE_DISPLAY_NAME", "Chainbase"),

			MockParallelEnabled:        envBool("COUNT_PROVIDER_MOCKPARALLEL_ENABLED", false),
			MockParallelMaxConcurrency: envInt("COUNT_PROVIDER_MOCKPARALLEL_MAX_CONCURRENCY", 100),
			MockParallelLatencyMS:      envInt("COUNT_PROVIDER_MOCKPARALLEL_LATENCY_MS", 200),
			MockParallelQueueCapacity:  envInt("COUNT_PROVIDER_MOCKPARALLEL_QUEUE_CAPACITY", 2048),
			MockParallelMaxRPS:         envFloat("COUNT_PROVIDER_MOCKPARALLEL_MAX_RPS", 0),
		},
		Global: GlobalConcurrencyConfig{
			MaxInflight:                      envInt("ANKR_GLOBAL_MAX_INFLIGHT", envInt("ANKR_MAX_CONCURRENCY", 48)),
			AutoTuneEnabled:                  envBool("ANKR_GLOBAL_AUTO_TUNE_ENABLED", false),
			FixedConcurrencyEnabled:          envBool("ANKR_FIXED_CONCURRENCY_ENABLED", false),
			LatencyNormalizationEnabled:      envBool("ANKR_PROVIDER_LATENCY_NORMALIZATION_ENABLED", true),
			ProfileLearnEnabled:              envBool("ANKR_GLOBAL_PROFILE_LEARN_ENABLED", false),
			TransactionsRangeShardingEnabled: envBool("ANKR_TRANSACTIONS_RANGE_SHARDING_ENABLED", false),
			RampSteps:                        parseRampSteps(env("ANKR_CONCURRENCY_RAMP_STEPS", "24,32,48")),
			RampSettleSamples:                envInt("ANKR_RAMP_SETTLE_SAMPLES", 9),
			GoodputMinGainPct:                envFloat("ANKR_RAMP_GOODPUT_MIN_GAIN_PCT", 10),
			RollbackCooldown:                 time.Duration(envInt("ANKR_RAMP_ROLLBACK_COOLDOWN_SECONDS", 600)) * time.Second,
		},
		AnkrManagement: AnkrManagementConfig{
			Enabled:       envBool("ANKR_MANAGEMENT_ENABLED", false),
			MCPURL:        env("ANKR_MANAGEMENT_MCP_URL", "https://mcp.ankr.com/mcp"),
			IssuerURL:     env("ANKR_MANAGEMENT_ISSUER_URL", "https://mcp.ankr.com"),
			RedirectURI:   strings.TrimSpace(os.Getenv("ANKR_MANAGEMENT_REDIRECT_URI")),
			FrontendURL:   env("ANKR_MANAGEMENT_FRONTEND_URL", "http://127.0.0.1:3000/api-control?tab=providers"),
			EncryptionKey: os.Getenv("ANKR_MANAGEMENT_ENCRYPTION_KEY"),
			SyncTTL:       time.Duration(envInt("ANKR_MANAGEMENT_SYNC_TTL_SECONDS", 300)) * time.Second,
		},
		Solana: solanaConfigFromEnv(),
		DRPC:   drpcConfigFromEnv(),
		Ankr: AnkrConfig{
			Mode:                    env("ANKR_MODE", "mock"),
			APIKey:                  os.Getenv("ANKR_API_KEY"),
			Endpoint:                env("ANKR_ENDPOINT", "https://rpc.ankr.com/multichain"),
			MaxPageSize:             envInt("ANKR_MAX_PAGE_SIZE", 10000),
			InitialConcurrency:      envInt("ANKR_INITIAL_CONCURRENCY", 32),
			MaxConcurrency:          envInt("ANKR_MAX_CONCURRENCY", 64),
			PerformanceModesEnabled: envBool("ANKR_PERFORMANCE_MODES_ENABLED", true),
			ProdSafeConcurrency:     envInt("ANKR_PROD_SAFE_CONCURRENCY", 24),
			ProdFastConcurrency:     envInt("ANKR_PROD_FAST_CONCURRENCY", 32),
			ProdTurboConcurrency:    envInt("ANKR_PROD_TURBO_CONCURRENCY", 64),
			ProdHardCeiling:         envInt("ANKR_PROD_HARD_CEILING", 64),
			ProdRejectBoundary:      envInt("ANKR_PROD_REJECT_BOUNDARY", 96),
			RequestsPerMinute:       envInt("ANKR_REQUESTS_PER_MINUTE", 900),
			RequestTimeout:          time.Duration(envInt("ANKR_REQUEST_TIMEOUT_SECONDS", 180)) * time.Second,
			ResponseHeaderTimeout:   time.Duration(envInt("ANKR_RESPONSE_HEADER_TIMEOUT_SECONDS", 30)) * time.Second,
			CreditsPerRequest:       envFloat("ANKR_CREDITS_PER_REQUEST", 700),
			CostPerRequestUSD:       envFloat("ANKR_COST_PER_REQUEST_USD", 0.00007),
			// §26：Soft 48MiB / Hard 128MiB（Soft 仅信号，不降 pageSize）。
			ResponseSoftLimitBytes:      int64(envInt("ANKR_RESPONSE_SOFT_LIMIT_MB", 48)) << 20,
			ResponseHardLimitBytes:      int64(envInt("ANKR_RESPONSE_HARD_LIMIT_MB", 128)) << 20,
			PageLevels:                  parsePageLevels(env("ANKR_PAGE_LEVELS", "1000,2000,5000,10000")),
			PageSizeTransactions:        envInt("ANKR_PAGE_SIZE_TRANSACTIONS", 10000),
			PageSizeTokenTransfers:      envInt("ANKR_PAGE_SIZE_TOKEN_TRANSFERS", 10000),
			PageSizeNftTransfers:        envInt("ANKR_PAGE_SIZE_NFT_TRANSFERS", 10000),
			PageSizeLogs:                envInt("ANKR_PAGE_SIZE_LOGS", 10000),
			PageUpSuccessCount:          envInt("ANKR_PAGE_UP_SUCCESS_COUNT", 10),
			PageUpMaxDuration:           time.Duration(envInt("ANKR_PAGE_UP_MAX_DURATION_SECONDS", 30)) * time.Second,
			PageUpMaxBodyBytes:          int64(envInt("ANKR_PAGE_UP_MAX_BODY_MB", 16)) << 20,
			PageDownCooldownSuccess:     envInt("ANKR_PAGE_DOWN_COOLDOWN_SUCCESS_COUNT", 10),
			RangeSplitOnMinPageFailures: envInt("ANKR_RANGE_SPLIT_ON_MIN_PAGE_FAILURES", 3),

			ConnectTimeout:      time.Duration(envInt("ANKR_CONNECT_TIMEOUT_SECONDS", 10)) * time.Second,
			TLSHandshakeTimeout: time.Duration(envInt("ANKR_TLS_TIMEOUT_SECONDS", 10)) * time.Second,
			BodyIdleTimeout:     time.Duration(envInt("ANKR_BODY_IDLE_TIMEOUT_SECONDS", 30)) * time.Second,

			SpoolDir:              env("ANKR_SPOOL_DIR", "/var/lib/chain-platform/spool"),
			SpoolMaxBytes:         int64(envInt("ANKR_SPOOL_MAX_MB", 20480)) << 20,
			SpoolWatermarkMild:    envFloat("ANKR_SPOOL_WATERMARK_MILD", 0.60),
			SpoolWatermarkReduce:  envFloat("ANKR_SPOOL_WATERMARK_REDUCE", 0.75),
			SpoolWatermarkPauseBG: envFloat("ANKR_SPOOL_WATERMARK_PAUSE_BG", 0.85),
			SpoolWatermarkStop:    envFloat("ANKR_SPOOL_WATERMARK_STOP", 0.95),

			FetchSlots:        envInt("ANKR_FETCH_SLOTS", 0), // 0 = 沿用 Initial/MaxConcurrency 语义
			ParserSlots:       envInt("ANKR_PARSER_SLOTS", 6),
			CHWriterSlots:     envInt("ANKR_CH_WRITERS", 2),
			CHBatchMaxRows:    envInt("ANKR_CH_BATCH_MAX_ROWS", 50000),
			CHBatchMaxBytes:   int64(envInt("ANKR_CH_BATCH_MAX_BYTES_MB", 16)) << 20,
			CHBatchFlush:      time.Duration(envInt("ANKR_CH_BATCH_FLUSH_MS", 250)) * time.Millisecond,
			CHInsertP95Thresh: time.Duration(envInt("ANKR_CH_INSERT_P95_THRESHOLD_MS", 5000)) * time.Millisecond,

			FetchMemoryBudgetBytes:     int64(envInt("ANKR_MEMORY_BUDGET_MB", 6144)) << 20,
			WorkerRSSHardMaxBytes:      int64(envInt("ANKR_WORKER_RSS_HARD_MAX_MB", 12288)) << 20,
			VMMemAvailableWarnBytes:    int64(envInt("ANKR_VM_MEM_AVAILABLE_WARN_MB", 6144)) << 20,
			VMMemAvailableProtectBytes: int64(envInt("ANKR_VM_MEM_AVAILABLE_PROTECT_MB", 4096)) << 20,
			SystemMemoryGuardEnabled:   envBool("ANKR_SYSTEM_MEMORY_GUARD_ENABLED", true),
			SwapGrowthSamples:          envInt("ANKR_SWAP_GROWTH_SAMPLES", 2),

			AsyncIngestEnabled:         envBool("ANKR_ASYNC_INGEST_ENABLED", true),
			LocalSpoolEnabled:          envBool("ANKR_LOCAL_SPOOL_ENABLED", true),
			RangeShardingEnabled:       envBool("ANKR_RANGE_SHARDING_ENABLED", true),
			AdaptivePageEnabled:        envBool("ANKR_ADAPTIVE_PAGE_ENABLED", true),
			TailAccelerationEnabled:    envBool("ANKR_TAIL_ACCELERATION_ENABLED", true),
			WeightedConcurrencyEnabled: envBool("ANKR_WEIGHTED_CONCURRENCY_ENABLED", false),
			ValidateFinalEnabled:       envBool("ANKR_VALIDATE_FINAL_ENABLED", true),

			TailBoostConcurrency: envInt("ANKR_TAIL_BOOST_CONCURRENCY", 8),

			V14Enabled:                    envBool("ANKR_V14_ENABLED", false),
			GlobalReadyQueueEnabled:       envBool("ANKR_GLOBAL_READY_QUEUE_ENABLED", false),
			GlobalReadyQueueShadow:        envBool("ANKR_GLOBAL_READY_QUEUE_SHADOW", false),
			TransactionsRangeCertRequired: envBool("ANKR_TRANSACTIONS_RANGE_CERT_REQUIRED", true),
			PageRetryMaxAttempts:          envInt("ANKR_PAGE_RETRY_MAX_ATTEMPTS", 8),
			PageRetryBudgetSeconds:        envInt("ANKR_PAGE_RETRY_BUDGET_SECONDS", 600),
			ReadyQueueMode:                env("ANKR_READY_QUEUE_MODE", "fifo"),
			TailBoostRemainingShards:      envInt("ANKR_TAIL_BOOST_REMAINING_SHARDS", 2),
			ReadyAgingWindowSeconds:       envInt("ANKR_READY_AGING_WINDOW_SECONDS", 300),
			LeaseReaperGraceSeconds:       envInt("ANKR_LEASE_REAPER_GRACE_SECONDS", 0),
			AllowSourceDuplicates:         envBool("ANKR_ALLOW_SOURCE_DUPLICATES", false),
			DuplicateDiagnosticsEnabled:   envBool("ANKR_DUPLICATE_DIAGNOSTICS_ENABLED", false),
			TailSplitWindowMin:            int64(envInt("ANKR_TAIL_SPLIT_WINDOW_MIN_BLOCKS", 50000)),
		},
		Method: MethodConfig{
			Enabled:          envBool("METHOD_RESOLVER_ENABLED", true),
			SeedOnStart:      envBool("METHOD_SEED_ON_START", true),
			RegistryRefresh:  time.Duration(envInt("METHOD_REGISTRY_REFRESH_SECONDS", 60)) * time.Second,
			ResolverInterval: time.Duration(envInt("METHOD_RESOLVER_INTERVAL_SECONDS", 15)) * time.Second,
			ResolverBatch:    envInt("METHOD_RESOLVER_BATCH", 50),
			// 公共 4-byte 签名库默认关闭：内网部署无外网，且它只能做候选补全（§31）
			PublicSignatureEnabled:  envBool("METHOD_PUBLIC_SIGNATURE_ENABLED", false),
			PublicSignatureEndpoint: env("METHOD_PUBLIC_SIGNATURE_ENDPOINT", "https://www.4byte.directory/api/v1/signatures/"),
			PublicSignatureTimeout:  time.Duration(envInt("METHOD_PUBLIC_SIGNATURE_TIMEOUT_SECONDS", 10)) * time.Second,
			BackfillInterval:        time.Duration(envInt("METHOD_BACKFILL_INTERVAL_SECONDS", 10)) * time.Second,
			BackfillBatchSize:       envInt("METHOD_BACKFILL_BATCH_SIZE", 20000),
			BackfillBatchIdle:       time.Duration(envInt("METHOD_BACKFILL_BATCH_IDLE_MS", 200)) * time.Millisecond,
			MaxCachedABIs:           envInt("METHOD_MAX_CACHED_ABIS", 20000),
		},
		RPC: RPCConfig{
			Enabled:         envBool("RPC_ENABLED", true),
			DefaultProvider: env("RPC_DEFAULT_PROVIDER", "ankr"),
			Chains:          splitList(env("RPC_CHAINS", "bsc,eth,polygon,arbitrum,base,avalanche,optimism")),
			ChainRegistry:   os.Getenv("CHAIN_REGISTRY_JSON"),

			AnkrEnabled:          envBool("ANKR_RPC_ENABLED", true),
			AnkrEndpointTemplate: env("ANKR_RPC_ENDPOINT_TEMPLATE", "https://rpc.ankr.com/{chain}/{key}"),
			AnkrAPIKey:           env("ANKR_RPC_API_KEY", os.Getenv("ANKR_API_KEY")),

			HTTPMaxIdleConns:        envInt("RPC_HTTP_MAX_IDLE_CONNS", 256),
			HTTPMaxIdleConnsPerHost: envInt("RPC_HTTP_MAX_IDLE_CONNS_PER_HOST", 128),
			HTTPMaxConnsPerHost:     envInt("RPC_HTTP_MAX_CONNS_PER_HOST", 128),

			GlobalConcurrency: envInt("RPC_GLOBAL_CONCURRENCY", 64),
			RequestsPerSecond: envFloat("RPC_REQUESTS_PER_SECOND", 25),

			ChainHeadTimeout: time.Duration(envInt("RPC_CHAIN_HEAD_TIMEOUT_SECONDS", 5)) * time.Second,
			StandardTimeout:  time.Duration(envInt("RPC_STANDARD_TIMEOUT_SECONDS", 15)) * time.Second,
			LogsTimeout:      time.Duration(envInt("RPC_LOGS_TIMEOUT_SECONDS", 60)) * time.Second,
			TraceTimeout:     time.Duration(envInt("RPC_TRACE_TIMEOUT_SECONDS", 60)) * time.Second,
			DebugTimeout:     time.Duration(envInt("RPC_DEBUG_TIMEOUT_SECONDS", 120)) * time.Second,

			ResponseHardLimitBytes: int64(envInt("RPC_RESPONSE_HARD_LIMIT_MB", 64)) << 20,
			MaxRequestBytes:        envInt("RPC_MAX_REQUEST_BYTES", 1<<20),

			BatchEnabled: envBool("RPC_BATCH_ENABLED", true),
			BatchMaxSize: envInt("RPC_BATCH_MAX_SIZE", 100),
			BatchMaxWait: time.Duration(envInt("RPC_BATCH_MAX_WAIT_MS", 10)) * time.Millisecond,

			CacheEnabled:  envBool("RPC_CACHE_ENABLED", true),
			CacheCapacity: envInt("RPC_CACHE_CAPACITY", 200000),

			// 写方法默认禁用（§247-10）
			TraceEnabled:    envBool("RPC_TRACE_ENABLED", true),
			DebugEnabled:    envBool("RPC_DEBUG_ENABLED", true),
			WriteEnabled:    envBool("RPC_WRITE_ENABLED", false),
			TraceRawPersist: envBool("RPC_TRACE_RAW_PERSIST", true),

			ProbeOnStart:    envBool("RPC_PROBE_ON_START", true),
			HealthSnapshot:  time.Duration(envInt("RPC_HEALTH_SNAPSHOT_SECONDS", 60)) * time.Second,
			RetryMaxAttempt: envInt("RPC_RETRY_MAX_ATTEMPTS", 3),

			BudgetEnabled:            envBool("RPC_BUDGET_ENABLED", true),
			BudgetWindow:             time.Duration(envInt("RPC_BUDGET_WINDOW_HOURS", 24)) * time.Hour,
			TraceMaxRequests:         int64(envInt("RPC_TRACE_MAX_REQUESTS_PER_WINDOW", 20000)),
			InvestigationMaxRequests: int64(envInt("RPC_INVESTIGATION_MAX_REQUESTS", 5000)),
			InvestigationMaxCredits:  int64(envInt("RPC_INVESTIGATION_MAX_CREDITS", 200000)),

			CreditsPerCall: envFloat("RPC_CREDITS_PER_CALL", 200),
			CostPerCallUSD: envFloat("RPC_COST_PER_CALL_USD", 0.00002),
		},
		TokenMetadata: TokenMetadataConfig{
			Enabled:           envBool("TOKEN_METADATA_ENABLED", true),
			BackfillEnabled:   envBool("TOKEN_METADATA_BACKFILL_ENABLED", true),
			BackfillBatchSize: envInt("TOKEN_METADATA_BACKFILL_BATCH_SIZE", 500),
			WorkerInterval:    time.Duration(envInt("TOKEN_METADATA_WORKER_INTERVAL_SECONDS", 5)) * time.Second,
			BatchIdle:         time.Duration(envInt("TOKEN_METADATA_BACKFILL_BATCH_IDLE_MS", 200)) * time.Millisecond,
			RPCMaxRetries:     envInt("TOKEN_METADATA_RPC_MAX_RETRIES", 3),
			RPCWorkers:        envInt("TOKEN_METADATA_RPC_WORKERS", 1),
			RPCMinInterval:    time.Duration(envInt("TOKEN_METADATA_RPC_MIN_INTERVAL_MS", 2000)) * time.Millisecond,
			LogoEnabled:       envBool("TOKEN_LOGO_ENABLED", true),
			LogoMaxBytes:      int64(envInt("TOKEN_LOGO_MAX_BYTES", 2097152)),
			LogoMaxWidth:      envInt("TOKEN_LOGO_MAX_WIDTH", 4096),
			LogoMaxHeight:     envInt("TOKEN_LOGO_MAX_HEIGHT", 4096),
			LogoMirrorBase:    env("TOKEN_LOGO_MIRROR_BASE", ""),
			LogoMirrorHosts:   splitList(env("TOKEN_LOGO_MIRROR_HOSTS", "")),
		},
	}
}
