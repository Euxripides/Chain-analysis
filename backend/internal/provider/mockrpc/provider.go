// Package mockrpc：可编程的 RPC Mock Provider（V1.5 §208）。
//
// 支持固定响应、延迟、超时、429、5xx、非法 JSON、超大响应体、不支持的方法，
// 既用于单元/集成测试，也用于没有 API Key 时的本地开发（§244 Soak 也用它）。
package mockrpc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// HandlerFunc 自定义方法响应。
type HandlerFunc func(req rpc.Request) (json.RawMessage, *rpc.Error)

// Provider 可编程 Mock。
type Provider struct {
	name string

	mu           sync.Mutex
	caps         map[string]rpc.CapabilitySet
	handlers     map[string]HandlerFunc
	calls        map[string]int
	batchCalls   int
	totalCalls   int
	failures     int
	failErr      *rpc.Error
	delay        time.Duration
	head         uint64
	unsupported  map[string]bool
	blockedChain map[string]bool
}

// New 构造一个默认可用的 Mock Provider。
func New(name string) *Provider {
	if name == "" {
		name = "mock"
	}
	return &Provider{
		name: name, caps: map[string]rpc.CapabilitySet{}, handlers: map[string]HandlerFunc{},
		calls: map[string]int{}, head: 40_000_000, unsupported: map[string]bool{},
		blockedChain: map[string]bool{},
	}
}

func (p *Provider) Name() string { return p.name }

// ---- 测试编排 ----

// SetCapabilities 设定某链能力。
func (p *Provider) SetCapabilities(chainSlug string, caps rpc.CapabilitySet) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if caps.MaxBatchSize == 0 {
		caps.MaxBatchSize = 100
	}
	p.caps[strings.ToLower(chainSlug)] = caps
}

// Handle 注册方法响应。
func (p *Provider) Handle(method string, fn HandlerFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers[method] = fn
}

// FailNext 接下来 n 次调用返回指定错误。
func (p *Provider) FailNext(n int, err *rpc.Error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures = n
	p.failErr = err
}

// SetDelay 每次调用的人工延迟（用于超时测试）。
func (p *Provider) SetDelay(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delay = d
}

// MarkUnsupported 标记方法不支持（返回 -32601 语义）。
func (p *Provider) MarkUnsupported(method string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unsupported[method] = true
}

// SetHead 设定链头（用于 head lag 测试）。
func (p *Provider) SetHead(head uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.head = head
}

// Calls 某方法的调用次数。
func (p *Provider) Calls(method string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[method]
}

// TotalCalls 全部方法调用次数（Batch 内每项单独计数）。
func (p *Provider) TotalCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.totalCalls
}

// BatchRequests HTTP 批请求次数（§40：与 method call 数区分）。
func (p *Provider) BatchRequests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.batchCalls
}

// Reset 清空计数。
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = map[string]int{}
	p.totalCalls = 0
	p.batchCalls = 0
}

// ---- rpc.Provider 实现 ----

func (p *Provider) Capabilities(_ context.Context, chainSlug string) (rpc.CapabilitySet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if caps, ok := p.caps[strings.ToLower(chainSlug)]; ok {
		return caps, nil
	}
	return rpc.CapabilitySet{StandardRPC: true, Archive: true, Batch: true, MaxBatchSize: 100,
		MaxLogBlockRange: 5000, Source: rpc.CapSourceStatic}, nil
}

func (p *Provider) Health(_ context.Context, chainSlug string) rpc.ProviderHealth {
	return rpc.ProviderHealth{Provider: p.name, Chain: chainSlug, State: rpc.CircuitClosed,
		SuccessRate: 1, LastCheck: time.Now()}
}

func (p *Provider) Call(ctx context.Context, req rpc.Request) (rpc.Response, error) {
	result, err := p.dispatch(ctx, req)
	if err != nil {
		return rpc.Response{Provider: p.name, Chain: req.Chain, Method: req.Method}, err
	}
	return rpc.Response{Result: result, Provider: p.name, Chain: req.Chain,
		Method: req.Method, Bytes: len(result)}, nil
}

func (p *Provider) BatchCall(ctx context.Context, reqs []rpc.Request) ([]rpc.Response, error) {
	p.mu.Lock()
	p.batchCalls++
	p.mu.Unlock()
	out := make([]rpc.Response, len(reqs))
	for i, req := range reqs {
		result, err := p.dispatch(ctx, req)
		out[i] = rpc.Response{Provider: p.name, Chain: req.Chain, Method: req.Method,
			BatchSize: len(reqs), Bytes: len(result)}
		if err != nil {
			// 部分失败：逐项返回，不影响其它项（§46）
			out[i].Error = err
			continue
		}
		out[i].Result = result
	}
	return out, nil
}

func (p *Provider) dispatch(ctx context.Context, req rpc.Request) (json.RawMessage, *rpc.Error) {
	p.mu.Lock()
	p.calls[req.Method]++
	p.totalCalls++
	delay := p.delay
	handler := p.handlers[req.Method]
	unsupported := p.unsupported[req.Method]
	var failErr *rpc.Error
	if p.failures > 0 {
		p.failures--
		failErr = p.failErr
	}
	head := p.head
	p.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, &rpc.Error{Class: rpc.ClassTimeout, Provider: p.name, Chain: req.Chain,
				Method: req.Method, Retryable: true, Message: "mock provider timed out"}
		case <-time.After(delay):
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, &rpc.Error{Class: rpc.ClassTimeout, Provider: p.name, Chain: req.Chain,
			Method: req.Method, Retryable: true, Message: "mock provider context expired"}
	}
	if failErr != nil {
		clone := *failErr
		clone.Provider, clone.Chain, clone.Method = p.name, req.Chain, req.Method
		return nil, &clone
	}
	if unsupported {
		class := rpc.ClassMethodUnsupported
		if req.Class == rpc.ClassTrace || req.Class == rpc.ClassDebugTrace {
			class = rpc.ClassTraceUnsupported
		}
		return nil, &rpc.Error{Class: class, Provider: p.name, Chain: req.Chain, Method: req.Method,
			Message: "mock provider does not support " + req.Method}
	}
	if handler != nil {
		return handler(req)
	}
	return p.defaultResult(req, head)
}

// defaultResult 让 Mock 模式可以完整跑通全平台链路。
func (p *Provider) defaultResult(req rpc.Request, head uint64) (json.RawMessage, *rpc.Error) {
	switch req.Method {
	case "eth_blockNumber":
		return jsonString(chain.Uint64ToHex(head)), nil
	case "eth_chainId":
		return jsonString("0x38"), nil
	case "net_version":
		return jsonString("56"), nil
	case "eth_getBalance":
		address := paramString(req.Params, 0)
		return jsonString("0x" + new(big.Int).SetUint64(deterministic(address)%1_000_000_000_000_000_000).Text(16)), nil
	case "eth_getCode":
		address := paramString(req.Params, 0)
		if deterministic(address)%3 == 0 {
			return jsonString("0x"), nil // EOA
		}
		return jsonString("0x60806040523480156100106000396000f3fe"), nil
	case "eth_getStorageAt":
		return jsonString("0x" + strings.Repeat("0", 64)), nil
	case "eth_call":
		return p.mockCall(req)
	case "eth_getTransactionByHash":
		return mockTransaction(paramString(req.Params, 0), head), nil
	case "eth_getTransactionReceipt":
		return mockReceipt(paramString(req.Params, 0), head), nil
	case "eth_getBlockByNumber", "eth_getBlockByHash":
		return json.RawMessage(fmt.Sprintf(`{"number":"%s","hash":"0x%064x","parentHash":"0x%064x",`+
			`"timestamp":"0x%x","gasUsed":"0x5208","gasLimit":"0x1c9c380","transactions":[]}`,
			chain.Uint64ToHex(head), head, head-1, time.Now().Unix())), nil
	case "eth_getLogs":
		return json.RawMessage("[]"), nil
	case "trace_transaction":
		return mockTrace(paramString(req.Params, 0)), nil
	case "debug_traceTransaction":
		return mockDebugTrace(paramString(req.Params, 0)), nil
	default:
		return nil, &rpc.Error{Class: rpc.ClassMethodUnsupported, Provider: p.name, Chain: req.Chain,
			Method: req.Method, Message: "mock provider has no handler for " + req.Method}
	}
}

// mockCall 覆盖 Token Metadata / LP / Proxy 探针使用的只读调用。
func (p *Provider) mockCall(req rpc.Request) (json.RawMessage, *rpc.Error) {
	callArgs, _ := req.Params[0].(map[string]any)
	data, _ := callArgs["data"].(string)
	to, _ := callArgs["to"].(string)
	selector := ""
	if len(data) >= 10 {
		selector = strings.ToLower(data[:10])
	}
	switch selector {
	case "0x95d89b41": // symbol()
		return jsonString(EncodeString("MCK")), nil
	case "0x06fdde03": // name()
		return jsonString(EncodeString("Mock Token")), nil
	case "0x313ce567": // decimals()
		return jsonString(EncodeUint(big.NewInt(18))), nil
	case "0x18160ddd": // totalSupply()
		return jsonString(EncodeUint(new(big.Int).SetUint64(1_000_000_000))), nil
	case "0x70a08231": // balanceOf(address)
		return jsonString(EncodeUint(new(big.Int).SetUint64(deterministic(to) % 1_000_000_000))), nil
	case "0x0dfe1681": // token0()
		return jsonString(EncodeAddress("0x1111111111111111111111111111111111111111")), nil
	case "0xd21220a7": // token1()
		return jsonString(EncodeAddress("0x2222222222222222222222222222222222222222")), nil
	case "0xc45a0155": // factory()
		return jsonString(EncodeAddress("0x3333333333333333333333333333333333333333")), nil
	case "0x0902f1ac": // getReserves()
		return jsonString("0x" +
			padHex(new(big.Int).SetUint64(1_000_000).Text(16)) +
			padHex(new(big.Int).SetUint64(2_000_000).Text(16)) +
			padHex(new(big.Int).SetUint64(uint64(time.Now().Unix())).Text(16))), nil
	case "0x5c60da1b": // implementation()
		return jsonString(EncodeAddress("0x4444444444444444444444444444444444444444")), nil
	default:
		return nil, &rpc.Error{Class: rpc.ClassExecutionReverted, Provider: p.name, Chain: req.Chain,
			Method: req.Method, Message: "execution reverted"}
	}
}

// ---- ABI 编码小工具（Mock 用）----

// EncodeString 动态 string 的标准 ABI 编码。
func EncodeString(value string) string {
	data := []byte(value)
	body := padHex("20") + padHex(new(big.Int).SetInt64(int64(len(data))).Text(16))
	encoded := hex.EncodeToString(data)
	for len(encoded)%64 != 0 {
		encoded += "0"
	}
	return "0x" + body + encoded
}

// EncodeUint uint256 编码。
func EncodeUint(value *big.Int) string { return "0x" + padHex(value.Text(16)) }

// EncodeAddress address 编码。
func EncodeAddress(address string) string {
	return "0x" + padHex(strings.TrimPrefix(strings.ToLower(address), "0x"))
}

func padHex(value string) string {
	if len(value) >= 64 {
		return value[len(value)-64:]
	}
	return strings.Repeat("0", 64-len(value)) + value
}

func jsonString(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func paramString(params []any, index int) string {
	if index >= len(params) {
		return ""
	}
	if s, ok := params[index].(string); ok {
		return s
	}
	return fmt.Sprint(params[index])
}

func deterministic(seed string) uint64 {
	var sum uint64 = 1469598103934665603
	for i := 0; i < len(seed); i++ {
		sum ^= uint64(seed[i])
		sum *= 1099511628211
	}
	return sum
}

func mockTransaction(hash string, head uint64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"hash":"%s","from":"0x1111111111111111111111111111111111111111",`+
		`"to":"0x2222222222222222222222222222222222222222","value":"0xde0b6b3a7640000","input":"0x",`+
		`"blockNumber":"%s","nonce":"0x1","gas":"0x5208","gasPrice":"0x3b9aca00","transactionIndex":"0x0"}`,
		hash, chain.Uint64ToHex(head-10)))
}

func mockReceipt(hash string, head uint64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"transactionHash":"%s","status":"0x1","blockNumber":"%s",`+
		`"gasUsed":"0x5208","cumulativeGasUsed":"0x5208","effectiveGasPrice":"0x3b9aca00","contractAddress":null,"logs":[],`+
		`"from":"0x1111111111111111111111111111111111111111","to":"0x2222222222222222222222222222222222222222"}`,
		hash, chain.Uint64ToHex(head-10)))
}

// mockTrace parity 风格 trace_transaction 响应。
func mockTrace(hash string) json.RawMessage {
	return json.RawMessage(`[` +
		`{"action":{"callType":"call","from":"0x1111111111111111111111111111111111111111",` +
		`"to":"0x2222222222222222222222222222222222222222","value":"0xde0b6b3a7640000","gas":"0x5208","input":"0x"},` +
		`"result":{"gasUsed":"0x5208","output":"0x"},"subtraces":1,"traceAddress":[],"type":"call"},` +
		`{"action":{"callType":"staticcall","from":"0x2222222222222222222222222222222222222222",` +
		`"to":"0x3333333333333333333333333333333333333333","value":"0x0","gas":"0x2710","input":"0x70a08231"},` +
		`"result":{"gasUsed":"0x1388","output":"0x"},"subtraces":0,"traceAddress":[0],"type":"call"}]`)
}

// mockDebugTrace geth callTracer 风格响应。
func mockDebugTrace(hash string) json.RawMessage {
	return json.RawMessage(`{"type":"CALL","from":"0x1111111111111111111111111111111111111111",` +
		`"to":"0x2222222222222222222222222222222222222222","value":"0xde0b6b3a7640000","gas":"0x5208",` +
		`"gasUsed":"0x5208","input":"0x","output":"0x","calls":[{"type":"STATICCALL",` +
		`"from":"0x2222222222222222222222222222222222222222","to":"0x3333333333333333333333333333333333333333",` +
		`"gas":"0x2710","gasUsed":"0x1388","input":"0x70a08231","output":"0x"}]}`)
}

var _ rpc.Provider = (*Provider)(nil)
