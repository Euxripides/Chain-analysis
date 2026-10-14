package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var contractOperations = []string{"analyze_contract", "get_contract_analysis", "list_contract_methods", "get_contract_method_analysis", "get_contract_execution_flow", "explain_contract_transaction", "compare_contract_versions", "get_contract_analysis_artifact"}

func (s *Server) registerContractActions(r chi.Router) {
	for _, name := range contractOperations {
		op := name
		prefix := "/analysis/contracts/"
		if op == "get_contract_analysis_artifact" {
			prefix = "/export/contracts/"
		}
		r.Post(prefix+op, func(w http.ResponseWriter, r *http.Request) { s.handleContractAnalysis(w, r, op, false) })
	}
}
func (s *Server) registerContractUserRoutes(r chi.Router) {
	r.Get("/contract-analyses/{analysis_id}/artifacts/{artifact_id}/preview", s.require("evidence:read", func(w http.ResponseWriter, r *http.Request) {
		s.handleContractAnalysis(w, r, "preview_contract_artifact", true)
	}))
	r.Post("/contract-analyses", s.require("investigation:run", func(w http.ResponseWriter, r *http.Request) { s.handleContractAnalysis(w, r, "analyze_contract", true) }))
	r.Get("/contract-analyses", s.require("explorer:read", s.handleContractList))
	for path, operation := range map[string]string{"": "get_contract_analysis", "/methods": "list_contract_methods", "/methods/{method_id}": "get_contract_method_analysis", "/flow": "get_contract_execution_flow", "/transactions": "explain_contract_transaction", "/compare/{compare_id}": "compare_contract_versions", "/artifacts/{artifact_id}": "get_contract_analysis_artifact"} {
		op := operation
		perm := "explorer:read"
		if op == "get_contract_analysis_artifact" {
			perm = "export:download"
		}
		r.Get("/contract-analyses/{analysis_id}"+path, s.require(perm, func(w http.ResponseWriter, r *http.Request) { s.handleContractAnalysis(w, r, op, true) }))
	}
	for _, name := range []string{"cancel", "resume"} {
		op := name
		r.Post("/contract-analyses/{analysis_id}/"+op, s.require("investigation:run", func(w http.ResponseWriter, r *http.Request) { s.handleContractAnalysis(w, r, op, true) }))
	}
}

// Session routes use the actual selected workspace. Only the already-authenticated
// Actions route may resolve the platform API-key workspace.
func (s *Server) contractRequestContext(r *http.Request, session bool) (context.Context, tenantScope, error) {
	var scope tenantScope
	var e error
	if session {
		scope, e = s.tenantScope(r.Context())
		if e == nil {
			scope.WorkspaceID, e = s.writableWorkspace(r.Context(), scope)
		}
	} else {
		scope, e = s.systemScope(r.Context())
	}
	if e != nil || scope.OrganizationID == "" || scope.WorkspaceID == "" {
		return r.Context(), scope, errNoTenantScope
	}
	return context.WithValue(r.Context(), nextWorkerKey{}, &nextWorkerState{Scope: scope}), scope, nil
}
func (s *Server) handleContractAnalysis(w http.ResponseWriter, r *http.Request, op string, session bool) {
	ctx, scope, e := s.contractRequestContext(r, session)
	if e != nil {
		investigationError(w, summaryError(401, "WORKSPACE_REQUIRED", "Select an authorized workspace"))
		return
	}
	var q ca.Request
	if r.Method == http.MethodGet {
		q.AnalysisID = chi.URLParam(r, "analysis_id")
		q.MethodID = chi.URLParam(r, "method_id")
		q.ArtifactID = chi.URLParam(r, "artifact_id")
		q.CompareAnalysisID = chi.URLParam(r, "compare_id")
		v := r.URL.Query()
		q.Section = v.Get("section")
		q.Cursor = v.Get("cursor")
		q.Search = v.Get("search")
		q.Behavior = v.Get("behavior")
		q.TxHash = v.Get("tx_hash")
		q.EvidenceID = v.Get("evidence_id")
		if q.MethodID == "" {
			q.MethodID = v.Get("method_id")
		}
		if v.Get("page_size") != "" {
			q.PageSize, e = strconv.Atoi(v.Get("page_size"))
		}
		if e == nil && v.Get("offset") != "" {
			q.Offset, e = strconv.ParseInt(v.Get("offset"), 10, 64)
		}
		if e == nil && v.Get("chunk_size") != "" {
			q.ChunkSize, e = strconv.Atoi(v.Get("chunk_size"))
		}
	} else if op == "cancel" || op == "resume" {
		q.AnalysisID = chi.URLParam(r, "analysis_id")
	} else {
		var b []byte
		b, e = io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
		if e == nil && len(b) > 2<<20 {
			e = fmt.Errorf("request exceeds 2 MiB")
		}
		if e == nil {
			q, e = ca.Decode(b)
		}
	}
	q.Defaults()
	if e == nil {
		e = q.Validate()
	}
	if e == nil {
		for _, v := range []string{q.AnalysisID, q.CompareAnalysisID} {
			if v != "" {
				if _, e = uuid.Parse(v); e != nil {
					break
				}
			}
		}
	}
	if e != nil {
		investigationError(w, e)
		return
	}
	if session && (q.EvidenceID != "" || q.Section == "evidence" || q.Section == "execution_evidence") {
		s.require("evidence:read", func(w http.ResponseWriter, r *http.Request) {
			ctx, scope, err := s.contractRequestContext(r, true)
			if err != nil {
				investigationError(w, err)
				return
			}
			s.handleContractPrepared(w, r, op, session, ctx, scope, q)
		})(w, r)
		return
	}
	s.handleContractPrepared(w, r, op, session, ctx, scope, q)
}
func (s *Server) handleContractPrepared(w http.ResponseWriter, r *http.Request, op string, session bool, ctx context.Context, scope tenantScope, q ca.Request) {
	bounded, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	var data any
	var e error
	switch op {
	case "analyze_contract":
		data, e = s.contractSubmit(bounded, q)
	case "cancel", "resume":
		data, e = s.contractJobControl(bounded, q.AnalysisID, op)
	case "get_contract_analysis_artifact":
		data, e = s.contractReadArtifact(bounded, q)
	case "preview_contract_artifact":
		data, e = s.contractReadArtifact(bounded, q, true)
	case "compare_contract_versions":
		data, e = s.contractCompare(bounded, q)
	case "explain_contract_transaction":
		if q.AnalysisID == "" {
			if q.TxHash == "" {
				e = fmt.Errorf("tx_hash is required")
			} else {
				q.TxHashes = []string{q.TxHash}
				data, e = s.contractSubmit(bounded, q)
			}
		} else {
			q.Section = "transactions"
			data, e = s.contractPage(bounded, q)
		}
	default:
		if op == "list_contract_methods" || op == "get_contract_method_analysis" {
			q.Section = "methods"
		}
		if op == "get_contract_execution_flow" {
			q.Section = "flows"
		}
		data, e = s.contractPage(bounded, q)
	}
	if e != nil {
		investigationError(w, e)
		return
	}
	{
		auditID := q.AnalysisID
		if auditID == "" {
			if record, ok := data.(map[string]any); ok {
				auditID, _ = record["analysis_id"].(string)
			}
		}
		ev := auditEventFor(r, PrincipalFrom(r.Context()), metaFrom(r.Context()), "contract_analysis."+op, "analysis", "contract_analysis", auditID, "success", map[string]any{"organization_id": scope.OrganizationID, "workspace_id": scope.WorkspaceID, "method_id": q.MethodID, "artifact_id": q.ArtifactID})
		if !session {
			ev.ActorType, ev.ActorName = "api_client", "MCP/GPT Actions"
			ev.OrganizationID, ev.WorkspaceID = scope.OrganizationID, scope.WorkspaceID
		}
		ev.Sensitive = ev.Sensitive || op == "get_contract_analysis_artifact" || q.EvidenceID != "" || q.Section == "evidence" || q.Section == "execution_evidence"
		if e = s.emitAudit(ctx, ev); e != nil {
			investigationError(w, summaryError(503, "AUDIT_UNAVAILABLE", "Analysis audit could not be saved"))
			return
		}
	}
	actionOK(w, data, map[string]any{"dataset_version": ca.Version, "source": "saved_contract_evidence"})
}
func (s *Server) contractSubmit(ctx context.Context, q ca.Request) (any, error) {
	if os.Getenv("CONTRACT_ANALYSIS_ENABLED") != "1" {
		return nil, summaryError(503, "CONTRACT_ANALYSIS_DISABLED", "Contract analysis task submission is disabled")
	}
	runner := ca.Runner{Image: os.Getenv("CONTRACT_ENGINE_IMAGE")}
	if !runner.Ready() {
		return nil, summaryError(503, "CONTRACT_ENGINE_UNAVAILABLE", "Digest-pinned analysis engine is not configured")
	}
	var e error
	q.Chain, e = canonicalActionChain(q.Chain)
	if e != nil {
		return nil, e
	}
	cfg := mustChain(s, q.Chain)
	if !cfg.IsEVM() {
		return nil, fmt.Errorf("contract engine supports EVM chains only")
	}
	if !ca.CompleteAddress.MatchString(q.Address) {
		return nil, fmt.Errorf("complete EVM contract address required")
	}
	q.Address = strings.ToLower(q.Address)
	for i := range q.TxHashes {
		q.TxHashes[i] = strings.ToLower(q.TxHashes[i])
	}
	scope, e := s.nextTenant(ctx)
	if e != nil {
		return nil, e
	}
	key := q.IdempotencyKey
	if key == "" {
		key = uuid.NewString()
	}
	q.IdempotencyKey = ""
	if q.AnalysisID != "" || q.MethodID != "" || q.ArtifactID != "" || q.Cursor != "" || q.Section != "" || q.CompareAnalysisID != "" {
		return nil, fmt.Errorf("submission contains read-only fields")
	}
	digest := nextHash(map[string]any{"operation": ca.Operation, "version": ca.Version, "request": q, "engine": runner.Image})
	if reused, found, err := nextReuseSubmission(s.deps.Store.Pool.QueryRow(ctx, nextSubmissionSQL, scope.OrganizationID, scope.WorkspaceID, key), digest, key); err != nil || found {
		return reused, err
	}
	if s.deps.Raw == nil {
		return nil, summaryError(503, "OBJECT_STORE_UNAVAILABLE", "Durable contract evidence storage unavailable")
	}
	analysisID := uuid.NewString()
	ctx = context.WithValue(ctx, nextWorkerKey{}, &nextWorkerState{JobID: analysisID, Scope: scope})
	ctx, e = s.contractBudgetContext(ctx, scope, analysisID, q)
	if e != nil {
		return nil, e
	}
	ref, at, e := requestRef(investigationRequest{Chain: q.Chain, Block: q.Block, Timestamp: q.Timestamp})
	if e != nil {
		return nil, e
	}
	point, e := s.deps.ChainRead.ResolveState(ctx, q.Chain, ref, at, contractOptions(ctx, q))
	if e != nil {
		return nil, e
	}
	if q.ToBlock != nil && *q.ToBlock > point.Block {
		return nil, fmt.Errorf("history range ends after the selected snapshot")
	}
	q.Block = json.RawMessage(fmt.Sprint(point.Block))
	q.Timestamp = ""
	boundary := map[string]any{"chain": q.Chain, "point": point, "engine_digest": runner.Image}
	tx, e := s.deps.Store.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope.OrganizationID+":"+scope.WorkspaceID); e != nil {
		return nil, e
	}
	if reused, found, err := nextReuseSubmission(tx.QueryRow(ctx, nextSubmissionSQL, scope.OrganizationID, scope.WorkspaceID, key), digest, key); err != nil {
		return nil, err
	} else if found {
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return reused, nil
	}
	var active int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM investigation_analysis_jobs WHERE organization_id=$1 AND workspace_id=$2 AND status IN ('queued','running')`, scope.OrganizationID, scope.WorkspaceID).Scan(&active); e != nil {
		return nil, e
	}
	if active >= 20 {
		return nil, summaryError(429, "ANALYSIS_QUEUE_FULL", "Workspace has 20 active analyses")
	}
	cursor := make([]byte, 32)
	if _, e = rand.Read(cursor); e != nil {
		return nil, e
	}
	e = tx.QueryRow(ctx, `INSERT INTO investigation_analysis_jobs(organization_id,workspace_id,operation,version,request,request_sha,idempotency_key,boundary,cursor_key,id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`, scope.OrganizationID, scope.WorkspaceID, ca.Operation, ca.Version, ca.Row(q), digest, key, ca.Row(boundary), cursor, analysisID).Scan(&analysisID)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return map[string]any{"analysis_id": analysisID, "status": "queued", "version": ca.Version, "snapshot": boundary, "idempotency_key": key, "reused": false, "result_tools": contractOperations[1:]}, nil
}
func (s *Server) contractJob(ctx context.Context, id string) (nextJob, error) {
	j, e := s.nextGetJob(ctx, id)
	if e == nil && (j.Version != ca.Version || j.Operation != ca.Operation) {
		e = summaryError(404, "ANALYSIS_NOT_FOUND", "Contract analysis not found")
	}
	return j, e
}
func (s *Server) contractJobControl(ctx context.Context, id, operation string) (any, error) {
	job, e := s.contractJob(ctx, id)
	if e != nil {
		return nil, e
	}
	scope, e := s.nextTenant(ctx)
	if e != nil {
		return nil, e
	}
	if operation == "resume" {
		if job.Status != "failed" && job.Status != "cancelled" {
			return nil, summaryError(409, "ANALYSIS_NOT_RESUMABLE", "Only failed/cancelled analyses can resume")
		}
		var b struct {
			Engine string `json:"engine_digest"`
		}
		if e = json.Unmarshal(job.Boundary, &b); e != nil {
			return nil, e
		}
		if b.Engine != os.Getenv("CONTRACT_ENGINE_IMAGE") {
			return nil, summaryError(409, "ANALYSIS_VERSION_CHANGED", "Resume requires the original engine digest")
		}
		tx, err := s.deps.Store.Pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope.OrganizationID+":"+scope.WorkspaceID); err != nil {
			return nil, err
		}
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM investigation_analysis_jobs WHERE organization_id=$1 AND workspace_id=$2 AND status IN ('queued','running')`, scope.OrganizationID, scope.WorkspaceID).Scan(&n); err != nil {
			return nil, err
		}
		if n >= 20 {
			return nil, summaryError(429, "ANALYSIS_QUEUE_FULL", "Workspace has 20 active analyses")
		}
		tag, err := tx.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='queued',phase='resume_queued',fence=fence+1,attempts=0,lease_owner=NULL,lease_until=NULL,error_code=NULL,error_message=NULL,updated_at=now() WHERE id=$1 AND organization_id=$2 AND workspace_id=$3 AND status IN ('failed','cancelled')`, id, scope.OrganizationID, scope.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, summaryError(409, "ANALYSIS_STATE_CHANGED", "Analysis state changed")
		}
		e = tx.Commit(ctx)
	} else {
		_, e = s.deps.Store.Pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='cancelled',phase='cancelled',fence=fence+1,lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE id=$1 AND organization_id=$2 AND workspace_id=$3 AND status IN ('queued','running')`, id, scope.OrganizationID, scope.WorkspaceID)
	}
	if e != nil {
		return nil, e
	}
	return s.contractPage(ctx, ca.Request{AnalysisID: id})
}
func (s *Server) contractPage(ctx context.Context, q ca.Request) (any, error) {
	if q.AnalysisID == "" {
		return nil, fmt.Errorf("analysis_id is required")
	}
	job, e := s.contractJob(ctx, q.AnalysisID)
	if e != nil {
		return nil, e
	}
	var meta any
	if e = nextDecode(job.Meta, &meta); e != nil {
		return nil, e
	}
	out := map[string]any{"analysis_id": job.ID, "version": job.Version, "status": job.Status, "phase": job.Phase, "attempts": job.Attempts, "snapshot": json.RawMessage(job.Boundary), "metadata": meta, "error_code": job.ErrorCode, "error_message": job.ErrorMessage, "result_sha256": job.ResultSHA, "created_at": job.Created, "updated_at": job.Updated}
	if q.Section == "" {
		return out, nil
	}
	if job.Status != "completed" {
		return nil, summaryError(409, "ANALYSIS_NOT_COMPLETED", "Results publish after atomic completion")
	}
	if _, _, e = nextSectionCount(meta, q.Section); e != nil {
		return nil, e
	}
	filter := nextHash(map[string]any{"method": q.MethodID, "evidence": q.EvidenceID, "search": q.Search, "behavior": q.Behavior, "tx_hash": q.TxHash})
	binding := q.Section + ":" + filter
	after := int64(-1)
	if q.Cursor != "" {
		after, e = parseNextCursor(q.Cursor, job, binding)
		if e != nil {
			return nil, e
		}
	}
	if q.PageSize == 0 {
		q.PageSize = 100
	}
	where := `analysis_id=$1 AND section=$2 AND ($3='' OR data->>'method_id'=$3 OR (section='flows' AND (data->>'from'=$3 OR data->>'to'=$3))) AND ($4='' OR data->>'evidence_id'=$4) AND ($5='' OR strpos(lower(data->>'name'),lower($5))>0 OR strpos(lower(data->>'selector'),lower($5))>0 OR strpos(lower(data->>'signature'),lower($5))>0) AND ($6='' OR data->'behaviors' ? $6) AND ($7='' OR data->>'tx_hash'=$7)`
	args := []any{job.ID, q.Section, q.MethodID, q.EvidenceID, q.Search, q.Behavior, q.TxHash}
	var count int64
	if e = s.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM investigation_analysis_rows WHERE `+where, args...).Scan(&count); e != nil {
		return nil, e
	}
	args = append(args, after, q.PageSize+1)
	rows, e := s.deps.Store.Pool.Query(ctx, `SELECT ordinal,data FROM investigation_analysis_rows WHERE `+where+` AND ordinal>$8 ORDER BY ordinal LIMIT $9`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []json.RawMessage{}
	last := after
	more := false
	size := 0
	for rows.Next() {
		var ordinal int64
		var raw []byte
		if e = rows.Scan(&ordinal, &raw); e != nil {
			return nil, e
		}
		if len(items) >= q.PageSize || size+len(raw) > 4<<20 {
			if len(items) == 0 {
				return nil, summaryError(422, "RESULT_ROW_TOO_LARGE", "One result row exceeds page budget")
			}
			more = true
			break
		}
		size += len(raw)
		items = append(items, json.RawMessage(raw))
		last = ordinal
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	var cursor any
	if more {
		cursor = signNextCursor(nextCursor{job.ID, binding, job.ResultSHA, last}, job.CursorKey)
	}
	out["section"] = q.Section
	out["items"] = items
	out["record_count"] = count
	out["returned_records"] = len(items)
	out["has_more"] = more
	out["next_cursor"] = cursor
	return out, nil
}
func (s *Server) handleContractList(w http.ResponseWriter, r *http.Request) {
	ctx, scope, e := s.contractRequestContext(r, true)
	if e != nil {
		investigationError(w, e)
		return
	}
	pageSize := 50
	if value := r.URL.Query().Get("page_size"); value != "" {
		pageSize, e = strconv.Atoi(value)
	}
	token := r.URL.Query().Get("cursor")
	if e != nil || pageSize < 1 || pageSize > 200 || len(token) > 2048 {
		investigationError(w, fmt.Errorf("invalid list pagination"))
		return
	}
	var anchorID string
	if token != "" {
		payload, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
		var c nextCursor
		if err != nil || json.Unmarshal(payload, &c) != nil {
			investigationError(w, fmt.Errorf("invalid list cursor"))
			return
		}
		if _, err = uuid.Parse(c.ID); err != nil {
			investigationError(w, fmt.Errorf("invalid list cursor"))
			return
		}
		anchorID = c.ID
	} else {
		e = s.deps.Store.Pool.QueryRow(ctx, `SELECT id::text FROM investigation_analysis_jobs WHERE organization_id=$1 AND workspace_id=$2 AND version=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, scope.OrganizationID, scope.WorkspaceID, ca.Version).Scan(&anchorID)
		if errors.Is(e, pgx.ErrNoRows) {
			actionOK(w, map[string]any{"items": []any{}, "record_count": 0, "returned_records": 0, "has_more": false, "next_cursor": nil}, nil)
			return
		}
		if e != nil {
			investigationError(w, e)
			return
		}
	}
	anchor, e := s.contractJob(ctx, anchorID)
	if e != nil {
		investigationError(w, e)
		return
	}
	anchor.ResultSHA = nextHash(map[string]any{"anchor": anchor.ID, "created": anchor.Created, "scope": scope, "version": ca.Version})
	after := int64(-1)
	if token != "" {
		after, e = parseNextCursor(token, anchor, "contract-job-list")
		if e != nil {
			investigationError(w, e)
			return
		}
	}
	where := `organization_id=$1 AND workspace_id=$2 AND version=$3 AND (created_at,id)<=($4,$5::uuid)`
	args := []any{scope.OrganizationID, scope.WorkspaceID, ca.Version, anchor.Created, anchor.ID}
	var count int64
	if e = s.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM investigation_analysis_jobs WHERE `+where, args...).Scan(&count); e != nil {
		investigationError(w, e)
		return
	}
	rows, e := s.deps.Store.Pool.Query(ctx, `SELECT id::text,status,phase,boundary,request,created_at FROM investigation_analysis_jobs WHERE `+where+` ORDER BY created_at DESC,id DESC OFFSET $6 LIMIT $7`, append(args, after+1, pageSize+1)...)
	if e != nil {
		investigationError(w, e)
		return
	}
	defer rows.Close()
	items := []any{}
	more := false
	for rows.Next() {
		if len(items) >= pageSize {
			more = true
			break
		}
		var id, status, phase string
		var boundary, request []byte
		var created time.Time
		if e = rows.Scan(&id, &status, &phase, &boundary, &request, &created); e != nil {
			break
		}
		var q ca.Request
		_ = json.Unmarshal(request, &q)
		items = append(items, map[string]any{"analysis_id": id, "status": status, "phase": phase, "chain": q.Chain, "address": q.Address, "snapshot": json.RawMessage(boundary), "created_at": created})
	}
	if e == nil {
		e = rows.Err()
	}
	if e != nil {
		investigationError(w, e)
		return
	}
	var next any
	if more {
		next = signNextCursor(nextCursor{anchor.ID, "contract-job-list", anchor.ResultSHA, after + int64(len(items))}, anchor.CursorKey)
	}
	ev := auditEventFor(r, PrincipalFrom(r.Context()), metaFrom(r.Context()), "contract_analysis.list", "analysis", "workspace", scope.WorkspaceID, "success", map[string]any{"organization_id": scope.OrganizationID, "workspace_id": scope.WorkspaceID, "returned_records": len(items)})
	if e = s.emitAudit(ctx, ev); e != nil {
		investigationError(w, summaryError(503, "AUDIT_UNAVAILABLE", "Analysis audit could not be saved"))
		return
	}
	actionOK(w, map[string]any{"items": items, "record_count": count, "returned_records": len(items), "has_more": more, "next_cursor": next, "list_scope": "workspace_jobs_at_list_anchor"}, nil)
}
