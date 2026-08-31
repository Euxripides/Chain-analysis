package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
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
	jobs, err := s.deps.Store.ListJobs(r.Context(), status, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

type jobDetail struct {
	Job      model.DownloadJob    `json:"job"`
	Subjects []model.Subject      `json:"subjects"`
	Datasets []model.DatasetItem  `json:"datasets"`
	Shards   []model.Shard        `json:"shards"`
	Progress map[string]any       `json:"progress"`
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
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
	detail.Progress = s.snapshotFor(r.Context(), id)
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) { s.controlJob(w, r, model.JobPauseReq, "pause") }
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	s.controlJob(w, r, model.JobResuming, "resume")
}
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.controlJob(w, r, model.JobCancelReq, "cancel")
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
		if job.Status != model.JobPaused {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "job is " + job.Status})
			return
		}
	case "cancel":
		switch job.Status {
		case model.JobCompleted, model.JobCancelled, model.JobFailed, model.JobPartialFailed:
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
	chain := r.URL.Query().Get("chain")
	subject := r.URL.Query().Get("subject")
	ds := r.URL.Query().Get("dataset")
	if chain == "" || subject == "" || ds == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "chain, subject, dataset required"})
		return
	}
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
		"SELECT COALESCE(SUM(downloaded_rows),0), COALESCE(SUM(downloaded_bytes),0), COUNT(*), "+
			"COUNT(*) FILTER (WHERE status='COMPLETED') FROM download_shards WHERE job_id=$1", jobID).
		Scan(&rows, &bytes, &total, &done)
	_ = s.deps.Store.Pool.QueryRow(ctx,
		"SELECT COUNT(*), COALESCE(SUM(estimated_cost_usd),0) FROM provider_usage WHERE job_id=$1", jobID).
		Scan(&reqs, &cost)
	job, err := s.deps.Store.GetJob(ctx, jobID)
	if err != nil {
		return nil
	}
	var progress float32
	if total > 0 {
		progress = float32(done) / float32(total)
	}
	elapsed := time.Since(job.CreatedAt).Seconds()
	var eta float64
	if progress > 0 && progress < 1 && elapsed > 0 {
		eta = elapsed*(1-float64(progress))/float64(progress)
	}
	snap := map[string]any{
		"job_id": jobID, "status": job.Status, "progress": progress,
		"downloaded_rows": rows, "downloaded_bytes": bytes, "api_requests": reqs, "api_cost_usd": cost,
		"total_shards": total, "done_shards": done,
		"elapsed_seconds": elapsed, "eta_seconds": eta, "ts": time.Now(),
	}
	if v, ok := job.Stats["coverage_before"].(float64); ok {
		snap["coverage_hit_ratio"] = v
	}
	return snap
}
