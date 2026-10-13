package api

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/xuri/excelize/v2"
)

type exportDatasetFile struct {
	Dataset   string `json:"dataset"`
	Name      string `json:"name"`
	Rows      int64  `json:"rows"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
	ObjectRef string `json:"object_ref"`
}

func (s *Server) runDatasetExport(ctx context.Context, exp *model.Export, job *model.DownloadJob, req exportV2CreateRequest, addresses, datasets []string, filter *query.StatisticsFilter, zipIt bool) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "RUNNING", "", "", 0, "")
	files := []exportDatasetFile{}
	paths := []string{}
	datasetPaths := []string{}
	var loc exportLocation
	uploadedKeys := []string{}
	var total int64
	defer func() {
		for _, path := range paths {
			_ = os.Remove(path)
		}
	}()
	fail := func(err error) {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cleanupCancel()
		if order, e := s.exportReadOrder(); e == nil {
			for _, bucket := range order {
				if name, e := s.exportBucketName(bucket); e == nil {
					for _, key := range uploadedKeys {
						_ = s.deps.Raw.Client().RemoveObject(cleanupCtx, name, key, minio.RemoveObjectOptions{})
					}
				}
			}
		}
		_ = s.deps.Store.UpdateExportV2(context.WithoutCancel(ctx), exp.ID, "FAILED", "", "", 0, err.Error())
		s.writeAuditCtx(context.WithoutCancel(ctx), "export.failed", "export", "export", exp.ID, map[string]any{"error": safeJobError(err.Error()), "content": req.Content})
	}
	wsID := s.exportWorkspaceID(ctx, exp.ID)
	for _, dataset := range datasets {
		file, err := os.CreateTemp("", "query-export-*."+extFor(req.Format))
		if err != nil {
			fail(err)
			return
		}
		paths = append(paths, file.Name())
		datasetPaths = append(datasetPaths, file.Name())
		var count int64
		if req.Content == "statistics" || req.Content == "counterparties" {
			f := datasetExportFilter(*filter, dataset, job)
			stats, err := s.query.Statistics(ctx, f)
			if err != nil {
				file.Close()
				fail(err)
				return
			}
			rows := statisticsExportRows(stats, req.Content)
			count = int64(len(rows))
			err = writeStatisticsExport(ctx, file, req.Format, rows)
			if err != nil {
				file.Close()
				fail(err)
				return
			}
		} else {
			format := req.Format
			var spool *os.File
			var target io.Writer = file
			if format == "xlsx" {
				spool, err = os.CreateTemp("", "query-export-spool-*.jsonl")
				if err != nil {
					file.Close()
					fail(err)
					return
				}
				paths = append(paths, spool.Name())
				target = spool
			}
			source := func(visit func(map[string]any) error) error {
				fromBlock, toBlock, fromTime, toTime := exportJobBounds(dataset, job)
				if req.StartTime != nil && (fromTime == nil || fromTime.Before(*req.StartTime)) {
					fromTime = req.StartTime
				}
				if req.EndTime != nil && (toTime == nil || toTime.After(*req.EndTime)) {
					toTime = req.EndTime
				}
				if filter == nil {
					return s.query.StreamRowsForExportAddresses(ctx, dataset, req.Chain, addresses, fromBlock, toBlock, fromTime, toTime, visit)
				}
				f := datasetExportFilter(*filter, dataset, job)
				if fromTime != nil && (f.FromTime == nil || f.FromTime.Before(*fromTime)) {
					f.FromTime = fromTime
				}
				if toTime != nil && (f.ToTime == nil || f.ToTime.After(*toTime)) {
					f.ToTime = toTime
				}
				if dataset != "transactions" && dataset != "token_transfers" && dataset != "internal_transactions" {
					return s.query.StreamRowsForExportAddresses(ctx, dataset, req.Chain, addresses, fromBlock, toBlock, f.FromTime, f.ToTime, func(row map[string]any) error {
						if f.AsOf != nil {
							if at, ok := row["source_ingested_at"].(time.Time); ok && at.After(*f.AsOf) {
								return nil
							}
						}
						if query.MatchExportRow(f, dataset, row) {
							return visit(row)
						}
						return nil
					})
				}
				return s.query.ScanCanonicalEvents(ctx, f, func(event query.CanonicalEvent) error {
					if fromBlock != nil && event.BlockNumber < uint64(*fromBlock) {
						return nil
					}
					if toBlock != nil && event.BlockNumber > uint64(*toBlock) {
						return nil
					}
					return visit(event.ExportRow(req.Chain))
				})
			}
			var cols []string
			count, cols, err = s.writeExportDatasetRowsSource(ctx, target, job, req.Chain, wsID, "", dataset, format, req.IncludeMeta, req.IncludeDecodedInput, source)
			if spool != nil {
				closeErr := spool.Close()
				if err == nil {
					err = closeErr
				}
				if err == nil {
					err = writeStreamedXLSXContext(ctx, file, spool.Name(), count, cols)
				}
			}
			if err != nil {
				file.Close()
				fail(err)
				return
			}
		}
		if err := file.Close(); err != nil {
			fail(err)
			return
		}
		uploadedKeys = append(uploadedKeys, fmt.Sprintf("exports/%s/files/%s.%s", exp.ID, dataset, extFor(req.Format)))
		stored, err := s.uploadDatasetFile(ctx, exp.ID, dataset, req.Format, count, file.Name())
		if err != nil {
			fail(err)
			return
		}
		_, loc, err = s.statExportObject(ctx, stored.ObjectRef)
		if err != nil {
			fail(err)
			return
		}
		files = append(files, stored)
		total += count
	}
	key := files[0].ObjectRef
	sha := files[0].SHA256
	if zipIt {
		archive, err := os.CreateTemp("", "query-export-*.zip")
		if err != nil {
			fail(err)
			return
		}
		paths = append(paths, archive.Name())
		zw := newFastZipWriter(archive)
		for index, file := range files {
			entry, err := zw.CreateHeader(&zip.FileHeader{Name: file.Name, Method: zipEntryMethod(req.Format)})
			if err != nil {
				archive.Close()
				fail(err)
				return
			}
			input, err := os.Open(datasetPaths[index])
			if err != nil {
				archive.Close()
				fail(err)
				return
			}
			_, err = io.Copy(entry, input)
			input.Close()
			if err != nil {
				archive.Close()
				fail(err)
				return
			}
		}
		if err := zw.Close(); err != nil {
			archive.Close()
			fail(err)
			return
		}
		if _, err := archive.Seek(0, io.SeekStart); err != nil {
			archive.Close()
			fail(err)
			return
		}
		key = fmt.Sprintf("exports/%s.zip", exp.ID)
		uploadedKeys = append(uploadedKeys, key)
		_, loc, err = s.putExportStream(ctx, exp.ID, key, archive, -1, "application/zip")
		archive.Close()
		if err != nil {
			fail(err)
			return
		}
		sha, err = s.objectSHA256(ctx, key)
		if err != nil {
			fail(err)
			return
		}
	}
	manifest, _ := json.Marshal(files)
	if _, err := s.deps.Store.Pool.Exec(ctx, "UPDATE exports SET file_manifest=$2::jsonb WHERE id=$1", exp.ID, manifest); err != nil {
		fail(err)
		return
	}
	if err := s.deps.Store.UpdateExportV2(ctx, exp.ID, "COMPLETED", key, sha, total, ""); err != nil {
		fail(err)
		return
	}
	details := map[string]any{"content": req.Content, "filters": filter, "file_layout": "dataset", "files": files, "rows": total}
	for k, v := range loc.auditFields() {
		details[k] = v
	}
	s.writeAuditCtx(ctx, "export.completed", "export", "export", exp.ID, details)
}

func (s *Server) uploadDatasetFile(ctx context.Context, exportID, dataset, format string, rows int64, path string) (exportDatasetFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return exportDatasetFile{}, err
	}
	defer file.Close()
	h := sha256.New()
	bytes, err := io.Copy(h, file)
	if err != nil {
		return exportDatasetFile{}, err
	}
	sha := hex.EncodeToString(h.Sum(nil))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return exportDatasetFile{}, err
	}
	key := fmt.Sprintf("exports/%s/files/%s.%s", exportID, dataset, extFor(format))
	_, _, err = s.putExportStream(ctx, exportID, key, file, bytes, contentType(format))
	if err != nil {
		return exportDatasetFile{}, err
	}
	actual, err := s.objectSHA256(ctx, key)
	if err != nil {
		return exportDatasetFile{}, err
	}
	if actual != sha {
		return exportDatasetFile{}, fmt.Errorf("dataset object integrity mismatch")
	}
	return exportDatasetFile{Dataset: dataset, Name: dataset + "." + extFor(format), Rows: rows, Bytes: bytes, SHA256: sha, ObjectRef: key}, nil
}

func statisticsExportRows(stats query.StatisticsResult, content string) []map[string]any {
	rows := []map[string]any{}
	groups := map[string][]query.StatisticsGroup{"assets": stats.Assets, "trend": stats.Trend, "hours": stats.Hours, "distribution": stats.Distribution, "addresses": stats.Addresses}
	if content == "counterparties" {
		groups = map[string][]query.StatisticsGroup{"counterparties": stats.CounterpartyItems}
	}
	for _, dimension := range []string{"assets", "trend", "hours", "distribution", "addresses", "counterparties"} {
		for _, group := range groups[dimension] {
			t := group.Total
			rows = append(rows, map[string]any{"chain": stats.Filter.Chain, "dimension": dimension, "dataset": t.Dataset, "token_address": t.TokenAddress, "token_symbol": t.TokenSymbol, "token_decimals": t.Decimals, "counterparty": group.Counterparty, "address": group.Address, "bucket": group.Bucket, "in_raw": t.InRaw, "out_raw": t.OutRaw, "net_raw": t.NetRaw, "internal_raw": t.InternalRaw, "self_raw": t.SelfRaw, "event_count": t.Events, "transaction_count": t.TxCount, "missing_amount_rows": t.Missing, "failed_event_count": t.Failed, "first_seen": t.First, "last_seen": t.Last, "share_percent": group.Share, "data_version": stats.Version, "timezone": stats.Filter.Timezone, "as_of": stats.Filter.AsOf})
		}
	}
	return rows
}

type statisticsParquetRow struct {
	Chain        string `parquet:"chain"`
	Dataset      string `parquet:"dataset"`
	Dimension    string `parquet:"dimension"`
	TokenAddress string `parquet:"token_address"`
	TokenSymbol  string `parquet:"token_symbol"`
	Decimals     uint64 `parquet:"token_decimals"`
	Counterparty string `parquet:"counterparty"`
	Address      string `parquet:"address"`
	Bucket       string `parquet:"bucket"`
	InRaw        string `parquet:"in_raw"`
	OutRaw       string `parquet:"out_raw"`
	NetRaw       string `parquet:"net_raw"`
	InternalRaw  string `parquet:"internal_raw"`
	SelfRaw      string `parquet:"self_raw"`
	Events       uint64 `parquet:"event_count"`
	Transactions uint64 `parquet:"transaction_count"`
	Missing      uint64 `parquet:"missing_amount_rows"`
	Failed       uint64 `parquet:"failed_event_count"`
	First        string `parquet:"first_seen"`
	Last         string `parquet:"last_seen"`
	Share        string `parquet:"share_percent"`
	Version      string `parquet:"data_version"`
	Timezone     string `parquet:"timezone"`
	AsOf         string `parquet:"as_of"`
}

func writeStatisticsExport(ctx context.Context, dst io.Writer, format string, rows []map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	columns := []string{"chain", "dimension", "dataset", "token_address", "token_symbol", "token_decimals", "counterparty", "address", "bucket", "in_raw", "out_raw", "net_raw", "internal_raw", "self_raw", "event_count", "transaction_count", "missing_amount_rows", "failed_event_count", "first_seen", "last_seen", "share_percent", "data_version", "timezone", "as_of"}
	switch format {
	case "csv":
		writer := csv.NewWriter(dst)
		if err := writer.Write(columns); err != nil {
			return err
		}
		record := make([]string, len(columns))
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			fillExportCSVRecord(record, columns, row)
			if err := writer.Write(record); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	case "jsonl":
		return streamNDJSON(dst, rows)
	case "xlsx":
		book := excelize.NewFile()
		defer book.Close()
		for start, sheet := 0, 1; start < len(rows) || sheet == 1; sheet++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := start + 1_048_575
			if end > len(rows) {
				end = len(rows)
			}
			name := fmt.Sprintf("Sheet%d", sheet)
			if err := writeXLSXSheet(book, name, rows[start:end], columns, 0, end-start); err != nil {
				return err
			}
			last, err := excelize.CoordinatesToCellName(len(columns), end-start+1)
			if err != nil {
				return err
			}
			if err := book.AutoFilter(name, "A1:"+last, nil); err != nil {
				return err
			}
			start = end
		}
		return book.Write(dst)
	case "parquet":
		pw := parquet.NewGenericWriter[statisticsParquetRow](dst, parquet.Compression(&zstd.Codec{}))
		batch := make([]statisticsParquetRow, 0, 4096)
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			batch = append(batch, statisticsParquetRow{Chain: exportString(row, "chain"), Dataset: exportString(row, "dataset"), Dimension: exportString(row, "dimension"), TokenAddress: exportString(row, "token_address"), TokenSymbol: exportString(row, "token_symbol"), Decimals: exportUint64(row, "token_decimals"), Counterparty: exportString(row, "counterparty"), Address: exportString(row, "address"), Bucket: exportString(row, "bucket"), InRaw: exportString(row, "in_raw"), OutRaw: exportString(row, "out_raw"), NetRaw: exportString(row, "net_raw"), InternalRaw: exportString(row, "internal_raw"), SelfRaw: exportString(row, "self_raw"), Events: exportUint64(row, "event_count"), Transactions: exportUint64(row, "transaction_count"), Missing: exportUint64(row, "missing_amount_rows"), Failed: exportUint64(row, "failed_event_count"), First: exportString(row, "first_seen"), Last: exportString(row, "last_seen"), Share: exportString(row, "share_percent"), Version: exportString(row, "data_version"), Timezone: exportString(row, "timezone"), AsOf: exportString(row, "as_of")})
			if len(batch) == cap(batch) {
				if _, err := pw.Write(batch); err != nil {
					return err
				}
				batch = batch[:0]
			}
		}
		if len(batch) > 0 {
			if _, err := pw.Write(batch); err != nil {
				return err
			}
		}
		return pw.Close()
	}
	return fmt.Errorf("unsupported export format")
}

func (s *Server) exportFiles(ctx context.Context, id string) ([]exportDatasetFile, error) {
	var data []byte
	err := s.deps.Store.Pool.QueryRow(ctx, "SELECT file_manifest FROM exports WHERE id=$1", id).Scan(&data)
	if err != nil {
		return nil, err
	}
	files := []exportDatasetFile{}
	err = json.Unmarshal(data, &files)
	return files, err
}

func (s *Server) handleExportFileTicket(w http.ResponseWriter, r *http.Request) {
	item, ok := s.requireExportScope(r, chi.URLParam(r, "id"))
	if !ok {
		respondError(w, r, 404, "RESOURCE_NOT_FOUND", "导出不存在或无权访问", nil)
		return
	}
	if item["download_available"] != true {
		respondError(w, r, 409, "CONFLICT_STATE", "导出文件不可下载", nil)
		return
	}
	files, err := s.exportFiles(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, r, 503, "DATA_UNAVAILABLE", "文件清单暂不可用", nil)
		return
	}
	var file *exportDatasetFile
	for i := range files {
		if files[i].Dataset == chi.URLParam(r, "dataset") {
			file = &files[i]
			break
		}
	}
	if file == nil {
		respondError(w, r, 404, "RESOURCE_NOT_FOUND", "数据集文件不存在", nil)
		return
	}
	if _, _, err := s.statExportObject(r.Context(), file.ObjectRef); err != nil {
		respondError(w, r, 503, "DATA_OBJECT_MISSING", "数据集文件缺失", nil)
		return
	}
	p := PrincipalFrom(r.Context())
	ticket := uuid.NewString()
	_, err = s.deps.Store.Pool.Exec(r.Context(), `INSERT INTO export_tickets(id,export_id,user_id,organization_id,workspace_id,expires_at,file_dataset) VALUES($1,$2,$3,$4,$5,now()+interval '60 seconds',$6)`, ticket, chi.URLParam(r, "id"), p.IdentityID, p.OrganizationID, p.WorkspaceID, file.Dataset)
	if err != nil {
		respondError(w, r, 500, "INTERNAL_ERROR", "创建下载票据失败", nil)
		return
	}
	s.writeAuditNow(r, "export.download_ticket", "export", "export", chi.URLParam(r, "id"), map[string]any{"dataset": file.Dataset, "ttl_seconds": 60})
	respondOK(w, r, map[string]any{"ticket_id": ticket, "url": "/api/v2/exports/download/" + ticket, "expires_in_seconds": 60, "single_use": true})
}

func (s *Server) safeDatasetManifest(ctx context.Context, files []exportDatasetFile, available bool) []map[string]any {
	items := []map[string]any{}
	for _, f := range files {
		fileAvailable := available
		if fileAvailable {
			_, _, err := s.statExportObject(ctx, f.ObjectRef)
			fileAvailable = err == nil
		}
		items = append(items, map[string]any{"dataset": f.Dataset, "name": f.Name, "rows": f.Rows, "bytes": f.Bytes, "sha256": f.SHA256, "download_available": fileAvailable})
	}
	return items
}

func datasetExportFilter(f query.StatisticsFilter, dataset string, job *model.DownloadJob) query.StatisticsFilter {
	f.Datasets = []string{dataset}
	fromBlock, toBlock, fromTime, toTime := exportJobBounds(dataset, job)
	if fromTime != nil && (f.FromTime == nil || f.FromTime.Before(*fromTime)) {
		f.FromTime = fromTime
	}
	if toTime != nil && (f.ToTime == nil || f.ToTime.After(*toTime)) {
		f.ToTime = toTime
	}
	if fromBlock != nil {
		f.BlockFrom = fromBlock
	}
	if toBlock != nil {
		f.BlockTo = toBlock
	}
	return f
}

// Expired dataset files retain their manifest for history; only this export's
// validated keys are removed. Retries are bounded and never touch shared cache.
func (s *Server) cleanupDatasetExports(parent context.Context) error {
	if s.deps.Store == nil || s.deps.Raw == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	rows, err := s.deps.Store.Pool.Query(ctx, `SELECT id::text,object_ref,file_manifest FROM exports WHERE expires_at<now() AND file_manifest<>'[]'::jsonb AND files_deleted_at IS NULL ORDER BY expires_at LIMIT 50`)
	if err != nil {
		return err
	}
	type expired struct {
		id, key string
		files   []exportDatasetFile
	}
	items := []expired{}
	for rows.Next() {
		var item expired
		var raw []byte
		if err := rows.Scan(&item.id, &item.key, &raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &item.files); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	order, err := s.exportReadOrder()
	if err != nil {
		return err
	}
	for _, item := range items {
		prefix := "exports/" + item.id + "/files/"
		keys := map[string]bool{}
		for _, file := range item.files {
			if !strings.HasPrefix(file.ObjectRef, prefix) || strings.Contains(strings.TrimPrefix(file.ObjectRef, prefix), "/") {
				return fmt.Errorf("dataset cleanup key outside export")
			}
			keys[file.ObjectRef] = true
		}
		if item.key != "exports/"+item.id+".zip" && !keys[item.key] {
			return fmt.Errorf("dataset cleanup root outside export")
		}
		keys[item.key] = true
		for _, role := range order {
			bucket, err := s.exportBucketName(role)
			if err != nil {
				return err
			}
			for key := range keys {
				if err := s.deps.Raw.Client().RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
					return err
				}
			}
		}
		if _, err := s.deps.Store.Pool.Exec(ctx, "UPDATE exports SET files_deleted_at=now() WHERE id=$1 AND expires_at<now()", item.id); err != nil {
			return err
		}
		s.writeAuditCtx(ctx, "export.files_expired", "export", "export", item.id, map[string]any{"files": len(item.files)})
	}
	return nil
}
