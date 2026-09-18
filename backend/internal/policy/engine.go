package policy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
)

// Principal 是授权计算的主体视图。它只包含服务端解析出的身份信息，
// 绝不接受客户端自报字段（§6.3）。
type Principal struct {
	IdentityID     string   `json:"identity_id"`
	Email          string   `json:"email"`
	DisplayName    string   `json:"display_name"`
	OrganizationID string   `json:"organization_id"`
	WorkspaceID    string   `json:"workspace_id,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	DeviceID       string   `json:"device_id,omitempty"`
	Audience       string   `json:"audience"`
	Roles          []string `json:"roles"`
	Permissions    []string `json:"permissions"`
	Authenticated  bool     `json:"authenticated"`
	System         bool     `json:"system"`
	// MustChangePassword mirrors identities.must_change_password for the authenticated
	// identity. It travels with the session row (iam.SessionManager.loadByToken already
	// selects it), so the API middleware can fail closed on business surfaces without an
	// extra database round trip. It is never taken from the client.
	MustChangePassword bool `json:"must_change_password,omitempty"`
}

// Has 判断是否具备某权限（仅 RBAC 部分；对象级条件见 Engine.Authorize）。
func (p Principal) Has(permission string) bool {
	for _, k := range p.Permissions {
		if k == permission {
			return true
		}
	}
	return false
}

// HasAny 判断是否具备任一权限。
func (p Principal) HasAny(permissions ...string) bool {
	for _, k := range permissions {
		if p.Has(k) {
			return true
		}
	}
	return false
}

// CacheEntry 是按身份缓存的权限集合。
type cacheEntry struct {
	permissions []string
	roles       []string
	expiresAt   time.Time
}

// Engine 加载并缓存主体权限，提供统一的 Authorize 判定。
type Engine struct {
	pool *dbtx.Pool
	ttl  time.Duration

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

func NewEngine(pool *dbtx.Pool, ttl time.Duration) *Engine {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Engine{pool: pool, ttl: ttl, cache: map[string]cacheEntry{}}
}

// Invalidate 在角色、成员关系或账号状态变化后清除缓存，使撤销在数秒内生效。
//
// 注意：缓存键是 "identityID|organizationID"，因此必须按前缀删除。
// 曾错误地写成 delete(e.cache, identityID)，实际什么也没删掉，
// 导致角色变更要等 TTL（30 秒）才生效，违反 §27 RBAC-006 的"5 秒内失效"。
func (e *Engine) Invalidate(identityID string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	for k := range e.cache {
		if k == identityID || strings.HasPrefix(k, identityID+"|") {
			delete(e.cache, k)
		}
	}
	e.mu.Unlock()
}

// InvalidateAll 用于批量变更（如角色权限编辑）。
func (e *Engine) InvalidateAll() {
	e.mu.Lock()
	e.cache = map[string]cacheEntry{}
	e.mu.Unlock()
}

// Load 读取身份在当前组织的生效权限。跨组织必须显式传入 organizationID，
// 避免"前端可提交任意 tenant ID"（§6.4）。
func (e *Engine) Load(ctx context.Context, identityID, organizationID string) ([]string, []string, error) {
	if e == nil || e.pool == nil || identityID == "" {
		return nil, nil, nil
	}
	cacheKey := identityID + "|" + organizationID
	e.mu.RLock()
	if entry, ok := e.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		e.mu.RUnlock()
		return entry.permissions, entry.roles, nil
	}
	e.mu.RUnlock()

	// §4.2：作用域可以是 organization 或 workspace。
	//
	// 这里刻意**不**按工作区过滤，而是返回"该身份在本组织内被授予的全部动作"，
	// 原因是有意的两层分工：
	//   - policy.Engine 回答"能不能做这类操作"（动作级）；
	//   - access.Guard  回答"能不能碰这一个对象"（对象级，按工作区绑定收窄）。
	// 若在这一层就按工作区裁剪，工作区级绑定会连动作权限都拿不到，
	// 结果是 403 而不是对象级的 404，既不可用也泄露了"对象存在但无权"的差异。
	rows, err := e.pool.Query(ctx, `
        SELECT DISTINCT rp.permission_key, ro.key
        FROM member_role_bindings b
        JOIN roles ro ON ro.id = b.role_id
        JOIN role_permissions rp ON rp.role_id = ro.id
        WHERE b.identity_id = $1
          AND b.revoked_at IS NULL
          AND (b.expires_at IS NULL OR b.expires_at > now())
          AND EXISTS (
              SELECT 1 FROM organization_memberships m
              WHERE m.identity_id = b.identity_id AND m.organization_id = $2::uuid AND m.status = 'active'
          )
          AND (
              b.scope_type = 'self'
              OR (b.scope_type = 'organization' AND b.scope_id = $2::uuid)
              OR (b.scope_type = 'workspace' AND b.scope_id IN (
                      SELECT w.id FROM workspaces w WHERE w.organization_id = $2::uuid))
              OR (b.scope_type = 'case' AND b.scope_id IN (
                      SELECT c.id FROM cases c WHERE c.organization_id = $2::uuid))
          )`, identityID, nullUUID(organizationID))
	if err != nil {
		return nil, nil, fmt.Errorf("load permissions: %w", err)
	}
	defer rows.Close()
	permSet := map[string]struct{}{}
	roleSet := map[string]struct{}{}
	for rows.Next() {
		var perm, role string
		if err := rows.Scan(&perm, &role); err != nil {
			return nil, nil, err
		}
		permSet[perm] = struct{}{}
		roleSet[role] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	perms := make([]string, 0, len(permSet))
	for k := range permSet {
		perms = append(perms, k)
	}
	sort.Strings(perms)
	roles := make([]string, 0, len(roleSet))
	for k := range roleSet {
		roles = append(roles, k)
	}
	sort.Strings(roles)

	e.mu.Lock()
	e.cache[cacheKey] = cacheEntry{permissions: perms, roles: roles, expiresAt: time.Now().Add(e.ttl)}
	e.mu.Unlock()
	return perms, roles, nil
}

// Decision 是授权结果。Reason 用于审计 deny_reason（§6.6.3）。
type Decision struct {
	Allowed    bool   `json:"allowed"`
	Permission string `json:"permission"`
	Reason     string `json:"reason"`
	RiskLevel  string `json:"risk_level"`
	Sensitive  bool   `json:"sensitive"`
}

// Authorize 执行 RBAC 判定。未在 Catalog 登记的权限一律拒绝（CI 中视为失败）。
func (e *Engine) Authorize(p Principal, permission string) Decision {
	def, known := Lookup(permission)
	d := Decision{Permission: permission, RiskLevel: RiskOf(permission), Sensitive: Sensitive(permission)}
	if !known {
		d.Reason = "permission_not_declared"
		return d
	}
	if p.System {
		d.Allowed = true
		return d
	}
	if !p.Authenticated {
		d.Reason = "unauthenticated"
		return d
	}
	if !p.Has(permission) {
		d.Reason = "missing_permission"
		return d
	}
	// 后台权限必须是 admin 会话；用户层会话不能调用后台 API（§3.3）。
	if IsAdminPermission(def.Key) && p.Audience != "admin" {
		d.Reason = "audience_mismatch"
		return d
	}
	d.Allowed = true
	return d
}

// AuthorizeAny 用于一个端点接受多个权限之一的场景（例如"本人可读"）。
func (e *Engine) AuthorizeAny(p Principal, permissions ...string) Decision {
	var last Decision
	for _, perm := range permissions {
		last = e.Authorize(p, perm)
		if last.Allowed {
			return last
		}
	}
	if last.Permission == "" {
		last = Decision{Reason: "no_permission_candidates", RiskLevel: RiskCritical}
	}
	return last
}

// LoadPrincipal 把身份与会话信息整理为可授权的 Principal。
func (e *Engine) LoadPrincipal(ctx context.Context, identityID, organizationID, workspaceID, sessionID, deviceID, audience string) (Principal, error) {
	perms, roles, err := e.Load(ctx, identityID, organizationID)
	if err != nil {
		return Principal{}, err
	}
	return Principal{
		IdentityID: identityID, OrganizationID: organizationID, WorkspaceID: workspaceID,
		SessionID: sessionID, DeviceID: deviceID, Audience: audience,
		Roles: roles, Permissions: perms, Authenticated: true,
	}, nil
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
