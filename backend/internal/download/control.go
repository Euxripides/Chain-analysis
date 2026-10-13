package download

import (
	"context"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// pauseConvergenceGrace 是 PAUSE_REQUESTED 允许被 FETCHED/INGESTED 残留
// Shard 阻塞的最长时间；超过后按"没有在途工作"处理，保证暂停一定能收敛。
const pauseConvergenceGrace = 90 * time.Second

// pauseBlocksConvergence 判断暂停收敛是否还要等待。
//   - 仍被 LEASED/RUNNING 的 Shard：一定要等（不能打断在途工作）。
//   - 只有 FETCHED/INGESTED 残留：宽限期内等待，超时后收敛为 PAUSED，
//     否则一个永远无法完成的残留 Shard 会把任务钉死在 PAUSE_REQUESTED，
//     用户既无法完成暂停也无法恢复（现场 DL-20260909-0001）。
//   - 无法读取暂停请求时间时按保守等待处理。
func pauseBlocksConvergence(active, leftovers int64, sinceRequest time.Duration, haveRequestTime bool) bool {
	if active > 0 {
		return true
	}
	if leftovers == 0 {
		return false
	}
	if !haveRequestTime {
		return true
	}
	return sinceRequest < pauseConvergenceGrace
}

// controlLoop 处理 Job 生命周期转换：CREATED→规划、PAUSE/CANCEL 收敛、RESUMING→重规划。
func (sc *Scheduler) controlLoop(ctx context.Context) {
	// Job creation is latency-sensitive and only needs the small CREATED /
	// RESUMING indexes. Keep it off the slower lifecycle sweep so a new cold
	// download does not spend up to two seconds waiting before planning starts.
	fast := time.NewTicker(200 * time.Millisecond)
	maintenance := time.NewTicker(2 * time.Second)
	defer fast.Stop()
	defer maintenance.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-fast.C:
			sc.planCreatedJobs(ctx)
			sc.resumeJobs(ctx)
		case <-maintenance.C:
			sc.convergePauseCancel(ctx)
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
	// 必须按状态分别取，而不是 ListJobs("", 200, 0)：后者按 created_at DESC 只返回
	// 最新 200 个任务，超过 200 个更新任务之后的历史 PAUSE_REQUESTED /
	// CANCEL_REQUESTED 永远扫不到（现场 DL-20260907-0106：210 个更新的任务把它挡在
	// 窗口外，取消请求永远不收敛）。按状态取时每个状态有自己的窗口。
	for _, status := range []string{model.JobPauseReq, model.JobCancelReq} {
		jobs, err := sc.store.ListJobs(ctx, status, 200, 0)
		if err != nil {
			continue
		}
		for i := range jobs {
			sc.convergePauseCancelJob(ctx, jobs[i])
		}
	}
}

// convergePauseCancelJob 收敛单个 PAUSE_REQUESTED / CANCEL_REQUESTED 任务。
func (sc *Scheduler) convergePauseCancelJob(ctx context.Context, j model.DownloadJob) {
	switch j.Status {
	case model.JobPauseReq:
		var active, leftovers int64
		_ = sc.store.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FILTER (WHERE status IN ('LEASED','RUNNING')), "+
				"COUNT(*) FILTER (WHERE status IN ('FETCHED','INGESTED')) "+
				"FROM download_shards WHERE job_id=$1", j.ID).Scan(&active, &leftovers)
		// 暂停收敛只允许"正在被处理"的 Shard 阻塞；FETCHED/INGESTED 残留
		// 只有在宽限期内才算阻塞。否则一个永远无法完成的 Shard（例如原始
		// spool 文件已丢失）会把任务永久钉在 PAUSE_REQUESTED，用户既不能
		// 完成暂停也不能恢复（现场 DL-20260909-0001，2026-09-08 起）。
		var sinceRequest time.Duration
		haveRequestTime := false
		if leftovers > 0 {
			var requestedAt time.Time
			if err := sc.store.Pool.QueryRow(ctx, "SELECT updated_at FROM download_jobs WHERE id=$1", j.ID).Scan(&requestedAt); err == nil {
				sinceRequest = time.Since(requestedAt)
				haveRequestTime = true
			}
		}
		blocked := pauseBlocksConvergence(active, leftovers, sinceRequest, haveRequestTime)
		if !blocked {
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
			"SELECT COUNT(*) FROM download_shards WHERE job_id=$1 AND status IN ('LEASED','RUNNING','FETCHED','INGESTED')", j.ID).Scan(&active)
		if active == 0 {
			if err := sc.store.SetJobStatus(ctx, j.ID, model.JobCancelled); err == nil {
				sc.publish("events.jobs."+j.ID, map[string]any{
					"type": "job_status", "job_id": j.ID, "status": model.JobCancelled, "ts": time.Now(),
				})
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
			// 恢复失败必须保持可重试：旧实现直接把任务置为 FAILED，
			// 用户一次失败的恢复就永久失去这个任务（现场投诉"暂停后无法恢复"）。
			sc.log.Error("resume job failed", "job_id", j.ID, "err", err)
			if rerr := sc.store.SetJobStatusError(ctx, j.ID, model.JobPaused, "resume failed: "+err.Error()); rerr != nil {
				sc.log.Error("resume rollback failed", "job_id", j.ID, "err", rerr)
			}
			continue
		}
		sc.publish("events.jobs."+j.ID, map[string]any{
			"type": "job_status", "job_id": j.ID, "status": model.JobRunning, "ts": time.Now(),
		})
	}
}

// emptyJobGrace 是零 Subject 任务被判为僵尸前的最短存活时间。规划是
// CREATED→PLANNING→QUEUED 的短过程，规划失败会留在 CREATED 由 planCreatedJobs
// 重试；因此存活超过宽限期的 QUEUED/RUNNING 空壳只可能是规划被中断留下的
// （现场 SSEPROBE-20260913T*：9 个任务 0 Subject / 0 Shard，永远 RUNNING）。
const emptyJobGrace = time.Hour

// convergeCompleted 自愈收敛：任务的全部有效 Shard 都已终态且无在途 Shard → 重算状态。
//
// 同时覆盖 RUNNING 与 QUEUED：Shard 在 Fetch 阶段终态失败时，runShard 的失败分支
// 只写 Shard 行，不做 Dataset/Subject/Job 汇总；单 Shard 数据集没有兄弟 Shard 的
// 收敛事件兜底，任务会永远停在 QUEUED/RUNNING（现场 DL-20260906-0119 等 19 个任务、
// DL-20260920-0007）。因此这里按"全部终态"兜底，而不是依赖某次 finalize 事件。
func (sc *Scheduler) convergeCompleted(ctx context.Context) {
	for _, status := range []string{
		model.JobRunning, model.JobQueued,
		model.JobPaused, model.JobPausedFallbackTooExpensive,
	} {
		jobs, err := sc.store.ListJobs(ctx, status, 20, 0)
		if err != nil {
			continue
		}
		for i := range jobs {
			sc.convergeJob(ctx, jobs[i])
		}
	}
}

// convergeJob 收敛单个 RUNNING/QUEUED 任务，两种形态：
//   - 空壳（0 Subject / 0 Shard）超过宽限期 → CANCELLED（从未规划，不是数据失败）；
//   - 全部有效 Shard 终态且无在途 Shard → 重算 Dataset/Subject/Job。
func (sc *Scheduler) convergeJob(ctx context.Context, j model.DownloadJob) {
	var total, done, terminal, active, subjects int64
	_ = sc.store.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FILTER (WHERE "+store.EffectiveShardSQL("")+"), "+
			"COUNT(*) FILTER (WHERE status='COMPLETED' AND "+store.EffectiveShardSQL("")+"), "+
			"COUNT(*) FILTER (WHERE status IN ('COMPLETED','FAILED_FINAL','CANCELLED') AND "+store.EffectiveShardSQL("")+"), "+
			"COUNT(*) FILTER (WHERE status IN ('LEASED','RUNNING') AND "+store.EffectiveShardSQL("")+"), "+
			"(SELECT COUNT(*) FROM download_subjects ds WHERE ds.job_id=$1) "+
			"FROM download_shards WHERE job_id=$1", j.ID).
		Scan(&total, &done, &terminal, &active, &subjects)
	if subjects == 0 && total == 0 {
		// 只回收"从未规划"的运行/排队空壳；显式暂停的空任务保留用户意图。
		if j.Status != model.JobRunning && j.Status != model.JobQueued {
			return
		}
		if time.Since(j.CreatedAt) < emptyJobGrace {
			return
		}
		const reason = "aborted before planning: no subjects were recorded"
		if err := sc.store.SetJobStatusError(ctx, j.ID, model.JobCancelled, reason); err != nil {
			sc.log.Error("converge empty job failed", "job_id", j.ID, "err", err)
			return
		}
		sc.log.Warn("empty job converged as cancelled",
			"job_id", j.ID, "job_no", j.JobNo, "age", time.Since(j.CreatedAt).String())
		sc.publish("events.jobs."+j.ID, map[string]any{
			"type": "job_status", "job_id": j.ID, "status": model.JobCancelled, "ts": time.Now(),
		})
		return
	}
	// 收敛条件从"全部成功"放宽为"全部终态"：FAILED_FINAL Shard 同样结束该任务，
	// 否则一个只剩永久失败 Shard 的任务会永远 RUNNING（现场 DL-20260915-0007/0008/0009）。
	if total == 0 || terminal != total || active != 0 {
		return
	}
	// 暂停态任务若已无任何剩余工作（全部有效 Shard 终态），必须先脱掉暂停态：
	// RecomputeJob 对 PAUSED/PAUSE_REQUESTED 直接返回、不写终态，于是"已经跑完
	// 但被暂停"的任务会永远停在 PAUSED（现场 DL-20260831-0036 1800/1800 完成、
	// DL-20260909-0001 50/51 全终态）。只对确实没有在途工作的任务这样做。
	if j.Status == model.JobPaused || j.Status == model.JobPausedFallbackTooExpensive || j.Status == model.JobPauseReq {
		if err := sc.store.SetJobStatus(ctx, j.ID, model.JobRunning); err != nil {
			sc.log.Error("converge paused job failed", "job_id", j.ID, "err", err)
			return
		}
		sc.log.Info("paused job has no remaining work, converging",
			"job_id", j.ID, "job_no", j.JobNo, "shards_total", total)
	}
	subjectList, serr := sc.store.SubjectsByJob(ctx, j.ID)
	if serr != nil {
		return
	}
	for _, subj := range subjectList {
		items, derr := sc.store.DatasetsBySubject(ctx, subj.ID)
		if derr != nil {
			sc.log.Warn("converge datasets list failed", "job_id", j.ID, "subject_id", subj.ID, "err", derr)
			continue
		}
		for _, item := range items {
			if derr := sc.store.RecomputeDataset(ctx, item.ID); derr != nil {
				sc.log.Warn("converge dataset recompute failed", "dataset_id", item.ID, "err", derr)
			}
		}
		if derr := sc.store.RecomputeSubject(ctx, subj.ID); derr != nil {
			sc.log.Warn("converge subject recompute failed", "subject_id", subj.ID, "err", derr)
		}
	}
	if _, err := sc.store.RecomputeJob(ctx, j.ID); err == nil {
		if after, gerr := sc.store.GetJob(ctx, j.ID); gerr == nil && after.Status != model.JobRunning && after.Status != model.JobQueued {
			sc.log.Info("job converged",
				"job_id", j.ID, "job_no", j.JobNo, "status", after.Status,
				"shards_total", total, "shards_done", done)
			sc.publish("events.jobs."+j.ID, map[string]any{
				"type": "job_status", "job_id": j.ID, "status": after.Status, "ts": time.Now(),
			})
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
		if shards > 0 && done != shards {
			continue
		} // Stale parent statuses must not hide unfinished retry shards.
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
