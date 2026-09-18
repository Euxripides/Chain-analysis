package api

// LEGACY-CONSOLE-01: the legacy console service principal.
//
// The legacy console (the frontend served at "/" on the public entry and on
// 192.168.31.152:3000 on the LAN) has no login of its own. It borrows the
// session issued at /admin/login, which exists only on the public host, so on
// any other host it carries no session -- and since 2026-09-13
// (S7-SEC-LEGACY-V1) every /api/v1 read route requires an authenticated
// principal, which is why it shows no data there.
//
// This file gives the legacy line a credential of its own so the two lines stop
// depending on each other:
//
//   - the token is injected by the ENTRY LAYER (nginx for the public host, the
//     vite dev proxy for the LAN entry) for requests that present no session
//     cookie. It is never shipped to the browser, and a client cannot forge it:
//     proxy_set_header REPLACES any client-supplied copy, and the server only
//     compares a SHA-256 in constant time;
//   - the principal is deliberately NOT System and NOT an admin session: it
//     carries exactly the permission keys configured in
//     LEGACY_CONSOLE_PERMISSIONS, so the existing policy engine still decides
//     every route (admin permission keys are refused by the audience rule too);
//   - it has no session row, so the CSRF gate, the session-workspace staleness
//     guard and the P1-A commit gate do not apply to it -- those are session
//     semantics, and this principal has no session;
//   - a real session always wins: identityMiddleware tries every session
//     candidate first and only then falls back to this principal;
//   - an empty LEGACY_CONSOLE_TOKEN_SHA256 disables the whole path.

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// legacyConsoleHeader is set by the entry layer and never by the browser.
const legacyConsoleHeader = "X-Legacy-Console-Token"

// legacyConsoleEmail labels the principal in logs and audit rows. It is a label,
// not an account: no identities row exists for it, and no credential of its own.
const legacyConsoleEmail = "legacy-console@local"

// legacyConsoleRole is the role label carried by the principal. It has no rows
// in the roles table either: the permission list below is the whole authority.
const legacyConsoleRole = "legacy_console"

type legacyConsoleAuth struct {
	enabled        bool
	hash           [sha256.Size]byte
	permissions    []string
	organizationID string
	workspaceID    string
	identityID     string
	audience       string
}

// newLegacyConsoleAuth builds the principal factory. Every failure mode leaves
// the path DISABLED, so a misconfiguration can only remove access, never grant
// it: an unknown permission key, a short or non-hex digest, a missing
// organization/workspace, or an empty permission list all disable it.
func newLegacyConsoleAuth(cfg *config.Config, log *slog.Logger) legacyConsoleAuth {
	if cfg == nil {
		return legacyConsoleAuth{}
	}
	raw := strings.ToLower(strings.TrimSpace(cfg.LegacyConsoleTokenSHA256))
	if raw == "" {
		return legacyConsoleAuth{}
	}
	hash, ok := parseSHA256Hex(raw)
	if !ok {
		if log != nil {
			log.Error("legacy console principal disabled: LEGACY_CONSOLE_TOKEN_SHA256 must be 64 hex characters")
		}
		return legacyConsoleAuth{}
	}
	permissions := make([]string, 0, len(cfg.LegacyConsolePermissions))
	for _, key := range cfg.LegacyConsolePermissions {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, known := policy.Lookup(key); !known {
			if log != nil {
				log.Error("legacy console principal disabled: permission is not declared in the catalog", "permission", key)
			}
			return legacyConsoleAuth{}
		}
		permissions = append(permissions, key)
	}
	if len(permissions) == 0 {
		if log != nil {
			log.Error("legacy console principal disabled: LEGACY_CONSOLE_PERMISSIONS is empty")
		}
		return legacyConsoleAuth{}
	}
	organizationID := strings.TrimSpace(cfg.LegacyConsoleOrganizationID)
	workspaceID := strings.TrimSpace(cfg.LegacyConsoleWorkspaceID)
	if organizationID == "" || workspaceID == "" {
		if log != nil {
			log.Error("legacy console principal disabled: organization and workspace must both be configured")
		}
		return legacyConsoleAuth{}
	}
	identityID := strings.TrimSpace(cfg.LegacyConsoleIdentityID)
	if identityID == "" {
		if log != nil {
			log.Error("legacy console principal disabled: LEGACY_CONSOLE_IDENTITY_ID must name the real identity that audit rows are attributed to")
		}
		return legacyConsoleAuth{}
	}
	// The audience decides whether admin:* keys can ever pass Authorize(), so the
	// two settings are validated against each other instead of silently producing
	// a principal whose configured permissions can never be used.
	audience := strings.ToLower(strings.TrimSpace(cfg.LegacyConsoleAudience))
	if audience == "" {
		audience = iam.AudienceUser
	}
	if audience != iam.AudienceUser && audience != iam.AudienceAdmin {
		if log != nil {
			log.Error("legacy console principal disabled: LEGACY_CONSOLE_AUDIENCE must be user or admin", "audience", audience)
		}
		return legacyConsoleAuth{}
	}
	for _, key := range permissions {
		if policy.IsAdminPermission(key) && audience != iam.AudienceAdmin {
			if log != nil {
				log.Error("legacy console principal disabled: an admin permission key requires LEGACY_CONSOLE_AUDIENCE=admin",
					"permission", key, "audience", audience)
			}
			return legacyConsoleAuth{}
		}
	}
	if log != nil {
		log.Info("legacy console principal enabled",
			"permissions", len(permissions), "identity_id", identityID, "audience", audience,
			"organization_id", organizationID, "workspace_id", workspaceID)
	}
	return legacyConsoleAuth{
		enabled: true, hash: hash, permissions: permissions,
		organizationID: organizationID, workspaceID: workspaceID,
		identityID: identityID, audience: audience,
	}
}

func parseSHA256Hex(value string) ([sha256.Size]byte, bool) {
	var out [sha256.Size]byte
	if len(value) != sha256.Size*2 {
		return out, false
	}
	for i := 0; i < sha256.Size; i++ {
		high, ok := hexNibble(value[i*2])
		if !ok {
			return out, false
		}
		low, ok := hexNibble(value[i*2+1])
		if !ok {
			return out, false
		}
		out[i] = high<<4 | low
	}
	return out, true
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	default:
		return 0, false
	}
}

// principal authenticates an entry-layer token. A missing header, a wrong token
// or a disabled path all report false, and the request then continues down the
// normal authentication path, where require() answers 401.
func (a legacyConsoleAuth) principal(r *http.Request) (policy.Principal, bool) {
	if !a.enabled {
		return policy.Principal{}, false
	}
	presented := strings.TrimSpace(r.Header.Get(legacyConsoleHeader))
	if presented == "" {
		return policy.Principal{}, false
	}
	sum := sha256.Sum256([]byte(presented))
	if subtle.ConstantTimeCompare(sum[:], a.hash[:]) != 1 {
		return policy.Principal{}, false
	}
	return policy.Principal{
		IdentityID:     a.identityID,
		Email:          legacyConsoleEmail,
		DisplayName:    "Legacy console",
		OrganizationID: a.organizationID,
		WorkspaceID:    a.workspaceID,
		Audience:       a.audience,
		Roles:          []string{legacyConsoleRole},
		Permissions:    append([]string(nil), a.permissions...),
		Authenticated:  true,
		// System stays FALSE on purpose: Authorize() must decide every route from
		// Permissions, never from a blanket allow. SessionID stays empty so the
		// session-only gates (CSRF, scope staleness, commit gate) do not apply.
		System: false,
	}, true
}
