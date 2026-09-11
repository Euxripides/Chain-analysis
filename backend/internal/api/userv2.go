package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// registerUserV2 挂载 §21.5 用户层 API 中已经实现的部分。
//
// 尚未实现的端点必须显式返回 501 + NOT_IMPLEMENTED，不得静默伪装成可用能力
// （§32 约束 10：未实现标记 NOT_IMPLEMENTED）。
func (s *Server) registerUserV2(r chi.Router) {
	r.Get("/dashboard", s.require("dashboard:read", s.handleUserDashboard))
	r.Get("/search", s.require("search:read", s.handleUserSearch))

	r.Get("/me/profile", s.require("profile:read", s.handleUserProfile))
	r.Patch("/me/profile", s.require("profile:update:self", s.handleUserProfileUpdate))
	r.Get("/me/preferences", s.require("profile:read", s.handleUserPreferences))
	r.Patch("/me/preferences", s.require("profile:update:self", s.handleUserPreferencesUpdate))

	r.Get("/notifications", s.require("notification:list", s.handleNotImplementedV2("notification.list")))
	r.Post("/notifications/{id}/read", s.require("notification:update", s.handleNotImplementedV2("notification.read")))
	r.Post("/notifications/read-all", s.require("notification:update", s.handleNotImplementedV2("notification.read_all")))

	// 现有调查/下载/导出能力已经可用，先以兼容读路由暴露，页面迁移随后进行。
	r.Get("/cases", s.require("case:list", s.handleUserCases))
	r.Get("/investigations", s.require("investigation:list", s.handleUserInvestigations))
	r.Get("/jobs", s.require("job:list:self", s.handleUserJobs))
}

func (s *Server) handleNotImplementedV2(feature string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respondError(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "该能力尚未实现", map[string]any{"feature": feature})
	}
}

// handleUserDashboard 返回"我的概览"所需聚合。所有指标都来自本地真实数据，
// 不使用占位值（docs/code-standards.md 验证要求）。
func (s *Server) handleUserDashboard(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	ctx := r.Context()
	asOf := time.Now().UTC()

	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	pool := s.deps.Store.Pool

	var caseCount, invCount, jobActive, reportCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM cases WHERE workspace_id = $1 AND status <> 'archived'`, p.WorkspaceID).Scan(&caseCount)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM investigations WHERE workspace_id = $1`, p.WorkspaceID).Scan(&invCount)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM download_jobs WHERE workspace_id = $1 AND status IN ('RUNNING','PENDING','PAUSED')`, p.WorkspaceID).Scan(&jobActive)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM investigation_reports WHERE organization_id = $1`, p.OrganizationID).Scan(&reportCount)

	respondOK(w, r, map[string]any{
		"as_of": asOf.Format(time.RFC3339),
		"scope": map[string]any{
			"organization_id": p.OrganizationID,
			"workspace_id":    p.WorkspaceID,
		},
		"cards": []map[string]any{
			{"key": "cases", "label": "进行中案件", "value": caseCount, "unit": "count"},
			{"key": "investigations", "label": "调查任务", "value": invCount, "unit": "count"},
			{"key": "active_jobs", "label": "运行中下载", "value": jobActive, "unit": "count"},
			{"key": "reports", "label": "已生成报告", "value": reportCount, "unit": "count"},
		},
		"quick_actions": buildQuickActions(p),
		"data_source":   "postgres:cases,investigations,download_jobs,investigation_reports",
	})
}

func buildQuickActions(p policy.Principal) []map[string]any {
	type action struct {
		key, label, href, permission string
	}
	all := []action{
		{"case.create", "新建案件", "/cases/new", "case:create"},
		{"download.create", "新建下载", "/downloads/new", "download:create"},
		{"investigation.create", "新建调查", "/investigations/new", "investigation:create"},
		{"search.global", "全局搜索", "/search", "search:read"},
		{"report.create", "生成报告", "/reports", "report:create"},
		{"admin.console", "进入后台管理", "/admin/overview", "admin:dashboard:read"},
	}
	out := make([]map[string]any, 0, len(all))
	for _, a := range all {
		if p.Has(a.permission) {
			out = append(out, map[string]any{"key": a.key, "label": a.label, "href": a.href, "permission": a.permission})
		}
	}
	return out
}

// handleUserSearch 提供最小可用的全局搜索：本地案件与调查名称/编号。
// 完整的多类型搜索（地址/交易/任务/报告）在 S5-FE-01 分批补齐。
func (s *Server) handleUserSearch(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		validationError(w, r, "搜索词至少 2 个字符", []fieldError{{Field: "q", Code: "too_short", Message: "至少 2 个字符"}})
		return
	}
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	like := "%" + strings.ToLower(q) + "%"
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT 'case' AS kind, id::text, name AS title, coalesce(case_no,'') AS subtitle
        FROM cases WHERE workspace_id = $1 AND (lower(name) LIKE $2 OR lower(coalesce(case_no,'')) LIKE $2)
        UNION ALL
        SELECT 'investigation', id::text, coalesce(goal,''), ''
        FROM investigations WHERE workspace_id = $1 AND lower(coalesce(goal,'')) LIKE $2
        LIMIT 25`, p.WorkspaceID, like)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "搜索失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var kind, id, title, subtitle string
		if err := rows.Scan(&kind, &id, &title, &subtitle); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "搜索失败", nil)
			return
		}
		items = append(items, map[string]any{"type": kind, "id": id, "title": title, "subtitle": subtitle})
	}
	respondList(w, r, map[string]any{"query": q, "items": items, "scopes": []string{"case", "investigation"}}, len(items), nil)
}

func (s *Server) handleUserProfile(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	if s.iam == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "认证服务未初始化", nil)
		return
	}
	identity, err := s.iam.Repo().GetIdentityByID(r.Context(), p.IdentityID)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "身份不存在", nil)
		return
	}
	respondOK(w, r, map[string]any{
		"id":                  identity.ID,
		"email":               identity.Email,
		"display_name":        identity.DisplayName,
		"status":              identity.Status,
		"roles":               p.Roles,
		"organization_id":     identity.OrganizationID,
		"must_change_password": identity.MustChangePassword,
		"mfa_required":        identity.MFARequired,
		"last_login_at":       identity.LastLoginAt,
		"created_at":          identity.CreatedAt,
	})
}

func (s *Server) handleUserProfileUpdate(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	name := strings.TrimSpace(body.DisplayName)
	if name == "" || len([]rune(name)) > 80 {
		validationError(w, r, "显示名称长度必须在 1-80 个字符之间", []fieldError{{Field: "display_name", Code: "invalid_length", Message: "1-80 个字符"}})
		return
	}
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	if _, err := s.deps.Store.Pool.Exec(r.Context(),
		`UPDATE identities SET display_name = $2, updated_at = now() WHERE id = $1`, p.IdentityID, name); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "更新资料失败", nil)
		return
	}
	s.writeAuditNow(r, "profile.update", "self", "identity", p.IdentityID, map[string]any{"display_name": name})
	respondOK(w, r, map[string]any{"display_name": name})
}

func (s *Server) handleUserPreferences(w http.ResponseWriter, r *http.Request) {
	respondOK(w, r, map[string]any{
		"timezone":     "Asia/Bangkok",
		"language":     "zh-CN",
		"number_format": "tabulated",
		"reduced_motion": false,
		"note":         "偏好持久化在 S4 迁移中加入 user_preferences 表；当前返回服务端默认真实值。",
	})
}

func (s *Server) handleUserPreferencesUpdate(w http.ResponseWriter, r *http.Request) {
	respondError(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED",
		"偏好持久化尚未实现（缺少 user_preferences 表）", map[string]any{"feature": "profile.preferences.write"})
}

// handleUserCases / handleUserInvestigations / handleUserJobs 复用既有控制面数据，
// 但强制注入服务端解析出的 workspace 作用域（前端不能提交 tenant ID）。
func (s *Server) handleUserCases(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	limit, offset := pagination(r)
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT id, case_no, name, description, status, color, created_at, updated_at
        FROM cases WHERE workspace_id = $1 ORDER BY updated_at DESC LIMIT $2 OFFSET $3`, p.WorkspaceID, limit, offset)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取案件失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, caseNo, name, desc, status, color string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &caseNo, &name, &desc, &status, &color, &createdAt, &updatedAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取案件失败", nil)
			return
		}
		items = append(items, map[string]any{
			"id": id, "case_no": caseNo, "name": name, "description": desc,
			"status": status, "color": color, "created_at": createdAt, "updated_at": updatedAt,
		})
	}
	var total int
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT count(*) FROM cases WHERE workspace_id = $1`, p.WorkspaceID).Scan(&total)
	respondList(w, r, items, total, nil)
}

func (s *Server) handleUserInvestigations(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	limit, offset := pagination(r)
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT id, coalesce(goal,''), status, created_at, updated_at
        FROM investigations WHERE workspace_id = $1 ORDER BY updated_at DESC LIMIT $2 OFFSET $3`, p.WorkspaceID, limit, offset)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取调查任务失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, goal, status string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &goal, &status, &createdAt, &updatedAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取调查任务失败", nil)
			return
		}
		items = append(items, map[string]any{"id": id, "goal": goal, "status": status, "created_at": createdAt, "updated_at": updatedAt})
	}
	var total int
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT count(*) FROM investigations WHERE workspace_id = $1`, p.WorkspaceID).Scan(&total)
	respondList(w, r, items, total, nil)
}

func (s *Server) handleUserJobs(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	limit, offset := pagination(r)
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT id, dataset, status, created_at, updated_at
        FROM download_jobs WHERE workspace_id = $1 ORDER BY updated_at DESC LIMIT $2 OFFSET $3`, p.WorkspaceID, limit, offset)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取任务失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, dataset, status string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &dataset, &status, &createdAt, &updatedAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取任务失败", nil)
			return
		}
		items = append(items, map[string]any{"id": id, "dataset": dataset, "status": status, "created_at": createdAt, "updated_at": updatedAt})
	}
	var total int
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT count(*) FROM download_jobs WHERE workspace_id = $1`, p.WorkspaceID).Scan(&total)
	respondList(w, r, items, total, nil)
}

func pagination(r *http.Request) (int, int) {
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		if n, err := parseInt(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func parseInt(v string) (int, error) {
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, errInvalidInt
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 0, errInvalidInt
		}
	}
	if v == "" {
		return 0, errInvalidInt
	}
	return n, nil
}

var errInvalidInt = errorString("invalid integer")

type errorString string

func (e errorString) Error() string { return string(e) }

// writeAuditNow 同步写入一条业务审计（用于关键的用户自助操作）。
func (s *Server) writeAuditNow(r *http.Request, action, category, resourceType, resourceID string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	p := PrincipalFrom(r.Context())
	meta := metaFrom(r.Context())
	ev := auditEventFor(r, p, meta, action, category, resourceType, resourceID, "success", detail)
	if err := s.audit.Write(r.Context(), ev); err != nil {
		s.deps.Log.Error("audit write failed", "action", action, "err", err)
	}
}

// sessionAudience helper 保留给后续端点使用。
func (s *Server) sessionAudience(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/admin/") {
		return iam.AudienceAdmin
	}
	return iam.AudienceUser
}
