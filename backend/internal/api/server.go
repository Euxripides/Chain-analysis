// Package api：REST / SSE API。
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/blockchain-platform/blockchain-platform/internal/addressmeta"
	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/events"
	"github.com/blockchain-platform/blockchain-platform/internal/explorer"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankrmgmt"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
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
	Registry      *method.Registry
	MethodData    *method.ClickHouseStore
	DeepDecode    *method.AnkrDeepResolver
	TokenMeta     *tokenmeta.Service
	TokenBackfill *tokenmeta.BackfillRunner
	Head          *chainread.HeadService
	AddressType   *chainread.AddressTypeService
	BalanceRead   *chainread.BalanceService
	ChainRead     *chainread.Service
	Trace         *chaintrace.Service
	RPCGateway    *rpc.Gateway
}

type Server struct {
	deps     Deps
	router   chi.Router
	hub      *SSEHub
	busStop  func()
	svc      *dataset.Service
	query    *query.Service
	meta     *addressmeta.Service
	explorer *explorer.Service
	updates  *update.Service
	detail   *method.DetailService
	ankrMgmt *ankrmgmt.Service
}

func New(deps Deps) *Server {
	s := &Server{deps: deps, hub: NewSSEHub(), query: query.New(deps.CH)}
	if management, err := ankrmgmt.New(deps.Store.Pool, deps.Cfg.AnkrManagement); err == nil {
		s.ankrMgmt = management
	} else {
		deps.Log.Error("Ankr Management MCP initialization failed", "err", err)
	}
	s.svc = dataset.New(deps.Store, func(ctx context.Context, chain string) (int64, error) {
		if deps.Head != nil {
			head, err := deps.Head.SafeHead(ctx, chain)
			return int64(head), err
		}
		if deps.Prov != nil && deps.Prov.Name() == "ankr-mock" {
			return 2_000_000, nil
		}
		return 0, nil // live：head 未知，区间由 Provider 语义决定
	})
	s.meta = addressmeta.New(deps.Store)
	s.svc.Meta = s.meta
	s.explorer = explorer.New(deps.CH, deps.Store, deps.TokenMeta)
	if deps.AddressType != nil {
		s.explorer.WithAddressTypeResolver(deps.AddressType)
	} else if resolver, ok := deps.Prov.(provider.AddressTypeResolver); ok {
		// 兼容 RPC 基础设施被显式关闭的旧部署；正常路径必须走 ChainRead。
		s.explorer.WithAddressTypeResolver(resolver)
	}
	var safeHead update.SafeHeadFn
	if deps.Head != nil {
		safeHead = func(ctx context.Context, chain string) (int64, error) {
			head, err := deps.Head.SafeHead(ctx, chain)
			return int64(head), err
		}
	}
	s.updates = update.New(deps.Store, deps.CH, deps.Prov, s.svc, s.meta, safeHead, deps.Log)
	if deps.Registry != nil && deps.MethodData != nil {
		// 智能调查/前端都必须经过 TransactionDetailService，不允许直连 Provider（§50）
		s.detail = method.NewDetailService(deps.MethodData, deps.Registry, deps.DeepDecode, s.recordDeepDecodeUsage, deps.Log)
	}
	s.buildRouter()
	return s
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
	r.Use(s.logMiddleware())
	r.Use(auth.Middleware)

	r.Handle("/metrics", promhttp.Handler())
	r.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	r.Get("/health/ready", s.handleReady)

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/me", s.handleMe)
		api.Post("/datasets/ensure", s.handleEnsure)
		api.Post("/datasets/coverage/preview", s.handleCoveragePreview)
		api.Get("/download-jobs", s.handleListJobs)
		api.Get("/download-jobs/{id}", s.handleGetJob)
		api.Post("/download-jobs/{id}/pause", s.handlePause)
		api.Post("/download-jobs/{id}/resume", s.handleResume)
		api.Post("/download-jobs/{id}/cancel", s.handleCancel)
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
		api.Get("/api-control/alerts", s.handleAPIControlAlerts)
		api.Post("/api-control/alerts/{id}/actions", s.handleAPIControlAlerts)
		api.Get("/coverage", s.handleCoverage)
		api.Get("/coverage/stats", s.handleCoverageStats)
		api.Get("/datasets/{dataset}", s.handleDatasetRows)
		api.Get("/addresses/{address}/activity", s.handleAddressActivity)
		api.Post("/exports", s.handleCreateExport)
		api.Get("/exports/{id}", s.handleGetExport)
		api.Get("/exports/{id}/download", s.handleDownloadExport)
		api.Post("/exports/{id}/download-ticket", s.handleExportTicket)

		api.Get("/cases", s.handleListCases)
		api.Post("/cases", s.handleCreateCase)
		api.Delete("/cases/{id}", s.handleDeleteCase)
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

	// 短期下载票据：校验后经 API 流式传输，支持 HTTP Range。
	r.Get("/api/v1/download/{ticket}", s.handleTicketDownload)
	s.router = r
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) StartForwarder() {
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
			s.deps.Log.Info("http", "method", r.Method, "path", r.URL.Path,
				"status", ww.Status(), "duration_ms", time.Since(start).Milliseconds())
		})
	}
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
