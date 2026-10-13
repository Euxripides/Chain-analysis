package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// ---- U-EXPORT-01 / S5-BE-03（§21.5 / §21.7）----

type exportV2CreateRequest struct {
	JobID               string                  `json:"job_id"`
	Chain               string                  `json:"chain"`
	Addresses           []string                `json:"addresses"`
	Datasets            []string                `json:"datasets"`
	Format              string                  `json:"format"`
	IncludeMeta         bool                    `json:"include_meta"`
	ForceZip            bool                    `json:"force_zip"`
	IncludeDecodedInput bool                    `json:"include_decoded_input"`
	StartTime           *time.Time              `json:"start_time,omitempty"`
	EndTime             *time.Time              `json:"end_time,omitempty"`
	RequireReady        bool                    `json:"require_ready"`
	Content             string                  `json:"content,omitempty"`
	Filters             *query.StatisticsFilter `json:"filters,omitempty"`
	FileLayout          string                  `json:"file_layout,omitempty"`
}

func (s *Server) handleExportV2Create(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		validationError(w, r, "创建导出必须携带 Idempotency-Key", []fieldError{{Field: "Idempotency-Key", Code: "required", Message: "缺少幂等键"}})
		return
	}
	p := PrincipalFrom(r.Context())
	if s.deps.Store == nil || s.deps.Raw == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "导出服务未就绪", nil)
		return
	}
	var req exportV2CreateRequest
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	req.Chain = strings.ToLower(strings.TrimSpace(req.Chain))
	req.Format = strings.ToLower(strings.TrimSpace(req.Format))
	if req.Format == "" {
		req.Format = "csv"
	}
	switch req.Format {
	case "csv", "xlsx", "jsonl", "parquet":
	default:
		validationError(w, r, "format 只接受 csv/xlsx/jsonl/parquet", []fieldError{{Field: "format", Code: "invalid", Message: req.Format}})
		return
	}
	var job *model.DownloadJob
	if req.JobID != "" {
		job, _ = s.requireJobScope(r, req.JobID)
		if job == nil {
			respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "任务不存在或无权导出", nil)
			return
		}
		if job.Status != model.JobCompleted && job.Status != model.JobPaused && job.Status != model.JobPausedFallbackTooExpensive {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "只有完成或暂停的任务可以导出", nil)
			return
		}
		req.Chain = job.Chain
		req.Datasets = append([]string(nil), job.Datasets...)
		subjects, err := s.deps.Store.SubjectsByJob(r.Context(), job.ID)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取任务对象失败", nil)
			return
		}
		req.Addresses = req.Addresses[:0]
		for _, sub := range subjects {
			req.Addresses = append(req.Addresses, sub.Subject)
		}
		// 单地址单 Dataset 默认输出原格式；只有 >3 地址或多 Dataset 才强制 ZIP（§18.10）。
	}
	if req.Chain == "" {
		validationError(w, r, "chain 必填", nil)
		return
	}
	addresses, err := dataset.NormalizeAndDedupeAddressesForChain(req.Chain, req.Addresses)
	if err != nil {
		validationError(w, r, "地址不合法或为空", []fieldError{{Field: "addresses", Code: "invalid", Message: err.Error()}})
		return
	}
	datasets := req.Datasets
	if len(datasets) == 0 {
		datasets = []string{"transactions"}
	}
	for _, d := range datasets {
		if !model.ValidDataset(d) {
			validationError(w, r, "未知 Dataset", []fieldError{{Field: "datasets", Code: "invalid", Message: d}})
			return
		}
	}
	uniqueDatasets := make([]string, 0, len(datasets))
	seenDatasets := map[string]bool{}
	for _, d := range datasets {
		if !seenDatasets[d] {
			seenDatasets[d] = true
			uniqueDatasets = append(uniqueDatasets, d)
		}
	}
	datasets = uniqueDatasets
	if len(addresses) > 100 {
		validationError(w, r, "单次导出最多 100 个地址；更多地址请拆分任务", nil)
		return
	}
	bounds, boundsErr := newExportBounds(req.StartTime, req.EndTime)
	if boundsErr != nil {
		validationError(w, r, boundsErr.Error(), []fieldError{{Field: "start_time", Code: "invalid", Message: boundsErr.Error()}})
		return
	}
	if req.Content != "" && req.Content != "details" && req.Content != "statistics" && req.Content != "counterparties" {
		validationError(w, r, "导出内容不合法", nil)
		return
	}
	if req.FileLayout != "" && req.FileLayout != "dataset" {
		validationError(w, r, "文件组织方式不合法", nil)
		return
	}
	var filterSnapshot *query.StatisticsFilter
	if req.Filters != nil || req.Content == "statistics" || req.Content == "counterparties" {
		filter := query.StatisticsFilter{}
		if req.Filters != nil {
			filter = *req.Filters
		}
		filter.Chain, filter.Addresses, filter.Datasets = req.Chain, addresses, datasets
		for _, value := range []*string{&filter.Counterparty, &filter.FromAddress, &filter.ToAddress} {
			if *value == "" {
				continue
			}
			values, err := dataset.NormalizeAndDedupeAddressesForChain(req.Chain, []string{*value})
			if err != nil || len(values) != 1 {
				validationError(w, r, "导出地址筛选不合法", nil)
				return
			}
			*value = values[0]
		}
		if bounds.From != nil && (filter.FromTime == nil || filter.FromTime.Before(*bounds.From)) {
			filter.FromTime = bounds.From
		}
		if bounds.To != nil && (filter.ToTime == nil || filter.ToTime.After(*bounds.To)) {
			filter.ToTime = bounds.To
		}
		originalDatasets := filter.Datasets
		if req.Content == "" || req.Content == "details" {
			filter.Datasets = []string{"transactions"}
		}
		if err := filter.Normalize(); err != nil {
			validationError(w, r, "导出筛选不合法", []fieldError{{Field: "filters", Code: "invalid", Message: err.Error()}})
			return
		}
		filter.Datasets = originalDatasets
		filterSnapshot = &filter
	}
	zipIt := req.ForceZip || len(addresses) > 3 || len(datasets) > 1
	partial := job != nil && job.Status != model.JobCompleted
	snapshot := buildExportSnapshot(req.Chain, addresses, datasets, req.Format, req.IncludeMeta, zipIt,
		req.IncludeDecodedInput, req.JobID, partial, statusOfJob(job), bounds)
	if req.FileLayout != "" || filterSnapshot != nil || (req.Content != "" && req.Content != "details") {
		snapshotFields := map[string]any{}
		if err := json.Unmarshal(snapshot, &snapshotFields); err != nil {
			respondError(w, r, 500, "INTERNAL_ERROR", "保存导出筛选失败", nil)
			return
		}
		if req.Content != "" && req.Content != "details" {
			snapshotFields["content"] = req.Content
		}
		if filterSnapshot != nil {
			snapshotFields["filters"] = filterSnapshot
		}
		if req.FileLayout != "" {
			snapshotFields["file_layout"] = req.FileLayout
		}
		snapshot, _ = json.Marshal(snapshotFields)
	}
	if job == nil && len(addresses) == 1 && filterSnapshot == nil && req.FileLayout == "" {
		if promoted, ok := s.tryPromotePrefetch(r.Context(), store.PrefetchSpecHash(snapshot),
			p.OrganizationID, p.WorkspaceID, p.IdentityID, strings.Join(datasets, "+"), req.Format, zipIt, snapshot); ok {
			respondOK(w, r, promoted)
			return
		}
	}
	if job != nil {
		var existingID string
		if err := s.deps.Store.Pool.QueryRow(r.Context(), `
			SELECT id::text FROM exports
			WHERE job_id=$1 AND status='COMPLETED' AND format=$2 AND zip=$3
			  AND (query_snapshot=$4::jsonb OR (query_snapshot-'file_layout'=$4::jsonb AND NOT ($4::jsonb ? 'file_layout'))) AND object_ref<>''
			  AND (expires_at IS NULL OR expires_at > now())
			  AND COALESCE(finished_at,updated_at) >= $5
			  AND organization_id=$6 AND workspace_id=$7
			ORDER BY updated_at DESC LIMIT 1`,
			job.ID, req.Format, zipIt, snapshot, job.UpdatedAt, p.OrganizationID, p.WorkspaceID).Scan(&existingID); err == nil {
			if item, ok := s.requireExportScope(r, existingID); ok {
				s.writeAuditNow(r, "export.reuse", "export", "export", existingID, map[string]any{"job_id": job.ID})
				// R10 缺陷修复（2026-09-13）：复用分支原先直接 respondOK(item)，返回的是列表项形状
				// （缺 job_id/addresses/datasets），与创建分支的响应契约不一致，前端弹窗因此显示
				// job_id —、地址数 0、Dataset —。这里在**已通过 org/ws 作用域校验**的 item 之上补齐
				// 创建契约字段；addresses/datasets 取自本次匹配所用的 snapshot（同一作用域，不是猜测，
				// 也不跨 scope），不额外查库、不新增字段来源。
				resp := make(map[string]any, len(item)+6)
				for k, v := range item {
					resp[k] = v
				}
				resp["status"] = existingStatus(item, "COMPLETED")
				resp["format"] = req.Format
				resp["zip"] = zipIt
				resp["job_id"] = req.JobID
				resp["addresses"] = len(addresses)
				resp["datasets"] = datasets
				respondOK(w, r, resp)
				return
			}
		}
	}
	var createdBy *string
	if p.IdentityID != "" {
		u := p.IdentityID
		createdBy = &u
	}
	exp, err := s.deps.Store.CreateExportV2(r.Context(), p.WorkspaceID, p.OrganizationID, createdBy, jobIDPtr(req.JobID),
		strings.Join(datasets, "+"), req.Format, zipIt, snapshot)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "创建导出失败", nil)
		return
	}
	if req.FileLayout == "dataset" || filterSnapshot != nil {
		go s.runDatasetExport(backgroundAuditCtx(r), exp, job, req, addresses, datasets, filterSnapshot, zipIt)
	} else {
		go s.runExportMulti(backgroundAuditCtx(r), exp, job, req.Chain, addresses, datasets, req.Format, req.IncludeMeta, zipIt, req.IncludeDecodedInput, bounds)
	}
	s.writeAuditNow(r, "export.create", "export", "export", exp.ID, map[string]any{
		"job_id": req.JobID, "format": req.Format, "zip": zipIt, "addresses": len(addresses), "datasets": datasets,
	})
	respondCreated(w, r, map[string]any{
		"id": exp.ID, "status": exp.Status, "format": req.Format, "zip": zipIt,
		"job_id": req.JobID, "addresses": len(addresses), "datasets": datasets,
	})
}

// existingStatus 取列表项里的 status，缺失时返回 fallback（复用分支只在 status='COMPLETED' 时命中）。
func existingStatus(item map[string]any, fallback string) any {
	if v, ok := item["status"]; ok && v != nil {
		return v
	}
	return fallback
}

func statusOfJob(job *model.DownloadJob) string {
	if job == nil {
		return ""
	}
	return job.Status
}

func jobIDPtr(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func (s *Server) handleExportsV2List(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	limit := parseLimit(r, 20, 100)
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	formatFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if formatFilter != "" && formatFilter != "csv" && formatFilter != "xlsx" && formatFilter != "jsonl" && formatFilter != "parquet" {
		validationError(w, r, "文件格式不合法", nil)
		return
	}
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len(search) > 160 {
		validationError(w, r, "搜索内容过长", nil)
		return
	}
	var cursor timeIDCursor
	if err := decodeCursor(r.URL.Query().Get("cursor"), &cursor); err != nil {
		validationError(w, r, "cursor 不合法", nil)
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
	if cursor.ID == "" {
		cursor.ID = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, job_id::text, dataset, format, zip, status, rows, sha256, error,
		       object_ref, created_at, updated_at, finished_at, expires_at
		FROM exports
		WHERE organization_id=$1 AND workspace_id=$2
		  AND ($3::text IS NULL OR status=$3)
		  AND ($4::timestamptz IS NULL OR (created_at,id) < ($4::timestamptz,$5::uuid))
		  AND ($7::text IS NULL OR format=$7)
		  AND ($8::text = '' OR strpos(lower(concat(id::text,' ',job_id::text,' ',dataset)), $8)>0
		    OR EXISTS (SELECT 1 FROM download_jobs j WHERE j.id=exports.job_id
		      AND j.organization_id=exports.organization_id AND j.workspace_id=exports.workspace_id
		      AND strpos(lower(j.job_no), $8)>0))
		ORDER BY created_at DESC, id DESC LIMIT $6`,
		p.OrganizationID, p.WorkspaceID, nullableString(status), cursorTS, cursor.ID, limit+1, nullableString(formatFilter), search)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取导出列表失败", nil)
		return
	}
	items := []map[string]any{}
	var lastTS time.Time
	var lastID string
	for rows.Next() {
		var id, datasetName, format, statusValue, sha, errMsg, objectRef string
		var jobID *string
		var zip bool
		var rowCount int64
		var createdAt, updatedAt time.Time
		var finishedAt, expiresAt *time.Time
		if err := rows.Scan(&id, &jobID, &datasetName, &format, &zip, &statusValue, &rowCount, &sha, &errMsg, &objectRef, &createdAt, &updatedAt, &finishedAt, &expiresAt); err != nil {
			rows.Close()
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取导出列表失败", nil)
			return
		}
		var byteSize *int64
		downloadAvailable := statusValue == "COMPLETED" && objectRef != "" &&
			(expiresAt == nil || expiresAt.After(time.Now())) && s.deps.Raw != nil
		if downloadAvailable {
			size, _, statErr := s.statExportObject(r.Context(), objectRef)
			if statErr == nil {
				byteSize = &size
			}
			downloadAvailable = statErr == nil
		}
		lastTS, lastID = createdAt, id
		items = append(items, map[string]any{
			"id": id, "job_id": jobID, "dataset": datasetName, "format": format, "zip": zip,
			"status": statusValue, "rows": rowCount, "bytes": byteSize, "sha256": sha, "error_safe": safeJobError(errMsg),
			"object_ref": objectRef, "created_at": createdAt, "updated_at": updatedAt,
			"finished_at": finishedAt, "expires_at": expiresAt,
			"download_available": downloadAvailable,
		})
	}
	rows.Close()
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		lastTS = last["created_at"].(time.Time)
		lastID = last["id"].(string)
		next = encodeCursor(timeIDCursor{TS: lastTS.UTC().Format(time.RFC3339Nano), ID: lastID})
	}
	respondList(w, r, map[string]any{"items": items,
		"scope": map[string]any{"organization_id": p.OrganizationID, "workspace_id": p.WorkspaceID},
	}, len(items), &next)
}

func (s *Server) handleExportV2Detail(w http.ResponseWriter, r *http.Request) {
	item, ok := s.requireExportScope(r, chi.URLParam(r, "id"))
	if !ok {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "导出不存在或无权访问", nil)
		return
	}
	files, err := s.exportFiles(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, r, 503, "DATA_UNAVAILABLE", "读取文件清单失败", nil)
		return
	}
	item["files"] = s.safeDatasetManifest(r.Context(), files, item["download_available"] == true)
	if size, _, e := s.statExportObject(r.Context(), objectString(item["object_ref"])); e == nil {
		item["bytes"] = size
	}
	respondOK(w, r, item)
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// handleExportV2Ticket 创建 60 秒、单次、绑定用户与作用域的下载票据（§21.7）。
func (s *Server) handleExportV2Ticket(w http.ResponseWriter, r *http.Request) {
	item, ok := s.requireExportScope(r, chi.URLParam(r, "id"))
	if !ok {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "导出不存在或无权访问", nil)
		return
	}
	if item["status"] != "COMPLETED" {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "导出尚未完成", map[string]any{"status": item["status"]})
		return
	}
	if missing, _ := item["object_missing"].(bool); missing {
		s.writeAuditNow(r, "export.download_failed", "export", "export", item["id"].(string),
			map[string]any{"reason": "object_missing", "object_ref": item["object_ref"], "action": "notify_admin"})
		respondError(w, r, http.StatusServiceUnavailable, "DATA_OBJECT_MISSING",
			"导出对象在对象存储中缺失，已禁止创建下载票据，请管理员恢复对象或重新生成", item)
		return
	}
	p := PrincipalFrom(r.Context())
	userID := p.IdentityID
	ticketID, err := s.deps.Store.CreateExportTicket(r.Context(), item["id"].(string), &userID, p.OrganizationID, p.WorkspaceID, 60*time.Second)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "创建下载票据失败", nil)
		return
	}
	s.writeAuditNow(r, "export.download_ticket", "export", "export", item["id"].(string), map[string]any{"ttl_seconds": 60})
	respondOK(w, r, map[string]any{
		"ticket_id": ticketID, "url": "/api/v2/exports/download/" + ticketID,
		"expires_in_seconds": 60, "single_use": true,
	})
}

// handleExportV2Download 用一次性票据换取文件；支持 GET/HEAD 与单 Range（复用 v1 流式实现）。
func (s *Server) handleExportV2Download(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	ticket := chi.URLParam(r, "ticket")
	exportID, err := s.redeemExportTicket(r.Context(), ticket, p, r.Method, r.Header.Get("Range"))
	if err != nil {
		s.writeAuditNow(r, "export.download_failed", "export", "export_ticket", ticket, map[string]any{"reason": "expired_or_used"})
		respondError(w, r, http.StatusGone, "RESOURCE_TICKET_EXPIRED", "下载票据已过期或已使用", nil)
		return
	}
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", exportID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx))
	var fileDataset string
	if err := s.deps.Store.Pool.QueryRow(r.Context(), "SELECT file_dataset FROM export_tickets WHERE id=$1", ticket).Scan(&fileDataset); err != nil {
		respondError(w, r, 500, "INTERNAL_ERROR", "读取下载票据失败", nil)
		return
	}
	if fileDataset != "" {
		files, err := s.exportFiles(r.Context(), exportID)
		if err != nil {
			respondError(w, r, 503, "DATA_UNAVAILABLE", "读取文件清单失败", nil)
			return
		}
		for _, file := range files {
			if file.Dataset == fileDataset {
				exp, err := s.deps.Store.GetExport(r.Context(), exportID)
				if err != nil || exp == nil {
					respondError(w, r, 404, "RESOURCE_NOT_FOUND", "导出不存在", nil)
					return
				}
				exp.ObjectRef, exp.SHA256, exp.Rows, exp.Zip = file.ObjectRef, file.SHA256, file.Rows, false
				s.serveExportObject(w, r, exp, map[string]any{"ticket": true, "dataset": file.Dataset, "filename": file.Name})
				return
			}
		}
		respondError(w, r, 404, "RESOURCE_NOT_FOUND", "数据集文件不存在", nil)
		return
	}
	// DATA-008：对象缺失时不得宣称下载成功，留下可操作的审计线索。
	// F-R10-BUCKET：对象可能在旧桶（未迁移/已回滚）或新桶，DATA-008 检查必须双读。
	if s.deps.Raw != nil {
		var objectRef string
		if err := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT object_ref FROM exports WHERE id=$1`, exportID).Scan(&objectRef); err == nil && objectRef != "" {
			if _, _, statErr := s.statExportObject(r.Context(), objectRef); statErr != nil {
				s.writeAuditNow(r, "export.download_failed", "export", "export", exportID,
					map[string]any{"reason": "object_missing", "object_ref": objectRef, "action": "notify_admin", "ticket": true})
				respondError(w, r, http.StatusServiceUnavailable, "DATA_OBJECT_MISSING",
					"导出对象在对象存储中缺失，请管理员检查 MinIO 与导出记录", map[string]any{"export_id": exportID, "object_ref": objectRef})
				return
			}
		}
	}
	// ADR-019 §3.4 + D-R10V9-01：成功下载审计统一由 serveExportObject 写（direct 与 ticket 同 action、
	// 同字段口径：object_bucket=真实 MinIO 桶名、object_bucket_role=逻辑角色、附 ticket=true），
	// 这里不再单独写第二条 —— 避免同一动作出现两套不可统一查询的记录。
	s.handleDownloadExportTicket(w, r)
}

func (s *Server) redeemExportTicket(ctx context.Context, ticket string, p policy.Principal, method, byteRange string) (string, error) {
	var exportID string
	var err error
	if method == http.MethodHead {
		err = s.deps.Store.Pool.QueryRow(ctx, `
			SELECT e.id::text FROM export_tickets t JOIN exports e ON e.id=t.export_id
			WHERE t.id=$1 AND t.user_id=$2 AND t.organization_id=$3 AND t.workspace_id=$4
			  AND t.expires_at>now() AND (t.used_at IS NULL OR t.used_by=$2)
			  AND e.status='COMPLETED' AND (e.expires_at IS NULL OR e.expires_at>now())`,
			ticket, p.IdentityID, p.OrganizationID, p.WorkspaceID).Scan(&exportID)
	} else {
		err = s.deps.Store.Pool.QueryRow(ctx, `
			UPDATE export_tickets AS t SET used_at=now(), used_by=$2,
			  expires_at=LEAST(now()+interval '24 hours',COALESCE(e.expires_at,now()+interval '24 hours'))
			FROM exports AS e WHERE t.id=$1 AND e.id=t.export_id
			  AND t.user_id=$2 AND t.organization_id=$3 AND t.workspace_id=$4
			  AND t.used_at IS NULL AND t.expires_at>now()
			  AND e.status='COMPLETED' AND (e.expires_at IS NULL OR e.expires_at>now())
			RETURNING e.id::text`,
			ticket, p.IdentityID, p.OrganizationID, p.WorkspaceID).Scan(&exportID)
		if errors.Is(err, pgx.ErrNoRows) && byteRange != "" {
			err = s.deps.Store.Pool.QueryRow(ctx, `
				SELECT e.id::text FROM export_tickets t JOIN exports e ON e.id=t.export_id
				WHERE t.id=$1 AND t.user_id=$2 AND t.used_by=$2
				  AND t.organization_id=$3 AND t.workspace_id=$4
				  AND t.used_at IS NOT NULL AND t.expires_at>now()
				  AND e.status='COMPLETED' AND (e.expires_at IS NULL OR e.expires_at>now())`,
				ticket, p.IdentityID, p.OrganizationID, p.WorkspaceID).Scan(&exportID)
		}
	}
	return exportID, err
}
