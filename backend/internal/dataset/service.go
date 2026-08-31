package dataset

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

var addrRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

type HeadFn func(ctx context.Context, chain string) (int64, error)

type Service struct {
	Store  *store.Store
	HeadFn HeadFn
}

func New(st *store.Store, headFn HeadFn) *Service {
	return &Service{Store: st, HeadFn: headFn}
}

// NormalizeAndDedupeAddresses 校验 + 小写 + 去重（无效地址跳过，前端已做格式校验与计数展示）。
func NormalizeAndDedupeAddresses(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, a := range in {
		a = strings.TrimSpace(strings.ToLower(a))
		if a == "" {
			continue
		}
		if !addrRe.MatchString(a) {
			continue
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no valid addresses")
	}
	return out, nil
}

func (s *Service) ResolveRange(ctx context.Context, chain string, spec RangeSpec) (model.DownloadJobRange, error) {
	r := model.DownloadJobRange{RangeType: spec.Type}
	switch spec.Type {
	case "full", "":
		r.RangeType = "full"
		if s.HeadFn != nil {
			head, err := s.HeadFn(ctx, chain)
			if err != nil {
				return r, err
			}
			r.HeadBlock = head
			if head > 0 {
				zero := int64(0)
				r.FromBlock = &zero
				to := head
				r.ToBlock = &to
			}
		}
	case "block":
		if spec.FromBlock == nil || spec.ToBlock == nil {
			return r, errors.New("block range requires from_block and to_block")
		}
		if *spec.FromBlock > *spec.ToBlock {
			return r, errors.New("from_block > to_block")
		}
		r.FromBlock = spec.FromBlock
		r.ToBlock = spec.ToBlock
	case "time":
		if spec.From == nil || spec.To == nil {
			return r, errors.New("time range requires from and to")
		}
		if spec.From.After(*spec.To) {
			return r, errors.New("from > to")
		}
		r.FromTimestamp = spec.From
		r.ToTimestamp = spec.To
		zero := int64(0)
		r.FromBlock = &zero
		r.ToBlock = &zero
	default:
		return r, fmt.Errorf("unsupported range type: %s", spec.Type)
	}
	return r, nil
}

// wantInterval 返回请求区间（时间区间以未知块表示，比率按已覆盖计算）。
func (s *Service) wantInterval(job *model.DownloadJob) store.BlockInterval {
	if job.FromBlock != nil && job.ToBlock != nil && *job.ToBlock > *job.FromBlock {
		return store.BlockInterval{From: *job.FromBlock, To: *job.ToBlock}
	}
	return store.BlockInterval{From: 0, To: 0}
}

func (s *Service) coverageFor(ctx context.Context, chain, subject, dataset string, want store.BlockInterval) (SubjectDatasetCoverage, error) {
	rows, err := s.Store.ListCoverage(ctx, chain, subject, dataset)
	if err != nil {
		return SubjectDatasetCoverage{}, err
	}
	item := SubjectDatasetCoverage{Subject: subject, Dataset: dataset, Gaps: []Gap{}}
	if dataset == "balances" {
		// Balance 是快照：只要有记录即视为命中
		if len(rows) > 0 {
			item.Ratio = 1
			item.CoveredBlocks = 1
			item.TotalBlocks = 1
		}
		return item, nil
	}
	if want.From == 0 && want.To == 0 {
		return item, nil // 全量/live：总量未知
	}
	var intervals []store.BlockInterval
	for _, r := range rows {
		intervals = append(intervals, store.BlockInterval{From: r.FromBlock, To: r.ToBlock})
	}
	covered := CoveredBlocks(intervals, want)
	total := want.To - want.From + 1
	item.CoveredBlocks = covered
	item.TotalBlocks = total
	if total > 0 {
		item.Ratio = float64(covered) / float64(total)
	}
	for _, g := range ComputeGaps(intervals, want) {
		item.Gaps = append(item.Gaps, Gap{FromBlock: g.From, ToBlock: g.To})
	}
	return item, nil
}

func (s *Service) Preview(ctx context.Context, req EnsureRequest) (*CoveragePreviewResponse, error) {
	subjects, err := NormalizeAndDedupeAddresses(req.Subjects)
	if err != nil {
		return nil, err
	}
	datasets := sanitizeDatasets(req.Datasets)
	chain := sanitizeChain(req.Chain)
	resp := &CoveragePreviewResponse{Chain: chain, Datasets: datasets, Items: []SubjectDatasetCoverage{}}
	tmpJob := &model.DownloadJob{}
	rng, err := s.ResolveRange(ctx, chain, req.Range)
	if err != nil {
		return nil, err
	}
	tmpJob.RangeType = rng.RangeType
	tmpJob.FromBlock = rng.FromBlock
	tmpJob.ToBlock = rng.ToBlock
	want := s.wantInterval(tmpJob)
	var sum float64
	var n int
	for _, subj := range subjects {
		for _, d := range datasets {
			item, err := s.coverageFor(ctx, chain, subj, d, want)
			if err != nil {
				return nil, err
			}
			resp.Items = append(resp.Items, item)
			sum += item.Ratio
			n++
		}
	}
	if n > 0 {
		resp.OverallRatio = sum / float64(n)
	}
	return resp, nil
}

// Ensure 创建下载任务（Worker 端 Planner 负责 Coverage 缺口 → Shard 规划）。
func (s *Service) Ensure(ctx context.Context, req EnsureRequest) (*EnsureResponse, error) {
	subjects, err := NormalizeAndDedupeAddresses(req.Subjects)
	if err != nil {
		return nil, err
	}
	datasets := sanitizeDatasets(req.Datasets)
	chain := sanitizeChain(req.Chain)
	priority := req.Priority
	if priority <= 0 {
		priority = 100
	}
	if req.Source == "" {
		req.Source = "user_download"
	}
	rng, err := s.ResolveRange(ctx, chain, req.Range)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	n, err := s.Store.CountJobNoForDay(ctx, now)
	if err != nil {
		return nil, err
	}
	jobNo := fmt.Sprintf("DL-%s-%04d", now.Format("20060102"), n+1)

	preview, err := s.Preview(ctx, EnsureRequest{Subjects: subjects, Chain: chain, Datasets: datasets, Range: req.Range})
	if err != nil {
		return nil, err
	}

	job := &model.DownloadJob{
		JobNo:         jobNo,
		Status:        model.JobCreated,
		Priority:      priority,
		Source:        req.Source,
		Chain:         chain,
		RangeType:     rng.RangeType,
		FromBlock:     rng.FromBlock,
		ToBlock:       rng.ToBlock,
		FromTimestamp: rng.FromTimestamp,
		ToTimestamp:   rng.ToTimestamp,
		Datasets:      datasets,
		ReusePolicy:   req.ReusePolicy,
		HeadBlock:     rng.HeadBlock,
		Stats: map[string]any{
			"coverage_before": preview.OverallRatio,
		},
	}
	if job.ReusePolicy == "" {
		job.ReusePolicy = "prefer_local"
	}
	if _, err := s.Store.CreateJobWithSubjects(ctx, job, subjects); err != nil {
		return nil, err
	}
	s.Store.InsertAudit(ctx, nil, "dataset.ensure", "download_job", job.ID, map[string]any{
		"subjects": len(subjects), "datasets": datasets, "chain": chain, "range_type": rng.RangeType,
	})
	jobID := job.ID
	return &EnsureResponse{
		Status:    "planned",
		DatasetRef: "ds_" + job.ID[:8],
		JobID:     &jobID,
		Coverage:  preview.OverallRatio,
	}, nil
}

func sanitizeDatasets(in []string) []string {
	var out []string
	for _, d := range in {
		if model.ValidDataset(d) {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return model.DefaultDatasets
	}
	return out
}

func sanitizeChain(chain string) string {
	chain = strings.ToLower(strings.TrimSpace(chain))
	if chain == "" {
		return "bsc"
	}
	return chain
}
