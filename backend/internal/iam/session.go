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
	NamePrefix      string
	// RefreshGrace 允许旧令牌在轮换后的宽限期内继续使用，用于抵御并发请求竞态。
	RefreshGrace    time.Duration
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
		RefreshGrace:    30 * time.Second,
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
	CSRFHash        string            `json:"-"`
	Identity        Identity          `json:"identity"`
	Roles           []string          `json:"roles"`
	Attributes      map[string]string `json:"attributes,omitempty"`
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

	var id string
	err = m.repo.Pool().QueryRow(ctx, `
        INSERT INTO sessions (
            identity_id, organization_id, workspace_id, audience, token_hash, csrf_hash,
            auth_method, mfa_satisfied, user_agent, client_hints, device,
            peer_ip, client_ip, proxy_chain, expires_at, absolute_expires_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11::jsonb,$12::inet,$13::inet,$14,$15,$16)
        RETURNING id`,
		req.IdentityID, req.OrganizationID, nullIfEmpty(req.WorkspaceID), req.Audience,
		hashToken(token), hashToken(csrf), req.AuthMethod, req.MFASatisfied,
		truncate(req.UserAgent, 512), string(hintsJSON), string(deviceJSON),
		nullIfEmpty(req.IP.PeerIP), nullIfEmpty(req.IP.ClientIP), req.IP.ProxyChain,
		now.Add(m.cfg.IdleTTL), now.Add(m.cfg.AbsoluteTTL),
	).Scan(&id)
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
		ID: id, IdentityID: req.IdentityID, OrganizationID: req.OrganizationID,
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
	if sess.RevokedAt != nil || now.After(sess.ExpiresAt) || now.After(sess.AbsoluteExpires) {
		return Session{}, ErrSessionInvalid
	}
	if sess.Identity.Status != StatusActive && sess.Identity.Status != StatusInvited {
		return Session{}, ErrSessionInvalid
	}
	if sess.Identity.LockedUntil != nil && sess.Identity.LockedUntil.After(now) {
		return Session{}, ErrSessionInvalid
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
        SELECT s.id, s.identity_id, s.organization_id, s.workspace_id, s.audience, s.auth_method,
               s.mfa_satisfied, s.device_id, s.user_agent, s.device, s.client_hints,
               COALESCE(host(s.peer_ip), ''), COALESCE(host(s.client_ip), ''),
               s.created_at, s.last_seen_at, s.expires_at, s.absolute_expires_at,
               s.revoked_at, s.revoked_reason, s.csrf_hash,
               i.id, i.organization_id, i.email, i.display_name, i.status,
               i.is_platform_admin, i.mfa_required, i.must_change_password, i.failed_login_count,
               i.locked_until, i.last_login_at, i.password_changed_at, i.created_at, i.updated_at,
               EXISTS (SELECT 1 FROM credentials c WHERE c.identity_id = i.id AND c.kind = 'password' AND c.revoked_at IS NULL)
        FROM sessions s JOIN identities i ON i.id = s.identity_id
        WHERE s.token_hash = $1`, tokenHash).
		Scan(&sess.ID, &sess.IdentityID, &sess.OrganizationID, &workspaceID, &sess.Audience, &sess.AuthMethod,
			&sess.MFASatisfied, &deviceID, &sess.UserAgent, &deviceRaw, &hintsRaw,
			&sess.PeerIP, &sess.ClientIP,
			&sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &sess.AbsoluteExpires,
			&sess.RevokedAt, &sess.RevokedReason, &sess.CSRFHash,
			&sess.Identity.ID, &sess.Identity.OrganizationID, &sess.Identity.Email, &sess.Identity.DisplayName,
			&sess.Identity.Status, &sess.Identity.IsPlatformAdmin, &sess.Identity.MFARequired,
			&sess.Identity.MustChangePassword, &sess.Identity.FailedLoginCount, &sess.Identity.LockedUntil,
			&sess.Identity.LastLoginAt, &sess.Identity.PasswordChangedAt, &sess.Identity.CreatedAt,
			&sess.Identity.UpdatedAt, &sess.Identity.HasPassword)
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

// SetCookies 写入会话 Cookie。HttpOnly 的会话 Cookie 不可被 JS 读取（§28.4 验收）。
func (m *SessionManager) SetCookies(w http.ResponseWriter, audience, token, csrf string) {
	http.SetCookie(w, &http.Cookie{
		Name: m.cfg.CookieName(audience), Value: token, Path: m.cfg.Path, Domain: m.cfg.Domain,
		HttpOnly: true, Secure: m.cfg.Secure, SameSite: m.cfg.SameSite,
		MaxAge: int(m.cfg.AbsoluteTTL.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name: m.cfg.CSRFSecretCookie(audience), Value: csrf, Path: m.cfg.Path, Domain: m.cfg.Domain,
		HttpOnly: false, Secure: m.cfg.Secure, SameSite: m.cfg.SameSite,
		MaxAge: int(m.cfg.AbsoluteTTL.Seconds()),
	})
}

// ClearCookies 退出登录时清除 Cookie。
func (m *SessionManager) ClearCookies(w http.ResponseWriter, audience string) {
	for _, name := range []string{m.cfg.CookieName(audience), m.cfg.CSRFSecretCookie(audience)} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: m.cfg.Path, Domain: m.cfg.Domain,
			HttpOnly: name == m.cfg.CookieName(audience), Secure: m.cfg.Secure,
			SameSite: m.cfg.SameSite, MaxAge: -1,
		})
	}
}

// Rotate 轮换会话令牌：旧令牌立即失效，新令牌（与配套 CSRF）返回给调用方写入 Cookie。
// 满足 §21.4 "旋转 Session token，检测重放"。
func (m *SessionManager) Rotate(ctx context.Context, sess Session) (Created, error) {
	token, err := NewToken()
	if err != nil {
		return Created{}, err
	}
	csrf, err := NewToken()
	if err != nil {
		return Created{}, err
	}
	now := time.Now().UTC()
	newExpiry := now.Add(m.cfg.IdleTTL)
	if newExpiry.After(sess.AbsoluteExpires) {
		newExpiry = sess.AbsoluteExpires
	}
	_, err = m.repo.Pool().Exec(ctx, `UPDATE sessions
        SET token_hash = $2, csrf_hash = $3, last_seen_at = now(), expires_at = $4
        WHERE id = $1 AND revoked_at IS NULL`, sess.ID, hashToken(token), hashToken(csrf), newExpiry)
	if err != nil {
		return Created{}, fmt.Errorf("rotate session: %w", err)
	}
	sess.CSRFHash = hashToken(csrf)
	sess.ExpiresAt = newExpiry
	sess.LastSeenAt = now
	return Created{Session: sess, Token: token, CSRF: csrf}, nil
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
