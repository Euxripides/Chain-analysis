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
	cfg      Config
	audit    *audit.Writer
	limiter  *rateLimiter
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
	return &Service{repo: repo, sessions: sessions, cfg: cfg, audit: writer, limiter: newRateLimiter(cfg.LoginRatePerMinute)}
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

	// 频率限制：按 IP + 账号双维度计数，避免单账号被暴力破解也避免单 IP 撒网。
	key := strings.Join([]string{in.IP.ClientIP, email}, "|")
	if !s.limiter.allow(key) && !s.limiter.allow("ip|"+in.IP.ClientIP) {
		record(EventLoginBlocked, "blocked", ReasonRateLimited, "medium", "")
		return LoginResult{}, ErrRateLimited
	}

	if email == "" || in.Password == "" {
		record(EventLoginFailure, "failure", ReasonBadPassword, "low", "")
		return LoginResult{}, ErrInvalidCredentials
	}

	identity, err := s.repo.GetIdentityByEmail(ctx, email)
	if errors.Is(err, ErrIdentityNotFound) {
		// 等价开销：对固定假哈希做一次校验，避免通过响应时间枚举账号。
		_, _, _ = VerifyPassword(dummyHash, in.Password)
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

// ---- 频率限制（进程内令牌桶，登录专用；分布式限流由 Nginx/网关承担）----

type rateLimiter struct {
	mu       sync.Mutex
	capacity int
	window   time.Duration
	buckets  map[string]*bucket
	lastGC   time.Time
}

type bucket struct {
	used     int
	resetAt  time.Time
}

func newRateLimiter(perMinute int) *rateLimiter {
	if perMinute <= 0 {
		perMinute = 20
	}
	return &rateLimiter{
		capacity: perMinute,
		window:   time.Minute,
		buckets:  map[string]*bucket{},
		lastGC:   time.Now(),
	}
}

func (l *rateLimiter) allow(key string) bool {
	if l == nil || key == "" {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastGC) > 5*time.Minute {
		for k, b := range l.buckets {
			if now.After(b.resetAt) {
				delete(l.buckets, k)
			}
		}
		l.lastGC = now
	}
	b, ok := l.buckets[key]
	if !ok || now.After(b.resetAt) {
		l.buckets[key] = &bucket{used: 1, resetAt: now.Add(l.window)}
		return true
	}
	if b.used >= l.capacity {
		return false
	}
	b.used++
	return true
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
