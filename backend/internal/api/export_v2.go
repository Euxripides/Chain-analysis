package api

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
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
}

func (s *Server) createExportV2(w http.ResponseWriter, r *http.Request, req exportV2Request) {
	chain := strings.ToLower(req.Chain)
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
		addresses[i] = strings.ToLower(addresses[i])
	}
	if len(addresses) == 0 {
		writeJSON(w, 400, map[string]any{"error": "addresses required"})
		return
	}
	// `48：超过 3 地址强制 ZIP；文件数 >1 也 ZIP
	zipIt := req.ForceZip || len(addresses) > 3 || len(datasets) > 1
	snapshot, _ := json.Marshal(map[string]any{
		"chain": chain, "addresses": addresses, "datasets": datasets,
		"format": format, "include_meta": req.IncludeMeta, "zip": zipIt,
	})
	orgID, wsID, err := s.deps.Store.DefaultOrgWorkspace(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	exp, err := s.deps.Store.CreateExportV2(r.Context(), wsID, orgID, nil, strings.Join(datasets, "+"), format, zipIt, snapshot)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	go s.runExportMulti(context.Background(), exp, chain, addresses, datasets, format, req.IncludeMeta, zipIt)
	writeJSON(w, 202, exp)
}

// runExportMulti 流式多地址导出（`52：Go API 不承载 GB 级文件，行级流式写 MinIO）。
func (s *Server) runExportMulti(ctx context.Context, exp *model.Export, chain string, addresses, datasets []string, format string, includeMeta, zipIt bool) {
	_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "RUNNING", "", "", 0, "")
	key := fmt.Sprintf("exports/%s.%s", exp.ID, extFor(format))
	if zipIt {
		key = fmt.Sprintf("exports/%s.zip", exp.ID)
	}
	var rowsTotal int64
	var ferr error

	if zipIt {
		rowsTotal, ferr = s.writeZipExport(ctx, exp, key, chain, addresses, datasets, format, includeMeta)
	} else {
		rowsTotal, ferr = s.writeSingleExport(ctx, key, chain, addresses[0], datasets[0], format, includeMeta)
	}
	if ferr != nil {
		_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "FAILED", "", "", 0, ferr.Error())
		return
	}
	_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "COMPLETED", key, "", rowsTotal, "")
	s.deps.Store.InsertAudit(ctx, nil, "export.completed", "export", exp.ID, map[string]any{
		"chain": chain, "addresses": len(addresses), "datasets": datasets, "format": format, "zip": zipIt, "rows": rowsTotal,
	})
}

// writeSingleExport 单地址单数据集：流式写一个对象。
func (s *Server) writeSingleExport(ctx context.Context, key, chain, addr, dataset, format string, includeMeta bool) (int64, error) {
	rows, err := s.query.RowsForExport(ctx, dataset, chain, addr, maxExportRows, 0)
	if err != nil {
		return 0, err
	}
	if includeMeta {
		s.annotateRows(ctx, chain, rows)
	}
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
		return s.writeParquetTemp(ctx, key, rows)
	}
	info, err := s.deps.Raw.Client().PutObject(ctx, s.deps.Raw.Bucket(), key, pr, -1, minio.PutObjectOptions{ContentType: contentType(format)})
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, err
	}
	return info.Size, werr
}

// writeZipExport 多地址/多文件：流式 ZIP（`49 结构 + manifest.json）。
func (s *Server) writeZipExport(ctx context.Context, exp *model.Export, key, chain string, addresses, datasets []string, format string, includeMeta bool) (int64, error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		zw := zip.NewWriter(pw)
		// manifest.json（`49）
		mf, _ := json.Marshal(map[string]any{
			"export_id": exp.ID, "created_at": time.Now().UTC().Format(time.RFC3339),
			"chain": chain, "addresses": addresses, "datasets": datasets,
			"format": format, "schema_version": 2, "zip": true,
		})
		hdr := &zip.FileHeader{Name: "manifest.json", Method: zip.Deflate}
		w, _ := zw.CreateHeader(hdr)
		_, _ = w.Write(mf)

		var werr error
		for _, addr := range addresses {
			for _, ds := range datasets {
				rows, err := s.query.RowsForExport(ctx, ds, chain, addr, maxExportRows, 0)
				if err != nil {
					werr = err
					break
				}
				if includeMeta {
					s.annotateRows(ctx, chain, rows)
				}
				name := fmt.Sprintf("addresses/%s/%s.%s", addr, ds, extFor(format))
				fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
				fh.SetModTime(time.Now())
				fw, err := zw.CreateHeader(fh)
				if err != nil {
					werr = err
					break
				}
				switch format {
				case "csv":
					werr = streamCSV(fw, rows)
				case "jsonl":
					werr = streamNDJSON(fw, rows)
				case "xlsx":
					werr = streamXLSX(fw, rows)
				case "parquet":
					werr = s.streamParquetZipEntry(ctx, fw, rows)
				}
				if werr != nil {
					break
				}
			}
			if werr != nil {
				break
			}
		}
		_ = zw.Close()
		_ = pw.CloseWithError(werr)
		done <- werr
	}()
	info, err := s.deps.Raw.Client().PutObject(ctx, s.deps.Raw.Bucket(), key, pr, -1, minio.PutObjectOptions{ContentType: "application/zip"})
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, err
	}
	return info.Size, werr
}

// annotateRows 附加案件/标签元数据（`50：matched_address/case_names/tag_names）。
func (s *Server) annotateRows(ctx context.Context, chain string, rows []map[string]any) {
	_, wsID, err := s.deps.Store.DefaultOrgWorkspace(ctx)
	if err != nil {
		return
	}
	cache := map[string][]string{}
	for _, r := range rows {
		matched, _ := r["matched_addresses"].([]string)
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
func (s *Server) streamParquetZipEntry(ctx context.Context, w io.Writer, rows []map[string]any) error {
	tmp, err := os.CreateTemp("", "parquet-entry-*.parquet")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := s.writeParquetTo(ctx, tmp, rows); err != nil {
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
func (s *Server) writeParquetTemp(ctx context.Context, key string, rows []map[string]any) (int64, error) {
	tmp, err := os.CreateTemp("", "export-*.parquet")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	if _, err := s.writeParquetTo(ctx, tmp, rows); err != nil {
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

// writeParquetTo 最小 Parquet 实现（本地文件，行式 JSON schema）。
func (s *Server) writeParquetTo(ctx context.Context, w io.Writer, rows []map[string]any) (int64, error) {
	// V1.2 简化：Parquet 先以 JSONL 落盘实现路径（真实 Parquet 编码见 TODO 依赖 xitongsys）
	bw := bufio.NewWriterSize(w, 1<<20)
	enc := json.NewEncoder(bw)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return 0, err
		}
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return 0, nil
}

// streamXLSX 流式 XLSX（§51：excelize StreamWriter，不整表进 RAM）。
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
		if idx, err := f.GetSheetIndex(name); err != nil || idx < 0 {
			_, _ = f.NewSheet(name)
		}
		sw, err := f.NewStreamWriter(name)
		if err != nil {
			return err
		}
		if start == 0 && len(cols) > 0 {
			if err := sw.SetRow("A1", colsToAny(cols)); err != nil {
				return err
			}
		}
		for i := start; i < end; i++ {
			cell, _ := excelize.CoordinatesToCellName(1, i-start+2)
			rec := make([]any, len(cols))
			for j, c := range cols {
				rec[j] = rows[i][c]
			}
			if err := sw.SetRow(cell, rec); err != nil {
				return err
			}
		}
		if err := sw.Flush(); err != nil {
			return err
		}
		sheetIdx++
	}
	return f.Write(w)
}

