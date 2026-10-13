package store

import (
	"context"
	"fmt"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// RetryJobFailures 只把失败分片/对象重置为 PENDING，已完成分片与其成功游标原样保留。
// Only irrecoverable raw/cursor failures restart the shard; exhausted transient
// failures retain the checkpoint and completed pages.
// 返回重置的分片数，供 API 展示“本次只补缺口”的影响预览。
func (s *Store) RetryJobFailures(ctx context.Context, jobID string) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var stats map[string]any
	if err := tx.QueryRow(ctx, `SELECT status,stats FROM download_jobs WHERE id=$1 FOR UPDATE`, jobID).Scan(&status, &stats); err != nil {
		return 0, err
	}
	switch status {
	case model.JobFailed, model.JobPartialFailed:
	default:
		return 0, fmt.Errorf("job is %s; retry only applies to failed tasks", status)
	}
	if _, err := tx.Exec(ctx, `UPDATE download_shards SET page_state=COALESCE(page_state,'{}')||jsonb_build_object('raw_restart_required',true)
 WHERE job_id=$1 AND status='FAILED_FINAL' AND (error LIKE 'ingest unrecoverable:%' OR error LIKE '%page token belongs to another shard%' OR EXISTS(SELECT 1 FROM download_pages p WHERE p.shard_id=download_shards.id AND p.status='FAILED_FINAL'))`, jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM download_pages p USING download_shards s WHERE p.shard_id=s.id AND s.job_id=$1 AND (s.page_state->>'raw_restart_required')='true'`, jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM checkpoints c USING download_shards s WHERE c.shard_id=s.id AND s.job_id=$1 AND (s.page_state->>'raw_restart_required')='true'`, jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE download_shards SET page_token='',page_no=0,downloaded_rows=0,downloaded_bytes=0,raw_object='',fetched_at=NULL,ingested_at=NULL,validated_at=NULL,page_count=0,
 page_state=(page_state-'raw_restart_required'-'last_timestamp'-'last_tx_id'-'last_success_at'-'token_hash')
 WHERE job_id=$1 AND (page_state->>'raw_restart_required')='true'`, jobID); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE download_shards SET status='PENDING', retries=0, error='', lease_owner='', leased_until=NULL,lease_epoch=lease_epoch+1, updated_at=now()
		WHERE job_id=$1 AND status IN ('FAILED','FAILED_FINAL')`, jobID)
	if err != nil {
		return 0, err
	}
	reset := tag.RowsAffected()
	if reset == 0 {
		reset, err = restoreRemovedRetryRanges(ctx, tx, jobID)
		if err != nil {
			return 0, err
		}
		if reset == 0 {
			return 0, fmt.Errorf("no failed shards remain; restore the missing planned range before retrying")
		}
	}
	v := RetryState(stats)
	if v.State == "" {
		v = defaultDownloadRetry(time.Now())
	}
	if v.Requests >= v.MaxRequests || v.Credits >= v.MaxCredits || v.CostMicros >= v.MaxCostMicros || time.Now().After(v.ExpiresAt) {
		return 0, ErrDownloadRetryBudget
	}
	v.State = "active"
	v.Reason = ""
	if err = saveRetryState(ctx, tx, jobID, v); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE download_datasets SET status='PENDING', updated_at=now() WHERE job_id=$1 AND status='FAILED'`, jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE download_subjects s SET status='PENDING', updated_at=now() WHERE job_id=$1 AND (status='FAILED' OR EXISTS(SELECT 1 FROM download_datasets d WHERE d.subject_id=s.id AND d.status='PENDING'))`, jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE download_jobs SET status='QUEUED', error='', updated_at=now() WHERE id=$1`, jobID); err != nil {
		return 0, err
	}
	return reset, tx.Commit(ctx)
}
