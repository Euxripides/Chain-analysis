package api

import (
	"errors"
	"net/http"

	"github.com/blockchain-platform/blockchain-platform/internal/access"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
)

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
// S3 的 access.Guard 支持“工作区级绑定读取该工作区对象”，但 S4 casework 的
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
