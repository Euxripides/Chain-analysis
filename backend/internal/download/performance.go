package download

import "github.com/blockchain-platform/blockchain-platform/internal/model"

// nextEffectiveMode implements the bounded V1.4.2 state machine. Pressure can
// only move down through the three certified levels; recovery moves one level
// at a time and never exceeds the requested mode.
func nextEffectiveMode(current, requested model.PerformanceMode, pressure bool, recoveryReady bool) model.PerformanceMode {
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
