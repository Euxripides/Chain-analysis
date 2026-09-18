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

// 生产策略回归：单地址单 dataset 超过 100 万行必须停下来等人工确认
// （2026-09-15 "超过百万的地址就不下载了"）。
func TestGuardTriggeredAtOneMillionRows(t *testing.T) {
	// 与 config 默认值一致：Warn 50 万 / 硬实际 100 万 / 硬预测 100 万。
	limits := model.VolumeGuardThresholds{
		Enabled: true, WarnRows: 500_000, HardActualRows: 1_000_000, HardProjectedRows: 1_000_000,
		ExtremeRows: 10_000_000, SuperExtremeRows: 50_000_000,
		ProjectedConfidence: 0.80, ExtremeConfidence: 0.70,
	}
	cases := []struct {
		name        string
		actual      int64
		projected   int64
		confidence  float64
		override    bool
		state       string
		wantTrigger bool
		wantReason  string
	}{
		{"actual just below the line", 999_999, 999_999, 0.99, false, model.ItemRunning, false, ""},
		{"actual at the line", 1_000_000, 1_000_000, 0.99, false, model.ItemRunning, true, model.GuardReasonActualRows},
		{"actual above the old 2M threshold", 2_500_000, 2_500_000, 0.99, false, model.ItemRunning, true, model.GuardReasonActualRows},
		{"projection over the line with enough confidence", 10_000, 1_200_000, 0.85, false, model.ItemRunning, true, model.GuardReasonProjectedRows},
		{"projection over the line but low confidence", 10_000, 1_200_000, 0.50, false, model.ItemRunning, false, ""},
		{"override keeps working", 9_000_000, 9_000_000, 0.99, true, model.ItemRunning, false, ""},
		{"aborted dataset stays stopped", 9_000_000, 9_000_000, 0.99, false, model.ItemAbortedByUser, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := guardTriggered(tc.actual, tc.projected, tc.confidence, limits, tc.override, tc.state)
			if got != tc.wantTrigger || reason != tc.wantReason {
				t.Fatalf("guardTriggered(actual=%d projected=%d conf=%.2f)=%v/%q want %v/%q",
					tc.actual, tc.projected, tc.confidence, got, reason, tc.wantTrigger, tc.wantReason)
			}
		})
	}
}
