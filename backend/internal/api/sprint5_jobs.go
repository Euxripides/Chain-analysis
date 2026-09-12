package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// ---- U-JOB-01/02 / S5-BE-03（§18.8 / §18.11 / §21.5）----

func (s *Server) handleUserJobsV2(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	limit := parseLimit(r, 20, 100)
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	chainSlug := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chain")))
	dataset := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("dataset")))
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
	workspaceScope := scope == "workspace" && p.Has("job:list:workspace")
	var cursor timeIDCursor
	if err := decodeCursor(r.URL.Query().Get("cursor"), &cursor); err != nil {
		validationError(w, r, "cursor 不合法", []fieldError{{Field: "cursor", Code: "invalid", Message: "请重新加载列表"}})
		return
	}
	var cursorTS *time.Time
	if cursor.TS != "" {
		t, err := time.Parse(time.RFC3339Nano, cursor.TS)
		if err != nil {
			validationError(w, r, "cursor 不合法", nil)
			return
		}
		cursorTS = &t
	}
	if cursor.ID != "" {
		if _, err := uuid.Parse(cursor.ID); err != nil {
			validationError(w, r, "cursor 不合法", nil)
			return
		}
	} else {
		cursor.ID = "00000000-0000-0000-0000-000000000000"
	}
	var datasetFilter any
	if dataset != "" {
		datasetFilter = []string{dataset}
	}
	var statusFilter, chainFilter, sourceFilter any
	if status != "" {
		statusFilter = status
	}
	if chainSlug != "" {
		chainFilter = chainSlug
	}
	if source != "" {
		sourceFilter = source
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text FROM download_jobs
		WHERE organization_id=$1 AND workspace_id=$2
		  AND ($4 OR created_by_identity::text=$3
		       OR created_by = (SELECT legacy_user_id FROM identities WHERE id=NULLIF($3,'')::uuid))
		  AND ($5::text IS NULL OR status=$5)
		  AND ($6::text IS NULL OR chain=$6)
		  AND ($7::text[] IS NULL OR datasets && $7::text[])
		  AND ($8::text IS NULL OR source=$8)
		  AND ($9::timestamptz IS NULL OR (updated_at,id) < ($9::timestamptz,$10::uuid))
		ORDER BY updated_at DESC, id DESC LIMIT $11`,
		p.OrganizationID, p.WorkspaceID, p.IdentityID, workspaceScope,
		statusFilter, chainFilter, datasetFilter, sourceFilter, cursorTS, cursor.ID, limit+1)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取任务列表失败", nil)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	rows.Close()
	next := ""
	if len(ids) > limit {
		ids = ids[:limit]
		if job, err := s.deps.Store.GetJob(r.Context(), ids[len(ids)-1]); err == nil && job != nil {
			next = encodeCursor(timeIDCursor{TS: job.UpdatedAt.UTC().Format(time.RFC3339Nano), ID: job.ID})
		}
	}
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		job, err := s.deps.Store.GetJob(r.Context(), id)
		if err != nil || job == nil {
			continue
		}
		items = append(items, s.jobListView(r, job))
	}
	respondList(w, r, map[string]any{
		"items":   items,
		"scope":   map[string]any{"mode": map[bool]string{true: "workspace", false: "self"}[workspaceScope], "organization_id": p.OrganizationID, "workspace_id": p.WorkspaceID},
		"filters": map[string]any{"status": status, "chain": chainSlug, "dataset": dataset, "source": source},
	}, len(items), &next)
}

func (s *Server) jobListView(r *http.Request, job *model.DownloadJob) map[string]any {
	ctx := r.Context()
	var subjects, subjectsDone, shards, shardsDone, shardsFailed int64
	var downloadedRows, downloadedBytes int64
	var lastSuccess *time.Time
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM download_subjects WHERE job_id=$1),
			(SELECT count(*) FROM download_subjects WHERE job_id=$1 AND status='COMPLETED'),
			(SELECT count(*) FROM download_shards WHERE job_id=$1),
			(SELECT count(*) FROM download_shards WHERE job_id=$1 AND status='COMPLETED'),
			(SELECT count(*) FROM download_shards WHERE job_id=$1 AND status='FAILED'),
			COALESCE((SELECT SUM(downloaded_rows) FROM download_shards WHERE job_id=$1),0),
			COALESCE((SELECT SUM(downloaded_bytes) FROM download_shards WHERE job_id=$1),0),
			(SELECT max(last_success_at) FROM checkpoints WHERE job_id=$1)`, job.ID).
		Scan(&subjects, &subjectsDone, &shards, &shardsDone, &shardsFailed, &downloadedRows, &downloadedBytes, &lastSuccess)
	progress := float64(0)
	if shards > 0 {
		progress = float64(shardsDone) / float64(shards)
	} else if subjects > 0 {
		progress = float64(subjectsDone) / float64(subjects)
	}
	failure := map[string]any{"kind": jobFailureKind(job.Error), "message_safe": safeJobError(job.Error)}
	return map[string]any{
		"id": job.ID, "job_no": job.JobNo, "status": job.Status,
		"chain": job.Chain, "datasets": job.Datasets, "source": job.Source,
		"reuse_policy": job.ReusePolicy, "range_type": job.RangeType,
		"from_block": job.FromBlock, "to_block": job.ToBlock,
		"requested_mode": job.RequestedMode, "effective_mode": job.EffectiveMode, "mode_reason": job.ModeReason,
		"created_at": job.CreatedAt, "updated_at": job.UpdatedAt,
		"progress":          progress,
		"addresses":         map[string]any{"total": subjects, "completed": subjectsDone},
		"datasets_progress": map[string]any{"total": len(job.Datasets)},
		"shards":            map[string]any{"total": shards, "completed": shardsDone, "failed": shardsFailed},
		"downloaded_rows":   downloadedRows, "downloaded_bytes": downloadedBytes,
		"last_success_at": lastSuccess,
		"failure":         failure,
	}
}

// safeJobError 只返回用户可理解摘要；原始 Provider 错误保留在诊断详情（S5-ADMIN-01）。
func safeJobError(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	switch jobFailureKind(raw) {
	case "provider":
		return "Provider 返回错误，请查看任务诊断详情"
	case "timeout":
		return "Provider 请求超时，可重试失败分片"
	case "data_gap":
		return "数据覆盖不完整，请检查 Coverage"
	case "resource":
		return "本地资源不足，任务已停止"
	default:
		return "任务失败，请查看诊断详情"
	}
}

func (s *Server) buildUserJobDetail(r *http.Request, job *model.DownloadJob) map[string]any {
	ctx := r.Context()
	subjects, _ := s.deps.Store.SubjectsByJob(ctx, job.ID)
	if subjects == nil {
		subjects = []model.Subject{}
	}
	datasets := []model.DatasetItem{}
	for _, subj := range subjects {
		items, _ := s.deps.Store.DatasetsBySubject(ctx, subj.ID)
		if items == nil {
			items = []model.DatasetItem{}
		}
		datasets = append(datasets, items...)
	}
	shards, _ := s.deps.Store.ShardsByJob(ctx, job.ID)
	if shards == nil {
		shards = []model.Shard{}
	}
	checkpoints := map[string]map[string]any{}
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT shard_id::text, next_page_token, page_no, downloaded_rows, downloaded_bytes, last_success_at
		FROM checkpoints WHERE job_id=$1`, job.ID)
	if err == nil {
		for rows.Next() {
			var shardID, token string
			var pageNo int
			var dlRows, dlBytes int64
			var lastSuccess *time.Time
			if rows.Scan(&shardID, &token, &pageNo, &dlRows, &dlBytes, &lastSuccess) == nil {
				checkpoints[shardID] = map[string]any{
					"next_page_token": token, "page_no": pageNo,
					"downloaded_rows": dlRows, "downloaded_bytes": dlBytes, "last_success_at": lastSuccess,
				}
			}
		}
		rows.Close()
	}
	addressLayers := make([]map[string]any, 0, len(subjects))
	for i := range subjects {
		layer := map[string]any{
			"subject_id": subjects[i].ID, "address": subjects[i].Subject,
			"status": subjects[i].Status, "progress": subjects[i].Progress,
			"updated_at": subjects[i].UpdatedAt, "stats": subjects[i].Stats,
		}
		var cursorMS *int64
		var cursorAt *time.Time
		_ = s.deps.Store.Pool.QueryRow(ctx, `
			SELECT NULLIF(sh.page_state->>'tron_last_timestamp_ms','')::bigint, cp.last_success_at
			FROM download_shards sh JOIN checkpoints cp ON cp.shard_id=sh.id
			WHERE sh.subject_id=$1 AND sh.page_state ? 'tron_last_timestamp_ms'
			ORDER BY cp.last_success_at DESC LIMIT 1`, subjects[i].ID).Scan(&cursorMS, &cursorAt)
		layer["successful_cursor"] = map[string]any{
			"tron_last_timestamp_ms": cursorMS, "checkpoint_at": cursorAt,
			"definition": "最后一个成功分页游标对应链上时间，不代表计划 Shard 边界。",
		}
		addressLayers = append(addressLayers, layer)
	}
	datasetLayers := make([]map[string]any, 0, len(datasets))
	for _, ds := range datasets {
		dsShards := []model.Shard{}
		for _, sh := range shards {
			if sh.DatasetID == ds.ID {
				dsShards = append(dsShards, sh)
			}
		}
		completed, failed := 0, 0
		for _, sh := range dsShards {
			switch sh.Status {
			case model.ItemCompleted:
				completed++
			case model.ItemFailed:
				failed++
			}
		}
		datasetLayers = append(datasetLayers, map[string]any{
			"dataset_id": ds.ID, "subject_id": ds.SubjectID, "dataset": ds.Dataset,
			"status": ds.Status, "progress": ds.Progress,
			"shards":     map[string]any{"total": len(dsShards), "completed": completed, "failed": failed},
			"updated_at": ds.UpdatedAt,
		})
	}
	shardLayers := make([]map[string]any, 0, len(shards))
	for _, sh := range shards {
		item := map[string]any{
			"shard_id": sh.ID, "subject_id": sh.SubjectID, "dataset": sh.Dataset,
			"from_block": sh.FromBlock, "to_block": sh.ToBlock,
			"status": sh.Status, "retries": sh.Retries, "max_retries": sh.MaxRetries,
			"downloaded_rows": sh.DownloadedRows, "downloaded_bytes": sh.DownloadedBytes,
			"raw_object": sh.RawObject, "error_safe": safeJobError(sh.Error),
			"fetched_at": sh.FetchedAt, "ingested_at": sh.IngestedAt, "validated_at": sh.ValidatedAt,
			"updated_at": sh.UpdatedAt,
		}
		if cp, ok := checkpoints[sh.ID]; ok {
			item["checkpoint"] = cp
		}
		shardLayers = append(shardLayers, item)
	}
	exports := []map[string]any{}
	expRows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT id::text, format, zip, status, rows, sha256, object_ref, error, expires_at, created_at
		FROM exports WHERE job_id=$1 AND organization_id=$2 ORDER BY created_at DESC LIMIT 20`,
		job.ID, PrincipalFrom(ctx).OrganizationID)
	if err == nil {
		for expRows.Next() {
			var id, format, status, sha, objectRef, errMsg string
			var zip bool
			var rowCount int64
			var expiresAt, createdAt *time.Time
			if expRows.Scan(&id, &format, &zip, &status, &rowCount, &sha, &objectRef, &errMsg, &expiresAt, &createdAt) == nil {
				exports = append(exports, map[string]any{
					"id": id, "format": format, "zip": zip, "status": status, "rows": rowCount,
					"sha256": sha, "object_ref": objectRef, "error_safe": safeJobError(errMsg),
					"expires_at": expiresAt, "created_at": createdAt,
				})
			}
		}
		expRows.Close()
	}
	return map[string]any{
		"job":      job,
		"subjects": subjects,
		"datasets": datasets,
		"shards":   shards,
		"progress": s.snapshotFor(ctx, job.ID),
		"performance": map[string]any{
			"requested_mode": job.RequestedMode, "effective_mode": job.EffectiveMode,
			"mode_reason": job.ModeReason,
		},
		"layers": map[string]any{
			"job":       map[string]any{"status": job.Status, "updated_at": job.UpdatedAt, "error_safe": safeJobError(job.Error)},
			"addresses": addressLayers,
			"datasets":  datasetLayers,
			"shards":    shardLayers,
		},
		"exports": exports,
		"source": map[string]any{
			"control": "postgres:download_jobs/subjects/datasets/shards/checkpoints",
			"usage":   "postgres:provider_usage",
			"as_of":   time.Now().UTC().Format(time.RFC3339),
		},
	}
}

func (s *Server) handleUserJobDetailV2(w http.ResponseWriter, r *http.Request) {
	job, ok := s.requireJobScope(r, chi.URLParam(r, "id"))
	if !ok {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "任务不存在或无权访问", nil)
		return
	}
	respondOK(w, r, s.buildUserJobDetail(r, job))
}
