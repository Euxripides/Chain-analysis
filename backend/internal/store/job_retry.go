package store

import (
	"context"
	"fmt"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// RetryJobFailures 只把失败分片/对象重置为 PENDING，已完成分片与其成功游标原样保留。
// FAILED_FINAL 表示该分片的持久页已经不可恢复；这种分片必须丢弃页级 checkpoint
// 并从分片起点重新抓取，否则 worker 会继续重放同一个已经丢失的 spool 文件。
// 返回重置的分片数，供 API 展示“本次只补缺口”的影响预览。
func (s *Store) RetryJobFailures(ctx context.Context, jobID string) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM download_jobs WHERE id=$1 FOR UPDATE`, jobID).Scan(&status); err != nil {
		return 0, err
	}
	switch status {
	case model.JobFailed, model.JobPartialFailed, model.JobPausedFallbackTooExpensive:
	default:
		return 0, fmt.Errorf("job is %s; retry only applies to failed tasks", status)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM download_pages p USING download_shards s
		WHERE p.shard_id=s.id AND s.job_id=$1 AND s.status='FAILED_FINAL'`, jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM checkpoints c USING download_shards s
		WHERE c.shard_id=s.id AND s.job_id=$1 AND s.status='FAILED_FINAL'`, jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE download_shards SET
			page_token='', page_no=0, downloaded_rows=0, downloaded_bytes=0,
			raw_object='', fetched_at=NULL, ingested_at=NULL, validated_at=NULL, page_count=0,
			page_state=jsonb_strip_nulls(jsonb_build_object(
				'query_mode',page_state->'query_mode',
				'tron_address',page_state->'tron_address',
				'tron_dataset',page_state->'tron_dataset',
				'tron_provider',page_state->'tron_provider'))
		WHERE job_id=$1 AND status='FAILED_FINAL'`, jobID); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE download_shards SET status='PENDING', retries=0, error='', lease_owner='', leased_until=NULL, updated_at=now()
		WHERE job_id=$1 AND status IN ('FAILED','FAILED_FINAL')`, jobID)
	if err != nil {
		return 0, err
	}
	reset := tag.RowsAffected()
	if _, err := tx.Exec(ctx, `UPDATE download_datasets SET status='PENDING', updated_at=now() WHERE job_id=$1 AND status='FAILED'`, jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE download_subjects SET status='PENDING', updated_at=now() WHERE job_id=$1 AND status='FAILED'`, jobID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE download_jobs SET status='QUEUED', error='', updated_at=now() WHERE id=$1`, jobID); err != nil {
		return 0, err
	}
	return reset, tx.Commit(ctx)
}
