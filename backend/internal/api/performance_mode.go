package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func (s *Server) productionProfile(r *http.Request) model.DownloadPerformanceProfile {
	profile, err := s.deps.Store.GetAnkrProductionProfile(r.Context())
	if err == nil {
		return profile
	}
	return s.deps.Cfg.AnkrProductionProfile()
}

func (s *Server) handleAnkrProductionProfile(w http.ResponseWriter, r *http.Request) {
	p := s.productionProfile(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"version": p.Version, "default_mode": p.DefaultMode,
		"modes": map[string]any{
			"safe":  map[string]int{"target_concurrency": p.Resolve(model.PerformanceSafe)},
			"fast":  map[string]int{"target_concurrency": p.Resolve(model.PerformanceFast)},
			"turbo": map[string]int{"target_concurrency": p.Resolve(model.PerformanceTurbo)},
		},
		"hard_ceiling": p.HardCeiling, "reject_boundary": p.RejectBoundary,
		"memory_budget_bytes": p.MemoryBudgetBytes, "absolute_rss_limit_bytes": p.AbsoluteRSSLimitBytes,
		"page_size_default": p.PageSizeDefault, "request_timeout_seconds": p.RequestTimeoutSeconds,
		"range_sharding_enabled": p.RangeShardingEnabled, "verified_at": p.VerifiedAt,
	})
}

func (s *Server) handleAnkrFetchRuntime(w http.ResponseWriter, r *http.Request) {
	state, err := s.deps.Store.GetAnkrRuntimeState(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Ankr runtime state unavailable"})
		return
	}
	p := s.productionProfile(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"requested_mode": state.RequestedMode, "effective_mode": state.EffectiveMode,
		"safe_concurrency": p.Resolve(model.PerformanceSafe), "fast_concurrency": p.Resolve(model.PerformanceFast),
		"turbo_concurrency": p.Resolve(model.PerformanceTurbo), "target_concurrency": state.TargetConcurrency,
		"active_concurrency": state.ActiveConcurrency, "hard_ceiling": p.HardCeiling,
		"reject_boundary": p.RejectBoundary, "mode_reason": state.ModeReason,
		"mode_changed_at": state.ModeChangedAt, "updated_at": state.UpdatedAt,
	})
}

type setPerformanceModeRequest struct {
	Mode string `json:"mode"`
}

func (s *Server) handleSetAnkrPerformanceMode(w http.ResponseWriter, r *http.Request) {
	var req setPerformanceModeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return
	}
	mode, err := model.ParsePerformanceMode(req.Mode)
	if err != nil || req.Mode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "mode must be safe, fast, or turbo"})
		return
	}
	before, _ := s.deps.Store.GetAnkrRuntimeState(r.Context())
	if err := s.deps.Store.SetAnkrRequestedMode(r.Context(), mode, "manual_admin_override"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	p := s.productionProfile(r)
	_ = s.deps.Store.InsertAudit(r.Context(), nil, "ankr_performance_mode_changed", "ankr_scheduler", "global", map[string]any{
		"requested_mode": mode, "old_effective_mode": before.EffectiveMode, "new_effective_mode": before.EffectiveMode,
		"reason": "manual_admin_override", "target_before": before.TargetConcurrency, "target_after": p.Resolve(mode),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"requested_mode": mode, "effective_mode": before.EffectiveMode,
		"target_concurrency": before.TargetConcurrency, "hard_ceiling": p.HardCeiling,
		"reason": "manual_admin_override",
	})
}

func (s *Server) handleSetJobPerformanceMode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req setPerformanceModeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return
	}
	mode, err := model.ParsePerformanceMode(req.Mode)
	if err != nil || req.Mode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "mode must be safe, fast, or turbo"})
		return
	}
	job, err := s.deps.Store.GetJob(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "job not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := s.deps.Store.SetJobPerformanceMode(r.Context(), id, mode, "user_selected"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	state, _ := s.deps.Store.GetAnkrRuntimeState(r.Context())
	p := s.productionProfile(r)
	_ = s.deps.Store.InsertAudit(r.Context(), nil, "ankr_performance_mode_changed", "download_job", id, map[string]any{
		"requested_mode": mode, "old_effective_mode": job.EffectiveMode, "new_effective_mode": state.EffectiveMode,
		"reason": "user_selected", "target_before": p.Resolve(job.RequestedMode), "target_after": p.Resolve(mode), "job_id": id,
	})
	s.publishEvent("events.jobs."+id, map[string]any{"type": "performance_mode", "job_id": id,
		"requested_mode": mode, "effective_mode": state.EffectiveMode, "reason": "user_selected", "ts": time.Now()})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "requested_mode": mode,
		"effective_mode": state.EffectiveMode, "global_target_concurrency": state.TargetConcurrency,
		"hard_ceiling": p.HardCeiling})
}
