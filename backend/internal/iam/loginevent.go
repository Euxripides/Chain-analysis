package iam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
)

// 登录事件类型（§6.6.1）。
const (
	EventLoginSuccess     = "auth.login.success"
	EventLoginFailure     = "auth.login.failure"
	EventLogout           = "auth.logout"
	EventSessionRefresh   = "auth.session.refresh"
	EventSessionTouch     = "auth.session.touch"
	EventSessionRevoked   = "auth.session.revoked"
	EventSessionForced    = "auth.session.forced_offline"
	EventLoginDenied      = "auth.login.denied"
	EventLoginBlocked     = "auth.login.blocked"
	EventPasswordChanged  = "auth.password.changed"
	EventPasswordReset    = "auth.password.reset"
	EventMFASuccess       = "auth.mfa.success"
	EventMFAFailure       = "auth.mfa.failure"
	EventMFARecoveryUsed  = "auth.mfa.recovery_used"
	EventDeviceTrusted    = "auth.device.trusted"
	EventSessionExpired   = "auth.session.expired"
)

// 失败原因码：对客户端统一返回 invalid_credentials，原因码只入库（§6.6.2）。
const (
	ReasonOK               = ""
	ReasonUnknownAccount   = "unknown_account"
	ReasonBadPassword      = "bad_password"
	ReasonDisabledAccount  = "account_disabled"
	ReasonLockedAccount    = "account_locked"
	ReasonNoPassword       = "no_password_credential"
	ReasonRateLimited      = "rate_limited"
	ReasonAudienceMismatch = "audience_mismatch"
	ReasonMFARequired      = "mfa_required"
	ReasonMFAFailed        = "mfa_failed"
)

// LoginEvent 是 §6.6.2 定义的登录记录写入模型。
type LoginEvent struct {
	EventType    string
	Result       string
	ReasonCode   string
	RiskLevel    string
	RiskAction   string

	IdentityID     string
	SubjectEmail   string
	OrganizationID string
	WorkspaceID    string
	Audience       string
	AuthMethod     string
	IdentityProvider string
	MFAMethod      string
	MFAResult      string

	SessionID      string
	DeviceID       string
	IsNewDevice    bool
	DeviceTrusted  bool
	DeviceInfo     DeviceInfo

	ClientHints map[string]string
	UserAgent   string
	// OccurredAt 必须显式传给数据库并参与哈希载荷，否则校验时无法复算。
	OccurredAt  time.Time
	IP          ClientIP
	EntryHost   string
	Scheme      string
	RequestID   string
	TraceID     string
	DurationMS  int
	RateLimited bool
	Detail      map[string]any
}

// Record 写入一条登录事件。数据库不可用时返回错误，调用方按风险策略决定
// 是否 fail closed（登录属于高风险路径，默认拒绝并返回可重试错误）。
func RecordLoginEvent(ctx context.Context, pool audit.DB, env string, e LoginEvent) error {
	if pool == nil {
		return nil
	}
	if e.Result == "" {
		e.Result = "failure"
	}
	if e.RiskLevel == "" {
		e.RiskLevel = "low"
	}
	if e.Audience == "" {
		e.Audience = AudienceUser
	}
	if e.AuthMethod == "" {
		e.AuthMethod = "password"
	}
	if e.IdentityProvider == "" {
		e.IdentityProvider = "local"
	}
	if e.ClientHints == nil {
		e.ClientHints = map[string]string{}
	}
	if e.Detail == nil {
		e.Detail = map[string]any{}
	}
	// 与 audit.Writer 相同：截断到微秒，保证落库后可以复算哈希。
	e.OccurredAt = e.OccurredAt.UTC().Truncate(time.Microsecond)
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	if e.IP.ProxyChain == nil {
		e.IP.ProxyChain = []string{}
	}
	hintsJSON, err := json.Marshal(e.ClientHints)
	if err != nil {
		return err
	}
	detailJSON, err := json.Marshal(e.Detail)
	if err != nil {
		return err
	}

	// 载荷必须与 internal/audit.chainReaders["login"] 重建的结果逐字节一致。
	payload, err := audit.Canonicalize(audit.ChainPayloadOf(map[string]any{
		"occurred_at_unix_micro": e.OccurredAt.UTC().UnixMicro(),
		"event_type":             e.EventType,
		"result":                 e.Result,
		"reason_code":            e.ReasonCode,
		"identity_id":            e.IdentityID,
		"subject_digest":         SubjectDigest(e.SubjectEmail),
		"organization_id":        e.OrganizationID,
		"audience":               e.Audience,
		"auth_method":            e.AuthMethod,
		"session_id":             e.SessionID,
		"device_id":              e.DeviceID,
		"client_ip":              e.IP.ClientIP,
		"peer_ip":                e.IP.PeerIP,
		"user_agent":             e.UserAgent,
		"detail":                 e.Detail,
	}))
	if err != nil {
		return err
	}
	seq, prevHash, eventHash, err := audit.ChainAppend(ctx, pool, audit.ChainLogin, payload)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
        INSERT INTO login_events (
            id, chain, seq, occurred_at, event_version, environment, event_type, result, reason_code,
            risk_level, risk_action, identity_id, subject_digest, subject_masked, organization_id, workspace_id,
            audience, auth_method, identity_provider, mfa_method, mfa_result,
            session_id, device_id, is_new_device, device_trusted,
            device_label, device_type, device_vendor, device_model, os_name, os_version, browser_name, browser_version,
            user_agent, client_hints, peer_ip, client_ip, proxy_chain, entry_host, scheme,
            request_id, trace_id, duration_ms, rate_limited, detail, prev_hash, event_hash
        ) VALUES (
            $1,$2,$3,$4,1,$5,$6,$7,$8,
            $9,$10,$11,$12,$13,$14,$15,
            $16,$17,$18,$19,$20,
            $21,$22,$23,$24,
            $25,$26,$27,$28,$29,$30,$31,$32,
            $33,$34::jsonb,$35::inet,$36::inet,$37,$38,$39,
            $40,$41,$42,$43,$44::jsonb,$45,$46
        )`,
		uuid.NewString(), audit.ChainLogin, seq, e.OccurredAt.UTC(), env, e.EventType, e.Result, e.ReasonCode,
		e.RiskLevel, e.RiskAction, nullIfEmpty(e.IdentityID), SubjectDigest(e.SubjectEmail),
		MaskEmail(e.SubjectEmail), nullIfEmpty(e.OrganizationID), nullIfEmpty(e.WorkspaceID),
		e.Audience, e.AuthMethod, e.IdentityProvider, e.MFAMethod, e.MFAResult,
		nullIfEmpty(e.SessionID), nullIfEmpty(e.DeviceID), e.IsNewDevice, e.DeviceTrusted,
		truncate(e.DeviceInfo.Label, 200), e.DeviceInfo.DeviceType, e.DeviceInfo.DeviceVendor,
		truncate(e.DeviceInfo.DeviceModel, 120), e.DeviceInfo.OSName, e.DeviceInfo.OSVersion,
		e.DeviceInfo.BrowserName, e.DeviceInfo.BrowserVersion,
		truncate(e.UserAgent, 512), string(hintsJSON), nullIfEmpty(e.IP.PeerIP), nullIfEmpty(e.IP.ClientIP),
		e.IP.ProxyChain, truncate(e.EntryHost, 200), e.Scheme,
		truncate(e.RequestID, 128), truncate(e.TraceID, 128), e.DurationMS, e.RateLimited,
		string(detailJSON), prevHash, eventHash,
	)
	return err
}

// SubjectDigest 是账号的不可逆摘要，用于失败登录时在不暴露账号的前提下关联尝试。
func SubjectDigest(email string) string {
	if strings.TrimSpace(email) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(NormalizeEmail(email)))
	return hex.EncodeToString(sum[:])
}

// LoginEventFilter 用于后台登录记录查询（§6.6.5）。
type LoginEventFilter struct {
	IdentityID     string
	OrganizationID string
	Result         string
	EventType      string
	ClientIP       string
	SessionID      string
	From           *time.Time
	To             *time.Time
	Limit          int
	Offset         int
}

// LoginEventRow 是登录记录的读取视图。
type LoginEventRow struct {
	ID           string         `json:"id"`
	Seq          int64          `json:"seq"`
	OccurredAt   time.Time      `json:"occurred_at"`
	EventType    string         `json:"event_type"`
	Result       string         `json:"result"`
	ReasonCode   string         `json:"reason_code"`
	RiskLevel    string         `json:"risk_level"`
	IdentityID   string         `json:"identity_id,omitempty"`
	Email        string         `json:"email"`
	SubjectMasked string        `json:"subject_masked"`
	Audience     string         `json:"audience"`
	AuthMethod   string         `json:"auth_method"`
	SessionID    string         `json:"session_id,omitempty"`
	DeviceID     string         `json:"device_id,omitempty"`
	IsNewDevice  bool           `json:"is_new_device"`
	DeviceLabel  string         `json:"device_label"`
	DeviceType   string         `json:"device_type"`
	OSName       string         `json:"os_name"`
	BrowserName  string         `json:"browser_name"`
	PeerIP       string         `json:"peer_ip"`
	ClientIP     string         `json:"client_ip"`
	EntryHost    string         `json:"entry_host"`
	RequestID    string         `json:"request_id"`
	Detail       map[string]any `json:"detail"`
	EventHash    string         `json:"event_hash"`
	PrevHash     string         `json:"prev_hash"`
}

func QueryLoginEvents(ctx context.Context, pool *pgxpool.Pool, f LoginEventFilter) ([]LoginEventRow, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	args := []any{}
	where := []string{"1=1"}
	if f.IdentityID != "" {
		args = append(args, f.IdentityID)
		where = append(where, fmt.Sprintf("identity_id = $%d", len(args)))
	}
	if f.OrganizationID != "" {
		args = append(args, f.OrganizationID)
		where = append(where, fmt.Sprintf("organization_id = $%d", len(args)))
	}
	if f.Result != "" {
		args = append(args, f.Result)
		where = append(where, fmt.Sprintf("result = $%d", len(args)))
	}
	if f.EventType != "" {
		args = append(args, f.EventType)
		where = append(where, fmt.Sprintf("event_type = $%d", len(args)))
	}
	if f.ClientIP != "" {
		args = append(args, f.ClientIP)
		where = append(where, fmt.Sprintf("client_ip = $%d::inet", len(args)))
	}
	if f.SessionID != "" {
		args = append(args, f.SessionID)
		where = append(where, fmt.Sprintf("session_id = $%d", len(args)))
	}
	if f.From != nil {
		args = append(args, *f.From)
		where = append(where, fmt.Sprintf("occurred_at >= $%d", len(args)))
	}
	if f.To != nil {
		args = append(args, *f.To)
		where = append(where, fmt.Sprintf("occurred_at <= $%d", len(args)))
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM login_events WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := pool.Query(ctx, `
        SELECT id, seq, occurred_at, event_type, result, reason_code, risk_level,
               COALESCE(identity_id::text, ''), subject_masked, audience, auth_method,
               COALESCE(session_id::text, ''), COALESCE(device_id::text, ''), is_new_device,
               device_label, device_type, os_name, browser_name,
               COALESCE(host(peer_ip), ''), COALESCE(host(client_ip), ''), entry_host, request_id, detail,
               event_hash, prev_hash
        FROM login_events WHERE `+clause+
		fmt.Sprintf(" ORDER BY seq DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []LoginEventRow
	for rows.Next() {
		var r LoginEventRow
		var detailRaw []byte
		if err := rows.Scan(&r.ID, &r.Seq, &r.OccurredAt, &r.EventType, &r.Result, &r.ReasonCode, &r.RiskLevel,
			&r.IdentityID, &r.SubjectMasked, &r.Audience, &r.AuthMethod,
			&r.SessionID, &r.DeviceID, &r.IsNewDevice, &r.DeviceLabel, &r.DeviceType, &r.OSName, &r.BrowserName,
			&r.PeerIP, &r.ClientIP, &r.EntryHost, &r.RequestID, &detailRaw, &r.EventHash, &r.PrevHash); err != nil {
			return nil, 0, err
		}
		r.Detail = map[string]any{}
		_ = json.Unmarshal(detailRaw, &r.Detail)
		out = append(out, r)
	}
	return out, total, rows.Err()
}
