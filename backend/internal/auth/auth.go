// Package auth 是 docs/dual-system-development-plan.md §28.2 迁移映射中的兼容层。
//
// 历史行为：解析客户端自报的 X-User-* 头并注入上下文（可伪造）。
// 现状：身份只能由服务端会话（internal/iam）确定。X-User-* 仅在
// AuthMode=hybrid 且请求未携带会话时作为迁移期兼容路径存在；
// 生产（AuthMode=session）一律忽略（§6.3 / §28.4 验收）。
package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey struct{}

// Mode 决定身份来源。
type Mode string

const (
	// ModeSession 只接受服务端会话。生产环境唯一允许的模式。
	ModeSession Mode = "session"
	// ModeHybrid 会话优先，无会话时允许 X-User-* 兼容头。仅限测试环境。
	ModeHybrid Mode = "hybrid"
	// ModeLegacy 完全依赖 X-User-* 头。仅用于迁移前旧行为回归对照。
	ModeLegacy Mode = "legacy"
)

// ParseMode 解析环境变量，未配置或非法值一律回退到最严格的 session。
func ParseMode(v string) Mode {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "legacy":
		return ModeLegacy
	case "hybrid":
		return ModeHybrid
	default:
		return ModeSession
	}
}

// TrustsHeaders 表示该模式是否允许客户端自报身份。
func (m Mode) TrustsHeaders() bool { return m == ModeLegacy || m == ModeHybrid }

type User struct {
	ID             string   `json:"id"`
	Email          string   `json:"email"`
	DisplayName    string   `json:"display_name,omitempty"`
	OrganizationID string   `json:"organization_id"`
	WorkspaceID    string   `json:"workspace_id,omitempty"`
	Role           string   `json:"role"`
	Roles          []string `json:"roles,omitempty"`
	Source         string   `json:"source"`
	SessionID      string   `json:"session_id,omitempty"`
	Audience       string   `json:"audience,omitempty"`
}

// Roles 保留历史角色名列表用于对外文档兼容。
var Roles = []string{"owner", "platform_admin", "security_admin", "auditor", "investigator", "analyst", "reviewer", "viewer", "api_client"}

// Anonymous 是没有有效身份时的上下文用户。
func Anonymous() *User {
	return &User{Source: "anonymous"}
}

// Lookup 返回上下文中已认证的用户；未认证时第二个返回值为 false。
func Lookup(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*User)
	if !ok || u == nil || u.ID == "" {
		return Anonymous(), false
	}
	return u, true
}

// FromContext 保持历史调用点可用：未认证时返回匿名用户而不是默认管理员。
// 需要严格判断时请使用 Lookup。
func FromContext(ctx context.Context) *User {
	u, _ := Lookup(ctx)
	return u
}

func WithUser(ctx context.Context, u *User) context.Context {
	if u == nil {
		u = Anonymous()
	}
	return context.WithValue(ctx, ctxKey{}, u)
}

func IsAuthenticated(ctx context.Context) bool {
	_, ok := Lookup(ctx)
	return ok
}

// IsSessionIdentity 表示身份来自服务端会话（而非兼容头）。
func IsSessionIdentity(ctx context.Context) bool {
	u, ok := Lookup(ctx)
	return ok && u.Source == "session"
}

// HeaderIdentity 只在 ModeTrustsHeaders() 为真时由 api 层调用。
// 该函数本身不校验模式，调用方必须保证前置条件。
func HeaderIdentity(r *http.Request) *User {
	u := &User{Source: "legacy_header"}
	if v := strings.TrimSpace(r.Header.Get("X-User-Id")); v != "" {
		u.ID = v
	}
	if v := strings.TrimSpace(r.Header.Get("X-User-Email")); v != "" {
		u.Email = v
	}
	if v := strings.TrimSpace(r.Header.Get("X-Org-Id")); v != "" {
		u.OrganizationID = v
	}
	if v := strings.TrimSpace(r.Header.Get("X-User-Role")); v != "" {
		u.Role = v
		u.Roles = []string{v}
	}
	return u
}
