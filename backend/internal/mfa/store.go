package mfa

// DB 支撑层：注册、校验、恢复码与关闭。密码学部分在 totp.go（已用 RFC 6238 官方向量钉死），
// 这里只负责持久化与防重放，不碰会话、不碰 HTTP。

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
)

var (
	// ErrNotEnrolled：该身份还没有确认过的 MFA。
	ErrNotEnrolled = errors.New("mfa: not enrolled")
	// ErrInvalidCode：验证码不对（含过期与重放）。
	ErrInvalidCode = errors.New("mfa: invalid code")
	// ErrAlreadyEnrolled：已确认注册，需先关闭再重新注册（避免静默换绑）。
	ErrAlreadyEnrolled = errors.New("mfa: already enrolled")
)

// Store 是 MFA 的持久化入口。pool 与其它 store 相同（运行角色，请求事务内可用）。
type Store struct{ pool *dbtx.Pool }

func NewStore(pool *dbtx.Pool) *Store { return &Store{pool: pool} }

// StartEnroll 生成新密钥并写入未确认状态；返回密钥与 otpauth URI（供渲染二维码）。
// 已确认注册的身份不会被静默覆盖。
func (s *Store) StartEnroll(ctx context.Context, identityID, issuer, account string) (string, string, error) {
	if s == nil || s.pool == nil {
		return "", "", errors.New("mfa: store unavailable")
	}
	var confirmed *time.Time
	err := s.pool.QueryRow(ctx, "SELECT confirmed_at FROM identity_mfa WHERE identity_id = $1", identityID).Scan(&confirmed)
	switch {
	case err == nil && confirmed != nil:
		return "", "", ErrAlreadyEnrolled
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return "", "", fmt.Errorf("mfa: read enroll state: %w", err)
	}
	secret, err := NewSecret()
	if err != nil {
		return "", "", err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO identity_mfa (identity_id, secret, confirmed_at, last_used_step, updated_at)
		VALUES ($1, $2, NULL, -1, now())
		ON CONFLICT (identity_id) DO UPDATE
		  SET secret = EXCLUDED.secret, confirmed_at = NULL, last_used_step = -1, updated_at = now()
		  WHERE identity_mfa.confirmed_at IS NULL`, identityID, secret); err != nil {
		return "", "", fmt.Errorf("mfa: store enroll secret: %w", err)
	}
	return secret, ProvisioningURI(issuer, account, secret), nil
}

// ConfirmEnroll 用一次正确的验证码确认注册，并同时下发恢复码（明文只此一次，库里只存摘要）。
func (s *Store) ConfirmEnroll(ctx context.Context, identityID, code string) ([]string, error) {
	secret, confirmed, err := s.loadSecret(ctx, identityID)
	if err != nil {
		return nil, err
	}
	if confirmed {
		return nil, ErrAlreadyEnrolled
	}
	if !Verify(secret, code, time.Now().Unix(), DefaultSkewSteps) {
		return nil, ErrInvalidCode
	}
	plain, digests, err := RecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("mfa: begin confirm: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "UPDATE identity_mfa SET confirmed_at = now(), last_used_step = $2, updated_at = now() WHERE identity_id = $1",
		identityID, time.Now().Unix()/PeriodSeconds); err != nil {
		return nil, fmt.Errorf("mfa: confirm enroll: %w", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM mfa_recovery_codes WHERE identity_id = $1", identityID); err != nil {
		return nil, fmt.Errorf("mfa: reset recovery codes: %w", err)
	}
	for _, digest := range digests {
		if _, err := tx.Exec(ctx, "INSERT INTO mfa_recovery_codes (identity_id, digest) VALUES ($1, $2)", identityID, digest[:]); err != nil {
			return nil, fmt.Errorf("mfa: store recovery code: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("mfa: commit confirm: %w", err)
	}
	return plain, nil
}

// Verify 校验一次登录/step-up：先试 TOTP（同一步只能用一次），再试未使用的恢复码。
// 返回值 usedRecovery 让调用方在审计里区分"用了恢复码"。
func (s *Store) Verify(ctx context.Context, identityID, code string) (bool, bool, error) {
	secret, confirmed, err := s.loadSecret(ctx, identityID)
	if err != nil {
		return false, false, err
	}
	if !confirmed {
		return false, false, ErrNotEnrolled
	}
	now := time.Now().Unix()
	if Verify(secret, code, now, DefaultSkewSteps) {
		step := now / PeriodSeconds
		// 防重放：只允许把 last_used_step 往前推，且必须是严格更大的步。
		tag, err := s.pool.Exec(ctx, `UPDATE identity_mfa SET last_used_step = $2, updated_at = now()
			WHERE identity_id = $1 AND last_used_step < $2`, identityID, step)
		if err != nil {
			return false, false, fmt.Errorf("mfa: advance step: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return false, false, ErrInvalidCode // 同一步的码被重复提交
		}
		return true, false, nil
	}
	digest := HashRecoveryCode(code)
	tag, err := s.pool.Exec(ctx, `UPDATE mfa_recovery_codes SET used_at = now()
		WHERE identity_id = $1 AND digest = $2 AND used_at IS NULL`, identityID, digest[:])
	if err != nil {
		return false, false, fmt.Errorf("mfa: consume recovery code: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return true, true, nil
	}
	return false, false, ErrInvalidCode
}

// Status 返回是否已确认注册（供登录流程决定是否要求第二步）。
func (s *Store) Status(ctx context.Context, identityID string) (bool, error) {
	_, confirmed, err := s.loadSecret(ctx, identityID)
	if errors.Is(err, ErrNotEnrolled) {
		return false, nil
	}
	return confirmed, err
}

// Summary 返回该身份的 MFA 现状：是否已确认注册、还有多少未使用的恢复码。
//
// 供管理台「重置 MFA」做影响预览与结果回执：重置不可逆，调用方必须先知道会删掉什么。
func (s *Store) Summary(ctx context.Context, identityID string) (bool, int, error) {
	if s == nil || s.pool == nil {
		return false, 0, errors.New("mfa: store unavailable")
	}
	var confirmed *time.Time
	err := s.pool.QueryRow(ctx, "SELECT confirmed_at FROM identity_mfa WHERE identity_id = $1", identityID).Scan(&confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("mfa: read enroll state: %w", err)
	}
	if confirmed == nil {
		// 未确认的注册不算启用：登录不受影响，也没有恢复码可删。
		return false, 0, nil
	}
	var codes int
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM mfa_recovery_codes WHERE identity_id = $1 AND used_at IS NULL", identityID).Scan(&codes); err != nil {
		return false, 0, fmt.Errorf("mfa: count recovery codes: %w", err)
	}
	return true, codes, nil
}

// Disable 自助关闭：删除密钥与全部恢复码（下次登录不再要求第二步）。
func (s *Store) Disable(ctx context.Context, identityID string) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM mfa_recovery_codes WHERE identity_id = $1", identityID); err != nil {
		return fmt.Errorf("mfa: delete recovery codes: %w", err)
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM identity_mfa WHERE identity_id = $1", identityID); err != nil {
		return fmt.Errorf("mfa: delete secret: %w", err)
	}
	return nil
}

func (s *Store) loadSecret(ctx context.Context, identityID string) (string, bool, error) {
	if s == nil || s.pool == nil {
		return "", false, errors.New("mfa: store unavailable")
	}
	var secret string
	var confirmed *time.Time
	err := s.pool.QueryRow(ctx, "SELECT secret, confirmed_at FROM identity_mfa WHERE identity_id = $1", identityID).
		Scan(&secret, &confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotEnrolled
	}
	if err != nil {
		return "", false, fmt.Errorf("mfa: read secret: %w", err)
	}
	return secret, confirmed != nil, nil
}
