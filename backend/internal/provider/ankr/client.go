// Package ankr 实现 Ankr Advanced API Provider（V1.1：响应体保护 + 官方错误码分类 + 自适应 pageSize；
// V1.6：wire/decoded 字节拆分、Body Idle 超时、传输层截断精确分类）。
package ankr

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

type Options struct {
	Endpoint               string
	APIKey                 string
	Runtime                func(context.Context) (endpoint, apiKey string, enabled bool, err error)
	MaxPageSize            int
	RequestsPerMinute      int
	RequestTimeout         time.Duration
	ResponseHeaderTimeout  time.Duration
	ResponseSoftLimitBytes int64
	ResponseHardLimitBytes int64
	CreditsPerRequest      float64
	CostPerRequestUSD      float64
	// Ankr 引擎（§21/§22）：超时分层
	ConnectTimeout      time.Duration
	TLSHandshakeTimeout time.Duration
	BodyIdleTimeout     time.Duration
}

type Client struct {
	endpoint    string
	apiKey      string
	runtime     func(context.Context) (string, string, bool, error)
	maxPageSize int
	softLimit   int64
	hardLimit   int64
	bodyIdle    time.Duration
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
	connTotal   atomic.Int64
	connReused  atomic.Int64
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
		opts.ResponseSoftLimitBytes = 48 << 20
	}
	if opts.ResponseHardLimitBytes <= 0 {
		opts.ResponseHardLimitBytes = 128 << 20
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = 10 * time.Second
	}
	if opts.TLSHandshakeTimeout <= 0 {
		opts.TLSHandshakeTimeout = 10 * time.Second
	}
	if opts.BodyIdleTimeout <= 0 {
		opts.BodyIdleTimeout = 30 * time.Second
	}
	// DisableCompression：wire（压缩）与 decoded（解压）字节必须分开计量（§28）。
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DisableCompression:    true,
		DialContext:           (&net.Dialer{Timeout: opts.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   128,
		MaxConnsPerHost:       128,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   opts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
	}
	c := &Client{
		endpoint:    strings.TrimSuffix(opts.Endpoint, "/"),
		apiKey:      opts.APIKey,
		runtime:     opts.Runtime,
		maxPageSize: opts.MaxPageSize,
		softLimit:   opts.ResponseSoftLimitBytes,
		hardLimit:   opts.ResponseHardLimitBytes,
		bodyIdle:    opts.BodyIdleTimeout,
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

func (c *Client) requestEndpoint(ctx context.Context) (string, error) {
	base, key := c.endpoint, c.apiKey
	if c.runtime != nil {
		var enabled bool
		var err error
		base, key, enabled, err = c.runtime(ctx)
		if err != nil {
			return "", err
		}
		if !enabled {
			return "", &provider.Error{Provider: "ankr", Class: provider.ClassUpstream, Message: "Ankr is disabled in provider control", Retryable: true}
		}
	}
	if key == "" && c.runtime != nil {
		return "", &provider.Error{Provider: "ankr", Class: provider.ClassAuth, Message: "Ankr runtime key is unavailable"}
	}
	return strings.TrimSuffix(base, "/") + "/" + url.PathEscape(key), nil
}

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
	"token_holders":   "ankr_getTokenHolders",
}

// idleBodyReader 监控 body 读取进展（§22）：只有连续 N 秒无进展才算卡死。
type idleBodyReader struct {
	rc           io.ReadCloser
	lastProgress atomic.Int64
	idleFired    atomic.Bool
}

func (b *idleBodyReader) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.lastProgress.Store(time.Now().UnixNano())
	}
	return n, err
}

func (b *idleBodyReader) Close() error { return b.rc.Close() }

// readBody 读取响应体：wire 与 decoded 分开计量（§28）。
// 返回 (decodedBody, wireBytes, decodedBytes, tooLarge, softExceeded, err)。
func (c *Client) readBody(res *http.Response) ([]byte, int64, int64, bool, bool, error) {
	br := &idleBodyReader{rc: res.Body}
	br.lastProgress.Store(time.Now().UnixNano())
	done := make(chan struct{})
	defer close(done)
	if c.bodyIdle > 0 {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					if time.Since(time.Unix(0, br.lastProgress.Load())) > c.bodyIdle {
						br.idleFired.Store(true)
						_ = br.Close() // 中断读：ErrBodyIdleTimeout，绝不等待整体超时
						return
					}
				}
			}
		}()
	}

	wire, rerr := io.ReadAll(io.LimitReader(br, c.hardLimit+1))
	if rerr != nil {
		if br.idleFired.Load() {
			pe := provider.NewBodyIdleTimeout(
				fmt.Sprintf("body idle timeout after %s without progress", c.bodyIdle), rerr)
			pe.Provider = "ankr"
			pe.BodyBytes = int64(len(wire))
			return nil, int64(len(wire)), 0, false, false, pe
		}
		// 传输层截断：连接在 body 中途断开（§66）——严禁误报 invalid JSON。
		pe := provider.NewBodyTruncated(
			fmt.Sprintf("body truncated after %d wire bytes: %v", len(wire), rerr), int64(len(wire)), rerr)
		pe.Provider = "ankr"
		return nil, int64(len(wire)), 0, false, false, pe
	}
	if int64(len(wire)) > c.hardLimit {
		return nil, int64(len(wire)), 0, true, false, nil
	}
	encoding := strings.ToLower(res.Header.Get("Content-Encoding"))
	if strings.Contains(encoding, "gzip") {
		zr, gerr := gzip.NewReader(bytes.NewReader(wire))
		if gerr != nil {
			return nil, int64(len(wire)), 0, false, false,
				provider.NewBodyTruncated("invalid gzip stream: "+gerr.Error(), int64(len(wire)), gerr)
		}
		dec, derr := io.ReadAll(io.LimitReader(zr, c.hardLimit+1))
		_ = zr.Close()
		if derr != nil && !errors.Is(derr, io.EOF) {
			return nil, int64(len(wire)), int64(len(dec)), false, false,
				provider.NewBodyTruncated("gzip body truncated: "+derr.Error(), int64(len(wire)), derr)
		}
		if int64(len(dec)) > c.hardLimit {
			return nil, int64(len(wire)), int64(len(dec)), true, false, nil
		}
		soft := int64(len(dec)) > c.softLimit
		if soft {
			observability.AnkrSoftLimitExceeded.Inc()
		}
		return dec, int64(len(wire)), int64(len(dec)), false, soft, nil
	}
	soft := int64(len(wire)) > c.softLimit
	if soft {
		observability.AnkrSoftLimitExceeded.Inc()
	}
	return wire, int64(len(wire)), int64(len(wire)), false, soft, nil
}

func (c *Client) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	observability.AnkrFetchAttempts.WithLabelValues("ankr", req.Dataset).Inc()
	ok, state := c.breaker.allow()
	if !ok {
		observability.AnkrCircuitRejection.WithLabelValues(req.Chain, req.Dataset).Inc()
		return provider.FetchResult{}, &provider.Error{
			Provider: "ankr", Class: provider.ClassUpstream,
			Message:   fmt.Sprintf("provider circuit breaker %s", state),
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
	endpoint, err := c.requestEndpoint(ctx)
	if err != nil {
		return provider.FetchResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return provider.FetchResult{}, &provider.Error{Provider: "ankr", Class: provider.ClassInvalidRequest, Message: "Ankr endpoint is invalid"}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// DisableCompression 后手动声明 gzip：传输层压缩仍生效，字节可分开计量。
	httpReq.Header.Set("Accept-Encoding", "gzip")
	httpReq = httpReq.WithContext(httptrace.WithClientTrace(httpReq.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			total := c.connTotal.Add(1)
			if info.Reused {
				c.connReused.Add(1)
			}
			if total > 0 {
				observability.AnkrConnectionReuseRatio.Set(float64(c.connReused.Load()) / float64(total))
			}
		},
	}))

	if err := provider.ReserveDownloadRequest(ctx, 1, c.credits, c.costUSD); err != nil {
		return provider.FetchResult{}, err
	}
	observability.AnkrInflight.Inc()
	observability.AnkrActualInflight.Inc()
	defer observability.AnkrActualInflight.Dec()
	res, err := c.http.Do(httpReq)
	observability.AnkrInflight.Dec()
	elapsed := time.Since(start)
	defer func() { observability.AnkrRequestDuration.Observe(time.Since(start).Seconds()) }()
	// V1.2（§56/§57）：TTFB = headers 到达前；Body = readBody 分段。
	headersAt := time.Now()
	ttfbMs := headersAt.Sub(start).Milliseconds()
	if err != nil {
		pe := c.safeTransportError(ctx, err, req.PageSize)
		pe.Duration = elapsed
		pe.Provider = "ankr"
		c.recordFailureFor(req, pe)
		c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, 0, 0, 0, elapsed.Milliseconds(), 0)
		return provider.FetchResult{}, pe
	}
	defer res.Body.Close()

	// Content-Length 快速拒绝（§7；Chunked 无 CL 时靠 hardLimit+1 兜底）。
	if res.ContentLength > 0 && res.ContentLength > c.hardLimit {
		pe := provider.NewResponseTooLarge(
			fmt.Sprintf("content-length %d exceeds hard limit %d", res.ContentLength, c.hardLimit),
			req.PageSize, res.ContentLength)
		pe.Provider = "ankr"
		pe.HTTPStatus = res.StatusCode
		c.recordFailureFor(req, pe)
		c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, res.ContentLength, 0, 0, ttfbMs, 0)
		return provider.FetchResult{}, pe
	}

	observability.AnkrBodyInflightActual.Inc()
	respBody, wireBytes, decodedBytes, exceeded, softExceeded, rerr := c.readBody(res)
	observability.AnkrBodyInflightActual.Dec()
	bodyMs := time.Since(headersAt).Milliseconds()
	elapsed = time.Since(start)
	if rerr != nil {
		pe := &provider.Error{}
		var timeout net.Error
		if ctx.Err() != nil {
			pe = c.classifyCtxErr(ctx, ctx.Err(), req.PageSize)
		} else if errors.Is(rerr, context.DeadlineExceeded) || (errors.As(rerr, &timeout) && timeout.Timeout()) {
			pe = provider.NewTimeout("Ankr response body timed out", req.PageSize, rerr)
		} else if pe2, ok := rerr.(*provider.Error); ok {
			pe = pe2
		} else {
			pe = &provider.Error{Provider: "ankr", Class: provider.ClassUpstream,
				Message: "read response: " + rerr.Error(), Retryable: true}
		}
		pe.Provider = "ankr"
		pe.BodyBytes = wireBytes
		pe.HTTPStatus = res.StatusCode
		pe.Duration = elapsed
		c.recordFailureFor(req, pe)
		c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, wireBytes, decodedBytes, 0, ttfbMs, bodyMs)
		return provider.FetchResult{}, pe
	}
	observability.AnkrResponseBytes.Observe(float64(decodedBytes))
	observability.AnkrFetchWireBytes.WithLabelValues("ankr", req.Dataset).Add(float64(wireBytes))
	observability.AnkrFetchDecodedBytes.WithLabelValues("ankr", req.Dataset).Add(float64(decodedBytes))

	if exceeded {
		pe := provider.NewResponseTooLarge(
			fmt.Sprintf("response body exceeded hard limit %d", c.hardLimit),
			req.PageSize, decodedBytes)
		pe.Provider = "ankr"
		pe.HTTPStatus = res.StatusCode
		c.recordFailureFor(req, pe)
		c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, wireBytes, decodedBytes, 0, ttfbMs, bodyMs)
		return provider.FetchResult{}, pe
	}

	retryAfter := parseRetryAfter(res.Header)
	if res.StatusCode == http.StatusOK {
		var rpc rpcResponse
		if err := json.Unmarshal(respBody, &rpc); err != nil {
			// 未超硬限制、未被截断、解析仍失败 → 才是真正 InvalidJSON（§9/§66）
			pe := &provider.Error{Provider: "ankr", Class: provider.ClassInvalidJSON,
				Message: "invalid json: " + err.Error(), Retryable: true,
				BodyBytes: decodedBytes, Cause: err, Kind: provider.ErrTemporary}
			if decodedBytes >= 1<<20 && err.Error() == "unexpected end of JSON input" {
				pe = provider.NewBodyTruncated("JSON response ended before the document was complete", decodedBytes, err)
				pe.Provider = "ankr"
			}
			pe.Duration = elapsed
			c.recordFailureFor(req, pe)
			c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, wireBytes, decodedBytes, 0, ttfbMs, bodyMs)
			return provider.FetchResult{}, pe
		}
		if rpc.Error != nil {
			pe := c.classifyRPCError(rpc.Error, retryAfter)
			sanitizeEndpointError(pe, endpoint)
			pe.Duration = elapsed
			pe.BodyBytes = decodedBytes
			c.recordFailureFor(req, pe)
			c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, wireBytes, decodedBytes, 0, ttfbMs, bodyMs)
			return provider.FetchResult{}, pe
		}
		result, n, perr := c.parseResult(req.Dataset, rpc.Result)
		if perr != nil {
			pe := &provider.Error{Provider: "ankr", Class: provider.ClassInvalidJSON,
				Message: "parse result: " + perr.Error(), Retryable: true, Cause: perr, Kind: provider.ErrTemporary}
			c.recordFailureFor(req, pe)
			c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, wireBytes, decodedBytes, 0, ttfbMs, bodyMs)
			return provider.FetchResult{}, pe
		}
		result.WireBytes = wireBytes
		result.DecodedBytes = decodedBytes
		result.SoftLimitExceeded = softExceeded
		result.RequestID = req.RequestID
		result.RequestFromBlock = req.FromBlock
		result.RequestToBlock = req.ToBlock
		result.RequestPageSize = req.PageSize
		result.AttemptNo = req.AttemptNo
		result.RawResponseBody = respBody
		result.StartedAt = start
		result.BodyCompleteAt = time.Now()
		c.recordSuccess()
		c.recordUsage(ctx, req, method, "ok", nil, elapsed, n, wireBytes, decodedBytes, 0, ttfbMs, bodyMs)
		observability.AnkrResponseRows.Observe(float64(n))
		return result, nil
	}

	pe := c.classifyHTTPError(res.StatusCode, respBody, retryAfter)
	sanitizeEndpointError(pe, endpoint)
	pe.Duration = elapsed
	pe.BodyBytes = decodedBytes
	c.recordFailureFor(req, pe)
	c.recordUsage(ctx, req, method, "error", pe, elapsed, 0, wireBytes, decodedBytes, 0, ttfbMs, bodyMs)
	return provider.FetchResult{}, pe
}

func (c *Client) buildParams(req provider.FetchRequest) map[string]any {
	params := map[string]any{}
	if req.Dataset == "token_holders" {
		params["blockchain"], params["contractAddress"], params["pageSize"] = req.Chain, req.Address, req.PageSize
		if req.PageToken != "" {
			params["pageToken"] = req.PageToken
		}
		return params
	}
	if req.Dataset == "balances" {
		params["blockchain"] = req.Chain
		params["walletAddress"] = req.Address
		return params
	}
	params["blockchain"] = req.Chain
	params["address"] = req.Address
	// Ankr rejects a request that contains block and timestamp bounds together.
	// Time-range jobs retain broad block metadata for coverage/range guards, so
	// the wire adapter must deliberately choose one dimension. Prefer timestamps
	// only for datasets that advertise native time-range support; otherwise keep
	// the block interval and omit unsupported timestamp fields.
	useTimeRange := c.Capabilities().SupportsTimeRange[req.Dataset] && (req.FromTime != nil || req.ToTime != nil)
	if useTimeRange {
		if req.FromTime != nil {
			params["fromTimestamp"] = req.FromTime.UnixMilli()
		}
		if req.ToTime != nil {
			params["toTimestamp"] = req.ToTime.UnixMilli()
		}
	} else {
		if req.FromBlock != nil {
			params["fromBlock"] = fmt.Sprintf("0x%x", *req.FromBlock)
		}
		if req.ToBlock != nil {
			params["toBlock"] = fmt.Sprintf("0x%x", *req.ToBlock)
		}
	}
	params["pageSize"] = req.PageSize
	if req.PageToken != "" {
		params["pageToken"] = req.PageToken
	}
	if req.Dataset == "logs" && len(req.Topics) > 0 {
		params["topics"] = req.Topics
	}
	if req.DescOrder {
		params["descOrder"] = true
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
	case "token_holders":
		rows = pr.Holders
	}
	return provider.FetchResult{Rows: rows, NextPageToken: pr.NextPageToken, HasMore: pr.NextPageToken != "", Total: pr.HoldersCount}, len(rows), nil
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

func (c *Client) safeTransportError(ctx context.Context, err error, pageSize int) *provider.Error {
	if ctx.Err() != nil {
		return c.classifyCtxErr(ctx, ctx.Err(), pageSize)
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		return provider.NewTimeout("Ankr transport timed out", pageSize, errors.New("Ankr transport timed out"))
	}
	return &provider.Error{Provider: "ankr", Class: provider.ClassUpstream, Message: "Ankr transport request failed", Retryable: true, Kind: provider.ErrTemporary}
}

func sanitizeEndpointError(pe *provider.Error, endpoint string) {
	if pe == nil {
		return
	}
	pe.Message = strings.ReplaceAll(pe.Message, endpoint, "Ankr endpoint")
	if last := strings.LastIndex(endpoint, "/"); last >= 0 && len(endpoint)-last-1 >= 8 {
		pe.Message = strings.ReplaceAll(pe.Message, endpoint[last+1:], "***")
	}
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
	before, after := c.breaker.update()
	if before != "OPEN" && after == "OPEN" {
		observability.AnkrCircuitOpenTransition.Inc()
	}
	c.consecutive.Store(0)
}

func (c *Client) recordFailure(pe *provider.Error) {
	c.breaker.record(false)
	before, after := c.breaker.update()
	if before != "OPEN" && after == "OPEN" {
		observability.AnkrCircuitOpenTransition.Inc()
	}
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
	case provider.ClassBodyTruncated:
		observability.AnkrBodyTruncated.Inc()
	case provider.ClassBodyIdleTimeout:
		observability.AnkrBodyIdleTimeout.Inc()
	}
	if pe.HTTPStatus == http.StatusTooManyRequests {
		observability.Ankr429.WithLabelValues("ankr", "").Inc()
	}
	if pe.HTTPStatus >= 500 {
		observability.Ankr5xx.WithLabelValues("ankr", "").Inc()
	}
}

func (c *Client) recordFailureFor(req provider.FetchRequest, pe *provider.Error) {
	observability.AnkrProviderRootFailure.WithLabelValues(req.Chain, req.Dataset, string(pe.Class)).Inc()
	c.recordFailure(pe)
}

// recordUsage 写用量表（§34 增强字段）+ Prometheus（§35）。
func (c *Client) recordUsage(ctx context.Context, req provider.FetchRequest, method, status string, pe *provider.Error, elapsed time.Duration, rows int, wireBytes, decodedBytes, respBytes int64, ttfbMs, bodyMs int64) {
	observability.AnkrRequests.WithLabelValues("ankr", req.Dataset, status).Inc()
	observability.AnkrLatencyMs.WithLabelValues("ankr", req.Dataset).Observe(float64(elapsed.Milliseconds()))
	observability.AnkrTTFBSeconds.WithLabelValues("ankr", req.Dataset).Observe(float64(ttfbMs) / 1000.0)
	observability.AnkrBodyDownloadSeconds.WithLabelValues("ankr", req.Dataset).Observe(float64(bodyMs) / 1000.0)
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
		Status: status, Rows: rows, Bytes: int(respBytes),
		WireBytes: wireBytes, DecodedBytes: decodedBytes,
		PageSize: req.PageSize, ResponseBytes: int(respBytes), RetryAttempt: attempt,
		Credits: c.credits, CostUSD: c.costUSD, LogicalCalls: 1, HTTPRequests: 1, MethodCalls: 1, RequestID: req.RequestID,
		TTFBMs: int(ttfbMs), BodyMs: int(bodyMs),
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
	if c.runtime != nil {
		_, _, enabled, err := c.runtime(ctx)
		if err != nil || !enabled {
			return provider.HealthStatus{Provider: "ankr", State: provider.HealthOpen, LastCheck: time.Now(), Error: "provider configuration unavailable or disabled"}
		}
	}
	st := c.breaker.stateStr()
	if st == "" {
		st = "NORMAL"
	}
	var lastErr string
	if v, ok := c.lastErr.Load().(string); ok {
		lastErr = v
	}
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
