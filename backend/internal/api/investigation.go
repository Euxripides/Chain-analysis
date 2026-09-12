package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/investigation"
)

func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	if s.capabilities == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "CAPABILITY_REGISTRY_UNAVAILABLE", "capability registry unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.capabilities.List()})
}

func (s *Server) handleArchitectureStatus(w http.ResponseWriter, r *http.Request) {
	if s.investigations == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "ARCHITECTURE_BASELINE_UNAVAILABLE", "architecture baseline unavailable")
		return
	}
	status, err := s.investigations.ArchitectureStatus(r.Context())
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "ARCHITECTURE_STATUS_FAILED", "architecture status query failed")
		return
	}
	status.Components["capability_registry"] = s.capabilities != nil && len(s.capabilities.List()) > 0
	status.Components["event_bus"] = s.deps.Bus != nil && s.deps.Bus.Enabled()
	ready := true
	for _, ok := range status.Components {
		ready = ready && ok
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": map[bool]string{true: "ready", false: "degraded"}[ready], "baseline": status})
}

func (s *Server) handleCreateInvestigation(w http.ResponseWriter, r *http.Request) {
	if s.investigations == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "INVESTIGATION_UNAVAILABLE", "investigation service unavailable")
		return
	}
	var req investigation.CreateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeArchitectureError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid investigation request")
		return
	}
	req.Organization = auth.FromContext(r.Context()).OrganizationID
	req.IdempotencyKey = r.Header.Get("Idempotency-Key")
	inv, replayed, err := s.investigations.Create(r.Context(), req)
	if err != nil {
		writeArchitectureError(w, http.StatusBadRequest, "INVESTIGATION_CREATE_FAILED", err.Error())
		return
	}
	code := http.StatusCreated
	if replayed {
		code = http.StatusOK
	}
	w.Header().Set("Location", "/api/v1/investigations/"+inv.ID)
	writeJSON(w, code, map[string]any{"investigation": inv, "idempotent_replay": replayed})
}

func (s *Server) handleGetInvestigation(w http.ResponseWriter, r *http.Request) {
	inv, err := s.investigations.Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, investigation.ErrNotFound) {
		writeArchitectureError(w, http.StatusNotFound, "INVESTIGATION_NOT_FOUND", "investigation not found")
		return
	}
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "INVESTIGATION_READ_FAILED", "investigation read failed")
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) handleListInvestigations(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.investigations.List(r.Context(), limit)
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "INVESTIGATION_LIST_FAILED", "investigation list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleInvestigationRuntime(w http.ResponseWriter, r *http.Request) {
	view, err := s.investigations.RuntimeView(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "INVESTIGATION_RUNTIME_READ_FAILED", "investigation runtime read failed")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleInvestigationArtifacts(w http.ResponseWriter, r *http.Request) {
	view, err := s.investigations.Artifacts(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "INVESTIGATION_ARTIFACTS_READ_FAILED", "investigation artifacts read failed")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleInvestigationAction(w http.ResponseWriter, r *http.Request) {
	action := chi.URLParam(r, "action")
	if action != "start" && action != "analyze" && action != "pause" && action != "resume" && action != "retry" && action != "cancel" && action != "approve" {
		writeArchitectureError(w, http.StatusNotFound, "ACTION_NOT_FOUND", "unsupported investigation action")
		return
	}
	var revision int64
	if raw := r.Header.Get("If-Match"); raw != "" {
		raw = strings.Trim(strings.TrimPrefix(strings.TrimSpace(raw), "W/"), "\"")
		revision, _ = strconv.ParseInt(raw, 10, 64)
	}
	inv, err := s.investigations.Transition(r.Context(), chi.URLParam(r, "id"), action, revision)
	if errors.Is(err, investigation.ErrNotFound) {
		writeArchitectureError(w, http.StatusConflict, "INVESTIGATION_STATE_CONFLICT", "investigation missing, terminal, or revision changed")
		return
	}
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "INVESTIGATION_ACTION_FAILED", "investigation action failed")
		return
	}
	if (action == "start" || action == "retry") && s.runtime != nil {
		actorIdentity := PrincipalFrom(r.Context()).IdentityID
		go func(id, actor string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if _, runtimeErr := s.runtime.Start(ctx, id, actor); runtimeErr != nil {
				s.deps.Log.Error("investigation background runtime failed", "investigation_id", id, "err", runtimeErr)
				failureCtx, failureCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer failureCancel()
				s.investigations.MarkRuntimeFailed(failureCtx, id, runtimeErr)
			}
		}(inv.ID, actorIdentity)
		writeJSON(w, http.StatusAccepted, map[string]any{"investigation": inv, "runtime": map[string]any{"status": "accepted"}})
		return
	}
	if action == "analyze" && s.runtime != nil {
		runtimeResult, runtimeErr := s.runtime.AnalyzeExisting(r.Context(), inv.ID)
		if runtimeErr != nil {
			writeArchitectureError(w, http.StatusInternalServerError, "INVESTIGATION_ANALYSIS_FAILED", runtimeErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"investigation": inv, "runtime": runtimeResult})
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

func writeArchitectureError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": message, "code": code})
}
