package ankrrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.Number     `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
}

// Client Ankr Chain RPC 适配器。实现 rpc.Provider。
type Client struct {
	opts   Options
	http   *http.Client
	nextID atomic.Int64

	requests    atomic.Int64
	failures    atomic.Int64
	lastErr     atomic.Value // string
	lastErrTime atomic.Value // time.Time
}

// New 构造客户端。HTTP Client 全局复用，禁止每请求新建（§122/§123）。
func New(opts Options) *Client {
	opts = opts.withDefaults()
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          opts.MaxIdleConns,
		MaxIdleConnsPerHost:   opts.MaxIdleConnsPerHost,
		MaxConnsPerHost:       opts.MaxConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
	}
	c := &Client{opts: opts, http: &http.Client{Transport: transport, Timeout: opts.RequestTimeout}}
	c.lastErr.Store("")
	c.lastErrTime.Store(time.Time{})
	return c
}

func (c *Client) Name() string { return "ankr" }

// Secrets 需要从日志中抹掉的明文（装配层传给 Gateway，§31）。
func (c *Client) Secrets() []string {
	if c.opts.APIKey == "" {
		return nil
	}
	return []string{c.opts.APIKey}
}

// Call 单次 JSON-RPC 调用。
func (c *Client) Call(ctx context.Context, req rpc.Request) (rpc.Response, error) {
	endpoint, ok, resolveErr := c.endpoint(ctx, req.Chain)
	if resolveErr != nil {
		return rpc.Response{}, &rpc.Error{Class: rpc.ClassNoProvider, Provider: c.Name(), Message: "provider configuration unavailable"}
	}
	if !ok {
		return rpc.Response{}, &rpc.Error{Class: rpc.ClassMethodUnsupported, Provider: c.Name(),
			Chain: req.Chain, Method: req.Method, Message: "chain is not mapped to an ankr rpc path"}
	}
	payload := jsonRPCRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: req.Method, Params: normalizeParams(req.Params)}
	body, err := json.Marshal(payload)
	if err != nil {
		return rpc.Response{}, &rpc.Error{Class: rpc.ClassInvalidParams, Provider: c.Name(),
			Chain: req.Chain, Method: req.Method, Message: "cannot encode rpc request"}
	}
	raw, size, httpErr := c.post(ctx, endpoint, body, req)
	if httpErr != nil {
		return rpc.Response{}, httpErr
	}
	var decoded jsonRPCResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		c.recordFailure("invalid json")
		return rpc.Response{Bytes: size}, &rpc.Error{Class: rpc.ClassInvalidResponse, Provider: c.Name(),
			Chain: req.Chain, Method: req.Method, Retryable: true, Message: "provider returned invalid json"}
	}
	if decoded.Error != nil {
		mapped := withRevertData(mapRPCError(decoded.Error.Code, decoded.Error.Message, req.Class), decoded.Error.Data)
		mapped.Provider, mapped.Chain, mapped.Method = c.Name(), req.Chain, req.Method
		mapped.Message = c.redactEndpointMessage(mapped.Message, endpoint)
		c.recordFailure(mapped.Message)
		return rpc.Response{Bytes: size}, mapped
	}
	c.requests.Add(1)
	return rpc.Response{Result: decoded.Result, Provider: c.Name(), Chain: req.Chain,
		Method: req.Method, Bytes: size}, nil
}

// BatchCall JSON-RPC Batch。响应顺序不可假设，必须按 id 映射（§45）；
// 部分失败逐项返回，不让整批失败（§46）。
func (c *Client) BatchCall(ctx context.Context, reqs []rpc.Request) ([]rpc.Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	endpoint, ok, resolveErr := c.endpoint(ctx, reqs[0].Chain)
	if resolveErr != nil {
		return nil, &rpc.Error{Class: rpc.ClassNoProvider, Provider: c.Name(), Message: "provider configuration unavailable"}
	}
	if !ok {
		return nil, &rpc.Error{Class: rpc.ClassMethodUnsupported, Provider: c.Name(), Chain: reqs[0].Chain,
			Message: "chain is not mapped to an ankr rpc path"}
	}
	payloads := make([]jsonRPCRequest, len(reqs))
	indexByID := make(map[int64]int, len(reqs))
	for i, req := range reqs {
		id := c.nextID.Add(1)
		payloads[i] = jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: req.Method, Params: normalizeParams(req.Params)}
		indexByID[id] = i
	}
	body, err := json.Marshal(payloads)
	if err != nil {
		return nil, &rpc.Error{Class: rpc.ClassInvalidParams, Provider: c.Name(), Chain: reqs[0].Chain,
			Message: "cannot encode rpc batch"}
	}
	raw, size, httpErr := c.post(ctx, endpoint, body, reqs[0])
	if httpErr != nil {
		return nil, httpErr
	}
	var decoded []jsonRPCResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		// 某些 Provider 在整批错误时返回单个对象
		var single jsonRPCResponse
		if err2 := json.Unmarshal(raw, &single); err2 == nil && single.Error != nil {
			mapped := withRevertData(mapRPCError(single.Error.Code, single.Error.Message, reqs[0].Class), single.Error.Data)
			mapped.Provider, mapped.Chain = c.Name(), reqs[0].Chain
			mapped.Message = c.redactEndpointMessage(mapped.Message, endpoint)
			c.recordFailure(mapped.Message)
			return nil, mapped
		}
		c.recordFailure("invalid batch json")
		return nil, &rpc.Error{Class: rpc.ClassInvalidResponse, Provider: c.Name(), Chain: reqs[0].Chain,
			Retryable: true, Message: "provider returned invalid batch json"}
	}
	c.requests.Add(1)

	out := make([]rpc.Response, len(reqs))
	seen := make([]bool, len(reqs))
	perItemBytes := size / maxIntValue(len(reqs), 1)
	for _, item := range decoded {
		id, convErr := strconv.ParseInt(item.ID.String(), 10, 64)
		if convErr != nil {
			continue
		}
		idx, known := indexByID[id]
		if !known {
			continue
		}
		seen[idx] = true
		response := rpc.Response{Provider: c.Name(), Chain: reqs[idx].Chain, Method: reqs[idx].Method,
			Bytes: perItemBytes, BatchSize: len(reqs)}
		if item.Error != nil {
			mapped := withRevertData(mapRPCError(item.Error.Code, item.Error.Message, reqs[idx].Class), item.Error.Data)
			mapped.Message = c.redactEndpointMessage(mapped.Message, endpoint)
			mapped.Provider, mapped.Chain, mapped.Method = c.Name(), reqs[idx].Chain, reqs[idx].Method
			response.Error = mapped
		} else {
			response.Result = item.Result
		}
		out[idx] = response
	}
	for i := range out {
		if !seen[i] {
			out[i] = rpc.Response{Provider: c.Name(), Chain: reqs[i].Chain, Method: reqs[i].Method,
				Error: &rpc.Error{Class: rpc.ClassInvalidResponse, Provider: c.Name(), Chain: reqs[i].Chain,
					Method: reqs[i].Method, Message: "batch response did not include this call id"}}
		}
	}
	return out, nil
}

// post 发送请求并做响应体硬限制（§120：不得截断 JSON 后报 bad json）。
func (c *Client) post(ctx context.Context, endpoint string, body []byte, req rpc.Request) ([]byte, int, *rpc.Error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, &rpc.Error{Class: rpc.ClassInvalidParams, Provider: c.Name(), Chain: req.Chain,
			Method: req.Method, Message: "cannot build rpc request"}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	res, err := c.http.Do(httpReq)
	if err != nil {
		mapped := mapContextError(ctx, err)
		mapped.Provider, mapped.Chain, mapped.Method = c.Name(), req.Chain, req.Method
		// 错误串可能内嵌带 Key 的 URL
		mapped.Message = c.redactEndpointMessage(mapped.Message, endpoint)
		mapped.Cause = nil
		c.recordFailure(mapped.Message)
		return nil, 0, mapped
	}
	defer res.Body.Close()

	if res.ContentLength > 0 && res.ContentLength > c.opts.ResponseHardLimit {
		c.recordFailure("response too large")
		return nil, 0, &rpc.Error{Class: rpc.ClassResponseTooLarge, Provider: c.Name(), Chain: req.Chain,
			Method: req.Method, HTTPStatus: res.StatusCode,
			Message: fmt.Sprintf("content-length %d exceeds hard limit %d", res.ContentLength, c.opts.ResponseHardLimit)}
	}
	raw, exceeded, readErr := readBodyLimit(res.Body, c.opts.ResponseHardLimit)
	if readErr != nil {
		mapped := mapContextError(ctx, readErr)
		mapped.Provider, mapped.Chain, mapped.Method = c.Name(), req.Chain, req.Method
		mapped.Message = c.redactEndpointMessage(mapped.Message, endpoint)
		c.recordFailure(mapped.Message)
		return nil, 0, mapped
	}
	if exceeded {
		c.recordFailure("response too large")
		return nil, 0, &rpc.Error{Class: rpc.ClassResponseTooLarge, Provider: c.Name(), Chain: req.Chain,
			Method: req.Method, HTTPStatus: res.StatusCode,
			Message: "response body exceeded the configured hard limit"}
	}
	if res.StatusCode != http.StatusOK {
		mapped := mapHTTPError(res.StatusCode, raw, parseRetryAfter(res.Header))
		mapped.Provider, mapped.Chain, mapped.Method = c.Name(), req.Chain, req.Method
		mapped.Message = c.redactEndpointMessage(mapped.Message, endpoint)
		c.recordFailure(mapped.Message)
		return nil, len(raw), mapped
	}
	return raw, len(raw), nil
}

// readBodyLimit 读取至多 limit+1 字节；超出返回 exceeded=true（§120）。
func readBodyLimit(r io.Reader, limit int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return nil, true, nil
	}
	return body, false, nil
}

// normalizeParams JSON-RPC 需要位置参数数组，nil 必须编码成 []。
func normalizeParams(params []any) []any {
	if params == nil {
		return []any{}
	}
	return params
}

func (c *Client) recordFailure(message string) {
	c.failures.Add(1)
	c.lastErr.Store(rpc.Redact(message, c.Secrets()...))
	c.lastErrTime.Store(time.Now())
}

func (c *Client) redactEndpointMessage(message, endpoint string) string {
	secrets := append([]string{}, c.Secrets()...)
	if last := strings.LastIndex(endpoint, "/"); last >= 0 && len(endpoint)-last-1 >= 8 {
		secrets = append(secrets, endpoint[last+1:])
	}
	return rpc.Redact(strings.ReplaceAll(message, endpoint, rpc.RedactURL(endpoint)), secrets...)
}

func maxIntValue(a, b int) int {
	if a > b {
		return a
	}
	return b
}
