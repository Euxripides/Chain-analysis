package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// 身份状态机（§20.1）。
const (
	StatusInvited  = "invited"
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusLocked   = "locked"
	// StatusDeleted 是「删除（离职）」后的终态（迁移 0067）：身份行**保留**
	//（审计与 login_events 通过外键引用它），email/display_name 被匿名化，
	// 一切登录路径都必须像 disabled 一样拒绝它。
	StatusDeleted = "deleted"
)

// ErrIdentityNotFound 与 ErrInvalidCredentials 对外都收敛为同一响应。
var ErrIdentityNotFound = errors.New("identity not found")

// ErrNoPassword 表示账号存在但没有可用密码凭据（需走重置/初始化流程）。
var ErrNoPassword = errors.New("identity has no password credential")

type Identity struct {
	ID              string `json:"id"`
	OrganizationID  string `json:"organization_id"`
	Email           string `json:"email"`
	DisplayName     string `json:"display_name"`
	Status          string `json:"status"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
	MFARequired     bool   `json:"mfa_required"`
	// MFAEnrolled 表示该身份已确认注册 TOTP（identity_mfa.confirmed_at 非空）。
	// 与 MFARequired（管理员策略要求）语义不同：前者是事实，后者是要求。
	MFAEnrolled        bool       `json:"mfa_enrolled"`
	MustChangePassword bool       `json:"must_change_password"`
	FailedLoginCount   int        `json:"failed_login_count"`
	LockedUntil        *time.Time `json:"locked_until,omitempty"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	PasswordChangedAt  *time.Time `json:"password_changed_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	HasPassword        bool       `json:"has_password"`
	Roles              []string   `json:"roles,omitempty"`
}

const identityColumns = `i.id, i.organization_id, i.email, i.display_name, i.status,
    i.is_platform_admin, i.mfa_required, i.must_change_password, i.failed_login_count,
    i.locked_until, i.last_login_at, i.password_changed_at, i.created_at, i.updated_at,
    EXISTS (SELECT 1 FROM credentials c WHERE c.identity_id = i.id AND c.kind = 'password' AND c.revoked_at IS NULL),
    EXISTS (SELECT 1 FROM identity_mfa m WHERE m.identity_id = i.id AND m.confirmed_at IS NOT NULL)`

func scanIdentity(row pgx.Row) (Identity, error) {
	var it Identity
	err := row.Scan(&it.ID, &it.OrganizationID, &it.Email, &it.DisplayName, &it.Status,
		&it.IsPlatformAdmin, &it.MFARequired, &it.MustChangePassword, &it.FailedLoginCount,
		&it.LockedUntil, &it.LastLoginAt, &it.PasswordChangedAt, &it.CreatedAt, &it.UpdatedAt, &it.HasPassword,
		&it.MFAEnrolled)
	return it, err
}

// Repo 是 IAM 的 PostgreSQL 访问层。所有查询都以 identity_id / organization_id 为边界。
type Repo struct {
	pool *dbtx.Pool
}

func NewRepo(pool *dbtx.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) Pool() *dbtx.Pool { return r.pool }

// NormalizeEmail 统一规范化，避免大小写/空白造成重复账号。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// GetIdentityByEmail 按大小写不敏感匹配查找身份。
func (r *Repo) GetIdentityByEmail(ctx context.Context, email string) (Identity, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+identityColumns+` FROM identities i WHERE lower(i.email) = lower($1)`, NormalizeEmail(email))
	it, err := scanIdentity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrIdentityNotFound
	}
	return it, err
}

func (r *Repo) GetIdentityByID(ctx context.Context, id string) (Identity, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+identityColumns+` FROM identities i WHERE i.id = $1`, id)
	it, err := scanIdentity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrIdentityNotFound
	}
	return it, err
}

// IdentityFilter 用于后台用户列表；orgID 为空表示仅平台管理员可跨组织查询。
type IdentityFilter struct {
	OrganizationID string
	Search         string
	Status         string
	Limit          int
	Offset         int
}

func (r *Repo) ListIdentities(ctx context.Context, f IdentityFilter) ([]Identity, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{}
	where := []string{"1=1"}
	if f.OrganizationID != "" {
		args = append(args, f.OrganizationID)
		where = append(where, fmt.Sprintf("i.organization_id = $%d", len(args)))
	}
	if f.Search != "" {
		args = append(args, "%"+strings.ToLower(strings.TrimSpace(f.Search))+"%")
		where = append(where, fmt.Sprintf("(lower(i.email) LIKE $%d OR lower(i.display_name) LIKE $%d)", len(args), len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("i.status = $%d", len(args)))
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM identities i WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Limit, f.Offset)
	rows, err := r.pool.Query(ctx, `SELECT `+identityColumns+` FROM identities i WHERE `+clause+
		fmt.Sprintf(" ORDER BY i.created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Identity
	for rows.Next() {
		it, err := scanIdentity(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// EffectiveRoles 返回身份当前生效的角色 key 列表（已过滤撤销与过期）。
func (r *Repo) EffectiveRoles(ctx context.Context, identityID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
        SELECT DISTINCT ro.key
        FROM member_role_bindings b
        JOIN roles ro ON ro.id = b.role_id
        JOIN organization_memberships m ON m.identity_id = b.identity_id AND m.status = 'active'
        WHERE b.identity_id = $1 AND b.revoked_at IS NULL
          AND (b.expires_at IS NULL OR b.expires_at > now())
        ORDER BY ro.key`, identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// PasswordHash 返回启用中的密码凭据哈希。
func (r *Repo) PasswordHash(ctx context.Context, identityID string) (string, error) {
	var hash string
	err := r.pool.QueryRow(ctx, `SELECT secret_hash FROM credentials
        WHERE identity_id = $1 AND kind = 'password' AND revoked_at IS NULL`, identityID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoPassword
	}
	return hash, err
}

// SetPassword 新增或替换密码凭据，并作为单事务同时更新身份上的密码时间与锁定状态。
func (r *Repo) SetPassword(ctx context.Context, identityID, hash string, mustChange bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE credentials SET revoked_at = now(), updated_at = now()
        WHERE identity_id = $1 AND kind = 'password' AND revoked_at IS NULL`, identityID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credentials (id, identity_id, kind, algorithm, secret_hash)
        VALUES ($1, $2, 'password', 'argon2id', $3)`, uuid.NewString(), identityID, hash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identities SET password_changed_at = now(), must_change_password = $2,
        failed_login_count = 0, locked_until = NULL, status = CASE WHEN status = 'invited' THEN 'active' ELSE status END,
        updated_at = now() WHERE id = $1`, identityID, mustChange); err != nil {
		return err
	}
	// 密码变更后撤销该身份的全部会话（§6.3 强制下线）。
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = 'password_changed'
        WHERE identity_id = $1 AND revoked_at IS NULL`, identityID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkLoginSuccess 清除失败计数并更新最近登录信息。
func (r *Repo) MarkLoginSuccess(ctx context.Context, identityID, clientIP string) error {
	_, err := r.pool.Exec(ctx, `UPDATE identities SET failed_login_count = 0, locked_until = NULL,
        last_login_at = now(), last_login_ip = $2::inet, updated_at = now() WHERE id = $1`,
		identityID, nullIfEmpty(clientIP))
	return err
}

// MarkLoginFailure 递增失败计数，并按策略锁定账号。
func (r *Repo) MarkLoginFailure(ctx context.Context, identityID string, maxFailures int, lockFor time.Duration) (locked bool, err error) {
	if identityID == "" {
		return false, nil
	}
	var count int
	if err := r.pool.QueryRow(ctx, `UPDATE identities
        SET failed_login_count = failed_login_count + 1, updated_at = now()
        WHERE id = $1 RETURNING failed_login_count`, identityID).Scan(&count); err != nil {
		return false, err
	}
	if maxFailures > 0 && count >= maxFailures {
		if _, err := r.pool.Exec(ctx, `UPDATE identities SET locked_until = $2, updated_at = now() WHERE id = $1`,
			identityID, time.Now().Add(lockFor)); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// UnlockIfExpired 在锁定到期后自动恢复，避免永久锁定。
func (r *Repo) UnlockIfExpired(ctx context.Context, identityID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE identities SET locked_until = NULL, failed_login_count = 0, updated_at = now()
        WHERE id = $1 AND locked_until IS NOT NULL AND locked_until <= now()`, identityID)
	return err
}

func (r *Repo) SetStatus(ctx context.Context, identityID, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE identities SET status = $2, updated_at = now() WHERE id = $1`, identityID, status)
	return err
}

// CountActiveOwners 用于"最后一个 Owner 约束"（§28.5 验收）。
func (r *Repo) CountActiveOwners(ctx context.Context, organizationID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
        SELECT count(DISTINCT b.identity_id)
        FROM member_role_bindings b
        JOIN roles ro ON ro.id = b.role_id AND ro.key = 'owner'
        JOIN identities i ON i.id = b.identity_id AND i.status = 'active'
        WHERE b.revoked_at IS NULL AND b.scope_type = 'organization' AND b.scope_id = $1`,
		organizationID).Scan(&n)
	return n, err
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
