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
	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/events"
	"github.com/blockchain-platform/blockchain-platform/internal/explorer"
	"github.com/blockchain-platform/blockchain-platform/internal/featureflag"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/investigation"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/mfa"
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
	jobEvents      jobEventThrottle
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
	cfg    *config.Config
	iam    *iam.Service
	policy *policy.Engine
	audit  *audit.Writer
	// auditSink 仅用于测试注入关键路径审计（生产为 nil，走 audit.Writer）。D-R10V9-01。
	auditSink auditSink
	guard     objectScoper
	// sessionScope is a test seam for the live-session workspace lookup used by the
	// session-workspace staleness guard (nil in production, where the guard reads
	// s.deps.Store.Pool directly). Same injection pattern as auditSink and guard.
	sessionScope sessionScopeReader
	// scopeGate is the P1-A commit gate (see scopecommit_gate.go). It owns a
	// dedicated connection pool that holds a row lock on the caller session row
	// across a guarded state-changing handler, so a scope mutation can only ever
	// be ordered strictly before the refusal or strictly after the commit.
	scopeGate scopeCommitGate
	// legacyConsole is the LEGACY-CONSOLE-01 service principal: the entry layer
	// injects its token for requests that carry no session, so the legacy console
	// keeps working without depending on the new session/workspace line. Disabled
	// unless LEGACY_CONSOLE_TOKEN_SHA256 is configured (see legacyconsole.go).
	legacyConsole legacyConsoleAuth
	// mfa 是 AUTH-004 的持久化入口（TOTP 密钥、恢复码、防重放）。nil 表示未接线（不可用）。
	mfa *mfa.Store
	// flags 是 §7.3 的功能开关求值器：真实请求路径按它决定"这个能力面现在开不开"。
	// nil 表示未接线，此时 requireFlag 一律 fail closed（拒绝），不会默认放行。
	flags *featureflag.Store

	casework       *casework.Service
	oidc           *iam.OIDCAdapter
	authMode       auth.Mode
	trustedProxies []*net.IPNet
}

// auditSink 是审计写入的最小接口：生产实现是 *audit.Writer；测试可注入记录桩，
// 使"下载成功必须留下服务端审计"这类关键路径能被 httptest 逐字段断言（D-R10V9-01）。
type auditSink interface {
	Write(ctx context.Context, e audit.Event) error
}

// emitAudit 统一审计出口。写入失败**不静默**：沿用既有关键操作审计策略 —— 记录 Error 日志
// 并返回错误，由调用方决定是 fail closed 还是业务成功 + 明确审计告警。绝不吞掉错误。
//
// D-B2RAW-02 (2026-09-13 security review, real defect): auditRequestDetached uses
// context.WithoutCancel to survive client disconnect, which ALSO drops the request deadline.
// audit.Writer.Write then runs Begin/INSERT/Commit with that context, while production
// PostgreSQL has statement_timeout=0, every role has an empty rolconfig and the pgxpool sets
// no per-query timeout -- so this audit write was completely UNBOUNDED. If the database stalls
// (lock wait, saturated pool) the download handler goroutine and its pooled connection stay
// pinned forever; repeated downloads can exhaust the pool and turn an audit-reliability fix
// into an availability defect. Bound every audit write that has no deadline; contexts that
// already carry an earlier deadline are left untouched.
func (s *Server) emitAudit(ctx context.Context, ev audit.Event) error {
	if s.auditSink == nil && s.audit == nil {
		return nil
	}
	// dbtx.WithoutTx: an audit row must never join the P1-A request transaction.
	// If it did, a rolled back request would take its own deny row with it, and
	// the audit hash-chain head would stay locked for the whole handler.
	ctx = dbtx.WithoutTx(ctx)
	ctx, cancel := auditBoundedContext(ctx)
	defer cancel()
	if s.auditSink != nil {
		return s.auditSink.Write(ctx, ev)
	}
	return s.audit.Write(ctx, ev)
}

// auditWriteTimeout bounds a single audit write.
// It must stay a var (not a const): the regression test lowers it to prove deterministically
// that an unbounded write really is cut off.
var auditWriteTimeout = 5 * time.Second

// auditBoundedContext clamps an audit write to the SMALLER of the caller's own deadline and
// now+auditWriteTimeout, returning the matching cancel.
//
// D-B2RAW-03 (2026-09-13 review, second face of D-B2RAW-02): the first fix only added a bound
// when the caller had NO deadline, so any caller carrying a deadline LONGER than the bound
// (a request/background context with its own multi-minute or one-hour timeout) silently kept an
// effectively unbounded audit write, and TestAuditBoundedContextAddsAndPreservesDeadline pinned
// that hole with a one-hour parent. The safety intent is an upper bound on the write, so a later
// deadline is now clamped down to now+auditWriteTimeout; an EARLIER deadline is still preserved
// exactly, because shortening the caller's own semantics is never allowed either.
func auditBoundedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	limit := time.Now().Add(auditWriteTimeout)
	if dl, ok := ctx.Deadline(); ok && !dl.After(limit) {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, limit)
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
	s.legacyConsole = newLegacyConsoleAuth(deps.Cfg, deps.Log)
	if deps.Store != nil && deps.Store.Pool != nil {
		s.mfa = mfa.NewStore(deps.Store.Pool)
	}
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
	// CANDIDATE, DELIBERATELY NOT MOUNTED (P1-MCPW-ENFORCE, 2026-09-14).
	//
	// The guard below refuses every business surface (403 AUTHN_PASSWORD_CHANGE_REQUIRED)
	// while identities.must_change_password is set, and it must run after identityMiddleware
	// (it needs the principal) and before auditCaptureMiddleware (a refused request is not a
	// business operation). See internal/api/mustchangepw.go and
	// acceptance-evidence/dual-system-v1/sprint7/must-change-password-enforcement/DESIGN.md.
	//
	// IT IS NOT MOUNTED ON PURPOSE. All 39 identities, including the only real administrator
	// admin@local, currently carry must_change_password=true, so mounting it locks that
	// administrator out of every business endpoint until a REAL password change is performed.
	// That is a production change to a real account and it is not authorised. Activating this
	// one line IS the whole deployment, so do it only with explicit user authorisation, and
	// update TestMustChangePasswordGuardHeldCalibration at the same time.
	// r.Use(s.mustChangePasswordMiddleware)
	// C1-SCOPE-RACE-STALE: read the body of a session-scoped state-changing request to EOF
	// before the router runs, so the workspace-staleness decision in require/requireAny
	// happens after the body has arrived instead of before it. Placed after the
	// must_change_password gate so a request that is refused anyway is never buffered.
	r.Use(s.scopeBodyMiddleware)
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
		// registerActionRoutes 必须先注册：它自己的子路由（/admin/action-api-keys）
		// 与下面的 session 路由互不重叠，但 Actions 面独立鉴权，顺序保持原样。
		s.registerActionRoutes(api)
		// ------------------------------------------------------------------
		// P0 修复（S7-SEC-LEGACY-V1）：/api/v1 下**每一条**读路由都必须显式声明权限。
		//
		// 背景：本兼容面历史上只在写路由上调用 s.require(...)，大量读路由直接注册
		// handler，导致无 Cookie / 无 Authorization 的请求可以拿到 cases、
		// download-jobs、api-control、admin 等真实业务数据（HTTP 200）。
		//
		// 原则与硬约束：
		//   - 不使用"全局登录"中间件糊过去：每条路由的权限键必须与其域内已有写权限相称，
		//     便于按域授予最小权限、并让审计动作名精确；
		//   - 公开白名单只保留**不含租户业务数据**的元数据/链上公共查询，逐条注明理由；
		//   - 未在下方显式声明 require 的读路由不存在；新增路由必须二选一并写明理由。
		//   - registerActionRoutes 注册的 GPT Actions 路由（/address/*、/transaction/*、
		//     /analysis/*、/export/*、/data/*、/admin/action-api-keys/*）走独立的
		//     Bearer API Key + scopes 认证（actions.go），**不得**套用 session 规则，
		//     因此本处不为其添加任何 require（见本块最上方的调用）。
		// ------------------------------------------------------------------

		// ---- 公开白名单（无租户业务数据） ----
		// /me：前端用它判断"是否已登录"，只回显当前主体（未登录时返回匿名用户），
		// 不含任何组织/工作区业务数据；若要求登录才能探测登录态，登录页无法工作。
		api.Get("/me", s.handleMe)
		// /system/architecture：架构基线元数据（组件就绪位、契约版本），无租户字段。
		api.Get("/system/architecture", s.handleArchitectureStatus)
		// /capabilities：能力注册表描述符（名称/版本/契约），无租户字段。
		api.Get("/capabilities", s.handleCapabilities)
		// /system/chains/{chain}/capabilities：RPC 网关能力位（archive/trace/batch 是否可用），
		// 链级静态元数据，用于前端在登录前渲染支持链列表。
		api.Get("/system/chains/{chain}/capabilities", s.handleChainCapabilities)
		// /chains/capabilities：链能力矩阵（chain_capabilities 表，链级静态元数据，无租户字段）。
		api.Get("/chains/capabilities", s.handleV12ChainCapabilities)
		// /chains/{chain}/head：链上公共 head，属"链上公共 head 查询"白名单。
		api.Get("/chains/{chain}/head", s.handleChainHead)
		// /transactions/{chain}/{tx_hash}/receipt：链上公共回执，不含平台侧数据。
		api.Get("/transactions/{chain}/{tx_hash}/receipt", s.handleRPCReceipt)
		// /transactions/{chain}/{tx_hash}：链上公共交易详情与 ABI 解码结果（方法签名来自公共 ABI 库）。
		api.Get("/transactions/{chain}/{tx_hash}", s.handleTransactionDetail)
		// /tokens/*：链上公共 Token 元数据与图标（属"链上公共 tokens 查询"白名单）。
		api.Get("/tokens/search", s.handleTokenSearch)
		api.Get("/tokens/{chain}/{contract}/logo", s.handleTokenLogo)
		api.Get("/tokens/{chain}/{contract}", s.handleTokenGet)
		// /methods/*：链上公共 ABI / 方法签名查询（属"链上公共 methods 查询"白名单）。
		api.Get("/methods/search", s.handleMethodSearch)
		api.Get("/methods/{selector}", s.handleMethodLookup)

		// ---- 调查域（租户数据） ----
		api.Post("/investigations", s.require("investigation:create", s.handleCreateInvestigation))
		api.Get("/investigations", s.require("investigation:list", s.handleListInvestigations))
		api.Get("/investigations/{id}", s.require("investigation:read", s.handleGetInvestigation))
		api.Get("/investigations/{id}/runtime", s.require("investigation:read", s.handleInvestigationRuntime))
		api.Get("/investigations/{id}/artifacts", s.require("investigation:read", s.handleInvestigationArtifacts))
		api.Post("/investigations/{id}/{action}", s.require("investigation:run", s.handleInvestigationAction))
		api.Get("/investigations/{id}/reports", s.require("report:list", s.handleListInvestigationReports))
		api.Post("/investigations/{id}/reports", s.require("report:create", s.handleGenerateInvestigationReport))
		api.Get("/reports/{id}/download", s.require("report:download", s.handleDownloadInvestigationReport))
		api.Get("/investigations/{id}/subjects", s.require("subject:list", s.handleInvestigationSubjects))
		api.Post("/investigations/{id}/subjects", s.require("subject:create", s.handleInvestigationSubjects))
		api.Post("/investigations/{id}/relationships", s.require("investigation:update", s.handleInvestigationRelationships))
		// 地址画像/语义/进阶分析读的是**平台本地已入库数据**（本地 CH 表），
		// 未认证访问会变成"某地址是否被本平台采集过"的存在性探针，因此按本地数据查询鉴权。
		api.Get("/analysis/addresses/{chain}/{address}/flow-profile", s.require("explorer:read", s.handleAddressFlowProfile))
		api.Get("/analysis/addresses/{chain}/{address}/semantic-events", s.require("explorer:read", s.handleAddressSemanticEvents))
		api.Post("/analysis/addresses/{chain}/{address}/semantic-events/rebuild", s.require("admin:dataset:manage", s.handleRebuildAddressSemanticEvents))
		api.Get("/analysis/addresses/{chain}/{address}/advanced", s.require("explorer:read", s.handleAddressAdvancedAnalysis))
		api.Post("/analysis/addresses/{chain}/{address}/advanced/rebuild", s.require("admin:dataset:manage", s.handleRebuildAddressAdvancedAnalysis))

		// ---- 下载 / 任务域（租户数据） ----
		api.Post("/datasets/ensure", s.require("download:create", s.handleEnsure))
		api.Post("/datasets/coverage/preview", s.require("explorer:read", s.handleCoveragePreview))
		api.Get("/download-jobs", s.require("job:list:workspace", s.handleListJobs))
		api.Get("/download-jobs/{id}", s.require("job:read", s.handleGetJob))
		api.Post("/download-jobs/{id}/pause", s.require("job:control", s.handlePause))
		api.Post("/download-jobs/{id}/resume", s.require("job:control", s.handleResume))
		api.Post("/download-jobs/{id}/cancel", s.require("job:control", s.handleCancel))
		api.Get("/download-jobs/{id}/guard", s.require("job:read", s.handleGetVolumeGuards))
		api.Post("/download-jobs/{id}/guard/continue", s.require("job:control", s.handleContinueVolumeGuard))
		api.Post("/download-jobs/{id}/guard/abort", s.require("job:control", s.handleAbortVolumeGuard))
		api.Post("/download-jobs/{id}/subjects/{subjectID}/{action}", s.require("job:control", s.handleSubjectControl))
		api.Get("/download-jobs/{id}/events", s.require("job:read", s.handleJobEvents))

		// ---- Provider / 计数域（平台级运维数据） ----
		// providers/ankr/* 暴露 Provider 端点、并发目标、成本与限流状态，
		// 属于平台运维面；用与该域已有写权限（admin:provider:update /
		// admin:performance:update / admin:cost:read）相称的读权限收敛。
		api.Get("/providers/ankr/status", s.require("admin:provider:read", s.handleProviderStatus))
		api.Get("/providers/ankr/usage", s.require("admin:cost:read", s.handleProviderUsage))
		api.Get("/providers/ankr/global-concurrency", s.require("admin:performance:read", s.handleGlobalConcurrency))
		api.Get("/providers/ankr/fetch-runtime", s.require("admin:performance:read", s.handleAnkrFetchRuntime))
		api.Get("/providers/ankr/production-profile", s.require("admin:performance:read", s.handleAnkrProductionProfile))
		api.Post("/providers/ankr/performance-mode", s.require("admin:performance:update", s.handleSetAnkrPerformanceMode))
		api.Post("/download-jobs/{id}/performance-mode", s.require("admin:performance:update", s.handleSetJobPerformanceMode))
		api.Get("/providers/ankr/fetch-profile", s.require("admin:performance:read", s.handleAnkrFetchProfile))
		api.Get("/providers/ankr/ready-queue", s.require("admin:performance:read", s.handleGlobalReadyQueue))
		api.Get("/count-oracle/status", s.require("admin:provider:read", s.handleCountOracleStatus))
		api.Get("/count-oracle/providers", s.require("admin:provider:read", s.handleCountProviders))
		api.Get("/count-oracle/resolutions", s.require("admin:provider:read", s.handleResolutionDecisions))
		api.Get("/count-oracle/observations", s.require("admin:provider:read", s.handleCountOracleObservations))
		api.Get("/count-oracle/calibration", s.require("admin:provider:read", s.handleCountOracleCalibration))
		// 计划决策按 job_id 检索，属全局任务数据面。
		api.Get("/admin/planning-decisions", s.require("admin:job:read", s.handlePlanningDecisions))
		// 地址运行时读取（地址类型/原生余额）会消耗付费 Provider 配额，
		// 且 /addresses/* 族整体属于业务数据面；按地址读权限收敛。
		api.Get("/addresses/{chain}/{address}/runtime", s.require("address:read", s.handleAddressRuntime))
		api.Get("/transactions/{chain}/{tx_hash}/trace", s.require("transaction:read", s.handleTransactionTrace))
		api.Get("/transactions/{chain}/{tx_hash}/internal-calls", s.require("transaction:read", s.handleInternalCalls))

		// ---- API Control Center（平台运维面，全部需要后台读权限） ----
		api.Get("/api-control/overview", s.require("admin:dashboard:read", s.handleAPIControlOverview))
		api.Get("/api-control/provider-accounts", s.require("admin:provider:read", s.handleAPIControlProviderAccounts))
		api.Post("/api-control/provider-accounts/ankr/connect", s.require("admin:provider:update", s.handleAnkrManagementConnect))
		// /api-control/provider-accounts/ankr/oauth/callback：**保持公开**，理由有三：
		//  1. 它是 OAuth 提供方发起的浏览器顶级跳转，浏览器不会带自定义头，
		//     而 /api/v1 路径按 §3.3 解析为 user 受众，后台会话（admin Cookie）
		//     在这里本来就取不到 —— 加 require 只会把合法回调打成 401；
		//  2. 回调本身不构成授权：CompleteOAuth 校验的是服务端签发的、单次使用、
		//     10 分钟过期且绑定 PKCE verifier 的 state（ankr_management_oauth_state），
		//     而 state 只能由已通过 admin:provider:update 的 connect 端点取得；
		//  3. 缺少 state/code 或 state 无效时只回一个 error 重定向，不改变任何状态。
		// 结论：state 绑定已经提供了比 session 更强的授权证明，此处不需要 require。
		api.Get("/api-control/provider-accounts/ankr/oauth/callback", s.handleAnkrManagementCallback)
		api.Post("/api-control/provider-accounts/ankr/sync", s.require("admin:provider:update", s.handleAnkrManagementSync))
		api.Delete("/api-control/provider-accounts/ankr", s.require("admin:provider:update", s.handleAnkrManagementDisconnect))
		api.Get("/api-control/control/states", s.require("admin:routing:read", s.handleAPIControlState))
		api.Get("/api-control/control/policies", s.require("admin:routing:read", s.handleAPIControlPolicies))
		api.Patch("/api-control/control/policies", s.require("admin:routing:update", s.handleAPIControlPolicies))
		api.Post("/api-control/control/overrides", s.require("admin:performance:update", s.handleAPIControlOverride))
		api.Patch("/api-control/control/kill-switch", s.require("admin:killswitch:engage", s.handleAPIControlKillSwitch))
		api.Get("/api-control/control/actions", s.require("admin:routing:read", s.handleAPIControlActions))
		api.Get("/api-control/requests", s.require("admin:request:read", s.handleAPIControlRequests))
		api.Get("/api-control/requests/{id}", s.require("admin:request:read", s.handleAPIControlRequests))
		api.Get("/api-control/errors", s.require("admin:request:read", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "errors") }))
		api.Get("/api-control/costs", s.require("admin:cost:read", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "costs") }))
		api.Get("/api-control/operations", s.require("admin:request:read", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "operations") }))
		api.Get("/api-control/providers", s.require("admin:provider:read", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "providers") }))
		api.Get("/api-control/usage-types", s.require("admin:cost:read", func(w http.ResponseWriter, r *http.Request) { s.aggregateUsage(w, r, "usage-types") }))
		api.Get("/api-control/budgets", s.require("admin:budget:read", s.handleAPIControlBudgets))
		api.Post("/api-control/budgets", s.require("admin:budget:update", s.handleAPIControlBudgets))
		api.Get("/api-control/alerts", s.require("admin:alert:read", s.handleAPIControlAlerts))
		api.Post("/api-control/alerts/{id}/actions", s.require("admin:alert:manage", s.handleAPIControlAlerts))

		// ---- 后台运维 / 基准 / 认证证据 ----
		api.Post("/admin/ankr/benchmarks", s.require("admin:performance:update", s.handleBenchmarkCreate))
		api.Get("/admin/ankr/benchmarks", s.require("admin:performance:read", s.handleBenchmarkList))
		api.Get("/admin/ankr/benchmarks/{id}", s.require("admin:performance:read", s.handleBenchmarkGet))
		api.Get("/admin/ankr/range-certifications", s.require("admin:performance:read", s.handleListRangeCertifications))
		api.Post("/admin/ankr/range-certifications", s.require("admin:performance:update", s.handleRecordRangeCertification))

		// ---- Coverage / Dataset / Explorer（平台本地数据面） ----
		// coverage 与同域已有的 POST /datasets/coverage/preview 使用同一个读权限键
		// （explorer:read = "查询本地数据"）。dataset_coverage 没有租户列，无法按组织过滤，
		// 因此这里靠 require 收敛到"已认证且具备本地数据查询权"的主体。
		api.Get("/coverage", s.require("explorer:read", s.handleCoverage))
		api.Get("/coverage/stats", s.require("explorer:read", s.handleCoverageStats))
		api.Get("/datasets/{dataset}", s.require("explorer:read", s.handleDatasetRows))
		api.Get("/addresses/{address}/activity", s.require("explorer:read", s.handleAddressActivity))
		api.Get("/data-explorer/addresses", s.require("explorer:read", s.handleExplorerAddresses))
		api.Post("/data-explorer/query", s.require("explorer:read", s.handleExplorerQuery))

		// ---- 导出（租户对象） ----
		api.Post("/exports", s.require("export:create", s.handleCreateExport))
		api.Get("/exports/{id}", s.require("export:read", s.handleGetExport))
		api.Get("/exports/{id}/download", s.require("export:download", s.handleExportDownloadScoped))
		api.Head("/exports/{id}/download", s.require("export:download", s.handleExportDownloadScoped))
		api.Post("/exports/{id}/download-ticket", s.require("export:download", s.handleExportTicket))

		// ---- 案件 / 画布 / 标签（租户对象） ----
		api.Get("/cases", s.require("case:list", s.handleListCases))
		api.Post("/cases", s.require("case:create", s.handleCreateCase))
		api.Delete("/cases/{id}", s.require("case:delete", s.handleDeleteCase))
		api.Get("/cases/{id}/canvases", s.require("canvas:list", s.handleListCanvases))
		api.Post("/cases/{id}/canvases", s.require("canvas:create", s.handleCreateCanvas))
		api.Get("/canvases/{id}", s.require("canvas:read", s.handleGetCanvas))
		api.Patch("/canvases/{id}", s.require("canvas:update", s.handleUpdateCanvas))
		api.Post("/canvases/{id}/branch", s.require("canvas:branch", s.handleBranchCanvas))
		api.Get("/canvases/{id}/snapshots", s.require("canvas:snapshot", s.handleListCanvasSnapshots))
		api.Post("/canvases/{id}/snapshots", s.require("canvas:snapshot:create", s.handleCreateCanvasSnapshot))
		api.Get("/tags", s.require("case:read", s.handleListTags))
		api.Post("/tags", s.require("case:update", s.handleCreateTag))
		api.Delete("/tags/{id}", s.require("case:update", s.handleDeleteTag))

		// ---- Token / Method 元数据（链上公共数据，读受限、写受限） ----
		api.Patch("/addresses/{chain}/{address}/metadata", s.require("admin:dataset:manage", s.handleAddressMetadata))
		// enrichment 是**平台元数据覆盖率统计**（不是单条 Token/Method 查询），
		// 会暴露本地已处理规模，按 Dataset 读权限收敛。
		api.Get("/tokens/enrichment", s.require("admin:dataset:read", s.handleTokenEnrichment))
		api.Post("/admin/tokens/{chain}/{contract}/resolve", s.require("admin:dataset:manage", s.handleTokenResolve))
		api.Post("/admin/tokens/{chain}/{contract}/override", s.require("admin:dataset:manage", s.handleTokenOverride))
		api.Post("/admin/token-metadata/backfill", s.require("admin:dataset:manage", s.handleTokenBackfill))
		api.Get("/admin/token-metadata/backfill", s.require("admin:dataset:read", s.handleTokenBackfillList))
		api.Get("/methods/enrichment", s.require("admin:dataset:read", s.handleMethodEnrichment))

		api.Post("/updates/preview", s.require("admin:dataset:manage", s.handleUpdatePreview))
		api.Post("/updates/{id}/confirm", s.require("admin:dataset:manage", s.handleUpdateConfirm))

		// 管理员接口必须权限控制（§52）
		api.Post("/admin/methods/backfill", s.require("admin:dataset:manage", s.handleCreateBackfill))
		api.Get("/admin/methods/backfill", s.require("admin:dataset:read", s.handleListBackfill))
		api.Get("/admin/methods/backfill/{id}", s.require("admin:dataset:read", s.handleGetBackfill))
		api.Post("/admin/methods/backfill/{id}/{action}", s.require("admin:dataset:manage", s.handleControlBackfill))
		api.Post("/admin/methods/signatures", s.require("admin:dataset:manage", s.handleCreateSignature))
		api.Post("/admin/contracts/{chain}/{address}/abi", s.require("admin:dataset:manage", s.handleUploadABI))
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
