package rpc

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- Provider Health（§67/§68）----
//
// 仅凭 HTTP 200 不能判定健康：还要看延迟分布、错误类型与链头落后。

const latencyWindow = 256

type providerStats struct {
	mu sync.Mutex

	requests    int64
	successes   int64
	timeouts    int64
	rateLimited int64
	serverError int64

	latencies [latencyWindow]float64
	pos       int
	filled    bool

	headBlock uint64
	headAt    time.Time
	lastError string
	lastCheck time.Time
}

// HealthTracker 汇总各 Provider/Chain 的健康画像。
type HealthTracker struct {
	mu    sync.RWMutex
	stats map[string]*providerStats
	heads map[string]uint64 // chain → 已知最高链头（用于计算 head lag）
}

func NewHealthTracker() *HealthTracker {
	return &HealthTracker{stats: map[string]*providerStats{}, heads: map[string]uint64{}}
}

func healthKey(provider, chainSlug string) string {
	return strings.ToLower(provider) + "|" + strings.ToLower(chainSlug)
}

func (t *HealthTracker) statsFor(provider, chainSlug string) *providerStats {
	key := healthKey(provider, chainSlug)
	t.mu.RLock()
	s, ok := t.stats[key]
	t.mu.RUnlock()
	if ok {
		return s
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok = t.stats[key]; ok {
		return s
	}
	s = &providerStats{}
	t.stats[key] = s
	return s
}

// Observe 记录一次调用结果。
func (t *HealthTracker) Observe(provider, chainSlug string, latency time.Duration, err *Error) {
	s := t.statsFor(provider, chainSlug)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	s.lastCheck = time.Now()
	s.latencies[s.pos] = float64(latency.Milliseconds())
	s.pos = (s.pos + 1) % latencyWindow
	if s.pos == 0 {
		s.filled = true
	}
	if providerRespondedHealthy(err) {
		s.successes++
		return
	}
	s.lastError = err.Public()
	switch err.Class {
	case ClassTimeout:
		s.timeouts++
	case ClassRateLimit:
		s.rateLimited++
	case ClassUpstream:
		s.serverError++
	}
}

// A well-formed rejection is evidence that the provider is reachable. Contract
// probes often revert because a selector is absent; that must not disable every
// eth_call on the chain. The original error is still returned and metered.
func providerRespondedHealthy(err *Error) bool {
	if err == nil {
		return true
	}
	switch err.Class {
	case ClassExecutionReverted, ClassInvalidParams, ClassMethodUnsupported,
		ClassTraceUnsupported, ClassArchiveRequired, ClassRequestTooLarge, ClassRangeTooLarge:
		return true
	default:
		return false
	}
}

// SetHead 记录 Provider 观测到的链头，同时维护全链已知最高链头（§68）。
func (t *HealthTracker) SetHead(provider, chainSlug string, head uint64) {
	s := t.statsFor(provider, chainSlug)
	s.mu.Lock()
	s.headBlock = head
	s.headAt = time.Now()
	s.mu.Unlock()

	key := strings.ToLower(chainSlug)
	t.mu.Lock()
	if head > t.heads[key] {
		t.heads[key] = head
	}
	best := t.heads[key]
	t.mu.Unlock()

	lag := uint64(0)
	if best > head {
		lag = best - head
	}
	ProviderHeadLag.WithLabelValues(provider, chainSlug).Set(float64(lag))
}

// Snapshot 单个 Provider/Chain 的健康画像。
func (t *HealthTracker) Snapshot(provider, chainSlug string, state CircuitState, inFlight int) ProviderHealth {
	s := t.statsFor(provider, chainSlug)
	s.mu.Lock()
	defer s.mu.Unlock()

	count := latencyWindow
	if !s.filled {
		count = s.pos
	}
	samples := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		samples = append(samples, s.latencies[i])
	}
	sort.Float64s(samples)

	health := ProviderHealth{
		Provider: provider, Chain: chainSlug, State: state,
		Requests: s.requests, RateLimited: s.rateLimited, ServerError: s.serverError,
		InFlight: inFlight, HeadBlock: s.headBlock, LastError: s.lastError, LastCheck: s.lastCheck,
		P50Ms: percentile(samples, 0.50), P95Ms: percentile(samples, 0.95), P99Ms: percentile(samples, 0.99),
	}
	if s.requests > 0 {
		health.SuccessRate = float64(s.successes) / float64(s.requests)
		health.TimeoutRate = float64(s.timeouts) / float64(s.requests)
	}
	t.mu.RLock()
	best := t.heads[strings.ToLower(chainSlug)]
	t.mu.RUnlock()
	if best > s.headBlock {
		health.HeadLag = best - s.headBlock
	}
	return health
}

// Score 健康分：Router 排序用（越大越好）。
func Score(h ProviderHealth) float64 {
	score := h.SuccessRate * 100
	switch h.State {
	case CircuitOpen:
		score -= 1000
	case CircuitHalfOpen:
		score -= 50
	case CircuitDegraded:
		score -= 20
	}
	// 落后链头的 Provider 不能仅凭 200 判为健康（§68）
	score -= float64(minUint64(h.HeadLag, 500)) / 5
	score -= h.P95Ms / 100
	return score
}

func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * q)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
