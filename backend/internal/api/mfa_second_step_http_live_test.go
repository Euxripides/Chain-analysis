//go:build live

// AUTH-004 登录第二步的 HTTP 端到端证据：打**已部署的 API 进程**，不是进程内调用。
//
// 与 TestMFASecondStepLive 的分工：那个测试证明服务层语义（单次使用/失败计数/防重放），
// 这个测试证明线上契约（202 + challenge_id、401/429 错误码、Cookie 只在第二步通过后下发）；
// 两者的夹具同一套，都只读写自己创建的夹具行。
//
// 运行（VM）：
//
//	set -a; . deploy/.env; set +a
//	MFA_LIVE_DSN="$MIGRATION_DATABASE_URL" MFA_LIVE_BASE=http://127.0.0.1:8080 \
//	  go test -tags live -count=1 -v -run TestMFASecondStepHTTPLive ./internal/api/
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/mfa"
)

type mfaHTTPEnvelope struct {
	Success bool           `json:"success"`
	Data    map[string]any `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type mfaHTTPClient struct {
	t      *testing.T
	base   string
	client *http.Client
}

func mfaLiveHTTPClient(t *testing.T) *mfaHTTPClient {
	t.Helper()
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MFA_LIVE_BASE")), "/")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &mfaHTTPClient{t: t, base: base, client: &http.Client{Timeout: 20 * time.Second, Jar: jar}}
}

// post 返回 (状态码, 响应头, 信封)。
func (c *mfaHTTPClient) post(path string, body any) (int, http.Header, mfaHTTPEnvelope) {
	return c.postAuth(path, body, nil)
}

// postAuth 与 post 相同，但可附加请求头（管理面写操作需要 X-CSRF-Token）。
func (c *mfaHTTPClient) postAuth(path string, body any, headers map[string]string) (int, http.Header, mfaHTTPEnvelope) {
	c.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		c.t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		c.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		c.t.Fatalf("read %s: %v", path, err)
	}
	var env mfaHTTPEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		c.t.Fatalf("decode %s body %q: %v", path, string(payload), err)
	}
	return resp.StatusCode, resp.Header, env
}
func (c *mfaHTTPClient) get(path string) (int, mfaHTTPEnvelope) {
	c.t.Helper()
	resp, err := c.client.Get(c.base + path)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		c.t.Fatalf("read %s: %v", path, err)
	}
	var env mfaHTTPEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		c.t.Fatalf("decode %s body %q: %v", path, string(payload), err)
	}
	return resp.StatusCode, env
}

func (c *mfaHTTPClient) errCode(env mfaHTTPEnvelope) string {
	if env.Error == nil {
		return ""
	}
	return env.Error.Code
}

func (c *mfaHTTPClient) str(env mfaHTTPEnvelope, key string) string {
	if env.Data == nil {
		return ""
	}
	v, _ := env.Data[key].(string)
	return v
}

func (c *mfaHTTPClient) hasSessionCookie() bool {
	base, err := url.Parse(c.base)
	if err != nil {
		c.t.Fatalf("parse base url %q: %v", c.base, err)
	}
	for _, ck := range c.client.Jar.Cookies(base) {
		if strings.Contains(ck.Name, "session") && ck.Value != "" {
			return true
		}
	}
	return false
}

func (c *mfaHTTPClient) clearCookies() {
	jar, _ := cookiejar.New(nil)
	c.client.Jar = jar
}

// TestMFASecondStepHTTPLive 打已部署进程，验证 §21.4 的线上契约。
func TestMFASecondStepHTTPLive(t *testing.T) {
	rig := mfaLiveSetup(t)
	ctx, st := rig.ctx, rig.st
	h := mfaLiveHTTPClient(t)
	loginPath := "/api/auth/v1/login"
	verifyPath := "/api/auth/v1/mfa/verify"
	login := func() (int, http.Header, mfaHTTPEnvelope) {
		return h.post(loginPath, map[string]any{
			"identifier": mfaLiveFixtureEmail, "password": rig.password, "audience": "user",
		})
	}
	good := func() string {
		code, err := mfa.CodeAt(rig.secret, uint64(time.Now().Unix()/mfa.PeriodSeconds), mfa.Digits)
		if err != nil {
			t.Fatalf("generate totp: %v", err)
		}
		return code
	}
	wrong := func() string {
		if c := good(); c != "000000" {
			return "000000"
		}
		return "111111"
	}

	// ---- 1) 密码通过 -> 202 + challenge_id，且不下发任何 Cookie ----
	h.clearCookies()
	status, _, env := login()
	if status != http.StatusAccepted {
		t.Fatalf("login status = %d, want 202（需要 MFA 时必须 202）; body=%+v", status, env)
	}
	if got := h.str(env, "state"); got != "mfa_required" {
		t.Fatalf("state = %q, want mfa_required", got)
	}
	challengeA := h.str(env, "challenge_id")
	if challengeA == "" {
		t.Fatal("202 响应必须带 challenge_id")
	}
	if h.hasSessionCookie() {
		t.Fatal("第一步之后不得下发会话 Cookie")
	}
	t.Logf("PASS 1 POST /login -> 202 state=mfa_required challenge=%s… 无会话 Cookie", challengeA[:8])

	// ---- 2) 错误验证码 -> 401 AUTHN_MFA_INVALID，仍然没有会话 ----
	status, _, env = h.post(verifyPath, map[string]any{"challenge_id": challengeA, "code": wrong()})
	if status != http.StatusUnauthorized || h.errCode(env) != "AUTHN_MFA_INVALID" {
		t.Fatalf("错误验证码 status=%d code=%q, want 401/AUTHN_MFA_INVALID", status, h.errCode(env))
	}
	if h.hasSessionCookie() {
		t.Fatal("验证码错误后不得下发会话 Cookie")
	}
	t.Logf("PASS 2 错误验证码 -> 401 AUTHN_MFA_INVALID 且无会话 Cookie")

	// ---- 3) 正确验证码 -> 200 authenticated + 会话 Cookie ----
	status, _, env = h.post(verifyPath, map[string]any{"challenge_id": challengeA, "code": good()})
	if status != http.StatusOK {
		t.Fatalf("正确验证码 status = %d, want 200; body=%+v", status, env)
	}
	if got := h.str(env, "state"); got != "authenticated" {
		t.Fatalf("state = %q, want authenticated", got)
	}
	if env.Data["mfa_satisfied"] != true {
		t.Fatalf("mfa_satisfied = %v, want true", env.Data["mfa_satisfied"])
	}
	if !h.hasSessionCookie() {
		t.Fatal("第二步通过后必须下发会话 Cookie")
	}
	if sid := h.str(env, "session_id"); sid == "" {
		t.Fatal("第二步通过后必须返回 session_id")
	} else {
		t.Logf("PASS 3 正确验证码 -> 200 authenticated session=%s mfa_satisfied=true 已下发会话 Cookie", sid)
	}

	// ---- 4) 已登录会话可见 mfa_satisfied / mfa_enrolled ----
	status, env = h.get("/api/auth/v1/session")
	if status != http.StatusOK {
		t.Fatalf("GET /session status = %d; body=%+v", status, env)
	}
	if env.Data["mfa_satisfied"] != true || env.Data["mfa_enrolled"] != true {
		t.Fatalf("session mfa_satisfied=%v mfa_enrolled=%v, want true/true", env.Data["mfa_satisfied"], env.Data["mfa_enrolled"])
	}
	t.Logf("PASS 4 GET /session -> mfa_satisfied=true mfa_enrolled=true")

	// ---- 5) 挑战单次使用 ----
	status, _, env = h.post(verifyPath, map[string]any{"challenge_id": challengeA, "code": good()})
	if status != http.StatusUnauthorized || h.errCode(env) != "AUTHN_MFA_CHALLENGE_INVALID" {
		t.Fatalf("重放 status=%d code=%q, want 401/AUTHN_MFA_CHALLENGE_INVALID", status, h.errCode(env))
	}
	t.Logf("PASS 5 重放已消费的挑战 -> 401 AUTHN_MFA_CHALLENGE_INVALID")

	// ---- 6) 未知/伪造 challenge_id 不得泄露差异 ----
	status, _, env = h.post(verifyPath, map[string]any{"challenge_id": "forged-challenge-id", "code": good()})
	if status != http.StatusUnauthorized || h.errCode(env) != "AUTHN_MFA_CHALLENGE_INVALID" {
		t.Fatalf("伪造挑战 status=%d code=%q, want 401/AUTHN_MFA_CHALLENGE_INVALID", status, h.errCode(env))
	}
	t.Logf("PASS 6 伪造 challenge_id -> 401 AUTHN_MFA_CHALLENGE_INVALID（不再是 501 占位）")

	// ---- 7) 失败计数上限 -> 429 + Retry-After ----
	h.clearCookies()
	status, _, env = login()
	if status != http.StatusAccepted {
		t.Fatalf("第二次 login status = %d, want 202", status)
	}
	challengeB := h.str(env, "challenge_id")
	for i := 0; i < mfa.ChallengeMaxAttempts; i++ {
		status, _, env = h.post(verifyPath, map[string]any{"challenge_id": challengeB, "code": wrong()})
		if status != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误验证码 status = %d, want 401", i+1, status)
		}
	}
	status, headers, env := h.post(verifyPath, map[string]any{"challenge_id": challengeB, "code": wrong()})
	if status != http.StatusTooManyRequests || h.errCode(env) != "RATE_LIMIT_MFA_ATTEMPTS" {
		t.Fatalf("超过上限 status=%d code=%q, want 429/RATE_LIMIT_MFA_ATTEMPTS", status, h.errCode(env))
	}
	if headers.Get("Retry-After") == "" {
		t.Fatal("429 必须带 Retry-After")
	}
	if h.hasSessionCookie() {
		t.Fatal("429 后不得下发会话 Cookie")
	}
	t.Logf("PASS 7 %d 次失败后 -> 429 RATE_LIMIT_MFA_ATTEMPTS Retry-After=%s", mfa.ChallengeMaxAttempts, headers.Get("Retry-After"))

	// ---- 8) 未注册 MFA 的身份直接登录（不受影响） ----
	if _, err := st.Pool.Exec(ctx, `DELETE FROM identity_mfa WHERE identity_id = $1::uuid`, rig.identityID); err != nil {
		t.Fatalf("unenroll fixture MFA: %v", err)
	}
	h.clearCookies()
	status, _, env = login()
	if status != http.StatusOK {
		t.Fatalf("未注册 MFA 登录 status = %d, want 200; body=%+v", status, env)
	}
	if got := h.str(env, "state"); got != "authenticated" {
		t.Fatalf("state = %q, want authenticated", got)
	}
	if !h.hasSessionCookie() {
		t.Fatal("未注册 MFA 的登录必须直接下发会话 Cookie")
	}
	t.Logf("PASS 8 未注册 MFA 的身份 -> 200 authenticated（不受第二步影响）")

	// ---- 9) 失败事件留痕（HTTP 层写的 auth.mfa.failure） ----
	var failures int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM login_events WHERE identity_id = $1::uuid AND event_type = 'auth.mfa.failure' AND reason_code = 'mfa_failed'`, rig.identityID).Scan(&failures); err != nil {
		t.Fatalf("count mfa failure events: %v", err)
	}
	if failures < mfa.ChallengeMaxAttempts+1 {
		t.Fatalf("auth.mfa.failure 事件 %d 条，少于本次失败提交数", failures)
	}
	t.Logf("PASS 9 auth.mfa.failure 事件 %d 条（每次失败提交一条）", failures)

	t.Logf("ALL PASS AUTH-004 HTTP 端到端 base=%s", h.base)
}

// TestMFAAdminResetLive 证明管理台「重置 MFA」的真实端到端行为（§21.6 commands/reset-mfa）：
// 管理员会话 + step-up 才能执行；执行后目标账号不再要求第二步，且其既有会话被吊销。
func TestMFAAdminResetLive(t *testing.T) {
	rig := mfaLiveSetup(t)
	ctx, st := rig.ctx, rig.st
	h := mfaLiveHTTPClient(t)

	// 前置：目标账号先用 MFA 完整登录一次，拿到一个**真实存活**的会话；
	// 重置之后它必须被吊销（否则"重置"形同虚设）。
	rig.setStep(t, -1)
	first := rig.login(t)
	if !first.MFARequired {
		t.Fatal("前置条件失败：目标账号必须处于已启用 MFA 状态")
	}
	state, method, err := rig.mfa.ConsumeLoginChallenge(ctx, first.ChallengeID, rig.code(t, time.Now().Unix()/mfa.PeriodSeconds))
	if err != nil {
		t.Fatalf("前置登录第二步失败: %v", err)
	}
	_ = rig.complete(t, state, method)
	if got := rig.liveSessions(t); got != 1 {
		t.Fatalf("前置条件失败：目标账号应有 1 个存活会话，实际 %d", got)
	}

	// 1) 管理员登录（夹具管理员不注册 MFA）
	status, _, env := h.post("/api/auth/v1/login", map[string]any{
		"identifier": rig.adminEmail, "password": rig.adminPassword, "audience": "admin",
	})
	if status != http.StatusOK {
		t.Fatalf("管理员登录 status = %d, want 200; body=%+v", status, env)
	}
	csrf := h.str(env, "csrf_token")
	if csrf == "" {
		t.Fatal("管理员登录必须返回 csrf_token（管理面写操作需要双提交）")
	}
	t.Logf("PASS 1 管理员登录 200 session=%s", h.str(env, "session_id"))

	path := "/api/admin/v1/users/" + rig.identityID + "/commands/reset-mfa"
	hdrs := map[string]string{"X-CSRF-Token": csrf}

	// 2) 未二次确认 → 412（一个字节都不能动）
	status, _, env = h.postAuth(path, map[string]any{"confirm": false, "step_up_password": rig.adminPassword}, hdrs)
	if status != http.StatusPreconditionRequired || h.errCode(env) != "CONFLICT_CONFIRMATION_REQUIRED" {
		t.Fatalf("未确认 status=%d code=%q, want 412/CONFLICT_CONFIRMATION_REQUIRED", status, h.errCode(env))
	}
	if got := rig.liveSessions(t); got != 1 {
		t.Fatalf("被拒绝的请求不得改动任何状态（会话数 = %d）", got)
	}
	t.Logf("PASS 2 未勾选二次确认 -> 412，且状态未被改动")

	// 3) step-up 密码错误 → 403 STEP_UP_INVALID
	status, _, env = h.postAuth(path, map[string]any{
		"confirm": true, "reason": "wrong step-up probe", "step_up_password": "not-the-password-" + uuid.NewString(),
	}, hdrs)
	if status != http.StatusForbidden || h.errCode(env) != "STEP_UP_INVALID" {
		t.Fatalf("step-up 失败 status=%d code=%q, want 403/STEP_UP_INVALID", status, h.errCode(env))
	}
	if got := rig.liveSessions(t); got != 1 {
		t.Fatalf("step-up 失败不得改动任何状态（会话数 = %d）", got)
	}
	t.Logf("PASS 3 step-up 密码错误 -> 403 STEP_UP_INVALID，且状态未被改动")

	// 4) 正常重置 → 200
	status, _, env = h.postAuth(path, map[string]any{
		"confirm": true, "reason": "AUTH-004 端到端：管理员重置 MFA", "step_up_password": rig.adminPassword,
	}, hdrs)
	if status != http.StatusOK {
		t.Fatalf("重置 status = %d, want 200; body=%+v", status, env)
	}
	if env.Data["mfa_enrolled"] != false || env.Data["reset"] != true {
		t.Fatalf("重置回执 mfa_enrolled=%v reset=%v", env.Data["mfa_enrolled"], env.Data["reset"])
	}
	removed, _ := env.Data["removed"].(map[string]any)
	if removed == nil || removed["secret"] != true {
		t.Fatalf("回执必须说明删除了密钥, removed=%v", env.Data["removed"])
	}
	codes, _ := removed["recovery_codes"].(float64)
	if codes <= 0 {
		t.Fatalf("回执必须说明删除了恢复码, removed=%v", removed)
	}
	revoked, _ := env.Data["sessions_revoked"].(float64)
	if revoked < 1 {
		t.Fatalf("重置必须吊销目标账号的既有会话, sessions_revoked=%v", env.Data["sessions_revoked"])
	}
	if env.Data["revocation_failed"] == true {
		t.Fatal("会话吊销失败必须被显式标记（revocation_failed=true），不应被读成已收拾干净")
	}
	if got := rig.liveSessions(t); got != 0 {
		t.Fatalf("重置后目标账号存活会话数 = %d, want 0", got)
	}
	t.Logf("PASS 4 重置成功 200：删除密钥 + %v 个恢复码，吊销 %v 个会话", codes, revoked)

	// 5) 重复重置 → 409（不做无意义的"成功"）。必须放在清 Cookie 之前：
	//    重复重置用的是管理员会话，而第 6 步要清 Cookie 换目标账号登录。
	status, _, env = h.postAuth(path, map[string]any{
		"confirm": true, "reason": "repeat", "step_up_password": rig.adminPassword,
	}, hdrs)
	if status != http.StatusConflict || h.errCode(env) != "CONFLICT_MFA_NOT_ENROLLED" {
		t.Fatalf("重复重置 status=%d code=%q, want 409/CONFLICT_MFA_NOT_ENROLLED", status, h.errCode(env))
	}
	t.Logf("PASS 5 重复重置 -> 409 CONFLICT_MFA_NOT_ENROLLED")

	// 6) 目标账号此后登录不再需要第二步（清 Cookie 后以用户态登录）
	h.clearCookies()
	status, _, env = h.post("/api/auth/v1/login", map[string]any{
		"identifier": mfaLiveFixtureEmail, "password": rig.password, "audience": "user",
	})
	if status != http.StatusOK || h.str(env, "state") != "authenticated" {
		t.Fatalf("重置后登录 status=%d state=%q, want 200/authenticated", status, h.str(env, "state"))
	}
	t.Logf("PASS 6 目标账号重置后直接登录 200（不再要求第二步）")

	// 7) 用户态会话不得执行管理命令：带上用户自己的 CSRF（否则会先被 CSRF 门挡住，测不出权限判定）。
	userCSRF := h.str(env, "csrf_token")
	if userCSRF == "" {
		t.Fatal("用户态登录必须返回 csrf_token") 
	}
	status, _, env = h.postAuth(path, map[string]any{"confirm": true, "step_up_password": rig.password},
		map[string]string{"X-CSRF-Token": userCSRF})
	// 401 与 403 都是正确拒绝：admin 路由先做受众绑定（用户态会话在管理面等于未认证，401），
	// 通过受众绑定后才轮到权限判定（403 AUTHZ_FORBIDDEN）。两者都不允许执行。
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("用户态会话调用管理命令 status=%d code=%q, want 401/403", status, h.errCode(env))
	}
	t.Logf("PASS 7 用户态会话调用管理命令被拒（%d %s）", status, h.errCode(env))

	// 8) 审计与数据库证据
	var auditCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events_v2 WHERE action = 'admin:user:reset_mfa' AND resource_id = $1`, rig.identityID).Scan(&auditCount); err != nil {
		t.Fatalf("count reset_mfa audit: %v", err)
	}
	if auditCount < 1 {
		t.Fatalf("重置必须留审计事件，实际 %d 条", auditCount)
	}
	var mfaRows, challengeRows int
	if err := st.Pool.QueryRow(ctx, "SELECT count(*) FROM identity_mfa WHERE identity_id = $1::uuid", rig.identityID).Scan(&mfaRows); err != nil {
		t.Fatalf("count identity_mfa: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, "SELECT count(*) FROM mfa_login_challenges WHERE identity_id = $1::uuid AND consumed_at IS NULL", rig.identityID).Scan(&challengeRows); err != nil {
		t.Fatalf("count open challenges: %v", err)
	}
	if mfaRows != 0 {
		t.Fatalf("重置后 identity_mfa 必须为空，实际 %d 行", mfaRows)
	}
	t.Logf("PASS 8 审计事件 %d 条；identity_mfa 行数 0；未消费挑战 %d", auditCount, challengeRows)
	t.Logf("ALL PASS 管理台重置 MFA 端到端 目标=%s 管理员=%s", rig.identityID, rig.adminID)
}
