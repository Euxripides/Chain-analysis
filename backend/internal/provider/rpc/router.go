package rpc

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// ---- Provider Router（§25-§29）----
//
// 业务只说"我要执行 eth_call"，由 Router 决定用哪个 Provider。
// 代码必须支持 []ProviderCandidate，不允许写死 provider = ankr（§27）。

type routerEntry struct {
	provider Provider
	name     string
	weight   int
	priority int
}

// Router 按 Chain + Capability + Health 选择 Provider。
type Router struct {
	mu       sync.RWMutex
	entries  map[string][]routerEntry
	caps     *CapabilityStore
	breakers *Breakers
	health   *HealthTracker
	conc     *ConcurrencyController
}

func NewRouter(caps *CapabilityStore, breakers *Breakers, health *HealthTracker, conc *ConcurrencyController) *Router {
	return &Router{entries: map[string][]routerEntry{}, caps: caps, breakers: breakers, health: health, conc: conc}
}

// Register 注册某条链上的一个 Provider 候选。priority 越小越优先（§28）。
func (r *Router) Register(chainSlug string, provider Provider, weight, priority int) {
	slug := strings.ToLower(strings.TrimSpace(chainSlug))
	if slug == "" || provider == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := r.entries[slug]
	for i := range entries {
		if entries[i].name == provider.Name() {
			entries[i] = routerEntry{provider: provider, name: provider.Name(), weight: weight, priority: priority}
			r.entries[slug] = entries
			return
		}
	}
	r.entries[slug] = append(entries, routerEntry{
		provider: provider, name: provider.Name(), weight: weight, priority: priority,
	})
}

// Providers 某条链上已注册的 Provider 名称。
func (r *Router) Providers(chainSlug string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := r.entries[strings.ToLower(chainSlug)]
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.name)
	}
	sort.Strings(out)
	return out
}

// Chains 已注册的链。
func (r *Router) Chains() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for slug := range r.entries {
		out = append(out, slug)
	}
	sort.Strings(out)
	return out
}

// Candidates 返回可执行该请求的候选 Provider（按优先级 + 健康度排序）。
//
// 过滤依据（§26）：Capability、熔断状态、Archive/Trace/Debug 需求。
// 全部不可用时返回具体原因，而不是笼统的失败（§222）。
func (r *Router) Candidates(ctx context.Context, req Request, spec MethodSpec) ([]ProviderCandidate, *Error) {
	slug := strings.ToLower(req.Chain)
	r.mu.RLock()
	entries := make([]routerEntry, len(r.entries[slug]))
	copy(entries, r.entries[slug])
	r.mu.RUnlock()

	if len(entries) == 0 {
		return nil, &Error{Class: ClassNoProvider, Chain: req.Chain, Method: req.Method,
			Message: "no rpc provider registered for chain " + slug}
	}

	requiresArchive := req.RequiresArchive()
	var candidates []ProviderCandidate
	var lastReason ErrorClass
	openCircuits := 0

	for _, entry := range entries {
		caps, ok := r.caps.Get(entry.name, slug)
		if !ok {
			// 没有能力信息时按 Provider 自述能力兜底
			if probed, err := entry.provider.Capabilities(ctx, slug); err == nil {
				r.caps.Set(entry.name, slug, probed, CapSourceStatic)
				caps = probed
			} else {
				lastReason = ClassMethodUnsupported
				continue
			}
		}
		if supported, reason := caps.Supports(spec, requiresArchive); !supported {
			lastReason = reason
			continue
		}
		state := r.breakers.State(BreakerKey(entry.name, slug, spec.Class))
		if state == CircuitOpen {
			openCircuits++
			lastReason = ClassCircuitOpen
			continue
		}
		inFlight := 0
		if r.conc != nil {
			inFlight = r.conc.InFlight(LimiterKey{Provider: entry.name, Chain: slug, Class: spec.Class})
		}
		health := r.health.Snapshot(entry.name, slug, state, inFlight)
		candidates = append(candidates, ProviderCandidate{
			Provider: entry.provider, Name: entry.name, Weight: entry.weight,
			Priority: entry.priority, Capability: caps, Health: health,
		})
	}

	if len(candidates) == 0 {
		class := lastReason
		if class == "" {
			class = ClassNoProvider
		}
		message := "no provider supports " + req.Method + " on " + slug
		if openCircuits > 0 && class == ClassCircuitOpen {
			message = "all providers for " + slug + " are circuit-open"
		}
		return nil, &Error{Class: class, Chain: req.Chain, Method: req.Method, Message: message, Retryable: class == ClassCircuitOpen}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		si, sj := Score(candidates[i].Health), Score(candidates[j].Health)
		if si != sj {
			return si > sj
		}
		return candidates[i].Weight > candidates[j].Weight
	})
	return candidates, nil
}
