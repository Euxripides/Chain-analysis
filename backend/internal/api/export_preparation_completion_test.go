package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func completionFixture(t *testing.T) (*Server, *pgxpool.Pool, exportPreparation, string) {
	t.Helper()
	pool := testutil.Postgres(t)
	for _, ddl := range []string{
		`CREATE TABLE download_jobs(id uuid PRIMARY KEY,status text)`,
		`CREATE TABLE exports(id uuid PRIMARY KEY,job_id uuid,format text,status text,
		 object_ref text NOT NULL DEFAULT '',sha256 text NOT NULL DEFAULT '',rows bigint NOT NULL DEFAULT 0,
		 error text NOT NULL DEFAULT '',finished_at timestamptz,updated_at timestamptz DEFAULT now())`,
		`CREATE TABLE export_preparations(job_id uuid,format text,data_version timestamptz,status text,
		 attempts int,lease_until timestamptz,export_id uuid,bytes bigint NOT NULL DEFAULT 0 CHECK(bytes>=0),
		 retry_after timestamptz,error text DEFAULT '',updated_at timestamptz DEFAULT now(),PRIMARY KEY(job_id,format))`,
	} {
		if _, err := pool.Exec(context.Background(), ddl); err != nil {
			t.Fatal(err)
		}
	}
	p := exportPreparation{jobID: uuid.NewString(), format: "csv", dataVersion: time.Now().UTC().Truncate(time.Microsecond), attempt: 1}
	id := uuid.NewString()
	if _, err := pool.Exec(context.Background(), `INSERT INTO download_jobs VALUES($1,'COMPLETED')`, p.jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO exports(id,job_id,format,status) VALUES($1,$2,'csv','RUNNING')`, id, p.jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO export_preparations(job_id,format,data_version,status,attempts,lease_until)
	 VALUES($1,'csv',$2,'RUNNING',1,now()+interval '2 minutes')`, p.jobID, p.dataVersion); err != nil {
		t.Fatal(err)
	}
	return &Server{deps: Deps{Store: &store.Store{Pool: dbtx.New(pool)}}}, pool, p, id
}

func TestPreparedExportCompletionAtomicPostgres(t *testing.T) {
	for _, scenario := range []string{"success", "new-version", "new-attempt", "expired-lease", "failed-format", "job-retried", "missing-export", "ready-write-failure"} {
		t.Run(scenario, func(t *testing.T) {
			srv, pool, p, id := completionFixture(t)
			ctx := context.Background()
			changes := map[string]string{
				"new-version":    `UPDATE export_preparations SET data_version=data_version+interval '1 second'`,
				"new-attempt":    `UPDATE export_preparations SET attempts=2`,
				"expired-lease":  `UPDATE export_preparations SET lease_until=now()-interval '1 second'`,
				"failed-format":  `UPDATE export_preparations SET status='FAILED'`,
				"job-retried":    `UPDATE download_jobs SET status='RUNNING'`,
				"missing-export": `DELETE FROM exports`,
			}
			if sql := changes[scenario]; sql != "" {
				if _, err := pool.Exec(ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			size := int64(100)
			if scenario == "ready-write-failure" {
				size = -1 // The READY write fails after the export UPDATE.
			}
			err := srv.completePreparedExport(ctx, p, id, "exports/result.csv", "verified-hash", 8193, size)
			if (err == nil) != (scenario == "success") {
				t.Fatalf("completion result: %v", err)
			}
			var ready, completed int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM export_preparations WHERE status='READY'`).Scan(&ready); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM exports WHERE status='COMPLETED'`).Scan(&completed); err != nil {
				t.Fatal(err)
			}
			if scenario == "success" {
				var verified bool
				if err := pool.QueryRow(ctx, `SELECT e.object_ref='exports/result.csv' AND e.sha256='verified-hash'
				 AND e.rows=8193 AND p.export_id=e.id AND p.bytes=100 AND p.lease_until IS NULL
				 FROM exports e JOIN export_preparations p ON p.job_id=e.job_id`).Scan(&verified); err != nil || !verified {
					t.Fatalf("completed payload: %v %v", verified, err)
				}
				if ready != 1 || completed != 1 {
					t.Fatalf("completion was split: ready=%d completed=%d", ready, completed)
				}
			} else if ready != 0 || completed != 0 {
				t.Fatalf("failed or stale completion escaped rollback: ready=%d completed=%d", ready, completed)
			}
		})
	}
}

func TestPreparedCompletionSeesConcurrentTakeoverPostgres(t *testing.T) {
	srv, pool, p, id := completionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var ownerPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM download_jobs WHERE id=$1 FOR UPDATE`, p.jobID).Scan(&ownerPID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.completePreparedExport(ctx, p, id, "result", "sha", 1, 1) }()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, ownerPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("completion did not wait for the job row lock: %v", err)
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("completion never reached the row lock")
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE export_preparations SET attempts=2,lease_until=now()+interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, errExportPreparationLost) {
		t.Fatalf("old attempt ignored the concurrent takeover: %v", err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT status FROM exports WHERE id=$1`, id).Scan(&state); err != nil || state != "RUNNING" {
		t.Fatalf("old attempt became visible: %q %v", state, err)
	}
}
