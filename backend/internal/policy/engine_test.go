package policy

import "testing"

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
