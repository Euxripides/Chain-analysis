package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
)

// Sprint 6 后台身份能力（S6-BE-01 补齐部分）：
//   - 创建用户并绑定工作区角色（ADMIN-001）
//   - 有效权限（角色来源 + 作用域 + 最终 allow/deny，§19.3）
//   - 停用用户影响预览（ADMIN-002：先展示任务/审批/案件/密钥/会话）
//   - 预算阈值评估（ADMIN-007）
//   - 审计导出产物读回（ADMIN-008 的 hash 校验）

func (s *Server) registerAdminIdentityRoutes(r chi.Router) {
	r.Post("/users", s.require("admin:user:create", s.handleAdminCreateUser))
	r.Get("/users/{id}/effective-permissions", s.require("admin:permission:read", s.handleAdminEffectivePermissions))
	r.Get("/users/{id}/impact-preview", s.require("admin:user:read", s.handleAdminUserImpactPreview))
	r.Post("/budgets/{id}/commands/evaluate", s.require("admin:budget:read", s.handleAdminEvaluateBudget))
	r.Get("/audit/exports/{id}/download", s.require("admin:audit:export", s.handleAdminAuditExportDownload))
}

type s6CreateUserInput struct {
	Email          string `json:"email"`
	DisplayName    string `json:"display_name"`
	OrganizationID string `json:"organization_id"`
	WorkspaceID    string `json:"workspace_id"`
	RoleKey        string `json:"role_key"`
	Reason         string `json:"reason"`
}

// handleAdminCreateUser 创建身份并可直接绑定工作区角色；一次性密码只在响应中出现一次。
func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in s6CreateUserInput
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if email == "" || !strings.Contains(email, "@") {
		validationError(w, r, "必须提供合法邮箱", []fieldError{{Field: "email", Code: "invalid", Message: "邮箱格式不正确"}})
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写创建原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	p := PrincipalFrom(ctx)
	orgID := strings.TrimSpace(in.OrganizationID)
	if orgID == "" {
		orgID = p.OrganizationID
	}
	if orgID == "" {
		respondError(w, r, http.StatusUnprocessableEntity, "VALIDATION_INVALID_ARGUMENT", "无法确定所属组织，请显式指定 organization_id", nil)
		return
	}
	var exists bool
	if err := s.deps.Store.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM identities WHERE lower(email)=lower($1))", email).Scan(&exists); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "用户查询失败", nil)
		return
	}
	if exists {
		respondError(w, r, http.StatusConflict, "CONFLICT_DUPLICATE", "该邮箱已存在", map[string]any{"email": email})
		return
	}
	roleKey := strings.TrimSpace(in.RoleKey)
	if roleKey == "" {
		roleKey = "viewer"
	}
	var roleID string
	if err := s.deps.Store.Pool.QueryRow(ctx, "SELECT id::text FROM roles WHERE key=$1 AND is_system LIMIT 1", roleKey).Scan(&roleID); err != nil {
		respondError(w, r, http.StatusUnprocessableEntity, "VALIDATION_UNKNOWN_ROLE", "未知系统角色", map[string]any{"role_key": roleKey})
		return
	}
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "初始密码生成失败", nil)
		return
	}
	password := hex.EncodeToString(raw)
	hash, err := iam.HashPassword(password, iam.DefaultParams())
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "初始密码生成失败", nil)
		return
	}
	rawDisplayName := in.DisplayName
	if strings.TrimSpace(rawDisplayName) == "" {
		rawDisplayName = email
	}
	displayName, nameProblem := normalizeIdentityDisplayName(rawDisplayName)
	if nameProblem != nil {
		validationError(w, r, nameProblem.Message, []fieldError{*nameProblem})
		return
	}
	var identityID string
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO identities (organization_id, email, display_name, status, must_change_password)
		VALUES ($1,$2,$3,'active',true) RETURNING id::text`, orgID, email, displayName).Scan(&identityID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "用户创建失败", nil)
		return
	}
	// 密码写入复用 iam.Repo，避免绕过凭据表约定（credentials.secret_hash + PHC 编码）。
	if err := s.iam.Repo().SetPassword(ctx, identityID, hash, true); err != nil {
		_, _ = s.deps.Store.Pool.Exec(ctx, "DELETE FROM identities WHERE id=$1", identityID)
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "凭据写入失败", nil)
		return
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, identity_id, status)
		VALUES ($1,$2,'active') ON CONFLICT (organization_id, identity_id) DO NOTHING`, orgID, identityID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "成员关系写入失败", nil)
		return
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	// workspace_memberships 没有 id 列（主键是 workspace_id + identity_id）：
	// 这里曾经写成 SELECT id::text，语句报 "column \"id\" does not exist" 后
	// **整个请求事务进入 aborted 状态**，提交门（scope commit gate）随后把结果
	// 改写成误导性的 409 AUTHZ_WORKSPACE_SCOPE_STALE —— 真实原因在响应里完全看不到。
	// 教训：请求事务内的语句失败必须当场暴露，不能"忽略后继续"。
	// ---- 个人空间（迁移 0066 / 计划 §3.3 N1、决策 D1）----
	// 新用户一律先拥有自己的个人空间：旧实现在未指定工作区时回退到「组织内最早创建的工作区」
	//（ORDER BY created_at LIMIT 1），实测把新用户塞进线上 default 工作区（30 名成员共享
	// 26 个案件）——这正是"所有用户共用"的成因，该回退已删除。
	// 角色绑 basic_user（基础操作、零 admin:*），**不绑 owner**：owner 实测持有 178 个权限
	//（含 79 个 admin:*），绑给普通用户等于把后台控制面一起给出去，与决策 D4 冲突。
	personalName := displayName + " 的个人空间"
	personalSlug := "p-" + strings.ReplaceAll(identityID, "-", "")[:12]
	var personalWorkspaceID string
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO workspaces (organization_id, name, slug, kind, owner_identity_id, created_by_identity)
		VALUES ($1,$2,$3,'personal',$4,$5) RETURNING id::text`,
		orgID, personalName, personalSlug, identityID, p.IdentityID).Scan(&personalWorkspaceID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "个人空间创建失败", nil)
		return
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status)
		VALUES ($1,$2,$3,'active') ON CONFLICT (workspace_id, identity_id) DO NOTHING`,
		personalWorkspaceID, orgID, identityID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "个人空间成员关系写入失败", nil)
		return
	}
	var basicRoleID string
	if err := s.deps.Store.Pool.QueryRow(ctx,
		`SELECT id::text FROM roles WHERE key = 'basic_user' AND is_system LIMIT 1`).Scan(&basicRoleID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "缺少系统角色 basic_user（迁移 0068）", nil)
		return
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, reason)
		VALUES ($1,$2,'workspace',$3,$4)`, identityID, basicRoleID, personalWorkspaceID, in.Reason); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "个人空间角色绑定失败", nil)
		return
	}
	if workspaceID != "" {
		if _, err := s.deps.Store.Pool.Exec(ctx, `
			INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status)
			VALUES ($1,$2,$3,'active') ON CONFLICT (workspace_id, identity_id) DO NOTHING`,
			workspaceID, orgID, identityID); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "工作区绑定失败", nil)
			return
		}
	}
	// 额外的 role_key 只在管理员**显式**要求角色、或**显式**指定共享工作区时才绑定：
	// 默认的普通用户只需要个人空间里的 basic_user，不再叠加 viewer（决策 D4/D5）。
	if strings.TrimSpace(in.RoleKey) != "" || workspaceID != "" {
		scopeType := "organization"
		scopeID := any(orgID)
		if workspaceID != "" {
			scopeType = "workspace"
			scopeID = workspaceID
		}
		if _, err := s.deps.Store.Pool.Exec(ctx, `
			INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, reason)
			VALUES ($1,$2,$3,$4,$5)`, identityID, roleID, scopeType, scopeID, in.Reason); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "角色绑定失败", nil)
			return
		}
	}
	s.policyEngineInvalidate(identityID)
	_ = iam.RecordLoginEvent(ctx, s.deps.Store.Pool, s.cfg.Env, iam.LoginEvent{
		EventType: "admin.user.created", Result: "success", IdentityID: identityID,
		SubjectEmail: email, OrganizationID: orgID, Audience: iam.AudienceAdmin, RiskLevel: "high",
		Detail: map[string]any{"role_key": roleKey, "workspace_id": workspaceID, "created_by": p.Email},
	})
	s.s6AuditChange(r, "admin.user.create", "admin.identity", "identity", identityID, nil,
		map[string]any{"email": email, "role_key": roleKey, "workspace_id": workspaceID,
			"personal_workspace_id": personalWorkspaceID, "organization_id": orgID},
		map[string]any{"reason": in.Reason})
	respondCreated(w, r, map[string]any{
		"id": identityID, "email": email, "display_name": displayName, "role_key": roleKey,
		"organization_id": orgID, "workspace_id": workspaceID,
		"personal_workspace_id": personalWorkspaceID, "default_role_key": "basic_user",
		"initial_password": password, "plaintext_returned_once": true, "must_change_password": true,
	})
}

// handleAdminEffectivePermissions 返回角色的来源、作用域与最终权限集合（§19.3）。
func (s *Server) handleAdminEffectivePermissions(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "用户不存在", nil)
		return
	}
	ctx := r.Context()
	var email, status, orgID string
	var platformAdmin bool
	if err := s.deps.Store.Pool.QueryRow(ctx,
		"SELECT email, status, coalesce(organization_id::text,''), is_platform_admin FROM identities WHERE id=$1", id).
		Scan(&email, &status, &orgID, &platformAdmin); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "用户不存在", nil)
		return
	}
	bindings := make([]map[string]any, 0, 8)
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT r.key, r.name, b.scope_type, coalesce(b.scope_id::text,''), b.granted_at, coalesce(b.reason,'')
		FROM member_role_bindings b JOIN roles r ON r.id = b.role_id
		WHERE b.identity_id = $1 AND b.revoked_at IS NULL ORDER BY r.key`, id)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "角色绑定读取失败", nil)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key, name, scopeType, scopeID, reason string
		var grantedAt time.Time
		if err := rows.Scan(&key, &name, &scopeType, &scopeID, &grantedAt, &reason); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "角色绑定读取失败", nil)
			return
		}
		bindings = append(bindings, map[string]any{
			"role_key": key, "role_name": name, "scope_type": scopeType, "scope_id": scopeID,
			"granted_at": grantedAt, "reason": reason,
		})
	}
	permissionRows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT DISTINCT rp.permission_key, coalesce(p.category,''), coalesce(p.risk_level,''), coalesce(p.sensitive,false)
		FROM member_role_bindings b
		JOIN role_permissions rp ON rp.role_id = b.role_id
		LEFT JOIN permissions p ON p.key = rp.permission_key
		WHERE b.identity_id = $1 AND b.revoked_at IS NULL
		ORDER BY 1`, id)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "权限读取失败", nil)
		return
	}
	defer permissionRows.Close()
	permissions := make([]map[string]any, 0, 64)
	for permissionRows.Next() {
		var key, category, risk string
		var sensitive bool
		if err := permissionRows.Scan(&key, &category, &risk, &sensitive); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "权限读取失败", nil)
			return
		}
		permissions = append(permissions, map[string]any{
			"key": key, "category": category, "risk_level": risk, "sensitive": sensitive,
			"source": "member_role_bindings", "decision": "allow",
		})
	}
	workspaces := make([]map[string]any, 0, 4)
	wsRows, wsErr := s.deps.Store.Pool.Query(ctx, `
		SELECT w.id::text, w.name, wm.status FROM workspace_memberships wm
		JOIN workspaces w ON w.id = wm.workspace_id WHERE wm.identity_id=$1 ORDER BY w.name`, id)
	if wsErr == nil {
		defer wsRows.Close()
		for wsRows.Next() {
			var wsID, name, wsStatus string
			if err := wsRows.Scan(&wsID, &name, &wsStatus); err == nil {
				workspaces = append(workspaces, map[string]any{"id": wsID, "name": name, "status": wsStatus})
			}
		}
	}
	s.s6Audit(r, "admin:permission:read.detail", "admin.identity", "identity", id, map[string]any{
		"permission_count": len(permissions), "binding_count": len(bindings),
	})
	respondOK(w, r, map[string]any{
		"identity_id": id, "email": email, "status": status, "organization_id": orgID,
		"is_platform_admin": platformAdmin, "bindings": bindings, "permissions": permissions,
		"permission_count": len(permissions), "workspaces": workspaces,
		"data_source": "postgres:member_role_bindings/role_permissions",
	})
}

// handleAdminUserImpactPreview 停用/修改前的影响预览（ADMIN-002）。
func (s *Server) handleAdminUserImpactPreview(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "用户不存在", nil)
		return
	}
	impact, err := s.s6UserImpact(r.Context(), id)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "用户不存在", nil)
		return
	}
	s.s6Audit(r, "admin:user:read.impact_preview", "admin.identity", "identity", id, map[string]any{"impact": impact})
	respondOK(w, r, map[string]any{"identity_id": id, "impact": impact, "requires_confirmation": true})
}

// s6UserImpact 统计停用该用户会影响的任务、审批、案件、密钥与会话。
func (s *Server) s6UserImpact(ctx context.Context, identityID string) (map[string]any, error) {
	var email, status string
	if err := s.deps.Store.Pool.QueryRow(ctx,
		"SELECT email, status FROM identities WHERE id=$1", identityID).Scan(&email, &status); err != nil {
		return nil, err
	}
	impact := map[string]any{"email": email, "current_status": status}
	var activeSessions, runningJobs, pendingApprovals, ownedCases, caseMemberships, apiKeys int
	_ = s.deps.Store.Pool.QueryRow(ctx,
		"SELECT count(*) FROM sessions WHERE identity_id=$1 AND revoked_at IS NULL AND expires_at > now()", identityID).Scan(&activeSessions)
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT count(*) FROM download_jobs WHERE created_by_identity=$1 AND status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','RESUMING')`,
		identityID).Scan(&runningJobs)
	_ = s.deps.Store.Pool.QueryRow(ctx,
		"SELECT count(*) FROM approval_requests WHERE requested_by=$1 AND status='pending'", identityID).Scan(&pendingApprovals)
	_ = s.deps.Store.Pool.QueryRow(ctx,
		"SELECT count(*) FROM cases WHERE created_by=$1 AND archived_at IS NULL", identityID).Scan(&ownedCases)
	_ = s.deps.Store.Pool.QueryRow(ctx,
		"SELECT count(*) FROM case_members WHERE identity_id=$1", identityID).Scan(&caseMemberships)
	_ = s.deps.Store.Pool.QueryRow(ctx,
		"SELECT count(*) FROM gpt_action_api_keys WHERE created_by_identity=$1 AND revoked_at IS NULL", identityID).Scan(&apiKeys)
	impact["active_sessions"] = activeSessions
	impact["running_jobs"] = runningJobs
	impact["pending_approvals"] = pendingApprovals
	impact["owned_cases"] = ownedCases
	impact["case_memberships"] = caseMemberships
	impact["active_api_keys"] = apiKeys
	impact["data_source"] = "postgres:sessions/download_jobs/approval_requests/cases/gpt_action_api_keys"
	impact["note"] = "停用会立即撤销全部会话并使权限缓存失效；运行中的任务不会被删除，但创建者的新操作会被拒绝。"
	return impact, nil
}

// ---- 预算阈值评估（ADMIN-007） ----

// s6EvaluateBudgets 按周期计算预算使用率；超阈值写入告警，达到动作阈值时登记配置动作。
func (s *Server) s6EvaluateBudgets(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT id::text, name, provider, period, limit_usd, warn_percent, action, action_threshold
		FROM api_control_budgets WHERE enabled ORDER BY name`)
	if err != nil {
		return nil, err
	}
	type budgetRow struct {
		id, name, provider, period, action string
		limit                              float64
		warnPercent, actionThreshold       int
	}
	items := make([]budgetRow, 0, 8)
	for rows.Next() {
		var b budgetRow
		if err := rows.Scan(&b.id, &b.name, &b.provider, &b.period, &b.limit, &b.warnPercent, &b.action, &b.actionThreshold); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, b)
	}
	rows.Close()
	out := make([]map[string]any, 0, len(items))
	for _, b := range items {
		window := "1 month"
		switch b.period {
		case "day":
			window = "1 day"
		case "week":
			window = "7 days"
		case "quarter":
			window = "90 days"
		}
		var spent float64
		_ = s.deps.Store.Pool.QueryRow(ctx, `
			SELECT coalesce(sum(estimated_cost_usd),0)::float8 FROM provider_usage
			WHERE ($1 = 'all' OR provider = $1) AND request_time >= now() - $2::interval`,
			b.provider, window).Scan(&spent)
		utilization := 0.0
		if b.limit > 0 {
			utilization = spent / b.limit
		}
		percent := utilization * 100
		entry := map[string]any{
			"id": b.id, "name": b.name, "provider": b.provider, "period": b.period,
			"limit_usd": formatUSD(b.limit), "spent_usd": formatUSD(spent), "utilization": utilization,
			"warn_percent": b.warnPercent, "action": b.action, "action_threshold": b.actionThreshold,
			"enabled": true, "window": window, "triggered": false, "alert_priority": "", "status": "ok",
		}
		switch {
		case percent >= float64(b.actionThreshold):
			entry["triggered"] = true
			entry["alert_priority"] = "P1"
			entry["status"] = "action_triggered"
			entry["action_detail"] = "预算达到动作阈值，已按配置登记动作：" + b.action
		case percent >= float64(b.warnPercent):
			entry["triggered"] = true
			entry["alert_priority"] = "P2"
			entry["status"] = "warning"
			entry["action_detail"] = "预算达到告警阈值，仅产生告警"
		}
		if entry["triggered"] == true {
			fingerprint := "budget:" + b.id
			priority, _ := entry["alert_priority"].(string)
			title := "预算超阈值：" + b.name
			message := "已用 " + formatUSD(spent) + " / 额度 " + formatUSD(b.limit) +
				"（" + strconv.FormatFloat(percent, 'f', 2, 64) + "%），周期 " + b.period
			labels := mustJSON(map[string]any{"budget_id": b.id, "provider": b.provider, "utilization": utilization, "limit_usd": b.limit, "spent_usd": spent})
			if _, err := s.deps.Store.Pool.Exec(ctx, `
				INSERT INTO api_control_alerts (severity, priority, title, message, fingerprint, status, source, labels, links, occurrence_count)
				VALUES ($1,$2,$3,$4,$5,'OPEN','budget',$6::jsonb,'{}'::jsonb,1)
				ON CONFLICT (fingerprint) DO UPDATE SET
				  severity=EXCLUDED.severity, priority=EXCLUDED.priority, message=EXCLUDED.message,
				  status=CASE WHEN api_control_alerts.status='RESOLVED' THEN 'OPEN' ELSE api_control_alerts.status END,
				  last_seen_at=now(), occurrence_count=api_control_alerts.occurrence_count+1, updated_at=now()`,
				priority, priority, title, message, fingerprint, labels); err == nil {
				entry["alert_created"] = true
			}
			if percent >= float64(b.actionThreshold) {
				_, _ = s.deps.Store.Pool.Exec(ctx, `
					INSERT INTO api_control_actions (action, source, reason, detail)
					VALUES ($1,'automatic',$2,$3::jsonb)`,
					"budget_"+b.action, "预算超阈值自动动作", mustJSON(entry))
				entry["action_recorded"] = "budget_" + b.action
			}
		} else {
			_, _ = s.deps.Store.Pool.Exec(ctx, `
				UPDATE api_control_alerts SET status='RESOLVED', resolved_at=now(), resolution_note='预算已回落', updated_at=now()
				WHERE fingerprint=$1 AND status IN ('OPEN','ACK')`, "budget:"+b.id)
		}
		out = append(out, entry)
	}
	return out, nil
}

// handleAdminEvaluateBudget 单个预算的阈值评估（ADMIN-007）。
func (s *Server) handleAdminEvaluateBudget(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "预算不存在", nil)
		return
	}
	items, err := s.s6EvaluateBudgets(r.Context())
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "预算评估失败", nil)
		return
	}
	for _, item := range items {
		if item["id"] == id {
			s.s6Audit(r, "admin.budget.evaluate", "admin.provider", "budget", id, item)
			respondOK(w, r, item)
			return
		}
	}
	respondError(w, r, http.StatusNotFound, "NOT_FOUND", "预算不存在或未启用", nil)
}

// handleAdminAuditExportDownload 从对象存储读回导出产物，用于校验 hash（ADMIN-008）。
func (s *Server) handleAdminAuditExportDownload(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "导出不存在", nil)
		return
	}
	ctx := r.Context()
	var objectRef, checksum, format string
	var sizeBytes int64
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT coalesce(object_ref,''), coalesce(sha256,''), format, size_bytes FROM audit_exports WHERE id=$1`, id).
		Scan(&objectRef, &checksum, &format, &sizeBytes); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "导出不存在", nil)
		return
	}
	if s.deps.Raw == nil || objectRef == "" {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "导出对象不可读", nil)
		return
	}
	reader, _, err := s.deps.Raw.OpenNamed(ctx, s.deps.Cfg.ExportBucket, objectRef)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "导出对象不存在", nil)
		return
	}
	defer reader.Close()
	contentType := "application/x-ndjson"
	if format == "csv" {
		contentType = "text/csv"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Checksum-Sha256", checksum)
	w.Header().Set("Content-Length", strconv.FormatInt(sizeBytes, 10))
	s.s6Audit(r, "admin.audit.export.download", "admin.security", "audit_export", id, map[string]any{"object_ref": objectRef})
	_, _ = io.Copy(w, reader)
}
