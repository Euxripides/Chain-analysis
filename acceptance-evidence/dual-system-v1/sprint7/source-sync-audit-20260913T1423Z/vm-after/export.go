package api

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/xuri/excelize/v2"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
)

const maxExportRows = 2_000_000

func (s *Server) handleCreateExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JobID               string   `json:"job_id"`
		Dataset             string   `json:"dataset"`
		Format              string   `json:"format"`
		Subject             string   `json:"subject"`
		Chain               string   `json:"chain"`
		Addresses           []string `json:"addresses"`
		Datasets            []string `json:"datasets"`
		IncludeMeta         bool     `json:"include_meta"`
		ForceZip            bool     `json:"force_zip"`
		IncludeDecodedInput bool     `json:"include_decoded_input"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// 租户/操作者作用域由服务端注入（§21.2），绝不接受客户端提交。
	scope, ok := s.tenantScopeOr401(w, r)
	if !ok {
		return
	}
	wsID, wsErr := s.writableWorkspace(r.Context(), scope)
	if wsErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": wsErr.Error()})
		return
	}
	actor := PrincipalFrom(r.Context())
	// V1.2：多地址导出走异步 ZIP 管线（§47-§53）
	if len(req.Addresses) > 0 || len(req.Datasets) > 0 {
		s.createExportV2(w, r, exportV2Request{
			JobID: req.JobID, Dataset: req.Dataset, Format: req.Format,
			Subject: req.Subject, Chain: req.Chain, Addresses: req.Addresses,
			Datasets: req.Datasets, IncludeMeta: req.IncludeMeta, ForceZip: req.ForceZip,
			IncludeDecodedInput: req.IncludeDecodedInput,
			OrgID:               scope.OrganizationID, WSID: wsID, UserID: actor.IdentityID,
		})
		return
	}
	if !model.ValidDataset(req.Dataset) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown dataset " + req.Dataset})
		return
	}
	switch req.Format {
	case "csv", "xlsx", "json":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "format must be csv, xlsx or json"})
		return
	}
	var jobID *string
	if req.JobID != "" {
		jobID = &req.JobID
	}
	exp, err := s.deps.Store.CreateExport(r.Context(), jobID, req.Dataset, req.Format)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	chain := strings.ToLower(strings.TrimSpace(req.Chain))
	subject := strings.TrimSpace(req.Subject)
	if chain != "tron" {
		subject = strings.ToLower(subject)
	}
	go s.runExport(backgroundAuditCtx(r), exp, subject, chain)
	writeJSON(w, http.StatusAccepted, exp)
}

func (s *Server) handleGetExport(w http.ResponseWriter, r *http.Request) {
	// 导出对象带租户归属（exports.organization_id / workspace_id）：跨租户统一 404。
	if !s.scopedObject(w, r, "export", chi.URLParam(r, "id")) {
		return
	}
	exp, err := s.deps.Store.GetExport(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if exp == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "export not found"})
		return
	}
	writeJSON(w, http.StatusOK, exp)
}

// handleExportDownloadScoped 是 /exports/{id}/download 的认证入口。
//
// 对象级授权必须放在这里而不是 handleDownloadExport 内部：后者同时被
// /api/v1/download/{ticket} 的票据路径复用（票据本身就是短时能力令牌，
// 没有 session 主体），在内部加作用域检查会把票据链路一起打断。
func (s *Server) handleExportDownloadScoped(w http.ResponseWriter, r *http.Request) {
	if !s.scopedObject(w, r, "export", chi.URLParam(r, "id")) {
		return
	}
	s.handleDownloadExport(w, r)
}

// auditRequestDetached 返回一个"与客户端断连解耦"的请求：context 保留原请求的值
// （principal / meta / origin / request_id / trace_id），但**不会**因为客户端关闭连接而被取消。
//
// 真实缺陷（2026-09-13 生产日志实测）：全量导出下载的成功审计在 io.CopyBuffer 返回之后才写，
// 此时客户端已收完字节并关闭连接，r.Context() 已取消，审计 INSERT 直接失败：
//
//	{"level":"ERROR","msg":"audit write failed","action":"export.download","err":"context canceled"}
//
// 结果是下载成功但审计链上**没有这一条记录**；Range（响应体很小）与 HEAD（无响应体）不受影响，
// 所以只看 httptest 的旧证据完全看不出来。票据下载路径复用同一个 serveExportObject，同样丢审计。
// 审计写入必须反映"这次下载确实发生了"，而不是"连接还活着"。
func auditRequestDetached(r *http.Request) *http.Request {
	return r.WithContext(context.WithoutCancel(r.Context()))
}

// auditExportDownloadFailed：DATA-008 语义——对象在所有读桶中都不存在时，绝不宣称成功，
// 且必须留下可操作线索（与 sprint5_exports.go 的票据路径同一口径）。
func (s *Server) auditExportDownloadFailed(r *http.Request, exp *model.Export, path string) {
	s.writeAuditNow(r, "export.download_failed", "export", "export", exp.ID,
		map[string]any{"reason": "object_missing", "object_ref": exp.ObjectRef, "path": path, "action": "notify_admin"})
}

func (s *Server) handleDownloadExport(w http.ResponseWriter, r *http.Request) {
	exp, err := s.deps.Store.GetExport(r.Context(), chi.URLParam(r, "id"))
	if err != nil || exp == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "export not found"})
		return
	}
	s.serveExportObject(w, r, exp, nil)
}

// handleDownloadExportTicket 供 /api/v1/download/{ticket} 的一次性票据路径复用；
// 与直接认证路径共用同一个 serveExportObject，只在成功审计上附加 ticket=true 标记，
// 保证两条路径的 action 与字段口径完全一致（D-R10V9-01）。
func (s *Server) handleDownloadExportTicket(w http.ResponseWriter, r *http.Request) {
	exp, err := s.deps.Store.GetExport(r.Context(), chi.URLParam(r, "id"))
	if err != nil || exp == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "export not found"})
		return
	}
	s.serveExportObject(w, r, exp, map[string]any{"ticket": true})
}

// serveExportObject 提供导出对象的流式下载（全量 GET / Range GET / HEAD）。
//
// D-R10V9-01（本轮实测发现的真实验收缺陷）：直接路径 /api/v1/exports/{id}/download 原先
// **只在失败时**写审计，成功的全量/Range/HEAD 没有任何服务端业务审计，导致"谁在何时下载了
// 哪个对象、命中哪个真实桶"在 v2 审计链上不可查询；而 ticket 路径有审计 → 两套不可查询口径。
// 现在三条路径统一写 action=export.download，字段口径与读/写 helper 的 auditFields 一致：
//
//	object_bucket      = **真实 MinIO 桶名**（loc.Name，如 blockchain-exports）
//	object_bucket_role = 逻辑角色（"raw" / "export"）
//
// 成功审计的时机（不虚报）：
//   - HEAD：响应头成功形成后即可审计（无响应体，不存在流失败）；
//   - GET / Range：只有流式拷贝**完整结束**（io.Copy 无错且写出字节数 == 期望长度）才写成功；
//     客户端中断 / 写端错误写 export.download_failed（reason=client_abort|stream_failed）+ bytes_written。
//
// extra 供调用方附加来源标记（如 ticket=true），nil 表示直接认证路径。
func (s *Server) serveExportObject(w http.ResponseWriter, r *http.Request, exp *model.Export, extra map[string]any) {
	if exp.Status != "COMPLETED" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "export not ready: " + exp.Status})
		return
	}
	// 经 API 流式转发（MinIO 只绑内网，避免暴露存储端口）；支持 HTTP Range 断点续传（§57）
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable, no-transform")
	if exp.SHA256 != "" {
		w.Header().Set("ETag", `"`+exp.SHA256+`"`)
	}
	ct := "application/octet-stream"
	switch exp.Format {
	case "csv":
		ct = "text/csv"
	case "xlsx":
		ct = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "json", "jsonl":
		ct = "application/x-ndjson"
	case "parquet":
		ct = "application/vnd.apache.parquet"
	}
	if strings.HasPrefix(exp.Format, "transactions") || strings.Contains(exp.ObjectRef, ".zip") {
		ct = "application/zip"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=export-%s.%s", exp.ID[:8], extFor(exp.Format)))
	if strings.Contains(exp.ObjectRef, ".zip") {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=export-%s.zip", exp.ID[:8]))
	}

	// 单区间 Range：bytes=start-[end]
	if rng := r.Header.Get("Range"); rng != "" {
		var start, end int64 = -1, -1
		if strings.Contains(rng, ",") {
			writeJSON(w, http.StatusRequestedRangeNotSatisfiable, map[string]any{"error": "multiple ranges are not supported"})
			return
		}
		if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil || start < 0 {
			if _, err2 := fmt.Sscanf(rng, "bytes=%d-", &start); err2 != nil || start < 0 {
				writeJSON(w, http.StatusRequestedRangeNotSatisfiable, map[string]any{"error": "bad range"})
				return
			}
		}
		// F-R10-BUCKET：Range 也走双读（迁移前后对象可能在新桶）。
		reader, size, loc, err := s.openExportObjectRange(r.Context(), exp.ObjectRef, start, end)
		if err != nil {
			switch {
			case errors.Is(err, rawstore.ErrInvalidRange):
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
				writeJSON(w, http.StatusRequestedRangeNotSatisfiable, map[string]any{"error": err.Error()})
			case isExportObjectMissing(err):
				s.auditExportDownloadFailed(auditRequestDetached(r), exp, "range")
				respondError(w, r, http.StatusServiceUnavailable, "DATA_OBJECT_MISSING",
					"导出对象在对象存储中缺失，请管理员检查 MinIO 与导出记录",
					map[string]any{"export_id": exp.ID, "object_ref": exp.ObjectRef})
			default:
				// 非对象级缺失（桶不存在/权限/网络/5xx）：fail closed 且必须留可操作审计线索。
				s.auditExportStorageFault(auditRequestDetached(r), exp, "range", err)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			}
			return
		}
		defer reader.Close()
		if end < 0 || end >= size {
			end = size - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		buf := make([]byte, 1024*1024)
		want := end - start + 1
		written, copyErr := io.CopyBuffer(w, reader, buf)
		if copyErr != nil {
			s.deps.Log.Error("export range stream failed", "export_id", exp.ID, "err", copyErr)
		}
		s.auditExportStreamResult(auditRequestDetached(r), exp, loc, "range", want, written, copyErr, extra)
		return
	}

	reader, size, loc, err := s.openExportObject(r.Context(), exp.ObjectRef)
	if err != nil {
		if isExportObjectMissing(err) {
			s.auditExportDownloadFailed(auditRequestDetached(r), exp, "download")
			respondError(w, r, http.StatusServiceUnavailable, "DATA_OBJECT_MISSING",
				"导出对象在对象存储中缺失，请管理员检查 MinIO 与导出记录",
				map[string]any{"export_id": exp.ID, "object_ref": exp.ObjectRef})
			return
		}
		// 非对象级缺失（桶不存在/权限/网络/5xx）：fail closed 且必须留可操作审计线索。
		s.auditExportStorageFault(auditRequestDetached(r), exp, "download", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Length", fmt.Sprint(size))
	if r.Method == http.MethodHead {
		// HEAD 无响应体：头成功形成即代表该对象可读，可写成功审计。
		s.auditExportDownloadSucceeded(auditRequestDetached(r), exp, loc, "head", size, extra)
		return
	}
	buf := make([]byte, 1024*1024)
	written, copyErr := io.CopyBuffer(w, reader, buf)
	if copyErr != nil {
		s.deps.Log.Error("export stream failed", "export_id", exp.ID, "err", copyErr)
	}
	s.auditExportStreamResult(auditRequestDetached(r), exp, loc, "full", size, written, copyErr, extra)
}

// auditExportStorageFault 记录"非对象级缺失"的存储故障（桶不存在 / 权限 / 网络 / 5xx）。
//
// 这类故障必须 fail closed（绝不回退到 RAW、绝不伪装成对象缺失），同时也必须留下与
// DATA-008 同级的可操作线索：旧实现在这条分支上只返回 500、不写任何业务审计，
// 导致"桶被误配/被删"在审计链上完全不可见（本轮实测并由根复核确认的缺陷）。
// 注意：此处拿不到命中桶（打开就已失败），因此只记录 object_ref，不记录 object_bucket。
func (s *Server) auditExportStorageFault(r *http.Request, exp *model.Export, path string, openErr error) {
	reason := "storage_fault"
	var bm bucketMissingError
	if errors.As(openErr, &bm) {
		reason = "bucket_missing"
	}
	detail := map[string]any{
		"reason":     reason,
		"export_id":  exp.ID,
		"object_ref": exp.ObjectRef,
		"path":       path,
		"action":     "notify_admin",
	}
	if openErr != nil {
		detail["storage_error"] = openErr.Error()
	}
	s.writeAuditNow(r, "export.download_failed", "export", "export", exp.ID, detail)
}

// auditExportDownloadSucceeded 写一条成功的下载业务审计（真实桶名 + 逻辑角色 + 对象键 + 字节数）。
func (s *Server) auditExportDownloadSucceeded(r *http.Request, exp *model.Export, loc exportLocation, path string, bytes int64, extra map[string]any) {
	detail := map[string]any{
		"export_id":          exp.ID,
		"object_ref":         loc.Key,
		"object_bucket":      loc.Name,
		"object_bucket_role": string(loc.Bucket),
		"bytes":              bytes,
		"method":             r.Method,
		"path":               path,
	}
	for k, v := range extra {
		detail[k] = v
	}
	s.writeAuditNow(r, "export.download", "export", "export", exp.ID, detail)
}

// auditExportStreamResult 只在流**完整结束**时写成功；否则写明确的失败审计并保留已写字节数。
// 绝不因为对象打开成功就把一次被中断的传输记成"下载成功"。
func (s *Server) auditExportStreamResult(r *http.Request, exp *model.Export, loc exportLocation, path string, want, written int64, copyErr error, extra map[string]any) {
	if copyErr == nil && written == want {
		s.auditExportDownloadSucceeded(r, exp, loc, path, written, extra)
		return
	}
	reason := "stream_failed"
	if isClientAbort(copyErr) {
		reason = "client_abort"
	}
	detail := map[string]any{
		"reason":             reason,
		"export_id":          exp.ID,
		"object_ref":         loc.Key,
		"object_bucket":      loc.Name,
		"object_bucket_role": string(loc.Bucket),
		"bytes_written":      written,
		"bytes_expected":     want,
		"method":             r.Method,
		"path":               path,
		"action":             "notify_admin",
	}
	if copyErr != nil {
		detail["stream_error"] = copyErr.Error()
	}
	for k, v := range extra {
		detail[k] = v
	}
	s.writeAuditNow(r, "export.download_failed", "export", "export", exp.ID, detail)
}

// isClientAbort 区分"客户端主动断开"与"服务端流式写失败"，两者都不得记成功，但线索不同。
func isClientAbort(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"broken pipe", "connection reset", "client disconnected", "unexpected eof"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// runExport 流式导出：查询 → 流式写入 MinIO（不把结果集全部装入内存）。
func (s *Server) runExport(ctx context.Context, exp *model.Export, subject, chain string) {
	_ = s.deps.Store.UpdateExport(ctx, exp.ID, "RUNNING", "", "", 0, "")
	var job *model.DownloadJob
	if exp.JobID != nil {
		var err error
		job, err = s.deps.Store.GetJob(ctx, *exp.JobID)
		if err != nil {
			_ = s.deps.Store.UpdateExport(ctx, exp.ID, "FAILED", "", "", 0, err.Error())
			return
		}
		chain = job.Chain
	}
	if chain == "" {
		chain = "bsc"
	}
	subjects := []string{subject}
	if subject == "" && exp.JobID != nil {
		jobSubjects, err := s.deps.Store.SubjectsByJob(ctx, *exp.JobID)
		if err != nil {
			_ = s.deps.Store.UpdateExport(ctx, exp.ID, "FAILED", "", "", 0, err.Error())
			return
		}
		subjects = subjects[:0]
		for _, item := range jobSubjects {
			subject := strings.TrimSpace(item.Subject)
			if chain != "tron" {
				subject = strings.ToLower(subject)
			}
			subjects = append(subjects, subject)
		}
	}
	var fromBlock, toBlock *int64
	var fromTime, toTime *time.Time
	if job != nil {
		fromBlock, toBlock, fromTime, toTime = exportJobBounds(exp.Dataset, job)
	}
	rows := make([]map[string]any, 0)
	var err error
	for _, itemSubject := range subjects {
		remaining := maxExportRows - len(rows)
		if remaining <= 0 {
			break
		}
		var batch []map[string]any
		batch, err = s.query.RowsForExportRange(ctx, exp.Dataset, chain, itemSubject, remaining, 0, fromBlock, toBlock, fromTime, toTime)
		if err != nil {
			break
		}
		rows = append(rows, batch...)
	}
	if err != nil {
		_ = s.deps.Store.UpdateExport(ctx, exp.ID, "FAILED", "", "", 0, err.Error())
		observability.ExportJobs.WithLabelValues(exp.Format, "FAILED").Inc()
		return
	}
	rows = dedupeExportRows(exp.Dataset, rows)
	s.attachTokenMetadata(ctx, chain, rows)
	key := fmt.Sprintf("exports/%s.%s", exp.ID, exp.Format)
	var size int64
	var loc exportLocation
	switch exp.Format {
	case "csv":
		size, loc, err = writeCSV(ctx, s, key, rows)
	case "json":
		size, loc, err = writeNDJSON(ctx, s, key, rows)
	case "xlsx":
		size, loc, err = writeXLSX(ctx, s, key, rows)
	}
	if err != nil {
		_ = s.deps.Store.UpdateExport(ctx, exp.ID, "FAILED", "", "", 0, err.Error())
		return
	}
	// BUG-001 修复：V1 导出同样回填对象 SHA-256，保证 GET /exports/{id} 的 sha256
	// 非空且可被下载文件复算（与 V2 管线口径一致）。
	sha, shaErr := s.objectSHA256(ctx, key)
	if shaErr != nil {
		s.deps.Log.Warn("export sha256 failed", "export_id", exp.ID, "err", shaErr)
	}
	_ = s.deps.Store.UpdateExport(ctx, exp.ID, "COMPLETED", key, sha, int64(len(rows)), "")
	observability.ExportJobs.WithLabelValues(exp.Format, "COMPLETED").Inc()
	completedDetail := map[string]any{
		"dataset": exp.Dataset, "format": exp.Format, "rows": len(rows), "bytes": size,
	}
	// ADR-019 §2.2：完成事件必须带落桶证据（PG 的 object_ref 只有 key，没有桶）。
	for k, v := range loc.auditFields() {
		completedDetail[k] = v
	}
	s.deps.Store.InsertAudit(ctx, nil, "export.completed", "export", exp.ID, completedDetail)
	// D-R10V4-01：同一完成事件必须同时进入 v2 不可抵赖审计链；legacy 写入保留（不改既有行为）。
	s.writeAuditCtx(ctx, "export.completed", "export", "export", exp.ID, completedDetail)
}

func dedupeExportRows(dataset string, rows []map[string]any) []map[string]any {
	seen := make(map[string]struct{}, len(rows))
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var key string
		switch dataset {
		case "transactions":
			key = fmt.Sprint(row["tx_hash"])
		case "token_transfers":
			key = fmt.Sprintf("%v|%v|%v", row["tx_hash"], row["log_index"], row["token_address"])
		case "nft_transfers":
			key = fmt.Sprintf("%v|%v|%v|%v", row["tx_hash"], row["log_index"], row["contract_address"], row["token_id"])
		case "logs":
			key = fmt.Sprintf("%v|%v", row["tx_hash"], row["log_index"])
		case "balances":
			key = fmt.Sprintf("%v|%v|%v", row["address"], row["token_address"], row["token_symbol"])
		}
		if key == "" {
			key = fmt.Sprint(row)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
	}
	return out
}

func exportJobBounds(dataset string, job *model.DownloadJob) (*int64, *int64, *time.Time, *time.Time) {
	// Balances are current snapshots. The provider and storage layer use a
	// synthetic snapshot_block=0, so historical job bounds must not filter
	// the rows produced for the job's addresses.
	if dataset == "balances" || job == nil {
		return nil, nil, nil, nil
	}
	if job.RangeType == "time" {
		return nil, nil, job.FromTimestamp, job.ToTimestamp
	}
	return job.FromBlock, job.ToBlock, nil, nil
}

func writeCSV(ctx context.Context, s *Server, key string, rows []map[string]any) (int64, exportLocation, error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		bw := bufio.NewWriterSize(pw, 1<<20)
		cw := csv.NewWriter(bw)
		if len(rows) > 0 {
			cols := mapKeys(rows[0])
			_ = cw.Write(cols)
			for _, r := range rows {
				rec := make([]string, len(cols))
				for i, c := range cols {
					rec[i] = fmt.Sprintf("%v", r[c])
				}
				_ = cw.Write(rec)
			}
		}
		cw.Flush()
		done <- bw.Flush()
		_ = pw.Close()
	}()
	size, loc, err := s.putExportStream(ctx, key, pr, -1, "text/csv")
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, loc, err
	}
	return size, loc, werr
}

func writeNDJSON(ctx context.Context, s *Server, key string, rows []map[string]any) (int64, exportLocation, error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		bw := bufio.NewWriterSize(pw, 1<<20)
		enc := json.NewEncoder(bw)
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				done <- err
				_ = pw.CloseWithError(err)
				return
			}
		}
		done <- bw.Flush()
		_ = pw.Close()
	}()
	size, loc, err := s.putExportStream(ctx, key, pr, -1, "application/x-ndjson")
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, loc, err
	}
	return size, loc, werr
}

func writeXLSX(ctx context.Context, s *Server, key string, rows []map[string]any) (int64, exportLocation, error) {
	f := excelize.NewFile()
	defer f.Close()
	var cols []string
	if len(rows) > 0 {
		cols = mapKeys(rows[0])
	}
	const maxRowsPerSheet = 1_048_576
	sheetIdx := 0
	for start := 0; start < len(rows); start += maxRowsPerSheet - 1 {
		end := start + maxRowsPerSheet - 1
		if end > len(rows) {
			end = len(rows)
		}
		name := "Sheet1"
		if sheetIdx > 0 {
			name = fmt.Sprintf("Sheet%d", sheetIdx+1)
		}
		if err := writeXLSXSheet(f, name, rows, cols, start, end); err != nil {
			return 0, exportLocation{Key: key}, err
		}
		sheetIdx++
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- f.Write(pw)
		_ = pw.Close()
	}()
	size, loc, err := s.putExportStream(ctx, key, pr, -1, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, loc, err
	}
	return size, loc, werr
}

// writeXLSXSheet 流式写单个 sheet，承载 rows[start:end)。首表（start==0）在第 1
// 行写表头，数据从第 2 行开始；续表从第 1 行开始。
//
// <dimension> 必须在 NewStreamWriter 之前设置：Excelize 在 StreamWriter.Flush()
// 时把 NewStreamWriter 快照的 worksheet 结构序列化进工作表 XML，Flush 之后再调
// SetSheetDimension 不会生效。若保持默认 <dimension ref="A1">，工作表 XML 中虽
// 有全部数据行，但依赖 dimension 的读取方（openpyxl read_only、pandas 等）会把
// 该工作簿报告为只有表头、0 数据行（XLSX V1/V2 验收失败的根因）。
func writeXLSXSheet(f *excelize.File, name string, rows []map[string]any, cols []string, start, end int) error {
	if idx, err := f.GetSheetIndex(name); err != nil || idx < 0 {
		if _, err := f.NewSheet(name); err != nil {
			return err
		}
	}
	withHeader := start == 0 && len(cols) > 0
	if len(cols) > 0 {
		totalRows := end - start
		if withHeader {
			totalRows++
		}
		last, err := excelize.CoordinatesToCellName(len(cols), totalRows)
		if err != nil {
			return err
		}
		if err := f.SetSheetDimension(name, "A1:"+last); err != nil {
			return err
		}
	}
	sw, err := f.NewStreamWriter(name)
	if err != nil {
		return err
	}
	firstDataRow := 1
	if withHeader {
		if err := sw.SetRow("A1", colsToAny(cols)); err != nil {
			return err
		}
		firstDataRow = 2
	}
	for i := start; i < end; i++ {
		cell, err := excelize.CoordinatesToCellName(1, i-start+firstDataRow)
		if err != nil {
			return err
		}
		rec := make([]any, len(cols))
		for j, c := range cols {
			rec[j] = rows[i][c]
		}
		if err := sw.SetRow(cell, rec); err != nil {
			return err
		}
	}
	return sw.Flush()
}

// exportFieldOrder 导出列的稳定顺序：交易身份 → Method（§47）→ 参与方 → 金额 → 技术字段。
// Go map 迭代顺序随机，必须显式排序，否则同一次导出的列顺序不可复现。
var exportFieldOrder = []string{
	"chain", "tx_hash", "block_number", "block_hash", "timestamp", "log_index",
	"method_id", "method_name", "method_signature", "method_source", "method_verified",
	"method_confidence", "method_type", "method_resolved_at", "decoded_input", "decode_source",
	"event_name", "event_signature", "event_source", "event_verified",
	"from_address", "to_address", "address", "counterparty", "direction", "activity_type",
	"token_address", "contract_address", "token_id", "token_symbol", "token_name", "token_decimals",
	"token_contract", "token_type", "token_metadata_status", "token_metadata_source", "token_metadata_confidence",
	"amount", "amount_raw", "raw_value", "formatted_amount", "amount_usd", "value_raw", "balance", "balance_raw", "balance_usd", "token_price",
	"status", "nonce", "gas", "gas_used", "gas_price", "tx_type", "topics", "data", "removed", "input",
	"matched_address", "case_names", "tag_names", "dataset", "source_provider", "source_ingested_at",
}

func mapKeys(m map[string]any) []string {
	rank := make(map[string]int, len(exportFieldOrder))
	for i, name := range exportFieldOrder {
		rank[name] = i
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, oki := rank[out[i]]
		rj, okj := rank[out[j]]
		if oki != okj {
			return oki
		}
		if oki && okj && ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

func colsToAny(cols []string) []any {
	out := make([]any, len(cols))
	for i, c := range cols {
		out[i] = c
	}
	return out
}
