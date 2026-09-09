// V1.3 MockParallelProvider 单元测试（§84/§85 语义 + 并发准入）。
package mockparallel

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider/count"
)

func mkReq(i int) count.CountRequest {
	return count.CountRequest{
		RequestID:         "req",
		ChainID:           56,
		Dataset:           count.DatasetTransactions,
		Subject:           count.SubjectRef{Type: count.SubjectAddress, Value: addr(i)},
		Range:             count.BlockRange{Start: 1, End: 1000},
		QueryShapeVersion: 1,
	}
}

func addr(i int) string { return "0x" + fmt.Sprintf("%040x", i) }

func TestDeterministicCount(t *testing.T) {
	a := New(Options{Latency: time.Millisecond})
	r1, err := a.Count(context.Background(), mkReq(5))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := a.Count(context.Background(), mkReq(5))
	if err != nil {
		t.Fatal(err)
	}
	if r1.Count != r2.Count || r1.Count < 1000 {
		t.Fatalf("deterministic count expected, got %d vs %d", r1.Count, r2.Count)
	}
	if r1.Semantics != count.SemanticsTotalRows || r1.Accuracy != count.AccuracyExact {
		t.Fatalf("semantics=%s accuracy=%s", r1.Semantics, r1.Accuracy)
	}
}

func TestConcurrencyCapAndInflight(t *testing.T) {
	a := New(Options{MaxConcurrency: 100, Latency: 50 * time.Millisecond})
	const n = 300
	var wg sync.WaitGroup
	ok := 0
	rl := 0
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := a.Count(context.Background(), mkReq(100+i))
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if ce, isCE := err.(*count.CountError); isCE && ce.Class == count.ErrRateLimit {
				rl++
			}
		}(i)
	}
	wg.Wait()
	st := a.Stats()
	if st.MaxObservedInflight > 100 {
		t.Fatalf("max observed inflight=%d > 100", st.MaxObservedInflight)
	}
	if ok != n-rl {
		t.Fatalf("ok=%d rl=%d total=%d", ok, rl, n)
	}
	if rl == 0 {
		t.Logf("note: no rate-limit observed (300 @ 100 cap / 50ms may fit) ok=%d", ok)
	}
}

func TestCtxTimeout(t *testing.T) {
	a := New(Options{Latency: 200 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := a.Count(ctx, mkReq(1))
	if err == nil {
		t.Fatalf("ctx timeout must fail")
	}
	if ce, ok := err.(*count.CountError); !ok || ce.Class != count.ErrTimeout {
		t.Fatalf("expected TIMEOUT error, got %v", err)
	}
}

func TestFailRateInjection(t *testing.T) {
	a := New(Options{Latency: time.Millisecond, FailRate: 1.0})
	if _, err := a.Count(context.Background(), mkReq(2)); err == nil {
		t.Fatalf("failrate=1 must fail")
	}
	st := a.Stats()
	if st.FailCalls == 0 {
		t.Fatalf("fail calls = 0")
	}
}
