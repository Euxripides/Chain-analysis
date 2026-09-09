package tron

import (
	"testing"
	"time"
)

func TestTRC20ReceiptCacheIsBoundedAndExpires(t *testing.T) {
	cache := newTRC20ReceiptCache(2, 15*time.Millisecond)
	cache.Add("a", ankrReceipt{TransactionHash: "a"})
	cache.Add("b", ankrReceipt{TransactionHash: "b"})
	if _, ok := cache.Get("a"); !ok {
		t.Fatal("expected cache hit")
	}
	cache.Add("c", ankrReceipt{TransactionHash: "c"})
	if _, ok := cache.Get("b"); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if cache.lru.Len() != 2 {
		t.Fatalf("cache length = %d", cache.lru.Len())
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := cache.Get("a"); ok {
		t.Fatal("expired entry was returned")
	}
}
