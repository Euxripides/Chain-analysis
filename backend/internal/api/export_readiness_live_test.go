package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/testutil"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/parquet-go/parquet-go"
	"github.com/xuri/excelize/v2"
)

// Explicit isolated service settings are mandatory; application configuration is
// never read. The fixture owns only its random PostgreSQL schema and S3 buckets.
func newExportReadinessFixture(t *testing.T, datasets []string) (*Server, *model.DownloadJob, []exportPreparation, policy.Principal) {
	t.Helper()
	endpoint := os.Getenv("EXPORT_READINESS_S3_ENDPOINT")
	access, secret := os.Getenv("EXPORT_READINESS_S3_ACCESS_KEY"), os.Getenv("EXPORT_READINESS_S3_SECRET_KEY")
	if endpoint == "" || access == "" || secret == "" {
		t.Skip("explicit isolated EXPORT_READINESS_S3_* settings required")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	for _, ddl := range []string{
		`CREATE TABLE download_jobs(
		 id uuid PRIMARY KEY,job_no text NOT NULL,status text NOT NULL,priority int DEFAULT 100,
		 source text DEFAULT 'user_download',mode text DEFAULT 'balanced',requested_mode text DEFAULT 'fast',
		 effective_mode text DEFAULT 'fast',mode_reason text DEFAULT '',mode_changed_at timestamptz DEFAULT now(),
		 chain text DEFAULT 'bsc',range_type text DEFAULT 'block',from_block bigint,to_block bigint,
		 from_timestamp timestamptz,to_timestamp timestamptz,datasets text[],reuse_policy text DEFAULT 'prefer_local',
		 request_fingerprint text,head_block bigint DEFAULT 10,created_by uuid,error text DEFAULT '',stats jsonb DEFAULT '{}',
		 organization_id uuid,workspace_id uuid,project_id uuid,resolved_at timestamptz,
		 created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now(),created_by_identity uuid)`,
		`CREATE TABLE workspaces(id uuid PRIMARY KEY,organization_id uuid)`,
		`CREATE TABLE workspace_memberships(workspace_id uuid,identity_id uuid,status text)`,
		`CREATE TABLE exports(id uuid PRIMARY KEY,job_id uuid,dataset text,format text,zip boolean DEFAULT false,
		 status text,organization_id uuid,workspace_id uuid,created_by uuid,query_snapshot jsonb,
		 object_ref text DEFAULT '',sha256 text DEFAULT '',rows bigint DEFAULT 0,error text DEFAULT '',
		 created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now(),finished_at timestamptz,expires_at timestamptz,file_manifest jsonb DEFAULT '[]',files_deleted_at timestamptz)`,
		`CREATE TABLE export_preparations(job_id uuid,format text,data_version timestamptz,status text,attempts int,
		 lease_until timestamptz,export_id uuid,bytes bigint DEFAULT 0 CHECK(bytes>=0),retry_after timestamptz,
		 error text DEFAULT '',updated_at timestamptz DEFAULT now(),PRIMARY KEY(job_id,format))`,
		`CREATE TABLE export_tickets(id uuid PRIMARY KEY,export_id uuid,user_id uuid,organization_id uuid,
		 workspace_id uuid,expires_at timestamptz,used_at timestamptz,used_by uuid,file_dataset text DEFAULT '')`,
		`CREATE TABLE audit_events(id uuid PRIMARY KEY,user_id uuid,action text,resource_type text,resource_id text,detail jsonb)`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	p := policy.Principal{IdentityID: uuid.NewString(), OrganizationID: uuid.NewString(), WorkspaceID: uuid.NewString(), Authenticated: true}
	id, version := uuid.NewString(), time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces VALUES($1,$2)`, p.WorkspaceID, p.OrganizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_memberships VALUES($1,$2,'active')`, p.WorkspaceID, p.IdentityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO download_jobs(id,job_no,status,datasets,organization_id,workspace_id,project_id,created_by_identity,updated_at)
	 VALUES($1,$2,'COMPLETED',$3,$4,$5,$6,$7,$8)`, id, "DL-"+id[:8], datasets, p.OrganizationID, p.WorkspaceID, uuid.NewString(), p.IdentityID, version); err != nil {
		t.Fatal(err)
	}
	group := make([]exportPreparation, 0, 4)
	for _, format := range []string{"xlsx", "parquet", "csv", "jsonl"} {
		prep := exportPreparation{jobID: id, format: format, dataVersion: version, attempt: 1}
		if _, err := pool.Exec(ctx, `INSERT INTO export_preparations(job_id,format,data_version,status,attempts,lease_until)
		 VALUES($1,$2,$3,'RUNNING',1,now()+interval '30 minutes')`, id, format, version); err != nil {
			t.Fatal(err)
		}
		group = append(group, prep)
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: false})
	if err != nil {
		t.Fatal(err)
	}
	bucket := "qa-export-ready-" + uuid.NewString()
	rawBucket, exportBucket := bucket+"-raw", bucket+"-export"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, name := range []string{rawBucket, exportBucket} {
			exists, err := client.BucketExists(cleanupCtx, name)
			if err != nil {
				t.Error(err)
				continue
			}
			if !exists {
				continue
			}
			for object := range client.ListObjects(cleanupCtx, name, minio.ListObjectsOptions{Recursive: true}) {
				if object.Err != nil {
					t.Error(object.Err)
					continue
				}
				if err := client.RemoveObject(cleanupCtx, name, object.Key, minio.RemoveObjectOptions{}); err != nil {
					t.Error(err)
				}
			}
			if err := client.RemoveBucket(cleanupCtx, name); err != nil {
				t.Error(err)
			}
		}
	})
	raw, err := rawstore.New(ctx, endpoint, access, secret, false, rawBucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.EnsureNamedBucket(ctx, exportBucket); err != nil {
		t.Fatal(err)
	}
	srv := &Server{deps: Deps{Store: &store.Store{Pool: dbtx.New(pool)}, Raw: raw,
		Cfg: &config.Config{RawBucket: rawBucket, ExportBucket: exportBucket, ExportWriteBucket: "export", ExportReadOrder: "export,raw"},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	job, err := srv.deps.Store.GetJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return srv, job, group, p
}

// Exercise the real scope, ticket creation, redemption and object-serving
// handlers over HTTP; the only injected value is the authenticated identity.
func downloadExportReadiness(t *testing.T, srv *Server, principal policy.Principal, exportID string) []byte {
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
	response, err := server.Client().Post(server.URL+"/exports/"+exportID+"/ticket", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var ticket struct {
		Data struct {
			ID  string `json:"ticket_id"`
			URL string `json:"url"`
		} `json:"data"`
	}
	err = json.NewDecoder(response.Body).Decode(&ticket)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || ticket.Data.ID == "" {
		t.Fatalf("ticket HTTP %d: %+v %v", response.StatusCode, ticket, err)
	}
	response, err = server.Client().Get(server.URL + ticket.Data.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("download HTTP %d: %v %.200s", response.StatusCode, err, body)
	}
	if response.ContentLength != int64(len(body)) {
		t.Fatalf("partial response: length=%d got=%d", response.ContentLength, len(body))
	}
	exp, err := srv.deps.Store.GetExport(context.Background(), exportID)
	if err != nil || exp == nil {
		t.Fatalf("export: %v", err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != exp.SHA256 {
		t.Fatalf("download SHA256 mismatch")
	}
	if response.Header.Get("ETag") != `"`+exp.SHA256+`"` {
		t.Fatalf("missing verified ETag")
	}
	response, err = server.Client().Get(server.URL + ticket.Data.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusGone {
		t.Fatalf("full GET reused single-use ticket: %d", response.StatusCode)
	}
	other := principal
	other.WorkspaceID = uuid.NewString()
	request := httptest.NewRequest(http.MethodPost, "/exports/"+exportID+"/ticket", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("id", exportID)
	request = request.WithContext(context.WithValue(withPrincipal(request.Context(), other), chi.RouteCtxKey, route))
	denied := httptest.NewRecorder()
	srv.handleExportV2Ticket(denied, request)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("other workspace obtained ticket: %d", denied.Code)
	}
	return body
}

func exportReadinessParts(t *testing.T, datasets []string, counts []int) []preparedDatasetParts {
	t.Helper()
	dir := t.TempDir()
	parts := make([]preparedDatasetParts, 0, len(datasets))
	for index, dataset := range datasets {
		columns := []string{"block_number", "amount_raw", "amount"}
		part := preparedDatasetParts{dataset: dataset, rows: int64(counts[index]), columns: columns, paths: map[string]string{}}
		var csvBytes, jsonBytes, parquetBytes bytes.Buffer
		parquetRows := make([]parquetExportRow, 0, counts[index])
		writer := csv.NewWriter(&csvBytes)
		if err := writer.Write(columns); err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(&jsonBytes)
		for i := 0; i < counts[index]; i++ {
			block := strconv.FormatInt(9007199254740993+int64(i), 10)
			raw, amount := "123456789012345678901234567890", "123456789012.345678901234567890"
			if err := writer.Write([]string{block, raw, amount}); err != nil {
				t.Fatal(err)
			}
			if err := encoder.Encode(map[string]any{"block_number": json.Number(block), "amount_raw": raw, "amount": amount}); err != nil {
				t.Fatal(err)
			}
			parquetRows = append(parquetRows, parquetExportRow{Dataset: dataset, BlockNumber: 9007199254740993 + uint64(i), AmountRaw: raw, Amount: amount})
		}
		parquetWriter := parquet.NewGenericWriter[parquetExportRow](&parquetBytes)
		if n, err := parquetWriter.Write(parquetRows); err != nil || n != len(parquetRows) {
			t.Fatalf("fixture parquet write: %d %v", n, err)
		}
		if err := parquetWriter.Close(); err != nil {
			t.Fatal(err)
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			t.Fatal(err)
		}
		for format, data := range map[string][]byte{"csv": csvBytes.Bytes(), "jsonl": jsonBytes.Bytes(), "parquet": parquetBytes.Bytes()} {
			path := filepath.Join(dir, dataset+"."+format)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			part.paths[format] = path
		}
		parts = append(parts, part)
	}
	return parts
}

func checkExportReadinessPayload(t *testing.T, body []byte, format, address string, parts []preparedDatasetParts) {
	t.Helper()
	files := map[string][]byte{}
	if len(parts) == 1 {
		files[parts[0].dataset] = body
	} else {
		archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		if len(archive.File) != len(parts)+1 {
			t.Fatalf("ZIP entries=%d datasets=%d", len(archive.File), len(parts))
		}
		manifest := 0
		for _, file := range archive.File {
			reader, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			if file.Name == "manifest.json" {
				manifest++
				if !json.Valid(data) {
					t.Fatal("invalid manifest")
				}
				continue
			}
			matched := false
			for _, part := range parts {
				if file.Name == fmt.Sprintf("addresses/%s/%s.%s", address, part.dataset, extFor(format)) {
					if _, exists := files[part.dataset]; exists {
						t.Fatalf("duplicate dataset %s", part.dataset)
					}
					files[part.dataset] = data
					matched = true
				}
			}
			if !matched {
				t.Fatalf("unexpected ZIP entry %s", file.Name)
			}
		}
		if manifest != 1 {
			t.Fatalf("manifest count=%d", manifest)
		}
	}
	for _, part := range parts {
		data, exists := files[part.dataset]
		if !exists {
			t.Fatalf("missing dataset %s", part.dataset)
		}
		if format != "xlsx" {
			want, err := os.ReadFile(part.paths[format])
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, want) {
				t.Fatalf("changed %s bytes for %s", format, part.dataset)
			}
			if format == "parquet" {
				reader := parquet.NewGenericReader[parquetExportRow](bytes.NewReader(data))
				rows := make([]parquetExportRow, 511)
				var total int64
				for {
					n, err := reader.Read(rows)
					for _, row := range rows[:n] {
						if row.Dataset != part.dataset || row.BlockNumber != 9007199254740993+uint64(total) || row.AmountRaw != "123456789012345678901234567890" || row.Amount != "123456789012.345678901234567890" {
							reader.Close()
							t.Fatalf("Parquet precision changed at row %d: %+v", total, row)
						}
						total++
					}
					if err == io.EOF {
						break
					}
					if err != nil {
						reader.Close()
						t.Fatal(err)
					}
				}
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
				if total != part.rows {
					t.Fatalf("Parquet rows=%d want=%d", total, part.rows)
				}
			}
			continue
		}
		book, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := book.GetRows("Sheet1")
		if err != nil {
			book.Close()
			t.Fatal(err)
		}
		if part.rows == 0 {
			if len(rows) > 1 {
				book.Close()
				t.Fatalf("empty dataset contains rows: %d", len(rows))
			}
		} else {
			if len(rows) != int(part.rows)+1 {
				book.Close()
				t.Fatalf("xlsx row count: %d want=%d", len(rows), part.rows+1)
			}
			for i, row := range rows[1:] {
				if len(row) != 3 || row[0] != strconv.FormatInt(9007199254740993+int64(i), 10) || row[1] != "123456789012345678901234567890" || row[2] != "123456789012.345678901234567890" {
					book.Close()
					t.Fatalf("XLSX precision changed at row %d: %v", i, row)
				}
			}
		}
		if err := book.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPreparedExportReadinessLive(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		datasets []string
		counts   []int
		failXLSX bool
	}{
		{"empty", []string{"transactions"}, []int{0}, false},
		{"multi-dataset", []string{"token_transfers", "transactions"}, []int{8193, 0}, false},
		{"xlsx-failure", []string{"token_transfers", "transactions"}, []int{3, 0}, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			srv, job, group, principal := newExportReadinessFixture(t, scenario.datasets)
			parts := exportReadinessParts(t, scenario.datasets, scenario.counts)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			entered, release, csvReady := make(chan struct{}), make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			address := "0x0000000000000000000000000000000000000001"
			failure := errors.New("deliberate XLSX encoder failure")
			go func() {
				done <- runPreparedExportFormats(ctx, group, parts, func(ctx context.Context, shared []preparedDatasetParts) ([]preparedDatasetParts, error) {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					if scenario.failXLSX {
						return nil, failure
					}
					return buildPreparedXLSXParts(ctx, shared)
				}, func(ctx context.Context, p exportPreparation, ready []preparedDatasetParts) error {
					err := srv.publishPreparedExportFormat(ctx, p, job, address, ready)
					if err == nil && p.format == "csv" {
						close(csvReady)
					}
					return err
				})
			}()
			for _, signal := range []<-chan struct{}{entered, csvReady} {
				select {
				case <-signal:
				case err := <-done:
					t.Fatalf("scheduler stopped before CSV ready: %v", err)
				case <-ctx.Done():
					t.Fatal("CSV waited for blocked XLSX")
				}
			}
			readID := func(format string) string {
				t.Helper()
				var id string
				var size int64
				if err := srv.deps.Store.Pool.QueryRow(ctx, `SELECT e.id::text,p.bytes FROM export_preparations p JOIN exports e ON e.id=p.export_id
			 WHERE p.job_id=$1 AND p.format=$2 AND p.status='READY' AND e.status='COMPLETED'`, job.ID, format).Scan(&id, &size); err != nil {
					t.Fatalf("%s is not atomically ready: %v", format, err)
				}
				if size < 0 {
					t.Fatal("invalid object bytes")
				}
				return id
			}
			csvID := readID("csv")
			body := downloadExportReadiness(t, srv, principal, csvID)
			checkExportReadinessPayload(t, body, "csv", address, parts)
			var xlsxReady bool
			if err := srv.deps.Store.Pool.QueryRow(ctx, `SELECT status='READY' FROM export_preparations WHERE job_id=$1 AND format='xlsx'`, job.ID).Scan(&xlsxReady); err != nil || xlsxReady {
				t.Fatalf("XLSX unexpectedly ready: %v %v", xlsxReady, err)
			}
			select {
			case err := <-done:
				t.Fatalf("scheduler returned while XLSX blocked: %v", err)
			default:
			}
			t.Logf("CSV fully downloaded and verified before XLSX release: bytes=%d datasets=%d", len(body), len(parts))
			close(release)
			err := <-done
			if scenario.failXLSX {
				if !errors.Is(err, failure) {
					t.Fatalf("expected isolated XLSX failure, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			jsonID := readID("jsonl")
			checkExportReadinessPayload(t, downloadExportReadiness(t, srv, principal, jsonID), "jsonl", address, parts)
			parquetID := readID("parquet")
			checkExportReadinessPayload(t, downloadExportReadiness(t, srv, principal, parquetID), "parquet", address, parts)
			if !scenario.failXLSX {
				xlsxID := readID("xlsx")
				checkExportReadinessPayload(t, downloadExportReadiness(t, srv, principal, xlsxID), "xlsx", address, parts)
			}
			var audited int
			if err := srv.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='export.completed'`).Scan(&audited); err != nil {
				t.Fatal(err)
			}
			want := 4
			if scenario.failXLSX {
				want = 3
			}
			if audited != want {
				t.Fatalf("completed audit events=%d want=%d", audited, want)
			}
			for _, part := range parts {
				for _, path := range part.paths {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("shared input removed before caller cleanup: %v", err)
					}
				}
			}
		})
	}
}

// Ensure this fixture never hides a real authorization failure behind the
// upload override used by narrower object-store unit tests.
func TestPreparedExportReadinessRejectsLostWorkspaceLive(t *testing.T) {
	srv, job, group, _ := newExportReadinessFixture(t, []string{"transactions"})
	parts := exportReadinessParts(t, job.Datasets, []int{1})
	if _, err := srv.deps.Store.Pool.Exec(context.Background(), `DELETE FROM workspaces`); err != nil {
		t.Fatal(err)
	}
	var csvPrep exportPreparation
	for _, p := range group {
		if p.format == "csv" {
			csvPrep = p
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := srv.publishPreparedExportFormat(ctx, csvPrep, job, "0x0000000000000000000000000000000000000001", parts)
	if err == nil || !strings.Contains(err.Error(), "intent") {
		t.Fatalf("lost scope upload was not refused: %v", err)
	}
	var completed int
	if err := srv.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM exports WHERE status='COMPLETED'`).Scan(&completed); err != nil || completed != 0 {
		t.Fatalf("invalid completed export: %d %v", completed, err)
	}
}
