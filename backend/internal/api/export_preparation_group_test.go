package api

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// Uses an isolated PostgreSQL fixture because the four-row claim relies on
// row locks and cannot be meaningfully tested with an in-memory mock.
func TestClaimExportPreparationGroupPostgres(t *testing.T) {
	dsn := os.Getenv("EXPORT_PREP_TEST_DSN")
	if dsn == "" {
		t.Skip("set EXPORT_PREP_TEST_DSN for PostgreSQL claim test")
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	jobID := uuid.NewString()
	_, err = st.Pool.Exec(ctx, `
		INSERT INTO download_jobs(id,status,source,updated_at)
		VALUES ($1,'COMPLETED','user_download',now())`, jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM download_jobs WHERE id=$1`, jobID) }()
	_, err = st.Pool.Exec(ctx, `INSERT INTO download_subjects(id,job_id) VALUES ($1,$2)`, uuid.NewString(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Pool.Exec(ctx, `
		INSERT INTO export_preparations(job_id,format,data_version)
		SELECT $1::uuid,f.format,j.updated_at
		FROM download_jobs j CROSS JOIN unnest(ARRAY['csv','xlsx','jsonl','parquet']) AS f(format)
		WHERE j.id=$1 ON CONFLICT (job_id,format) DO UPDATE
		SET data_version=EXCLUDED.data_version,status='PENDING',attempts=0`, jobID)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{deps: Deps{Store: st}}
	group, err := s.claimExportPreparationGroup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(group) != 4 {
		t.Fatalf("claimed %d formats, want 4", len(group))
	}
	for _, p := range group {
		if p.jobID != jobID || p.attempt != 1 {
			t.Fatalf("unexpected claim: %#v", p)
		}
	}
	_, err = s.claimExportPreparationGroup(ctx)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second claim = %v, want no rows", err)
	}
}
