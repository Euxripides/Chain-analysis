package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
)

// registerAdminV1 挂载 §21.6 后台管理 API。
//
// 安全规则（§3.3 / §28.5 验收）：
//   - 每个端点必须显式声明权限键；未声明权限的端点不得挂载。
//   - 管理会话与管理权限缺一不可（require 内部同时校验 audience 与权限）。
//   - 所有管理写操作进入 audit_events_v2，且默认要求二次确认字段。
func (s *Server) registerAdminV1(r chi.Router) {
	// 组织、工作区、成员角色（S3-ADMIN-01）。
	s.registerAdminOrgRoutes(r)

	// 运营总览
	r.Get("/overview", s.require("admin:dashboard:read", s.handleAdminOverview))
	r.Get("/system/health", s.require("admin:system:read", s.handleAdminSystemHealth))

	// 身份与权限
	r.Get("/users", s.require("admin:user:read", s.handleAdminListUsers))
	r.Get("/users/{id}", s.require("admin:user:read", s.handleAdminGetUser))
	r.Patch("/users/{id}", s.require("admin:user:update", s.handleAdminUpdateUser))
	r.Post("/users/{id}/commands/disable", s.require("admin:user:disable", s.handleAdminDisableUser))
	r.Post("/users/{id}/commands/enable", s.require("admin:user:disable", s.handleAdminEnableUser))
	r.Post("/users/{id}/commands/reset-password", s.require("admin:user:password:reset", s.handleAdminResetPassword))
	r.Get("/roles", s.require("admin:role:read", s.handleAdminListRoles))
	r.Get("/roles/{id}/permissions", s.require("admin:role:read", s.handleAdminRolePermissions))
	r.Get("/permissions", s.require("admin:permission:read", s.handleAdminListPermissions))

	// 会话与登录记录
	r.Get("/sessions", s.require("admin:session:read", s.handleAdminListSessions))
	r.Post("/sessions/{id}/commands/revoke", s.require("admin:session:revoke", s.handleAdminRevokeSession))
	r.Get("/login-events", s.require("admin:login-event:read", s.handleAdminLoginEvents))

	// 审计
	r.Get("/audit/events", s.require("admin:audit:read", s.handleAdminAuditEvents))
	r.Get("/audit/events/{id}", s.require("admin:audit:read", s.handleAdminAuditEventDetail))
	r.Get("/audit/chains", s.require("admin:audit:verify", s.handleAdminAuditChains))
	r.Post("/audit/verify", s.require("admin:audit:verify", s.handleAdminAuditVerify))
	r.Post("/audit/export", s.require("admin:audit:export", s.handleNotImplementedV2("audit.export")))

	// Sprint 5：全局任务与固定诊断链（A-JOB-01/02）。
	r.Get("/jobs", s.require("admin:job:list", s.handleAdminJobsList))
	r.Get("/jobs/{id}/diagnostics", s.require("admin:job:read", s.handleAdminJobDiagnostics))
	r.Post("/jobs/{id}/commands/{command}", s.require("admin:job:control", s.handleAdminJobCommand))

	// 显式未实现的后台能力，避免"页面存在即认为已完成"。
	r.Get("/provider/accounts", s.require("admin:provider:read", s.handleNotImplementedV2("admin.provider.accounts")))
	r.Get("/alerts", s.require("admin:alert:read", s.handleNotImplementedV2("admin.alert.list")))
	r.Get("/config/settings", s.require("admin:config:read", s.handleNotImplementedV2("admin.config.settings")))
	r.Get("/backups", s.require("admin:backup:read", s.handleNotImplementedV2("admin.backup.list")))
}

// ---- 运营总览 ----

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	pool := s.deps.Store.Pool
	ctx := r.Context()

	var users, activeSessions, jobsRunning, jobsFailed, loginFailures24h, auditEvents24h int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM identities WHERE status = 'active'`).Scan(&users)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND expires_at > now() AND absolute_expires_at > now()`).Scan(&activeSessions)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM download_jobs WHERE status IN ('RUNNING','PENDING')`).Scan(&jobsRunning)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM download_jobs WHERE status = 'FAILED'`).Scan(&jobsFailed)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM login_events WHERE result <> 'success' AND occurred_at > now() - interval '24 hours'`).Scan(&loginFailures24h)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events_v2 WHERE occurred_at > now() - interval '24 hours'`).Scan(&auditEvents24h)

	status := "ok"
	if jobsFailed > 0 || loginFailures24h > 10 {
		status = "degraded"
	}

	respondOK(w, r, map[string]any{
		"as_of":  time.Now().UTC().Format(time.RFC3339),
		"status": status,
		"cards": []map[string]any{
			{"key": "users", "label": "启用用户", "value": users, "unit": "count"},
			{"key": "sessions", "label": "活跃会话", "value": activeSessions, "unit": "count"},
			{"key": "jobs_running", "label": "运行中任务", "value": jobsRunning, "unit": "count"},
			{"key": "jobs_failed", "label": "失败任务", "value": jobsFailed, "unit": "count"},
			{"key": "login_failures_24h", "label": "24h 登录失败", "value": loginFailures24h, "unit": "count"},
			{"key": "audit_events_24h", "label": "24h 审计事件", "value": auditEvents24h, "unit": "count"},
		},
		"my_roles": p.Roles,
		"data_source": map[string]string{
			"users": "postgres:identities", "sessions": "postgres:sessions",
			"jobs": "postgres:download_jobs", "login": "postgres:login_events", "audit": "postgres:audit_events_v2",
		},
	})
}

func (s *Server) handleAdminSystemHealth(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if s.deps.Store != nil {
		if err := s.deps.Store.Pool.Ping(ctx); err != nil {
			checks["postgres"] = "down"
		} else {
			checks["postgres"] = "ok"
		}
	}
	if s.deps.CH != nil {
		if err := s.deps.CH.Exec(ctx, "SELECT 1"); err != nil {
			checks["clickhouse"] = "down"
		} else {
			checks["clickhouse"] = "ok"
		}
	}
	if s.deps.Bus != nil && s.deps.Bus.Enabled() {
		checks["nats"] = "ok"
	} else {
		checks["nats"] = "degraded"
	}
	if s.deps.Raw != nil {
		checks["minio"] = "ok"
	}
	respondOK(w, r, map[string]any{"checks": checks, "auth_mode": string(s.authMode), "env": s.cfg.Env})
}

// ---- 用户 ----

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	limit, offset := pagination(r)
	filter := iam.IdentityFilter{
		Search: strings.TrimSpace(r.URL.Query().Get("search")),
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Limit:  limit, Offset: offset,
	}
	// 跨组织查看需要平台级身份；普通管理员只能看本组织（§3.3 边界规则）。
	if !s.isPlatformScope(r) {
		filter.OrganizationID = PrincipalFrom(r.Context()).OrganizationID
	} else if orgID := strings.TrimSpace(r.URL.Query().Get("organization_id")); orgID != "" {
		filter.OrganizationID = orgID
	}
	list, total, err := s.iam.Repo().ListIdentities(r.Context(), filter)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取用户列表失败", nil)
		return
	}
	for i := range list {
		roles, err := s.iam.Repo().EffectiveRoles(r.Context(), list[i].ID)
		if err == nil {
			list[i].Roles = roles
		}
	}
	s.writeAuditNow(r, "admin:user:read", "admin.identity", "identity", "", map[string]any{
		"result_count": total, "search": filter.Search,
	})
	respondList(w, r, list, total, nil)
}

func (s *Server) handleAdminGetUser(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	identity, err := s.iam.Repo().GetIdentityByID(r.Context(), id)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "用户不存在", nil)
		return
	}
	p := PrincipalFrom(r.Context())
	if identity.OrganizationID != p.OrganizationID && !s.isPlatformScope(r) {
		// 跨组织对象统一返回 404，不泄露存在性。
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "用户不存在", nil)
		return
	}
	roles, _ := s.iam.Repo().EffectiveRoles(r.Context(), id)
	sessions, _, _ := s.iam.Sessions().List(r.Context(), iam.SessionFilter{IdentityID: id, ActiveOnly: true, Limit: 20})
	identity.Roles = roles
	s.writeAuditNow(r, "admin:user:read.detail", "admin.identity", "identity", id, nil)
	respondOK(w, r, map[string]any{"identity": identity, "roles": roles, "active_sessions": sessions})
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		DisplayName string `json:"display_name"`
		Confirm     bool   `json:"confirm"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "高风险操作需要二次确认", nil)
		return
	}
	identity, err := s.iam.Repo().GetIdentityByID(r.Context(), id)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "用户不存在", nil)
		return
	}
	name := strings.TrimSpace(body.DisplayName)
	if name == "" || len([]rune(name)) > 80 {
		validationError(w, r, "显示名称长度必须在 1-80 个字符之间", nil)
		return
	}
	if _, err := s.deps.Store.Pool.Exec(r.Context(),
		`UPDATE identities SET display_name = $2, updated_at = now() WHERE id = $1`, id, name); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "更新用户失败", nil)
		return
	}
	p := PrincipalFrom(r.Context())
	s.writeAuditWithChange(r, "admin:user:update", "admin.identity", "identity", id,
		map[string]any{"display_name": identity.DisplayName}, map[string]any{"display_name": name},
		map[string]any{"actor_id": p.IdentityID})
	respondOK(w, r, map[string]any{"updated": true})
}

func (s *Server) handleAdminDisableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, iam.StatusDisabled, "admin:user:disable")
}

func (s *Server) handleAdminEnableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, iam.StatusActive, "admin:user:enable")
}

func (s *Server) setUserStatus(w http.ResponseWriter, r *http.Request, status, action string) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Confirm bool   `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "高风险操作需要二次确认", nil)
		return
	}
	identity, err := s.iam.Repo().GetIdentityByID(r.Context(), id)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "用户不存在", nil)
		return
	}
	p := PrincipalFrom(r.Context())
	if identity.ID == p.IdentityID && status != iam.StatusActive {
		respondError(w, r, http.StatusUnprocessableEntity, "VALIDATION_SELF_LOCKOUT", "不能停用当前登录账号", nil)
		return
	}
	// 最后一个 Owner 约束（§28.5 验收）。
	if status == iam.StatusDisabled {
		if owners, err := s.iam.Repo().CountActiveOwners(r.Context(), identity.OrganizationID); err == nil && owners <= 1 {
			roles, _ := s.iam.Repo().EffectiveRoles(r.Context(), identity.ID)
			if contains(roles, "owner") {
				respondError(w, r, http.StatusUnprocessableEntity, "CONFLICT_LAST_OWNER",
					"组织必须保留至少一名可用的 Owner", nil)
				return
			}
		}
	}
	if err := s.iam.Repo().SetStatus(r.Context(), id, status); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "更新用户状态失败", nil)
		return
	}
	if status == iam.StatusDisabled {
		_, _ = s.iam.ForceLogout(r.Context(), identity, id, iam.LoginInput{})
	}
	s.policyEngineInvalidate(id)
	s.writeAuditWithChange(r, action, "admin.identity", "identity", id,
		map[string]any{"status": identity.Status}, map[string]any{"status": status},
		map[string]any{"reason": body.Reason})
	respondOK(w, r, map[string]any{"status": status})
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		NewPassword string `json:"new_password"`
		Confirm     bool   `json:"confirm"`
		Reason      string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "重置密码需要二次确认", nil)
		return
	}
	identity, err := s.iam.Repo().GetIdentityByID(r.Context(), id)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "用户不存在", nil)
		return
	}
	actor := iam.Identity{ID: PrincipalFrom(r.Context()).IdentityID, Email: PrincipalFrom(r.Context()).Email}
	meta := metaFrom(r.Context())
	if err := s.iam.AdminResetPassword(r.Context(), actor, id, body.NewPassword, iam.LoginInput{
		Audience: iam.AudienceAdmin, IP: meta.IP, UserAgent: meta.UserAgent,
		RequestID: meta.RequestID, TraceID: meta.TraceID,
	}); err != nil {
		validationError(w, r, err.Error(), nil)
		return
	}
	s.policyEngineInvalidate(id)
	s.writeAuditNow(r, "admin:user:password:reset", "admin.identity", "identity", identity.ID,
		map[string]any{"reason": body.Reason, "sessions_revoked": true})
	respondOK(w, r, map[string]any{"reset": true, "sessions_revoked": true})
}

// ---- 角色与权限 ----

func (s *Server) handleAdminListRoles(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	p := PrincipalFrom(r.Context())
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT id, key, name, description, is_system, organization_id,
               (SELECT count(*) FROM role_permissions rp WHERE rp.role_id = roles.id)
        FROM roles
        WHERE is_system OR organization_id = $1
        ORDER BY is_system DESC, key`, p.OrganizationID)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取角色失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, key, name, desc string
		var isSystem bool
		var orgID *string
		var permCount int
		if err := rows.Scan(&id, &key, &name, &desc, &isSystem, &orgID, &permCount); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取角色失败", nil)
			return
		}
		items = append(items, map[string]any{
			"id": id, "key": key, "name": name, "description": desc,
			"is_system": isSystem, "organization_id": orgID, "permission_count": permCount,
		})
	}
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleAdminRolePermissions(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT p.key, p.category, p.description, p.risk_level, p.sensitive
        FROM role_permissions rp JOIN permissions p ON p.key = rp.permission_key
        WHERE rp.role_id = $1 ORDER BY p.key`, id)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取角色权限失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var key, category, desc, risk string
		var sensitive bool
		if err := rows.Scan(&key, &category, &desc, &risk, &sensitive); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取角色权限失败", nil)
			return
		}
		items = append(items, map[string]any{"key": key, "category": category, "description": desc, "risk_level": risk, "sensitive": sensitive})
	}
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleAdminListPermissions(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(),
		`SELECT key, category, description, risk_level, sensitive FROM permissions ORDER BY key`)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取权限目录失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var key, category, desc, risk string
		var sensitive bool
		if err := rows.Scan(&key, &category, &desc, &risk, &sensitive); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取权限目录失败", nil)
			return
		}
		items = append(items, map[string]any{"key": key, "category": category, "description": desc, "risk_level": risk, "sensitive": sensitive})
	}
	respondList(w, r, items, len(items), nil)
}

// ---- 会话 ----

func (s *Server) handleAdminListSessions(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	limit, offset := pagination(r)
	filter := iam.SessionFilter{
		IdentityID: strings.TrimSpace(r.URL.Query().Get("identity_id")),
		Audience:   strings.TrimSpace(r.URL.Query().Get("audience")),
		ActiveOnly: r.URL.Query().Get("active") == "true",
		Limit:      limit, Offset: offset,
	}
	if !s.isPlatformScope(r) {
		filter.OrganizationID = PrincipalFrom(r.Context()).OrganizationID
	}
	list, total, err := s.iam.Sessions().List(r.Context(), filter)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取会话失败", nil)
		return
	}
	s.writeAuditNow(r, "admin:session:read", "admin.security", "session", "", map[string]any{"result_count": total})
	respondList(w, r, list, total, nil)
}

func (s *Server) handleAdminRevokeSession(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Confirm bool   `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "强制下线需要二次确认", nil)
		return
	}
	p := PrincipalFrom(r.Context())
	var targetIdentity, targetOrg string
	if err := s.deps.Store.Pool.QueryRow(r.Context(),
		`SELECT identity_id, organization_id FROM sessions WHERE id = $1`, id).Scan(&targetIdentity, &targetOrg); err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "会话不存在", nil)
		return
	}
	if targetOrg != p.OrganizationID && !s.isPlatformScope(r) {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "会话不存在", nil)
		return
	}
	if err := s.iam.Sessions().RevokeByID(r.Context(), id, "revoked_by_admin", p.IdentityID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "撤销会话失败", nil)
		return
	}
	meta := metaFrom(r.Context())
	_ = iam.RecordLoginEvent(r.Context(), s.iam.Repo().Pool(), s.cfg.Env, iam.LoginEvent{
		EventType: iam.EventSessionRevoked, Result: "success", IdentityID: targetIdentity,
		Audience: iam.AudienceAdmin, SessionID: id, IP: meta.IP, UserAgent: meta.UserAgent,
		RiskLevel: "critical", Detail: map[string]any{"actor_id": p.IdentityID, "reason": body.Reason},
	})
	s.writeAuditNow(r, "admin:session:revoke", "admin.security", "session", id,
		map[string]any{"reason": body.Reason, "target_identity_id": targetIdentity})
	respondOK(w, r, map[string]any{"revoked": true})
}

// ---- 登录记录 ----

func (s *Server) handleAdminLoginEvents(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	limit, offset := pagination(r)
	filter := iam.LoginEventFilter{
		IdentityID: strings.TrimSpace(r.URL.Query().Get("identity_id")),
		Result:     strings.TrimSpace(r.URL.Query().Get("result")),
		EventType:  strings.TrimSpace(r.URL.Query().Get("event_type")),
		ClientIP:   strings.TrimSpace(r.URL.Query().Get("client_ip")),
		SessionID:  strings.TrimSpace(r.URL.Query().Get("session_id")),
		Limit:      limit, Offset: offset,
	}
	if !s.isPlatformScope(r) {
		filter.OrganizationID = PrincipalFrom(r.Context()).OrganizationID
	}
	if from := parseTimeQuery(r, "from"); from != nil {
		filter.From = from
	}
	if to := parseTimeQuery(r, "to"); to != nil {
		filter.To = to
	}
	list, total, err := iam.QueryLoginEvents(r.Context(), s.deps.Store.Pool, filter)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取登录记录失败", nil)
		return
	}
	s.writeAuditNow(r, "admin:login-event:read", "admin.security", "login_event", "", map[string]any{"result_count": total})
	respondList(w, r, list, total, nil)
}

// ---- 审计 ----

type auditEventRow struct {
	ID             string         `json:"id"`
	Seq            int64          `json:"seq"`
	OccurredAt     time.Time      `json:"occurred_at"`
	ActorID        string         `json:"actor_id,omitempty"`
	ActorEmail     string         `json:"actor_email"`
	ActorType      string         `json:"actor_type"`
	ActorRoles     []string       `json:"actor_roles"`
	OrganizationID string         `json:"organization_id,omitempty"`
	WorkspaceID    string         `json:"workspace_id,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	Action         string         `json:"action"`
	Category       string         `json:"category"`
	ResourceType   string         `json:"resource_type"`
	ResourceID     string         `json:"resource_id"`
	Result         string         `json:"result"`
	HTTPStatus     int            `json:"http_status"`
	HTTPMethod     string         `json:"http_method"`
	Route          string         `json:"route_template"`
	RequestID      string         `json:"request_id"`
	TraceID        string         `json:"trace_id"`
	PermissionKey  string         `json:"permission_key,omitempty"`
	PolicyDecision string         `json:"policy_decision"`
	DenyReason     string         `json:"deny_reason"`
	RiskLevel      string         `json:"risk_level"`
	Sensitive      bool           `json:"sensitive"`
	ClientIP       string         `json:"client_ip"`
	PeerIP         string         `json:"peer_ip"`
	UserAgent      string         `json:"user_agent"`
	Detail         map[string]any `json:"detail"`
	Changes        map[string]any `json:"changes,omitempty"`
	PrevHash       string         `json:"prev_hash"`
	EventHash      string         `json:"event_hash"`
}

func (s *Server) handleAdminAuditEvents(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	limit, offset := pagination(r)
	where := []string{"1=1"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.Replace(cond, "?", "$"+itoa(len(args)), 1))
	}
	if v := strings.TrimSpace(r.URL.Query().Get("actor_id")); v != "" {
		add("actor_id = ?", v)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("action")); v != "" {
		add("action = ?", v)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("category")); v != "" {
		add("category = ?", v)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("result")); v != "" {
		add("result = ?", v)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("resource_type")); v != "" {
		add("resource_type = ?", v)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("trace_id")); v != "" {
		add("trace_id = ?", v)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("session_id")); v != "" {
		add("session_id = ?::uuid", v)
	}
	if from := parseTimeQuery(r, "from"); from != nil {
		add("occurred_at >= ?", *from)
	}
	if to := parseTimeQuery(r, "to"); to != nil {
		add("occurred_at <= ?", *to)
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT count(*) FROM audit_events_v2 WHERE `+clause, args...).Scan(&total); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取审计日志失败", nil)
		return
	}
	args = append(args, limit, offset)
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT id, seq, occurred_at, coalesce(actor_id::text,''), actor_email, actor_type, actor_roles,
               coalesce(organization_id::text,''), coalesce(workspace_id::text,''), coalesce(session_id::text,''),
               action, category, resource_type, resource_id, result, http_status, http_method, route_template,
               request_id, trace_id, coalesce(permission_key,''), policy_decision, deny_reason, risk_level, sensitive,
               coalesce(host(client_ip),''), coalesce(host(peer_ip),''), user_agent, detail, changes, prev_hash, event_hash
        FROM audit_events_v2 WHERE `+clause+
		" ORDER BY seq DESC LIMIT $"+itoa(len(args)-1)+" OFFSET $"+itoa(len(args)), args...)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取审计日志失败", nil)
		return
	}
	defer rows.Close()
	items := []auditEventRow{}
	for rows.Next() {
		var row auditEventRow
		var detailRaw, changesRaw []byte
		if err := rows.Scan(&row.ID, &row.Seq, &row.OccurredAt, &row.ActorID, &row.ActorEmail, &row.ActorType, &row.ActorRoles,
			&row.OrganizationID, &row.WorkspaceID, &row.SessionID, &row.Action, &row.Category, &row.ResourceType,
			&row.ResourceID, &row.Result, &row.HTTPStatus, &row.HTTPMethod, &row.Route, &row.RequestID, &row.TraceID,
			&row.PermissionKey, &row.PolicyDecision, &row.DenyReason, &row.RiskLevel, &row.Sensitive,
			&row.ClientIP, &row.PeerIP, &row.UserAgent, &detailRaw, &changesRaw, &row.PrevHash, &row.EventHash); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取审计日志失败", nil)
			return
		}
		row.Detail = map[string]any{}
		_ = json.Unmarshal(detailRaw, &row.Detail)
		if len(changesRaw) > 0 {
			row.Changes = map[string]any{}
			_ = json.Unmarshal(changesRaw, &row.Changes)
		}
		items = append(items, row)
	}
	s.writeAuditNow(r, "admin:audit:read", "admin.security", "audit_event", "", map[string]any{"result_count": total})
	respondList(w, r, items, total, nil)
}

func (s *Server) handleAdminAuditEventDetail(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var row auditEventRow
	var detailRaw, changesRaw, beforeRaw, afterRaw []byte
	err := s.deps.Store.Pool.QueryRow(r.Context(), `
        SELECT id, seq, occurred_at, coalesce(actor_id::text,''), actor_email, actor_type, actor_roles,
               coalesce(organization_id::text,''), coalesce(workspace_id::text,''), coalesce(session_id::text,''),
               action, category, resource_type, resource_id, result, http_status, http_method, route_template,
               request_id, trace_id, coalesce(permission_key,''), policy_decision, deny_reason, risk_level, sensitive,
               coalesce(host(client_ip),''), coalesce(host(peer_ip),''), user_agent, detail, changes, before, after, prev_hash, event_hash
        FROM audit_events_v2 WHERE id = $1`, id).
		Scan(&row.ID, &row.Seq, &row.OccurredAt, &row.ActorID, &row.ActorEmail, &row.ActorType, &row.ActorRoles,
			&row.OrganizationID, &row.WorkspaceID, &row.SessionID, &row.Action, &row.Category, &row.ResourceType,
			&row.ResourceID, &row.Result, &row.HTTPStatus, &row.HTTPMethod, &row.Route, &row.RequestID, &row.TraceID,
			&row.PermissionKey, &row.PolicyDecision, &row.DenyReason, &row.RiskLevel, &row.Sensitive,
			&row.ClientIP, &row.PeerIP, &row.UserAgent, &detailRaw, &changesRaw, &beforeRaw, &afterRaw, &row.PrevHash, &row.EventHash)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "审计事件不存在", nil)
		return
	}
	row.Detail = map[string]any{}
	_ = json.Unmarshal(detailRaw, &row.Detail)
	if len(changesRaw) > 0 {
		row.Changes = map[string]any{}
		_ = json.Unmarshal(changesRaw, &row.Changes)
	}
	before := map[string]any{}
	after := map[string]any{}
	_ = json.Unmarshal(beforeRaw, &before)
	_ = json.Unmarshal(afterRaw, &after)
	s.writeAuditNow(r, "admin:audit:read.detail", "admin.security", "audit_event", id, nil)
	respondOK(w, r, map[string]any{"event": row, "before": before, "after": after})
}

func (s *Server) handleAdminAuditChains(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	chains := []audit.ChainStatus{}
	for _, name := range []string{audit.ChainAudit, audit.ChainLogin} {
		st, err := audit.ChainState(r.Context(), s.deps.Store.Pool, name)
		if err != nil {
			continue
		}
		chains = append(chains, st)
	}
	respondOK(w, r, map[string]any{"chains": chains})
}

// handleAdminAuditVerify 重新计算哈希链，检测篡改（§27 AUDIT-004）。
func (s *Server) handleAdminAuditVerify(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var body struct {
		Chain   string `json:"chain"`
		FromSeq int64  `json:"from_seq"`
		ToSeq   int64  `json:"to_seq"`
	}
	_ = decodeJSON(r, &body)
	if body.Chain == "" {
		body.Chain = audit.ChainAudit
	}
	if body.Chain != audit.ChainAudit && body.Chain != audit.ChainLogin {
		validationError(w, r, "chain 取值非法", []fieldError{{Field: "chain", Code: "invalid_value", Message: "audit 或 login"}})
		return
	}
	result, err := audit.VerifyChain(r.Context(), s.deps.Store.Pool, body.Chain, body.FromSeq, body.ToSeq)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "校验失败", nil)
		return
	}
	s.writeAuditNow(r, "admin:audit:verify", "admin.security", "audit_chain", body.Chain, map[string]any{
		"checked": result.Checked, "valid": result.Valid, "broken_at_seq": result.BrokenAtSeq,
	})
	respondOK(w, r, result)
}

// ---- 辅助 ----

func (s *Server) adminDBReady(w http.ResponseWriter, r *http.Request) bool {
	if s.deps.Store == nil || s.iam == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return false
	}
	return true
}

// isPlatformScope 判断当前主体是否具备跨组织可见性（owner / platform_admin / security_admin / auditor）。
func (s *Server) isPlatformScope(r *http.Request) bool {
	p := PrincipalFrom(r.Context())
	return p.HasAny("admin:org:manage", "admin:user:create", "admin:role:manage")
}

func (s *Server) policyEngineInvalidate(identityID string) {
	if s.policy != nil {
		s.policy.Invalidate(identityID)
	}
	if s.guard != nil {
		s.guard.Invalidate(identityID)
	}
}

func (s *Server) writeAuditWithChange(r *http.Request, action, category, resourceType, resourceID string, before, after any, detail map[string]any) {
	if s.audit == nil {
		return
	}
	p := PrincipalFrom(r.Context())
	meta := metaFrom(r.Context())
	ev := auditEventFor(r, p, meta, action, category, resourceType, resourceID, "success", detail)
	ev.Before = before
	ev.After = after
	ev.Changes = map[string]any{"before": before, "after": after}
	if err := s.audit.Write(r.Context(), ev); err != nil {
		s.deps.Log.Error("audit write failed", "action", action, "err", err)
	}
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func parseTimeQuery(r *http.Request, key string) *time.Time {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return &t
	}
	return nil
}
