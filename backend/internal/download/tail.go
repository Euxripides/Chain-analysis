// Tail Acceleration（文档 §15）：当其它 Dataset 已完成、只剩最后一个大型 Dataset 时，
// 将空闲 Provider Slots 分配给剩余 Range（并发提升 + PENDING 未启动 Range 再拆分）。
package download

import (
	"context"
	"math"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
)

// tailLoop 周期检测单 Dataset 拖尾并加速。
func (sc *Scheduler) tailLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sc.tailOnce(ctx)
		}
	}
}

func (sc *Scheduler) tailOnce(ctx context.Context) {
	if !sc.cfg.Ankr.TailAccelerationEnabled {
		return
	}
	jobs, err := sc.store.ListJobs(ctx, model.JobRunning, 20, 0)
	if err != nil {
		return
	}
	for i := range jobs {
		j := jobs[i]
		// 找出仍未完成（且非 SKIPPED）的 dataset
		var remaining []model.DatasetItem
		var remainingCount int
		for _, subj := range mustSubjects(ctx, sc, j.ID) {
			items, err := sc.store.DatasetsBySubject(ctx, subj.ID)
			if err != nil {
				continue
			}
			for _, item := range items {
				if item.Status == model.ItemCompleted || item.Status == model.ItemSkipped {
					continue
				}
				remainingCount++
				if len(remaining) < 4 {
					remaining = append(remaining, item)
				}
			}
		}
		if remainingCount != 1 {
			sc.clearTailState(ctx, j.ID)
			if sc.leaseMode() != "fifo" {
				_ = sc.store.ClearTailBoostByJob(ctx, j.ID)
			}
			continue
		}
		item := remaining[0]
		sc.accelerateTail(ctx, &j, item)
		// V1.4 §9 Tail Boost：进入单 dataset 拖尾时提升其 PENDING shard 在全局队列中的权重
		if sc.leaseMode() != "fifo" {
			if rem, err := sc.store.RemainingPendingShards(ctx, item.ID); err == nil && rem <= int64(sc.cfg.Ankr.TailBoostRemainingShards) {
				if n, err := sc.store.MarkTailBoostShards(ctx, item.ID); err == nil && n > 0 {
					observability.AnkrFetchTailBoostTotal.Add(float64(n))
					sc.log.Info("v1.4 tail boost marked", "dataset", item.Dataset, "shards", n)
				}
			}
		}
	}
}

func mustSubjects(ctx context.Context, sc *Scheduler, jobID string) []model.Subject {
	subs, err := sc.store.SubjectsByJob(ctx, jobID)
	if err != nil {
		return nil
	}
	return subs
}

func (sc *Scheduler) clearTailState(ctx context.Context, jobID string) {
	if job, err := sc.store.GetJob(ctx, jobID); err == nil && job.Stats != nil {
		_, accelerated := job.Stats["tail_acceleration"]
		_, splitDone := job.Stats["tail_split_done"]
		if accelerated || splitDone {
			delete(job.Stats, "tail_acceleration")
			delete(job.Stats, "tail_split_done")
			_ = sc.store.SetJobStats(ctx, jobID, job.Stats)
		}
	}
}

func (sc *Scheduler) accelerateTail(ctx context.Context, job *model.DownloadJob, item model.DatasetItem) {
	// 1) 并发提升（§15：4 shards → 6 → 8，仍受 MaxConcurrency / 预算约束）
	cur := sc.currentConcurrency()
	target := minInt(sc.cfg.Ankr.MaxConcurrency, cur+sc.cfg.Ankr.TailBoostConcurrency/2)
	if target > cur {
		sc.setConcurrency(target)
	}
	// 2) 拆分未启动的 PENDING 大 Range（§14：只拆尚未开始的明确 block range）
	if sc.cfg.Ankr.RangeShardingEnabled && tailSplitAllowed(job) {
		if n, err := sc.store.SplitPendingShardsForDataset(ctx, item.ID, 4, sc.cfg.Ankr.TailSplitWindowMin); err != nil {
			sc.log.Warn("tail split failed", "dataset", item.Dataset, "err", err)
		} else if n > 0 {
			sc.log.Info("tail acceleration split pending ranges", "dataset", item.Dataset, "splits", n, "concurrency", target)
		}
		// A tail phase may split once. Repeating every tick exponentially grows
		// the queue and invalidates fixed-shard benchmark experiments.
		if job.Stats == nil {
			job.Stats = map[string]any{}
		}
		job.Stats["tail_split_done"] = true
	}
	// 3) ETA 估计（§12 画像密度 × 剩余区块 ÷ Fetch Goodput）
	eta := sc.estimateTailETA(ctx, job.Chain, item)
	if eta > 0 {
		observability.DownloadTailEtaSeconds.Set(eta)
	}
	// 4) 标记
	if job.Stats == nil {
		job.Stats = map[string]any{}
	}
	job.Stats["tail_acceleration"] = true
	job.Stats["tail_dataset"] = item.Dataset
	if eta > 0 {
		job.Stats["tail_eta_seconds"] = int64(eta)
	}
	_ = sc.store.SetJobStats(ctx, job.ID, job.Stats)
}

func tailSplitAllowed(job *model.DownloadJob) bool {
	if job == nil || job.Source == "benchmark" {
		return false
	}
	if job.Stats == nil {
		return true
	}
	done, _ := job.Stats["tail_split_done"].(bool)
	return !done
}

func (sc *Scheduler) estimateTailETA(ctx context.Context, chain string, item model.DatasetItem) float64 {
	// 剩余区块：PENDING + RUNNING + FETCHED 的未完成范围总和
	var remainingBlocks int64
	rows, err := sc.store.Pool.Query(ctx, "SELECT from_block, to_block FROM download_shards WHERE dataset_id=$1 AND status IN ('PENDING','FAILED_RETRYABLE','LEASED','RUNNING','FETCHED','INGESTED')", item.ID)
	if err != nil {
		return 0
	}
	for rows.Next() {
		var from, to int64
		if err := rows.Scan(&from, &to); err != nil {
			continue
		}
		if to >= from {
			remainingBlocks += to - from + 1
		}
	}
	rows.Close()
	if remainingBlocks <= 0 {
		return 0
	}
	rpb, err := sc.store.EstimateRowsPerBlock(ctx, chain, item.Dataset)
	if err != nil || rpb <= 0 {
		rpb = 1
	}
	goodput := observabilityGoodput()
	if goodput < 1 {
		return 0 // Goodput 未知时不输出误导性 ETA（§31 Tail ETA 仅在可估计时展示）
	}
	return float64(remainingBlocks) * rpb / goodput
}

func observabilityGoodput() float64 {
	// AnkrFetchGoodput 由 progressLoop 周期性写入；这里只读当前值。
	v := math.Float64frombits(currentGoodput.Load())
	if v <= 0 {
		return 0
	}
	return v
}
