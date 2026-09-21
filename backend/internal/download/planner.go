package download

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/countoracle"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/route"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"golang.org/x/sync/errgroup"
)

const (
	probeWindow        = 500_000
	minShardWindow     = 50_000
	maxShardWindow     = 2_000_000
	targetRowsPerShard = 200_000
	defaultMaxRetries  = 5
	tronMainnetGenesis = "2018-06-25T00:00:00Z"
)

type timeInterval struct {
	From time.Time
	To   time.Time
}

// multiRangeDatasets V1.1：Provider 实测尊重 block 边界的数据集（token_transfers 实测验证；
// logs 本就支持）。只有这些数据集允许单地址多 Range 并行（§4/§5：Range 间并行、Range 内串行）。
var multiRangeDatasets = map[string]bool{"token_transfers": true, "logs": true}

type gap struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type Planner struct {
	store *store.Store
	ch    *clickhouse.Conn
	log   *slog.Logger

	// V1.2 Count Oracle 平面（nil = 完全关闭，Planner 行为与 V1.1 一致）。
	oracle            countoracle.Oracle
	zeroVerify        bool
	transactionsRange bool
	txCertRequired    bool // V1.4 §14：transactions 多 Range 必须 Range Certification
	lpt               bool
	planningDeadline  time.Duration
	prov              provider.Provider
	routes            *route.Registry // 路由固化来源（nil = 不写路由列，保持旧行为）
	zeroSkipped       sync.Map        // itemID → bool（Ankr verify 确认空，跳过该 dataset）
	slotMu            sync.Mutex
	slotCacheAt       time.Time
	slotCache         *model.SchedulerState

	// V1.2 Planning 软预算（§16）：只限制"等 Count"的时间，绝不卡死规划流程
	planBudgetSet bool
	planBudgetAt  time.Time
}

// SetRoutes 注入运行时路由注册表：Shard 创建时把路由固化进列（开发文档 §9.1）。
//
// 后台改路由只影响新 Shard；恢复旧 Shard 时读取列里的固化值，缺失才回落到链级默认。
func (p *Planner) SetRoutes(reg *route.Registry) { p.routes = reg }

// createShards 是 Planner 内部唯一的落库入口：先固化路由，再写库。
func (p *Planner) createShards(ctx context.Context, chain string, shards []model.Shard) error {
	p.applyRoutes(chain, shards)
	return p.store.CreateShards(ctx, shards)
}

// applyRoutes 为还没有路由的 Shard 填上当前解析结果。
// 解析不到（例如 Solana 未启用 dRPC）时保持空串，运行期按链级默认回退。
func (p *Planner) applyRoutes(chain string, shards []model.Shard) {
	if p.routes == nil {
		return
	}
	for i := range shards {
		if shards[i].ProviderKey != "" {
			continue
		}
		r, ok := p.routes.Resolve(chain, shards[i].Dataset)
		if !ok {
			continue
		}
		shards[i].ProviderKey = r.ProviderKey
		shards[i].APIChannel = r.APIChannel
		shards[i].OperationClass = r.OperationClass
		shards[i].RoutingPolicyKey = r.PolicyKey
		shards[i].RoutingPolicyRevision = r.PolicyRevision
	}
}

// beginPlanBudget 开启本 job 的 Planning 预算（Count 不能拖慢下载，§16）。
func (p *Planner) beginPlanBudget(deadline time.Duration) {
	if deadline <= 0 {
		return
	}
	p.planBudgetAt = time.Now().Add(deadline)
	p.planBudgetSet = true
}

// budgetOpen Count 网络调用是否仍在预算窗口内。
func (p *Planner) budgetOpen() bool {
	if !p.planBudgetSet {
		return true
	}
	return time.Now().Before(p.planBudgetAt)
}

func NewPlanner(st *store.Store, ch *clickhouse.Conn, log *slog.Logger, _ bool) *Planner {
	return &Planner{store: st, ch: ch, log: log}
}

// ConfigureV12 Worker 启动时注入 Count Oracle 平面（§89 Shadow / §95 aware 由 oracle.Aware() 决定）。
func (p *Planner) ConfigureV12(oracle countoracle.Oracle, zeroVerify, txRangeSharding, txCertRequired, lpt bool, deadline time.Duration, prov provider.Provider) {
	p.oracle = oracle
	p.zeroVerify = zeroVerify
	p.transactionsRange = txRangeSharding
	p.txCertRequired = txCertRequired
	p.lpt = lpt
	p.planningDeadline = deadline
	p.prov = prov
}

// takeZeroSkip 检查并消费"已 zero-verify 确认空"标记。
func (p *Planner) takeZeroSkip(itemID string) bool {
	_, ok := p.zeroSkipped.LoadAndDelete(itemID)
	return ok
}

func (p *Planner) markZeroSkip(itemID string) {
	p.zeroSkipped.Store(itemID, true)
}

// schedulerStateSnapshot 读取 Scheduler 心跳（1s 缓存，供 Decision Log §88）。
func (p *Planner) schedulerStateSnapshot(ctx context.Context) *model.SchedulerState {
	p.slotMu.Lock()
	defer p.slotMu.Unlock()
	if p.slotCache != nil && time.Since(p.slotCacheAt) < time.Second {
		return p.slotCache
	}
	st, err := p.store.GetSchedulerState(ctx)
	if err != nil {
		return nil
	}
	if st != nil {
		p.slotCache = st
		p.slotCacheAt = time.Now()
	}
	return st
}

func (p *Planner) PlanJob(ctx context.Context, job *model.DownloadJob) error {
	if err := p.store.SetJobStatus(ctx, job.ID, model.JobPlanning); err != nil {
		return err
	}
	subjects, err := p.store.SubjectsByJob(ctx, job.ID)
	if err != nil {
		return err
	}
	// V1.2（§16）：Planning Deadline 是"软预算"——只限制等待 Count 的总时长，
	// 不包在 ctx 上，避免预算耗尽后规划流程/DB 回写被 ctx 取消而卡死。
	if p.oracle != nil && p.oracle.Enabled() {
		p.beginPlanBudget(p.planningDeadline)
	}
	// V1.2（§46-§48）：Multi-address LPT —— 重的地址先规划（先入 Ready Queue）
	if p.lpt && len(subjects) > 1 && p.oracle != nil && p.oracle.Enabled() && p.oracle.Configured() && p.oracle.Aware() {
		scored := p.subjectWorkScores(ctx, job, subjects)
		subjects = subjects[:0]
		for _, ws := range scored {
			subjects = append(subjects, ws.subj)
		}
		p.log.Info("multi-address LPT order applied", "job_id", job.ID, "subjects", len(subjects))
	}
	want, bounded := requestedBlockInterval(job)
	if tronParallelPlanningEligible(job, subjects) {
		if err := p.planTronSubjectsParallel(ctx, job, subjects, want, bounded); err != nil {
			p.log.Error("parallel TRON planning failed", "job_id", job.ID, "err", err)
			_ = p.store.SetJobStatusError(ctx, job.ID, model.JobFailed, err.Error())
			return err
		}
		return p.store.SetJobStatus(ctx, job.ID, model.JobQueued)
	}
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

func tronParallelPlanningEligible(job *model.DownloadJob, subjects []model.Subject) bool {
	if job == nil || job.Chain != "tron" || len(subjects) < 2 {
		return false
	}
	for _, ds := range job.Datasets {
		if ds != "transactions" && ds != "token_transfers" {
			return false
		}
	}
	return true
}

// planTronSubjectsParallel overlaps the independent address-boundary probes.
// The limit is intentionally below the production TRC20 pool so planning can
// never consume every Provider slot before acquisition begins.
func (p *Planner) planTronSubjectsParallel(ctx context.Context, job *model.DownloadJob, subjects []model.Subject, want store.BlockInterval, bounded bool) error {
	g, groupCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, minInt(8, len(subjects)))
	for _, subject := range subjects {
		subject := subject
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-groupCtx.Done():
				return groupCtx.Err()
			}
			defer func() { <-sem }()
			for _, ds := range job.Datasets {
				if err := p.planDataset(groupCtx, job, subject, ds, want, bounded); err != nil {
					return fmt.Errorf("subject %s dataset %s: %w", subject.Subject, ds, err)
				}
			}
			return p.store.RecomputeSubject(groupCtx, subject.ID)
		})
	}
	return g.Wait()
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
	// V1.1 Benchmark 任务强制全量重跑（固定 target_block 对比，不命中 Coverage 复用）；
	// S5：显式 refresh 同样必须跳过本地 Coverage 复用，否则“强制重下”会被静默吞掉。
	if job.Source == "benchmark" || strings.EqualFold(job.ReusePolicy, "refresh") {
		covered = nil
	}
	if ds == "balances" {
		if len(covered) > 0 {
			observability.CoverageSavedRequests.Inc()
			return p.store.UpdateDatasetStatus(ctx, item.ID, model.ItemSkipped)
		}
		sh := p.baseShard(job, subj, item)
		return p.createShards(ctx, job.Chain, []model.Shard{sh})
	}

	var gaps []store.BlockInterval
	if !bounded {
		// TronGrid fingerprints serialize one address stream. Once TRC20
		// enrichment is Logs-first, that serial pagination becomes the dominant
		// wall-clock cost. Independent, non-overlapping time ranges preserve
		// exact coverage while allowing the existing bounded scheduler and
		// TronGrid concurrency controller to overlap those streams.
		if job.Chain == "tron" && (ds == "token_transfers" || ds == "transactions") {
			return p.createTronTimeShards(ctx, job, subj, item)
		}
		// 全量（live）：多 Range 数据集按 [0, head] 均分 N 段（V1.1 §4；V1.2 Count-aware 初始分片）
		if p.multiRangeEligibleV12(job.Chain, ds) && job.HeadBlock > minShardWindow {
			n := p.rangeShardsFor(ctx, job, subj, item, ds)
			if p.takeZeroSkip(item.ID) {
				return nil // Ankr verify 确认空（§26）：zero-row Coverage，无需下载
			}
			return p.createRangeShards(ctx, job, subj, item, []store.BlockInterval{{From: 0, To: job.HeadBlock}}, n)
		}
		// 其余数据集保持单 Shard（V1.1）；Count 仍可记录（ETA/Decision Log，不改变分片）
		if p.v12Active(ctx, job, ds) {
			p.estimateFor(ctx, job, subj, item, ds)
		}
		sh := p.baseShard(job, subj, item)
		return p.createShards(ctx, job.Chain, []model.Shard{sh})
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

	// V1.1：多 Range 数据集直接按块区间均分 N 段（0 Missing / 0 Overlap，§6）；
	// V1.2：Profile MISS + Count-aware 时 n 来自 Count Estimate（§28/§31/§95）
	if p.multiRangeEligibleForJob(job, ds) {
		n := p.rangeShardsFor(ctx, job, subj, item, ds)
		if p.takeZeroSkip(item.ID) {
			return nil // Ankr verify 确认空（§26）：zero-row Coverage，无需下载
		}
		return p.createRangeShards(ctx, job, subj, item, gaps, n)
	}

	// 其余数据集：自适应 Range——先发 Probe Shard，完成后按 rows/block 密度拆分剩余区间
	// V1.2：Count 信息仍记录（决策日志/ETA），不改变 V1.1 探测路径
	if p.v12Active(ctx, job, ds) {
		p.estimateFor(ctx, job, subj, item, ds)
	}
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
		"UPDATE download_datasets SET stats = COALESCE(stats,'{}'::jsonb) || $2::jsonb WHERE id=$1", item.ID, stats); err != nil {
		return err
	}
	return p.createShards(ctx, job.Chain, []model.Shard{sh})
}

func tronTimeShardCount(job *model.DownloadJob) int {
	mode := string(job.EffectiveMode)
	if mode == "" {
		mode = string(job.RequestedMode)
	}
	if mode == "" {
		mode = job.Mode
	}
	switch mode {
	case "economy", "safe":
		return 4
	case "fast":
		return 16
	case "turbo":
		return 32
	default:
		return 8
	}
}

func tronTimeIntervals(job *model.DownloadJob, now time.Time) []timeInterval {
	start, _ := time.Parse(time.RFC3339, tronMainnetGenesis)
	end := now.UTC().Add(time.Minute)
	if job.FromTimestamp != nil {
		start = job.FromTimestamp.UTC()
	}
	if job.ToTimestamp != nil {
		end = job.ToTimestamp.UTC()
	}
	if end.Before(start) {
		return nil
	}
	n := tronTimeShardCount(job)
	spanMillis := end.UnixMilli() - start.UnixMilli() + 1
	// TronGrid fingerprints serialize every page within an address stream. A
	// seven-day floor left short, high-density ranges with only one or two
	// streams, so a single large transaction response (often several MiB) could
	// dominate the whole job. Six-hour shards keep Turbo's bounded 32-way fanout
	// available without changing the requested time coverage or paid Ankr work.
	const minShardMillis = int64((6 * time.Hour) / time.Millisecond)
	if maxBySpan := int((spanMillis + minShardMillis - 1) / minShardMillis); n > maxBySpan {
		n = max(1, maxBySpan)
	}
	intervals := make([]timeInterval, 0, n)
	for i := 0; i < n; i++ {
		fromMillis := start.UnixMilli() + spanMillis*int64(i)/int64(n)
		toMillis := start.UnixMilli() + spanMillis*int64(i+1)/int64(n) - 1
		intervals = append(intervals, timeInterval{From: time.UnixMilli(fromMillis).UTC(), To: time.UnixMilli(toMillis).UTC()})
	}
	return intervals
}

func tronColdTimeIntervals(job *model.DownloadJob, now time.Time) []timeInterval {
	base := tronTimeIntervals(job, now)
	if len(base) == 0 {
		return nil
	}
	start, end := base[0].From, base[len(base)-1].To
	const maxColdShards = 128
	// New addresses have no local quantiles. Use monthly shards for the last two
	// years and quarterly shards for older history. Annual cold shards looked
	// cheap for sparse accounts, but a previously unseen address with 3k-4k
	// transfers in an older year collapsed to one serial 16-20 page tail after
	// every other shard had finished. Quarterly history keeps enough independent
	// work available to the bounded worker pool without paying ~100 monthly
	// probes across the entire chain lifetime.
	recentCutoff := end.Add(-730 * 24 * time.Hour)
	if recentCutoff.Before(start) {
		recentCutoff = start
	}
	out := make([]timeInterval, 0, 40)
	appendSpan := func(from, to time.Time, span time.Duration) {
		for from.Before(to) && len(out) < maxColdShards {
			next := from.Add(span)
			if next.After(to) {
				next = to
			}
			out = append(out, timeInterval{From: from, To: next.Add(-time.Millisecond)})
			from = next
		}
	}
	appendSpan(start, recentCutoff, 90*24*time.Hour)
	appendSpan(recentCutoff, end.Add(time.Millisecond), 30*24*time.Hour)
	if len(out) > 0 {
		out[len(out)-1].To = end
	}
	return out
}

func tronColdActivityIntervals(job *model.DownloadJob, now, firstActivity, latestActivity time.Time) []timeInterval {
	return tronActivityIntervals(job, now, firstActivity, latestActivity, tronActivityShardCap(job))
}

func tronActivityShardCap(job *model.DownloadJob) int {
	mode := string(job.EffectiveMode)
	if mode == "" {
		mode = string(job.RequestedMode)
	}
	switch mode {
	case "safe", "economy":
		return 47
	case "fast":
		return 95
	case "turbo":
		return 127
	default:
		return 47
	}
}

func tronProfileShardCount(job *model.DownloadJob, profile provider.TRC20ActivityProfile) int {
	capShards := tronActivityShardCap(job)
	if profile.ExactRows > 0 {
		// Small exact histories should not pay more empty range probes than
		// useful pages. Aim for about eight pages per active shard.
		pages := (profile.ExactRows + 199) / 200
		return maxInt(1, minInt(capShards, (pages+7)/8))
	}
	base := minInt(capShards, tronTimeShardCount(job))
	if profile.PeakRowsPerDay <= 0 || profile.First.IsZero() || profile.Latest.Before(profile.First) {
		return maxInt(1, base)
	}
	activeDays := math.Max(1, profile.Latest.Sub(profile.First).Hours()/24)
	// Edge-page density is deliberately damped because bursty accounts are
	// not uniformly dense. 20k projected rows per shard keeps hot histories
	// parallel without turning ordinary sparse addresses into hundreds of
	// one-page probes.
	projectedRows := profile.PeakRowsPerDay * activeDays * 0.25
	densityShards := int(math.Ceil(projectedRows / 20_000))
	return minInt(capShards, maxInt(base, densityShards))
}

func tronProfiledActivityIntervals(job *model.DownloadJob, now time.Time, profile provider.TRC20ActivityProfile) []timeInterval {
	return tronActivityIntervals(job, now, profile.First, profile.Latest, tronProfileShardCount(job, profile))
}

func tronActivityIntervals(job *model.DownloadJob, now, firstActivity, latestActivity time.Time, n int) []timeInterval {
	base := tronTimeIntervals(job, now)
	if len(base) == 0 {
		return nil
	}
	start, end := base[0].From, base[len(base)-1].To
	if firstActivity.After(start) && !firstActivity.After(end) {
		start = firstActivity.UTC()
	}
	activeEnd := end
	if latestActivity.After(start) && latestActivity.Before(end) {
		activeEnd = latestActivity.UTC()
	}
	n = maxInt(1, n)
	spanMillis := activeEnd.UnixMilli() - start.UnixMilli() + 1
	const minShardMillis = int64((6 * time.Hour) / time.Millisecond)
	if maxBySpan := int((spanMillis + minShardMillis - 1) / minShardMillis); n > maxBySpan {
		n = max(1, maxBySpan)
	}
	intervals := make([]timeInterval, 0, n)
	for i := 0; i < n; i++ {
		fromMillis := start.UnixMilli() + spanMillis*int64(i)/int64(n)
		toMillis := start.UnixMilli() + spanMillis*int64(i+1)/int64(n) - 1
		intervals = append(intervals, timeInterval{From: time.UnixMilli(fromMillis).UTC(), To: time.UnixMilli(toMillis).UTC()})
	}
	// Keep full-history semantics if the account has gone quiet: one cheap
	// catch-up shard covers everything after the newest indexed event through
	// the job's resolved end, instead of spreading dozens of empty probes there.
	if activeEnd.Before(end) {
		intervals = append(intervals, timeInterval{From: activeEnd.Add(time.Millisecond), To: end})
	}
	return intervals
}

func (p *Planner) createTronTimeShards(ctx context.Context, job *model.DownloadJob, subj model.Subject, item model.DatasetItem) error {
	planningJob := job
	if job.RangeType == "full" && !strings.EqualFold(job.ReusePolicy, "refresh") && job.Source != "benchmark" {
		state, err := p.store.GetAddressDatasetSyncState(ctx, job.Chain, subj.Subject, item.Dataset)
		if err != nil {
			return err
		}
		if state != nil {
			incremental, alreadyCurrent := tronIncrementalPlanningJob(job, state, time.Now())
			if alreadyCurrent {
				observability.CoverageSavedRequests.Inc()
				return p.store.UpdateDatasetStatus(ctx, item.ID, model.ItemSkipped)
			}
			planningJob = incremental
			p.log.Info("TRON address dataset tail-only planning", "job_id", job.ID, "dataset", item.Dataset, "address", subj.Subject, "complete_through", state.CompleteThrough, "tail_from", *incremental.FromTimestamp, "tail_to", *incremental.ToTimestamp)
		}
	}
	intervals := p.tronDataAwareTimeIntervals(ctx, planningJob, subj.Subject, item.Dataset, time.Now())
	if len(intervals) == 0 {
		return p.createShards(ctx, job.Chain, []model.Shard{p.baseShard(job, subj, item)})
	}
	shards := make([]model.Shard, 0, len(intervals))
	for i, interval := range intervals {
		from, to := interval.From, interval.To
		shards = append(shards, model.Shard{
			JobID: job.ID, SubjectID: subj.ID, DatasetID: item.ID, Dataset: item.Dataset,
			FromBlock: 0, ToBlock: 0, FromTimestamp: &from, ToTimestamp: &to,
			Status: model.ShardPending, Priority: job.Priority, MaxRetries: defaultMaxRetries,
			PageState: map[string]any{"query_mode": "time_subshard", "time_shard_index": i, "time_shard_count": len(intervals)},
		})
	}
	p.log.Info("TRON fingerprint stream split into bounded time shards", "job_id", job.ID, "dataset", item.Dataset, "address", subj.Subject, "shards", len(shards))
	return p.createShards(ctx, job.Chain, shards)
}

func tronIncrementalPlanningJob(job *model.DownloadJob, state *store.AddressDatasetSyncState, now time.Time) (*model.DownloadJob, bool) {
	if state == nil {
		return job, false
	}
	from := state.CompleteThrough.UTC().Add(time.Millisecond)
	to := now.UTC().Add(time.Minute)
	if from.After(to) {
		return nil, true
	}
	clone := *job
	clone.FromTimestamp = &from
	clone.ToTimestamp = &to
	return &clone, false
}

func (p *Planner) tronDataAwareTimeIntervals(ctx context.Context, job *model.DownloadJob, address, dataset string, now time.Time) []timeInterval {
	fallback := tronTimeIntervals(job, now)
	if len(fallback) < 2 || (dataset != "token_transfers" && dataset != "transactions") {
		return fallback
	}
	// ClickHouse can contain partial rows from interrupted or historical jobs
	// even when no complete sync watermark exists. Quantiles from that partial
	// population create severely skewed shards: an unobserved multi-year period
	// collapses into one serial tail. Only a durable full-sync watermark makes
	// local row quantiles trustworthy for full-history planning.
	if job.RangeType == "full" && !strings.EqualFold(job.ReusePolicy, "refresh") && job.Source != "benchmark" {
		state, err := p.store.GetAddressDatasetSyncState(ctx, job.Chain, address, dataset)
		if err != nil || !localTRONQuantilesTrusted(job, state) {
			if dataset == "token_transfers" {
				return p.remoteTRC20ActivityIntervals(ctx, job, address, dataset, now)
			}
			return tronColdTimeIntervals(job, now)
		}
	}
	if p.ch == nil {
		if dataset == "token_transfers" {
			return p.remoteTRC20ActivityIntervals(ctx, job, address, dataset, now)
		}
		return fallback
	}
	quantiles := make([]string, 0, len(fallback)-1)
	for i := 1; i < len(fallback); i++ {
		quantiles = append(quantiles, fmt.Sprintf("%.8f", float64(i)/float64(len(fallback))))
	}
	query := "SELECT count(), quantilesExact(" + strings.Join(quantiles, ",") + ")(toUnixTimestamp64Milli(timestamp)) FROM blockchain." + dataset + " FINAL WHERE chain = ? AND (from_address = ? OR to_address = ?)"
	var count uint64
	var boundaries []int64
	if err := p.ch.DB().QueryRow(ctx, query, job.Chain, address, address).Scan(&count, &boundaries); err != nil {
		if dataset == "token_transfers" {
			return p.remoteTRC20ActivityIntervals(ctx, job, address, dataset, now)
		}
		return fallback
	}
	if count == 0 || len(boundaries) == 0 {
		if dataset == "token_transfers" {
			return p.remoteTRC20ActivityIntervals(ctx, job, address, dataset, now)
		}
		return tronColdTimeIntervals(job, now)
	}
	startMillis := fallback[0].From.UnixMilli()
	endMillis := fallback[len(fallback)-1].To.UnixMilli()
	intervals := make([]timeInterval, 0, len(boundaries)+1)
	current := startMillis
	for _, boundary := range boundaries {
		if boundary < current || boundary >= endMillis {
			continue
		}
		intervals = append(intervals, timeInterval{From: time.UnixMilli(current).UTC(), To: time.UnixMilli(boundary).UTC()})
		current = boundary + 1
	}
	if current <= endMillis {
		intervals = append(intervals, timeInterval{From: time.UnixMilli(current).UTC(), To: time.UnixMilli(endMillis).UTC()})
	}
	if len(intervals) < 2 {
		return fallback
	}
	p.log.Info("TRON time shards balanced by local row quantiles", "job_id", job.ID, "dataset", dataset, "address", address, "local_rows", count, "shards", len(intervals))
	return intervals
}

func localTRONQuantilesTrusted(job *model.DownloadJob, state *store.AddressDatasetSyncState) bool {
	if job == nil {
		return false
	}
	if job.RangeType == "full" && !strings.EqualFold(job.ReusePolicy, "refresh") && job.Source != "benchmark" {
		return state != nil
	}
	return true
}

func (p *Planner) remoteTRC20ActivityIntervals(ctx context.Context, job *model.DownloadJob, address, dataset string, now time.Time) []timeInterval {
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if resolver, ok := p.prov.(interface {
		TRC20ActivityProfile(context.Context, string) (provider.TRC20ActivityProfile, error)
	}); ok {
		profile, err := resolver.TRC20ActivityProfile(probeCtx, address)
		if err == nil && profile.Empty {
			fallback := tronTimeIntervals(job, now)
			if len(fallback) > 0 {
				p.log.Info("TRON cold address has no indexed activity", "job_id", job.ID, "dataset", dataset, "address", address)
				return []timeInterval{{From: fallback[0].From, To: fallback[len(fallback)-1].To}}
			}
		}
		if err == nil && !profile.First.IsZero() {
			intervals := tronProfiledActivityIntervals(job, now, profile)
			p.log.Info("TRON cold history profiled by indexed activity", "job_id", job.ID, "dataset", dataset, "address", address,
				"first_activity", profile.First, "latest_activity", profile.Latest, "exact_rows", profile.ExactRows,
				"peak_rows_per_day", profile.PeakRowsPerDay, "shards", len(intervals))
			return intervals
		}
		p.log.Warn("TRON activity profile failed; using genesis-safe cold plan", "job_id", job.ID, "address", address, "error", err)
		return tronColdTimeIntervals(job, now)
	}
	if resolver, ok := p.prov.(interface {
		TRC20ActivityBounds(context.Context, string) (time.Time, time.Time, error)
	}); ok {
		first, latest, err := resolver.TRC20ActivityBounds(probeCtx, address)
		if err == nil && !first.IsZero() {
			return tronColdActivityIntervals(job, now, first, latest)
		}
	}
	return tronColdTimeIntervals(job, now)
}

func (p *Planner) baseShard(job *model.DownloadJob, subj model.Subject, item model.DatasetItem) model.Shard {
	return model.Shard{
		JobID: job.ID, SubjectID: subj.ID, DatasetID: item.ID, Dataset: item.Dataset,
		FromBlock: 0, ToBlock: job.HeadBlock,
		FromTimestamp: job.FromTimestamp, ToTimestamp: job.ToTimestamp,
		Status: model.ShardPending, Priority: job.Priority, MaxRetries: defaultMaxRetries,
		PageState: map[string]any{"query_mode": job.RangeType},
	}
}

// createRangeShards V1.1：把若干缺口区间按块数比例均分为 n 个 Range Shard。
// 边界规范 [start, end)（§6）；数据库 ToBlock 为 inclusive → ToBlock = end-1，保证 0 Missing / 0 Overlap。
func (p *Planner) createRangeShards(ctx context.Context, job *model.DownloadJob, subj model.Subject, item model.DatasetItem, gaps []store.BlockInterval, n int) error {
	if n < 1 {
		n = 1
	}
	// V1.1 密度感知（§12/§17）：已知数据跨度时，空前缀固定 1 个 Shard（秒级 0 行），
	// n 个 Range 全部用于数据段 [first, target] 的均分 —— 让每个 Range 承担相近工作量。
	var firstData int64 = -1
	if job.Stats != nil {
		if v, ok := job.Stats["data_first_block"].(float64); ok && v > 0 {
			firstData = int64(v)
		}
	}
	var prefixGaps, dataGaps []store.BlockInterval
	for _, g := range gaps {
		if firstData > 0 && firstData > g.From && firstData <= g.To {
			prefixGaps = append(prefixGaps, store.BlockInterval{From: g.From, To: firstData - 1})
			dataGaps = append(dataGaps, store.BlockInterval{From: firstData, To: g.To})
		} else if firstData > 0 && g.To < firstData {
			prefixGaps = append(prefixGaps, g)
		} else {
			dataGaps = append(dataGaps, g)
		}
	}
	if len(dataGaps) == 0 {
		dataGaps = prefixGaps // 无密度提示：按原逻辑处理
		prefixGaps = nil
	}
	var shards []model.Shard
	// 空前缀：每个 gap 恰好 1 个 Shard（Provider 一次空响应即完成）
	for _, g := range prefixGaps {
		shards = append(shards, model.Shard{
			JobID: job.ID, SubjectID: subj.ID, DatasetID: item.ID, Dataset: item.Dataset,
			FromBlock: g.From, ToBlock: g.To,
			Status: model.ShardPending, Priority: job.Priority, MaxRetries: defaultMaxRetries,
			PageState: map[string]any{"query_mode": job.RangeType},
		})
	}
	// 分位数边界（§17 工作量均衡）：把数据段切成等行量 Range，每段 1 个 Shard
	var boundaries []int64
	if job.Stats != nil {
		if raw, ok := job.Stats["data_boundaries"].([]any); ok {
			for _, v := range raw {
				if f, ok := v.(float64); ok && f > 0 {
					boundaries = append(boundaries, int64(f))
				}
			}
		}
	}
	if len(boundaries) > 0 && len(dataGaps) == 1 {
		g := dataGaps[0]
		var segments []store.BlockInterval
		cur := g.From
		for _, b := range boundaries {
			if b >= cur && b < g.To {
				segments = append(segments, store.BlockInterval{From: cur, To: b})
				cur = b + 1
			}
		}
		if cur <= g.To {
			segments = append(segments, store.BlockInterval{From: cur, To: g.To})
		}
		for _, seg := range segments {
			shards = append(shards, model.Shard{
				JobID: job.ID, SubjectID: subj.ID, DatasetID: item.ID, Dataset: item.Dataset,
				FromBlock: seg.From, ToBlock: seg.To,
				Status: model.ShardPending, Priority: job.Priority, MaxRetries: defaultMaxRetries,
				PageState: map[string]any{"query_mode": job.RangeType},
			})
		}
		p.log.Info("multi-range plan (quantile)", "dataset", item.Dataset, "ranges", len(shards), "job_mode", job.Mode)
		return p.createShards(ctx, job.Chain, shards)
	}
	// 数据段：共享 n 个 Range
	gaps = dataGaps
	var total int64
	for _, g := range gaps {
		if g.To >= g.From {
			total += g.To - g.From + 1
		}
	}
	if total <= 0 {
		if len(shards) == 0 {
			return nil
		}
		return p.createShards(ctx, job.Chain, shards)
	}
	for _, g := range gaps {
		span := g.To - g.From + 1
		if span <= 0 {
			continue
		}
		// 该缺口分配到的 shard 数（按块数比例四舍五入，至少 1，至多 n）
		k := int(float64(n)*float64(span)/float64(total) + 0.5)
		if k < 1 {
			k = 1
		}
		if k > n {
			k = n
		}
		per := span / int64(k)
		rem := span % int64(k)
		cur := g.From
		for i := 0; i < k && cur <= g.To; i++ {
			size := per
			if int64(i) < rem {
				size++
			}
			end := cur + size - 1
			if end > g.To {
				end = g.To
			}
			if cur > g.To {
				break
			}
			shards = append(shards, model.Shard{
				JobID: job.ID, SubjectID: subj.ID, DatasetID: item.ID, Dataset: item.Dataset,
				FromBlock: cur, ToBlock: end,
				Status: model.ShardPending, Priority: job.Priority, MaxRetries: defaultMaxRetries,
				PageState: map[string]any{"query_mode": job.RangeType},
			})
			cur = end + 1
		}
	}
	if len(shards) == 0 {
		return nil
	}
	p.log.Info("multi-range plan", "dataset", item.Dataset, "ranges", len(shards), "job_mode", job.Mode)
	return p.createShards(ctx, job.Chain, shards)
}

// shardCountFor V1.1 §24/§41-§44：Profile HIT → preferred_shard_count；MISS → 模式默认；
// economy 减半、turbo ×2（仍受 Provider Health / Backpressure / Budget 运行时约束）。
func (p *Planner) shardCountFor(ctx context.Context, job *model.DownloadJob, ds string) int {
	if job.Stats != nil {
		if v, ok := job.Stats["benchmark_shard_count"].(float64); ok && v >= 1 {
			return int(v)
		}
	}
	mode := job.Mode
	if mode == "" {
		mode = "balanced"
	}
	base := 2
	switch mode {
	case "economy":
		base = 1
	case "balanced":
		base = 2
	case "turbo":
		base = 4
	}
	if n, samples, err := p.store.GetSweetSpotProfile(ctx, job.Chain, ds, ds, "NORMAL"); err == nil && samples >= 3 && n > 0 {
		base = n
		switch mode {
		case "economy":
			base = maxInt(1, n/2)
		case "turbo":
			base = minInt(8, n*2)
		}
	}
	if base < 1 {
		base = 1
	}
	if base > 8 {
		base = 8
	}
	return base
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
			PageState: sh.PageState,
			// 自适应拆分的子 Shard 必须沿用父 Shard 的路由固化值：
			// 中途重新解析路由会让同一个 Shard 家族跨 Provider 换源。
			ProviderKey: sh.ProviderKey, APIChannel: sh.APIChannel, OperationClass: sh.OperationClass,
			RoutingPolicyKey: sh.RoutingPolicyKey, RoutingPolicyRevision: sh.RoutingPolicyRevision,
			ProfileRevision: sh.ProfileRevision,
		}
		newShards = append(newShards, ns)
	}
	if len(newShards) > 0 {
		return p.store.CreateShards(ctx, newShards)
	}
	return nil
}

// ResumeJob 恢复：优先原地恢复已有可续传 Shard，保留 committed cursor、页码与累计行数。
// 只有不存在任何可续传 Shard 时（例如历史 FAILED_FINAL 任务），才按 Coverage 重规划。
func (p *Planner) ResumeJob(ctx context.Context, job *model.DownloadJob) error {
	if err := p.store.SetJobStatus(ctx, job.ID, model.JobResuming); err != nil {
		return err
	}
	var resumable int64
	if err := p.store.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM download_shards WHERE job_id=$1 AND status IN ('PAUSED','PENDING','FAILED_RETRYABLE','LEASED','RUNNING')",
		job.ID).Scan(&resumable); err != nil {
		return err
	}
	if resumable > 0 {
		// PAUSED is the only state that is not dispatchable after the job returns
		// to RUNNING. PENDING/FAILED_RETRYABLE already participate in leasing;
		// LEASED/RUNNING are retained defensively so a duplicate resume request can
		// never destroy an in-flight checkpoint.
		if _, err := p.store.Pool.Exec(ctx,
			"UPDATE download_shards SET status='PENDING', lease_owner='', leased_until=NULL, updated_at=now() "+
				"WHERE job_id=$1 AND status='PAUSED'", job.ID); err != nil {
			return err
		}
		return p.store.SetJobStatus(ctx, job.ID, model.JobRunning)
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
			// PAUSED can be observed after every shard left LEASED/RUNNING but
			// before the last runner has refreshed its parent dataset. Recompute
			// from durable shards before deciding whether another shard is needed.
			if err := p.store.RecomputeDataset(ctx, item.ID); err != nil {
				return err
			}
			fresh, err := p.store.GetDataset(ctx, item.ID)
			if err != nil {
				return err
			}
			item = *fresh
			if item.Status == model.ItemSkipped || item.Status == model.ItemCompleted {
				continue
			}
			if err := p.planDataset(ctx, job, subj, item.Dataset, want, bounded); err != nil {
				return err
			}
		}
		if err := p.store.RecomputeSubject(ctx, subj.ID); err != nil {
			return err
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
