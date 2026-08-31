package ankr

import (
	"context"
	"sync"
	"time"
)

// TokenBucket 软件令牌桶：Provider 全局限流（不按用户各自限流）。
type TokenBucket struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

func NewTokenBucket(rpm int) *TokenBucket {
	if rpm <= 0 {
		rpm = 900
	}
	rate := float64(rpm) / 60.0
	return &TokenBucket{rate: rate, burst: rate, tokens: rate, last: time.Now()}
}

func (b *TokenBucket) refill(now time.Time) {
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
}

// Acquire 阻塞直到取得令牌或 ctx 取消。
func (b *TokenBucket) Acquire(ctx context.Context) error {
	for {
		b.mu.Lock()
		now := time.Now()
		b.refill(now)
		if b.tokens >= 1 {
			b.tokens -= 1
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
