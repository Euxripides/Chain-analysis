// Package chainbase 实现 Chainbase Web3 API Count Oracle Provider Adapter（V1.2）。
// 职责边界（§7/§17）：Chainbase 只回答"大概有多少行"；任何失败都必须 Fail Open；
// Count 永不参与 Coverage / 停止条件 / 最终真值（Ankr + ClickHouse Validation 才是真值）。
package chainbase

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// Options Chainbase 客户端配置。
type Options struct {
	Endpoint string
	APIKey   string

	CountTimeout           time.Duration
	MaxRetries             int
	RequestsPerMinute      int
	ResponseHardLimitBytes int64
	ConnectTimeout         time.Duration
	TLSHandshakeTimeout    time.Duration
	BodyIdleTimeout        time.Duration
	CreditsPerRequest      float64
	CostPerRequestUSD      float64
}

// HealthSnapshot 供 DB/API 展示的运行期健康快照。
type HealthSnapshot struct {
	State          string
	Requests       int64
	SuccessCount   int64
	P50Ms          int
	P95Ms          int
	SuccessRate    float64 // 最近窗口（300）
	CacheHitRate   float64
	QuotaExhausted bool
	LastError      string
	LastErrorAt    time.Time
	UpdatedAt      time.Time
}

// Client Chainbase REST 客户端（GET /v1/...）。
type Client struct {
	endpoint  string
	apiKey    string
	http      *http.Client
	limiter   *TokenBucket
	breaker   *circuitBreaker
	hardLimit int64
	credits   float64
	costUSD   float64
	usageHook provider.UsageHook

	mu         sync.Mutex
	latencies  []int64 // 最近成功请求延迟
	reqResults []bool  // 最近请求成败（窗口）
	reqCache   []bool  // 对应请求是否 cache 命中（未命中网络的不计网络窗口）
	total      int64
	success    int64
	cacheHits  int64
	lastErr    string
	lastErrAt  time.Time
	quota      bool
}

const healthWindow = 300

func New(opts Options, hook provider.UsageHook) *Client {
	if opts.Endpoint == "" {
		opts.Endpoint = "https://api.chainbase.com"
	}
	if opts.CountTimeout <= 0 {
		opts.CountTimeout = 3 * time.Second
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if opts.ResponseHardLimitBytes <= 0 {
		opts.ResponseHardLimitBytes = 4 << 20
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = 5 * time.Second
	}
	if opts.TLSHandshakeTimeout <= 0 {
		opts.TLSHandshakeTimeout = 5 * time.Second
	}
	if opts.BodyIdleTimeout <= 0 {
		opts.BodyIdleTimeout = 10 * time.Second
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DisableCompression:    true,
		DialContext:           (&net.Dialer{Timeout: opts.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		MaxConnsPerHost:       32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   opts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.CountTimeout,
	}
	return &Client{
		endpoint:  strings.TrimSuffix(opts.Endpoint, "/"),
		apiKey:    opts.APIKey,
		http:      &http.Client{Transport: transport, Timeout: opts.CountTimeout},
		limiter:   NewTokenBucket(opts.RequestsPerMinute),
		breaker:   newCircuitBreaker(40, 0.25, 30*time.Second),
		hardLimit: opts.ResponseHardLimitBytes,
		credits:   opts.CreditsPerRequest,
		costUSD:   opts.CostPerRequestUSD,
		usageHook: hook,
	}
}

func (c *Client) Name() string { return "chainbase" }

// Available APIKey 配置可用且未触发断路器。
func (c *Client) Available() bool {
	return c.apiKey != ""
}

// Configured APIKey 是否已配置（禁止输出 key 本身）。
func (c *Client) Configured() bool { return c.apiKey != "" }

// QuotaExhausted 最近一次错误是否为配额耗尽。
func (c *Client) QuotaExhausted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quota
}

// Snapshot 健康快照（供 count_oracle_status 心跳写入）。
func (c *Client) Snapshot() HealthSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := HealthSnapshot{
		State: "CLOSED", Requests: c.total, SuccessCount: c.success,
		QuotaExhausted: c.quota, LastError: c.lastErr, LastErrorAt: c.lastErrAt,
		UpdatedAt: time.Now(),
	}
	switch c.breaker.stateStr() {
	case "NORMAL", "":
		s.State = "CLOSED"
	case "DEGRADED":
		s.State = "DEGRADED"
	case "OPEN":
		s.State = "OPEN"
	case "HALF_OPEN":
		s.State = "HALF_OPEN"
	}
	if n := len(c.latencies); n > 0 {
		sorted := append([]int64(nil), c.latencies...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		s.P50Ms = int(sorted[(n*50)/100])
		s.P95Ms = int(sorted[(n*95)/100])
	}
	if n := len(c.reqResults); n > 0 {
		var ok int
		for _, v := range c.reqResults {
			if v {
				ok++
			}
		}
		s.SuccessRate = float64(ok) / float64(n)
	}
	if n := len(c.reqCache); n > 0 {
		var hit int
		for _, v := range c.reqCache {
			if v {
				hit++
			}
		}
		s.CacheHitRate = float64(hit) / float64(n)
	}
	return s
}

func (c *Client) recordNetworkResult(ok bool, latencyMs int64) {
	c.mu.Lock()
	c.total++
	if ok {
		c.success++
	}
	c.reqResults = append(c.reqResults, ok)
	if len(c.reqResults) > healthWindow {
		c.reqResults = c.reqResults[len(c.reqResults)-healthWindow:]
	}
	if ok {
		c.latencies = append(c.latencies, latencyMs)
		if len(c.latencies) > healthWindow {
			c.latencies = c.latencies[len(c.latencies)-healthWindow:]
		}
	}
	c.mu.Unlock()
}

func (c *Client) recordCacheHit() {
	c.mu.Lock()
	c.cacheHits++
	c.reqCache = append(c.reqCache, true)
	if len(c.reqCache) > healthWindow {
		c.reqCache = c.reqCache[len(c.reqCache)-healthWindow:]
	}
	c.mu.Unlock()
}

func (c *Client) recordFailure(pe *provider.Error) {
	c.breaker.record(false)
	c.breaker.update()
	c.mu.Lock()
	c.lastErr = pe.Message
	c.lastErrAt = time.Now()
	if pe.Class == provider.ClassRateLimit && strings.Contains(strings.ToLower(pe.Message), "quota") {
		c.quota = true
	} else if pe.Class != provider.ClassRateLimit {
		// 非限流成功后配额标记复位（保持乐观）
		if pe.Class != provider.ClassAuth {
			c.quota = false
		}
	}
	c.mu.Unlock()
	ChainbaseCountErrors.WithLabelValues(string(pe.Class)).Inc()
}

func (c *Client) recordSuccess() {
	c.breaker.record(true)
	c.breaker.update()
}

func (c *Client) recordUsage(ctx context.Context, dataset, method, status string, pe *provider.Error, elapsed time.Duration, ttfbMs, bodyMs int64, rows int) {
	ChainbaseCountAttempts.WithLabelValues(dataset, status).Inc()
	if status == "ok" {
		ChainbaseCountSuccess.WithLabelValues(dataset).Inc()
	}
	ChainbaseCountLatencyMs.WithLabelValues(dataset).Observe(float64(elapsed.Milliseconds()))
	u := model.UsageRow{
		Provider: "chainbase", Method: method, Dataset: dataset,
		RequestTime: time.Now(), DurationMs: int(elapsed.Milliseconds()),
		Status: status, Rows: rows,
		UsageType: model.UsageTypeForCountDataset(dataset),
		APIType:   model.APITypeAdvanced,
		TTFBMs:    int(ttfbMs), BodyMs: int(bodyMs),
		Credits: c.credits, CostUSD: c.costUSD,
	}
	if v := ctx.Value("job_id"); v != nil {
		u.JobID, _ = v.(string)
	}
	if pe != nil {
		u.HTTPStatus = pe.HTTPStatus
		u.APIError = pe.Message
		u.ErrorClass = string(pe.Class)
	}
	if c.usageHook != nil {
		c.usageHook(u)
	}
}

// doGet 执行 GET：返回 (body, wireBytes, decodedBytes, ttfbMs, bodyMs, *http.Response close, err)。
func (c *Client) doGet(ctx context.Context, url string) ([]byte, int64, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, 0, 0, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, 0, provider.NewTimeout("chainbase count deadline exceeded", 0, err)
		}
		return nil, 0, 0, cbError(provider.ClassUpstream, "chainbase request failed: "+err.Error(), true)
	}
	defer res.Body.Close()

	if res.ContentLength > 0 && res.ContentLength > c.hardLimit {
		pe := provider.NewResponseTooLarge(fmt.Sprintf("chainbase response %d exceeds hard limit", res.ContentLength), 0, res.ContentLength)
		pe.Provider = "chainbase"
		return nil, res.ContentLength, 0, pe
	}
	wire, rerr := io.ReadAll(io.LimitReader(res.Body, c.hardLimit+1))
	if rerr != nil {
		pe := provider.NewBodyTruncated("chainbase body read failed: "+rerr.Error(), int64(len(wire)), rerr)
		pe.Provider = "chainbase"
		return nil, int64(len(wire)), 0, pe
	}
	if int64(len(wire)) > c.hardLimit {
		pe := provider.NewResponseTooLarge("chainbase response exceeds hard limit", 0, int64(len(wire)))
		pe.Provider = "chainbase"
		return nil, int64(len(wire)), 0, pe
	}
	decoded := wire
	if strings.Contains(strings.ToLower(res.Header.Get("Content-Encoding")), "gzip") {
		zr, gerr := gzip.NewReader(strings.NewReader(string(wire)))
		if gerr != nil {
			return nil, int64(len(wire)), 0, cbError(provider.ClassUpstream, "chainbase gzip error: "+gerr.Error(), true)
		}
		decoded, rerr = io.ReadAll(io.LimitReader(zr, c.hardLimit+1))
		_ = zr.Close()
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return nil, int64(len(wire)), int64(len(decoded)), provider.NewBodyTruncated("chainbase gzip truncated", int64(len(wire)), rerr)
		}
	}
	if res.StatusCode != http.StatusOK {
		pe := cbErrorHTTP(res.StatusCode, truncate(string(decoded), 300), res.Header.Get("Retry-After"))
		return nil, int64(len(wire)), int64(len(decoded)), pe
	}
	return decoded, int64(len(wire)), int64(len(decoded)), nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
