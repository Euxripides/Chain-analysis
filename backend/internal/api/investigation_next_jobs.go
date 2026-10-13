package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/fundtrace"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const nextObjectLimit = 32 << 20

type nextWorkerKey struct{}
type nextWorkerState struct {
	JobID, Owner          string
	Fence                 int64
	Scope                 tenantScope
	InputObject, InputSHA string
}

func (s *Server) nextTenant(ctx context.Context) (tenantScope, error) {
	if worker, ok := ctx.Value(nextWorkerKey{}).(*nextWorkerState); ok {
		return worker.Scope, nil
	}
	return s.systemScope(ctx)
}
func nextDecode(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(target)
}
func (s *Server) nextObject(ctx context.Context, key, digest string) ([]byte, error) {
	if s.deps.Raw == nil {
		return nil, summaryError(503, "OBJECT_STORE_UNAVAILABLE", "Evidence storage unavailable")
	}
	r, size, e := s.deps.Raw.OpenNamed(ctx, s.deps.Raw.Bucket(), key)
	if e != nil {
		return nil, summaryError(503, "EVIDENCE_UNAVAILABLE", "Saved evidence unavailable")
	}
	defer r.Close()
	if size > nextObjectLimit {
		return nil, fmt.Errorf("evidence exceeds 32 MiB")
	}
	b, e := io.ReadAll(io.LimitReader(r, nextObjectLimit+1))
	if e != nil {
		return nil, e
	}
	if len(b) > nextObjectLimit || int64(len(b)) != size {
		return nil, summaryError(422, "EVIDENCE_INTEGRITY_FAILED", "Saved evidence length mismatch")
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != digest {
		return nil, summaryError(422, "EVIDENCE_INTEGRITY_FAILED", "Saved evidence checksum mismatch")
	}
	return b, nil
}
func (s *Server) nextFrozen(ctx context.Context, target any) (bool, error) {
	worker, ok := ctx.Value(nextWorkerKey{}).(*nextWorkerState)
	if !ok || worker.InputObject == "" {
		return false, nil
	}
	raw, e := s.nextObject(ctx, worker.InputObject, worker.InputSHA)
	if e != nil {
		return false, e
	}
	return true, nextDecode(raw, target)
}
func (s *Server) nextSaveFrozen(ctx context.Context, input any) error {
	worker, ok := ctx.Value(nextWorkerKey{}).(*nextWorkerState)
	if !ok {
		return nil
	}
	raw, e := json.Marshal(input)
	if e != nil {
		return e
	}
	if len(raw) > nextObjectLimit {
		return fmt.Errorf("materialized input exceeds 32 MiB")
	}
	h := sha256.Sum256(raw)
	digest := hex.EncodeToString(h[:])
	key := fmt.Sprintf("investigation/%s/%s/%s/input-%s.json", worker.Scope.OrganizationID, worker.Scope.WorkspaceID, worker.JobID, digest)
	if _, e = s.deps.Raw.Put(ctx, key, raw, "application/json"); e != nil {
		return e
	}
	tag, e := s.deps.Store.Pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET input_object=$1,input_sha=$2,phase='allocation',updated_at=now() WHERE id=$3 AND lease_owner=$4 AND fence=$5 AND status='running' AND lease_until>now() AND input_object=''`, key, digest, worker.JobID, worker.Owner, worker.Fence)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("analysis lease lost while checkpointing")
	}
	worker.InputObject = key
	worker.InputSHA = digest
	return nil
}

// nextReuseSubmission works before RPC/storage access and is rechecked under the
// workspace lock, so retries can retrieve the original snapshot during outages.
func nextReuseSubmission(row pgx.Row, digest, key string) (any, bool, error) {
	var id, oldSHA, status, version string
	var boundary []byte
	e := row.Scan(&id, &oldSHA, &status, &version, &boundary)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	if oldSHA != digest {
		return nil, false, summaryError(409, "IDEMPOTENCY_CONFLICT", "Idempotency key already names different operation arguments")
	}
	var snapshot any
	if e = nextDecode(boundary, &snapshot); e != nil {
		return nil, false, e
	}
	return map[string]any{"analysis_id": id, "status": status, "version": version, "idempotency_key": key, "snapshot": snapshot, "reused": true}, true, nil
}

const nextSubmissionSQL = `SELECT id::text,request_sha,status,version,boundary FROM investigation_analysis_jobs WHERE organization_id=$1 AND workspace_id=$2 AND idempotency_key=$3`

func (s *Server) nextSubmit(ctx context.Context, operation string, q nextRequest) (any, error) {
	scope, e := s.nextTenant(ctx)
	if e != nil {
		return nil, e
	}
	q.Mode = "inline"
	q.AnalysisID = ""
	key := q.IdempotencyKey
	if key == "" {
		key = uuid.NewString()
	}
	q.IdempotencyKey = ""
	digest := nextHash(map[string]any{"operation": operation, "version": nextVersion, "request": q})
	if reused, found, err := nextReuseSubmission(s.deps.Store.Pool.QueryRow(ctx, nextSubmissionSQL, scope.OrganizationID, scope.WorkspaceID, key), digest, key); err != nil || found {
		return reused, err
	}
	if s.deps.Raw == nil {
		return nil, summaryError(503, "OBJECT_STORE_UNAVAILABLE", "Durable analysis requires object storage")
	}
	boundary := map[string]any{}
	if operation != "explain_transaction" && operation != "match_bridge_transfer" {
		point, e := s.nextPoint(ctx, q)
		if e != nil {
			return nil, e
		}
		if q.ToBlock != nil && operation != "get_token_allowances" {
			point, e = s.nextPoint(ctx, nextRequest{Chain: q.Chain, Block: json.RawMessage(fmt.Sprintf("%d", *q.ToBlock))})
			if e != nil {
				return nil, e
			}
		}
		q.Timestamp = ""
		q.Block = json.RawMessage(fmt.Sprintf("%d", point.Block))
		boundary = map[string]any{"chain": q.Chain, "point": point}
	}
	raw, e := json.Marshal(q)
	if e != nil {
		return nil, e
	}
	tx, e := s.deps.Store.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope.OrganizationID+":"+scope.WorkspaceID); e != nil {
		return nil, e
	}
	var id string
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
	cursorKey := make([]byte, 32)
	if _, e = rand.Read(cursorKey); e != nil {
		return nil, e
	}
	boundaryRaw, _ := json.Marshal(boundary)
	e = tx.QueryRow(ctx, `INSERT INTO investigation_analysis_jobs(organization_id,workspace_id,operation,version,request,request_sha,idempotency_key,boundary,cursor_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text`, scope.OrganizationID, scope.WorkspaceID, operation, nextVersion, raw, digest, key, boundaryRaw, cursorKey).Scan(&id)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return map[string]any{"analysis_id": id, "status": "queued", "reused": false, "version": nextVersion, "idempotency_key": key, "snapshot": boundary, "result_tools": []string{"get_analysis_status", "get_analysis_results", "create_evidence_bundle"}}, nil
}

type nextJob struct {
	RequestSHA                                                                            string
	ID, Operation, Version, Status, Phase, InputObject, InputSHA, ResultObject, ResultSHA string
	Request, Boundary, Meta                                                               []byte
	CursorKey                                                                             []byte
	Attempts                                                                              int
	Fence                                                                                 int64
	ErrorCode, ErrorMessage                                                               *string
	Created, Updated                                                                      time.Time
}

func (s *Server) nextGetJob(ctx context.Context, id string, metadata ...bool) (nextJob, error) {
	var job nextJob
	scope, e := s.nextTenant(ctx)
	if e != nil {
		return job, e
	}
	e = s.deps.Store.Pool.QueryRow(ctx, `SELECT id::text,operation,version,status,phase,request,boundary,input_object,input_sha,result_object,result_sha,CASE WHEN $4 THEN result_meta ELSE jsonb_build_object('sections',result_meta->'sections') END,cursor_key,attempts,fence,error_code,error_message,created_at,updated_at,request_sha FROM investigation_analysis_jobs WHERE id=$1 AND organization_id=$2 AND workspace_id=$3`, id, scope.OrganizationID, scope.WorkspaceID, len(metadata) == 0 || metadata[0]).Scan(&job.ID, &job.Operation, &job.Version, &job.Status, &job.Phase, &job.Request, &job.Boundary, &job.InputObject, &job.InputSHA, &job.ResultObject, &job.ResultSHA, &job.Meta, &job.CursorKey, &job.Attempts, &job.Fence, &job.ErrorCode, &job.ErrorMessage, &job.Created, &job.Updated, &job.RequestSHA)
	if errors.Is(e, pgx.ErrNoRows) {
		e = summaryError(404, "ANALYSIS_NOT_FOUND", "Analysis not found")
	}
	return job, e
}

type nextCursor struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	SHA     string `json:"sha"`
	After   int64  `json:"after"`
}

func signNextCursor(c nextCursor, key []byte) string {
	raw, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func parseNextCursor(token string, job nextJob, section string) (int64, error) {
	p := strings.Split(token, ".")
	if len(p) != 2 {
		return 0, fmt.Errorf("invalid cursor")
	}
	mac := hmac.New(sha256.New, job.CursorKey)
	mac.Write([]byte(p[0]))
	signature, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return 0, fmt.Errorf("invalid cursor signature")
	}
	raw, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return 0, e
	}
	var c nextCursor
	if e = json.Unmarshal(raw, &c); e != nil || c.ID != job.ID || c.SHA != job.ResultSHA || c.Section != section || c.After < 0 {
		return 0, fmt.Errorf("cursor is bound to another analysis, snapshot or section")
	}
	return c.After, nil
}
func (s *Server) nextJobOperation(ctx context.Context, q nextRequest, op string) (any, error) {
	if q.AnalysisID == "" {
		return nil, fmt.Errorf("analysis_id is required")
	}
	includeMetadata := q.IncludeMetadata == nil || *q.IncludeMetadata
	job, e := s.nextGetJob(ctx, q.AnalysisID, includeMetadata)
	if e != nil {
		return nil, e
	}
	scope, e := s.nextTenant(ctx)
	if e != nil {
		return nil, e
	}
	if op == "cancel_analysis" || op == "resume_analysis" {
		if op == "resume_analysis" {
			if job.Status != "failed" && job.Status != "cancelled" {
				return nil, summaryError(409, "ANALYSIS_NOT_RESUMABLE", "Only failed/cancelled analyses can resume")
			}
			if job.Version != nextVersion {
				return nil, summaryError(409, "ANALYSIS_VERSION_CHANGED", "Resume requires the original processor version")
			}
			tx, err := s.deps.Store.Pool.Begin(ctx)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope.OrganizationID+":"+scope.WorkspaceID); err != nil {
				return nil, err
			}
			var active int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM investigation_analysis_jobs WHERE organization_id=$1 AND workspace_id=$2 AND status IN ('queued','running')`, scope.OrganizationID, scope.WorkspaceID).Scan(&active); err != nil {
				return nil, err
			}
			if active >= 20 {
				return nil, summaryError(429, "ANALYSIS_QUEUE_FULL", "Workspace has 20 active analyses")
			}
			tag, err := tx.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='queued',phase='resume_queued',attempts=0,fence=fence+1,lease_owner=NULL,lease_until=NULL,error_code=NULL,error_message=NULL,updated_at=now() WHERE id=$1 AND organization_id=$2 AND workspace_id=$3 AND status IN ('failed','cancelled')`, job.ID, scope.OrganizationID, scope.WorkspaceID)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() != 1 {
				return nil, summaryError(409, "ANALYSIS_NOT_RESUMABLE", "Analysis state changed during resume")
			}
			e = tx.Commit(ctx)
		} else {
			_, e = s.deps.Store.Pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='cancelled',phase='cancelled',fence=fence+1,lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE id=$1 AND organization_id=$2 AND workspace_id=$3 AND status IN ('queued','running')`, job.ID, scope.OrganizationID, scope.WorkspaceID)
		}
		if e != nil {
			return nil, e
		}
		job, e = s.nextGetJob(ctx, q.AnalysisID, includeMetadata)
		if e != nil {
			return nil, e
		}
	}
	var meta any
	if e = nextDecode(job.Meta, &meta); e != nil {
		return nil, e
	}
	status := map[string]any{"analysis_id": job.ID, "operation": job.Operation, "version": job.Version, "status": job.Status, "phase": job.Phase, "attempts": job.Attempts, "input_sha256": job.InputSHA, "result_sha256": job.ResultSHA, "error_code": job.ErrorCode, "error_message": job.ErrorMessage, "created_at": job.Created, "updated_at": job.Updated, "metadata": meta, "resume_scope": "Reuses materialized ledger/balances when available; allocation restarts deterministically from that checkpoint"}
	if metadata, ok := meta.(map[string]any); ok {
		status["available_sections"] = metadata["sections"]
	}
	if !includeMetadata {
		delete(status, "metadata")
	}
	if op != "get_analysis_results" {
		return status, nil
	}
	if job.Status != "completed" {
		return nil, summaryError(409, "ANALYSIS_NOT_COMPLETED", "Results become visible after atomic completion")
	}
	section := q.Section
	if section == "" {
		section = "evidence"
	}
	count, counted, e := nextSectionCount(meta, section)
	if e != nil {
		return nil, e
	}
	// Legacy results without section metadata retain the exact SQL fallback.
	if !counted {
		if e = s.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM investigation_analysis_rows WHERE analysis_id=$1 AND section=$2`, job.ID, section).Scan(&count); e != nil {
			return nil, e
		}
	}
	after := int64(-1)
	if q.Cursor != "" {
		after, e = parseNextCursor(q.Cursor, job, section)
		if e != nil {
			return nil, e
		}
	}
	rows, e := s.deps.Store.Pool.Query(ctx, `SELECT ordinal,data FROM investigation_analysis_rows WHERE analysis_id=$1 AND section=$2 AND ordinal>$3 ORDER BY ordinal LIMIT $4`, job.ID, section, after, q.PageSize+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []any{}
	last := after
	more := false
	bytes := 0
	for rows.Next() {
		var ordinal int64
		var raw []byte
		if e = rows.Scan(&ordinal, &raw); e != nil {
			return nil, e
		}
		if len(items) == q.PageSize || bytes+len(raw) > 4<<20 {
			if len(items) == 0 {
				return nil, summaryError(422, "RESULT_ROW_TOO_LARGE", "One result row exceeds page budget")
			}
			more = true
			break
		}
		var data any
		if e = nextDecode(raw, &data); e != nil {
			return nil, e
		}
		bytes += len(raw)
		items = append(items, data)
		last = ordinal
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	var cursor any
	if more {
		cursor = signNextCursor(nextCursor{job.ID, section, job.ResultSHA, last}, job.CursorKey)
	}
	status["section"] = section
	status["items"] = items
	status["record_count"] = count
	status["returned_records"] = len(items)
	status["has_more"] = more
	status["next_cursor"] = cursor
	return status, nil
}

func nextSectionCount(meta any, section string) (int64, bool, error) {
	metadata, ok := meta.(map[string]any)
	if !ok {
		return 0, false, nil
	}
	sections, ok := metadata["sections"].(map[string]any)
	if !ok {
		return 0, false, nil
	}
	value, exists := sections[section]
	if !exists {
		return 0, false, summaryError(400, "UNKNOWN_RESULT_SECTION", "Choose a section listed in available_sections or metadata.sections")
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, false, summaryError(422, "RESULT_METADATA_INVALID", "Saved section count must be an exact nonnegative integer")
	}
	count, e := number.Int64()
	if e != nil || count < 0 {
		return 0, false, summaryError(422, "RESULT_METADATA_INVALID", "Saved section count must be an exact nonnegative integer")
	}
	return count, true, nil
}

func flattenNextResult(data any) (map[string]any, map[string][]any, error) {
	raw, e := json.Marshal(data)
	if e != nil {
		return nil, nil, e
	}
	if len(raw) > nextObjectLimit {
		return nil, nil, summaryError(422, "RESULT_BUDGET_EXCEEDED", "Analysis result exceeds 32 MiB")
	}
	meta := map[string]any{}
	if e = nextDecode(raw, &meta); e != nil {
		return nil, nil, e
	}
	sections := map[string][]any{}
	for name, value := range meta {
		if rows, ok := value.([]any); ok {
			sections[name] = rows
			delete(meta, name)
		}
	}
	if models, ok := sections["models"]; ok {
		summaries := []any{}
		edges := []any{}
		addresses := []any{}
		for _, value := range models {
			model, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if rs, ok := model["edges"].([]any); ok {
				for _, r := range rs {
					edges = append(edges, map[string]any{"model": model["model"], "edge": r})
				}
				delete(model, "edges")
			}
			if sources, ok := model["sources"].(map[string]any); ok {
				for source, value := range sources {
					m, ok := value.(map[string]any)
					if !ok {
						continue
					}
					if remaining, ok := m["remaining_attributed_raw"].(map[string]any); ok {
						for address, amount := range remaining {
							addresses = append(addresses, map[string]any{"model": model["model"], "source_id": source, "address": address, "attributed_balance_raw": amount})
						}
					}
				}
			}
			summaries = append(summaries, model)
		}
		delete(sections, "models")
		sections["edges"] = edges
		sections["addresses"] = addresses
		sort.Slice(addresses, func(i, j int) bool {
			a, _ := json.Marshal(addresses[i])
			b, _ := json.Marshal(addresses[j])
			return string(a) < string(b)
		})
		meta["models"] = summaries
	}
	counts := map[string]int{}
	for name, rows := range sections {
		counts[name] = len(rows)
	}
	meta["sections"] = counts
	return meta, sections, nil
}

// StartInvestigationWorkers is tied to the API run context and disabled in
// standby mode. Leases + fence tokens prevent stale workers publishing results.
func (s *Server) StartInvestigationWorkers(ctx context.Context) {
	go s.nextAnalysisLoop(ctx)
	go s.nextWatchLoop(ctx)
}
func (s *Server) nextAnalysisLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	owner := uuid.NewString()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if e := s.nextWorkOne(ctx, owner); e != nil && ctx.Err() == nil {
				s.deps.Log.Error("investigation analysis worker", "error", e)
			}
		}
	}
}
func (s *Server) nextExpireAnalysisLeases(ctx context.Context) error {
	_, e := s.deps.Store.Pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='failed',phase='failed',fence=fence+1,lease_owner=NULL,lease_until=NULL,error_code='WORKER_RETRY_EXHAUSTED',error_message='Lease expired after three attempts; inspect and resume',updated_at=now() WHERE version=$1 AND status='running' AND lease_until<now() AND attempts>=3`, nextVersion)
	return e
}
func (s *Server) nextWorkOne(parent context.Context, owner string) error {
	if e := s.nextExpireAnalysisLeases(parent); e != nil {
		return e
	}
	var e error
	var id, operation string
	var request, boundary []byte
	var worker nextWorkerState
	worker.Owner = owner
	e = s.deps.Store.Pool.QueryRow(parent, `WITH candidate AS (SELECT id FROM investigation_analysis_jobs WHERE version=$1 AND (status='queued' OR (status='running' AND lease_until<now() AND attempts<3)) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE investigation_analysis_jobs j SET status='running',phase='reading_inputs',lease_owner=$2,lease_until=now()+interval '30 seconds',attempts=attempts+1,fence=fence+1,updated_at=now() FROM candidate WHERE j.id=candidate.id RETURNING j.id::text,j.operation,j.request,j.boundary,j.organization_id::text,j.workspace_id::text,j.fence,j.input_object,j.input_sha`, nextVersion, owner).Scan(&id, &operation, &request, &boundary, &worker.Scope.OrganizationID, &worker.Scope.WorkspaceID, &worker.Fence, &worker.InputObject, &worker.InputSHA)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	worker.JobID = id
	ctx, cancel := context.WithTimeout(parent, 300*time.Second)
	ctx = context.WithValue(ctx, nextWorkerKey{}, &worker)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				tag, e := s.deps.Store.Pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET lease_until=now()+interval '30 seconds',updated_at=now() WHERE id=$1 AND lease_owner=$2 AND fence=$3 AND status='running' AND lease_until>now()`, id, owner, worker.Fence)
				if e != nil || tag.RowsAffected() != 1 {
					cancel()
					return
				}
			}
		}
	}()
	q, e := decodeNext(request)
	if e == nil {
		var b struct {
			Chain string               `json:"chain"`
			Point chainread.StatePoint `json:"point"`
		}
		if er := nextDecode(boundary, &b); er != nil {
			e = er
		} else if b.Chain != "" {
			e = s.nextBlockGuard(ctx, b.Chain, b.Point)
		}
	}
	var result any
	if e == nil {
		result, e = s.nextDispatch(ctx, q, operation)
	}
	if e == nil {
		e = s.nextPublish(ctx, worker, result)
	}
	if e != nil {
		code := "ANALYSIS_FAILED"
		message := "Analysis failed; retry the bounded request or inspect server diagnostics"
		var se *exportSummaryError
		if errors.As(e, &se) {
			code = se.code
			message = se.message
		}
		var providerErr *rpc.Error
		if errors.As(e, &providerErr) {
			code = "RPC_" + strings.ToUpper(string(providerErr.Class))
			message = providerErr.Public()
		}
		if errors.Is(e, fundtrace.ErrAllocationBudget) {
			code = "ALLOCATION_BUDGET_EXCEEDED"
			message = "Allocation work exceeded budget; narrow the requested ledger"
		}
		if errors.Is(e, context.DeadlineExceeded) {
			code = "ANALYSIS_TIMEOUT"
		}
		finish, c := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer c()
		_, finishErr := s.deps.Store.Pool.Exec(finish, `UPDATE investigation_analysis_jobs SET status='failed',phase='failed',lease_owner=NULL,lease_until=NULL,error_code=$1,error_message=$2,updated_at=now() WHERE id=$3 AND lease_owner=$4 AND fence=$5 AND status='running'`, code, message, id, owner, worker.Fence)
		if finishErr != nil {
			return finishErr
		}
		s.deps.Log.Error("investigation analysis failed", "analysis_id", id, "error", e)
	}
	return nil
}
func (s *Server) nextPublish(ctx context.Context, worker nextWorkerState, result any) error {
	meta, sections, e := flattenNextResult(result)
	if e != nil {
		return e
	}
	raw, _ := json.Marshal(result)
	h := sha256.Sum256(raw)
	digest := hex.EncodeToString(h[:])
	key := fmt.Sprintf("investigation/%s/%s/%s/result-%s.json", worker.Scope.OrganizationID, worker.Scope.WorkspaceID, worker.JobID, digest)
	if _, e = s.deps.Raw.Put(ctx, key, raw, "application/json"); e != nil {
		return e
	}
	tx, e := s.deps.Store.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var owned bool
	e = tx.QueryRow(ctx, `SELECT true FROM investigation_analysis_jobs WHERE id=$1 AND lease_owner=$2 AND fence=$3 AND status='running' AND lease_until>now() FOR UPDATE`, worker.JobID, worker.Owner, worker.Fence).Scan(&owned)
	if e != nil {
		return fmt.Errorf("analysis lease lost before publish: %w", e)
	}
	if _, e = tx.Exec(ctx, `DELETE FROM investigation_analysis_rows WHERE analysis_id=$1`, worker.JobID); e != nil {
		return e
	}
	for section, rows := range sections {
		batch := &pgx.Batch{}
		for i, row := range rows {
			encoded, e := json.Marshal(row)
			if e != nil {
				return e
			}
			batch.Queue(`INSERT INTO investigation_analysis_rows(analysis_id,section,ordinal,data) VALUES($1,$2,$3,$4)`, worker.JobID, section, i, encoded)
		}
		if e = tx.SendBatch(ctx, batch).Close(); e != nil {
			return e
		}
	}
	encoded, _ := json.Marshal(meta)
	_, e = tx.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='completed',phase='completed',result_object=$1,result_sha=$2,result_meta=$3,completed_at=now(),updated_at=now(),lease_owner=NULL,lease_until=NULL,error_code=NULL,error_message=NULL WHERE id=$4 AND fence=$5`, key, digest, encoded, worker.JobID, worker.Fence)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
