package api

// Server-side enforcement of identities.must_change_password (sprint7 C1-SEC).
//
// Before this file existed the flag was only echoed in the /api/auth/v1/login and
// /api/auth/v1/session responses: a session whose identity still carried
// must_change_password = true kept full access to every business endpoint (measured on the
// running API). The guard below makes the flag load bearing and fail closed.
//
// Contract:
//   - Business surfaces (/api/v1/*, /api/v2/*, /api/admin/v1/*) refuse every request with
//     HTTP 403 and the stable code AUTHN_PASSWORD_CHANGE_REQUIRED, before any business
//     logic runs.
//   - The self-service auth surface stays reachable so the holder can repair the state.
//   - The surface rule and the self-service allowlist are DATA (the tables below), not
//     scattered conditionals, so the reachable set is reviewable in one place.
//   - Any new /api/ surface is guarded by default (fail closed); only the self-service
//     prefix and the allowlist entries are exempt.
//
// The flag travels on policy.Principal.MustChangePassword, which principalFromSession copies
// from the session. iam.SessionManager.loadByToken re-reads identities.must_change_password
// on every request, so clearing the flag takes effect on the very next request of the same
// session: no extra query, and no cached flag to invalidate.

import (
	"net/http"
	"strings"
)

// ErrorAUTHNPasswordChangeRequired is the stable refusal code (HTTP 403) for business
// requests made by an identity that still has must_change_password = true.
const ErrorAUTHNPasswordChangeRequired = "AUTHN_PASSWORD_CHANGE_REQUIRED"

// mustChangePasswordMessage is the user-facing text. Clients must branch on the stable error
// code, not on this message. It deliberately carries no identity, password or session data.
const mustChangePasswordMessage = "Password change required: change the password through the self-service endpoint before using business APIs."

// mustChangePasswordChangePath is the self-service route that clears the flag.
const mustChangePasswordChangePath = "/api/auth/v1/password/change"

// selfServiceRoute is one allowlist entry: an exact method and an exact path.
type selfServiceRoute struct {
	Method string
	Path   string
}

// mustChangePasswordAllowlist pins the endpoints that must stay reachable while the flag is
// set. Decision record (see acceptance-evidence/dual-system-v1/sprint7/
// must-change-password-enforcement/DESIGN.md):
//   - GET  /api/auth/v1/session          the console reads its own session state from here
//   - POST /api/auth/v1/logout           leave a session that cannot be used
//   - POST /api/auth/v1/password/change  the only way to clear the flag
//   - GET  /api/auth/v1/methods          discover which sign-in methods are configured
//
// Every other /api/auth/v1 route keeps its previous behaviour because the whole self-service
// surface is exempt from the guard (see mustChangePasswordSurface); the allowlist is the
// documented minimum that must never be cut off, and it is asserted by a regression test.
var mustChangePasswordAllowlist = []selfServiceRoute{
	{Method: http.MethodGet, Path: "/api/auth/v1/session"},
	{Method: http.MethodPost, Path: "/api/auth/v1/logout"},
	{Method: http.MethodPost, Path: mustChangePasswordChangePath},
	{Method: http.MethodGet, Path: "/api/auth/v1/methods"},
}

// mustChangePasswordGuardedPrefixes lists the business surfaces enforced by the guard.
var mustChangePasswordGuardedPrefixes = []string{
	"/api/v1/",
	"/api/v2/",
	"/api/admin/v1/",
}

// mustChangePasswordSelfServicePrefix is the auth self-service surface. It is excluded from
// the fail-closed catch-all so that its existing behaviour is neither widened nor narrowed.
const mustChangePasswordSelfServicePrefix = "/api/auth/v1/"

// apiPathPrefix is the guarded API universe. Anything outside it (health, metrics, docs,
// static assets) is not an API request and is left alone.
const apiPathPrefix = "/api/"

// mustChangeSurface is the classification of one request path.
type mustChangeSurface int

const (
	// mustChangeSurfaceExempt: outside the guarded universe, or part of the self-service
	// surface outside the allowlist (previous behaviour preserved).
	mustChangeSurfaceExempt mustChangeSurface = iota
	// mustChangeSurfaceAllowlisted: an explicit self-service allowlist entry.
	mustChangeSurfaceAllowlisted
	// mustChangeSurfaceGuarded: a business surface, refused while the flag is set.
	mustChangeSurfaceGuarded
)

// mustChangePasswordSurface classifies a request. It is a pure function of method and path
// so the rules can be table-tested without a server, a session or a database.
func mustChangePasswordSurface(method, path string) mustChangeSurface {
	if !strings.HasPrefix(path, apiPathPrefix) {
		return mustChangeSurfaceExempt
	}
	if mustChangePasswordAllowlisted(method, path) {
		return mustChangeSurfaceAllowlisted
	}
	if hasAnyPathPrefix(path, mustChangePasswordGuardedPrefixes) {
		return mustChangeSurfaceGuarded
	}
	// Fail closed for /api/ paths that are neither allowlisted nor part of the self-service
	// surface: a future /api/v3 (or any other business prefix) is guarded without a change
	// to this file.
	if !strings.HasPrefix(path, mustChangePasswordSelfServicePrefix) {
		return mustChangeSurfaceGuarded
	}
	// Self-service route outside the allowlist: keep the existing behaviour.
	return mustChangeSurfaceExempt
}

func mustChangePasswordAllowlisted(method, path string) bool {
	for _, route := range mustChangePasswordAllowlist {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

func hasAnyPathPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// mustChangePasswordMiddleware refuses business requests while the authenticated identity
// still has must_change_password = true. It runs after identityMiddleware (it needs the
// principal) and before the business handlers, so a refused request never reaches business
// logic and never produces a business audit row.
func (s *Server) mustChangePasswordMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := PrincipalFrom(r.Context())
		if !p.Authenticated || !p.MustChangePassword {
			next.ServeHTTP(w, r)
			return
		}
		if mustChangePasswordSurface(r.Method, r.URL.Path) != mustChangeSurfaceGuarded {
			next.ServeHTTP(w, r)
			return
		}
		// Audit before the response: every refusal is traceable to a route and a request id.
		s.auditDenyCode(r, p, "", "must_change_password", ErrorAUTHNPasswordChangeRequired, http.StatusForbidden)
		writeErrorCode(w, r, http.StatusForbidden, ErrorAUTHNPasswordChangeRequired,
			mustChangePasswordMessage, map[string]any{
				"reason":                   "must_change_password",
				"password_change_endpoint": mustChangePasswordChangePath,
			})
	})
}
