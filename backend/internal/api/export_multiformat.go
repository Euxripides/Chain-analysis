package api

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// preparedDatasetParts keeps a single exact ClickHouse snapshot for every
// format. A sequential preparation would run four FINAL scans and repeat token
// metadata hydration for the same rows.
type preparedDatasetParts struct {
	dataset string
	paths   map[string]string
	rows    int64
	columns []string
}

func removePreparedDatasetParts(parts []preparedDatasetParts) {
	for _, part := range parts {
		for _, path := range part.paths {
			_ = os.Remove(path)
		}
	}
}

func (s *Server) buildAllFormatParts(ctx context.Context, job *model.DownloadJob,
	chain, wsID, address, dataset string) (preparedDatasetParts, error) {
	shared, err := s.buildSharedFormatParts(ctx, job, chain, wsID, address, dataset)
	if err != nil {
		return shared, err
	}
	xlsx, err := buildPreparedXLSXParts(ctx, []preparedDatasetParts{shared})
	if err != nil {
		removePreparedDatasetParts([]preparedDatasetParts{shared})
		return preparedDatasetParts{}, err
	}
	shared.paths["xlsx"] = xlsx[0].paths["xlsx"]
	return shared, nil
}

func (s *Server) buildSharedFormatParts(ctx context.Context, job *model.DownloadJob,
	chain, wsID, address, dataset string) (result preparedDatasetParts, err error) {
	result.dataset = dataset
	result.paths = make(map[string]string, 4)
	files := make(map[string]*os.File, 4)
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
		if err != nil {
			removePreparedDatasetParts([]preparedDatasetParts{result})
		}
	}()
	for _, format := range []string{"csv", "jsonl", "parquet"} {
		file, createErr := os.CreateTemp("", "export-shared-*-."+format)
		if createErr != nil {
			return result, createErr
		}
		files[format] = file
		result.paths[format] = file.Name()
	}
	csvBuffer := bufio.NewWriterSize(files["csv"], 1<<20)
	jsonBuffer := bufio.NewWriterSize(files["jsonl"], 1<<20)
	csvWriter := csv.NewWriter(csvBuffer)
	jsonWriter := json.NewEncoder(jsonBuffer)
	parquetWriter := parquet.NewGenericWriter[parquetExportRow](files["parquet"],
		parquet.Compression(&zstd.Codec{}), parquet.MaxRowsPerRowGroup(65_536))
	parquetClosed := false
	defer func() {
		if !parquetClosed {
			_ = parquetWriter.Close()
		}
	}()
	var cols, csvRecord []string
	batch := make([]map[string]any, 0, 4096)
	writeBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		unique := filterGPTActionExportToken(job, batch)
		s.attachTokenMetadata(ctx, chain, unique)
		// Task-detail prepared exports intentionally use the same no-metadata,
		// no-decoded-input snapshot as the one-format path.
		if len(unique) > 0 && cols == nil {
			cols = mapKeys(unique[0])
			csvRecord = make([]string, len(cols))
			if err := csvWriter.Write(cols); err != nil {
				return err
			}
		}
		converted := make([]parquetExportRow, 0, len(unique))
		for _, row := range unique {
			fillExportCSVRecord(csvRecord, cols, row)
			if err := csvWriter.Write(csvRecord); err != nil {
				return err
			}
			if err := jsonWriter.Encode(row); err != nil {
				return err
			}
			converted = append(converted, toParquetExportRow(dataset, row))
		}
		if len(converted) > 0 {
			n, err := parquetWriter.Write(converted)
			if err != nil {
				return err
			}
			if n != len(converted) {
				return io.ErrShortWrite
			}
		}
		result.rows += int64(len(unique))
		batch = batch[:0]
		return nil
	}
	fromBlock, toBlock, fromTime, toTime := exportJobBounds(dataset, job)
	scanStarted := time.Now()
	err = s.query.StreamRowsForExportRange(ctx, dataset, chain, address, fromBlock, toBlock, fromTime, toTime,
		func(row map[string]any) error {
			batch = append(batch, row)
			if len(batch) == cap(batch) {
				return writeBatch()
			}
			return nil
		})
	if err != nil {
		return result, err
	}
	if err = writeBatch(); err != nil {
		return result, err
	}
	scanElapsed := time.Since(scanStarted)
	csvWriter.Flush()
	if err = csvWriter.Error(); err != nil {
		return result, err
	}
	if err = csvBuffer.Flush(); err != nil {
		return result, err
	}
	if err = jsonBuffer.Flush(); err != nil {
		return result, err
	}
	if err = parquetWriter.Close(); err != nil {
		return result, err
	}
	parquetClosed = true
	for _, format := range []string{"csv", "jsonl", "parquet"} {
		if err = files[format].Close(); err != nil {
			return result, err
		}
	}
	result.columns = append([]string(nil), cols...)
	if s.deps.Log != nil {
		jobID := ""
		if job != nil {
			jobID = job.ID
		}
		s.deps.Log.Info("shared export parts encoded", "job_id", jobID,
			"dataset", dataset, "rows", result.rows,
			"scan_encode_ms", scanElapsed.Milliseconds())
	}
	return result, nil
}

// Each result owns only its XLSX file. Input maps and paths remain read-only
// while CSV/JSONL/Parquet upload concurrently from the shared scan.
func buildPreparedXLSXParts(ctx context.Context, shared []preparedDatasetParts) ([]preparedDatasetParts, error) {
	created := make([]preparedDatasetParts, 0, len(shared))
	complete := false
	defer func() {
		if !complete {
			removePreparedDatasetParts(created)
		}
	}()
	for _, input := range shared {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := os.CreateTemp("", "export-derived-*-.xlsx")
		if err != nil {
			return nil, err
		}
		created = append(created, preparedDatasetParts{dataset: input.dataset, rows: input.rows,
			columns: append([]string(nil), input.columns...), paths: map[string]string{"xlsx": file.Name()}})
		writeErr := writeStreamedXLSXContext(ctx, file, input.paths["jsonl"], input.rows, input.columns)
		closeErr := file.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	complete = true
	return created, nil
}

func (s *Server) uploadAllFormatParts(ctx context.Context, exp *model.Export, job *model.DownloadJob,
	address, format string, parts []preparedDatasetParts) (int64, string, exportLocation, error) {
	files := []exportDatasetFile{}
	for _, part := range parts {
		f, err := s.uploadDatasetFile(ctx, exp.ID, part.dataset, format, part.rows, part.paths[format])
		if err != nil {
			return 0, "", exportLocation{}, err
		}
		files = append(files, f)
	}
	manifest, _ := json.Marshal(files)
	if _, err := s.deps.Store.Pool.Exec(ctx, "UPDATE exports SET file_manifest=$2::jsonb WHERE id=$1", exp.ID, manifest); err != nil {
		return 0, "", exportLocation{}, err
	}
	zipIt := len(parts) > 1
	key := fmt.Sprintf("exports/%s.%s", exp.ID, extFor(format))
	var rows int64
	for _, part := range parts {
		rows += part.rows
	}
	if !zipIt {
		sha, loc, err := s.uploadPreparedFile(ctx, exp.ID, key, parts[0].paths[format], contentType(format))
		return rows, sha, loc, err
	}
	key = fmt.Sprintf("exports/%s.zip", exp.ID)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		writeErr := writeSharedExportZIP(pw, exp, job, address, format, parts)
		_ = pw.CloseWithError(writeErr)
		done <- writeErr
	}()
	uploadStarted := time.Now()
	bytes, loc, putErr := s.putExportStream(ctx, exp.ID, key, pr, -1, "application/zip")
	_ = pr.CloseWithError(putErr)
	writeErr := <-done
	if putErr != nil {
		return 0, "", loc, putErr
	}
	if writeErr != nil {
		return 0, "", loc, writeErr
	}
	uploadElapsed := time.Since(uploadStarted)
	readbackStarted := time.Now()
	sha, err := s.objectSHA256(ctx, key)
	if err == nil && s.deps.Log != nil {
		s.deps.Log.Info("prepared export object verified", "export_id", exp.ID,
			"format", format, "object_bucket", loc.Name, "object_ref", key, "bytes", bytes,
			"upload_size_verify_ms", uploadElapsed.Milliseconds(),
			"sha256_readback_ms", time.Since(readbackStarted).Milliseconds())
	}
	return rows, sha, loc, err
}

// Stream directly to multipart upload. Building a second multi-gigabyte ZIP
// on local disk before upload added a full copy pass and doubled peak spool.
func writeSharedExportZIP(dst io.Writer, exp *model.Export, job *model.DownloadJob,
	address, format string, parts []preparedDatasetParts) error {
	zw := newFastZipWriter(dst)
	for _, part := range parts {
		name := fmt.Sprintf("%s.%s", part.dataset, extFor(format))
		header := &zip.FileHeader{Name: name, Method: zipEntryMethod(format)}
		header.SetModTime(time.Now())
		entry, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(part.paths[format])
		if err != nil {
			return err
		}
		_, err = io.CopyBuffer(entry, file, make([]byte, 1<<20))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return zw.Close()
}
