package access

import (
	"os"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

func TestResourcesAreRegistered(t *testing.T) {
	for _, want := range []string{"case", "investigation", "canvas", "job", "export", "report"} {
		def, ok := Resources[want]
		if !ok {
			t.Fatalf("resource %q must be registered for tenant scope", want)
		}
		if def.table == "" || def.orgColumn == "" || def.wsColumn == "" {
			t.Fatalf("resource %q is registered without table/org/ws columns: %+v", want, def)
		}
	}
}

func TestScopeOfRejectsUnknownResource(t *testing.T) {
	r := NewResolver(nil)
	if _, err := r.ScopeOf(t.Context(), "definitely-not-a-resource", "x"); err == nil {
		t.Fatal("unregistered resource types must be rejected, not silently allowed")
	}
}

func TestScopedProducesTenantCondition(t *testing.T) {
	p := policy.Principal{Authenticated: true, OrganizationID: "org-1"}
	cond, args := Scoped("c", p, 3)
	if cond != "c.organization_id = $3" {
		t.Fatalf("unexpected condition: %q", cond)
	}
	if len(args) != 1 || args[0] != "org-1" {
		t.Fatalf("unexpected args: %v", args)
	}

	sys := policy.Principal{System: true}
	cond, args = Scoped("", sys, 1)
	if cond != "1=1" || args != nil {
		t.Fatalf("system principal should not be narrowed: %q %v", cond, args)
	}
}

// Require 对未认证主体的判定必须失败，且不能触发任何数据库访问。
func TestRequireRejectsUnauthenticatedWithoutDB(t *testing.T) {
	g := NewGuard(NewResolver(nil), 0)
	if _, err := g.Require(t.Context(), policy.Principal{}, "case", "abc"); err != ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if reason := g.Reason(t.Context(), policy.Principal{}, "case", "abc"); reason != ReasonUnauthenticated {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

// 回归：会话里的 workspace_id 只是 UI 默认值，绝不能授权。
// 该缺陷曾让"没有任何工作区绑定"的用户通过 workspaceAllowed 的短路判断。
func TestSessionWorkspaceIsNotAnAuthorizationInput(t *testing.T) {
	src, err := os.ReadFile("access.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "scope.WorkspaceID == p.WorkspaceID") {
		t.Fatal("workspaceAllowed 不得把会话默认工作区当作授权依据")
	}
}
