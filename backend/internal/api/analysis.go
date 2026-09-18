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
	// P1-1：与 v2 explorer 的 flow-profile 共用同一套交互扫描预算。
	readCtx, cancelRead := explorerReadContext(r.Context())
	defer cancelRead()
	result, err := s.analysis.Analyze(readCtx, chi.URLParam(r, "chain"), chi.URLParam(r, "address"))
	if err != nil {
		if explorerScanRefusal(w, r, readCtx, err, "资金流分析") {
			return
		}
		writeArchitectureError(w, http.StatusBadRequest, "FLOW_PROFILE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
