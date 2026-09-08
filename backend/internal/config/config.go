package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

type AnkrConfig struct {
	Mode                  string
	APIKey                string
	Endpoint              string
	MaxPageSize           int
	InitialConcurrency    int
	MaxConcurrency        int
	RequestsPerMinute     int
	RequestTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	CreditsPerRequest     float64
	CostPerRequestUSD     float64

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

	// V1.4.2 production performance policy. These values bootstrap the audited
	// database profile; the worker never accepts a target above HardCeiling.
	ProdSafeConcurrency   int
	ProdFastConcurrency   int
	ProdTurboConcurrency  int
	ProdHardCeiling       int
	ProdRejectBoundary    int
	MemoryBudgetBytes     int64
	AbsoluteRSSLimitBytes int64
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

type Config struct {
	Env               string
	Timezone          string
	LogLevel          string
	HTTPPort          int
	DatabaseURL       string
	ClickHouseURL     string
	MinioEndpoint     string
	MinioSecure       bool
	MinioAccessKey    string
	MinioSecretKey    string
	NatsURL           string
	RawBucket         string
	ExportBucket      string
	BackupsBucket     string
	WorkerMetricsPort int
	Ankr              AnkrConfig
	Method            MethodConfig
	TokenMetadata     TokenMetadataConfig
	RPC               RPCConfig
	AnkrManagement    AnkrManagementConfig
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

func (c *Config) AnkrProductionProfile() model.DownloadPerformanceProfile {
	now := time.Now().UTC()
	return model.DownloadPerformanceProfile{
		Scope: "global", DefaultMode: model.PerformanceFast,
		SafeConcurrency: c.Ankr.ProdSafeConcurrency, FastConcurrency: c.Ankr.ProdFastConcurrency,
		TurboConcurrency: c.Ankr.ProdTurboConcurrency, HardCeiling: c.Ankr.ProdHardCeiling,
		RejectBoundary: c.Ankr.ProdRejectBoundary, MemoryBudgetBytes: c.Ankr.MemoryBudgetBytes,
		AbsoluteRSSLimitBytes: c.Ankr.AbsoluteRSSLimitBytes, PageSizeDefault: c.Ankr.MaxPageSize,
		RequestTimeoutSeconds: int(c.Ankr.RequestTimeout / time.Second), RangeShardingEnabled: true,
		Version: "v1.4.2", VerifiedAt: now, UpdatedAt: now, UpdatedBy: "env-bootstrap",
	}
}

// splitList 解析逗号分隔配置。
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

// Validate 启动校验（§32：错误配置直接启动失败）。
func (c *Config) Validate() error {
	a := &c.Ankr
	if a.ProdSafeConcurrency <= 0 || a.ProdFastConcurrency <= a.ProdSafeConcurrency || a.ProdTurboConcurrency <= a.ProdFastConcurrency {
		return fmt.Errorf("Ankr production concurrency must increase from SAFE to FAST to TURBO")
	}
	if a.ProdHardCeiling <= 0 || a.ProdHardCeiling > 48 {
		return fmt.Errorf("ANKR_PROD_HARD_CEILING must be between 1 and 48")
	}
	if a.ProdTurboConcurrency > a.ProdHardCeiling || a.MaxConcurrency > a.ProdHardCeiling || a.InitialConcurrency > a.ProdHardCeiling {
		return fmt.Errorf("Ankr production concurrency must not exceed hard ceiling %d", a.ProdHardCeiling)
	}
	if a.ProdRejectBoundary <= a.ProdHardCeiling {
		return fmt.Errorf("ANKR_PROD_REJECT_BOUNDARY must exceed the production hard ceiling")
	}
	if a.ResponseHardLimitBytes <= 0 || a.ResponseSoftLimitBytes <= 0 {
		return fmt.Errorf("ankr response limits must be positive")
	}
	if a.ResponseSoftLimitBytes >= a.ResponseHardLimitBytes {
		return fmt.Errorf("ankr response soft limit must be < hard limit")
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
		Env:               env("APP_ENV", "development"),
		Timezone:          env("APP_TIMEZONE", "Asia/Bangkok"),
		LogLevel:          env("API_LOG_LEVEL", "info"),
		HTTPPort:          envInt("API_HTTP_PORT", 8080),
		DatabaseURL:       env("DATABASE_URL", "postgres://blockchain:change_me_pg_2026@127.0.0.1:5433/blockchain_platform?sslmode=disable"),
		ClickHouseURL:     env("CLICKHOUSE_URL", "clickhouse://blockchain:change_me_ch_2026@127.0.0.1:9000/blockchain"),
		MinioEndpoint:     env("MINIO_ENDPOINT", "127.0.0.1:9001"),
		MinioSecure:       envBool("MINIO_SECURE", false),
		MinioAccessKey:    env("MINIO_ROOT_USER", "blockchain_admin"),
		MinioSecretKey:    env("MINIO_ROOT_PASSWORD", "change_me_minio_2026"),
		NatsURL:           env("NATS_URL", "nats://127.0.0.1:4222"),
		RawBucket:         env("RAW_BUCKET", "blockchain-raw"),
		ExportBucket:      env("EXPORT_BUCKET", "blockchain-exports"),
		BackupsBucket:     env("BACKUPS_BUCKET", "blockchain-backups"),
		WorkerMetricsPort: envInt("WORKER_METRICS_PORT", 8081),
		AnkrManagement: AnkrManagementConfig{
			Enabled:       envBool("ANKR_MANAGEMENT_ENABLED", false),
			MCPURL:        env("ANKR_MANAGEMENT_MCP_URL", "https://mcp.ankr.com/mcp"),
			IssuerURL:     env("ANKR_MANAGEMENT_ISSUER_URL", "https://mcp.ankr.com"),
			RedirectURI:   strings.TrimSpace(os.Getenv("ANKR_MANAGEMENT_REDIRECT_URI")),
			FrontendURL:   env("ANKR_MANAGEMENT_FRONTEND_URL", "http://127.0.0.1:3000/api-control?tab=providers"),
			EncryptionKey: os.Getenv("ANKR_MANAGEMENT_ENCRYPTION_KEY"),
			SyncTTL:       time.Duration(envInt("ANKR_MANAGEMENT_SYNC_TTL_SECONDS", 300)) * time.Second,
		},
		Ankr: AnkrConfig{
			Mode:                        env("ANKR_MODE", "mock"),
			APIKey:                      os.Getenv("ANKR_API_KEY"),
			Endpoint:                    env("ANKR_ENDPOINT", "https://rpc.ankr.com/multichain"),
			MaxPageSize:                 envInt("ANKR_MAX_PAGE_SIZE", 10000),
			InitialConcurrency:          envInt("ANKR_INITIAL_CONCURRENCY", 32),
			MaxConcurrency:              envInt("ANKR_MAX_CONCURRENCY", 48),
			RequestsPerMinute:           envInt("ANKR_REQUESTS_PER_MINUTE", 900),
			RequestTimeout:              time.Duration(envInt("ANKR_REQUEST_TIMEOUT_SECONDS", 180)) * time.Second,
			ResponseHeaderTimeout:       time.Duration(envInt("ANKR_RESPONSE_HEADER_TIMEOUT_SECONDS", 120)) * time.Second,
			CreditsPerRequest:           envFloat("ANKR_CREDITS_PER_REQUEST", 700),
			CostPerRequestUSD:           envFloat("ANKR_COST_PER_REQUEST_USD", 0.00007),
			ResponseSoftLimitBytes:      int64(envInt("ANKR_RESPONSE_SOFT_LIMIT_MB", 32)) << 20,
			ResponseHardLimitBytes:      int64(envInt("ANKR_RESPONSE_HARD_LIMIT_MB", 64)) << 20,
			PageLevels:                  parsePageLevels(env("ANKR_PAGE_LEVELS", "1000,2000,5000,10000")),
			PageSizeTransactions:        envInt("ANKR_PAGE_SIZE_TRANSACTIONS", 10000),
			PageSizeTokenTransfers:      envInt("ANKR_PAGE_SIZE_TOKEN_TRANSFERS", 10000),
			PageSizeNftTransfers:        envInt("ANKR_PAGE_SIZE_NFT_TRANSFERS", 10000),
			PageSizeLogs:                envInt("ANKR_PAGE_SIZE_LOGS", 10000),
			PageUpSuccessCount:          envInt("ANKR_PAGE_UP_SUCCESS_COUNT", 3),
			PageUpMaxDuration:           time.Duration(envInt("ANKR_PAGE_UP_MAX_DURATION_SECONDS", 30)) * time.Second,
			PageUpMaxBodyBytes:          int64(envInt("ANKR_PAGE_UP_MAX_BODY_MB", 16)) << 20,
			PageDownCooldownSuccess:     envInt("ANKR_PAGE_DOWN_COOLDOWN_SUCCESS_COUNT", 5),
			RangeSplitOnMinPageFailures: envInt("ANKR_RANGE_SPLIT_ON_MIN_PAGE_FAILURES", 3),
			ProdSafeConcurrency:         envInt("ANKR_PROD_SAFE_CONCURRENCY", 24),
			ProdFastConcurrency:         envInt("ANKR_PROD_FAST_CONCURRENCY", 32),
			ProdTurboConcurrency:        envInt("ANKR_PROD_TURBO_CONCURRENCY", 48),
			ProdHardCeiling:             envInt("ANKR_PROD_HARD_CEILING", 48),
			ProdRejectBoundary:          envInt("ANKR_PROD_REJECT_BOUNDARY", 64),
			MemoryBudgetBytes:           int64(envInt("ANKR_MEMORY_BUDGET_MB", 6144)) << 20,
			AbsoluteRSSLimitBytes:       int64(envInt("ANKR_ABSOLUTE_RSS_LIMIT_MB", 12288)) << 20,
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
		},
	}
}
