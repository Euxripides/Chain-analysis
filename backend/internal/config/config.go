package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type AnkrConfig struct {
	Mode               string
	APIKey             string
	Endpoint           string
	MaxPageSize        int
	InitialConcurrency int
	MaxConcurrency     int
	RequestsPerMinute  int
	RequestTimeout     time.Duration
	ResponseHeaderTimeout time.Duration
	CreditsPerRequest  float64
	CostPerRequestUSD  float64

	// V1.1：响应体保护（§8/§32）
	ResponseSoftLimitBytes int64
	ResponseHardLimitBytes int64

	// V1.1：自适应 PageSize（§12-§15/§32）
	PageLevels             []int
	PageSizeTransactions   int
	PageSizeTokenTransfers int
	PageSizeNftTransfers   int
	PageSizeLogs           int
	PageUpSuccessCount     int
	PageUpMaxDuration      time.Duration
	PageUpMaxBodyBytes     int64
	PageDownCooldownSuccess int
	RangeSplitOnMinPageFailures int
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

type Config struct {
	Env            string
	Timezone       string
	LogLevel       string
	HTTPPort       int
	DatabaseURL    string
	ClickHouseURL  string
	MinioEndpoint  string
	MinioSecure    bool
	MinioAccessKey string
	MinioSecretKey string
	NatsURL        string
	RawBucket      string
	ExportBucket   string
	BackupsBucket  string
	WorkerMetricsPort int
	Ankr           AnkrConfig
	Method         MethodConfig
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
		Env:            env("APP_ENV", "development"),
		Timezone:       env("APP_TIMEZONE", "Asia/Bangkok"),
		LogLevel:       env("API_LOG_LEVEL", "info"),
		HTTPPort:       envInt("API_HTTP_PORT", 8080),
		DatabaseURL:    env("DATABASE_URL", "postgres://blockchain:change_me_pg_2026@127.0.0.1:5433/blockchain_platform?sslmode=disable"),
		ClickHouseURL:  env("CLICKHOUSE_URL", "clickhouse://blockchain:change_me_ch_2026@127.0.0.1:9000/blockchain"),
		MinioEndpoint:  env("MINIO_ENDPOINT", "127.0.0.1:9001"),
		MinioSecure:    envBool("MINIO_SECURE", false),
		MinioAccessKey: env("MINIO_ROOT_USER", "blockchain_admin"),
		MinioSecretKey: env("MINIO_ROOT_PASSWORD", "change_me_minio_2026"),
		NatsURL:        env("NATS_URL", "nats://127.0.0.1:4222"),
		RawBucket:      env("RAW_BUCKET", "blockchain-raw"),
		ExportBucket:   env("EXPORT_BUCKET", "blockchain-exports"),
		BackupsBucket:  env("BACKUPS_BUCKET", "blockchain-backups"),
		WorkerMetricsPort: envInt("WORKER_METRICS_PORT", 8081),
		Ankr: AnkrConfig{
			Mode:               env("ANKR_MODE", "mock"),
			APIKey:             os.Getenv("ANKR_API_KEY"),
			Endpoint:           env("ANKR_ENDPOINT", "https://rpc.ankr.com/multichain"),
			MaxPageSize:        envInt("ANKR_MAX_PAGE_SIZE", 10000),
			InitialConcurrency: envInt("ANKR_INITIAL_CONCURRENCY", 4),
			MaxConcurrency:     envInt("ANKR_MAX_CONCURRENCY", 32),
			RequestsPerMinute:  envInt("ANKR_REQUESTS_PER_MINUTE", 900),
			RequestTimeout:     time.Duration(envInt("ANKR_REQUEST_TIMEOUT_SECONDS", 60)) * time.Second,
			ResponseHeaderTimeout: time.Duration(envInt("ANKR_RESPONSE_HEADER_TIMEOUT_SECONDS", 120)) * time.Second,
			CreditsPerRequest:  envFloat("ANKR_CREDITS_PER_REQUEST", 700),
			CostPerRequestUSD:  envFloat("ANKR_COST_PER_REQUEST_USD", 0.00007),
			ResponseSoftLimitBytes:  int64(envInt("ANKR_RESPONSE_SOFT_LIMIT_MB", 32)) << 20,
			ResponseHardLimitBytes:  int64(envInt("ANKR_RESPONSE_HARD_LIMIT_MB", 64)) << 20,
			PageLevels:             parsePageLevels(env("ANKR_PAGE_LEVELS", "1000,2000,5000,10000")),
			PageSizeTransactions:   envInt("ANKR_PAGE_SIZE_TRANSACTIONS", 10000),
			PageSizeTokenTransfers: envInt("ANKR_PAGE_SIZE_TOKEN_TRANSFERS", 10000),
			PageSizeNftTransfers:   envInt("ANKR_PAGE_SIZE_NFT_TRANSFERS", 10000),
			PageSizeLogs:           envInt("ANKR_PAGE_SIZE_LOGS", 5000),
			PageUpSuccessCount:     envInt("ANKR_PAGE_UP_SUCCESS_COUNT", 3),
			PageUpMaxDuration:      time.Duration(envInt("ANKR_PAGE_UP_MAX_DURATION_SECONDS", 30)) * time.Second,
			PageUpMaxBodyBytes:     int64(envInt("ANKR_PAGE_UP_MAX_BODY_MB", 16)) << 20,
			PageDownCooldownSuccess: envInt("ANKR_PAGE_DOWN_COOLDOWN_SUCCESS_COUNT", 5),
			RangeSplitOnMinPageFailures: envInt("ANKR_RANGE_SPLIT_ON_MIN_PAGE_FAILURES", 3),
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
	}
}
