package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/google/uuid"
)

// Real PostgreSQL, isolated schema, no provider traffic or production data.
func recoveryStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DOWNLOAD_RECOVERY_TEST_DSN")
	if dsn == "" {
		t.Skip("DOWNLOAD_RECOVERY_TEST_DSN is required for PostgreSQL fault tests")
	}
	ctx := context.Background()
	admin, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "recovery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Pool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	fixture, err := os.ReadFile("testdata/download_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ReplaceAll(string(fixture), "public.", schema+".")
	sql = strings.ReplaceAll(sql, "SELECT pg_catalog.set_config('search_path', '', false);", "")
	migration, err := os.ReadFile("../../migrations/postgres/0080_download_recovery_archive.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Pool.Exec(ctx, "SET search_path TO "+schema+";"+sql+string(migration)); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	st, err := New(ctx, dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestPopulatedPageProfileSurvivesEmptyTail(t *testing.T) {
	s := recoveryStore(t)
	ctx := context.Background()
	first := model.PageProfile{Chain: "bsc", Dataset: "transactions", QueryShape: "transactions", PreferredPageSize: 2000, AvgRows: 1000, AvgDecodedBytes: 2000000, AvgLatencyMs: 5000, PagesObserved: 2}
	if err := s.UpsertPageProfile(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.AvgRows, second.AvgDecodedBytes, second.PagesObserved = 4000, 8000000, 1
	if err := s.UpsertPageProfile(ctx, second); err != nil {
		t.Fatal(err)
	}
	empty := first
	empty.AvgRows, empty.PagesObserved, empty.PreferredPageSize = 0, 1, 10000
	if err := s.UpsertPageProfile(ctx, empty); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPageProfile(ctx, first.Chain, first.Dataset, first.QueryShape)
	if err != nil {
		t.Fatal(err)
	}
	if p.PagesObserved != 3 || p.AvgRows != 2000 || p.AvgDecodedBytes != 4000000 || p.PreferredPageSize != 2000 {
		t.Fatalf("empty tail erased weighted profile: %+v", p)
	}
}

func seedRecovery(t *testing.T, s *Store, status string) (job, shard string) {
	t.Helper()
	ctx := context.Background()
	job, shard = uuid.NewString(), uuid.NewString()
	_, err := s.Pool.Exec(ctx, `INSERT INTO download_jobs(id,job_no,status,chain,range_type,datasets,organization_id,workspace_id) VALUES($1::uuid,$1::text,$2,'bsc','block',ARRAY['transactions'],$3,$4)`, job, status, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO download_shards(id,job_id,subject_id,dataset_id,dataset,from_block,to_block,status) VALUES($1,$2,$3,$4,'transactions',1,100,'RUNNING')`, shard, job, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return
}

func seedPage(t *testing.T, s *Store, job, shard string, n int, status string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := s.Pool.Exec(context.Background(), `INSERT INTO download_pages(id,job_id,shard_id,dataset,page_no,query_hash,status,from_block,to_block,raw_path,raw_sha256) VALUES($1,$2,$3,'transactions',$4,'query',$5,1,100,'/fixture/page.json','sha')`, id, job, shard, n, status)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRecoveryPageIdentityAndAtomicAcknowledgement(t *testing.T) {
	s := recoveryStore(t)
	ctx := context.Background()
	job, sh := seedRecovery(t, s, "RUNNING")
	id := seedPage(t, s, job, sh, 1, "DURABLE")
	p := model.Page{JobID: job, ShardID: sh, Dataset: "transactions", PageNo: 1, QueryHash: "query", FromBlock: 1, ToBlock: 100, RawPath: "/fixture/page.json", RawSHA256: "sha", RowsFetched: 10}
	returned, err := s.PageDurableCheckpoint(ctx, p, "next", 1, 10, 10, 100, nil)
	if err != nil || returned != id {
		t.Fatalf("UPSERT returned %q, want persisted %q: %v", returned, id, err)
	}
	returned, err = s.PageDurableCheckpointCAS(ctx, p, 0, "next", 1, 10, 10, 100, nil)
	if err != nil || returned != id {
		t.Fatalf("CAS identity: %q %v", returned, err)
	}
	_, err = s.PageDurableCheckpointCAS(ctx, p, 99, "stale", 1, 10, 10, 100, nil)
	if !errors.Is(err, ErrLeaseEpochMismatch) {
		t.Fatalf("stale lease allowed: %v", err)
	}
	err = s.UpdatePagesIngested(ctx, []PageIngestUpdate{{id, 10, 10, 10}, {uuid.NewString(), 10, 10, 10}})
	if err == nil {
		t.Fatal("missing acknowledgement accepted")
	}
	state, _ := s.PageStatus(ctx, id)
	if state != "DURABLE" {
		t.Fatalf("partial batch committed: %s", state)
	}
	if err = s.UpdatePageIngested(ctx, id, 10, 10, 10); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPagesValidated(ctx, sh); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdatePageIngested(ctx, id, 10, 10, 10); err != nil {
		t.Fatal(err)
	}
	if err = s.FailPageFinal(ctx, id, "stale failure"); err == nil {
		t.Fatal("validated page downgraded")
	}
	state, _ = s.PageStatus(ctx, id)
	if state != "VALIDATED" {
		t.Fatal(state)
	}
	seedPage(t, s, job, sh, 2, "FAILED")
	seedPage(t, s, job, sh, 3, "FAILED_FINAL")
	ingested, pending, permanent, _, err := s.PageIngestSummary(ctx, sh)
	if err != nil || ingested != 1 || pending != 1 || permanent != 1 {
		t.Fatalf("fresh failure omitted: %d %d %d %v", ingested, pending, permanent, err)
	}
}

func TestRecoveryRetryPreservesCheckpointsAndRejectsEmptyOrPaused(t *testing.T) {
	s := recoveryStore(t)
	ctx := context.Background()
	job, sh := seedRecovery(t, s, "FAILED")
	id := seedPage(t, s, job, sh, 1, "INGESTED")
	_, err := s.Pool.Exec(ctx, `UPDATE download_shards SET status='FAILED_FINAL',page_no=7,page_token='resume-token',error='temporary provider timeout' WHERE id=$1;`, sh)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO checkpoints(shard_id,job_id,range_start,range_end,next_page_token,page_no) VALUES($1,$2,1,100,'resume-token',7)`, sh, job)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.RetryJobFailures(ctx, job)
	if err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	state, _ := s.PageStatus(ctx, id)
	if state != "INGESTED" {
		t.Fatal("completed page deleted")
	}
	var token string
	if err = s.Pool.QueryRow(ctx, "SELECT next_page_token FROM checkpoints WHERE shard_id=$1", sh).Scan(&token); err != nil || token != "resume-token" {
		t.Fatalf("checkpoint lost: %q %v", token, err)
	}
	empty, _ := seedRecovery(t, s, "PARTIAL_FAILED")
	_, _ = s.Pool.Exec(ctx, "DELETE FROM download_shards WHERE job_id=$1", empty)
	if _, err = s.RetryJobFailures(ctx, empty); err == nil {
		t.Fatal("empty repair queued")
	}
	var status string
	_ = s.Pool.QueryRow(ctx, "SELECT status FROM download_jobs WHERE id=$1", empty).Scan(&status)
	if status != "PARTIAL_FAILED" {
		t.Fatal(status)
	}
	paused, _ := seedRecovery(t, s, "PAUSED_FALLBACK_TOO_EXPENSIVE")
	if _, err = s.RetryJobFailures(ctx, paused); err == nil {
		t.Fatal("cost confirmation bypassed")
	}
}

func TestRecoveryAutomaticRetryAndConcurrentPersistentBudget(t *testing.T) {
	s := recoveryStore(t)
	ctx := context.Background()
	job, sh := seedRecovery(t, s, "FAILED")
	now := time.Now()
	_, err := s.Pool.Exec(ctx, `UPDATE download_shards SET status='FAILED_FINAL',page_state='{"last_failure_retryable":true}' WHERE id=$1`, sh)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.AdvanceDownloadRetry(ctx, job, now)
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	n, err = s.AdvanceDownloadRetry(ctx, job, now.Add(61*time.Second))
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var stats map[string]any
	_ = s.Pool.QueryRow(ctx, "SELECT stats FROM download_jobs WHERE id=$1", job).Scan(&stats)
	v := RetryState(stats)
	v.MaxRequests = 7
	v.MaxCredits = 70
	v.MaxCostMicros = 70
	raw, _ := json.Marshal(v)
	_, _ = s.Pool.Exec(ctx, "UPDATE download_jobs SET stats=jsonb_set(stats,'{download_retry}',$2::jsonb) WHERE id=$1", job, raw)
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.ReserveDownloadRetryRequest(ctx, job, 1, 10, 0.00001) == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 7 {
		t.Fatalf("concurrent overspend or under-admission: %d", accepted.Load())
	}
	reopened := &Store{Pool: s.Pool}
	if err = reopened.ReserveDownloadRetryRequest(ctx, job, 1, 10, 0.00001); !errors.Is(err, ErrDownloadRetryBudget) {
		t.Fatalf("restart reset budget: %v", err)
	}
	_ = s.Pool.QueryRow(ctx, "SELECT stats FROM download_jobs WHERE id=$1", job).Scan(&stats)
	v = RetryState(stats)
	if v.Requests != 7 || v.Credits != 70 || v.State != "budget_exhausted" {
		t.Fatalf("wrong persisted budget: %+v", v)
	}
	stale := map[string]any{"tail_active": true, "download_retry": defaultDownloadRetry(now)}
	if err = s.SetJobStats(ctx, job, stale); err != nil {
		t.Fatal(err)
	}
	_ = s.Pool.QueryRow(ctx, "SELECT stats FROM download_jobs WHERE id=$1", job).Scan(&stats)
	if got := RetryState(stats); got.Requests != 7 || got.Credits != 70 || got.State != "budget_exhausted" {
		t.Fatalf("tail snapshot restored already-spent recovery budget: %+v", got)
	}
	unsafe, us := seedRecovery(t, s, "FAILED")
	_, _ = s.Pool.Exec(ctx, "UPDATE download_shards SET status='FAILED_FINAL' WHERE id=$1", us)
	_, _ = s.AdvanceDownloadRetry(ctx, unsafe, now)
	n, err = s.AdvanceDownloadRetry(ctx, unsafe, now.Add(61*time.Second))
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	_ = s.Pool.QueryRow(ctx, "SELECT stats FROM download_jobs WHERE id=$1", unsafe).Scan(&stats)
	if RetryState(stats).State != "blocked" {
		t.Fatal("unsafe historical replay admitted")
	}
}

func TestRecoveryArchiveLeaseAndFinalizerFairness(t *testing.T) {
	s := recoveryStore(t)
	ctx := context.Background()
	job, sh := seedRecovery(t, s, "RUNNING")
	id := seedPage(t, s, job, sh, 1, "DURABLE")
	pages, err := s.ClaimPageArchives(ctx, 2)
	if err != nil || len(pages) != 1 {
		t.Fatal(len(pages), err)
	}
	pages, err = s.ClaimPageArchives(ctx, 2)
	if err != nil || len(pages) != 0 {
		t.Fatal("archive lease duplicated", err)
	}
	if err = s.RetryPageArchive(ctx, id); err != nil {
		t.Fatal(err)
	}
	var attempts int
	_ = s.Pool.QueryRow(ctx, "SELECT upload_attempts FROM download_pages WHERE id=$1", id).Scan(&attempts)
	if attempts != 1 {
		t.Fatal(attempts)
	}
	if err = s.CompletePageArchive(ctx, id, "fixture/key", "sha"); err != nil {
		t.Fatal(err)
	}
	key, err := s.PageArchiveKey(ctx, id)
	if err != nil || key != "fixture/key" {
		t.Fatal(key, err)
	}
	if err = s.CompletePageArchive(ctx, uuid.NewString(), "fixture/key", "sha"); err == nil {
		t.Fatal("missing archive page accepted")
	}
	if err = s.CompletePageArchive(ctx, id, "fixture/stale-key", "old-sha"); err == nil {
		t.Fatal("stale archive upload accepted")
	}
	key, err = s.PageArchiveKey(ctx, id)
	if err != nil || key != "fixture/key" {
		t.Fatal("stale upload replaced the durable object", key, err)
	}
	_, _ = s.Pool.Exec(ctx, "UPDATE download_shards SET status='COMPLETED' WHERE id=$1", sh)
	ready, err := s.ListArchiveReadyShards(ctx, 8)
	if err != nil || len(ready) != 1 {
		t.Fatal(len(ready), err)
	}
	for i := 0; i < 65; i++ {
		_, ss := seedRecovery(t, s, "RUNNING")
		_, err = s.Pool.Exec(ctx, "UPDATE download_shards SET status='FETCHED',updated_at=$2 WHERE id=$1", ss, time.Now().Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ListFinalizingShardsAfter(ctx, "FETCHED", time.Time{}, "", 64)
	if err != nil || len(first) != 64 {
		t.Fatal(len(first), err)
	}
	last := first[len(first)-1]
	second, err := s.ListFinalizingShardsAfter(ctx, "FETCHED", last.UpdatedAt, last.ID, 64)
	if err != nil || len(second) != 1 {
		t.Fatalf("65th shard starved: %d %v", len(second), err)
	}
	t.Log(fmt.Sprintf("archive lease, persisted key and %d+%d fair finalizer scan verified", len(first), len(second)))
}

func TestRecoveryRestoresOnlyProvenRemovedRange(t *testing.T) {
	s := recoveryStore(t)
	ctx := context.Background()
	job, sh := seedRecovery(t, s, "PARTIAL_FAILED")
	var dataset, subject string
	if err := s.Pool.QueryRow(ctx, "SELECT dataset_id,subject_id FROM download_shards WHERE id=$1", sh).Scan(&dataset, &subject); err != nil {
		t.Fatal(err)
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO download_datasets(id,job_id,subject_id,dataset,status,stats) VALUES($1,$2,$3,'transactions','FAILED','{"coverage":"PARTIAL","failure_reason":"incomplete shard removed by resume; coverage unproven beyond block 50"}')`, dataset, job, subject)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Pool.Exec(ctx, "UPDATE download_shards SET to_block=50,status='COMPLETED' WHERE id=$1", sh)
	_, _ = s.Pool.Exec(ctx, "UPDATE download_jobs SET range_type='full',from_block=1,to_block=100,head_block=100 WHERE id=$1", job)
	_, err = s.Pool.Exec(ctx, "INSERT INTO download_subjects(id,job_id,subject,status,progress) VALUES($1,$2,'address','COMPLETED',1)", subject, job)
	if err != nil {
		t.Fatal(err)
	}
	seedPage(t, s, job, sh, 1, "VALIDATED")
	n, err := s.RetryJobFailures(ctx, job)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var lo, hi int64
	err = s.Pool.QueryRow(ctx, "SELECT from_block,to_block FROM download_shards WHERE job_id=$1 AND status='PENDING'", job).Scan(&lo, &hi)
	if err != nil || lo != 51 || hi != 100 {
		t.Fatalf("wrong repair range %d..%d %v", lo, hi, err)
	}
	var parent string
	_ = s.Pool.QueryRow(ctx, "SELECT status FROM download_subjects WHERE id=$1", subject).Scan(&parent)
	if parent != "PENDING" {
		t.Fatalf("stale completed parent retained: %s", parent)
	}
	// Even a concurrently stale subject snapshot cannot mark unfinished work done.
	_, _ = s.Pool.Exec(ctx, "UPDATE download_subjects SET status='COMPLETED' WHERE id=$1", subject)
	if _, err = s.RecomputeJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	_ = s.Pool.QueryRow(ctx, "SELECT status FROM download_jobs WHERE id=$1", job).Scan(&parent)
	if parent == "COMPLETED" {
		t.Fatal("unfinished repair falsely completed")
	}
	var existing string
	_ = s.Pool.QueryRow(ctx, "SELECT status FROM download_shards WHERE id=$1", sh).Scan(&existing)
	if existing != "COMPLETED" {
		t.Fatal("completed range replayed")
	}
	if _, err = s.RetryJobFailures(ctx, job); err == nil {
		t.Fatal("duplicate range repair admitted")
	}
	// A terminal failure also cannot inherit a stale parent's completed state.
	_, _ = s.Pool.Exec(ctx, "UPDATE download_shards SET status='FAILED_FINAL' WHERE job_id=$1 AND status='PENDING'", job)
	if _, err = s.RecomputeJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	_ = s.Pool.QueryRow(ctx, "SELECT status FROM download_jobs WHERE id=$1", job).Scan(&parent)
	if parent != "PARTIAL_FAILED" {
		t.Fatalf("failed recovery falsely completed: %s", parent)
	}
}

func TestRecoveryFailedShardOverridesStaleCompletedParent(t *testing.T) {
	s := recoveryStore(t)
	ctx := context.Background()
	job, sh := seedRecovery(t, s, "RUNNING")
	var subject string
	if err := s.Pool.QueryRow(ctx, "SELECT subject_id FROM download_shards WHERE id=$1", sh).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, "INSERT INTO download_subjects(id,job_id,subject,status,progress) VALUES($1,$2,'address','COMPLETED',1)", subject, job); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE download_shards SET status='FAILED_FINAL' WHERE id=$1", sh); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecomputeJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.Pool.QueryRow(ctx, "SELECT status FROM download_jobs WHERE id=$1", job).Scan(&status); err != nil || status != "FAILED" {
		t.Fatalf("all failed shards must fail the job: %s %v", status, err)
	}
}

func TestRecoveryBatchAcknowledgementTiming(t *testing.T) {
	s := recoveryStore(t)
	ctx := context.Background()
	job, sh := seedRecovery(t, s, "RUNNING")
	updates := make([]PageIngestUpdate, 250)
	for i := range updates {
		updates[i] = PageIngestUpdate{seedPage(t, s, job, sh, i+1, "DURABLE"), 1000, 1000, 1000}
	}
	for round := 0; round < 3; round++ {
		start := time.Now()
		for _, u := range updates {
			if _, err := s.Pool.Exec(ctx, "UPDATE download_pages SET status='INGESTED',rows_parsed=$2,rows_inserted=$3,unique_rows=$4,ingested_at=now(),updated_at=now() WHERE id=$1", u.PageID, u.Parsed, u.Inserted, u.Unique); err != nil {
				t.Fatal(err)
			}
		}
		perPage := time.Since(start)
		_, _ = s.Pool.Exec(ctx, "UPDATE download_pages SET status='DURABLE' WHERE shard_id=$1", sh)
		start = time.Now()
		if err := s.UpdatePagesIngested(ctx, updates); err != nil {
			t.Fatal(err)
		}
		batch := time.Since(start)
		t.Logf("round=%d pages=250 old_per_page=%s batch=%s speedup=%.2fx", round+1, perPage, batch, float64(perPage)/float64(batch))
	}
	var rows int64
	_ = s.Pool.QueryRow(ctx, "SELECT sum(rows_inserted) FROM download_pages WHERE shard_id=$1 AND status='INGESTED'", sh).Scan(&rows)
	if rows != 250000 {
		t.Fatal("acknowledgement completeness", rows)
	}
}

func TestRecoverySplitRangeHasNoGapsOrOverlap(t *testing.T) {
	parts := splitRetryInterval(BlockInterval{61560074, 123120147})
	if len(parts) != 8 {
		t.Fatal(len(parts))
	}
	next := int64(61560074)
	for _, p := range parts {
		if p.From != next || p.To < p.From {
			t.Fatal(p)
		}
		next = p.To + 1
	}
	if next != 123120148 {
		t.Fatal(next)
	}
}
