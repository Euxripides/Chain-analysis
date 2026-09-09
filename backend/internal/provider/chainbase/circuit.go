// V1.2 Chainbase Count Oracle：断路器（§17：circuit open → Fail Open，绝不阻塞下载）。
package chainbase

import (
	"sync"
	"time"
)

// circuitBreaker NORMAL → DEGRADED → OPEN → HALF_OPEN。
type circuitBreaker struct {
	mu         sync.Mutex
	state      string
	window     []bool
	pos        int
	full       bool
	openedAt   time.Time
	cooldown   time.Duration
	threshold  float64
	minSamples int
}

func newCircuitBreaker(windowSize int, threshold float64, cooldown time.Duration) *circuitBreaker {
	return &circuitBreaker{
		state: "NORMAL", window: make([]bool, windowSize),
		cooldown: cooldown, threshold: threshold, minSamples: windowSize / 2,
	}
}

func (cb *circuitBreaker) record(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.pos >= len(cb.window) {
		cb.pos = 0
		cb.full = true
	}
	cb.window[cb.pos] = success
	cb.pos++
	if cb.state == "OPEN" && time.Since(cb.openedAt) >= cb.cooldown {
		cb.state = "HALF_OPEN"
	}
}

func (cb *circuitBreaker) errorRate() float64 {
	cb.mu.Lock()
	n := cb.pos
	fail := 0
	for i := 0; i < n; i++ {
		if !cb.window[i] {
			fail++
		}
	}
	cb.mu.Unlock()
	if n < cb.minSamples {
		return 0
	}
	return float64(fail) / float64(n)
}

func (cb *circuitBreaker) allow() (bool, string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case "OPEN":
		if time.Since(cb.openedAt) >= cb.cooldown {
			cb.state = "HALF_OPEN"
			return true, cb.state
		}
		return false, cb.state
	default:
		return true, cb.state
	}
}

func (cb *circuitBreaker) stateStr() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *circuitBreaker) update() {
	er := cb.errorRate()
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == "OPEN" {
		return
	}
	if cb.state == "HALF_OPEN" {
		if er < cb.threshold {
			cb.state = "NORMAL"
		} else {
			cb.state = "OPEN"
			cb.openedAt = time.Now()
		}
		return
	}
	if er >= cb.threshold {
		cb.state = "OPEN"
		cb.openedAt = time.Now()
		return
	}
	if er >= cb.threshold/2 {
		cb.state = "DEGRADED"
		return
	}
	cb.state = "NORMAL"
}
