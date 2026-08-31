package config

import (
	"os"
	"strconv"
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
	CreditsPerRequest  float64
	CostPerRequestUSD  float64
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
	Ankr           AnkrConfig
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
		Ankr: AnkrConfig{
			Mode:               env("ANKR_MODE", "mock"),
			APIKey:             os.Getenv("ANKR_API_KEY"),
			Endpoint:           env("ANKR_ENDPOINT", "https://rpc.ankr.com/multichain"),
			MaxPageSize:        envInt("ANKR_MAX_PAGE_SIZE", 10000),
			InitialConcurrency: envInt("ANKR_INITIAL_CONCURRENCY", 4),
			MaxConcurrency:     envInt("ANKR_MAX_CONCURRENCY", 32),
			RequestsPerMinute:  envInt("ANKR_REQUESTS_PER_MINUTE", 900),
			RequestTimeout:     time.Duration(envInt("ANKR_REQUEST_TIMEOUT_SECONDS", 60)) * time.Second,
			CreditsPerRequest:  envFloat("ANKR_CREDITS_PER_REQUEST", 700),
			CostPerRequestUSD:  envFloat("ANKR_COST_PER_REQUEST_USD", 0.00007),
		},
	}
}
