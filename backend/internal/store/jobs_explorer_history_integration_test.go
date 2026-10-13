package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/google/uuid"
)

// Run against a migrated database with LOOKUP_TEST_DATABASE_URL. Every fixture
// is held in one transaction and rolled back, so workers cannot observe a job.
func TestCompletedFullJobReuseRequiresContinuousHistory(t *testing.T) {
	url := os.Getenv("LOOKUP_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("LOOKUP_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := NewWithRuntimeRole(ctx, url, os.Getenv("LOOKUP_TEST_RUNTIME_ROLE"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil {
			t.Errorf("rollback fixture: %v", err)
		}
	}()
	ctx = dbtx.WithTx(ctx, tx)
	var orgID, workspaceID string
	if err := tx.QueryRow(ctx, `SELECT organization_id::text,workspace_id::text FROM download_jobs
	 WHERE organization_id IS NOT NULL AND workspace_id IS NOT NULL LIMIT 1`).Scan(&orgID, &workspaceID); err != nil {
		t.Fatal(err)
	}
	seed := sha256.Sum256([]byte(uuid.NewString()))
	address, fingerprint := fmt.Sprintf("0x%x", seed[:20]), fmt.Sprintf("%x", seed)
	resolved := time.Now().UTC().Add(-time.Hour)
	job := &model.DownloadJob{
		ID: uuid.NewString(), JobNo: "lookup-proof-" + uuid.NewString(),
		Status: model.JobCompleted, Source: "user_download", Chain: "bsc", RangeType: "full",
		Datasets: []string{"transactions"}, ReusePolicy: "prefer_local", RequestFingerprint: fingerprint,
		HeadBlock: 100, OrganizationID: &orgID, WorkspaceID: &workspaceID, ResolvedAt: &resolved,
	}
	subjects, err := s.CreateJobWithSubjects(ctx, job, []string{address})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO address_dataset_sync_state
	 (chain,subject,dataset,complete_from,complete_through,source_job_id)
	 VALUES('bsc',$1,'transactions',$2,$3,$4)`, address, resolved.Add(-24*time.Hour), time.Now().Add(time.Hour), job.ID); err != nil {
		t.Fatal(err)
	}
	find := func(t *testing.T, want bool) {
		t.Helper()
		got, err := s.FindReusableJob(ctx, fingerprint, "bsc", "full", nil, nil, nil, nil, job.Datasets, []string{address}, orgID, workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if (got != nil) != want {
			t.Fatalf("reusable=%v, want %v", got != nil, want)
		}
	}
	t.Run("watermark_without_evidence", func(t *testing.T) { find(t, false) })
	if _, err := tx.Exec(ctx, `INSERT INTO download_datasets(id,job_id,subject_id,dataset,status)
	 VALUES($1,$2,$3,'transactions','COMPLETED')`, uuid.NewString(), job.ID, subjects[0].ID); err != nil {
		t.Fatal(err)
	}
	coverageID := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO dataset_coverage(id,chain,subject,dataset,from_block,to_block,status)
	 VALUES($1,'bsc',$2,'transactions',0,100,'COMPLETE')`, coverageID, address); err != nil {
		t.Fatal(err)
	}
	t.Run("continuous_full_history", func(t *testing.T) { find(t, true) })
	if _, err := tx.Exec(ctx, `UPDATE dataset_coverage SET to_block=40 WHERE id=$1`, coverageID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO dataset_coverage(id,chain,subject,dataset,from_block,to_block,status)
	 VALUES($1,'bsc',$2,'transactions',60,100,'COMPLETE')`, uuid.NewString(), address); err != nil {
		t.Fatal(err)
	}
	t.Run("historical_middle_gap", func(t *testing.T) { find(t, false) })
	if _, err := tx.Exec(ctx, `UPDATE download_jobs SET status='RUNNING' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	t.Run("active_same_request_reuse", func(t *testing.T) { find(t, true) })
}
