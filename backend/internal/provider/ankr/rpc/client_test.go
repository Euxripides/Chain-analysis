package ankrrpc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

func newTestClient(t *testing.T, handler http.HandlerFunc, hardLimit int64) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := New(Options{
		EndpointTemplate:  server.URL + "/{chain}/{key}",
		APIKey:            "SUPER_SECRET_API_KEY_VALUE",
		RequestTimeout:    2 * time.Second,
		ResponseHardLimit: hardLimit,
	})
	return client, server
}

func headRequest() rpc.Request {
	return rpc.Request{Chain: "bsc", Method: "eth_blockNumber", Params: []any{}, Class: rpc.ClassChainHead}
}

func TestCallSuccess(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload jsonRPCRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		if payload.Method != "eth_blockNumber" || payload.JSONRPC != "2.0" {
			t.Errorf("unexpected payload %+v", payload)
		}
		// 位置参数必须是数组而不是 null
		if !strings.Contains(string(body), `"params":[]`) {
			t.Errorf("params must be encoded as an array: %s", body)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(payload.ID) + `,"result":"0x2625a00"}`))
	}, 1<<20)

	response, err := client.Call(context.Background(), headRequest())
	if err != nil {
		t.Fatal(err)
	}
	var hexValue string
	if err := json.Unmarshal(response.Result, &hexValue); err != nil {
		t.Fatal(err)
	}
	if hexValue != "0x2625a00" {
		t.Fatalf("result = %s", hexValue)
	}
}

// §219 响应体超硬限制必须是 ErrResponseTooLarge，而不是 unexpected EOF / bad json。
func TestResponseTooLarge(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		payload := `{"jsonrpc":"2.0","id":1,"result":"0x` + strings.Repeat("a", 4096) + `"}`
		_, _ = w.Write([]byte(payload))
	}, 512)

	_, err := client.Call(context.Background(), headRequest())
	if !rpc.IsClass(err, rpc.ClassResponseTooLarge) {
		t.Fatalf("err = %v, want response_too_large", err)
	}
}

// §220 HTTP 429 必须映射成 ErrRateLimit 并携带 Retry-After。
func TestRateLimitMapping(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":-32090,"message":"rate limit"}}`))
	}, 1<<20)

	_, err := client.Call(context.Background(), headRequest())
	mapped := rpc.AsError(err)
	if mapped.Class != rpc.ClassRateLimit {
		t.Fatalf("class = %s, want rate_limit", mapped.Class)
	}
	if !mapped.ReduceRate || mapped.RetryAfter != 2*time.Second {
		t.Fatalf("control signals = %+v", mapped)
	}
}

// §64 execution reverted 不得重试。
func TestExecutionRevertedMapping(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted"}}`))
	}, 1<<20)

	_, err := client.Call(context.Background(), rpc.Request{Chain: "bsc", Method: "eth_call",
		Params: []any{map[string]any{"to": "0x1", "data": "0x95d89b41"}, "latest"}, Class: rpc.ClassContractRead})
	mapped := rpc.AsError(err)
	if mapped.Class != rpc.ClassExecutionReverted || mapped.Retryable {
		t.Fatalf("mapped = %+v", mapped)
	}
}

// 自定义错误的身份只在 error.data 里：revert 时保留原始载荷，非 revert 和畸形载荷不保留。
func TestExecutionRevertedKeepsRevertData(t *testing.T) {
	for body, want := range map[string]string{
		`{"code":3,"message":"execution reverted","data":"0x118cdaa7000000000000000000000000000000000000000000000000000000000000dead"}`: "118cdaa7000000000000000000000000000000000000000000000000000000000000dead",
		`{"code":-32000,"message":"execution reverted","data":{"data":"0xe2517d3f"}}`:                                                   "e2517d3f",
		`{"code":3,"message":"execution reverted","data":"0x"}`:                                                                         "",
		`{"code":3,"message":"execution reverted","data":"0xzz"}`:                                                                       "",
		`{"code":3,"message":"execution reverted","data":"0x123"}`:                                                                      "",
		`{"code":-32602,"message":"invalid params","data":"0x118cdaa7"}`:                                                                "",
	} {
		client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":` + body + `}`))
		}, 1<<20)
		_, err := client.Call(context.Background(), rpc.Request{Chain: "bsc", Method: "eth_call",
			Params: []any{map[string]any{"to": "0x1", "data": "0x95d89b41"}, "latest"}, Class: rpc.ClassContractRead})
		if got := hex.EncodeToString(rpc.AsError(err).RevertData); got != want {
			t.Fatalf("body %s: revert data %q, want %q", body, got, want)
		}
	}
}

// §222 Trace 方法上的 -32601 必须映射为 trace_unsupported，供 Router 换 Provider。
func TestTraceUnsupportedMapping(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"the method trace_transaction does not exist/is not available"}}`))
	}, 1<<20)

	_, err := client.Call(context.Background(), rpc.Request{Chain: "bsc", Method: "trace_transaction",
		Params: []any{"0xabc"}, Class: rpc.ClassTrace})
	if !rpc.IsClass(err, rpc.ClassTraceUnsupported) {
		t.Fatalf("err = %v, want trace_unsupported", err)
	}
}

func TestArchiveAndRangeMapping(t *testing.T) {
	archive := mapRPCError(-32000, "missing trie node 0xabc", rpc.ClassBalance)
	if archive.Class != rpc.ClassArchiveRequired {
		t.Fatalf("archive class = %s", archive.Class)
	}
	rangeErr := mapRPCError(-32000, "query returned more than 10000 results", rpc.ClassLogs)
	if rangeErr.Class != rpc.ClassRangeTooLarge || !rangeErr.ReduceRange {
		t.Fatalf("range error = %+v", rangeErr)
	}
}

// §45 Batch 响应乱序时必须按 id 正确映射。
func TestBatchOutOfOrderMapping(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payloads []jsonRPCRequest
		if err := json.Unmarshal(body, &payloads); err != nil {
			t.Errorf("batch body: %v", err)
		}
		// 故意逆序返回，并让其中一项失败（§46）
		var parts []string
		for i := len(payloads) - 1; i >= 0; i-- {
			if i == 1 {
				parts = append(parts, `{"jsonrpc":"2.0","id":`+itoa(payloads[i].ID)+`,"error":{"code":3,"message":"execution reverted"}}`)
				continue
			}
			parts = append(parts, `{"jsonrpc":"2.0","id":`+itoa(payloads[i].ID)+`,"result":"`+payloads[i].Method+`"}`)
		}
		_, _ = w.Write([]byte("[" + strings.Join(parts, ",") + "]"))
	}, 1<<20)

	reqs := []rpc.Request{
		{Chain: "bsc", Method: "eth_blockNumber", Params: []any{}, Class: rpc.ClassChainHead},
		{Chain: "bsc", Method: "eth_chainId", Params: []any{}, Class: rpc.ClassNetwork},
		{Chain: "bsc", Method: "net_version", Params: []any{}, Class: rpc.ClassNetwork},
	}
	responses, err := client.BatchCall(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 3 {
		t.Fatalf("responses = %d", len(responses))
	}
	for i, response := range responses {
		if i == 1 {
			if response.Error == nil || response.Error.Class != rpc.ClassExecutionReverted {
				t.Fatalf("item 1 error = %+v", response.Error)
			}
			continue
		}
		var got string
		if err := json.Unmarshal(response.Result, &got); err != nil {
			t.Fatal(err)
		}
		if got != reqs[i].Method {
			t.Fatalf("response %d mapped to %s, want %s", i, got, reqs[i].Method)
		}
	}
}

// §224 端点脱敏：任何对外暴露都不得包含 API Key。
func TestEndpointRedaction(t *testing.T) {
	client, server := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}, 1<<20)
	_ = server
	safe := client.SafeEndpoint("bsc")
	if strings.Contains(safe, "SUPER_SECRET_API_KEY_VALUE") {
		t.Fatalf("safe endpoint leaks the key: %s", safe)
	}
	if !strings.Contains(safe, "***") {
		t.Fatalf("safe endpoint should mask the credential: %s", safe)
	}
	if secrets := client.Secrets(); len(secrets) != 1 {
		t.Fatalf("secrets = %v", secrets)
	}
}

func TestUnknownChainIsRejected(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {}, 1<<20)
	_, err := client.Call(context.Background(), rpc.Request{Chain: "solana", Method: "eth_blockNumber"})
	if !rpc.IsClass(err, rpc.ClassMethodUnsupported) {
		t.Fatalf("err = %v, want method_unsupported", err)
	}
}

func TestInvalidJSONMapping(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	}, 1<<20)
	_, err := client.Call(context.Background(), headRequest())
	if !rpc.IsClass(err, rpc.ClassInvalidResponse) {
		t.Fatalf("err = %v, want invalid_response", err)
	}
}

func itoa(v int64) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
