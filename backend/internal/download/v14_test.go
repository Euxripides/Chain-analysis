// V1.4 纯逻辑测试：LPT work score / Retry Budget / Lease Epoch 判定 / Certification 门控（nil store）。
package download

import (
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func TestV14WorkScore(t *testing.T) {
	cases := []struct {
		from, to int64
		pageSize int64
		want     float64
	}{
		{0, 0, 10000, 0},
		{1, 10000, 10000, 1},
		{1, 20000, 10000, 2},
		{1, 5000, 10000, 0.5},
		{100, 200, 0, 0.011}, // pageSize<=0 → 10000 → span 101/10000=0.0101 → round 0.010? compute below
	}
	for _, tc := range cases {
		got := v14WorkScore(tc.from, tc.to, tc.pageSize)
		want := tc.want
		if tc.pageSize <= 0 {
			want = 0.01 // (200-100+1)=101/10000=0.0101 → round3 = 0.010
		}
		if got != want {
			t.Fatalf("v14WorkScore(%d,%d,%d)=%v want %v", tc.from, tc.to, tc.pageSize, got, want)
		}
	}
	// LPT：大 Range 分数应大于小 Range
	if v14WorkScore(1, 1_000_000, 10000) <= v14WorkScore(1, 1000, 10000) {
		t.Fatalf("LPT score ordering broken")
	}
}

func TestRetryBudgetOK(t *testing.T) {
	start := time.Now()
	if !retryBudgetOK(0, start, 8, 10*time.Minute) {
		t.Fatalf("first attempt must be allowed")
	}
	if retryBudgetOK(8, start, 8, 10*time.Minute) {
		t.Fatalf("attempt==max must stop")
	}
	if retryBudgetOK(2, start.Add(-11*time.Minute), 8, 10*time.Minute) {
		t.Fatalf("elapsed budget must stop")
	}
	// maxAttempts<=0 → 无限次数预算（只用时间预算兜底）
	if !retryBudgetOK(3, start.Add(-1*time.Minute), 0, 10*time.Minute) {
		t.Fatalf("maxAttempts=0 means unlimited attempts (elapsed budget only)")
	}
	if !retryBudgetOK(0, start.Add(-9*time.Minute), 0, 10*time.Minute) {
		t.Fatalf("within elapsed budget allowed")
	}
}

func TestLeaseEpochMismatchDetection(t *testing.T) {
	if !leaseEpochMismatch(store.ErrLeaseEpochMismatch) {
		t.Fatalf("must detect epoch mismatch")
	}
	if leaseEpochMismatch(nil) || leaseEpochMismatch(timeoutErr{}) {
		t.Fatalf("must not detect ordinary errors")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string { return "timeout" }

func TestTxCertGateNilStore(t *testing.T) {
	// store nil：certRequired=false → 允许（旧 env 语义）；certRequired=true → 拒绝（无证据）
	p := &Planner{}
	if !p.txRangeCertified("bsc") {
		t.Fatalf("nil store + certRequired=false must fall back to legacy allow")
	}
	p = &Planner{txCertRequired: true}
	if p.txRangeCertified("bsc") {
		t.Fatalf("nil store + certRequired=true must deny without certification")
	}
	// multiRangeEligibleV12 组合
	p = &Planner{transactionsRange: true, txCertRequired: true}
	if p.multiRangeEligibleV12("bsc", "transactions") {
		t.Fatalf("tx multi-range must be denied without certification")
	}
	p = &Planner{transactionsRange: true, txCertRequired: false}
	if !p.multiRangeEligibleV12("bsc", "transactions") {
		t.Fatalf("legacy env-only gate must allow")
	}
	if !p.multiRangeEligibleV12("bsc", "token_transfers") {
		t.Fatalf("token_transfers remains eligible")
	}
	p = &Planner{transactionsRange: false, txCertRequired: false}
	if p.multiRangeEligibleV12("bsc", "transactions") {
		t.Fatalf("tx range flag off must deny")
	}
}
