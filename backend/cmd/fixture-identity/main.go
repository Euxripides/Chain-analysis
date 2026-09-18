// fixture-identity：为 **专用测试组织** 创建 组织/工作区/身份，并把一次性口令写入受限文件。
//
// 与 cmd/admin-fixture 的区别（为什么不直接用它）：
//   - admin-fixture 的账号邮箱固定为 fixture-<role>@fixture.local 且按邮箱查找即复用，
//     无法为"新组织里的同名角色"创建独立身份（会复用 ACME 夹具身份的 organization_id，
//     并把新组织作为额外 membership 混入），不满足"回归必须使用专用身份/组织/工作区"的要求。
//   - admin-fixture 只创建一个工作区；scope 边界回归需要同一组织下**两个**工作区。
//
// 安全边界：
//   - APP_ENV=production 直接拒绝；
//   - 口令一次性随机生成，只写入 --out（0600）文件，不打印到 stdout；
//   - 身份一律 must_change_password=true；
//   - 只创建/复用 --org-slug 指定的组织，绝不改动其它组织、真实账号或既有活跃任务；
//   - 幂等：组织/工作区/身份/成员关系/角色绑定均 ON CONFLICT DO NOTHING。
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

func main() {
	orgSlug := flag.String("org-slug", "", "组织 slug（必填，例如 s7jobscope）")
	orgName := flag.String("org-name", "", "组织名称（必填）")
	workspaces := flag.String("workspaces", "", "工作区 slug 列表，逗号分隔（必填）")
	identities := flag.String("identities", "", "身份列表 role:email:ws-slug，逗号分隔（必填）")
	out := flag.String("out", "", "一次性口令输出文件（0600）")
	flag.Parse()

	if *orgSlug == "" || *orgName == "" || *workspaces == "" || *identities == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: fixture-identity -org-slug S -org-name N -workspaces a,b -identities role:email:ws,... -out /path/creds.env")
		os.Exit(2)
	}

	ctx := context.Background()
	cfg := config.Load()
	if strings.EqualFold(cfg.Env, "production") {
		fmt.Fprintln(os.Stderr, "拒绝在 production 环境创建测试夹具。")
		os.Exit(2)
	}
	// 夹具需要 DDL/所有权权限：优先使用迁移账号（blockchain_api 对 organizations 无权限）。
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
		roleKey, email, wsSlug := parts[0], parts[1], parts[2]
		wsID, ok := wsIDs[wsSlug]
		if !ok {
			fmt.Fprintln(os.Stderr, "unknown workspace slug in identity spec:", wsSlug)
			os.Exit(2)
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
			ins := "INSERT INTO identities (organization_id, email, display_name, status, must_change_password) VALUES ($1,$2,$3,'active',true) RETURNING id::text"
			if err := pool.QueryRow(ctx, ins, orgID, email, "S7 JobScope "+roleKey).Scan(&identityID); err != nil {
				fmt.Fprintln(os.Stderr, "create identity failed:", err)
				os.Exit(1)
			}
		}
		if err := repo.SetPassword(ctx, identityID, hash, true); err != nil {
			fmt.Fprintln(os.Stderr, "set password failed:", err)
			os.Exit(1)
		}
		orgMem := "INSERT INTO organization_memberships (organization_id, identity_id, status) VALUES ($1,$2,'active') ON CONFLICT (organization_id, identity_id) DO NOTHING"
		if _, err := pool.Exec(ctx, orgMem, orgID, identityID); err != nil {
			fmt.Fprintln(os.Stderr, "org membership failed:", err)
			os.Exit(1)
		}
		wsMem := "INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status) VALUES ($1,$2,$3,'active') ON CONFLICT (workspace_id, identity_id) DO NOTHING"
		if _, err := pool.Exec(ctx, wsMem, wsID, orgID, identityID); err != nil {
			fmt.Fprintln(os.Stderr, "ws membership failed:", err)
			os.Exit(1)
		}
		var roleID string
		if err := pool.QueryRow(ctx, "SELECT id::text FROM roles WHERE key=$1 AND is_system LIMIT 1", roleKey).Scan(&roleID); err != nil {
			fmt.Fprintln(os.Stderr, "unknown role:", roleKey, err)
			os.Exit(1)
		}
		bind := "INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, reason) VALUES ($1,$2,'organization',$3,'S7 JobScope scope-isolation fixture') ON CONFLICT (identity_id, role_id, scope_type, COALESCE(scope_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE revoked_at IS NULL DO UPDATE SET granted_at = now()"
		if _, err := pool.Exec(ctx, bind, identityID, roleID, orgID); err != nil {
			fmt.Fprintln(os.Stderr, "role binding failed:", err)
			os.Exit(1)
		}
		tag := strings.ToUpper(strings.ReplaceAll(roleKey, "-", "_"))
		lines = append(lines, fmt.Sprintf("S7JS_%s_EMAIL=%s", tag, email))
		lines = append(lines, fmt.Sprintf("S7JS_%s_PW=%s", tag, pw))
		lines = append(lines, fmt.Sprintf("S7JS_%s_WS=%s", tag, wsID))
	}

	body := strings.Join(lines, "\n") + "\n"
	body += fmt.Sprintf("S7JS_ORG_ID=%s\nS7JS_ORG_SLUG=%s\n", orgID, *orgSlug)
	for slug, id := range wsIDs {
		body += fmt.Sprintf("S7JS_WS_%s=%s\n", strings.ToUpper(strings.ReplaceAll(slug, "-", "_")), id)
	}
	if err := os.WriteFile(*out, []byte(body), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write creds failed:", err)
		os.Exit(1)
	}
	fmt.Printf("org_id=%s org_slug=%s\n", orgID, *orgSlug)
	for slug, id := range wsIDs {
		fmt.Printf("workspace %s=%s\n", slug, id)
	}
	fmt.Printf("identities_created=%d creds_file=%s mode=0600\n", len(splitAndTrim(*identities)), *out)
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
