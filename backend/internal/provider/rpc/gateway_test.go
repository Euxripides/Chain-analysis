package rpc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/mockrpc"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

type harness struct {
	gateway *rpc.Gateway
	usage   []model.UsageRow
	mu      sync.Mutex
}

func newHarness(t *testing.T, providers ...*mockrpc.Provider) *harness {
	t.Helper()
	h := &harness{}
	caps := rpc.NewCapabilityStore()
	breakers := rpc.NewBreakers(20, 0.9, 50*time.Millisecond)
	health := rpc.NewHealthTracker()
	conc := rpc.NewConcurrencyController(16)
	router := rpc.NewRouter(caps, breakers, health, conc)
	gateway := rpc.NewGateway(rpc.GatewayOptions{
		Router: router, Capabilities: caps, Breakers: breakers, Health: health, Concurrency: conc,
		Rate:  rpc.NewRateController(10000),
		Cache: rpc.NewCache(1000, true),
		Usage: func(row model.UsageRow) {
			h.mu.Lock()
			h.usage = append(h.usage, row)
			h.mu.Unlock()
		},
		Retry:   rpc.RetryPolicy{MaxAttempts: 2, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond},
		Secrets: []string{"SUPER_SECRET_API_KEY_VALUE"},
	})
	for i, p := range providers {
		capsSet, _ := p.Capabilities(context.Background(), "bsc")
		caps.Set(p.Name(), "bsc", capsSet, rpc.CapSourceStatic)
		router.Register("bsc", p, 100, i)
	}
	h.gateway = gateway
	return h
}

func (h *harness) usageRows() []model.UsageRow {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]model.UsageRow, len(h.usage))
	copy(out, h.usage)
	return out
}

func headRequest() rpc.Request {
	return rpc.Request{Chain: "bsc", Method: "eth_blockNumber", Params: []any{},
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeChainHead}}
}

// §212 Router：Provider A 不支持 Trace，Provider B 支持 → 必须选 B。
func TestRouterPicksTraceCapableProvider(t *testing.T) {
	noTrace := mockrpc.New("no-trace")
	noTrace.SetCapabilities("bsc", rpc.CapabilitySet{StandardRPC: true, Archive: true, Batch: true, Trace: false})
	withTrace := mockrpc.New("with-trace")
	withTrace.SetCapabilities("bsc", rpc.CapabilitySet{StandardRPC: true, Archive: true, Batch: true, Trace: true})

	h := newHarness(t, noTrace, withTrace)
	response, err := h.gateway.Call(context.Background(), rpc.Request{
		Chain: "bsc", Method: "trace_transaction", Params: []any{"0xabc"},
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeTrace},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "with-trace" {
		t.Fatalf("provider = %s, want with-trace", response.Provider)
	}
	if noTrace.Calls("trace_transaction") != 0 {
		t.Fatal("trace-incapable provider must not be called")
	}
}

// §222 全部 Provider 都不支持 Trace → 明确的 trace_unsupported，而不是笼统失败。
func TestTraceUnsupportedEverywhere(t *testing.T) {
	p := mockrpc.New("no-trace")
	p.SetCapabilities("bsc", rpc.CapabilitySet{StandardRPC: true, Batch: true, Trace: false})
	h := newHarness(t, p)
	_, err := h.gateway.Call(context.Background(), rpc.Request{
		Chain: "bsc", Method: "trace_transaction", Params: []any{"0xabc"},
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeTrace},
	})
	if !rpc.IsClass(err, rpc.ClassTraceUnsupported) {
		t.Fatalf("err = %v, want trace_unsupported", err)
	}
	// 标准 RPC 不受影响（§187）
	if _, err := h.gateway.Call(context.Background(), headRequest()); err != nil {
		t.Fatalf("standard rpc must keep working: %v", err)
	}
}

// §213 Archive：固定历史区块必须选具备 Archive 能力的 Provider。
func TestArchiveRoutingRequiresArchiveProvider(t *testing.T) {
	fullNode := mockrpc.New("full")
	fullNode.SetCapabilities("bsc", rpc.CapabilitySet{StandardRPC: true, Batch: true, Archive: false})
	archive := mockrpc.New("archive")
	archive.SetCapabilities("bsc", rpc.CapabilitySet{StandardRPC: true, Batch: true, Archive: true})

	h := newHarness(t, fullNode, archive)
	response, err := h.gateway.Call(context.Background(), rpc.Request{
		Chain: "bsc", Method: "eth_getBalance",
		Params:   []any{"0x1111111111111111111111111111111111111111", "0x64"},
		BlockRef: chain.AtBlock(100),
		Budget:   rpc.BudgetContext{Purpose: rpc.PurposeBalance},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "archive" {
		t.Fatalf("provider = %s, want archive", response.Provider)
	}
	if fullNode.Calls("eth_getBalance") != 0 {
		t.Fatal("non-archive provider must not serve a historical block")
	}
}

// §214 Budget：预算 10，第 11 次必须 ErrBudgetExceeded 且不产生 Provider 请求。
func TestBudgetRejectsBeforeProvider(t *testing.T) {
	p := mockrpc.New("mock")
	h := newHarness(t, p)
	budget := rpc.BudgetContext{Purpose: rpc.PurposeInvestigation, InvestigationID: "case-1", MaxRequests: 10}
	for i := 0; i < 10; i++ {
		req := rpc.Request{Chain: "bsc", Method: "eth_getBalance",
			Params: []any{fmt.Sprintf("0x%040x", i), "latest"}, Budget: budget,
			CachePolicy: rpc.CachePolicy{Disabled: true}}
		if _, err := h.gateway.Call(context.Background(), req); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}
	callsBefore := p.TotalCalls()
	_, err := h.gateway.Call(context.Background(), rpc.Request{Chain: "bsc", Method: "eth_getBalance",
		Params: []any{"0xffff000000000000000000000000000000000000", "latest"}, Budget: budget,
		CachePolicy: rpc.CachePolicy{Disabled: true}})
	if !rpc.IsClass(err, rpc.ClassBudgetExceeded) {
		t.Fatalf("err = %v, want budget_exceeded", err)
	}
	if p.TotalCalls() != callsBefore {
		t.Fatalf("provider requests after rejection = %d, want %d", p.TotalCalls(), callsBefore)
	}
}

// §215 Singleflight：100 并发同 key → Provider 只收到 1 次请求。
func TestSingleflightCollapsesConcurrentCalls(t *testing.T) {
	p := mockrpc.New("mock")
	p.SetDelay(30 * time.Millisecond)
	h := newHarness(t, p)

	var wg sync.WaitGroup
	errs := make([]error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := h.gateway.Call(context.Background(), rpc.Request{
				Chain: "bsc", Method: "eth_getCode",
				Params: []any{"0xabcabcabcabcabcabcabcabcabcabcabcabcabca", "latest"},
				Budget: rpc.BudgetContext{Purpose: rpc.PurposeContractClassification},
			})
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d failed: %v", i, err)
		}
	}
	if got := p.Calls("eth_getCode"); got != 1 {
		t.Fatalf("provider requests = %d, want 1", got)
	}
}

// §216 Cache：第一次走 Provider，第二次走缓存。
func TestCacheServesSecondCall(t *testing.T) {
	p := mockrpc.New("mock")
	h := newHarness(t, p)
	req := rpc.Request{Chain: "bsc", Method: "eth_getTransactionReceipt",
		Params: []any{"0x" + strings.Repeat("a", 64)},
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeReceipt}}

	first, err := h.gateway.Call(context.Background(), req)
	if err != nil || first.CacheHit {
		t.Fatalf("first call = %+v %v", first, err)
	}
	second, err := h.gateway.Call(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.CacheHit {
		t.Fatal("second call must be a cache hit")
	}
	if got := p.Calls("eth_getTransactionReceipt"); got != 1 {
		t.Fatalf("provider requests = %d, want 1", got)
	}
	// force_refresh 必须绕过缓存（§206）
	refresh := req
	refresh.Consistency = rpc.ConsistencyForceRefresh
	if _, err := h.gateway.Call(context.Background(), refresh); err != nil {
		t.Fatal(err)
	}
	if got := p.Calls("eth_getTransactionReceipt"); got != 2 {
		t.Fatalf("force_refresh provider requests = %d, want 2", got)
	}
	// local_only 永不触达 Provider（§204）
	localOnly := rpc.Request{Chain: "bsc", Method: "eth_getTransactionReceipt",
		Params: []any{"0x" + strings.Repeat("b", 64)}, Consistency: rpc.ConsistencyLocalOnly,
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeReceipt}}
	if _, err := h.gateway.Call(context.Background(), localOnly); !rpc.IsClass(err, rpc.ClassLocalMiss) {
		t.Fatalf("local_only err = %v, want local_miss", err)
	}
	if got := p.Calls("eth_getTransactionReceipt"); got != 2 {
		t.Fatalf("local_only must not call the provider, got %d", got)
	}
}

// §217/§218 Batch：乱序响应正确映射，部分失败逐项返回。
func TestBatchOrderingAndPartialFailure(t *testing.T) {
	p := mockrpc.New("mock")
	p.Handle("eth_getBalance", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		address, _ := req.Params[0].(string)
		if strings.HasSuffix(address, "5") {
			return nil, &rpc.Error{Class: rpc.ClassExecutionReverted, Message: "mock failure"}
		}
		raw, _ := json.Marshal(address)
		return raw, nil
	})
	h := newHarness(t, p)

	reqs := make([]rpc.Request, 100)
	for i := range reqs {
		reqs[i] = rpc.Request{Chain: "bsc", Method: "eth_getBalance",
			Params: []any{fmt.Sprintf("0x%039x%d", i, i%10), "latest"},
			Budget: rpc.BudgetContext{Purpose: rpc.PurposeBalance}, CachePolicy: rpc.CachePolicy{Disabled: true}}
	}
	responses, err := h.gateway.BatchCall(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != len(reqs) {
		t.Fatalf("responses = %d, want %d", len(responses), len(reqs))
	}
	failures := 0
	for i, response := range responses {
		if response.Error != nil {
			failures++
			continue
		}
		var got string
		if err := json.Unmarshal(response.Result, &got); err != nil {
			t.Fatal(err)
		}
		want, _ := reqs[i].Params[0].(string)
		if got != want {
			t.Fatalf("response %d mapped to %s, want %s", i, got, want)
		}
	}
	if failures != 10 {
		t.Fatalf("partial failures = %d, want 10", failures)
	}
	// §40：批请求数远小于 method call 数
	if p.BatchRequests() == 0 || p.BatchRequests() >= 100 {
		t.Fatalf("batch http requests = %d, expected far fewer than 100", p.BatchRequests())
	}
	if p.TotalCalls() != 100 {
		t.Fatalf("method calls = %d, want 100", p.TotalCalls())
	}
}

// §223 只读守卫：写方法在到达 Provider 之前就被拒绝。
func TestWriteMethodsDeniedBeforeProvider(t *testing.T) {
	p := mockrpc.New("mock")
	h := newHarness(t, p)
	_, err := h.gateway.Call(context.Background(), rpc.Request{
		Chain: "bsc", Method: "eth_sendRawTransaction", Params: []any{"0xdeadbeef"},
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeAdmin},
	})
	if !rpc.IsClass(err, rpc.ClassMethodDenied) {
		t.Fatalf("err = %v, want method_denied", err)
	}
	if p.TotalCalls() != 0 {
		t.Fatal("write method must never reach the provider")
	}
	// 未登记方法同样拒绝（§117）
	if _, err := h.gateway.Call(context.Background(), rpc.Request{Chain: "bsc", Method: "personal_unlockAccount",
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeAdmin}}); !rpc.IsClass(err, rpc.ClassMethodDenied) {
		t.Fatalf("unregistered method err = %v, want method_denied", err)
	}
}

// §229 Provider Failover：A 超时/熔断，B 健康 → 业务成功。
func TestProviderFailover(t *testing.T) {
	broken := mockrpc.New("broken")
	broken.FailNext(100, &rpc.Error{Class: rpc.ClassUpstream, Message: "boom", Retryable: true})
	healthy := mockrpc.New("healthy")
	h := newHarness(t, broken, healthy)

	response, err := h.gateway.Call(context.Background(), headRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "healthy" {
		t.Fatalf("provider = %s, want healthy", response.Provider)
	}
}

// §247-15/§88：无 Archive Provider 时固定区块必须给出 archive_required。
func TestArchiveRequiredWhenNoArchiveProvider(t *testing.T) {
	p := mockrpc.New("full")
	p.SetCapabilities("bsc", rpc.CapabilitySet{StandardRPC: true, Batch: true, Archive: false})
	h := newHarness(t, p)
	_, err := h.gateway.Call(context.Background(), rpc.Request{
		Chain: "bsc", Method: "eth_call", BlockRef: chain.AtBlock(10),
		Params: []any{map[string]any{"to": "0x1", "data": "0x95d89b41"}, "0xa"},
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeTokenMetadata},
	})
	if !rpc.IsClass(err, rpc.ClassArchiveRequired) {
		t.Fatalf("err = %v, want archive_required", err)
	}
}

// §39/§40 用量：method call 数与 HTTP 请求数分别记录，缓存命中不计费。
func TestUsageAccounting(t *testing.T) {
	p := mockrpc.New("mock")
	h := newHarness(t, p)
	req := rpc.Request{Chain: "bsc", Method: "eth_getCode",
		Params: []any{"0xdddddddddddddddddddddddddddddddddddddddd", "latest"},
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeContractClassification, JobID: "job-1"}}
	if _, err := h.gateway.Call(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := h.gateway.Call(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	rows := h.usageRows()
	if len(rows) < 2 {
		t.Fatalf("usage rows = %d", len(rows))
	}
	provider, cached := rows[0], rows[len(rows)-1]
	if provider.APIType != model.APITypeRPC || provider.Chain != "bsc" || provider.MethodClass != "CODE" {
		t.Fatalf("provider usage row = %+v", provider)
	}
	if provider.UsageType != rpc.PurposeContractClassification || provider.JobID != "job-1" {
		t.Fatalf("usage attribution = %+v", provider)
	}
	if provider.Credits <= 0 {
		t.Fatal("provider call must record credits")
	}
	if !cached.CacheHit || cached.Credits != 0 || cached.CostUSD != 0 {
		t.Fatalf("cache hit row must be free: %+v", cached)
	}
}

// §31/§224 脱敏：带 Key 的 endpoint 不得出现在错误或日志中。
func TestSecretRedaction(t *testing.T) {
	if got := rpc.RedactURL("https://rpc.ankr.com/bsc/SUPER_SECRET_API_KEY_VALUE"); strings.Contains(got, "SECRET") {
		t.Fatalf("redacted url still leaks: %s", got)
	} else if got != "rpc.ankr.com/bsc/***" {
		t.Fatalf("redacted url = %s", got)
	}
	message := "dial https://rpc.ankr.com/bsc/SUPER_SECRET_API_KEY_VALUE failed"
	if got := rpc.Redact(message, "SUPER_SECRET_API_KEY_VALUE"); strings.Contains(got, "SUPER_SECRET") {
		t.Fatalf("redacted message still leaks: %s", got)
	}

	failing := mockrpc.New("mock")
	failing.FailNext(10, &rpc.Error{Class: rpc.ClassUpstream,
		Message: "upstream https://rpc.ankr.com/bsc/SUPER_SECRET_API_KEY_VALUE exploded", Retryable: false})
	h := newHarness(t, failing)
	_, err := h.gateway.Call(context.Background(), headRequest())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SUPER_SECRET_API_KEY_VALUE") {
		t.Fatalf("gateway error leaks the api key: %v", err)
	}
	// 对外文案不得包含 Provider 原始细节（§148）
	if public := rpc.AsError(err).Public(); strings.Contains(public, "ankr.com") {
		t.Fatalf("public message leaks internals: %s", public)
	}
}

// §220/§221：429 降速率，连接数限制降并发，二者互不干扰（§35）。
func TestRateAndConcurrencyControlsAreSeparate(t *testing.T) {
	rateController := rpc.NewRateController(100)
	concurrency := rpc.NewConcurrencyController(16)
	key := rpc.LimiterKey{Provider: "ankr", Chain: "bsc", Class: rpc.ClassLogs}

	beforeRate := rateController.Rate(key)
	beforeLimit := concurrency.Limit(key)

	rateController.OnRateLimited(key, 0)
	if rateController.Rate(key) >= beforeRate {
		t.Fatal("429 must reduce the request rate")
	}
	if concurrency.Limit(key) != beforeLimit {
		t.Fatal("429 must not change the concurrency limit")
	}

	concurrency.Reduce(key)
	if concurrency.Limit(key) >= beforeLimit {
		t.Fatal("connection limit must reduce in-flight concurrency")
	}
}

// §65 熔断维度：Trace 故障不得连带熔断同 Provider 的 eth_blockNumber。
func TestCircuitBreakerIsPerMethodClass(t *testing.T) {
	breakers := rpc.NewBreakers(8, 0.5, time.Minute)
	traceKey := rpc.BreakerKey("ankr", "bsc", rpc.ClassTrace)
	headKey := rpc.BreakerKey("ankr", "bsc", rpc.ClassChainHead)
	for i := 0; i < 8; i++ {
		breakers.Record(traceKey, false)
	}
	if state := breakers.State(traceKey); state != rpc.CircuitOpen {
		t.Fatalf("trace circuit = %s, want OPEN", state)
	}
	if state := breakers.State(headKey); state == rpc.CircuitOpen {
		t.Fatal("chain head circuit must stay closed")
	}
}
