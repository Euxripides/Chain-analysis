package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// registerAdminOrgRoutes 挂载 §21.6 的组织 / 工作区 / 成员角色接口（S3-ADMIN-01）。
//
// 所有变更类接口都要求：
//   - 显式权限声明（require）
//   - 二次确认字段 confirm=true（高风险操作必须显式确认，§20.7）
//   - 审计中的 before/after（writeAuditWithChange）
func (s *Server) registerAdminOrgRoutes(r chi.Router) {
	r.Get("/organizations", s.require("admin:org:read", s.handleAdminListOrganizations))
	r.Get("/organizations/{id}", s.require("admin:org:read", s.handleAdminGetOrganization))
	r.Patch("/organizations/{id}", s.require("admin:org:manage", s.handleAdminUpdateOrganization))

	r.Get("/workspaces", s.require("admin:workspace:read", s.handleAdminListWorkspaces))
	r.Post("/workspaces", s.require("admin:workspace:manage", s.handleAdminCreateWorkspace))
	r.Patch("/workspaces/{id}", s.require("admin:workspace:manage", s.handleAdminUpdateWorkspace))

	r.Get("/members", s.require("admin:member:read", s.handleAdminListMembers))
	r.Get("/users/{id}/roles", s.require("admin:member:read", s.handleAdminUserRoles))
	r.Post("/users/{id}/roles", s.require("admin:member:manage", s.handleAdminGrantRole))
	r.Delete("/users/{id}/roles/{binding}", s.require("admin:member:manage", s.handleAdminRevokeRole))
}

type organizationRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	CreatedAt    string `json:"created_at"`
	MemberCount  int    `json:"member_count"`
	WorkspaceNum int    `json:"workspace_count"`
}

func (s *Server) handleAdminListOrganizations(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	p := PrincipalFrom(r.Context())
	// 非平台级主体只能看到自己所属组织（§3.3）。
	args := []any{}
	where := "1=1"
	if !s.isPlatformScope(r) {
		args = append(args, p.OrganizationID)
		where = "o.id = $1"
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT o.id, o.name, o.slug, o.created_at,
               (SELECT count(*) FROM organization_memberships m WHERE m.organization_id = o.id AND m.status = 'active'),
               (SELECT count(*) FROM workspaces w WHERE w.organization_id = o.id)
        FROM organizations o WHERE `+where+` ORDER BY o.slug`, args...)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取组织失败", nil)
		return
	}
	defer rows.Close()
	items := []organizationRow{}
	for rows.Next() {
		var o organizationRow
		var createdAt any
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &createdAt, &o.MemberCount, &o.WorkspaceNum); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取组织失败", nil)
			return
		}
		o.CreatedAt = formatTime(createdAt)
		items = append(items, o)
	}
	s.writeAuditNow(r, "admin:org:read", "admin.identity", "organization", "", map[string]any{"result_count": len(items)})
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleAdminGetOrganization(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	p := PrincipalFrom(r.Context())
	if !s.isPlatformScope(r) && p.OrganizationID != id {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "组织不存在", nil)
		return
	}
	var o organizationRow
	var createdAt any
	err := s.deps.Store.Pool.QueryRow(r.Context(), `
        SELECT o.id, o.name, o.slug, o.created_at,
               (SELECT count(*) FROM organization_memberships m WHERE m.organization_id = o.id AND m.status = 'active'),
               (SELECT count(*) FROM workspaces w WHERE w.organization_id = o.id)
        FROM organizations o WHERE o.id = $1`, id).
		Scan(&o.ID, &o.Name, &o.Slug, &createdAt, &o.MemberCount, &o.WorkspaceNum)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "组织不存在", nil)
		return
	}
	o.CreatedAt = formatTime(createdAt)
	respondOK(w, r, o)
}

func (s *Server) handleAdminUpdateOrganization(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "修改组织需要二次确认", nil)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len([]rune(name)) > 120 {
		validationError(w, r, "组织名称长度必须在 1-120 个字符之间", []fieldError{{Field: "name", Code: "invalid_length", Message: "1-120 个字符"}})
		return
	}
	var before string
	if err := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT name FROM organizations WHERE id = $1`, id).Scan(&before); err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "组织不存在", nil)
		return
	}
	if _, err := s.deps.Store.Pool.Exec(r.Context(), `UPDATE organizations SET name = $2 WHERE id = $1`, id, name); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "更新组织失败", nil)
		return
	}
	s.writeAuditWithChange(r, "admin:org:manage", "admin.identity", "organization", id,
		map[string]any{"name": before}, map[string]any{"name": name}, map[string]any{"reason": body.Reason})
	respondOK(w, r, map[string]any{"updated": true, "name": name})
}

type workspaceRow struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	CreatedAt      string `json:"created_at"`
	CaseCount      int    `json:"case_count"`
	MemberCount    int    `json:"member_count"`
}

func (s *Server) handleAdminListWorkspaces(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	p := PrincipalFrom(r.Context())
	args := []any{}
	where := "1=1"
	if !s.isPlatformScope(r) {
		args = append(args, p.OrganizationID)
		where = "w.organization_id = $1"
	} else if orgID := strings.TrimSpace(r.URL.Query().Get("organization_id")); orgID != "" {
		args = append(args, orgID)
		where = "w.organization_id = $1"
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT w.id, w.organization_id, w.name, w.slug, w.created_at,
               (SELECT count(*) FROM cases c WHERE c.workspace_id = w.id),
               (SELECT count(*) FROM workspace_memberships m WHERE m.workspace_id = w.id AND m.status = 'active')
        FROM workspaces w WHERE `+where+` ORDER BY w.slug`, args...)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取工作区失败", nil)
		return
	}
	defer rows.Close()
	items := []workspaceRow{}
	for rows.Next() {
		var ws workspaceRow
		var createdAt any
		if err := rows.Scan(&ws.ID, &ws.OrganizationID, &ws.Name, &ws.Slug, &createdAt, &ws.CaseCount, &ws.MemberCount); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取工作区失败", nil)
			return
		}
		ws.CreatedAt = formatTime(createdAt)
		items = append(items, ws)
	}
	s.writeAuditNow(r, "admin:workspace:read", "admin.identity", "workspace", "", map[string]any{"result_count": len(items)})
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleAdminCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var body struct {
		OrganizationID string `json:"organization_id"`
		Name           string `json:"name"`
		Slug           string `json:"slug"`
		Confirm        bool   `json:"confirm"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "创建工作区需要二次确认", nil)
		return
	}
	p := PrincipalFrom(r.Context())
	orgID := strings.TrimSpace(body.OrganizationID)
	if orgID == "" {
		orgID = p.OrganizationID
	}
	name := strings.TrimSpace(body.Name)
	slug := strings.TrimSpace(body.Slug)
	if name == "" || slug == "" {
		validationError(w, r, "名称与 slug 都不能为空", nil)
		return
	}
	if !validSlug(slug) {
		validationError(w, r, "slug 只能包含小写字母、数字与连字符", []fieldError{{Field: "slug", Code: "invalid_format", Message: "小写字母/数字/连字符"}})
		return
	}
	if !s.isPlatformScope(r) && orgID != p.OrganizationID {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "组织不存在", nil)
		return
	}
	var count int
	if err := s.deps.Store.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM workspaces WHERE organization_id = $1 AND slug = $2`, orgID, slug).Scan(&count); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "创建失败", nil)
		return
	}
	if count > 0 {
		respondError(w, r, http.StatusConflict, "CONFLICT_DUPLICATE", "该组织下已存在同名 slug 的工作区", nil)
		return
	}
	var newID string
	if err := s.deps.Store.Pool.QueryRow(r.Context(),
		`INSERT INTO workspaces (organization_id, name, slug) VALUES ($1,$2,$3) RETURNING id`,
		orgID, name, slug).Scan(&newID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "创建失败", nil)
		return
	}
	// 创建者自动成为该工作区成员，否则新工作区对任何人都不可见。
	if p.IdentityID != "" {
		_, _ = s.deps.Store.Pool.Exec(r.Context(),
			`INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status)
             VALUES ($1,$2,$3,'active') ON CONFLICT (workspace_id, identity_id) DO NOTHING`,
			newID, orgID, p.IdentityID)
	}
	s.writeAuditNow(r, "admin:workspace:manage", "admin.identity", "workspace", newID,
		map[string]any{"organization_id": orgID, "slug": slug, "name": name})
	respondCreated(w, r, map[string]any{"id": newID, "slug": slug, "name": name})
}

func (s *Server) handleAdminUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "修改工作区需要二次确认", nil)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len([]rune(name)) > 120 {
		validationError(w, r, "工作区名称长度必须在 1-120 个字符之间", nil)
		return
	}
	p := PrincipalFrom(r.Context())
	var ownerOrg, before string
	if err := s.deps.Store.Pool.QueryRow(r.Context(),
		`SELECT organization_id, name FROM workspaces WHERE id = $1`, id).Scan(&ownerOrg, &before); err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "工作区不存在", nil)
		return
	}
	if !s.isPlatformScope(r) && ownerOrg != p.OrganizationID {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "工作区不存在", nil)
		return
	}
	if _, err := s.deps.Store.Pool.Exec(r.Context(), `UPDATE workspaces SET name = $2 WHERE id = $1`, id, name); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "更新失败", nil)
		return
	}
	s.writeAuditWithChange(r, "admin:workspace:manage", "admin.identity", "workspace", id,
		map[string]any{"name": before}, map[string]any{"name": name}, nil)
	respondOK(w, r, map[string]any{"updated": true, "name": name})
}

// ---- 成员与角色绑定 ----

func (s *Server) handleAdminListMembers(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	p := PrincipalFrom(r.Context())
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" || !s.isPlatformScope(r) {
		orgID = p.OrganizationID
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT m.identity_id, i.email, i.display_name, i.status, m.status, m.joined_at,
               COALESCE(string_agg(DISTINCT ro.key, ',' ORDER BY ro.key), '')
        FROM organization_memberships m
        JOIN identities i ON i.id = m.identity_id
        LEFT JOIN member_role_bindings b
               ON b.identity_id = m.identity_id AND b.revoked_at IS NULL
              AND (b.expires_at IS NULL OR b.expires_at > now())
        LEFT JOIN roles ro ON ro.id = b.role_id
        WHERE m.organization_id = $1
        GROUP BY m.identity_id, i.email, i.display_name, i.status, m.status, m.joined_at
        ORDER BY i.email`, orgID)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取成员失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var identityID, email, displayName, identityStatus, memberStatus, roles string
		var joinedAt any
		if err := rows.Scan(&identityID, &email, &displayName, &identityStatus, &memberStatus, &joinedAt, &roles); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取成员失败", nil)
			return
		}
		items = append(items, map[string]any{
			"identity_id": identityID, "email": email, "display_name": displayName,
			"identity_status": identityStatus, "membership_status": memberStatus,
			"joined_at": formatTime(joinedAt), "roles": splitCSV(roles),
		})
	}
	s.writeAuditNow(r, "admin:member:read", "admin.identity", "membership", orgID, map[string]any{"result_count": len(items)})
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleAdminUserRoles(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	identityID := chi.URLParam(r, "id")
	orgID, ok := s.resolveTargetOrg(w, r, identityID)
	if !ok {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
        SELECT b.id, ro.id, ro.key, ro.name, b.scope_type, COALESCE(b.scope_id::text,''),
               b.granted_at, b.expires_at
        FROM member_role_bindings b
        JOIN roles ro ON ro.id = b.role_id
        WHERE b.identity_id = $1 AND b.revoked_at IS NULL
          AND (b.expires_at IS NULL OR b.expires_at > now())
        ORDER BY ro.key`, identityID)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取角色绑定失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var bindingID, roleID, roleKey, roleName, scopeType, scopeID string
		var grantedAt, expiresAt any
		if err := rows.Scan(&bindingID, &roleID, &roleKey, &roleName, &scopeType, &scopeID, &grantedAt, &expiresAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取角色绑定失败", nil)
			return
		}
		items = append(items, map[string]any{
			"id": bindingID, "role_id": roleID, "role_key": roleKey, "role_name": roleName,
			"scope_type": scopeType, "scope_id": scopeID,
			"granted_at": formatTime(grantedAt), "expires_at": expiresAt,
		})
	}
	_ = orgID
	s.writeAuditNow(r, "admin:member:read", "admin.identity", "role_binding", identityID, map[string]any{"result_count": len(items)})
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleAdminGrantRole(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	identityID := chi.URLParam(r, "id")
	var body struct {
		RoleKey   string `json:"role_key"`
		ScopeType string `json:"scope_type"`
		ScopeID   string `json:"scope_id"`
		Confirm   bool   `json:"confirm"`
		Reason    string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "授予角色需要二次确认", nil)
		return
	}
	orgID, ok := s.resolveTargetOrg(w, r, identityID)
	if !ok {
		return
	}
	scopeType := strings.TrimSpace(body.ScopeType)
	if scopeType == "" {
		scopeType = "organization"
	}
	if scopeType != "organization" && scopeType != "workspace" {
		validationError(w, r, "scope_type 只允许 organization 或 workspace", []fieldError{{Field: "scope_type", Code: "invalid_value", Message: "organization|workspace"}})
		return
	}
	scopeID := strings.TrimSpace(body.ScopeID)
	if scopeID == "" {
		scopeID = orgID
	}
	if scopeType == "workspace" {
		var ownerOrg string
		if err := s.deps.Store.Pool.QueryRow(r.Context(),
			`SELECT organization_id FROM workspaces WHERE id = $1`, scopeID).Scan(&ownerOrg); err != nil || ownerOrg != orgID {
			validationError(w, r, "工作区不属于该组织", []fieldError{{Field: "scope_id", Code: "invalid_value", Message: "不属于目标组织"}})
			return
		}
	}

	var roleID, roleName string
	if err := s.deps.Store.Pool.QueryRow(r.Context(),
		`SELECT id, name FROM roles WHERE key = $1 AND (is_system OR organization_id = $2) LIMIT 1`,
		strings.TrimSpace(body.RoleKey), orgID).Scan(&roleID, &roleName); err != nil {
		respondError(w, r, http.StatusUnprocessableEntity, "VALIDATION_UNKNOWN_ROLE", "角色不存在", nil)
		return
	}

	p := PrincipalFrom(r.Context())
	var bindingID string
	err := s.deps.Store.Pool.QueryRow(r.Context(), `
        INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, granted_by, reason)
        VALUES ($1,$2,$3,$4,$5,$6)
        ON CONFLICT (identity_id, role_id, scope_type, COALESCE(scope_id, '00000000-0000-0000-0000-000000000000'::uuid))
            WHERE revoked_at IS NULL
        DO UPDATE SET granted_at = now(), reason = EXCLUDED.reason
        RETURNING id`,
		identityID, roleID, scopeType, scopeID, nullUUIDString(p.IdentityID), body.Reason).Scan(&bindingID)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "授予角色失败", nil)
		return
	}
	// 角色变化后必须立刻失效授权缓存，使撤销在数秒内生效（§27 RBAC-006）。
	s.policyEngineInvalidate(identityID)
	s.writeAuditNow(r, "admin:member:manage", "admin.identity", "role_binding", bindingID,
		map[string]any{"identity_id": identityID, "role": body.RoleKey, "scope_type": scopeType, "scope_id": scopeID, "reason": body.Reason})
	respondCreated(w, r, map[string]any{"id": bindingID, "role_key": body.RoleKey, "role_name": roleName})
}

func (s *Server) handleAdminRevokeRole(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	identityID := chi.URLParam(r, "id")
	bindingID := chi.URLParam(r, "binding")
	var body struct {
		Confirm bool   `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !body.Confirm {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_CONFIRMATION_REQUIRED", "撤销角色需要二次确认", nil)
		return
	}
	if _, ok := s.resolveTargetOrg(w, r, identityID); !ok {
		return
	}
	var roleKey, scopeType, scopeID string
	if err := s.deps.Store.Pool.QueryRow(r.Context(), `
        SELECT ro.key, b.scope_type, COALESCE(b.scope_id::text,'')
        FROM member_role_bindings b JOIN roles ro ON ro.id = b.role_id
        WHERE b.id = $1 AND b.identity_id = $2 AND b.revoked_at IS NULL`, bindingID, identityID).
		Scan(&roleKey, &scopeType, &scopeID); err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "角色绑定不存在", nil)
		return
	}
	// §27 RBAC-005：不允许移除最后一个 Owner。
	if roleKey == "owner" {
		owners, err := s.iam.Repo().CountActiveOwners(r.Context(), scopeID)
		if err == nil && owners <= 1 {
			respondError(w, r, http.StatusUnprocessableEntity, "CONFLICT_LAST_OWNER",
				"组织必须保留至少一名可用的 Owner", map[string]any{"current_owners": owners})
			return
		}
	}
	p := PrincipalFrom(r.Context())
	if _, err := s.deps.Store.Pool.Exec(r.Context(),
		`UPDATE member_role_bindings SET revoked_at = now(), reason = $3 WHERE id = $1 AND identity_id = $2`,
		bindingID, identityID, body.Reason); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "撤销角色失败", nil)
		return
	}
	s.policyEngineInvalidate(identityID)
	s.writeAuditNow(r, "admin:member:manage.revoke", "admin.identity", "role_binding", bindingID,
		map[string]any{"identity_id": identityID, "role": roleKey, "scope_type": scopeType, "reason": body.Reason, "actor_id": p.IdentityID})
	respondOK(w, r, map[string]any{"revoked": true})
}

// resolveTargetOrg 校验目标身份存在且在当前主体的可见范围内；不可见时返回 404。
func (s *Server) resolveTargetOrg(w http.ResponseWriter, r *http.Request, identityID string) (string, bool) {
	if _, err := uuid.Parse(identityID); err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "用户不存在", nil)
		return "", false
	}
	var orgID string
	if err := s.deps.Store.Pool.QueryRow(r.Context(),
		`SELECT organization_id::text FROM identities WHERE id = $1`, identityID).Scan(&orgID); err != nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "用户不存在", nil)
		return "", false
	}
	p := PrincipalFrom(r.Context())
	if !s.isPlatformScope(r) && orgID != p.OrganizationID {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "用户不存在", nil)
		return "", false
	}
	return orgID, true
}

func validSlug(slug string) bool {
	for _, c := range slug {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return false
	}
	return len(slug) > 0 && len(slug) <= 63
}

func nullUUIDString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func formatTime(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case interface{ Format(string) string }:
		return t.Format("2006-01-02T15:04:05.000Z07:00")
	default:
		return ""
	}
}
