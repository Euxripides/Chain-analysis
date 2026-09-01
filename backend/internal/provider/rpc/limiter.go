package rpc

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---- 全局限流（§32-§35）----
//
// 关键：Rate Controller 与 Concurrency Controller 必须分离。
// 429/-32090 降速率；连接数限制降在途并发。二者不能混成一个变量（§35/§60/§61）。

// LimiterKey 限流维度：provider + credential + chain + method_class（§34）。
type LimiterKey struct {
	Provider   string
	Credential string
	Chain      string
	Class      MethodClass
}

func (k LimiterKey) String() string {
	return strings.Join([]string{k.Provider, k.Credential, k.Chain, string(k.Class)}, "|")
}

// ProviderKey 并发维度只按 provider + credential + chain（连接数是 Provider 级别资源）。
func (k LimiterKey) ProviderKey() string {
	return strings.Join([]string{k.Provider, k.Credential, k.Chain}, "|")
}

// RateController 令牌桶速率控制。所有业务共享同一实例，
// 避免 TokenMeta/Balance/Trace 各自限流叠加超过 Provider 限制（§33）。
type RateController struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	baseRPS float64
	minRPS  float64
}

type rateBucket struct {
	rate      float64
	tokens    float64
	last      time.Time
	penalized time.Time
	successes int
}

func NewRateController(requestsPerSecond float64) *RateController {
	if requestsPerSecond <= 0 {
		requestsPerSecond = 25
	}
	return &RateController{
		buckets: map[string]*rateBucket{},
		baseRPS: requestsPerSecond,
		minRPS:  maxFloat(1, requestsPerSecond/16),
	}
}

func (c *RateController) bucket(key string) *rateBucket {
	b, ok := c.buckets[key]
	if !ok {
		b = &rateBucket{rate: c.baseRPS, tokens: c.baseRPS, last: time.Now()}
		c.buckets[key] = b
	}
	return b
}

// Acquire 阻塞直到取得令牌或 ctx 结束。
func (c *RateController) Acquire(ctx context.Context, key LimiterKey) error {
	name := key.String()
	for {
		c.mu.Lock()
		b := c.bucket(name)
		now := time.Now()
		elapsed := now.Sub(b.last).Seconds()
		b.tokens += elapsed * b.rate
		if b.tokens > b.rate {
			b.tokens = b.rate
		}
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			c.mu.Unlock()
			return nil
		}
		wait := time.Duration((1-b.tokens)/b.rate*float64(time.Second)) + time.Millisecond
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return &Error{Class: ClassCancelled, Message: "rate limiter wait cancelled", Cause: ctx.Err()}
		case <-time.After(wait):
		}
	}
}

// OnRateLimited 429 / -32090：立即降速并遵守 Retry-After（§60）。
func (c *RateController) OnRateLimited(key LimiterKey, retryAfter time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.bucket(key.String())
	b.rate = maxFloat(c.minRPS, b.rate/2)
	b.successes = 0
	b.penalized = time.Now()
	if retryAfter > 0 {
		// 惩罚期内不给令牌
		b.tokens = -retryAfter.Seconds() * b.rate
	}
}

// OnSuccess 连续成功后缓慢恢复速率（加性恢复，避免抖动）。
func (c *RateController) OnSuccess(key LimiterKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.bucket(key.String())
	if b.rate >= c.baseRPS {
		return
	}
	b.successes++
	if b.successes >= 20 && time.Since(b.penalized) > 5*time.Second {
		b.rate = minFloat(c.baseRPS, b.rate+maxFloat(1, c.baseRPS/10))
		b.successes = 0
	}
}

// Rate 当前速率（观测用）。
func (c *RateController) Rate(key LimiterKey) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bucket(key.String()).rate
}

// ConcurrencyController 在途请求控制（§35/§61）。
type ConcurrencyController struct {
	mu   sync.Mutex
	sems map[string]*dynSem
	max  int
	min  int
}

func NewConcurrencyController(max int) *ConcurrencyController {
	if max <= 0 {
		max = 64
	}
	return &ConcurrencyController{sems: map[string]*dynSem{}, max: max, min: maxInt(1, max/16)}
}

func (c *ConcurrencyController) sem(key string) *dynSem {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sems[key]
	if !ok {
		s = newDynSem(c.max, c.min)
		c.sems[key] = s
	}
	return s
}

// Acquire 取得一个在途名额，返回释放函数。
func (c *ConcurrencyController) Acquire(ctx context.Context, key LimiterKey) (func(), error) {
	s := c.sem(key.ProviderKey())
	if err := s.acquire(ctx); err != nil {
		return func() {}, err
	}
	var once sync.Once
	return func() { once.Do(s.release) }, nil
}

// Reduce 连接数限制：降低在途并发，而不是降低 page/range（§61/§221）。
func (c *ConcurrencyController) Reduce(key LimiterKey) { c.sem(key.ProviderKey()).reduce() }

// Grow 稳定后缓慢恢复。
func (c *ConcurrencyController) Grow(key LimiterKey) { c.sem(key.ProviderKey()).grow() }

// InFlight 当前在途数（观测/健康画像用）。
func (c *ConcurrencyController) InFlight(key LimiterKey) int {
	return int(c.sem(key.ProviderKey()).inflight.Load())
}

// Limit 当前并发上限。
func (c *ConcurrencyController) Limit(key LimiterKey) int { return c.sem(key.ProviderKey()).limit() }

// dynSem 可动态收缩的信号量：以"扣留令牌"的方式降低上限，避免中断在途请求。
type dynSem struct {
	tokens   chan struct{}
	mu       sync.Mutex
	parked   int
	debt     int
	max      int
	min      int
	inflight atomic.Int64
}

func newDynSem(max, min int) *dynSem {
	if min < 1 {
		min = 1
	}
	s := &dynSem{tokens: make(chan struct{}, max), max: max, min: min}
	for i := 0; i < max; i++ {
		s.tokens <- struct{}{}
	}
	return s
}

func (s *dynSem) acquire(ctx context.Context) error {
	select {
	case <-s.tokens:
		s.inflight.Add(1)
		return nil
	default:
	}
	select {
	case <-s.tokens:
		s.inflight.Add(1)
		return nil
	case <-ctx.Done():
		return &Error{Class: ClassCancelled, Message: "concurrency wait cancelled", Cause: ctx.Err()}
	}
}

func (s *dynSem) release() {
	s.inflight.Add(-1)
	s.mu.Lock()
	if s.debt > 0 && s.max-s.parked-s.debt >= s.min {
		s.debt--
		s.parked++
		s.mu.Unlock()
		return // 令牌被扣留，不归还
	}
	s.mu.Unlock()
	select {
	case s.tokens <- struct{}{}:
	default:
	}
}

func (s *dynSem) reduce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.max - s.parked - s.debt
	target := maxInt(s.min, current/2)
	for i := 0; i < current-target; i++ {
		select {
		case <-s.tokens:
			s.parked++
		default:
			s.debt++
		}
	}
}

func (s *dynSem) grow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.debt > 0:
		s.debt--
	case s.parked > 0:
		s.parked--
		select {
		case s.tokens <- struct{}{}:
		default:
			s.parked++
		}
	}
}

func (s *dynSem) limit() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.max - s.parked - s.debt
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
