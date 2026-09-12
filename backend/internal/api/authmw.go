package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

type principalCtxKey struct{}
type requestMetaCtxKey struct{}

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
				c, err := r.Cookie(cfg.CookieName(aud))
				if err != nil || c.Value == "" {
					continue
				}
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
					s.iam.Sessions().ClearCookies(w, aud)
				case errors.Is(err, iam.ErrSessionInvalid):
					// 过期/已撤销：清理 Cookie，继续走未认证路径，
					// 由 require() 返回 401 而不是静默降级为兼容头。
					s.iam.Sessions().ClearCookies(w, aud)
				default:
					if !errors.Is(err, context.Canceled) {
						s.deps.Log.Error("session authenticate failed", "err", err, "audience", aud)
					}
				}
			}
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
			writeErrorCode(w, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
			s.auditDeny(r, p, permission, policy.Decision{Permission: permission, Reason: "unauthenticated"}, http.StatusUnauthorized)
			return
		}
		decision := s.decide(r, p, permission)
		observability.RecordAuthzDecision(boolLabel(decision.Allowed), "route")
		if !decision.Allowed {
			observability.RecordAuthzDenied(permission, decision.Reason)
			writeErrorCode(w, http.StatusForbidden, "permission_denied", "当前身份没有该操作的权限", map[string]any{
				"permission": permission, "reason": decision.Reason,
			})
			s.auditDeny(r, p, permission, decision, http.StatusForbidden)
			return
		}
		// CSRF Cookie 名与会话受众绑定：必须用主体自己的受众，而不是路径推断值，
		// 否则后台会话调用 /api/auth/v1/* 自助端点时会取错 Cookie。
		if s.csrfRequired(r, p) && !s.checkCSRF(r, p.Audience) {
			writeErrorCode(w, http.StatusForbidden, "csrf_failed", "请求校验失败，请刷新页面后重试", nil)
			s.auditDeny(r, p, permission, policy.Decision{Permission: permission, Reason: "csrf_failed"}, http.StatusForbidden)
			return
		}
		next(w, r)
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

func (s *Server) checkCSRF(r *http.Request, audience string) bool {
	if s.iam == nil {
		return true
	}
	cfg := s.iam.Sessions().Config()
	if audience == "" {
		audience = audienceForPath(r.URL.Path)
	}
	cookie, err := r.Cookie(cfg.CSRFSecretCookie(audience))
	if err != nil {
		return false
	}
	header := strings.TrimSpace(r.Header.Get(cfg.CSRFHeader))
	if header == "" {
		header = strings.TrimSpace(r.Header.Get("X-Csrf-Token"))
	}
	if header == "" || header != cookie.Value {
		return false
	}
	sessionCookie, err := r.Cookie(cfg.CookieName(audience))
	if err != nil {
		return false
	}
	sess, err := s.iam.Sessions().Authenticate(r.Context(), sessionCookie.Value, audience)
	if err != nil {
		return false
	}
	return s.iam.Sessions().ValidateCSRF(sess, header, cookie.Value)
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
