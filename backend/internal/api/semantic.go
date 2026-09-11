package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

func (s *Server) handleAddressSemanticEvents(w http.ResponseWriter, r *http.Request) {
	if s.semantic == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "semantic engine unavailable"})
		return
	}
	res, err := s.semantic.Get(r.Context(), strings.ToLower(chi.URLParam(r, "chain")), chi.URLParam(r, "address"))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, res)
}
func (s *Server) handleRebuildAddressSemanticEvents(w http.ResponseWriter, r *http.Request) {
	if s.semantic == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "semantic engine unavailable"})
		return
	}
	chain := strings.ToLower(chi.URLParam(r, "chain"))
	if err := s.validateChain(chain); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	res, err := s.semantic.Rebuild(r.Context(), chain, chi.URLParam(r, "address"))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, res)
}
