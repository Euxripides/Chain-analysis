package rpc

import (
	"strings"
	"sync"
	"time"
)

// Breakers 熔断器集合（§65/§66）。
//
// 维度必须是 provider + chain + method_class：
// Ankr BSC Trace 故障不得连带熔断 Ankr BSC eth_blockNumber。
type Breakers struct {
	mu        sync.Mutex
	breakers  map[string]*breaker
	window    int
	threshold float64
	cooldown  time.Duration
}

func NewBreakers(window int, threshold float64, cooldown time.Duration) *Breakers {
	if window <= 0 {
		window = 40
	}
	if threshold <= 0 {
		threshold = 0.5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &Breakers{breakers: map[string]*breaker{}, window: window, threshold: threshold, cooldown: cooldown}
}

// BreakerKey provider + chain + method class。
func BreakerKey(provider, chainSlug string, class MethodClass) string {
	return strings.Join([]string{provider, chainSlug, string(class)}, "|")
}

func (b *Breakers) get(key string) *breaker {
	b.mu.Lock()
	defer b.mu.Unlock()
	cb, ok := b.breakers[key]
	if !ok {
		cb = &breaker{state: CircuitClosed, window: make([]bool, b.window),
			threshold: b.threshold, cooldown: b.cooldown, minSamples: maxInt(4, b.window/4)}
		b.breakers[key] = cb
	}
	return cb
}

// Allow 是否允许发起请求。
func (b *Breakers) Allow(key string) (bool, CircuitState) { return b.get(key).allow() }

// Record 记录一次调用结果并更新状态。
func (b *Breakers) Record(key string, success bool) { b.get(key).record(success) }

// State 当前状态。
func (b *Breakers) State(key string) CircuitState { return b.get(key).currentState() }

// ErrorRate 当前窗口错误率。
func (b *Breakers) ErrorRate(key string) float64 { return b.get(key).errorRate() }

// States 全部熔断器状态快照（Admin Dashboard 用，§144）。
func (b *Breakers) States() map[string]CircuitState {
	b.mu.Lock()
	keys := make([]string, 0, len(b.breakers))
	for key := range b.breakers {
		keys = append(keys, key)
	}
	b.mu.Unlock()
	out := make(map[string]CircuitState, len(keys))
	for _, key := range keys {
		out[key] = b.State(key)
	}
	return out
}

type breaker struct {
	mu         sync.Mutex
	state      CircuitState
	window     []bool
	pos        int
	count      int
	openedAt   time.Time
	threshold  float64
	cooldown   time.Duration
	minSamples int
}

func (c *breaker) allow() (bool, CircuitState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == CircuitOpen {
		if time.Since(c.openedAt) >= c.cooldown {
			c.state = CircuitHalfOpen
			return true, c.state
		}
		return false, c.state
	}
	return true, c.state
}

func (c *breaker) record(success bool) {
	c.mu.Lock()
	c.window[c.pos] = success
	c.pos = (c.pos + 1) % len(c.window)
	if c.count < len(c.window) {
		c.count++
	}
	rate := c.rateLocked()
	switch c.state {
	case CircuitHalfOpen:
		if success {
			c.state = CircuitClosed
		} else {
			c.state = CircuitOpen
			c.openedAt = time.Now()
		}
	case CircuitOpen:
		if time.Since(c.openedAt) >= c.cooldown {
			c.state = CircuitHalfOpen
		}
	default:
		switch {
		case c.count >= c.minSamples && rate >= c.threshold:
			c.state = CircuitOpen
			c.openedAt = time.Now()
		case c.count >= c.minSamples && rate >= c.threshold/2:
			c.state = CircuitDegraded
		default:
			c.state = CircuitClosed
		}
	}
	c.mu.Unlock()
}

func (c *breaker) rateLocked() float64 {
	if c.count == 0 {
		return 0
	}
	failures := 0
	for i := 0; i < c.count; i++ {
		if !c.window[i] {
			failures++
		}
	}
	return float64(failures) / float64(c.count)
}

func (c *breaker) errorRate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rateLocked()
}

func (c *breaker) currentState() CircuitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == CircuitOpen && time.Since(c.openedAt) >= c.cooldown {
		return CircuitHalfOpen
	}
	return c.state
}
