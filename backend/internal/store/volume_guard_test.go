package store

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestVolumeRiskBoundaries(t *testing.T) {
	limits := model.VolumeGuardThresholds{WarnRows: 1_000_000, HardProjectedRows: 5_000_000, ExtremeRows: 10_000_000, SuperExtremeRows: 50_000_000}
	tests := []struct {
		rows int64
		want string
	}{
		{999_999, model.GuardNormal}, {1_000_000, model.GuardLarge}, {5_000_000, model.GuardHuge},
		{10_000_000, model.GuardExtreme}, {50_000_000, model.GuardSuperExtreme},
	}
	for _, tt := range tests {
		if got := volumeRisk(tt.rows, limits); got != tt.want {
			t.Fatalf("rows=%d: got %s want %s", tt.rows, got, tt.want)
		}
	}
}
