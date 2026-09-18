package store

import (
	"context"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/jackc/pgx/v5"
)

func (s *Store) GetAnkrProductionProfile(ctx context.Context) (model.DownloadPerformanceProfile, error) {
	var p model.DownloadPerformanceProfile
	err := s.Pool.QueryRow(ctx, `SELECT id,scope,chain,dataset,default_mode,safe_concurrency,fast_concurrency,turbo_concurrency,hard_ceiling,reject_boundary,memory_budget_bytes,absolute_rss_limit_bytes,page_size_default,request_timeout_seconds,range_sharding_enabled,version,verified_at,updated_at,updated_by FROM ankr_production_profiles WHERE scope='global' AND chain='' AND dataset='' LIMIT 1`).Scan(&p.ID, &p.Scope, &p.Chain, &p.Dataset, &p.DefaultMode, &p.SafeConcurrency, &p.FastConcurrency, &p.TurboConcurrency, &p.HardCeiling, &p.RejectBoundary, &p.MemoryBudgetBytes, &p.AbsoluteRSSLimitBytes, &p.PageSizeDefault, &p.RequestTimeoutSeconds, &p.RangeShardingEnabled, &p.Version, &p.VerifiedAt, &p.UpdatedAt, &p.UpdatedBy)
	return p, err
}

func (s *Store) GetAnkrRuntimeState(ctx context.Context) (model.PerformanceRuntimeState, error) {
	var st model.PerformanceRuntimeState
	err := s.Pool.QueryRow(ctx, `SELECT requested_mode,effective_mode,target_concurrency,active_concurrency,mode_reason,mode_changed_at,updated_at FROM ankr_performance_control WHERE singleton=TRUE`).Scan(&st.RequestedMode, &st.EffectiveMode, &st.TargetConcurrency, &st.ActiveConcurrency, &st.ModeReason, &st.ModeChangedAt, &st.UpdatedAt)
	return st, err
}

func (s *Store) SetAnkrRequestedMode(ctx context.Context, mode model.PerformanceMode, reason string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE ankr_performance_control SET configured_mode=$1,requested_mode=$1,mode_reason=$2,mode_changed_at=now(),updated_at=now() WHERE singleton=TRUE`, mode, reason)
	return err
}

func (s *Store) GetAnkrConfiguredMode(ctx context.Context) (model.PerformanceMode, error) {
	var raw string
	err := s.Pool.QueryRow(ctx, `SELECT configured_mode FROM ankr_performance_control WHERE singleton=TRUE`).Scan(&raw)
	if err != nil {
		return "", err
	}
	return model.ParsePerformanceMode(raw)
}

func (s *Store) HighestActiveRequestedMode(ctx context.Context) (model.PerformanceMode, bool, error) {
	var raw string
	err := s.Pool.QueryRow(ctx, `SELECT requested_mode FROM download_jobs WHERE status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','RESUMING') ORDER BY CASE requested_mode WHEN 'turbo' THEN 3 WHEN 'fast' THEN 2 ELSE 1 END DESC LIMIT 1`).Scan(&raw)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	mode, err := model.ParsePerformanceMode(raw)
	return mode, true, err
}

func (s *Store) SetAnkrRuntimeState(ctx context.Context, st model.PerformanceRuntimeState) error {
	if st.ModeChangedAt.IsZero() {
		st.ModeChangedAt = time.Now()
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// WAL 优化（2026-09-17）：这是 5 秒心跳路径（internal/download/performance.go 调用），
	// 实测 12 次 UPDATE/分钟，而 singleton 行的值绝大多数时间不变；每一行新版本都会产生
	// WAL 与死元组，24h 清理前会一直堆积。加 WHERE 守卫：只有业务值真的变化时才写。
	//
	// 有意**不参与**比较的两列：
	//   - mode_changed_at：调用方每次都会传一个新时间戳（st.ModeChangedAt 为空时还会就地赋
	//     time.Now()），参与比较会让守卫恒真；不参与之后它的语义更准确——“最后一次模式真正
	//     发生变化的时间”，而不是“最后一次心跳时间”。
	//   - updated_at：在 SET 里被赋成 now()，参与比较同样会让守卫恒真、完全失效。
	_, err = tx.Exec(ctx, `UPDATE ankr_performance_control SET requested_mode=$1,effective_mode=$2,target_concurrency=$3,active_concurrency=$4,mode_reason=$5,mode_changed_at=$6,updated_at=now() WHERE singleton=TRUE AND (requested_mode IS DISTINCT FROM $1 OR effective_mode IS DISTINCT FROM $2 OR target_concurrency IS DISTINCT FROM $3 OR active_concurrency IS DISTINCT FROM $4 OR mode_reason IS DISTINCT FROM $5)`, st.RequestedMode, st.EffectiveMode, st.TargetConcurrency, st.ActiveConcurrency, st.ModeReason, st.ModeChangedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE download_jobs SET effective_mode=$1,mode_reason=$2,mode_changed_at=$3,updated_at=now() WHERE status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','RESUMING') AND (effective_mode<>$1 OR mode_reason<>$2)`, st.EffectiveMode, st.ModeReason, st.ModeChangedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetJobPerformanceMode(ctx context.Context, id string, mode model.PerformanceMode, reason string) error {
	r, err := s.Pool.Exec(ctx, `UPDATE download_jobs SET requested_mode=$2,mode_reason=$3,mode_changed_at=now(),updated_at=now() WHERE id=$1`, id, mode, reason)
	if err != nil {
		return err
	}
	if r.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
