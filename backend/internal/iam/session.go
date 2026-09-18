package iam

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audience 区分用户层与会话后台会话（§28.4 S2-BE-03）。
const (
	AudienceUser  = "user"
	AudienceAdmin = "admin"
)

var ErrSessionInvalid = errors.New("session invalid")

// ErrSessionReuse 表示客户端出示了一个已被 rotation 淘汰的令牌。
// 这通常意味着令牌泄露（攻击者与合法用户各持一份），必须按安全事件处理：
// 撤销整个家族，双方都被迫重新登录。与 ErrSessionInvalid（过期/普通撤销）区分开，
// 以便 API 层写审计、返回不同的用户提示。
var ErrSessionReuse = errors.New("session token reuse detected")

// ErrWorkspaceAccessRevoked 表示会话绑定的工作区成员资格已被撤销（P1-20260913）。
// 撤权必须让既有会话立即 fail closed：调用方据此撤销会话并写安全审计，
// 而不是让持有人继续用旧会话读取该工作区的数据。
var ErrWorkspaceAccessRevoked = errors.New("workspace membership revoked")

// 会话撤销原因。重放检测依赖 'rotated' 与其他原因的区分，不要随意改名。
const (
	RevokeReasonRotated        = "rotated"
	RevokeReasonLogout         = "logout"
	RevokeReasonSelfRevoked    = "self_revoked"
	RevokeReasonSelfRevokedAll = "self_revoked_all"
	RevokeReasonAdmin          = "revoked_by_admin"
	RevokeReasonPasswordChange = "password_changed"
	// RevokeReasonWorkspaceRevoked：会话绑定的工作区成员资格被撤销（P1-20260913）。
	RevokeReasonWorkspaceRevoked = "workspace_membership_revoked"
)

// SessionConfig 由环境变量注入；默认值对本地开发可用，生产必须显式设置 Cookie 安全属性。
type SessionConfig struct {
	UserCookieName  string
	AdminCookieName string
	CSRFHeader      string
	IdleTTL         time.Duration
	AbsoluteTTL     time.Duration
	Secure          bool
	SameSite        http.SameSite
	Domain          string
	Path            string
	// NamePrefix 支持 __Host- 前缀（要求 Secure + Path=/ + 无 Domain）。
	NamePrefix string
	// SecureMode 决定 Secure 属性与 __Host- 前缀如何生效（S7-SEC-01 R-05）：
	//   "always" —— 始终 Secure（要求入口全为 HTTPS，否则 Cookie 不会被浏览器接受）；
	//   "never"  —— 始终不 Secure（仅限本机 HTTP 调试）；
	//   "auto"（默认）—— 按**请求入口协议**决定：X-Forwarded-Proto=https 或直接 TLS 时
	//                才使用 Secure + NamePrefix，HTTP 入口退回不带前缀的普通 Cookie。
	// 这样公网 HTTPS 入口得到 __Host- 前缀 + Secure（§23.2），而内网 HTTP 测试链路行为不变。
	SecureMode string
}

// SecureModeAuto / Always / Never 见 SessionConfig.SecureMode。
const (
	SecureModeAuto   = "auto"
	SecureModeAlways = "always"
	SecureModeNever  = "never"
)

// SecureFor 依据入口协议判定本次响应是否应写 Secure Cookie。
func (c SessionConfig) SecureFor(scheme string) bool {
	switch c.SecureMode {
	case SecureModeAlways:
		return true
	case SecureModeNever:
		return false
	default:
		// auto：显式配置的 Secure=true 视为 always 的历史语义。
		if c.Secure {
			return true
		}
		return strings.EqualFold(strings.TrimSpace(scheme), "https")
	}
}

// CookieNameFor 返回指定安全上下文下的 Cookie 名。
// __Host- 前缀要求 Secure + Path=/ + 无 Domain，因此不安全的上下文里必须去掉前缀，
// 否则浏览器会直接拒绝该 Cookie。
func (c SessionConfig) CookieNameFor(audience string, secure bool) string {
	base := c.UserCookieName
	if audience == AudienceAdmin {
		base = c.AdminCookieName
	}
	if c.NamePrefix == "" || !secure {
		return base
	}
	return c.NamePrefix + base
}

// CSRFSecretCookieFor 返回 CSRF（double-submit）Cookie 名。
//
// **刻意不加 NamePrefix（__Host-）**：
//   - 前端按**固定名**读取该 Cookie 来填充 X-CSRF-Token
//     （frontend/packages/api-client/client.ts: csrfCookieName = cs_<audience>_session_csrf）。
//     服务端若下发 __Host- 前缀名，前端取不到值 → 所有写请求 403 csrf_failed
//     （S7 实测：加前缀后 7 条浏览器用例因"创建失败：请求校验失败"失败）。
//   - CSRF 令牌不是凭据：其有效性由服务端做"请求头 == Cookie == 会话绑定哈希"三方比对
//     （ValidateCSRF），第三方写入同名值也无法通过校验。
//   - 真正的凭据（会话令牌）仍然使用 __Host- 前缀 + Secure，防护目标不受影响。
//
// Secure 属性仍按入口安全上下文设置。
func (c SessionConfig) CSRFSecretCookieFor(audience string, secure bool) string {
	_ = secure
	return c.CSRFCookieBaseName(audience) + "_csrf"
}

// CSRFCookieBaseName 返回不含前缀的 CSRF Cookie 基名（与会话 Cookie 基名一致）。
func (c SessionConfig) CSRFCookieBaseName(audience string) string {
	if audience == AudienceAdmin {
		return c.AdminCookieName
	}
	return c.UserCookieName
}

// CookieNameCandidates 返回读取时应当尝试的 Cookie 名（先安全名，再普通名）。
// 这样部署从 HTTP 切到 HTTPS（或反向回滚）时，既有会话不会因为改名而全部失效。
func (c SessionConfig) CookieNameCandidates(audience string) []string {
	secureName := c.CookieNameFor(audience, true)
	plainName := c.CookieNameFor(audience, false)
	if secureName == plainName {
		return []string{plainName}
	}
	return []string{secureName, plainName}
}

// CSRFSecretCookieCandidates 返回读取时应尝试的 CSRF Cookie 名：
// 先无前缀名（当前写入形态），再带前缀名（兼容曾用过前缀写入的旧会话）。
func (c SessionConfig) CSRFSecretCookieCandidates(audience string) []string {
	plain := c.CSRFCookieBaseName(audience) + "_csrf"
	prefixed := c.CookieNameFor(audience, true) + "_csrf"
	if prefixed == plain {
		return []string{plain}
	}
	return []string{plain, prefixed}
}

func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		UserCookieName:  "cs_user_session",
		AdminCookieName: "cs_admin_session",
		CSRFHeader:      "X-CSRF-Token",
		IdleTTL:         12 * time.Hour,
		AbsoluteTTL:     7 * 24 * time.Hour,
		Secure:          false,
		SameSite:        http.SameSiteLaxMode,
		Path:            "/",
	}
}

func (c SessionConfig) CookieName(audience string) string {
	base := c.UserCookieName
	if audience == AudienceAdmin {
		base = c.AdminCookieName
	}
	if c.NamePrefix == "" {
		return base
	}
	return c.NamePrefix + base
}

func (c SessionConfig) CSRFSecretCookie(audience string) string {
	return c.CookieName(audience) + "_csrf"
}

// Session 是服务端会话视图。Token 本身永不落库、永不返回给前端 JS。
type Session struct {
	ID              string            `json:"id"`
	IdentityID      string            `json:"identity_id"`
	OrganizationID  string            `json:"organization_id"`
	WorkspaceID     string            `json:"workspace_id,omitempty"`
	Audience        string            `json:"audience"`
	AuthMethod      string            `json:"auth_method"`
	MFASatisfied    bool              `json:"mfa_satisfied"`
	DeviceID        string            `json:"device_id,omitempty"`
	Device          map[string]any    `json:"device,omitempty"`
	UserAgent       string            `json:"user_agent"`
	PeerIP          string            `json:"peer_ip,omitempty"`
	ClientIP        string            `json:"client_ip,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	LastSeenAt      time.Time         `json:"last_seen_at"`
	ExpiresAt       time.Time         `json:"expires_at"`
	AbsoluteExpires time.Time         `json:"absolute_expires_at"`
	RevokedAt       *time.Time        `json:"revoked_at,omitempty"`
	RevokedReason   string            `json:"revoked_reason,omitempty"`
	FamilyID        string            `json:"family_id,omitempty"`
	ParentID        string            `json:"parent_id,omitempty"`
	ReplacedBy      string            `json:"replaced_by,omitempty"`
	CSRFHash        string            `json:"-"`
	Identity        Identity          `json:"identity"`
	Roles           []string          `json:"roles"`
	Attributes      map[string]string `json:"attributes,omitempty"`
	// WorkspaceMember 表示会话绑定的工作区当前仍是该身份的 active 成员资格。
	// P1-20260913：撤权后既有会话必须 fail closed（见 Authenticate）。
	WorkspaceMember bool `json:"-"`
}

// SessionRequest 聚合创建会话所需的请求侧信息（IP / UA / Client Hints）。
type SessionRequest struct {
	Audience       string
	IdentityID     string
	OrganizationID string
	WorkspaceID    string
	AuthMethod     string
	MFASatisfied   bool
	IP             ClientIP
	UserAgent      string
	ClientHints    map[string]string
	DeviceInfo     DeviceInfo
	EntryHost      string
	Scheme         string
	// FamilyID 为空时新建一个令牌族；refresh 轮换时传入原家族 ID。
	FamilyID string
	// ParentID 指向被本次轮换淘汰的会话。
	ParentID string
}

// SessionManager 负责会话的创建、校验、续期和撤销。
type SessionManager struct {
	repo *Repo
	cfg  SessionConfig
}

func NewSessionManager(repo *Repo, cfg SessionConfig) *SessionManager {
	if cfg.UserCookieName == "" {
		cfg = DefaultSessionConfig()
	}
	if cfg.Path == "" {
		cfg.Path = "/"
	}
	return &SessionManager{repo: repo, cfg: cfg}
}

func (m *SessionManager) Config() SessionConfig { return m.cfg }
func (m *SessionManager) Repo() *Repo           { return m.repo }

// NewToken 生成不可猜测的不透明令牌。
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Created 是创建会话的返回值：Token/CSRF 明文只在此刻返回一次。
type Created struct {
	Session Session
	Token   string
	CSRF    string
}

func (m *SessionManager) Create(ctx context.Context, req SessionRequest) (Created, error) {
	token, err := NewToken()
	if err != nil {
		return Created{}, err
	}
	csrf, err := NewToken()
	if err != nil {
		return Created{}, err
	}
	now := time.Now().UTC()
	deviceJSON, _ := json.Marshal(req.DeviceInfo)
	hintsJSON, _ := json.Marshal(req.ClientHints)
	if len(hintsJSON) == 0 {
		hintsJSON = []byte("{}")
	}
	if req.Audience == "" {
		req.Audience = AudienceUser
	}
	if req.AuthMethod == "" {
		req.AuthMethod = "password"
	}
	if req.IP.ProxyChain == nil {
		// text[] 列是 NOT NULL：nil 切片会被编码成 NULL 并触发约束错误。
		req.IP.ProxyChain = []string{}
	}

	// 会话 ID 由服务端生成：family_id 需要指向家族首个会话，
	// 用一条 INSERT 同时落 id 与 family_id，避免"先插入再回填"的中间态。
	id := uuid.NewString()
	familyID := req.FamilyID
	if familyID == "" {
		familyID = id
	}
	_, err = m.repo.Pool().Exec(ctx, `
        INSERT INTO sessions (
            id, family_id, parent_id, identity_id, organization_id, workspace_id, audience,
            token_hash, csrf_hash, auth_method, mfa_satisfied, user_agent, client_hints, device,
            peer_ip, client_ip, proxy_chain, expires_at, absolute_expires_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb,$14::jsonb,
                  $15::inet,$16::inet,$17,$18,$19)`,
		id, familyID, nullIfEmpty(req.ParentID), req.IdentityID, req.OrganizationID,
		nullIfEmpty(req.WorkspaceID), req.Audience,
		hashToken(token), hashToken(csrf), req.AuthMethod, req.MFASatisfied,
		truncate(req.UserAgent, 512), string(hintsJSON), string(deviceJSON),
		nullIfEmpty(req.IP.PeerIP), nullIfEmpty(req.IP.ClientIP), req.IP.ProxyChain,
		now.Add(m.cfg.IdleTTL), now.Add(m.cfg.AbsoluteTTL))
	if err != nil {
		return Created{}, fmt.Errorf("create session: %w", err)
	}

	identity, err := m.repo.GetIdentityByID(ctx, req.IdentityID)
	if err != nil {
		return Created{}, err
	}
	roles, err := m.repo.EffectiveRoles(ctx, req.IdentityID)
	if err != nil {
		return Created{}, err
	}
	sess := Session{
		ID: id, FamilyID: familyID, ParentID: req.ParentID,
		IdentityID: req.IdentityID, OrganizationID: req.OrganizationID,
		WorkspaceID: req.WorkspaceID, Audience: req.Audience, AuthMethod: req.AuthMethod,
		MFASatisfied: req.MFASatisfied, UserAgent: req.UserAgent,
		PeerIP: req.IP.PeerIP, ClientIP: req.IP.ClientIP,
		CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(m.cfg.IdleTTL), AbsoluteExpires: now.Add(m.cfg.AbsoluteTTL),
		Identity: identity, Roles: roles, Device: map[string]any{"label": req.DeviceInfo.Label},
	}
	if req.DeviceInfo.Label != "" {
		sess.Device = map[string]any{
			"label": req.DeviceInfo.Label, "type": req.DeviceInfo.DeviceType,
			"os": req.DeviceInfo.OSName, "browser": req.DeviceInfo.BrowserName,
		}
	}
	return Created{Session: sess, Token: token, CSRF: csrf}, nil
}

// Authenticate 校验令牌：必须未撤销、未超时、身份为 active，且 audience 匹配。
// audience 不匹配一律拒绝，防止用户层会话调用后台 API。
func (m *SessionManager) Authenticate(ctx context.Context, token, audience string) (Session, error) {
	if strings.TrimSpace(token) == "" {
		return Session{}, ErrSessionInvalid
	}
	sess, err := m.loadByToken(ctx, hashToken(token))
	if err != nil {
		return Session{}, err
	}
	if sess.Audience != audience {
		return Session{}, ErrSessionInvalid
	}
	now := time.Now().UTC()
	if sess.RevokedAt != nil {
		// 被 rotation 淘汰的令牌再次出现 = 重放。此时不能只返回 401：
		// 无法判断当前持有者是不是合法用户，必须撤销整个家族。
		if sess.RevokedReason == RevokeReasonRotated {
			// 连同会话信息一起返回：调用方需要 family_id / identity_id
			// 才能撤销整个家族并写入可追溯的安全审计。
			return sess, ErrSessionReuse
		}
		return Session{}, ErrSessionInvalid
	}
	if now.After(sess.ExpiresAt) || now.After(sess.AbsoluteExpires) {
		return Session{}, ErrSessionInvalid
	}
	if sess.Identity.Status != StatusActive && sess.Identity.Status != StatusInvited {
		return Session{}, ErrSessionInvalid
	}
	if sess.Identity.LockedUntil != nil && sess.Identity.LockedUntil.After(now) {
		return Session{}, ErrSessionInvalid
	}
	// P1-20260913：工作区成员资格被撤销后，既有会话必须立即失效（fail closed），
	// 而不是等会话自然过期。workspace_id 为空的历史会话不受影响（无工作区作用域）。
	if !sess.WorkspaceMember {
		return sess, ErrWorkspaceAccessRevoked
	}
	roles, err := m.repo.EffectiveRoles(ctx, sess.IdentityID)
	if err != nil {
		return Session{}, err
	}
	sess.Roles = roles
	sess.Identity.Roles = roles

	// 会话续期：最多每 60 秒写一次，避免每个请求都产生写放大（§30.2）。
	if now.Sub(sess.LastSeenAt) > 60*time.Second {
		newExpiry := now.Add(m.cfg.IdleTTL)
		if newExpiry.After(sess.AbsoluteExpires) {
			newExpiry = sess.AbsoluteExpires
		}
		if _, err := m.repo.Pool().Exec(ctx,
			`UPDATE sessions SET last_seen_at = now(), expires_at = $2 WHERE id = $1 AND revoked_at IS NULL`,
			sess.ID, newExpiry); err == nil {
			sess.LastSeenAt = now
			sess.ExpiresAt = newExpiry
		}
	}
	return sess, nil
}

func (m *SessionManager) loadByToken(ctx context.Context, tokenHash string) (Session, error) {
	var sess Session
	var deviceRaw, hintsRaw []byte
	var workspaceID *string
	var deviceID *string
	err := m.repo.Pool().QueryRow(ctx, `
        SELECT s.id, s.family_id::text, COALESCE(s.parent_id::text,''), COALESCE(s.replaced_by::text,''),
               s.identity_id, s.organization_id, s.workspace_id, s.audience, s.auth_method,
               s.mfa_satisfied, s.device_id, s.user_agent, s.device, s.client_hints,
               COALESCE(host(s.peer_ip), ''), COALESCE(host(s.client_ip), ''),
               s.created_at, s.last_seen_at, s.expires_at, s.absolute_expires_at,
               s.revoked_at, s.revoked_reason, s.csrf_hash,
               i.id, i.organization_id, i.email, i.display_name, i.status,
               i.is_platform_admin, i.mfa_required, i.must_change_password, i.failed_login_count,
               i.locked_until, i.last_login_at, i.password_changed_at, i.created_at, i.updated_at,
               EXISTS (SELECT 1 FROM credentials c WHERE c.identity_id = i.id AND c.kind = 'password' AND c.revoked_at IS NULL),
               (s.workspace_id IS NULL OR EXISTS (
                   SELECT 1 FROM workspace_memberships wm
                   WHERE wm.identity_id = s.identity_id AND wm.workspace_id = s.workspace_id
                     AND wm.status = 'active'))
        FROM sessions s JOIN identities i ON i.id = s.identity_id
        WHERE s.token_hash = $1`, tokenHash).
		Scan(&sess.ID, &sess.FamilyID, &sess.ParentID, &sess.ReplacedBy,
			&sess.IdentityID, &sess.OrganizationID, &workspaceID, &sess.Audience, &sess.AuthMethod,
			&sess.MFASatisfied, &deviceID, &sess.UserAgent, &deviceRaw, &hintsRaw,
			&sess.PeerIP, &sess.ClientIP,
			&sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &sess.AbsoluteExpires,
			&sess.RevokedAt, &sess.RevokedReason, &sess.CSRFHash,
			&sess.Identity.ID, &sess.Identity.OrganizationID, &sess.Identity.Email, &sess.Identity.DisplayName,
			&sess.Identity.Status, &sess.Identity.IsPlatformAdmin, &sess.Identity.MFARequired,
			&sess.Identity.MustChangePassword, &sess.Identity.FailedLoginCount, &sess.Identity.LockedUntil,
			&sess.Identity.LastLoginAt, &sess.Identity.PasswordChangedAt, &sess.Identity.CreatedAt,
			&sess.Identity.UpdatedAt, &sess.Identity.HasPassword, &sess.WorkspaceMember)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionInvalid
	}
	if err != nil {
		return Session{}, err
	}
	if workspaceID != nil {
		sess.WorkspaceID = *workspaceID
	}
	if deviceID != nil {
		sess.DeviceID = *deviceID
	}
	sess.Device = map[string]any{}
	_ = json.Unmarshal(deviceRaw, &sess.Device)
	sess.Attributes = map[string]string{}
	_ = json.Unmarshal(hintsRaw, &sess.Attributes)
	return sess, nil
}

// ValidateCSRF 实现 double-submit：请求头令牌必须与 Cookie 中令牌一致，
// 且与数据库中该会话绑定的哈希一致（防止 Cookie 注入）。
func (m *SessionManager) ValidateCSRF(sess Session, headerToken, cookieToken string) bool {
	if headerToken == "" || cookieToken == "" {
		return false
	}
	if subtleEqual(headerToken, cookieToken) == false {
		return false
	}
	return subtleEqual(hashToken(headerToken), sess.CSRFHash)
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// SetCookies 写入会话 Cookie（沿用配置里的静态 Secure 判定，保留给测试与兼容调用）。
func (m *SessionManager) SetCookies(w http.ResponseWriter, audience, token, csrf string) {
	m.SetCookiesFor(w, audience, token, csrf, m.cfg.Secure)
}

// SetCookiesFor 按**本次请求的安全上下文**写入会话 Cookie。
//
// secure=true 时会同时使用 NamePrefix（例如 __Host-）与 Secure 属性；
// secure=false 时二者都必须去掉：__Host- 前缀要求 Secure，浏览器会拒绝不满足条件的 Cookie。
// HttpOnly 的会话 Cookie 不可被 JS 读取（§28.4 验收）。
func (m *SessionManager) SetCookiesFor(w http.ResponseWriter, audience, token, csrf string, secure bool) {
	name := m.cfg.CookieNameFor(audience, secure)
	csrfName := m.cfg.CSRFSecretCookieFor(audience, secure)
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: token, Path: m.cfg.Path, Domain: m.cfg.Domain,
		HttpOnly: true, Secure: secure, SameSite: m.cfg.SameSite,
		MaxAge: int(m.cfg.AbsoluteTTL.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfName, Value: csrf, Path: m.cfg.Path, Domain: m.cfg.Domain,
		HttpOnly: false, Secure: secure, SameSite: m.cfg.SameSite,
		MaxAge: int(m.cfg.AbsoluteTTL.Seconds()),
	})
}

// ClearCookies 退出登录时清除 Cookie（沿用配置里的静态 Secure 判定）。
func (m *SessionManager) ClearCookies(w http.ResponseWriter, audience string) {
	m.ClearCookiesFor(w, audience, m.cfg.Secure)
}

// ClearCookiesFor 清除**两种命名形态**的 Cookie：无论是带前缀（HTTPS 入口写入）
// 还是不带前缀（HTTP 入口或切换前写入），都要一并清除，否则会出现"退出后仍有残留会话 Cookie"。
func (m *SessionManager) ClearCookiesFor(w http.ResponseWriter, audience string, secure bool) {
	names := m.cfg.CookieNameCandidates(audience)
	for _, name := range names {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: m.cfg.Path, Domain: m.cfg.Domain,
			HttpOnly: true, Secure: secure,
			SameSite: m.cfg.SameSite, MaxAge: -1,
		})
	}
	for _, name := range m.cfg.CSRFSecretCookieCandidates(audience) {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: m.cfg.Path, Domain: m.cfg.Domain,
			HttpOnly: false, Secure: secure,
			SameSite: m.cfg.SameSite, MaxAge: -1,
		})
	}
}

// Rotate 轮换会话令牌（refresh token rotation）。
//
// 语义（§21.4 / §27.1 AUTH-005）：
//   - 每次 refresh 生成**新的一行**会话，沿用同一 family_id，并记录 parent_id；
//   - 旧行标记 revoked_reason='rotated'、replaced_by=<新会话 ID>；
//   - 旧令牌此后一旦被使用，Authenticate 返回 ErrSessionReuse，
//     由 RevokeFamily 撤销整个家族 —— 合法用户与攻击者同时被强制重新登录。
//
// 这样"轮换"才真正具备泄露检测能力：仅仅改同一行的 token_hash 只能让旧令牌 401，
// 无法区分"过期"与"令牌被复制"。
func (m *SessionManager) Rotate(ctx context.Context, sess Session) (Created, error) {
	created, err := m.Create(ctx, SessionRequest{
		Audience:       sess.Audience,
		IdentityID:     sess.IdentityID,
		OrganizationID: sess.OrganizationID,
		WorkspaceID:    sess.WorkspaceID,
		AuthMethod:     sess.AuthMethod,
		MFASatisfied:   sess.MFASatisfied,
		UserAgent:      sess.UserAgent,
		IP:             ClientIP{PeerIP: sess.PeerIP, ClientIP: sess.ClientIP, ProxyChain: []string{}},
		DeviceInfo:     DeviceInfo{},
		FamilyID:       sess.FamilyID,
		ParentID:       sess.ID,
	})
	if err != nil {
		return Created{}, err
	}
	if _, err := m.repo.Pool().Exec(ctx, `
        UPDATE sessions
        SET revoked_at = now(), revoked_reason = $2, replaced_by = $3,
            rotated_at = now(), rotation_count = rotation_count + 1
        WHERE id = $1 AND revoked_at IS NULL`,
		sess.ID, RevokeReasonRotated, created.Session.ID); err != nil {
		return Created{}, fmt.Errorf("retire rotated session: %w", err)
	}
	return created, nil
}

// RebindWorkspace 把会话的 workspace 作用域切换到另一个工作区。
//
// 调用方必须先完成成员资格判定（iam.ResolveLoginWorkspace）：本方法只做持久化，
// 并用 SQL 再确认一次目标工作区属于同一组织，避免把会话绑到跨组织工作区。
// 目标为空、会话已撤销、或工作区不属于该组织时 fail closed，不改变任何行。
func (m *SessionManager) RebindWorkspace(ctx context.Context, sess Session, workspaceID string) error {
	ws := strings.TrimSpace(workspaceID)
	if ws == "" {
		return ErrWorkspaceNotMember
	}
	tag, err := m.repo.Pool().Exec(ctx, `UPDATE sessions SET workspace_id = $1
        WHERE id = $2 AND organization_id = $3 AND revoked_at IS NULL
          AND EXISTS (SELECT 1 FROM workspaces w WHERE w.id = $1 AND w.organization_id = $3)`,
		ws, sess.ID, sess.OrganizationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkspaceNotMember
	}
	return nil
}

// RevokeFamily 撤销整个令牌族，返回被撤销的会话数。
// 由重放检测与"退出全部设备"共用。
func (m *SessionManager) RevokeFamily(ctx context.Context, familyID, reason, byID string) (int64, error) {
	if familyID == "" {
		return 0, nil
	}
	tag, err := m.repo.Pool().Exec(ctx, `UPDATE sessions
        SET revoked_at = now(), revoked_reason = $2, revoked_by = $3
        WHERE family_id = $1 AND revoked_at IS NULL`, familyID, reason, nullIfEmpty(byID))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RecordReuseEvent 记录一次令牌重放，供后台"风险事件"页与告警使用。
func (m *SessionManager) RecordReuseEvent(ctx context.Context, sess Session, revoked int64, ip ClientIP, userAgent, requestID, traceID string) error {
	if m.repo.Pool() == nil {
		return nil
	}
	_, err := m.repo.Pool().Exec(ctx, `
        INSERT INTO session_reuse_events (
            family_id, identity_id, organization_id, presented_session_id,
            revoked_at, revoked_reason, replaced_by, sessions_revoked,
            client_ip, peer_ip, user_agent, request_id, trace_id, detail)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::inet,$10::inet,$11,$12,$13,$14::jsonb)`,
		sess.FamilyID, sess.IdentityID, nullIfEmpty(sess.OrganizationID), sess.ID,
		sess.RevokedAt, sess.RevokedReason, nullIfEmpty(sess.ReplacedBy), revoked,
		nullIfEmpty(ip.ClientIP), nullIfEmpty(ip.PeerIP), truncate(userAgent, 512),
		truncate(requestID, 128), truncate(traceID, 128),
		fmt.Sprintf(`{"replaced_by":"%s","detected_by":"authenticate"}`, sess.ReplacedBy))
	return err
}

// DeviceRow 是设备列表读取视图。
type DeviceRow struct {
	ID           string     `json:"id"`
	Label        string     `json:"device_label"`
	Platform     string     `json:"platform"`
	Browser      string     `json:"browser"`
	FirstSeenAt  time.Time  `json:"first_seen_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	TrustedUntil *time.Time `json:"trusted_until,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	CreatedIP    string     `json:"created_ip"`
	LastIP       string     `json:"last_ip"`
}

// ListDevices 返回某个身份的设备列表（§21.4 GET /devices）。
func (m *SessionManager) ListDevices(ctx context.Context, identityID string) ([]DeviceRow, error) {
	rows, err := m.repo.Pool().Query(ctx, `
        SELECT id, device_label, platform, browser, first_seen_at, last_seen_at, trusted_until, revoked_at,
               COALESCE(host(created_ip), ''), COALESCE(host(last_ip), '')
        FROM trusted_devices WHERE identity_id = $1 ORDER BY last_seen_at DESC`, identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceRow
	for rows.Next() {
		var d DeviceRow
		if err := rows.Scan(&d.ID, &d.Label, &d.Platform, &d.Browser, &d.FirstSeenAt, &d.LastSeenAt,
			&d.TrustedUntil, &d.RevokedAt, &d.CreatedIP, &d.LastIP); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDevice 修改设备名称与信任状态（§21.4 PATCH /devices/{id}）。
func (m *SessionManager) UpdateDevice(ctx context.Context, identityID, deviceID, label string, trustDays int) error {
	var trustedUntil any
	if trustDays > 0 {
		trustedUntil = time.Now().Add(time.Duration(trustDays) * 24 * time.Hour)
	}
	_, err := m.repo.Pool().Exec(ctx, `UPDATE trusted_devices
        SET device_label = COALESCE(NULLIF($3, ''), device_label), trusted_until = $4, last_seen_at = now()
        WHERE id = $2 AND identity_id = $1`, identityID, deviceID, label, trustedUntil)
	return err
}

// RevokeDevice 撤销一个设备（同时撤销该设备上的全部会话）。
func (m *SessionManager) RevokeDevice(ctx context.Context, identityID, deviceID, reason string) error {
	tx, err := m.repo.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE trusted_devices SET revoked_at = now() WHERE id = $2 AND identity_id = $1`,
		identityID, deviceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = $3
        WHERE device_id = $2 AND identity_id = $1 AND revoked_at IS NULL`, identityID, deviceID, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RevokeByID 撤销指定会话。
func (m *SessionManager) RevokeByID(ctx context.Context, id, reason, byID string) error {
	_, err := m.repo.Pool().Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = $2, revoked_by = $3
        WHERE id = $1 AND revoked_at IS NULL`, id, reason, nullIfEmpty(byID))
	return err
}

// RevokeAllForIdentity 强制下线（退出全部设备 / 停用账号 / 改密）。
func (m *SessionManager) RevokeAllForIdentity(ctx context.Context, identityID, reason, byID string) (int64, error) {
	tag, err := m.repo.Pool().Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = $2, revoked_by = $3
        WHERE identity_id = $1 AND revoked_at IS NULL`, identityID, reason, nullIfEmpty(byID))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SessionFilter 用于后台会话列表。
type SessionFilter struct {
	OrganizationID string
	IdentityID     string
	Audience       string
	ActiveOnly     bool
	Limit          int
	Offset         int
}

func (m *SessionManager) List(ctx context.Context, f SessionFilter) ([]Session, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{}
	where := []string{"1=1"}
	if f.OrganizationID != "" {
		args = append(args, f.OrganizationID)
		where = append(where, fmt.Sprintf("s.organization_id = $%d", len(args)))
	}
	if f.IdentityID != "" {
		args = append(args, f.IdentityID)
		where = append(where, fmt.Sprintf("s.identity_id = $%d", len(args)))
	}
	if f.Audience != "" {
		args = append(args, f.Audience)
		where = append(where, fmt.Sprintf("s.audience = $%d", len(args)))
	}
	if f.ActiveOnly {
		where = append(where, "s.revoked_at IS NULL AND s.expires_at > now() AND s.absolute_expires_at > now()")
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := m.repo.Pool().QueryRow(ctx, `SELECT count(*) FROM sessions s WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := m.repo.Pool().Query(ctx, `
        SELECT s.id, s.identity_id, s.organization_id, s.workspace_id, s.audience, s.auth_method,
               s.mfa_satisfied, s.device_id, s.user_agent, s.device,
               COALESCE(host(s.peer_ip), ''), COALESCE(host(s.client_ip), ''),
               s.created_at, s.last_seen_at, s.expires_at, s.absolute_expires_at,
               s.revoked_at, s.revoked_reason,
               i.email, i.display_name, i.status
        FROM sessions s JOIN identities i ON i.id = s.identity_id
        WHERE `+clause+fmt.Sprintf(" ORDER BY s.last_seen_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var wsID, devID *string
		var deviceRaw []byte
		if err := rows.Scan(&s.ID, &s.IdentityID, &s.OrganizationID, &wsID, &s.Audience, &s.AuthMethod,
			&s.MFASatisfied, &devID, &s.UserAgent, &deviceRaw,
			&s.PeerIP, &s.ClientIP, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.AbsoluteExpires,
			&s.RevokedAt, &s.RevokedReason,
			&s.Identity.Email, &s.Identity.DisplayName, &s.Identity.Status); err != nil {
			return nil, 0, err
		}
		if wsID != nil {
			s.WorkspaceID = *wsID
		}
		if devID != nil {
			s.DeviceID = *devID
		}
		s.Device = map[string]any{}
		_ = json.Unmarshal(deviceRaw, &s.Device)
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// EnsureDevice 登记设备，返回是否为新设备。仅在服务端可确定身份时调用。
func (m *SessionManager) EnsureDevice(ctx context.Context, identityID string, info DeviceInfo, ip string) (deviceID string, isNew bool, err error) {
	if identityID == "" {
		return "", false, nil
	}
	var existing string
	err = m.repo.Pool().QueryRow(ctx, `SELECT id FROM trusted_devices
        WHERE identity_id = $1 AND platform = $2 AND browser = $3 AND revoked_at IS NULL
        ORDER BY last_seen_at DESC LIMIT 1`, identityID, info.OSName, info.BrowserName).Scan(&existing)
	if err == nil {
		_, _ = m.repo.Pool().Exec(ctx, `UPDATE trusted_devices SET last_seen_at = now(), last_ip = $2::inet WHERE id = $1`,
			existing, nullIfEmpty(ip))
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	id := uuid.NewString()
	err = m.repo.Pool().QueryRow(ctx, `INSERT INTO trusted_devices
        (id, identity_id, device_label, platform, browser, created_ip, last_ip)
        VALUES ($1,$2,$3,$4,$5,$6::inet,$7::inet) RETURNING id`,
		id, identityID, truncate(info.Label, 200), info.OSName, info.BrowserName,
		nullIfEmpty(ip), nullIfEmpty(ip)).Scan(&id)
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
