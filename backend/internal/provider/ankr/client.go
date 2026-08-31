// Package ankr 实现 Ankr Advanced API Provider（V1.1：响应体保护 + 官方错误码分类 + 自适应 pageSize）。
package ankr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

type Options struct {
	Endpoint     string
	APIKey       string
	MaxPageSize  int
	RequestsPerMinute int
	RequestTimeout    time.Duration
	ResponseHeaderTimeout time.Duration
	ResponseSoftLimitBytes int64
	ResponseHardLimitBytes int64
	CreditsPerRequest float64
	CostPerRequestUSD float64
}

type Client struct {
	endpoint    string
	apiKey      string
	maxPageSize int
	softLimit   int64
	hardLimit   int64
	http        *http.Client
	limiter     *TokenBucket
	breaker     *circuitBreaker
	usageHook   provider.UsageHook
	credits     float64
	costUSD     float64
	idCounter   atomic.Int64
	lastErr     atomic.Value // string
	lastErrAt   atomic.Value // time.Time
	consecutive atomic.Int64
	connLimits  atomic.Int64
	rateLimits  atomic.Int64
	timeouts    atomic.Int64
	tooLarge    atomic.Int64
}

func New(opts Options, hook provider.UsageHook) *Client {
	if opts.MaxPageSize <= 0 {
		opts.MaxPageSize = 10000
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 60 * time.Second
	}
	if opts.ResponseHeaderTimeout <= 0 {
		opts.ResponseHeaderTimeout = opts.RequestTimeout
	}
	if opts.ResponseSoftLimitBytes <= 0 {
		opts.ResponseSoftLimitBytes = 32 << 20
	}
	if opts.ResponseHardLimitBytes <= 0 {
		opts.ResponseHardLimitBytes = 64 << 20
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 128,
		MaxConnsPerHost:     128,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
	}
	c := &Client{
		endpoint:    strings.TrimSuffix(opts.Endpoint, "/"),
		apiKey:      opts.APIKey,
		maxPageSize: opts.MaxPageSize,
		softLimit:   opts.ResponseSoftLimitBytes,
		hardLimit:   opts.ResponseHardLimitBytes,
		http:        &http.Client{Transport: transport, Timeout: opts.RequestTimeout},
		limiter:     NewTokenBucket(opts.RequestsPerMinute),
		breaker:     newCircuitBreaker(40, 0.25, 30*time.Second),
		usageHook:   hook,
		credits:     opts.CreditsPerRequest,
		costUSD:     opts.CostPerRequestUSD,
	}
	c.lastErr.Store("")
	c.lastErrAt.Store(time.Time{})
	return c
}

func (c *Client) Name() string { return "ankr" }

func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Name:        "ankr",
		Datasets:    model.AllDatasets,
		MaxPageSize: c.maxPageSize,
		SupportsTimeRange: map[string]bool{
			"transactions": true, "token_transfers": true,
			"nft_transfers": false, "logs": false, "balances": false,
		},
	}
}

// 控制信号计数（供 Rate/Concurrency 控制器读取）
func (c *Client) ConnectionLimitCount() int64 { return c.connLimits.Load() }
func (c *Client) RateLimitCount() int64       { return c.rateLimits.Load() }
func (c *Client) TimeoutCount() int64         { return c.timeouts.Load() }
func (c *Client) TooLargeCount() int64        { return c.tooLarge.Load() }

var methodByDataset = map[string]string{
	"transactions":    "ankr_getTransactionsByAddress",
	"token_transfers": "ankr_getTokenTransfers",
	"nft_transfers":   "ankr_getNftTransfers",
	"logs":            "ankr_getLogs",
	"balances":        "ankr_getAccountBalance",
}

// readBodyWithLimit 读取至多 maxBytes+1：超过返回 exceeded=true（防止 64MiB 截断被误报 bad json，§6）。
func readBodyWithLimit(r io.Reader, maxBytes int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > maxBytes {
		return nil, true, nil
	}
	return body, false, nil
}

func (c *Client) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	ok, state := c.breaker.allow()
	if !ok {
		return provider.FetchResult{}, &provider.Error{
			Provider: "ankr", Class: provider.ClassUpstream,
			Message: fmt.Sprintf("provider circuit breaker %s", state),
			Retryable: true, ReduceConcurrency: true,
		}
	}
	if err := c.limiter.Acquire(ctx); err != nil {
		return provider.FetchResult{}, c.classifyCtxErr(ctx, err, req.PageSize)
	}
	method, known := methodByDataset[req.Dataset]
	if !known {
		return provider.FetchResult{}, &provider.Error{Provider: "ankr", Class: provider.ClassInvalidRequest,
			Message: "unknown dataset " + req.Dataset, Retryable: false}
	}

	params := c.buildParams(req)
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: int(c.idCounter.Add(1)), Method: method, Params: params})
	if err != nil {
		return provider.FetchResult{}, err
	}

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/"+c.apiKey, bytes.NewReader(body))
	if err != nil {
		return provider.FetchResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	observability.AnkrInflight.Inc()
	res, err := c.http.Do(httpReq)
	observability.AnkrInflight.Dec()
	elapsed := time.Since(start)
	observability.AnkrRequestDuration.Observe(elapsed.Seconds())
	if err != nil {
		pe := c.classifyCtxErr(ctx, err, req.PageSize)
		pe.Duration = elapsed
		pe.Provider = "ankr"
		c.recordFailure(pe)
		c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, 0)
		return provider.FetchResult{}, pe
	}
	defer res.Body.Close()

	// Content-Length 快速拒绝（§7；Chunked 无 CL 时靠 maxBytes+1 兜底）
	if res.ContentLength > 0 && res.ContentLength > c.hardLimit {
		pe := provider.NewResponseTooLarge(
			fmt.Sprintf("content-length %d exceeds hard limit %d", res.ContentLength, c.hardLimit),
			req.PageSize, res.ContentLength)
		pe.Provider = "ankr"
		pe.HTTPStatus = res.StatusCode
		c.recordFailure(pe)
		c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, 0)
		return provider.FetchResult{}, pe
	}

	respBody, exceeded, rerr := readBodyWithLimit(res.Body, c.hardLimit)
	if rerr != nil {
		pe := &provider.Error{Provider: "ankr", Class: provider.ClassUpstream,
			Message: "read response: " + rerr.Error(), Retryable: true}
		c.recordFailure(pe)
		c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, 0)
		return provider.FetchResult{}, pe
	}
	respBytes := len(respBody)
	observability.AnkrResponseBytes.Observe(float64(respBytes))

	if exceeded {
		pe := provider.NewResponseTooLarge(
			fmt.Sprintf("response body exceeded hard limit %d", c.hardLimit),
			req.PageSize, int64(respBytes))
		pe.Provider = "ankr"
		pe.HTTPStatus = res.StatusCode
		c.recordFailure(pe)
		c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, respBytes)
		return provider.FetchResult{}, pe
	}

	retryAfter := parseRetryAfter(res.Header)
	if res.StatusCode == http.StatusOK {
		var rpc rpcResponse
		if err := json.Unmarshal(respBody, &rpc); err != nil {
			// 未超硬限制、未被截断、解析仍失败 → 才是真正 InvalidJSON（§9）
			pe := &provider.Error{Provider: "ankr", Class: provider.ClassInvalidJSON,
				Message: "invalid json: " + err.Error(), Retryable: true,
				BodyBytes: int64(respBytes), Cause: err, Kind: provider.ErrTemporary}
			c.recordFailure(pe)
			c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, respBytes)
			return provider.FetchResult{}, pe
		}
		if rpc.Error != nil {
			pe := c.classifyRPCError(rpc.Error, retryAfter)
			pe.Duration = elapsed
			pe.BodyBytes = int64(respBytes)
			c.recordFailure(pe)
			c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, respBytes)
			return provider.FetchResult{}, pe
		}
		result, n, perr := c.parseResult(req.Dataset, rpc.Result)
		if perr != nil {
			pe := &provider.Error{Provider: "ankr", Class: provider.ClassInvalidJSON,
				Message: "parse result: " + perr.Error(), Retryable: true, Cause: perr, Kind: provider.ErrTemporary}
			c.recordFailure(pe)
			c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, respBytes)
			return provider.FetchResult{}, pe
		}
		c.recordSuccess()
		c.recordUsage(ctx, req, method, "ok", nil, elapsed, n, respBytes)
		observability.AnkrResponseRows.Observe(float64(n))
		return result, nil
	}

	pe := c.classifyHTTPError(res.StatusCode, respBody, retryAfter)
	pe.Duration = elapsed
	pe.BodyBytes = int64(respBytes)
	c.recordFailure(pe)
	c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, respBytes)
	return provider.FetchResult{}, pe
}

func (c *Client) buildParams(req provider.FetchRequest) map[string]any {
	params := map[string]any{}
	if req.Dataset == "balances" {
		params["blockchain"] = req.Chain
		params["walletAddress"] = req.Address
		return params
	}
	params["blockchain"] = req.Chain
	params["address"] = req.Address
	if req.FromBlock != nil {
		params["fromBlock"] = fmt.Sprintf("0x%x", *req.FromBlock)
	}
	if req.ToBlock != nil {
		params["toBlock"] = fmt.Sprintf("0x%x", *req.ToBlock)
	}
	if req.FromTime != nil && c.Capabilities().SupportsTimeRange[req.Dataset] {
		params["fromTimestamp"] = req.FromTime.UnixMilli()
	}
	if req.ToTime != nil && c.Capabilities().SupportsTimeRange[req.Dataset] {
		params["toTimestamp"] = req.ToTime.UnixMilli()
	}
	params["pageSize"] = req.PageSize
	if req.PageToken != "" {
		params["pageToken"] = req.PageToken
	}
	return params
}

func (c *Client) parseResult(dataset string, raw []byte) (provider.FetchResult, int, error) {
	if dataset == "balances" {
		var br balanceResult
		if err := json.Unmarshal(raw, &br); err != nil {
			return provider.FetchResult{}, 0, err
		}
		return provider.FetchResult{Rows: br.Assets, HasMore: false}, len(br.Assets), nil
	}
	var pr pagedResult
	if err := json.Unmarshal(raw, &pr); err != nil {
		return provider.FetchResult{}, 0, err
	}
	var rows []json.RawMessage
	switch dataset {
	case "transactions":
		rows = pr.Transactions
	case "token_transfers", "nft_transfers":
		rows = pr.Transfers
	case "logs":
		rows = pr.Logs
	}
	return provider.FetchResult{Rows: rows, NextPageToken: pr.NextPageToken, HasMore: pr.NextPageToken != ""}, len(rows), nil
}

// classifyCtxErr 优先 context 错误（§10/§25 优先级 1）。
func (c *Client) classifyCtxErr(ctx context.Context, err error, pageSize int) *provider.Error {
	if errors.Is(err, context.Canceled) {
		return provider.NewCancelled("request cancelled: " + err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return provider.NewTimeout("request deadline exceeded", pageSize, err)
	}
	return &provider.Error{Provider: "ankr", Class: provider.ClassUpstream,
		Message: err.Error(), Retryable: true, Kind: provider.ErrTemporary}
}

// classifyRPCError 官方错误码映射（§2/§22/§23，优先级 3）。
func (c *Client) classifyRPCError(e *rpcError, retryAfter time.Duration) *provider.Error {
	pe := &provider.Error{
		Provider: "ankr", Message: e.Message, RPCCode: e.Code, TraceID: e.Data.TraceID,
		Retryable: true, RetryAfter: retryAfter,
	}
	switch e.Code {
	case -32090:
		pe.Class = provider.ClassRateLimit
		pe.ReduceRate = true
	case -32005:
		// legacy compatibility：非当前官方请求频率主码（§22）
		pe.Class = provider.ClassRateLimit
		pe.ReduceRate = true
	case -32067, -32068, -32069, -32077, -32070:
		pe.Class = provider.ClassConnectionLimit
		pe.ReduceConcurrency = true
	case -32062:
		pe.Class = provider.ClassRequestTooLarge
		pe.SplitRange = true
	case -32071:
		pe.Class = provider.ClassRequestTimeout
		pe.ReducePageSize = true
	case -32055, -32056, -32057, -32059, -32061, -32063, -32064, -32076:
		pe.Class = provider.ClassUpstream
	default:
		c.classifyByMessage(pe)
	}
	if pe.HTTPStatus == 0 {
		pe.HTTPStatus = http.StatusOK // JSON-RPC 错误随 200 返回
	}
	return pe
}

// classifyHTTPError 非 200 分类（§24：HTTP 429 先解析 JSON-RPC 错误码再兜底）。
func (c *Client) classifyHTTPError(code int, body []byte, retryAfter time.Duration) *provider.Error {
	if code == http.StatusTooManyRequests {
		var rpc rpcResponse
		if err := json.Unmarshal(body, &rpc); err == nil && rpc.Error != nil {
			pe := c.classifyRPCError(rpc.Error, retryAfter)
			pe.HTTPStatus = code
			observability.Ankr429.WithLabelValues("ankr", "").Inc()
			return pe
		}
		c.rateLimits.Add(1)
		observability.Ankr429.WithLabelValues("ankr", "").Inc()
		return &provider.Error{Provider: "ankr", Class: provider.ClassRateLimit,
			Message: fmt.Sprintf("http 429 rate limited"), HTTPStatus: code,
			Retryable: true, ReduceRate: true, RetryAfter: retryAfter, Kind: provider.ErrRateLimit}
	}
	switch {
	case code == http.StatusRequestEntityTooLarge:
		return &provider.Error{Provider: "ankr", Class: provider.ClassRequestTooLarge,
			Message: "http 413 request too large", HTTPStatus: code, Retryable: true, SplitRange: true}
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return &provider.Error{Provider: "ankr", Class: provider.ClassAuth,
			Message: "authentication failed (check ANKR_API_KEY)", HTTPStatus: code, Retryable: false, Kind: provider.ErrAuth}
	case code == http.StatusBadRequest:
		return &provider.Error{Provider: "ankr", Class: provider.ClassInvalidRequest,
			Message: "bad request: " + truncate(string(body), 300), HTTPStatus: code, Retryable: false, Kind: provider.ErrInvalidParams}
	case code >= 500:
		return &provider.Error{Provider: "ankr", Class: provider.ClassUpstream,
			Message: fmt.Sprintf("http %d", code), HTTPStatus: code, Retryable: true, Kind: provider.ErrTemporary}
	default:
		return &provider.Error{Provider: "ankr", Class: provider.ClassUpstream,
			Message: fmt.Sprintf("http %d: %s", code, truncate(string(body), 300)), HTTPStatus: code,
			Retryable: true, Kind: provider.ErrTemporary}
	}
}

// classifyByMessage 字符串匹配只作兜底（§25 优先级 5）。
func (c *Client) classifyByMessage(pe *provider.Error) {
	low := strings.ToLower(pe.Message)
	switch {
	case strings.Contains(low, "rate") || strings.Contains(low, "limit") || strings.Contains(low, "too many"):
		pe.Class = provider.ClassRateLimit
		pe.ReduceRate = true
	case strings.Contains(low, "connection") || strings.Contains(low, "connections"):
		pe.Class = provider.ClassConnectionLimit
		pe.ReduceConcurrency = true
	case strings.Contains(low, "auth") || strings.Contains(low, "api key") || strings.Contains(low, "unauthorized"):
		pe.Class = provider.ClassAuth
		pe.Retryable = false
	case strings.Contains(low, "invalid") || strings.Contains(low, "param") || strings.Contains(low, "unsupported"):
		pe.Class = provider.ClassInvalidRequest
		pe.Retryable = false
	default:
		pe.Class = provider.ClassUpstream
	}
}

func parseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func (c *Client) recordSuccess() {
	c.breaker.record(true)
	c.breaker.update()
	c.consecutive.Store(0)
}

func (c *Client) recordFailure(pe *provider.Error) {
	c.breaker.record(false)
	c.breaker.update()
	c.consecutive.Add(1)
	c.lastErr.Store(pe.Message)
	c.lastErrAt.Store(time.Now())
	// 错误类计数（§35）
	switch pe.Class {
	case provider.ClassResponseTooLarge:
		c.tooLarge.Add(1)
		observability.AnkrResponseTooLarge.Inc()
	case provider.ClassRequestTimeout:
		c.timeouts.Add(1)
		observability.AnkrRequestTimeout.Inc()
	case provider.ClassInvalidJSON:
		observability.AnkrInvalidJSON.Inc()
	case provider.ClassRateLimit:
		c.rateLimits.Add(1)
		observability.AnkrRateLimit.Inc()
	case provider.ClassConnectionLimit:
		c.connLimits.Add(1)
		observability.AnkrConnectionLimit.Inc()
	}
	if pe.HTTPStatus == http.StatusTooManyRequests {
		observability.Ankr429.WithLabelValues("ankr", "").Inc()
	}
	if pe.HTTPStatus >= 500 {
		observability.Ankr5xx.WithLabelValues("ankr", "").Inc()
	}
}

// recordUsage 写用量表（§34 增强字段）+ Prometheus（§35）。
func (c *Client) recordUsage(ctx context.Context, req provider.FetchRequest, method, status string, pe *provider.Error, elapsed time.Duration, rows, respBytes int) {
	observability.AnkrRequests.WithLabelValues("ankr", req.Dataset, status).Inc()
	observability.AnkrLatencyMs.WithLabelValues("ankr", req.Dataset).Observe(float64(elapsed.Milliseconds()))
	if status == "ok" {
		observability.AnkrRequestsSuccess.WithLabelValues("ankr", req.Dataset).Inc()
		observability.AnkrRows.WithLabelValues("ankr", req.Dataset).Add(float64(rows))
		observability.AnkrBytes.WithLabelValues("ankr", req.Dataset).Add(float64(respBytes))
	} else {
		observability.AnkrRequestsFailed.WithLabelValues("ankr", req.Dataset).Inc()
	}

	var jobID, shardID string
	if v := ctx.Value("job_id"); v != nil {
		jobID, _ = v.(string)
	}
	if v := ctx.Value("shard_id"); v != nil {
		shardID, _ = v.(string)
	}
	attempt := 0
	if v := ctx.Value("retry_attempt"); v != nil {
		if a, ok := v.(int); ok {
			attempt = a
		}
	}
	u := model.UsageRow{
		Provider: "ankr", Method: method, Dataset: req.Dataset,
		JobID: jobID, ShardID: shardID,
		RequestTime: time.Now(), DurationMs: int(elapsed.Milliseconds()),
		Status: status, Rows: rows, Bytes: respBytes,
		PageSize: req.PageSize, ResponseBytes: respBytes, RetryAttempt: attempt,
		Credits: c.credits, CostUSD: c.costUSD,
	}
	if pe != nil {
		u.HTTPStatus = pe.HTTPStatus
		u.APIError = pe.Message
		u.ErrorClass = string(pe.Class)
		u.RPCCode = pe.RPCCode
		u.TraceID = pe.TraceID
	}
	if c.usageHook != nil {
		c.usageHook(u)
	}
}

func (c *Client) Health(ctx context.Context) provider.HealthStatus {
	st := c.breaker.stateStr()
	if st == "" {
		st = "NORMAL"
	}
	var lastErr string
	if v, ok := c.lastErr.Load().(string); ok {
		lastErr = v
	}
	_ = ctx
	return provider.HealthStatus{
		Provider:           "ankr",
		State:              provider.HealthState(st),
		LastCheck:          time.Now(),
		Error:              lastErr,
		ConsecutiveFailure: int(c.consecutive.Load()),
		RecentErrorRate:    c.breaker.errorRate(),
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
