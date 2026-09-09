package tron

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

type receiptSample struct {
	at      time.Time
	latency time.Duration
	class   provider.ErrorClass
	status  int
	retry   bool
}

// receiptController is a process-wide, bounded adaptive gate. It deliberately
// does not share TronGrid pacing: only Ankr receipt enrichment enters it.
type receiptController struct {
	mu         sync.Mutex
	active     int
	waiting    int
	target     int
	floor      int
	maxTarget  int
	hardMax    int
	adaptive   bool
	lastAdjust time.Time
	samples    []receiptSample
	notify     chan struct{}
}

func newReceiptController(initial, target, hardMax int, adaptive bool) *receiptController {
	if initial < 1 {
		initial = 4
	}
	if target < initial {
		target = initial
	}
	if hardMax < target {
		hardMax = target
	}
	c := &receiptController{target: initial, floor: 4, maxTarget: target, hardMax: hardMax, adaptive: adaptive, lastAdjust: time.Now(), notify: make(chan struct{}, 1)}
	c.publish()
	return c
}

func (c *receiptController) acquire(ctx context.Context) error {
	c.mu.Lock()
	c.waiting++
	c.publishLocked()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.waiting--
		c.publishLocked()
		c.mu.Unlock()
	}()
	for {
		c.mu.Lock()
		if c.active < c.target {
			c.active++
			c.publishLocked()
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.notify:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (c *receiptController) release() {
	c.mu.Lock()
	if c.active > 0 {
		c.active--
	}
	c.publishLocked()
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *receiptController) observe(latency time.Duration, status int, class provider.ErrorClass, retry bool) {
	c.mu.Lock()
	now := time.Now()
	c.samples = append(c.samples, receiptSample{at: now, latency: latency, status: status, class: class, retry: retry})
	cutoff := now.Add(-time.Minute)
	first := 0
	for first < len(c.samples) && c.samples[first].at.Before(cutoff) {
		first++
	}
	if first > 0 {
		c.samples = append([]receiptSample(nil), c.samples[first:]...)
	}
	if c.adaptive && now.Sub(c.lastAdjust) >= 30*time.Second && len(c.samples) >= 20 {
		latencies := make([]time.Duration, 0, len(c.samples))
		bad, rateLimited, retried := 0, 0, 0
		for _, s := range c.samples {
			latencies = append(latencies, s.latency)
			if s.status == 429 {
				rateLimited++
			}
			if s.status >= 500 || s.class == provider.ClassRequestTimeout || s.class == provider.ClassOverallTimeout {
				bad++
			}
			if s.retry {
				retried++
			}
		}
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p95 := latencies[(len(latencies)-1)*95/100]
		total := float64(len(c.samples))
		if rateLimited > 0 || float64(bad)/total >= .01 || float64(retried)/total >= .01 || p95 > 3*time.Second {
			c.target /= 2
			if c.target < c.floor {
				c.target = c.floor
			}
		} else if c.waiting > 0 && p95 < 2*time.Second && c.target < c.maxTarget {
			c.target += 2
			if c.target > c.maxTarget {
				c.target = c.maxTarget
			}
		}
		c.lastAdjust = now
	}
	c.publishLocked()
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *receiptController) workerLimit() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hardMax
}

func (c *receiptController) publish() { c.mu.Lock(); c.publishLocked(); c.mu.Unlock() }
func (c *receiptController) publishLocked() {
	observability.TronReceiptActiveWorkers.Set(float64(c.active))
	observability.TronReceiptTargetWorkers.Set(float64(c.target))
	observability.TronReceiptQueueDepth.Set(float64(c.waiting))
	observability.TronReceiptQueueCapacity.Set(float64(c.hardMax))
}
