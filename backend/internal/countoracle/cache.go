// V1.3 Canonical Count Cache（§52/§54）：Provider-independent，key = canonical request hash。
package countoracle

import (
	"sync"
	"time"
)

type cacheEntry struct {
	Count        int64
	ProviderID   string
	Confidence   float64
	Calibration  float64
	LatencyMS    int64
	StoredAt     time.Time
	ExpiresAt    time.Time
	CostRequests int64
	Semantics    string
}

type countCache struct {
	mu    sync.Mutex
	items map[string]cacheEntry
}

func newCountCache() *countCache {
	return &countCache{items: map[string]cacheEntry{}}
}

func (c *countCache) get(key string, now time.Time) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || now.After(e.ExpiresAt) {
		return cacheEntry{}, false
	}
	return e, true
}

func (c *countCache) set(key string, e cacheEntry) {
	if e.ExpiresAt.IsZero() || !e.ExpiresAt.After(time.Now()) {
		return
	}
	c.mu.Lock()
	c.items[key] = e
	c.mu.Unlock()
}

func (c *countCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *countCache) clear() {
	c.mu.Lock()
	c.items = map[string]cacheEntry{}
	c.mu.Unlock()
}
