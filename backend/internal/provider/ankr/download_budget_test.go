package ankr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func TestRecoveryBudgetRejectsBeforeHTTPAndUsage(t *testing.T) {
	var requests, usage atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"transactions":[]}}`))
	}))
	defer server.Close()
	c := New(Options{Endpoint: server.URL, RequestsPerMinute: 60000, CreditsPerRequest: 700, CostPerRequestUSD: 0.00007}, func(model.UsageRow) { usage.Add(1) })
	ctx := provider.WithDownloadBudget(context.Background(), func(context.Context, int64, float64, float64) error { return errors.New("persisted ceiling") })
	_, err := c.Fetch(ctx, provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1111111111111111111111111111111111111111", PageSize: 10})
	if !errors.Is(err, provider.ErrDownloadBudget) {
		t.Fatalf("budget error lost: %v", err)
	}
	if requests.Load() != 0 || usage.Load() != 0 {
		t.Fatalf("unsent requests billed: http=%d usage=%d", requests.Load(), usage.Load())
	}
}
