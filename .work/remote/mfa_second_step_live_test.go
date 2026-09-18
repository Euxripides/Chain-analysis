
//go:build live

// AUTH-004 登录第二步的真实数据库证据（-tags live）。
//
// 覆盖 §27 AUTH-004 的三条验收点：challenge 单次使用、失败计数、以及未注册 MFA 的
// identity 不受影响。全部驱动生产代码路径：iam.Service.Login（注入与生产完全相同的
// api.newMFAChallengeGate 适配器）、mfa.Store.ConsumeLoginChallenge、
// iam.Service.CompleteMFALogin。
//
// 关于夹具（重要）：
//   - 使用专用夹具身份 mfa-live-fixture@example.invalid，非管理员、无角色绑定；
//   - 每次运行开始时把它置为 active，结束时置回 disabled，因此它在测试之外无法登录；
//   - **不删除**该身份，也不删除它写入的 login_events：login_events 是 append-only
//     哈希链（audit.ChainAppend），删行会打断链式校验，而 identities 又被 login_events
//     以 NO ACTION 外键引用（删除身份本身也不可能）。测试期间产生的登录事件是真实记录，
//     保留它们是唯一不破坏审计完整性的做法。
//   - 只读写自己创建的夹具行：身份 / 凭据 / 工作区成员资格 / identity_mfa /
//     mfa_recovery_codes / mfa_login_challenges / 该身份的 sessions。
//
// 运行（VM）：
//
//	set -a; . deploy/.env; set +a
//	MFA_LIVE_DSN="$DATABASE_URL" go test -tags live -count=1 -v \
//	  -run TestMFASecondStepLive ./internal/api/
package api

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/mfa"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

const mfaLiveFixtureEmail = "mfa-live-fixture@example.invalid"

const mfaLiveAdminEmail = "mfa-e2e-admin@example.invalid" // mfaLiveAdminFixture 准备一个能执行管理命令的夹具：平台管理员、已知口令、不注册 MFA。
// 真实管理员（admin@local）的口令只在运维手里，端到端回归不能也不应该使用它；
// 该夹具在测试之外一律 disabled、无角色绑定、无成员资格、非平台管理员。
func mfaLiveAdminFixture(t *testing.T, ctx context.Context, st *store.Store, org, ws string) (string, string) {
	t.Helper()
	var identityID string
	err := st.Pool.QueryRow(ctx, `SELECT id::text FROM identities WHERE lower(email) = $1`, mfaLiveAdminEmail).Scan(&identityID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		identityID = uuid.NewString()
		if _, err := st.Pool.Exec(ctx, `INSERT INTO identities (id, organization_id, email, display_name, status, is_platform_admin)
			VALUES ($1::uuid, $2::uuid, $3, 'MFA E2E Admin', 'active', true)`, identityID, org, mfaLiveAdminEmail); err != nil {
			t.Fatalf("insert admin fixture: %v", err)
		}
	case err != nil:
		t.Fatalf("read admin fixture: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE identities SET status = 'active', is_platform_admin = true,
		failed_login_count = 0, locked_until = NULL, must_change_password = false, organization_id = $2::uuid
		WHERE id = $1::uuid`, identityID, org); err != nil {
		t.Fatalf("activate admin fixture: %v", err)
	}
	password := "MfaAdmin!" + uuid.NewString()
	hash, err := iam.HashPassword(password, iam.DefaultParams())
	if err != nil {
		t.Fatalf("hash admin fixture password: %v", err)
	}
	if err := iam.NewRepo(st.Pool).SetPassword(ctx, identityID, hash, false); err != nil {
		t.Fatalf("set admin fixture password: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id, identity_id, status)
		VALUES ($1::uuid, $2::uuid, 'active')
		ON CONFLICT (organization_id, identity_id) DO UPDATE SET status = 'active'`, org, identityID); err != nil {
		t.Fatalf("grant admin fixture organization membership: %v", err)
	}
	// 工作区成员资格必须有：ResolveLoginWorkspace 只认 workspace_memberships，
	// 缺了它管理员登录会以 no_workspace_membership 被 403 拒绝（不是权限问题）。
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'active')
		ON CONFLICT (workspace_id, identity_id) DO UPDATE SET status = 'active'`, ws, org, identityID); err != nil {
		t.Fatalf("grant admin fixture workspace membership: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, reason)
		SELECT $1::uuid, ro.id, 'organization', $2::uuid, 'mfa live admin fixture' FROM roles ro WHERE ro.key = 'platform_admin'
		ON CONFLICT DO NOTHING`, identityID, org); err != nil {
		t.Fatalf("grant admin fixture role: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM identity_mfa WHERE identity_id = $1::uuid`, identityID); err != nil {
		t.Fatalf("clear admin fixture mfa: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = 'mfa_live_setup'
		WHERE identity_id = $1::uuid AND revoked_at IS NULL`, identityID); err != nil {
		t.Fatalf("revoke stale admin fixture sessions: %v", err)
	}
	return identityID, password
}

type mfaLiveRig struct {
	ctx        context.Context
	st         *store.Store
	svc        *iam.Service
	mfa        *mfa.Store
	identityID string
	secret     string
	password   string
	// 管理面夹具：reset-mfa 是管理命令，必须用真实管理员会话验证。
	org           string
	adminID       string
	adminEmail    string
	adminPassword string
}

func mfaLiveSetup(t *testing.T) *mfaLiveRig {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("MFA_LIVE_DSN"))
	if dsn == "" {
		t.Skip("MFA_LIVE_DSN not set")
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(st.Close)

	var org, ws string
	if err := st.Pool.QueryRow(ctx, `SELECT organization_id::text, workspace_id::text
		  FROM workspace_memberships WHERE status = 'active' ORDER BY joined_at LIMIT 1`).Scan(&org, &ws); err != nil {
		t.Fatalf("pick fixture organization/workspace: %v", err)
	}

	var identityID string
	err = st.Pool.QueryRow(ctx, `SELECT id::text FROM identities WHERE lower(email) = $1`, mfaLiveFixtureEmail).Scan(&identityID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		identityID = uuid.NewString()
		if _, err := st.Pool.Exec(ctx, `INSERT INTO identities (id, organization_id, email, display_name, status)
			VALUES ($1::uuid, $2::uuid, $3, 'MFA Live Fixture', 'active')`, identityID, org, mfaLiveFixtureEmail); err != nil {
			t.Fatalf("insert fixture identity: %v", err)
		}
	case err != nil:
		t.Fatalf("read fixture identity: %v", err)
	}
	// 上一轮结束时会被置为 disabled；这里恢复为 active（幂等）。
	if _, err := st.Pool.Exec(ctx, `UPDATE identities SET status = 'active', failed_login_count = 0,
		locked_until = NULL, must_change_password = false, organization_id = $2::uuid WHERE id = $1::uuid`,
		identityID, org); err != nil {
		t.Fatalf("activate fixture identity: %v", err)
	}

	random := uuid.NewString()
	password := "MfaLive!" + random
	hash, err := iam.HashPassword(password, iam.DefaultParams())
	if err != nil {
		t.Fatalf("hash fixture password: %v", err)
	}
	repo := iam.NewRepo(st.Pool)
	if err := repo.SetPassword(ctx, identityID, hash, false); err != nil {
		t.Fatalf("set fixture password: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'active')
		ON CONFLICT (workspace_id, identity_id) DO UPDATE SET status = 'active'`, ws, org, identityID); err != nil {
		t.Fatalf("grant fixture workspace membership: %v", err)
	}

	secret, err := mfa.NewSecret()
	if err != nil {
		t.Fatalf("generate fixture secret: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO identity_mfa (identity_id, secret, confirmed_at, last_used_step)
		VALUES ($1::uuid, $2, now(), -1)
		ON CONFLICT (identity_id) DO UPDATE SET secret = EXCLUDED.secret, confirmed_at = now(), last_used_step = -1`,
		identityID, secret); err != nil {
		t.Fatalf("enroll fixture MFA: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE identity_id = $1::uuid`, identityID); err != nil {
		t.Fatalf("reset fixture recovery codes: %v", err)
	}
	// 真实注册流程会一次性下发 10 个恢复码；夹具也放几个未使用的，
	// 否则「重置删掉了多少恢复码」这条回执永远是 0，测不出 Summary 的计数。
	_, fixtureDigests, err := mfa.RecoveryCodes(3)
	if err != nil {
		t.Fatalf("generate fixture recovery codes: %v", err)
	}
	for _, digest := range fixtureDigests {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO mfa_recovery_codes (identity_id, digest) VALUES ($1::uuid, $2)`,
			identityID, digest[:]); err != nil {
			t.Fatalf("insert fixture recovery code: %v", err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM mfa_login_challenges WHERE identity_id = $1::uuid`, identityID); err != nil {
		t.Fatalf("reset fixture challenges: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = 'mfa_live_setup'
		WHERE identity_id = $1::uuid AND revoked_at IS NULL`, identityID); err != nil {
		t.Fatalf("revoke stale fixture sessions: %v", err)
	}

	sessionCfg := iam.DefaultSessionConfig()
	sessionCfg.IdleTTL = time.Hour
	sessionCfg.AbsoluteTTL = 2 * time.Hour
	cfg := iam.DefaultConfig()
	cfg.Environment = "test"
	cfg.MaxFailedLogins = 50
	cfg.LoginRatePerMinute = 1000
	svc := iam.NewService(repo, iam.NewSessionManager(repo, sessionCfg), cfg, nil)
	mfaStore := mfa.NewStore(st.Pool)
	// 与 bootstrap.go 完全相同的接线：生产里也是这一行。
	svc.SetMFAChallengeGate(newMFAChallengeGate(mfaStore))

	adminID, adminPassword := mfaLiveAdminFixture(t, ctx, st, org, ws)

	rig := &mfaLiveRig{
		ctx: ctx, st: st, svc: svc, mfa: mfaStore, identityID: identityID, secret: secret, password: password,
		org: org, adminID: adminID, adminEmail: mfaLiveAdminEmail, adminPassword: adminPassword,
	}
	t.Cleanup(func() {
		// 夹具在测试之外必须无法登录：停用身份并吊销本次运行的会话。
		_, _ = st.Pool.Exec(ctx, `UPDATE identities SET status = 'disabled' WHERE id = $1::uuid`, identityID)
		_, _ = st.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = 'mfa_live_cleanup'
			WHERE identity_id = $1::uuid AND revoked_at IS NULL`, identityID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM mfa_login_challenges WHERE identity_id = $1::uuid`, identityID)
		// 管理夹具同样必须彻底失效：停用 + 收回平台管理员标记 + 撤销角色绑定与成员资格。
		_, _ = st.Pool.Exec(ctx, `UPDATE identities SET status = 'disabled', is_platform_admin = false WHERE id = $1::uuid`, adminID)
		_, _ = st.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = 'mfa_live_cleanup' WHERE identity_id = $1::uuid AND revoked_at IS NULL`, adminID)
		_, _ = st.Pool.Exec(ctx, `UPDATE member_role_bindings SET revoked_at = now() WHERE identity_id = $1::uuid AND revoked_at IS NULL`, adminID)
		_, _ = st.Pool.Exec(ctx, `UPDATE organization_memberships SET status = 'removed' WHERE identity_id = $1::uuid`, adminID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM mfa_login_challenges WHERE identity_id = $1::uuid`, adminID)
	})
	return rig
}

func (r *mfaLiveRig) login(t *testing.T) iam.LoginResult {
	t.Helper()
	res, err := r.svc.Login(r.ctx, iam.LoginInput{
		Email: mfaLiveFixtureEmail, Password: r.password, Audience: iam.AudienceUser,
		IP:        iam.ClientIP{ClientIP: "127.0.0.1", PeerIP: "127.0.0.1", ProxyChain: []string{}},
		UserAgent: "mfa-live-test/1.0", EntryHost: "127.0.0.1", Scheme: "http",
		RequestID: uuid.NewString(), TraceID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return res
}

func (r *mfaLiveRig) liveSessions(t *testing.T) int {
	t.Helper()
	var n int
	if err := r.st.Pool.QueryRow(r.ctx, `SELECT count(*) FROM sessions
		WHERE identity_id = $1::uuid AND revoked_at IS NULL`, r.identityID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func (r *mfaLiveRig) setStep(t *testing.T, step int64) {
	t.Helper()
	if _, err := r.st.Pool.Exec(r.ctx, `UPDATE identity_mfa SET last_used_step = $2 WHERE identity_id = $1::uuid`,
		r.identityID, step); err != nil {
		t.Fatalf("set fixture last_used_step: %v", err)
	}
}

func (r *mfaLiveRig) attempts(t *testing.T, token string) (int, bool) {
	t.Helper()
	var attempts int
	var consumed *time.Time
	if err := r.st.Pool.QueryRow(r.ctx, `SELECT attempts, consumed_at FROM mfa_login_challenges WHERE token_hash = $1`,
		mfa.HashChallengeToken(token)).Scan(&attempts, &consumed); err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	return attempts, consumed != nil
}

func (r *mfaLiveRig) code(t *testing.T, step int64) string {
	t.Helper()
	code, err := mfa.CodeAt(r.secret, uint64(step), mfa.Digits)
	if err != nil {
		t.Fatalf("generate totp: %v", err)
	}
	return code
}

func (r *mfaLiveRig) wrongCode(t *testing.T, good string) string {
	t.Helper()
	if good != "000000" {
		return "000000"
	}
	return "111111"
}

func (r *mfaLiveRig) complete(t *testing.T, state mfa.ChallengeState, method string) iam.LoginResult {
	t.Helper()
	res, err := r.svc.CompleteMFALogin(r.ctx, iam.MFACompletion{
		IdentityID: state.IdentityID, Audience: state.Audience,
		OrganizationID: state.OrganizationID, WorkspaceID: state.WorkspaceID,
		ClientIP: state.ClientIP, PeerIP: state.PeerIP, ProxyChain: state.ProxyChain,
		UserAgent: state.UserAgent, EntryHost: state.EntryHost, Scheme: state.Scheme,
		Method: method, RequestID: uuid.NewString(), TraceID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("complete mfa login: %v", err)
	}
	return res
}

// TestMFASecondStepLive 是 AUTH-004 的端到端证据：单次使用、失败计数、未注册身份不受影响。
func TestMFASecondStepLive(t *testing.T) {
	rig := mfaLiveSetup(t)
	ctx, st := rig.ctx, rig.st
	step := func() int64 { return time.Now().Unix() / mfa.PeriodSeconds }

	// ---- 1) 密码通过但登录未完成：返回挑战而不是会话 ----
	rig.setStep(t, -1)
	first := rig.login(t)
	if !first.MFARequired {
		t.Fatal("已注册 MFA 的身份在密码通过后必须返回挑战，而不是直接建会话")
	}
	if strings.TrimSpace(first.ChallengeID) == "" || first.ChallengeExpiresAt.IsZero() {
		t.Fatal("挑战响应必须带 challenge_id 与过期时间")
	}
	if first.Session.ID != "" || first.Token != "" || first.CSRF != "" {
		t.Fatal("返回挑战时不得携带任何会话或令牌")
	}
	if got := rig.liveSessions(t); got != 0 {
		t.Fatalf("第一步之后未吊销会话数 = %d, want 0（密码正确不等于登录完成）", got)
	}
	if ttl := time.Until(first.ChallengeExpiresAt); ttl > mfa.ChallengeTTL+time.Minute || ttl < mfa.ChallengeTTL-time.Minute {
		t.Fatalf("挑战有效期 %s 偏离契约 %s", ttl, mfa.ChallengeTTL)
	}
	t.Logf("PASS 1 挑战已签发 expires_in=%s 未吊销会话数=0", time.Until(first.ChallengeExpiresAt).Round(time.Second))

	// ---- 2) 验证码错误：拒绝 + 失败计数 +1 + 仍然没有会话 ----
	good := rig.code(t, step())
	state, method, err := rig.mfa.ConsumeLoginChallenge(ctx, first.ChallengeID, rig.wrongCode(t, good))
	if !errors.Is(err, mfa.ErrInvalidCode) {
		t.Fatalf("错误验证码必须返回 ErrInvalidCode, got %v", err)
	}
	if method != "" {
		t.Fatalf("失败时不得返回通过方式, got %q", method)
	}
	if state.IdentityID != rig.identityID {
		t.Fatal("失败时必须回填身份，否则失败侧审计无法记账")
	}
	if attempts, consumed := rig.attempts(t, first.ChallengeID); attempts != 1 || consumed {
		t.Fatalf("失败计数 attempts=%d consumed=%v, want 1/false", attempts, consumed)
	}
	if got := rig.liveSessions(t); got != 0 {
		t.Fatalf("验证码错误后未吊销会话数 = %d, want 0", got)
	}
	t.Logf("PASS 2 错误验证码被拒 attempts=1 consumed=false 未吊销会话数=0")

	// ---- 3) 正确验证码：建立会话且 MFASatisfied=true ----
	state, method, err = rig.mfa.ConsumeLoginChallenge(ctx, first.ChallengeID, good)
	if err != nil {
		t.Fatalf("正确验证码必须通过: %v", err)
	}
	if method != mfa.ChallengeMethodTOTP {
		t.Fatalf("通过方式 = %q, want %q", method, mfa.ChallengeMethodTOTP)
	}
	result := rig.complete(t, state, method)
	if !result.Session.MFASatisfied {
		t.Fatal("第二步通过后的会话必须 MFASatisfied=true")
	}
	if result.Session.AuthMethod != "password+mfa" {
		t.Fatalf("会话 auth_method = %q, want password+mfa", result.Session.AuthMethod)
	}
	if result.Session.IdentityID != rig.identityID {
		t.Fatalf("会话身份 = %s, want 夹具身份 %s", result.Session.IdentityID, rig.identityID)
	}
	if result.Token == "" || result.CSRF == "" {
		t.Fatal("第二步通过后必须下发令牌与 CSRF")
	}
	if got := rig.liveSessions(t); got != 1 {
		t.Fatalf("第二步通过后未吊销会话数 = %d, want 1", got)
	}
	t.Logf("PASS 3 第二步通过 session=%s auth_method=%s mfa_satisfied=true", result.Session.ID, result.Session.AuthMethod)

	// ---- 4) challenge 单次使用 ----
	if _, _, err := rig.mfa.ConsumeLoginChallenge(ctx, first.ChallengeID, rig.code(t, step())); !errors.Is(err, mfa.ErrChallengeInvalid) {
		t.Fatalf("已消费的挑战必须拒绝再次提交, got %v", err)
	}
	if _, consumed := rig.attempts(t, first.ChallengeID); !consumed {
		t.Fatal("消费后的挑战必须带 consumed_at")
	}
	if got := rig.liveSessions(t); got != 1 {
		t.Fatalf("重放已消费的挑战不得新建会话, 会话数 = %d, want 1", got)
	}
	t.Logf("PASS 4 挑战单次使用：重放返回 ErrChallengeInvalid 且未新建会话")

	// ---- 5) TOTP 防重放：同一个时间步的码不能在第二个挑战上复用 ----
	second := rig.login(t)
	if !second.MFARequired {
		t.Fatal("第二次登录同样必须要求第二步")
	}
	rig.setStep(t, step())
	if _, _, err := rig.mfa.ConsumeLoginChallenge(ctx, second.ChallengeID, rig.code(t, step())); !errors.Is(err, mfa.ErrInvalidCode) {
		t.Fatalf("同一步的验证码必须只能成功一次, got %v", err)
	}
	if got := rig.liveSessions(t); got != 1 {
		t.Fatalf("TOTP 重放不得新建会话, 会话数 = %d, want 1", got)
	}
	t.Logf("PASS 5 同一步验证码重放被拒（last_used_step 防重放）")

	// ---- 6) 恢复码：只能用一次 ----
	plain, digests, err := mfa.RecoveryCodes(1)
	if err != nil || len(plain) != 1 {
		t.Fatalf("generate recovery code: %v", err)
	}
	if !mfa.RecoveryCodeMatches(plain[0], digests[0]) {
		t.Fatal("恢复码与其摘要不匹配，夹具无效")
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO mfa_recovery_codes (identity_id, digest) VALUES ($1::uuid, $2)`,
		rig.identityID, digests[0][:]); err != nil {
		t.Fatalf("insert fixture recovery code: %v", err)
	}
	rig.setStep(t, -1)
	third := rig.login(t)
	state3, method3, err := rig.mfa.ConsumeLoginChallenge(ctx, third.ChallengeID, plain[0])
	if err != nil {
		t.Fatalf("未使用的恢复码必须可用: %v", err)
	}
	if method3 != mfa.ChallengeMethodRecoveryCode {
		t.Fatalf("通过方式 = %q, want %q", method3, mfa.ChallengeMethodRecoveryCode)
	}
	_ = rig.complete(t, state3, method3)
	if got := rig.liveSessions(t); got != 2 {
		t.Fatalf("恢复码登录后未吊销会话数 = %d, want 2", got)
	}
	fourth := rig.login(t)
	if _, _, err := rig.mfa.ConsumeLoginChallenge(ctx, fourth.ChallengeID, plain[0]); !errors.Is(err, mfa.ErrInvalidCode) {
		t.Fatalf("用过的恢复码必须失效, got %v", err)
	}
	t.Logf("PASS 6 恢复码可用一次：重复使用返回 ErrInvalidCode")

	// ---- 7) 过期挑战 ----
	fifth := rig.login(t)
	if _, err := st.Pool.Exec(ctx, `UPDATE mfa_login_challenges SET expires_at = now() - interval '1 second' WHERE token_hash = $1`,
		mfa.HashChallengeToken(fifth.ChallengeID)); err != nil {
		t.Fatalf("expire fixture challenge: %v", err)
	}
	rig.setStep(t, -1)
	if _, _, err := rig.mfa.ConsumeLoginChallenge(ctx, fifth.ChallengeID, rig.code(t, step())); !errors.Is(err, mfa.ErrChallengeInvalid) {
		t.Fatalf("过期挑战即使验证码正确也必须拒绝, got %v", err)
	}
	t.Logf("PASS 7 过期挑战被拒（即使验证码正确）")

	// ---- 8) 失败计数：达到上限后即使验证码正确也拒绝 ----
	sixth := rig.login(t)
	good6 := rig.code(t, step())
	wrong6 := rig.wrongCode(t, good6)
	for i := 0; i < mfa.ChallengeMaxAttempts; i++ {
		_, _, err := rig.mfa.ConsumeLoginChallenge(ctx, sixth.ChallengeID, wrong6)
		if !errors.Is(err, mfa.ErrInvalidCode) {
			t.Fatalf("第 %d 次错误验证码应返回 ErrInvalidCode, got %v", i+1, err)
		}
	}
	if attempts, consumed := rig.attempts(t, sixth.ChallengeID); attempts != mfa.ChallengeMaxAttempts || consumed {
		t.Fatalf("上限后的计数 attempts=%d consumed=%v, want %d/false", attempts, consumed, mfa.ChallengeMaxAttempts)
	}
	rig.setStep(t, -1)
	if _, _, err := rig.mfa.ConsumeLoginChallenge(ctx, sixth.ChallengeID, good6); !errors.Is(err, mfa.ErrChallengeExhausted) {
		t.Fatalf("尝试次数用尽后必须拒绝（含正确验证码）, got %v", err)
	}
	if attempts, _ := rig.attempts(t, sixth.ChallengeID); attempts != mfa.ChallengeMaxAttempts {
		t.Fatalf("被拒绝的提交不得再计数, attempts=%d", attempts)
	}
	if got := rig.liveSessions(t); got != 2 {
		t.Fatalf("用尽的挑战不得新建会话, 会话数 = %d, want 2", got)
	}
	t.Logf("PASS 8 失败计数上限 %d：第 %d 次起一律拒绝（含正确验证码）", mfa.ChallengeMaxAttempts, mfa.ChallengeMaxAttempts+1)

	// ---- 9) 未注册 MFA 的身份不受影响 ----
	if _, err := st.Pool.Exec(ctx, `DELETE FROM identity_mfa WHERE identity_id = $1::uuid`, rig.identityID); err != nil {
		t.Fatalf("unenroll fixture MFA: %v", err)
	}
	seventh := rig.login(t)
	if seventh.MFARequired {
		t.Fatal("未注册 MFA 的身份不得被要求第二步")
	}
	if seventh.Session.ID == "" || seventh.Token == "" || seventh.CSRF == "" {
		t.Fatal("未注册 MFA 的身份必须一次性拿到完整会话")
	}
	if got := rig.liveSessions(t); got != 3 {
		t.Fatalf("未注册 MFA 的登录后会话数 = %d, want 3", got)
	}
	t.Logf("PASS 9 未注册 MFA 的身份直接登录，不受第二步影响 session=%s", seventh.Session.ID)

	// ---- 10) 事件证据（§6.6.1：MFA 成功/失败与挑战都必须留痕） ----
	counts := map[string]int{}
	for _, q := range []struct{ name, sql string }{
		{"challenge", `SELECT count(*) FROM login_events WHERE identity_id = $1::uuid AND event_type = 'auth.login.denied' AND reason_code = 'mfa_required'`},
		{"mfa_failure", `SELECT count(*) FROM login_events WHERE identity_id = $1::uuid AND event_type = 'auth.mfa.failure'`},
		{"mfa_success", `SELECT count(*) FROM login_events WHERE identity_id = $1::uuid AND event_type = 'auth.mfa.success'`},
		{"login_success", `SELECT count(*) FROM login_events WHERE identity_id = $1::uuid AND event_type = 'auth.login.success'`},
	} {
		var n int
		if err := st.Pool.QueryRow(ctx, q.sql, rig.identityID).Scan(&n); err != nil {
			t.Fatalf("count %s events: %v", q.name, err)
		}
		counts[q.name] = n
	}
	t.Logf("AUTH-004 事件统计: challenge=%d mfa_failure=%d mfa_success=%d login_success=%d",
		counts["challenge"], counts["mfa_failure"], counts["mfa_success"], counts["login_success"])
	if counts["challenge"] < 6 {
		t.Fatalf("挑战事件 %d 条，少于本次运行签发的挑战数 6", counts["challenge"])
	}
	// auth.mfa.failure 由 HTTP 层（Server.recordMFAVerifyFailure）写入；本测试直接驱动服务层，
	// 因此这里只记录计数，失败事件的证据由部署后的 HTTP 探针（202/401/429）给出。
	if counts["mfa_success"] < 2 {
		t.Fatalf("MFA 成功事件 %d 条，want >= 2", counts["mfa_success"])
	}
	t.Logf("ALL PASS AUTH-004 登录第二步夹具=%s", rig.identityID)
}
