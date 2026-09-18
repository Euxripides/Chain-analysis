package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"

	"github.com/blockchain-platform/blockchain-platform/internal/access"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/featureflag"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// wireIdentityAndAudit 建立 IAM / Policy / Audit 底座并校验启动前置条件。
//
// fail closed 原则：生产环境若仍允许信任客户端自报身份头，启动即失败，
// 而不是打一条警告日志继续运行（§28.4 验收"生产模式不接受 X-User-*"）。
func (s *Server) wireIdentityAndAudit(deps Deps) {
	s.cfg = deps.Cfg
	if deps.Cfg == nil {
		s.authMode = auth.ModeSession
		return
	}
	s.authMode = auth.ParseMode(deps.Cfg.Auth.Mode)

	proxies, err := iam.ParseTrustedProxies(deps.Cfg.Auth.TrustedProxies)
	if err != nil {
		deps.Log.Error("invalid AUTH_TRUSTED_PROXIES; falling back to loopback only", "err", err)
		proxies, _ = iam.ParseTrustedProxies([]string{"127.0.0.1/32", "::1/128"})
	}
	s.trustedProxies = proxies

	// The audit writer deliberately holds the RAW pool, not the dbtx wrapper: an
	// audit row must survive a rolled back business transaction (that is how a
	// scope-deny is recorded), and the audit hash-chain head must never stay
	// locked for the length of a gated handler. emitAudit also strips the request
	// transaction from the context before writing.
	s.audit = audit.NewWriter(poolOf(deps).Raw(), deps.Cfg.Env, deps.Log)

	// §7.3：功能开关在真实请求路径上求值。开关是"低频改、高频读"的配置，
	// 读取带 10 秒 TTL 缓存；未接线（无数据库）时 requireFlag 一律拒绝，不会默认放行。
	s.flags = featureflag.NewStore(poolOf(deps).Raw(), featureflag.DefaultTTL, deps.Log)

	if deps.Store == nil {
		return
	}
	repo := iam.NewRepo(deps.Store.Pool)
	s.policy = policy.NewEngine(deps.Store.Pool, 30*time.Second)
	s.guard = access.NewGuard(access.NewResolver(deps.Store.Pool), 30*time.Second)
	audit.RegisterAuditMetrics(observability.AuditWrites)
	s.iam = iam.NewService(repo, iam.NewSessionManager(repo, iam.SessionConfig{
		UserCookieName:  "cs_user_session",
		AdminCookieName: "cs_admin_session",
		CSRFHeader:      "X-CSRF-Token",
		IdleTTL:         deps.Cfg.Auth.SessionIdleTTL,
		AbsoluteTTL:     deps.Cfg.Auth.SessionAbsoluteTTL,
		Secure:          deps.Cfg.Auth.CookieSecure,
		SecureMode:      deps.Cfg.Auth.CookieSecureMode,
		SameSite:        http.SameSiteLaxMode,
		Domain:          deps.Cfg.Auth.CookieDomain,
		Path:            "/",
		NamePrefix:      deps.Cfg.Auth.CookiePrefix,
	}), iam.Config{
		Environment:        deps.Cfg.Env,
		MaxFailedLogins:    deps.Cfg.Auth.MaxFailedLogins,
		LockDuration:       deps.Cfg.Auth.LockDuration,
		LoginRatePerMinute: deps.Cfg.Auth.LoginRatePerMinute,
		MinPasswordLength:  deps.Cfg.Auth.MinPasswordLength,
		RequireAdminMFA:    deps.Cfg.Auth.RequireAdminMFA,
	}, s.audit)
	// AUTH-004 登录第二步：把 MFA 持久化层适配成 iam 的挑战门禁。
	// s.mfa 为 nil（数据库未接线）时不注入，登录行为与接入前完全一致；
	// 数据库可用时一律注入，避免"配置里少一行就静默不要求第二步"。
	s.iam.SetMFAChallengeGate(newMFAChallengeGate(s.mfa))
	// 登录事件写失败必须留痕：调用方普遍写 `_ = RecordLoginEvent(...)`，
	// 没有这条钩子时"审计少了一行"是完全静默的（2026-09-16 的链上空洞就是这样产生的）。
	iam.SetLoginEventErrorHandler(func(err error) {
		if s.deps.Log != nil {
			s.deps.Log.Error("login event write failed", "err", err.Error())
		}
	})

	oidcCfg := deps.Cfg.Auth.OIDC
	if oidcCfg.Enabled {
		candidate := iam.OIDCConfig{
			Enabled: true, Issuer: oidcCfg.Issuer, ClientID: oidcCfg.ClientID,
			ClientSecret: oidcCfg.ClientSecret, RedirectURL: oidcCfg.RedirectURL, Scopes: oidcCfg.Scopes,
		}
		adapter := iam.NewOIDCAdapter(candidate, nil)
		if !adapter.Enabled() {
			deps.Log.Error("OIDC config is incomplete; OIDC login stays disabled", "env", deps.Cfg.Env)
		} else if deps.Cfg.Env == "production" && !strings.HasPrefix(candidate.Issuer, "https://") {
			// 生产环境禁止明文 issuer；配置无效时保持禁用而不是降级为不安全登录。
			deps.Log.Error("OIDC issuer must use https in production; OIDC login stays disabled", "issuer", candidate.Issuer)
		} else {
			s.oidc = adapter
		}
	}

	if err := s.assertSessionMode(); err != nil {
		// fail closed：不崩溃，但强制降级到最严格模式并留下高优先级错误日志。
		deps.Log.Error("insecure auth configuration rejected; forcing session-only mode", "err", err.Error())
		s.authMode = auth.ModeSession
	}
	if s.authMode.TrustsHeaders() {
		deps.Log.Warn("AUTH_MODE trusts X-User-* headers; this is only acceptable outside production",
			"mode", string(s.authMode), "env", deps.Cfg.Env)
	}
}

func poolOf(deps Deps) *dbtx.Pool {
	if deps.Store == nil {
		return nil
	}
	return deps.Store.Pool
}

// auditCaptureMiddleware 捕获响应状态与耗时，用于写业务审计（§6.6.3 结果字段）。
//
// 两条覆盖路径：
//  1. 新路由（/api/v2、/api/admin/v1、/api/auth/v1）通过 require() 声明权限键，
//     审计动作就是权限键，语义精确；
//  2. 兼容层 /api/v1 的历史写操作没有权限声明。早期实现因此**完全不审计**，
//     形成"调查/报告/导出写操作无迹可查"的真实盲区（§6.5 要求覆盖业务写操作）。
//     这里对 legacy 的非幂等请求按路径补一条审计，动作名使用 `legacy:<method> <path>`，
//     并标记 contract_level=legacy，便于后续迁移时区分与收敛。
func (s *Server) auditCaptureMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if s.audit == nil {
			return
		}
		if declaredPermission(r.Context()) != "" {
			s.auditBusiness(r, rec.status, time.Since(start), rec.written)
			return
		}
		if isLegacyMutation(r) {
			s.auditLegacyMutation(r, rec.status, time.Since(start))
		}
	})
}

// isLegacyMutation 只对 /api/v1 下会改变状态的方法补审计；
// GET/HEAD/OPTIONS 属于读取，旧接口数量多且噪声大，留给各接口迁移后再逐条声明。
func isLegacyMutation(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	// 下载票据本身是短时令牌换取文件，审计已在签发与校验处单独记录，避免重复。
	if strings.HasPrefix(r.URL.Path, "/api/v1/download/") {
		return false
	}
	return true
}

// auditLegacyMutation 记录一条兼容层写操作审计。
func (s *Server) auditLegacyMutation(r *http.Request, status int, duration time.Duration) {
	p := PrincipalFrom(r.Context())
	meta := metaFrom(r.Context())
	result := "success"
	if status >= 500 {
		result = "error"
	} else if status >= 400 {
		result = "failure"
	}
	action := "legacy:" + r.Method + " " + legacyActionPath(r.URL.Path)
	ev := auditEventFor(r, p, meta, action, "legacy", resourceFromPath(r.URL.Path), "", result,
		map[string]any{"contract_level": "legacy", "raw_path": r.URL.Path})
	ev.HTTPStatus = status
	ev.DurationMS = int(duration.Milliseconds())
	ev.RiskLevel = "medium"
	if err := s.audit.Write(r.Context(), ev); err != nil {
		s.deps.Log.Error("audit legacy mutation failed", "err", err, "path", r.URL.Path)
	}
}

// legacyActionPath 把路径中的 UUID 替换成占位符，避免审计动作名基数爆炸
// （否则每个任务 ID 都会生成一个新的 action 值）。
func legacyActionPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	for i, part := range parts {
		if looksLikeUUID(part) {
			parts[i] = "{id}"
		}
	}
	return "/" + strings.Join(parts, "/")
}

func looksLikeUUID(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i, c := range v {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.written += n
	return n, err
}

// Flush 保持 SSE 流式推送能力（chi middleware.WrapResponseWriter 之外的最小实现）。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 让 http.ResponseController 可以访问底层 writer。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// auditEventFor 构造业务/管理审计事件，统一填充主体、来源与追踪字段。
func auditEventFor(r *http.Request, p policy.Principal, meta RequestMeta, action, category, resourceType, resourceID, result string, detail map[string]any) audit.Event {
	risk := policy.RiskOf(action)
	if category == "" {
		category = categoryOfPermission(action)
	}
	return audit.Event{
		ActorType: "user", ActorID: p.IdentityID, ActorEmail: p.Email, ActorName: p.DisplayName,
		ActorRoles: p.Roles, OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID,
		SessionID: p.SessionID, DeviceID: p.DeviceID,
		Action: action, Category: category, ResourceType: resourceType, ResourceID: resourceID,
		HTTPMethod: r.Method, Route: r.URL.Path, RequestID: meta.RequestID, TraceID: meta.TraceID,
		Result: result, PermissionKey: action, PolicyDecision: "allow",
		RiskLevel: risk, Sensitive: policy.Sensitive(action),
		PeerIP: meta.IP.PeerIP, ClientIP: meta.IP.ClientIP, ProxyChain: meta.IP.ProxyChain,
		UserAgent: meta.UserAgent, Detail: detail,
	}
}

// policyEngine 暴露给需要二次授权的服务（§28.5 S3-BE-01）。
func (s *Server) policyEngine() *policy.Engine { return s.policy }

// privileged 判断当前请求是否具备某权限（用于 handler 内部对象级判断）。
func (s *Server) privileged(r *http.Request, permission string) bool {
	p := PrincipalFrom(r.Context())
	if !p.Authenticated {
		return false
	}
	return s.decide(r, p, permission).Allowed
}
