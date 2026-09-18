//go:build live

// Real-PostgreSQL integration test for the membership-aware writable-workspace
// resolution (P1-20260913-MEMBERSHIP). It drives the PRODUCTION code path
// Server.writableWorkspace, which calls iam.ResolveLoginWorkspace, so the
// evidence covers the real SQL against real workspace_memberships rows rather
// than a stubbed decision core.
//
// Run on the VM with:
//
//	C1MEM_LIVE_DSN=<deploy dsn> C1MEM_ORG_ID=... C1MEM_SINGLE_EMAIL=... //   go test -tags live -run TestWritableWorkspaceMembershipLive ./internal/api/
package api

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func liveMembershipServer(t *testing.T) (*Server, *store.Store, context.Context) {
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
	srv := &Server{deps: Deps{Store: st}, iam: iam.NewService(iam.NewRepo(st.Pool), nil, iam.Config{}, nil)}
	return srv, st, ctx
}

func envRequired(t *testing.T, key string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		t.Skipf("%s not set", key)
	}
	return v
}

func identityIDByEmail(t *testing.T, ctx context.Context, st *store.Store, email string) string {
	t.Helper()
	var id string
	if err := st.Pool.QueryRow(ctx, "SELECT id::text FROM identities WHERE lower(email)=lower($1)", email).Scan(&id); err != nil {
		t.Fatalf("identity lookup %s: %v", email, err)
	}
	return id
}

// resolveWritable drives the PRODUCTION composition: scopeFromPrincipal builds
// the tenant scope exactly as the router does, then writableWorkspace resolves
// the concrete workspace. A hand-built tenantScope would bypass the
// authentication/scope check and prove less.
func resolveWritable(t *testing.T, srv *Server, ctx context.Context, p policy.Principal) (string, error) {
	t.Helper()
	scope, err := srv.scopeFromPrincipal(p)
	if err != nil {
		return "", err
	}
	return srv.writableWorkspace(withPrincipal(ctx, p), scope)
}

// Class 1: zero active memberships must fail closed.
func TestWritableWorkspaceMembershipLiveZeroMembers(t *testing.T) {
	srv, st, ctx := liveMembershipServer(t)
	org := envRequired(t, "C1MEM_ORG_ID")
	email := envRequired(t, "C1MEM_NOMEM_EMAIL")
	identityID := identityIDByEmail(t, ctx, st, email)

	var active int
	if err := st.Pool.QueryRow(ctx,
		"SELECT count(*) FROM workspace_memberships WHERE identity_id=$1::uuid AND status='active'", identityID).Scan(&active); err != nil {
		t.Fatalf("membership census: %v", err)
	}
	if active != 0 {
		t.Fatalf("fixture %s must hold 0 active memberships, PG says %d", email, active)
	}
	if ws, err := resolveWritable(t, srv, ctx, policy.Principal{IdentityID: identityID, OrganizationID: org, Authenticated: true}); err == nil {
		t.Fatalf("identity with 0 memberships resolved workspace %q; expected fail closed", ws)
	}
}

// Class 2: several memberships and no explicit scope must fail closed (never
// "take the first row"), and an explicit choice must be honoured.
func TestWritableWorkspaceMembershipLiveMultipleMembers(t *testing.T) {
	srv, st, ctx := liveMembershipServer(t)
	org := envRequired(t, "C1MEM_ORG_ID")
	email := envRequired(t, "C1MEM_MULTI_EMAIL")
	identityID := identityIDByEmail(t, ctx, st, email)

	rows, err := st.Pool.Query(ctx,
		"SELECT w.id::text FROM workspace_memberships wm JOIN workspaces w ON w.id=wm.workspace_id "+
			"WHERE wm.identity_id=$1::uuid AND wm.status='active' ORDER BY w.slug", identityID)
	if err != nil {
		t.Fatalf("membership rows: %v", err)
	}
	defer rows.Close()
	var wsIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		wsIDs = append(wsIDs, id)
	}
	if len(wsIDs) < 2 {
		t.Fatalf("fixture %s must hold >=2 active memberships, PG says %d", email, len(wsIDs))
	}
	if ws, err := resolveWritable(t, srv, ctx, policy.Principal{IdentityID: identityID, OrganizationID: org, Authenticated: true}); err == nil {
		t.Fatalf("multi-membership identity without explicit scope resolved %q; expected fail closed", ws)
	}
	// An explicit session workspace is honoured verbatim.
	got, err := resolveWritable(t, srv, ctx, policy.Principal{IdentityID: identityID, OrganizationID: org, WorkspaceID: wsIDs[1], Authenticated: true})
	if err != nil || got != wsIDs[1] {
		t.Fatalf("explicit session workspace must be honoured: got %q err=%v want %q", got, err, wsIDs[1])
	}
}

// Class 3: a single membership resolves to exactly that workspace.
func TestWritableWorkspaceMembershipLiveSingleMember(t *testing.T) {
	srv, st, ctx := liveMembershipServer(t)
	org := envRequired(t, "C1MEM_ORG_ID")
	email := envRequired(t, "C1MEM_SINGLE_EMAIL")
	identityID := identityIDByEmail(t, ctx, st, email)
	want := envRequired(t, "C1MEM_WS_A")

	got, err := resolveWritable(t, srv, ctx, policy.Principal{IdentityID: identityID, OrganizationID: org, Authenticated: true})
	if err != nil {
		t.Fatalf("single-membership identity must resolve: %v", err)
	}
	if got != want {
		t.Fatalf("resolved %q want %q", got, want)
	}
}

// Class 4: after revocation the very same identity must fail closed again.
func TestWritableWorkspaceMembershipLiveRevokedMember(t *testing.T) {
	srv, st, ctx := liveMembershipServer(t)
	org := envRequired(t, "C1MEM_ORG_ID")
	email := envRequired(t, "C1MEM_REVOKED_EMAIL")
	identityID := identityIDByEmail(t, ctx, st, email)

	var active int
	if err := st.Pool.QueryRow(ctx,
		"SELECT count(*) FROM workspace_memberships WHERE identity_id=$1::uuid AND status='active'", identityID).Scan(&active); err != nil {
		t.Fatalf("membership census: %v", err)
	}
	if active != 0 {
		t.Fatalf("revoked fixture %s still holds %d active memberships", email, active)
	}
	if ws, err := resolveWritable(t, srv, ctx, policy.Principal{IdentityID: identityID, OrganizationID: org, Authenticated: true}); err == nil {
		t.Fatalf("revoked identity resolved workspace %q; expected fail closed", ws)
	}
}

// Class 5a: the organisation-wide fallback remains reachable for the
// migration-era system principal only, and only inside one organisation.
func TestWritableWorkspaceMembershipLiveSystemPrincipal(t *testing.T) {
	srv, st, ctx := liveMembershipServer(t)
	org := envRequired(t, "C1MEM_ORG_ID")
	var orgFirst string
	if err := st.Pool.QueryRow(ctx, "SELECT id::text FROM workspaces WHERE organization_id=$1::uuid ORDER BY created_at, id LIMIT 1", org).Scan(&orgFirst); err != nil {
		t.Fatalf("org first workspace: %v", err)
	}
	got, err := resolveWritable(t, srv, ctx, policy.Principal{System: true, OrganizationID: org, Authenticated: true})
	if err != nil {
		t.Fatalf("system principal must resolve the org first workspace: %v", err)
	}
	if got != orgFirst {
		t.Fatalf("system principal resolved %q want %q", got, orgFirst)
	}
	// An identity-free, unauthenticated principal is refused twice over:
	// scopeFromPrincipal rejects it, and even a hand-built scope without the
	// system marker must not reach the organisation-wide fallback.
	if _, err := srv.scopeFromPrincipal(policy.Principal{}); err == nil {
		t.Fatal("scopeFromPrincipal accepted an unauthenticated principal")
	}
	if ws, err := srv.writableWorkspace(withPrincipal(ctx, policy.Principal{}), tenantScope{OrganizationID: org}); err == nil {
		t.Fatalf("empty, unmarked principal resolved %q; expected fail closed", ws)
	}
}

// Class 5b: an identity that belongs to another organisation can never resolve
// into this organisation.
func TestWritableWorkspaceMembershipLiveForeignIdentity(t *testing.T) {
	srv, st, ctx := liveMembershipServer(t)
	org := envRequired(t, "C1MEM_ORG_ID")
	foreign := envRequired(t, "C1MEM_FOREIGN_EMAIL")
	identityID := identityIDByEmail(t, ctx, st, foreign)

	var inOrg int
	if err := st.Pool.QueryRow(ctx, "SELECT count(*) FROM identities WHERE id=$1::uuid AND organization_id=$2::uuid", identityID, org).Scan(&inOrg); err != nil {
		t.Fatalf("foreign check: %v", err)
	}
	if inOrg != 0 {
		t.Fatalf("fixture %s is expected to live outside org %s", foreign, org)
	}
	if ws, err := resolveWritable(t, srv, ctx, policy.Principal{IdentityID: identityID, OrganizationID: org, Authenticated: true}); err == nil {
		t.Fatalf("foreign identity resolved workspace %q; expected fail closed", ws)
	}
}
