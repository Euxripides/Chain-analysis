// Package app：API 与 Worker 共用的启动引导。
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/canvas"
	"github.com/blockchain-platform/blockchain-platform/internal/capability"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/events"
	"github.com/blockchain-platform/blockchain-platform/internal/investigation"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/drpc"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/drpcmgmt"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/route"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	solprovider "github.com/blockchain-platform/blockchain-platform/internal/provider/solana"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/secret"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/tokenmeta"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
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

	// V1.5 RPC Infrastructure（§7）：业务只拿类型化服务，拿不到 Provider 与 Key。
	Chains         *chain.Registry
	RPCProvider    rpc.Provider
	RPCGateway     *rpc.Gateway
	RPCBatcher     *rpc.Batcher
	ChainRead      *chainread.Service
	Head           *chainread.HeadService
	Contract       *chainread.ContractService
	AddressType    *chainread.AddressTypeService
	BalanceRead    *chainread.BalanceService
	LogsRead       *chainread.LogsService
	Trace          *chaintrace.Service
	Capabilities   *capability.Registry
	Investigations *investigation.Service
	Canvases       *canvas.Service

	// Solana dRPC 接入：路由注册表、受管密钥解析、dRPC 数据面与管理同步。
	// 后台探针必须经 Routes 取运行时实现，而不是比较 deps.Prov.Name()。
	Routes         *route.Registry
	Secrets        *secret.Manager
	DRPC           *drpc.Client
	DRPCManagement *drpcmgmt.Service
}

// Bootstrap 连接全部依赖并执行迁移。
func Bootstrap(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Infra, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config invalid: %w", err)
	}
	// 迁移用连接账号（表 owner / 具备 DDL 权限）：只在启动阶段使用。
	// S7-SEC-01 R-07：应用运行账号（cfg.DatabaseURL）已改为非超级用户的受限登录角色，
	// 不再具备 CREATE TABLE / ALTER 权限，因此这里必须显式使用运维账号
	// （MIGRATION_DATABASE_URL，未配置时回落 DATABASE_URL）。
	owner, err := store.New(ctx, cfg.MigrationDatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := migrations.ApplyPostgres(ctx, owner.Pool.Raw()); err != nil {
		return nil, err
	}
	// 运行期降权：DB_RUNTIME_ROLE 非空时，业务语句统一按受限角色执行（§27.6 AUDIT-003）。
	st := owner
	if cfg.DatabaseRuntimeRole != "" {
		runtime, err := store.NewWithRuntimeRole(ctx, cfg.DatabaseURL, cfg.DatabaseRuntimeRole)
		if err != nil {
			return nil, err
		}
		st = runtime
		owner.Close()
		log.Info("postgres ready", "runtime_role", cfg.DatabaseRuntimeRole)
	} else {
		log.Warn("postgres ready without runtime role separation", "hint", "set DB_RUNTIME_ROLE=blockchain_app")
	}
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
			ConnectTimeout:      cfg.Ankr.ConnectTimeout,
			TLSHandshakeTimeout: cfg.Ankr.TLSHandshakeTimeout,
			BodyIdleTimeout:     cfg.Ankr.BodyIdleTimeout,
		}, usageHook)
		log.Info("provider: ankr (live)")
	} else {
		prov = ankr.NewMock(cfg.Ankr.MaxPageSize, usageHook)
		log.Info("provider: ankr-mock (ANKR_MODE=mock)", "hint", "set ANKR_API_KEY and ANKR_MODE=live for real data")
	}
	// Solana 的取数路径有两条：
	//   1) dRPC（Data API + Solana JSON-RPC）—— 新增主通道；
	//   2) 既有标准 Solana JSON-RPC（Ankr Solana 端点）—— 回退通道，也是 CPI 调用树
	//      （internal_transactions）的唯一来源。
	// 运行期用 Runtime Route Registry 解析，未登记的链继续回落 Ankr。
	secrets := secret.NewManager(secret.ManagerOptions{FilePath: cfg.DRPC.ProviderSecretsFilePath(), Log: log})
	routes := route.NewRegistry(log)
	routes.SetTronRouter(func(dataset string) (string, string) {
		key := cfg.Tron.ProviderForDataset(dataset)
		return key, tronChannelFor(key)
	})

	var solanaLegacy provider.Provider
	if cfg.Solana.Enabled {
		if solanaProvider := solprovider.New(solprovider.Options{
			Endpoint: cfg.Solana.Endpoint, APIKey: cfg.Solana.APIKey,
			MaxPageSize: cfg.Solana.MaxPageSize, Concurrency: cfg.Solana.Concurrency,
			RequestTimeout: cfg.Solana.RequestTimeout, UsageHook: usageHook, Log: log,
		}, usageHook); solanaProvider != nil {
			solanaLegacy = solanaProvider
			// 同时登记 "solana-rpc"：数据库里的既有策略行用的就是这个名字，
			// 两个 key 指向同一个实现，避免"策略行指向未注册 Provider"。
			routes.Register("solana", "Solana JSON-RPC (legacy)", []string{route.ChannelSolanaRPC}, solanaProvider)
			routes.Register("solana-rpc", "Solana JSON-RPC", []string{route.ChannelSolanaRPC}, solanaProvider)
			log.Info("provider: solana (standard json-rpc)",
				"datasets", []string{"transactions", "token_transfers", "internal_transactions"})
		}
	}

	var drpcClient *drpc.Client
	if cfg.DRPC.Enabled {
		drpcClient = drpc.New(drpc.Options{
			Enabled: cfg.DRPC.Enabled, Tier: cfg.DRPC.Tier, BaseURL: cfg.DRPC.BaseURL,
			RuntimeKeyRef: cfg.DRPC.RuntimeKeyRef, StatisticsTokenRef: cfg.DRPC.StatisticsTokenRef,
			KeysTokenRef: cfg.DRPC.KeysTokenRef, Secrets: secrets,
			HistoryPageSize: cfg.DRPC.HistoryPageSize, MaxTxVersion: cfg.DRPC.MaxTxVersion,
			RPCTimeout: cfg.DRPC.RPCTimeout, DataTimeout: cfg.DRPC.DataTimeout,
			SlotTimeSearchLimit: cfg.DRPC.SlotTimeSearchLimit, SlotTimeMargin: cfg.DRPC.SlotTimeMargin,
			UnknownTierCUPerMinute: cfg.DRPC.UnknownTierCUPerMinute,
			QuotaWarnPercent:       cfg.DRPC.QuotaWarnPercent, QuotaSafeOnlyPercent: cfg.DRPC.QuotaSafeOnlyPercent,
			QuotaStopPercent: cfg.DRPC.QuotaStopPercent, BalanceWarn: cfg.DRPC.BalanceWarn,
			ManagementStaleAfter:    cfg.DRPC.ManagementStaleAfter,
			ManagementSafeOnlyAfter: cfg.DRPC.ManagementSafeOnlyAfter,
			// 请求模式由调度器经 context 下传（provider.WithRequestedMode）。
			ModeResolver:  provider.RequestedModeFrom,
			ProfileSource: routeProfileAdapter{reg: routes},
			UsageHook:     usageHook, Log: log,
		}, usageHook)
		routes.Register(route.ProviderDRPC, "dRPC Solana",
			[]string{route.ChannelDataAPI, route.ChannelSolanaRPC}, drpcClient)
		log.Info("provider: drpc (solana data api + json-rpc)",
			"base_url", drpcMaskBaseURL(cfg.DRPC.BaseURL), "tier", cfg.DRPC.Tier,
			"key_ref", cfg.DRPC.RuntimeKeyRef)
	}

	// dRPC 管理同步：账户余额、当日 CU 与 Key 状态。两类管理 Token 独立解析，
	// 未配置时服务仍然启动（页面明确显示"额度同步未配置"），只是数据面按未知层级钳制。
	var drpcManagement *drpcmgmt.Service
	if drpcClient != nil {
		statRef := secrets.Describe(ctx, cfg.DRPC.StatisticsTokenRef)
		keysRef := secrets.Describe(ctx, cfg.DRPC.KeysTokenRef)
		managementConfigured := statRef.Configured || keysRef.Configured
		keyStatus := secrets.Describe(ctx, cfg.DRPC.RuntimeKeyRef)
		drpcManagement = drpcmgmt.New(drpcmgmt.Options{
			Enabled:      managementConfigured,
			BaseURL:      cfg.DRPC.ManagementBaseURL,
			Secrets:      secrets,
			SyncInterval: cfg.DRPC.ManagementSyncInterval,
			StaleAfter:   cfg.DRPC.ManagementStaleAfter, SafeOnlyAfter: cfg.DRPC.ManagementSafeOnlyAfter,
			HTTPTimeout:           cfg.DRPC.RPCTimeout,
			StatisticsTokenRef:    cfg.DRPC.StatisticsTokenRef,
			KeysTokenRef:          cfg.DRPC.KeysTokenRef,
			RuntimeKeyFingerprint: keyStatus.Fingerprint,
			OnSnapshot: func(snap drpcmgmt.Snapshot) {
				// 额度闸门跨通道共享：Data API 与 JSON-RPC 同属一个账户，
				// 任一侧把 CU 用尽都必须同时拦住另一侧。
				drpcClient.SetQuotaSnapshot(drpcQuotaFromSnapshot(snap))
			},
			Log: log,
		}, st, log)
		go drpcManagement.Run(ctx)
		log.Info("drpc management sync configured",
			"statistics_token", statRef.Configured, "keys_token", keysRef.Configured,
			"interval_seconds", int(cfg.DRPC.ManagementSyncInterval/time.Second))
	}

	// 数据库策略行是路由的事实来源；key 需要归一化，因为历史行用的是
	// 'ankr-multichain' / 'tron-grid' / 'solana-rpc'，而运行时注册的是 'ankr' / 'trongrid' / 'drpc'。
	//
	// 注意这里**先读原始行、再按 dRPC 可用性投影**：0075 迁移已把 solana:* 策略指向 drpc
	// （这是"配置事实"），但运行期 dRPC 没有可用密钥时必须投影回旧通道，
	// 否则策略行会指向一个拒绝服务的 Provider，Solana 下载直接失败。
	var rawPolicies []route.PolicyRow
	if policies, perr := st.RoutingPoliciesForRouting(ctx); perr != nil {
		log.Warn("routing policies load failed; falling back to builtin routes", "err", perr)
	} else {
		rawPolicies = policies
		log.Info("routing policies loaded", "count", len(policies))
	}
	applyPolicies := func(drpcReady bool) {
		routes.LoadPolicies(projectSolanaPolicies(rawPolicies, drpcReady))
	}

	// Solana 路由按"密钥是否真的可解析"动态开关（开发文档 §12 的部署顺序）：
	// 部署时先开着 DRPC_ENABLED、Key 随后在后台录入 —— 在那之前必须继续走旧通道，
	// 而不是让所有 Solana 下载立刻失败。10 秒重估一次，与 dRPC 客户端的密钥缓存同量级。
	var lastRouteKey string
	solanaRouteGate := func(ctx context.Context) {
		key, ok := "",
			false
		if drpcClient != nil && drpcClient.Configured(ctx) {
			// 密钥"能解析"不等于"能用"：录入错误的 Key 时数据面会 401，
			// 此时 Health 进入 OPEN。继续把新任务路由到 dRPC 只会让它们全部失败，
			// 因此熔断打开期间退回旧通道，Key 修正后自动恢复。
			if health := drpcClient.Health(ctx); health.State != provider.HealthOpen {
				key, ok = route.ProviderDRPC, true
			}
		}
		if !ok && solanaLegacy != nil {
			key, ok = "solana-rpc", true
		}
		routes.SetSolanaRoute(ok, key)
		applyPolicies(key == route.ProviderDRPC)
		model.SetSolanaDataAPI(key == route.ProviderDRPC)
		if key != lastRouteKey {
			lastRouteKey = key
			// 后台录入 Key 后不需要重启，这条日志就是"已生效"的证据。
			log.Info("solana route resolved", "provider", key, "drpc_configured", drpcClient != nil && drpcClient.Configured(ctx))
		}
	}
	solanaRouteGate(ctx)
	if drpcClient != nil {
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					solanaRouteGate(ctx)
				}
			}
		}()
	}

	if cfg.Tron.Enabled {
		needsAnkr, needsTronGrid := false, false
		for _, dataset := range []string{"transactions", "token_transfers", "internal_transactions"} {
			switch cfg.Tron.ProviderForDataset(dataset) {
			case "ankr":
				needsAnkr = true
			case "trongrid":
				needsTronGrid = true
			}
		}
		if cfg.Tron.TRC20AnkrFallbackEnabled {
			needsAnkr = true
		}
		for _, capability := range []string{"receipt", "tx_detail"} {
			switch cfg.Tron.ProviderForCapability(capability) {
			case "ankr":
				needsAnkr = true
			case "trongrid":
				needsTronGrid = true
			}
		}
		var tronAnkr, tronGridTransactions, tronGridTRC20 provider.Provider
		if needsAnkr {
			client, tronErr := tron.NewAnkr(tron.AnkrOptions{APIKey: cfg.Ankr.APIKey,
				RequestTimeout: cfg.Tron.RequestTimeout, ConnectTimeout: cfg.Tron.ConnectTimeout,
				MaxRetries: cfg.Tron.MaxRetries, Concurrency: cfg.Tron.AnkrConcurrency,
				LogInitialSpan: cfg.Tron.LogInitialSpan, LogMinSpan: cfg.Tron.LogMinSpan, LogMaxSpan: cfg.Tron.LogMaxSpan,
				BlockSpan: cfg.Tron.BlockSpan, ReceiptSpan: cfg.Tron.ReceiptSpan, MaxScanBlocks: cfg.Tron.MaxScanBlocks,
				MaxTxAutoScan: cfg.Tron.MaxTxAutoScan, MaxInternalAutoScan: cfg.Tron.MaxInternalAutoScan,
				TimeBlockCacheTTL: cfg.Tron.TimeBlockCacheTTL, TimeBlockProbeRetry: cfg.Tron.TimeBlockProbeRetry,
				ReceiptCacheMaxEntries: cfg.Tron.ReceiptCacheMaxEntries, ReceiptCacheTTL: cfg.Tron.ReceiptCacheTTL,
				ReceiptInitialConcurrency: cfg.Tron.ReceiptInitialConcurrency, ReceiptMaxConcurrency: cfg.Tron.ReceiptMaxConcurrency,
				ReceiptAdaptiveEnabled: cfg.Tron.ReceiptAdaptiveEnabled, ReceiptAdaptiveHardMax: cfg.Tron.ReceiptAdaptiveHardMax,
				TRC20AdaptiveEnrichment: cfg.Tron.TRC20AdaptiveEnrichment, TRC20LogEnrichmentRatio: cfg.Tron.TRC20LogEnrichmentRatio,
				TRC20EnrichLogMaxWindows: cfg.Tron.TRC20EnrichLogMaxWindows, TRC20EnrichLogSpan: cfg.Tron.TRC20EnrichLogSpan,
				TRC20BatchReceipts: cfg.Tron.TRC20BatchReceipts}, usageHook, log)
			if tronErr != nil {
				return nil, fmt.Errorf("Ankr TRON init: %w", tronErr)
			}
			probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			caps, probeErr := client.Probe(probeCtx)
			cancel()
			if probeErr != nil {
				return nil, fmt.Errorf("Ankr TRON capability probe: %w", probeErr)
			}
			tronAnkr = client
			log.Info("provider: Ankr TRON Node API enabled", "endpoint", "https://rpc.ankr.com/tron_jsonrpc/***", "capabilities", caps)
		}
		if needsTronGrid {
			accountPacer := tron.NewRatePacer(cfg.Tron.TronGridMinRequestInterval)
			newGrid := func(pageSize, initial, normal, maximum int, timeout time.Duration, retries int, identity tron.TRC20IdentityResolver) (provider.Provider, error) {
				return tron.New(tron.Options{BaseURL: cfg.Tron.BaseURL, APIKey: cfg.Tron.APIKey,
					RequestTimeout: timeout, ConnectTimeout: cfg.Tron.ConnectTimeout,
					MaxRetries: retries, InitialConcurrency: initial, NormalConcurrency: normal, MaxConcurrency: maximum,
					PageSize: pageSize, MinRequestInterval: cfg.Tron.TronGridMinRequestInterval,
					DailyRequestBudget: cfg.Tron.TronGridDailyRequestBudget, RatePacer: accountPacer, IdentityResolver: identity,
					TRC20PrefetchPages: cfg.Tron.TRC20PrefetchPages, TRC20PageQueueMax: cfg.Tron.TRC20PageQueueMax}, usageHook, log)
			}
			txClient, tronErr := newGrid(cfg.Tron.TronGridTXPageSize, cfg.Tron.TronGridTXInitialConcurrency, cfg.Tron.TronGridTXNormalConcurrency, cfg.Tron.TronGridTXMaxConcurrency, cfg.Tron.TronGridTXRequestTimeout, cfg.Tron.TronGridTXMaxRetries, nil)
			if tronErr != nil {
				return nil, fmt.Errorf("TronGrid transactions init: %w", tronErr)
			}
			trc20Client, tronErr := newGrid(cfg.Tron.TronGridTRC20PageSize, cfg.Tron.TronGridTRC20InitialConcurrency, cfg.Tron.TronGridTRC20NormalConcurrency, cfg.Tron.TronGridTRC20MaxConcurrency, cfg.Tron.TronGridTRC20RequestTimeout, cfg.Tron.TronGridTRC20MaxRetries, newTRC20IdentityResolver(ch, tronAnkr, log, cfg.Tron.TRC20LocalIdentityEnabled))
			if tronErr != nil {
				return nil, fmt.Errorf("TronGrid TRC20 init: %w", tronErr)
			}
			tronGridTransactions, tronGridTRC20 = txClient, trc20Client
			log.Info("provider: TronGrid dataset pools enabled", "base_url", cfg.Tron.BaseURL, "api_key_configured", cfg.Tron.APIKey != "", "tx_max_concurrency", cfg.Tron.TronGridTXMaxConcurrency, "trc20_max_concurrency", cfg.Tron.TronGridTRC20MaxConcurrency)
		}
		defaultTron := tronAnkr
		if defaultTron == nil {
			defaultTron = tronGridTransactions
		}
		var trc20 provider.Provider = tronGridTRC20
		if cfg.Tron.TRC20AnkrFallbackEnabled {
			resolver, ok := tronAnkr.(tron.RangeResolver)
			if !ok {
				return nil, fmt.Errorf("Ankr TRON client does not implement range resolution")
			}
			trc20 = &tron.HybridProvider{Primary: tronGridTRC20, Fallback: tronAnkr, Resolver: resolver,
				Options: tron.HybridOptions{FallbackEnabled: true, MaxWindows: cfg.Tron.TRC20FallbackMaxWindows, MaxWindowSpan: cfg.Tron.LogMaxSpan}}
		}
		prov = &tron.Dispatcher{EVM: prov, Tron: &tron.DatasetRouter{
			Default: defaultTron, Transactions: tronGridTransactions, TRC20: trc20, Internal: tronAnkr,
			ReceiptProvider: tronAnkr, TxDetailProvider: tronAnkr,
		}}
	}

	// 路由兜底、策略装载与最终装配都必须在 TRON 包装之后：
	// TRON 的 Dispatcher 才是非 Solana 链的最终取数实现，早于它注册 fallback 会把
	// TRON 请求直接送回 Ankr，绕过整个 TRON 数据集路由。
	routes.SetFallback(prov)
	routes.SetProfileSource(st)
	// 下载引擎与后台探针共用同一个注册表实例：
	// 后台"测试连接"取到的实现与真正跑下载的实现因此不可能不一致。
	prov = routes

	capabilities := capability.NewRegistry()
	capability.RegisterBuiltins(capabilities)
	infra := &Infra{Store: st, CH: ch, Raw: raw, Bus: bus, Prov: prov,
		Capabilities: capabilities, Investigations: investigation.New(st.Pool), Canvases: canvas.New(st.Pool)}
	if err := infra.initMethod(ctx, cfg, log); err != nil {
		return nil, err
	}
	// V1.5：RPC 基础设施必须在依赖它的业务服务之前装配（§248）
	if err := infra.initRPC(ctx, cfg, log); err != nil {
		return nil, err
	}
	if cfg.TokenMetadata.Enabled {
		// V1.5 §234：Token Metadata 必须走 ContractReadService → RPCGateway，
		// 不允许保留第二套 raw http RPC client。
		var resolver tokenmeta.RPCResolver
		if infra.Contract != nil {
			resolver = tokenmeta.NewContractResolver(infra.Contract)
		} else {
			log.Warn("token metadata rpc resolver disabled: rpc infrastructure is not available")
		}
		infra.TokenMeta = tokenmeta.New(st, ch, resolver, log)
		infra.TokenMeta.ConfigureLogo(raw, tokenmeta.LogoConfig{Enabled: cfg.TokenMetadata.LogoEnabled, MaxBytes: cfg.TokenMetadata.LogoMaxBytes, MaxWidth: cfg.TokenMetadata.LogoMaxWidth, MaxHeight: cfg.TokenMetadata.LogoMaxHeight})
		if err := infra.TokenMeta.EnsureVerifiedRegistry(ctx); err != nil {
			log.Warn("verified token registry seed failed", "err", err)
		}
		infra.TokenBackfill = &tokenmeta.BackfillRunner{Service: infra.TokenMeta, BatchSize: cfg.TokenMetadata.BackfillBatchSize, Interval: cfg.TokenMetadata.WorkerInterval, BatchIdle: cfg.TokenMetadata.BatchIdle, MaxRetries: cfg.TokenMetadata.RPCMaxRetries, RPCWorkers: cfg.TokenMetadata.RPCWorkers, RPCMinInterval: cfg.TokenMetadata.RPCMinInterval, Log: log}
	}
	return infra, nil
}

// StartOutboxBackground bridges transactional domain events to JetStream.
func (i *Infra) StartOutboxBackground(ctx context.Context, log *slog.Logger) {
	go events.RunOutbox(ctx, i.Store.Pool, i.Bus, log)
}

// StartAuditBackground 启动 §28.5 S3-BE-02 要求的审计后台任务：
// 定时复算哈希链、按日生成锚点并归档到 MinIO 审计桶、采集 Outbox 积压指标。
//
// 只在 worker 进程启动：归档属于后台批处理，放在 API 进程会随 API 重启而抖动，
// 且多副本同时跑会产生重复归档（虽有幂等保护，但没必要）。
func (i *Infra) StartAuditBackground(ctx context.Context, env string, log *slog.Logger) {
	if i.Store == nil || i.Store.Pool == nil {
		return
	}
	var store audit.ObjectPutter
	if i.Raw != nil {
		store = i.Raw
	} else {
		log.Warn("audit archive runs without object storage; anchors will only be recorded in PostgreSQL")
	}
	archiver := audit.NewArchiver(i.Store.Pool, store, env, log)
	if i.Raw != nil {
		// 锚点在 0055 起不可改写，因此归档器需要能回读对象以证明归档件未被替换。
		archiver = archiver.WithObjectGetter(i.Raw)
	}
	audit.RegisterReporter(observability.NewAuditReporter())
	go audit.RunScheduler(ctx, archiver, log, audit.DefaultSchedulerOptions())
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
		// TRON's Ankr node client records the true logical call count but older
		// call sites did not attach pricing. Normalize it here so every live
		// request reaches provider_usage with an auditable estimate.
		if u.Provider == "ankr" && u.UsageType != "trc20_enrichment_summary" && u.CostUSD <= 0 {
			calls := u.MethodCalls
			if calls <= 0 {
				calls = 1
			}
			u.Credits = 700 * float64(calls)
			u.CostUSD = 0.00007 * float64(calls)
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := st.InsertUsage(ctx, u); err != nil {
				log.Error("insert usage failed", "err", err)
			}
		}()
	}
}

// ---- dRPC / 路由接线辅助 ----

// routeProfileAdapter 把 route.Registry 的档位读取适配成 drpc.ProfileSource。
//
// 为什么需要适配器：drpc 不能 import route（route 已 import provider，drpc 也 import provider，
// 但真正的原因是 route.Registry 需要引用 drpc.Client 才能注册，直接互引会成环）。
// 两个 Profile 结构字段一一对应，这里逐字段拷贝，不做任何默认值填充 —— 缺失即返回 false，
// 由 drpc 自己回退内置默认档，避免"零值被当成有效档位"。
type routeProfileAdapter struct{ reg *route.Registry }

func (a routeProfileAdapter) Profile(ctx context.Context, providerKey, chain, dataset, operation, mode string) (drpc.Profile, bool) {
	p, ok := a.reg.Profile(ctx, providerKey, chain, dataset, operation, mode)
	if !ok {
		return drpc.Profile{}, false
	}
	return drpc.Profile{
		ProviderKey: p.ProviderKey, Chain: p.Chain, Dataset: p.Dataset,
		OperationClass: p.OperationClass, Mode: p.Mode,
		ShardConcurrency: p.ShardConcurrency, RequestConcurrency: p.RequestConcurrency,
		RequestsPerMinute: p.RequestsPerMinute, RequestsPerSecond: p.RequestsPerSecond,
		CUPerMinute: p.CUPerMinute, PageSize: p.PageSize, BatchSize: p.BatchSize,
		RequestWeight: p.RequestWeight, RequestTimeoutSeconds: p.RequestTimeoutSeconds,
		HardCeiling: p.HardCeiling, Revision: p.Revision, VerifiedAt: p.VerifiedAt,
		VerificationSource: p.VerificationSource,
	}, true
}

// policyProviderAliases 把数据库里历史 Provider Key 映射到运行时注册的 Key。
//
// routing_policies 的既有行（0051/0072 种子）用 'ankr-multichain'、'tron-grid'、'solana-rpc'，
// 而运行时注册名是 'ankr'、'trongrid'、'drpc'/'solana-rpc'。不归一化会让策略行指向一个
// 未注册 Provider，路由随之告警并回退 —— 表面能跑，实际"后台改了路由没生效"。
var policyProviderAliases = map[string]string{
	"ankr-multichain": "ankr",
	"ankr":            "ankr",
	"ankr-mock":       "ankr-mock",
	"tron-grid":       "trongrid",
	"trongrid":        "trongrid",
	"solana-rpc":      "solana-rpc",
	"drpc":            route.ProviderDRPC,
}

// normalizePolicyProviders 归一化策略行的主 Provider Key，未知值原样保留（fail closed）。
func normalizePolicyProviders(rows []route.PolicyRow) []route.PolicyRow {
	out := make([]route.PolicyRow, 0, len(rows))
	for _, row := range rows {
		if mapped, ok := policyProviderAliases[strings.ToLower(strings.TrimSpace(row.PrimaryProvider))]; ok {
			row.PrimaryProvider = mapped
		}
		out = append(out, row)
	}
	return out
}

// tronChannelFor 把 TRON 的 Provider Key 映射到 API 通道名（与 provider_performance_profiles 口径一致）。
func tronChannelFor(providerKey string) string {
	switch strings.ToLower(strings.TrimSpace(providerKey)) {
	case "trongrid":
		return route.ChannelTronGrid
	case "ankr":
		return route.ChannelAnkrTronNode
	default:
		return ""
	}
}

// drpcMaskBaseURL 只用于日志：base URL 本身不含密钥，但仍统一去掉可能的路径细节。
func drpcMaskBaseURL(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return raw
}

// drpcQuotaFromSnapshot 把管理同步结果投影成数据面额度闸门的输入。
//
// 只映射闸门真正需要的字段；余额用字符串（numeric(38,18) 的精度不能被 float 吃掉），
// 未知值保持 nil —— 闸门把 nil 解释为"该项未知"，而不是"额度为 0"。
func drpcQuotaFromSnapshot(snap drpcmgmt.Snapshot) drpc.QuotaSnapshot {
	return drpc.QuotaSnapshot{
		Tier: snap.Tier, Balance: snap.BalanceString(), BalanceUnit: snap.BalanceUnit,
		CUSpentToday: snap.CUSpentToday, CUDayLimit: snap.CUDayLimit,
		RateLimit: snap.RateLimit, SyncedAt: snap.SyncedAt,
		KeyEnabled: snap.KeyEnabled, Source: snap.Source,
	}
}

// projectSolanaPolicies 按 dRPC 运行时就绪情况投影 Solana 策略行。
//
// 背景（本次接入最容易踩的坑）：0075 迁移把 solana:* 策略行的 primary_provider 改成 drpc，
// 这是「配置事实」。但部署顺序是「先上代码、后录 Key」，在 Key 到位之前 dRPC 无法服务；
// 如果直接按策略行路由，Solana 下载会全部失败 —— 比不接 dRPC 还差。
// 因此运行期必须按「密钥是否真的可解析」投影回旧通道，Key 到位后自动切回 drpc。
//
// 只改 Provider，不动 api_channel / operation_class / revision：
// 那两个字段描述的是数据集语义，与由谁承载无关。
func projectSolanaPolicies(raw []route.PolicyRow, drpcReady bool) []route.PolicyRow {
	rows := make([]route.PolicyRow, 0, len(raw))
	for _, row := range raw {
		if !drpcReady && strings.EqualFold(strings.TrimSpace(row.Chain), route.ChainSolana) {
			row.PrimaryProvider = "solana-rpc"
		}
		rows = append(rows, row)
	}
	return normalizePolicyProviders(rows)
}
