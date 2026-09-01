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
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/tokenmeta"
	"github.com/blockchain-platform/blockchain-platform/migrations"
)

type Infra struct {
	Store *store.Store
	CH    *clickhouse.Conn
	Raw   *rawstore.Store
	Bus   *events.Bus
	Prov  provider.Provider

	// V1.3 Method / ABI 解析栈（§17/§20）：API 与 Worker 共享同一份内存索引结构。
	Registry      *method.Registry
	Resolver      *method.FastResolver
	Enqueuer      *method.Enqueuer
	MethodData    *method.ClickHouseStore
	DeepDecode    *method.AnkrDeepResolver
	TokenMeta     *tokenmeta.Service
	TokenBackfill *tokenmeta.BackfillRunner
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
			ResponseHeaderTimeout:  cfg.Ankr.ResponseHeaderTimeout,
			ResponseSoftLimitBytes: cfg.Ankr.ResponseSoftLimitBytes,
			ResponseHardLimitBytes: cfg.Ankr.ResponseHardLimitBytes,
			CreditsPerRequest:      cfg.Ankr.CreditsPerRequest, CostPerRequestUSD: cfg.Ankr.CostPerRequestUSD,
		}, usageHook)
		log.Info("provider: ankr (live)")
	} else {
		prov = ankr.NewMock(cfg.Ankr.MaxPageSize, usageHook)
		log.Info("provider: ankr-mock (ANKR_MODE=mock)", "hint", "set ANKR_API_KEY and ANKR_MODE=live for real data")
	}

	infra := &Infra{Store: st, CH: ch, Raw: raw, Bus: bus, Prov: prov}
	if err := infra.initMethod(ctx, cfg, log); err != nil {
		return nil, err
	}
	if cfg.TokenMetadata.Enabled {
		var rpc tokenmeta.RPCResolver
		if candidate, ok := prov.(provider.TokenMetadataResolver); ok {
			rpc = candidate
		}
		infra.TokenMeta = tokenmeta.New(st, ch, rpc, log)
		infra.TokenMeta.ConfigureLogo(raw, tokenmeta.LogoConfig{Enabled: cfg.TokenMetadata.LogoEnabled, MaxBytes: cfg.TokenMetadata.LogoMaxBytes, MaxWidth: cfg.TokenMetadata.LogoMaxWidth, MaxHeight: cfg.TokenMetadata.LogoMaxHeight})
		if err := infra.TokenMeta.EnsureVerifiedRegistry(ctx); err != nil {
			log.Warn("verified token registry seed failed", "err", err)
		}
		infra.TokenBackfill = &tokenmeta.BackfillRunner{Service: infra.TokenMeta, BatchSize: cfg.TokenMetadata.BackfillBatchSize, Interval: cfg.TokenMetadata.WorkerInterval, BatchIdle: cfg.TokenMetadata.BatchIdle, MaxRetries: cfg.TokenMetadata.RPCMaxRetries, RPCWorkers: cfg.TokenMetadata.RPCWorkers, RPCMinInterval: cfg.TokenMetadata.RPCMinInterval, Log: log}
	}
	return infra, nil
}

func (i *Infra) StartTokenMetadataBackground(ctx context.Context, cfg *config.Config, log *slog.Logger, withWorkers bool) {
	if i.TokenMeta == nil || !withWorkers || !cfg.TokenMetadata.BackfillEnabled {
		return
	}
	go i.TokenBackfill.Run(ctx)
	i.TokenMeta.StartLogoWorker(ctx)
	// Backfill can consume thousands of billable RPC calls. Startup only resumes
	// an explicitly created pending/running job; it never creates a new full-chain
	// job implicitly. Administrators start backfills through the admin endpoint.
	log.Info("token metadata background worker started", "batch", cfg.TokenMetadata.BackfillBatchSize)
}

// initMethod 初始化 Method 解析栈：种子 + 内存索引首次加载（§17/§32）。
// 解析属于 Enrichment：即使这里失败也不能阻断数据面启动（§36）。
func (i *Infra) initMethod(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	i.Registry = method.NewRegistry()
	i.MethodData = method.NewClickHouseStore(i.CH)
	if fetcher, ok := i.Prov.(provider.TxDetailFetcher); ok {
		i.DeepDecode = method.NewAnkrDeepResolver(fetcher)
	}
	if !cfg.Method.Enabled {
		i.Resolver = method.NewFastResolver(i.Registry, nil)
		log.Warn("method resolver disabled (METHOD_RESOLVER_ENABLED=false)")
		return nil
	}
	if cfg.Method.SeedOnStart {
		if methods, events, err := method.EnsureSeed(ctx, i.Store); err != nil {
			log.Warn("method signature seed failed", "err", err)
		} else {
			log.Info("method signature seed ready", "signatures", methods, "events", events)
		}
	}
	if err := i.Registry.Refresh(ctx, i.Store); err != nil {
		log.Warn("method registry load failed", "err", err)
	} else {
		log.Info("method registry ready", "loaded_at", i.Registry.LoadedAt())
	}
	i.Enqueuer = method.NewEnqueuer(i.Store, func(subject string, payload any) {
		// NATS Stream 只订阅 events.>，未知 Selector 事件只带元数据（§18）
		i.Bus.Publish(context.Background(), "events."+subject, payload)
	}, log)
	i.Resolver = method.NewFastResolver(i.Registry, i.Enqueuer.Enqueue)
	return nil
}

// StartMethodBackground 启动后台循环。
// withWorkers=true 时同时启动异步补全与回填（只在 Worker 进程启用，避免多实例重复执行）。
func (i *Infra) StartMethodBackground(ctx context.Context, cfg *config.Config, log *slog.Logger, withWorkers bool) {
	if !cfg.Method.Enabled || i.Registry == nil {
		return
	}
	i.Registry.StartRefresh(ctx, i.Store, cfg.Method.RegistryRefresh, func(err error) {
		log.Warn("method registry refresh failed", "err", err)
	})
	if !withWorkers || i.Enqueuer == nil {
		return
	}
	go i.Enqueuer.Run(ctx)
	go (&method.ResolverWorker{
		Store:    i.Store,
		Registry: i.Registry,
		Public: method.NewPublicSignatureResolver(cfg.Method.PublicSignatureEnabled,
			cfg.Method.PublicSignatureEndpoint, cfg.Method.PublicSignatureTimeout),
		Log:      log,
		Interval: cfg.Method.ResolverInterval,
		Batch:    cfg.Method.ResolverBatch,
	}).Run(ctx)
	go (&method.BackfillRunner{
		Store: i.Store, Data: i.MethodData, Resolver: i.Resolver, Log: log,
		Interval: cfg.Method.BackfillInterval, BatchIdle: cfg.Method.BackfillBatchIdle,
	}).Run(ctx)
	log.Info("method background workers started",
		"public_signature", cfg.Method.PublicSignatureEnabled, "backfill_batch", cfg.Method.BackfillBatchSize)
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
