package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The fixture rejects new exports after recording an INSERT attempt in a
// PostgreSQL sequence. This proves whether a request treats a failure as a
// cache miss, without starting an unrelated asynchronous export pipeline.
type exportReuseFixture struct {
	server  *Server
	pool    *pgxpool.Pool
	stub    *s3Stub
	request exportV2Request
	export  string
	version time.Time
}

func newExportReuseFixture(t *testing.T, zipped bool, failS3 bool) exportReuseFixture {
	t.Helper()
	ctx := context.Background()
	pool := testutil.Postgres(t)
	for _, ddl := range []string{
		`CREATE TABLE download_jobs(
			id uuid PRIMARY KEY, job_no text NOT NULL DEFAULT 'qa', status text NOT NULL DEFAULT 'COMPLETED',
			priority int NOT NULL DEFAULT 100, source text NOT NULL DEFAULT 'user_download',
			mode text NOT NULL DEFAULT 'balanced', requested_mode text NOT NULL DEFAULT 'fast',
			effective_mode text NOT NULL DEFAULT 'fast', mode_reason text NOT NULL DEFAULT '',
			mode_changed_at timestamptz NOT NULL DEFAULT now(), chain text NOT NULL DEFAULT 'bsc',
			range_type text NOT NULL DEFAULT 'block', from_block bigint, to_block bigint,
			from_timestamp timestamptz, to_timestamp timestamptz,
			datasets text[] NOT NULL DEFAULT ARRAY['transactions'], reuse_policy text NOT NULL DEFAULT 'prefer_local',
			request_fingerprint text, head_block bigint NOT NULL DEFAULT 10, created_by uuid,
			error text NOT NULL DEFAULT '', stats jsonb NOT NULL DEFAULT '{}', organization_id uuid,
			workspace_id uuid, project_id uuid, resolved_at timestamptz,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL,
			created_by_identity uuid)`,
		`CREATE TABLE download_subjects(id uuid, job_id uuid, subject_type text NOT NULL DEFAULT 'address',
			subject text NOT NULL, status text NOT NULL DEFAULT 'COMPLETED', progress double precision NOT NULL DEFAULT 1,
			stats jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE export_preparations(job_id uuid, data_version timestamptz)`,
		`CREATE TABLE exports(id uuid PRIMARY KEY, job_id uuid, dataset text, format text, zip boolean,
			status text, object_ref text NOT NULL DEFAULT '', rows bigint NOT NULL DEFAULT 0,
			sha256 text NOT NULL DEFAULT '', error text NOT NULL DEFAULT '',
			organization_id uuid, workspace_id uuid, created_by uuid, query_snapshot jsonb,
			expires_at timestamptz, finished_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now())`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	job, org, ws, exportID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	version := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	address := "0x0000000000000000000000000000000000000001"
	if _, err := pool.Exec(ctx, `INSERT INTO download_jobs(id,organization_id,workspace_id,updated_at)
		VALUES($1,$2,$3,$4)`, job, org, ws, version); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO download_subjects(id,job_id,subject) VALUES($1,$2,$3)`, uuid.NewString(), job, address); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO export_preparations(job_id,data_version) VALUES($1,$2)`, job, version); err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(map[string]any{
		"chain": "bsc", "addresses": []string{address}, "datasets": []string{"transactions"},
		"format": "csv", "include_meta": false, "zip": zipped, "include_decoded_input": false,
		"job_id": job, "partial": false, "job_status": "COMPLETED",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO exports(id,job_id,dataset,format,zip,status,object_ref,
		organization_id,workspace_id,query_snapshot,expires_at,finished_at)
		VALUES($1,$2,'transactions','csv',$3,'COMPLETED','exports/cached.csv',$4,$5,$6,now()+interval '1 hour',now())`,
		exportID, job, zipped, org, ws, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE SEQUENCE reuse_insert_attempts`,
		`CREATE FUNCTION reject_reuse_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM nextval('reuse_insert_attempts'); RAISE EXCEPTION 'reuse fixture insert reached'; END $$`,
		`CREATE TRIGGER reject_reuse_insert BEFORE INSERT ON exports FOR EACH ROW EXECUTE FUNCTION reject_reuse_insert()`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	stub := newS3Stub(t)
	stub.objects[testExportBucket]["exports/cached.csv"] = "complete fixture"
	if failS3 {
		stub.server.Close()
		var failures atomic.Int32
		stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/"+testExportBucket) {
				failures.Add(1)
				stub.writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable")
				return
			}
			stub.handle(w, r)
		}))
		t.Cleanup(stub.server.Close)
		t.Cleanup(func() {
			if failures.Load() == 0 {
				t.Error("object storage failure was not exercised")
			}
		})
	}
	server := newTestServer(t, stub, nil)
	server.deps.Store = &store.Store{Pool: dbtx.New(pool)}
	return exportReuseFixture{server: server, pool: pool, stub: stub,
		request: exportV2Request{JobID: job, Format: "csv", OrgID: org, WSID: ws}, export: exportID, version: version}
}

func (f exportReuseFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func (f exportReuseFixture) assertInsertAttempt(t *testing.T, want bool) {
	t.Helper()
	var called bool
	if err := f.pool.QueryRow(context.Background(), `SELECT is_called FROM reuse_insert_attempts`).Scan(&called); err != nil {
		t.Fatal(err)
	}
	if called != want {
		t.Fatalf("new export INSERT attempted=%v, want %v", called, want)
	}
}

func TestCreateExportReuseFailurePostgres(t *testing.T) {
	for _, tc := range []struct {
		name       string
		change     func(*testing.T, exportReuseFixture)
		status     int
		body       string
		createMiss bool
	}{
		{name: "existing object", status: http.StatusOK, body: "COMPLETED"},
		{name: "lookup database failure", change: func(t *testing.T, f exportReuseFixture) {
			f.exec(t, `ALTER TABLE exports DROP COLUMN query_snapshot`)
		}, status: http.StatusInternalServerError, body: "INTERNAL_ERROR"},
		{name: "record database failure", change: func(t *testing.T, f exportReuseFixture) {
			f.exec(t, `ALTER TABLE exports DROP COLUMN sha256`)
		}, status: http.StatusInternalServerError, body: "INTERNAL_ERROR"},
		{name: "access denied", change: func(_ *testing.T, f exportReuseFixture) {
			f.stub.denied[testExportBucket] = true
		}, status: http.StatusServiceUnavailable, body: "EXPORT_STORAGE_UNAVAILABLE"},
		{name: "bucket missing", change: func(_ *testing.T, f exportReuseFixture) {
			delete(f.stub.objects, testExportBucket)
		}, status: http.StatusServiceUnavailable, body: "EXPORT_STORAGE_UNAVAILABLE"},
		{name: "storage service unavailable", status: http.StatusServiceUnavailable, body: "EXPORT_STORAGE_UNAVAILABLE"},
		{name: "object missing", change: func(_ *testing.T, f exportReuseFixture) {
			delete(f.stub.objects[testExportBucket], "exports/cached.csv")
		}, status: http.StatusInternalServerError, body: "reuse fixture insert reached", createMiss: true},
		{name: "no completed record", change: func(t *testing.T, f exportReuseFixture) {
			f.exec(t, `UPDATE exports SET status='FAILED'`)
		}, status: http.StatusInternalServerError, body: "reuse fixture insert reached", createMiss: true},
		{name: "different tenant", change: func(t *testing.T, f exportReuseFixture) {
			f.exec(t, `UPDATE exports SET workspace_id=$1`, uuid.NewString())
		}, status: http.StatusInternalServerError, body: "reuse fixture insert reached", createMiss: true},
		{name: "older version", change: func(t *testing.T, f exportReuseFixture) {
			f.exec(t, `UPDATE exports SET finished_at=$1`, f.version.Add(-time.Second))
		}, status: http.StatusInternalServerError, body: "reuse fixture insert reached", createMiss: true},
		{name: "different snapshot", change: func(t *testing.T, f exportReuseFixture) {
			f.exec(t, `UPDATE exports SET query_snapshot='{}'`)
		}, status: http.StatusInternalServerError, body: "reuse fixture insert reached", createMiss: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExportReuseFixture(t, true, tc.name == "storage service unavailable")
			if tc.change != nil {
				tc.change(t, f)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r := httptest.NewRequest(http.MethodPost, "/api/v2/exports", nil).WithContext(ctx)
			w := httptest.NewRecorder()
			f.server.createExportV2(w, r, f.request)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.body) {
				t.Fatalf("status=%d body=%s; want %d containing %q", w.Code, w.Body.String(), tc.status, tc.body)
			}
			f.assertInsertAttempt(t, tc.createMiss)
			if tc.status == http.StatusOK && !strings.Contains(w.Body.String(), f.export) {
				t.Fatal("reuse returned a different export")
			}
		})
	}
}

func TestPreparedExportReuseStorageFailurePostgres(t *testing.T) {
	for _, name := range []string{"existing object", "access denied", "bucket missing", "object missing"} {
		t.Run(name, func(t *testing.T) {
			f := newExportReuseFixture(t, false, false)
			switch name {
			case "access denied":
				f.stub.denied[testExportBucket] = true
			case "bucket missing":
				delete(f.stub.objects, testExportBucket)
			case "object missing":
				delete(f.stub.objects[testExportBucket], "exports/cached.csv")
			}
			id, size, err := f.server.buildPreparedExport(context.Background(), exportPreparation{
				jobID: f.request.JobID, format: "csv", dataVersion: f.version,
			})
			if name == "existing object" {
				if err != nil || id != f.export || size != int64(len("complete fixture")) {
					t.Fatalf("reuse result: id=%q size=%d err=%v", id, size, err)
				}
			} else if err == nil {
				t.Fatal("expected failed storage check or rejected test INSERT")
			}
			f.assertInsertAttempt(t, name == "object missing")
		})
	}
}
