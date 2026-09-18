package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/blockchain-platform/blockchain-platform/internal/access"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// objectScoper 是对象级（"能不能碰这一个对象"）授权判定接口。
//
// 生产装配 access.Guard；单元测试可注入假实现，从而在不依赖真实数据库的前提下
// 验证"跨租户读单对象必须返回 404 而不是 403"这条安全契约。
type objectScoper interface {
	Require(ctx context.Context, p policy.Principal, resourceType, id string) (access.Scope, error)
	Reason(ctx context.Context, p policy.Principal, resourceType, id string) string
	Invalidate(identityID string)
}

// errNoTenantScope 表示无法为本次请求解析出可信的租户作用域（fail closed）。
var errNoTenantScope = errors.New("no trusted tenant scope for this request")

// tenantScope 是本次请求解析出的租户作用域。
//
// P0 背景（legacy /api/v1 无凭证越权读取）：旧兼容面的读接口大量调用
// Store.DefaultOrgWorkspace(ctx) 取"库里 slug=default 的第一个组织/工作区"，
// 与调用者身份完全无关。结果是两层缺陷叠加：
//
//  1. 路由层没有 require，未认证请求直接进入 handler；
//  2. handler 层就算加了 require，任何已登录用户仍然会读到**默认组织**的数据。
//
// 本类型是修复第 2 层缺陷的集中点：作用域只能来自已认证主体本身。
type tenantScope struct {
	OrganizationID string
	// WorkspaceID 为空表示主体只有组织级绑定，允许读取该组织下全部工作区；
	// 绝不能被解释为"读取默认工作区"。
	WorkspaceID string
	// System 为真表示迁移期系统主体（AuthMode=hybrid/legacy 的兼容头），
	// 其对象级归属仍由 access.Guard 的 System 分支放行，此处只做组织收窄。
	System bool
}

// tenantScope 解析本次请求的租户作用域。
//
// 规则（§4.2 / §6.3 / §6.4）：
//   - 未认证主体：直接失败，由路由层 s.require(...) 拦成 401；
//   - 已认证主体：只用主体自己的 OrganizationID / WorkspaceID；
//   - 系统主体：使用其显式绑定的组织；兼容头未提供组织时收敛到平台默认组织
//     （仍然是"一个具体组织"，不是"任意组织"），见 systemTenantScope。
//
// 该函数永远不会回退到"默认组织 + 默认工作区"来替代缺失的主体作用域。
func (s *Server) tenantScope(ctx context.Context) (tenantScope, error) {
	return s.scopeFromPrincipal(PrincipalFrom(ctx))
}

// scopeFromPrincipal 是 tenantScope 的纯函数内核，便于单元测试直接覆盖。
func (s *Server) scopeFromPrincipal(p policy.Principal) (tenantScope, error) {
	if !p.Authenticated {
		return tenantScope{}, errNoTenantScope
	}
	if p.System {
		return s.systemTenantScope(p)
	}
	if p.OrganizationID == "" {
		// 主体没有组织归属：宁可拒绝，也不要用默认组织兜底。
		return tenantScope{}, errNoTenantScope
	}
	return tenantScope{OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID}, nil
}

// systemTenantScope 解析迁移期系统主体（X-User-* 兼容头）的作用域。
//
// 该分支只在 AuthMode=hybrid/legacy 下可达（生产强制 session，见 bootstrap.go 的
// assertSessionMode）。兼容头不携带工作区，因此这里返回组织级作用域；
// 若兼容头连组织都没有，则收敛到平台默认组织——这是唯一允许触碰
// DefaultOrgWorkspace 的入口，且仍然只覆盖**一个**组织。
func (s *Server) systemTenantScope(p policy.Principal) (tenantScope, error) {
	if p.OrganizationID != "" {
		return tenantScope{OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID, System: true}, nil
	}
	if s.deps.Store == nil {
		return tenantScope{}, errNoTenantScope
	}
	orgID, wsID, err := s.deps.Store.DefaultOrgWorkspace(context.Background())
	if err != nil || orgID == "" {
		return tenantScope{}, errNoTenantScope
	}
	return tenantScope{OrganizationID: orgID, WorkspaceID: wsID, System: true}, nil
}

// tenantScopeOr401 解析作用域；失败时写 401 并返回 false。
//
// handler 用它替代原先的 Store.DefaultOrgWorkspace 调用：未认证请求在这里被
// 明确拒绝（正常情况下路由层的 require 已经先一步拦下，这里是第二道防线）。
func (s *Server) tenantScopeOr401(w http.ResponseWriter, r *http.Request) (tenantScope, bool) {
	scope, err := s.tenantScope(r.Context())
	if err != nil {
		respondError(w, r, http.StatusUnauthorized, "AUTHN_UNAUTHENTICATED", "需要登录", nil)
		return tenantScope{}, false
	}
	return scope, true
}

// writableWorkspace 解析写操作要落到的**具体**工作区。
//
// 解析顺序（P1-20260913-MEMBERSHIP，membership-aware）：
//
//  1. 会话已绑定工作区：直接使用。会话签发时已由 iam.ResolveLoginWorkspace
//     按 workspace_memberships 判定过，这里不重复查询。
//  2. 组织级**身份**主体（无会话工作区，但 Principal 带 IdentityID）：
//     只认该身份在该组织内的 active 成员资格。0 个 → fail closed；
//     >1 个 → 必须由客户端显式选择，同样 fail closed。
//     **绝不**任取组织内第一个工作区 —— 那正是"用组织默认绕过成员资格"。
//  3. 迁移期系统主体（兼容头 / 平台数据面，无身份可判定）：退回到
//     "该组织内的第一个工作区"，这是唯一允许的例外，且仍然限定在**一个具体组织**内，
//     不跨组织回退到 DefaultOrgWorkspace（那正是 P0 的成因）。
func (s *Server) writableWorkspace(ctx context.Context, scope tenantScope) (string, error) {
	if scope.WorkspaceID != "" {
		return scope.WorkspaceID, nil
	}
	if s.deps.Store == nil || s.deps.Store.Pool == nil {
		return "", errNoTenantScope
	}
	// memberOf：只认成员资格（iam.WorkspaceOptions + decideWorkspace）。
	memberOf := func(ctx context.Context, identityID, orgID string) (string, error) {
		if s.iam == nil {
			return "", errNoTenantScope
		}
		return s.iam.ResolveLoginWorkspace(ctx, identityID, orgID, "")
	}
	// orgFirst：迁移期系统主体的回退（无身份可判定）。
	orgFirst := func(ctx context.Context, orgID string) (string, error) {
		var wsID string
		if err := s.deps.Store.Pool.QueryRow(ctx,
			"SELECT id::text FROM workspaces WHERE organization_id=$1 ORDER BY created_at, id LIMIT 1",
			orgID).Scan(&wsID); err != nil || wsID == "" {
			return "", errNoTenantScope
		}
		return wsID, nil
	}
	return decideWritableWorkspace(ctx, scope, PrincipalFrom(ctx), memberOf, orgFirst)
}

// decideWritableWorkspace 是 writableWorkspace 的纯决策内核（无 IO，可单测）。
//
// 两个解析器由调用方注入：memberOf 只查 workspace_memberships，orgFirst 是
// 系统主体的例外回退。任何"身份存在但成员资格不唯一"的情况都必须 fail closed。
func decideWritableWorkspace(
	ctx context.Context,
	scope tenantScope,
	p policy.Principal,
	memberOf func(ctx context.Context, identityID, orgID string) (string, error),
	orgFirst func(ctx context.Context, orgID string) (string, error),
) (string, error) {
	if scope.WorkspaceID != "" {
		return scope.WorkspaceID, nil
	}
	// A migration-era SYSTEM principal is the only caller allowed to use the
	// organisation-wide fallback. "No identity" is NOT the same thing as
	// "system": an empty, unmarked principal must fail closed here as well, so
	// that a caller which forgot to go through scopeFromPrincipal cannot reach
	// the organisation default by accident (P1-20260914-MEMBERSHIP-HARDENING).
	if scope.System || p.System {
		if orgFirst == nil {
			return "", errNoTenantScope
		}
		return orgFirst(ctx, scope.OrganizationID)
	}
	if memberOf == nil {
		return "", errNoTenantScope
	}
	wsID, err := memberOf(ctx, p.IdentityID, scope.OrganizationID)
	if err != nil || wsID == "" {
		return "", errNoTenantScope
	}
	return wsID, nil
}

// systemScope 返回平台级主体（GPT Actions API Key、后台运维任务）的租户绑定。
//
// GPT Actions 走独立的 Bearer API Key 认证（见 actions.go），不产生 session
// Principal，因此不能复用 tenantScope。它的绑定是**平台数据面**：
// gpt_action_api_keys 没有 org/workspace 列，Actions 写入的下载/导出对象
// 归属于平台默认组织。这里把这个隐式约定收进一个显式函数，
// 避免在 Actions handler 里散落 DefaultOrgWorkspace 调用。
//
// 严禁在 session 认证的 handler 中调用本函数。
func (s *Server) systemScope(ctx context.Context) (tenantScope, error) {
	if s.deps.Store == nil {
		return tenantScope{}, errNoTenantScope
	}
	orgID, wsID, err := s.deps.Store.DefaultOrgWorkspace(ctx)
	if err != nil || orgID == "" {
		return tenantScope{}, errNoTenantScope
	}
	return tenantScope{OrganizationID: orgID, WorkspaceID: wsID, System: true}, nil
}

// requireObject 执行 §28.5 S3-BE-01 要求的"service 二次授权"：
// 路由级权限只回答"能不能做这类操作"，这里再回答"能不能碰这一个对象"。
//
// 越界统一返回 404（不返回 403），避免通过状态码差异枚举他人对象 ID；
// 同时写入一条 authorization.denied 审计，deny_reason 区分跨组织/跨工作区。
func (s *Server) requireObject(w http.ResponseWriter, r *http.Request, resourceType, id string) (access.Scope, bool) {
	if s.guard == nil {
		// 未装配守卫时拒绝，而不是放行（fail closed）。
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "租户守卫未初始化", nil)
		return access.Scope{}, false
	}
	p := PrincipalFrom(r.Context())
	scope, err := s.guard.Require(r.Context(), p, resourceType, id)
	switch {
	case err == nil:
		observability.RecordAuthzDecision("allow", "object")
		return scope, true
	case errors.Is(err, access.ErrUnauthenticated):
		respondError(w, r, http.StatusUnauthorized, "AUTHN_UNAUTHENTICATED", "需要登录", nil)
		return access.Scope{}, false
	case errors.Is(err, access.ErrNotFound):
		// 不区分"不存在"与"无权访问"：这是刻意的（§27 RBAC-002）。
		reason := s.guard.Reason(r.Context(), p, resourceType, id)
		observability.RecordAuthzDecision("deny", "object")
		observability.RecordTenantScopeDenied(resourceType, reason)
		s.auditObjectDeny(r, resourceType, id, reason)
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "对象不存在或无权访问", nil)
		return access.Scope{}, false
	default:
		s.deps.Log.Error("object scope check failed", "resource", resourceType, "id", id, "err", err)
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "授权检查失败", nil)
		return access.Scope{}, false
	}
}

// scopedObject 在 requireObject 通过后，把授权得到的 workspace 写回 Principal。
//
// S3 的 access.Guard 支持"工作区级绑定读取该工作区对象"，但 S4 casework 的
// SQL 仍以会话默认 workspace 过滤；RBAC-003 回归因此失败。这里统一把对象
// 授权 scope 注入本次请求的 Principal，后续二次查询即使用正确 workspace。
func (s *Server) scopedObject(w http.ResponseWriter, r *http.Request, resourceType, id string) bool {
	scope, ok := s.requireObject(w, r, resourceType, id)
	if !ok {
		return false
	}
	if scope.WorkspaceID == "" {
		return true
	}
	p := PrincipalFrom(r.Context())
	if p.WorkspaceID == scope.WorkspaceID {
		return true
	}
	p.WorkspaceID = scope.WorkspaceID
	*r = *r.WithContext(withPrincipal(r.Context(), p))
	return true
}

// auditObjectDeny 记录跨租户访问尝试（§6.6.1 "越权尝试与策略决策"）。
func (s *Server) auditObjectDeny(r *http.Request, resourceType, id, reason string) {
	if s.audit == nil {
		return
	}
	p := PrincipalFrom(r.Context())
	meta := metaFrom(r.Context())
	ev := auditEventFor(r, p, meta, "authorization.object_denied", "authorization", resourceType, id, "deny",
		map[string]any{"deny_reason": reason})
	ev.PolicyDecision = "deny"
	ev.DenyReason = reason
	ev.RiskLevel = "high"
	if err := s.audit.Write(r.Context(), ev); err != nil {
		s.deps.Log.Error("audit object deny failed", "err", err, "resource", resourceType, "id", id)
	}
	observability.RecordAuthzDenied("object_scope", reason)
}
