package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func (s *Store) GetAnkrProductionProfile(ctx context.Context) (model.DownloadPerformanceProfile, error) {
	var p model.DownloadPerformanceProfile
	err := s.Pool.QueryRow(ctx, `SELECT id, scope, chain, dataset, default_mode, safe_concurrency,
		fast_concurrency, turbo_concurrency, hard_ceiling, reject_boundary, memory_budget_bytes,
		absolute_rss_limit_bytes, page_size_default, request_timeout_seconds, range_sharding_enabled,
		version, verified_at, updated_at, updated_by FROM ankr_production_profiles
		WHERE scope='global' AND chain='' AND dataset='' LIMIT 1`).Scan(
		&p.ID, &p.Scope, &p.Chain, &p.Dataset, &p.DefaultMode, &p.SafeConcurrency,
		&p.FastConcurrency, &p.TurboConcurrency, &p.HardCeiling, &p.RejectBoundary,
		&p.MemoryBudgetBytes, &p.AbsoluteRSSLimitBytes, &p.PageSizeDefault,
		&p.RequestTimeoutSeconds, &p.RangeShardingEnabled, &p.Version, &p.VerifiedAt,
		&p.UpdatedAt, &p.UpdatedBy)
	return p, err
}

func (s *Store) GetAnkrRuntimeState(ctx context.Context) (model.PerformanceRuntimeState, error) {
	var state model.PerformanceRuntimeState
	err := s.Pool.QueryRow(ctx, `SELECT requested_mode, effective_mode, target_concurrency,
		active_concurrency, mode_reason, mode_changed_at, updated_at
		FROM ankr_scheduler_state WHERE singleton=TRUE`).Scan(&state.RequestedMode,
		&state.EffectiveMode, &state.TargetConcurrency, &state.ActiveConcurrency,
		&state.ModeReason, &state.ModeChangedAt, &state.UpdatedAt)
	return state, err
}

func (s *Store) SetAnkrRequestedMode(ctx context.Context, mode model.PerformanceMode, reason string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE ankr_scheduler_state SET configured_mode=$1, requested_mode=$1,
		mode_reason=$2, mode_changed_at=now(), updated_at=now() WHERE singleton=TRUE`, mode, reason)
	return err
}

func (s *Store) GetAnkrConfiguredMode(ctx context.Context) (model.PerformanceMode, error) {
	var raw string
	err := s.Pool.QueryRow(ctx, `SELECT configured_mode FROM ankr_scheduler_state WHERE singleton=TRUE`).Scan(&raw)
	if err != nil {
		return "", err
	}
	return model.ParsePerformanceMode(raw)
}

func (s *Store) HighestActiveRequestedMode(ctx context.Context) (model.PerformanceMode, bool, error) {
	var raw string
	err := s.Pool.QueryRow(ctx, `SELECT requested_mode FROM download_jobs
		WHERE status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','RESUMING')
		ORDER BY CASE requested_mode WHEN 'turbo' THEN 3 WHEN 'fast' THEN 2 ELSE 1 END DESC LIMIT 1`).Scan(&raw)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	mode, err := model.ParsePerformanceMode(raw)
	return mode, true, err
}

func (s *Store) SetAnkrRuntimeState(ctx context.Context, state model.PerformanceRuntimeState) error {
	if state.ModeChangedAt.IsZero() {
		state.ModeChangedAt = time.Now()
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `UPDATE ankr_scheduler_state SET requested_mode=$1, effective_mode=$2,
		target_concurrency=$3, active_concurrency=$4, mode_reason=$5, mode_changed_at=$6, updated_at=now()
		WHERE singleton=TRUE`, state.RequestedMode, state.EffectiveMode, state.TargetConcurrency,
		state.ActiveConcurrency, state.ModeReason, state.ModeChangedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE download_jobs SET effective_mode=$1, mode_reason=$2,
		mode_changed_at=$3, updated_at=now() WHERE status IN
		('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','RESUMING')
		AND (effective_mode<>$1 OR mode_reason<>$2)`, state.EffectiveMode, state.ModeReason, state.ModeChangedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetJobPerformanceMode(ctx context.Context, id string, mode model.PerformanceMode, reason string) error {
	result, err := s.Pool.Exec(ctx, `UPDATE download_jobs SET requested_mode=$2, mode_reason=$3,
		mode_changed_at=now(), updated_at=now() WHERE id=$1`, id, mode, reason)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
