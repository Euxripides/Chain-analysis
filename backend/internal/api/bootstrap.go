package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
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

	s.audit = audit.NewWriter(poolOf(deps), deps.Cfg.Env, deps.Log)

	if deps.Store == nil {
		return
	}
	repo := iam.NewRepo(deps.Store.Pool)
	s.policy = policy.NewEngine(deps.Store.Pool, 30*time.Second)
	s.iam = iam.NewService(repo, iam.NewSessionManager(repo, iam.SessionConfig{
		UserCookieName:  "cs_user_session",
		AdminCookieName: "cs_admin_session",
		CSRFHeader:      "X-CSRF-Token",
		IdleTTL:         deps.Cfg.Auth.SessionIdleTTL,
		AbsoluteTTL:     deps.Cfg.Auth.SessionAbsoluteTTL,
		Secure:          deps.Cfg.Auth.CookieSecure,
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

func poolOf(deps Deps) *pgxpool.Pool {
	if deps.Store == nil {
		return nil
	}
	return deps.Store.Pool
}

// auditCaptureMiddleware 捕获响应状态与耗时，用于写业务审计（§6.6.3 结果字段）。
func (s *Server) auditCaptureMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if s.audit != nil && declaredPermission(r.Context()) != "" {
			s.auditBusiness(r, rec.status, time.Since(start), rec.written)
		}
	})
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
