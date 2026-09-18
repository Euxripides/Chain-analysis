package api

// MFA/TOTP 端点（§19.9 / AUTH-004）。此前 `POST /mfa/verify` 是 501 占位。
//
// 两类用法：
//   * 自助面（requireSession）：安全设置里注册/确认/关闭 MFA，以及用一次验证码确认 step-up；
//   * 登录第二步（无会话，凭 challenge_id）：登录返回 202 后收尾建会话，见 completeMFALogin。
//
// 安全要点：
//   * 注册只在**已确认会话**上发起，避免未完成的注册被静默换绑（store 里已确认则报 AlreadyEnrolled）；
//   * 恢复码明文只在 confirm 响应里出现一次，库里只存摘要；
//   * 校验失败一律 401 AUTHN_MFA_INVALID，不回显期望值、不区分"码错"与"重放"；
//   * 挑战单次使用、5 分钟过期、提交次数上限 5（成功那次也计入）。

import (
	"errors"
	"net/http"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/mfa"
)

// handleAuthMFAEnrollStart 生成密钥与 otpauth URI（未确认，不改变登录要求）。
func (s *Server) handleAuthMFAEnrollStart(w http.ResponseWriter, r *http.Request) {
	if s.mfa == nil {
		respondError(w, r, http.StatusServiceUnavailable, "AUTHN_MFA_UNAVAILABLE", "MFA 未启用", nil)
		return
	}
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	issuer := "ChainSight"
	if s.cfg != nil && strings.TrimSpace(s.cfg.Auth.CookiePrefix) != "" {
		issuer = "ChainSight"
	}
	secret, uri, err := s.mfa.StartEnroll(r.Context(), sess.IdentityID, issuer, sess.Identity.Email)
	switch {
	case errors.Is(err, mfa.ErrAlreadyEnrolled):
		respondError(w, r, http.StatusConflict, "AUTHN_MFA_ALREADY_ENROLLED", "该账号已启用 MFA，请先关闭再重新注册", nil)
		return
	case err != nil:
		if s.deps.Log != nil {
			s.deps.Log.Error("mfa enroll start failed", "err", err, "identity_id", sess.IdentityID)
		}
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "无法开始 MFA 注册", nil)
		return
	}
	respondOK(w, r, map[string]any{"secret": secret, "otpauth_uri": uri, "digits": mfa.Digits, "period_seconds": mfa.PeriodSeconds})
}

// handleAuthMFAEnrollConfirm 用一次正确验证码确认注册，并一次性下发恢复码。
func (s *Server) handleAuthMFAEnrollConfirm(w http.ResponseWriter, r *http.Request) {
	if s.mfa == nil {
		respondError(w, r, http.StatusServiceUnavailable, "AUTHN_MFA_UNAVAILABLE", "MFA 未启用", nil)
		return
	}
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不合法", nil)
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		validationError(w, r, "code 必填", []fieldError{{Field: "code", Code: "required", Message: "请输入认证器上的一次性验证码"}})
		return
	}
	codes, err := s.mfa.ConfirmEnroll(r.Context(), sess.IdentityID, body.Code)
	switch {
	case errors.Is(err, mfa.ErrInvalidCode):
		respondError(w, r, http.StatusUnauthorized, "AUTHN_MFA_INVALID", "验证码不正确", nil)
		return
	case errors.Is(err, mfa.ErrAlreadyEnrolled):
		respondError(w, r, http.StatusConflict, "AUTHN_MFA_ALREADY_ENROLLED", "该账号已启用 MFA", nil)
		return
	case errors.Is(err, mfa.ErrNotEnrolled):
		respondError(w, r, http.StatusConflict, "AUTHN_MFA_NOT_ENROLLED", "请先开始 MFA 注册", nil)
		return
	case err != nil:
		if s.deps.Log != nil {
			s.deps.Log.Error("mfa enroll confirm failed", "err", err, "identity_id", sess.IdentityID)
		}
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "无法确认 MFA 注册", nil)
		return
	}
	s.writeAuditNow(r, "auth:mfa:enroll", "security", "identity", sess.IdentityID, map[string]any{"recovery_codes": len(codes)})
	respondOK(w, r, map[string]any{"enrolled": true, "recovery_codes": codes})
}

// handleAuthMFAVerify 校验一次验证码，支持两种用法：
//
//  1. 登录第二步：body 带 challenge_id，此时**没有会话**。通过后建会话并下发 Cookie
//     （§21.4：登录需要 MFA 时返回 202 + challenge_id，单次使用、5 分钟过期）。
//  2. 已登录用户的 step-up：带会话 + 只有 code，返回 verified=true。
//
// 两条路径都不区分"码错"与"重放"，也不回显任何期望值。
func (s *Server) handleAuthMFAVerify(w http.ResponseWriter, r *http.Request) {
	if s.mfa == nil {
		respondError(w, r, http.StatusServiceUnavailable, "AUTHN_MFA_UNAVAILABLE", "MFA 未启用", nil)
		return
	}
	var body struct {
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
		ReturnTo    string `json:"return_to"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不合法", nil)
		return
	}
	if strings.TrimSpace(body.ChallengeID) != "" {
		s.completeMFALogin(w, r, body.ChallengeID, body.Code, body.ReturnTo)
		return
	}
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	okCode, usedRecovery, err := s.mfa.Verify(r.Context(), sess.IdentityID, body.Code)
	switch {
	case errors.Is(err, mfa.ErrNotEnrolled):
		respondError(w, r, http.StatusConflict, "AUTHN_MFA_NOT_ENROLLED", "该账号未启用 MFA", nil)
		return
	case err != nil:
		if s.deps.Log != nil {
			s.deps.Log.Error("mfa verify failed", "err", err, "identity_id", sess.IdentityID)
		}
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "无法校验验证码", nil)
		return
	case !okCode:
		s.writeAuditNow(r, "auth:mfa:verify", "security", "identity", sess.IdentityID, map[string]any{"result": "denied"})
		respondError(w, r, http.StatusUnauthorized, "AUTHN_MFA_INVALID", "验证码不正确", nil)
		return
	}
	s.writeAuditNow(r, "auth:mfa:verify", "security", "identity", sess.IdentityID,
		map[string]any{"result": "ok", "used_recovery_code": usedRecovery})
	respondOK(w, r, map[string]any{"verified": true, "used_recovery_code": usedRecovery})
}

// completeMFALogin 是登录第二步的收尾：消费挑战 -> 建会话 -> 下发 Cookie。
//
// 身份、受众、工作区与设备上下文全部取自挑战落库时的服务端记录，客户端只能提供
// challenge_id 与验证码 —— 因此第二步不可能被用来切换到别的身份或工作区。
func (s *Server) completeMFALogin(w http.ResponseWriter, r *http.Request, challengeID, code, returnTo string) {
	state, method, err := s.mfa.ConsumeLoginChallenge(r.Context(), challengeID, code)
	if err != nil {
		s.recordMFAVerifyFailure(r, state, err)
		switch {
		case errors.Is(err, mfa.ErrChallengeExhausted):
			// 尝试次数用尽：该挑战作废，客户端必须重新走第一步登录。
			w.Header().Set("Retry-After", "60")
			respondError(w, r, http.StatusTooManyRequests, "RATE_LIMIT_MFA_ATTEMPTS",
				"验证码尝试次数过多，请重新登录", map[string]any{"retry_after_seconds": 60})
		case errors.Is(err, mfa.ErrChallengeInvalid):
			respondError(w, r, http.StatusUnauthorized, "AUTHN_MFA_CHALLENGE_INVALID", "登录挑战已失效，请重新登录", nil)
		case errors.Is(err, mfa.ErrInvalidCode), errors.Is(err, mfa.ErrNotEnrolled):
			respondError(w, r, http.StatusUnauthorized, "AUTHN_MFA_INVALID", "验证码不正确", nil)
		default:
			if s.deps.Log != nil {
				s.deps.Log.Error("mfa challenge verify failed", "err", err,
					"challenge_digest", mfa.HashChallengeToken(challengeID))
			}
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "无法校验验证码", nil)
		}
		return
	}
	meta := metaFrom(r.Context())
	result, err := s.iam.CompleteMFALogin(r.Context(), iam.MFACompletion{
		IdentityID: state.IdentityID, Audience: state.Audience,
		OrganizationID: state.OrganizationID, WorkspaceID: state.WorkspaceID,
		ClientIP: state.ClientIP, PeerIP: state.PeerIP, ProxyChain: state.ProxyChain,
		UserAgent: state.UserAgent, EntryHost: state.EntryHost, Scheme: state.Scheme,
		Method: method, RequestID: meta.RequestID, TraceID: meta.TraceID,
	})
	if err != nil {
		status, code := iam.StatusForError(err)
		if status >= 500 && s.deps.Log != nil {
			s.deps.Log.Error("mfa login completion failed", "err", err, "audience", state.Audience)
		}
		respondError(w, r, status, authErrCode(status, code), authErrMessage(code), nil)
		return
	}
	audience := state.Audience
	if audience == "" {
		audience = iam.AudienceUser
	}
	s.iam.Sessions().SetCookiesFor(w, audience, result.Token, result.CSRF, s.secureCookieFor(r))
	respondOK(w, r, map[string]any{
		"state":                "authenticated",
		"user":                 userSummary(result.Session),
		"roles":                jsonStrings(result.Roles),
		"audience":             audience,
		"next":                 sanitizeReturnToDefault(returnTo, defaultNextFor(audience)),
		"csrf_token":           result.CSRF,
		"session_id":           result.Session.ID,
		"must_change_password": result.Session.Identity.MustChangePassword,
		"mfa_satisfied":        true,
		"mfa_method":           method,
	})
}

// recordMFAVerifyFailure 写一条 auth.mfa.failure 登录事件（§6.6.1 / AUTH-004 的失败侧证据）。
//
// 挑战不可用时身份未知，此时不写事件（避免用猜测的身份污染审计）；
// 码错 / 次数用尽两种情况下 mfa.Store 会回填 ChallengeState，身份可确定，必须留痕。
func (s *Server) recordMFAVerifyFailure(r *http.Request, state mfa.ChallengeState, err error) {
	if s.iam == nil || state.IdentityID == "" {
		return
	}
	email := ""
	if identity, ierr := s.iam.Repo().GetIdentityByID(r.Context(), state.IdentityID); ierr == nil {
		email = identity.Email
	}
	reason := iam.ReasonMFAFailed
	switch {
	case errors.Is(err, mfa.ErrChallengeExhausted):
		reason = "mfa_attempts_exhausted"
	case errors.Is(err, mfa.ErrChallengeInvalid):
		reason = "mfa_challenge_invalid"
	}
	meta := metaFrom(r.Context())
	_ = iam.RecordLoginEvent(r.Context(), s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventMFAFailure, Result: "failure", ReasonCode: reason, RiskLevel: "medium",
		IdentityID: state.IdentityID, SubjectEmail: email, Audience: state.Audience,
		AuthMethod: "password", MFAResult: "failure",
		UserAgent: state.UserAgent, EntryHost: state.EntryHost, Scheme: state.Scheme,
		IP:        iam.ClientIP{ClientIP: state.ClientIP, PeerIP: state.PeerIP, ProxyChain: state.ProxyChain},
		RequestID: meta.RequestID, TraceID: meta.TraceID,
		Detail: map[string]any{"attempts": state.Attempts},
	})
}

// handleAuthMFADisable 自助关闭 MFA（删除密钥与全部恢复码）。
func (s *Server) handleAuthMFADisable(w http.ResponseWriter, r *http.Request) {
	if s.mfa == nil {
		respondError(w, r, http.StatusServiceUnavailable, "AUTHN_MFA_UNAVAILABLE", "MFA 未启用", nil)
		return
	}
	sess, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if err := s.mfa.Disable(r.Context(), sess.IdentityID); err != nil {
		if s.deps.Log != nil {
			s.deps.Log.Error("mfa disable failed", "err", err, "identity_id", sess.IdentityID)
		}
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "无法关闭 MFA", nil)
		return
	}
	s.writeAuditNow(r, "auth:mfa:disable", "security", "identity", sess.IdentityID, nil)
	respondOK(w, r, map[string]any{"enrolled": false})
}

// mfaStatusFor 把 MFA 状态挂进会话自助信息（前端据此显示"已启用/未启用"）。
func (s *Server) mfaStatusFor(r *http.Request, identityID string) bool {
	if s.mfa == nil {
		return false
	}
	enrolled, err := s.mfa.Status(r.Context(), identityID)
	if err != nil {
		if s.deps.Log != nil {
			s.deps.Log.Warn("mfa status read failed", "err", err)
		}
		return false
	}
	return enrolled
}
