// V1.2 Global Concurrency Ramp（§33-§38/§98-§102/§41）：
// ANKR_GLOBAL_MAX_INFLIGHT=600 是全局硬 Ceiling（所有链/地址/数据集共享，§33/§34/§35）；
// 实际并发通过 Ramp 逐级试探（§37/§99），Goodput 边际收益不足必须停止（§38/§41/§101）。
// Memory / Spool / CH Backpressure 优先级永远高于 600 ceiling（§111-§114）。
package download

import (
	"context"
	"math"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// rampPhase 状态。
const (
	rampPhaseStart    = "warming"
	rampPhaseStable   = "stable"
	rampPhaseRollback = "rollback"
	rampPhaseDisabled = "disabled"
)

type rampSample struct {
	at      time.Time
	goodput float64
}

type globalRamp struct {
	phase            string
	idx              int // RampSteps 当前下标
	levelStart       time.Time
	prevGoodput      float64
	samples          []rampSample
	last429          int64
	lastConn         int64
	lastTimeout      int64
	lastTooLarge     int64
	rollbackUntil    time.Time
	settledTarget    int
	firstFetchAt     time.Time
	fullSafeAt       time.Time
	fullSafeMeasured bool
	lastTTFBUpdate   time.Time
	ttfbP50Ms        int
	latP50Ms         int
	latP95Ms         int
}

// ceiling 全局硬 Ceiling（§33：ANKR_GLOBAL_MAX_INFLIGHT）。
// observabilityGoodputGlobal 全局 Fetch Goodput（rows/s；progressLoop 写入，§41）。
func observabilityGoodputGlobal() float64 {
	return math.Float64frombits(currentGoodput.Load())
}

func (sc *Scheduler) ceiling() int {
	if sc.cfg.Global.MaxInflight > 0 {
		return sc.cfg.Global.MaxInflight
	}
	return sc.cfg.Ankr.MaxConcurrency
}

// concurrencyLoopV12 Ramp 状态机（Auto-tune 开启时替换 V1.1 AIMD）。
func (sc *Scheduler) concurrencyLoopV12(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	rc := &globalRamp{}
	steps := sc.cfg.Global.RampSteps
	if len(steps) == 0 {
		steps = []int{4, 8, 16, 32, 64, 96, 128, 192, 256, 384, 512, 600}
	}
	initial := sc.cfg.Ankr.InitialConcurrency
	if initial < 1 {
		initial = steps[0]
	}
	startIdx := 0
	for i, s := range steps {
		if s >= initial {
			startIdx = i
			break
		}
	}
	rc.idx = startIdx
	rc.phase = rampPhaseStart
	rc.levelStart = time.Now()
	rc.settledTarget = steps[startIdx]
	sc.setConcurrency(steps[startIdx])
	sc.log.Info("global concurrency ramp started",
		"ceiling", sc.ceiling(), "steps", steps, "start_level", steps[startIdx],
		"auto_tune", sc.cfg.Global.AutoTuneEnabled)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if sc.bp != nil {
				sc.bp.Refresh(ctx, sc.aggOrNil())
			}
			hs := sc.prov.Health(ctx)
			sc.rampStep(ctx, rc, steps, hs)
		}
	}
}

// rampStep 每个 5s tick：采样 → 稳态判定 → 升级/稳定/回退。
func (sc *Scheduler) rampStep(ctx context.Context, rc *globalRamp, steps []int, hs provider.HealthStatus) {
	now := time.Now()
	cur := sc.currentConcurrency()
	goodput := observabilityGoodputGlobal()
	active := atomic.LoadInt64(&sc.active)

	if active > 0 && rc.firstFetchAt.IsZero() {
		rc.firstFetchAt = now
	}

	// 采样（当前级别内）
	rc.samples = append(rc.samples, rampSample{at: now, goodput: goodput})
	if len(rc.samples) > 300 {
		rc.samples = rc.samples[len(rc.samples)-300:]
	}

	// 错误增量信号（§40/§101）
	new429, newConn, newTimeout, newTooLarge := sc.errDeltas(rc)
	errNow := new429 + newConn + newTimeout + newTooLarge
	reasons := sc.pressureReasons()
	if hs.State == provider.HealthOpen || hs.State == provider.HealthDegraded {
		reasons = append(reasons, "provider_health:"+string(hs.State))
	}
	if rc.phase != rampPhaseDisabled && (errNow > 0 || len(reasons) > 0) {
		sc.log.Warn("global ramp pressure detected",
			"level", cur, "err429", new429, "conn_limit", newConn, "timeout", newTimeout,
			"too_large", newTooLarge, "reasons", reasons)
		rc.rollback(sc, steps, reasons)
		sc.persistRampState(ctx, rc, goodput)
		return
	}

	// 空闲（无 Ready 工作）不推进（§37：Ramp 需要足够 Ready Shards）
	var pending int64
	_ = sc.store.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM download_shards WHERE status IN ('PENDING','FAILED_RETRYABLE')").Scan(&pending)
	if pending == 0 && active == 0 {
		sc.persistRampState(ctx, rc, goodput)
		return
	}

	need := sc.cfg.Global.RampSettleSamples
	if need <= 0 {
		need = 9
	}
	if len(rc.samples) < need {
		sc.persistRampState(ctx, rc, goodput)
		return
	}
	mean := meanGoodput(rc.samples[len(rc.samples)-need:])
	rc.samples = rc.samples[:0]
	gain := 0.0
	if rc.prevGoodput > 0 {
		gain = (mean - rc.prevGoodput) / rc.prevGoodput
	}
	prev := rc.prevGoodput
	rc.prevGoodput = mean
	top := steps[len(steps)-1]
	// 基线种子：首个有产出且资源饱和的级别 → 先升级再测量（§37 逐级爬升）
	saturated := active >= int64(cur)*9/10 || pending > 0
	if gain == 0 && mean > 0 && saturated && errNow == 0 && rc.rollbackUntil.Before(now) && rc.idx < len(steps)-1 {
		rc.idx++
		next := steps[rc.idx]
		rc.phase = rampPhaseStart
		rc.levelStart = now
		sc.setConcurrency(next)
		rc.settledTarget = next
		sc.log.Info("global ramp seed up", "level", next, "goodput", mean)
		sc.persistRampState(ctx, rc, goodput)
		return
	}

	switch {
	case rc.rollbackUntil.After(now):
		// 冷却期内保持稳定，不再升级
		rc.phase = rampPhaseStable
		rc.settledTarget = cur
		sc.log.Info("global ramp in cooldown", "settled_target", cur, "until", rc.rollbackUntil)
	case rc.idx >= len(steps)-1 && cur >= top:
		rc.phase = rampPhaseStable
		rc.settledTarget = top
		sc.log.Info("global ramp reached ceiling", "ceiling", top, "goodput", mean)
	case gain < sc.cfg.Global.GoodputMinGainPct/100.0:
		// §38/§41：Goodput 边际收益不足 → 稳定在当前档位；
		// 只有 Fetch 仍活跃且收益显著为负才回退（纯 Ingest 拖尾期不做无意义回退）
		if mean < prev && rc.idx > 0 && active > 0 && rc.prevGoodput > 0 && gain < -0.5 {
			rc.idx--
		}
		rc.phase = rampPhaseStable
		rc.settledTarget = steps[rc.idx]
		sc.setConcurrency(steps[rc.idx])
		sc.log.Info("global ramp stopped by goodput gate",
			"level", rc.settledTarget, "gain_pct", gain*100, "goodput", mean)
		if sc.cfg.Global.ProfileLearnEnabled {
			sc.learnGlobalProfile(ctx, rc, mean, steps[rc.idx])
		}
	default:
		// 升级（§37/§99：4→8→16→...→600）
		if rc.idx < len(steps)-1 {
			rc.idx++
		}
		next := steps[rc.idx]
		rc.phase = rampPhaseStart
		rc.levelStart = now
		sc.setConcurrency(next)
		rc.settledTarget = next
		sc.log.Info("global ramp up", "level", next, "gain_pct", gain*100, "goodput", mean)
	}

	// time_to_full_safe_concurrency（§75）
	if !rc.fullSafeMeasured && !rc.firstFetchAt.IsZero() {
		target := rc.settledTarget
		if target <= 0 {
			target = cur
		}
		if int64(active) >= int64(target)*85/100 && now.Sub(rc.firstFetchAt) > 30*time.Second {
			rc.fullSafeMeasured = true
			rc.fullSafeAt = now
			ms := now.Sub(rc.firstFetchAt).Milliseconds()
			sc.log.Info("full safe concurrency reached", "active", active, "target", target, "elapsed_ms", ms)
		}
	}
	sc.persistRampState(ctx, rc, goodput)
}

// errDeltas Provider 错误计数增量（§40：429 → Rate↓；Connection Limit → Concurrency↓）。
func (sc *Scheduler) errDeltas(rc *globalRamp) (new429, newConn, newTimeout, newTooLarge int64) {
	if ac, ok := sc.ankrClient(); ok {
		c429 := ac.RateLimitCount()
		cConn := ac.ConnectionLimitCount()
		cTimeout := ac.TimeoutCount()
		cTooLarge := ac.TooLargeCount()
		new429 = c429 - rc.last429
		newConn = cConn - rc.lastConn
		newTimeout = cTimeout - rc.lastTimeout
		newTooLarge = cTooLarge - rc.lastTooLarge
		rc.last429 = c429
		rc.lastConn = cConn
		rc.lastTimeout = cTimeout
		rc.lastTooLarge = cTooLarge
	}
	return
}

// pressureReasons 背压/健康信号（§111-§114：任一信号优先级高于 600 ceiling）。
func (sc *Scheduler) pressureReasons() []string {
	var reasons []string
	if sc.bp != nil {
		if sc.bp.State() >= stateProtected {
			reasons = append(reasons, "backpressure:"+sc.bp.Reason())
		}
	}
	if sc.pipeline != nil && sc.pipeline.agg != nil {
		if !sc.pipeline.agg.Healthy() {
			reasons = append(reasons, "clickhouse_degraded")
		} else if sc.pipeline.agg.InsertP95() > sc.cfg.Ankr.CHInsertP95Thresh {
			reasons = append(reasons, "clickhouse_p95")
		}
	}
	if sc.mem != nil {
		if used := sc.mem.Used(); used > sc.cfg.Ankr.FetchMemoryBudgetBytes*85/100 {
			reasons = append(reasons, "memory_budget")
		}
	}
	if sc.spool != nil && sc.cfg.Ankr.SpoolMaxBytes > 0 {
		if used, err := sc.spool.UsedBytes(); err == nil {
			if float64(used)/float64(sc.cfg.Ankr.SpoolMaxBytes) >= sc.cfg.Ankr.SpoolWatermarkReduce {
				reasons = append(reasons, "spool_watermark")
			}
		}
	}
	return reasons
}

func meanGoodput(samples []rampSample) float64 {
	var sum float64
	var n int
	for _, s := range samples {
		if s.goodput > 0 {
			sum += s.goodput
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// rollback 回退一级 + 冷却（§101/§102）。
func (rc *globalRamp) rollback(sc *Scheduler, steps []int, reasons []string) {
	cur := sc.currentConcurrency()
	if rc.idx > 0 {
		rc.idx--
	}
	next := steps[rc.idx]
	sc.setConcurrency(next)
	rc.phase = rampPhaseRollback
	rc.rollbackUntil = time.Now().Add(sc.cfg.Global.RollbackCooldown)
	rc.levelStart = time.Now()
	rc.samples = nil
	rc.prevGoodput = 0
	rc.settledTarget = next
	sc.log.Warn("global concurrency rolled back", "from", cur, "to", next,
		"cooldown", sc.cfg.Global.RollbackCooldown, "reasons", reasons)
}

// learnGlobalProfile §42：Ramp 稳态落画像（chain_mix/dataset_mix 聚合；P1 学习）。
func (sc *Scheduler) learnGlobalProfile(ctx context.Context, rc *globalRamp, goodput float64, target int) {
	prof := &model.GlobalConcurrencyProfile{
		ChainMix: "all", DatasetMix: "all",
		PreferredGlobalConcurrency: target,
		Goodput:                    goodput,
	}
	if lat, _, err := sc.recentLatencyContext(ctx, 10); err == nil {
		prof.P50Ms = lat[0]
		prof.P95Ms = lat[1]
	}
	if err := sc.store.UpsertGlobalConcurrencyProfile(ctx, prof); err != nil {
		sc.log.Warn("global profile upsert failed", "err", err)
	}
}

// recentLatencyContext 最近 N 分钟 ok 请求的 p50/p95（总延迟 + TTFB，§55）。
func (sc *Scheduler) recentLatencyContext(ctx context.Context, minutes int) ([2]int, [2]int, error) {
	var lat [2]int
	var ttfb [2]int
	err := sc.store.Pool.QueryRow(ctx,
		"SELECT COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY duration_ms),0)::int, "+
			"COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms),0)::int, "+
			"COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY ttfb_ms),0)::int, "+
			"COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY ttfb_ms),0)::int "+
			"FROM provider_usage WHERE status='ok' AND ttfb_ms > 0 AND request_time > now() - make_interval(mins => $1)",
		minutes).Scan(&lat[0], &lat[1], &ttfb[0], &ttfb[1])
	return lat, ttfb, err
}

// persistRampState 写心跳（API / 前端 Global Concurrency Card 数据源，§80）。
func (sc *Scheduler) persistRampState(ctx context.Context, rc *globalRamp, goodput float64) {
	var memUsed, spoolUsed int64
	if sc.mem != nil {
		memUsed = sc.mem.Used()
	}
	if sc.spool != nil {
		if u, err := sc.spool.UsedBytes(); err == nil {
			spoolUsed = u
		}
	}
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	observability.AnkrHeapAllocBytes.Set(float64(memStats.HeapAlloc))
	observability.AnkrHeapInuseBytes.Set(float64(memStats.HeapInuse))
	observability.AnkrHeapSysBytes.Set(float64(memStats.HeapSys))
	if sc.mem != nil {
		observability.AnkrFetchDecodedActiveBytes.Set(float64(sc.mem.Used()))
	}
	if sc.pipeline != nil {
		observability.AnkrParserQueueBytes.Set(float64(len(sc.pipeline.queue)) * float64(sc.cfg.Ankr.CHBatchMaxBytes))
	}
	observability.AnkrSpoolPendingBytes.Set(float64(spoolUsed))
	state, reason := "NORMAL", ""
	if sc.bp != nil && sc.bp.State() != stateNormal {
		state = sc.bp.State().String()
		reason = sc.bp.Reason()
	}
	now := time.Now()
	if now.Sub(rc.lastTTFBUpdate) > 30*time.Second {
		if lat, ttfb, err := sc.recentLatencyContext(ctx, 10); err == nil {
			rc.latP50Ms = lat[0]
			rc.latP95Ms = lat[1]
			rc.ttfbP50Ms = ttfb[0]
		}
		rc.lastTTFBUpdate = now
	}
	ms := int64(0)
	if rc.fullSafeMeasured && !rc.fullSafeAt.IsZero() {
		ms = rc.fullSafeAt.Sub(rc.firstFetchAt).Milliseconds()
	}
	st := &model.SchedulerState{
		WorkerID:                     sc.workerID,
		ConfiguredInitialConcurrency: sc.cfg.Ankr.InitialConcurrency,
		ConfiguredMaxConcurrency:     sc.cfg.Ankr.MaxConcurrency,
		GlobalHardCeiling:            sc.ceiling(),
		ReadyQueueEnabled:            sc.cfg.Ankr.GlobalReadyQueueEnabled,
		ReadyQueueShadow:             sc.cfg.Ankr.GlobalReadyQueueShadow,
		ReadyQueueMode:               sc.leaseMode(),
		AutoTuneEnabled:              sc.cfg.Global.AutoTuneEnabled,
		ControllerMode:               "global_ramp",
		ActiveSlots:                  int(atomic.LoadInt64(&sc.active)),
		WeightedSlots:                int(atomic.LoadInt64(&sc.weighted)),
		ConcurrencyTarget:            sc.currentConcurrency(),
		HardCeiling:                  sc.ceiling(),
		RampLevel:                    rc.idx,
		RampPhase:                    rc.phase,
		ControlState:                 state,
		ControlReason:                reason,
		Goodput:                      goodput,
		TTFBP50Ms:                    rc.ttfbP50Ms,
		MemUsedBytes:                 memUsed,
		SpoolUsedBytes:               spoolUsed,
		TimeToFullSafeConcurrencyMs:  ms,
	}
	sc.rampPhase.Store(rc.phase)
	cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := sc.store.UpsertSchedulerState(cctx, st); err != nil {
		sc.log.Warn("scheduler state write failed", "err", err)
	}
	observability.AnkrGlobalActiveSlots.Set(float64(st.ActiveSlots))
	observability.AnkrGlobalTargetSlots.Set(float64(st.ConcurrencyTarget))
	observability.AnkrGlobalCeiling.Set(float64(st.HardCeiling))
	observability.AnkrGlobalGoodput.Set(goodput)
	observability.AnkrTimeToFullSafeConcurrencyMs.Set(float64(ms))
}

// persistStaticTelemetry keeps the same worker heartbeat truthful when the
// V1.2 ramp is disabled.  Previously all scheduler gauges were only refreshed
// by concurrencyLoopV12, leaving /fetch-runtime and Prometheus stale under
// ANKR_GLOBAL_AUTO_TUNE_ENABLED=false.
func (sc *Scheduler) persistStaticTelemetry(ctx context.Context) {
	var memUsed, spoolUsed int64
	if sc.mem != nil {
		memUsed = sc.mem.Used()
	}
	if sc.spool != nil {
		if u, err := sc.spool.UsedBytes(); err == nil {
			spoolUsed = u
		}
	}
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	observability.AnkrHeapAllocBytes.Set(float64(memStats.HeapAlloc))
	observability.AnkrHeapInuseBytes.Set(float64(memStats.HeapInuse))
	observability.AnkrHeapSysBytes.Set(float64(memStats.HeapSys))
	if sc.mem != nil {
		observability.AnkrFetchDecodedActiveBytes.Set(float64(sc.mem.Used()))
	}
	if sc.pipeline != nil {
		observability.AnkrParserQueueBytes.Set(float64(len(sc.pipeline.queue)) * float64(sc.cfg.Ankr.CHBatchMaxBytes))
	}
	observability.AnkrSpoolPendingBytes.Set(float64(spoolUsed))
	state, reason := "NORMAL", ""
	if sc.bp != nil && sc.bp.State() != stateNormal {
		state, reason = sc.bp.State().String(), sc.bp.Reason()
	}
	controllerMode := "aimd"
	if sc.cfg.Global.FixedConcurrencyEnabled {
		controllerMode = "fixed_benchmark"
	}
	st := &model.SchedulerState{
		WorkerID:                     sc.workerID,
		ConfiguredInitialConcurrency: sc.cfg.Ankr.InitialConcurrency,
		ConfiguredMaxConcurrency:     sc.cfg.Ankr.MaxConcurrency,
		GlobalHardCeiling:            sc.ceiling(),
		ReadyQueueEnabled:            sc.cfg.Ankr.GlobalReadyQueueEnabled,
		ReadyQueueShadow:             sc.cfg.Ankr.GlobalReadyQueueShadow,
		ReadyQueueMode:               sc.leaseMode(),
		AutoTuneEnabled:              false,
		ControllerMode:               controllerMode,
		ActiveSlots:                  int(atomic.LoadInt64(&sc.active)),
		WeightedSlots:                int(atomic.LoadInt64(&sc.weighted)),
		ConcurrencyTarget:            sc.currentConcurrency(),
		HardCeiling:                  sc.ceiling(),
		RampLevel:                    0,
		RampPhase:                    rampPhaseDisabled,
		ControlState:                 state,
		ControlReason:                reason,
		Goodput:                      observabilityGoodputGlobal(),
		MemUsedBytes:                 memUsed,
		SpoolUsedBytes:               spoolUsed,
	}
	if err := sc.store.UpsertSchedulerState(ctx, st); err != nil {
		sc.log.Warn("static scheduler telemetry write failed", "err", err)
	}
	observability.AnkrGlobalActiveSlots.Set(float64(st.ActiveSlots))
	observability.AnkrGlobalTargetSlots.Set(float64(st.ConcurrencyTarget))
	observability.AnkrGlobalCeiling.Set(float64(st.HardCeiling))
	observability.AnkrGlobalGoodput.Set(st.Goodput)
}
