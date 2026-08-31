// Package ankr 实现 Ankr Advanced API Provider。
package ankr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
	CreditsPerRequest float64
	CostPerRequestUSD float64
}

type Client struct {
	endpoint    string
	apiKey      string
	maxPageSize int
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
}

func New(opts Options, hook provider.UsageHook) *Client {
	if opts.MaxPageSize <= 0 {
		opts.MaxPageSize = 10000
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        512,
		MaxIdleConnsPerHost: 256,
		MaxConnsPerHost:     256,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: opts.RequestTimeout,
	}
	c := &Client{
		endpoint:    strings.TrimSuffix(opts.Endpoint, "/"),
		apiKey:      opts.APIKey,
		maxPageSize: opts.MaxPageSize,
		http:        &http.Client{Transport: transport, Timeout: opts.RequestTimeout},
		limiter:     NewTokenBucket(opts.RequestsPerMinute),
		breaker:     newCircuitBreaker(40, 0.25, 30*time.Second),
		usageHook:   hook,
		credits:     opts.CreditsPerRequest,
		costUSD:     opts.CostPerRequestUSD,
	}
	c.lastErr.Store("")
	t := time.Time{}
	c.lastErrAt.Store(t)
	return c
}

func (c *Client) Name() string { return "ankr" }

func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Name:        "ankr",
		Datasets:    model.AllDatasets,
		MaxPageSize: c.maxPageSize,
		SupportsTimeRange: map[string]bool{
			"transactions":    true,
			"token_transfers": true,
			"nft_transfers":   false,
			"logs":            false,
			"balances":        false,
		},
	}
}

var methodByDataset = map[string]string{
	"transactions":    "ankr_getTransactionsByAddress",
	"token_transfers": "ankr_getTokenTransfers",
	"nft_transfers":   "ankr_getNftTransfers",
	"logs":            "ankr_getLogs",
	"balances":        "ankr_getAccountBalance",
}

func (c *Client) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	ok, state := c.breaker.allow()
	if !ok {
		return provider.FetchResult{}, &provider.Error{Kind: provider.ErrCircuitOpen,
			Message: fmt.Sprintf("provider circuit breaker %s", state)}
	}
	if err := c.limiter.Acquire(ctx); err != nil {
		return provider.FetchResult{}, err
	}
	method, known := methodByDataset[req.Dataset]
	if !known {
		return provider.FetchResult{}, &provider.Error{Kind: provider.ErrInvalidParams, Message: "unknown dataset " + req.Dataset}
	}

	params := map[string]any{}
	if req.Dataset == "balances" {
		params["blockchain"] = req.Chain
		params["walletAddress"] = req.Address
	} else {
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
		if req.Dataset != "logs" {
			params["pageSize"] = req.PageSize
			if req.PageToken != "" {
				params["pageToken"] = req.PageToken
			}
		} else {
			params["pageSize"] = req.PageSize
			if req.PageToken != "" {
				params["pageToken"] = req.PageToken
			}
		}
	}

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: int(c.idCounter.Add(1)), Method: method, Params: params})
	if err != nil {
		return provider.FetchResult{}, err
	}

	start := time.Now()
	var status string
	var rows int
	var respBytes int
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/"+c.apiKey, bytes.NewReader(body))
	if err != nil {
		return provider.FetchResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(httpReq)
	if err != nil {
		c.recordFailure(err)
		return provider.FetchResult{}, &provider.Error{Kind: provider.ErrTemporary, Message: err.Error()}
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		c.recordFailure(err)
		return provider.FetchResult{}, &provider.Error{Kind: provider.ErrTemporary, Message: err.Error()}
	}
	respBytes = len(respBody)
	elapsed := time.Since(start)

	if res.StatusCode == http.StatusOK {
		var rpc rpcResponse
		if err := json.Unmarshal(respBody, &rpc); err != nil {
			c.recordFailure(err)
			return provider.FetchResult{}, &provider.Error{Kind: provider.ErrTemporary, Message: "bad json: " + err.Error(), HTTPStatus: res.StatusCode}
		}
		if rpc.Error != nil {
			pe := c.classifyRPCError(rpc.Error)
			c.recordFailure(pe)
			status = "error"
			c.recordUsage(ctx, req, method, status, elapsed, res.StatusCode, pe.Message, 0, respBytes)
			return provider.FetchResult{}, pe
		}
		result, n, err := c.parseResult(req.Dataset, rpc.Result)
		if err != nil {
			c.recordFailure(err)
			status = "error"
			c.recordUsage(ctx, req, method, status, elapsed, res.StatusCode, err.Error(), 0, respBytes)
			return provider.FetchResult{}, &provider.Error{Kind: provider.ErrTemporary, Message: err.Error()}
		}
		rows = n
		status = "ok"
		c.recordSuccess()
		c.recordUsage(ctx, req, method, status, elapsed, res.StatusCode, "", rows, respBytes)
		return result, nil
	}

	pe := c.classifyHTTPStatus(res.StatusCode, string(respBody))
	c.recordFailure(pe)
	status = "error"
	c.recordUsage(ctx, req, method, status, elapsed, res.StatusCode, pe.Message, 0, respBytes)
	return provider.FetchResult{}, pe
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

func (c *Client) classifyHTTPStatus(code int, body string) *provider.Error {
	switch {
	case code == http.StatusTooManyRequests:
		return &provider.Error{Kind: provider.ErrRateLimit, Message: fmt.Sprintf("http %d rate limited", code), HTTPStatus: code}
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return &provider.Error{Kind: provider.ErrAuth, Message: "authentication failed (check ANKR_API_KEY)", HTTPStatus: code}
	case code == http.StatusBadRequest:
		return &provider.Error{Kind: provider.ErrInvalidParams, Message: "bad request: " + truncate(body, 300), HTTPStatus: code}
	case code >= 500:
		return &provider.Error{Kind: provider.ErrTemporary, Message: fmt.Sprintf("http %d", code), HTTPStatus: code}
	default:
		return &provider.Error{Kind: provider.ErrTemporary, Message: fmt.Sprintf("http %d: %s", code, truncate(body, 300)), HTTPStatus: code}
	}
}

func (c *Client) classifyRPCError(e *rpcError) *provider.Error {
	msg := strings.ToLower(e.Message)
	switch {
	case e.Code == -32005 || e.Code == 429 || strings.Contains(msg, "rate") || strings.Contains(msg, "limit") || strings.Contains(msg, "too many"):
		return &provider.Error{Kind: provider.ErrRateLimit, Message: e.Message}
	case strings.Contains(msg, "auth") || strings.Contains(msg, "api key") || strings.Contains(msg, "unauthorized"):
		return &provider.Error{Kind: provider.ErrAuth, Message: e.Message}
	case strings.Contains(msg, "invalid") || strings.Contains(msg, "param") || strings.Contains(msg, "unsupported"):
		return &provider.Error{Kind: provider.ErrInvalidParams, Message: e.Message}
	default:
		return &provider.Error{Kind: provider.ErrTemporary, Message: e.Message}
	}
}

func (c *Client) recordSuccess() {
	c.breaker.record(true)
	c.breaker.update()
	c.consecutive.Store(0)
}

func (c *Client) recordFailure(err error) {
	c.breaker.record(false)
	c.breaker.update()
	c.consecutive.Add(1)
	c.lastErr.Store(err.Error())
	c.lastErrAt.Store(time.Now())
}

func (c *Client) recordUsage(ctx context.Context, req provider.FetchRequest, method, status string, elapsed time.Duration, httpStatus int, apiErr string, rows, respBytes int) {
	// Prometheus 指标（§74）
	observability.AnkrRequests.WithLabelValues("ankr", req.Dataset, status).Inc()
	observability.AnkrLatencyMs.WithLabelValues("ankr", req.Dataset).Observe(float64(elapsed.Milliseconds()))
	if status == "ok" {
		observability.AnkrRequestsSuccess.WithLabelValues("ankr", req.Dataset).Inc()
		observability.AnkrRows.WithLabelValues("ankr", req.Dataset).Add(float64(rows))
		observability.AnkrBytes.WithLabelValues("ankr", req.Dataset).Add(float64(respBytes))
	} else {
		observability.AnkrRequestsFailed.WithLabelValues("ankr", req.Dataset).Inc()
	}
	if httpStatus == http.StatusTooManyRequests {
		observability.Ankr429.WithLabelValues("ankr", req.Dataset).Inc()
	}
	if httpStatus >= 500 {
		observability.Ankr5xx.WithLabelValues("ankr", req.Dataset).Inc()
	}
	if c.usageHook == nil {
		return
	}
	var jobID, shardID string
	if v := ctx.Value("job_id"); v != nil {
		jobID, _ = v.(string)
	}
	if v := ctx.Value("shard_id"); v != nil {
		shardID, _ = v.(string)
	}
	c.usageHook(model.UsageRow{
		Provider:    "ankr",
		Method:      method,
		Dataset:     req.Dataset,
		JobID:       jobID,
		ShardID:     shardID,
		RequestTime: time.Now(),
		DurationMs:  int(elapsed.Milliseconds()),
		Status:      status,
		HTTPStatus:  httpStatus,
		APIError:    apiErr,
		Rows:        rows,
		Bytes:       respBytes,
		Credits:     c.credits,
		CostUSD:     c.costUSD,
	})
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
	hs := provider.HealthStatus{
		Provider:           "ankr",
		State:              provider.HealthState(st),
		LastCheck:          time.Now(),
		Error:              lastErr,
		ConsecutiveFailure: int(c.consecutive.Load()),
		RecentErrorRate:    c.breaker.errorRate(),
	}
	_ = ctx
	return hs
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
