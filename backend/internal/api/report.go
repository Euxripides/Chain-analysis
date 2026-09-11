package api

import (
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleGenerateInvestigationReport(w http.ResponseWriter, r *http.Request) {
	if s.reports == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "REPORT_UNAVAILABLE", "report service unavailable")
		return
	}
	report, err := s.reports.Generate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "REPORT_GENERATION_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, report)
}
func (s *Server) handleListInvestigationReports(w http.ResponseWriter, r *http.Request) {
	if s.reports == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "REPORT_UNAVAILABLE", "report service unavailable")
		return
	}
	reports, err := s.reports.List(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeArchitectureError(w, http.StatusInternalServerError, "REPORT_LIST_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": reports})
}
func (s *Server) handleDownloadInvestigationReport(w http.ResponseWriter, r *http.Request) {
	if s.reports == nil {
		writeArchitectureError(w, http.StatusServiceUnavailable, "REPORT_UNAVAILABLE", "report service unavailable")
		return
	}
	reader, report, err := s.reports.Open(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeArchitectureError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "report or report object unavailable")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="investigation-report-r%d.md"`, report.Revision))
	w.Header().Set("ETag", `"`+report.ContentHash+`"`)
	w.Header().Set("Content-Length", fmt.Sprint(report.SizeBytes))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}
