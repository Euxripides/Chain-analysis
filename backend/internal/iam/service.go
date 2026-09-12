package iam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
)

// 认证错误：HTTP 层据此外映状态码与稳定错误码（§21.3）。
var (
	ErrAccountDisabled = errors.New("account disabled")
	ErrAccountLocked   = errors.New("account locked")
	ErrRateLimited     = errors.New("rate limited")
	ErrMFAUnavailable  = errors.New("mfa not available")
)

// Config 是认证策略。字段来自环境变量，便于按环境收紧。
type Config struct {
	Environment         string
	MaxFailedLogins     int
	LockDuration        time.Duration
	LoginRatePerMinute  int
	MinPasswordLength   int
	RequireAdminMFA     bool
}

func DefaultConfig() Config {
	return Config{
		Environment:        "development",
		MaxFailedLogins:    10,
		LockDuration:       15 * time.Minute,
		LoginRatePerMinute: 20,
		MinPasswordLength:  12,
	}
}

// LoginInput 聚合一次登录尝试所需的全部请求上下文。
type LoginInput struct {
	Email       string
	Password    string
	Audience    string
	IP          ClientIP
	UserAgent   string
	ClientHints map[string]string
	EntryHost   string
	Scheme      string
	RequestID   string
	TraceID     string
}

// LoginResult 成功后返回会话与一次性令牌。
type LoginResult struct {
	Session Session
	Token   string
	CSRF    string
	Roles   []string
}

// Service 编排登录、退出、改密与会话撤销。
type Service struct {
	repo     *Repo
	sessions *SessionManager
	cfg     Config
	audit   *audit.Writer
	limiter *loginLimiter
}

func NewService(repo *Repo, sessions *SessionManager, cfg Config, writer *audit.Writer) *Service {
	if cfg.MaxFailedLogins <= 0 {
		cfg.MaxFailedLogins = 10
	}
	if cfg.LockDuration <= 0 {
		cfg.LockDuration = 15 * time.Minute
	}
	if cfg.LoginRatePerMinute <= 0 {
		cfg.LoginRatePerMinute = 20
	}
	if cfg.MinPasswordLength <= 0 {
		cfg.MinPasswordLength = 12
	}
	return &Service{repo: repo, sessions: sessions, cfg: cfg, audit: writer, limiter: newLoginLimiter(cfg.LoginRatePerMinute)}
}

// RetryAfterSeconds 暴露账号维度的限流窗口剩余时间，供 HTTP 层设置 Retry-After 头。
// 必须使用与 Login 完全相同的桶键（IP + 规范化邮箱），否则读到的永远是空桶。
func (s *Service) RetryAfterSeconds(identifier, clientIP string) int {
	if s == nil || s.limiter == nil {
		return 60
	}
	key := strings.Join([]string{clientIP, NormalizeEmail(identifier)}, "|")
	return s.limiter.retryAfter(key)
}

func (s *Service) Repo() *Repo                { return s.repo }
func (s *Service) Sessions() *SessionManager  { return s.sessions }
func (s *Service) Config() Config             { return s.cfg }
func (s *Service) Audit() *audit.Writer       { return s.audit }

// Login 执行完整登录流程并记录登录事件（成功与失败都记录）。
//
// 账号枚举防护：未知账号与密码错误对外返回同一个 ErrInvalidCredentials，
// 且对未知账号执行一次等价开销的哈希运算以抹平时间差。
func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	start := time.Now()
	email := NormalizeEmail(in.Email)
	if in.Audience == "" {
		in.Audience = AudienceUser
	}
	if in.IP.ProxyChain == nil {
		in.IP.ProxyChain = []string{}
	}
	device := ParseUserAgent(in.UserAgent)

	record := func(eventType, result, reason, risk string, identityID string) {
		_ = RecordLoginEvent(ctx, s.repo.Pool(), s.cfg.Environment, LoginEvent{
			EventType: eventType, Result: result, ReasonCode: reason, RiskLevel: risk,
			IdentityID: identityID, SubjectEmail: email, Audience: in.Audience,
			DeviceInfo: device, UserAgent: in.UserAgent, ClientHints: in.ClientHints,
			IP: in.IP, EntryHost: in.EntryHost, Scheme: in.Scheme,
			RequestID: in.RequestID, TraceID: in.TraceID,
			DurationMS: int(time.Since(start).Milliseconds()),
		})
	}

	// 频率限制：账号维度为主、IP 维度为兜底。
	//
	// 语义必须是"任一维度超限即阻断"（||）。早期实现写成
	// `!allow(account) && !allow(ip)`，两个条件是短路关系，实际要两个桶同时耗尽
	// 才会阻断 —— 单 IP 撒网会一直放行，而两个桶又互相抵消。
	//
	// 只在**失败**后消耗配额：正常用户的成功登录不应占用额度，
	// 否则一个 NAT 出口后的多人会因为彼此的手误互相影响（§27 SEC-004
	// "正常用户不受越权共享 bucket 影响"）。
	accountKey := strings.Join([]string{in.IP.ClientIP, email}, "|")
	ipKey := "ip|" + in.IP.ClientIP
	if blocked, retryAfter := s.limiter.blocked(accountKey, ipKey); blocked {
		_ = retryAfter
		record(EventLoginBlocked, "blocked", ReasonRateLimited, "medium", "")
		return LoginResult{}, ErrRateLimited
	}

	if email == "" || in.Password == "" {
		s.limiter.consume(accountKey, ipKey)
		record(EventLoginFailure, "failure", ReasonBadPassword, "low", "")
		return LoginResult{}, ErrInvalidCredentials
	}

	identity, err := s.repo.GetIdentityByEmail(ctx, email)
	if errors.Is(err, ErrIdentityNotFound) {
		// 等价开销：对固定假哈希做一次校验，避免通过响应时间枚举账号。
		_, _, _ = VerifyPassword(dummyHash, in.Password)
		s.limiter.consume(accountKey, ipKey)
		record(EventLoginFailure, "failure", ReasonUnknownAccount, "medium", "")
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}

	_ = s.repo.UnlockIfExpired(ctx, identity.ID)

	switch identity.Status {
	case StatusDisabled:
		record(EventLoginDenied, "denied", ReasonDisabledAccount, "high", identity.ID)
		return LoginResult{}, ErrAccountDisabled
	case StatusLocked:
		record(EventLoginDenied, "denied", ReasonLockedAccount, "high", identity.ID)
		return LoginResult{}, ErrAccountLocked
	}
	if identity.LockedUntil != nil && identity.LockedUntil.After(time.Now()) {
		record(EventLoginBlocked, "blocked", ReasonLockedAccount, "high", identity.ID)
		return LoginResult{}, ErrAccountLocked
	}

	hash, err := s.repo.PasswordHash(ctx, identity.ID)
	if errors.Is(err, ErrNoPassword) {
		_, _, _ = VerifyPassword(dummyHash, in.Password)
		record(EventLoginFailure, "failure", ReasonNoPassword, "high", identity.ID)
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}

	ok, params, err := VerifyPassword(hash, in.Password)
	if err != nil || !ok {
		s.limiter.consume(accountKey, ipKey)
		locked, _ := s.repo.MarkLoginFailure(ctx, identity.ID, s.cfg.MaxFailedLogins, s.cfg.LockDuration)
		risk := "low"
		reason := ReasonBadPassword
		if locked {
			risk = "high"
		}
		record(EventLoginFailure, "failure", reason, risk, identity.ID)
		if locked {
			return LoginResult{}, ErrAccountLocked
		}
		return LoginResult{}, ErrInvalidCredentials
	}

	// 参数升级：旧参数哈希在成功登录后按当前参数重算（不改变用户可见行为）。
	if NeedsRehash(hash, DefaultParams()) {
		if newHash, err := HashPassword(in.Password, DefaultParams()); err == nil {
			_ = s.repo.SetPassword(ctx, identity.ID, newHash, identity.MustChangePassword)
		}
	} else if params.Memory == 0 {
		_ = params
	}

	if s.cfg.RequireAdminMFA && in.Audience == AudienceAdmin && !identity.MFARequired {
		// P0 仅保留策略与接口位置：真正的 TOTP 校验在 S2-BE-01 后续迭代实现，
		// 未实现前不得宣称已支持，因此这里显式失败而不是静默放行。
		record(EventLoginDenied, "denied", ReasonMFARequired, "high", identity.ID)
		return LoginResult{}, ErrMFAUnavailable
	}

	deviceID, isNewDevice, err := s.sessions.EnsureDevice(ctx, identity.ID, device, in.IP.ClientIP)
	if err != nil {
		return LoginResult{}, err
	}

	orgID := identity.OrganizationID
	wsID := s.defaultWorkspace(ctx, orgID)

	created, err := s.sessions.Create(ctx, SessionRequest{
		Audience: in.Audience, IdentityID: identity.ID, OrganizationID: orgID, WorkspaceID: wsID,
		AuthMethod: "password", MFASatisfied: !s.cfg.RequireAdminMFA,
		IP: in.IP, UserAgent: in.UserAgent, ClientHints: in.ClientHints, DeviceInfo: device,
		EntryHost: in.EntryHost, Scheme: in.Scheme,
	})
	if err != nil {
		return LoginResult{}, err
	}
	if deviceID != "" {
		_, _ = s.repo.Pool().Exec(ctx, `UPDATE sessions SET device_id = $2 WHERE id = $1`, created.Session.ID, deviceID)
		created.Session.DeviceID = deviceID
	}
	if err := s.repo.MarkLoginSuccess(ctx, identity.ID, in.IP.ClientIP); err != nil {
		return LoginResult{}, err
	}
	// 成功登录后清空该账号的失败额度，避免"手误若干次 + 正常登录"把额度耗光。
	s.limiter.reset(accountKey)

	risk := "low"
	if isNewDevice {
		risk = "medium"
	}
	_ = RecordLoginEvent(ctx, s.repo.Pool(), s.cfg.Environment, LoginEvent{
		EventType: EventLoginSuccess, Result: "success", RiskLevel: risk,
		IdentityID: identity.ID, SubjectEmail: email, OrganizationID: orgID, WorkspaceID: wsID,
		Audience: in.Audience, SessionID: created.Session.ID, DeviceID: deviceID,
		IsNewDevice: isNewDevice, DeviceInfo: device, UserAgent: in.UserAgent,
		ClientHints: in.ClientHints, IP: in.IP, EntryHost: in.EntryHost, Scheme: in.Scheme,
		RequestID: in.RequestID, TraceID: in.TraceID,
		DurationMS: int(time.Since(start).Milliseconds()),
	})
	return LoginResult{Session: created.Session, Token: created.Token, CSRF: created.CSRF, Roles: created.Session.Roles}, nil
}

func (s *Service) defaultWorkspace(ctx context.Context, orgID string) string {
	var id string
	if err := s.repo.Pool().QueryRow(ctx,
		`SELECT id FROM workspaces WHERE organization_id = $1 ORDER BY slug LIMIT 1`, orgID).Scan(&id); err != nil {
		return ""
	}
	return id
}

// Logout 撤销当前会话并记录事件。
func (s *Service) Logout(ctx context.Context, sess Session, in LoginInput) error {
	if err := s.sessions.RevokeByID(ctx, sess.ID, "logout", sess.IdentityID); err != nil {
		return err
	}
	_ = RecordLoginEvent(ctx, s.repo.Pool(), s.cfg.Environment, LoginEvent{
		EventType: EventLogout, Result: "success", IdentityID: sess.IdentityID,
		SubjectEmail: sess.Identity.Email, OrganizationID: sess.OrganizationID,
		Audience: sess.Audience, SessionID: sess.ID, DeviceID: sess.DeviceID,
		DeviceInfo: ParseUserAgent(in.UserAgent), UserAgent: in.UserAgent,
		IP: in.IP, EntryHost: in.EntryHost, Scheme: in.Scheme,
		RequestID: in.RequestID, TraceID: in.TraceID,
	})
	return nil
}

// ChangePassword 校验旧密码后设置新密码，并撤销该身份全部会话（含当前会话）。
func (s *Service) ChangePassword(ctx context.Context, identityID, oldPassword, newPassword string, in LoginInput) error {
	if len([]rune(newPassword)) < s.cfg.MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", s.cfg.MinPasswordLength)
	}
	hash, err := s.repo.PasswordHash(ctx, identityID)
	if err != nil {
		return err
	}
	ok, _, err := VerifyPassword(hash, oldPassword)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	newHash, err := HashPassword(newPassword, DefaultParams())
	if err != nil {
		return err
	}
	if err := s.repo.SetPassword(ctx, identityID, newHash, false); err != nil {
		return err
	}
	_ = RecordLoginEvent(ctx, s.repo.Pool(), s.cfg.Environment, LoginEvent{
		EventType: EventPasswordChanged, Result: "success", IdentityID: identityID,
		SubjectEmail: in.Email, Audience: in.Audience, IP: in.IP,
		DeviceInfo: ParseUserAgent(in.UserAgent), UserAgent: in.UserAgent,
		RequestID: in.RequestID, TraceID: in.TraceID, RiskLevel: "high",
		Detail: map[string]any{"sessions_revoked": true},
	})
	return nil
}

// AdminResetPassword 由管理员执行，返回一次性密码（调用方必须只在响应中出现一次）。
func (s *Service) AdminResetPassword(ctx context.Context, actor Identity, targetID, newPassword string, in LoginInput) error {
	if len([]rune(newPassword)) < s.cfg.MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", s.cfg.MinPasswordLength)
	}
	hash, err := HashPassword(newPassword, DefaultParams())
	if err != nil {
		return err
	}
	if err := s.repo.SetPassword(ctx, targetID, hash, true); err != nil {
		return err
	}
	_ = RecordLoginEvent(ctx, s.repo.Pool(), s.cfg.Environment, LoginEvent{
		EventType: EventPasswordReset, Result: "success", IdentityID: targetID,
		Audience: in.Audience, IP: in.IP, RiskLevel: "critical",
		Detail: map[string]any{"actor_id": actor.ID, "actor_email": actor.Email},
	})
	return nil
}

// ForceLogout 强制下线某个身份的全部会话。
func (s *Service) ForceLogout(ctx context.Context, actor Identity, targetID string, in LoginInput) (int64, error) {
	n, err := s.sessions.RevokeAllForIdentity(ctx, targetID, "forced_by_admin", actor.ID)
	if err != nil {
		return 0, err
	}
	_ = RecordLoginEvent(ctx, s.repo.Pool(), s.cfg.Environment, LoginEvent{
		EventType: EventSessionForced, Result: "success", IdentityID: targetID,
		Audience: in.Audience, IP: in.IP, RiskLevel: "high",
		Detail: map[string]any{"actor_id": actor.ID, "sessions_revoked": n},
	})
	return n, nil
}

// dummyHash 是固定口令的 Argon2id 哈希，用于抹平"账号不存在"的响应时间差。
var dummyHash = func() string {
	h, err := HashPassword("some-nonexistent-account-placeholder", Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 4, KeyLength: 32, SaltLength: 16})
	if err != nil {
		return "$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return h
}()

// ---- 频率限制（进程内固定窗口计数，登录专用；分布式限流由 Nginx/网关承担）----

// rateLimiter 是单维度固定窗口计数器。
type rateLimiter struct {
	mu       sync.Mutex
	capacity int
	window   time.Duration
	buckets  map[string]*bucket
	lastGC   time.Time
}

type bucket struct {
	used    int
	resetAt time.Time
}

func newRateLimiter(perWindow int, window time.Duration) *rateLimiter {
	if perWindow <= 0 {
		perWindow = 20
	}
	return &rateLimiter{capacity: perWindow, window: window, buckets: map[string]*bucket{}, lastGC: time.Now()}
}

func (l *rateLimiter) gc(now time.Time) {
	if now.Sub(l.lastGC) <= 5*time.Minute {
		return
	}
	for k, b := range l.buckets {
		if now.After(b.resetAt) {
			delete(l.buckets, k)
		}
	}
	l.lastGC = now
}

// exceeded 只读检查，不消耗配额。
func (l *rateLimiter) exceeded(key string) (bool, int) {
	if l == nil || key == "" {
		return false, 0
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gc(now)
	b, ok := l.buckets[key]
	if !ok || now.After(b.resetAt) {
		return false, 0
	}
	if b.used >= l.capacity {
		secs := int(time.Until(b.resetAt).Seconds()) + 1
		if secs < 1 {
			secs = 1
		}
		return true, secs
	}
	return false, 0
}

// consume 记一次失败尝试。
func (l *rateLimiter) consume(key string) {
	if l == nil || key == "" {
		return
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gc(now)
	b, ok := l.buckets[key]
	if !ok || now.After(b.resetAt) {
		l.buckets[key] = &bucket{used: 1, resetAt: now.Add(l.window)}
		return
	}
	b.used++
}

func (l *rateLimiter) reset(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// loginLimiter 组合两个维度：
//   - account：同一个 (IP, 账号) 组合的失败次数，主控制，默认 20/分钟；
//   - ip：同一个来源 IP 的失败次数，兜底防止撒网，容量放宽 5 倍且不低于 100，
//     避免同一 NAT 出口后的正常用户被他人拖累。
type loginLimiter struct {
	account *rateLimiter
	ip      *rateLimiter
}

func newLoginLimiter(perMinute int) *loginLimiter {
	if perMinute <= 0 {
		perMinute = 20
	}
	ipCapacity := perMinute * 5
	if ipCapacity < 100 {
		ipCapacity = 100
	}
	return &loginLimiter{
		account: newRateLimiter(perMinute, time.Minute),
		ip:      newRateLimiter(ipCapacity, time.Minute),
	}
}

// blocked 返回是否应阻断本次尝试，以及建议的 Retry-After 秒数。
func (l *loginLimiter) blocked(accountKey, ipKey string) (bool, int) {
	if l == nil {
		return false, 0
	}
	if over, retry := l.account.exceeded(accountKey); over {
		return true, retry
	}
	if over, retry := l.ip.exceeded(ipKey); over {
		return true, retry
	}
	return false, 0
}

func (l *loginLimiter) consume(accountKey, ipKey string) {
	if l == nil {
		return
	}
	l.account.consume(accountKey)
	l.ip.consume(ipKey)
}

func (l *loginLimiter) reset(accountKey string) {
	if l == nil {
		return
	}
	l.account.reset(accountKey)
}

// retryAfter 返回该账号维度剩余等待秒数，供 Retry-After 头使用。
func (l *loginLimiter) retryAfter(accountKey string) int {
	if l == nil {
		return 60
	}
	if _, retry := l.account.exceeded(accountKey); retry > 0 {
		return retry
	}
	return 60
}

// IsAuthError 帮助 HTTP 层把认证错误映射为稳定错误码。
func IsAuthError(err error) bool {
	switch {
	case errors.Is(err, ErrInvalidCredentials), errors.Is(err, ErrAccountDisabled),
		errors.Is(err, ErrAccountLocked), errors.Is(err, ErrRateLimited),
		errors.Is(err, ErrMFAUnavailable):
		return true
	}
	return false
}

// StatusForError 返回稳定错误码与 HTTP 状态（§21.3：错误码与 HTTP 状态分离且稳定）。
func StatusForError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		return http.StatusUnauthorized, "invalid_credentials"
	case errors.Is(err, ErrAccountDisabled):
		return http.StatusForbidden, "account_disabled"
	case errors.Is(err, ErrAccountLocked):
		return http.StatusLocked, "account_locked"
	case errors.Is(err, ErrRateLimited):
		return http.StatusTooManyRequests, "rate_limited"
	case errors.Is(err, ErrMFAUnavailable):
		return http.StatusNotImplemented, "mfa_not_available"
	case errors.Is(err, ErrSessionInvalid):
		return http.StatusUnauthorized, "session_invalid"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}
