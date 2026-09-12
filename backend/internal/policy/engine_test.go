package policy

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestCatalogKeysAreNamespaced(t *testing.T) {
	for k, def := range Catalog {
		if k != def.Key {
			t.Errorf("catalog key %q does not match definition key %q", k, def.Key)
		}
		if len(k) == 0 || k[0] == ':' {
			t.Errorf("invalid permission key %q", k)
		}
		switch def.RiskLevel {
		case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		default:
			t.Errorf("permission %q has unknown risk level %q", k, def.RiskLevel)
		}
	}
	if len(Catalog) < 100 {
		t.Fatalf("catalog unexpectedly small: %d entries", len(Catalog))
	}
}

func TestAuthorizeMatrix(t *testing.T) {
	e := &Engine{}

	anonymous := Principal{}
	if d := e.Authorize(anonymous, "case:read"); d.Allowed || d.Reason != "unauthenticated" {
		t.Fatalf("anonymous must be rejected: %+v", d)
	}

	viewer := Principal{Authenticated: true, Audience: "user", Permissions: []string{"case:read", "report:read"}}
	if d := e.Authorize(viewer, "case:read"); !d.Allowed {
		t.Fatalf("viewer should read cases: %+v", d)
	}
	if d := e.Authorize(viewer, "case:delete"); d.Allowed || d.Reason != "missing_permission" {
		t.Fatalf("viewer must not delete cases: %+v", d)
	}

	// 用户层会话即使被授予 admin 权限也不得调用后台接口（audience 校验）。
	sneaky := Principal{Authenticated: true, Audience: "user", Permissions: []string{"admin:user:read"}}
	if d := e.Authorize(sneaky, "admin:user:read"); d.Allowed || d.Reason != "audience_mismatch" {
		t.Fatalf("user audience must not pass admin permission: %+v", d)
	}

	admin := Principal{Authenticated: true, Audience: "admin", Permissions: []string{"admin:user:read"}}
	if d := e.Authorize(admin, "admin:user:read"); !d.Allowed {
		t.Fatalf("admin session should pass: %+v", d)
	}

	// 未登记的权限一律拒绝（CI 中视为失败）。
	if d := e.Authorize(admin, "admin:made:up"); d.Allowed || d.Reason != "permission_not_declared" {
		t.Fatalf("undeclared permission must be denied: %+v", d)
	}

	sys := Principal{System: true, Audience: "user"}
	if d := e.Authorize(sys, "case:read"); !d.Allowed {
		t.Fatalf("system principal should pass: %+v", d)
	}
}

func TestAuthorizeAny(t *testing.T) {
	e := &Engine{}
	p := Principal{Authenticated: true, Audience: "user", Permissions: []string{"evidence:read"}}
	if d := e.AuthorizeAny(p, "evidence:sensitive:read", "evidence:read"); !d.Allowed || d.Permission != "evidence:read" {
		t.Fatalf("expected fallback permission to pass: %+v", d)
	}
}

func TestRiskAndSensitiveMetadata(t *testing.T) {
	if RiskOf("admin:killswitch:engage") != RiskCritical {
		t.Error("kill switch engage must be critical risk")
	}
	if !Sensitive("admin:audit:read") {
		t.Error("audit read must be flagged sensitive")
	}
	if RiskOf("unknown:permission") != RiskCritical {
		t.Error("unknown permission must fail closed as critical")
	}
	if !IsAdminPermission("admin:user:read") || IsAdminPermission("case:read") {
		t.Error("admin namespace detection is wrong")
	}
}

// 回归：Engine.Load 的 SQL 必须认可 workspace 作用域绑定。
// 曾经只匹配 scope_type='organization'，导致工作区级角色拿不到任何权限，
// 对象级授权因此永远走不到（返回 403 而不是 404）。
func TestLoadQueryAcceptsWorkspaceScopedBindings(t *testing.T) {
	src, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "b.scope_type = 'workspace'") {
		t.Fatal("Engine.Load 必须包含 workspace 作用域绑定，否则工作区级角色无法生效")
	}
	if strings.Contains(body, `AND ($2::uuid IS NULL OR (b.scope_type = 'organization' AND b.scope_id = $2::uuid))`) {
		t.Fatal("Engine.Load 仍在按组织作用域裁剪，会屏蔽工作区级绑定")
	}
}

// 回归：缓存键是 identityID|organizationID，Invalidate 必须按前缀清理。
// 该缺陷曾让撤销后的旧会话继续持有权限，直到 30 秒 TTL 到期（违反 RBAC-006）。
func TestInvalidateClearsAllOrganizationsForIdentity(t *testing.T) {
	e := NewEngine(nil, time.Minute)
	e.cache["user-1|org-a"] = cacheEntry{permissions: []string{"case:read"}}
	e.cache["user-1|org-b"] = cacheEntry{permissions: []string{"case:read"}}
	e.cache["user-2|org-a"] = cacheEntry{permissions: []string{"case:read"}}

	e.Invalidate("user-1")

	if _, ok := e.cache["user-1|org-a"]; ok {
		t.Error("org-a 的缓存未被清除，撤销无法在 5 秒内生效")
	}
	if _, ok := e.cache["user-1|org-b"]; ok {
		t.Error("org-b 的缓存未被清除")
	}
	if _, ok := e.cache["user-2|org-a"]; !ok {
		t.Error("不得误删其他身份的缓存")
	}
}

func TestInvalidateAllClearsEverything(t *testing.T) {
	e := NewEngine(nil, time.Minute)
	e.cache["a|o"] = cacheEntry{}
	e.cache["b|o"] = cacheEntry{}
	e.InvalidateAll()
	if len(e.cache) != 0 {
		t.Fatalf("InvalidateAll 未清空缓存：%d 条残留", len(e.cache))
	}
}
