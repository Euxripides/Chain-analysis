package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/canvas"
)

// ---- U-GRAPH-01 / S5-FE-03 / USER-007 / RBAC-007 ----

var canvasEvidenceRefRe = regexp.MustCompile(`(?i)evidence[:/]([0-9a-f-]{36})`)

// canvasEvidenceRefs 递归提取 Canvas 状态中的 Evidence 引用。
// 只识别显式 evidence:<uuid> / evidence/<uuid> 或 {type:"evidence",id:"..."}；
// 不把普通字符串误判为引用（避免为了过测试而扩大拒绝范围）。
func canvasEvidenceRefs(v any) []string {
	// 统一经过 JSON 往返，既能处理 canvas.State 结构体，也能处理请求体 map。
	if b, err := json.Marshal(v); err == nil {
		var normalized any
		if json.Unmarshal(b, &normalized) == nil {
			v = normalized
		}
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(id string) {
		id = strings.ToLower(strings.TrimSpace(id))
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	var walk func(any)
	walk = func(node any) {
		switch t := node.(type) {
		case string:
			for _, m := range canvasEvidenceRefRe.FindAllStringSubmatch(t, -1) {
				add(m[1])
			}
		case []any:
			for _, item := range t {
				walk(item)
			}
		case []map[string]any:
			for _, item := range t {
				walk(item)
			}
		case map[string]string:
			for _, item := range t {
				walk(item)
			}
		case map[string]any:
			typ := strings.ToLower(strings.TrimSpace(objectString(t["type"])))
			id := objectString(t["id"])
			if typ == "evidence" && id != "" {
				add(id)
			}
			for _, item := range t {
				walk(item)
			}
		case []string:
			for _, item := range t {
				walk(item)
			}
		}
	}
	walk(v)
	return out
}

// validateCanvasEvidenceRefs 保证 Canvas 不能引用其他案件的 Evidence（RBAC-007）。
func (s *Server) validateCanvasEvidenceRefs(w http.ResponseWriter, r *http.Request, expectedCaseID string, state any) bool {
	refs := canvasEvidenceRefs(state)
	if len(refs) == 0 {
		return true
	}
	var crossCase, missing []string
	for _, id := range refs {
		var caseID *string
		err := s.deps.Store.Pool.QueryRow(r.Context(), `
			SELECT i.case_id::text FROM investigation_evidence e
			JOIN investigations i ON i.id=e.investigation_id
			WHERE e.id=$1`, id).Scan(&caseID)
		if err != nil || caseID == nil {
			missing = append(missing, id)
			continue
		}
		if expectedCaseID == "" || *caseID != expectedCaseID {
			crossCase = append(crossCase, id)
		}
	}
	if len(crossCase) > 0 || len(missing) > 0 {
		s.writeAuditNow(r, "canvas.evidence_ref.denied", "canvas", "canvas_reference", expectedCaseID,
			map[string]any{"cross_case": crossCase, "missing": missing, "refs": len(refs)})
		respondError(w, r, http.StatusUnprocessableEntity, "BUSINESS_CROSS_CASE_EVIDENCE",
			"Canvas 引用了其他案件或不存在的 Evidence，已拒绝保存",
			map[string]any{"cross_case_ids": crossCase, "missing_ids": missing, "case_id": expectedCaseID})
		return false
	}
	return true
}

func (s *Server) scopedCanvas(w http.ResponseWriter, r *http.Request, canvasID string) (canvas.Canvas, bool) {
	if s.canvases == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "Canvas 服务未启用", nil)
		return canvas.Canvas{}, false
	}
	item, err := s.canvases.Get(r.Context(), canvasID)
	if errors.Is(err, canvas.ErrNotFound) {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Canvas 不存在或无权访问", nil)
		return canvas.Canvas{}, false
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取 Canvas 失败", nil)
		return canvas.Canvas{}, false
	}
	p := PrincipalFrom(r.Context())
	if item.OrganizationID != p.OrganizationID || item.WorkspaceID != p.WorkspaceID {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Canvas 不存在或无权访问", nil)
		return canvas.Canvas{}, false
	}
	return item, true
}

func (s *Server) canvasCaseID(w http.ResponseWriter, r *http.Request, investigationID string) (string, bool) {
	var caseID, orgID, wsID string
	err := s.deps.Store.Pool.QueryRow(r.Context(), `
		SELECT case_id::text, organization_id::text, workspace_id::text
		FROM investigations WHERE id=$1`, investigationID).Scan(&caseID, &orgID, &wsID)
	if err != nil || caseID == "" {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "调查不存在或无权访问", nil)
		return "", false
	}
	p := PrincipalFrom(r.Context())
	if orgID != p.OrganizationID || wsID != p.WorkspaceID {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "调查不存在或无权访问", nil)
		return "", false
	}
	return caseID, true
}

func (s *Server) handleInvestigationCanvasesV2(w http.ResponseWriter, r *http.Request) {
	investigationID := strings.TrimSpace(chi.URLParam(r, "id"))
	caseID, ok := s.canvasCaseID(w, r, investigationID)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		items, err := s.canvases.List(r.Context(), caseID)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取 Canvas 列表失败", nil)
			return
		}
		if items == nil {
			items = []canvas.Canvas{}
		}
		respondOK(w, r, map[string]any{"items": items, "case_id": caseID})
		return
	}
	var req canvas.CreateRequest
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if !s.validateCanvasEvidenceRefs(w, r, caseID, req.State) {
		return
	}
	item, err := s.canvases.Create(r.Context(), caseID, req)
	if err != nil {
		respondError(w, r, http.StatusConflict, "CONFLICT_CANVAS", err.Error(), nil)
		return
	}
	s.writeAuditNow(r, "canvas.create", "canvas", "canvas", item.ID, map[string]any{"case_id": caseID, "investigation_id": investigationID})
	respondCreated(w, r, item)
}

func (s *Server) handleCanvasV2Get(w http.ResponseWriter, r *http.Request) {
	item, ok := s.scopedCanvas(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleCanvasV2Update(w http.ResponseWriter, r *http.Request) {
	item, ok := s.scopedCanvas(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	revRaw := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	rev, err := strconv.ParseInt(revRaw, 10, 64)
	if err != nil || rev <= 0 {
		validationError(w, r, "If-Match 必须携带当前 revision", []fieldError{{Field: "If-Match", Code: "required", Message: "缺少 revision"}})
		return
	}
	var req canvas.UpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	caseID := ""
	if item.CaseID != nil {
		caseID = *item.CaseID
	}
	if !s.validateCanvasEvidenceRefs(w, r, caseID, req.State) {
		return
	}
	updated, err := s.canvases.Update(r.Context(), item.ID, rev, req)
	if errors.Is(err, canvas.ErrRevisionConflict) {
		current, gerr := s.canvases.Get(r.Context(), item.ID)
		if gerr != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_REVISION", "数据版本已变化", nil)
			return
		}
		diff := canvasStateDiff(req, current)
		s.writeAuditNow(r, "canvas.update.conflict", "canvas", "canvas", item.ID,
			map[string]any{"expected_revision": rev, "current_revision": current.Revision, "changed_fields": len(diff)})
		respondError(w, r, http.StatusPreconditionFailed, "CONFLICT_REVISION",
			"画布已被其他客户端更新，请先比较差异再保存",
			map[string]any{"expected_revision": rev, "current_revision": current.Revision, "diff": diff, "current": current})
		return
	}
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "VALIDATION_INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	s.writeAuditNow(r, "canvas.update", "canvas", "canvas", updated.ID, map[string]any{"revision": updated.Revision})
	w.Header().Set("ETag", "\""+strconv.FormatInt(updated.Revision, 10)+"\"")
	respondOK(w, r, updated)
}

// canvasStateDiff 返回第二次保存相对服务端当前状态的字段级差异。
func canvasStateDiff(req canvas.UpdateRequest, current canvas.Canvas) []map[string]any {
	type pair struct {
		Field string
		Want  any
		Have  any
	}
	pairs := []pair{
		{"name", req.Name, current.Name},
		{"node_refs", req.NodeRefs, current.NodeRefs},
		{"edge_refs", req.EdgeRefs, current.EdgeRefs},
		{"groups", req.Groups, current.Groups},
		{"annotations", req.Annotations, current.Annotations},
		{"filters", req.Filters, current.Filters},
		{"layout", req.Layout, current.Layout},
		{"camera", req.Camera, current.Camera},
		{"view_state", req.ViewState, current.ViewState},
	}
	out := []map[string]any{}
	for _, p := range pairs {
		want, _ := json.Marshal(p.Want)
		have, _ := json.Marshal(p.Have)
		if string(want) == string(have) {
			continue
		}
		out = append(out, map[string]any{
			"field": p.Field, "submitted": truncateJSON(want), "current": truncateJSON(have),
		})
	}
	return out
}

func truncateJSON(raw []byte) string {
	const max = 2000
	if len(raw) <= max {
		return string(raw)
	}
	return string(raw[:max]) + "…(truncated)"
}

func (s *Server) handleCanvasV2Branch(w http.ResponseWriter, r *http.Request) {
	item, ok := s.scopedCanvas(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req canvas.CreateRequest
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	caseID := ""
	if item.CaseID != nil {
		caseID = *item.CaseID
	}
	if !s.validateCanvasEvidenceRefs(w, r, caseID, req.State) {
		return
	}
	branch, err := s.canvases.Branch(r.Context(), item.ID, req)
	if err != nil {
		respondError(w, r, http.StatusConflict, "CONFLICT_CANVAS", err.Error(), nil)
		return
	}
	s.writeAuditNow(r, "canvas.branch", "canvas", "canvas", branch.ID, map[string]any{"parent": item.ID})
	respondCreated(w, r, branch)
}

func (s *Server) handleCanvasV2Snapshots(w http.ResponseWriter, r *http.Request) {
	item, ok := s.scopedCanvas(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		items, err := s.canvases.ListSnapshots(r.Context(), item.ID)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取快照失败", nil)
			return
		}
		if items == nil {
			items = []canvas.Snapshot{}
		}
		respondOK(w, r, map[string]any{"items": items})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	snap, err := s.canvases.CreateSnapshot(r.Context(), item.ID, body.Name)
	if err != nil {
		respondError(w, r, http.StatusConflict, "CONFLICT_CANVAS", err.Error(), nil)
		return
	}
	s.writeAuditNow(r, "canvas.snapshot", "canvas", "canvas", item.ID, map[string]any{"canvas_revision": snap.CanvasRevision})
	respondCreated(w, r, snap)
}
