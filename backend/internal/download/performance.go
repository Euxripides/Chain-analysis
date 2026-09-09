package download

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
)

func nextEffectiveMode(current, requested model.PerformanceMode, pressure, recoveryReady bool) model.PerformanceMode {
	if requested.Level() < current.Level() {
		return requested
	}
	if pressure {
		return model.LowerPerformanceMode(current)
	}
	if !recoveryReady || current.Level() >= requested.Level() {
		return current
	}
	if current == model.PerformanceSafe {
		return model.PerformanceFast
	}
	return model.PerformanceTurbo
}

// performanceModeLoop maps the only three user-facing modes to certified
// targets. Existing memory/spool/ClickHouse/provider backpressure remains the
// source of truth for downward protection.
func (sc *Scheduler) performanceModeLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	healthyTicks := 0
	run := func() {
		profile, err := sc.store.GetAnkrProductionProfile(ctx)
		if err != nil {
			profile = sc.cfg.AnkrProductionProfile()
		}
		state, err := sc.store.GetAnkrRuntimeState(ctx)
		if err != nil {
			return
		}
		requested, err := sc.store.GetAnkrConfiguredMode(ctx)
		if err != nil {
			requested = profile.DefaultMode
		}
		if activeMode, ok, activeErr := sc.store.HighestActiveRequestedMode(ctx); activeErr == nil && ok {
			requested = activeMode
		}
		if state.EffectiveMode == "" {
			state.EffectiveMode = requested
		}
		if sc.bp != nil {
			sc.bp.Refresh(ctx, sc.aggOrNil())
		}
		hs := sc.prov.Health(ctx)
		pressure := hs.State == provider.HealthOpen || hs.State == provider.HealthDegraded || hs.RecentErrorRate > 0.10
		reason := "requested_mode"
		if sc.bp != nil && sc.bp.State() != stateNormal {
			pressure = true
			reason = sc.bp.Reason()
		}
		if ac, ok := sc.prov.(*ankr.Client); ok {
			conn := ac.ConnectionLimitCount()
			if conn > sc.lastConnLimit {
				pressure = true
				reason = "provider connection limit"
			}
			sc.lastConnLimit = conn
		}
		if pressure {
			healthyTicks = 0
		} else {
			healthyTicks++
		}
		next := nextEffectiveMode(state.EffectiveMode, requested, pressure, healthyTicks >= 3)
		if next != state.EffectiveMode && !pressure {
			healthyTicks = 0
			reason = "healthy recovery"
		}
		changed := next != state.EffectiveMode || requested != state.RequestedMode
		if changed {
			state.ModeChangedAt = time.Now()
		}
		state.RequestedMode = requested
		state.EffectiveMode = next
		state.TargetConcurrency = profile.Resolve(next)
		state.ActiveConcurrency = int(atomic.LoadInt64(&sc.active))
		state.ModeReason = reason
		sc.setConcurrency(state.TargetConcurrency)
		if err := sc.store.SetAnkrRuntimeState(ctx, state); err != nil {
			sc.log.Warn("performance mode state write failed", "err", err)
			return
		}
		if changed {
			_ = sc.store.InsertAudit(ctx, nil, "ankr_performance_mode_effective_changed", "ankr_scheduler", "global", map[string]any{"requested_mode": requested, "effective_mode": next, "reason": reason, "target": state.TargetConcurrency})
			sc.publish("events.ankr.performance-mode", state)
		}
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
