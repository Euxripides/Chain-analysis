// V1.2 Count Oracle / Global Concurrency API（§80-§82/§85/§88）：
// 状态、健康、观测、校准、决策日志、能力矩阵、全局并发 Card。
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// handleCountOracleStatus §82：Chainbase Count Oracle 健康 + 配置状态。
func (s *Server) handleCountOracleStatus(w http.ResponseWriter, r *http.Request) {
	health, _ := s.deps.Store.GetCountOracleHealth(r.Context(), "chainbase")
	cfg := s.deps.Cfg
	writeJSON(w, http.StatusOK, map[string]any{
		"oracle_enabled":               cfg.Chainbase.Enabled && cfg.CountOracle.Enabled,
		"key_configured":               cfg.Chainbase.APIKey != "",
		"count_aware_sharding_enabled": cfg.CountOracle.CountAwareShardingEnabled,
		"shadow_mode":                  cfg.Chainbase.Enabled && cfg.CountOracle.Enabled && !cfg.CountOracle.CountAwareShardingEnabled,
		"planning_deadline_ms":         cfg.CountOracle.PlanningDeadline.Milliseconds(),
		"count_timeout_ms":             cfg.Chainbase.CountTimeout.Milliseconds(),
		"count_workers":                cfg.Chainbase.Workers,
		"endpoint":                     cfg.Chainbase.Endpoint,
		"zero_verify_enabled":          cfg.CountOracle.ZeroVerifyEnabled,
		"calibration_enabled":          cfg.CountOracle.CalibrationEnabled,
		"health":                       health,
	})
}

// handleCountOracleObservations §87 查询。
func (s *Server) handleCountOracleObservations(w http.ResponseWriter, r *http.Request) {
	chain := r.URL.Query().Get("chain")
	dataset := r.URL.Query().Get("dataset")
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.deps.Store.ListCountObservations(r.Context(), chain, dataset, hours, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []model.CountObservation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"observations": rows, "count": len(rows)})
}

// handleCountOracleCalibration §23 查询。
func (s *Server) handleCountOracleCalibration(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.ListCalibrations(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []model.CountCalibration{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"calibrations": rows})
}

// handlePlanningDecisions §88 查询。
func (s *Server) handlePlanningDecisions(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.deps.Store.ListPlanningDecisions(r.Context(), jobID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []model.PlanningDecision{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"decisions": rows, "count": len(rows)})
}

// handleGlobalConcurrency §80：Ankr Fetch Capacity Card。
func (s *Server) handleGlobalConcurrency(w http.ResponseWriter, r *http.Request) {
	cfg := s.deps.Cfg
	st, _ := s.deps.Store.GetSchedulerState(r.Context())
	prof, _ := s.deps.Store.GetGlobalConcurrencyProfile(r.Context(), "all", "all")
	resp := map[string]any{
		"hard_ceiling":          cfg.Global.MaxInflight,
		"auto_tune":             cfg.Global.AutoTuneEnabled,
		"ramp_steps":            cfg.Global.RampSteps,
		"normalization_enabled": cfg.Global.LatencyNormalizationEnabled,
		"state":                 st,
		"profile":               prof,
		"ts":                    time.Now(),
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleChainCapabilities §61：能力矩阵。
func (s *Server) handleV12ChainCapabilities(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.ListChainCapabilities(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []model.ChainCapability{}
	}
	if !s.deps.Cfg.Tron.Enabled {
		filtered := rows[:0]
		for _, row := range rows {
			if row.Chain != "tron" {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": rows})
}

// handleCountProviders V1.3：Count Providers（§72-§74）—— registry + capabilities + runtime profiles + 配置视图。
func (s *Server) handleCountProviders(w http.ResponseWriter, r *http.Request) {
	cfg := s.deps.Cfg
	registries, err1 := s.deps.Store.ListCountProviderRegistries(r.Context())
	if err1 != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err1.Error()})
		return
	}
	caps, err2 := s.deps.Store.ListCountProviderCapabilities(r.Context())
	if err2 != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err2.Error()})
		return
	}
	profiles, err3 := s.deps.Store.ListCountProviderRuntimeProfiles(r.Context())
	if err3 != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err3.Error()})
		return
	}
	if registries == nil {
		registries = []model.CountProviderRegistryRow{}
	}
	if caps == nil {
		caps = []model.CountProviderCapabilityRow{}
	}
	if profiles == nil {
		profiles = []model.CountProviderRuntimeProfile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"port_enabled":              cfg.CountProvider.PortEnabled,
		"port_shadow":               cfg.CountProvider.Shadow,
		"router_enabled":            cfg.CountProvider.RouterEnabled,
		"global_max_inflight":       cfg.CountProvider.GlobalMaxInflight,
		"chainbase_tx_enabled":      cfg.CountProvider.ChainbaseTXEnabled,
		"chainbase_token_enabled":   cfg.CountProvider.ChainbaseTokenEnabled,
		"chainbase_max_concurrency": cfg.CountProvider.ChainbaseMaxConcurrency,
		"chainbase_max_rps":         cfg.CountProvider.ChainbaseMaxRPS,
		"mockparallel_enabled":      cfg.CountProvider.MockParallelEnabled,
		"registries":                registries,
		"capabilities":              caps,
		"profiles":                  profiles,
	})
}

// handleResolutionDecisions V1.3：Resolution / Shadow Diff 证据（§66/§98）。
func (s *Server) handleResolutionDecisions(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	rows, err := s.deps.Store.ListResolutionDecisions(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []model.ShadowDiff{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"decisions": rows, "count": len(rows)})
}
