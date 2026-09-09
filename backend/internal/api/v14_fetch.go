// V1.4 Ankr Fetch API（§51）：fetch-runtime / fetch-profile / global-ready-queue / range-certifications。
package api

import (
	"net/http"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// handleAnkrFetchRuntime §51：Global Fetch Controller 实时视图。
func (s *Server) handleAnkrFetchRuntime(w http.ResponseWriter, r *http.Request) {
	st, err := s.deps.Store.GetSchedulerState(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	pending, retryReady, leased, err2 := s.deps.Store.ReadyQueueStats(r.Context())
	if err2 != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err2.Error()})
		return
	}
	// The worker heartbeat is authoritative.  The API process can be deployed
	// with a different environment, so exposing cfg.Ankr/Global here would
	// recreate the V1.4.1 stale-aggregate bug.
	configuredInitial, configuredMax, hardCeiling, globalCeiling := 0, 0, 0, 0
	queueEnabled, queueShadow, autoTune := false, false, false
	queueMode, controllerMode := "", ""
	actualInflight, weightedInflight, effectiveTarget := int64(0), int64(0), 0
	var workerUpdatedAt time.Time
	if st != nil {
		configuredInitial = st.ConfiguredInitialConcurrency
		configuredMax = st.ConfiguredMaxConcurrency
		hardCeiling = st.GlobalHardCeiling
		if hardCeiling == 0 {
			hardCeiling = st.HardCeiling
		}
		globalCeiling = hardCeiling
		queueEnabled, queueShadow, autoTune = st.ReadyQueueEnabled, st.ReadyQueueShadow, st.AutoTuneEnabled
		queueMode, controllerMode = st.ReadyQueueMode, st.ControllerMode
		actualInflight, weightedInflight, effectiveTarget = int64(st.ActiveSlots), int64(st.WeightedSlots), st.ConcurrencyTarget
		workerUpdatedAt = st.UpdatedAt
	}
	stale := st == nil || workerUpdatedAt.IsZero() || time.Since(workerUpdatedAt) > 15*time.Second
	out := map[string]any{
		"configured_initial_concurrency": configuredInitial,
		"configured_max_concurrency":     configuredMax,
		"hard_ceiling":                   hardCeiling,
		"global_ceiling":                 globalCeiling,
		"effective_target":               effectiveTarget,
		"actual_inflight":                actualInflight,
		"actual_weighted_inflight":       weightedInflight,
		"controller_mode":                controllerMode,
		"auto_tune_enabled":              autoTune,
		"ready_queue_enabled":            queueEnabled,
		"ready_queue_shadow":             queueShadow,
		"ready_queue_mode":               queueMode,
		"telemetry_stale":                stale,
		"worker_updated_at":              workerUpdatedAt,
		"ready_queue_depth":              pending + retryReady,
		"retry_queue_depth":              retryReady,
		"active_or_leased":               leased,
		"scheduler":                      st,
	}
	if modeState, modeErr := s.deps.Store.GetAnkrRuntimeState(r.Context()); modeErr == nil {
		profile := s.productionProfile(r)
		out["requested_mode"] = modeState.RequestedMode
		out["effective_mode"] = modeState.EffectiveMode
		out["target_concurrency"] = modeState.TargetConcurrency
		out["active_concurrency"] = actualInflight
		out["mode_reason"] = modeState.ModeReason
		out["mode_changed_at"] = modeState.ModeChangedAt
		out["safe_concurrency"] = profile.Resolve(model.PerformanceSafe)
		out["fast_concurrency"] = profile.Resolve(model.PerformanceFast)
		out["turbo_concurrency"] = profile.Resolve(model.PerformanceTurbo)
		out["reject_boundary"] = profile.RejectBoundary
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAnkrFetchProfile §44/§53：Sweet Spot profiles + concurrency runs。
func (s *Server) handleAnkrFetchProfile(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.deps.Store.ListGlobalFetchProfiles(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	runs, err := s.deps.Store.ListFetchConcurrencyRuns(r.Context(), 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if profiles == nil {
		profiles = []model.GlobalFetchProfile{}
	}
	if runs == nil {
		runs = []model.FetchConcurrencyRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles, "runs": runs})
}

// handleGlobalReadyQueue §51：Ready/Retry Queue 状态。
func (s *Server) handleGlobalReadyQueue(w http.ResponseWriter, r *http.Request) {
	pending, retryReady, leased, err := s.deps.Store.ReadyQueueStats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ready": pending, "retry_ready": retryReady, "active_or_leased": leased,
		"generated_at": time.Now(),
	})
}

// handleListRangeCertifications §14：已认证 dataset。
func (s *Server) handleListRangeCertifications(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.ListRangeCertifications(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []model.RangeCertification{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"certifications": rows})
}

// handleRecordRangeCertification §14：记录 Certification 证据（1R==2R==4R==8R union 一致性通过后）。
func (s *Server) handleRecordRangeCertification(w http.ResponseWriter, r *http.Request) {
	var in model.RangeCertification
	if err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if in.Chain == "" || in.Dataset == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "chain and dataset required"})
		return
	}
	if in.VerificationVersion <= 0 {
		in.VerificationVersion = 1
	}
	now := time.Now()
	in.VerifiedAt = &now
	if err := s.deps.Store.UpsertRangeCertification(r.Context(), &in); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "certification": in})
}
