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

	r.Get("/notifications", s.require("notification:list", s.handleUserNotifications))
	r.Post("/notifications/{id}/read", s.require("notification:update", s.handleUserNotificationRead))
	r.Post("/notifications/read-all", s.require("notification:update", s.handleUserNotificationsReadAll))
	r.Post("/notifications/{id}/open", s.require("notification:list", s.handleUserNotificationOpen))

	// 现有调查/下载/导出能力已经可用，先以兼容读路由暴露，页面迁移随后进行。
	// 列表查询在 SQL 层强制作用域；详情路由逐对象执行二次授权（§28.5 S3-BE-01）。
	r.Get("/cases", s.require("case:list", s.handleUserCases))
	r.Post("/cases", s.require("case:create", s.handleUserCreateCase))
	r.Get("/cases/{id}", s.require("case:read", s.handleUserCaseDetail))
	r.Patch("/cases/{id}", s.require("case:update", s.handleUserUpdateCase))
	r.Post("/cases/{id}/commands/{command}", s.require("case:state:update", s.handleUserCaseCommand))
	r.Get("/cases/{id}/members", s.require("case:member:list", s.handleUserCaseMembers))
	r.Post("/cases/{id}/members", s.require("case:member:manage", s.handleUserCaseMemberAdd))
	r.Delete("/cases/{id}/members/{identity_id}", s.require("case:member:manage", s.handleUserCaseMemberRemove))
	r.Get("/cases/{id}/subjects", s.require("subject:list", s.handleUserCaseSubjects))
	r.Post("/cases/{id}/subjects", s.require("subject:create", s.handleUserCaseSubjectAdd))
	r.Delete("/cases/{id}/subjects/{subject_id}", s.require("subject:remove", s.handleUserCaseSubjectRemove))
	r.Get("/cases/{id}/activity", s.require("case:activity:read", s.handleUserCaseActivity))
	r.Get("/cases/{id}/comments", s.require("comment:list", s.handleUserCaseComments))
	r.Post("/cases/{id}/comments", s.require("comment:create", s.handleUserCaseCommentAdd))
	r.Get("/cases/{id}/reports", s.require("report:list", s.handleUserCaseReports))
	r.Post("/cases/{id}/reports", s.require("report:create", s.handleUserCaseReportCreate))
	r.Get("/workspace-members", s.require("case:member:list", s.handleUserWorkspaceMembers))

	r.Get("/investigations", s.require("investigation:list", s.handleUserInvestigationListV2))
	r.Post("/investigations", s.require("investigation:create", s.handleUserCreateInvestigation))
	r.Post("/investigations/preview", s.require("investigation:create", s.handleUserInvestigationPreview))
	r.Get("/investigations/{id}", s.require("investigation:read", s.handleUserInvestigationDetailV2))
	r.Get("/investigations/{id}/runtime", s.require("investigation:read", s.handleUserInvestigationRuntime))
	r.Get("/investigations/{id}/findings", s.require("finding:list", s.handleUserInvestigationFindings))
	r.Get("/investigations/{id}/evidence", s.require("evidence:list", s.handleUserInvestigationEvidence))
	r.Post("/investigations/{id}/commands/start", s.require("investigation:start", s.handleUserInvestigationStart))
	r.Post("/investigations/{id}/commands/pause", s.require("investigation:pause", s.handleUserInvestigationPause))
	r.Post("/investigations/{id}/commands/resume", s.require("investigation:resume", s.handleUserInvestigationResume))
	r.Post("/investigations/{id}/commands/cancel", s.require("investigation:cancel", s.handleUserInvestigationCancel))
	r.Post("/investigations/{id}/commands/retry", s.require("investigation:retry", s.handleUserInvestigationRetry))
	r.Post("/investigations/{id}/commands/submit-review", s.require("investigation:submit_review", s.handleUserInvestigationSubmitReview))

	r.Get("/findings/{id}", s.require("finding:read", s.handleUserFindingDetail))
	r.Patch("/findings/{id}", s.require("finding:update", s.handleUserFindingPatch))
	r.Post("/findings/{id}/commands/confirm", s.require("finding:review", s.handleUserFindingConfirm))
	r.Post("/findings/{id}/commands/reject", s.require("finding:review", s.handleUserFindingReject))
	r.Post("/findings/{id}/commands/supersede", s.require("finding:review", s.handleUserFindingSupersede))

	r.Get("/evidence/{id}", s.require("evidence:read", s.handleUserEvidenceDetail))
	r.Post("/evidence/{id}/commands/verify", s.require("evidence:review", s.handleUserEvidenceVerify))
	r.Post("/evidence/{id}/commands/dispute", s.require("evidence:review", s.handleUserEvidenceDispute))
	r.Post("/evidence/{id}/commands/supersede", s.require("evidence:review", s.handleUserEvidenceSupersede))

	r.Get("/reports", s.require("report:list", s.handleUserReports))
	r.Get("/reports/{id}", s.require("report:read", s.handleUserReportDetail))
	r.Post("/reports/{id}/commands/submit", s.require("report:create", s.handleUserReportSubmit))
	r.Post("/reports/{id}/commands/approve", s.require("report:review", s.handleUserReportApprove))
	r.Post("/reports/{id}/commands/reject", s.require("report:review", s.handleUserReportReject))
	r.Post("/reports/{id}/commands/sign", s.require("report:sign", s.handleUserReportSign))
	r.Post("/reports/{id}/commands/archive", s.require("report:review", s.handleUserReportArchive))
	r.Post("/reports/{id}/download-ticket", s.require("report:download", s.handleUserReportTicket))
	r.Get("/reports/download/{ticket}", s.require("report:download", s.handleUserReportDownload))
	r.Head("/reports/download/{ticket}", s.require("report:download", s.handleUserReportDownload))

	r.Get("/inbox", s.require("inbox:read", s.handleUserInbox))

	// ---- Sprint 5：Explorer / 下载 / 任务 / 导出 / Canvas（§28.7）----
	// Explorer v2：所有 Tab 返回统一 envelope（来源/口径/Coverage），cursor 由服务端生成。
	r.Get("/explorer/addresses/{chain}/{address}/overview", s.require("explorer:read", s.handleExplorerOverview))
	r.Get("/explorer/addresses/{chain}/{address}/assets", s.require("explorer:read", s.handleExplorerAssets))
	r.Get("/explorer/addresses/{chain}/{address}/activity", s.require("explorer:read", s.handleExplorerActivity))
	r.Get("/explorer/addresses/{chain}/{address}/counterparties", s.require("explorer:read", s.handleExplorerCounterparties))
	r.Get("/explorer/addresses/{chain}/{address}/semantic", s.require("explorer:read", s.handleExplorerSemantic))
	r.Get("/explorer/addresses/{chain}/{address}/flow-profile", s.require("explorer:read", s.handleExplorerFlowProfile))

	// v2 元数据：链/Dataset 选项来自服务端 registry。
	r.Get("/meta/chains", s.require("explorer:read", s.handleUserChains))
	r.Get("/meta/datasets", s.require("download:create", s.handleUserDatasets))
	r.Get("/explorer/transactions/{chain}/{hash}", s.require("explorer:read", s.handleExplorerTransaction))
	r.Get("/explorer/transactions/{chain}/{hash}/logs", s.require("explorer:read", s.handleExplorerTransactionLogs))
	r.Get("/explorer/transactions/{chain}/{hash}/trace", s.require("explorer:read", s.handleExplorerTransactionTrace))

	// 下载 v2：preview/create 分离；默认 prefer_local 复用本地，refresh 需要显式权限。
	r.Post("/downloads/preview", s.require("download:create", s.handleDownloadPreview))
	r.Post("/downloads", s.require("download:create", s.handleDownloadCreate))

	// 任务 v2：列表/详情/控制/SSE（Last-Event-ID 重放）。
	r.Get("/jobs", s.require("job:list:self", s.handleUserJobsV2))
	r.Get("/jobs/{id}", s.require("job:read", s.handleUserJobDetailV2))
	r.Post("/jobs/{id}/commands/{command}", s.require("job:control", s.handleUserJobCommand))
	r.Get("/jobs/{id}/events", s.require("job:read", s.handleUserJobEvents))

	// 导出 v2：异步创建、cursor 列表、60 秒一次性票据下载。
	r.Get("/exports/download/{ticket}", s.require("export:download", s.handleExportV2Download))
	r.Head("/exports/download/{ticket}", s.require("export:download", s.handleExportV2Download))
	r.Get("/exports", s.require("export:list", s.handleExportsV2List))
	r.Post("/exports", s.require("export:create", s.handleExportV2Create))
	r.Get("/exports/{id}", s.require("export:read", s.handleExportV2Detail))
	r.Post("/exports/{id}/download-ticket", s.require("export:download", s.handleExportV2Ticket))

	// Canvas v2：revision 冲突返回字段级 diff；跨案件 Evidence 引用在保存前拒绝。
	r.Get("/investigations/{id}/canvases", s.require("canvas:list", s.handleInvestigationCanvasesV2))
	r.Post("/investigations/{id}/canvases", s.require("canvas:create", s.handleInvestigationCanvasesV2))
	r.Get("/canvases/{id}", s.require("canvas:read", s.handleCanvasV2Get))
	r.Patch("/canvases/{id}", s.require("canvas:update", s.handleCanvasV2Update))
	r.Post("/canvases/{id}/branches", s.require("canvas:branch", s.handleCanvasV2Branch))
	r.Get("/canvases/{id}/snapshots", s.require("canvas:snapshot", s.handleCanvasV2Snapshots))
	r.Post("/canvases/{id}/snapshots", s.require("canvas:snapshot:create", s.handleCanvasV2Snapshots))
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
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM cases WHERE organization_id = $1 AND workspace_id = $2 AND status <> 'archived'`, p.OrganizationID, p.WorkspaceID).Scan(&caseCount)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM investigations WHERE organization_id = $1 AND workspace_id = $2`, p.OrganizationID, p.WorkspaceID).Scan(&invCount)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM download_jobs WHERE organization_id = $1 AND workspace_id = $2 AND status IN ('RUNNING','PENDING','PAUSED')`, p.OrganizationID, p.WorkspaceID).Scan(&jobActive)
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
		"id":                   identity.ID,
		"email":                identity.Email,
		"display_name":         identity.DisplayName,
		"status":               identity.Status,
		"roles":                p.Roles,
		"organization_id":      identity.OrganizationID,
		"must_change_password": identity.MustChangePassword,
		"mfa_required":         identity.MFARequired,
		"last_login_at":        identity.LastLoginAt,
		"created_at":           identity.CreatedAt,
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
		"timezone":       "Asia/Bangkok",
		"language":       "zh-CN",
		"number_format":  "tabulated",
		"reduced_motion": false,
		"note":           "偏好持久化在 S4 迁移中加入 user_preferences 表；当前返回服务端默认真实值。",
	})
}

func (s *Server) handleUserPreferencesUpdate(w http.ResponseWriter, r *http.Request) {
	respondError(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED",
		"偏好持久化尚未实现（缺少 user_preferences 表）", map[string]any{"feature": "profile.preferences.write"})
}

// ---- 对象详情：先做对象级二次授权，再取数据 ----

// handleUserCaseDetail 演示两道防线：requireObject 判权限与租户归属，
// 随后的 SQL 再带 organization_id 条件，任一防线失效都不会返回跨租户数据。
func (s *Server) handleUserCaseDetail(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireObject(w, r, "case", chi.URLParam(r, "id")); !ok {
		return
	}
	if s.casework == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "案件服务未初始化", nil)
		return
	}
	item, err := s.casework.GetCase(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserInvestigationDetail(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.requireObject(w, r, "investigation", chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var id, goal, status string
	var caseID *string
	var createdAt, updatedAt time.Time
	err := s.deps.Store.Pool.QueryRow(r.Context(), `
        SELECT id, coalesce(goal,''), status, case_id::text, created_at, updated_at
        FROM investigations WHERE id = $1 AND organization_id = $2`,
		chi.URLParam(r, "id"), PrincipalFrom(r.Context()).OrganizationID).
		Scan(&id, &goal, &status, &caseID, &createdAt, &updatedAt)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "调查不存在或无权访问", nil)
		return
	}
	respondOK(w, r, map[string]any{
		"id": id, "goal": goal, "status": status, "case_id": caseID,
		"organization_id": scope.OrganizationID, "workspace_id": scope.WorkspaceID,
		"created_at": createdAt, "updated_at": updatedAt,
	})
}

func (s *Server) handleUserJobDetail(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.requireObject(w, r, "job", chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var id, dataset, status string
	var createdAt, updatedAt time.Time
	err := s.deps.Store.Pool.QueryRow(r.Context(), `
        SELECT id, dataset, status, created_at, updated_at
        FROM download_jobs WHERE id = $1 AND organization_id = $2`,
		chi.URLParam(r, "id"), PrincipalFrom(r.Context()).OrganizationID).
		Scan(&id, &dataset, &status, &createdAt, &updatedAt)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "任务不存在或无权访问", nil)
		return
	}
	respondOK(w, r, map[string]any{
		"id": id, "dataset": dataset, "status": status,
		"organization_id": scope.OrganizationID, "workspace_id": scope.WorkspaceID,
		"created_at": createdAt, "updated_at": updatedAt,
	})
}

// handleUserCases / handleUserInvestigations / handleUserJobs 复用既有控制面数据，
// 但强制注入服务端解析出的 workspace 作用域（前端不能提交 tenant ID）。
func (s *Server) handleUserCases(w http.ResponseWriter, r *http.Request) {
	if s.casework == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "案件服务未初始化", nil)
		return
	}
	items, next, total, err := s.casework.ListCases(r.Context(), s.caseworkActor(r),
		r.URL.Query().Get("status"), r.URL.Query().Get("q"), r.URL.Query().Get("cursor"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, total, next)
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
        FROM investigations WHERE organization_id = $1 AND workspace_id = $2
        ORDER BY updated_at DESC LIMIT $3 OFFSET $4`, p.OrganizationID, p.WorkspaceID, limit, offset)
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
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT count(*) FROM investigations WHERE organization_id = $1 AND workspace_id = $2`, p.OrganizationID, p.WorkspaceID).Scan(&total)
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
        FROM download_jobs WHERE organization_id = $1 AND workspace_id = $2
        ORDER BY updated_at DESC LIMIT $3 OFFSET $4`, p.OrganizationID, p.WorkspaceID, limit, offset)
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
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT count(*) FROM download_jobs WHERE organization_id = $1 AND workspace_id = $2`, p.OrganizationID, p.WorkspaceID).Scan(&total)
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
