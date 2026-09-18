package mfa

// 登录第二步的一次性挑战（计划 §21.4 / AUTH-004）。
//
// 契约：需要 MFA 时登录返回 202，challenge_id 仅可使用一次且 5 分钟过期。
//
// 设计要点：
//   - 明文 token 只在签发响应里出现一次，库里只存 SHA-256 摘要；
//   - 身份、受众与签发时的请求上下文由服务端落库，第二步不信任客户端自报字段；
//   - attempts 为提交次数（含成功的一次），并发下用"先自增再校验"的 UPDATE 保证计数原子；
//   - consumed_at 一次性消费，重复提交必然影响 0 行。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// ChallengeTTL 是挑战有效期（契约：5 分钟）。
	ChallengeTTL = 5 * time.Minute
	// ChallengeMaxAttempts 是同一挑战允许的提交次数上限（含成功的那一次）。
	ChallengeMaxAttempts = 5
	challengeTokenBytes  = 32

	// ChallengeMethodTOTP / ChallengeMethodRecoveryCode 是第二步的两种通过方式。
	ChallengeMethodTOTP         = "totp"
	ChallengeMethodRecoveryCode = "recovery_code"
)

var (
	// ErrChallengeInvalid：挑战不存在、已过期或已被使用。
	ErrChallengeInvalid = errors.New("mfa: challenge invalid")
	// ErrChallengeExhausted：同一挑战的提交次数已用尽。
	ErrChallengeExhausted = errors.New("mfa: challenge attempts exhausted")
)

// ChallengeRequest 是签发挑战所需的上下文（全部来自本次密码登录请求）。
type ChallengeRequest struct {
	IdentityID     string
	Audience       string
	OrganizationID string
	WorkspaceID    string
	ClientIP       string
	PeerIP         string
	ProxyChain     []string
	UserAgent      string
	EntryHost      string
	Scheme         string
	RequestID      string
	TraceID        string
}

// ChallengeTicket 是签发结果：明文 Token 只在此刻返回一次。
type ChallengeTicket struct {
	ID        string
	Token     string
	ExpiresAt time.Time
}

// ChallengeState 是挑战里保存的登录上下文，用于第二步建会话。
//
// 注意：ConsumeLoginChallenge 在返回 ErrInvalidCode / ErrChallengeExhausted 时
// **也会**返回已填好的 ChallengeState（身份可确定），供调用方写 MFA 失败审计；
// 只有挑战本身不可用时（ErrChallengeInvalid）才返回零值。
type ChallengeState struct {
	IdentityID     string
	Audience       string
	OrganizationID string
	WorkspaceID    string
	ClientIP       string
	PeerIP         string
	ProxyChain     []string
	UserAgent      string
	EntryHost      string
	Scheme         string
	Attempts       int
}

// IssueLoginChallenge 签发一次性挑战。明文 token 只在此刻返回一次。
func (s *Store) IssueLoginChallenge(ctx context.Context, in ChallengeRequest) (ChallengeTicket, error) {
	if s == nil || s.pool == nil {
		return ChallengeTicket{}, errors.New("mfa: store unavailable")
	}
	if strings.TrimSpace(in.IdentityID) == "" {
		return ChallengeTicket{}, errors.New("mfa: challenge requires identity")
	}
	raw := make([]byte, challengeTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return ChallengeTicket{}, fmt.Errorf("mfa: challenge token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().UTC().Add(ChallengeTTL)
	proxy := in.ProxyChain
	if proxy == nil {
		// text[] 列 NOT NULL：nil 切片会被编码成 NULL 并触发约束错误。
		proxy = []string{}
	}
	var id string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO mfa_login_challenges (
			token_hash, identity_id, audience, organization_id, workspace_id,
			client_ip, peer_ip, proxy_chain, user_agent, entry_host, scheme,
			request_id, trace_id, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id`,
		hashChallengeToken(token), strings.TrimSpace(in.IdentityID), audienceOrDefault(in.Audience),
		nullUUID(in.OrganizationID), nullUUID(in.WorkspaceID),
		truncateText(in.ClientIP, 64), truncateText(in.PeerIP, 64), proxy,
		truncateText(in.UserAgent, 512), truncateText(in.EntryHost, 255), truncateText(in.Scheme, 16),
		truncateText(in.RequestID, 128), truncateText(in.TraceID, 128), expires,
	).Scan(&id); err != nil {
		return ChallengeTicket{}, fmt.Errorf("mfa: insert challenge: %w", err)
	}
	return ChallengeTicket{ID: id, Token: token, ExpiresAt: expires}, nil
}

// ConsumeLoginChallenge 校验一次第二步提交并消费挑战。
//
// 成功返回 (state, method, nil)，method 为 totp 或 recovery_code。
// 验证码错误返回 (state, "", ErrInvalidCode)，尝试次数用尽返回 (state, "", ErrChallengeExhausted)。
func (s *Store) ConsumeLoginChallenge(ctx context.Context, token, code string) (ChallengeState, string, error) {
	empty := ChallengeState{}
	if s == nil || s.pool == nil {
		return empty, "", errors.New("mfa: store unavailable")
	}
	digest := hashChallengeToken(strings.TrimSpace(token))
	if digest == "" {
		return empty, "", ErrChallengeInvalid
	}
	var (
		st       ChallengeState
		expires  time.Time
		consumed *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT identity_id::text, audience,
		       COALESCE(organization_id::text, ''), COALESCE(workspace_id::text, ''),
		       COALESCE(client_ip, ''), COALESCE(peer_ip, ''), COALESCE(proxy_chain, '{}'),
		       COALESCE(user_agent, ''), COALESCE(entry_host, ''), COALESCE(scheme, ''),
		       attempts, expires_at, consumed_at
		  FROM mfa_login_challenges WHERE token_hash = $1`, digest).
		Scan(&st.IdentityID, &st.Audience, &st.OrganizationID, &st.WorkspaceID,
			&st.ClientIP, &st.PeerIP, &st.ProxyChain, &st.UserAgent, &st.EntryHost, &st.Scheme,
			&st.Attempts, &expires, &consumed)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, "", ErrChallengeInvalid
	}
	if err != nil {
		return empty, "", fmt.Errorf("mfa: read challenge: %w", err)
	}
	if consumed != nil || time.Now().UTC().After(expires) {
		return st, "", ErrChallengeInvalid
	}
	// 先记账再校验：并发提交无法用"多次猜测只算一次"绕过上限。
	tag, err := s.pool.Exec(ctx, `
		UPDATE mfa_login_challenges SET attempts = attempts + 1
		 WHERE token_hash = $1 AND consumed_at IS NULL AND attempts < $2`,
		digest, ChallengeMaxAttempts)
	if err != nil {
		return st, "", fmt.Errorf("mfa: count challenge attempt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return st, "", ErrChallengeExhausted
	}
	st.Attempts++

	okCode, usedRecovery, err := s.Verify(ctx, st.IdentityID, code)
	if err != nil {
		// ErrInvalidCode / ErrNotEnrolled 由调用方映射为 401，其余为内部错误。
		return st, "", err
	}
	if !okCode {
		return st, "", ErrInvalidCode
	}
	method := ChallengeMethodTOTP
	if usedRecovery {
		method = ChallengeMethodRecoveryCode
	}
	tag, err = s.pool.Exec(ctx, `
		UPDATE mfa_login_challenges SET consumed_at = now()
		 WHERE token_hash = $1 AND consumed_at IS NULL`, digest)
	if err != nil {
		return st, "", fmt.Errorf("mfa: consume challenge: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// 并发下另一个请求已先消费：本请求不得再建会话。
		return st, "", ErrChallengeInvalid
	}
	return st, method, nil
}

// HashChallengeToken 供测试与调用方在日志里定位挑战（不得用于比对明文）。
func HashChallengeToken(token string) string { return hashChallengeToken(token) }

func hashChallengeToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func audienceOrDefault(audience string) string {
	audience = strings.TrimSpace(audience)
	if audience == "" {
		return "user"
	}
	return audience
}

func nullUUID(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.TrimSpace(v)
}

func truncateText(v string, max int) string {
	if len(v) <= max {
		return v
	}
	return v[:max]
}
