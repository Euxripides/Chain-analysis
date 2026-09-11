package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleAddressFlowProfile(w http.ResponseWriter, r *http.Request) {
	if s.analysis == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "ANALYSIS_UNAVAILABLE", "analysis service unavailable")
		return
	}
	result, err := s.analysis.Analyze(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "address"))
	if err != nil {
		writeArchitectureError(w, http.StatusBadRequest, "FLOW_PROFILE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
