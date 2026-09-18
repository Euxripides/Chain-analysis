package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

type principalCtxKey struct{}
type requestMetaCtxKey struct{}
type auditOriginCtxKey struct{}

// auditOrigin 记录"派生后台审计上下文的那次**真实**请求"的方法与路径。
//
// 背景（R10v8 定点实测证据，audit_events_v2 seq 51944）：D-R10V4-01 修复把
// export.completed 写进了 v2 不可抵赖审计链，但 request 维度是**假的** ——
// writeAuditCtx 当时用 r0() 合成请求（POST /api/auth/v1/refresh），
// 于是"导出完成"这条事件记录的路由指向了另一个端点。
// 审计的 request 维度必须可追溯到真实请求：这里把真实 method/path 随 ctx 传下去，
// 来源缺失时**留空**，绝不伪造——宁可字段为空，也不写一条指向别的端点的假记录。
type auditOrigin struct {
	Method string
	Path   string
}

// withAuditOrigin 在请求中间件里记录真实来源（method + 路由路径）。
func withAuditOrigin(ctx context.Context, method, path string) context.Context {
	return context.WithValue(ctx, auditOriginCtxKey{}, auditOrigin{Method: method, Path: path})
}

// auditOriginFrom 取回真实来源；不存在时返回空串（调用方必须按"未知"处理，不得编造）。
func auditOriginFrom(ctx context.Context) (string, string) {
	if o, ok := ctx.Value(auditOriginCtxKey{}).(auditOrigin); ok {
		return o.Method, o.Path
	}
	return "", ""
}

// auditRequestFor 用真实来源还原一个请求对象，仅供 auditEventFor 读取 request 维度。
func auditRequestFor(ctx context.Context) *http.Request {
	method, path := auditOriginFrom(ctx)
	return &http.Request{Method: method, URL: &url.URL{Path: path}, Header: http.Header{}}
}

// actorCtxKey 保存一个可变主体槽位：identityMiddleware 在下游填充，
// logMiddleware / metricsMiddleware 在上游读取，用于 §25.1 的 actor_id 字段。
type actorCtxKey struct{}

type actorHolder struct {
	Principal principalSnapshot
}

// principalSnapshot 是日志用主体的最小快照（避免日志持有整个授权上下文）。
type principalSnapshot struct {
	IdentityID     string
	OrganizationID string
	WorkspaceID    string
}

// actorFrom 读取日志用主体快照。
func actorFrom(ctx context.Context) principalSnapshot {
	if h, ok := ctx.Value(actorCtxKey{}).(*actorHolder); ok && h != nil {
		return h.Principal
	}
	return principalSnapshot{}
}

// recordActor 由 identityMiddleware 调用，把主体写入槽位。
func recordActor(ctx context.Context, p policy.Principal) {
	if h, ok := ctx.Value(actorCtxKey{}).(*actorHolder); ok && h != nil {
		h.Principal = principalSnapshot{IdentityID: p.IdentityID, OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID}
	}
}

type routePermCtxKey struct{}
type sessionReuseCtxKey struct{}

// RequestMeta 保存本次请求的追踪与来源信息，供审计与错误响应复用。
type RequestMeta struct {
	RequestID string
	TraceID   string
	IP        iam.ClientIP
	UserAgent string
	Scheme    string
	Host      string
}

func metaFrom(ctx context.Context) RequestMeta {
	if m, ok := ctx.Value(requestMetaCtxKey{}).(RequestMeta); ok {
		return m
	}
	return RequestMeta{}
}

// PrincipalFrom 返回当前请求的授权主体。
func PrincipalFrom(ctx context.Context) policy.Principal {
	if p, ok := ctx.Value(principalCtxKey{}).(policy.Principal); ok {
		return p
	}
	return policy.Principal{}
}

func withPrincipal(ctx context.Context, p policy.Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

func withRequestMeta(ctx context.Context, m RequestMeta) context.Context {
	return context.WithValue(ctx, requestMetaCtxKey{}, m)
}

func declaredPermission(ctx context.Context) string {
	if v, ok := ctx.Value(routePermCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// audienceForPath 决定该路径使用哪一个会话受众。
// 后台 API 使用独立 Cookie，用户层会话无法调用后台接口（§3.3）。
func audienceForPath(path string) string {
	if strings.HasPrefix(path, "/api/admin/") {
		return iam.AudienceAdmin
	}
	return iam.AudienceUser
}

// socialSelfServicePath 判断是否为允许跨受众解析 Cookie 的自助端点。
func socialSelfServicePath(path string) bool {
	return strings.HasPrefix(path, "/api/auth/v1/")
}

// requestedAudience 读取客户端显式声明的会话受众。
//
// 这是一个"收窄"信号：客户端声明只接受某个受众时，服务端不再跨受众回退。
// 后台前端必须声明 admin，否则会在没有管理会话时误判为已登录。
// 该头部永远不能用来"放宽"授权——只能让判定更严格。
func requestedAudience(r *http.Request) string {
	v := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Auth-Audience")))
	if v == iam.AudienceAdmin || v == iam.AudienceUser {
		return v
	}
	return ""
}

// requestMetaMiddleware 生成请求/Trace ID 并解析客户端位置。
func (s *Server) requestMetaMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta := RequestMeta{
			RequestID: requestIDFrom(r),
			TraceID:   traceIDFrom(r),
			UserAgent: r.UserAgent(),
			Host:      r.Host,
			Scheme:    requestScheme(r),
		}
		if s.iam != nil {
			meta.IP = iam.ResolveClientIP(r, s.trustedProxies)
		} else {
			meta.IP = iam.ClientIP{PeerIP: r.RemoteAddr, ClientIP: r.RemoteAddr}
		}
		w.Header().Set("X-Request-Id", meta.RequestID)
		w.Header().Set("X-Trace-Id", meta.TraceID)
		ctx := withRequestMeta(r.Context(), meta)
		ctx = withAuditOrigin(ctx, r.Method, r.URL.Path)
		ctx = context.WithValue(ctx, actorCtxKey{}, &actorHolder{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDFrom(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Request-Id")); v != "" && len(v) <= 128 {
		return v
	}
	return uuid.NewString()
}

func traceIDFrom(r *http.Request) string {
	for _, h := range []string{"X-Trace-Id", "traceparent"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" && len(v) <= 128 {
			return v
		}
	}
	return uuid.NewString()
}

// cookieByName 依次按候选名读取 Cookie（先安全名 __Host-xxx，再普通名 xxx）。
// 部署在 HTTP/HTTPS 之间切换或回滚时，两类命名的既有会话都不会因改名而失效。
func cookieByName(r *http.Request, names []string) (*http.Cookie, bool) {
	for _, n := range names {
		if c, err := r.Cookie(n); err == nil && c.Value != "" {
			return c, true
		}
	}
	return nil, false
}

// secureCookieFor 依据**本次请求的入口协议**决定 Cookie 是否使用 Secure 与 __Host- 前缀
// （S7-SEC-01 R-05）。HTTPS 入口（Cloudflare/Nginx TLS 终止，X-Forwarded-Proto=https）
// 使用 __Host- 前缀 + Secure；HTTP 入口退回普通命名，避免浏览器因 __Host- 约束直接拒绝 Cookie。
func (s *Server) secureCookieFor(r *http.Request) bool {
	if s.iam == nil {
		return false
	}
	return s.iam.Sessions().Config().SecureFor(requestScheme(r))
}

func requestScheme(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); v != "" {
		return v
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// identityMiddleware 解析身份并注入上下文。
//
// 关键安全性质：
//   - 会话令牌从不写入响应体或可被 JS 读取的存储（仅 HttpOnly Cookie）。
//   - AuthMode=session 时完全不读取 X-User-*，伪造头无效。
//   - audience 与路径绑定：后台路径只接受 admin 会话。
func (s *Server) identityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		audience := audienceForPath(r.URL.Path)
		// 客户端显式声明的受众优先于路径推断：后台前端位于 /admin/*，
		// 但它调用的自助接口在 /api/auth/v1/* 下，路径推断会得到 user。
		if ra := requestedAudience(r); ra != "" {
			audience = ra
		}

		if s.iam != nil {
			cfg := s.iam.Sessions().Config()
			// 自助类认证端点（/api/auth/v1/*）允许跨受众回退：
			// 后台会话需要调用 /session、/logout 等同一套接口。
			// 业务与后台 API 严格按路径受众解析，不做回退（§3.3）。
			candidates := []string{audience}
			if socialSelfServicePath(r.URL.Path) && requestedAudience(r) == "" {
				if audience == iam.AudienceUser {
					candidates = append(candidates, iam.AudienceAdmin)
				} else {
					candidates = append(candidates, iam.AudienceUser)
				}
			}
			for _, aud := range candidates {
				c, ok := cookieByName(r, cfg.CookieNameCandidates(aud))
				if !ok {
					continue
				}
				err := error(nil)
				_ = err
				sess, err := s.iam.Sessions().Authenticate(ctx, c.Value, aud)
				switch {
				case err == nil:
					p := principalFromSession(ctx, sess, s.policy)
					ctx = withPrincipal(ctx, p)
					recordActor(ctx, p)
					ctx = auth.WithUser(ctx, userFromPrincipal(p))
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				case errors.Is(err, iam.ErrSessionReuse):
					// 令牌重放：撤销整个会话族并写审计。这里必须处理，
					// 否则只有走 currentSessionDetailed 的少数端点会检测到，
					// 普通业务请求会静默退化成"未登录"。
					s.handleTokenReuse(ctx, sess, aud)
					ctx = context.WithValue(ctx, sessionReuseCtxKey{}, true)
					s.iam.Sessions().ClearCookiesFor(w, aud, s.secureCookieFor(r))
				case errors.Is(err, iam.ErrWorkspaceAccessRevoked):
					// P1-20260913：工作区成员资格被撤销 → 既有会话立即 fail closed。
					// 先撤销会话并写安全审计，再返回显式错误码；绝不继续放行，
					// 也绝不静默退化成普通未登录（否则撤权不可观测）。
					s.handleWorkspaceAccessRevoked(ctx, sess, aud)
					s.iam.Sessions().ClearCookiesFor(w, aud, s.secureCookieFor(r))
					respondError(w, r, http.StatusUnauthorized, "AUTHZ_WORKSPACE_ACCESS_REVOKED",
						"工作区访问权限已被撤销，请重新登录", nil)
					return
				case errors.Is(err, iam.ErrSessionInvalid):
					// 过期/已撤销：清理 Cookie，继续走未认证路径，
					// 由 require() 返回 401 而不是静默降级为兼容头。
					s.iam.Sessions().ClearCookiesFor(w, aud, s.secureCookieFor(r))
				default:
					if !errors.Is(err, context.Canceled) {
						s.deps.Log.Error("session authenticate failed", "err", err, "audience", aud)
					}
				}
			}
		}

		// LEGACY-CONSOLE-01：会话全部失败之后才轮到入口层注入的老控制台服务凭据。
		// 有真实会话时永远走上面的会话分支，因此新线在这一步不受任何影响。
		if p, ok := s.legacyConsole.principal(r); ok {
			ctx = withPrincipal(ctx, p)
			recordActor(ctx, p)
			ctx = auth.WithUser(ctx, userFromPrincipal(p))
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		if s.authMode.TrustsHeaders() {
			if h := auth.HeaderIdentity(r); h.ID != "" || h.Email != "" {
				ctx = auth.WithUser(ctx, h)
				ctx = withPrincipal(ctx, principalFromLegacyHeader(ctx, h))
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userFromPrincipal(p policy.Principal) *auth.User {
	role := ""
	if len(p.Roles) > 0 {
		role = p.Roles[0]
	}
	return &auth.User{
		ID: p.IdentityID, Email: p.Email, DisplayName: p.DisplayName,
		OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID,
		Role: role, Roles: p.Roles, SessionID: p.SessionID, Audience: p.Audience,
		Source: "session",
	}
}

func principalFromSession(ctx context.Context, sess iam.Session, engine *policy.Engine) policy.Principal {
	p := policy.Principal{
		IdentityID: sess.IdentityID, Email: sess.Identity.Email, DisplayName: sess.Identity.DisplayName,
		OrganizationID: sess.OrganizationID, WorkspaceID: sess.WorkspaceID,
		SessionID: sess.ID, DeviceID: sess.DeviceID, Audience: sess.Audience,
		Roles: sess.Roles, Authenticated: true,
		// The flag comes from the session row, which loadByToken re-reads from identities on
		// every request: no extra query and no stale cache. See mustchangepw.go.
		MustChangePassword: sess.Identity.MustChangePassword,
	}
	if engine != nil {
		perms, roles, err := engine.Load(ctx, sess.IdentityID, sess.OrganizationID)
		if err == nil {
			p.Permissions = perms
			if len(roles) > 0 {
				p.Roles = roles
			}
		}
	}
	return p
}

func principalFromLegacyHeader(_ context.Context, h *auth.User) policy.Principal {
	return policy.Principal{
		IdentityID: h.ID, Email: h.Email, DisplayName: h.DisplayName,
		OrganizationID: h.OrganizationID, WorkspaceID: h.WorkspaceID,
		Audience: iam.AudienceUser, Roles: h.Roles,
		Authenticated: h.ID != "" || h.Email != "",
		// 兼容头的权限按历史语义放开，仅用于测试环境回归对照。
		Permissions: policy.Keys(),
		System:      true,
	}
}

// declared 包装一个 handler，声明它需要的权限。声明同时写入上下文供审计使用。
func declared(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), routePermCtxKey{}, permission)
		next(w, r.WithContext(ctx))
	}
}

// require 执行授权判定：未认证 401，已认证但无权限 403，并记录 deny 事件。
func (s *Server) require(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), routePermCtxKey{}, permission)
		r = r.WithContext(ctx)
		p := PrincipalFrom(ctx)

		if !p.Authenticated {
			writeErrorCode(w, r, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
			s.auditDeny(r, p, permission, policy.Decision{Permission: permission, Reason: "unauthenticated"}, http.StatusUnauthorized)
			return
		}
		decision := s.decide(r, p, permission)
		observability.RecordAuthzDecision(boolLabel(decision.Allowed), "route")
		if !decision.Allowed {
			observability.RecordAuthzDenied(permission, decision.Reason)
			writeErrorCode(w, r, http.StatusForbidden, "permission_denied", "当前身份没有该操作的权限", map[string]any{
				"permission": permission, "reason": decision.Reason,
			})
			s.auditDeny(r, p, permission, decision, http.StatusForbidden)
			return
		}
		// Session-scope staleness runs BEFORE the CSRF check on purpose: checkCSRF
		// re-authenticates the session cookie, so a session that was revoked while
		// this request's body was still uploading must not be judged by the CSRF gate
		// alone. Ordering narrows that window but cannot close it -- the session can
		// still be revoked between the guard read below and the CSRF re-authentication,
		// which is exactly why the CSRF gate classifies csrfSessionNotLive as 401
		// AUTHN_SESSION_MISSING instead of 403 csrf_failed. The guard is a read-only
		// decision that can only refuse, so running it first weakens nothing.
		if !s.guardSessionWorkspace(w, r, p, permission) {
			return
		}
		// CSRF Cookie 名与会话受众绑定：必须用主体自己的受众，而不是路径推断值，
		// 否则后台会话调用 /api/auth/v1/* 自助端点时会取错 Cookie。
		if s.csrfRequired(r, p) {
			switch s.checkCSRFOutcome(r, p.Audience) {
			case csrfSessionNotLive:
				// The session row stopped authenticating under this request (concurrent
				// revoke-all / admin revoke / other device's logout). The caller is no
				// longer logged in: 401, never 403 csrf_failed.
				s.clearDeadSessionCookies(w, r, p.Audience)
				writeErrorCode(w, r, http.StatusUnauthorized, "AUTHN_SESSION_MISSING", "未登录", nil)
				s.auditDenyCodeDetail(r, p, permission, "session_missing", "AUTHN_SESSION_MISSING", http.StatusUnauthorized, nil)
				return
			case csrfTokenInvalid:
				writeErrorCode(w, r, http.StatusForbidden, "csrf_failed", "请求校验失败，请刷新页面后重试", nil)
				s.auditDenyCodeDetail(r, p, permission, "csrf_failed", "AUTHZ_CSRF_FAILED", http.StatusForbidden, nil)
				return
			}
		}
		// P1-A calibration seam: no-op in production builds (see
		// scopecommit_calib_off.go). It marks the exact point at which the
		// authorization decision has been taken and the handler has not run.
		scopeCommitCalibDelay(r, "pre")
		// P1-A commit gate: the decision above ran after the body arrived but still
		// before the handler. The gate opens the request transaction plus an
		// optimistic scope token, so every PG write of the handler joins ONE
		// transaction and the release compares the token again: unchanged ->
		// COMMIT, changed -> ROLLBACK of the whole request and 409. The caller
		// session row is locked only for the release re-read and the COMMIT, so a
		// scope mutation never waits for the handler (AUTH-007 <= 5 s).
		gateWriter, gateReq, release, ok := s.enterScopeCommitGate(w, r, p, permission)
		if !ok {
			return
		}
		defer release()
		scopeCommitCalibDelay(gateReq, "post")
		next(gateWriter, gateReq)
	}
}

// requireAny 是 require 的多权限变体：主体持有其中任意一个即通过，并对**实际命中的那个**权限
// 做判定与审计（不会因为"有一个通过"就跳过 CSRF 与 deny 记录）。
//
// 使用场景（S7）：同一路径下由 {command} 决定具体动作的路由，例如
// POST /providers/{id}/commands/{command} —— 入口必须允许"读 / 改配置 / 轮换密钥"三类主体进入，
// 再由 handler 按命令做精确鉴权。若入口只写 admin:provider:read，
// 则 policy 目录里被授予 admin:provider:key:rotate 的 security_admin 反而永远进不来。
func (s *Server) requireAny(permissions []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := PrincipalFrom(r.Context())
		if !p.Authenticated {
			ctx := context.WithValue(r.Context(), routePermCtxKey{}, strings.Join(permissions, "|"))
			r = r.WithContext(ctx)
			writeErrorCode(w, r, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
			s.auditDeny(r, p, strings.Join(permissions, "|"), policy.Decision{Permission: strings.Join(permissions, "|"), Reason: "unauthenticated"}, http.StatusUnauthorized)
			return
		}
		matched := ""
		var lastDecision policy.Decision
		for _, perm := range permissions {
			decision := s.decide(r, p, perm)
			observability.RecordAuthzDecision(boolLabel(decision.Allowed), "route")
			lastDecision = decision
			if decision.Allowed {
				matched = perm
				break
			}
		}
		label := strings.Join(permissions, "|")
		if matched == "" {
			observability.RecordAuthzDenied(label, lastDecision.Reason)
			writeErrorCode(w, r, http.StatusForbidden, "permission_denied", "当前身份没有该操作的权限", map[string]any{
				"permissions": jsonStrings(permissions), "reason": lastDecision.Reason,
			})
			s.auditDeny(r, p, label, lastDecision, http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), routePermCtxKey{}, matched)
		r = r.WithContext(ctx)
		// Same order and the same classification as require: scope staleness first,
		// CSRF second, and a session that died under the request is reported as 401
		// AUTHN_SESSION_MISSING rather than as a CSRF failure.
		if !s.guardSessionWorkspace(w, r, p, matched) {
			return
		}
		if s.csrfRequired(r, p) {
			switch s.checkCSRFOutcome(r, p.Audience) {
			case csrfSessionNotLive:
				s.clearDeadSessionCookies(w, r, p.Audience)
				writeErrorCode(w, r, http.StatusUnauthorized, "AUTHN_SESSION_MISSING", "未登录", nil)
				s.auditDenyCodeDetail(r, p, matched, "session_missing", "AUTHN_SESSION_MISSING", http.StatusUnauthorized, nil)
				return
			case csrfTokenInvalid:
				writeErrorCode(w, r, http.StatusForbidden, "csrf_failed", "请求校验失败，请刷新页面后重试", nil)
				s.auditDenyCodeDetail(r, p, matched, "csrf_failed", "AUTHZ_CSRF_FAILED", http.StatusForbidden, nil)
				return
			}
		}
		// P1-A calibration seam, identical to the one in require.
		scopeCommitCalibDelay(r, "pre")
		// Same commit gate as require, keyed by the permission that actually matched.
		gateWriter, gateReq, release, ok := s.enterScopeCommitGate(w, r, p, matched)
		if !ok {
			return
		}
		defer release()
		scopeCommitCalibDelay(gateReq, "post")
		next(gateWriter, gateReq)
	}
}

func boolLabel(ok bool) string {
	if ok {
		return "allow"
	}
	return "deny"
}

func (s *Server) decide(r *http.Request, p policy.Principal, permission string) policy.Decision {
	if s.policy == nil {
		return policy.Decision{Permission: permission, Reason: "policy_engine_unavailable"}
	}
	decision := s.policy.Authorize(p, permission)
	if decision.Allowed {
		return decision
	}
	// ABAC：同一权限下按对象作用域拒绝跨工作区访问（§4.2 条件）。
	if decision.Reason == "missing_permission" && s.policyDenyByScope(r, p, permission) {
		return policy.Decision{Permission: permission, Reason: "workspace_scope_mismatch", RiskLevel: decision.RiskLevel}
	}
	return decision
}

// policyDenyByScope 是预留的对象级条件钩子。当前实现只校验工作区头与主体一致，
// 完整对象级授权在各 feature service 内二次执行（§28.5 S3-BE-01）。
func (s *Server) policyDenyByScope(r *http.Request, p policy.Principal, _ string) bool {
	claimed := strings.TrimSpace(r.Header.Get("X-Workspace-Id"))
	return claimed != "" && p.WorkspaceID != "" && claimed != p.WorkspaceID
}

// csrfRequired 只对"由 Cookie 鉴权且会改变状态"的请求生效。
func (s *Server) csrfRequired(r *http.Request, p policy.Principal) bool {
	if s.iam == nil {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return p.SessionID != "" && !p.System
}

// csrfOutcome is the classified result of the double-submit CSRF gate.
//
// The distinction between a wrong token and a dead session is not cosmetic. A live
// session that presents a missing or wrong token is a real CSRF failure and must stay a
// 403 csrf_failed. A session row that stopped authenticating between the scope guard and
// this re-authentication is NOT a CSRF failure: the caller is no longer logged in, and
// answering 403 csrf_failed tells the browser to refresh its token and retry -- which can
// never succeed, because the session it would refresh is gone. That masking is the
// P1-1/AUTH-007 defect proven by the revoke-all concurrency gate (5 of 16 concurrent
// requests answered 403 AUTHZ_CSRF_FAILED after their session was revoked by a sibling
// request). Such requests answer 401 AUTHN_SESSION_MISSING instead.
type csrfOutcome int

const (
	// csrfOK: the session is live and the double-submit token matches.
	csrfOK csrfOutcome = iota
	// csrfTokenInvalid: the session is live, but the CSRF Cookie/header is missing or
	// wrong, or the session could not be re-read for a reason that is NOT proof that it
	// is gone (pool exhaustion, cancelled context). Fail closed: still refused.
	csrfTokenInvalid
	// csrfSessionNotLive: the session behind the Cookie no longer authenticates
	// (revoked, expired, rotated away, or its workspace membership was revoked).
	csrfSessionNotLive
)

// csrfOutcomeForSessionError classifies a failed session authentication. Only errors that
// positively mean "this session is not usable any more" are reported as
// csrfSessionNotLive; every other error stays a CSRF-class refusal, so an infrastructure
// failure can never be dressed up as "the user is simply logged out".
func csrfOutcomeForSessionError(err error) csrfOutcome {
	switch {
	case err == nil:
		return csrfOK
	case errors.Is(err, iam.ErrSessionInvalid):
		return csrfSessionNotLive
	case errors.Is(err, iam.ErrSessionReuse):
		return csrfSessionNotLive
	case errors.Is(err, iam.ErrWorkspaceAccessRevoked):
		return csrfSessionNotLive
	default:
		return csrfTokenInvalid
	}
}

// checkCSRF keeps the boolean form for callers that only need allow/refuse.
func (s *Server) checkCSRF(r *http.Request, audience string) bool {
	return s.checkCSRFOutcome(r, audience) == csrfOK
}

// checkCSRFOutcome performs the double-submit CSRF check and classifies the refusal.
//
// Order matters and is deliberate: the SESSION is authenticated first, so that a session
// which disappeared under the request is reported as csrfSessionNotLive (401) whatever the
// token comparison would have said. Only a session that is still live can reach the token
// comparison, and only a live session with a bad token is csrfTokenInvalid (403). Nothing
// is relaxed by this order: a live session with a missing or wrong token is still refused,
// the tokens are still compared with the DB-bound hash, and the Cookie names are still
// resolved through the same candidate list as identityMiddleware.
func (s *Server) checkCSRFOutcome(r *http.Request, audience string) csrfOutcome {
	if s.iam == nil {
		return csrfOK
	}
	cfg := s.iam.Sessions().Config()
	if audience == "" {
		audience = audienceForPath(r.URL.Path)
	}
	// S7-SEC-01 R-05 修复回归：这里必须与 identityMiddleware / 写入路径使用同一套
	// "候选名" 逻辑。CookieName() 会**无条件**加上 NamePrefix（__Host-），
	// 而 HTTP 入口写入的是不带前缀的名字 —— 用单名查找会找不到会话 Cookie，
	// 结果是"CSRF token 正确却仍 403 csrf_failed"，把所有直连 :8080 的验收链路打红。
	sessionCookie, ok := cookieByName(r, cfg.CookieNameCandidates(audience))
	if !ok {
		// No session Cookie at all: the request cannot be a live cookie-authenticated
		// write, and there is no session to declare dead.
		return csrfTokenInvalid
	}
	sess, err := s.iam.Sessions().Authenticate(r.Context(), sessionCookie.Value, audience)
	if outcome := csrfOutcomeForSessionError(err); outcome != csrfOK {
		return outcome
	}
	cookie, ok := cookieByName(r, cfg.CSRFSecretCookieCandidates(audience))
	if !ok {
		return csrfTokenInvalid
	}
	header := strings.TrimSpace(r.Header.Get(cfg.CSRFHeader))
	if header == "" {
		header = strings.TrimSpace(r.Header.Get("X-Csrf-Token"))
	}
	if header == "" || header != cookie.Value {
		return csrfTokenInvalid
	}
	if !s.iam.Sessions().ValidateCSRF(sess, header, cookie.Value) {
		return csrfTokenInvalid
	}
	return csrfOK
}

// clearDeadSessionCookies expires the session and CSRF Cookies of a session that was found
// dead mid-request, so the browser stops replaying a token that can never authenticate
// again. It mirrors what identityMiddleware does when it detects the same condition at
// request start. It must run before the response body is written.
func (s *Server) clearDeadSessionCookies(w http.ResponseWriter, r *http.Request, audience string) {
	if s.iam == nil {
		return
	}
	if audience == "" {
		audience = audienceForPath(r.URL.Path)
	}
	s.iam.Sessions().ClearCookiesFor(w, audience, s.secureCookieFor(r))
}

// auditDeny 记录授权拒绝事件（§6.6.1 "每次 401/403 都记录"）。
func (s *Server) auditDeny(r *http.Request, p policy.Principal, permission string, d policy.Decision, status int) {
	if s.audit == nil {
		return
	}
	meta := metaFrom(r.Context())
	risk := d.RiskLevel
	if risk == "" {
		risk = policy.RiskOf(permission)
	}
	event := audit.Event{
		ActorType: "anonymous", ActorID: p.IdentityID, ActorEmail: p.Email, ActorName: p.DisplayName,
		ActorRoles: p.Roles, OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID,
		SessionID: p.SessionID, DeviceID: p.DeviceID,
		Action: "authorization.denied", Category: "authorization",
		ResourceType: resourceFromPath(r.URL.Path), ResourceID: "",
		HTTPMethod: r.Method, Route: r.URL.Path, RequestID: meta.RequestID, TraceID: meta.TraceID,
		Result: "deny", HTTPStatus: status, ErrorCode: "permission_denied",
		PermissionKey: permission, PolicyDecision: "deny", DenyReason: d.Reason,
		RiskLevel: risk, Sensitive: policy.Sensitive(permission),
		PeerIP: meta.IP.PeerIP, ClientIP: meta.IP.ClientIP, ProxyChain: meta.IP.ProxyChain,
		UserAgent: meta.UserAgent,
		Detail:    map[string]any{"reason": d.Reason},
	}
	if p.Authenticated {
		event.ActorType = "user"
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.audit.Write(ctx, event); err != nil {
			s.deps.Log.Error("audit deny write failed", "err", err, "permission", permission)
		}
	}()
}

// auditBusiness 记录已授权的业务操作。策略（§6.6.1 "所有业务动作"）：
//   - 所有非 GET 方法（写操作）都记录；
//   - GET 仅在权限被标记为敏感时记录，避免把普通浏览放大成写风暴。
func (s *Server) auditBusiness(r *http.Request, status int, duration time.Duration, written int) {
	if s.audit == nil {
		return
	}
	permission := declaredPermission(r.Context())
	if permission == "" {
		return
	}
	write := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
	if !write && !policy.Sensitive(permission) {
		return
	}
	p := PrincipalFrom(r.Context())
	meta := metaFrom(r.Context())
	risk := policy.RiskOf(permission)
	result := "success"
	if status >= 500 {
		result = "error"
	} else if status >= 400 {
		result = "failure"
	}
	event := audit.Event{
		ActorType: "user", ActorID: p.IdentityID, ActorEmail: p.Email, ActorName: p.DisplayName,
		ActorRoles: p.Roles, OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID,
		SessionID: p.SessionID, DeviceID: p.DeviceID,
		Action: permission, Category: categoryOfPermission(permission),
		ResourceType: resourceFromPath(r.URL.Path), ResourceID: r.PathValue("id"),
		HTTPMethod: r.Method, Route: r.URL.Path, RequestID: meta.RequestID, TraceID: meta.TraceID,
		Result: result, HTTPStatus: status, DurationMS: int(duration.Milliseconds()),
		PermissionKey: permission, PolicyDecision: "allow",
		RiskLevel: risk, Sensitive: policy.Sensitive(permission),
		PeerIP: meta.IP.PeerIP, ClientIP: meta.IP.ClientIP, ProxyChain: meta.IP.ProxyChain,
		UserAgent: meta.UserAgent,
		Detail:    map[string]any{"bytes_written": written, "query": sanitizedQuery(r)},
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.audit.Write(ctx, event); err != nil {
			s.deps.Log.Error("audit write failed", "err", err, "permission", permission)
		}
	}()
}

// sanitizedQuery 记录查询条件摘要，禁止把可疑的敏感参数写入审计。
func sanitizedQuery(r *http.Request) map[string]string {
	out := map[string]string{}
	for k := range r.URL.Query() {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "token") || strings.Contains(lk, "password") || strings.Contains(lk, "secret") || strings.Contains(lk, "key") {
			continue
		}
		out[k] = r.URL.Query().Get(k)
	}
	return out
}

func categoryOfPermission(permission string) string {
	if def, ok := policy.Lookup(permission); ok {
		return def.Category
	}
	return "unknown"
}

func resourceFromPath(path string) string {
	trimmed := strings.TrimPrefix(path, "/api/")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return trimmed
}

// ---------------------------------------------------------------------------
// Session workspace staleness guard (sprint7 C1-RACE, defence in depth).
//
// Measured defect: a state-changing request that was authenticated while the session was in
// workspace A could still commit into workspace A after the same session had been switched
// to workspace B -- the principal is resolved once, at the start of the request, while the
// request body is still being uploaded. The security-critical variant (membership revoked in
// A) is already fail closed by the feature-level membership re-check; this guard closes the
// remaining window, where the SESSION workspace itself goes stale mid-request.
//
// Contract: for state-changing requests only (never GET/HEAD/OPTIONS) the guard compares
// principal.workspace_id with the CURRENT workspace of the live session row, read with one
// primary-key lookup. A missing or revoked session row, a different workspace, and any
// database error all refuse with HTTP 409 and the stable code
// AUTHZ_WORKSPACE_SCOPE_STALE. Safe methods never touch the database.
// ---------------------------------------------------------------------------

// ErrorAUTHZWorkspaceScopeStale is the stable refusal code (HTTP 409) for a state-changing
// request whose session workspace no longer matches the live session row.
const ErrorAUTHZWorkspaceScopeStale = "AUTHZ_WORKSPACE_SCOPE_STALE"

// workspaceScopeStaleMessage is the user-facing text of that refusal. It names no object, no
// workspace and no session identifier.
const workspaceScopeStaleMessage = "The session workspace changed while this request was in flight; reload the session and retry."

// sessionScopeReader reads the CURRENT workspace of a live session row.
//
// It is an interface so the guard can be unit-tested without PostgreSQL; in production
// Server.sessionScope stays nil and the lookup below runs against s.deps.Store.Pool.
type sessionScopeReader interface {
	LiveSessionWorkspace(ctx context.Context, sessionID string) (workspaceID string, found bool, err error)
}

// sessionWorkspaceLookup is the production implementation: one indexed primary-key lookup on
// the sessions table. It selects the workspace of the row only while it is still live.
type sessionWorkspaceLookup struct {
	pool *dbtx.Pool
}

// sessionWorkspaceQuery reads the live session row for the staleness guard. The session id
// is a UUID primary key, so this is a single index lookup.
const sessionWorkspaceQuery = "SELECT workspace_id::text FROM sessions WHERE id = $1::uuid AND revoked_at IS NULL"

func (l sessionWorkspaceLookup) LiveSessionWorkspace(ctx context.Context, sessionID string) (string, bool, error) {
	var workspaceID *string
	err := l.pool.QueryRow(ctx, sessionWorkspaceQuery, sessionID).Scan(&workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if workspaceID == nil {
		return "", true, nil
	}
	return *workspaceID, true, nil
}

// liveSessionWorkspace resolves the reader: an injected stub (tests) or the production pool.
// A server without a usable pool cannot verify the session scope, which the caller treats as
// fail closed.
func (s *Server) liveSessionWorkspace(ctx context.Context, sessionID string) (string, bool, error) {
	if s.sessionScope != nil {
		return s.sessionScope.LiveSessionWorkspace(ctx, sessionID)
	}
	if s.deps.Store == nil || s.deps.Store.Pool == nil {
		return "", false, errSessionScopeUnverifiable
	}
	return sessionWorkspaceLookup{pool: s.deps.Store.Pool}.LiveSessionWorkspace(ctx, sessionID)
}

// errSessionScopeUnverifiable means the session scope could not be read at all; callers must
// fail closed, never allow.
var errSessionScopeUnverifiable = errors.New("session workspace unverifiable")

// needsSessionWorkspaceCheck reports whether the staleness guard applies to this request.
// Only cookie-authenticated, session-scoped, state-changing requests are checked: an
// organisation-level principal without a session workspace, a system/compat principal and
// every safe method are out of scope and cost no database round trip.
func needsSessionWorkspaceCheck(r *http.Request, p policy.Principal) bool {
	if !stateChangingMethod(r.Method) {
		return false
	}
	return !p.System && p.SessionID != "" && p.WorkspaceID != ""
}

// guardSessionWorkspace is called by require/requireAny after the permission decision and
// BEFORE the CSRF check, for state-changing requests only. It returns false when the request
// was already refused, in which case the caller must not run the handler.
//
// The ordering is part of the contract, not an accident. This doc comment used to claim
// "after the CSRF check", which had been false ever since the guard was moved ahead of the
// CSRF gate (the call sites were updated, this comment was not) -- that stale comment is
// the reason the ordering looked enforced when only the call-site comment said so. What the
// ordering buys: an ordinary revoked-session write keeps the long-standing 409
// session_missing / session_workspace_changed answer (c1-session-scope-convergence-gate-v4)
// instead of being judged by the CSRF gate first. What it does NOT buy: it cannot close the
// window between this read and the CSRF gate's own session re-authentication. That window
// is closed inside the CSRF gate, which reports a session that died under the request as
// 401 AUTHN_SESSION_MISSING (csrfSessionNotLive) and never as 403 csrf_failed. The guard's
// own 409 semantics are unchanged.
//
// Fail closed. The refusal reasons are:
//
//	request_body_not_fully_received -- scopeBodyMiddleware did not run, so the body may still
//	    be in flight and the comparison below would be meaningless. Defence in depth: it can
//	    only refuse, never allow.
//	session_scope_unverifiable       -- the live session row could not be read at all.
//	session_missing                  -- the session row is gone or revoked (logout, revoke-all,
//	    admin revoke, password change).
//	session_workspace_changed        -- the session moved to another workspace while the
//	    request body was being uploaded.
//
// Every refusal is 409 AUTHZ_WORKSPACE_SCOPE_STALE plus a synchronous deny audit row that
// carries request/trace, the route, the old scope taken from the authenticated snapshot and
// the new scope read from PostgreSQL.
func (s *Server) guardSessionWorkspace(w http.ResponseWriter, r *http.Request, p policy.Principal, permission string) bool {
	if !needsSessionWorkspaceCheck(r, p) {
		return true
	}
	if !scopeBodyFullyReceived(r.Context()) {
		writeErrorCode(w, r, http.StatusConflict, ErrorAUTHZWorkspaceScopeStale, workspaceScopeStaleMessage, nil)
		s.auditDenyCodeDetail(r, p, permission, "request_body_not_fully_received",
			ErrorAUTHZWorkspaceScopeStale, http.StatusConflict, map[string]any{
				"old_workspace_id": p.WorkspaceID,
			})
		return false
	}
	current, found, err := s.liveSessionWorkspace(r.Context(), p.SessionID)
	reason := ""
	detail := map[string]any{"old_workspace_id": p.WorkspaceID}
	switch {
	case err != nil:
		reason = "session_scope_unverifiable"
	case !found:
		reason = "session_missing"
	case current != p.WorkspaceID:
		reason = "session_workspace_changed"
	default:
		return true
	}
	if found && current != "" {
		detail["new_workspace_id"] = current
	}
	if err != nil && s.deps.Log != nil {
		s.deps.Log.Error("session workspace lookup failed; failing closed",
			"err", err, "session_id", p.SessionID, "route", r.URL.Path)
	}
	writeErrorCode(w, r, http.StatusConflict, ErrorAUTHZWorkspaceScopeStale, workspaceScopeStaleMessage, nil)
	s.auditDenyCodeDetail(r, p, permission, reason, ErrorAUTHZWorkspaceScopeStale, http.StatusConflict, detail)
	return false
}

// denyDetail builds the audit detail of a denial: the reason plus any explicit extra
// fields. The reason key always wins, so a caller cannot overwrite it.
func denyDetail(reason string, extra map[string]any) map[string]any {
	detail := make(map[string]any, len(extra)+1)
	for k, v := range extra {
		detail[k] = v
	}
	detail["reason"] = reason
	return detail
}

// auditDenyCode records an authorization denial whose stable error code is not
// permission_denied (for example AUTHZ_WORKSPACE_SCOPE_STALE or
// AUTHN_PASSWORD_CHANGE_REQUIRED). It keeps the existing deny convention -- action
// authorization.denied, category authorization -- and adds the accurate error code, the deny
// reason, the route and the request id.
//
// The write is synchronous and detached from the client connection (auditRequestDetached) so
// the row survives a client disconnect. A failed write is logged but never turns the refusal
// into an allow: the refusal is the fail-closed decision. It never records a password, a
// password hash or a session token.
func (s *Server) auditDenyCode(r *http.Request, p policy.Principal, permission, reason, code string, status int) {
	s.auditDenyCodeDetail(r, p, permission, reason, code, status, nil)
}

// auditDenyCodeDetail is auditDenyCode with extra detail fields (for example the old and the
// new workspace scope of a stale-scope refusal). The extra map is merged into the event
// detail; it must never carry a secret.
func (s *Server) auditDenyCodeDetail(r *http.Request, p policy.Principal, permission, reason, code string, status int, extra map[string]any) {
	if s.auditSink == nil && s.audit == nil {
		return
	}
	meta := metaFrom(r.Context())
	risk := policy.RiskHigh
	if permission != "" {
		if declared := policy.RiskOf(permission); declared != "" {
			risk = declared
		}
	}
	event := audit.Event{
		ActorType: "user", ActorID: p.IdentityID, ActorEmail: p.Email, ActorName: p.DisplayName,
		ActorRoles: p.Roles, OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID,
		SessionID: p.SessionID, DeviceID: p.DeviceID,
		Action: "authorization.denied", Category: "authorization",
		ResourceType: resourceFromPath(r.URL.Path), ResourceID: r.PathValue("id"),
		HTTPMethod: r.Method, Route: r.URL.Path, RequestID: meta.RequestID, TraceID: meta.TraceID,
		Result: "deny", HTTPStatus: status, ErrorCode: code,
		PermissionKey: permission, PolicyDecision: "deny", DenyReason: reason,
		RiskLevel: risk, Sensitive: true,
		PeerIP: meta.IP.PeerIP, ClientIP: meta.IP.ClientIP, ProxyChain: meta.IP.ProxyChain,
		UserAgent: meta.UserAgent,
		Detail:    denyDetail(reason, extra),
	}
	if err := s.emitAudit(auditRequestDetached(r).Context(), event); err != nil && s.deps.Log != nil {
		s.deps.Log.Error("audit deny write failed", "err", err, "error_code", code, "route", r.URL.Path)
	}
}
