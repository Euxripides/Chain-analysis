// Package app：API 与 Worker 共用的启动引导。
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/events"
	"github.com/blockchain-platform/blockchain-platform/migrations"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

type Infra struct {
	Store *store.Store
	CH    *clickhouse.Conn
	Raw   *rawstore.Store
	Bus   *events.Bus
	Prov  provider.Provider
}

// Bootstrap 连接全部依赖并执行迁移。
func Bootstrap(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Infra, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config invalid: %w", err)
	}
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := migrations.ApplyPostgres(ctx, st.Pool); err != nil {
		return nil, err
	}
	log.Info("postgres ready")
	usageHook := UsageRecorder(st, log)

	ch, err := clickhouse.New(ctx, cfg.ClickHouseURL)
	if err != nil {
		return nil, err
	}
	if err := migrations.ApplyClickHouse(ctx, ch.Exec); err != nil {
		return nil, err
	}
	log.Info("clickhouse ready")

	raw, err := rawstore.New(ctx, cfg.MinioEndpoint, cfg.MinioAccessKey, cfg.MinioSecretKey, cfg.MinioSecure, cfg.RawBucket)
	if err != nil {
		return nil, err
	}
	_ = raw.EnsureBucket(ctx)
	_ = raw.EnsureNamedBucket(ctx, cfg.ExportBucket)
	_ = raw.EnsureNamedBucket(ctx, cfg.BackupsBucket)
	log.Info("minio ready", "buckets", []string{cfg.RawBucket, cfg.ExportBucket, cfg.BackupsBucket})

	bus := events.New(cfg.NatsURL, log)

	var prov provider.Provider
	if cfg.Ankr.Mode == "live" && cfg.Ankr.APIKey != "" {
		prov = ankr.New(ankr.Options{
			Endpoint: cfg.Ankr.Endpoint, APIKey: cfg.Ankr.APIKey, MaxPageSize: cfg.Ankr.MaxPageSize,
			RequestsPerMinute: cfg.Ankr.RequestsPerMinute, RequestTimeout: cfg.Ankr.RequestTimeout,
			ResponseHeaderTimeout: cfg.Ankr.ResponseHeaderTimeout,
			ResponseSoftLimitBytes: cfg.Ankr.ResponseSoftLimitBytes,
			ResponseHardLimitBytes: cfg.Ankr.ResponseHardLimitBytes,
			CreditsPerRequest: cfg.Ankr.CreditsPerRequest, CostPerRequestUSD: cfg.Ankr.CostPerRequestUSD,
		}, usageHook)
		log.Info("provider: ankr (live)")
	} else {
		prov = ankr.NewMock(cfg.Ankr.MaxPageSize, usageHook)
		log.Info("provider: ankr-mock (ANKR_MODE=mock)", "hint", "set ANKR_API_KEY and ANKR_MODE=live for real data")
	}

	return &Infra{Store: st, CH: ch, Raw: raw, Bus: bus, Prov: prov}, nil
}

// UsageRecorder 每次 Provider 请求异步写入用量表（成本核算基础）。
func UsageRecorder(st *store.Store, log *slog.Logger) provider.UsageHook {
	return func(u model.UsageRow) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := st.InsertUsage(ctx, u); err != nil {
				log.Error("insert usage failed", "err", err)
			}
		}()
	}
}
