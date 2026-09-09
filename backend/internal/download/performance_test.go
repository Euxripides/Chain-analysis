package download

import (
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"testing"
)

func TestNextEffectiveModeUsesCertifiedSteps(t *testing.T) {
	tests := []struct {
		cur, req          model.PerformanceMode
		pressure, recover bool
		want              model.PerformanceMode
	}{
		{model.PerformanceTurbo, model.PerformanceTurbo, true, false, model.PerformanceFast},
		{model.PerformanceFast, model.PerformanceTurbo, true, false, model.PerformanceSafe},
		{model.PerformanceSafe, model.PerformanceTurbo, false, true, model.PerformanceFast},
		{model.PerformanceFast, model.PerformanceFast, false, true, model.PerformanceFast},
		{model.PerformanceTurbo, model.PerformanceSafe, false, false, model.PerformanceSafe},
	}
	for _, tc := range tests {
		if got := nextEffectiveMode(tc.cur, tc.req, tc.pressure, tc.recover); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}

func TestProfileResolveHardClamps(t *testing.T) {
	p := model.DownloadPerformanceProfile{SafeConcurrency: 24, FastConcurrency: 32, TurboConcurrency: 128, HardCeiling: 48}
	if got := p.Resolve(model.PerformanceTurbo); got != 48 {
		t.Fatalf("got %d want 48", got)
	}
}
func TestPerformanceModeRejectsNumeric(t *testing.T) {
	if _, err := model.ParsePerformanceMode("48"); err == nil {
		t.Fatal("numeric mode must be rejected")
	}
}
