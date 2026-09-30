package ankrrpc

import (
	"context"
	"time"

	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// Health Provider 自述健康状态。
// 权威健康画像由 Gateway 的 HealthTracker 维护（含延迟分位与 head lag，§67/§68）。
func (c *Client) Health(ctx context.Context, chainSlug string) rpc.ProviderHealth {
	if c.opts.Runtime != nil {
		_, _, enabled, err := c.opts.Runtime(ctx)
		if err != nil || !enabled {
			return rpc.ProviderHealth{Provider: c.Name(), Chain: chainSlug, State: rpc.CircuitOpen, LastCheck: time.Now(), LastError: "provider configuration unavailable or disabled"}
		}
	}
	requests := c.requests.Load()
	failures := c.failures.Load()
	health := rpc.ProviderHealth{
		Provider: c.Name(), Chain: chainSlug, State: rpc.CircuitClosed,
		Requests: requests, LastCheck: time.Now(),
	}
	if total := requests + failures; total > 0 {
		health.SuccessRate = float64(requests) / float64(total)
	}
	if message, ok := c.lastErr.Load().(string); ok {
		health.LastError = message
	}
	return health
}

var _ rpc.Provider = (*Client)(nil)
