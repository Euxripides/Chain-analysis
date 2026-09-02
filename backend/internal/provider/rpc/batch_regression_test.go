package rpc_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider/mockrpc"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

func TestBatchFallbackDoesNotChargeBudgetTwice(t *testing.T) {
	provider := mockrpc.New("no-batch")
	provider.SetCapabilities("bsc", rpc.CapabilitySet{StandardRPC: true, Archive: true, Batch: false})
	caps := rpc.NewCapabilityStore()
	breakers := rpc.NewBreakers(20, 0.9, time.Second)
	health := rpc.NewHealthTracker()
	concurrency := rpc.NewConcurrencyController(8)
	router := rpc.NewRouter(caps, breakers, health, concurrency)
	providerCaps, _ := provider.Capabilities(context.Background(), "bsc")
	caps.Set(provider.Name(), "bsc", providerCaps, rpc.CapSourceStatic)
	router.Register("bsc", provider, 100, 0)
	budgets := rpc.NewBudgetManager(time.Hour, map[string]rpc.BudgetLimits{
		rpc.PurposeBalance: {MaxRequests: 2},
	}, true)
	gateway := rpc.NewGateway(rpc.GatewayOptions{
		Router: router, Capabilities: caps, Breakers: breakers, Health: health,
		Concurrency: concurrency, Rate: rpc.NewRateController(10000), Budgets: budgets,
	})
	reqs := []rpc.Request{
		{Chain: "bsc", Method: "eth_getBalance", Params: []any{"0x1111111111111111111111111111111111111111", "latest"}, Budget: rpc.BudgetContext{Purpose: rpc.PurposeBalance}, CachePolicy: rpc.CachePolicy{Disabled: true}},
		{Chain: "bsc", Method: "eth_getBalance", Params: []any{"0x2222222222222222222222222222222222222222", "latest"}, Budget: rpc.BudgetContext{Purpose: rpc.PurposeBalance}, CachePolicy: rpc.CachePolicy{Disabled: true}},
	}
	responses, err := gateway.BatchCall(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	for i, response := range responses {
		if response.Error != nil {
			t.Fatalf("response[%d] error=%v", i, response.Error)
		}
	}
	usage := budgets.Snapshot()
	if len(usage) != 1 || usage[0].Requests != 2 || provider.TotalCalls() != 2 {
		t.Fatalf("budget=%+v provider calls=%d", usage, provider.TotalCalls())
	}
}

func TestAutomaticBatcherSeparatesUsageContexts(t *testing.T) {
	provider := mockrpc.New("mock")
	h := newHarness(t, provider)
	batcher := rpc.NewBatcher(h.gateway, rpc.BatcherOptions{MaxSize: 10, MaxWait: 20 * time.Millisecond})
	defer batcher.Close()
	reqs := []rpc.Request{
		{Chain: "bsc", Method: "eth_getCode", Params: []any{"0x1111111111111111111111111111111111111111", "latest"}, Budget: rpc.BudgetContext{Purpose: rpc.PurposeContractClassification}},
		{Chain: "bsc", Method: "eth_getCode", Params: []any{"0x2222222222222222222222222222222222222222", "latest"}, Budget: rpc.BudgetContext{Purpose: rpc.PurposeInvestigation, InvestigationID: "case-1"}},
	}
	var wg sync.WaitGroup
	errs := make([]error, len(reqs))
	for i := range reqs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, errs[index] = batcher.Call(context.Background(), reqs[index])
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if provider.BatchRequests() != 2 {
		t.Fatalf("batch requests=%d, want 2 isolated usage contexts", provider.BatchRequests())
	}
}
