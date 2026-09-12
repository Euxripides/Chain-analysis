package api

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/casework"
)

// caseworkActor 只使用服务端解析出来的主体与请求元数据构造审计上下文。
func (s *Server) caseworkActor(r *http.Request) casework.Actor {
	p := PrincipalFrom(r.Context())
	m := metaFrom(r.Context())
	return casework.Actor{
		Principal: p, RequestID: m.RequestID, TraceID: m.TraceID,
		Route: r.URL.Path, Method: r.Method,
		ClientIP: m.IP.ClientIP, PeerIP: m.IP.PeerIP, UserAgent: m.UserAgent,
	}
}

func (s *Server) caseworkReady(w http.ResponseWriter, r *http.Request) bool {
	if s.casework == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "案件服务未初始化", nil)
		return false
	}
	return true
}

func (s *Server) writeCaseworkError(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := casework.AsAPIError(err)
	respondError(w, r, apiErr.Status, apiErr.Code, apiErr.Message, apiErr.Details)
}

func ifMatchRevision(r *http.Request) int64 {
	raw := strings.Trim(strings.TrimPrefix(strings.TrimSpace(r.Header.Get("If-Match")), "W/"), "\"")
	n, _ := strconv.ParseInt(raw, 10, 64)
	return n
}

// ---- 案件 ------------------------------------------------------------------

func (s *Server) handleUserCreateCase(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	var req casework.CreateCaseRequest
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	item, err := s.casework.CreateCase(r.Context(), s.caseworkActor(r), req)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondCreated(w, r, item)
}

func (s *Server) handleUserUpdateCase(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	var req casework.UpdateCaseRequest
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	item, err := s.casework.UpdateCase(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), ifMatchRevision(r), req)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserCaseCommand(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	var req casework.CaseCommandRequest
	_ = decodeJSON(r, &req)
	item, err := s.casework.CommandCase(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "command"), ifMatchRevision(r), req)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserCaseMembers(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	items, err := s.casework.ListMembers(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleUserCaseMemberAdd(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	var req struct {
		IdentityID string `json:"identity_id"`
		Role       string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	item, err := s.casework.AddMember(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), req.IdentityID, req.Role)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondCreated(w, r, item)
}

func (s *Server) handleUserCaseMemberRemove(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	if err := s.casework.RemoveMember(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "identity_id")); err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondNoContent(w)
}

func (s *Server) handleUserCaseSubjects(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	items, err := s.casework.ListSubjects(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleUserCaseSubjectAdd(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	var req casework.CaseSubject
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	item, err := s.casework.AddSubject(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), req)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondCreated(w, r, item)
}

func (s *Server) handleUserCaseSubjectRemove(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	if err := s.casework.RemoveSubject(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "subject_id")); err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondNoContent(w)
}

func (s *Server) handleUserCaseActivity(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	items, next, err := s.casework.ListActivity(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), r.URL.Query().Get("cursor"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, -1, next)
}

func (s *Server) handleUserCaseComments(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	items, next, err := s.casework.ListComments(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), r.URL.Query().Get("cursor"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, -1, next)
}

func (s *Server) handleUserCaseCommentAdd(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	var req struct {
		Body     string `json:"body_markdown"`
		ThreadID string `json:"thread_id"`
		ParentID string `json:"parent_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	item, err := s.casework.CreateComment(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), req.Body, req.ThreadID, req.ParentID)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondCreated(w, r, item)
}

func (s *Server) handleUserWorkspaceMembers(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	items, err := s.casework.ListWorkspaceMembers(r.Context(), s.caseworkActor(r), r.URL.Query().Get("q"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, len(items), nil)
}

// ---- 调查 v2 ---------------------------------------------------------------

func (s *Server) handleUserCreateInvestigation(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	var req casework.CreateInvestigationRequest
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	inv, replayed, err := s.casework.CreateInvestigation(r.Context(), s.caseworkActor(r), req, strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeEnvelope(w, status, envelope{Success: true, Data: map[string]any{"investigation": inv, "idempotent_replay": replayed}, Meta: metaFor(r.Context())})
}

func (s *Server) handleUserInvestigationPreview(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	var req struct {
		CaseID          string                   `json:"case_id"`
		Subjects        []casework.SubjectInput  `json:"subjects"`
		DataRequirement casework.DataRequirement `json:"data_requirement"`
	}
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	preview, summary, err := s.casework.PreviewInvestigation(r.Context(), s.caseworkActor(r), req.CaseID, req.Subjects, req.DataRequirement)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, map[string]any{"coverage": preview, "summary": summary})
}

func (s *Server) handleUserInvestigationListV2(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	items, next, err := s.casework.ListInvestigationsV2(r.Context(), s.caseworkActor(r),
		r.URL.Query().Get("status"), r.URL.Query().Get("case_id"), r.URL.Query().Get("cursor"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, -1, next)
}

func (s *Server) handleUserInvestigationDetailV2(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "investigation", chi.URLParam(r, "id")) {
		return
	}
	item, err := s.casework.GetInvestigationV2(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserInvestigationRuntime(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "investigation", chi.URLParam(r, "id")) {
		return
	}
	view, err := s.casework.InvestigationRuntime(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, view)
}

func (s *Server) handleUserInvestigationStart(w http.ResponseWriter, r *http.Request) {
	s.handleUserInvestigationLifecycle(w, r, "start")
}
func (s *Server) handleUserInvestigationPause(w http.ResponseWriter, r *http.Request) {
	s.handleUserInvestigationLifecycle(w, r, "pause")
}
func (s *Server) handleUserInvestigationResume(w http.ResponseWriter, r *http.Request) {
	s.handleUserInvestigationLifecycle(w, r, "resume")
}
func (s *Server) handleUserInvestigationCancel(w http.ResponseWriter, r *http.Request) {
	s.handleUserInvestigationLifecycle(w, r, "cancel")
}
func (s *Server) handleUserInvestigationRetry(w http.ResponseWriter, r *http.Request) {
	s.handleUserInvestigationLifecycle(w, r, "retry")
}

func (s *Server) handleUserInvestigationLifecycle(w http.ResponseWriter, r *http.Request, command string) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "investigation", chi.URLParam(r, "id")) {
		return
	}
	var req casework.ReviewCommandRequest
	_ = decodeJSON(r, &req)
	actor := s.caseworkActor(r)
	id := chi.URLParam(r, "id")
	rev := ifMatchRevision(r)
	var (
		item casework.InvestigationView
		err  error
	)
	switch command {
	case "start":
		item, err = s.casework.StartInvestigation(r.Context(), actor, id, rev)
	case "pause":
		item, err = s.casework.PauseInvestigation(r.Context(), actor, id, rev, req.Reason)
	case "resume":
		item, err = s.casework.ResumeInvestigation(r.Context(), actor, id, rev, req.Reason)
	case "cancel":
		item, err = s.casework.CancelInvestigation(r.Context(), actor, id, rev, req.Reason)
	case "retry":
		item, err = s.casework.RetryInvestigation(r.Context(), actor, id, rev, req.Reason)
	default:
		s.writeCaseworkError(w, r, casework.ErrNotFound)
		return
	}
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserInvestigationSubmitReview(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "investigation", chi.URLParam(r, "id")) {
		return
	}
	var req struct {
		ReviewerID string `json:"reviewer_id"`
		Note       string `json:"note"`
	}
	_ = decodeJSON(r, &req)
	item, err := s.casework.SubmitInvestigationReview(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), ifMatchRevision(r), req.ReviewerID, req.Note)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserInvestigationFindings(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "investigation", chi.URLParam(r, "id")) {
		return
	}
	items, next, err := s.casework.ListFindings(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"),
		r.URL.Query().Get("status"), r.URL.Query().Get("type"), r.URL.Query().Get("nature"), r.URL.Query().Get("cursor"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, -1, next)
}

func (s *Server) handleUserInvestigationEvidence(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "investigation", chi.URLParam(r, "id")) {
		return
	}
	items, err := s.casework.ListEvidence(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), r.URL.Query().Get("status"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, len(items), nil)
}

// ---- Finding ---------------------------------------------------------------

func (s *Server) handleUserFindingDetail(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	item, err := s.casework.GetFinding(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserFindingPatch(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	var req struct {
		Summary    *string `json:"summary"`
		ReviewNote *string `json:"review_note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	item, err := s.casework.PatchFinding(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), ifMatchRevision(r), req.Summary, req.ReviewNote)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserFindingConfirm(w http.ResponseWriter, r *http.Request) {
	s.handleUserFindingCommand(w, r, "confirm")
}
func (s *Server) handleUserFindingReject(w http.ResponseWriter, r *http.Request) {
	s.handleUserFindingCommand(w, r, "reject")
}
func (s *Server) handleUserFindingSupersede(w http.ResponseWriter, r *http.Request) {
	s.handleUserFindingCommand(w, r, "supersede")
}

func (s *Server) handleUserFindingCommand(w http.ResponseWriter, r *http.Request, command string) {
	if !s.caseworkReady(w, r) {
		return
	}
	var req casework.ReviewCommandRequest
	_ = decodeJSON(r, &req)
	item, err := s.casework.CommandFinding(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), command, ifMatchRevision(r), req)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

// ---- Evidence --------------------------------------------------------------

func (s *Server) handleUserEvidenceDetail(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	includeSensitive := false
	if v := strings.ToLower(r.URL.Query().Get("include")); v == "sensitive" || v == "content" {
		p := PrincipalFrom(r.Context())
		if !p.Has("evidence:sensitive:read") {
			respondError(w, r, http.StatusForbidden, "AUTHZ_PERMISSION_DENIED", "需要 evidence:sensitive:read 权限才能读取敏感证据正文", map[string]any{"permission": "evidence:sensitive:read"})
			return
		}
		includeSensitive = true
	}
	item, err := s.casework.GetEvidence(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), includeSensitive)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserEvidenceVerify(w http.ResponseWriter, r *http.Request) {
	s.handleUserEvidenceCommand(w, r, "verify")
}
func (s *Server) handleUserEvidenceDispute(w http.ResponseWriter, r *http.Request) {
	s.handleUserEvidenceCommand(w, r, "dispute")
}
func (s *Server) handleUserEvidenceSupersede(w http.ResponseWriter, r *http.Request) {
	s.handleUserEvidenceCommand(w, r, "supersede")
}

func (s *Server) handleUserEvidenceCommand(w http.ResponseWriter, r *http.Request, command string) {
	if !s.caseworkReady(w, r) {
		return
	}
	var req casework.EvidenceCommandRequest
	_ = decodeJSON(r, &req)
	item, err := s.casework.CommandEvidence(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), command, req)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

// ---- 报告 ------------------------------------------------------------------

func (s *Server) handleUserCaseReports(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	items, err := s.casework.ListReports(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), "", r.URL.Query().Get("status"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleUserCaseReportCreate(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "case", chi.URLParam(r, "id")) {
		return
	}
	var req struct {
		InvestigationID string `json:"investigation_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	item, err := s.casework.GenerateReport(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), req.InvestigationID)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondCreated(w, r, item)
}

func (s *Server) handleUserReports(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	items, err := s.casework.ListReports(r.Context(), s.caseworkActor(r), r.URL.Query().Get("case_id"), r.URL.Query().Get("investigation_id"), r.URL.Query().Get("status"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, len(items), nil)
}

func (s *Server) handleUserReportDetail(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "report", chi.URLParam(r, "id")) {
		return
	}
	item, err := s.casework.GetReport(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserReportSubmit(w http.ResponseWriter, r *http.Request) {
	s.handleUserReportCommand(w, r, "submit")
}
func (s *Server) handleUserReportApprove(w http.ResponseWriter, r *http.Request) {
	s.handleUserReportCommand(w, r, "approve")
}
func (s *Server) handleUserReportReject(w http.ResponseWriter, r *http.Request) {
	s.handleUserReportCommand(w, r, "reject")
}
func (s *Server) handleUserReportSign(w http.ResponseWriter, r *http.Request) {
	s.handleUserReportCommand(w, r, "sign")
}
func (s *Server) handleUserReportArchive(w http.ResponseWriter, r *http.Request) {
	s.handleUserReportCommand(w, r, "archive")
}

func (s *Server) handleUserReportCommand(w http.ResponseWriter, r *http.Request, command string) {
	if !s.caseworkReady(w, r) {
		return
	}
	var req casework.ReportCommandRequest
	_ = decodeJSON(r, &req)
	item, err := s.casework.CommandReport(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"), command, ifMatchRevision(r), req)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserReportTicket(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	if !s.scopedObject(w, r, "report", chi.URLParam(r, "id")) {
		return
	}
	ticket, err := s.casework.CreateReportTicket(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondCreated(w, r, ticket)
}

func (s *Server) handleUserReportDownload(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	body, report, err := s.casework.ConsumeReportTicket(r.Context(), s.caseworkActor(r), chi.URLParam(r, "ticket"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="investigation-report-r`+strconv.FormatInt(report.Revision, 10)+`.md"`)
	w.Header().Set("ETag", `"`+report.ContentHash+`"`)
	w.Header().Set("X-Content-SHA256", report.ContentHash)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "report.md", time.Now(), bytes.NewReader(body))
}

// ---- 通知与 Inbox ----------------------------------------------------------

func (s *Server) handleUserNotifications(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	unreadOnly := r.URL.Query().Get("unread") == "true" || r.URL.Query().Get("unread") == "1"
	items, next, total, err := s.casework.ListNotifications(r.Context(), s.caseworkActor(r), unreadOnly, r.URL.Query().Get("type"), r.URL.Query().Get("cursor"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, total, next)
}

func (s *Server) handleUserNotificationRead(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	item, err := s.casework.MarkNotificationRead(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, item)
}

func (s *Server) handleUserNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	n, err := s.casework.MarkAllNotificationsRead(r.Context(), s.caseworkActor(r))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondOK(w, r, map[string]any{"marked_read": n})
}

func (s *Server) handleUserNotificationOpen(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	item, err := s.casework.MarkNotificationRead(r.Context(), s.caseworkActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	if item.Href == "" {
		respondOK(w, r, map[string]any{"notification": item, "target": nil})
		return
	}
	// USER-010：跳转前必须重新做对象级授权。无权时统一 404，避免通过通知枚举对象。
	switch item.ObjectType {
	case "case", "investigation", "report":
		if !s.scopedObject(w, r, item.ObjectType, item.ObjectID) {
			return
		}
	case "finding", "evidence":
		invID, _ := item.Payload["investigation_id"].(string)
		if invID == "" {
			respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "通知对象不存在或无权访问", nil)
			return
		}
		if !s.scopedObject(w, r, "investigation", invID) {
			return
		}
	default:
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "通知对象不存在或无权访问", nil)
		return
	}
	respondOK(w, r, map[string]any{"notification": item, "target": item.Href})
}

func (s *Server) handleUserInbox(w http.ResponseWriter, r *http.Request) {
	if !s.caseworkReady(w, r) {
		return
	}
	items, err := s.casework.ListInbox(r.Context(), s.caseworkActor(r), r.URL.Query().Get("status"), 0)
	if err != nil {
		s.writeCaseworkError(w, r, err)
		return
	}
	respondList(w, r, items, len(items), nil)
}
