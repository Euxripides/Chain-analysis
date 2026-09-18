package api

// LEGACY-CONSOLE-01 tests. These pin the three properties the decoupling depends
// on: the path is fail-closed, the principal carries exactly the configured
// permissions, and it can never reach an admin permission.

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

const legacyConsoleTestToken = "legacy-console-test-token-0001"

func legacyConsoleTestConfig() *config.Config {
	sum := sha256.Sum256([]byte(legacyConsoleTestToken))
	return &config.Config{
		LegacyConsoleTokenSHA256:    hex.EncodeToString(sum[:]),
		LegacyConsolePermissions:    []string{"case:list", "job:list:workspace"},
		LegacyConsoleOrganizationID: "11111111-1111-1111-1111-111111111111",
		LegacyConsoleWorkspaceID:    "22222222-2222-2222-2222-222222222222",
		LegacyConsoleIdentityID:     "33333333-3333-3333-3333-333333333333",
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestLegacyConsoleIsDisabledWithoutAConfiguredToken: no digest means no path.
func TestLegacyConsoleIsDisabledWithoutAConfiguredToken(t *testing.T) {
	auth := newLegacyConsoleAuth(&config.Config{}, discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cases", nil)
	req.Header.Set(legacyConsoleHeader, legacyConsoleTestToken)
	if _, ok := auth.principal(req); ok {
		t.Fatal("an unconfigured legacy console must never authenticate a request")
	}
}

// TestLegacyConsoleFailsClosedOnBadConfiguration: every malformed configuration
// disables the path instead of granting it.
func TestLegacyConsoleFailsClosedOnBadConfiguration(t *testing.T) {
	short := *legacyConsoleTestConfig()
	short.LegacyConsoleTokenSHA256 = "abcd"
	nonHex := *legacyConsoleTestConfig()
	nonHex.LegacyConsoleTokenSHA256 = "zz" + legacyConsoleTestConfig().LegacyConsoleTokenSHA256[2:]
	unknown := *legacyConsoleTestConfig()
	unknown.LegacyConsolePermissions = []string{"case:list", "case:not-a-real-permission"}
	empty := *legacyConsoleTestConfig()
	empty.LegacyConsolePermissions = nil
	noOrg := *legacyConsoleTestConfig()
	noOrg.LegacyConsoleOrganizationID = ""
	noWorkspace := *legacyConsoleTestConfig()
	noWorkspace.LegacyConsoleWorkspaceID = ""
	noIdentity := *legacyConsoleTestConfig()
	noIdentity.LegacyConsoleIdentityID = ""
	badAudience := *legacyConsoleTestConfig()
	badAudience.LegacyConsoleAudience = "root"
	adminKeyUserAudience := *legacyConsoleTestConfig()
	adminKeyUserAudience.LegacyConsolePermissions = []string{"case:list", "admin:provider:read"}
	adminKeyUserAudience.LegacyConsoleAudience = "user"

	for name, cfg := range map[string]*config.Config{
		"short digest": &short, "non-hex digest": &nonHex, "unknown permission": &unknown,
		"empty permissions": &empty, "missing organization": &noOrg, "missing workspace": &noWorkspace,
		"missing audit identity": &noIdentity, "unknown audience": &badAudience,
		"admin key under the user audience": &adminKeyUserAudience,
	} {
		auth := newLegacyConsoleAuth(cfg, discardLogger())
		req := httptest.NewRequest(http.MethodGet, "/api/v1/cases", nil)
		req.Header.Set(legacyConsoleHeader, legacyConsoleTestToken)
		if _, ok := auth.principal(req); ok {
			t.Errorf("%s must disable the legacy console principal", name)
		}
	}
}

// TestLegacyConsoleRequiresTheExactToken: a missing or wrong token is simply not
// this principal, so the request continues to the normal 401 path.
func TestLegacyConsoleRequiresTheExactToken(t *testing.T) {
	auth := newLegacyConsoleAuth(legacyConsoleTestConfig(), discardLogger())
	missing := httptest.NewRequest(http.MethodGet, "/api/v1/cases", nil)
	if _, ok := auth.principal(missing); ok {
		t.Fatal("no header must not authenticate")
	}
	for _, wrong := range []string{"", " ", legacyConsoleTestToken + "x", "LEGACY-CONSOLE-TEST-TOKEN-0001"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/cases", nil)
		req.Header.Set(legacyConsoleHeader, wrong)
		if _, ok := auth.principal(req); ok {
			t.Fatalf("token %q must not authenticate", wrong)
		}
	}
}

// TestLegacyConsolePrincipalScope pins what the principal IS: authenticated,
// sessionless, non-admin audience, and carrying exactly the configured keys.
func TestLegacyConsolePrincipalScope(t *testing.T) {
	auth := newLegacyConsoleAuth(legacyConsoleTestConfig(), discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cases", nil)
	req.Header.Set(legacyConsoleHeader, legacyConsoleTestToken)
	p, ok := auth.principal(req)
	if !ok {
		t.Fatal("the configured token must authenticate")
	}
	if !p.Authenticated {
		t.Error("the principal must be authenticated")
	}
	if p.System {
		t.Error("the principal must NOT be a system principal: the policy engine has to decide every route from Permissions")
	}
	if p.SessionID != "" {
		t.Error("the principal must carry no session id: CSRF, scope staleness and the commit gate are session semantics")
	}
	if p.Audience == "admin" {
		t.Error("the principal must not claim the admin audience")
	}
	if len(p.Permissions) != 2 || !p.Has("case:list") || !p.Has("job:list:workspace") {
		t.Fatalf("permissions = %v, want exactly the configured two keys", p.Permissions)
	}
	if p.OrganizationID == "" || p.WorkspaceID == "" {
		t.Error("the principal needs an organization and a workspace scope")
	}

	engine := &policy.Engine{}
	if d := engine.Authorize(p, "case:list"); !d.Allowed {
		t.Errorf("a configured permission must be allowed, got %+v", d)
	}
	if d := engine.Authorize(p, "job:read"); d.Allowed {
		t.Error("a permission that was NOT configured must be refused")
	}
	if d := engine.Authorize(p, "admin:provider:read"); d.Allowed {
		t.Errorf("an admin permission must be refused even though it is declared, got %+v", d)
	}
	if p.IdentityID != "33333333-3333-3333-3333-333333333333" {
		t.Error("the principal must carry the configured audit identity")
	}
	if p.Audience != "user" {
		t.Errorf("audience = %q, want user by default", p.Audience)
	}
}

// TestLegacyConsoleAdminAudienceUnlocksAdminKeys proves the audience switch is
// the only way the legacy console can reach an admin:* key, and that it still
// never becomes a System principal.
func TestLegacyConsoleAdminAudienceUnlocksAdminKeys(t *testing.T) {
	cfg := legacyConsoleTestConfig()
	cfg.LegacyConsolePermissions = []string{"admin:provider:read", "admin:performance:read"}
	cfg.LegacyConsoleAudience = "admin"
	auth := newLegacyConsoleAuth(cfg, discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/providers/ankr/status", nil)
	req.Header.Set(legacyConsoleHeader, legacyConsoleTestToken)
	p, ok := auth.principal(req)
	if !ok {
		t.Fatal("an admin-audience legacy console must authenticate")
	}
	if p.System {
		t.Fatal("an admin audience must not turn the principal into a System principal")
	}
	engine := &policy.Engine{}
	if d := engine.Authorize(p, "admin:provider:read"); !d.Allowed {
		t.Errorf("the configured admin key must pass under the admin audience, got %+v", d)
	}
	if d := engine.Authorize(p, "admin:dataset:manage"); d.Allowed {
		t.Error("a key that was NOT configured must still be refused")
	}
}

// TestIdentityMiddlewareUsesTheLegacyConsolePrincipal proves the seam end to end
// at the middleware boundary: the entry-layer header authenticates the request,
// and a request without it stays anonymous.
func TestIdentityMiddlewareUsesTheLegacyConsolePrincipal(t *testing.T) {
	cfg := legacyConsoleTestConfig()
	s := &Server{deps: Deps{Cfg: cfg, Log: discardLogger()}}
	s.legacyConsole = newLegacyConsoleAuth(cfg, s.deps.Log)

	captured := policy.Principal{}
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = PrincipalFrom(r.Context())
	})

	withToken := httptest.NewRequest(http.MethodGet, "/api/v1/cases", nil)
	withToken.Header.Set(legacyConsoleHeader, legacyConsoleTestToken)
	s.identityMiddleware(next).ServeHTTP(httptest.NewRecorder(), withToken)
	if !captured.Authenticated || !captured.Has("case:list") {
		t.Fatalf("the injected token must authenticate the request, got %+v", captured)
	}

	captured = policy.Principal{}
	withoutToken := httptest.NewRequest(http.MethodGet, "/api/v1/cases", nil)
	s.identityMiddleware(next).ServeHTTP(httptest.NewRecorder(), withoutToken)
	if captured.Authenticated {
		t.Fatal("a request without the token must stay anonymous and be refused 401 by require()")
	}
}
