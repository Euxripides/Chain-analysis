// V1.3 Provider Router（§33-§37/§93：P0 规则式，不做 AI）。
// 职责：给定 CountRequest → 选择 Provider / Strategy / Reason。
package countoracle

import (
	"context"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider/count"
)

// RouteDecision（§34）。
type RouteDecision struct {
	ProviderID      string
	Strategy        RouteStrategy
	Reason          string
	ExpectedLatency time.Duration
	ExpectedCost    count.CountCost
}

// route 规则路由（§36：Profile/Cache 由上层负责；这里只做 Provider 选择）。
// 无候选 → StrategyFailOpen。
func (s *Service) route(ctx context.Context, chainID uint64, dataset count.DatasetType, purpose count.CountPurpose) RouteDecision {
	var chosen *executor
	var reason string
	for _, p := range s.providers() {
		pol, ok := s.policyMap[p.ID()]
		if !ok || !pol.Enabled {
			continue
		}
		caps := p.Capabilities(ctx)
		dc, ok := caps.DatasetCap(chainID, dataset)
		if !ok || !dc.Enabled {
			continue
		}
		if dc.RuntimeStatus == count.StatusBlocked || dc.RuntimeStatus == count.StatusDisabled {
			continue
		}
		if p.ID() == "chainbase" && !s.deploy.chainbaseCountable(ctx, chainID, string(dataset)) {
			continue
		}
		exec, ok := s.executors[p.ID()]
		if !ok {
			continue
		}
		if routable, st := exec.routable(); !routable {
			reason = "provider_state_" + st
			continue
		}
		chosen = exec
		reason = ""
		break
	}
	if chosen == nil {
		r := "no_available_provider"
		if reason != "" {
			r += ": " + reason
		}
		return RouteDecision{Strategy: StrategyFailOpen, Reason: r}
	}
	return RouteDecision{
		ProviderID:      chosen.provider.ID(),
		Strategy:        StrategySingleProvider,
		Reason:          "provider_available",
		ExpectedLatency: chosen.expectedLatency(),
	}
}

// providers 按注册顺序列出（Router 使用；内部加锁拷贝）。
func (s *Service) providers() []count.CountProvider {
	s.regMu.RLock()
	defer s.regMu.RUnlock()
	out := make([]count.CountProvider, len(s.providerList))
	copy(out, s.providerList)
	return out
}

// expectedLatency 用 P95 窗口估计。
func (e *executor) expectedLatency() time.Duration {
	snap := e.rt.snapshot()
	if snap.P95Ms > 0 {
		return time.Duration(snap.P95Ms) * time.Millisecond
	}
	return 300 * time.Millisecond
}
