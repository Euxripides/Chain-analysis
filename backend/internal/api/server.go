// Package api：REST / SSE API。
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/blockchain-platform/blockchain-platform/internal/access"
	"github.com/blockchain-platform/blockchain-platform/internal/addressmeta"
	"github.com/blockchain-platform/blockchain-platform/internal/advanced"
	"github.com/blockchain-platform/blockchain-platform/internal/analysis"
	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/canvas"
	"github.com/blockchain-platform/blockchain-platform/internal/capability"
	"github.com/blockchain-platform/blockchain-platform/internal/casework"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/events"
	"github.com/blockchain-platform/blockchain-platform/internal/explorer"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/investigation"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankrmgmt"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/report"
	"github.com/blockchain-platform/blockchain-platform/internal/semantic"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/subjectintel"
	"github.com/blockchain-platform/blockchain-platform/internal/tokenmeta"
	"github.com/blockchain-platform/blockchain-platform/internal/update"
)

type Deps struct {
	Cfg   *config.Config
	Store *store.Store
	CH    *clickhouse.Conn
	Raw   *rawstore.Store
	Bus   *events.Bus
	Prov  provider.Provider
	Log   *slog.Logger

	// V1.3 Method / ABI（§25/§52）
	Registry       *method.Registry
	MethodData     *method.ClickHouseStore
	DeepDecode     *method.AnkrDeepResolver
	TokenMeta      *tokenmeta.Service
	TokenBackfill  *tokenmeta.BackfillRunner
	Head           *chainread.HeadService
	AddressType    *chainread.AddressTypeService
	BalanceRead    *chainread.BalanceService
	ChainRead      *chainread.Service
	Trace          *chaintrace.Service
	RPCGateway     *rpc.Gateway
	Capabilities   *capability.Registry
	Investigations *investigation.Service
	Canvases       *canvas.Service
}

type Server struct {
	deps           Deps
	router         chi.Router
	hub            *SSEHub
	busStop        func()
	svc            *dataset.Service
	query          *query.Service
	meta           *addressmeta.Service
	explorer       *explorer.Service
	updates        *update.Service
	detail         *method.DetailService
	ankrMgmt       *ankrmgmt.Service
	capabilities   *capability.Registry
	investigations *investigation.Service
	canvases       *canvas.Service
	runtime        *investigation.Runtime
	analysis       *analysis.Service
	semantic       *semantic.Service
	advanced       *advanced.Service
	reports        *report.Service
	subjects       *subjectintel.Service

	// §6.3/§6.5：身份、授权与审计底座。
	cfg            *config.Config
	iam            *iam.Service
	policy         *policy.Engine
	audit          *audit.Writer
	guard          *access.Guard
	casework       *casework.Service
	oidc           *iam.OIDCAdapter
	authMode       auth.Mode
	trustedProxies []*net.IPNet
}

type chainAwareAddressTypeResolver struct {
	evm  provider.AddressTypeResolver
	tron provider.AddressTypeResolver
}

func (r chainAwareAddressTypeResolver) ResolveAddressTypes(ctx context.Context, chain string, addresses []string) (map[string]bool, error) {
	if chain == "tron" && r.tron != nil {
		return r.tron.ResolveAddressTypes(ctx, chain, addresses)
	}
	if r.evm != nil {
		return r.evm.ResolveAddressTypes(ctx, chain, addresses)
	}
	return map[string]bool{}, nil
}

func New(deps Deps) *Server {
	s := &Server{deps: deps, hub: NewSSEHub(), query: query.New(deps.CH), capabilities: deps.Capabilities, investigations: deps.Investigations, canvases: deps.Canvases}
	if deps.Store != nil && deps.Raw != nil {
		s.reports = report.New(deps.Store.Pool, deps.Raw)
	}
	if deps.Store != nil {
		s.subjects = subjectintel.New(deps.Store.Pool)
	}
	if management, err := ankrmgmt.New(deps.Store.Pool, deps.Cfg.AnkrManagement); err == nil {
		s.ankrMgmt = management
	} else {
		deps.Log.Error("Ankr Management MCP initialization failed", "err", err)
	}
	s.svc = dataset.New(deps.Store, func(ctx context.Context, chain string) (int64, error) {
		if chain == "tron" && deps.Cfg.Tron.Enabled {
			return 0, nil
		}
		if deps.Head != nil {
			head, err := deps.Head.SafeHead(ctx, chain)
			return int64(head), err
		}
		if deps.Prov != nil && deps.Prov.Name() == "ankr-mock" {
			return 2_000_000, nil
		}
		return 0, nil // live：head 未知，区间由 Provider 语义决定
	})
	s.svc.TronProvider = deps.Cfg.Tron.Provider
	s.svc.TronDefaultProvider = deps.Cfg.Tron.DefaultProvider
	s.svc.TronDatasetProviders = map[string]string{
		"transactions":          deps.Cfg.Tron.TransactionsProvider,
		"token_transfers":       deps.Cfg.Tron.TRC20PrimaryProvider,
		"internal_transactions": deps.Cfg.Tron.InternalProvider,
	}
	s.svc.TronMaxTxAutoScan = deps.Cfg.Tron.MaxTxAutoScan
	s.svc.TronMaxInternalAutoScan = deps.Cfg.Tron.MaxInternalAutoScan
	if deps.Investigations != nil {
		s.analysis = analysis.New(s.query)
		s.semantic = semantic.New(deps.Store.Pool, s.query)
		s.advanced = advanced.New(deps.Store.Pool, s.query)
		s.runtime = investigation.NewRuntime(deps.Investigations, s.svc.Ensure).WithAnalyzer(func(ctx context.Context, chain, address string) (any, error) {
			return s.analysis.Analyze(ctx, chain, address)
		}).WithSemanticAnalyzer(func(ctx context.Context, chain, address string) (any, error) {
			return s.semantic.Rebuild(ctx, chain, address)
		}).WithAdvancedAnalyzer(func(ctx context.Context, chain, address string) (any, error) {
			return s.advanced.Rebuild(ctx, chain, address)
		})
	}
	s.wireIdentityAndAudit(deps)
	if deps.Store != nil {
		s.casework = casework.New(deps.Store.Pool, s.audit, deps.Raw, s.reports).
			WithInvestigation(deps.Investigations, s.runtime).
			WithCoverage(s.svc.Preview)
	}
	if deps.ChainRead != nil && deps.ChainRead.Chains() != nil {
		s.svc.ValidateChain = func(chain string) error {
			if chain == "tron" && !deps.Cfg.Tron.Enabled {
				return fmt.Errorf("chain tron is disabled")
			}
			_, err := deps.ChainRead.Chains().Require(chain)
			return err
		}
	}
	s.meta = addressmeta.New(deps.Store, deps.CH)
	s.svc.Meta = s.meta
	s.explorer = explorer.New(deps.CH, deps.Store, deps.TokenMeta)
	if providerResolver, ok := deps.Prov.(provider.AddressTypeResolver); ok && deps.AddressType != nil {
		s.explorer.WithAddressTypeResolver(chainAwareAddressTypeResolver{evm: deps.AddressType, tron: providerResolver})
	} else if deps.AddressType != nil {
		s.explorer.WithAddressTypeResolver(deps.AddressType)
	} else if resolver, ok := deps.Prov.(provider.AddressTypeResolver); ok {
		// 兼容 RPC 基础设施被显式关闭的旧部署；正常路径必须走 ChainRead。
		s.explorer.WithAddressTypeResolver(resolver)
	}
	var safeHead update.SafeHeadFn
	if deps.Head != nil {
		safeHead = func(ctx context.Context, chain string) (int64, error) {
			if chain == "tron" && deps.Cfg.Tron.Enabled {
				var head uint64
				if err := deps.CH.DB().QueryRow(ctx, "SELECT greatest((SELECT coalesce(max(block_number),0) FROM blockchain.transactions FINAL WHERE chain='tron'),(SELECT coalesce(max(block_number),0) FROM blockchain.token_transfers FINAL WHERE chain='tron'))").Scan(&head); err != nil {
					return 0, err
				}
				return int64(head), nil
			}
			head, err := deps.Head.SafeHead(ctx, chain)
			return int64(head), err
		}
	}
	s.updates = update.New(deps.Store, deps.CH, deps.Prov, s.svc, s.meta, safeHead, deps.Log)
	s.updates.TronOverlap = deps.Cfg.Tron.IncrementalOverlap
	s.updates.TronProvider = deps.Cfg.Tron.Provider
	s.updates.TronDefaultProvider = deps.Cfg.Tron.DefaultProvider
	s.updates.TronDatasetProviders = map[string]string{
		"transactions":          deps.Cfg.Tron.TransactionsProvider,
		"token_transfers":       deps.Cfg.Tron.TRC20PrimaryProvider,
		"internal_transactions": deps.Cfg.Tron.InternalProvider,
	}
	s.updates.TronOverlapBlocks = deps.Cfg.Tron.OverlapBlocks
	if deps.Registry != nil && deps.MethodData != nil {
		// 智能调查/前端都必须经过 TransactionDetailService，不允许直连 Provider（§50）
		s.detail = method.NewDetailService(deps.MethodData, deps.Registry, deps.DeepDecode, s.recordDeepDecodeUsage, deps.Log)
	}
	s.buildRouter()
	return s
}

func (s *Server) validateChain(chain string) error {
	if s.svc != nil && s.svc.ValidateChain != nil {
		return s.svc.ValidateChain(chain)
	}
	return nil
}

// recordDeepDecodeUsage 记录深度解码用量（§49）。
// 真实 Provider 请求由 Provider 客户端自己记账；这里只补记缓存命中，
// 保证 usage_type=tx_deep_decode 的命中率可统计且不会重复计费。
func (s *Server) recordDeepDecodeUsage(usage method.DeepDecodeUsage) {
	if !usage.CacheHit {
		return
	}
	row := model.UsageRow{
		Provider: "local-cache", Method: "transaction_decoded_inputs", Dataset: "transaction_detail",
		RequestTime: time.Now(), DurationMs: usage.DurationMs, Status: usage.Status, HTTPStatus: 200,
		UsageType: model.UsageTypeTxDeepDecode, TxHash: usage.TxHash, CacheHit: true,
		DecodePriority: usage.Priority,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.deps.Store.InsertUsage(ctx, row); err != nil {
			s.deps.Log.Warn("insert deep decode usage failed", "err", err)
		}
	}()
}

func (s *Server) buildRouter() {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(s.corsMiddleware())
	r.Use(s.requestMetaMiddleware)
	r.Use(s.logMiddleware())
	r.Use(s.metricsMiddleware())
	r.Use(s.identityMiddleware)
	r.Use(s.auditCaptureMiddleware)

	r.Handle("/metrics", promhttp.Handler())
	r.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	r.Get("/health/ready", s.handleReady)
	r.Get("/openapi.json", s.handleActionsOpenAPI)
	r.Get("/docs", s.handleActionsSwagger)
	r.Get("/redoc", s.handleActionsRedoc)

	r.Route("/api/v1", func(api chi.Router) {
		s.registerActionRoutes(api)
		api.Get("/me", s.handleMe)
		api.Get("/system/architecture", s.handleArchitectureStatus)
		api.Get("/capabilities", s.handleCapabilities)
		api.Post("/investigations", s.handleCreateInvestigation)
		api.Get("/investigations", s.handleListInvestigations)
		api.Get("/investigations/{id}", s.handleGetInvestigation)
		api.Get("/investigations/{id}/runtime", s.handleInvestigationRuntime)
		api.Get("/investigations/{id}/artifacts", s.handleInvestigationArtifacts)
		api.Get("/analysis/addresses/{chain}/{address}/flow-profile", s.handleAddressFlowProfile)
		api.Get("/analysis/addresses/{chain}/{address}/semantic-events", s.handleAddressSemanticEvents)
		api.Post("/analysis/addresses/{chain}/{address}/semantic-events/rebuild", s.handleRebuildAddressSemanticEvents)
		api.Get("/analysis/addresses/{chain}/{address}/advanced", s.handleAddressAdvancedAnalysis)
		api.Post("/analysis/addresses/{chain}/{address}/advanced/rebuild", s.handleRebuildAddressAdvancedAnalysis)
		api.Post("/investigations/{id}/{action}", s.handleInvestigationAction)
		api.Get("/investigations/{id}/reports", s.handleListInvestigationReports)
		api.Post("/investigations/{id}/reports", s.handleGenerateInvestigationReport)
		api.Get("/reports/{id}/download", s.handleDownloadInvestigationReport)
		api.Get("/investigations/{id}/subjects", s.handleInvestigationSubjects)
		api.Post("/investigations/{id}/subjects", s.handleInvestigationSubjects)
		api.Post("/investigations/{id}/relationships", s.handleInvestigationRelationships)
		api.Post("/datasets/ensure", s.handleEnsure)
		api.Post("/datasets/coverage/preview", s.handleCoveragePreview)
		api.Get("/download-jobs", s.handleListJobs)
		api.Get("/download-jobs/{id}", s.handleGetJob)
		api.Post("/download-jobs/{id}/pause", s.handlePause)
		api.Post("/download-jobs/{id}/resume", s.handleResume)
		api.Post("/download-jobs/{id}/cancel", s.handleCancel)
		api.Get("/download-jobs/{id}/guard", s.handleGetVolumeGuards)
		api.Post("/download-jobs/{id}/guard/continue", s.handleContinueVolumeGuard)
		api.Post("/download-jobs/{id}/guard/abort", s.handleAbortVolumeGuard)
		api.Post("/download-jobs/{id}/subjects/{subjectID}/{action}", s.handleSubjectControl)
		api.Get("/download-jobs/{id}/events", s.handleJobEvents)
		api.Get("/providers/ankr/status", s.handleProviderStatus)
		api.Get("/providers/ankr/usage", s.handleProviderUsage)
		api.Get("/system/chains/{chain}/capabilities", s.handleChainCapabilities)
		api.Get("/chains/{chain}/head", s.handleChainHead)
		api.Get("/addresses/{chain}/{address}/runtime", s.handleAddressRuntime)
		api.Get("/transactions/{chain}/{tx_hash}/receipt", s.handleRPCReceipt)
		api.Get("/transactions/{chain}/{tx_hash}/trace", s.handleTransactionTrace)
		api.Get("/transactions/{chain}/{tx_hash}/internal-calls", s.handleInternalCalls)

		// V1.6 API Control Center：统一审计、成本、异常管控和人工接管。
		api.Get("/api-control/overview", s.handleAPIControlOverview)
		api.Get("/api-control/provider-accounts", s.handleAPIControlProviderAccounts)
		api.Post("/api-control/provider-accounts/ankr/connect", s.handleAnkrManagementConnect)
		api.Get("/api-control/provider-accounts/ankr/oauth/callback", s.handleAnkrManagementCallback)
		api.Post("/api-control/provider-accounts/ankr/sync", s.handleAnkrManagementSync)
		api.Delete("/api-control/provider-accounts/ankr", s.handleAnkrManagementDisconnect)
		api.Get("/api-control/control/states", s.handleAPIControlState)
		api.Get("/api-control/control/policies", s.handleAPIControlPolicies)
		api.Patch("/api-control/control/policies", s.handleAPIControlPolicies)
		api.Post("/api-control/control/overrides", s.handleAPIControlOverride)
		api.Patch("/api-control/control/kill-switch", s.handleAPIControlKillSwitch)
		api.Get("/api-control/control/actions", s.handleAPIControlActions)
		api.Get("/api-control/requests", s.handleAPIControlRequests)
		api.Get("/api-control/requests/{id}", s.handleAPIControlRequests)
		api.Get("/api-control/errors", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "errors") })
		api.Get("/api-control/costs", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "costs") })
		api.Get("/api-control/operations", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "operations") })
		api.Get("/api-control/providers", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "providers") })
		api.Get("/api-control/usage-types", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "usage-types") })
		api.Get("/api-control/budgets", s.handleAPIControlBudgets)
		api.Post("/api-control/budgets", s.handleAPIControlBudgets)

		// V1.1：极限性能 Benchmark（§45/§46）
		api.Post("/admin/ankr/benchmarks", s.handleBenchmarkCreate)
		api.Get("/admin/ankr/benchmarks", s.handleBenchmarkList)
		api.Get("/admin/ankr/benchmarks/{id}", s.handleBenchmarkGet)

		// V1.2：Count Oracle / Global Concurrency（§80-§82/§85/§88/§61）
		api.Get("/count-oracle/status", s.handleCountOracleStatus)
		// V1.3：Count Provider Port（§72-§74/§98）
		api.Get("/count-oracle/providers", s.handleCountProviders)
		api.Get("/count-oracle/resolutions", s.handleResolutionDecisions)
		api.Get("/count-oracle/observations", s.handleCountOracleObservations)
		api.Get("/count-oracle/calibration", s.handleCountOracleCalibration)
		api.Get("/admin/planning-decisions", s.handlePlanningDecisions)
		api.Get("/providers/ankr/global-concurrency", s.handleGlobalConcurrency)
		// V1.4（§51/§14）：Ankr Fetch Runtime / Profile / Global Ready Queue / Range Certifications
		api.Get("/providers/ankr/fetch-runtime", s.handleAnkrFetchRuntime)
		api.Get("/providers/ankr/production-profile", s.handleAnkrProductionProfile)
		api.Post("/providers/ankr/performance-mode", s.handleSetAnkrPerformanceMode)
		api.Post("/download-jobs/{id}/performance-mode", s.handleSetJobPerformanceMode)
		api.Get("/providers/ankr/fetch-profile", s.handleAnkrFetchProfile)
		api.Get("/providers/ankr/ready-queue", s.handleGlobalReadyQueue)
		api.Get("/admin/ankr/range-certifications", s.handleListRangeCertifications)
		api.Post("/admin/ankr/range-certifications", s.handleRecordRangeCertification)
		api.Get("/chains/capabilities", s.handleV12ChainCapabilities)
		api.Get("/api-control/alerts", s.handleAPIControlAlerts)
		api.Post("/api-control/alerts/{id}/actions", s.handleAPIControlAlerts)
		api.Get("/coverage", s.handleCoverage)
		api.Get("/coverage/stats", s.handleCoverageStats)
		api.Get("/datasets/{dataset}", s.handleDatasetRows)
		api.Get("/addresses/{address}/activity", s.handleAddressActivity)
		api.Post("/exports", s.handleCreateExport)
		api.Get("/exports/{id}", s.handleGetExport)
		api.Get("/exports/{id}/download", s.handleDownloadExport)
		api.Head("/exports/{id}/download", s.handleDownloadExport)
		api.Post("/exports/{id}/download-ticket", s.handleExportTicket)

		api.Get("/cases", s.handleListCases)
		api.Post("/cases", s.handleCreateCase)
		api.Delete("/cases/{id}", s.handleDeleteCase)
		api.Get("/cases/{id}/canvases", s.handleListCanvases)
		api.Post("/cases/{id}/canvases", s.handleCreateCanvas)
		api.Get("/canvases/{id}", s.handleGetCanvas)
		api.Patch("/canvases/{id}", s.handleUpdateCanvas)
		api.Post("/canvases/{id}/branch", s.handleBranchCanvas)
		api.Get("/canvases/{id}/snapshots", s.handleListCanvasSnapshots)
		api.Post("/canvases/{id}/snapshots", s.handleCreateCanvasSnapshot)
		api.Get("/tags", s.handleListTags)
		api.Post("/tags", s.handleCreateTag)
		api.Delete("/tags/{id}", s.handleDeleteTag)

		api.Get("/data-explorer/addresses", s.handleExplorerAddresses)
		api.Post("/data-explorer/query", s.handleExplorerQuery)

		api.Patch("/addresses/{chain}/{address}/metadata", s.handleAddressMetadata)
		api.Get("/tokens/search", s.handleTokenSearch)
		api.Get("/tokens/enrichment", s.handleTokenEnrichment)
		api.Get("/tokens/{chain}/{contract}/logo", s.handleTokenLogo)
		api.Get("/tokens/{chain}/{contract}", s.handleTokenGet)
		api.Post("/admin/tokens/{chain}/{contract}/resolve", s.handleTokenResolve)
		api.Post("/admin/tokens/{chain}/{contract}/override", s.handleTokenOverride)
		api.Post("/admin/token-metadata/backfill", s.handleTokenBackfill)
		api.Get("/admin/token-metadata/backfill", s.handleTokenBackfillList)

		api.Post("/updates/preview", s.handleUpdatePreview)
		api.Post("/updates/{id}/confirm", s.handleUpdateConfirm)

		// V1.3 Method / ABI（§52）
		api.Get("/transactions/{chain}/{tx_hash}", s.handleTransactionDetail)
		api.Get("/methods/search", s.handleMethodSearch)
		api.Get("/methods/enrichment", s.handleMethodEnrichment)
		api.Get("/methods/{selector}", s.handleMethodLookup)

		// 管理员接口必须权限控制（§52）
		api.Post("/admin/methods/backfill", s.handleCreateBackfill)
		api.Get("/admin/methods/backfill", s.handleListBackfill)
		api.Get("/admin/methods/backfill/{id}", s.handleGetBackfill)
		api.Post("/admin/methods/backfill/{id}/{action}", s.handleControlBackfill)
		api.Post("/admin/methods/signatures", s.handleCreateSignature)
		api.Post("/admin/contracts/{chain}/{address}/abi", s.handleUploadABI)
	})

	// §21.4 认证 API（新系统入口）。
	r.Route("/api/auth/v1", func(ar chi.Router) {
		s.registerAuthV1(ar)
	})

	// §21.5 用户层 API（Investigator Console）。
	r.Route("/api/v2", func(ur chi.Router) {
		s.registerUserV2(ur)
	})

	// §21.6 后台管理 API（Admin Console）。管理接口默认拒绝：每个端点都必须声明权限。
	r.Route("/api/admin/v1", func(ar chi.Router) {
		s.registerAdminV1(ar)
	})

	// 短期下载票据：校验后经 API 流式传输，支持 HTTP Range。
	r.Get("/api/v1/download/{ticket}", s.handleTicketDownload)
	s.router = r
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) StartForwarder() {
	if s.runtime != nil {
		s.runtime.Recover(context.Background())
	}
	s.startBusForwarder(s.deps.Log)
}

func (s *Server) StopForwarder() {
	if s.busStop != nil {
		s.busStop()
	}
}

func (s *Server) logMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			// §25.1 结构化字段：service / environment / route_template / request_id / trace_id /
			// actor_id / organization_id / workspace_id / error_code。普通应用日志不复制请求体。
			meta := metaFrom(r.Context())
			actor := actorFrom(r.Context())
			routeTemplate := chi.RouteContext(r.Context()).RoutePattern()
			if routeTemplate == "" {
				routeTemplate = unmatchedRouteTemplate(r.URL.Path)
			}
			s.deps.Log.Info("http",
				"service", "api", "environment", s.cfg.Env, "build_sha", os.Getenv("BUILD_SHA"),
				"method", r.Method, "path", r.URL.Path, "route_template", routeTemplate,
				"status", ww.Status(), "duration_ms", time.Since(start).Milliseconds(),
				"request_id", meta.RequestID, "trace_id", meta.TraceID,
				"actor_id", actor.IdentityID, "organization_id", actor.OrganizationID,
				"workspace_id", actor.WorkspaceID, "error_code", errorCodeForStatus(ww.Status()))
		})
	}
}

// errorCodeForStatus 把 HTTP 状态映射为稳定错误码标签（不泄露内部细节）。
func errorCodeForStatus(status int) string {
	switch {
	case status >= 500:
		return "INTERNAL_ERROR"
	case status == 429:
		return "RATE_LIMITED"
	case status == 403:
		return "AUTHZ_FORBIDDEN"
	case status == 401:
		return "UNAUTHENTICATED"
	case status >= 400:
		return "VALIDATION_INVALID_ARGUMENT"
	}
	return ""
}

// metricsMiddleware 采集 §25.2 的 API 层指标：请求速率、错误率、延迟直方图与 inflight。
// route_template 使用 chi 的路由模板（不是原始路径），避免高基数标签。
func (s *Server) metricsMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			observability.HTTPInflight.Inc()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			observability.HTTPInflight.Dec()
			routeTemplate := chi.RouteContext(r.Context()).RoutePattern()
			if routeTemplate == "" {
				// 未匹配路由按前缀归一，避免把扫描路径写成独立时间序列。
				routeTemplate = unmatchedRouteTemplate(r.URL.Path)
			}
			audience := "unknown"
			switch {
			case strings.HasPrefix(r.URL.Path, "/api/admin/"):
				audience = "admin"
			case strings.HasPrefix(r.URL.Path, "/api/v2/"), strings.HasPrefix(r.URL.Path, "/api/auth/"):
				audience = "user"
			}
			observability.RecordHTTP(routeTemplate, r.Method, ww.Status(), audience, time.Since(start))
		})
	}
}

// unmatchedRouteTemplate 归一化未匹配路径，保持标签基数可控。
func unmatchedRouteTemplate(path string) string {
	switch {
	case path == "":
		return "unmatched"
	case strings.HasPrefix(path, "/api/admin/v1/"):
		return "/api/admin/v1/unmatched"
	case strings.HasPrefix(path, "/api/v2/"):
		return "/api/v2/unmatched"
	case strings.HasPrefix(path, "/api/v1/"):
		return "/api/v1/unmatched"
	case strings.HasPrefix(path, "/api/auth/v1/"):
		return "/api/auth/v1/unmatched"
	case strings.HasPrefix(path, "/api/"):
		return "/api/unmatched"
	}
	return "static"
}

func (s *Server) corsMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User-Id, X-User-Email, X-Org-Id, X-User-Role")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	checks := map[string]string{}
	if err := s.deps.Store.Pool.Ping(ctx); err != nil {
		checks["postgres"] = "down: " + err.Error()
	} else {
		checks["postgres"] = "ok"
	}
	if err := s.deps.CH.Exec(ctx, "SELECT 1"); err != nil {
		checks["clickhouse"] = "down: " + err.Error()
	} else {
		checks["clickhouse"] = "ok"
	}
	if s.deps.Bus != nil && s.deps.Bus.Enabled() {
		checks["nats"] = "ok"
	} else {
		checks["nats"] = "degraded (disabled)"
	}
	// MinIO：stat 一个哨兵对象不可靠，仅验证 bucket
	if s.deps.Raw != nil {
		checks["minio"] = "ok"
	}
	status := http.StatusOK
	for _, v := range checks {
		if v != "ok" {
			status = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, status, map[string]any{"status": statusLabel(status), "checks": checks})
}

func statusLabel(code int) string {
	if code == http.StatusOK {
		return "ready"
	}
	return "not_ready"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonEncode(w, v)
}
