package download

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

const (
	probeWindow        = 500_000
	minShardWindow     = 50_000
	maxShardWindow     = 2_000_000
	targetRowsPerShard = 200_000
	defaultMaxRetries  = 5
)

type gap struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type Planner struct {
	store *store.Store
	log   *slog.Logger
}

func NewPlanner(st *store.Store, log *slog.Logger) *Planner {
	return &Planner{store: st, log: log}
}

func (p *Planner) PlanJob(ctx context.Context, job *model.DownloadJob) error {
	if err := p.store.SetJobStatus(ctx, job.ID, model.JobPlanning); err != nil {
		return err
	}
	subjects, err := p.store.SubjectsByJob(ctx, job.ID)
	if err != nil {
		return err
	}
	want, bounded := requestedBlockInterval(job)
	for _, subj := range subjects {
		for _, ds := range job.Datasets {
			if err := p.planDataset(ctx, job, subj, ds, want, bounded); err != nil {
				p.log.Error("plan dataset failed", "job_id", job.ID, "subject", subj.Subject, "dataset", ds, "err", err)
				_ = p.store.SetJobStatusError(ctx, job.ID, model.JobFailed, err.Error())
				return err
			}
		}
		if err := p.store.RecomputeSubject(ctx, subj.ID); err != nil {
			return err
		}
	}
	return p.store.SetJobStatus(ctx, job.ID, model.JobQueued)
}

func (p *Planner) planDataset(ctx context.Context, job *model.DownloadJob, subj model.Subject, ds string, want store.BlockInterval, bounded bool) error {
	items, err := p.store.CreateDatasetsForSubject(ctx, job.ID, subj.ID, []string{ds})
	if err != nil {
		return err
	}
	item := items[0]

	// 已全部覆盖 → SKIPPED（复用）
	covered, err := p.store.ListCoverage(ctx, job.Chain, subj.Subject, ds)
	if err != nil {
		return err
	}
	if ds == "balances" {
		if len(covered) > 0 {
			observability.CoverageSavedRequests.Inc()
			return p.store.UpdateDatasetStatus(ctx, item.ID, model.ItemSkipped)
		}
		sh := p.baseShard(job, subj, item)
		return p.store.CreateShards(ctx, []model.Shard{sh})
	}

	var gaps []store.BlockInterval
	if !bounded {
		// 全量(live)/时间范围：单 Shard，区间由 Provider 语义决定
		sh := p.baseShard(job, subj, item)
		return p.store.CreateShards(ctx, []model.Shard{sh})
	}
	var intervals []store.BlockInterval
	for _, c := range covered {
		intervals = append(intervals, store.BlockInterval{From: c.FromBlock, To: c.ToBlock})
	}
	gaps = dataset.ComputeGaps(intervals, want)
	if len(gaps) == 0 {
		observability.CoverageSavedRequests.Inc()
		return p.store.UpdateDatasetStatus(ctx, item.ID, model.ItemSkipped)
	}

	// 自适应 Range：先发 Probe Shard，完成后按 rows/block 密度拆分剩余区间
	first := gaps[0]
	sh := p.baseShard(job, subj, item)
	sh.FromBlock = first.From
	sh.ToBlock = min64(first.From+probeWindow-1, first.To)
	var allGaps []gap
	for _, g := range gaps {
		allGaps = append(allGaps, gap{From: g.From, To: g.To})
	}
	stats, _ := json.Marshal(map[string]any{"gaps": allGaps})
	if _, err := p.store.Pool.Exec(ctx,
		"UPDATE download_datasets SET stats=$2::jsonb WHERE id=$1", item.ID, stats); err != nil {
		return err
	}
	return p.store.CreateShards(ctx, []model.Shard{sh})
}

func (p *Planner) baseShard(job *model.DownloadJob, subj model.Subject, item model.DatasetItem) model.Shard {
	return model.Shard{
		JobID: job.ID, SubjectID: subj.ID, DatasetID: item.ID, Dataset: item.Dataset,
		FromBlock: 0, ToBlock: job.HeadBlock,
		FromTimestamp: job.FromTimestamp, ToTimestamp: job.ToTimestamp,
		Status: model.ShardPending, Priority: job.Priority, MaxRetries: defaultMaxRetries,
	}
}

// ContinueShards Probe Shard 完成后，按实测密度拆分剩余缺口（自适应 Range 反馈）。
func (p *Planner) ContinueShards(ctx context.Context, sh model.Shard) error {
	item, err := p.store.GetDataset(ctx, sh.DatasetID)
	if err != nil {
		return err
	}
	var gaps []gap
	if item.Stats != nil {
		if raw, ok := item.Stats["gaps"]; ok {
			b, _ := json.Marshal(raw)
			_ = json.Unmarshal(b, &gaps)
		}
	}
	if len(gaps) == 0 {
		return nil
	}
	// 该 dataset 已完成到哪一块
	shards, err := p.store.ShardsByDataset(ctx, sh.DatasetID)
	if err != nil {
		return err
	}
	var completedTo int64 = -1
	for _, s := range shards {
		if s.Status == model.ShardCompleted && s.ToBlock > completedTo {
			completedTo = s.ToBlock
		}
	}
	// 密度反馈
	span := sh.ToBlock - sh.FromBlock + 1
	if span < 1 {
		span = 1
	}
	rowsPerBlock := float64(sh.DownloadedRows) / float64(span)
	window := int64(targetRowsPerShard / maxF(rowsPerBlock, 0.0001))
	if window < minShardWindow {
		window = minShardWindow
	}
	if window > maxShardWindow {
		window = maxShardWindow
	}

	var newShards []model.Shard
	for _, g := range gaps {
		if g.To <= completedTo {
			continue
		}
		from := max64(g.From, completedTo+1)
		if from > g.To {
			continue
		}
		to := min64(from+window-1, g.To)
		ns := model.Shard{
			JobID: sh.JobID, SubjectID: sh.SubjectID, DatasetID: sh.DatasetID, Dataset: sh.Dataset,
			FromBlock: from, ToBlock: to,
			FromTimestamp: sh.FromTimestamp, ToTimestamp: sh.ToTimestamp,
			Status: model.ShardPending, Priority: sh.Priority, MaxRetries: defaultMaxRetries,
		}
		newShards = append(newShards, ns)
	}
	if len(newShards) > 0 {
		return p.store.CreateShards(ctx, newShards)
	}
	return nil
}

// ResumeJob 恢复：重新计算 Coverage（可能其他任务已补全），再规划剩余缺口。
func (p *Planner) ResumeJob(ctx context.Context, job *model.DownloadJob) error {
	if err := p.store.SetJobStatus(ctx, job.ID, model.JobResuming); err != nil {
		return err
	}
	// 删除未完成 Shard（已完成数据保留，属于公共链上数据）
	if _, err := p.store.Pool.Exec(ctx,
		"DELETE FROM download_shards WHERE job_id=$1 AND status NOT IN ('COMPLETED','CANCELLED')", job.ID); err != nil {
		return err
	}
	subjects, err := p.store.SubjectsByJob(ctx, job.ID)
	if err != nil {
		return err
	}
	want, bounded := requestedBlockInterval(job)
	for _, subj := range subjects {
		items, err := p.store.DatasetsBySubject(ctx, subj.ID)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Status == model.ItemSkipped || item.Status == model.ItemCompleted {
				continue
			}
			if err := p.planDataset(ctx, job, subj, item.Dataset, want, bounded); err != nil {
				return err
			}
		}
	}
	return p.store.SetJobStatus(ctx, job.ID, model.JobRunning)
}

// requestedBlockInterval keeps a one-block request (including block 0) distinct
// from the 0..0 sentinel used by full/time downloads.
func requestedBlockInterval(job *model.DownloadJob) (store.BlockInterval, bool) {
	if job.RangeType == "block" && job.FromBlock != nil && job.ToBlock != nil && *job.ToBlock >= *job.FromBlock {
		return store.BlockInterval{From: *job.FromBlock, To: *job.ToBlock}, true
	}
	return store.BlockInterval{}, false
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
