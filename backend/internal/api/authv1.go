package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
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
		// P1-20260913 后半段：登录后切换会话绑定的工作区（显式选择 + 成员资格 fail closed）。
		pr.Get("/session/workspaces", s.handleAuthWorkspaceOptions)
		pr.Post("/session/workspace", s.handleAuthSwitchWorkspace)
		pr.Get("/sessions", s.handleAuthListSessions)
		pr.Delete("/sessions/{id}", s.handleAuthRevokeSession)
		pr.Post("/sessions/revoke-all", s.handleAuthRevokeAll)
		pr.Get("/devices", s.handleAuthDevices)
		pr.Patch("/devices/{id}", s.handleAuthUpdateDevice)
		pr.Post("/password/change", s.handleAuthChangePassword)

		// OIDC Authorization Code + PKCE。未完整配置（尤其生产无真实 IdP）时必须 501，
		// 不得把本地密码登录伪装成 OIDC，也不得伪造 live 验收（AD-004 / AUTH-011）。
		pr.Get("/oidc/start", s.handleAuthOIDCStart)
		pr.Get("/oidc/callback", s.handleAuthOIDCCallback)
		// MFA/TOTP（§19.9 / AUTH-004）：自助注册、确认、校验与关闭，外加登录第二步。
		// /mfa/verify 对未登录调用方按 challenge_id 处理（登录 202 的收尾），
		// 对已登录调用方按 step-up 处理；两条路径都不在 require() 包装内，各自显式校验。
		pr.Post("/mfa/enroll/start", s.handleAuthMFAEnrollStart)
		pr.Post("/mfa/enroll/confirm", s.handleAuthMFAEnrollConfirm)
		pr.Post("/mfa/verify", s.handleAuthMFAVerify)
		pr.Post("/mfa/disable", s.handleAuthMFADisable)
	})
}

// handleAuthOIDCStart 生成 state/nonce/PKCE 并重定向到 IdP；未配置时显式 501。
func (s *Server) handleAuthOIDCStart(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil || !s.oidc.Enabled() {
		respondError(w, r, http.StatusNotImplemented, "OIDC_NOT_CONFIGURED", "OIDC 未配置，当前部署保持禁用", nil)
		return
	}
	audience := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("audience")))
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
	returnTo := sanitizeReturnToDefault(r.URL.Query().Get("return_to"), defaultNextFor(audience))
	req, err := s.oidc.BeginAuthorization(returnTo, audience)
	if err != nil {
		s.deps.Log.Error("oidc start failed", "err", err)
		respondError(w, r, http.StatusServiceUnavailable, "OIDC_UNAVAILABLE", "OIDC 服务暂不可用", nil)
		return
	}
	http.Redirect(w, r, req.URL, http.StatusFound)
}

// handleAuthOIDCCallback 校验 state/PKCE/ID Token，并按“已验证邮箱匹配既有 identity”建立 Session。
// 本实现不做身份自动开通：未知邮箱/未验证邮箱一律拒绝，避免 IdP 账户静默获得租户身份。
func (s *Server) handleAuthOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil || !s.oidc.Enabled() {
		s.oidcDenied(w, r, "oidc_not_configured")
		return
	}
	if errCode := strings.TrimSpace(r.URL.Query().Get("error")); errCode != "" {
		s.recordOIDCFailure(r, "", "idp_"+errCode)
		s.oidcDenied(w, r, "idp_denied")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		s.recordOIDCFailure(r, "", "missing_code_or_state")
		s.oidcDenied(w, r, "oidc_state_invalid")
		return
	}
	result, err := s.oidc.HandleCallback(r.Context(), code, state)
	if err != nil {
		s.recordOIDCFailure(r, "", "oidc_callback_failed")
		s.deps.Log.Warn("oidc callback rejected", "err", err)
		s.oidcDenied(w, r, "oidc_callback_failed")
		return
	}
	if !result.EmailVerified {
		s.recordOIDCFailure(r, result.Email, "email_not_verified")
		s.oidcDenied(w, r, "oidc_email_unverified")
		return
	}
	identity, err := s.iam.Repo().GetIdentityByEmail(r.Context(), result.Email)
	if err != nil {
		s.recordOIDCFailure(r, result.Email, "identity_not_provisioned")
		s.oidcDenied(w, r, "oidc_identity_not_provisioned")
		return
	}
	if identity.Status != "active" {
		s.recordOIDCFailure(r, result.Email, "identity_"+identity.Status)
		s.oidcDenied(w, r, "oidc_identity_disabled")
		return
	}
	meta := metaFrom(r.Context())
	// P1-20260913：OIDC 与密码登录必须使用同一个作用域策略 —— 只认成员资格，
	// 0 个成员资格或多名未选择一律拒绝（fail closed），不回退组织默认工作区。
	oidcWorkspaceID, wsErr := s.iam.ResolveLoginWorkspace(r.Context(), identity.ID, identity.OrganizationID, "")
	if wsErr != nil {
		s.recordOIDCFailure(r, result.Email, "oidc_workspace_scope_denied")
		s.deps.Log.Warn("oidc workspace scope denied", "err", wsErr, "identity_id", identity.ID)
		s.oidcDenied(w, r, "oidc_workspace_scope_denied")
		return
	}
	created, err := s.iam.Sessions().Create(r.Context(), iam.SessionRequest{
		Audience: result.Audience, IdentityID: identity.ID, OrganizationID: identity.OrganizationID,
		WorkspaceID: oidcWorkspaceID,
		AuthMethod:  "oidc", IP: meta.IP, UserAgent: meta.UserAgent, ClientHints: iam.ClientHints(r.Header),
		EntryHost: meta.Host, Scheme: meta.Scheme,
	})
	if err != nil {
		s.deps.Log.Error("oidc session create failed", "err", err)
		s.oidcDenied(w, r, "oidc_session_failed")
		return
	}
	s.iam.Sessions().SetCookiesFor(w, result.Audience, created.Token, created.CSRF, s.secureCookieFor(r))
	_ = iam.RecordLoginEvent(r.Context(), s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventLoginSuccess, Result: "success", IdentityID: identity.ID,
		SubjectEmail: identity.Email, OrganizationID: identity.OrganizationID,
		WorkspaceID: created.Session.WorkspaceID, Audience: result.Audience, AuthMethod: "oidc",
		IdentityProvider: "oidc", SessionID: created.Session.ID,
		IP: meta.IP, UserAgent: meta.UserAgent, RiskLevel: "low",
		Detail: map[string]any{"subject": result.Subject, "return_to": result.ReturnTo},
	})
	target := result.ReturnTo
	if result.Audience == iam.AudienceAdmin {
		target = "/admin" + sanitizeReturnToDefault(target, "/overview")
	} else {
		target = "/app-v2" + sanitizeReturnToDefault(target, "/home")
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) recordOIDCFailure(r *http.Request, email, reason string) {
	if s.iam == nil {
		return
	}
	meta := metaFrom(r.Context())
	_ = iam.RecordLoginEvent(r.Context(), s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventLoginFailure, Result: "failure", ReasonCode: reason,
		SubjectEmail: email, AuthMethod: "oidc", IdentityProvider: "oidc",
		IP: meta.IP, UserAgent: meta.UserAgent, RiskLevel: "medium",
		Detail: map[string]any{"source": "oidc_callback"},
	})
}

func (s *Server) oidcDenied(w http.ResponseWriter, r *http.Request, reason string) {
	target := "/app-v2/login?oidc_error=" + url.QueryEscape(reason)
	if requestedAudience(r) == iam.AudienceAdmin || strings.HasPrefix(r.URL.Path, "/admin") {
		target = "/admin/login?oidc_error=" + url.QueryEscape(reason)
	}
	http.Redirect(w, r, target, http.StatusFound)
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
	// WorkspaceID：多工作区身份的显式选择（P1-20260913）。留空且成员资格唯一时自动绑定。
	WorkspaceID string `json:"workspace_id"`
}

func (s *Server) authReady(w http.ResponseWriter, r *http.Request) bool {
	if s.iam == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "认证服务未初始化", nil)
		return false
	}
	return true
}

func (s *Server) handleAuthMethods(w http.ResponseWriter, r *http.Request) {
	oidcEnabled := false
	if s.oidc != nil && s.oidc.Enabled() {
		if enabled, err := s.oidc.Methods(r.Context()); err == nil {
			oidcEnabled = enabled
		}
	}
	respondOK(w, r, map[string]any{
		"local":        s.iam != nil,
		"oidc":         oidcEnabled,
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
		WorkspaceID: strings.TrimSpace(req.WorkspaceID),
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
		// P1-20260913：多工作区身份必须显式选择工作区。把可选工作区原样返回，
		// 供客户端渲染选择器后带 workspace_id 重试；服务端绝不替用户任取默认值。
		var selErr *iam.WorkspaceSelectionRequiredError
		if errors.As(err, &selErr) {
			respondError(w, r, status, authErrCode(status, code), authErrMessage(code),
				map[string]any{"workspaces": selErr.Options, "reason": code})
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
	if result.MFARequired {
		// §21.4：需要 MFA 时返回 202，challenge_id 仅可使用一次且 5 分钟过期。
		// 这里**绝不** Set-Cookie：密码正确不等于登录完成，第二步通过前不得存在任何会话。
		respondAccepted(w, r, map[string]any{
			"state":              "mfa_required",
			"challenge_id":       result.ChallengeID,
			"expires_at":         result.ChallengeExpiresAt.UTC().Format(time.RFC3339),
			"expires_in_seconds": int(time.Until(result.ChallengeExpiresAt).Seconds()),
			"audience":           audience,
			"methods":            []string{"totp", "recovery_code"},
			"next":               sanitizeReturnToDefault(next, defaultNextFor(audience)),
			// 密码已验证，回显用户摘要不构成账号枚举；第二步之前前端还没有会话可查。
			"user": map[string]any{
				"id":           result.Identity.ID,
				"display_name": displayOrEmail(result.Identity.DisplayName, result.Identity.Email),
				"email":        result.Identity.Email,
			},
		})
		return
	}
	s.iam.Sessions().SetCookiesFor(w, audience, result.Token, result.CSRF, s.secureCookieFor(r))
	respondOK(w, r, map[string]any{
		"state":                "authenticated",
		"user":                 userSummary(result.Session),
		"roles":                jsonStrings(result.Roles),
		"audience":             audience,
		"next":                 sanitizeReturnToDefault(next, defaultNextFor(audience)),
		"csrf_token":           result.CSRF,
		"session_id":           result.Session.ID,
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
		s.iam.Sessions().ClearCookiesFor(w, audience, s.secureCookieFor(r))
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
	// C1-SSE-OLD-SCOPE-OPEN: a signed-out session must not keep a long-lived
	// authenticated read open.
	s.closeScopeSSESubscriptions(sess.ID, "", sseReasonLoggedOut, r)
	s.iam.Sessions().ClearCookiesFor(w, audience, s.secureCookieFor(r))
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
	s.iam.Sessions().SetCookiesFor(w, audience, created.Token, created.CSRF, s.secureCookieFor(r))
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
	if c, ok := cookieByName(r, s.iam.Sessions().Config().CSRFSecretCookieCandidates(audience)); ok {
		csrf = c.Value
	}
	respondOK(w, r, map[string]any{
		"user":                 userSummary(sess),
		"email":                sess.Identity.Email,
		"display_name":         sess.Identity.DisplayName,
		"organization_id":      sess.OrganizationID,
		"workspace_id":         sess.WorkspaceID,
		"audience":             audience,
		"session_id":           sess.ID,
		"roles":                jsonStrings(p.Roles),
		"permissions":          jsonStrings(p.Permissions),
		"expires_at":           sess.ExpiresAt.UTC().Format(time.RFC3339),
		"absolute_expires_at":  sess.AbsoluteExpires.UTC().Format(time.RFC3339),
		"must_change_password": sess.Identity.MustChangePassword,
		"mfa_satisfied":        sess.MFASatisfied,
		"mfa_enrolled":         s.mfaStatusFor(r, sess.IdentityID),
		"csrf_token":           csrf,
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
	// C1-SSE-OLD-SCOPE-OPEN: the revoked session may hold live streams.
	s.closeScopeSSESubscriptions(id, "", sseReasonSessionRevoked, r)
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
			s.iam.Sessions().SetCookiesFor(w, sess.Audience, created.Token, created.CSRF, s.secureCookieFor(r))
		}
	}
	meta := metaFrom(r.Context())
	_ = iam.RecordLoginEvent(r.Context(), s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventSessionForced, Result: "success", IdentityID: sess.IdentityID,
		SubjectEmail: sess.Identity.Email, Audience: sess.Audience, SessionID: sess.ID,
		IP: meta.IP, UserAgent: meta.UserAgent, RiskLevel: "high",
		Detail: map[string]any{"revoked": n, "keep_current": body.KeepCurrent, "by": "self"},
	})
	// C1-SSE-OLD-SCOPE-OPEN: every session of this identity was revoked, so every
	// stream authorised under any of them must end. The identity index covers the
	// other tabs/devices too; a stream created by the keep_current re-login is
	// registered afterwards and is unaffected.
	s.closeScopeSSESubscriptions("", sess.IdentityID, sseReasonIdentityRevoked, r)
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
	// C1-SSE-OLD-SCOPE-OPEN: ChangePassword revokes every session of the identity
	// (iam.Repo.SetPassword), so no stream may survive it.
	s.closeScopeSSESubscriptions("", sess.IdentityID, sseReasonIdentityRevoked, r)
	s.iam.Sessions().ClearCookiesFor(w, sess.Audience, s.secureCookieFor(r))
	respondOK(w, r, map[string]any{"changed": true, "sessions_revoked": true, "reauthenticate": true})
}

// handleAuthWorkspaceOptions 列出当前身份在当前组织内的**有效成员资格**工作区。
//
// 与登录选择器同源（iam.WorkspaceOptions），只返回 active 成员资格，
// 不读取组织默认工作区 —— 组织默认值不能替代成员资格（P1-20260913）。
func (s *Server) handleAuthWorkspaceOptions(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	options, err := s.iam.WorkspaceOptions(r.Context(), sess.IdentityID, sess.OrganizationID)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取工作区列表失败", nil)
		return
	}
	respondOK(w, r, map[string]any{
		"workspaces":           options,
		"current_workspace_id": sess.WorkspaceID,
	})
}

// handleAuthSwitchWorkspace 在登录后切换会话绑定的工作区。
//
// 约束：
//   - workspace_id 必填，服务端绝不回退组织默认工作区；
//   - 目标必须是该身份的 active 成员资格，否则 fail closed，且**不改变**现有会话；
//   - requireSession 已对非 GET 强制 CSRF（double-submit），缺失/不匹配一律 403；
//   - 成功或拒绝都写 login_events 与 audit_events_v2，携带同一 request_id / trace_id。
func (s *Server) handleAuthSwitchWorkspace(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var body struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", []fieldError{{Field: "body", Code: "invalid_json", Message: err.Error()}})
		return
	}
	requested := strings.TrimSpace(body.WorkspaceID)
	if requested == "" {
		validationError(w, r, "workspace_id 不能为空", []fieldError{{
			Field: "workspace_id", Code: "required", Message: "必须显式指定目标工作区，服务端不会选取默认值",
		}})
		return
	}
	meta := metaFrom(r.Context())
	target, err := s.iam.ResolveLoginWorkspace(r.Context(), sess.IdentityID, sess.OrganizationID, requested)
	if err != nil {
		status, code := iam.StatusForError(err)
		s.recordWorkspaceSwitch(r, sess, requested, sess.WorkspaceID, "denied", code)
		respondError(w, r, status, authErrCode(status, code), authErrMessage(code), nil)
		return
	}
	if target == sess.WorkspaceID {
		respondOK(w, r, map[string]any{"state": "unchanged", "workspace_id": target, "session_id": sess.ID})
		return
	}
	if err := s.iam.Sessions().RebindWorkspace(r.Context(), sess, target); err != nil {
		s.recordWorkspaceSwitch(r, sess, requested, sess.WorkspaceID, "failure", "rebind_failed")
		s.deps.Log.Error("workspace switch rebind failed", "err", err, "request_id", meta.RequestID)
		respondError(w, r, http.StatusConflict, "AUTHZ_WORKSPACE_SWITCH_CONFLICT", "会话状态已变化，请重新登录后再切换", nil)
		return
	}
	s.recordWorkspaceSwitch(r, sess, target, sess.WorkspaceID, "success", "")
	// C1-SSE-OLD-SCOPE-OPEN: the session is now in another workspace, so every
	// stream that was authorised under the previous scope must end here. The
	// close happens after the rebind committed, so a stream can never outlive the
	// scope it was opened with.
	s.closeScopeSSESubscriptions(sess.ID, "", sseReasonWorkspaceSwitched, r)
	respondOK(w, r, map[string]any{
		"state":                 "switched",
		"workspace_id":          target,
		"previous_workspace_id": sess.WorkspaceID,
		"session_id":            sess.ID,
	})
}

// recordWorkspaceSwitch 同时写登录链事件与 audit_events_v2，两者携带同一 request_id / trace_id，
// 使"谁在什么时候把会话从哪个工作区切到哪个工作区"可被独立串联（AUDIT-001 / RBAC-003）。
func (s *Server) recordWorkspaceSwitch(r *http.Request, sess iam.Session, target, previous, result, reason string) {
	meta := metaFrom(r.Context())
	detail := map[string]any{"target_workspace_id": target, "previous_workspace_id": previous}
	if reason != "" {
		detail["reason"] = reason
	}
	eventResult := "success"
	if result != "success" {
		eventResult = "failure"
	}
	_ = iam.RecordLoginEvent(r.Context(), s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventWorkspaceSwitch, Result: eventResult,
		IdentityID: sess.IdentityID, SubjectEmail: sess.Identity.Email,
		OrganizationID: sess.OrganizationID, WorkspaceID: target,
		Audience: sess.Audience, SessionID: sess.ID, DeviceID: sess.DeviceID,
		DeviceInfo: iam.ParseUserAgent(meta.UserAgent), UserAgent: meta.UserAgent,
		IP: meta.IP, EntryHost: meta.Host, Scheme: meta.Scheme,
		RequestID: meta.RequestID, TraceID: meta.TraceID,
		Detail: detail,
	})
	if s.audit == nil {
		return
	}
	auditResult := "success"
	status := http.StatusOK
	if result != "success" {
		auditResult = "deny"
		status = http.StatusForbidden
	}
	event := audit.Event{
		ActorType: "user", ActorID: sess.IdentityID, ActorEmail: sess.Identity.Email,
		ActorName: sess.Identity.DisplayName, ActorRoles: sess.Roles,
		OrganizationID: sess.OrganizationID, WorkspaceID: target,
		SessionID: sess.ID, DeviceID: sess.DeviceID,
		Action: "authorization.workspace_switched", Category: "authorization",
		ResourceType: "workspace", ResourceID: target,
		HTTPMethod: r.Method, Route: r.URL.Path,
		RequestID: meta.RequestID, TraceID: meta.TraceID,
		Result: auditResult, HTTPStatus: status,
		PermissionKey: "workspace:switch", PolicyDecision: result,
		DenyReason: reason, RiskLevel: "medium",
		PeerIP: meta.IP.PeerIP, ClientIP: meta.IP.ClientIP, ProxyChain: meta.IP.ProxyChain,
		UserAgent: meta.UserAgent,
		Detail:    detail,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.audit.Write(ctx, event); err != nil {
			s.deps.Log.Error("workspace switch audit write failed", "err", err, "request_id", meta.RequestID)
		}
	}()
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
		// A state-changing self-service request refused because the session is already
		// gone is audited exactly once, with the same reason the CSRF gate below uses.
		// Without this row a 401 leaves no trace at all and cannot be told apart from a
		// request that never arrived (plan 6.6.1). Safe methods are deliberately not
		// audited here: an expired Cookie polling GET /session must not amplify into a
		// write storm.
		if stateChangingMethod(r.Method) {
			s.auditDenyCodeDetail(r, PrincipalFrom(r.Context()), declaredPermission(r.Context()), "session_missing", "AUTHN_SESSION_MISSING", http.StatusUnauthorized, nil)
		}
		respondError(w, r, http.StatusUnauthorized, "AUTHN_SESSION_MISSING", "未登录", nil)
		return iam.Session{}, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		switch s.checkCSRFOutcome(r, sess.Audience) {
		case csrfSessionNotLive:
			// The session row disappeared between the read above and the CSRF gate's own
			// re-authentication: a sibling request of the same identity ran revoke-all,
			// an administrator revoked this session, or another device signed out. The
			// caller is simply no longer logged in -- 401, audited exactly once, cookies
			// cleared. Answering 403 AUTHZ_CSRF_FAILED here told the client to refresh
			// its CSRF token and retry, which can never succeed; that masking is the
			// P1-1/AUTH-007 defect proven by the revoke-all concurrency gate, where 5 of
			// 16 concurrent requests were refused with the wrong cause.
			p := PrincipalFrom(r.Context())
			s.clearDeadSessionCookies(w, r, sess.Audience)
			s.auditDenyCodeDetail(r, p, declaredPermission(r.Context()), "session_missing", "AUTHN_SESSION_MISSING", http.StatusUnauthorized, nil)
			respondError(w, r, http.StatusUnauthorized, "AUTHN_SESSION_MISSING", "未登录", nil)
			return iam.Session{}, false
		case csrfTokenInvalid:
			p := PrincipalFrom(r.Context())
			s.auditDenyCodeDetail(r, p, declaredPermission(r.Context()), "csrf_failed", "AUTHZ_CSRF_FAILED", http.StatusForbidden, nil)
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
		ev := auditEventFor(auditRequestFor(ctx), p, meta, "auth.session.reuse_detected", "security", "session", sess.ID, "deny",
			map[string]any{"family_id": sess.FamilyID, "sessions_revoked": revoked, "replaced_by": sess.ReplacedBy})
		ev.RiskLevel = "critical"
		ev.PolicyDecision = "deny"
		ev.DenyReason = "token_reuse"
		if err := s.audit.Write(ctx, ev); err != nil {
			s.deps.Log.Error("audit reuse event failed", "err", err)
		}
	}
	// C1-SSE-OLD-SCOPE-OPEN: the whole token family was revoked, so its streams
	// must not stay open either.
	s.closeScopeSSESubscriptionsCtx("", sess.IdentityID, sseReasonTokenReuse, meta.RequestID)
	s.deps.Log.Warn("session token reuse detected",
		"family_id", sess.FamilyID, "identity_id", sess.IdentityID,
		"sessions_revoked", revoked, "client_ip", meta.IP.ClientIP)
	return revoked
}

// handleWorkspaceAccessRevoked 处理"会话绑定的工作区成员资格已被撤销"（P1-20260913）。
//
// 语义：撤权即刻生效（fail closed）——撤销该会话、写不可抵赖的安全审计，
// 再让调用方返回显式错误码，绝不静默降级为"未登录"或继续放行。
func (s *Server) handleWorkspaceAccessRevoked(ctx context.Context, sess iam.Session, audience string) {
	meta := metaFrom(ctx)
	if err := s.iam.Sessions().RevokeByID(ctx, sess.ID, iam.RevokeReasonWorkspaceRevoked, sess.IdentityID); err != nil {
		s.deps.Log.Error("revoke session after workspace membership revocation failed", "err", err, "session_id", sess.ID)
	}
	_ = iam.RecordLoginEvent(ctx, s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventSessionRevoked, Result: "denied",
		ReasonCode: iam.ReasonWorkspaceAccessRevoked, RiskLevel: "high",
		IdentityID: sess.IdentityID, SubjectEmail: sess.Identity.Email,
		OrganizationID: sess.OrganizationID, WorkspaceID: sess.WorkspaceID,
		Audience: audience, SessionID: sess.ID, DeviceID: sess.DeviceID,
		DeviceInfo: iam.ParseUserAgent(meta.UserAgent), UserAgent: meta.UserAgent,
		IP: meta.IP, EntryHost: meta.Host, Scheme: meta.Scheme,
		RequestID: meta.RequestID, TraceID: meta.TraceID,
		Detail: map[string]any{"revoked_reason": iam.RevokeReasonWorkspaceRevoked, "workspace_id": sess.WorkspaceID},
	})
	if s.audit != nil {
		p := policy.Principal{
			IdentityID: sess.IdentityID, Email: sess.Identity.Email,
			OrganizationID: sess.OrganizationID, WorkspaceID: sess.WorkspaceID,
			SessionID: sess.ID, Audience: audience, Authenticated: true, Roles: sess.Roles,
		}
		ev := auditEventFor(auditRequestFor(ctx), p, meta, "auth.workspace.access_revoked", "security", "session", sess.ID, "deny",
			map[string]any{"workspace_id": sess.WorkspaceID, "revoked_reason": iam.RevokeReasonWorkspaceRevoked})
		ev.RiskLevel = "high"
		ev.PolicyDecision = "deny"
		ev.DenyReason = iam.ReasonWorkspaceAccessRevoked
		if err := s.audit.Write(ctx, ev); err != nil {
			s.deps.Log.Error("audit workspace revocation failed", "err", err)
		}
	}
	// C1-SSE-OLD-SCOPE-OPEN: the session lost its workspace, so a stream opened
	// under it must end now.
	s.closeScopeSSESubscriptionsCtx(sess.ID, "", sseReasonMembershipRevoked, meta.RequestID)
	s.deps.Log.Warn("session workspace membership revoked; session fail closed",
		"session_id", sess.ID, "identity_id", sess.IdentityID, "workspace_id", sess.WorkspaceID)
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
		c, ok := cookieByName(r, cfg.CookieNameCandidates(aud))
		if !ok {
			return iam.Session{}, false
		}
		sess, err := s.iam.Sessions().Authenticate(r.Context(), c.Value, aud)
		if err != nil {
			if errors.Is(err, iam.ErrSessionReuse) {
				// 重放：撤销家族、写审计，并且**不**回退到其它受众继续解析。
				reuse = true
				s.handleTokenReuse(r.Context(), sess, aud)
			}
			s.iam.Sessions().ClearCookiesFor(w, aud, s.secureCookieFor(r))
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
//
// 注：原先的 r0() 合成请求（POST /api/auth/v1/refresh）已删除 —— 它会让审计事件
// 记录一条指向别的端点的**假路由**（R10v8 实测证据 audit_events_v2 seq 51944）。
// 需要无请求上下文写审计时一律用 auditRequestFor(ctx)，它只反映真实来源。
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
	case "mfa_enrollment_required":
		// 策略要求 MFA 但该账号尚未注册。缺这一条会退化成笼统的 AUTHN_FAILED，
		// 客户端就无法据此引导用户去注册（§21.3 要求错误码稳定且可区分）。
		return "AUTHN_MFA_ENROLLMENT_REQUIRED"
	case "no_workspace_membership":
		return "AUTHZ_NO_WORKSPACE_MEMBERSHIP"
	case "workspace_selection_required":
		return "AUTHN_WORKSPACE_SELECTION_REQUIRED"
	case "workspace_not_member":
		return "AUTHZ_WORKSPACE_NOT_MEMBER"
	case "workspace_access_revoked":
		return "AUTHZ_WORKSPACE_ACCESS_REVOKED"
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
	case "mfa_enrollment_required":
		return "该账号被要求使用多因素认证，请先在「个人资料与安全」中完成注册"
	case "no_workspace_membership":
		return "该账号没有任何工作区成员资格，请联系管理员开通"
	case "workspace_selection_required":
		return "该账号属于多个工作区，请先选择要进入的工作区"
	case "workspace_not_member":
		return "所选工作区不在该账号的成员资格内"
	case "workspace_access_revoked":
		return "工作区访问权限已被撤销，请重新登录"
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
