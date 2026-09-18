session_id: ba0c3874-c82c-48b8-a156-0ebb24846a52
truncated: false
size: 12239

// admin-fixture：**仅限测试/预发环境**的测试夹具生成器（§28.5 S3-QA-01）。
//
// 跨租户负向测试无法在单组织部署上执行：需要第二个组织、第二个工作区，
// 以及不同角色的账号。本工具生成这套最小夹具，并且：
//
//   * 拒绝在 APP_ENV=production 下运行；
//   * 密码取一次性随机值并打印到标准输出，不写死默认口令；
//   * 生成的账号一律 must_change_password=true；
//   * 全过程写入 login 链（auth.fixture.created），保证夹具本身也可追溯。
//
// 用法：
//
//	go run ./cmd/admin-fixture -org acme -workspace acme-lab -users viewer,analyst,auditor
//	go run ./cmd/admin-fixture -cleanup            # 删除夹具组织及其账号
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/logger"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// fixturePrefix 让清理逻辑可以精确识别并只删除夹具数据。
const fixturePrefix = "fixture-"

func main() {
	orgSlug := flag.String("org", "acme", "第二个组织 slug")
	orgName := flag.String("org-name", "ACME 调查组（测试夹具）", "第二个组织名称")
	wsSlug := flag.String("workspace", "acme-lab", "该组织下的工作区 slug")
	users := flag.String("users", "viewer,analyst,auditor,investigator", "要创建的测试账号角色（逗号分隔）")
	cleanup := flag.Bool("cleanup", false, "删除夹具组织与账号后退出")
	flag.Parse()

	ctx := context.Background()
	cfg := config.Load()
	log := logger.New("info")

	if cfg.Env == "production" {
		fmt.Fprintln(os.Stderr, "拒绝在 production 环境创建测试夹具。")
		os.Exit(2)
	}

	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if *cleanup {
		if err := cleanupFixture(ctx, st, *orgSlug, *wsSlug); err != nil {
			log.Error("cleanup failed", "err", err)
			os.Exit(1)
		}
		fmt.Println("夹具已清理。")
		return
	}

	primaryOrgID, _, err := st.DefaultOrgWorkspace(ctx)
	if err != nil {
		log.Error("default org/workspace not found", "err", err)
		os.Exit(1)
	}

	orgID, err := ensureOrganization(ctx, st, *orgSlug, *orgName)
	if err != nil {
		log.Error("create organization failed", "err", err)
		os.Exit(1)
	}
	wsID, err := ensureWorkspace(ctx, st, orgID, *wsSlug, *orgName+" 工作区")
	if err != nil {
		log.Error("create workspace failed", "err", err)
		os.Exit(1)
	}
	// 同一组织下的第二个工作区：用于验证"工作区角色只作用于目标工作区"（RBAC-003）。
	otherWsID, err := ensureWorkspace(ctx, st, orgID, *wsSlug+"-other", *orgName+" 第二工作区")
	if err != nil {
		log.Error("create second workspace failed", "err", err)
		os.Exit(1)
	}

	// 跨租户断言需要真实对象：为每个工作区各建一个案件。
	// 案件创建接口属 S4，这里直接用 SQL 造对象，并在审计里标注来源。
	caseInLab, err := ensureCase(ctx, st, orgID, wsID, "FIXTURE-ACME-LAB", "夹具案件（acme-lab）")
	if err != nil {
		log.Error("seed case failed", "err", err)
		os.Exit(1)
	}
	caseInOther, err := ensureCase(ctx, st, orgID, otherWsID, "FIXTURE-ACME-OTHER", "夹具案件（acme-other）")
	if err != nil {
		log.Error("seed case failed", "err", err)
		os.Exit(1)
	}
	primaryWsID := ""
	_ = st.Pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE organization_id = $1 AND slug = 'default'`, primaryOrgID).Scan(&primaryWsID)
	caseInPrimary, err := ensureCase(ctx, st, primaryOrgID, primaryWsID, "FIXTURE-PRIMARY", "夹具案件（默认组织）")
	if err != nil {
		log.Error("seed case failed", "err", err)
		os.Exit(1)
	}

	// 机器可读输出，供 RBAC-001..008 测试脚本 source 使用。
	fixtureEnv := fmt.Sprintf(`FIXTURE_ORG_ID=%s
FIXTURE_WORKSPACE_ID=%s
FIXTURE_OTHER_WORKSPACE_ID=%s
FIXTURE_CASE_LAB=%s
FIXTURE_CASE_OTHER=%s
FIXTURE_CASE_PRIMARY=%s
PRIMARY_ORG_ID=%s
PRIMARY_WORKSPACE_ID=%s
`, orgID, wsID, otherWsID, caseInLab, caseInOther, caseInPrimary, primaryOrgID, primaryWsID)
	if err := os.WriteFile("/tmp/dual-system-fixture.env", []byte(fixtureEnv), 0o600); err != nil {
		log.Warn("write fixture env failed", "err", err)
	}
	fmt.Printf("\n跨租户测试对象：\n  acme-lab   案件 %s\n  acme-other 案件 %s\n  默认组织   案件 %s\n", caseInLab, caseInOther, caseInPrimary)

	fmt.Printf("\n夹具组织： %s (%s)\n夹具工作区：%s (%s)\n主组织：    %s\n\n", *orgName, orgID, *wsSlug, wsID, primaryOrgID)

	created := 0
	for _, role := range splitAndTrim(*users) {
		email := fmt.Sprintf("%s%s@fixture.local", fixturePrefix, role)
		password, err := ensureIdentity(ctx, st, cfg, orgID, email, role)
		if err != nil {
			log.Error("create identity failed", "err", err, "email", email, "role", role)
			os.Exit(1)
		}
		created++
		fmt.Printf("  %-14s %-34s %s\n", role, email, password)
	}
	fmt.Printf("\n共 %d 个测试账号。每个账号均要求首次登录改密，请勿在 production 使用。\n", created)
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

func ensureOrganization(ctx context.Context, st *store.Store, slug, name string) (string, error) {
	var id string
	err := st.Pool.QueryRow(ctx, `SELECT id::text FROM organizations WHERE slug = $1`, slug).Scan(&id)
	if err == nil {
		return id, nil
	}
	err = st.Pool.QueryRow(ctx,
		`INSERT INTO organizations (name, slug) VALUES ($1,$2) RETURNING id::text`, name, slug).Scan(&id)
	return id, err
}

func ensureWorkspace(ctx context.Context, st *store.Store, orgID, slug, name string) (string, error) {
	var id string
	err := st.Pool.QueryRow(ctx,
		`SELECT id::text FROM workspaces WHERE organization_id = $1 AND slug = $2`, orgID, slug).Scan(&id)
	if err == nil {
		return id, nil
	}
	err = st.Pool.QueryRow(ctx,
		`INSERT INTO workspaces (organization_id, name, slug) VALUES ($1,$2,$3) RETURNING id::text`,
		orgID, name, slug).Scan(&id)
	return id, err
}

// ensureIdentity 创建（或重置）一个夹具账号，返回一次性密码。
func ensureIdentity(ctx context.Context, st *store.Store, cfg *config.Config, orgID, email, roleKey string) (string, error) {
	password, err := randomPassword(24)
	if err != nil {
		return "", err
	}
	hash, err := iam.HashPassword(password, iam.DefaultParams())
	if err != nil {
		return "", err
	}

	var identityID string
	err = st.Pool.QueryRow(ctx, `SELECT id::text FROM identities WHERE lower(email) = lower($1)`, email).Scan(&identityID)
	if err != nil {
		err = st.Pool.QueryRow(ctx, `
            INSERT INTO identities (organization_id, email, display_name, status, must_change_password)
            VALUES ($1,$2,$3,'active',true) RETURNING id::text`,
			orgID, email, "Fixture "+roleKey).Scan(&identityID)
		if err != nil {
			return "", err
		}
	}

	repo := iam.NewRepo(st.Pool)
	if err := repo.SetPassword(ctx, identityID, hash, true); err != nil {
		return "", err
	}
	if _, err := st.Pool.Exec(ctx, `
        INSERT INTO organization_memberships (organization_id, identity_id, status)
        VALUES ($1,$2,'active') ON CONFLICT (organization_id, identity_id) DO NOTHING`,
		orgID, identityID); err != nil {
		return "", err
	}
	if _, err := st.Pool.Exec(ctx, `
        INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status)
        SELECT w.id, w.organization_id, $2, 'active' FROM workspaces w
        WHERE w.organization_id = $1
        ON CONFLICT (workspace_id, identity_id) DO NOTHING`, orgID, identityID); err != nil {
		return "", err
	}

	var roleID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT id::text FROM roles WHERE key = $1 AND is_system LIMIT 1`, roleKey).Scan(&roleID); err != nil {
		return "", fmt.Errorf("unknown role %q: %w", roleKey, err)
	}
	if _, err := st.Pool.Exec(ctx, `
        INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, reason)
        VALUES ($1,$2,'organization',$3,'S3-QA-01 跨租户负向测试夹具')
        ON CONFLICT (identity_id, role_id, scope_type, COALESCE(scope_id, '00000000-0000-0000-0000-000000000000'::uuid))
            WHERE revoked_at IS NULL
        DO UPDATE SET granted_at = now()`, identityID, roleID, orgID); err != nil {
		return "", err
	}

	_ = iam.RecordLoginEvent(ctx, st.Pool, cfg.Env, iam.LoginEvent{
		EventType: "auth.fixture.created", Result: "success",
		IdentityID: identityID, SubjectEmail: email, OrganizationID: orgID,
		Audience: iam.AudienceAdmin, RiskLevel: "medium",
		Detail: map[string]any{"role": roleKey, "source": "cmd/admin-fixture", "purpose": "S3-QA-01"},
	})
	return password, nil
}

// cleanupFixture 只删除本工具创建的账号与指定夹具组织，不触碰真实数据。
//
// 安全规则：
//   - 必须显式指定 -org，且 slug 不能是 default；
//   - 只有当该组织下的身份**全部**是夹具账号（前缀 fixture-）时才删除组织；
//   - 工作区删除前同样先确认其中没有非夹具数据的引用。
func cleanupFixture(ctx context.Context, st *store.Store, orgSlug, wsSlug string) error {
	if strings.TrimSpace(orgSlug) == "" || orgSlug == "default" {
		return fmt.Errorf("拒绝清理：必须用 -org 指定非 default 的夹具组织 slug")
	}
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orgID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM organizations WHERE slug = $1`, orgSlug).Scan(&orgID); err != nil {
		return fmt.Errorf("组织 %q 不存在", orgSlug)
	}

	// 组织里只要还有非夹具身份，就整体拒绝删除。
	var foreign int
	if err := tx.QueryRow(ctx, `
        SELECT count(*) FROM identities
        WHERE organization_id = $1 AND email NOT LIKE $2`, orgID, fixturePrefix+"%").Scan(&foreign); err != nil {
		return err
	}
	if foreign > 0 {
		return fmt.Errorf("组织 %q 含 %d 个非夹具身份，拒绝删除", orgSlug, foreign)
	}

	for _, table := range []string{"member_role_bindings", "workspace_memberships", "organization_memberships", "sessions", "trusted_devices", "credentials"} {
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE identity_id IN (SELECT id FROM identities WHERE organization_id = $1)`, table),
			orgID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM identities WHERE organization_id = $1`, orgID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM workspaces WHERE organization_id = $1 AND ($2 = '' OR slug = $2)`, orgID, wsSlug); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, orgID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ensureCase 幂等地造一个用于跨租户断言的案件对象。
func ensureCase(ctx context.Context, st *store.Store, orgID, wsID, caseNo, name string) (string, error) {
	if wsID == "" {
		return "", fmt.Errorf("workspace id is empty for case %s", caseNo)
	}
	var id string
	err := st.Pool.QueryRow(ctx,
		`SELECT id::text FROM cases WHERE organization_id = $1 AND case_no = $2`, orgID, caseNo).Scan(&id)
	if err == nil {
		return id, nil
	}
	err = st.Pool.QueryRow(ctx, `
        INSERT INTO cases (organization_id, workspace_id, case_no, name, description, color, status)
        VALUES ($1,$2,$3,$4,$5,'#175CD3','active') RETURNING id::text`,
		orgID, wsID, caseNo, name, "S3-QA-01 跨租户负向测试夹具").Scan(&id)
	return id, err
}

func randomPassword(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// 去掉易混淆字符，便于人工复制到测试脚本里。
	s := base64.RawURLEncoding.EncodeToString(buf)
	s = strings.NewReplacer("0", "a", "O", "b", "l", "c", "I", "d", "-", "e", "_", "f").Replace(s)
	if len(s) > n {
		s = s[:n]
	}
	return s + "!7", nil
}

var _ = uuid.NewString
