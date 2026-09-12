package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// registerAuthV1 挂载 §21.4 认证 API。
//
// 这些路由不经过 require()：login/logout 自身就是身份入口。
// 其余需要会话的路由在 handler 内显式校验 session。
func (s *Server) registerAuthV1(r chi.Router) {
	r.Get("/methods", s.handleAuthMethods)

	r.Group(func(pr chi.Router) {
		// 登录类接口：public，但受 §27 SEC-004 限流与登录事件审计约束。
		pr.Post("/login", s.handleAuthLogin)
		pr.Post("/logout", s.handleAuthLogout)
		pr.Post("/refresh", s.handleAuthRefresh)
		pr.Get("/session", s.handleAuthSession)
		pr.Get("/sessions", s.handleAuthListSessions)
		pr.Delete("/sessions/{id}", s.handleAuthRevokeSession)
		pr.Post("/sessions/revoke-all", s.handleAuthRevokeAll)
		pr.Get("/devices", s.handleAuthDevices)
		pr.Patch("/devices/{id}", s.handleAuthUpdateDevice)
		pr.Post("/password/change", s.handleAuthChangePassword)

		// OIDC 适配器位置已预留，但未实现前必须显式返回未启用，不得伪装为可用能力。
		pr.Get("/oidc/start", s.handleNotImplemented("OIDC_NOT_CONFIGURED"))
		pr.Get("/oidc/callback", s.handleNotImplemented("OIDC_NOT_CONFIGURED"))
		pr.Post("/mfa/verify", s.handleNotImplemented("MFA_NOT_IMPLEMENTED"))
	})
}

func (s *Server) handleNotImplemented(code string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respondError(w, r, http.StatusNotImplemented, code, "该能力尚未实现", nil)
	}
}

type loginRequest struct {
	Identifier     string `json:"identifier"`
	Email          string `json:"email"`
	Password       string `json:"password"`
	RememberDevice bool   `json:"remember_device"`
	ReturnTo       string `json:"return_to"`
	Audience       string `json:"audience"`
}

func (s *Server) authReady(w http.ResponseWriter, r *http.Request) bool {
	if s.iam == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "认证服务未初始化", nil)
		return false
	}
	return true
}

func (s *Server) handleAuthMethods(w http.ResponseWriter, r *http.Request) {
	respondOK(w, r, map[string]any{
		"local":        s.iam != nil,
		"oidc":         false,
		"mfa_required": s.cfg != nil && s.cfg.Auth.RequireAdminMFA,
		"audiences":    []string{iam.AudienceUser, iam.AudienceAdmin},
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authReady(w, r) {
		return
	}
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", []fieldError{{Field: "body", Code: "invalid_json", Message: err.Error()}})
		return
	}
	identifier := req.Identifier
	if identifier == "" {
		identifier = req.Email
	}
	// 未显式声明时，按客户端声明的受众/路径受众推断（后台前端位于 /admin/*）。
	audience := req.Audience
	if audience == "" {
		audience = requestedAudience(r)
	}
	if audience == "" {
		audience = iam.AudienceUser
	}
	if audience != iam.AudienceUser && audience != iam.AudienceAdmin {
		validationError(w, r, "audience 取值非法", []fieldError{{Field: "audience", Code: "invalid_value", Message: "只允许 user 或 admin"}})
		return
	}
	meta := metaFrom(r.Context())

	result, err := s.iam.Login(r.Context(), iam.LoginInput{
		Email: identifier, Password: req.Password, Audience: audience,
		IP: meta.IP, UserAgent: meta.UserAgent, ClientHints: iam.ClientHints(r.Header),
		EntryHost: meta.Host, Scheme: meta.Scheme,
		RequestID: meta.RequestID, TraceID: meta.TraceID,
	})
	if err != nil {
		status, code := iam.StatusForError(err)
		// §21.3 / §27 SEC-004：429 必须带 Retry-After，客户端才能正确退避；
		// 缺少该头会让调用方直接重试，反而放大冲击。
		if status == http.StatusTooManyRequests {
			retryAfter := s.iam.RetryAfterSeconds(identifier, meta.IP.ClientIP)
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			respondError(w, r, status, authErrCode(status, code), authErrMessage(code),
				map[string]any{"retry_after_seconds": retryAfter})
			return
		}
		// 客户端主动断开（浏览器导航、SSE 取消）不是服务端故障，不按 ERROR 记录。
		if status >= 500 && !errors.Is(err, context.Canceled) {
			// 内部错误必须留下可诊断的 cause，但绝不回显给客户端（§21.3）。
			s.deps.Log.Error("login failed", "err", err, "identifier_digest", iam.SubjectDigest(identifier),
				"audience", audience, "request_id", meta.RequestID)
		}
		respondError(w, r, status, authErrCode(status, code), authErrMessage(code), nil)
		return
	}

	s.iam.Sessions().SetCookies(w, audience, result.Token, result.CSRF)
	// `next` 是"应用内相对路径"（§21.4 契约示例 "next": "/home"），
	// 前端在 react-router basename 之下直接跳转，因此不能带 /admin 前缀。
	next := req.ReturnTo
	if next == "" {
		if audience == iam.AudienceAdmin {
			next = "/overview"
		} else {
			next = "/home"
		}
	}
	respondOK(w, r, map[string]any{
		"state":        "authenticated",
		"user":         userSummary(result.Session),
		"roles":        result.Roles,
		"audience":     audience,
		"next":         sanitizeReturnToDefault(next, defaultNextFor(audience)),
		"csrf_token":   result.CSRF,
		"session_id":   result.Session.ID,
		"must_change_password": result.Session.Identity.MustChangePassword,
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if !s.authReady(w, r) {
		return
	}
	audience := audienceForPath(r.URL.Path)
	if v := strings.TrimSpace(r.Header.Get("X-Auth-Audience")); v == iam.AudienceAdmin {
		audience = iam.AudienceAdmin
	}
	cfg := s.iam.Sessions().Config()
	sess, ok := s.currentSession(w, r, audience)
	if !ok {
		// 无会话也算退出成功（幂等），但仍清理 Cookie。
		s.iam.Sessions().ClearCookies(w, audience)
		respondOK(w, r, map[string]any{"state": "signed_out"})
		return
	}
	meta := metaFrom(r.Context())
	if err := s.iam.Logout(r.Context(), sess, iam.LoginInput{
		Audience: audience, IP: meta.IP, UserAgent: meta.UserAgent,
		EntryHost: meta.Host, Scheme: meta.Scheme, RequestID: meta.RequestID, TraceID: meta.TraceID,
		Email: sess.Identity.Email,
	}); err != nil {
		s.deps.Log.Error("logout failed", "err", err)
	}
	_ = cfg
	s.iam.Sessions().ClearCookies(w, audience)
	respondOK(w, r, map[string]any{"state": "signed_out"})
}

func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.authReady(w, r) {
		return
	}
	audience := audienceForPath(r.URL.Path)
	sess, ok := s.currentSession(w, r, audience)
	if !ok {
		respondError(w, r, http.StatusUnauthorized, "AUTHN_SESSION_INVALID", "会话已失效，请重新登录", nil)
		return
	}
	created, err := s.iam.Sessions().Rotate(r.Context(), sess)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "会话刷新失败", nil)
		return
	}
	s.iam.Sessions().SetCookies(w, audience, created.Token, created.CSRF)
	meta := metaFrom(r.Context())
	_ = iam.RecordLoginEvent(r.Context(), s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventSessionRefresh, Result: "success",
		IdentityID: sess.IdentityID, SubjectEmail: sess.Identity.Email,
		OrganizationID: sess.OrganizationID, WorkspaceID: sess.WorkspaceID,
		Audience: audience, SessionID: sess.ID, DeviceID: sess.DeviceID,
		DeviceInfo: iam.ParseUserAgent(meta.UserAgent), UserAgent: meta.UserAgent,
		IP: meta.IP, EntryHost: meta.Host, Scheme: meta.Scheme,
		RequestID: meta.RequestID, TraceID: meta.TraceID,
	})
	respondOK(w, r, map[string]any{"state": "refreshed", "csrf_token": created.CSRF, "session_id": sess.ID})
}

func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	if !s.authReady(w, r) {
		return
	}
	audience := audienceForPath(r.URL.Path)
	if v := strings.TrimSpace(r.Header.Get("X-Auth-Audience")); v == iam.AudienceAdmin {
		audience = iam.AudienceAdmin
	}
	if reuse, _ := r.Context().Value(sessionReuseCtxKey{}).(bool); reuse {
		respondError(w, r, http.StatusUnauthorized, "AUTHN_SESSION_REUSE",
			"检测到会话令牌重复使用，该会话族的全部设备已被强制下线，请重新登录", nil)
		return
	}
	sess, reuse, ok := s.currentSessionDetailed(w, r, audience)
	if !ok {
		if reuse {
			respondError(w, r, http.StatusUnauthorized, "AUTHN_SESSION_REUSE",
				"检测到会话令牌重复使用，该会话族的全部设备已被强制下线，请重新登录", nil)
			return
		}
		respondError(w, r, http.StatusUnauthorized, "AUTHN_SESSION_MISSING", "未登录", nil)
		return
	}
	p := PrincipalFrom(r.Context())
	// 受众必须以"实际解析出的会话"为准：自助端点允许跨受众回退（见 currentSession）。
	audience = sess.Audience
	csrf := ""
	if c, err := r.Cookie(s.iam.Sessions().Config().CSRFSecretCookie(audience)); err == nil {
		csrf = c.Value
	}
	respondOK(w, r, map[string]any{
		"user":        userSummary(sess),
		"email":       sess.Identity.Email,
		"display_name": sess.Identity.DisplayName,
		"organization_id": sess.OrganizationID,
		"workspace_id":    sess.WorkspaceID,
		"audience":        audience,
		"session_id":      sess.ID,
		"roles":           p.Roles,
		"permissions":     p.Permissions,
		"expires_at":      sess.ExpiresAt.UTC().Format(time.RFC3339),
		"absolute_expires_at": sess.AbsoluteExpires.UTC().Format(time.RFC3339),
		"must_change_password": sess.Identity.MustChangePassword,
		"mfa_satisfied":   sess.MFASatisfied,
		"csrf_token":      csrf,
	})
}

func (s *Server) handleAuthListSessions(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	list, total, err := s.iam.Sessions().List(r.Context(), iam.SessionFilter{
		IdentityID: sess.IdentityID, Limit: 50,
	})
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取会话列表失败", nil)
		return
	}
	respondList(w, r, list, total, nil)
}

func (s *Server) handleAuthRevokeSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	// 只能撤销自己的会话；越权返回 404 以免暴露会话存在性。
	list, _, err := s.iam.Sessions().List(r.Context(), iam.SessionFilter{IdentityID: sess.IdentityID, Limit: 200})
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取会话列表失败", nil)
		return
	}
	found := false
	for _, item := range list {
		if item.ID == id {
			found = true
			break
		}
	}
	if !found {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "会话不存在", nil)
		return
	}
	if err := s.iam.Sessions().RevokeByID(r.Context(), id, "self_revoked", sess.IdentityID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "撤销会话失败", nil)
		return
	}
	meta := metaFrom(r.Context())
	_ = iam.RecordLoginEvent(r.Context(), s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventSessionRevoked, Result: "success", IdentityID: sess.IdentityID,
		SubjectEmail: sess.Identity.Email, Audience: sess.Audience, SessionID: id,
		IP: meta.IP, UserAgent: meta.UserAgent, RiskLevel: "high",
		Detail: map[string]any{"by": "self"},
	})
	respondOK(w, r, map[string]any{"revoked": true})
}

func (s *Server) handleAuthRevokeAll(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var body struct {
		KeepCurrent bool `json:"keep_current"`
	}
	_ = decodeJSON(r, &body)
	n, err := s.iam.Sessions().RevokeAllForIdentity(r.Context(), sess.IdentityID, "self_revoked_all", sess.IdentityID)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "撤销会话失败", nil)
		return
	}
	if body.KeepCurrent {
		if created, err := s.iam.Sessions().Create(r.Context(), iam.SessionRequest{
			Audience: sess.Audience, IdentityID: sess.IdentityID, OrganizationID: sess.OrganizationID,
			WorkspaceID: sess.WorkspaceID, AuthMethod: sess.AuthMethod, MFASatisfied: sess.MFASatisfied,
			UserAgent: sess.UserAgent,
		}); err == nil {
			s.iam.Sessions().SetCookies(w, sess.Audience, created.Token, created.CSRF)
		}
	}
	meta := metaFrom(r.Context())
	_ = iam.RecordLoginEvent(r.Context(), s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventSessionForced, Result: "success", IdentityID: sess.IdentityID,
		SubjectEmail: sess.Identity.Email, Audience: sess.Audience, SessionID: sess.ID,
		IP: meta.IP, UserAgent: meta.UserAgent, RiskLevel: "high",
		Detail: map[string]any{"revoked": n, "keep_current": body.KeepCurrent, "by": "self"},
	})
	respondOK(w, r, map[string]any{"revoked": n})
}

func (s *Server) handleAuthDevices(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	devices, err := s.iam.Sessions().ListDevices(r.Context(), sess.IdentityID)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取设备列表失败", nil)
		return
	}
	respondOK(w, r, devices)
}

func (s *Server) handleAuthUpdateDevice(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var body struct {
		Label     string `json:"device_label"`
		TrustDays int    `json:"trust_days"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	err := s.iam.Sessions().UpdateDevice(r.Context(), sess.IdentityID, chi.URLParam(r, "id"), body.Label, body.TrustDays)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "更新设备失败", nil)
		return
	}
	respondOK(w, r, map[string]any{"updated": true})
}

func (s *Server) handleAuthChangePassword(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	meta := metaFrom(r.Context())
	err := s.iam.ChangePassword(r.Context(), sess.IdentityID, body.CurrentPassword, body.NewPassword, iam.LoginInput{
		Email: sess.Identity.Email, Audience: sess.Audience, IP: meta.IP, UserAgent: meta.UserAgent,
		RequestID: meta.RequestID, TraceID: meta.TraceID,
	})
	if err != nil {
		if err == iam.ErrInvalidCredentials {
			respondError(w, r, http.StatusUnauthorized, "AUTHN_INVALID_CREDENTIALS", "当前密码不正确", nil)
			return
		}
		validationError(w, r, err.Error(), nil)
		return
	}
	s.iam.Sessions().ClearCookies(w, sess.Audience)
	respondOK(w, r, map[string]any{"changed": true, "sessions_revoked": true, "reauthenticate": true})
}

// requireSession 是认证 API 内部使用的会话校验（这些路径不在 require() 包装内）。
func (s *Server) requireSession(w http.ResponseWriter, r *http.Request) (iam.Session, bool) {
	if !s.authReady(w, r) {
		return iam.Session{}, false
	}
	audience := audienceForPath(r.URL.Path)
	if v := strings.TrimSpace(r.Header.Get("X-Auth-Audience")); v == iam.AudienceAdmin {
		audience = iam.AudienceAdmin
	}
	if reuse, _ := r.Context().Value(sessionReuseCtxKey{}).(bool); reuse {
		respondError(w, r, http.StatusUnauthorized, "AUTHN_SESSION_REUSE",
			"检测到会话令牌重复使用，该会话族的全部设备已被强制下线，请重新登录", nil)
		return iam.Session{}, false
	}
	sess, reuse, ok := s.currentSessionDetailed(w, r, audience)
	if !ok {
		if reuse {
			respondError(w, r, http.StatusUnauthorized, "AUTHN_SESSION_REUSE",
				"检测到会话令牌重复使用，该会话族的全部设备已被强制下线，请重新登录", nil)
			return iam.Session{}, false
		}
		respondError(w, r, http.StatusUnauthorized, "AUTHN_SESSION_MISSING", "未登录", nil)
		return iam.Session{}, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		if !s.checkCSRF(r, sess.Audience) {
			respondError(w, r, http.StatusForbidden, "AUTHZ_CSRF_FAILED", "请求校验失败，请刷新页面后重试", nil)
			return iam.Session{}, false
		}
	}
	return sess, true
}

// currentSession 从 Cookie 解析会话。
//
// audience 优先由调用方/头部决定；对自助类认证端点（/api/auth/v1/*）允许回退到
// 另一种受众的 Cookie，因为用户层与后台共用同一套自助接口。
// 后台 API（/api/admin/v1/*）不允许回退：用户层会话无法调用后台（§3.3）。
// handleTokenReuse 处理令牌重放：撤销整个家族、写安全审计、记录风险事件。
//
// 返回被撤销的会话数，便于调用方与审计串联。
func (s *Server) handleTokenReuse(ctx context.Context, sess iam.Session, audience string) int64 {
	meta := metaFrom(ctx)
	revoked, err := s.iam.Sessions().RevokeFamily(ctx, sess.FamilyID, "token_reuse_detected", "")
	if err != nil {
		s.deps.Log.Error("revoke family failed", "err", err, "family", sess.FamilyID)
	}
	if err := s.iam.Sessions().RecordReuseEvent(ctx, sess, revoked, meta.IP, meta.UserAgent, meta.RequestID, meta.TraceID); err != nil {
		s.deps.Log.Error("record reuse event failed", "err", err, "family", sess.FamilyID)
	}
	_ = iam.RecordLoginEvent(ctx, s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: "auth.session.reuse_detected", Result: "blocked",
		ReasonCode: "token_reuse", RiskLevel: "critical",
		IdentityID: sess.IdentityID, OrganizationID: sess.OrganizationID,
		Audience: audience, SessionID: sess.ID, DeviceID: sess.DeviceID,
		DeviceInfo: iam.ParseUserAgent(meta.UserAgent), UserAgent: meta.UserAgent,
		IP: meta.IP, EntryHost: meta.Host, Scheme: meta.Scheme,
		RequestID: meta.RequestID, TraceID: meta.TraceID,
		Detail: map[string]any{
			"family_id": sess.FamilyID, "sessions_revoked": revoked,
			"replaced_by": sess.ReplacedBy, "revoked_reason": sess.RevokedReason,
		},
	})
	if s.audit != nil {
		p := policy.Principal{
			IdentityID: sess.IdentityID, Email: sess.Identity.Email,
			OrganizationID: sess.OrganizationID, SessionID: sess.ID, Audience: audience,
			Authenticated: true, Roles: sess.Roles,
		}
		ev := auditEventFor(r0(ctx), p, meta, "auth.session.reuse_detected", "security", "session", sess.ID, "deny",
			map[string]any{"family_id": sess.FamilyID, "sessions_revoked": revoked, "replaced_by": sess.ReplacedBy})
		ev.RiskLevel = "critical"
		ev.PolicyDecision = "deny"
		ev.DenyReason = "token_reuse"
		if err := s.audit.Write(ctx, ev); err != nil {
			s.deps.Log.Error("audit reuse event failed", "err", err)
		}
	}
	s.deps.Log.Warn("session token reuse detected",
		"family_id", sess.FamilyID, "identity_id", sess.IdentityID,
		"sessions_revoked", revoked, "client_ip", meta.IP.ClientIP)
	return revoked
}

func (s *Server) currentSession(w http.ResponseWriter, r *http.Request, audience string) (iam.Session, bool) {
	sess, _, ok := s.currentSessionDetailed(w, r, audience)
	return sess, ok
}

// currentSessionDetailed 在 currentSession 基础上额外告知"是否发生了令牌重放"。
func (s *Server) currentSessionDetailed(w http.ResponseWriter, r *http.Request, audience string) (iam.Session, bool, bool) {
	if s.iam == nil {
		return iam.Session{}, false, false
	}
	cfg := s.iam.Sessions().Config()
	reuse := false
	tryAudience := func(aud string) (iam.Session, bool) {
		c, err := r.Cookie(cfg.CookieName(aud))
		if err != nil || c.Value == "" {
			return iam.Session{}, false
		}
		sess, err := s.iam.Sessions().Authenticate(r.Context(), c.Value, aud)
		if err != nil {
			if errors.Is(err, iam.ErrSessionReuse) {
				// 重放：撤销家族、写审计，并且**不**回退到其它受众继续解析。
				reuse = true
				s.handleTokenReuse(r.Context(), sess, aud)
			}
			s.iam.Sessions().ClearCookies(w, aud)
			return iam.Session{}, false
		}
		return sess, true
	}
	if sess, ok := tryAudience(audience); ok {
		return sess, reuse, true
	}
	if reuse {
		return iam.Session{}, true, false
	}
	if strings.HasPrefix(r.URL.Path, "/api/auth/") && !strings.HasPrefix(r.URL.Path, "/api/admin/") && requestedAudience(r) == "" {
		other := iam.AudienceUser
		if audience == iam.AudienceUser {
			other = iam.AudienceAdmin
		}
		if sess, ok := tryAudience(other); ok {
			return sess, true, true
		}
	}
	return iam.Session{}, reuse, false
}

func userSummary(sess iam.Session) map[string]any {
	return map[string]any{
		"id":           sess.IdentityID,
		"display_name": displayOrEmail(sess.Identity.DisplayName, sess.Identity.Email),
		"email":        sess.Identity.Email,
	}
}

func displayOrEmail(name, email string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	if at := strings.Index(email, "@"); at > 0 {
		return email[:at]
	}
	return email
}

// sanitizeReturnTo 只允许站内相对路径，阻断开放重定向（§27 SEC 负向用例）。
func sanitizeReturnTo(v string) string {
	return sanitizeReturnToDefault(v, "/home")
}

// sanitizeReturnToDefault 只允许应用内相对路径，阻断开放重定向（§27 SEC 负向用例）。
// 同时剥离应用挂载前缀，避免 basename 二次拼接（例如 /admin/overview -> /overview）。
func sanitizeReturnToDefault(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" || !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
		return fallback
	}
	if strings.Contains(v, "\\") || strings.Contains(v, "://") {
		return fallback
	}
	for _, prefix := range []string{"/admin", "/app-v2"} {
		if v == prefix {
			return fallback
		}
		if strings.HasPrefix(v, prefix+"/") {
			return strings.TrimPrefix(v, prefix)
		}
	}
	return v
}

// authErrCode 把 iam 的稳定错误码映射到 §21.3 的 AUTHN_/AUTHZ_ 前缀。
// r0 构造一个仅用于审计字段填充的空请求（重放发生在中间件之外，
// 没有原始 *http.Request 可复用；真实来源信息全部来自 RequestMeta）。
func r0(_ context.Context) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "/api/auth/v1/refresh", nil)
	return req
}

func defaultNextFor(audience string) string {
	if audience == iam.AudienceAdmin {
		return "/overview"
	}
	return "/home"
}

func authErrCode(status int, code string) string {
	switch code {
	case "invalid_credentials":
		return "AUTHN_INVALID_CREDENTIALS"
	case "account_disabled":
		return "AUTHZ_ACCOUNT_DISABLED"
	case "account_locked":
		return "AUTHZ_ACCOUNT_LOCKED"
	case "rate_limited":
		return "RATE_LIMIT_EXCEEDED"
	case "session_invalid":
		return "AUTHN_SESSION_INVALID"
	case "mfa_not_available":
		return "AUTHN_MFA_UNAVAILABLE"
	default:
		if status >= 500 {
			return "INTERNAL_ERROR"
		}
		return "AUTHN_FAILED"
	}
}

func authErrMessage(code string) string {
	switch code {
	case "invalid_credentials":
		return "账号或密码不正确"
	case "account_disabled":
		return "账号已停用，请联系管理员"
	case "account_locked":
		return "账号暂时锁定，请稍后再试"
	case "rate_limited":
		return "登录尝试过于频繁，请稍后再试"
	case "mfa_not_available":
		return "该环境尚未启用多因素认证"
	default:
		return "登录失败"
	}
}

// assertSessionMode 在生产模式下必须使用 session，否则启动即失败（fail closed）。
func (s *Server) assertSessionMode() error {
	if s.cfg == nil {
		return nil
	}
	if s.cfg.Env == "production" && s.authMode.TrustsHeaders() {
		return errProductionTrustsHeaders
	}
	return nil
}

var errProductionTrustsHeaders = authModeError("production 环境禁止信任客户端自报身份头（AUTH_MODE 必须为 session）")

type authModeError string

func (e authModeError) Error() string { return string(e) }

// 确保 auth 包被引用（兼容层在 identityMiddleware 中使用）。
var _ = auth.Anonymous
