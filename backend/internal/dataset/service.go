package dataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

var addrRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

type HeadFn func(ctx context.Context, chain string) (int64, error)
type ChainValidator func(chain string) error

// MetaUpserter 元数据落库（由 addressmeta 实现，避免循环依赖）。
type MetaUpserter interface {
	EnsureLinked(ctx context.Context, chain, addr, caseName string, tags []string, createMissing bool) error
}

type Service struct {
	Store                   *store.Store
	HeadFn                  HeadFn
	Meta                    MetaUpserter
	ValidateChain           ChainValidator
	TronProvider            string
	TronDefaultProvider     string
	TronDatasetProviders    map[string]string
	TronMaxTxAutoScan       uint64
	TronMaxInternalAutoScan uint64
}

func New(st *store.Store, headFn HeadFn) *Service {
	return &Service{Store: st, HeadFn: headFn}
}

func (s *Service) tronProviderFor(dataset string) string {
	if s.TronDatasetProviders != nil {
		if p := strings.ToLower(strings.TrimSpace(s.TronDatasetProviders[dataset])); p != "" {
			return p
		}
	}
	if p := strings.ToLower(strings.TrimSpace(s.TronDefaultProvider)); p != "" {
		return p
	}
	if p := strings.ToLower(strings.TrimSpace(s.TronProvider)); p != "" {
		return p
	}
	return "ankr"
}

func (s *Service) tronNeedsAnkr(datasets []string) bool {
	for _, dataset := range datasets {
		if s.tronProviderFor(dataset) == "ankr" {
			return true
		}
	}
	return false
}

func (s *Service) validateTronFullRange(chain string, datasets []string, spec RangeSpec) error {
	if strings.EqualFold(chain, "tron") && (spec.Type == "" || spec.Type == "full") && s.tronNeedsAnkr(datasets) {
		return errors.New("TRON datasets using Ankr require an explicit block or time range")
	}
	return nil
}

// NormalizeAndDedupeAddresses 校验 + 小写 + 去重（无效地址跳过，前端已做格式校验与计数展示）。
func NormalizeAndDedupeAddresses(in []string) ([]string, error) {
	return NormalizeAndDedupeAddressesForChain("", in)
}

func NormalizeAndDedupeAddressesForChain(chain string, in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if strings.EqualFold(chain, "tron") {
			var err error
			a, err = tron.NormalizeTronAddress(a)
			if err != nil {
				continue
			}
		} else {
			a = strings.ToLower(a)
			if !addrRe.MatchString(a) {
				continue
			}
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
		// Keep the legacy direct-call guard for services that only expose the
		// historical global provider field. API callers configure dataset-level
		// routing and are validated by validateTronFullRange before this method.
		if strings.EqualFold(chain, "tron") && strings.EqualFold(s.TronProvider, "ankr") && s.TronDefaultProvider == "" && len(s.TronDatasetProviders) == 0 {
			return r, errors.New("TRON with Ankr Node API requires an explicit block or time range")
		}
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
		if *spec.FromBlock < 0 || *spec.ToBlock < 0 {
			return r, errors.New("block range cannot be negative")
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

// ResolveRangeLocal 与 ResolveRange 相同，但对 full 范围不调用 HeadFn。
//
// v2 preview/create 的 Coverage HIT 判定必须完全不产生 Provider 请求；
// 链头探测只发生在本地缺口确实需要新建下载任务时（§28.7 验收）。
func (s *Service) ResolveRangeLocal(chain string, spec RangeSpec) (model.DownloadJobRange, error) {
	r := model.DownloadJobRange{RangeType: spec.Type}
	switch spec.Type {
	case "full", "":
		if strings.EqualFold(chain, "tron") && strings.EqualFold(s.TronProvider, "ankr") && s.TronDefaultProvider == "" && len(s.TronDatasetProviders) == 0 {
			return r, errors.New("TRON with Ankr Node API requires an explicit block or time range")
		}
		r.RangeType = "full"
	case "block":
		if spec.FromBlock == nil || spec.ToBlock == nil {
			return r, errors.New("block range requires from_block and to_block")
		}
		if *spec.FromBlock > *spec.ToBlock {
			return r, errors.New("from_block > to_block")
		}
		if *spec.FromBlock < 0 || *spec.ToBlock < 0 {
			return r, errors.New("block range cannot be negative")
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
func (s *Service) wantInterval(job *model.DownloadJob) (store.BlockInterval, bool) {
	if job.RangeType == "block" && job.FromBlock != nil && job.ToBlock != nil && *job.ToBlock >= *job.FromBlock {
		return store.BlockInterval{From: *job.FromBlock, To: *job.ToBlock}, true
	}
	return store.BlockInterval{From: 0, To: 0}, false
}

func (s *Service) validateTronAutoScan(chain string, datasets []string, rng model.DownloadJobRange) error {
	if !strings.EqualFold(chain, "tron") || !s.tronNeedsAnkr(datasets) {
		return nil
	}
	var blocks uint64
	switch rng.RangeType {
	case "block":
		if rng.FromBlock == nil || rng.ToBlock == nil || *rng.ToBlock < *rng.FromBlock {
			return nil
		}
		blocks = uint64(*rng.ToBlock-*rng.FromBlock) + 1
	case "time":
		if rng.FromTimestamp == nil || rng.ToTimestamp == nil || !rng.ToTimestamp.After(*rng.FromTimestamp) {
			return nil
		}
		// TRON targets roughly three-second blocks. This is an admission guard;
		// the provider repeats the exact resolved-block check before acquisition.
		seconds := uint64(rng.ToTimestamp.Sub(*rng.FromTimestamp).Seconds())
		blocks = (seconds + 2) / 3
	}
	for _, item := range datasets {
		switch item {
		case "transactions":
			if s.TronMaxTxAutoScan > 0 && blocks > s.TronMaxTxAutoScan {
				return fmt.Errorf("TRON transactions scan estimates %d blocks; production auto-scan limit is %d", blocks, s.TronMaxTxAutoScan)
			}
		case "internal_transactions":
			if s.TronMaxInternalAutoScan > 0 && blocks > s.TronMaxInternalAutoScan {
				return fmt.Errorf("TRON internal scan estimates %d blocks; production auto-scan limit is %d", blocks, s.TronMaxInternalAutoScan)
			}
		}
	}
	return nil
}

func (s *Service) coverageFor(ctx context.Context, chain, subject, dataset string, want store.BlockInterval, bounded bool) (SubjectDatasetCoverage, error) {
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
	if !bounded {
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
	return s.previewWithPolicy(ctx, req, false)
}

// PreviewLocal 只读本地索引，不探测链头，供 v2 下载预览与 Coverage HIT 判定使用。
func (s *Service) PreviewLocal(ctx context.Context, req EnsureRequest) (*CoveragePreviewResponse, error) {
	return s.previewWithPolicy(ctx, req, true)
}

func (s *Service) previewWithPolicy(ctx context.Context, req EnsureRequest, localOnly bool) (*CoveragePreviewResponse, error) {
	chain := sanitizeChain(req.Chain)
	subjects, err := NormalizeAndDedupeAddressesForChain(chain, req.Subjects)
	if err != nil {
		return nil, err
	}
	datasets, err := sanitizeDatasets(req.Datasets)
	if err != nil {
		return nil, err
	}
	if s.ValidateChain != nil {
		if err := s.ValidateChain(chain); err != nil {
			return nil, err
		}
	}
	if err := s.validateTronFullRange(chain, datasets, req.Range); err != nil {
		return nil, err
	}
	resp := &CoveragePreviewResponse{Chain: chain, Datasets: datasets, Items: []SubjectDatasetCoverage{}}
	tmpJob := &model.DownloadJob{}
	var rng model.DownloadJobRange
	var err2 error
	if localOnly {
		rng, err2 = s.ResolveRangeLocal(chain, req.Range)
	} else {
		rng, err2 = s.ResolveRange(ctx, chain, req.Range)
	}
	if err2 != nil {
		return nil, err2
	}
	if err := s.validateTronAutoScan(chain, datasets, rng); err != nil {
		return nil, err
	}
	tmpJob.RangeType = rng.RangeType
	tmpJob.FromBlock = rng.FromBlock
	tmpJob.ToBlock = rng.ToBlock
	want, bounded := s.wantInterval(tmpJob)
	var sum float64
	var n int
	for _, subj := range subjects {
		for _, d := range datasets {
			item, err := s.coverageFor(ctx, chain, subj, d, want, bounded)
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
	if req.Concurrency != nil {
		return nil, errors.New("numeric concurrency is not accepted; use performance_mode safe, fast, or turbo")
	}
	performanceMode, err := model.ParsePerformanceMode(req.PerformanceMode)
	if err != nil {
		return nil, err
	}
	chain := sanitizeChain(req.Chain)
	subjects, err := NormalizeAndDedupeAddressesForChain(chain, req.Subjects)
	if err != nil {
		return nil, err
	}
	datasets, err := sanitizeDatasets(req.Datasets)
	if err != nil {
		return nil, err
	}
	if s.ValidateChain != nil {
		if err := s.ValidateChain(chain); err != nil {
			return nil, err
		}
	}
	if err := s.validateTronFullRange(chain, datasets, req.Range); err != nil {
		return nil, err
	}
	if req.OrganizationID == "" || req.WorkspaceID == "" {
		orgID, wsID, scopeErr := s.Store.DefaultOrgWorkspace(ctx)
		if scopeErr != nil {
			return nil, fmt.Errorf("resolve tenant scope: %w", scopeErr)
		}
		if req.OrganizationID == "" {
			req.OrganizationID = orgID
		}
		if req.WorkspaceID == "" {
			req.WorkspaceID = wsID
		}
	}
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
	if err := s.validateTronAutoScan(chain, datasets, rng); err != nil {
		return nil, err
	}
	if req.ReusePolicy == "" {
		req.ReusePolicy = "prefer_local"
	}

	// Metadata is independent from download scheduling and must still be linked
	// when an identical download request reuses an existing job.
	if s.Meta != nil {
		for _, subj := range subjects {
			caseName := req.CaseName
			tags := req.Tags
			if m, ok := req.SubjectMeta[subj]; ok {
				caseName = m.CaseName
				tags = m.Tags
			}
			if caseName != "" || len(tags) > 0 {
				if err := s.Meta.EnsureLinked(ctx, chain, subj, caseName, tags, req.CreateMissingMeta); err != nil {
					return nil, err
				}
			}
		}
	}

	fingerprint := ""
	if !strings.EqualFold(req.ReusePolicy, "refresh") {
		fingerprint = scopedDownloadFingerprint(req.OrganizationID, req.WorkspaceID, downloadRequestFingerprint(chain, subjects, datasets, rng))
		if existing, findErr := s.Store.FindReusableJob(ctx, fingerprint, chain, rng.RangeType, rng.FromBlock, rng.ToBlock, rng.FromTimestamp, rng.ToTimestamp, datasets, subjects, req.OrganizationID, req.WorkspaceID); findErr != nil {
			return nil, findErr
		} else if existing != nil {
			return s.reusedEnsureResponse(ctx, existing, fingerprint), nil
		}
	}
	now := time.Now()
	// Job numbers are formatted in the process business timezone, so the
	// counting window must use that same local day. Using UTC here reused
	// DL-YYYYMMDD-0001 between local midnight and UTC midnight.
	day := now
	n, err := s.Store.CountJobNoForDay(ctx, day)
	if err != nil {
		return nil, err
	}
	resolvedAt := now.UTC()
	jobNo := fmt.Sprintf("DL-%s-%04d", day.Format("20060102"), n+1)

	preview, err := s.Preview(ctx, EnsureRequest{Subjects: subjects, Chain: chain, Datasets: datasets, Range: req.Range})
	if err != nil {
		return nil, err
	}
	// 显式 refresh 必须跳过 Coverage 命中短路，否则“强制重下”会被本地完成数据吞掉。
	if !strings.EqualFold(req.ReusePolicy, "refresh") && preview.OverallRatio >= 1 {
		fingerprint := sha256.Sum256([]byte(chain + "\x00" + strings.Join(subjects, ",") + "\x00" + strings.Join(datasets, ",") + fmt.Sprint(rng.FromBlock, rng.ToBlock)))
		return &EnsureResponse{Status: "ready", DatasetRef: "ds_" + hex.EncodeToString(fingerprint[:6]), Coverage: preview.OverallRatio, ProviderWorkRequired: false}, nil
	}

	var createdBy *string
	if req.CreatedBy != "" {
		createdBy = &req.CreatedBy
	} else if req.CreatedByIdentity != "" {
		// legacy 外键 users(id)：能从 identities.legacy_user_id 推导时保留兼容值，
		// 推导不到则留 NULL，真正的用户作用域走 created_by_identity。
		var legacy *string
		_ = s.Store.Pool.QueryRow(ctx, `SELECT legacy_user_id::text FROM identities WHERE id=$1`, req.CreatedByIdentity).Scan(&legacy)
		createdBy = legacy
	}
	var createdByIdentity *string
	if req.CreatedByIdentity != "" {
		v := req.CreatedByIdentity
		createdByIdentity = &v
	}
	job := &model.DownloadJob{
		JobNo:              jobNo,
		Status:             model.JobCreated,
		CreatedBy:          createdBy,
		CreatedByIdentity:  createdByIdentity,
		OrganizationID:     &req.OrganizationID,
		WorkspaceID:        &req.WorkspaceID,
		Priority:           priority,
		Source:             req.Source,
		Mode:               req.Mode,
		RequestedMode:      performanceMode,
		EffectiveMode:      performanceMode,
		ModeReason:         "user_selected",
		ModeChangedAt:      now,
		Chain:              chain,
		RangeType:          rng.RangeType,
		FromBlock:          rng.FromBlock,
		ToBlock:            rng.ToBlock,
		FromTimestamp:      rng.FromTimestamp,
		ToTimestamp:        rng.ToTimestamp,
		Datasets:           datasets,
		ReusePolicy:        req.ReusePolicy,
		RequestFingerprint: fingerprint,
		HeadBlock:          rng.HeadBlock,
		ResolvedAt:         &resolvedAt,
		Stats: map[string]any{
			"coverage_before": preview.OverallRatio,
			"request_source":  req.Source,
			"ai_managed":      strings.EqualFold(req.Source, "chatgpt_mcp"),
			"request_origin": func() string {
				if strings.EqualFold(req.Source, "chatgpt_mcp") {
					return "mcp"
				}
				return "system"
			}(),
		},
	}
	// job_no 冲突（并发建任务）自动递增重试
	if _, err := s.Store.CreateJobWithSubjects(ctx, job, subjects); err != nil {
		if fingerprint != "" && strings.Contains(err.Error(), "download_jobs_reusable_fingerprint_key") {
			if existing, findErr := s.Store.FindReusableJob(ctx, fingerprint, chain, rng.RangeType, rng.FromBlock, rng.ToBlock, rng.FromTimestamp, rng.ToTimestamp, datasets, subjects, req.OrganizationID, req.WorkspaceID); findErr == nil && existing != nil {
				return s.reusedEnsureResponse(ctx, existing, fingerprint), nil
			}
		}
		if strings.Contains(err.Error(), "job_no_key") {
			for attempt := 1; attempt <= 5; attempt++ {
				job.JobNo = fmt.Sprintf("DL-%s-%04d", day.Format("20060102"), n+1+attempt)
				if _, retryErr := s.Store.CreateJobWithSubjects(ctx, job, subjects); retryErr == nil {
					err = nil
					break
				} else if fingerprint != "" && strings.Contains(retryErr.Error(), "download_jobs_reusable_fingerprint_key") {
					if existing, findErr := s.Store.FindReusableJob(ctx, fingerprint, chain, rng.RangeType, rng.FromBlock, rng.ToBlock, rng.FromTimestamp, rng.ToTimestamp, datasets, subjects, req.OrganizationID, req.WorkspaceID); findErr == nil && existing != nil {
						return s.reusedEnsureResponse(ctx, existing, fingerprint), nil
					}
					return nil, retryErr
				} else if !strings.Contains(retryErr.Error(), "job_no_key") {
					return nil, retryErr
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	s.Store.InsertAudit(ctx, nil, "dataset.ensure", "download_job", job.ID, map[string]any{
		"subjects": len(subjects), "datasets": datasets, "chain": chain, "range_type": rng.RangeType,
	})
	jobID := job.ID
	return &EnsureResponse{
		Status:               "planned",
		DatasetRef:           "ds_" + job.ID[:8],
		JobID:                &jobID,
		Coverage:             preview.OverallRatio,
		Created:              true,
		ProviderWorkRequired: true,
	}, nil
}

func (s *Service) reusedEnsureResponse(ctx context.Context, job *model.DownloadJob, fingerprint string) *EnsureResponse {
	status := "planned"
	coverage := 0.0
	if job.Status == model.JobCompleted {
		status = "ready"
		coverage = 1
	}
	s.Store.InsertAudit(ctx, nil, "dataset.ensure.reused", "download_job", job.ID, map[string]any{"request_fingerprint": fingerprint, "status": job.Status})
	id := job.ID
	return &EnsureResponse{Status: status, DatasetRef: "ds_" + job.ID[:8], JobID: &id, Coverage: coverage, Reused: true, ProviderWorkRequired: false}
}

// ReuseCandidateLocal 查找同作用域可复用任务；不调用 Provider。
func (s *Service) ReuseCandidateLocal(ctx context.Context, req EnsureRequest) (*model.DownloadJob, error) {
	chain := sanitizeChain(req.Chain)
	subjects, err := NormalizeAndDedupeAddressesForChain(chain, req.Subjects)
	if err != nil {
		return nil, err
	}
	datasets, err := sanitizeDatasets(req.Datasets)
	if err != nil {
		return nil, err
	}
	if s.ValidateChain != nil {
		if err := s.ValidateChain(chain); err != nil {
			return nil, err
		}
	}
	if err := s.validateTronFullRange(chain, datasets, req.Range); err != nil {
		return nil, err
	}
	if req.OrganizationID == "" || req.WorkspaceID == "" {
		orgID, wsID, scopeErr := s.Store.DefaultOrgWorkspace(ctx)
		if scopeErr != nil {
			return nil, scopeErr
		}
		if req.OrganizationID == "" {
			req.OrganizationID = orgID
		}
		if req.WorkspaceID == "" {
			req.WorkspaceID = wsID
		}
	}
	rng, err := s.ResolveRangeLocal(chain, req.Range)
	if err != nil {
		return nil, err
	}
	fp := scopedDownloadFingerprint(req.OrganizationID, req.WorkspaceID, downloadRequestFingerprint(chain, subjects, datasets, rng))
	return s.Store.FindReusableJob(ctx, fp, chain, rng.RangeType, rng.FromBlock, rng.ToBlock, rng.FromTimestamp, rng.ToTimestamp, datasets, subjects, req.OrganizationID, req.WorkspaceID)
}

// scopedDownloadFingerprint 把租户作用域并入指纹。
// 唯一索引是全局的；若不加 scope，不同组织对同一地址/范围的请求会互相
// 竞争同一个指纹，既可能复用错误任务，也可能因唯一冲突而创建失败。
func scopedDownloadFingerprint(orgID, wsID, fingerprint string) string {
	sum := sha256.Sum256([]byte(orgID + "\x00" + wsID + "\x00" + fingerprint))
	return hex.EncodeToString(sum[:])
}

func downloadRequestFingerprint(chain string, subjects, datasets []string, rng model.DownloadJobRange) string {
	sortedSubjects := append([]string(nil), subjects...)
	sortedDatasets := append([]string(nil), datasets...)
	sort.Strings(sortedSubjects)
	sort.Strings(sortedDatasets)
	parts := []string{chain, rng.RangeType, strings.Join(sortedSubjects, ","), strings.Join(sortedDatasets, ",")}
	if rng.FromBlock != nil {
		parts = append(parts, fmt.Sprintf("fb:%d", *rng.FromBlock))
	}
	if rng.ToBlock != nil {
		parts = append(parts, fmt.Sprintf("tb:%d", *rng.ToBlock))
	}
	if rng.FromTimestamp != nil {
		parts = append(parts, "ft:"+rng.FromTimestamp.UTC().Format(time.RFC3339Nano))
	}
	if rng.ToTimestamp != nil {
		parts = append(parts, "tt:"+rng.ToTimestamp.UTC().Format(time.RFC3339Nano))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func sanitizeDatasets(in []string) ([]string, error) {
	if len(in) == 0 {
		return model.DefaultDatasets, nil
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if !model.ValidDataset(d) {
			return nil, fmt.Errorf("unknown dataset %q", d)
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func sanitizeChain(chain string) string {
	chain = strings.ToLower(strings.TrimSpace(chain))
	if chain == "" {
		return "bsc"
	}
	return chain
}
