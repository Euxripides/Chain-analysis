package api

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const ankrPlatformSyncInterval = 15 * time.Minute

// StartProviderCenterSync refreshes the configured Ankr account in the API
// process. A PostgreSQL transaction lock prevents duplicate refreshes when
// multiple API instances are running.
func (s *Server) StartProviderCenterSync(ctx context.Context) {
	if s == nil || s.providerControl == nil || s.deps.Store == nil || s.deps.Store.Pool == nil {
		return
	}
	go func() {
		run := func() {
			callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			if err := s.syncAnkrPlatform(callCtx); err != nil && ctx.Err() == nil && s.deps.Log != nil {
				s.deps.Log.Warn("Ankr Platform account refresh failed", "err", err)
			}
		}
		run()
		ticker := time.NewTicker(ankrPlatformSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

func (s *Server) syncAnkrPlatform(ctx context.Context) error {
	state, err := s.providerCenterState(ctx, "ankr")
	if err != nil || !state.Enabled || state.PlatformKey == "" {
		return err
	}
	tx, err := s.deps.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	var acquired bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(790930103)`).Scan(&acquired); err != nil || !acquired {
		return err
	}
	var last *time.Time
	var source string
	err = tx.QueryRow(ctx, `SELECT synced_at,source FROM provider_official_snapshots WHERE provider_key='ankr'`).Scan(&last, &source)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if source == "Ankr Platform Swagger" && last != nil && time.Since(*last) < ankrPlatformSyncInterval {
		return nil
	}
	snapshot := fetchAnkrPlatform(ctx, state)
	if err := saveOfficialSnapshotTo(ctx, tx, snapshot); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
