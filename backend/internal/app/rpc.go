package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/mockrpc"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"

	ankrrpc "github.com/blockchain-platform/blockchain-platform/internal/provider/ankr/rpc"
)

// initRPC 装配 V1.5 统一 RPC 基础设施（§7/§248）。
//
// 装配顺序：Chain Registry → Method Registry → Capability/Router/Gateway → Provider 注册 →
// 类型化服务（ChainReader/Head/Contract/AddressType/Balance/Logs/Trace）。
// 业务模块只拿到类型化服务，永远看不到 Provider 与 API Key（§8/§9）。
func (i *Infra) initRPC(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if !cfg.RPC.Enabled {
		log.Warn("rpc infrastructure disabled (RPC_ENABLED=false)")
		return nil
	}
	chains, err := chain.LoadRegistry(cfg.RPC.ChainRegistry)
	if err != nil {
		return err
	}
	i.Chains = chains

	methods := rpc.NewMethodRegistry(rpc.RegistryOptions{
		TraceEnabled: cfg.RPC.TraceEnabled,
		DebugEnabled: cfg.RPC.DebugEnabled,
		WriteEnabled: cfg.RPC.WriteEnabled,
		Timeouts: map[rpc.MethodClass]time.Duration{
			rpc.ClassChainHead:    cfg.RPC.ChainHeadTimeout,
			rpc.ClassBalance:      cfg.RPC.StandardTimeout,
			rpc.ClassCode:         cfg.RPC.StandardTimeout,
			rpc.ClassStorage:      cfg.RPC.StandardTimeout,
			rpc.ClassContractRead: cfg.RPC.StandardTimeout,
			rpc.ClassTransaction:  cfg.RPC.StandardTimeout,
			rpc.ClassReceipt:      cfg.RPC.StandardTimeout,
			rpc.ClassBlock:        cfg.RPC.StandardTimeout,
			rpc.ClassLogs:         cfg.RPC.LogsTimeout,
			rpc.ClassTrace:        cfg.RPC.TraceTimeout,
			rpc.ClassDebugTrace:   cfg.RPC.DebugTimeout,
		},
	})

	capabilities := rpc.NewCapabilityStore()
	breakers := rpc.NewBreakers(40, 0.5, 30*time.Second)
	health := rpc.NewHealthTracker()
	concurrency := rpc.NewConcurrencyController(cfg.RPC.GlobalConcurrency)
	rate := rpc.NewRateController(cfg.RPC.RequestsPerSecond)
	router := rpc.NewRouter(capabilities, breakers, health, concurrency)

	// 预算：Trace 与 Investigation 单独限额（§109/§112/§169）
	budgets := rpc.NewBudgetManager(cfg.RPC.BudgetWindow, map[string]rpc.BudgetLimits{
		rpc.PurposeTrace:         {MaxRequests: cfg.RPC.TraceMaxRequests, MaxTraceRequests: cfg.RPC.TraceMaxRequests},
		rpc.PurposeDebugTrace:    {MaxRequests: cfg.RPC.TraceMaxRequests, MaxTraceRequests: cfg.RPC.TraceMaxRequests},
		rpc.PurposeInvestigation: {MaxRequests: cfg.RPC.InvestigationMaxRequests, MaxCredits: cfg.RPC.InvestigationMaxCredits},
	}, cfg.RPC.BudgetEnabled)

	provider, secrets := buildRPCProvider(cfg, log)
	i.RPCProvider = provider
	pricing, pricingErr := i.Store.ProviderPricing(ctx, provider.Name(), model.APITypeRPC)
	if pricingErr != nil {
		log.Warn("load rpc provider pricing failed; using configured fallback", "provider", provider.Name(), "err", pricingErr)
	}
	if pricing.CreditsPerCall <= 0 {
		pricing = rpc.Pricing{CreditsPerCall: cfg.RPC.CreditsPerCall, CostPerCallUSD: cfg.RPC.CostPerCallUSD}
	}

	gateway := rpc.NewGateway(rpc.GatewayOptions{
		Chains: chains, Methods: methods, Router: router, Capabilities: capabilities,
		Cache:       rpc.NewCache(cfg.RPC.CacheCapacity, cfg.RPC.CacheEnabled),
		Budgets:     budgets,
		Rate:        rate,
		Concurrency: concurrency,
		Breakers:    breakers,
		Health:      health,
		Usage:       rpc.UsageHook(UsageRecorder(i.Store, log)),
		Pricing:     pricing,
		Retry: rpc.RetryPolicy{MaxAttempts: cfg.RPC.RetryMaxAttempt,
			BaseBackoff: 200 * time.Millisecond, MaxBackoff: 5 * time.Second},
		Log: log, Credential: "env:ANKR_API_KEY", Secrets: secrets,
		MaxRequestBytes: cfg.RPC.MaxRequestBytes, MaxBatchSize: cfg.RPC.BatchMaxSize,
	})

	// 注册链：Router 必须支持未来多 Provider，禁止写死 provider = ankr（§27/§247-4）
	for _, slug := range cfg.RPC.Chains {
		config, err := chains.Require(slug)
		if err != nil {
			log.Warn("rpc chain skipped", "chain", slug, "err", err)
			continue
		}
		caps, capErr := provider.Capabilities(ctx, config.Slug)
		if capErr != nil {
			log.Warn("rpc provider capability unavailable", "chain", config.Slug, "err", capErr)
			continue
		}
		capabilities.Set(provider.Name(), config.Slug, caps, rpc.CapSourceStatic)
		router.Register(config.Slug, provider, 100, 10)
	}
	// 管理员覆盖优先级最高（§20）
	if overrides, err := i.Store.LoadProviderCapabilities(ctx); err == nil {
		for _, record := range overrides {
			capabilities.Set(record.Provider, record.Chain, record.Capability, rpc.CapSourceAdmin)
		}
	} else {
		log.Warn("load rpc capability overrides failed", "err", err)
	}

	i.RPCGateway = gateway
	if cfg.RPC.BatchEnabled {
		i.RPCBatcher = rpc.NewBatcher(gateway, rpc.BatcherOptions{
			MaxSize: cfg.RPC.BatchMaxSize, MaxWait: cfg.RPC.BatchMaxWait})
	}
	i.ChainRead = chainread.New(gateway, i.RPCBatcher)
	i.Head = chainread.NewHeadService(i.ChainRead)
	i.Contract = chainread.NewContractService(i.ChainRead)
	i.AddressType = chainread.NewAddressTypeService(i.ChainRead)
	i.BalanceRead = chainread.NewBalanceService(i.ChainRead)
	i.LogsRead = chainread.NewLogsService(i.ChainRead)
	i.Trace = chaintrace.NewService(gateway, chaintrace.NewStore(i.CH), i.Raw, cfg.RPC.TraceRawPersist, log)

	log.Info("rpc infrastructure ready", "provider", provider.Name(),
		"chains", cfg.RPC.Chains, "batch", cfg.RPC.BatchEnabled, "cache", cfg.RPC.CacheEnabled,
		"trace", cfg.RPC.TraceEnabled, "write_enabled", cfg.RPC.WriteEnabled)
	return nil
}

// buildRPCProvider 有 Key 用真实 Ankr Chain RPC，否则用可编程 Mock（§208）。
func buildRPCProvider(cfg *config.Config, log *slog.Logger) (rpc.Provider, []string) {
	if cfg.RPC.AnkrEnabled && cfg.RPC.AnkrAPIKey != "" {
		client := ankrrpc.New(ankrrpc.Options{
			EndpointTemplate:    cfg.RPC.AnkrEndpointTemplate,
			APIKey:              cfg.RPC.AnkrAPIKey,
			RequestTimeout:      cfg.RPC.StandardTimeout,
			ResponseHardLimit:   cfg.RPC.ResponseHardLimitBytes,
			MaxIdleConns:        cfg.RPC.HTTPMaxIdleConns,
			MaxIdleConnsPerHost: cfg.RPC.HTTPMaxIdleConnsPerHost,
			MaxConnsPerHost:     cfg.RPC.HTTPMaxConnsPerHost,
		})
		log.Info("rpc provider: ankr (live)", "endpoint", client.SafeEndpoint("bsc"))
		return client, client.Secrets()
	}
	log.Info("rpc provider: mock (set ANKR_API_KEY for live chain rpc)")
	return mockrpc.New("mock-rpc"), nil
}

// StartRPCBackground 启动能力探测与健康快照。
// withWorkers=true 只在 Worker 进程启用，避免多实例重复写快照。
func (i *Infra) StartRPCBackground(ctx context.Context, cfg *config.Config, log *slog.Logger, withWorkers bool) {
	if !cfg.RPC.Enabled || i.RPCGateway == nil {
		return
	}
	// 启动探测一次，结果落库并缓存（§21：Trace 不做频繁探测）
	if cfg.RPC.ProbeOnStart {
		go i.probeCapabilities(ctx, cfg, log)
	}
	if !withWorkers {
		return
	}
	go i.snapshotHealth(ctx, cfg, log)
}

func (i *Infra) probeCapabilities(ctx context.Context, cfg *config.Config, log *slog.Logger) {
	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for _, slug := range cfg.RPC.Chains {
		caps, err := i.RPCProvider.Capabilities(probeCtx, slug)
		if err != nil {
			log.Warn("rpc capability probe failed", "chain", slug, "err", rpc.Redact(err.Error()))
			continue
		}
		caps.Source = rpc.CapSourceProbe
		probeBudget := rpc.BudgetContext{Purpose: rpc.PurposeCanary, Priority: rpc.PriorityMaintenance}
		_, err = i.RPCGateway.Call(probeCtx, rpc.Request{
			Chain: slug, Method: "eth_blockNumber", Params: []any{}, Class: rpc.ClassChainHead,
			Priority: rpc.PriorityMaintenance, Budget: probeBudget, Consistency: rpc.ConsistencyForceRefresh,
			CachePolicy: rpc.CachePolicy{Disabled: true},
		})
		if err != nil {
			caps.StandardRPC = false
			log.Warn("rpc standard capability probe failed", "chain", slug, "err", rpc.Redact(err.Error()))
		} else {
			caps.StandardRPC = true
		}
		_, archiveErr := i.RPCGateway.Call(probeCtx, rpc.Request{
			Chain: slug, Method: "eth_getBalance",
			Params: []any{"0x0000000000000000000000000000000000000000", chain.AtBlock(1).String()},
			Class:  rpc.ClassBalance, BlockRef: chain.AtBlock(1), Priority: rpc.PriorityMaintenance,
			Budget: probeBudget, Consistency: rpc.ConsistencyForceRefresh, CachePolicy: rpc.CachePolicy{Disabled: true},
		})
		if archiveErr == nil {
			caps.Archive = true
		} else if rpc.IsClass(archiveErr, rpc.ClassArchiveRequired) || rpc.IsClass(archiveErr, rpc.ClassMethodUnsupported) {
			caps.Archive = false
		}
		batchResults, batchErr := i.RPCGateway.BatchCall(probeCtx, []rpc.Request{
			{Chain: slug, Method: "eth_blockNumber", Params: []any{}, Class: rpc.ClassChainHead, Priority: rpc.PriorityMaintenance, Budget: probeBudget, Consistency: rpc.ConsistencyForceRefresh, CachePolicy: rpc.CachePolicy{Disabled: true}},
			{Chain: slug, Method: "eth_chainId", Params: []any{}, Class: rpc.ClassNetwork, Priority: rpc.PriorityMaintenance, Budget: probeBudget, Consistency: rpc.ConsistencyForceRefresh, CachePolicy: rpc.CachePolicy{Disabled: true}},
		})
		caps.Batch = batchErr == nil && len(batchResults) == 2 && batchResults[0].Error == nil && batchResults[1].Error == nil
		i.RPCGateway.Capabilities().Set(i.RPCProvider.Name(), slug, caps, rpc.CapSourceProbe)
		log.Info("rpc capability probed", "chain", slug, "archive", caps.Archive,
			"batch", caps.Batch, "trace", caps.Trace, "debug", caps.Debug)
	}
	if err := i.Store.SaveProviderCapabilities(probeCtx, i.RPCGateway.Capabilities().All()); err != nil {
		log.Warn("persist rpc capabilities failed", "err", err)
	}
}

func (i *Infra) snapshotHealth(ctx context.Context, cfg *config.Config, log *slog.Logger) {
	interval := cfg.RPC.HealthSnapshot
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshots := i.RPCGateway.HealthSnapshot()
			if len(snapshots) == 0 {
				continue
			}
			writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := i.Store.SaveProviderHealth(writeCtx, snapshots); err != nil {
				log.Warn("persist rpc health failed", "err", err)
			}
			cancel()
		}
	}
}

var _ = model.APITypeRPC
