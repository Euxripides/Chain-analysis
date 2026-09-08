package download

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestNextEffectiveModeUsesCertifiedSteps(t *testing.T) {
	tests := []struct {
		name               string
		current, requested model.PerformanceMode
		pressure, recovery bool
		want               model.PerformanceMode
	}{
		{"turbo pressure", model.PerformanceTurbo, model.PerformanceTurbo, true, false, model.PerformanceFast},
		{"continued pressure", model.PerformanceFast, model.PerformanceTurbo, true, false, model.PerformanceSafe},
		{"recover one step", model.PerformanceSafe, model.PerformanceTurbo, false, true, model.PerformanceFast},
		{"recover turbo only when requested", model.PerformanceFast, model.PerformanceFast, false, true, model.PerformanceFast},
		{"lower user request immediately", model.PerformanceTurbo, model.PerformanceSafe, false, false, model.PerformanceSafe},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextEffectiveMode(tc.current, tc.requested, tc.pressure, tc.recovery); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestProfileResolveHardClamps(t *testing.T) {
	p := model.DownloadPerformanceProfile{SafeConcurrency: 24, FastConcurrency: 32, TurboConcurrency: 128, HardCeiling: 48}
	if got := p.Resolve(model.PerformanceTurbo); got != 48 {
		t.Fatalf("got %d want 48", got)
	}
}

func TestPerformanceModeRejectsNumericAndUnknownValues(t *testing.T) {
	for _, raw := range []string{"48", "extreme"} {
		if _, err := model.ParsePerformanceMode(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}
