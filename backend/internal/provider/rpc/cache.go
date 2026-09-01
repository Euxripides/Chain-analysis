package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- Cache（§47-§55）----
//
// 不同方法缓存语义不同，禁止统一 TTL。
// Key 必须包含 chain/method/规范化参数/区块引用，且 latest 与固定区块必须区分（§52/§247-16）。

// CacheKey 生成缓存键（§58）。
func CacheKey(req Request) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(req.Chain))
	b.WriteByte('|')
	b.WriteString(req.Method)
	b.WriteByte('|')
	b.WriteString(req.BlockRef.String())
	b.WriteByte('|')
	// encoding/json 对 map key 排序，参数由内部服务构造，序列化是确定性的
	if raw, err := json.Marshal(req.Params); err == nil {
		b.Write(raw)
	}
	key := b.String()
	if len(key) <= 200 {
		return key
	}
	// 超长参数（例如大 calldata）使用摘要，避免缓存键膨胀
	sum := sha256.Sum256([]byte(key))
	return strings.ToLower(req.Chain) + "|" + req.Method + "|" + req.BlockRef.String() + "|sha256:" + hex.EncodeToString(sum[:])
}

type cacheEntry struct {
	response  Response
	expiresAt time.Time
}

// Cache 进程内 TTL 缓存。多实例共享缓存留待后续（§20）。
type Cache struct {
	mu       sync.RWMutex
	entries  map[string]cacheEntry
	capacity int
	enabled  bool
}

func NewCache(capacity int, enabled bool) *Cache {
	if capacity <= 0 {
		capacity = 100000
	}
	return &Cache{entries: make(map[string]cacheEntry, 1024), capacity: capacity, enabled: enabled}
}

func (c *Cache) Enabled() bool { return c != nil && c.enabled }

func (c *Cache) Get(key string) (Response, bool) {
	if !c.Enabled() {
		return Response{}, false
	}
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return Response{}, false
	}
	return entry.response, true
}

func (c *Cache) Put(key string, response Response, ttl time.Duration) {
	if !c.Enabled() || ttl <= 0 {
		return
	}
	// 缓存的是结果本身，命中标记由读取方设置
	response.CacheHit = false
	response.SingleflightHit = false
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.capacity {
		c.evictLocked()
	}
	c.entries[key] = cacheEntry{response: response, expiresAt: time.Now().Add(ttl)}
}

func (c *Cache) Delete(key string) {
	if !c.Enabled() {
		return
	}
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// evictLocked 先清过期，再按到期时间淘汰最早的一成。
func (c *Cache) evictLocked() {
	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) < c.capacity {
		return
	}
	type item struct {
		key       string
		expiresAt time.Time
	}
	items := make([]item, 0, len(c.entries))
	for key, entry := range c.entries {
		items = append(items, item{key: key, expiresAt: entry.expiresAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].expiresAt.Before(items[j].expiresAt) })
	drop := maxInt(1, len(items)/10)
	for i := 0; i < drop && i < len(items); i++ {
		delete(c.entries, items[i].key)
	}
}
