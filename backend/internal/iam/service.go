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
	// ErrMFAEnrollmentRequired：策略要求该受众必须使用 MFA，但该身份尚未注册。
	// 与 ErrMFAUnavailable 的区别是「MFA 能力已可用，只是这个账号没启用」。
	ErrMFAEnrollmentRequired = errors.New("mfa enrollment required")

	// P1-20260913（会话作用域越权，根 11:12 工程决定）：工作区必须来自
	// 该身份在该组织的 active 成员资格；以下错误一律 fail closed，绝不回退组织默认工作区。
	ErrNoWorkspaceMembership      = errors.New("identity has no active workspace membership")
	ErrWorkspaceSelectionRequired = errors.New("workspace selection required")
	ErrWorkspaceNotMember         = errors.New("requested workspace is not an active membership")
)

// Config 是认证策略。字段来自环境变量，便于按环境收紧。
type Config struct {
	Environment        string
	MaxFailedLogins    int
	LockDuration       time.Duration
	LoginRatePerMinute int
	MinPasswordLength  int
	RequireAdminMFA    bool
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
	// WorkspaceID 是登录时由客户端显式选择的工作区（P1-20260913）。
	// 多成员资格身份必须给出；留空且成员资格唯一时自动绑定那一个。
	WorkspaceID string
}

// LoginResult 成功后返回会话与一次性令牌。
type LoginResult struct {
	Session Session
	Token   string
	CSRF    string
	Roles   []string

	// MFARequired 为 true 时表示：密码已通过，但登录**尚未完成**。
	// 此时 Session/Token/CSRF 全部为零值（没有会话、没有 Cookie），
	// 调用方必须把 ChallengeID 交给客户端完成第二步（§21.4 返回 202）。
	MFARequired        bool
	ChallengeID        string
	ChallengeExpiresAt time.Time
	// Identity 用于在挑战响应里回显用户摘要（第二步之前还没有会话）。
	Identity Identity
}

// Service 编排登录、退出、改密与会话撤销。
type Service struct {
	repo     *Repo
	sessions *SessionManager
	cfg      Config
	audit    *audit.Writer
	limiter  *loginLimiter
	// mfaGate 为 nil 时登录行为与接入 MFA 之前完全一致（api 层在数据库不可用时才不注入）。
	mfaGate MFAChallengeGate
}

// SetMFAChallengeGate 注入登录第二步的挑战能力（见 iam.MFAChallengeGate）。
func (s *Service) SetMFAChallengeGate(gate MFAChallengeGate) {
	if s == nil {
		return
	}
	s.mfaGate = gate
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

func (s *Service) Repo() *Repo               { return s.repo }
func (s *Service) Sessions() *SessionManager { return s.sessions }
func (s *Service) Config() Config            { return s.cfg }
func (s *Service) Audit() *audit.Writer      { return s.audit }

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
	case StatusDeleted:
		// 已删除（离职）账号：对外与停用同形（不泄露账号状态），原因码入库便于审计区分。
		record(EventLoginDenied, "denied", ReasonDeletedAccount, "high", identity.ID)
		return LoginResult{}, ErrAccountDisabled
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

	// 登录第二步（AUTH-004）：密码通过后，已启用 MFA 的身份**不得**直接拿到会话。
	//
	// 这一步必须发生在 EnsureDevice / ResolveLoginWorkspace / sessions.Create 之前：
	// 先建会话再撤销会留下一个"未满足 MFA 但已生效"的真实窗口，一旦撤销失败就是安全缺口。
	mfaSatisfied := !(s.cfg.RequireAdminMFA && in.Audience == AudienceAdmin)
	if gate := s.mfaGate; gate != nil {
		enrolled, err := gate.Enrolled(ctx, identity.ID)
		if err != nil {
			// fail closed：读不到 MFA 状态时不得假设"该账号没启用"。
			return LoginResult{}, err
		}
		if enrolled {
			return s.startMFALogin(ctx, in, identity, record)
		}
		if s.cfg.RequireAdminMFA && in.Audience == AudienceAdmin {
			// 策略要求后台必须 MFA，但该身份还没注册：显式拒绝而不是静默放行。
			// 此前这里返回 ErrMFAUnavailable（501 未实现）；能力可用后语义变为"需先注册"。
			record(EventLoginDenied, "denied", ReasonMFARequired, "high", identity.ID)
			return LoginResult{}, ErrMFAEnrollmentRequired
		}
	}

	return s.issueSession(ctx, sessionIssue{
		Input: in, Identity: identity, Device: device, Start: start, AccountKey: accountKey,
		AuthMethod: "password", MFASatisfied: mfaSatisfied, Record: record,
	})
}

// sessionIssue 聚合"签发会话"所需的全部输入。
//
// 抽取原因：密码直登与 MFA 第二步是两个入口，但设备/工作区/会话/审计语义必须完全一致；
// 任何一处复制都会让两条路径逐渐漂移（P1-20260913 的工作区越权正是这种漂移的产物）。
type sessionIssue struct {
	Input        LoginInput
	Identity     Identity
	Device       DeviceInfo
	Start        time.Time
	AccountKey   string
	AuthMethod   string
	MFASatisfied bool
	MFAMethod    string
	Record       func(eventType, result, reason, risk, identityID string)
}

// issueSession 是登录尾段：登记设备 -> 解析工作区 -> 建会话 -> 记登录成功事件。
func (s *Service) issueSession(ctx context.Context, req sessionIssue) (LoginResult, error) {
	in, identity, device := req.Input, req.Identity, req.Device
	record := req.Record
	if record == nil {
		record = func(string, string, string, string, string) {}
	}
	deviceID, isNewDevice, err := s.sessions.EnsureDevice(ctx, identity.ID, device, in.IP.ClientIP)
	if err != nil {
		return LoginResult{}, err
	}

	orgID := identity.OrganizationID
	wsID, wsErr := s.resolveLoginWorkspace(ctx, identity, in.WorkspaceID, record)
	if wsErr != nil {
		return LoginResult{}, wsErr
	}

	created, err := s.sessions.Create(ctx, SessionRequest{
		Audience: in.Audience, IdentityID: identity.ID, OrganizationID: orgID, WorkspaceID: wsID,
		AuthMethod: req.AuthMethod, MFASatisfied: req.MFASatisfied,
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
	if req.AccountKey != "" {
		s.limiter.reset(req.AccountKey)
	}

	risk := "low"
	if isNewDevice {
		risk = "medium"
	}
	_ = RecordLoginEvent(ctx, s.repo.Pool(), s.cfg.Environment, LoginEvent{
		EventType: EventLoginSuccess, Result: "success", RiskLevel: risk,
		IdentityID: identity.ID, SubjectEmail: identity.Email, OrganizationID: orgID, WorkspaceID: wsID,
		Audience: in.Audience, SessionID: created.Session.ID, DeviceID: deviceID,
		IsNewDevice: isNewDevice, DeviceInfo: device, UserAgent: in.UserAgent,
		ClientHints: in.ClientHints, IP: in.IP, EntryHost: in.EntryHost, Scheme: in.Scheme,
		RequestID: in.RequestID, TraceID: in.TraceID,
		AuthMethod: req.AuthMethod, MFAMethod: req.MFAMethod, MFAResult: mfaResultFor(req.MFAMethod),
		DurationMS: int(time.Since(req.Start).Milliseconds()),
	})
	return LoginResult{Session: created.Session, Token: created.Token, CSRF: created.CSRF, Roles: created.Session.Roles}, nil
}

// resolveLoginWorkspace 解析会话要绑定的工作区，并记录每一次拒绝的原因。
//
// P1-20260913：旧实现 wsID := s.defaultWorkspace(ctx, orgID) 取组织内 ORDER BY slug 的
// 第一个工作区，完全不看 workspace_memberships —— wsB-only 的 viewer 因此拿到 wsA 的会话，
// 读到 wsA 的导出列表/明细（已真实复现）。现在只认成员资格，0 个成员/多成员未显式选择
// 一律拒绝（fail closed），且拒绝必须留下登录事件。
func (s *Service) resolveLoginWorkspace(ctx context.Context, identity Identity, requested string, record func(string, string, string, string, string)) (string, error) {
	wsID, err := s.ResolveLoginWorkspace(ctx, identity.ID, identity.OrganizationID, requested)
	if err != nil {
		switch {
		case errors.Is(err, ErrNoWorkspaceMembership):
			record(EventLoginDenied, "denied", ReasonNoWorkspaceMembership, "high", identity.ID)
		case errors.Is(err, ErrWorkspaceNotMember):
			record(EventLoginDenied, "denied", ReasonWorkspaceNotMember, "high", identity.ID)
		default:
			record(EventLoginDenied, "denied", ReasonWorkspaceSelectionRequired, "medium", identity.ID)
		}
		return "", err
	}
	return wsID, nil
}

// startMFALogin 在密码通过后签发一次性挑战：不建会话、不下发 Cookie。
//
// 工作区在这里先解析一次：多工作区身份必须先给出 workspace_id（fail closed），
// 且解析结果随挑战落库，第二步不需要客户端再报一次、也不可能被客户端篡改。
func (s *Service) startMFALogin(ctx context.Context, in LoginInput, identity Identity, record func(string, string, string, string, string)) (LoginResult, error) {
	gate := s.mfaGate
	if gate == nil {
		return LoginResult{}, ErrMFAEnrollmentRequired
	}
	wsID, err := s.resolveLoginWorkspace(ctx, identity, in.WorkspaceID, record)
	if err != nil {
		return LoginResult{}, err
	}
	challenge, err := gate.IssueLoginChallenge(ctx, MFAChallengeRequest{
		IdentityID: identity.ID, Audience: in.Audience,
		OrganizationID: identity.OrganizationID, WorkspaceID: wsID,
		ClientIP: in.IP.ClientIP, PeerIP: in.IP.PeerIP, ProxyChain: in.IP.ProxyChain,
		UserAgent: in.UserAgent, EntryHost: in.EntryHost, Scheme: in.Scheme,
		RequestID: in.RequestID, TraceID: in.TraceID,
	})
	if err != nil {
		return LoginResult{}, err
	}
	// 状态机 §20.1：credentials_submitted -> mfa_required（密码已通过，但登录尚未完成）。
	// result 只能取 login_events_result_check 允许的四个值（success/failure/denied/blocked）：
	// 曾用 "challenge" 导致插入被约束拒绝、而链头已前进，在审计链上留下空洞。
	record(EventLoginDenied, "denied", ReasonMFARequired, "medium", identity.ID)
	return LoginResult{
		MFARequired:        true,
		ChallengeID:        challenge.Token,
		ChallengeExpiresAt: challenge.ExpiresAt,
		Identity:           identity,
	}, nil
}

// CompleteMFALogin 在第二步校验通过后建会话（MFASatisfied=true，AuthMethod=password+mfa）。
//
// 入参全部来自挑战落库时的服务端记录，客户端只能提供 challenge_id 与验证码；
// 这里重新校验身份状态与工作区成员资格，因为它们可能在 5 分钟窗口内被管理员改掉。
func (s *Service) CompleteMFALogin(ctx context.Context, in MFACompletion) (LoginResult, error) {
	if s == nil || s.repo == nil {
		return LoginResult{}, errors.New("iam: service unavailable")
	}
	start := time.Now()
	identity, err := s.repo.GetIdentityByID(ctx, in.IdentityID)
	if err != nil {
		// 挑战里的身份已不存在：对外与凭据错误同形，不泄露账号存在性。
		return LoginResult{}, ErrInvalidCredentials
	}
	switch identity.Status {
	case StatusDeleted, StatusDisabled:
		return LoginResult{}, ErrAccountDisabled
	case StatusLocked:
		return LoginResult{}, ErrAccountLocked
	}
	if identity.LockedUntil != nil && identity.LockedUntil.After(time.Now()) {
		return LoginResult{}, ErrAccountLocked
	}
	audience := in.Audience
	if audience == "" {
		audience = AudienceUser
	}
	device := ParseUserAgent(in.UserAgent)
	proxy := in.ProxyChain
	if proxy == nil {
		proxy = []string{}
	}
	ip := ClientIP{ClientIP: in.ClientIP, PeerIP: in.PeerIP, ProxyChain: proxy}
	record := func(eventType, result, reason, risk, identityID string) {
		_ = RecordLoginEvent(ctx, s.repo.Pool(), s.cfg.Environment, LoginEvent{
			EventType: eventType, Result: result, ReasonCode: reason, RiskLevel: risk,
			IdentityID: identityID, SubjectEmail: identity.Email, Audience: audience,
			DeviceInfo: device, UserAgent: in.UserAgent, IP: ip,
			EntryHost: in.EntryHost, Scheme: in.Scheme,
			RequestID: in.RequestID, TraceID: in.TraceID,
			DurationMS: int(time.Since(start).Milliseconds()),
		})
	}
	// §6.6.1 auth.mfa.success：与随后的 auth.login.success 分开记账，
	// AUTH-004 的"MFA 成功/失败"因此可以直接按事件类型取数。
	_ = RecordLoginEvent(ctx, s.repo.Pool(), s.cfg.Environment, LoginEvent{
		EventType: EventMFASuccess, Result: "success", RiskLevel: "low",
		IdentityID: identity.ID, SubjectEmail: identity.Email, Audience: audience,
		AuthMethod: "password+mfa", MFAMethod: in.Method, MFAResult: "success",
		DeviceInfo: device, UserAgent: in.UserAgent, IP: ip,
		EntryHost: in.EntryHost, Scheme: in.Scheme,
		RequestID: in.RequestID, TraceID: in.TraceID,
		DurationMS: int(time.Since(start).Milliseconds()),
	})
	return s.issueSession(ctx, sessionIssue{
		Input: LoginInput{
			Email: identity.Email, Audience: audience, IP: ip,
			UserAgent: in.UserAgent, EntryHost: in.EntryHost, Scheme: in.Scheme,
			RequestID: in.RequestID, TraceID: in.TraceID, WorkspaceID: in.WorkspaceID,
		},
		Identity: identity, Device: device, Start: start,
		AccountKey:   strings.Join([]string{in.ClientIP, NormalizeEmail(identity.Email)}, "|"),
		AuthMethod:   "password+mfa",
		MFASatisfied: true,
		MFAMethod:    in.Method,
		Record:       record,
	})
}

// mfaResultFor 把通过方式映射为登录事件的 MFAResult（未走第二步时为空）。
func mfaResultFor(method string) string {
	if strings.TrimSpace(method) == "" {
		return ""
	}
	return "success"
}

// WorkspaceOption 是一个可供登录/切换显式选择的工作区。
type WorkspaceOption struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// DefaultWorkspace 已删除（P1-20260913）。
//
// 旧实现是 SELECT id FROM workspaces WHERE organization_id = $1 ORDER BY slug LIMIT 1，
// 不读 workspace_memberships：任何身份登录都会拿到组织内字母序第一个工作区。
// 仅属于 wsB 的 viewer 因此拿到 wsA 会话并读到 wsA 的导出列表与明细（真实泄露，已复现）。
// 现统一走 ResolveLoginWorkspace，只认成员资格且 fail closed。

// WorkspaceOptions 返回该身份在指定组织内**有效成员资格**对应的工作区，按 slug 稳定排序。
func (s *Service) WorkspaceOptions(ctx context.Context, identityID, orgID string) ([]WorkspaceOption, error) {
	rows, err := s.repo.Pool().Query(ctx, `
		SELECT w.id::text, w.slug, w.name
		FROM workspace_memberships wm
		JOIN workspaces w ON w.id = wm.workspace_id
		WHERE wm.identity_id = $1 AND wm.organization_id = $2 AND wm.status = 'active'
		  AND w.organization_id = $2
		ORDER BY w.slug`, identityID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkspaceOption{}
	for rows.Next() {
		var o WorkspaceOption
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ResolveLoginWorkspace 决定一次登录可以绑定的工作区（签发会话时的作用域）。
//
// 规则（P1-20260913）：
//   - 只从该 identity 在该 org 的 active workspace_memberships 中选择；
//   - 0 个 → ErrNoWorkspaceMembership：拒绝登录/签发会话，不回退组织默认工作区；
//   - 1 个 → 直接绑定；
//   - >1 个 → 必须由请求显式给出 workspace_id 且属于成员资格集合，否则
//     ErrWorkspaceSelectionRequired（附可选列表）。不得任取首行，也不得用组织默认。
func (s *Service) ResolveLoginWorkspace(ctx context.Context, identityID, orgID, requested string) (string, error) {
	if identityID == "" || orgID == "" {
		return "", ErrNoWorkspaceMembership
	}
	options, err := s.WorkspaceOptions(ctx, identityID, orgID)
	if err != nil {
		return "", err
	}
	return decideWorkspace(options, strings.TrimSpace(requested))
}

// decideWorkspace 是纯决策函数（无 IO），全部拒绝分支都能被单测覆盖。
func decideWorkspace(options []WorkspaceOption, requested string) (string, error) {
	if len(options) == 0 {
		return "", ErrNoWorkspaceMembership
	}
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, o := range options {
			if o.ID == requested {
				return o.ID, nil
			}
		}
		return "", ErrWorkspaceNotMember
	}
	if len(options) == 1 {
		return options[0].ID, nil
	}
	return "", &WorkspaceSelectionRequiredError{Options: options}
}

// WorkspaceSelectionRequiredError 携带可选工作区列表，供客户端渲染选择器后重试。
type WorkspaceSelectionRequiredError struct{ Options []WorkspaceOption }

func (e *WorkspaceSelectionRequiredError) Error() string { return "workspace selection required" }

func (e *WorkspaceSelectionRequiredError) Is(target error) bool {
	return target == ErrWorkspaceSelectionRequired
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
		errors.Is(err, ErrMFAUnavailable), errors.Is(err, ErrMFAEnrollmentRequired):
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
	case errors.Is(err, ErrMFAEnrollmentRequired):
		// 403 而不是 501：MFA 能力已实现，只是这个账号还没注册（策略要求先注册）。
		return http.StatusForbidden, "mfa_enrollment_required"
	case errors.Is(err, ErrSessionInvalid):
		return http.StatusUnauthorized, "session_invalid"
	case errors.Is(err, ErrNoWorkspaceMembership):
		return http.StatusForbidden, "no_workspace_membership"
	case errors.Is(err, ErrWorkspaceSelectionRequired):
		return http.StatusConflict, "workspace_selection_required"
	case errors.Is(err, ErrWorkspaceNotMember):
		return http.StatusForbidden, "workspace_not_member"
	case errors.Is(err, ErrWorkspaceAccessRevoked):
		return http.StatusUnauthorized, "workspace_access_revoked"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}
