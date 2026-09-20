package provider

import (
	"context"
	"strings"
	"time"
)

// Router 按 chain 把 Fetch 分派给该链专属 Provider，未登记的链回落到 fallback。
//
// 存在的理由：Scheduler 只接受单个 Provider（历史设计里 Ankr 是唯一来源），
// 而 Solana 必须走标准 Solana JSON-RPC（Ankr Advanced API 明确拒绝 solana，
// 见 internal/provider/solana 的包注释）。用一层薄路由避免把 chain == "solana"
// 分支散落进下载引擎。
type Router struct {
	fallback Provider
	byChain  map[string]Provider
}

// NewRouter 构造路由；fallback 为 nil 时只有显式登记的链可用（fail closed）。
func NewRouter(fallback Provider, byChain map[string]Provider) *Router {
	normalized := make(map[string]Provider, len(byChain))
	for chain, p := range byChain {
		if p == nil {
			continue
		}
		normalized[strings.ToLower(strings.TrimSpace(chain))] = p
	}
	return &Router{fallback: fallback, byChain: normalized}
}

func (r *Router) Name() string {
	if r.fallback != nil {
		return r.fallback.Name()
	}
	return "router"
}

// Capabilities 返回 fallback 的能力集：它是「平台默认取数面」，链级差异由
// /api/v2/meta/datasets 按链表达，绝不用一份合并表冒充某条链的能力。
func (r *Router) Capabilities() Capabilities {
	if r.fallback != nil {
		return r.fallback.Capabilities()
	}
	return Capabilities{Name: "router"}
}

// Fetch 按 req.Chain 分派；未登记的链一律走 fallback。
func (r *Router) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	if p, ok := r.byChain[strings.ToLower(strings.TrimSpace(req.Chain))]; ok {
		return p.Fetch(ctx, req)
	}
	if r.fallback == nil {
		return FetchResult{}, &Error{Class: ClassUnsupported, Message: "no provider registered for chain " + req.Chain}
	}
	return r.fallback.Fetch(ctx, req)
}

// Health 返回 fallback 的健康度：调度器的并发/背压决策针对默认 Provider，
// 链专属 Provider 的健康度由其自身错误分类（重试/降并发）表达。
func (r *Router) Health(ctx context.Context) HealthStatus {
	if r.fallback != nil {
		return r.fallback.Health(ctx)
	}
	return HealthStatus{Provider: "router", State: HealthNormal, LastCheck: time.Now().UTC()}
}
