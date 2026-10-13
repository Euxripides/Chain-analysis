package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/google/uuid"
)

func TestCompletedJobStatsUpdateKeepsPreparedExportVersionPostgres(t *testing.T) {
	dsn := os.Getenv("EXPORT_PREP_VERSION_TEST_DSN")
	if dsn == "" {
		t.Skip("set EXPORT_PREP_VERSION_TEST_DSN for PostgreSQL preparation version test")
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	s := &Server{deps: Deps{Store: st}}
	jobID := uuid.NewString()
	sealedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	laterStatsAt := sealedAt.Add(10 * time.Minute)
	if _, err := st.Pool.Exec(ctx, `INSERT INTO download_jobs(id,status,source,updated_at)
		VALUES($1,'COMPLETED','user_download',$2)`, jobID, sealedAt); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM export_preparations WHERE job_id=$1`, jobID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM download_subjects WHERE job_id=$1`, jobID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM download_jobs WHERE id=$1`, jobID)
	})
	if _, err := st.Pool.Exec(ctx, `INSERT INTO download_subjects(id,job_id) VALUES($1,$2)`, uuid.NewString(), jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO export_preparations(job_id,format,data_version)
		SELECT $1::uuid,format,$2::timestamptz FROM unnest(ARRAY['csv','xlsx','jsonl','parquet']) AS format`, jobID, sealedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE export_preparations SET updated_at='2000-01-01' WHERE job_id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE download_jobs SET updated_at=$2 WHERE id=$1`, jobID, laterStatsAt); err != nil {
		t.Fatal(err)
	}
	job := &model.DownloadJob{ID: jobID, Status: model.JobCompleted, UpdatedAt: laterStatsAt}
	if got := s.jobExportVersion(ctx, job); !got.Equal(sealedAt) {
		t.Fatalf("version=%v, want sealed completion time %v", got, sealedAt)
	}
	group, err := s.claimExportPreparationGroup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(group) != 4 || group[0].jobID != jobID || !group[0].dataVersion.Equal(sealedAt) {
		t.Fatalf("claim after stats update: %#v", group)
	}
}
