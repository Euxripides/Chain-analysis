// Package download：下载调度核心（Worker 进程）。
// 并发模型：Address × Chain × Dataset × Shard 并行，Shard 内串行翻页（Ankr pageToken 天然串行）。
package download

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/events"
	"github.com/blockchain-platform/blockchain-platform/internal/addressmeta"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

type Scheduler struct {
	cfg     *config.Config
	store   *store.Store
	prov    provider.Provider
	raw     *rawstore.Store
	writer  *ingest.Writer
	planner *Planner
	bus     *events.Bus
	log     *slog.Logger
	workerID string

	mu          sync.Mutex
	concurrency int
	active      int64
	wg          sync.WaitGroup

	rates      sync.Map // jobID -> *rateTracker
	pagePolicy *ankr.PagePolicy
	lastConnLimit int64
	meta       *addressmeta.Service

	// V1.3：Fast Method Resolver（§16/§17）。只读内存索引，不阻塞下载。
	methodResolver *method.FastResolver
	methodRegistry *method.Registry
}

// SetMethodResolver 注入 Method 解析栈。为空时下载链路完全不做解析，
// 交易本体照常入库（§36：Method 未解析不影响 Coverage）。
func (sc *Scheduler) SetMethodResolver(resolver *method.FastResolver, registry *method.Registry) {
	sc.methodResolver = resolver
	sc.methodRegistry = registry
}

func NewScheduler(cfg *config.Config, st *store.Store, prov provider.Provider, raw *rawstore.Store,
	writer *ingest.Writer, bus *events.Bus, log *slog.Logger) *Scheduler {
	host, _ := os.Hostname()
	return &Scheduler{
		cfg: cfg, store: st, prov: prov, raw: raw, writer: writer,
		planner: NewPlanner(st, log), bus: bus, log: log,
		workerID:    fmt.Sprintf("%s-%d", host, os.Getpid()),
		concurrency: cfg.Ankr.InitialConcurrency,
		pagePolicy:  ankr.NewPagePolicy(cfg),
		meta:        addressmeta.New(st),
	}
}

func (sc *Scheduler) Run(ctx context.Context) error {
	sc.log.Info("worker started", "worker_id", sc.workerID, "initial_concurrency", sc.concurrency)
	go sc.controlLoop(ctx)
	go sc.dispatchLoop(ctx)
	go sc.reaperLoop(ctx)
	go sc.concurrencyLoop(ctx)
	go sc.progressLoop(ctx)
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
	if n > sc.cfg.Ankr.MaxConcurrency {
		n = sc.cfg.Ankr.MaxConcurrency
	}
	sc.mu.Lock()
	sc.concurrency = n
	sc.mu.Unlock()
}

func (sc *Scheduler) dispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			free := sc.currentConcurrency() - int(atomic.LoadInt64(&sc.active))
			if free <= 0 {
				continue
			}
			shards, err := sc.store.LeaseShards(ctx, sc.workerID, free, 5*time.Minute)
			if err != nil {
				sc.log.Error("lease shards failed", "err", err)
				continue
			}
			for _, sh := range shards {
				sh := sh
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

// concurrencyLoop 自适应并发：AIMD + Provider 健康状态（安全门：429/5xx/超时率、P95、断路器）。
func (sc *Scheduler) concurrencyLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
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

			// V1.1（§23/§29）：Connection Limit 由 Concurrency Controller 响应，
			// 不因单地址超时/大响应直接降全局并发。
			if ac, ok := sc.prov.(*ankr.Client); ok {
				conn := ac.ConnectionLimitCount()
				if conn > sc.lastConnLimit {
					cur = maxInt(1, cur/2)
					sc.log.Warn("connection limit detected, reducing concurrency", "current", cur)
				}
				sc.lastConnLimit = conn
			}

			switch hs.State {
			case provider.HealthOpen:
				cur = 1
			case provider.HealthDegraded:
				cur = maxInt(1, cur/2)
			default:
				if hs.RecentErrorRate > 0.10 {
					cur = maxInt(1, cur/2)
				} else if hs.RecentErrorRate < 0.01 && pending > active && active >= int64(cur) {
					cur = minInt(sc.cfg.Ankr.MaxConcurrency, cur+2)
				}
			}
			sc.setConcurrency(cur)
		}
	}
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

// requeueDispatchable 重新统计可调度数量（供并发控制判断背压）。
func (sc *Scheduler) requeueDispatchable(ctx context.Context) int64 {
	var n int64
	_ = sc.store.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM download_shards WHERE status IN ('PENDING','FAILED_RETRYABLE')").Scan(&n)
	return n
}

var _ = model.JobRunning
