// Package featureflag 在**真实请求路径**上求值功能开关（计划 §7.3 / §16.7）。
//
// 背景（2026-09-16 验收发现）：功能开关此前只有后台「试算」——`POST /api/admin/v1/
// feature-flags/{key}/commands/evaluate` 会把一次评估写进 feature_flag_evaluations，
// 但没有任何业务请求读过 feature_flags，因此"开关"改不动任何真实行为。
// 计划 §7.3 明确要求：「功能开关必须在真实请求路径求值，不能只在后台页面试算」。
//
// 本包提供两件事：
//
//  1. 唯一的求值算法 Evaluate：后台试算与真实请求路径共用同一份实现，
//     所以"试算说的"和"线上做的"不可能各说各话（此前 s6EvaluateFlag 只在 api 包内）；
//  2. 带短 TTL 缓存的 Flag 读取：开关是低频变更、高频读取的配置，
//     每次请求打一次库既没必要也会把热路径拖慢（实测表只有个位数行）。
//
// 边界（写进注释而不是靠约定）：
//   - 开关只用来**关闭能力面**，不用来关闭**安全与恢复路径**（Kill Switch 执行/恢复、
//     审批、登出、会话撤销等）。把安全开关做成可灰度的功能开关，等于给出一个"悄悄关掉
//     恢复能力"的入口。
//   - 求值失败（读库错误、无实例）一律返回"按开关默认值"的裁决并带 err，由调用方决定口径；
//     API 侧的口径是 fail-closed（见 internal/api/featureflag_gate.go）。
package featureflag

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Flag 是 feature_flags 的一行（只保留求值需要的字段）。
type Flag struct {
	Key              string
	Environment      string
	Enabled          bool
	DefaultValue     bool
	Percentage       int
	ScopeType        string
	ScopeOrgID       string
	ScopeWorkspaceID string
	ScopeUserID      string
	Revision         int64
}

// Decision 是一次求值的结果。Reason 是可解释的稳定字符串，直接落库/回显。
type Decision struct {
	Key      string
	Allowed  bool
	Reason   string
	Bucket   int
	Revision int64
}

// Evaluate 计算开关对某个主体的取值（确定性哈希分桶，结果可复现）。
//
// 与后台试算共用：s6EvaluateFlag 已改为调用本函数，避免两套算法漂移。
func Evaluate(f Flag, subjectID, organizationID, workspaceID, environment string) Decision {
	d := Decision{Key: f.Key, Revision: f.Revision}
	if f.Environment != "" && f.Environment != "all" && f.Environment != environment {
		d.Allowed, d.Reason = f.DefaultValue, "environment_mismatch"
		return d
	}
	if !f.Enabled {
		d.Allowed, d.Reason = f.DefaultValue, "disabled"
		return d
	}
	switch f.ScopeType {
	case "organization":
		if f.ScopeOrgID == "" || f.ScopeOrgID != organizationID {
			d.Allowed, d.Reason = f.DefaultValue, "scope_mismatch"
			return d
		}
	case "workspace":
		if f.ScopeWorkspaceID == "" || f.ScopeWorkspaceID != workspaceID {
			d.Allowed, d.Reason = f.DefaultValue, "scope_mismatch"
			return d
		}
	case "user":
		if f.ScopeUserID == "" || f.ScopeUserID != subjectID {
			d.Allowed, d.Reason = f.DefaultValue, "scope_mismatch"
			return d
		}
	}
	if f.Percentage >= 100 {
		d.Allowed, d.Reason, d.Bucket = true, "scope_match_full_rollout", 100
		return d
	}
	if f.Percentage <= 0 {
		d.Allowed, d.Reason, d.Bucket = f.DefaultValue, "scope_match_zero_rollout", 0
		return d
	}
	sum := sha256.Sum256([]byte(f.Key + ":" + subjectID))
	bucket := (int(sum[0])<<8 | int(sum[1])) % 100
	d.Bucket = bucket
	if bucket < f.Percentage {
		d.Allowed, d.Reason = true, "percentage_bucket_hit"
		return d
	}
	d.Allowed, d.Reason = false, "percentage_bucket_miss"
	return d
}

// DefaultTTL 是开关读取的缓存时间。
//
// 为什么是 10 秒：开关变更本身要经过 admin:flag:update + 审计，属于低频运维动作；
// 10 秒内的旧值不会造成安全问题（关掉一个能力面晚 10 秒生效），但能让热路径完全不碰库。
const DefaultTTL = 10 * time.Second

// Store 读取并缓存 feature_flags。
type Store struct {
	pool *pgxpool.Pool
	ttl  time.Duration
	log  *slog.Logger

	mu    sync.RWMutex
	flags map[string]Flag
	at    time.Time
}

// NewStore 构造缓存读取器。pool 为 nil 时 Lookup 恒返回 (Flag{}, false, nil)，
// 调用方据此走 fail-closed 口径（与"没有数据库就没有开关"一致）。
func NewStore(pool *pgxpool.Pool, ttl time.Duration, log *slog.Logger) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if log == nil {
		log = slog.Default()
	}
	return &Store{pool: pool, ttl: ttl, log: log}
}

const selectFlags = `SELECT key, coalesce(environment,'all'), enabled, default_value, percentage, coalesce(scope_type,'global'),
	coalesce(scope_org_id::text,''), coalesce(scope_workspace_id::text,''), coalesce(scope_user_id::text,''), revision FROM feature_flags`

// Lookup 返回某个开关。ok=false 表示开关不存在（调用方按 fail-closed 处理）。
func (s *Store) Lookup(ctx context.Context, key string) (Flag, bool, error) {
	if s == nil || s.pool == nil {
		return Flag{}, false, nil
	}
	flags, err := s.snapshot(ctx)
	if err != nil {
		return Flag{}, false, err
	}
	f, ok := flags[key]
	return f, ok, nil
}

// Invalidate 立即失效缓存（写开关后调用，避免最长 10 秒的旧值窗口）。
func (s *Store) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.at = time.Time{}
	s.mu.Unlock()
}

func (s *Store) snapshot(ctx context.Context) (map[string]Flag, error) {
	s.mu.RLock()
	if s.flags != nil && time.Since(s.at) < s.ttl {
		cached := s.flags
		s.mu.RUnlock()
		return cached, nil
	}
	s.mu.RUnlock()

	rows, err := s.pool.Query(ctx, selectFlags)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	loaded := make(map[string]Flag, 16)
	for rows.Next() {
		var f Flag
		if err := rows.Scan(&f.Key, &f.Environment, &f.Enabled, &f.DefaultValue, &f.Percentage,
			&f.ScopeType, &f.ScopeOrgID, &f.ScopeWorkspaceID, &f.ScopeUserID, &f.Revision); err != nil {
			return nil, err
		}
		loaded[f.Key] = f
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.flags, s.at = loaded, time.Now()
	s.mu.Unlock()
	return loaded, nil
}
