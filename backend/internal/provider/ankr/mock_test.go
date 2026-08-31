package ankr

import (
	"context"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func TestMockPagination(t *testing.T) {
	m := NewMock(100, nil)
	from, to := uint64(1), uint64(1000)
	ctx := context.Background()
	var pages int
	var token string
	var total int
	for {
		res, err := m.Fetch(ctx, provider.FetchRequest{
			Chain: "bsc", Dataset: "transactions", Address: "0xabc",
			FromBlock: &from, ToBlock: &to, PageSize: 100, PageToken: token,
		})
		if err != nil {
			t.Fatal(err)
		}
		total += len(res.Rows)
		pages++
		if !res.HasMore {
			break
		}
		token = res.NextPageToken
		if pages > 100 {
			t.Fatal("pagination did not terminate")
		}
	}
	if total <= 0 {
		t.Error("mock returned no rows")
	}
}

func TestMockBalances(t *testing.T) {
	m := NewMock(100, nil)
	res, err := m.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "balances", Address: "0xabc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) == 0 {
		t.Error("no balances")
	}
}

func BenchmarkTokenBucketAcquire(b *testing.B) {
	tk := NewTokenBucket(1_000_000)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := tk.Acquire(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func TestTokenBucket(t *testing.T) {
	b := NewTokenBucket(60) // 1/s
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	for i := 0; i < 2; i++ {
		if err := b.Acquire(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// 第二个令牌应等待约 1s
	if d := time.Since(start); d < 800*time.Millisecond {
		t.Errorf("second token granted too early: %v", d)
	}
}
