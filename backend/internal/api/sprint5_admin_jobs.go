package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// ---- A-JOB-01/02 / S5-ADMIN-01（§19.6 / §21.6）----

func (s *Server) handleAdminJobsList(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	limit := parseLimit(r, 20, 100)
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	chainSlug := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chain")))
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	wsID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
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
		SELECT id::text FROM download_jobs
		WHERE ($1::text IS NULL OR status=$1)
		  AND ($2::text IS NULL OR chain=$2)
		  AND ($3::uuid IS NULL OR organization_id=$3)
		  AND ($4::uuid IS NULL OR workspace_id=$4)
		  AND ($5::timestamptz IS NULL OR (updated_at,id) < ($5::timestamptz,$6::uuid))
		ORDER BY updated_at DESC, id DESC LIMIT $7`,
		nullableString(status), nullableString(chainSlug), nullableString(orgID), nullableString(wsID),
		cursorTS, cursor.ID, limit+1)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取全局任务失败", nil)
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
	items := []map[string]any{}
	for _, id := range ids {
		job, err := s.deps.Store.GetJob(r.Context(), id)
		if err != nil || job == nil {
			continue
		}
		view := s.jobListView(r, job)
		var org, ws *string
		_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT organization_id::text, workspace_id::text FROM download_jobs WHERE id=$1`, id).Scan(&org, &ws)
		view["organization_id"] = org
		view["workspace_id"] = ws
		view["diagnostics_url"] = "/api/admin/v1/jobs/" + id + "/diagnostics"
		items = append(items, view)
	}
	respondList(w, r, map[string]any{"items": items}, len(items), &next)
}

func (s *Server) handleAdminJobDiagnostics(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	id := chi.URLParam(r, "id")
	job, err := s.deps.Store.GetJob(r.Context(), id)
	if err != nil || job == nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "任务不存在", nil)
		return
	}
	chain := []map[string]any{}
	jobLayer := s.jobListView(r, job)
	jobLayer["layer"] = "DownloadJob"
	chain = append(chain, jobLayer)

	subjects, _ := s.deps.Store.SubjectsByJob(r.Context(), id)
	subjectLayer := []map[string]any{}
	for _, sub := range subjects {
		item := map[string]any{"id": sub.ID, "layer": "SubjectJob", "subject": sub.Subject, "status": sub.Status, "progress": sub.Progress, "updated_at": sub.UpdatedAt}
		var cursorMS *int64
		var cursorAt *time.Time
		_ = s.deps.Store.Pool.QueryRow(r.Context(), `
			SELECT NULLIF(sh.page_state->>'tron_last_timestamp_ms','')::bigint, cp.last_success_at
			FROM download_shards sh JOIN checkpoints cp ON cp.shard_id=sh.id
			WHERE sh.subject_id=$1 AND sh.page_state ? 'tron_last_timestamp_ms'
			ORDER BY cp.last_success_at DESC LIMIT 1`, sub.ID).Scan(&cursorMS, &cursorAt)
		item["advance_to_chain_time"] = map[string]any{"tron_last_timestamp_ms": cursorMS, "checkpoint_at": cursorAt}
		subjectLayer = append(subjectLayer, item)
	}
	chain = append(chain, map[string]any{"layer": "SubjectJob", "items": subjectLayer})

	datasets := []map[string]any{}
	for _, sub := range subjects {
		items, _ := s.deps.Store.DatasetsBySubject(r.Context(), sub.ID)
		for _, ds := range items {
			datasets = append(datasets, map[string]any{"id": ds.ID, "layer": "DatasetJob", "subject_id": ds.SubjectID, "dataset": ds.Dataset, "status": ds.Status, "progress": ds.Progress, "updated_at": ds.UpdatedAt})
		}
	}
	chain = append(chain, map[string]any{"layer": "DatasetJob", "items": datasets})

	shards, _ := s.deps.Store.ShardsByJob(r.Context(), id)
	shardItems := []map[string]any{}
	rawItems := []map[string]any{}
	for _, sh := range shards {
		entry := map[string]any{
			"id": sh.ID, "layer": "ShardCheckpoint", "dataset": sh.Dataset, "subject_id": sh.SubjectID,
			"from_block": sh.FromBlock, "to_block": sh.ToBlock, "status": sh.Status,
			"retries": sh.Retries, "error": maskProviderError(sh.Error), "raw_object": sh.RawObject,
			"downloaded_rows": sh.DownloadedRows, "downloaded_bytes": sh.DownloadedBytes,
		}
		var token string
		var pageNo int
		var lastSuccess *time.Time
		if err := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT next_page_token,page_no,last_success_at FROM checkpoints WHERE shard_id=$1`, sh.ID).Scan(&token, &pageNo, &lastSuccess); err == nil {
			entry["checkpoint"] = map[string]any{"next_page_token": token, "page_no": pageNo, "last_success_at": lastSuccess}
		}
		shardItems = append(shardItems, entry)
		if sh.RawObject != "" {
			rawItems = append(rawItems, s.rawObjectEvidence(r.Context(), sh.ID, sh.RawObject))
		}
	}
	chain = append(chain, map[string]any{"layer": "ShardCheckpoint", "items": shardItems})
	chain = append(chain, map[string]any{"layer": "RawObject", "items": rawItems})
	chain = append(chain, s.providerUsageLayer(r.Context(), id))
	chain = append(chain, s.clickhouseCoverageLayer(r.Context(), job, subjects, datasets))
	respondOK(w, r, map[string]any{
		"job_id": id,
		"chain":  chain,
		"fixed_chain": []string{"DownloadJob", "SubjectJob", "DatasetJob", "ShardCheckpoint",
			"ProviderUsage", "RawObject", "ClickHouseCoverage"},
		"as_of": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) rawObjectEvidence(ctx context.Context, shardID, key string) map[string]any {
	item := map[string]any{"layer": "RawObject", "shard_id": shardID, "object_ref": key, "exists": false}
	if s.deps.Raw == nil {
		item["note"] = "rawstore disabled"
		return item
	}
	size, err := s.deps.Raw.Stat(ctx, key)
	if err != nil {
		item["note"] = "object missing or unreadable"
		return item
	}
	item["exists"] = true
	item["size_bytes"] = size
	// 大对象不阻塞诊断；明确标注跳过原因，不虚报 hash。
	if size > 8*1024*1024 {
		item["sha256"] = nil
		item["sha256_note"] = "object larger than 8MB; hash skipped in diagnostics"
		return item
	}
	reader, _, err := s.deps.Raw.Open(ctx, key)
	if err != nil {
		item["sha256"] = nil
		item["sha256_note"] = "object unreadable"
		return item
	}
	defer reader.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(reader, 8*1024*1024+1)); err != nil {
		item["sha256"] = nil
		item["sha256_note"] = "hash read failed"
		return item
	}
	item["sha256"] = hex.EncodeToString(h.Sum(nil))
	return item
}

func (s *Server) providerUsageLayer(ctx context.Context, jobID string) map[string]any {
	items := []map[string]any{}
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT id::text, provider, method, dataset, request_time, duration_ms, status, http_status,
		       api_error, rows, bytes, credits::float8, estimated_cost_usd::float8,
		       COALESCE(trace_id,''), COALESCE(request_id,''), retry_attempt, cache_hit, singleflight_hit
		FROM provider_usage WHERE job_id=$1 ORDER BY request_time DESC LIMIT 100`, jobID)
	if err == nil {
		for rows.Next() {
			var id, provider, methodName, dataset, status, apiError, traceID, requestID string
			var requestTime time.Time
			var duration, rowsN, bytes int64
			var httpStatus, retry int
			var credits, cost float64
			var cacheHit, singleflight bool
			if rows.Scan(&id, &provider, &methodName, &dataset, &requestTime, &duration, &status, &httpStatus,
				&apiError, &rowsN, &bytes, &credits, &cost, &traceID, &requestID, &retry, &cacheHit, &singleflight) == nil {
				items = append(items, map[string]any{
					"id": id, "layer": "ProviderUsage", "provider": provider, "method": methodName, "dataset": dataset,
					"request_time": requestTime, "duration_ms": duration, "status": status, "http_status": httpStatus,
					"error": maskProviderError(apiError), "rows": rowsN, "bytes": bytes, "credits": credits,
					"estimated_cost_usd": cost, "trace_id": traceID, "request_id": requestID,
					"retry_attempt": retry, "cache_hit": cacheHit, "singleflight_hit": singleflight,
				})
			}
		}
		rows.Close()
	}
	return map[string]any{"layer": "ProviderUsage", "items": items}
}

func (s *Server) clickhouseCoverageLayer(ctx context.Context, job *model.DownloadJob, subjects []model.Subject, datasets []map[string]any) map[string]any {
	items := []map[string]any{}
	if s.deps.CH == nil {
		return map[string]any{"layer": "ClickHouseCoverage", "items": items, "note": "clickhouse disabled"}
	}
	// 交互路径的扫描预算：单条 count 再慢也只吃自己的额度，不会把 60 s 的请求预算打满。
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	seen := map[string]bool{}
	for _, sub := range subjects {
		for _, ds := range datasets {
			datasetName, _ := ds["dataset"].(string)
			if datasetName == "" {
				continue
			}
			key := sub.Subject + "|" + datasetName
			if seen[key] {
				continue
			}
			seen[key] = true
			count := int64(-1)
			// 不用 FINAL：ReplacingMergeTree 的 FINAL 会在**交互路径上做全表归并**。
			// 实测（2026-09-16，bsc token_transfers，单个地址）：FINAL 18.9 s，显式去重 0.30 s，
			// 两者计数完全一致（5426）。诊断链按 地址×数据集 逐个查，5 个地址就能把
			// 60 s 的请求预算打满（该端点因此恒定 60004 ms 并触发路由超时中间件）。
			// 去重键与 ORDER BY (chain, tx_hash, log_index, token_address) 对齐，
			// 与 query.ActionFlowSummary 采用同一形态。
			// count() 的类型是 UInt64：clickhouse-go v2 **拒绝**把 UInt64 扫进 *int64
			// （"converting UInt64 to *int64 is unsupported"）。此前这里直接扫 &count(int64)，
			// 错误被 `_ =` 吞掉，于是 clickhouse_rows 永远是 -1 —— 一层**永远没有数据**的
			// "真实数据"页面（2026-09-16 定位）。改成先扫 uint64 再转换。
			var rows uint64
			if datasetName == "token_transfers" {
				if err := s.deps.CH.DB().QueryRow(ctx, `SELECT count() FROM (
					SELECT tx_hash, log_index, token_address FROM blockchain.token_transfers
					 WHERE chain=? AND (from_address=? OR to_address=?)
					 GROUP BY tx_hash, log_index, token_address)`, job.Chain, sub.Subject, sub.Subject).Scan(&rows); err == nil {
					count = int64(rows)
				} else if s.deps.Log != nil {
					s.deps.Log.Warn("diagnostics clickhouse count failed", "err", err, "chain", job.Chain, "subject", sub.Subject)
				}
			} else if datasetName == "transactions" {
				if err := s.deps.CH.DB().QueryRow(ctx, `SELECT count() FROM (
					SELECT tx_hash FROM blockchain.transactions
					 WHERE chain=? AND (from_address=? OR to_address=?)
					 GROUP BY tx_hash)`, job.Chain, sub.Subject, sub.Subject).Scan(&rows); err == nil {
					count = int64(rows)
				} else if s.deps.Log != nil {
					s.deps.Log.Warn("diagnostics clickhouse count failed", "err", err, "chain", job.Chain, "subject", sub.Subject)
				}
			}
			var fromBlock, toBlock *int64
			var updated *time.Time
			_ = s.deps.Store.Pool.QueryRow(ctx, `
				SELECT min(from_block), max(to_block), max(updated_at) FROM dataset_coverage
				WHERE chain=$1 AND subject=$2 AND dataset=$3`, job.Chain, sub.Subject, datasetName).Scan(&fromBlock, &toBlock, &updated)
			item := map[string]any{"layer": "ClickHouseCoverage", "subject": sub.Subject, "dataset": datasetName,
				"clickhouse_rows": count, "coverage_from_block": fromBlock, "coverage_to_block": toBlock, "coverage_updated_at": updated}
			if count >= 0 {
				item["coverage_consistent"] = count > 0
			}
			items = append(items, item)
		}
	}
	return map[string]any{"layer": "ClickHouseCoverage", "items": items}
}

// maskProviderError 保留错误分类但屏蔽疑似凭证/URL 查询参数（§19.6）。
func maskProviderError(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	replacer := strings.NewReplacer("apikey=", "apikey=***", "api_key=", "api_key=***", "token=", "token=***", "password=", "password=***")
	return replacer.Replace(raw)
}

func (s *Server) handleAdminJobCommand(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	id := chi.URLParam(r, "id")
	job, err := s.deps.Store.GetJob(r.Context(), id)
	if err != nil || job == nil {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "任务不存在", nil)
		return
	}
	command := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "command")))
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			validationError(w, r, "请求体不是合法 JSON", nil)
			return
		}
	}
	if strings.TrimSpace(body.Reason) == "" {
		validationError(w, r, "管理控制必须填写原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	switch command {
	case "pause", "resume", "cancel":
		if !PrincipalFrom(r.Context()).Has("admin:job:control") {
			respondError(w, r, http.StatusForbidden, "AUTHZ_FORBIDDEN", "缺少 admin:job:control", nil)
			return
		}
		if err := s.validateJobTransition(job, command); err != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", err.Error(), nil)
			return
		}
		target := model.JobPauseReq
		if command == "resume" {
			target = model.JobResuming
		} else if command == "cancel" {
			target = model.JobCancelReq
		}
		if err := s.deps.Store.SetJobStatus(r.Context(), id, target); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "任务状态更新失败", nil)
			return
		}
		s.writeAuditNow(r, "admin.job."+command, "job", "download_job", id, map[string]any{"reason": body.Reason, "target": target})
		s.emitJobEvent(r.Context(), id, "admin_control", map[string]any{"command": command, "status": target, "reason": body.Reason, "actor": PrincipalFrom(r.Context()).IdentityID})
		respondOK(w, r, map[string]any{"job_id": id, "status": target, "command": command})
	case "retry":
		if !PrincipalFrom(r.Context()).Has("admin:job:retry") {
			respondError(w, r, http.StatusForbidden, "AUTHZ_FORBIDDEN", "缺少 admin:job:retry", nil)
			return
		}
		reset, err := s.deps.Store.RetryJobFailures(r.Context(), id)
		if err != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", err.Error(), nil)
			return
		}
		s.writeAuditNow(r, "admin.job.retry", "job", "download_job", id, map[string]any{"reason": body.Reason, "reset_shards": reset})
		s.emitJobEvent(r.Context(), id, "retry", map[string]any{"status": model.JobQueued, "reset_shards": reset, "reason": body.Reason})
		respondOK(w, r, map[string]any{"job_id": id, "status": model.JobQueued, "command": "retry", "reset_shards": reset})
	default:
		validationError(w, r, "不支持的任务命令", nil)
	}
}

var _ = uuid.Nil
