package api

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/go-chi/chi/v5"
	"github.com/parquet-go/parquet-go"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
)

// Opt-in end-to-end comparison of publication scheduling, using identical
// encoders, exact FINAL queries and two upload slots in both cases. The legacy
// ordering holds every upload until XLSX encoding finishes. Downloads run after
// preparation timing to avoid adding an unequal competing storage workload.
func TestExportReadinessPerformanceLive(t *testing.T) {
	if os.Getenv("EXPORT_READINESS_PERF") != "1" || os.Getenv("EXPORT_PERF_TEST_DSN") == "" {
		t.Skip("explicit isolated ClickHouse/PG/S3 performance settings required")
	}
	conn, err := clickhouse.New(context.Background(), os.Getenv("EXPORT_PERF_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var marker string
	if err := conn.DB().QueryRow(context.Background(), "SELECT marker FROM blockchain.qa_export_fixture LIMIT 1").Scan(&marker); err != nil || marker != "chainsight-perf-20261003-v1" {
		t.Fatalf("isolated fixture: %q %v", marker, err)
	}
	count, runs := int64(1000000), 3
	if value := os.Getenv("EXPORT_READINESS_PERF_ROWS"); value != "" {
		count, err = strconv.ParseInt(value, 10, 64)
		if err != nil || count < 0 || count > 1000000 {
			t.Fatal("invalid fixture row count")
		}
	}
	if value := os.Getenv("EXPORT_READINESS_PERF_RUNS"); value != "" {
		runs, err = strconv.Atoi(value)
		if err != nil || runs < 1 || runs > 5 {
			t.Fatal("invalid repetitions")
		}
	}
	for round := 0; round < runs; round++ {
		order := []string{"legacy", "overlap"}
		if round%2 == 1 {
			order[0], order[1] = order[1], order[0]
		}
		for _, mode := range order {
			t.Run(fmt.Sprintf("%s-%d-%d", mode, count, round+1), func(t *testing.T) {
				srv, job, group, principal := newExportReadinessFixture(t, []string{"token_transfers"})
				srv.query = query.New(conn)
				address := "0x1111111111111111111111111111111111111111"
				from, to := int64(9007199254740993), int64(9007199254740993+count-1)
				if count == 0 {
					from, to = 1, 2
				}
				job.FromBlock, job.ToBlock = &from, &to
				if _, err := srv.deps.Store.Pool.Exec(context.Background(), `UPDATE download_jobs SET from_block=$2,to_block=$3 WHERE id=$1`, job.ID, from, to); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
				defer cancel()
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				start := time.Now()
				var part preparedDatasetParts
				if mode == "legacy" {
					part, err = srv.buildAllFormatParts(ctx, job, "bsc", principal.WorkspaceID, address, "token_transfers")
				} else {
					part, err = srv.buildSharedFormatParts(ctx, job, "bsc", principal.WorkspaceID, address, "token_transfers")
				}
				if err != nil {
					t.Fatal(err)
				}
				defer removePreparedDatasetParts([]preparedDatasetParts{part})
				if part.rows != count {
					t.Fatalf("scan rows %d want %d", part.rows, count)
				}
				encodedMS := float64(time.Since(start).Nanoseconds()) / 1e6
				readyMS := map[string]float64{}
				var mu sync.Mutex
				publish := func(ctx context.Context, prep exportPreparation, parts []preparedDatasetParts) error {
					if err := srv.publishPreparedExportFormat(ctx, prep, job, address, parts); err != nil {
						return err
					}
					mu.Lock()
					readyMS[prep.format] = float64(time.Since(start).Nanoseconds()) / 1e6
					mu.Unlock()
					return nil
				}
				if mode == "legacy" {
					work, workCtx := errgroup.WithContext(ctx)
					work.SetLimit(2)
					for _, prep := range group {
						prep := prep
						work.Go(func() error { return publish(workCtx, prep, []preparedDatasetParts{part}) })
					}
					err = work.Wait()
				} else {
					err = runPreparedExportFormats(ctx, group, []preparedDatasetParts{part}, buildPreparedXLSXParts, publish)
				}
				if err != nil {
					t.Fatal(err)
				}
				allMS := float64(time.Since(start).Nanoseconds()) / 1e6
				runtime.ReadMemStats(&after)
				downloads := map[string]any{}
				for _, format := range []string{"csv", "jsonl", "parquet", "xlsx"} {
					var id string
					if err := srv.deps.Store.Pool.QueryRow(ctx, `SELECT export_id::text FROM export_preparations WHERE job_id=$1 AND format=$2 AND status='READY'`, job.ID, format).Scan(&id); err != nil {
						t.Fatal(err)
					}
					path, stats := downloadReadinessFile(t, srv, principal, id)
					verifyReadinessDataset(t, path, format, count, part.columns)
					downloads[format] = stats
				}
				raw, _ := json.Marshal(map[string]any{"mode": mode, "round": round + 1, "rows": count, "encoded_ms": encodedMS, "ready_ms": readyMS, "all_ready_ms": allMS, "allocated_bytes": after.TotalAlloc - before.TotalAlloc, "downloads": downloads, "scope": "FINAL scan through real S3 upload, size/SHA256 readback and atomic READY; downloads separately measured over loopback HTTP with real identity and single-use tickets"})
				t.Logf("EXPORT_READINESS_RESULT %s", raw)
			})
		}
	}
}

func downloadReadinessFile(t *testing.T, srv *Server, principal policy.Principal, id string) (string, map[string]any) {
	t.Helper()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
		})
	})
	router.Post("/exports/{id}/ticket", srv.handleExportV2Ticket)
	router.Get("/api/v2/exports/download/{ticket}", srv.handleExportV2Download)
	server := httptest.NewServer(router)
	defer server.Close()
	start := time.Now()
	response, err := server.Client().Post(server.URL+"/exports/"+id+"/ticket", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var ticket struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	err = json.NewDecoder(response.Body).Decode(&ticket)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || ticket.Data.URL == "" {
		t.Fatalf("ticket %d: %v", response.StatusCode, err)
	}
	ticketMS := float64(time.Since(start).Nanoseconds()) / 1e6
	start = time.Now()
	response, err = server.Client().Get(server.URL + ticket.Data.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download status %d", response.StatusCode)
	}
	path := filepath.Join(t.TempDir(), "download")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 1<<20)
	first, readErr := response.Body.Read(buffer)
	firstMS := float64(time.Since(start).Nanoseconds()) / 1e6
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		t.Fatal(readErr)
	}
	writer := io.MultiWriter(file, hash)
	if _, err := writer.Write(buffer[:first]); err != nil {
		t.Fatal(err)
	}
	n, err := io.CopyBuffer(writer, response.Body, buffer)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	fullMS := float64(time.Since(start).Nanoseconds()) / 1e6
	n += int64(first)
	exp, err := srv.deps.Store.GetExport(context.Background(), id)
	if err != nil || exp == nil || n != response.ContentLength || hex.EncodeToString(hash.Sum(nil)) != exp.SHA256 {
		t.Fatalf("incomplete downloaded artifact: bytes=%d length=%d export=%v error=%v", n, response.ContentLength, exp, err)
	}
	return path, map[string]any{"bytes": n, "sha256": exp.SHA256, "ticket_ms": ticketMS, "first_byte_ms": firstMS, "full_download_ms": fullMS}
}

func verifyReadinessDataset(t *testing.T, path, format string, count int64, columns []string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	base, _ := new(big.Int).SetString("1234567890123456789012345678901234567890", 10)
	check := func(index int64, block, raw, amount string) {
		n := count - 1 - index
		want := new(big.Int).Add(base, big.NewInt(n))
		if n%1000 == 0 {
			want.Add(want, big.NewInt(100))
		}
		if n < 0 || block != strconv.FormatInt(9007199254740993+n, 10) || raw != want.String() || amount != decimal.NewFromBigInt(want, -18).String() {
			t.Fatalf("%s changed row %d: block=%s raw=%s amount=%s", format, index, block, raw, amount)
		}
	}
	var rows int64
	switch format {
	case "csv":
		reader := csv.NewReader(bufio.NewReaderSize(file, 1<<20))
		header, err := reader.Read()
		if count == 0 && errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		cols := map[string]int{}
		for i, col := range header {
			cols[col] = i
		}
		for {
			record, err := reader.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			check(rows, record[cols["block_number"]], record[cols["amount_raw"]], record[cols["amount"]])
			rows++
		}
	case "jsonl":
		decoder := json.NewDecoder(bufio.NewReaderSize(file, 1<<20))
		decoder.UseNumber()
		for {
			var row map[string]any
			err := decoder.Decode(&row)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			check(rows, fmt.Sprint(row["block_number"]), fmt.Sprint(row["amount_raw"]), fmt.Sprint(row["amount"]))
			rows++
		}
	case "parquet":
		reader := parquet.NewGenericReader[parquetExportRow](file)
		defer reader.Close()
		batch := make([]parquetExportRow, 4096)
		for {
			n, err := reader.Read(batch)
			for _, row := range batch[:n] {
				check(rows, strconv.FormatUint(row.BlockNumber, 10), row.AmountRaw, row.Amount)
				rows++
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	case "xlsx":
		archive, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer archive.Close()
		sort.Slice(archive.File, func(i, j int) bool { return archive.File[i].Name < archive.File[j].Name })
		var sheets int
		for _, entry := range archive.File {
			if !strings.HasPrefix(entry.Name, "xl/worksheets/sheet") || !strings.HasSuffix(entry.Name, ".xml") {
				continue
			}
			sheets++
			reader, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			decoder := xml.NewDecoder(bufio.NewReaderSize(reader, 1<<20))
			var sheetRows int64
			for {
				token, err := decoder.Token()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					reader.Close()
					t.Fatal(err)
				}
				begin, ok := token.(xml.StartElement)
				if !ok || begin.Name.Local != "row" {
					continue
				}
				var row struct {
					Cells []struct {
						Value  string `xml:"v"`
						Inline struct {
							Text string `xml:"t"`
						} `xml:"is"`
					} `xml:"c"`
				}
				if err := decoder.DecodeElement(&row, &begin); err != nil {
					reader.Close()
					t.Fatal(err)
				}
				if sheetRows > 0 {
					values := map[string]string{}
					for i, cell := range row.Cells {
						if i < len(columns) {
							value := cell.Value
							if value == "" {
								value = cell.Inline.Text
							}
							values[columns[i]] = value
						}
					}
					check(rows, values["block_number"], values["amount_raw"], values["amount"])
					rows++
				}
				sheetRows++
			}
			reader.Close()
		}
		wantSheets := int((count + 249999) / 250000)
		if wantSheets == 0 {
			wantSheets = 1
		}
		if sheets != wantSheets {
			t.Fatalf("XLSX sheets=%d want=%d", sheets, wantSheets)
		}
	}
	if rows != count {
		t.Fatalf("%s rows=%d want=%d", format, rows, count)
	}
}
