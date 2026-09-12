// Package access 实现 §4.2 的对象级二次授权（S3-BE-01）。
//
// 路由级 `s.require(permission, ...)` 只能回答"这个身份能不能做这类操作"，
// 不能回答"这个身份能不能碰这一个对象"。本包补上后半句：
//
//	1. 按资源类型与 ID 读取对象的 organization_id / workspace_id；
//	2. 校验对象租户是否落在主体的作用域内（organization / workspace 两种绑定）；
//	3. 越界时不返回 403 而是返回"不存在"，避免通过错误码枚举他人对象（§27 RBAC-002）。
//
// 关键在于**调用点必须在读取对象之前**执行，且查询本身也要带作用域条件，
// 两道防线任何一道失效都不会造成跨租户读取。
package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

var (
	// ErrNotFound 同时表示"不存在"与"存在但不在你的作用域内"。
	ErrNotFound = errors.New("resource not found")
	// ErrUnauthenticated 表示调用方没有已认证主体。
	ErrUnauthenticated = errors.New("unauthenticated")
)

// Scope 是对象的租户归属。
type Scope struct {
	OrganizationID string
	WorkspaceID    string
	CaseID         string
}

// resource 描述一张受租户约束的表。
type resource struct {
	table      string
	orgColumn  string
	wsColumn   string
	caseColumn string
}

// Resources 是受作用域保护的资源白名单。
// 新增受保护资源必须在此登记，未登记的类型一律拒绝（fail closed）。
var Resources = map[string]resource{
	"case":          {table: "cases", orgColumn: "organization_id", wsColumn: "workspace_id"},
	"investigation": {table: "investigations", orgColumn: "organization_id", wsColumn: "workspace_id", caseColumn: "case_id"},
	"canvas":        {table: "investigation_canvases", orgColumn: "organization_id", wsColumn: "workspace_id", caseColumn: "case_id"},
	"job":           {table: "download_jobs", orgColumn: "organization_id", wsColumn: "workspace_id"},
	"export":        {table: "exports", orgColumn: "organization_id", wsColumn: "workspace_id"},
	"report":        {table: "investigation_reports", orgColumn: "organization_id", wsColumn: "workspace_id", caseColumn: "case_id"},
}

// Resolver 读取对象归属。查询全部使用参数化 SQL，不接受拼接的表名/列名以外的输入。
type Resolver struct {
	pool *pgxpool.Pool
}

func NewResolver(pool *pgxpool.Pool) *Resolver { return &Resolver{pool: pool} }

// ScopeOf 返回对象的作用域；对象不存在时返回 ErrNotFound。
func (r *Resolver) ScopeOf(ctx context.Context, resourceType, id string) (Scope, error) {
	if r == nil || r.pool == nil {
		return Scope{}, ErrNotFound
	}
	def, ok := Resources[resourceType]
	if !ok {
		return Scope{}, fmt.Errorf("resource type %q is not registered for tenant scope", resourceType)
	}
	if strings.TrimSpace(id) == "" {
		return Scope{}, ErrNotFound
	}
	cols := fmt.Sprintf("COALESCE(%s::text,''), COALESCE(%s::text,'')", def.orgColumn, def.wsColumn)
	if def.caseColumn != "" {
		cols += fmt.Sprintf(", COALESCE(%s::text,'')", def.caseColumn)
	} else {
		cols += ", ''"
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE id = $1", cols, def.table)

	var s Scope
	err := r.pool.QueryRow(ctx, query, id).Scan(&s.OrganizationID, &s.WorkspaceID, &s.CaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Scope{}, ErrNotFound
	}
	if err != nil {
		return Scope{}, err
	}
	return s, nil
}

// ScopeOfOrg 返回组织是否存在（用于组织级管理操作）。
func (r *Resolver) ScopeOfOrg(ctx context.Context, organizationID string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `SELECT id::text FROM organizations WHERE id = $1`, organizationID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// bindingCache 缓存"该身份在本组织的作用域绑定形态"，避免每个请求都查库。
type bindingCache struct {
	orgWide  bool
	wsScopes map[string]struct{}
	expires  time.Time
}

// Guard 是对象级授权判定器。
type Guard struct {
	resolver *Resolver
	ttl      time.Duration

	mu    sync.RWMutex
	cache map[string]bindingCache
}

func NewGuard(resolver *Resolver, ttl time.Duration) *Guard {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Guard{resolver: resolver, ttl: ttl, cache: map[string]bindingCache{}}
}

// Invalidate 在角色绑定变化后清除缓存（与 policy.Engine 一起调用）。
func (g *Guard) Invalidate(identityID string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	for k := range g.cache {
		if strings.HasPrefix(k, identityID+"|") {
			delete(g.cache, k)
		}
	}
	g.mu.Unlock()
}

// DenyReason 是拒绝原因码，写入审计的 deny_reason 字段（§6.6.3）。
const (
	ReasonUnauthenticated = "unauthenticated"
	ReasonNotFound        = "object_not_found"
	ReasonCrossOrg        = "cross_organization"
	ReasonCrossWorkspace  = "cross_workspace"
)

// Require 校验主体是否有权访问该对象。
//
// 返回 ErrNotFound 表达两种语义（不存在 / 不属于你），调用方统一映射为 404，
// 以避免通过响应差异枚举他人对象 ID。
func (g *Guard) Require(ctx context.Context, p policy.Principal, resourceType, id string) (Scope, error) {
	if p.System {
		return g.resolver.ScopeOf(ctx, resourceType, id)
	}
	if !p.Authenticated {
		return Scope{}, ErrUnauthenticated
	}
	scope, err := g.resolver.ScopeOf(ctx, resourceType, id)
	if err != nil {
		return Scope{}, err
	}
	if scope.OrganizationID == "" {
		// 数据异常：对象没有租户归属。拒绝访问并让上层记录，不要静默放行。
		return Scope{}, fmt.Errorf("%s %s has no organization scope", resourceType, id)
	}
	if p.OrganizationID == "" || p.OrganizationID != scope.OrganizationID {
		return Scope{}, ErrNotFound
	}
	if !g.workspaceAllowed(ctx, p, scope) {
		return Scope{}, ErrNotFound
	}
	return scope, nil
}

// Reason 给出拒绝原因，供审计使用（调用方在判定失败后再调用一次即可）。
func (g *Guard) Reason(ctx context.Context, p policy.Principal, resourceType, id string) string {
	if !p.Authenticated {
		return ReasonUnauthenticated
	}
	scope, err := g.resolver.ScopeOf(ctx, resourceType, id)
	if err != nil {
		return ReasonNotFound
	}
	if p.OrganizationID != scope.OrganizationID {
		return ReasonCrossOrg
	}
	if !g.workspaceAllowed(ctx, p, scope) {
		return ReasonCrossWorkspace
	}
	return ""
}

// workspaceAllowed 实现 §4.2 的作用域规则：
//   - 主体在本组织拥有 organization 级绑定 → 可访问组织内全部工作区；
//   - 仅拥有 workspace 级绑定时 → 只能访问被绑定的工作区。
func (g *Guard) workspaceAllowed(ctx context.Context, p policy.Principal, scope Scope) bool {
	if scope.WorkspaceID == "" {
		return true
	}
	// 注意：不能把 p.WorkspaceID（会话的默认工作区）当作授权依据。
	// 它只是 UI 默认值，若用于授权，任何用户只要改一下默认工作区就能越权。
	// 工作区级访问必须来自实际的角色绑定。
	orgWide, wsScopes, err := g.bindings(ctx, p.IdentityID, p.OrganizationID)
	if err != nil {
		return false // 读不到绑定就拒绝（fail closed）
	}
	if orgWide {
		return true
	}
	_, ok := wsScopes[scope.WorkspaceID]
	return ok
}

func (g *Guard) bindings(ctx context.Context, identityID, organizationID string) (bool, map[string]struct{}, error) {
	key := identityID + "|" + organizationID
	g.mu.RLock()
	if c, ok := g.cache[key]; ok && time.Now().Before(c.expires) {
		g.mu.RUnlock()
		return c.orgWide, c.wsScopes, nil
	}
	g.mu.RUnlock()

	rows, err := g.resolver.pool.Query(ctx, `
        SELECT scope_type, COALESCE(scope_id::text, '')
        FROM member_role_bindings
        WHERE identity_id = $1 AND revoked_at IS NULL
          AND (expires_at IS NULL OR expires_at > now())`, identityID)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()
	orgWide := false
	wsScopes := map[string]struct{}{}
	for rows.Next() {
		var scopeType, scopeID string
		if err := rows.Scan(&scopeType, &scopeID); err != nil {
			return false, nil, err
		}
		switch scopeType {
		case "organization":
			if scopeID == organizationID {
				orgWide = true
			}
		case "workspace":
			wsScopes[scopeID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return false, nil, err
	}

	g.mu.Lock()
	g.cache[key] = bindingCache{orgWide: orgWide, wsScopes: wsScopes, expires: time.Now().Add(g.ttl)}
	g.mu.Unlock()
	return orgWide, wsScopes, nil
}

// Scoped 返回可直接拼进 SQL 的强制作用域条件片段与参数。
//
// 目标：列表查询不允许"先全表查再在内存里过滤"，必须在 SQL 层就限定租户。
//
//	cond, args := access.Scoped("c", p, 1)   // 1 为下一个占位符序号
//	pool.Query("SELECT ... FROM cases c WHERE "+cond, args...)
func Scoped(alias string, p policy.Principal, nextParam int) (string, []any) {
	if p.System {
		return "1=1", nil
	}
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return fmt.Sprintf("%sorganization_id = $%d", prefix, nextParam), []any{p.OrganizationID}
}
