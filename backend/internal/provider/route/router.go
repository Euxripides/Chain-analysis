package route

import (
	"context"
	"sort"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// Name 返回 "route"：调度器看到的是分派层，而不是某条链的实现。
func (r *Registry) Name() string { return Name }

// Fetch 按 Resolve 结果把请求交给对应 Provider。
//
// 为什么不用 provider.Router 的"按链注册"：同一链的不同数据集可能走不同通道
// （Solana 历史走 Data API、余额/日志走 JSON-RPC），按链分派做不到这件事。
// 未命中或 Provider 未注册时回退 fallback；没有 fallback 则 fail closed，
// 绝不在未知路由下随便挑一个 Provider（那会取到别的链的数据）。
func (r *Registry) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	resolved, hit := r.Resolve(req.Chain, req.Dataset)
	if hit {
		if p, ok := r.Get(resolved.ProviderKey); ok {
			return p.Fetch(ctx, req)
		}
		r.logger().Warn("routed provider is not registered; falling back",
			"provider", resolved.ProviderKey, "chain", req.Chain, "dataset", req.Dataset,
			"policy_key", resolved.PolicyKey, "policy_revision", resolved.PolicyRevision)
	}

	r.mu.RLock()
	fallback := r.fallback
	r.mu.RUnlock()
	if fallback != nil {
		return fallback.Fetch(ctx, req)
	}

	message := "no route for " + req.Chain + "/" + req.Dataset
	if hit {
		message = "provider " + resolved.ProviderKey + " is not registered for " + req.Chain + "/" + req.Dataset
	}
	return provider.FetchResult{}, &provider.Error{
		Provider: Name, Class: provider.ClassUnsupported, Message: message,
	}
}

// Capabilities 返回 fallback 的能力集（它是平台默认取数面）；没有 fallback 时返回
// 已注册 Provider 数据集的并集。并集只用于元数据展示：链级能力仍由 capability_registry
// 表达，绝不用一份合并表冒充某条链的能力（与 provider.Router 同一口径）。
func (r *Registry) Capabilities() provider.Capabilities {
	r.mu.RLock()
	fallback := r.fallback
	registered := make([]RuntimeProvider, 0, len(r.providers))
	for _, rp := range r.providers {
		registered = append(registered, rp)
	}
	r.mu.RUnlock()

	if fallback != nil {
		return fallback.Capabilities()
	}
	caps := provider.Capabilities{Name: Name}
	seen := make(map[string]bool)
	for _, rp := range registered {
		if rp.Provider == nil {
			continue
		}
		providerCaps := rp.Provider.Capabilities()
		if providerCaps.MaxPageSize > caps.MaxPageSize {
			caps.MaxPageSize = providerCaps.MaxPageSize
		}
		for _, dataset := range providerCaps.Datasets {
			if dataset == "" || seen[dataset] {
				continue
			}
			seen[dataset] = true
			caps.Datasets = append(caps.Datasets, dataset)
		}
	}
	sort.Strings(caps.Datasets)
	return caps
}

// Health 返回 fallback 的健康度：调度器的并发/背压决策针对默认 Provider，
// 单个运行时 Provider 的健康度由它自己的错误分类与 runtime_states 表达。
// 没有 fallback 时按 NORMAL 上报（分派层本身没有可探测的远端）。
func (r *Registry) Health(ctx context.Context) provider.HealthStatus {
	r.mu.RLock()
	fallback := r.fallback
	r.mu.RUnlock()
	if fallback != nil {
		return fallback.Health(ctx)
	}
	return provider.HealthStatus{Provider: Name, State: provider.HealthNormal, LastCheck: time.Now().UTC()}
}
