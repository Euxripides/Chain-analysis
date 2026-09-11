package api

import (
	"net/http"

	"github.com/blockchain-platform/blockchain-platform/internal/subjectintel"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleInvestigationSubjects(w http.ResponseWriter, r *http.Request) {
	if s.subjects == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "SUBJECT_INTELLIGENCE_UNAVAILABLE", "subject intelligence service unavailable")
		return
	}
	id := chi.URLParam(r, "id")
	if r.Method == http.MethodGet {
		sub, rel, err := s.subjects.List(r.Context(), id)
		if err != nil {
			writeArchitectureError(w, http.StatusInternalServerError, "SUBJECT_LIST_FAILED", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subjects": sub, "relationships": rel})
		return
	}
	var in subjectintel.CreateSubject
	if err := decodeJSON(r, &in); err != nil {
		writeArchitectureError(w, http.StatusBadRequest, "SUBJECT_INVALID", err.Error())
		return
	}
	out, err := s.subjects.AddSubject(r.Context(), id, in)
	if err != nil {
		writeArchitectureError(w, http.StatusBadRequest, "SUBJECT_CREATE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *Server) handleInvestigationRelationships(w http.ResponseWriter, r *http.Request) {
	if s.subjects == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "SUBJECT_INTELLIGENCE_UNAVAILABLE", "subject intelligence service unavailable")
		return
	}
	var in subjectintel.CreateRelationship
	if err := decodeJSON(r, &in); err != nil {
		writeArchitectureError(w, http.StatusBadRequest, "RELATIONSHIP_INVALID", err.Error())
		return
	}
	out, err := s.subjects.AddRelationship(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		writeArchitectureError(w, http.StatusBadRequest, "RELATIONSHIP_CREATE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
