// Package auth：API 鉴权接口预留（文档 §72）。
// V1 单用户：中间件解析 X-User-* 头并注入上下文，不强制鉴权；
// 企业版在此扩展 JWT/OIDC/RBAC 即可，Endpoint 无需重写。
package auth

import (
	"context"
	"net/http"
)

type ctxKey struct{}

type User struct {
	ID             string `json:"id"`
	Email          string `json:"email"`
	OrganizationID string `json:"organization_id"`
	Role           string `json:"role"`
}

// Roles 预留角色体系（§72）
var Roles = []string{"owner", "admin", "analyst", "investigator", "viewer", "api_client"}

// DefaultUser V1 单用户（对应迁移中的 admin@local）。
func DefaultUser() *User {
	return &User{ID: "", Email: "admin@local", OrganizationID: "default", Role: "owner"}
}

func FromContext(ctx context.Context) *User {
	if u, ok := ctx.Value(ctxKey{}).(*User); ok && u != nil {
		return u
	}
	return DefaultUser()
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := DefaultUser()
		if v := r.Header.Get("X-User-Id"); v != "" {
			u.ID = v
		}
		if v := r.Header.Get("X-User-Email"); v != "" {
			u.Email = v
		}
		if v := r.Header.Get("X-Org-Id"); v != "" {
			u.OrganizationID = v
		}
		if v := r.Header.Get("X-User-Role"); v != "" {
			u.Role = v
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}
