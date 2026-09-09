// V1.3 Provider Executor（§30/§31/§38-§40/§56）：每 Provider 独立 Queue / Executor /
// Rate Controller / Circuit；禁止全局大锁串行化；Queue 必须 bounded。
package countoracle

import (
	"context"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider/count"
)

// ProviderPolicy 单 Provider 执行策略（由配置构建；禁止把 Chainbase 1 RPS 写死到 Core）。
type ProviderPolicy struct {
	ProviderID string

	Enabled bool

	// MaxConcurrency 该 Provider Executor 并发上限。
	MaxConcurrency int
	// MaxRPS 每秒令牌（0 = 不限）。
	MaxRPS float64
	// Burst 瞬时突发。
	Burst int

	// QueueCapacity Bounded Queue（§40：禁止无限 pending）。
	QueueCapacity int

	// Retries 前台重试预算（§90：Planner 前台 0~1；后台可更多）。
	Retries int

	// RateLimitCooldown RATE_LIMIT 后冷却（仿 V1.2 §40 ~1.8s）。
	RateLimitCooldown time.Duration
	// QuotaCooldown QUOTA 后长冷却。
	QuotaCooldown time.Duration

	// CallTimeout 单次 Provider 调用超时。
	CallTimeout time.Duration

	CostPerRequestCredits float64
	CostPerRequestUSD     float64
}

// providerRuntime 每 Provider 运行态（§91 state machine 的执行器侧视图）。
type providerRuntime struct {
	id string

	mu            sync.Mutex
	state         string // DISABLED|SHADOW|AVAILABLE|DEGRADED|THROTTLED|RECOVERING|BLOCKED（§91）
	lastErr       string
	lastErrAt     time.Time
	nextAllowedAt time.Time // cooldown until

	// 滑动窗口（最近 300 次结果）
	results []bool
	ok      int64 // 窗口内成功
	lat     []int64
	total   int64
	success int64
	rlHits  int64
	toHits  int64
}

func newProviderRuntime(id string) *providerRuntime {
	return &providerRuntime{id: id, state: "AVAILABLE", results: nil, lat: nil}
}

const runtimeWindow = 300

func (rt *providerRuntime) record(success bool, latencyMs int64, class count.ErrorClass) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.total++
	rt.results = append(rt.results, success)
	if len(rt.results) > runtimeWindow {
		rt.results = rt.results[len(rt.results)-runtimeWindow:]
	}
	if success {
		rt.success++
		rt.lat = append(rt.lat, latencyMs)
		if len(rt.lat) > runtimeWindow {
			rt.lat = rt.lat[len(rt.lat)-runtimeWindow:]
		}
		rt.state = "AVAILABLE"
		rt.nextAllowedAt = time.Time{}
	} else {
		switch class {
		case count.ErrRateLimit:
			rt.rlHits++
			rt.state = "THROTTLED"
		case count.ErrTimeout:
			rt.toHits++
			if rt.state == "AVAILABLE" {
				rt.state = "DEGRADED"
			}
		case count.ErrAuth, count.ErrQuota:
			rt.state = "BLOCKED"
		case count.ErrProvider5xx, count.ErrConnection:
			if rt.state == "AVAILABLE" {
				rt.state = "DEGRADED"
			}
		}
	}
}

func (rt *providerRuntime) snapshot() providerRunSnapshot {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	s := providerRunSnapshot{
		State: rt.state, LastError: rt.lastErr, LastErrorAt: rt.lastErrAt,
		Total: rt.total, Success: rt.success,
	}
	ok := 0
	for _, b := range rt.results {
		if b {
			ok++
		}
	}
	if n := len(rt.results); n > 0 {
		s.SuccessRate = float64(ok) / float64(n)
	}
	if n := len(rt.lat); n > 0 {
		sorted := append([]int64(nil), rt.lat...)
		for i := 1; i < len(sorted); i++ {
			for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
				sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			}
		}
		s.P50Ms = int(sorted[(n*50)/100])
		s.P95Ms = int(sorted[(n*95)/100])
	}
	return s
}

func (rt *providerRuntime) setNextAllowed(t time.Time, err string) {
	rt.mu.Lock()
	rt.nextAllowedAt = t
	rt.lastErr = err
	rt.lastErrAt = time.Now()
	rt.mu.Unlock()
}

func (rt *providerRuntime) waitAllowed(now time.Time) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return now.After(rt.nextAllowedAt)
}

func (rt *providerRuntime) setState(state, err string) {
	rt.mu.Lock()
	rt.state = state
	rt.lastErr = err
	rt.lastErrAt = time.Now()
	rt.mu.Unlock()
}

type providerRunSnapshot struct {
	State       string
	LastError   string
	LastErrorAt time.Time
	Total       int64
	Success     int64
	SuccessRate float64
	P50Ms       int
	P95Ms       int
}

// ---- 每 Provider 令牌桶（Rate Controller，§26-§29 的最小实现：单层 GLOBAL/ACCOUNT） ----

type rateBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newRateBucket(rps float64, burst int) *rateBucket {
	b := float64(burst)
	if b <= 0 {
		b = rps
		if b <= 0 {
			b = 1
		}
	}
	return &rateBucket{rate: rps, burst: b, tokens: b, last: time.Now()}
}

// acquire 阻塞直到取得令牌或 ctx 取消。
func (b *rateBucket) acquire(ctx context.Context) error {
	if b.rate <= 0 {
		return nil
	}
	for {
		b.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(b.last).Seconds()
		b.tokens += elapsed * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((1-b.tokens)/b.rate*float64(time.Second)) + time.Millisecond
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// ---- Executor：bounded queue + worker pool + rate + retry（provider-specific） ----

type attemptRec struct {
	providerID   string
	requestHash  string
	chainID      uint64
	dataset      string
	status       string // ok | error
	errorClass   string
	latencyMs    int64
	count        int64
	requestsCost int64
	creditsCost  float64
	usdCost      float64
}

type callReq struct {
	ctx    context.Context
	req    count.CountRequest
	result chan callResult
}

type callResult struct {
	res *count.CountResult
	err error
}

type executor struct {
	provider count.CountProvider
	policy   *ProviderPolicy
	rt       *providerRuntime
	bucket   *rateBucket

	sem   chan struct{}
	queue chan *callReq

	active    int64
	queueSeen int64
	started   bool

	attemptHook func(a attemptRec) // DB 记录（可空）
}

func newExecutor(provider count.CountProvider, policy *ProviderPolicy, hook func(a attemptRec)) *executor {
	conc := policy.MaxConcurrency
	if conc <= 0 {
		conc = 1
	}
	qc := policy.QueueCapacity
	if qc <= 0 {
		qc = 64
	}
	return &executor{
		provider:    provider,
		policy:      policy,
		rt:          newProviderRuntime(provider.ID()),
		bucket:      newRateBucket(policy.MaxRPS, policy.Burst),
		sem:         make(chan struct{}, conc),
		queue:       make(chan *callReq, qc),
		attemptHook: hook,
	}
}

// start 启动 worker goroutines（数量 = MaxConcurrency，bounded，§40）。
func (e *executor) start(ctx context.Context) {
	if e.started {
		return
	}
	e.started = true
	for i := 0; i < e.policy.MaxConcurrency; i++ {
		go e.worker(ctx)
	}
}

// enqueue 有界入队（§40：预计 Deadline 无法处理 → 直接失败，不阻塞 Planner）。
func (e *executor) enqueue(c *callReq) error {
	select {
	case e.queue <- c:
		return nil
	default:
		return count.NewCountError(e.provider.ID(), count.ErrDeadline, "provider queue full", false)
	}
}

func (e *executor) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-e.queue:
			e.process(c)
		}
	}
}

// process 执行一次调用（准入 → 令牌 → 重试预算内调用）。
func (e *executor) process(c *callReq) {
	e.sem <- struct{}{}
	defer func() { <-e.sem }()

	// Deadline-aware：剩余预算过小则直接放弃网络调用（§40/§89）
	deadline := c.req.Deadline
	if dl, ok := c.ctx.Deadline(); ok && (deadline.IsZero() || dl.Before(deadline)) {
		deadline = dl
	}
	attempts := e.policy.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	lastClass := count.ErrUnknown
	for attempt := 0; attempt < attempts; attempt++ {
		if !deadline.IsZero() && time.Now().After(deadline) {
			lastErr = count.NewCountError(e.provider.ID(), count.ErrDeadline, "provider deadline elapsed", false)
			lastClass = count.ErrDeadline
			break
		}
		// 冷却等待（RATE_LIMIT/QUOTA 后）
		for !e.rt.waitAllowed(time.Now()) {
			if !deadline.IsZero() && time.Now().After(deadline) {
				lastErr = count.NewCountError(e.provider.ID(), count.ErrDeadline, "provider cooldown exceeded deadline", false)
				lastClass = count.ErrDeadline
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if lastErr != nil {
			break
		}
		// 令牌
		if err := e.bucket.acquire(c.ctx); err != nil {
			lastErr = count.NewCountError(e.provider.ID(), count.ErrDeadline, "rate token wait cancelled: "+err.Error(), false)
			lastClass = count.ErrDeadline
			break
		}
		callCtx := c.ctx
		if !deadline.IsZero() {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithDeadline(callCtx, deadline)
			callCtx = addCallTimeout(callCtx, e.policy.CallTimeout)
			res, cerr := e.doCall(callCtx, c.req)
			cancel()
			if cerr == nil {
				e.deliver(c, res, nil)
				return
			}
			lastErr = cerr
			lastClass = count.Class(cerr)
		} else if e.policy.CallTimeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(callCtx, e.policy.CallTimeout)
			res, cerr := e.doCall(callCtx, c.req)
			cancel()
			if cerr == nil {
				e.deliver(c, res, nil)
				return
			}
			lastErr = cerr
			lastClass = count.Class(cerr)
		} else {
			res, cerr := e.doCall(callCtx, c.req)
			if cerr == nil {
				e.deliver(c, res, nil)
				return
			}
			lastErr = cerr
			lastClass = count.Class(cerr)
		}
		// 错误处理（§56：429 只影响 Provider Runtime；AUTH 不 retry storm；QUOTA cooldown）
		switch lastClass {
		case count.ErrAuth, count.ErrQuota, count.ErrUnsupported, count.ErrSemanticsMismatch:
			if lastClass == count.ErrQuota {
				e.rt.setNextAllowed(time.Now().Add(e.cooldownFor(count.ErrQuota)), lastErr.Error())
			}
			if lastClass == count.ErrAuth {
				e.rt.setState("BLOCKED", lastErr.Error())
			}
			e.deliver(c, nil, lastErr)
			return
		case count.ErrRateLimit:
			e.rt.setNextAllowed(time.Now().Add(e.cooldownFor(count.ErrRateLimit)), lastErr.Error())
			continue // retry per budget（前台 0~1，§90）
		case count.ErrDeadline, count.ErrCancelled:
			e.deliver(c, nil, lastErr)
			return
		default:
			continue // timeout/5xx/connection: retry per budget
		}
	}
	e.deliver(c, nil, lastErr)
}

func (e *executor) cooldownFor(class count.ErrorClass) time.Duration {
	switch class {
	case count.ErrQuota:
		if e.policy.QuotaCooldown > 0 {
			return e.policy.QuotaCooldown
		}
		return time.Minute
	case count.ErrRateLimit:
		if e.policy.RateLimitCooldown > 0 {
			return e.policy.RateLimitCooldown
		}
		return 1800 * time.Millisecond
	}
	return 0
}

func (e *executor) doCall(ctx context.Context, req count.CountRequest) (*count.CountResult, error) {
	start := time.Now()
	res, err := e.provider.Count(ctx, req)
	latencyMs := time.Since(start).Milliseconds()
	class := count.Class(err)
	e.rt.record(err == nil, latencyMs, class)
	if e.attemptHook != nil {
		rec := attemptRec{
			providerID: e.provider.ID(), requestHash: req.RequestID,
			chainID: req.ChainID, dataset: string(req.Dataset),
			status: "error", errorClass: string(class), latencyMs: latencyMs,
		}
		if err == nil {
			rec.status = "ok"
			rec.count = res.Count
			rec.requestsCost = res.Cost.Requests
			rec.creditsCost = res.Cost.Credits
			rec.usdCost = res.Cost.USD
		} else {
			rec.requestsCost = 1
			rec.creditsCost = e.policy.CostPerRequestCredits
			rec.usdCost = e.policy.CostPerRequestUSD
		}
		e.attemptHook(rec)
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (e *executor) deliver(c *callReq, res *count.CountResult, err error) {
	select {
	case c.result <- callResult{res: res, err: err}:
	case <-c.ctx.Done():
	}
}

// runtimeState 供 Router 使用：是否可以路由（§24：Router 不能选 BLOCKED）。
func (e *executor) routable() (bool, string) {
	rt := e.rt.snapshot()
	switch rt.State {
	case "BLOCKED", "DISABLED":
		return false, rt.State
	case "THROTTLED", "DEGRADED", "AVAILABLE", "RECOVERING", "SHADOW", "":
		return true, rt.State
	}
	return true, rt.State
}

// inflight 当前活跃调用数。
func (e *executor) inflight() int64 {
	return e.active
}

// queueDepth 当前排队数。
func (e *executor) queueDepth() int { return len(e.queue) }

func (e *executor) snapshot() map[string]any {
	rt := e.rt.snapshot()
	return map[string]any{
		"provider_id":     e.provider.ID(),
		"state":           rt.State,
		"requests":        rt.Total,
		"success":         rt.Success,
		"success_rate":    rt.SuccessRate,
		"p50_ms":          rt.P50Ms,
		"p95_ms":          rt.P95Ms,
		"queue_depth":     len(e.queue),
		"active":          len(e.sem),
		"max_concurrency": e.policy.MaxConcurrency,
		"max_rps":         e.policy.MaxRPS,
		"last_error":      rt.LastError,
		"updated_at":      rt.LastErrorAt,
	}
}

func addCallTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if timeout <= 0 {
		return ctx
	}
	if dl, ok := ctx.Deadline(); ok {
		cut := time.Now().Add(timeout)
		if cut.Before(dl) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, cut)
			_ = cancel // deadline parent cancels on expiry anyway; child derives from parent
		}
		return ctx
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	_ = cancel
	return ctx
}

// inflightActive 当前正在处理（占用 sem）的 worker 数。
func (e *executor) inflightActive() int { return len(e.sem) }
