package api

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/xuri/excelize/v2"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
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
}

func filterGPTActionExportToken(job *model.DownloadJob, rows []map[string]any) []map[string]any {
	if job == nil || job.Stats == nil {
		return rows
	}
	token := strings.ToUpper(strings.TrimSpace(fmt.Sprint(job.Stats["gpt_actions_token_filter"])))
	if token == "" {
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
		addresses[i] = strings.TrimSpace(addresses[i])
		if chain != "tron" {
			addresses[i] = strings.ToLower(addresses[i])
		}
	}
	if len(addresses) == 0 {
		writeJSON(w, 400, map[string]any{"error": "addresses required"})
		return
	}
	// `48：超过 3 地址强制 ZIP；文件数 >1 也 ZIP
	zipIt := req.ForceZip || len(addresses) > 3 || len(datasets) > 1
	partial := job != nil && job.Status != model.JobCompleted
	snapshot, _ := json.Marshal(map[string]any{
		"chain": chain, "addresses": addresses, "datasets": datasets,
		"format": format, "include_meta": req.IncludeMeta, "zip": zipIt,
		"include_decoded_input": req.IncludeDecodedInput, "job_id": req.JobID,
		"partial": partial, "job_status": func() string {
			if job != nil {
				return job.Status
			}
			return ""
		}(),
	})
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
			ORDER BY finished_at DESC NULLS LAST, updated_at DESC LIMIT 1`,
			req.JobID, format, zipIt, snapshot, job.UpdatedAt).Scan(&existingID); err == nil {
			if existing, getErr := s.deps.Store.GetExport(r.Context(), existingID); getErr == nil && existing != nil {
				writeJSON(w, http.StatusOK, existing)
				return
			}
		}
	}
	orgID, wsID, err := s.deps.Store.DefaultOrgWorkspace(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	exp, err := s.deps.Store.CreateExportV2(r.Context(), wsID, orgID, nil, jobID, strings.Join(datasets, "+"), format, zipIt, snapshot)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	go s.runExportMulti(context.Background(), exp, job, chain, addresses, datasets, format, req.IncludeMeta, zipIt, req.IncludeDecodedInput)
	writeJSON(w, 202, exp)
}

// runExportMulti 流式多地址导出（`52：Go API 不承载 GB 级文件，行级流式写 MinIO）。
func (s *Server) runExportMulti(ctx context.Context, exp *model.Export, job *model.DownloadJob, chain string, addresses, datasets []string, format string, includeMeta, zipIt, includeDecodedInput bool) {
	_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "RUNNING", "", "", 0, "")
	key := fmt.Sprintf("exports/%s.%s", exp.ID, extFor(format))
	if zipIt {
		key = fmt.Sprintf("exports/%s.zip", exp.ID)
	}
	var rowsTotal int64
	var objectSHA string
	var ferr error

	if zipIt {
		rowsTotal, objectSHA, ferr = s.writeZipExport(ctx, exp, job, key, chain, addresses, datasets, format, includeMeta, includeDecodedInput)
	} else {
		rowsTotal, objectSHA, ferr = s.writeSingleExport(ctx, job, key, chain, addresses[0], datasets[0], format, includeMeta, includeDecodedInput)
	}
	if ferr != nil {
		_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "FAILED", "", "", 0, ferr.Error())
		return
	}
	_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "COMPLETED", key, objectSHA, rowsTotal, "")
	s.deps.Store.InsertAudit(ctx, nil, "export.completed", "export", exp.ID, map[string]any{
		"chain": chain, "addresses": len(addresses), "datasets": datasets, "format": format, "zip": zipIt, "rows": rowsTotal,
	})
}

// writeSingleExport 单地址单数据集：流式写一个对象。
func (s *Server) writeSingleExport(ctx context.Context, job *model.DownloadJob, key, chain, addr, dataset, format string, includeMeta, includeDecodedInput bool) (int64, string, error) {
	fromBlock, toBlock, fromTime, toTime := exportJobBounds(dataset, job)
	rows, err := s.query.RowsForExportRange(ctx, dataset, chain, addr, maxExportRows, 0, fromBlock, toBlock, fromTime, toTime)
	if err != nil {
		return 0, "", err
	}
	rows = filterGPTActionExportToken(job, rows)
	rows = dedupeExportRows(dataset, rows)
	s.attachTokenMetadata(ctx, chain, rows)
	if includeMeta {
		s.annotateRows(ctx, chain, addr, rows)
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
		if _, err := s.writeParquetTemp(ctx, key, dataset, rows); err != nil {
			return 0, "", err
		}
		sha, err := s.objectSHA256(ctx, key)
		return rowCount, sha, err
	}
	info, err := s.deps.Raw.Client().PutObject(ctx, s.deps.Raw.Bucket(), key, pr, -1, minio.PutObjectOptions{ContentType: contentType(format)})
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, "", err
	}
	_ = info
	if werr != nil {
		return 0, "", werr
	}
	sha, err := s.objectSHA256(ctx, key)
	return rowCount, sha, err
}

// writeZipExport 多地址/多文件：流式 ZIP（`49 结构 + manifest.json）。
func (s *Server) writeZipExport(ctx context.Context, exp *model.Export, job *model.DownloadJob, key, chain string, addresses, datasets []string, format string, includeMeta, includeDecodedInput bool) (int64, string, error) {
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
			err      error
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
								s.annotateRows(pipelineCtx, chain, addr, partRows)
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
					rows, err := s.query.RowsForExportRange(pipelineCtx, task.ds, chain, task.addr, maxExportRows, 0, fromBlock, toBlock, fromTime, toTime)
					if err == nil {
						rows = filterGPTActionExportToken(job, rows)
						rows = dedupeExportRows(task.ds, rows)
						s.attachTokenMetadata(pipelineCtx, chain, rows)
						if includeMeta {
							s.annotateRows(pipelineCtx, chain, task.addr, rows)
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
				werr = part.err
				cancel()
				continue
			}
			if werr != nil {
				continue
			}
			rowsTotal += int64(len(part.rows))
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
			fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
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
	_, err := s.deps.Raw.Client().PutObject(ctx, s.deps.Raw.Bucket(), key, pr, -1, minio.PutObjectOptions{ContentType: "application/zip"})
	_ = pr.CloseWithError(err)
	result := <-done
	if err != nil {
		return 0, "", err
	}
	if result.err != nil {
		return 0, "", result.err
	}
	sha, err := s.objectSHA256(ctx, key)
	return result.rows, sha, err
}

type preparedZipEntry struct {
	path                     string
	crc32                    uint32
	compressed, uncompressed int64
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
	normalize := func(v string) string {
		v = strings.TrimSpace(v)
		if chain != "tron" {
			return strings.ToLower(v)
		}
		return v
	}
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

// annotateRows 附加案件/标签元数据（`50：matched_address/case_names/tag_names）。
func (s *Server) annotateRows(ctx context.Context, chain, subject string, rows []map[string]any) {
	_, wsID, err := s.deps.Store.DefaultOrgWorkspace(ctx)
	if err != nil {
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
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		if chain == "tron" {
			return value
		}
		return strings.ToLower(value)
	}
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
func streamCSV(w io.Writer, rows []map[string]any) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	cw := csv.NewWriter(bw)
	if len(rows) > 0 {
		cols := mapKeys(rows[0])
		if err := cw.Write(cols); err != nil {
			return err
		}
		for _, r := range rows {
			rec := make([]string, len(cols))
			for i, c := range cols {
				rec[i] = fmt.Sprintf("%v", r[c])
			}
			if err := cw.Write(rec); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return bw.Flush()
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
func (s *Server) writeParquetTemp(ctx context.Context, key, dataset string, rows []map[string]any) (int64, error) {
	tmp, err := os.CreateTemp("", "export-*.parquet")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	if _, err := s.writeParquetTo(ctx, tmp, dataset, rows); err != nil {
		tmp.Close()
		return 0, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return 0, err
	}
	info, err := s.deps.Raw.Client().PutObject(ctx, s.deps.Raw.Bucket(), key, tmp, -1, minio.PutObjectOptions{ContentType: "application/vnd.apache.parquet"})
	tmp.Close()
	if err != nil {
		return 0, err
	}
	return info.Size, nil
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

func (s *Server) objectSHA256(ctx context.Context, key string) (string, error) {
	r, _, err := s.deps.Raw.Open(ctx, key)
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
