package store

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// RecordVolumeCommit accounts for one durable page exactly once, recomputes the
// density-aware projection, and atomically closes this dataset to new work when
// a threshold is crossed. Replaying a checkpoint is therefore harmless.
func (s *Store) RecordVolumeCommit(ctx context.Context, sh model.Shard, pageNo int, rows, providerCalls int64, cost float64, minBlock, maxBlock int64, fullPage bool, limits model.VolumeGuardThresholds) (model.VolumeGuard, bool, error) {
	if !limits.Enabled || rows < 0 {
		return model.VolumeGuard{}, false, nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return model.VolumeGuard{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `INSERT INTO download_volume_guards(job_id,subject_id,dataset_id,dataset)
		VALUES($1,$2,$3,$4) ON CONFLICT(dataset_id) DO NOTHING`, sh.JobID, sh.SubjectID, sh.DatasetID, sh.Dataset)
	if err != nil {
		return model.VolumeGuard{}, false, err
	}
	var state string
	var override bool
	if err = tx.QueryRow(ctx, `SELECT state,guard_override FROM download_volume_guards WHERE dataset_id=$1 FOR UPDATE`, sh.DatasetID).Scan(&state, &override); err != nil {
		return model.VolumeGuard{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO download_volume_guard_commits
		(shard_id,page_no,dataset_id,rows_committed,provider_calls,estimated_cost_usd,min_block,max_block,full_page)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(shard_id,page_no) DO NOTHING`,
		sh.ID, pageNo, sh.DatasetID, rows, providerCalls, cost, minBlock, maxBlock, fullPage)
	if err != nil {
		return model.VolumeGuard{}, false, err
	}

	var actualRows, actualCalls, pages, fullPages, observedSpan, totalSpan, completed, total int64
	var actualCost, elapsed float64
	err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(rows_committed),0),COALESCE(SUM(provider_calls),0),
		COALESCE(SUM(estimated_cost_usd),0),COUNT(*),COUNT(*) FILTER(WHERE full_page),
		COALESCE(SUM(GREATEST(max_block-min_block+1,0)),0),
		COALESCE(EXTRACT(EPOCH FROM (now()-MIN(committed_at))),0)
		FROM download_volume_guard_commits WHERE dataset_id=$1`, sh.DatasetID).
		Scan(&actualRows, &actualCalls, &actualCost, &pages, &fullPages, &observedSpan, &elapsed)
	if err != nil {
		return model.VolumeGuard{}, false, err
	}
	err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(GREATEST(to_block-from_block+1,1)) FILTER(WHERE `+EffectiveShardSQL("")+`),0),
		COUNT(*) FILTER(WHERE status='COMPLETED' AND `+EffectiveShardSQL("")+`),COUNT(*) FILTER(WHERE `+EffectiveShardSQL("")+`)
		FROM download_shards WHERE dataset_id=$1`, sh.DatasetID).Scan(&totalSpan, &completed, &total)
	if err != nil {
		return model.VolumeGuard{}, false, err
	}

	coverage := 0.0
	if totalSpan > 0 {
		coverage = math.Min(1, float64(observedSpan)/float64(totalSpan))
	}
	fullRatio := 0.0
	if pages > 0 {
		fullRatio = float64(fullPages) / float64(pages)
	}
	confidence := math.Min(0.98, 0.35+math.Min(0.35, float64(pages)*0.035)+math.Min(0.20, coverage)+0.08*fullRatio)
	projectedMid := actualRows
	if observedSpan > 0 && totalSpan > observedSpan {
		projectedMid = int64(math.Ceil(float64(actualRows) * float64(totalSpan) / float64(observedSpan)))
	}
	if projectedMid < actualRows {
		projectedMid = actualRows
	}
	projectedLow := max64(actualRows, int64(float64(projectedMid)*0.70))
	projectedHigh := max64(projectedMid, int64(math.Ceil(float64(projectedMid)*1.40)))
	ratio := 1.0
	if actualRows > 0 {
		ratio = float64(projectedMid) / float64(actualRows)
	}
	projectedCalls := int64(math.Ceil(float64(actualCalls) * ratio))
	projectedCost := actualCost * ratio
	projectedDuration := elapsed * ratio
	risk := volumeRisk(projectedMid, limits)
	trigger, reason := guardTriggered(actualRows, projectedMid, confidence, limits, override, state)
	newlyTriggered := trigger && state != model.ItemWaitingUserConfirmation
	if trigger {
		state = model.ItemWaitingUserConfirmation
	}
	_, err = tx.Exec(ctx, `UPDATE download_volume_guards SET state=$2,reason=CASE WHEN $3<>'' THEN $3 ELSE reason END,
		risk_level=$4,actual_rows=$5,projected_rows_low=$6,projected_rows_mid=$7,projected_rows_high=$8,
		confidence=$9,actual_provider_calls=$10,projected_provider_calls=$11,actual_cost_usd=$12,
		projected_cost_usd=$13,elapsed_seconds=$14,projected_duration_seconds=$15,
		guard_triggered_at=CASE WHEN $16 AND guard_triggered_at IS NULL THEN now() ELSE guard_triggered_at END,updated_at=now()
		WHERE dataset_id=$1`, sh.DatasetID, state, reason, risk, actualRows, projectedLow, projectedMid, projectedHigh,
		confidence, actualCalls, projectedCalls, actualCost, projectedCost, elapsed, projectedDuration, trigger)
	if err != nil {
		return model.VolumeGuard{}, false, err
	}
	if newlyTriggered {
		_, err = tx.Exec(ctx, `UPDATE download_datasets SET status=$2,stats=COALESCE(stats,'{}'::jsonb)||
			jsonb_build_object('coverage','PARTIAL','volume_guard_reason',$3),updated_at=now() WHERE id=$1`, sh.DatasetID, model.ItemWaitingUserConfirmation, reason)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE download_shards SET status='PAUSED',lease_owner='',leased_until=NULL,updated_at=now()
				WHERE dataset_id=$1 AND status IN ('PENDING','FAILED_RETRYABLE')`, sh.DatasetID)
		}
		if err != nil {
			return model.VolumeGuard{}, false, err
		}
		_, _ = tx.Exec(ctx, `UPDATE download_jobs j SET status=$2,updated_at=now() WHERE id=$1
			AND NOT EXISTS(SELECT 1 FROM download_datasets d WHERE d.job_id=j.id AND d.status NOT IN ('WAITING_USER_CONFIRMATION','COMPLETED','SKIPPED','ABORTED_BY_USER'))`, sh.JobID, model.JobWaitingUserConfirmation)
	}
	if err = tx.Commit(ctx); err != nil {
		return model.VolumeGuard{}, false, err
	}
	guard, err := s.GetVolumeGuard(ctx, sh.JobID, sh.SubjectID, sh.Dataset)
	return guard, newlyTriggered, err
}

// guardTriggered 判定是否停下来等人工确认（纯函数，便于单测）。
//
// 生产策略（2026-09-15 起）：**单地址单 dataset 超过 100 万行即触发** ——
// 实际行数到线，或预测行数到线且置信度达标，二者任一成立都暂停该地址的
// 下载并弹窗让用户选择继续/终止。阈值全部来自配置
// （TRON_VOLUME_HARD_ACTUAL_ROWS / TRON_VOLUME_HARD_PROJECTED_ROWS）。
func guardTriggered(actualRows, projectedMid int64, confidence float64, l model.VolumeGuardThresholds, override bool, state string) (bool, string) {
	if override || state == model.ItemAbortedByUser {
		return false, ""
	}
	switch {
	case actualRows >= l.HardActualRows:
		return true, model.GuardReasonActualRows
	case projectedMid >= l.ExtremeRows && confidence >= l.ExtremeConfidence:
		return true, model.GuardReasonProjectedRows
	case projectedMid >= l.HardProjectedRows && confidence >= l.ProjectedConfidence:
		return true, model.GuardReasonProjectedRows
	}
	return false, ""
}

func volumeRisk(projected int64, l model.VolumeGuardThresholds) string {
	switch {
	case projected >= l.SuperExtremeRows:
		return model.GuardSuperExtreme
	case projected >= l.ExtremeRows:
		return model.GuardExtreme
	case projected >= l.HardProjectedRows:
		return model.GuardHuge
	case projected >= l.WarnRows:
		return model.GuardLarge
	default:
		return model.GuardNormal
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (s *Store) DatasetGuardState(ctx context.Context, datasetID string) (string, error) {
	var state string
	err := s.Pool.QueryRow(ctx, `SELECT state FROM download_volume_guards WHERE dataset_id=$1`, datasetID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ItemRunning, nil
	}
	return state, err
}

func scanVolumeGuard(row pgx.Row) (model.VolumeGuard, error) {
	var g model.VolumeGuard
	err := row.Scan(&g.JobID, &g.SubjectID, &g.Subject, &g.DatasetID, &g.Dataset, &g.State, &g.Reason, &g.RiskLevel,
		&g.ActualRows, &g.ProjectedRowsLow, &g.ProjectedRowsMid, &g.ProjectedRowsHigh, &g.Confidence,
		&g.ActualProviderCalls, &g.ProjectedProviderCalls, &g.ActualCostUSD, &g.ProjectedCostUSD, &g.ElapsedSeconds,
		&g.ProjectedDurationSeconds, &g.CompletedShards, &g.TotalShards, &g.Override, &g.TriggeredAt, &g.ApprovedAt,
		&g.ApprovedBy, &g.ApprovedProjection, &g.UpdatedAt)
	return g, err
}

const volumeGuardSelect = `SELECT g.job_id,g.subject_id,s.subject,g.dataset_id,g.dataset,g.state,g.reason,g.risk_level,
	g.actual_rows,g.projected_rows_low,g.projected_rows_mid,g.projected_rows_high,g.confidence,
	g.actual_provider_calls,g.projected_provider_calls,g.actual_cost_usd,g.projected_cost_usd,g.elapsed_seconds,
	g.projected_duration_seconds,(SELECT COUNT(*) FROM download_shards sh WHERE sh.dataset_id=g.dataset_id AND sh.status='COMPLETED'),
	(SELECT COUNT(*) FROM download_shards sh WHERE sh.dataset_id=g.dataset_id),g.guard_override,g.guard_triggered_at,
	g.guard_approved_at,g.guard_approved_by,g.guard_approved_projection,g.updated_at
	FROM download_volume_guards g JOIN download_subjects s ON s.id=g.subject_id`

func (s *Store) GetVolumeGuard(ctx context.Context, jobID, subjectID, dataset string) (model.VolumeGuard, error) {
	return scanVolumeGuard(s.Pool.QueryRow(ctx, volumeGuardSelect+` WHERE g.job_id=$1 AND g.subject_id=$2 AND g.dataset=$3`, jobID, subjectID, dataset))
}

func (s *Store) VolumeGuardsByJob(ctx context.Context, jobID string) ([]model.VolumeGuard, error) {
	rows, err := s.Pool.Query(ctx, volumeGuardSelect+` WHERE g.job_id=$1 ORDER BY g.guard_triggered_at NULLS LAST,s.subject,g.dataset`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.VolumeGuard{}
	for rows.Next() {
		g, e := scanVolumeGuard(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) ContinueVolumeGuard(ctx context.Context, jobID, subject, dataset, approvedBy string) (model.VolumeGuard, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return model.VolumeGuard{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var subjectID, datasetID string
	err = tx.QueryRow(ctx, `SELECT s.id,d.id FROM download_subjects s JOIN download_datasets d ON d.subject_id=s.id
		WHERE s.job_id=$1 AND s.subject=$2 AND d.dataset=$3 FOR UPDATE OF d`, jobID, subject, dataset).Scan(&subjectID, &datasetID)
	if err != nil {
		return model.VolumeGuard{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE download_volume_guards SET state='RUNNING',guard_override=true,guard_approved_at=now(),
		guard_approved_by=$2,guard_approved_projection=projected_rows_mid,updated_at=now() WHERE dataset_id=$1 AND state='WAITING_USER_CONFIRMATION'`, datasetID, approvedBy)
	if err != nil || tag.RowsAffected() == 0 {
		if err == nil {
			err = fmt.Errorf("volume guard is not waiting for confirmation")
		}
		return model.VolumeGuard{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE download_datasets SET status='RUNNING',updated_at=now() WHERE id=$1`, datasetID)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE download_shards SET status='PENDING',updated_at=now() WHERE dataset_id=$1 AND status='PAUSED'`, datasetID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE download_jobs SET status='RUNNING',updated_at=now() WHERE id=$1 AND status='WAITING_USER_CONFIRMATION'`, jobID)
	}
	if err != nil {
		return model.VolumeGuard{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return model.VolumeGuard{}, err
	}
	return s.GetVolumeGuard(ctx, jobID, subjectID, dataset)
}

func (s *Store) AbortVolumeGuard(ctx context.Context, jobID, subject, dataset string) (model.VolumeGuard, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return model.VolumeGuard{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var subjectID, datasetID string
	err = tx.QueryRow(ctx, `SELECT s.id,d.id FROM download_subjects s JOIN download_datasets d ON d.subject_id=s.id
		WHERE s.job_id=$1 AND s.subject=$2 AND d.dataset=$3 FOR UPDATE OF d`, jobID, subject, dataset).Scan(&subjectID, &datasetID)
	if err != nil {
		return model.VolumeGuard{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE download_volume_guards SET state=$2,updated_at=now() WHERE dataset_id=$1 AND state='WAITING_USER_CONFIRMATION'`, datasetID, model.ItemAbortedByUser)
	if err != nil || tag.RowsAffected() == 0 {
		if err == nil {
			err = fmt.Errorf("volume guard is not waiting for confirmation")
		}
		return model.VolumeGuard{}, err
	}
	marker := `{"coverage":"PARTIAL","aborted_by_user":true}`
	_, err = tx.Exec(ctx, `UPDATE download_datasets SET status=$2,stats=COALESCE(stats,'{}'::jsonb)||$3::jsonb,updated_at=now() WHERE id=$1`, datasetID, model.ItemAbortedByUser, marker)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE download_shards SET status='CANCELLED',error='aborted by user after volume guard',lease_owner='',leased_until=NULL,updated_at=now() WHERE dataset_id=$1 AND status IN ('PENDING','FAILED_RETRYABLE','PAUSED')`, datasetID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE download_subjects s SET status=$2,stats=COALESCE(stats,'{}'::jsonb)||$3::jsonb,updated_at=now() WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM download_datasets d WHERE d.subject_id=s.id AND d.status NOT IN ('COMPLETED','SKIPPED','ABORTED_BY_USER'))`, subjectID, model.ItemAbortedByUser, marker)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE download_jobs j SET status=$2,stats=COALESCE(stats,'{}'::jsonb)||$3::jsonb,updated_at=now() WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM download_datasets d WHERE d.job_id=j.id AND d.status NOT IN ('COMPLETED','SKIPPED','ABORTED_BY_USER'))`, jobID, model.JobAbortedByUser, marker)
	}
	if err != nil {
		return model.VolumeGuard{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return model.VolumeGuard{}, err
	}
	return s.GetVolumeGuard(ctx, jobID, subjectID, dataset)
}
