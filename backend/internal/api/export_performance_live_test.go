package api

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/shopspring/decimal"
)

// This opt-in check uses a dedicated disposable ClickHouse with a seeded QA
// marker. It never creates/deletes tables or falls back to application config.
func TestExportPerformanceClickHouse(t *testing.T) {
	dsn := os.Getenv("EXPORT_PERF_TEST_DSN")
	if dsn == "" {
		t.Skip("EXPORT_PERF_TEST_DSN not set; isolated ClickHouse fixture required")
	}
	ctx := context.Background()
	conn, err := clickhouse.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var fixture string
	if err := conn.DB().QueryRow(ctx, "SELECT marker FROM blockchain.qa_export_fixture LIMIT 1").Scan(&fixture); err != nil || fixture != "chainsight-perf-20261003-v1" {
		t.Fatalf("isolated fixture marker missing: %q %v", fixture, err)
	}
	srv := &Server{query: query.New(conn)}
	address := "0x1111111111111111111111111111111111111111"
	const count = int64(1000000)
	var exact uint64
	if err := conn.DB().QueryRow(ctx, "SELECT count() FROM blockchain.token_transfers FINAL WHERE chain='bsc' AND from_address=?", address).Scan(&exact); err != nil || exact != uint64(count) {
		t.Fatalf("fixture count %d: %v", exact, err)
	}
	path := filepath.Join(t.TempDir(), "token_transfers.csv")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	rows, _, writeErr := srv.writeExportDatasetRows(ctx, file, nil, "bsc", "", address, "token_transfers", "csv", false, false)
	closeErr := file.Close()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if writeErr != nil || closeErr != nil || rows != count {
		t.Fatalf("export rows=%d write=%v close=%v", rows, writeErr, closeErr)
	}
	read, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	hash := sha256.New()
	csvReader := csv.NewReader(bufio.NewReaderSize(io.TeeReader(read, hash), 1<<20))
	header, err := csvReader.Read()
	if err != nil {
		t.Fatal(err)
	}
	columns := map[string]int{}
	for i, name := range header {
		columns[name] = i
	}
	for _, field := range []string{"chain", "block_number", "timestamp", "tx_hash", "log_index", "token_address", "token_symbol", "token_decimals", "amount_raw", "amount"} {
		if _, ok := columns[field]; !ok {
			t.Fatalf("missing column %s", field)
		}
	}
	baseRaw, _ := new(big.Int).SetString("1234567890123456789012345678901234567890", 10)
	var verified int64
	for {
		record, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		n := count - 1 - verified
		if n < 0 {
			t.Fatal("extra exported rows")
		}
		value := func(key string) string { return record[columns[key]] }
		// Unique timestamps make the order an independent no-omission/no-duplicate check.
		wantRaw := new(big.Int).Add(baseRaw, big.NewInt(n))
		wantSymbol := "SAME"
		if n%1000 == 0 {
			wantRaw.Add(wantRaw, big.NewInt(100))
			wantSymbol = "ALIAS"
		}
		wantAmount := decimal.NewFromBigInt(wantRaw, -18).String()
		if value("chain") != "bsc" || value("block_number") != strconv.FormatInt(9007199254740993+n, 10) || value("tx_hash") != fmt.Sprintf("0x%064x", n/2) || value("log_index") != strconv.FormatInt(n%2, 10) || value("token_symbol") != wantSymbol || value("amount_raw") != wantRaw.String() || value("amount") != wantAmount {
			t.Fatalf("row %d changed: %v", verified, record)
		}
		verified++
	}
	if verified != count {
		t.Fatalf("verified %d want %d", verified, count)
	}
	info, err := read.Stat()
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]any{"rows": rows, "verified_rows": verified, "csv_bytes": info.Size(), "csv_sha256": hex.EncodeToString(hash.Sum(nil)), "write_ms": float64(elapsed.Nanoseconds()) / 1e6, "allocated_bytes": after.TotalAlloc - before.TotalAlloc, "mallocs": after.Mallocs - before.Mallocs, "scope": "ClickHouse query through final CSV file close; excludes object upload and download"}
	raw, _ := json.Marshal(result)
	t.Logf("EXPORT_PERF_RESULT %s", raw)
	// Exercise the real query cancellation/callback path, then acquire a new
	// scan slot. An interrupted export must not block subsequent work.
	stop := errors.New("stop at first row")
	err = srv.query.StreamRowsForExportRange(ctx, "token_transfers", "bsc", address, nil, nil, nil, nil, func(map[string]any) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("callback error lost: %v", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	err = srv.query.StreamRowsForExportRange(cancelCtx, "token_transfers", "bsc", address, nil, nil, nil, nil, func(map[string]any) error { t.Fatal("cancelled scan visited data"); return nil })
	if err == nil {
		t.Fatal("cancelled scan succeeded")
	}
	// ZIP generation keeps precisely one file per dataset. Use a bounded job
	// range across the 4,096-row flush boundary and validate every entry's CRC.
	from, to := int64(9007199254740993), int64(9007199254740993+8192)
	job := &model.DownloadJob{Status: model.JobCompleted, RangeType: "block", FromBlock: &from, ToBlock: &to}
	zipPath := filepath.Join(t.TempDir(), "export.zip")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zipRows, err := writeStreamedSingleAddressZIPEntries(zipFile, &model.Export{ID: "qa-export"}, job, "bsc", address, []string{"token_transfers"}, "csv", false, func(ds string, w io.Writer) (int64, error) {
		n, _, err := srv.writeExportDatasetRows(ctx, w, job, "bsc", "", address, ds, "csv", false, false)
		return n, err
	})
	closeErr = zipFile.Close()
	if err != nil || closeErr != nil || zipRows != 8193 {
		t.Fatalf("ZIP rows=%d err=%v close=%v", zipRows, err, closeErr)
	}
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 2 {
		t.Fatalf("ZIP contains %d files, want manifest and one dataset", len(z.File))
	}
	for _, entry := range z.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
