package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
)

// handleAdminDeleteUser 删除（离职）用户 —— 迁移 0067 + 计划
// docs/multiuser-login-and-isolation-plan.md §3.2 / §3.5.1（权限键 admin:user:delete）。
//
// 语义（决策 D6，2026-09-18 需求方确认）：
//  1. 二次确认 + 原因必填（与 disable / reset-password / reset-mfa 同规格）；
//  2. **软删而非物理删除**：status='deleted' + deleted_at=now()，email 与 display_name 匿名化。
//     身份行必须保留 —— audit_events_v2 与 login_events 通过外键引用 identities(id)，
//     物理删除会破坏 append-only 审计的可回溯性（AD-005 / ADR-018）；
//  3. 撤销授权：member_role_bindings 置 revoked_at、组织/工作区成员关系置 removed，
//     因此该账号既不能登录，也不会再出现在任何作用域判定里；
//  4. **归档其个人空间**（0066 的 kind='personal'）：archived_at=now()，其下对象转为只读；
//  5. 强制下线其全部会话，并使策略缓存失效；
//  6. 全过程写审计，且审计里保留**原邮箱**（匿名化只发生在身份行上，历史仍可追溯）。
//
// 保护：不能删除当前登录账号；不能删除组织最后一个可用 Owner（与停用同一约束）。
func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "用户不存在", nil)
		return
	}
	var body struct {
		Confirm bool   `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "删除用户需要二次确认", nil)
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		validationError(w, r, "必须填写删除原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}

	ctx := r.Context()
	identity, err := s.iam.Repo().GetIdentityByID(ctx, id)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "用户不存在", nil)
		return
	}
	p := PrincipalFrom(ctx)
	if identity.ID == p.IdentityID {
		respondError(w, r, http.StatusUnprocessableEntity, "VALIDATION_SELF_LOCKOUT", "不能删除当前登录账号", nil)
		return
	}
	if identity.Status == iam.StatusDeleted {
		respondError(w, r, http.StatusConflict, "CONFLICT_ALREADY_DELETED", "该用户已删除", nil)
		return
	}
	// 最后一个 Owner 约束：删除与停用一样会让组织失去一名 Owner（§28.5）。
	if owners, err := s.iam.Repo().CountActiveOwners(ctx, identity.OrganizationID); err == nil && owners <= 1 {
		roles, _ := s.iam.Repo().EffectiveRoles(ctx, identity.ID)
		if contains(roles, "owner") {
			respondError(w, r, http.StatusUnprocessableEntity, "CONFLICT_LAST_OWNER",
				"组织必须保留至少一名可用的 Owner", nil)
			return
		}
	}

	// 1) 归档个人空间（其下对象随之只读）。
	var archivedWorkspaces []string
	rows, err := s.deps.Store.Pool.Query(ctx, `
		UPDATE workspaces
		   SET archived_at = now()
		 WHERE kind = 'personal' AND owner_identity_id = $1 AND archived_at IS NULL
		RETURNING id::text`, id)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "个人空间归档失败", nil)
		return
	}
	for rows.Next() {
		var wsID string
		if scanErr := rows.Scan(&wsID); scanErr != nil {
			rows.Close()
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "个人空间归档失败", nil)
			return
		}
		archivedWorkspaces = append(archivedWorkspaces, wsID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "个人空间归档失败", nil)
		return
	}

	// 2) 撤销授权（角色绑定 + 两类成员关系）。
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE member_role_bindings SET revoked_at = now()
		 WHERE identity_id = $1 AND revoked_at IS NULL`, id); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "角色绑定撤销失败", nil)
		return
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE workspace_memberships SET status = 'removed', updated_at = now()
		 WHERE identity_id = $1 AND status <> 'removed'`, id); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "工作区成员关系撤销失败", nil)
		return
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE organization_memberships SET status = 'removed', updated_at = now()
		 WHERE identity_id = $1 AND status <> 'removed'`, id); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "组织成员关系撤销失败", nil)
		return
	}

	// 3) 软删 + 匿名化。邮箱改写为 deleted+<uuid>@invalid：既不可识别，又满足
	//    identities_email_lower_uidx 的唯一约束（同一邮箱不会被再次占用）。
	anonymousEmail := "deleted+" + id + "@invalid"
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE identities
		   SET status = 'deleted',
		       deleted_at = now(),
		       email = $2,
		       display_name = '已删除用户',
		       updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`, id, anonymousEmail); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "用户删除失败", nil)
		return
	}

	// 4) 强制下线全部会话 + 策略缓存失效。
	if _, err := s.iam.ForceLogout(ctx, identity, id, iam.LoginInput{}); err != nil {
		// 会话撤销失败不阻断删除（账号已是 deleted，登录路径会拒绝），但要留痕。
		if s.deps.Log != nil {
			s.deps.Log.Error("force logout after user delete failed", "err", err, "identity_id", id)
		}
	}
	s.policyEngineInvalidate(id)

	// 5) 审计：before 保留原邮箱与状态，after 记录匿名化结果与归档的工作区。
	s.writeAuditWithChange(r, "admin:user:delete", "admin.identity", "identity", id,
		map[string]any{"status": identity.Status, "email": identity.Email, "display_name": identity.DisplayName},
		map[string]any{"status": iam.StatusDeleted, "email": anonymousEmail, "archived_workspaces": archivedWorkspaces},
		map[string]any{"reason": reason})
	_ = iam.RecordLoginEvent(ctx, s.deps.Store.Pool, s.cfg.Env, iam.LoginEvent{
		EventType: "admin.user.deleted", Result: "success", IdentityID: id,
		SubjectEmail: identity.Email, OrganizationID: identity.OrganizationID,
		Audience: iam.AudienceAdmin, RiskLevel: "high",
		Detail: map[string]any{"deleted_by": p.Email, "reason": reason, "archived_workspaces": archivedWorkspaces},
	})

	respondOK(w, r, map[string]any{
		"deleted":                      true,
		"identity_id":                  id,
		"status":                       iam.StatusDeleted,
		"anonymous_email":              anonymousEmail,
		"archived_personal_workspaces": archivedWorkspaces,
		"note":                         "身份行保留以维持审计可回溯；邮箱与显示名已匿名化，个人空间已归档为只读。",
	})
}
