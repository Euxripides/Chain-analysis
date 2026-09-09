// V1.2 Chainbase Count Oracle：内存 Count Cache（§19/§20）。
// Key = chain+dataset+address+from_block+to_block；历史固定区间长 TTL，接近链头短 TTL。
package chainbase

import (
	"sync"
	"time"
)

type CacheKey struct {
	Chain   string
	Dataset string
	Address string
	From    uint64
	To      uint64
}

type cacheEntry struct {
	RawCount      int64
	Adjusted      int64
	Calibr        float64
	Confidence    float64
	StoredAt      time.Time
	ExpiresAt     time.Time
	ProbeRequests int
}

// CountCache 线程安全 TTL Cache（单进程语义：Count 只发生在 Worker）。
type CountCache struct {
	mu    sync.Mutex
	items map[CacheKey]cacheEntry
}

func NewCountCache() *CountCache {
	return &CountCache{items: map[CacheKey]cacheEntry{}}
}

func (c *CountCache) Get(key CacheKey, now time.Time) (int64, int64, float64, float64, bool, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || now.After(e.ExpiresAt) {
		return 0, 0, 0, 0, false, 0
	}
	return e.RawCount, e.Adjusted, e.Calibr, e.Confidence, true, e.ProbeRequests
}

func (c *CountCache) Set(key CacheKey, raw, adjusted int64, calibr, confidence float64, probeRequests int, ttl time.Duration, now time.Time) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.items[key] = cacheEntry{RawCount: raw, Adjusted: adjusted, Calibr: calibr, Confidence: confidence,
		StoredAt: now, ExpiresAt: now.Add(ttl), ProbeRequests: probeRequests}
	c.mu.Unlock()
}

func (c *CountCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *CountCache) Clear() {
	c.mu.Lock()
	c.items = map[CacheKey]cacheEntry{}
	c.mu.Unlock()
}
