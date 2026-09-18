//go:build live

// Real-PostgreSQL integration test for the case-write workspace resolution
// (P1-20260914-CASEWORK-MEMBERSHIP). Before this round the case-write path
// silently fell back to the organisation default / first workspace whenever the
// principal carried no workspace, which is the same class of defect as the
// P0 cross-tenant read that was fixed in api.objectaccess.
package casework

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func liveCasework(t *testing.T) (*Service, *store.Store, context.Context) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("C1MEM_LIVE_DSN"))
	if dsn == "" {
		t.Skip("C1MEM_LIVE_DSN not set")
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(st.Close)
	return &Service{pool: st.Pool}, st, ctx
}

func envRequiredCW(t *testing.T, key string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		t.Skipf("%s not set", key)
	}
	return v
}

func identityIDCW(t *testing.T, ctx context.Context, st *store.Store, email string) string {
	t.Helper()
	var id string
	if err := st.Pool.QueryRow(ctx, "SELECT id::text FROM identities WHERE lower(email)=lower($1)", email).Scan(&id); err != nil {
		t.Fatalf("identity lookup %s: %v", email, err)
	}
	return id
}

func TestResolveWorkspaceLiveMembershipClasses(t *testing.T) {
	svc, st, ctx := liveCasework(t)
	org := envRequiredCW(t, "C1MEM_ORG_ID")
	single := identityIDCW(t, ctx, st, envRequiredCW(t, "C1MEM_SINGLE_EMAIL"))
	nomem := identityIDCW(t, ctx, st, envRequiredCW(t, "C1MEM_NOMEM_EMAIL"))
	multi := identityIDCW(t, ctx, st, envRequiredCW(t, "C1MEM_MULTI_EMAIL"))
	wsA := envRequiredCW(t, "C1MEM_WS_A")
	wsB := envRequiredCW(t, "C1MEM_WS_B")

	// A single-member identity writing with its session workspace succeeds.
	got, err := svc.resolveWorkspace(ctx, st.Pool, policy.Principal{IdentityID: single, OrganizationID: org, WorkspaceID: wsA, Authenticated: true})
	if err != nil || got != wsA {
		t.Fatalf("single member must write into its own workspace: got %q err=%v", got, err)
	}

	// The same identity pointed at a workspace it is not a member of is refused,
	// even though that workspace belongs to the same organisation.
	// (c1mem-multi holds both; the single-member fixture holds wsA only.)
	if _, err := svc.resolveWorkspace(ctx, st.Pool, policy.Principal{IdentityID: multi, OrganizationID: org, WorkspaceID: wsB, Authenticated: true}); err != nil {
		t.Fatalf("multi member must resolve its second workspace: %v", err)
	}
	if _, err := svc.resolveWorkspace(ctx, st.Pool, policy.Principal{IdentityID: multi, OrganizationID: org, WorkspaceID: "00000000-0000-0000-0000-000000000000", Authenticated: true}); err == nil {
		t.Fatal("a workspace outside the organisation must be refused")
	}

	// Zero memberships and no session workspace: fail closed, never the org default.
	if ws, err := svc.resolveWorkspace(ctx, st.Pool, policy.Principal{IdentityID: nomem, OrganizationID: org, Authenticated: true}); err == nil {
		t.Fatalf("identity with no membership and no workspace resolved %q; expected fail closed", ws)
	}
	// Even a workspace-bearing principal without an active membership is refused.
	if ws, err := svc.resolveWorkspace(ctx, st.Pool, policy.Principal{IdentityID: nomem, OrganizationID: org, WorkspaceID: wsA, Authenticated: true}); err == nil {
		t.Fatalf("non-member resolved %q; expected fail closed", ws)
	}

	// Only the migration-era system principal may use the org-wide fallback.
	var orgFirst string
	if err := st.Pool.QueryRow(ctx, "SELECT id::text FROM workspaces WHERE organization_id=$1::uuid ORDER BY created_at, id LIMIT 1", org).Scan(&orgFirst); err != nil {
		t.Fatalf("org first workspace: %v", err)
	}
	got, err = svc.resolveWorkspace(ctx, st.Pool, policy.Principal{System: true, OrganizationID: org, Authenticated: true})
	if err != nil || got != orgFirst {
		t.Fatalf("system principal must resolve the org first workspace: got %q err=%v want %q", got, err, orgFirst)
	}
	// Legacy compat principals keep the migration behaviour (System=true).
	got, err = svc.resolveWorkspace(ctx, st.Pool, policy.Principal{System: true, IdentityID: single, OrganizationID: org, WorkspaceID: wsA, Authenticated: true})
	if err != nil || got != wsA {
		t.Fatalf("system principal with explicit workspace: got %q err=%v", got, err)
	}
}
