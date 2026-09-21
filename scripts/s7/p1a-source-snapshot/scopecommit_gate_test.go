package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// scopeCommitGateStubScope is the unit-test stand-in for the live session
// lookup: its presence switches the gate off, exactly like the guard seam.
type scopeCommitGateStubScope struct{}

func (scopeCommitGateStubScope) LiveSessionWorkspace(ctx context.Context, sessionID string) (string, bool, error) {
	return "", false, nil
}

// The commit gate is only as complete as its two decision inputs: which requests
// are session-scoped state-changing requests, and which permissions are excluded
// because their handler mutates the caller own session row. Both are asserted
// here; the end-to-end behaviour is driven by scripts/s7/c1-scope-commit-gate-v3.sh
// against a real API and a real PostgreSQL.
func TestScopeCommitGateAppliesOnlyToSessionScopedWrites(t *testing.T) {
	sessionPrincipal := policy.Principal{Authenticated: true, SessionID: "s-1", WorkspaceID: "w-1"}
	systemPrincipal := policy.Principal{Authenticated: true, System: true, SessionID: "s-1", WorkspaceID: "w-1"}
	orgPrincipal := policy.Principal{Authenticated: true, SessionID: "s-1", WorkspaceID: ""}

	cases := []struct {
		name       string
		method     string
		principal  policy.Principal
		permission string
		want       bool
	}{
		{"session write takes the gate", http.MethodPost, sessionPrincipal, "case:create", true},
		{"session patch takes the gate", http.MethodPatch, sessionPrincipal, "case:update", true},
		{"session delete takes the gate", http.MethodDelete, sessionPrincipal, "case:delete", true},
		{"safe GET never takes the gate", http.MethodGet, sessionPrincipal, "case:read", false},
		{"HEAD never takes the gate", http.MethodHead, sessionPrincipal, "case:read", false},
		{"system principal never takes the gate", http.MethodPost, systemPrincipal, "case:create", false},
		{"organisation principal without a session workspace never takes the gate", http.MethodPost, orgPrincipal, "case:create", false},
		{"excluded permission never takes the gate", http.MethodPost, sessionPrincipal, "admin:session:revoke", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/v2/cases", nil)
			if got := scopeCommitGateApplies(r, tc.principal, tc.permission); got != tc.want {
				t.Fatalf("scopeCommitGateApplies(%s, perm=%s) = %v, want %v", tc.method, tc.permission, got, tc.want)
			}
		})
	}
}

// TestScopeCommitGateExclusionListIsEnumerated pins the exclusion list. Adding a
// permission here silently weakens the gate, so the list is asserted explicitly
// and every entry must carry the reason it is excluded. The coverage report
// scripts/s7/scope_commit_coverage.py prints the same list for the same reason.
func TestScopeCommitGateExclusionListIsEnumerated(t *testing.T) {
	want := map[string]bool{"admin:session:revoke": true}
	if len(scopeCommitGateExcludedPermissions) != len(want) {
		t.Fatalf("exclusion list size = %d, want %d: %v", len(scopeCommitGateExcludedPermissions), len(want), scopeCommitGateExcludedPermissions)
	}
	for perm := range want {
		reason, ok := scopeCommitGateExcludedPermissions[perm]
		if !ok {
			t.Fatalf("permission %q must be excluded from the commit gate", perm)
		}
		if len(reason) < 20 {
			t.Fatalf("exclusion %q must document why it is excluded, got %q", perm, reason)
		}
	}
}

// TestScopeCommitGateSkipsTheUnitTestStubSeam makes the only bypass explicit: a
// Server with an injected sessionScope stub (unit tests) has no pool and must not
// try to open one.
func TestScopeCommitGateSkipsTheUnitTestStubSeam(t *testing.T) {
	s := &Server{sessionScope: scopeCommitGateStubScope{}}
	r := httptest.NewRequest(http.MethodPost, "/api/v2/cases", nil)
	release, ok := s.enterScopeCommitGate(httptest.NewRecorder(), r,
		policy.Principal{Authenticated: true, SessionID: "s-1", WorkspaceID: "w-1"}, "case:create")
	if !ok {
		t.Fatal("the stub seam must not refuse the request")
	}
	if release == nil {
		t.Fatal("a no-op release function must be returned")
	}
	release()
}
