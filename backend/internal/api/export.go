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
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/minio/minio-go/v7"
	"github.com/xuri/excelize/v2"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
)

const maxExportRows = 2_000_000

func (s *Server) handleCreateExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
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
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// V1.2：多地址导出走异步 ZIP 管线（§47-§53）
	if len(req.Addresses) > 0 || len(req.Datasets) > 0 {
		s.createExportV2(w, r, exportV2Request{
			JobID: req.JobID, Dataset: req.Dataset, Format: req.Format,
			Subject: req.Subject, Chain: req.Chain, Addresses: req.Addresses,
			Datasets: req.Datasets, IncludeMeta: req.IncludeMeta, ForceZip: req.ForceZip,
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
	go s.runExport(context.Background(), exp, strings.ToLower(req.Subject), strings.ToLower(req.Chain))
	writeJSON(w, http.StatusAccepted, exp)
}

func (s *Server) handleGetExport(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleDownloadExport(w http.ResponseWriter, r *http.Request) {
	exp, err := s.deps.Store.GetExport(r.Context(), chi.URLParam(r, "id"))
	if err != nil || exp == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "export not found"})
		return
	}
	if exp.Status != "COMPLETED" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "export not ready: " + exp.Status})
		return
	}
	// 经 API 流式转发（MinIO 只绑内网，避免暴露存储端口）；支持 HTTP Range 断点续传（§57）
	w.Header().Set("Accept-Ranges", "bytes")
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
		reader, size, err := s.deps.Raw.OpenRange(r.Context(), exp.ObjectRef, start, end)
		if err != nil {
			if errors.Is(err, rawstore.ErrInvalidRange) {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
				writeJSON(w, http.StatusRequestedRangeNotSatisfiable, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		defer reader.Close()
		if end < 0 || end >= size {
			end = size - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		if _, err := io.Copy(w, reader); err != nil {
			s.deps.Log.Error("export range stream failed", "export_id", exp.ID, "err", err)
		}
		return
	}

	reader, size, err := s.deps.Raw.Open(r.Context(), exp.ObjectRef)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Length", fmt.Sprint(size))
	if _, err := io.Copy(w, reader); err != nil {
		s.deps.Log.Error("export stream failed", "export_id", exp.ID, "err", err)
	}
}

// runExport 流式导出：查询 → 流式写入 MinIO（不把结果集全部装入内存）。
func (s *Server) runExport(ctx context.Context, exp *model.Export, subject, chain string) {
	_ = s.deps.Store.UpdateExport(ctx, exp.ID, "RUNNING", "", 0, "")
	if chain == "" {
		chain = "bsc"
		if exp.JobID != nil {
			if job, err := s.deps.Store.GetJob(ctx, *exp.JobID); err == nil {
				chain = job.Chain
			}
		}
	}
	rows, err := s.query.RowsForExport(ctx, exp.Dataset, chain, subject, maxExportRows, 0)
	if err != nil {
		_ = s.deps.Store.UpdateExport(ctx, exp.ID, "FAILED", "", 0, err.Error())
		observability.ExportJobs.WithLabelValues(exp.Format, "FAILED").Inc()
		return
	}
	key := fmt.Sprintf("exports/%s.%s", exp.ID, exp.Format)
	var size int64
	switch exp.Format {
	case "csv":
		size, err = writeCSV(ctx, s, key, rows)
	case "json":
		size, err = writeNDJSON(ctx, s, key, rows)
	case "xlsx":
		size, err = writeXLSX(ctx, s, key, rows)
	}
	if err != nil {
		_ = s.deps.Store.UpdateExport(ctx, exp.ID, "FAILED", "", 0, err.Error())
		return
	}
	_ = s.deps.Store.UpdateExport(ctx, exp.ID, "COMPLETED", key, int64(len(rows)), "")
	observability.ExportJobs.WithLabelValues(exp.Format, "COMPLETED").Inc()
	s.deps.Store.InsertAudit(ctx, nil, "export.completed", "export", exp.ID, map[string]any{
		"dataset": exp.Dataset, "format": exp.Format, "rows": len(rows), "bytes": size,
	})
}

func writeCSV(ctx context.Context, s *Server, key string, rows []map[string]any) (int64, error) {
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
	info, err := s.deps.Raw.Client().PutObject(ctx, s.deps.Raw.Bucket(), key, pr, -1, minio.PutObjectOptions{ContentType: "text/csv"})
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, err
	}
	return info.Size, werr
}

func writeNDJSON(ctx context.Context, s *Server, key string, rows []map[string]any) (int64, error) {
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
	info, err := s.deps.Raw.Client().PutObject(ctx, s.deps.Raw.Bucket(), key, pr, -1, minio.PutObjectOptions{ContentType: "application/x-ndjson"})
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, err
	}
	return info.Size, werr
}

func writeXLSX(ctx context.Context, s *Server, key string, rows []map[string]any) (int64, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Sheet1"
	var cols []string
	if len(rows) > 0 {
		cols = mapKeys(rows[0])
	}
	const maxRowsPerSheet = 1_048_576
	writeSheet := func(name string, start, end int) error {
		idx, err := f.GetSheetIndex(name)
		if err != nil {
			return err
		}
		if idx < 0 {
			idx, err = f.NewSheet(name)
			if err != nil {
				return err
			}
		}
		_ = idx
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
		return sw.Flush()
	}
	sheetIdx := 0
	for start := 0; start < len(rows); start += maxRowsPerSheet - 1 {
		end := start + maxRowsPerSheet - 1
		if end > len(rows) {
			end = len(rows)
		}
		name := sheet
		if sheetIdx > 0 {
			name = fmt.Sprintf("Sheet%d", sheetIdx+1)
		}
		if err := writeSheet(name, start, end); err != nil {
			return 0, err
		}
		sheetIdx++
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- f.Write(pw)
		_ = pw.Close()
	}()
	info, err := s.deps.Raw.Client().PutObject(ctx, s.deps.Raw.Bucket(), key, pr, -1, minio.PutObjectOptions{ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"})
	_ = pr.CloseWithError(err)
	werr := <-done
	if err != nil {
		return 0, err
	}
	return info.Size, werr
}

func mapKeys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func colsToAny(cols []string) []any {
	out := make([]any, len(cols))
	for i, c := range cols {
		out[i] = c
	}
	return out
}
