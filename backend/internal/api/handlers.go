package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func jsonEncode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

// handleMe 鉴权接口预留（§72）：返回当前用户上下文。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "roles": auth.Roles})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 10<<20)).Decode(v)
}

func (s *Server) handleEnsure(w http.ResponseWriter, r *http.Request) {
	var req dataset.EnsureRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return
	}
	// 服务端注入租户/操作者作用域：EnsureRequest 的这两个字段是 json:"-"，
	// 客户端无法提交；旧实现留空则会退化成 Store.DefaultOrgWorkspace（P0 成因之一）。
	scope, ok := s.tenantScopeOr401(w, r)
	if !ok {
		return
	}
	wsID, err := s.writableWorkspace(r.Context(), scope)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	p := PrincipalFrom(r.Context())
	req.OrganizationID = scope.OrganizationID
	req.WorkspaceID = wsID
	req.CreatedBy = p.Email
	req.CreatedByIdentity = p.IdentityID
	// §21.2：/datasets/ensure 只要求 download:create，但 create_missing_meta=true 会经
	// addressmeta.EnsureLinked 真实创建案件/标签。这里必须按既有写入口的权限键显式授权，
	// 否则只持有 download:create 的调用方可以越权造元数据。
	if req.CreateMissingMeta {
		caseRequested := strings.TrimSpace(req.CaseName) != ""
		tagRequested := len(trimDedupeStrings(req.Tags)) > 0
		for _, meta := range req.SubjectMeta {
			if strings.TrimSpace(meta.CaseName) != "" {
				caseRequested = true
			}
			if len(trimDedupeStrings(meta.Tags)) > 0 {
				tagRequested = true
			}
		}
		if !s.authorizeMetaCreate(w, r, caseRequested, tagRequested) {
			return
		}
	}
	resp, err := s.svc.Ensure(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (s *Server) handleCoveragePreview(w http.ResponseWriter, r *http.Request) {
	var req dataset.EnsureRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return
	}
	scope, ok := s.tenantScopeOr401(w, r)
	if !ok {
		return
	}
	wsID, err := s.writableWorkspace(r.Context(), scope)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	req.OrganizationID = scope.OrganizationID
	req.WorkspaceID = wsID
	resp, err := s.svc.Preview(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	// P0 修复：任务列表必须按主体作用域过滤。
	// 旧实现调用 Store.ListJobs（无 WHERE 租户条件），把全部组织的任务连同
	// organization_id / workspace_id 一起返回给任何调用者。
	scope, ok := s.tenantScopeOr401(w, r)
	if !ok {
		return
	}
	jobs, err := s.deps.Store.ListJobsScoped(r.Context(), scope.OrganizationID, scope.WorkspaceID, status, limit, offset)
	if err != nil {
		// [FIX 2026-09-13 / P0] 旧实现把 err.Error()（含原始 SQL 报错文本）直接写进响应体：
		// 既泄露内部结构，又使该错误在服务端完全不可观测（历史 /api/v1/download-jobs 500 即如此）。
		// 现改为：响应体只给稳定错误码，详情写入服务端日志。
		if s.deps.Log != nil {
			s.deps.Log.Error("list jobs failed", "err", err.Error(),
				"request_id", requestIDFrom(r), "trace_id", traceIDFrom(r),
				"organization_id", scope.OrganizationID, "workspace_id", scope.WorkspaceID,
				"status", status, "limit", limit, "offset", offset)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

type jobDetail struct {
	Job         model.DownloadJob              `json:"job"`
	Subjects    []model.Subject                `json:"subjects"`
	Datasets    []model.DatasetItem            `json:"datasets"`
	Shards      []model.Shard                  `json:"shards"`
	Progress    map[string]any                 `json:"progress"`
	Performance *model.PerformanceRuntimeState `json:"performance,omitempty"`
	Guards      []model.VolumeGuard            `json:"guards"`

	// V1.2（§85）：Planning API Cost 与 Acquisition API Cost 分账
	PlanningAPIRequests   int64   `json:"planning_api_requests"`
	PlanningAPICostUSD    float64 `json:"planning_api_cost_usd"`
	AcquisitionAPICostUSD float64 `json:"acquisition_api_cost_usd"`
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	// 对象级授权：跨组织/跨工作区统一 404，不泄露任务是否存在。
	if !s.scopedObject(w, r, "job", chi.URLParam(r, "id")) {
		return
	}
	id := chi.URLParam(r, "id")
	job, err := s.deps.Store.GetJob(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "job not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	detail := jobDetail{Job: *job}
	subjects, _ := s.deps.Store.SubjectsByJob(r.Context(), id)
	if subjects == nil {
		subjects = []model.Subject{}
	}
	detail.Subjects = subjects
	// A shard's from/to timestamps are planning boundaries, not its live cursor.
	// Attach the cursor from the most recently durable checkpoint so the UI
	// reports the chain time actually reached by this address.
	for i := range detail.Subjects {
		var cursorMS *int64
		var checkpointAt *time.Time
		err := s.deps.Store.Pool.QueryRow(r.Context(), `
			SELECT NULLIF(sh.page_state->>'tron_last_timestamp_ms','')::bigint, cp.last_success_at
			FROM download_shards sh
			JOIN checkpoints cp ON cp.shard_id=sh.id
			WHERE sh.subject_id=$1 AND sh.page_state ? 'tron_last_timestamp_ms'
			ORDER BY cp.last_success_at DESC LIMIT 1`, detail.Subjects[i].ID).Scan(&cursorMS, &checkpointAt)
		if err == nil && cursorMS != nil {
			if detail.Subjects[i].Stats == nil {
				detail.Subjects[i].Stats = map[string]any{}
			}
			detail.Subjects[i].Stats["cursor_timestamp_ms"] = *cursorMS
			if checkpointAt != nil {
				detail.Subjects[i].Stats["cursor_checkpoint_at"] = *checkpointAt
			}
		}
	}
	detail.Datasets = []model.DatasetItem{}
	for _, subj := range subjects {
		items, _ := s.deps.Store.DatasetsBySubject(r.Context(), subj.ID)
		if items == nil {
			items = []model.DatasetItem{}
		}
		detail.Datasets = append(detail.Datasets, items...)
	}
	shards, _ := s.deps.Store.ShardsByJob(r.Context(), id)
	if shards == nil {
		shards = []model.Shard{}
	}
	detail.Shards = shards
	guards, _ := s.deps.Store.VolumeGuardsByJob(r.Context(), id)
	if guards == nil {
		guards = []model.VolumeGuard{}
	}
	detail.Guards = guards
	detail.Progress = s.snapshotFor(r.Context(), id)
	if runtimeState, runtimeErr := s.deps.Store.GetAnkrRuntimeState(r.Context()); runtimeErr == nil {
		runtimeState.RequestedMode = job.RequestedMode
		detail.Performance = &runtimeState
	}
	// V1.2（§85）：成本分账 —— count_oracle 用量 = Planning；其余 = Acquisition
	var totalCost float64
	_ = s.deps.Store.Pool.QueryRow(r.Context(),
		"SELECT COALESCE(SUM(estimated_cost_usd),0), COUNT(*) FILTER (WHERE usage_type LIKE 'count_oracle%'), "+
			"COALESCE(SUM(estimated_cost_usd) FILTER (WHERE usage_type LIKE 'count_oracle%'),0) "+
			"FROM provider_usage WHERE job_id=$1", id).
		Scan(&totalCost, &detail.PlanningAPIRequests, &detail.PlanningAPICostUSD)
	detail.AcquisitionAPICostUSD = totalCost - detail.PlanningAPICostUSD
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.controlJob(w, r, model.JobPauseReq, "pause")
}
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	s.controlJob(w, r, model.JobResuming, "resume")
}
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.controlJob(w, r, model.JobCancelReq, "cancel")
}

type volumeGuardActionRequest struct {
	Subject string `json:"subject"`
	Dataset string `json:"dataset"`
}

func (s *Server) handleGetVolumeGuards(w http.ResponseWriter, r *http.Request) {
	if !s.scopedObject(w, r, "job", chi.URLParam(r, "id")) {
		return
	}
	guards, err := s.deps.Store.VolumeGuardsByJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"guards": guards})
}

func (s *Server) decodeVolumeGuardAction(w http.ResponseWriter, r *http.Request) (volumeGuardActionRequest, bool) {
	var req volumeGuardActionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return req, false
	}
	req.Subject = strings.TrimSpace(req.Subject)
	req.Dataset = strings.ToLower(strings.TrimSpace(req.Dataset))
	if req.Subject == "" || !model.ValidDataset(req.Dataset) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid subject and dataset required"})
		return req, false
	}
	return req, true
}

func (s *Server) handleContinueVolumeGuard(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeVolumeGuardAction(w, r)
	if !ok {
		return
	}
	jobID := chi.URLParam(r, "id")
	guard, err := s.deps.Store.ContinueVolumeGuard(r.Context(), jobID, req.Subject, req.Dataset, auth.FromContext(r.Context()).Email)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "guard unit not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	s.deps.Store.InsertAudit(r.Context(), nil, "volume_guard.approved", "download_dataset", guard.DatasetID, map[string]any{"job_id": jobID, "subject": req.Subject, "dataset": req.Dataset, "projection": guard.ApprovedProjection})
	s.publishEvent("events.jobs."+jobID, map[string]any{"type": "VOLUME_GUARD_APPROVED", "job_id": jobID, "subject": req.Subject, "dataset": req.Dataset, "status": guard.State, "ts": time.Now()})
	writeJSON(w, http.StatusOK, guard)
}

func (s *Server) handleAbortVolumeGuard(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeVolumeGuardAction(w, r)
	if !ok {
		return
	}
	jobID := chi.URLParam(r, "id")
	guard, err := s.deps.Store.AbortVolumeGuard(r.Context(), jobID, req.Subject, req.Dataset)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "guard unit not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	s.deps.Store.InsertAudit(r.Context(), nil, "volume_guard.aborted", "download_dataset", guard.DatasetID, map[string]any{"job_id": jobID, "subject": req.Subject, "dataset": req.Dataset, "coverage": "PARTIAL"})
	s.publishEvent("events.jobs."+jobID, map[string]any{"type": "VOLUME_GUARD_ABORTED", "job_id": jobID, "subject": req.Subject, "dataset": req.Dataset, "status": guard.State, "coverage": "PARTIAL", "ts": time.Now()})
	writeJSON(w, http.StatusOK, guard)
}

func (s *Server) handleSubjectControl(w http.ResponseWriter, r *http.Request) {
	jobID, subjectID, action := chi.URLParam(r, "id"), chi.URLParam(r, "subjectID"), chi.URLParam(r, "action")
	if action != "pause" && action != "resume" && action != "complete" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unsupported address action"})
		return
	}
	status, err := s.deps.Store.ControlSubject(r.Context(), jobID, subjectID, action)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "address not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	if action == "complete" {
		_, _ = s.deps.Store.RecomputeJob(r.Context(), jobID)
	}
	s.deps.Store.InsertAudit(r.Context(), nil, "download_subject."+action, "download_subject", subjectID, map[string]any{"job_id": jobID, "status": status, "partial": action == "complete"})
	s.publishEvent("events.jobs."+jobID, map[string]any{"type": "subject_status", "job_id": jobID, "subject_id": subjectID, "status": status, "ts": time.Now()})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID, "subject_id": subjectID, "status": status})
}

// canResumeJob 决定哪些状态允许"继续/重试"。
//
//   - PAUSED / PAUSED_FALLBACK_TOO_EXPENSIVE：常规恢复。
//   - PAUSE_REQUESTED：暂停收敛可能因为残留 Shard 短暂停在该状态。旧实现只接受
//     PAUSED，用户按下暂停后既不能完成暂停也不能恢复下载（409 固定失败，
//     现场投诉"暂停之后不能恢复下载"）。
//   - FAILED / PARTIAL_FAILED：允许重新规划缺口后重试（Planner.ResumeJob 会保留
//     已完成数据、只对未完成数据集重新规划），否则失败任务没有任何补救入口。
func canResumeJob(status string) bool {
	switch status {
	case model.JobPaused, model.JobPausedFallbackTooExpensive, model.JobPauseReq,
		model.JobFailed, model.JobPartialFailed:
		return true
	default:
		return false
	}
}

func (s *Server) controlJob(w http.ResponseWriter, r *http.Request, target, action string) {
	id := chi.URLParam(r, "id")
	job, err := s.deps.Store.GetJob(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "job not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	switch action {
	case "pause":
		if job.Status != model.JobRunning && job.Status != model.JobQueued && job.Status != model.JobPauseReq {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "job is " + job.Status})
			return
		}
	case "resume":
		if !canResumeJob(job.Status) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "job is " + job.Status})
			return
		}
	case "cancel":
		switch job.Status {
		case model.JobCompleted, model.JobCancelled, model.JobFailed, model.JobPartialFailed, model.JobAbortedByUser:
			writeJSON(w, http.StatusConflict, map[string]any{"error": "job is " + job.Status})
			return
		}
	}
	if err := s.deps.Store.SetJobStatus(r.Context(), id, target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.deps.Store.InsertAudit(r.Context(), nil, "download_job."+action, "download_job", id, map[string]any{"status": target})
	s.publishEvent("events.jobs."+id, map[string]any{
		"type": "job_status", "job_id": id, "status": target, "ts": time.Now(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "status": target})
}

func (s *Server) handleProviderStatus(w http.ResponseWriter, r *http.Request) {
	hs := s.deps.Prov.Health(r.Context())
	writeJSON(w, http.StatusOK, hs)
}

func (s *Server) handleProviderUsage(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-24 * time.Hour)
	if v := r.URL.Query().Get("hours"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 && h <= 24*30 {
			since = time.Now().Add(-time.Duration(h) * time.Hour)
		}
	}
	summary, err := s.deps.Store.UsageSummary(r.Context(), since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"since": since, "by_dataset": summary})
}

func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request) {
	chain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chain")))
	subject := strings.TrimSpace(r.URL.Query().Get("subject"))
	ds := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("dataset")))
	if chain == "" || subject == "" || ds == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "chain, subject, dataset required"})
		return
	}
	if err := s.validateChain(chain); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !model.ValidDataset(ds) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown dataset " + ds})
		return
	}
	normalized, err := dataset.NormalizeAndDedupeAddressesForChain(chain, []string{subject})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	subject = normalized[0]
	rows, err := s.deps.Store.ListCoverage(r.Context(), chain, subject, ds)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"coverage": rows})
}

func (s *Server) handleCoverageStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.deps.Store.CoverageStats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": stats})
}

func (s *Server) handleDatasetRows(w http.ResponseWriter, r *http.Request) {
	ds := chi.URLParam(r, "dataset")
	if !model.ValidDataset(ds) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown dataset " + ds})
		return
	}
	chain := r.URL.Query().Get("chain")
	subject := r.URL.Query().Get("subject")
	if chain == "" {
		chain = "bsc"
	}
	chain = strings.ToLower(strings.TrimSpace(chain))
	if err := s.validateChain(chain); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if subject != "" {
		normalized, err := dataset.NormalizeAndDedupeAddressesForChain(chain, []string{subject})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		subject = normalized[0]
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	rows, err := s.query.Rows(r.Context(), ds, chain, subject, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dataset": ds, "rows": rows, "count": len(rows)})
}

// handleAddressActivity 地址活动查询（§87）。
func (s *Server) handleAddressActivity(w http.ResponseWriter, r *http.Request) {
	address := chi.URLParam(r, "address")
	if address == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "address required"})
		return
	}
	chain := r.URL.Query().Get("chain")
	if chain == "" {
		chain = "bsc"
	}
	chain = strings.ToLower(strings.TrimSpace(chain))
	if err := s.validateChain(chain); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	normalized, err := dataset.NormalizeAndDedupeAddressesForChain(chain, []string{address})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	address = normalized[0]
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	rows, err := s.query.Activity(r.Context(), chain, address, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"address": address, "chain": chain, "rows": rows, "count": len(rows)})
}

// snapshotFor 轻量进度快照（API 侧，供 SSE 降级轮询与详情页使用）。
func (s *Server) snapshotFor(ctx context.Context, jobID string) map[string]any {
	var rows, bytes int64
	var total, done int64
	var reqs int64
	var cost float64
	_ = s.deps.Store.Pool.QueryRow(ctx,
		"SELECT COALESCE(SUM(downloaded_rows) FILTER (WHERE "+store.EffectiveShardSQL("")+"),0), "+
			"COALESCE(SUM(downloaded_bytes) FILTER (WHERE "+store.EffectiveShardSQL("")+"),0), "+
			"COUNT(*) FILTER (WHERE "+store.EffectiveShardSQL("")+"), "+
			"COUNT(*) FILTER (WHERE status='COMPLETED' AND "+store.EffectiveShardSQL("")+") "+
			"FROM download_shards WHERE job_id=$1", jobID).
		Scan(&rows, &bytes, &total, &done)
	_ = s.deps.Store.Pool.QueryRow(ctx,
		"SELECT COUNT(*), COALESCE(SUM(estimated_cost_usd),0) FROM provider_usage WHERE job_id=$1 AND usage_type<>'trc20_enrichment_summary'", jobID).
		Scan(&reqs, &cost)
	job, err := s.deps.Store.GetJob(ctx, jobID)
	if err != nil {
		return nil
	}
	var progress float32
	if total > 0 {
		progress = float32(done) / float32(total)
	}
	end := time.Now()
	switch job.Status {
	case model.JobCompleted, model.JobPartialFailed, model.JobFailed, model.JobCancelled, model.JobAbortedByUser:
		if !job.UpdatedAt.IsZero() {
			end = job.UpdatedAt
		}
	}
	elapsed := end.Sub(job.CreatedAt).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	var eta float64
	if progress > 0 && progress < 1 && elapsed > 0 {
		eta = elapsed * (1 - float64(progress)) / float64(progress)
	}
	snap := map[string]any{
		"job_id": jobID, "status": job.Status, "progress": progress,
		"downloaded_rows": rows, "downloaded_bytes": bytes, "api_requests": reqs, "api_cost_usd": cost,
		"total_shards": total, "done_shards": done,
		"elapsed_seconds": elapsed, "eta_seconds": eta, "ts": time.Now(),
	}
	snap["trc20_metrics"] = s.trc20MetricsSnapshot(ctx, jobID, elapsed)
	if v, ok := job.Stats["coverage_before"].(float64); ok {
		snap["coverage_hit_ratio"] = v
	}
	if v, ok := job.Stats["time_to_first_fetch_ms"].(float64); ok {
		snap["time_to_first_fetch_ms"] = v
	}
	// V1.2（§80/§85）：Planning 成本分账 + 全局并发（worker 心跳）
	var planningReqs int64
	var planningCost float64
	_ = s.deps.Store.Pool.QueryRow(ctx,
		"SELECT COUNT(*), COALESCE(SUM(estimated_cost_usd),0) FROM provider_usage "+
			"WHERE job_id=$1 AND usage_type LIKE 'count_oracle%'", jobID).
		Scan(&planningReqs, &planningCost)
	snap["planning_api_requests"] = planningReqs
	snap["planning_api_cost_usd"] = planningCost
	snap["acquisition_api_cost_usd"] = cost - planningCost
	if st, err := s.deps.Store.GetSchedulerState(ctx); err == nil && st != nil {
		snap["global_active_slots"] = st.ActiveSlots
		snap["global_weighted_slots"] = st.WeightedSlots
		snap["global_concurrency_target"] = st.ConcurrencyTarget
		snap["global_ceiling"] = st.HardCeiling
		snap["global_ramp_phase"] = st.RampPhase
		snap["time_to_full_safe_concurrency_ms"] = st.TimeToFullSafeConcurrencyMs
	}
	return snap
}

func (s *Server) trc20MetricsSnapshot(ctx context.Context, jobID string, elapsed float64) map[string]any {
	var rows, logicalCalls, httpRequests, logsRequests, matchedRows, fallbackRows, savedCalls int64
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT COALESCE(SUM(rows),0),COALESCE(SUM(logical_calls),0),COALESCE(SUM(http_requests),0),
		COALESCE(SUM(logs_requests),0),COALESCE(SUM(matched_rows),0),COALESCE(SUM(fallback_rows),0),COALESCE(SUM(saved_calls),0)
		FROM provider_usage WHERE job_id=$1 AND usage_type='trc20_enrichment_summary'`, jobID).
		Scan(&rows, &logicalCalls, &httpRequests, &logsRequests, &matchedRows, &fallbackRows, &savedCalls)
	reasons := map[string]int{}
	reasonRows, err := s.deps.Store.Pool.Query(ctx, "SELECT failure_reasons FROM provider_usage WHERE job_id=$1 AND usage_type='trc20_enrichment_summary'", jobID)
	if err == nil {
		defer reasonRows.Close()
		for reasonRows.Next() {
			var raw []byte
			var current map[string]int
			if reasonRows.Scan(&raw) == nil && json.Unmarshal(raw, &current) == nil {
				for reason, count := range current {
					reasons[reason] += count
				}
			}
		}
	}
	perRow, matchRate, fallbackPct, rowsPerSecond := 0.0, 0.0, 0.0, 0.0
	if rows > 0 {
		perRow = float64(logicalCalls) / float64(rows)
		matchRate = float64(matchedRows) / float64(rows)
		fallbackPct = float64(fallbackRows) / float64(rows)
		if elapsed > 0 {
			rowsPerSecond = float64(rows) / elapsed
		}
	}
	return map[string]any{
		"rows": rows, "receipt_logical_calls": logicalCalls, "receipt_http_requests": httpRequests,
		"receipt_calls_per_row": perRow, "logs_requests": logsRequests, "logs_matched_rows": matchedRows,
		"logs_match_rate": matchRate, "receipt_fallback_rows": fallbackRows, "receipt_fallback_pct": fallbackPct,
		"saved_receipt_calls": savedCalls, "wall_clock_seconds": elapsed, "rows_per_second": rowsPerSecond,
		"failure_reasons": reasons,
	}
}
