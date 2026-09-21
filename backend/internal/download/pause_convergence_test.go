package download

import (
	"testing"
	"time"
)

// 暂停收敛有界性：残留的 FETCHED/INGESTED Shard 不能永久阻止暂停收敛。
func TestPauseBlocksConvergence(t *testing.T) {
	cases := []struct {
		name      string
		active    int64
		leftovers int64
		since     time.Duration
		haveTime  bool
		want      bool
	}{
		{"in-flight shard always blocks", 1, 0, time.Hour, true, true},
		{"in-flight shard blocks even past grace", 2, 5, 10 * time.Minute, true, true},
		{"no shards at all converges immediately", 0, 0, 0, false, false},
		{"leftover inside grace waits", 0, 3, 10 * time.Second, true, true},
		{"leftover past grace converges", 0, 3, pauseConvergenceGrace + time.Second, true, false},
		{"unknown request time stays conservative", 0, 1, 0, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pauseBlocksConvergence(tc.active, tc.leftovers, tc.since, tc.haveTime); got != tc.want {
				t.Fatalf("pauseBlocksConvergence(%d,%d,%s,%v)=%v want %v", tc.active, tc.leftovers, tc.since, tc.haveTime, got, tc.want)
			}
		})
	}
}
