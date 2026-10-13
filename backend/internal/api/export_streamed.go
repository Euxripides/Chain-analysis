package api

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/xuri/excelize/v2"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// A single dense address is read once per dataset without the legacy two
// million row cap. CSV, JSONL, and Parquet stream into the object or ZIP;
// XLSX retains a bounded local spool for its exact sheet ranges and filters.
func (s *Server) writeStreamedSingleAddressExport(ctx context.Context, exp *model.Export, job *model.DownloadJob,
	key, chain, wsID, address string, datasets []string, format string,
	includeMeta, zipIt, includeDecodedInput bool) (int64, string, exportLocation, error) {
	if len(datasets) == 1 && !zipIt && format != "xlsx" {
		return s.streamQueryExportToObject(ctx, exp.ID, key, job, chain, wsID,
			address, datasets[0], format, includeMeta, includeDecodedInput)
	}
	if zipIt && format != "xlsx" {
		return s.streamQueryZIPToObject(ctx, exp, job, key, chain, wsID,
			address, datasets, format, includeMeta, includeDecodedInput)
	}
	parts := make([]streamedAddressPart, 0, len(datasets))
	defer func() {
		for _, p := range parts {
			_ = os.Remove(p.path)
		}
	}()
	var total int64
	for _, ds := range datasets {
		path, count, err := s.buildStreamedExportPart(ctx, job, chain, wsID, address, ds, format, includeMeta, includeDecodedInput)
		if err != nil {
			return 0, "", exportLocation{Key: key}, err
		}
		parts = append(parts, streamedAddressPart{dataset: ds, path: path, rows: count})
		total += count
	}
	if !zipIt {
		sha, loc, err := s.uploadPreparedFile(ctx, exp.ID, key, parts[0].path, contentType(format))
		return total, sha, loc, err
	}
	reader, writer := io.Pipe()
	written := make(chan error, 1)
	go func() {
		err := writeStreamedSingleAddressZIP(writer, exp, job, chain, address, datasets, format, includeDecodedInput, parts)
		_ = writer.CloseWithError(err)
		written <- err
	}()
	_, loc, putErr := s.putExportStream(ctx, exp.ID, key, reader, -1, "application/zip")
	_ = reader.CloseWithError(putErr)
	writeErr := <-written
	if putErr != nil {
		return 0, "", loc, putErr
	}
	if writeErr != nil {
		return 0, "", loc, writeErr
	}
	sha, err := s.objectSHA256(ctx, key)
	return total, sha, loc, err
}

// streamQueryZIPToObject writes each dataset straight into its final ZIP
// entry. The object store sees one stream and no full-size dataset or ZIP
// needs a temporary disk copy.
func (s *Server) streamQueryZIPToObject(ctx context.Context, exp *model.Export, job *model.DownloadJob,
	key, chain, wsID, address string, datasets []string, format string,
	includeMeta, includeDecodedInput bool) (int64, string, exportLocation, error) {
	type result struct {
		rows int64
		err  error
	}
	reader, writer := io.Pipe()
	written := make(chan result, 1)
	go func() {
		rows, err := writeStreamedSingleAddressZIPEntries(writer, exp, job, chain, address,
			datasets, format, includeDecodedInput, func(dataset string, entry io.Writer) (int64, error) {
				count, _, err := s.writeExportDatasetRows(ctx, entry, job, chain, wsID,
					address, dataset, format, includeMeta, includeDecodedInput)
				return count, err
			})
		_ = writer.CloseWithError(err)
		written <- result{rows: rows, err: err}
	}()
	_, loc, putErr := s.putExportStream(ctx, exp.ID, key, reader, -1, "application/zip")
	_ = reader.CloseWithError(putErr)
	out := <-written
	if putErr != nil {
		return 0, "", loc, putErr
	}
	if out.err != nil {
		return 0, "", loc, out.err
	}
	sha, err := s.objectSHA256(ctx, key)
	return out.rows, sha, loc, err
}

func (s *Server) streamQueryExportToObject(ctx context.Context, exportID, key string,
	job *model.DownloadJob, chain, wsID, address, dataset, format string,
	includeMeta, includeDecodedInput bool) (int64, string, exportLocation, error) {
	type result struct {
		rows int64
		err  error
	}
	reader, writer := io.Pipe()
	written := make(chan result, 1)
	go func() {
		rows, _, err := s.writeExportDatasetRows(ctx, writer, job, chain, wsID,
			address, dataset, format, includeMeta, includeDecodedInput)
		_ = writer.CloseWithError(err)
		written <- result{rows: rows, err: err}
	}()
	_, loc, putErr := s.putExportStream(ctx, exportID, key, reader, -1, contentType(format))
	_ = reader.CloseWithError(putErr)
	out := <-written
	if putErr != nil {
		return 0, "", loc, putErr
	}
	if out.err != nil {
		return 0, "", loc, out.err
	}
	sha, err := s.objectSHA256(ctx, key)
	return out.rows, sha, loc, err
}

type streamedAddressPart struct {
	dataset string
	path    string
	rows    int64
}

func writeStreamedSingleAddressZIP(dst io.Writer, exp *model.Export, job *model.DownloadJob,
	chain, address string, datasets []string, format string, includeDecodedInput bool, parts []streamedAddressPart) error {
	byDataset := make(map[string]streamedAddressPart, len(parts))
	for _, part := range parts {
		byDataset[part.dataset] = part
	}
	_, err := writeStreamedSingleAddressZIPEntries(dst, exp, job, chain, address,
		datasets, format, includeDecodedInput, func(dataset string, entry io.Writer) (int64, error) {
			part, ok := byDataset[dataset]
			if !ok {
				return 0, fmt.Errorf("missing export dataset %q", dataset)
			}
			file, err := os.Open(part.path)
			if err != nil {
				return 0, err
			}
			_, copyErr := io.CopyBuffer(entry, file, make([]byte, 1<<20))
			closeErr := file.Close()
			if copyErr != nil {
				return 0, copyErr
			}
			return part.rows, closeErr
		})
	return err
}

func writeStreamedSingleAddressZIPEntries(dst io.Writer, exp *model.Export, job *model.DownloadJob,
	chain, address string, datasets []string, format string, includeDecodedInput bool,
	writeEntry func(dataset string, entry io.Writer) (int64, error)) (int64, error) {
	zw := newFastZipWriter(dst)
	manifest, _ := json.Marshal(map[string]any{
		"export_id": exp.ID, "created_at": time.Now().UTC().Format(time.RFC3339),
		"chain": chain, "addresses": []string{address}, "datasets": datasets,
		"format": format, "schema_version": 3, "zip": true,
		"include_decoded_input": includeDecodedInput, "job_id": exp.JobID,
		"partial":    job != nil && job.Status != model.JobCompleted,
		"job_status": statusOfJob(job),
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
	mw, err := zw.Create("manifest.json")
	if err != nil {
		return 0, err
	}
	if _, err := mw.Write(manifest); err != nil {
		return 0, err
	}
	var rows int64
	for _, dataset := range datasets {
		name := fmt.Sprintf("addresses/%s/%s.%s", address, dataset, extFor(format))
		fh := &zip.FileHeader{Name: name, Method: zipEntryMethod(format)}
		fh.SetModTime(time.Now())
		entry, err := zw.CreateHeader(fh)
		if err != nil {
			return 0, err
		}
		count, err := writeEntry(dataset, entry)
		if err != nil {
			return 0, err
		}
		rows += count
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	return rows, nil
}

func newFastZipWriter(dst io.Writer) *zip.Writer {
	zw := zip.NewWriter(dst)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	return zw
}

func (s *Server) uploadPreparedFile(ctx context.Context, exportID, key, path, mime string) (string, exportLocation, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", exportLocation{Key: key}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", exportLocation{Key: key}, err
	}
	uploadStarted := time.Now()
	_, loc, err := s.putExportStream(ctx, exportID, key, f, info.Size(), mime)
	if err != nil {
		return "", loc, err
	}
	uploadElapsed := time.Since(uploadStarted)
	readbackStarted := time.Now()
	sha, err := s.objectSHA256(ctx, key)
	if err == nil && s.deps.Log != nil {
		s.deps.Log.Info("prepared export object verified", "export_id", exportID,
			"object_bucket", loc.Name, "object_ref", key, "bytes", info.Size(),
			"upload_size_verify_ms", uploadElapsed.Milliseconds(),
			"sha256_readback_ms", time.Since(readbackStarted).Milliseconds())
	}
	return sha, loc, err
}

func (s *Server) buildStreamedExportPart(ctx context.Context, job *model.DownloadJob,
	chain, wsID, address, dataset, format string, includeMeta, includeDecodedInput bool) (string, int64, error) {
	f, err := os.CreateTemp("", "export-part-*."+extFor(format))
	if err != nil {
		return "", 0, err
	}
	path := f.Name()
	failed := true
	defer func() {
		_ = f.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	count, cols, err := s.writeExportDatasetRows(ctx, f, job, chain, wsID,
		address, dataset, format, includeMeta, includeDecodedInput)
	if err != nil {
		return "", 0, err
	}
	if err := f.Close(); err != nil {
		return "", 0, err
	}
	if format == "xlsx" {
		book, err := os.CreateTemp("", "export-book-*.xlsx")
		if err != nil {
			return "", 0, err
		}
		bookPath := book.Name()
		if err := writeStreamedXLSXContext(ctx, book, path, count, cols); err != nil {
			_ = book.Close()
			_ = os.Remove(bookPath)
			return "", 0, err
		}
		if err := book.Close(); err != nil {
			_ = os.Remove(bookPath)
			return "", 0, err
		}
		_ = os.Remove(path)
		path = bookPath
	}
	failed = false
	return path, count, nil
}

// writeExportDatasetRows retains the existing columns and enrichment while
// allowing CSV, JSONL and Parquet query exports to write directly into an
// object upload instead of first materializing a local multi-gigabyte file.
// XLSX uses the JSONL spool because its sheets need exact final row counts.
func (s *Server) writeExportDatasetRows(ctx context.Context, dst io.Writer, job *model.DownloadJob,
	chain, wsID, address, dataset, format string, includeMeta, includeDecodedInput bool) (int64, []string, error) {
	return s.writeExportDatasetRowsSource(ctx, dst, job, chain, wsID, address, dataset, format, includeMeta, includeDecodedInput, nil)
}

func (s *Server) writeExportDatasetRowsSource(ctx context.Context, dst io.Writer, job *model.DownloadJob,
	chain, wsID, address, dataset, format string, includeMeta, includeDecodedInput bool, source func(func(map[string]any) error) error) (int64, []string, error) {
	var (
		err     error
		bw      *bufio.Writer
		cw      *csv.Writer
		encoder *json.Encoder
		pw      *parquet.GenericWriter[parquetExportRow]
		cols    []string
	)
	if format == "csv" || format == "jsonl" || format == "xlsx" {
		bw = bufio.NewWriterSize(dst, 1<<20)
	}
	switch format {
	case "csv":
		cw = csv.NewWriter(bw)
	case "jsonl", "xlsx":
		encoder = json.NewEncoder(bw)
	case "parquet":
		pw = parquet.NewGenericWriter[parquetExportRow](dst,
			parquet.Compression(&zstd.Codec{}), parquet.MaxRowsPerRowGroup(65_536))
	default:
		return 0, nil, fmt.Errorf("unsupported export format %q", format)
	}
	var csvRecord []string
	batch := make([]map[string]any, 0, 4096)
	var count int64
	writeBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		// The FINAL query resolves ReplacingMergeTree versions by its sorting
		// key. Keeping every key in a Go map would scale memory with the entire
		// address history and defeat streaming for million-row exports.
		unique := filterGPTActionExportToken(job, batch)
		s.attachTokenMetadata(ctx, chain, unique)
		if includeMeta {
			s.annotateRows(ctx, chain, address, unique, wsID)
		}
		s.attachDecodedInput(ctx, chain, dataset, unique, includeDecodedInput)
		if len(unique) > 0 && cols == nil {
			cols = mapKeys(unique[0])
			if cw != nil {
				csvRecord = make([]string, len(cols))
				if err := cw.Write(cols); err != nil {
					return err
				}
			}
		}
		for _, row := range unique {
			switch format {
			case "csv":
				fillExportCSVRecord(csvRecord, cols, row)
				if err := cw.Write(csvRecord); err != nil {
					return err
				}
			case "jsonl", "xlsx":
				if err := encoder.Encode(row); err != nil {
					return err
				}
			}
		}
		if pw != nil && len(unique) > 0 {
			converted := make([]parquetExportRow, len(unique))
			for i, row := range unique {
				converted[i] = toParquetExportRow(dataset, row)
			}
			n, err := pw.Write(converted)
			if err != nil {
				return err
			}
			if n != len(converted) {
				return io.ErrShortWrite
			}
		}
		count += int64(len(unique))
		batch = batch[:0]
		return nil
	}
	fromBlock, toBlock, fromTime, toTime := exportJobBounds(dataset, job)
	visit := func(row map[string]any) error {
		batch = append(batch, row)
		if len(batch) == cap(batch) {
			return writeBatch()
		}
		return nil
	}
	if source != nil {
		err = source(visit)
	} else {
		err = s.query.StreamRowsForExportRange(ctx, dataset, chain, address, fromBlock, toBlock, fromTime, toTime, visit)
	}
	if err == nil {
		err = writeBatch()
	}
	if err != nil {
		return 0, nil, err
	}
	if cols == nil {
		cols = []string{"chain", "tx_hash", "timestamp", "block_number", "from_address", "to_address", "amount_raw", "token_address", "token_symbol", "token_decimals"}
		if cw != nil {
			if err := cw.Write(cols); err != nil {
				return 0, nil, err
			}
		}
	}
	if cw != nil {
		cw.Flush()
		if err := cw.Error(); err != nil {
			return 0, nil, err
		}
	}
	if bw != nil {
		if err := bw.Flush(); err != nil {
			return 0, nil, err
		}
	}
	if pw != nil {
		if err := pw.Close(); err != nil {
			return 0, nil, err
		}
	}
	return count, cols, nil
}

func writeStreamedXLSX(dst io.Writer, spoolPath string, count int64, cols []string) error {
	return writeStreamedXLSXContext(context.Background(), dst, spoolPath, count, cols)
}

func writeStreamedXLSXContext(ctx context.Context, dst io.Writer, spoolPath string, count int64, cols []string) error {
	return writeStreamedXLSXWithSheetLimitContext(ctx, dst, spoolPath, count, cols, 250_000)
}

func writeStreamedXLSXWithSheetLimit(dst io.Writer, spoolPath string, count int64, cols []string, dataRowsPerSheet int64) error {
	return writeStreamedXLSXWithSheetLimitContext(context.Background(), dst, spoolPath, count, cols, dataRowsPerSheet)
}

func writeStreamedXLSXWithSheetLimitContext(ctx context.Context, dst io.Writer, spoolPath string, count int64, cols []string, dataRowsPerSheet int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dataRowsPerSheet <= 0 || dataRowsPerSheet > 1_048_575 {
		return fmt.Errorf("invalid XLSX sheet limit %d", dataRowsPerSheet)
	}
	book := excelize.NewFile()
	defer book.Close()
	// Excelize's File.Write calls WriteToBuffer, which retains the entire XLSX
	// in Go heap. Its public ZipWriter hook lets the archive write to the
	// destination directly. Each sheet is kept well below the 4 GiB ZIP64
	// local-header rewrite threshold, which WriteToBuffer normally handles.
	book.SetZipWriter(func(io.Writer) excelize.ZipWriter {
		return newFastZipWriter(streamedXLSXContextWriter{ctx: ctx, dst: dst})
	})
	if count == 0 {
		if len(cols) > 0 {
			if err := book.SetSheetRow("Sheet1", "A1", &cols); err != nil {
				return err
			}
			last, err := excelize.CoordinatesToCellName(len(cols), 1)
			if err != nil {
				return err
			}
			if err := book.AutoFilter("Sheet1", "A1:"+last, nil); err != nil {
				return err
			}
		}
		return book.Write(dst)
	}
	spool, err := os.Open(spoolPath)
	if err != nil {
		return err
	}
	defer spool.Close()
	decoder := json.NewDecoder(bufio.NewReaderSize(spool, 1<<20))
	decoder.UseNumber()
	var written int64
	for sheetIndex := 0; written < count; sheetIndex++ {
		name := fmt.Sprintf("Sheet%d", sheetIndex+1)
		if sheetIndex > 0 {
			if _, err := book.NewSheet(name); err != nil {
				return err
			}
		}
		dataRows := min(dataRowsPerSheet, count-written)
		last, err := excelize.CoordinatesToCellName(len(cols), int(dataRows+1))
		if err != nil {
			return err
		}
		if err := book.SetSheetDimension(name, "A1:"+last); err != nil {
			return err
		}
		if err := book.AutoFilter(name, "A1:"+last, nil); err != nil {
			return err
		}
		sw, err := book.NewStreamWriter(name)
		if err != nil {
			return err
		}
		if err := sw.SetRow("A1", colsToAny(cols)); err != nil {
			return err
		}
		for rowIndex := int64(0); rowIndex < dataRows; rowIndex++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			var row map[string]any
			if err := decoder.Decode(&row); err != nil {
				return err
			}
			rec := make([]any, len(cols))
			for i, col := range cols {
				rec[i] = xlsxSpoolValue(row[col])
			}
			cell, err := excelize.CoordinatesToCellName(1, int(rowIndex+2))
			if err != nil {
				return err
			}
			if err := sw.SetRow(cell, rec); err != nil {
				return err
			}
		}
		if err := sw.Flush(); err != nil {
			return err
		}
		written += dataRows
	}
	return book.Write(io.Discard)
}

type streamedXLSXContextWriter struct {
	ctx context.Context
	dst io.Writer
}

func (w streamedXLSXContextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.dst.Write(p)
}

// Excel preserves at most 15 significant numeric digits. Keep ordinary
// integer indices numeric, but write larger or fractional JSON numbers as
// their exact text. Amount strings already pass through unchanged.
func xlsxSpoolValue(value any) any {
	if number, ok := value.(json.Number); ok {
		if n, err := number.Int64(); err == nil && n >= -999999999999999 && n <= 999999999999999 {
			return n
		}
		return number.String()
	}
	return value
}
