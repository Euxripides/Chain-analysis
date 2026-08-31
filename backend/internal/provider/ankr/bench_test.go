package ankr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// fakeAnkrServer 生成 pageSize 决定行数的合成响应（文档 §89 Provider Benchmark）。
func fakeAnkrServer(tb testing.TB, pageSize int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows := make([]json.RawMessage, pageSize)
		for i := range rows {
			rows[i] = json.RawMessage(fmt.Sprintf(
				`{"blockNumber":"0x%x","hash":"0xabc","from":"0x1111111111111111111111111111111111111111","to":"0x2222222222222222222222222222222222222222","value":"0x1","nonce":"0x1","gas":"0x5208","gasPrice":"0x5","gasUsed":"0x5208","status":"0x1","input":"0x","timestamp":"0x6a89e66e"}`, i))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"transactions": rows},
		})
	}))
	tb.Cleanup(srv.Close)
	return srv
}

// BenchmarkProviderPageSize §89：pageSize 1000/5000/10000 的吞吐与延迟。
func BenchmarkProviderPageSize(b *testing.B) {
	for _, ps := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("pageSize_%d", ps), func(b *testing.B) {
			srv := fakeAnkrServer(b, ps)
			c := New(Options{Endpoint: srv.URL, APIKey: "k", MaxPageSize: ps, RequestsPerMinute: 1000000, RequestTimeout: 30 * time.Second}, nil)
			b.ReportAllocs()
			b.SetBytes(int64(ps * 512)) // 估算每行 ~512B
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				res, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1111111111111111111111111111111111111111", PageSize: ps})
				if err != nil {
					b.Fatal(err)
				}
				if len(res.Rows) != ps {
					b.Fatalf("rows=%d want %d", len(res.Rows), ps)
				}
			}
		})
	}
}

// BenchmarkConcurrentFetch §89：并发 1/2/4/8/16/32 的合成 RPS/rows-per-sec。
func BenchmarkConcurrentFetch(b *testing.B) {
	for _, cc := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(fmt.Sprintf("concurrency_%d", cc), func(b *testing.B) {
			srv := fakeAnkrServer(b, 1000)
			c := New(Options{Endpoint: srv.URL, APIKey: "k", MaxPageSize: 1000, RequestsPerMinute: 1000000, RequestTimeout: 30 * time.Second}, nil)
			req := provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1111111111111111111111111111111111111111", PageSize: 1000}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := c.Fetch(context.Background(), req); err != nil {
						b.Error(err)
					}
				}
			})
			_ = sync.Once{}
		})
	}
}
