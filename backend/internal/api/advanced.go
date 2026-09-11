package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleAddressAdvancedAnalysis(w http.ResponseWriter, r *http.Request) {
	if s.advanced == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "ADVANCED_ANALYSIS_UNAVAILABLE", "advanced analysis engine unavailable")
		return
	}
	res, err := s.advanced.Get(r.Context(), strings.ToLower(chi.URLParam(r, "chain")), chi.URLParam(r, "address"))
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "ADVANCED_ANALYSIS_READ_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRebuildAddressAdvancedAnalysis(w http.ResponseWriter, r *http.Request) {
	if s.advanced == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "ADVANCED_ANALYSIS_UNAVAILABLE", "advanced analysis engine unavailable")
		return
	}
	chain := strings.ToLower(chi.URLParam(r, "chain"))
	if err := s.validateChain(chain); err != nil {
		writeArchitectureError(w, http.StatusBadRequest, "CHAIN_INVALID", err.Error())
		return
	}
	res, err := s.advanced.Rebuild(r.Context(), chain, chi.URLParam(r, "address"))
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "ADVANCED_ANALYSIS_REBUILD_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
