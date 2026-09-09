// V1.3 MockParallelProvider（§84-§87/§31：证明 Count Oracle Port 没有全局串行化）。
//
// 特性：
//   - 模拟"高并发真实 Count API"：MaxConcurrency=100、Latency=200ms、Success≈100%
//   - 内部 enforce 并发上限：超过 MaxConcurrency 的请求直接 RATE_LIMIT（模拟 API 服务端行为）
//   - 计数确定性：count = base + (sha256(subject+range) % 997)（同一请求永远同值，支持缓存/单飞断言）
//   - 记录 CountCalls / MaxObservedInflight / Inflight，供验收断言（§85/§87）
package mockparallel

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider/count"
)

// Options MockParallel 配置。
type Options struct {
	// ID 注册名（默认 mockparallel；隔离测试可注册第二个实例如 mockslow）。
	ID string

	// MaxConcurrency 模拟 API 并发上限（默认 100）。
	MaxConcurrency int
	// Latency 单请求模拟延迟（默认 200ms）。
	Latency time.Duration
	// CountBase 返回 count 基数（默认 1000）。
	CountBase int64
	// FailRate 0~1：随机失败比例（默认 0）。用于失败路径测试。
	FailRate float64
	// RateLimitRate 0~1：随机 429 比例（默认 0）。
	RateLimitRate float64
}

// Adapter MockParallel CountProvider。
type Adapter struct {
	opts Options

	inflight   atomic.Int64
	maxInfl    atomic.Int64
	calls      atomic.Int64
	okCalls    atomic.Int64
	failCalls  atomic.Int64
	rlCalls    atomic.Int64
	latencySum atomic.Int64 // ns

	mu      sync.Mutex
	state   string
	message string
	upd     time.Time

	seq atomic.Uint64
}

// New 构造 Adapter（未指定选项给默认值）。
func New(opts Options) *Adapter {
	if opts.MaxConcurrency <= 0 {
		opts.MaxConcurrency = 100
	}
	if opts.Latency <= 0 {
		opts.Latency = 200 * time.Millisecond
	}
	if opts.CountBase <= 0 {
		opts.CountBase = 1000
	}
	return &Adapter{opts: opts, state: "AVAILABLE", message: "ok", upd: time.Now()}
}

func (a *Adapter) ID() string {
	if a.opts.ID != "" {
		return a.opts.ID
	}
	return "mockparallel"
}

// Capabilities 静态+运行时能力：BSC/ETH 任意 chain（chain 0 = 部署矩阵决定），
// 支持 Range（RangeVerified=true，语义即 [start,end) 区间行数）。
func (a *Adapter) Capabilities(ctx context.Context) count.ProviderCapabilities {
	base := count.DatasetCapability{
		Enabled: true, SupportsRange: true, RangeVerified: true,
		Semantics: count.SemanticsTotalRows, Accuracy: count.AccuracyExact,
		RuntimeStatus: count.StatusAvailable,
	}
	ds := count.DatasetCapability{Enabled: true, SupportsRange: true, RangeVerified: true,
		Semantics: count.SemanticsTotalRows, Accuracy: count.AccuracyExact,
		RuntimeStatus: count.StatusAvailable}
	_ = ds
	return count.ProviderCapabilities{
		ProviderID: "mockparallel",
		Chains:     nil, // 空 = 部署矩阵决定（测试/影子环境全 chain 可用）
		Datasets: map[count.DatasetKey]count.DatasetCapability{
			{ChainID: 0, Dataset: count.DatasetTransactions}:   base,
			{ChainID: 0, Dataset: count.DatasetTokenTransfers}: base,
			{ChainID: 0, Dataset: count.DatasetLogs}:           base,
		},
		SupportsBatch:        false,
		SupportsMultiDataset: false,
		RateLimits: []count.RateBucketPolicy{
			{Scope: count.ScopeGlobal, MaxConcurrency: a.opts.MaxConcurrency},
		},
	}
}

// Count 模拟真实 Count API：并发准入（精确上限）+ 延迟 + 确定性 count。
func (a *Adapter) Count(ctx context.Context, req count.CountRequest) (count.CountResult, error) {
	a.calls.Add(1)
	// CAS 精确并发准入：超过 MaxConcurrency 直接 RATE_LIMIT（模拟 API 服务端并发上限）
	for {
		cur := a.inflight.Load()
		if cur >= int64(a.opts.MaxConcurrency) {
			a.rlCalls.Add(1)
			a.failCalls.Add(1)
			return count.CountResult{}, count.RateLimit(a.ID(), "mock api concurrency limit exceeded")
		}
		if a.inflight.CompareAndSwap(cur, cur+1) {
			a.trackMax(cur + 1)
			break
		}
	}
	defer a.inflight.Add(-1)
	start := time.Now()
	// 模拟服务端延迟（ctx 取消则立即返回）
	select {
	case <-ctx.Done():
		a.failCalls.Add(1)
		return count.CountResult{}, count.Timeout(a.ID(), "mock api deadline: "+ctx.Err().Error())
	case <-time.After(a.opts.Latency):
	}
	// 随机失败注入（用于 Fail-Open / retry 测试）
	if a.opts.FailRate > 0 {
		if float64(a.seq.Add(1)%1000)/1000 < a.opts.FailRate {
			a.failCalls.Add(1)
			return count.CountResult{}, count.Provider5xx(a.ID(), "mock 5xx")
		}
	}
	if a.opts.RateLimitRate > 0 {
		if float64(a.seq.Add(1)%1000)/1000 < a.opts.RateLimitRate {
			a.rlCalls.Add(1)
			a.failCalls.Add(1)
			return count.CountResult{}, count.RateLimit(a.ID(), "mock 429")
		}
	}
	elapsed := time.Since(start)
	a.latencySum.Add(int64(elapsed))
	a.okCalls.Add(1)
	c := a.opts.CountBase + int64(hashOf(req)%997)
	return count.CountResult{
		RequestID:    req.RequestID,
		ProviderID:   "mockparallel",
		ChainID:      req.ChainID,
		Dataset:      req.Dataset,
		Subject:      req.Subject,
		Range:        req.Range,
		Count:        c,
		Semantics:    count.SemanticsTotalRows,
		Accuracy:     count.AccuracyExact,
		Confidence:   0.99,
		Source:       count.SourceLiveProvider,
		ObservedAt:   time.Now(),
		Latency:      elapsed,
		Cost:         count.CountCost{Requests: 1, Credits: 1, USD: 0},
		ProviderMeta: count.ProviderMeta{Fields: map[string]string{"mock": "parallel"}},
	}, nil
}

// Health 快照。
func (a *Adapter) Health(ctx context.Context) count.ProviderHealth {
	a.mu.Lock()
	defer a.mu.Unlock()
	return count.ProviderHealth{
		ProviderID: "mockparallel",
		State:      a.state,
		Healthy:    a.state == "AVAILABLE",
		Message:    a.message,
		UpdatedAt:  a.upd,
	}
}

// setState 供测试注入状态。
func (a *Adapter) setState(state, msg string) {
	a.mu.Lock()
	a.state = state
	a.message = msg
	a.upd = time.Now()
	a.mu.Unlock()
}

// Stats 验收统计（§85/§87/§88）。
type Stats struct {
	Calls               int64
	OKCalls             int64
	FailCalls           int64
	RateLimitedCalls    int64
	Inflight            int64
	MaxObservedInflight int64
	AvgLatency          time.Duration
}

func (a *Adapter) Stats() Stats {
	calls := a.calls.Load()
	var avg time.Duration
	if a.okCalls.Load() > 0 {
		avg = time.Duration(a.latencySum.Load() / a.okCalls.Load())
	}
	return Stats{
		Calls: calls, OKCalls: a.okCalls.Load(), FailCalls: a.failCalls.Load(),
		RateLimitedCalls: a.rlCalls.Load(), Inflight: a.inflight.Load(),
		MaxObservedInflight: a.maxInfl.Load(), AvgLatency: avg,
	}
}

func (a *Adapter) trackMax(v int64) {
	for {
		old := a.maxInfl.Load()
		if v <= old || a.maxInfl.CompareAndSwap(old, v) {
			return
		}
	}
}

func (a *Adapter) resetStats() {
	a.calls.Store(0)
	a.okCalls.Store(0)
	a.failCalls.Store(0)
	a.rlCalls.Store(0)
	a.inflight.Store(0)
	a.maxInfl.Store(0)
	a.latencySum.Store(0)
}

func hashOf(req count.CountRequest) uint64 {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%s|%s|%d|%d|%d", req.ChainID, req.Dataset, req.Subject.Type, req.Subject.Value, req.Range.Start, req.Range.End, req.QueryShapeVersion)
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}
