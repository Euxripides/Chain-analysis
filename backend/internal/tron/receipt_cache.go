package tron

import (
	"container/list"
	"sync"
	"time"
)

type receiptCacheEntry struct {
	key       string
	receipt   ankrReceipt
	expiresAt time.Time
}

// trc20ReceiptCache is a bounded TTL/LRU performance cache. Durable state and
// correctness never depend on it, so losing it on restart is harmless.
type trc20ReceiptCache struct {
	mu         sync.Mutex
	maxEntries int
	ttl        time.Duration
	items      map[string]*list.Element
	lru        *list.List
}

func newTRC20ReceiptCache(maxEntries int, ttl time.Duration) *trc20ReceiptCache {
	if maxEntries < 1 {
		maxEntries = 1
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &trc20ReceiptCache{maxEntries: maxEntries, ttl: ttl, items: make(map[string]*list.Element), lru: list.New()}
}

func (c *trc20ReceiptCache) Get(key string) (ankrReceipt, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return ankrReceipt{}, false
	}
	entry := element.Value.(*receiptCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.remove(element)
		return ankrReceipt{}, false
	}
	c.lru.MoveToFront(element)
	return cloneAnkrReceipt(entry.receipt), true
}

func (c *trc20ReceiptCache) Add(key string, receipt ankrReceipt) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		entry := element.Value.(*receiptCacheEntry)
		entry.receipt, entry.expiresAt = cloneAnkrReceipt(receipt), time.Now().Add(c.ttl)
		c.lru.MoveToFront(element)
		return
	}
	entry := &receiptCacheEntry{key: key, receipt: cloneAnkrReceipt(receipt), expiresAt: time.Now().Add(c.ttl)}
	c.items[key] = c.lru.PushFront(entry)
	for c.lru.Len() > c.maxEntries {
		c.remove(c.lru.Back())
	}
}

func (c *trc20ReceiptCache) remove(element *list.Element) {
	if element == nil {
		return
	}
	delete(c.items, element.Value.(*receiptCacheEntry).key)
	c.lru.Remove(element)
}

func cloneAnkrReceipt(receipt ankrReceipt) ankrReceipt {
	copyReceipt := receipt
	copyReceipt.Logs = append([]ankrLog(nil), receipt.Logs...)
	for i := range copyReceipt.Logs {
		copyReceipt.Logs[i].Topics = append([]string(nil), receipt.Logs[i].Topics...)
	}
	return copyReceipt
}
