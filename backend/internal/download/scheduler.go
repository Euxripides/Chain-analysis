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

	"github.com/blockchain-platform/blockchain-platform/internal/addressmeta"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/events"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/tokenmeta"
)

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
	profile       model.DownloadPerformanceProfile
	requestedMode model.PerformanceMode
	effectiveMode model.PerformanceMode
	modeReason    string
	modeChangedAt time.Time
	healthyTicks  int
	active        int64
	wg            sync.WaitGroup

	rates         sync.Map // jobID -> *rateTracker
	pagePolicy    *ankr.PagePolicy
	lastConnLimit int64
	meta          *addressmeta.Service

	// V1.3：Fast Method Resolver（§16/§17）。只读内存索引，不阻塞下载。
	methodResolver *method.FastResolver
	methodRegistry *method.Registry
	tokenMeta      *tokenmeta.Service
}

func (sc *Scheduler) SetTokenMetadata(service *tokenmeta.Service) { sc.tokenMeta = service }

// SetMethodResolver 注入 Method 解析栈。为空时下载链路完全不做解析，
// 交易本体照常入库（§36：Method 未解析不影响 Coverage）。
func (sc *Scheduler) SetMethodResolver(resolver *method.FastResolver, registry *method.Registry) {
	sc.methodResolver = resolver
	sc.methodRegistry = registry
}

func NewScheduler(cfg *config.Config, st *store.Store, ch *clickhouse.Conn, prov provider.Provider, raw *rawstore.Store,
	writer *ingest.Writer, bus *events.Bus, log *slog.Logger) *Scheduler {
	host, _ := os.Hostname()
	profile := cfg.AnkrProductionProfile()
	return &Scheduler{
		cfg: cfg, store: st, prov: prov, raw: raw, writer: writer,
		planner: NewPlanner(st, log), bus: bus, log: log,
		workerID:    fmt.Sprintf("%s-%d", host, os.Getpid()),
		concurrency: profile.Resolve(profile.DefaultMode), profile: profile,
		requestedMode: profile.DefaultMode, effectiveMode: profile.DefaultMode,
		modeReason: "system_default", modeChangedAt: time.Now(),
		pagePolicy: ankr.NewPagePolicy(cfg),
		meta:       addressmeta.New(st, ch),
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
	if sc.profile.HardCeiling > 0 && n > sc.profile.HardCeiling {
		n = sc.profile.HardCeiling
	}
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

// concurrencyLoop maps requested SAFE/FAST/TURBO modes to the audited profile
// and applies bounded downward protection. It never probes above TURBO.
func (sc *Scheduler) concurrencyLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if profile, err := sc.store.GetAnkrProductionProfile(ctx); err == nil {
				sc.mu.Lock()
				sc.profile = profile
				sc.mu.Unlock()
			}
			hs := sc.prov.Health(ctx)
			active := atomic.LoadInt64(&sc.active)
			sc.mu.Lock()
			profile := sc.profile
			requested := sc.requestedMode
			current := sc.effectiveMode
			sc.mu.Unlock()
			if activeMode, ok, err := sc.store.HighestActiveRequestedMode(ctx); err == nil && ok {
				requested = activeMode
			} else if !ok {
				if configured, configuredErr := sc.store.GetAnkrConfiguredMode(ctx); configuredErr == nil {
					requested = configured
				} else {
					requested = profile.DefaultMode
				}
			}

			pressure := hs.State == provider.HealthOpen || hs.State == provider.HealthDegraded || hs.RecentErrorRate > 0.10
			reason := "user_selected"
			if hs.State == provider.HealthOpen {
				reason = "provider_circuit_open"
			}
			if hs.State == provider.HealthDegraded || hs.RecentErrorRate > 0.10 {
				reason = "provider_degraded"
			}

			if ac, ok := sc.prov.(*ankr.Client); ok {
				conn := ac.ConnectionLimitCount()
				if conn > sc.lastConnLimit {
					pressure = true
					reason = "provider_degraded"
				}
				sc.lastConnLimit = conn
			}
			if pressure {
				sc.healthyTicks = 0
			} else {
				sc.healthyTicks++
			}
			effective := nextEffectiveMode(current, requested, pressure, sc.healthyTicks >= 3)
			if effective != current {
				sc.healthyTicks = 0
				if !pressure {
					reason = "recovered"
				}
				sc.modeChangedAt = time.Now()
				sc.log.Info("Ankr performance mode changed", "requested_mode", requested,
					"old_effective_mode", current, "new_effective_mode", effective, "reason", reason)
				_ = sc.store.InsertAudit(ctx, nil, "ankr_performance_mode_changed", "ankr_scheduler", "global", map[string]any{
					"requested_mode": requested, "old_effective_mode": current, "new_effective_mode": effective,
					"reason": reason, "target_before": profile.Resolve(current), "target_after": profile.Resolve(effective),
				})
				sc.publish("events.ankr.performance_mode", map[string]any{
					"type": "performance_mode", "requested_mode": requested, "old_effective_mode": current,
					"effective_mode": effective, "reason": reason, "ts": time.Now(),
				})
			} else if !pressure && effective == requested {
				if active == 0 {
					reason = "system_default"
				}
			}
			target := profile.Resolve(effective)
			sc.setConcurrency(target)
			sc.mu.Lock()
			sc.requestedMode, sc.effectiveMode, sc.modeReason = requested, effective, reason
			changedAt := sc.modeChangedAt
			sc.mu.Unlock()
			_ = sc.store.SetAnkrRuntimeState(ctx, model.PerformanceRuntimeState{
				RequestedMode: requested, EffectiveMode: effective, TargetConcurrency: sc.currentConcurrency(),
				ActiveConcurrency: int(active), ModeReason: reason, ModeChangedAt: changedAt,
			})
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
