package store

import (
	"context"
	"fmt"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// RetryJobFailures 只把失败分片/对象重置为 PENDING，已完成分片与其成功游标原样保留。
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
	tag, err := tx.Exec(ctx, `
		UPDATE download_shards SET status='PENDING', retries=0, error='', lease_owner='', leased_until=NULL, updated_at=now()
		WHERE job_id=$1 AND status='FAILED'`, jobID)
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
