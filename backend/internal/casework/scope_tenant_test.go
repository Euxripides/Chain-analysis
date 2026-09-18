package casework

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// ---- scripted queryRower -------------------------------------------------

type boolRow struct {
	v   bool
	err error
}

func (r boolRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return errors.New("boolRow: need exactly one dest")
	}
	p, ok := dest[0].(*bool)
	if !ok {
		return errors.New("boolRow: dest is not *bool")
	}
	*p = r.v
	return nil
}

type textRow struct {
	v   string
	err error
}

func (r textRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return errors.New("textRow: need exactly one dest")
	}
	p, ok := dest[0].(*string)
	if !ok {
		return errors.New("textRow: dest is not *string")
	}
	*p = r.v
	return nil
}

// scriptedRower answers the three queries resolveWorkspace may issue and
// records every statement so a test can assert which branch really ran.
type scriptedRower struct {
	orgFirstID string
	orgFirstNo bool
	wsExists   bool
	member     bool
	seen       []string
}

func (f *scriptedRower) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	f.seen = append(f.seen, sql)
	switch {
	case strings.Contains(sql, "FROM workspace_memberships"):
		return boolRow{v: f.member}
	case strings.Contains(sql, "FROM workspaces WHERE id="):
		return boolRow{v: f.wsExists}
	case strings.Contains(sql, "FROM workspaces WHERE organization_id="):
		if f.orgFirstNo {
			return textRow{err: pgx.ErrNoRows}
		}
		return textRow{v: f.orgFirstID}
	default:
		return textRow{err: errors.New("unexpected query: " + sql)}
	}
}

func (f *scriptedRower) usedAny(substr string) bool {
	for _, q := range f.seen {
		if strings.Contains(q, substr) {
			return true
		}
	}
	return false
}

// ---- P1-20260914-CASEWORK-MEMBERSHIP -------------------------------------

// An identity principal must never inherit the organisation default workspace.
func TestResolveWorkspaceIdentityWithoutWorkspaceFailsClosed(t *testing.T) {
	s := &Service{}
	q := &scriptedRower{orgFirstID: "ws-org-first", wsExists: true, member: true}
	_, err := s.resolveWorkspace(context.Background(), q, policy.Principal{
		IdentityID: "id-1", OrganizationID: "org-1", Authenticated: true,
	})
	if err == nil {
		t.Fatal("identity principal without a workspace must fail closed, got nil")
	}
	if apiErr := AsAPIError(err); apiErr.Status != 403 {
		t.Fatalf("expected 403 fail closed, got status=%d code=%s", apiErr.Status, apiErr.Code)
	}
	if q.usedAny("ORDER BY created_at") || q.usedAny("slug='default'") {
		t.Fatalf("organisation-wide workspace fallback was executed: %v", q.seen)
	}
}

// A bound workspace outside the principal organisation is refused.
func TestResolveWorkspaceForeignWorkspaceFailsClosed(t *testing.T) {
	s := &Service{}
	q := &scriptedRower{wsExists: false}
	_, err := s.resolveWorkspace(context.Background(), q, policy.Principal{
		IdentityID: "id-1", OrganizationID: "org-1", WorkspaceID: "ws-other", Authenticated: true,
	})
	if err == nil || AsAPIError(err).Status != 403 {
		t.Fatalf("expected 403 for a workspace outside the org, got %v", err)
	}
}

// A bound workspace inside the org still requires an ACTIVE membership.
func TestResolveWorkspaceRequiresActiveMembership(t *testing.T) {
	s := &Service{}
	q := &scriptedRower{wsExists: true, member: false}
	_, err := s.resolveWorkspace(context.Background(), q, policy.Principal{
		IdentityID: "id-1", OrganizationID: "org-1", WorkspaceID: "ws-1", Authenticated: true,
	})
	if err == nil {
		t.Fatal("identity without an active membership must be refused")
	}
	if AsAPIError(err).Status != 403 {
		t.Fatalf("expected 403, got %v", err)
	}
	if !q.usedAny("FROM workspace_memberships") {
		t.Fatal("membership was not consulted for an identity principal")
	}

	q2 := &scriptedRower{wsExists: true, member: true}
	got, err := s.resolveWorkspace(context.Background(), q2, policy.Principal{
		IdentityID: "id-1", OrganizationID: "org-1", WorkspaceID: "ws-1", Authenticated: true,
	})
	if err != nil || got != "ws-1" {
		t.Fatalf("active member must resolve to its own workspace, got %q err=%v", got, err)
	}
}

// Only a migration-era system principal may use the org-wide fallback.
func TestResolveWorkspaceSystemPrincipalUsesOrgFirstWorkspace(t *testing.T) {
	s := &Service{}
	q := &scriptedRower{orgFirstID: "ws-org-first"}
	got, err := s.resolveWorkspace(context.Background(), q, policy.Principal{
		System: true, OrganizationID: "org-1", Authenticated: true,
	})
	if err != nil || got != "ws-org-first" {
		t.Fatalf("system principal must resolve the org first workspace, got %q err=%v", got, err)
	}
	empty := &scriptedRower{orgFirstNo: true}
	if _, err := s.resolveWorkspace(context.Background(), empty, policy.Principal{System: true, OrganizationID: "org-empty"}); err == nil {
		t.Fatal("an organisation with no workspace must be not-found, not a silent success")
	}
}

// Static regression: the case-write package must not reintroduce an
// organisation-wide workspace fallback outside the system-principal branch.
func TestCaseworkPackageHasNoOrgDefaultWorkspaceFallback(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, ln := range strings.Split(string(b), "\n") {
			code := ln
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			if strings.Contains(code, "DefaultOrgWorkspace(") {
				t.Fatalf("%s:%d still calls DefaultOrgWorkspace(: %s", name, i+1, strings.TrimSpace(ln))
			}
			if strings.Contains(code, "slug='default'") && !strings.Contains(code, "ORDER BY created_at") {
				t.Fatalf("%s:%d reintroduced an org-default workspace fallback: %s", name, i+1, strings.TrimSpace(ln))
			}
		}
	}
}
