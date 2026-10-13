package api

import (
	"context"
	"errors"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type explorerProbeEntry struct {
	Found bool
	Until time.Time
}
type explorerProbeCache struct {
	mu      sync.Mutex
	entries map[string]explorerProbeEntry
}

func (c *explorerProbeCache) get(key string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.entries[key]
	if !ok || !time.Now().Before(v.Until) {
		delete(c.entries, key)
		return false, false
	}
	return v.Found, true
}
func (c *explorerProbeCache) put(key string, found bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]explorerProbeEntry{}
	}
	if len(c.entries) >= 1024 {
		for k, v := range c.entries {
			if time.Now().After(v.Until) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= 1024 {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
	}
	ttl := 15 * time.Second
	if found {
		ttl = 30 * time.Second
	}
	c.entries[key] = explorerProbeEntry{Found: found, Until: time.Now().Add(ttl)}
}

func respondLookupFailure(w http.ResponseWriter, r *http.Request, err error, label string) {
	status, code, message := http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE", label+"暂不可用，请稍后重试"
	var rpcErr *rpc.Error
	if errors.Is(err, context.DeadlineExceeded) {
		status, code, message = 504, "RPC_TIMEOUT", label+"请求超时，请稍后重试"
	}
	if errors.As(err, &rpcErr) {
		switch rpcErr.Class {
		case rpc.ClassRateLimit:
			status, code, message = 429, "RPC_RATE_LIMIT", label+"请求达到限流，请稍后重试"
			if rpcErr.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(rpcErr.RetryAfter/time.Second)+1))
			}
		case rpc.ClassTimeout:
			status, code, message = 504, "RPC_TIMEOUT", label+"请求超时，请稍后重试"
		case rpc.ClassBudgetExceeded:
			status, code, message = 429, "RPC_BUDGET_EXCEEDED", "查询额度已达到当前限制"
		}
	}
	respondError(w, r, status, code, message, nil)
}
