// mfa-e2e-fixture：MFA 登录第二步「真实浏览器 + 真实后端」端到端回归的夹具工具。
//
// 为什么单独做一个工具：internal/api 的 live 测试在进程内直接调服务层，验证不了浏览器；
// 而浏览器回归需要一个**口令已知、MFA 已注册**的账号。这里把该账号准备好并把
// {email,password,secret} 以 JSON 打到 stdout，供前端 E2E 脚本使用；跑完再 teardown。
//
// 安全边界：
//   * 只碰专用夹具身份 mfa-live-fixture@example.invalid（非管理员、无角色绑定）；
//   * 口令每次随机生成，只存在于本次进程与 stdout，不落盘；
//   * teardown 会把夹具身份置为 disabled 并吊销其会话，使它在测试之外无法登录；
//   * 不删除该身份，也不删它写入的 login_events（审计链 append-only，删行会打断校验）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/mfa"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

const (
	fixtureEmail      = "mfa-live-fixture@example.invalid"
	adminFixtureEmail = "mfa-e2e-admin@example.invalid"
)

func main() {
	action := flag.String("action", "setup", "setup | teardown")
	flag.Parse()

	ctx := context.Background()
	cfg := config.Load()
	dsn := strings.TrimSpace(os.Getenv("MFA_LIVE_DSN"))
	if dsn == "" {
		dsn = cfg.MigrationDatabaseURL
	}
	if dsn == "" {
		dsn = cfg.DatabaseURL
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "postgres connect failed:", err)
		os.Exit(1)
	}
	defer st.Close()

	switch *action {
	case "setup":
		if err := setup(ctx, st, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "setup failed:", err)
			os.Exit(1)
		}
	case "teardown":
		if err := teardown(ctx, st); err != nil {
			fmt.Fprintln(os.Stderr, "teardown failed:", err)
			os.Exit(1)
		}
		fmt.Println("{\"teardown\":true}")
	default:
		fmt.Fprintln(os.Stderr, "usage: mfa-e2e-fixture -action setup|teardown")
		os.Exit(2)
	}
}

// setup 创建/激活夹具身份、授予成员资格、写入并确认一份新的 TOTP 密钥，并打印口令与密钥。
func setup(ctx context.Context, st *store.Store, cfg *config.Config) error {
	var org, ws string
	if err := st.Pool.QueryRow(ctx, `SELECT organization_id::text, workspace_id::text
		  FROM workspace_memberships WHERE status = 'active' ORDER BY joined_at LIMIT 1`).Scan(&org, &ws); err != nil {
		return fmt.Errorf("pick fixture organization/workspace: %w", err)
	}

	var identityID string
	err := st.Pool.QueryRow(ctx, `SELECT id::text FROM identities WHERE lower(email) = $1`, fixtureEmail).Scan(&identityID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		identityID = uuid.NewString()
		if _, err := st.Pool.Exec(ctx, `INSERT INTO identities (id, organization_id, email, display_name, status)
			VALUES ($1::uuid, $2::uuid, $3, 'MFA Live Fixture', 'active')`, identityID, org, fixtureEmail); err != nil {
			return fmt.Errorf("insert fixture identity: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read fixture identity: %w", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE identities SET status = 'active', failed_login_count = 0,
		locked_until = NULL, must_change_password = false, organization_id = $2::uuid WHERE id = $1::uuid`,
		identityID, org); err != nil {
		return fmt.Errorf("activate fixture identity: %w", err)
	}

	password := "MfaE2E!" + uuid.NewString()
	hash, err := iam.HashPassword(password, iam.DefaultParams())
	if err != nil {
		return fmt.Errorf("hash fixture password: %w", err)
	}
	if err := iam.NewRepo(st.Pool).SetPassword(ctx, identityID, hash, false); err != nil {
		return fmt.Errorf("set fixture password: %w", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'active')
		ON CONFLICT (workspace_id, identity_id) DO UPDATE SET status = 'active'`, ws, org, identityID); err != nil {
		return fmt.Errorf("grant fixture workspace membership: %w", err)
	}

	// 组织成员资格：真实用户的角色都挂在 organization_memberships 之上
	// （EffectiveRoles 会 join 这张表），缺少它会让会话拿不到任何角色。
	if _, err := st.Pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id, identity_id, status)
		VALUES ($1::uuid, $2::uuid, 'active')
		ON CONFLICT (organization_id, identity_id) DO UPDATE SET status = 'active'`, org, identityID); err != nil {
		return fmt.Errorf("grant fixture organization membership: %w", err)
	}

	// 角色绑定：真实用户必然至少有一个角色。没有绑定的身份会让工作台在渲染期
	// （roles.join）抛异常，无法作为端到端夹具，因此这里补一个最小可用角色。
	if _, err := st.Pool.Exec(ctx, `INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, reason)
		SELECT $1::uuid, ro.id, 'workspace', $2::uuid, 'mfa e2e fixture' FROM roles ro WHERE ro.key = 'investigator'
		ON CONFLICT DO NOTHING`, identityID, ws); err != nil {
		return fmt.Errorf("grant fixture role: %w", err)
	}

	secret, err := mfa.NewSecret()
	if err != nil {
		return fmt.Errorf("generate fixture secret: %w", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO identity_mfa (identity_id, secret, confirmed_at, last_used_step)
		VALUES ($1::uuid, $2, now(), -1)
		ON CONFLICT (identity_id) DO UPDATE SET secret = EXCLUDED.secret, confirmed_at = now(), last_used_step = -1`,
		identityID, secret); err != nil {
		return fmt.Errorf("enroll fixture MFA: %w", err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM mfa_login_challenges WHERE identity_id = $1::uuid`, identityID); err != nil {
		return fmt.Errorf("reset fixture challenges: %w", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = 'mfa_e2e_setup'
		WHERE identity_id = $1::uuid AND revoked_at IS NULL`, identityID); err != nil {
		return fmt.Errorf("revoke stale fixture sessions: %w", err)
	}

	adminEmail, adminPassword, adminID, err := ensureAdminFixture(ctx, st, org, ws)
	if err != nil {
		return err
	}

	out, err := json.Marshal(map[string]any{
		"email": fixtureEmail, "password": password, "secret": secret,
		"identity_id": identityID, "organization_id": org, "workspace_id": ws,
		"admin_email": adminEmail, "admin_password": adminPassword, "admin_identity_id": adminID,
		"digits": mfa.Digits, "period_seconds": mfa.PeriodSeconds, "env": cfg.Env,
	})
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// ensureAdminFixture 准备一个**能执行管理命令**的夹具：平台管理员、已知口令、不注册 MFA。
//
// 为什么需要它：reset-mfa 是管理面命令，必须用真实管理员会话来验证；而真实管理员（admin@local）
// 的口令只在运维手里，端到端回归不能也不应该使用它。该身份与目标夹具同规格：
// 测试之外一律 disabled、无角色绑定、无成员资格。
func ensureAdminFixture(ctx context.Context, st *store.Store, org, ws string) (string, string, string, error) {
	var identityID string
	err := st.Pool.QueryRow(ctx, `SELECT id::text FROM identities WHERE lower(email) = $1`, adminFixtureEmail).Scan(&identityID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		identityID = uuid.NewString()
		if _, err := st.Pool.Exec(ctx, `INSERT INTO identities (id, organization_id, email, display_name, status, is_platform_admin)
			VALUES ($1::uuid, $2::uuid, $3, 'MFA E2E Admin', 'active', true)`, identityID, org, adminFixtureEmail); err != nil {
			return "", "", "", fmt.Errorf("insert admin fixture: %w", err)
		}
	case err != nil:
		return "", "", "", fmt.Errorf("read admin fixture: %w", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE identities SET status = 'active', is_platform_admin = true,
		failed_login_count = 0, locked_until = NULL, must_change_password = false, organization_id = $2::uuid
		WHERE id = $1::uuid`, identityID, org); err != nil {
		return "", "", "", fmt.Errorf("activate admin fixture: %w", err)
	}
	password := "MfaAdmin!" + uuid.NewString()
	hash, err := iam.HashPassword(password, iam.DefaultParams())
	if err != nil {
		return "", "", "", fmt.Errorf("hash admin fixture password: %w", err)
	}
	if err := iam.NewRepo(st.Pool).SetPassword(ctx, identityID, hash, false); err != nil {
		return "", "", "", fmt.Errorf("set admin fixture password: %w", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id, identity_id, status)
		VALUES ($1::uuid, $2::uuid, 'active')
		ON CONFLICT (organization_id, identity_id) DO UPDATE SET status = 'active'`, org, identityID); err != nil {
		return "", "", "", fmt.Errorf("grant admin fixture organization membership: %w", err)
	}
	// 工作区成员资格必须有：ResolveLoginWorkspace 只认 workspace_memberships，
	// 缺了它管理员登录会以 no_workspace_membership 被 403 拒绝（不是权限问题）。
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'active')
		ON CONFLICT (workspace_id, identity_id) DO UPDATE SET status = 'active'`, ws, org, identityID); err != nil {
		return "", "", "", fmt.Errorf("grant admin fixture workspace membership: %w", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, reason)
		SELECT $1::uuid, ro.id, 'organization', $2::uuid, 'mfa e2e admin fixture' FROM roles ro WHERE ro.key = 'platform_admin'
		ON CONFLICT DO NOTHING`, identityID, org); err != nil {
		return "", "", "", fmt.Errorf("grant admin fixture role: %w", err)
	}
	// 管理员夹具不注册 MFA：本用例验证的是管理命令本身，不是第二步。
	if _, err := st.Pool.Exec(ctx, `DELETE FROM identity_mfa WHERE identity_id = $1::uuid`, identityID); err != nil {
		return "", "", "", fmt.Errorf("clear admin fixture mfa: %w", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = 'mfa_e2e_setup'
		WHERE identity_id = $1::uuid AND revoked_at IS NULL`, identityID); err != nil {
		return "", "", "", fmt.Errorf("revoke stale admin fixture sessions: %w", err)
	}
	return adminFixtureEmail, password, identityID, nil
}

// teardown 让**两个**夹具在测试之外都无法登录：停用身份、吊销会话、移除成员资格与角色、清理挑战。
func teardown(ctx context.Context, st *store.Store) error {
	for _, email := range []string{fixtureEmail, adminFixtureEmail} {
		if err := retireFixture(ctx, st, email); err != nil {
			return err
		}
	}
	return nil
}

func retireFixture(ctx context.Context, st *store.Store, email string) error {
	var identityID string
	err := st.Pool.QueryRow(ctx, `SELECT id::text FROM identities WHERE lower(email) = $1`, email).Scan(&identityID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if identityID == "" {
		return nil
	}
	// 夹具在测试之外必须无角色可用：撤销绑定并移除两层成员资格（身份本身也会被停用）。
	if _, err := st.Pool.Exec(ctx, `UPDATE organization_memberships SET status = 'removed' WHERE identity_id = $1::uuid`, identityID); err != nil {
		return err
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE workspace_memberships SET status = 'removed' WHERE identity_id = $1::uuid`, identityID); err != nil {
		return err
	}
	// 夹具在测试之外必须无角色可用：撤销本次授予的绑定。
	if _, err := st.Pool.Exec(ctx, `UPDATE member_role_bindings SET revoked_at = now() WHERE identity_id = $1::uuid AND revoked_at IS NULL`, identityID); err != nil {
		return err
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE identities SET status = 'disabled' WHERE id = $1::uuid`, identityID); err != nil {
		return err
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = 'mfa_e2e_cleanup'
		WHERE identity_id = $1::uuid AND revoked_at IS NULL`, identityID); err != nil {
		return err
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM mfa_login_challenges WHERE identity_id = $1::uuid`, identityID); err != nil {
		return err
	}
	// 平台管理员标记也要收回：夹具不能长期是一个可跨组织查看的账号。
	if _, err := st.Pool.Exec(ctx, `UPDATE identities SET is_platform_admin = false WHERE id = $1::uuid`, identityID); err != nil {
		return err
	}
	return nil
}
