package api

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/xuri/excelize/v2"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// exportV2Request 多地址/多数据集异步导出（`47-`53）。
type exportV2Request struct {
	JobID       string   `json:"job_id"`
	Dataset     string   `json:"dataset"`
	Format      string   `json:"format"`
	Subject     string   `json:"subject"`
	Chain       string   `json:"chain"`
	Addresses   []string `json:"addresses"`
	Datasets    []string `json:"datasets"`
	IncludeMeta bool     `json:"include_meta"`
	ForceZip    bool     `json:"force_zip"`
	// §47：Method 字段默认导出；Decoded Input 默认关闭（完整参数会显著增加文件体积）
	IncludeDecodedInput bool `json:"include_decoded_input"`
	// 用户指定的导出时间范围（闭区间）；两者都可省略，省略即全量。
	StartTime *time.Time `json:"start_time"`
	EndTime   *time.Time `json:"end_time"`
	// 服务端注入的租户/操作者作用域；不从 JSON 读取（§21.2）。
	OrgID  string `json:"-"`
	WSID   string `json:"-"`
	UserID string `json:"-"`
}

func filterGPTActionExportToken(job *model.DownloadJob, rows []map[string]any) []map[string]any {
	if job == nil || job.Stats == nil {
		return rows
	}
	raw, ok := job.Stats["gpt_actions_token_filter"]
	if !ok || raw == nil {
		return rows
	}
	token := strings.ToUpper(strings.TrimSpace(fmt.Sprint(raw)))
	if token == "" || token == "<NIL>" {
		return rows
	}
	out := rows[:0]
	for _, row := range rows {
		if strings.EqualFold(fmt.Sprint(row["token_symbol"]), token) {
			out = append(out, row)
		}
	}
	return out
}

func (s *Server) createExportV2(w http.ResponseWriter, r *http.Request, req exportV2Request) {
	chain := strings.ToLower(req.Chain)
	var job *model.DownloadJob
	var jobID *string
	if req.JobID != "" {
		var err error
		job, err = s.deps.Store.GetJob(r.Context(), req.JobID)
		if err != nil || job == nil {
			writeJSON(w, 404, map[string]any{"error": "job not found"})
			return
		}
		if job.Status != model.JobCompleted && job.Status != model.JobPaused && job.Status != model.JobPausedFallbackTooExpensive {
			writeJSON(w, 409, map[string]any{"error": "job export requires a completed or paused job"})
			return
		}
		if req.OrgID != "" && (job.OrganizationID == nil || job.WorkspaceID == nil ||
			*job.OrganizationID != req.OrgID || *job.WorkspaceID != req.WSID) {
			writeJSON(w, 404, map[string]any{"error": "job not found"})
			return
		}
		jobID = &req.JobID
		chain = job.Chain
		req.Datasets = append([]string(nil), job.Datasets...)
		jobSubjects, err := s.deps.Store.SubjectsByJob(r.Context(), req.JobID)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		req.Addresses = req.Addresses[:0]
		for _, subject := range jobSubjects {
			req.Addresses = append(req.Addresses, subject.Subject)
		}
		req.ForceZip = true
	}
	if chain == "" {
		chain = "bsc"
	}
	format := req.Format
	if format == "" {
		format = "csv"
	}
	switch format {
	case "csv", "xlsx", "jsonl", "parquet":
	default:
		writeJSON(w, 400, map[string]any{"error": "format must be csv|xlsx|jsonl|parquet"})
		return
	}
	datasets := req.Datasets
	if len(datasets) == 0 {
		datasets = []string{"transactions", "token_transfers"}
	}
	for _, d := range datasets {
		if !model.ValidDataset(d) {
			writeJSON(w, 400, map[string]any{"error": "unknown dataset " + d})
			return
		}
	}
	addresses := req.Addresses
	for i := range addresses {
		// 导出键归一化：Solana 保留 base58 大小写，EVM 折叠小写。
		addresses[i] = addressnorm.NormalizeKey(chain, addresses[i])
	}
	if len(addresses) == 0 {
		writeJSON(w, 400, map[string]any{"error": "addresses required"})
		return
	}
	// `48：超过 3 地址强制 ZIP；文件数 >1 也 ZIP
	zipIt := exportZipDecision(req.ForceZip, len(addresses), len(datasets))
	partial := job != nil && job.Status != model.JobCompleted
	// 快照必须由 buildExportSnapshot 统一产出：预生成（export_prefetch.go）用同一份
	// 内容算规格哈希，任何字段/顺序差异都会让命中率归零。
	bounds, boundsErr := newExportBounds(req.StartTime, req.EndTime)
	if boundsErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": boundsErr.Error()})
		return
	}
	snapshot := buildExportSnapshot(chain, addresses, datasets, format, req.IncludeMeta, zipIt,
		req.IncludeDecodedInput, req.JobID, partial, jobStatusOf(job), bounds)
	// Reuse an identical artifact produced after the job's last mutation instead
	// of querying and compressing millions of rows again. Snapshot equality keeps
	// paused/partial exports from reusing a different progress version.
	if job != nil {
		var existingID string
		if err := s.deps.Store.Pool.QueryRow(r.Context(), `
			SELECT id::text FROM exports
			WHERE job_id=$1 AND status='COMPLETED' AND format=$2 AND zip=$3
			  AND query_snapshot=$4::jsonb AND object_ref<>''
			  AND COALESCE(finished_at,updated_at) >= $5
			  AND organization_id=$6 AND workspace_id=$7
			ORDER BY finished_at DESC NULLS LAST, updated_at DESC LIMIT 1`,
			req.JobID, format, zipIt, snapshot, job.UpdatedAt, req.OrgID, req.WSID).Scan(&existingID); err == nil {
			if existing, getErr := s.deps.Store.GetExport(r.Context(), existingID); getErr == nil && existing != nil {
				writeJSON(w, http.StatusOK, existing)
				return
			}
		}
	}
	// P0 修复：作用域必须由调用方注入（session 主体来自 tenantScope，
	// GPT Actions 来自 systemScope）。这里不再回退到 DefaultOrgWorkspace ——
	// 否则任何认证用户都能把导出写进默认组织，并在导出列表/详情里读到它。
	orgID, wsID := req.OrgID, req.WSID
	// 预生成命中：同一份规格的产物已经算好了，直接提升成正式导出，不再跑流水线。
	// 只在"单地址 + 无 job"时尝试——那正是预生成唯一支持的形态（见 handleExportPrefetch）。
	if job == nil && len(addresses) == 1 {
		if promoted, ok := s.tryPromotePrefetch(r.Context(), store.PrefetchSpecHash(snapshot),
			orgID, wsID, req.UserID, strings.Join(datasets, "+"), format, zipIt, snapshot); ok {
			writeJSON(w, http.StatusOK, promoted)
			return
		}
	}
	if orgID == "" || wsID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "tenant scope is required for exports"})
		return
	}
	var createdBy *string
	if req.UserID != "" {
		u := req.UserID
		createdBy = &u
	}
	exp, err := s.deps.Store.CreateExportV2(r.Context(), wsID, orgID, createdBy, jobID, strings.Join(datasets, "+"), format, zipIt, snapshot)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	go s.runExportMulti(backgroundAuditCtx(r), exp, job, chain, addresses, datasets, format, req.IncludeMeta, zipIt, req.IncludeDecodedInput, bounds)
	writeJSON(w, 202, exp)
}

// runExportMulti 流式多地址导出（`52：Go API 不承载 GB 级文件，行级流式写 MinIO）。
func (s *Server) runExportMulti(ctx context.Context, exp *model.Export, job *model.DownloadJob, chain string, addresses, datasets []string, format string, includeMeta, zipIt, includeDecodedInput bool, bounds exportBounds) {
	_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "RUNNING", "", "", 0, "")
	// NOTE (P4, 2026-09-15): there is deliberately NO start-of-pipeline intent
	// check here. The pipeline goroutine is launched inside the request handler,
	// i.e. BEFORE the request transaction commits, so a check that runs outside
	// that transaction can observe "no row" for a perfectly legitimate export.
	// Measured on the all-format gate run p4-allfmt-20260914T235024Z: 6 of 8
	// accepted exports aborted at start with intent_row_missing while their rows
	// were committed. The single enforcement point is the pre-Put gate in
	// putExportStream, which runs after the pipeline has done real work and the
	// request has long committed.
	key := fmt.Sprintf("exports/%s.%s", exp.ID, extFor(format))
	if zipIt {
		key = fmt.Sprintf("exports/%s.zip", exp.ID)
	}
	var rowsTotal int64
	var objectSHA string
	var loc exportLocation
	var ferr error

	// 导出文件里的案件/标签注释必须使用**该导出对象自己的**工作区，
	// 而不是默认工作区（否则会把另一个租户的案件名写进文件）。
	exportWS := s.exportWorkspaceID(ctx, exp.ID)

	if zipIt {
		rowsTotal, objectSHA, loc, ferr = s.writeZipExport(ctx, exp, job, key, chain, exportWS, addresses, datasets, format, includeMeta, includeDecodedInput, bounds)
	} else {
		rowsTotal, objectSHA, loc, ferr = s.writeSingleExport(ctx, exp.ID, job, key, chain, exportWS, addresses[0], datasets[0], format, includeMeta, includeDecodedInput, bounds)
	}
	if ferr != nil {
		// A failed pipeline used to be visible only in the exports row, so the
		// end-to-end gate could see a stuck PENDING/FAILED export with no line
		// in the runtime log that explained it. Always log the real cause.
		if s.deps.Log != nil {
			s.deps.Log.Warn("export pipeline failed", "export_id", exp.ID, "zip", zipIt,
				"format", format, "datasets", len(datasets), "error", ferr.Error())
		}
		_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "FAILED", "", "", 0, ferr.Error())
		return
	}
	_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "COMPLETED", key, objectSHA, rowsTotal, "")
	multiDetail := map[string]any{
		"chain": chain, "addresses": len(addresses), "datasets": datasets, "format": format, "zip": zipIt, "rows": rowsTotal,
	}
	// 时间范围进入审计：用户能据此核对"我导出的到底是哪一段"。
	for k, v := range bounds.auditFields() {
		multiDetail[k] = v
	}
	// ADR-019 §2.2：完成事件必须带落桶证据。
	for k, v := range loc.auditFields() {
		multiDetail[k] = v
	}
	s.deps.Store.InsertAudit(ctx, nil, "export.completed", "export", exp.ID, multiDetail)
	// D-R10V4-01：同时写入 v2 审计链（audit_events_v2），legacy 写入保留。
	s.writeAuditCtx(ctx, "export.completed", "export", "export", exp.ID, multiDetail)
}

// writeSingleExport 单地址单数据集：流式写一个对象。
// canStreamSingleExport 判断该请求能否走"边取边写"的流式路径。
//
// 条件刻意保守：
//   - 只支持 csv/jsonl。xlsx 与 parquet 的写入器需要随机访问（工作表索引、行组），
//     无法增量产出；强行流式会改变输出结构。
//   - 不开 include_decoded_input。该列要先按整批 tx_hash 查一次解码表，
//     流式下只能按块查，会让同一批哈希被查多次且引入跨块顺序差异。
//   - 不带 GPT Actions 的 token 过滤。过滤发生在取数之后，流式下无法"
//     取一批滤一批"地保证与批量路径相同的行数上限语义。
//
// 不满足时回退到原批量路径，行为与改动前完全一致。
func canStreamSingleExport(format string, includeDecodedInput bool, job *model.DownloadJob) bool {
	if includeDecodedInput || job != nil {
		return false
	}
	switch format {
	case "csv", "jsonl":
		return true
	default:
		return false
	}
}

// writeSingleExportStream 是 writeSingleExport 的流式版本：查询结果按块取回、
// 按块富化、按块序列化，直接推进 io.Pipe → MinIO，不再把全部行攒在内存里。
//
// 实测（EXPORT_STREAM=1 时由 export_flowcost_test 记录）与批量路径的内存差异见
// export_stream.go 文件头。
func (s *Server) writeSingleExportStream(ctx context.Context, exportID string, key, chain, wsID, addr, dataset, format string,
	includeMeta bool, bounds exportBounds) (int64, string, exportLocation, error) {

	pr, pw := io.Pipe()
	type streamResult struct {
		rows int64
		err  error
	}
	done := make(chan streamResult, 1)
	enrich := newStreamEnrichment(s, chain, wsID, includeMeta)

	go func() {
		var werr error
		var writer interface {
			write([]map[string]any) error
			close() error
		}
		switch format {
		case "csv":
			writer = newStreamCSVWriter(pw)
		case "jsonl":
			writer = newStreamJSONLWriter(pw)
		}
		var total int
		if writer != nil {
			// 优先走两阶段形状（先按天聚合定时间下界，再带界取数）：实测同一导出
			// 请求的服务端耗时从 3.68 s 降到 0.75 s 量级，因为成本主要在"物化 22 列
			// 宽行"而不是扫描或排序。定界不适用时回退无界形状。
			// 全量取数：支持键集游标的数据集走分页（每页一条有限查询），其余数据集
			// 走不带 LIMIT 的单查询流式消费。两条路都不再有行数上限，也不再需要
			// "先按天聚合定时间下界"的两阶段形状——分页本身就把扫描量限住了。
			table, where, sqlArgs := exportStreamQueryParts(dataset, chain, addr)
			where, sqlArgs = appendExportTimeBounds(where, sqlArgs, bounds)
			emit := func(chunk []map[string]any) error {
				enrich.enrich(ctx, addr, chunk)
				return writer.write(chunk)
			}
			if _, ok := query.ExportCursorFor(dataset); ok {
				total, _, werr = s.query.ForEachExportChunkKeyset(ctx,
					query.ExportKeysetQuery{Dataset: dataset, Table: table, Where: where, Args: sqlArgs},
					0, exportStreamChunk, emit)
			} else {
				total, werr = s.query.ForEachExportChunk(ctx, dataset,
					exportStreamSQLUnbounded(table, where, 0), 0, exportStreamChunk, emit, sqlArgs...)
			}
			if werr == nil {
				werr = writer.close()
			}
		}
		_ = pw.CloseWithError(werr)
		done <- streamResult{rows: int64(total), err: werr}
	}()

	_, loc, err := s.putExportStream(ctx, exportID, key, pr, -1, contentType(format))
	_ = pr.CloseWithError(err)
	res := <-done
	if err != nil {
		return 0, "", loc, err
	}
	if res.err != nil {
		return 0, "", loc, res.err
	}
	sha, err := s.objectSHA256(ctx, key)
	return res.rows, sha, loc, err
}

// errStreamUnsupported 表示该请求不满足流式导出的前提，调用方应回退批量路径。
var errStreamUnsupported = errors.New("streaming export unsupported for this request")

// exportStreamChunk 是流式导出的块大小（行）。3.2 万行时元数据批量查询约 1 ms，
// 同时把单块内存控制在几十 MiB。
const exportStreamChunk = 32 * 1024

// exportStreamSQL 与 RowsForExportRange 的无 FINAL 形状保持一致：
// 列集、过滤、排序完全相同，只是由调用方按块消费。
// exportStreamQueryParts 给出数据集的取数组成：表名、条件子句与参数。
//
// 参数个数必须与占位符个数严格一致：logs/balances 的 `chain=? AND address=?`
// 只有两个占位符，而 exportStreamArgs 过去恒返回三个——多出的参数会让驱动直接报错，
// 也就是 logs/balances 的流式导出从未真正跑通。由
// TestExportStreamQueryPartsMatchPlaceholders 冻结这条不变量。
func exportStreamQueryParts(dataset, chain, subject string) (string, string, []any) {
	switch dataset {
	case "logs", "balances":
		return exportStreamTable(dataset), "chain=? AND address=?", []any{chain, subject}
	default:
		return exportStreamTable(dataset), "chain=? AND (from_address=? OR to_address=?)",
			[]any{chain, subject, subject}
	}
}

// exportStreamSQLUnbounded 渲染导出查询。limit<=0 时不带 LIMIT——那是"全量"，
// 由键集分页（每页自带 LIMIT）或单查询流式消费保证内存有界。
func exportStreamSQLUnbounded(table, where string, limit int) string {
	sql := "SELECT * FROM blockchain." + table + " WHERE " + where + " ORDER BY timestamp DESC"
	if limit > 0 {
		sql += " LIMIT ? OFFSET ?"
	}
	return sql
}

func exportStreamTable(dataset string) string {
	if dataset == "internal_transactions" {
		return "internal_calls"
	}
	return dataset
}

func (s *Server) writeSingleExport(ctx context.Context, exportID string, job *model.DownloadJob, key, chain, wsID, addr, dataset, format string, includeMeta, includeDecodedInput bool, bounds exportBounds) (int64, string, exportLocation, error) {
	// 列式直写：不建 []map[string]any，从 ClickHouse 列直接写 CSV。实测把 20 万行
	// 完整链路从 5.5-5.9 s 压到 2 s 量级（见 export_columnar.go 文件头的下限测算）。
	// 只在"单地址 + csv + 不带 GPT 过滤 + 不开 decoded_input + 不需要案件/标签注释"
	// 时启用；其余情形语义不同（多文件、随机访问格式、整批哈希查询、按用途改行值），
	// 一律回退原有 map 路径。
	if exportColumnarEnabled() && canUseColumnarExport(job, format, dataset, includeDecodedInput) {
		tokenFilter, _ := columnarTokenFilter(job)
		rows, sha, loc, err := s.writeSingleExportColumnar(ctx, exportID, key, chain, wsID, addr, dataset, includeMeta, bounds, tokenFilter)
		if !errors.Is(err, errColumnarUnsupported) {
			return rows, sha, loc, err
		}
	}
	if exportStreamEnabled() && canStreamSingleExport(format, includeDecodedInput, job) {
		rows, sha, loc, err := s.writeSingleExportStream(ctx, exportID, key, chain, wsID, addr, dataset, format, includeMeta, bounds)
		if !errors.Is(err, errStreamUnsupported) {
			return rows, sha, loc, err
		}
	}
	fromBlock, toBlock, fromTime, toTime := exportJobBounds(dataset, job)
	bounds = bounds.intersect(exportBounds{From: fromTime, To: toTime})
	fromTime, toTime = bounds.From, bounds.To
	// 批量写出器（xlsx/parquet/整批过滤）必须一次拿到全部行：走键集分页取全量，
	// 超过内存上限时明确失败，绝不再截断。
	rows, err := s.collectExportRows(ctx, dataset, chain, addr, bounds, fromBlock, toBlock)
	if err != nil {
		return 0, "", exportLocation{Key: key}, err
	}
	rows = filterGPTActionExportToken(job, rows)
	rows = dedupeExportRows(dataset, rows)
	s.attachTokenMetadata(ctx, chain, rows)
	if includeMeta {
		s.annotateRows(ctx, chain, addr, rows, wsID)
	}
	s.attachDecodedInput(ctx, chain, dataset, rows, includeDecodedInput)
	rowCount := int64(len(rows))
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var werr error
		switch format {
		case "csv":
			werr = streamCSV(pw, rows)
		case "jsonl":
			werr = streamNDJSON(pw, rows)
		case "xlsx":
			werr = streamXLSX(pw, rows)
		}
		_ = pw.CloseWithError(werr)
		done <- werr
	}()
	if format == "parquet" {
		_, loc, err := s.writeParquetTemp(ctx, exportID, key, dataset, rows)
		if err != nil {
			return 0, "", loc, err
		}
		sha, err := s.objectSHA256(ctx, key)
		return rowCount, sha, loc, err
	}
	_, loc, err := s.putExportStream(ctx, exportID, key, pr, -1, contentType(format))
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, "", loc, err
	}
	if werr != nil {
		return 0, "", loc, werr
	}
	sha, err := s.objectSHA256(ctx, key)
	return rowCount, sha, loc, err
}

// writeZipExport 多地址/多文件：流式 ZIP（`49 结构 + manifest.json）。
func (s *Server) writeZipExport(ctx context.Context, exp *model.Export, job *model.DownloadJob, key, chain, wsID string, addresses, datasets []string, format string, includeMeta, includeDecodedInput bool, bounds exportBounds) (int64, string, exportLocation, error) {
	pr, pw := io.Pipe()
	type zipResult struct {
		rows int64
		err  error
	}
	done := make(chan zipResult, 1)
	go func() {
		zw := zip.NewWriter(pw)
		// manifest.json（`49）
		partial := job != nil && job.Status != model.JobCompleted
		mf, _ := json.Marshal(map[string]any{
			"export_id": exp.ID, "created_at": time.Now().UTC().Format(time.RFC3339),
			"chain": chain, "addresses": addresses, "datasets": datasets,
			"format": format, "schema_version": 3, "zip": true,
			"include_decoded_input": includeDecodedInput, "job_id": exp.JobID,
			"partial": partial,
			"job_status": func() string {
				if job != nil {
					return job.Status
				}
				return ""
			}(),
			"job_progress": func() any {
				if job != nil {
					return job.Stats["progress"]
				}
				return nil
			}(),
			"completed_shards": func() any {
				if job != nil {
					return job.Stats["done_shards"]
				}
				return nil
			}(),
			"total_shards": func() any {
				if job != nil {
					return job.Stats["total_shards"]
				}
				return nil
			}(),
		})
		hdr := &zip.FileHeader{Name: "manifest.json", Method: zip.Deflate}
		w, _ := zw.CreateHeader(hdr)
		_, _ = w.Write(mf)

		type exportPart struct {
			addr, ds string
			rows     []map[string]any
			prepared *preparedZipEntry
			// columnarRows 是列式路径已经写进 prepared 的行数（rows 为空时用它计数）。
			columnarRows int64
			err          error
		}
		type exportTask struct {
			addr, ds string
			multi    bool
		}
		pipelineCtx, cancel := context.WithCancel(ctx)
		jobs := make(chan exportTask)
		parts := make(chan exportPart)
		workerCount := min(4, len(addresses)*len(datasets))
		var wg sync.WaitGroup
		for range workerCount {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for task := range jobs {
					fromBlock, toBlock, fromTime, toTime := exportJobBounds(task.ds, job)
					taskBounds := bounds.intersect(exportBounds{From: fromTime, To: toTime})
					fromTime, toTime = taskBounds.From, taskBounds.To
					if task.multi {
						rows, err := s.query.RowsForExportRangeMulti(pipelineCtx, task.ds, chain, append([]string(nil), addresses...), maxExportRows*len(addresses), fromBlock, toBlock, fromTime, toTime)
						if err != nil {
							select {
							case parts <- exportPart{ds: task.ds, err: err}:
							case <-pipelineCtx.Done():
							}
							continue
						}
						rows = filterGPTActionExportToken(job, rows)
						buckets := make(map[string][]map[string]any, len(addresses))
						for _, row := range rows {
							for _, addr := range matchingExportSubjects(chain, addresses, row) {
								buckets[addr] = append(buckets[addr], row)
							}
						}
						for _, addr := range addresses {
							partRows := dedupeExportRows(task.ds, buckets[addr])
							s.attachTokenMetadata(pipelineCtx, chain, partRows)
							if includeMeta {
								s.annotateRows(pipelineCtx, chain, addr, partRows, wsID)
							}
							s.attachDecodedInput(pipelineCtx, chain, task.ds, partRows, includeDecodedInput)
							select {
							case parts <- exportPart{addr: addr, ds: task.ds, rows: partRows}:
							case <-pipelineCtx.Done():
								return
							}
						}
						continue
					}
					// 列式直写：不建行 map，deflate 到临时文件后写进 ZIP。
					//
					// 实测（173 万行、csv、include_meta=true，即 Actions 导出的实际参数）：
					// map 路径整条 zip 导出 5-6 分钟（1.7M 行 map 常驻堆 1.7-2 GiB，
					// CPU profile 里 GC 占 79.7%）；列式同口径单个条目 16.4 s。
					//
					// 条件里的 job == nil 是因为带 job 的导出还要按 token 过滤整批行
					// （filterGPTActionExportToken），那条语义列式路径目前不复刻。
					useColumnar := format == "csv" && exportColumnarEnabled() &&
						canUseColumnarExport(job, format, task.ds, includeDecodedInput)
					if useColumnar {
						tokenFilter, _ := columnarTokenFilter(job)
						entry, entryRows, entryErr := s.prepareColumnarZipEntry(pipelineCtx, task.ds, chain, task.addr, 0, includeMeta, wsID, taskBounds, tokenFilter)
						if entryErr == nil {
							select {
							case parts <- exportPart{addr: task.addr, ds: task.ds, prepared: entry, columnarRows: entryRows}:
							case <-pipelineCtx.Done():
								entry.remove()
								return
							}
							continue
						}
						if !errors.Is(entryErr, errColumnarUnsupported) {
							select {
							case parts <- exportPart{addr: task.addr, ds: task.ds, err: entryErr}:
							case <-pipelineCtx.Done():
							}
							continue
						}
					}
					rows, err := s.collectExportRows(pipelineCtx, task.ds, chain, task.addr, taskBounds, fromBlock, toBlock)
					if err == nil {
						rows = filterGPTActionExportToken(job, rows)
						rows = dedupeExportRows(task.ds, rows)
						s.attachTokenMetadata(pipelineCtx, chain, rows)
						if includeMeta {
							s.annotateRows(pipelineCtx, chain, task.addr, rows, wsID)
						}
						s.attachDecodedInput(pipelineCtx, chain, task.ds, rows, includeDecodedInput)
						if format == "csv" || format == "jsonl" {
							prepared, prepareErr := prepareZipEntry(format, rows)
							if prepareErr != nil {
								err = prepareErr
							} else {
								select {
								case parts <- exportPart{addr: task.addr, ds: task.ds, rows: rows, prepared: prepared}:
								case <-pipelineCtx.Done():
									prepared.remove()
									return
								}
								continue
							}
						}
					}
					select {
					case parts <- exportPart{addr: task.addr, ds: task.ds, rows: rows, err: err}:
					case <-pipelineCtx.Done():
						return
					}
				}
			}()
		}
		go func() {
			defer close(jobs)
			for _, addr := range addresses {
				for _, ds := range datasets {
					select {
					case jobs <- exportTask{addr: addr, ds: ds}:
					case <-pipelineCtx.Done():
						return
					}
				}
			}
		}()
		go func() { wg.Wait(); close(parts) }()

		var rowsTotal int64
		var werr error
		for part := range parts {
			if part.err != nil {
				// Keep the FIRST failure. Every worker shares pipelineCtx, so the
				// cancel() below makes the other in-flight queries return
				// context.Canceled. Overwriting werr with that cascade used to hide
				// the real cause: measured 2026-09-15 on the P4 v2 ZIP positive, a
				// ClickHouse MEMORY_LIMIT_EXCEEDED on token_transfers surfaced as
				// "put export object ...: context canceled" and the exports row
				// recorded nothing about the real error at all.
				if werr == nil {
					werr = fmt.Errorf("zip dataset %q address %q: %w", part.ds, part.addr, part.err)
				}
				cancel()
				continue
			}
			if werr != nil {
				continue
			}
			rowsTotal += int64(len(part.rows))
			rowsTotal += part.columnarRows
			name := fmt.Sprintf("addresses/%s/%s.%s", part.addr, part.ds, extFor(format))
			if part.prepared != nil {
				fh := &zip.FileHeader{Name: name, Method: zip.Deflate, CRC32: part.prepared.crc32, CompressedSize64: uint64(part.prepared.compressed), UncompressedSize64: uint64(part.prepared.uncompressed)}
				fh.SetModTime(time.Now())
				fw, err := zw.CreateRaw(fh)
				if err == nil {
					err = part.prepared.copyTo(fw)
				}
				part.prepared.remove()
				if err != nil {
					werr = err
					cancel()
				}
				continue
			}
			fh := &zip.FileHeader{Name: name, Method: zipEntryMethod(format)}
			fh.SetModTime(time.Now())
			fw, err := zw.CreateHeader(fh)
			if err != nil {
				werr = err
				break
			}
			switch format {
			case "csv":
				werr = streamCSV(fw, part.rows)
			case "jsonl":
				werr = streamNDJSON(fw, part.rows)
			case "xlsx":
				werr = streamXLSX(fw, part.rows)
			case "parquet":
				werr = s.streamParquetZipEntry(ctx, fw, part.ds, part.rows)
			}
			if werr != nil {
				cancel()
			}
		}
		cancel()
		if err := zw.Close(); werr == nil && err != nil {
			werr = err
		}
		_ = pw.CloseWithError(werr)
		done <- zipResult{rows: rowsTotal, err: werr}
	}()
	// Immediately before the object write, which is the side effect that object
	// storage cannot roll back: without the committed intent row this pipeline must
	// not create the artifact at all.
	if !s.exportIntentCommitted(ctx, exp.ID) {
		_ = pr.CloseWithError(errExportIntentGone)
		<-done
		s.exportAbortNoIntent(exp.ID, "zip-put")
		return 0, "", exportLocation{Key: key}, errExportIntentGone
	}
	_, loc, err := s.putExportStream(ctx, exp.ID, key, pr, -1, "application/zip")
	_ = pr.CloseWithError(err)
	result := <-done
	if err != nil {
		return 0, "", loc, err
	}
	if result.err != nil {
		return 0, "", loc, result.err
	}
	sha, err := s.objectSHA256(ctx, key)
	return result.rows, sha, loc, err
}

type preparedZipEntry struct {
	path                     string
	crc32                    uint32
	compressed, uncompressed int64
}

// XLSX is itself ZIP; Parquet already compresses its data pages.
func zipEntryMethod(format string) uint16 {
	if format == "xlsx" || format == "parquet" {
		return zip.Store
	}
	return zip.Deflate
}

type countOnlyWriter struct{ n int64 }

func (w *countOnlyWriter) Write(p []byte) (int, error) { w.n += int64(len(p)); return len(p), nil }
func (p *preparedZipEntry) remove() {
	if p != nil && p.path != "" {
		_ = os.Remove(p.path)
	}
}
func (p *preparedZipEntry) copyTo(w io.Writer) error {
	f, err := os.Open(p.path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.CopyBuffer(w, f, make([]byte, 1<<20))
	return err
}

func prepareZipEntry(format string, rows []map[string]any) (*preparedZipEntry, error) {
	f, err := os.CreateTemp("", "export-entry-*.deflate")
	if err != nil {
		return nil, err
	}
	entry := &preparedZipEntry{path: f.Name()}
	failed := true
	defer func() {
		_ = f.Close()
		if failed {
			entry.remove()
		}
	}()
	zw, err := flate.NewWriter(f, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	hash, count := crc32.NewIEEE(), &countOnlyWriter{}
	w := io.MultiWriter(zw, hash, count)
	if format == "csv" {
		err = streamCSV(w, rows)
	} else {
		err = streamNDJSON(w, rows)
	}
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	entry.crc32, entry.compressed, entry.uncompressed = hash.Sum32(), info.Size(), count.n
	failed = false
	return entry, nil
}

func matchingExportSubjects(chain string, subjects []string, row map[string]any) []string {
	// 行对象与主体地址都走同一套按链归一化，Solana 才可能匹配上。
	normalize := func(v string) string { return addressnorm.NormalizeKey(chain, v) }
	from, to, address := normalize(fmt.Sprint(row["from_address"])), normalize(fmt.Sprint(row["to_address"])), normalize(fmt.Sprint(row["address"]))
	out := make([]string, 0, 2)
	for _, subject := range subjects {
		n := normalize(subject)
		if n == from || n == to || n == address {
			out = append(out, subject)
		}
	}
	return out
}

// attachDecodedInput 可选附加 Decoded Input（§47：默认关闭，完整参数会显著增加文件体积）。
func (s *Server) attachDecodedInput(ctx context.Context, chain, dataset string, rows []map[string]any, enabled bool) {
	if !enabled || dataset != "transactions" || len(rows) == 0 || s.deps.MethodData == nil {
		return
	}
	hashes := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		hash := strings.ToLower(fmt.Sprint(row["tx_hash"]))
		if hash == "" || seen[hash] {
			continue
		}
		seen[hash] = true
		hashes = append(hashes, hash)
	}
	decoded, err := s.deps.MethodData.DecodedInputsByTxHashes(ctx, chain, hashes)
	if err != nil {
		s.deps.Log.Warn("attach decoded input failed", "err", err)
		return
	}
	for _, row := range rows {
		hash := strings.ToLower(fmt.Sprint(row["tx_hash"]))
		if record, ok := decoded[hash]; ok {
			row["decoded_input"] = record.DecodedJSON
			row["decode_source"] = record.DecodeSource
		} else {
			row["decoded_input"] = ""
			row["decode_source"] = ""
		}
	}
}

// exportWorkspaceID 返回导出对象自己的工作区。
//
// exports 表带 organization_id / workspace_id，但 model.Export 没有映射这两列，
// 因此这里单独查一次；查询失败时返回空串，调用方会跳过案件/标签注释（fail closed），
// 绝不回退到 Store.DefaultOrgWorkspace（否则会把别的租户案件名写进导出文件）。
func (s *Server) exportWorkspaceID(ctx context.Context, exportID string) string {
	if s.deps.Store == nil || s.deps.Store.Pool == nil || exportID == "" {
		return ""
	}
	var wsID string
	if err := s.deps.Store.Pool.QueryRow(ctx,
		"SELECT COALESCE(workspace_id::text,'') FROM exports WHERE id=$1", exportID).Scan(&wsID); err != nil {
		return ""
	}
	return wsID
}

// annotateRows 附加案件/标签元数据（`50：matched_address/case_names/tag_names）。
func (s *Server) annotateRows(ctx context.Context, chain, subject string, rows []map[string]any, wsID string) {
	if wsID == "" {
		// 拿不到任务自己的作用域就不附加案件/标签名，绝不回退到默认工作区
		// （否则会把另一个租户的案件名写进导出文件）。
		return
	}
	cache := map[string][]string{}
	for _, r := range rows {
		matched := matchedExportAddresses(chain, subject, r)
		var cases, tags []string
		for _, a := range matched {
			if v, ok := cache["c:"+a]; ok {
				cases = append(cases, v...)
			} else {
				names, _ := s.deps.Store.AddressCaseNames(ctx, wsID, chain, a)
				cache["c:"+a] = names
				cases = append(cases, names...)
			}
			if v, ok := cache["t:"+a]; ok {
				tags = append(tags, v...)
			} else {
				names, _ := s.deps.Store.AddressTagNames(ctx, wsID, chain, a)
				cache["t:"+a] = names
				tags = append(tags, names...)
			}
		}
		r["matched_address"] = strings.Join(matched, "|")
		r["case_names"] = strings.Join(dedupeStrings(cases), "|")
		r["tag_names"] = strings.Join(dedupeStrings(tags), "|")
	}
}

func matchedExportAddresses(chain, subject string, row map[string]any) []string {
	// 行对象与主体地址都走同一套按链归一化，Solana 才可能匹配上。
	normalize := func(value string) string { return addressnorm.NormalizeKey(chain, value) }
	subject = normalize(subject)
	if subject == "" {
		return nil
	}
	from := normalize(fmt.Sprint(row["from_address"]))
	to := normalize(fmt.Sprint(row["to_address"]))
	address := normalize(fmt.Sprint(row["address"]))
	if from == subject || to == subject || address == subject {
		return []string{subject}
	}
	return nil
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func extFor(format string) string {
	switch format {
	case "jsonl":
		return "jsonl"
	case "json":
		return "jsonl"
	case "parquet":
		return "parquet"
	case "xlsx":
		return "xlsx"
	default:
		return "csv"
	}
}

func contentType(format string) string {
	switch format {
	case "jsonl":
		return "application/x-ndjson"
	case "parquet":
		return "application/vnd.apache.parquet"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	default:
		return "text/csv"
	}
}

// streamCSV / streamNDJSON / streamXLSX：行级流式（不整表进内存，`51）。
// streamCSV 把导出行为 CSV。
//
// 实测（cmd/exportcost/csvthroughput_test.go，20 万行 × 27 列合成宽行）：
// 旧的"每格 fmt.Sprintf + encoding/csv"写法 1.539 s，改成把值直接 append 进
// 复用缓冲区后 0.399 s（3.9 倍）。encoding/csv 每字段还要再扫一遍找引号，
// fmt.Sprintf 每格还要分配一个临时字符串——导出文件的单元格数是百万级，两笔都省不掉。
//
// 输出与旧写法**逐字节一致**：同样的引号规则（含分隔符/引号/CR/LF 才加引号，
// 内部引号翻倍）、同样的换行符（\n）、同样的取值方式（fmt.Sprint 语义，
// 只是非字符串类型走 strconv 快路径）。csv_equivalence_test.go 锁定这一点。
func streamCSV(w io.Writer, rows []map[string]any) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	if len(rows) == 0 {
		return bw.Flush()
	}
	cols := mapKeys(rows[0])
	for i, c := range cols {
		if i > 0 {
			bw.WriteByte(',')
		}
		writeCSVField(bw, c)
	}
	bw.WriteByte('\n')
	buf := make([]byte, 0, 4096)
	for _, r := range rows {
		buf = buf[:0]
		for i, c := range cols {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendCSVValue(buf, r[c])
		}
		buf = append(buf, '\n')
		if _, err := bw.Write(buf); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// appendCSVValue 把单元格值按 fmt.Sprint 的语义追加到 dst，字符串走快路径。
func appendCSVValue(dst []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return dst
	case string:
		return appendCSVQuoted(dst, x)
	case uint64:
		return strconv.AppendUint(dst, x, 10)
	case uint32:
		return strconv.AppendUint(dst, uint64(x), 10)
	case uint16:
		return strconv.AppendUint(dst, uint64(x), 10)
	case uint8:
		return strconv.AppendUint(dst, uint64(x), 10)
	case int:
		return strconv.AppendInt(dst, int64(x), 10)
	case int64:
		return strconv.AppendInt(dst, x, 10)
	case int32:
		return strconv.AppendInt(dst, int64(x), 10)
	case float64:
		return strconv.AppendFloat(dst, x, 'g', -1, 64)
	case bool:
		return strconv.AppendBool(dst, x)
	default:
		// 其余类型（time.Time、decimal.Decimal、json.Number…）保留 fmt.Sprint 语义。
		return appendCSVQuoted(dst, fmt.Sprint(x))
	}
}

// appendCSVQuoted 按 encoding/csv 的规则给字段加引号。
func appendCSVQuoted(dst []byte, s string) []byte {
	needQuote := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ',', '"', '\n', '\r':
			needQuote = true
		}
		if needQuote {
			break
		}
	}
	if !needQuote {
		return append(dst, s...)
	}
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			dst = append(dst, '"')
		}
		dst = append(dst, s[i])
	}
	return append(dst, '"')
}

// writeCSVField 写表头单元格（数量等于列数，不是热点路径，保持可读写法）。
func writeCSVField(bw *bufio.Writer, s string) {
	if !csvFieldNeedsQuotes(s) {
		bw.WriteString(s)
		return
	}
	bw.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			bw.WriteByte('"')
		}
		bw.WriteByte(s[i])
	}
	bw.WriteByte('"')
}

func csvFieldNeedsQuotes(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ',', '"', '\n', '\r':
			return true
		}
	}
	return false
}

func streamNDJSON(w io.Writer, rows []map[string]any) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	enc := json.NewEncoder(bw)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// streamParquetZipEntry 占位：Parquet 在 ZIP 中先落临时文件再拷贝（保持内存有界）。
func (s *Server) streamParquetZipEntry(ctx context.Context, w io.Writer, dataset string, rows []map[string]any) error {
	tmp, err := os.CreateTemp("", "parquet-entry-*.parquet")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := s.writeParquetTo(ctx, tmp, dataset, rows); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return err
	}
	_, err = io.Copy(w, tmp)
	tmp.Close()
	return err
}

// writeParquetTemp 单文件 Parquet：临时文件 + 上传（内存有界）。
func (s *Server) writeParquetTemp(ctx context.Context, exportID, key, dataset string, rows []map[string]any) (int64, exportLocation, error) {
	tmp, err := os.CreateTemp("", "export-*.parquet")
	if err != nil {
		return 0, exportLocation{Key: key}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := s.writeParquetTo(ctx, tmp, dataset, rows); err != nil {
		tmp.Close()
		return 0, exportLocation{Key: key}, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return 0, exportLocation{Key: key}, err
	}
	size, loc, err := s.putExportStream(ctx, exportID, key, tmp, -1, "application/vnd.apache.parquet")
	tmp.Close()
	if err != nil {
		return 0, loc, err
	}
	return size, loc, nil
}

// parquetExportRow 是所有链上数据集的稳定列并集；不适用的列保持零值。
// 金额字段保留字符串，避免 Decimal256 精度损失。
type parquetExportRow struct {
	Dataset          string   `parquet:"dataset"`
	Chain            string   `parquet:"chain"`
	BlockNumber      uint64   `parquet:"block_number"`
	BlockHash        string   `parquet:"block_hash"`
	TxHash           string   `parquet:"tx_hash"`
	Timestamp        string   `parquet:"timestamp"`
	FromAddress      string   `parquet:"from_address"`
	ToAddress        string   `parquet:"to_address"`
	Address          string   `parquet:"address"`
	LogIndex         uint64   `parquet:"log_index"`
	TokenAddress     string   `parquet:"token_address"`
	ContractAddress  string   `parquet:"contract_address"`
	TokenID          string   `parquet:"token_id"`
	TokenSymbol      string   `parquet:"token_symbol"`
	TokenName        string   `parquet:"token_name"`
	TokenDecimals    uint64   `parquet:"token_decimals"`
	AmountRaw        string   `parquet:"amount_raw"`
	Amount           string   `parquet:"amount"`
	ValueRaw         string   `parquet:"value_raw"`
	BalanceRaw       string   `parquet:"balance_raw"`
	Balance          string   `parquet:"balance"`
	BalanceUSD       string   `parquet:"balance_usd"`
	TokenPrice       string   `parquet:"token_price"`
	MethodID         string   `parquet:"method_id"`
	MethodName       string   `parquet:"method_name"`
	MethodSignature  string   `parquet:"method_signature"`
	MethodSource     string   `parquet:"method_source"`
	MethodVerified   string   `parquet:"method_verified"`
	MethodConfidence string   `parquet:"method_confidence"`
	MethodType       string   `parquet:"method_type"`
	DecodedInput     string   `parquet:"decoded_input"`
	EventName        string   `parquet:"event_name"`
	EventSignature   string   `parquet:"event_signature"`
	Input            string   `parquet:"input"`
	Data             string   `parquet:"data"`
	Topics           []string `parquet:"topics,list"`
	SourceProvider   string   `parquet:"source_provider"`
	SourceIngestedAt string   `parquet:"source_ingested_at"`
	MatchedAddress   string   `parquet:"matched_address"`
	CaseNames        string   `parquet:"case_names"`
	TagNames         string   `parquet:"tag_names"`
}

func (s *Server) writeParquetTo(ctx context.Context, w io.Writer, dataset string, rows []map[string]any) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	pw := parquet.NewGenericWriter[parquetExportRow](w, parquet.Compression(&zstd.Codec{}))
	batch := make([]parquetExportRow, len(rows))
	for i, row := range rows {
		batch[i] = toParquetExportRow(dataset, row)
	}
	n, err := pw.Write(batch)
	if err != nil {
		_ = pw.Close()
		return int64(n), err
	}
	if err := pw.Close(); err != nil {
		return int64(n), err
	}
	return int64(n), nil
}

func toParquetExportRow(dataset string, row map[string]any) parquetExportRow {
	return parquetExportRow{
		Dataset: dataset, Chain: exportString(row, "chain"), BlockNumber: exportUint64(row, "block_number"),
		BlockHash: exportString(row, "block_hash"), TxHash: exportString(row, "tx_hash"), Timestamp: exportString(row, "timestamp"),
		FromAddress: exportString(row, "from_address"), ToAddress: exportString(row, "to_address"), Address: exportString(row, "address"),
		LogIndex: exportUint64(row, "log_index"), TokenAddress: exportString(row, "token_address"), ContractAddress: exportString(row, "contract_address"),
		TokenID: exportString(row, "token_id"), TokenSymbol: exportString(row, "token_symbol"), TokenName: exportString(row, "token_name"),
		TokenDecimals: exportUint64(row, "token_decimals"), AmountRaw: exportString(row, "amount_raw"), Amount: exportString(row, "amount"),
		ValueRaw: exportString(row, "value_raw"), BalanceRaw: exportString(row, "balance_raw"), Balance: exportString(row, "balance"),
		BalanceUSD: exportString(row, "balance_usd"), TokenPrice: exportString(row, "token_price"), MethodID: exportString(row, "method_id"),
		MethodName: exportString(row, "method_name"), MethodSignature: exportString(row, "method_signature"),
		MethodSource: exportString(row, "method_source"), MethodVerified: exportString(row, "method_verified"),
		MethodConfidence: exportString(row, "method_confidence"), MethodType: exportString(row, "method_type"),
		DecodedInput: exportString(row, "decoded_input"),
		EventName:    exportString(row, "event_name"), EventSignature: exportString(row, "event_signature"),
		Input: exportString(row, "input"), Data: exportString(row, "data"), Topics: exportStrings(row, "topics"),
		SourceProvider: exportString(row, "source_provider"), SourceIngestedAt: exportString(row, "source_ingested_at"),
		MatchedAddress: exportString(row, "matched_address"), CaseNames: exportString(row, "case_names"), TagNames: exportString(row, "tag_names"),
	}
}

func exportString(row map[string]any, key string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func exportUint64(row map[string]any, key string) uint64 {
	n, _ := strconv.ParseUint(exportString(row, key), 10, 64)
	return n
}

func exportStrings(row map[string]any, key string) []string {
	if values, ok := row[key].([]string); ok {
		return values
	}
	return nil
}

// objectSHA256 复算对象内容的 SHA-256。F-R10-BUCKET：必须走双读，
// 否则迁移到新桶的对象在切写后无法复算（旧实现只读 raw 桶）。
func (s *Server) objectSHA256(ctx context.Context, key string) (string, error) {
	r, _, _, err := s.openExportObject(ctx, key)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func streamXLSX(w io.Writer, rows []map[string]any) error {
	f := excelize.NewFile()
	defer f.Close()
	var cols []string
	if len(rows) > 0 {
		cols = mapKeys(rows[0])
	}
	sheet := "Sheet1"
	sheetIdx := 0
	const maxRowsPerSheet = 1_048_576
	for start := 0; start < len(rows); start += maxRowsPerSheet - 1 {
		end := start + maxRowsPerSheet - 1
		if end > len(rows) {
			end = len(rows)
		}
		name := sheet
		if sheetIdx > 0 {
			name = fmt.Sprintf("Sheet%d", sheetIdx+1)
		}
		if err := writeXLSXSheet(f, name, rows, cols, start, end); err != nil {
			return err
		}
		sheetIdx++
	}
	return f.Write(w)
}
