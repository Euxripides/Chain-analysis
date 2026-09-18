// fixture-membership creates a DEDICATED acceptance organisation whose
// identities can hold zero, one or several workspace memberships.
//
// Why a second fixture tool (cmd/fixture-identity cannot express this):
//   - the membership-aware workspace resolution (P1-20260913-MEMBERSHIP /
//     P1-20260914-CASEWORK-MEMBERSHIP) has to be proven against REAL PostgreSQL
//     membership rows for three shapes the old tool cannot create:
//   - an identity with ZERO active workspace memberships,
//   - an identity with SEVERAL active memberships (explicit choice required),
//   - an identity whose membership is revoked and later restored;
//   - it must never touch a real user row: everything it creates lives under the
//     dedicated org slug passed on the command line.
//
// Safety boundary (same as cmd/fixture-identity):
//   - APP_ENV=production refuses to run;
//   - the one-time password is random, written only to --out (0600), never printed;
//   - identity status is active; must_change_password mirrors the product default
//     (true) and can be turned off explicitly with -must-change-password=false;
//   - idempotent: org / workspace / identity / membership / binding are all
//     ON CONFLICT DO NOTHING or ON CONFLICT DO UPDATE.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

const noneMarker = "none"

func main() {
	orgSlug := flag.String("org-slug", "", "organization slug (required)")
	orgName := flag.String("org-name", "", "organization display name (required)")
	workspaces := flag.String("workspaces", "", "comma separated workspace slugs (required)")
	identities := flag.String("identities", "", "role:email:memberships, memberships = none | ws1 | ws1+ws2 (required)")
	out := flag.String("out", "", "credential output file, written 0600 (required)")
	mustChange := flag.Bool("must-change-password", true, "initial value of identities.must_change_password")
	flag.Parse()

	if *orgSlug == "" || *orgName == "" || *workspaces == "" || *identities == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: fixture-membership -org-slug S -org-name N -workspaces a,b -identities role:email:mem,... -out /path/creds.env")
		os.Exit(2)
	}

	ctx := context.Background()
	cfg := config.Load()
	if strings.EqualFold(cfg.Env, "production") {
		fmt.Fprintln(os.Stderr, "refusing to create acceptance fixtures in production")
		os.Exit(2)
	}
	dsn := cfg.MigrationDatabaseURL
	if dsn == "" {
		dsn = cfg.DatabaseURL
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "postgres connect failed:", err)
		os.Exit(1)
	}
	defer st.Close()
	pool := st.Pool

	var orgID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM organizations WHERE slug=$1", *orgSlug).Scan(&orgID); err != nil {
		if err := pool.QueryRow(ctx, "INSERT INTO organizations (name, slug) VALUES ($1,$2) RETURNING id::text", *orgName, *orgSlug).Scan(&orgID); err != nil {
			fmt.Fprintln(os.Stderr, "create org failed:", err)
			os.Exit(1)
		}
	}

	wsIDs := map[string]string{}
	for _, slug := range splitAndTrim(*workspaces) {
		var id string
		if err := pool.QueryRow(ctx, "SELECT id::text FROM workspaces WHERE organization_id=$1 AND slug=$2", orgID, slug).Scan(&id); err != nil {
			if err := pool.QueryRow(ctx, "INSERT INTO workspaces (organization_id, name, slug) VALUES ($1,$2,$3) RETURNING id::text",
				orgID, *orgName+" "+slug, slug).Scan(&id); err != nil {
				fmt.Fprintln(os.Stderr, "create workspace failed:", err)
				os.Exit(1)
			}
		}
		wsIDs[slug] = id
	}

	repo := iam.NewRepo(pool)
	var lines []string
	for _, spec := range splitAndTrim(*identities) {
		parts := strings.Split(spec, ":")
		if len(parts) != 3 {
			fmt.Fprintln(os.Stderr, "bad identity spec:", spec)
			os.Exit(2)
		}
		roleKey, email, memSpec := parts[0], parts[1], parts[2]
		var memSlugs []string
		if !strings.EqualFold(memSpec, noneMarker) {
			for _, s := range strings.Split(memSpec, "+") {
				s = strings.TrimSpace(s)
				if s == "" {
					continue
				}
				if _, ok := wsIDs[s]; !ok {
					fmt.Fprintln(os.Stderr, "unknown workspace slug in identity spec:", s)
					os.Exit(2)
				}
				memSlugs = append(memSlugs, s)
			}
		}

		pw, err := randomPassword(24)
		if err != nil {
			fmt.Fprintln(os.Stderr, "random password failed:", err)
			os.Exit(1)
		}
		hash, err := iam.HashPassword(pw, iam.DefaultParams())
		if err != nil {
			fmt.Fprintln(os.Stderr, "hash failed:", err)
			os.Exit(1)
		}
		var identityID string
		if err := pool.QueryRow(ctx, "SELECT id::text FROM identities WHERE lower(email)=lower($1)", email).Scan(&identityID); err != nil {
			ins := "INSERT INTO identities (organization_id, email, display_name, status, must_change_password) VALUES ($1,$2,$3,'active',$4) RETURNING id::text"
			if err := pool.QueryRow(ctx, ins, orgID, email, "C1 membership "+roleKey, *mustChange).Scan(&identityID); err != nil {
				fmt.Fprintln(os.Stderr, "create identity failed:", err)
				os.Exit(1)
			}
		}
		if err := repo.SetPassword(ctx, identityID, hash, *mustChange); err != nil {
			fmt.Fprintln(os.Stderr, "set password failed:", err)
			os.Exit(1)
		}
		orgMem := "INSERT INTO organization_memberships (organization_id, identity_id, status) VALUES ($1,$2,'active') ON CONFLICT (organization_id, identity_id) DO NOTHING"
		if _, err := pool.Exec(ctx, orgMem, orgID, identityID); err != nil {
			fmt.Fprintln(os.Stderr, "org membership failed:", err)
			os.Exit(1)
		}
		for _, slug := range memSlugs {
			wsMem := "INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status) VALUES ($1,$2,$3,'active') ON CONFLICT (workspace_id, identity_id) DO UPDATE SET status='active', updated_at=now()"
			if _, err := pool.Exec(ctx, wsMem, wsIDs[slug], orgID, identityID); err != nil {
				fmt.Fprintln(os.Stderr, "ws membership failed:", err)
				os.Exit(1)
			}
		}
		var roleID string
		if err := pool.QueryRow(ctx, "SELECT id::text FROM roles WHERE key=$1 AND is_system LIMIT 1", roleKey).Scan(&roleID); err != nil {
			fmt.Fprintln(os.Stderr, "unknown role:", roleKey, err)
			os.Exit(1)
		}
		bind := "INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, reason) VALUES ($1,$2,'organization',$3,'C1 membership-aware write fixture') ON CONFLICT (identity_id, role_id, scope_type, COALESCE(scope_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE revoked_at IS NULL DO UPDATE SET granted_at = now()"
		if _, err := pool.Exec(ctx, bind, identityID, roleID, orgID); err != nil {
			fmt.Fprintln(os.Stderr, "role binding failed:", err)
			os.Exit(1)
		}

		// Membership census straight from PG: the caller must not have to trust
		// the spec it just passed in.
		var activeCount int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM workspace_memberships WHERE identity_id=$1::uuid AND status='active'", identityID).Scan(&activeCount); err != nil {
			fmt.Fprintln(os.Stderr, "membership census failed:", err)
			os.Exit(1)
		}
		// Tag from the email local part: one role is instantiated several times
		// (0 / 1 / many memberships), so the role key alone is not a unique key.
		local := email
		if idx := strings.Index(local, "@"); idx > 0 {
			local = local[:idx]
		}
		tag := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(local))
		lines = append(lines, fmt.Sprintf("C1MEM_%s_EMAIL=%s", tag, email))
		lines = append(lines, fmt.Sprintf("C1MEM_%s_PW=%s", tag, pw))
		lines = append(lines, fmt.Sprintf("C1MEM_%s_IDENTITY_ID=%s", tag, identityID))
		lines = append(lines, fmt.Sprintf("C1MEM_%s_ACTIVE_MEMBERSHIPS=%d", tag, activeCount))
		for _, slug := range memSlugs {
			lines = append(lines, fmt.Sprintf("C1MEM_%s_WS_%s=%s", tag, strings.ToUpper(strings.ReplaceAll(slug, "-", "_")), wsIDs[slug]))
		}
	}

	body := strings.Join(lines, "\n") + "\n"
	body += fmt.Sprintf("C1MEM_ORG_ID=%s\nC1MEM_ORG_SLUG=%s\n", orgID, *orgSlug)
	for slug, id := range wsIDs {
		body += fmt.Sprintf("C1MEM_WS_%s=%s\n", strings.ToUpper(strings.ReplaceAll(slug, "-", "_")), id)
	}
	if err := os.WriteFile(*out, []byte(body), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write creds failed:", err)
		os.Exit(1)
	}
	fmt.Printf("org_id=%s org_slug=%s identities=%d creds_file=%s mode=0600\n", orgID, *orgSlug, len(splitAndTrim(*identities)), *out)
	for slug, id := range wsIDs {
		fmt.Printf("workspace %s=%s\n", slug, id)
	}
}

func splitAndTrim(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func randomPassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
