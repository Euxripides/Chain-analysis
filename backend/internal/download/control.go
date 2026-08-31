package download

import (
	"context"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// controlLoop 处理 Job 生命周期转换：CREATED→规划、PAUSE/CANCEL 收敛、RESUMING→重规划。
func (sc *Scheduler) controlLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sc.planCreatedJobs(ctx)
			sc.convergePauseCancel(ctx)
			sc.resumeJobs(ctx)
			sc.finishSkippedJobs(ctx)
			sc.convergeCompleted(ctx)
		}
	}
}

func (sc *Scheduler) planCreatedJobs(ctx context.Context) {
	jobs, err := sc.store.ListJobs(ctx, model.JobCreated, 10, 0)
	if err != nil {
		return
	}
	for i := range jobs {
		j := jobs[i]
		sc.log.Info("planning job", "job_id", j.ID, "job_no", j.JobNo)
		if err := sc.planner.PlanJob(ctx, &j); err != nil {
			sc.log.Error("plan job failed", "job_id", j.ID, "err", err)
			continue
		}
		sc.publish("events.jobs."+j.ID, map[string]any{
			"type": "job_status", "job_id": j.ID, "status": model.JobQueued, "ts": time.Now(),
		})
	}
}

func (sc *Scheduler) convergePauseCancel(ctx context.Context) {
	jobs, err := sc.store.ListJobs(ctx, "", 200, 0)
	if err != nil {
		return
	}
	for i := range jobs {
		j := jobs[i]
		switch j.Status {
		case model.JobPauseReq:
			var active int64
			_ = sc.store.Pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM download_shards WHERE job_id=$1 AND status IN ('LEASED','RUNNING')", j.ID).Scan(&active)
			if active == 0 {
				if err := sc.store.SetJobStatus(ctx, j.ID, model.JobPaused); err == nil {
					sc.publish("events.jobs."+j.ID, map[string]any{
						"type": "job_status", "job_id": j.ID, "status": model.JobPaused, "ts": time.Now(),
					})
				}
			}
		case model.JobCancelReq:
			_, _ = sc.store.Pool.Exec(ctx,
				"UPDATE download_shards SET status='CANCELLED', updated_at=now() "+
					"WHERE job_id=$1 AND status NOT IN ('COMPLETED','CANCELLED','LEASED','RUNNING')", j.ID)
			var active int64
			_ = sc.store.Pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM download_shards WHERE job_id=$1 AND status IN ('LEASED','RUNNING')", j.ID).Scan(&active)
			if active == 0 {
				if err := sc.store.SetJobStatus(ctx, j.ID, model.JobCancelled); err == nil {
					sc.publish("events.jobs."+j.ID, map[string]any{
						"type": "job_status", "job_id": j.ID, "status": model.JobCancelled, "ts": time.Now(),
					})
				}
			}
		}
	}
}

func (sc *Scheduler) resumeJobs(ctx context.Context) {
	jobs, err := sc.store.ListJobs(ctx, model.JobResuming, 10, 0)
	if err != nil {
		return
	}
	for i := range jobs {
		j := jobs[i]
		sc.log.Info("resuming job", "job_id", j.ID)
		if err := sc.planner.ResumeJob(ctx, &j); err != nil {
			sc.log.Error("resume job failed", "job_id", j.ID, "err", err)
			_ = sc.store.SetJobStatusError(ctx, j.ID, model.JobFailed, err.Error())
			continue
		}
		sc.publish("events.jobs."+j.ID, map[string]any{
			"type": "job_status", "job_id": j.ID, "status": model.JobRunning, "ts": time.Now(),
		})
	}
}

// convergeCompleted 自愈收敛：RUNNING 任务全部 Shard 已完成且无执行中 Shard → 重算状态。
func (sc *Scheduler) convergeCompleted(ctx context.Context) {
	jobs, err := sc.store.ListJobs(ctx, model.JobRunning, 20, 0)
	if err != nil {
		return
	}
	for i := range jobs {
		j := jobs[i]
		var total, done, active int64
		_ = sc.store.Pool.QueryRow(ctx,
			"SELECT COUNT(*), COUNT(*) FILTER (WHERE status='COMPLETED'), "+
				"COUNT(*) FILTER (WHERE status IN ('LEASED','RUNNING')) FROM download_shards WHERE job_id=$1", j.ID).
			Scan(&total, &done, &active)
		if total > 0 && done == total && active == 0 {
			// 先自愈 subject：数据集全完成但 subject 仍 RUNNING 的直接置 COMPLETED
			_, _ = sc.store.Pool.Exec(ctx,
				"UPDATE download_subjects s SET status='COMPLETED', progress=1, updated_at=now() "+
					"WHERE s.job_id=$1 AND s.status='RUNNING' "+
					"AND EXISTS (SELECT 1 FROM download_datasets d WHERE d.subject_id=s.id) "+
					"AND NOT EXISTS (SELECT 1 FROM download_datasets d WHERE d.subject_id=s.id AND d.status NOT IN ('COMPLETED','SKIPPED'))",
				j.ID)
			if _, err := sc.store.RecomputeJob(ctx, j.ID); err == nil {
				sc.publish("events.jobs."+j.ID, map[string]any{
					"type": "job_status", "job_id": j.ID, "status": model.JobCompleted, "ts": time.Now(),
				})
			}
		}
	}
}

// finishSkippedJobs 全部数据集命中 Coverage（无 Shard）时收敛为 COMPLETED。
func (sc *Scheduler) finishSkippedJobs(ctx context.Context) {
	jobs, err := sc.store.ListJobs(ctx, model.JobQueued, 20, 0)
	if err != nil {
		return
	}
	for i := range jobs {
		j := jobs[i]
		var shards, done int64
		_ = sc.store.Pool.QueryRow(ctx,
			"SELECT COUNT(*), COUNT(*) FILTER (WHERE status IN ('COMPLETED','SKIPPED')) FROM download_shards WHERE job_id=$1", j.ID).
			Scan(&shards, &done)
		_ = shards
		subjects, err := sc.store.SubjectsByJob(ctx, j.ID)
		if err != nil {
			continue
		}
		allDone := true
		for _, s := range subjects {
			if s.Status != model.ItemCompleted && s.Status != model.ItemSkipped {
				allDone = false
				break
			}
		}
		if allDone {
			if _, err := sc.store.RecomputeJob(ctx, j.ID); err == nil {
				sc.publish("events.jobs."+j.ID, map[string]any{
					"type": "job_status", "job_id": j.ID, "status": model.JobCompleted, "ts": time.Now(),
				})
			}
		}
	}
}
