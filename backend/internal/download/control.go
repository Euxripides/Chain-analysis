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
