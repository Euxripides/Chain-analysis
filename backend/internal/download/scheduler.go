// Package download 下载调度核心模块（Worker 进程）。
// 任务模型：Address → Chain → Dataset → Shard（Range），Shard 内翻页（Ankr pageToken 串行）。
package download

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/addressmeta"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/countoracle"
	"github.com/blockchain-platform/blockchain-platform/internal/events"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/spool"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/tokenmeta"
)

// currentGoodput Fetch Goodput rows/s（progressLoop 写入，Tail ETA 消费）。
var currentGoodput atomic.Uint64 // math.Float64bits

type Scheduler struct {
	cfg      *config.Config
	store    *store.Store
	prov     provider.Provider
	raw      *rawstore.Store
	writer   *ingest.Writer
	planner  *Planner
	bus      *events.Bus
	log      *slog.Logger
	workerID string

	mu            sync.Mutex
	concurrency   int
	active        int64
	activeNonzero int64
	weighted      int64 // 加权占用（§48：small=1/medium=2/large=4）
	wg            sync.WaitGroup

	rates         sync.Map // jobID -> *rateTracker
	pagePolicy    *ankr.PagePolicy
	lastConnLimit int64
	meta          *addressmeta.Service

	methodResolver *method.FastResolver
	methodRegistry *method.Registry
	tokenMeta      *tokenmeta.Service

	// ---- Ankr 引擎组件 ----
	spool    *spool.Store
	pipeline *ingestPipeline
	bp       *backpressure
	mem      *byteBudget

	// V1.2：Ramp 阶段（并发循环更新，快照/状态行读取）+ 实际行数回写 once 表
	rampPhase       atomic.Value // string
	actualsRecorded sync.Map     // datasetID → bool
	replayCheckedAt sync.Map     // shardID → time.Time; bounds incomplete-page scans

	countOracle countoracle.Oracle

	// V1.4：Global Ready Queue / 阶段 inflight / Sweet Spot
	v14    *fetchV14State
	tokens *tokenRegistry
}

func (sc *Scheduler) SetTokenMetadata(service *tokenmeta.Service) { sc.tokenMeta = service }

// SetCountOracle V1.2：注入 Count Oracle 平面（nil → Planner 完全保持 V1.1 行为）。
func (sc *Scheduler) SetCountOracle(o countoracle.Oracle) {
	sc.countOracle = o
	sc.planner.ConfigureV12(o, sc.cfg.CountOracle.ZeroVerifyEnabled,
		sc.cfg.Global.TransactionsRangeShardingEnabled, sc.cfg.Ankr.TransactionsRangeCertRequired,
		sc.cfg.CountOracle.LPTEnabled, sc.cfg.CountOracle.PlanningDeadline, sc.prov)
	sc.log.Info("count oracle configured", "enabled", o != nil && o.Enabled(),
		"aware", o != nil && o.Aware(), "key_configured", o != nil && o.Configured())
}

// SetMethodResolver 注册 Method 解析栈（只读内存路由，绝不让交易入库失败）。
func (sc *Scheduler) SetMethodResolver(resolver *method.FastResolver, registry *method.Registry) {
	sc.methodResolver = resolver
	sc.methodRegistry = registry
}

func NewScheduler(cfg *config.Config, st *store.Store, ch *clickhouse.Conn, prov provider.Provider, raw *rawstore.Store,
	writer *ingest.Writer, bus *events.Bus, log *slog.Logger) *Scheduler {
	host, _ := os.Hostname()
	sc := &Scheduler{
		cfg: cfg, store: st, prov: prov, raw: raw, writer: writer,
		planner: NewPlanner(st, ch, log, cfg.Tron.TRC20LocalIdentityEnabled), bus: bus, log: log,
		workerID:    fmt.Sprintf("%s-%d", host, os.Getpid()),
		concurrency: cfg.Ankr.InitialConcurrency,
		pagePolicy:  ankr.NewPagePolicy(cfg),
		meta:        addressmeta.New(st, ch),
		tokens:      newTokenRegistry(),
	}
	// The planner uses read-only provider probes to bound a never-seen TRON
	// address to its actual indexed activity window before creating shards.
	sc.planner.prov = prov
	// Local Durable Spool（§37/§38）
	if cfg.Ankr.LocalSpoolEnabled {
		sp, err := spool.New(spool.Config{Dir: cfg.Ankr.SpoolDir, MaxBytes: cfg.Ankr.SpoolMaxBytes})
		if err != nil {
			log.Error("spool init failed, falling back to synchronous path", "err", err)
		} else {
			sc.spool = sp
		}
	}
	// 全局内存预算（§47）
	sc.mem = newByteBudget(cfg.Ankr.FetchMemoryBudgetBytes)
	// Backpressure（§71/§72）
	sc.bp = newBackpressure(cfg, sc.spool, sc.mem, prov)
	// 异步 Ingest 流水线（§55）
	if cfg.Ankr.AsyncIngestEnabled && sc.spool != nil {
		sc.pipeline = newIngestPipeline(cfg, st, sc.spool, raw, log)
		sc.pipeline.sc = sc
	}
	return sc
}

func (sc *Scheduler) Run(ctx context.Context) error {
	sc.log.Info("worker started", "worker_id", sc.workerID, "initial_concurrency", sc.concurrency,
		"async_ingest", sc.cfg.Ankr.AsyncIngestEnabled, "local_spool", sc.cfg.Ankr.LocalSpoolEnabled)
	// 先启动流水线（submit 依赖 pipeline ctx），再执行 kill -9 恢复（§43）
	if sc.pipeline != nil {
		sc.pipeline.start(ctx)
	}
	if err := sc.recoverSpool(ctx); err != nil {
		sc.log.Error("spool recovery failed", "err", err)
	}
	go sc.controlLoop(ctx)
	go sc.dispatchLoop(ctx)
	go sc.reaperLoop(ctx)
	go sc.concurrencyLoop(ctx)
	go sc.progressLoop(ctx)
	go sc.finalizerLoop(ctx)
	go sc.tailLoop(ctx)
	go sc.benchmarkLoop(ctx)
	<-ctx.Done()
	sc.log.Info("worker stopping, waiting for in-flight shards")
	sc.wg.Wait()
	return nil
}

func (sc *Scheduler) currentConcurrency() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.concurrency
}

func (sc *Scheduler) setConcurrency(n int) {
	if n < 1 {
		n = 1
	}
	// V1.2：限幅为全局硬 Ceiling（§33：ANKR_GLOBAL_MAX_INFLIGHT=600）
	if n > sc.ceiling() {
		n = sc.ceiling()
	}
	if sc.cfg.Ankr.PerformanceModesEnabled && n > sc.cfg.Ankr.ProdHardCeiling {
		n = sc.cfg.Ankr.ProdHardCeiling
	}
	if n > sc.cfg.Ankr.MaxConcurrency {
		n = sc.cfg.Ankr.MaxConcurrency
	}
	sc.mu.Lock()
	sc.concurrency = n
	sc.mu.Unlock()
}

// markFirstFetchByJob 记录 job 级 time_to_first_fetch_ms（首条 Shard 进入调度即算，§74）。
func (sc *Scheduler) markFirstFetchByJob(ctx context.Context, shards []model.Shard) {
	seen := map[string]bool{}
	for _, sh := range shards {
		if seen[sh.JobID] {
			continue
		}
		seen[sh.JobID] = true
		if _, err := sc.store.Pool.Exec(ctx,
			"UPDATE download_jobs SET stats = COALESCE(stats,'{}'::jsonb) || jsonb_build_object('time_to_first_fetch_ms', "+
				"GREATEST(0, ROUND(EXTRACT(EPOCH FROM (now() - created_at))*1000)::int)) "+
				"WHERE id=$1 AND (stats IS NULL OR NOT stats ? 'time_to_first_fetch_ms')", sh.JobID); err != nil {
			sc.log.Warn("time_to_first_fetch stats update failed", "job_id", sh.JobID, "err", err)
		}
	}
}

// fetchSlots 可用 Fetch 槽位（§48 Weighted Concurrency：按权重占用）。
func (sc *Scheduler) fetchSlots() int {
	cur := sc.currentConcurrency()
	used := atomic.LoadInt64(&sc.weighted)
	free := cur - int(used)
	if free < 0 {
		free = 0
	}
	return free
}

func (sc *Scheduler) dispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if sc.bp != nil && !sc.bp.DispatchAllowed() {
				continue // 背压：停发新 Shard（§72）
			}
			free := sc.fetchSlots()
			if free <= 0 {
				continue
			}
			mode, shadow := sc.v14DispatchMode()
			shards, err := sc.store.LeaseShardsOrdered(ctx, sc.workerID, free, 5*time.Minute, mode)
			if err != nil {
				sc.log.Error("lease shards failed", "err", err)
				continue
			}
			if shadow {
				sc.queueShadowSample(ctx, shards) // §56：只记录 diff，不改真实顺序
			}
			// V1.2（§74）：Time-to-First-Fetch —— 首批 Shard 领取即记录（只写一次）
			sc.markFirstFetchByJob(ctx, shards)
			for _, sh := range shards {
				sh := sh
				atomic.AddInt64(&sc.weighted, 1) // 首权重 1（§48：首页后按大小调整）
				sc.wg.Add(1)
				go sc.runShard(ctx, sh)
			}
		}
	}
}

func (sc *Scheduler) reaperLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := sc.store.ResetExpiredLeases(ctx, time.Now().Add(-5*time.Minute))
			if err != nil {
				sc.log.Error("reaper failed", "err", err)
			} else if n > 0 {
				sc.log.Warn("reaped expired leases", "count", n)
			}
		}
	}
}

// concurrencyLoop 自适应并发：V1.2 Ramp（ANKR_GLOBAL_AUTO_TUNE_ENABLED=true，默认）
// 或 V1.1 AIMD + Provider 健康 + CH 背压（§73/§74）。
func (sc *Scheduler) concurrencyLoop(ctx context.Context) {
	if sc.cfg.Ankr.PerformanceModesEnabled {
		sc.performanceModeLoop(ctx)
		return
	}
	if sc.cfg.Global.AutoTuneEnabled {
		sc.concurrencyLoopV12(ctx)
		return
	}
	if sc.cfg.Global.FixedConcurrencyEnabled {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if sc.bp != nil {
					sc.bp.Refresh(ctx, sc.aggOrNil())
				}
			}
		}
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	recoverSince := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hs := sc.prov.Health(ctx)
			var pending int64
			_ = sc.store.Pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM download_shards WHERE status IN ('PENDING','FAILED_RETRYABLE')").Scan(&pending)
			active := atomic.LoadInt64(&sc.active)
			cur := sc.currentConcurrency()

			if ac, ok := sc.prov.(*ankr.Client); ok {
				conn := ac.ConnectionLimitCount()
				if conn > sc.lastConnLimit {
					cur = maxInt(1, cur/2)
					sc.log.Warn("connection limit detected, reducing concurrency", "current", cur)
				}
				sc.lastConnLimit = conn
			}

			chDegraded := false
			if sc.pipeline != nil && sc.pipeline.agg != nil {
				if !sc.pipeline.agg.Healthy() || sc.pipeline.agg.InsertP95() > sc.cfg.Ankr.CHInsertP95Thresh {
					chDegraded = true
				}
			}

			switch {
			case hs.State == provider.HealthOpen:
				cur = 1
				recoverSince = time.Time{}
			case hs.State == provider.HealthDegraded || chDegraded:
				cur = maxInt(1, cur/2)
				recoverSince = time.Time{}
			default:
				if hs.RecentErrorRate > 0.10 {
					cur = maxInt(1, cur/2)
					recoverSince = time.Time{}
				} else if hs.RecentErrorRate < 0.01 && pending > active && active >= int64(cur) {
					// Additive Increase（§74）：持续良好才 +2
					if recoverSince.IsZero() {
						recoverSince = time.Now()
					}
					if time.Since(recoverSince) > 60*time.Second {
						cur = minInt(sc.ceiling(), cur+2)
					}
				} else {
					recoverSince = time.Time{}
				}
			}
			sc.setConcurrency(cur)
			if sc.bp != nil {
				sc.bp.Refresh(ctx, sc.aggOrNil())
			}
		}
	}
}

func (sc *Scheduler) aggOrNil() *batchAggregator {
	if sc.pipeline == nil {
		return nil
	}
	return sc.pipeline.agg
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (sc *Scheduler) publish(subject string, payload any) {
	sc.bus.Publish(context.Background(), subject, payload)
}

// requeueDispatchable 统计可调度 Shard 数（背压参考）。
func (sc *Scheduler) requeueDispatchable(ctx context.Context) int64 {
	var n int64
	_ = sc.store.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM download_shards WHERE status IN ('PENDING','FAILED_RETRYABLE')").Scan(&n)
	return n
}

// setGoodput 记录 Fetch Goodput（rows/s）。
func setGoodput(v float64) {
	currentGoodput.Store(math.Float64bits(v))
}

var _ = model.JobRunning
