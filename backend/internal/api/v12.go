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

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/explorer"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/update"
)

// ---- Cases / Tags（`63/`64）----

func (s *Server) handleListCases(w http.ResponseWriter, r *http.Request) {
	_, wsID, err := s.deps.Store.DefaultOrgWorkspace(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	rows, err := s.deps.Store.ListCases(r.Context(), wsID)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"cases": rows})
}

func (s *Server) handleCreateCase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CaseNo      string `json:"case_no"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Color       string `json:"color"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Name == "" {
		writeJSON(w, 400, map[string]any{"error": "name required"})
		return
	}
	orgID, wsID, err := s.deps.Store.DefaultOrgWorkspace(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	c, err := s.deps.Store.CreateCase(r.Context(), wsID, orgID, req.CaseNo, req.Name, req.Description, req.Color)
	if err != nil {
		writeJSON(w, 409, map[string]any{"error": err.Error()})
		return
	}
	s.deps.Store.InsertAudit(r.Context(), nil, "case.create", "case", c.ID, map[string]any{"name": c.Name})
	writeJSON(w, 201, c)
}

func (s *Server) handleDeleteCase(w http.ResponseWriter, r *http.Request) {
	_, wsID, err := s.deps.Store.DefaultOrgWorkspace(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := s.deps.Store.DeleteCase(r.Context(), wsID, chi.URLParam(r, "id")); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	_, wsID, err := s.deps.Store.DefaultOrgWorkspace(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	rows, err := s.deps.Store.ListTags(r.Context(), wsID)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"tags": rows})
}

func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Color       string `json:"color"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Name == "" {
		writeJSON(w, 400, map[string]any{"error": "name required"})
		return
	}
	orgID, wsID, err := s.deps.Store.DefaultOrgWorkspace(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	t, err := s.deps.Store.CreateTag(r.Context(), wsID, orgID, req.Name, req.Description, req.Color, "user")
	if err != nil {
		writeJSON(w, 409, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 201, t)
}

func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	_, wsID, err := s.deps.Store.DefaultOrgWorkspace(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := s.deps.Store.DeleteTag(r.Context(), wsID, chi.URLParam(r, "id")); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}

// ---- 地址目录（`61）----

func (s *Server) handleExplorerAddresses(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	chain := q.Get("chain")
	search := q.Get("q")
	tagMode := q.Get("tag_mode")
	if tagMode == "" {
		tagMode = "any"
	}
	syncStatus := q.Get("sync_status")
	cursor := q.Get("cursor")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	caseIDs := splitCSV(q.Get("case_ids"))
	tagIDs := splitCSV(q.Get("tag_ids"))
	rows, next, err := s.meta.Catalog(r.Context(), chain, search, caseIDs, tagIDs, tagMode, syncStatus, cursor, limit)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"addresses": rows, "next_cursor": next})
}

// ---- 统一浏览查询（`62）----

func (s *Server) handleExplorerQuery(w http.ResponseWriter, r *http.Request) {
	var req explorer.QueryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	req.Chain = strings.ToLower(strings.TrimSpace(req.Chain))
	req.Dataset = strings.ToLower(strings.TrimSpace(req.Dataset))
	if err := s.validateChain(req.Chain); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !validExplorerDataset(req.Dataset) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown dataset " + req.Dataset})
		return
	}
	addresses, err := dataset.NormalizeAndDedupeAddressesForChain(req.Chain, req.Addresses)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	req.Addresses = addresses
	if req.Limit <= 0 || req.Limit > 500 {
		req.Limit = 100
	}
	res, err := s.explorer.Query(r.Context(), req)
	if err != nil {
		if errors.Is(err, explorer.ErrInvalidCursor) || errors.Is(err, explorer.ErrInvalidFilter) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, res)
}

func validExplorerDataset(dataset string) bool {
	return dataset == "activity" || model.ValidDataset(dataset)
}

// ---- 地址元数据（`65）----

func (s *Server) handleAddressMetadata(w http.ResponseWriter, r *http.Request) {
	chain := chi.URLParam(r, "chain")
	addr := chi.URLParam(r, "address")
	var req struct {
		DisplayName    *string  `json:"display_name"`
		SetPrimaryCase bool     `json:"set_primary_case"`
		AddCases       []string `json:"add_cases"`
		RemoveCases    []string `json:"remove_cases"`
		AddTags        []string `json:"add_tags"`
		RemoveTags     []string `json:"remove_tags"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	_, wsID, err := s.deps.Store.DefaultOrgWorkspace(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	for _, id := range req.AddCases {
		if err := s.deps.Store.AddCaseLink(r.Context(), wsID, chain, addr, id); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		if req.SetPrimaryCase {
			if err := s.deps.Store.SetPrimaryCase(r.Context(), wsID, chain, addr, id); err != nil {
				writeJSON(w, 500, map[string]any{"error": err.Error()})
				return
			}
		}
	}
	for _, id := range req.RemoveCases {
		if err := s.deps.Store.RemoveCaseLink(r.Context(), wsID, chain, addr, id); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
	}
	for _, id := range req.AddTags {
		if err := s.deps.Store.AddTagLink(r.Context(), wsID, chain, addr, id); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
	}
	for _, id := range req.RemoveTags {
		if err := s.deps.Store.RemoveTagLink(r.Context(), wsID, chain, addr, id); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
	}
	if req.DisplayName != nil {
		if err := s.deps.Store.SetDisplayName(r.Context(), wsID, chain, addr, *req.DisplayName); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
	}
	s.deps.Store.InsertAudit(r.Context(), nil, "address.metadata.update", "address", chain+":"+addr, map[string]any{})
	entry, err := s.meta.Meta(r.Context(), chain, addr)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if entry == nil {
		writeJSON(w, 404, map[string]any{"error": "address not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"address": entry})
}

// ---- 更新预览/确认（`66/`44）----

func (s *Server) handleUpdatePreview(w http.ResponseWriter, r *http.Request) {
	var req update.PreviewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	resp, err := s.updates.Preview(r.Context(), req)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleUpdateConfirm(w http.ResponseWriter, r *http.Request) {
	jobIDs, err := s.updates.Confirm(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 409, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"job_ids": jobIDs})
}

// ---- 导出 Ticket（`54/`56）----

func (s *Server) handleExportTicket(w http.ResponseWriter, r *http.Request) {
	exp, err := s.deps.Store.GetExport(r.Context(), chi.URLParam(r, "id"))
	if err != nil || exp == nil {
		writeJSON(w, 404, map[string]any{"error": "export not found"})
		return
	}
	if exp.Status != "COMPLETED" {
		writeJSON(w, 409, map[string]any{"error": "export not ready: " + exp.Status})
		return
	}
	orgID, wsID, _ := s.deps.Store.DefaultOrgWorkspace(r.Context())
	ticketID, err := s.deps.Store.CreateExportTicket(r.Context(), exp.ID, nil, orgID, wsID, 15*time.Minute)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ticket_id": ticketID, "url": "/api/v1/download/" + ticketID, "expires_in_seconds": 900})
}

func (s *Server) handleTicketDownload(w http.ResponseWriter, r *http.Request) {
	ticketID := chi.URLParam(r, "ticket")
	exportID, expiresAt, err := s.deps.Store.GetExportTicket(r.Context(), ticketID)
	if err != nil || time.Now().After(expiresAt) {
		writeJSON(w, 403, map[string]any{"error": "ticket expired or invalid"})
		return
	}
	exp, err := s.deps.Store.GetExport(r.Context(), exportID)
	if err != nil || exp == nil || exp.Status != "COMPLETED" {
		writeJSON(w, 404, map[string]any{"error": "export not found"})
		return
	}
	// MinIO is intentionally bound to loopback, so a presigned internal URL is not
	// reachable by a remote browser. Stream through the ticket endpoint and reuse
	// the same bounded-memory/Range implementation as the authenticated route.
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", exp.ID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx))
	s.handleDownloadExport(w, r)
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

var _ = explorer.QueryRequest{}
var _ = json.Marshal
