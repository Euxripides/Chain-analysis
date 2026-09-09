package tron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

const maxResponseBytes = 32 << 20

type Options struct {
	BaseURL            string
	APIKey             string
	RequestTimeout     time.Duration
	ConnectTimeout     time.Duration
	MaxRetries         int
	InitialConcurrency int
	NormalConcurrency  int
	MaxConcurrency     int
	PageSize           int
	MinRequestInterval time.Duration
	DailyRequestBudget int64
	// RatePacer may be shared by multiple dataset clients using the same API
	// key. TronGrid applies its frequency limit at account level, not per URL.
	RatePacer *RatePacer
	// IdentityResolver supplies the consensus logIndex that TronGrid's address
	// TRC20 index omits. Production wires the Ankr receipt capability here.
	IdentityResolver   TRC20IdentityResolver
	TRC20PrefetchPages int
	TRC20PageQueueMax  int
}

type TRC20IdentityResolver interface {
	EnrichTRC20Identity(context.Context, provider.FetchRequest, []json.RawMessage) ([]json.RawMessage, error)
}

// RatePacer serializes request starts across every client sharing one
// TronGrid account. Concurrency remains independent per dataset; only the
// account-level request frequency is shared.
type RatePacer struct {
	mu          sync.Mutex
	nextRequest time.Time
	minInterval time.Duration
}

func NewRatePacer(minInterval time.Duration) *RatePacer {
	if minInterval <= 0 {
		minInterval = 100 * time.Millisecond
	}
	return &RatePacer{minInterval: minInterval}
}

func (p *RatePacer) wait(ctx context.Context) error {
	p.mu.Lock()
	now := time.Now()
	slot := now
	if p.nextRequest.After(slot) {
		slot = p.nextRequest
	}
	p.nextRequest = slot.Add(p.minInterval)
	p.mu.Unlock()
	if wait := time.Until(slot); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

// TRC20BatchIdentityResolver lets the page prefetcher resolve several adjacent
// address-index pages as one block/log scan. Adjacent pages heavily overlap at
// their block boundaries; resolving them independently repeats boundary
// receipts and eth_getLogs windows.
type TRC20BatchIdentityResolver interface {
	EnrichTRC20IdentityBatch(context.Context, provider.FetchRequest, [][]json.RawMessage) ([][]json.RawMessage, error)
}

type Client struct {
	baseURL       string
	apiKey        string
	http          *http.Client
	maxRetries    int
	usage         provider.UsageHook
	log           *slog.Logger
	pageSize      int
	quota         *dailyQuota
	concMu        sync.Mutex
	concurrency   int
	inflight      int
	initial       int
	normal        int
	max           int
	cooldown      time.Time
	ratePacer     *RatePacer
	mu            sync.RWMutex
	health        provider.HealthStatus
	identity      TRC20IdentityResolver
	prefetchPages int
	prefetchMax   int
	prefetchMu    sync.Mutex
	prefetch      map[string]*tronPrefetchSlot
}

type tronPrefetchPage struct {
	parsed             pageResponse
	body               []byte
	started, completed time.Time
	pageSize           int
	enriched           bool
}

type tronPrefetchSlot struct {
	ready   chan struct{}
	page    tronPrefetchPage
	err     error
	expires time.Time
}

func New(opts Options, usage provider.UsageHook, log *slog.Logger) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("TRONGRID_BASE_URL must be a fixed https origin")
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 30 * time.Second
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = 5 * time.Second
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	limit := opts.InitialConcurrency
	if limit <= 0 {
		limit = 4
	}
	maxConcurrency := opts.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = limit
	}
	if maxConcurrency < limit {
		maxConcurrency = limit
	}
	normal := opts.NormalConcurrency
	if normal < limit {
		normal = limit
	}
	if normal > maxConcurrency {
		normal = maxConcurrency
	}
	pageSize := opts.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 200
	}
	if opts.TRC20PrefetchPages < 1 {
		opts.TRC20PrefetchPages = 1
	}
	if opts.TRC20PageQueueMax < opts.TRC20PrefetchPages {
		opts.TRC20PageQueueMax = opts.TRC20PrefetchPages
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: opts.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: opts.RequestTimeout,
		MaxIdleConns:          max(8, limit*2), MaxIdleConnsPerHost: max(4, limit), MaxConnsPerHost: max(4, limit),
	}
	apiKey := strings.TrimSpace(opts.APIKey)
	// Soft pacing is an application safety control, not a claim about an
	// official account QPS limit. It is independent of the 4/6/8 in-flight
	// concurrency ladder and applies only to TronGrid HTTP requests.
	ratePacer := opts.RatePacer
	if ratePacer == nil {
		ratePacer = NewRatePacer(opts.MinRequestInterval)
	}
	c := &Client{baseURL: base, apiKey: apiKey, http: &http.Client{Transport: tr, Timeout: opts.RequestTimeout}, maxRetries: opts.MaxRetries, usage: usage, log: log, ratePacer: ratePacer, pageSize: pageSize, quota: newDailyQuota(opts.DailyRequestBudget), concurrency: limit, initial: limit, normal: normal, max: maxConcurrency, identity: opts.IdentityResolver, prefetchPages: opts.TRC20PrefetchPages, prefetchMax: opts.TRC20PageQueueMax, prefetch: map[string]*tronPrefetchSlot{}}
	c.health = provider.HealthStatus{Provider: "trongrid", State: provider.HealthNormal, LastCheck: time.Now(), CurrentConcurrency: limit}
	return c, nil
}

func (c *Client) Name() string { return "trongrid" }
func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{Name: c.Name(), Datasets: []string{"transactions", "token_transfers"}, MaxPageSize: 200,
		SupportsTimeRange: map[string]bool{"transactions": true, "token_transfers": true}}
}
func (c *Client) Health(context.Context) provider.HealthStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

type pageResponse struct {
	Success bool              `json:"success"`
	Data    []json.RawMessage `json:"data"`
	Meta    struct {
		Fingerprint string `json:"fingerprint"`
	} `json:"meta"`
}

// TRC20ActivityBounds returns the oldest and newest indexed TRC20 event times
// without downloading the history. The bounds include Approval events when
// present; that is conservative and guarantees no Transfer is excluded.
func (c *Client) TRC20ActivityBounds(ctx context.Context, address string) (time.Time, time.Time, error) {
	address, err := NormalizeTronAddress(address)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	type result struct {
		time time.Time
		err  error
	}
	results := make(chan result, 2)
	for _, order := range []string{"block_timestamp,asc", "block_timestamp,desc"} {
		order := order
		go func() {
			t, fetchErr := c.trc20Boundary(ctx, address, order)
			results <- result{time: t, err: fetchErr}
		}()
	}
	oldest, newest := <-results, <-results
	if oldest.err != nil {
		return time.Time{}, time.Time{}, oldest.err
	}
	if newest.err != nil {
		return time.Time{}, time.Time{}, newest.err
	}
	if oldest.time.After(newest.time) {
		oldest, newest = newest, oldest
	}
	return oldest.time, newest.time, nil
}

func (c *Client) trc20Boundary(ctx context.Context, address, order string) (time.Time, error) {
	if err := c.acquire(ctx); err != nil {
		return time.Time{}, err
	}
	defer c.release()
	if _, err := c.quota.allow(ctx, "manual"); err != nil {
		return time.Time{}, err
	}
	q := url.Values{"limit": {"1"}, "only_confirmed": {"true"}, "order_by": {order}}
	endpoint := c.baseURL + "/v1/accounts/" + url.PathEscape(address) + "/transactions/trc20?" + q.Encode()
	started := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", c.apiKey)
	}
	resp, err := c.Do(ctx, req)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	var body []byte
	if err == nil {
		body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		resp.Body.Close()
	}
	var parsed pageResponse
	if err == nil && status >= 200 && status < 300 {
		err = json.Unmarshal(body, &parsed)
	}
	if err != nil || status < 200 || status >= 300 || !parsed.Success || len(parsed.Data) == 0 {
		c.record(ctx, provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: address, PageSize: 1}, "trc20_boundary", started, status, 0, "error", string(provider.ClassProviderInvalid), 0, len(body))
		return time.Time{}, &provider.Error{Provider: c.Name(), Class: provider.ClassProviderInvalid, Message: "TronGrid TRC20 activity boundary unavailable", HTTPStatus: status, Cause: err}
	}
	var row struct {
		BlockTimestamp int64 `json:"block_timestamp"`
	}
	if err := json.Unmarshal(parsed.Data[0], &row); err != nil || row.BlockTimestamp <= 0 {
		return time.Time{}, &provider.Error{Provider: c.Name(), Class: provider.ClassProviderInvalid, Message: "TronGrid TRC20 activity boundary is missing timestamp", Cause: err}
	}
	c.record(ctx, provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: address, PageSize: 1}, "trc20_boundary", started, status, 0, "success", "", 1, len(body))
	return time.UnixMilli(row.BlockTimestamp).UTC(), nil
}

func (c *Client) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	if req.Chain != "tron" {
		return provider.FetchResult{}, &provider.Error{Provider: c.Name(), Class: provider.ClassUnsupported, Message: "TronGrid only supports chain=tron"}
	}
	address, err := NormalizeTronAddress(req.Address)
	if err != nil {
		return provider.FetchResult{}, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: err.Error()}
	}
	if req.Dataset != "transactions" && req.Dataset != "token_transfers" {
		return provider.FetchResult{}, &provider.Error{Provider: c.Name(), Class: provider.ClassUnsupported, Message: "unsupported TRON dataset: " + req.Dataset}
	}
	path, usageType := "/v1/accounts/"+url.PathEscape(address)+"/transactions", "transactions"
	if req.Dataset == "token_transfers" {
		path, usageType = "/v1/accounts/"+url.PathEscape(address)+"/transactions/trc20", "trc20"
	}
	q := url.Values{}
	pageSize := req.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = c.pageSize
	}
	q.Set("limit", strconv.Itoa(pageSize))
	q.Set("only_confirmed", "true")
	q.Set("order_by", "block_timestamp,desc")
	if req.PageToken != "" {
		q.Set("fingerprint", req.PageToken)
	}
	if req.FromTime != nil {
		q.Set("min_timestamp", strconv.FormatInt(req.FromTime.UnixMilli(), 10))
	}
	if req.ToTime != nil {
		q.Set("max_timestamp", strconv.FormatInt(req.ToTime.UnixMilli(), 10))
	}
	endpoint := c.baseURL + path + "?" + q.Encode()
	if req.Dataset == "token_transfers" && req.PageToken != "" {
		if prefetched, ok := c.consumePrefetched(ctx, req, address); ok {
			if prefetched.parsed.Meta.Fingerprint != "" {
				c.startPrefetch(ctx, req, address, path, prefetched.parsed.Meta.Fingerprint, pageSize, usageType)
			}
			if !prefetched.enriched {
				prefetched.parsed.Data, err = c.enrichTRC20Identity(ctx, req, prefetched.parsed.Data)
				if err != nil {
					return provider.FetchResult{}, err
				}
			}
			return provider.FetchResult{Rows: prefetched.parsed.Data, NextPageToken: prefetched.parsed.Meta.Fingerprint, HasMore: prefetched.parsed.Meta.Fingerprint != "", WireBytes: int64(len(prefetched.body)), DecodedBytes: int64(len(prefetched.body)), RequestID: req.RequestID, RequestPageSize: prefetched.pageSize, AttemptNo: 1, StartedAt: prefetched.started, BodyCompleteAt: prefetched.completed, RawResponseBody: prefetched.body}, nil
		}
	}

	if err := c.acquire(ctx); err != nil {
		return provider.FetchResult{}, err
	}
	released := false
	defer func() {
		if !released {
			c.release()
		}
	}()

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		level, quotaErr := c.quota.allow(ctx, req.Priority)
		if quotaErr != nil {
			pe, _ := quotaErr.(*provider.Error)
			class := provider.ClassQuota
			if pe != nil && pe.Class != "" {
				class = pe.Class
			}
			c.record(ctx, req, usageType, time.Now(), 0, attempt, "error", string(class), 0, 0)
			return provider.FetchResult{}, quotaErr
		}
		if level != "normal" && level != "" && c.log != nil {
			used, budget, _ := c.quota.snapshot()
			c.log.Warn("TronGrid quota guard", "level", level, "used", used, "budget", budget, "message", quotaMessage(level, used, budget))
		}
		if attempt > 0 {
			d := time.Duration(1<<min(attempt-1, 5))*250*time.Millisecond + time.Duration(rand.Intn(200))*time.Millisecond
			select {
			case <-ctx.Done():
				return provider.FetchResult{}, provider.NewCancelled(ctx.Err().Error())
			case <-time.After(d):
			}
		}
		started := time.Now()
		httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		httpReq.Header.Set("Accept", "application/json")
		if c.apiKey != "" {
			httpReq.Header.Set("TRON-PRO-API-KEY", c.apiKey)
		}
		resp, callErr := c.Do(ctx, httpReq)
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		var body []byte
		if callErr == nil {
			body, callErr = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
			resp.Body.Close()
			if len(body) > maxResponseBytes {
				callErr = errors.New("TronGrid response exceeds 32 MiB")
			}
		}
		var parsed pageResponse
		if callErr == nil && status >= 200 && status < 300 {
			callErr = json.Unmarshal(body, &parsed)
			if callErr == nil && !parsed.Success {
				callErr = errors.New("TronGrid returned success=false")
			}
			if callErr == nil && req.Dataset == "token_transfers" {
				parsed.Data = filterTRC20Transfers(parsed.Data)
			}
			if callErr == nil {
				// The page transport slot is no longer needed once the body has
				// been decoded. Release it before optional receipt enrichment;
				// enrichment acquires its own slots.
				c.release()
				released = true
			}
		}
		if callErr == nil && status >= 200 && status < 300 {
			if req.Dataset == "token_transfers" && parsed.Meta.Fingerprint != "" {
				c.startPrefetch(ctx, req, address, path, parsed.Meta.Fingerprint, pageSize, usageType)
			}
			if req.Dataset == "token_transfers" {
				parsed.Data, callErr = c.enrichTRC20Identity(ctx, req, parsed.Data)
				if callErr != nil {
					return provider.FetchResult{}, callErr
				}
			}
			c.record(ctx, req, usageType, started, status, attempt, "success", "", len(parsed.Data), len(body))
			c.setHealth(provider.HealthNormal, "")
			c.observeSuccess()
			return provider.FetchResult{Rows: parsed.Data, NextPageToken: parsed.Meta.Fingerprint, HasMore: parsed.Meta.Fingerprint != "", WireBytes: int64(len(body)), DecodedBytes: int64(len(body)), RequestID: req.RequestID, RequestPageSize: pageSize, AttemptNo: attempt + 1, StartedAt: started, BodyCompleteAt: time.Now(), RawResponseBody: body}, nil
		}
		class, retryable := classify(status, callErr)
		message := boundedError(status, body, callErr)
		c.record(ctx, req, usageType, started, status, attempt, "error", string(class), 0, len(body))
		if status == http.StatusTooManyRequests {
			c.observeRateLimit()
		}
		lastErr = &provider.Error{Provider: c.Name(), Class: class, Message: message, HTTPStatus: status, Retryable: retryable, ReduceConcurrency: status == 429, RetryAfter: retryAfter(resp), Cause: callErr}
		c.setHealth(provider.HealthDegraded, message)
		if !retryable {
			break
		}
	}
	return provider.FetchResult{}, lastErr
}

func (c *Client) prefetchKey(ctx context.Context, req provider.FetchRequest, address, token string) string {
	return contextValue(ctx, "job_id") + "|" + contextValue(ctx, "shard_id") + "|" + req.Dataset + "|" + address + "|" + token
}

func (c *Client) consumePrefetched(ctx context.Context, req provider.FetchRequest, address string) (tronPrefetchPage, bool) {
	key := c.prefetchKey(ctx, req, address, req.PageToken)
	c.prefetchMu.Lock()
	slot := c.prefetch[key]
	c.prefetchMu.Unlock()
	if slot == nil {
		return tronPrefetchPage{}, false
	}
	select {
	case <-ctx.Done():
		return tronPrefetchPage{}, false
	case <-slot.ready:
	}
	c.prefetchMu.Lock()
	delete(c.prefetch, key)
	c.prefetchMu.Unlock()
	return slot.page, slot.err == nil
}

func (c *Client) startPrefetch(parent context.Context, req provider.FetchRequest, address, path, token string, pageSize int, usageType string) {
	if c.prefetchPages <= 1 || token == "" {
		return
	}
	ctx := context.Background()
	for _, name := range []string{"job_id", "shard_id"} {
		if value := parent.Value(name); value != nil {
			ctx = context.WithValue(ctx, name, value)
		}
	}
	key := c.prefetchKey(ctx, req, address, token)
	c.prefetchMu.Lock()
	if _, exists := c.prefetch[key]; exists {
		c.prefetchMu.Unlock()
		return
	}
	now := time.Now()
	for oldKey, old := range c.prefetch {
		if now.After(old.expires) {
			delete(c.prefetch, oldKey)
		}
	}
	if len(c.prefetch) >= c.prefetchMax {
		c.prefetchMu.Unlock()
		return
	}
	c.prefetch[key] = &tronPrefetchSlot{ready: make(chan struct{}), expires: now.Add(5 * time.Minute)}
	c.prefetchMu.Unlock()
	go c.prefetchChain(ctx, req, address, path, token, pageSize, usageType, c.prefetchPages-1)
}

func (c *Client) prefetchChain(ctx context.Context, req provider.FetchRequest, address, path, token string, pageSize int, usageType string, remaining int) {
	type pendingPage struct {
		slot *tronPrefetchSlot
		page tronPrefetchPage
	}
	pending := make([]pendingPage, 0, remaining)
	for remaining > 0 && token != "" {
		key := c.prefetchKey(ctx, req, address, token)
		c.prefetchMu.Lock()
		slot := c.prefetch[key]
		c.prefetchMu.Unlock()
		if slot == nil {
			break
		}
		page, err := c.fetchPrefetchPage(ctx, req, address, path, token, pageSize, usageType)
		if err != nil {
			slot.err = err
			close(slot.ready)
			break
		}
		pending = append(pending, pendingPage{slot: slot, page: page})
		if page.parsed.Meta.Fingerprint == "" {
			break
		}
		token = page.parsed.Meta.Fingerprint
		remaining--
		if remaining == 0 {
			break
		}
		nextKey := c.prefetchKey(ctx, req, address, token)
		c.prefetchMu.Lock()
		if len(c.prefetch) >= c.prefetchMax {
			c.prefetchMu.Unlock()
			// Capacity limits future look-ahead only. Pages already fetched in
			// this chain must still be enriched and their ready channels closed;
			// returning here strands the consumer forever on consumePrefetched.
			break
		}
		if _, exists := c.prefetch[nextKey]; !exists {
			c.prefetch[nextKey] = &tronPrefetchSlot{ready: make(chan struct{}), expires: time.Now().Add(5 * time.Minute)}
		}
		c.prefetchMu.Unlock()
	}
	if len(pending) == 0 {
		return
	}
	if batch, ok := c.identity.(TRC20BatchIdentityResolver); ok {
		pages := make([][]json.RawMessage, len(pending))
		for i := range pending {
			pages[i] = pending[i].page.parsed.Data
		}
		enriched, err := batch.EnrichTRC20IdentityBatch(ctx, req, pages)
		if err != nil || len(enriched) != len(pending) {
			if err == nil {
				err = fmt.Errorf("TRC20 batch resolver returned %d pages for %d inputs", len(enriched), len(pending))
			}
			for i := range pending {
				pending[i].slot.err = err
				close(pending[i].slot.ready)
			}
			return
		}
		for i := range pending {
			pending[i].page.parsed.Data = enriched[i]
			pending[i].page.enriched = true
		}
	}
	for i := range pending {
		pending[i].slot.page = pending[i].page
		close(pending[i].slot.ready)
	}
}

func (c *Client) fetchPrefetchPage(parent context.Context, req provider.FetchRequest, address, path, token string, pageSize int, usageType string) (tronPrefetchPage, error) {
	timeout := c.http.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	q := url.Values{"limit": {strconv.Itoa(pageSize)}, "only_confirmed": {"true"}, "order_by": {"block_timestamp,desc"}, "fingerprint": {token}}
	if req.FromTime != nil {
		q.Set("min_timestamp", strconv.FormatInt(req.FromTime.UnixMilli(), 10))
	}
	if req.ToTime != nil {
		q.Set("max_timestamp", strconv.FormatInt(req.ToTime.UnixMilli(), 10))
	}
	endpoint := c.baseURL + path + "?" + q.Encode()
	if err := c.acquire(ctx); err != nil {
		return tronPrefetchPage{}, err
	}
	defer c.release()
	if _, err := c.quota.allow(ctx, req.Priority); err != nil {
		return tronPrefetchPage{}, err
	}
	started := time.Now()
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	httpReq.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("TRON-PRO-API-KEY", c.apiKey)
	}
	resp, err := c.Do(ctx, httpReq)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	var body []byte
	if err == nil {
		body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		resp.Body.Close()
	}
	var parsed pageResponse
	if err == nil && status >= 200 && status < 300 {
		err = json.Unmarshal(body, &parsed)
		if err == nil && !parsed.Success {
			err = errors.New("TronGrid returned success=false")
		}
		if err == nil {
			parsed.Data = filterTRC20Transfers(parsed.Data)
		}
	}
	if err != nil || status < 200 || status >= 300 {
		class, _ := classify(status, err)
		c.record(ctx, req, usageType, started, status, 0, "error", string(class), 0, len(body))
		if err == nil {
			err = fmt.Errorf("TronGrid prefetch HTTP %d", status)
		}
		return tronPrefetchPage{}, err
	}
	c.record(ctx, req, usageType, started, status, 0, "success", "", len(parsed.Data), len(body))
	return tronPrefetchPage{parsed: parsed, body: body, started: started, completed: time.Now(), pageSize: pageSize}, nil
}

// TronGrid's account TRC20 history can include Approval records alongside
// Transfer records. Approval is an allowance mutation, not a token transfer,
// and has no Transfer log to map to the dataset's exact log index. Keep rows
// with an absent type for backwards compatibility, but exclude every explicit
// non-Transfer event before identity enrichment and ingestion.
func filterTRC20Transfers(rows []json.RawMessage) []json.RawMessage {
	filtered := rows[:0]
	for _, raw := range rows {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &event); err != nil || event.Type == "" || strings.EqualFold(event.Type, "Transfer") {
			filtered = append(filtered, raw)
		}
	}
	return filtered
}

type tronGridEvent struct {
	BlockNumber    uint64         `json:"block_number"`
	BlockTimestamp int64          `json:"block_timestamp"`
	Contract       string         `json:"contract_address"`
	EventIndex     uint32         `json:"event_index"`
	EventName      string         `json:"event_name"`
	TransactionID  string         `json:"transaction_id"`
	Result         map[string]any `json:"result"`
}

// enrichTRC20Identity obtains the consensus-aligned block/log identity from
// TronGrid's transaction-event index. The account TRC20 endpoint intentionally
// omits block_number and event_index; inventing an index would break overlap
// dedupe against Ankr and is therefore rejected.
func (c *Client) enrichTRC20Identity(ctx context.Context, req provider.FetchRequest, rows []json.RawMessage) ([]json.RawMessage, error) {
	if c.identity == nil {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassProviderInvalid, Message: "TronGrid TRC20 requires an exact receipt log-index resolver"}
	}
	return c.identity.EnrichTRC20Identity(ctx, req, rows)
}

func trc20EventMatches(row map[string]any, event tronGridEvent) bool {
	info := mapValue(row["token_info"])
	if !strings.EqualFold(canonicalAddress(info["address"]), canonicalAddress(event.Contract)) || stringValue(row["value"]) != stringValue(event.Result["value"]) {
		return false
	}
	from, to := stringValue(event.Result["from"]), stringValue(event.Result["to"])
	if converted, err := EVMHex20ToBase58T(from); err == nil {
		from = converted
	}
	if converted, err := EVMHex20ToBase58T(to); err == nil {
		to = converted
	}
	return strings.EqualFold(canonicalAddress(row["from"]), canonicalAddress(from)) && strings.EqualFold(canonicalAddress(row["to"]), canonicalAddress(to))
}

func (c *Client) transactionEvents(ctx context.Context, req provider.FetchRequest, txID string) ([]tronGridEvent, error) {
	endpoint := c.baseURL + "/v1/transactions/" + url.PathEscape(txID) + "/events?only_confirmed=true"
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if _, quotaErr := c.quota.allow(ctx, req.Priority); quotaErr != nil {
			return nil, quotaErr
		}
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(1<<min(attempt-1, 5)) * 250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, provider.NewCancelled(ctx.Err().Error())
			case <-timer.C:
			}
		}
		httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		started := time.Now()
		resp, err := c.Do(ctx, httpReq)
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		var body []byte
		if err == nil {
			body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
			resp.Body.Close()
		}
		if err == nil && status >= 200 && status < 300 {
			var parsed struct {
				Success bool            `json:"success"`
				Data    []tronGridEvent `json:"data"`
			}
			if json.Unmarshal(body, &parsed) != nil || !parsed.Success {
				return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidJSON, Message: "invalid TronGrid transaction events response"}
			}
			c.record(ctx, req, "trc20_event", started, status, attempt, "success", "", len(parsed.Data), len(body))
			return parsed.Data, nil
		}
		class, retryable := classify(status, err)
		c.record(ctx, req, "trc20_event", started, status, attempt, "error", string(class), 0, len(body))
		lastErr = &provider.Error{Provider: c.Name(), Class: class, Message: boundedError(status, body, err), HTTPStatus: status, Retryable: retryable, Cause: err}
		if status == http.StatusTooManyRequests {
			c.observeRateLimit()
		}
		if !retryable {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) record(ctx context.Context, req provider.FetchRequest, usageType string, started time.Time, status, attempt int, outcome, class string, rows, bytes int) {
	if c.usage == nil {
		return
	}
	c.usage(model.UsageRow{Provider: c.Name(), Method: "trongrid_" + usageType, Dataset: req.Dataset, JobID: contextValue(ctx, "job_id"), ShardID: contextValue(ctx, "shard_id"), RequestTime: started, DurationMs: int(time.Since(started).Milliseconds()), Status: outcome, HTTPStatus: status, Rows: rows, Bytes: bytes, WireBytes: int64(bytes), DecodedBytes: int64(bytes), ErrorClass: class, PageSize: req.PageSize, ReturnedRows: rows, ResponseBytes: bytes, RetryAttempt: attempt, RequestID: req.RequestID, UsageType: usageType, APIType: "indexer", Chain: "tron", MethodClass: usageType, MethodCalls: 1, Priority: req.Priority})
}

func (c *Client) acquire(ctx context.Context) error {
	for {
		c.concMu.Lock()
		limit := c.concurrency
		if c.inflight < limit {
			c.inflight++
			c.concMu.Unlock()
			c.setConcurrencyHealth(limit)
			return nil
		}
		c.concMu.Unlock()
		c.setConcurrencyHealth(limit)
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return provider.NewCancelled(ctx.Err().Error())
		}
	}
}

func (c *Client) release() {
	c.concMu.Lock()
	if c.inflight > 0 {
		c.inflight--
	}
	c.concMu.Unlock()
}

func (c *Client) observeRateLimit() {
	c.concMu.Lock()
	defer c.concMu.Unlock()
	if c.concurrency > 2 {
		// The documented recovery ladder is 8→6→4→2. Reduce in
		// two-slot steps so one burst of 429s does not collapse the pool
		// more aggressively than the provider guidance allows.
		c.concurrency -= 2
		if c.concurrency < 2 {
			c.concurrency = 2
		}
	}
	c.cooldown = time.Now().Add(45 * time.Second)
	c.setConcurrencyHealth(c.concurrency)
}

func (c *Client) observeSuccess() {
	c.concMu.Lock()
	defer c.concMu.Unlock()
	if !time.Now().Before(c.cooldown) && c.concurrency < c.max {
		c.concurrency += 2
		if c.concurrency > c.max {
			c.concurrency = c.max
		}
	}
	c.setConcurrencyHealth(c.concurrency)
}

func (c *Client) setConcurrencyHealth(n int) {
	c.mu.Lock()
	c.health.CurrentConcurrency = n
	c.mu.Unlock()
}

func contextValue(ctx context.Context, key string) string {
	if v := ctx.Value(key); v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// Do is the only TronGrid transport exit. It enforces the configured origin,
// context cancellation and secret header handling centrally.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	base, _ := url.Parse(c.baseURL)
	if req.URL.Scheme != base.Scheme || req.URL.Host != base.Host {
		return nil, errors.New("TronGrid request origin is not allowed")
	}
	if req.Context() != ctx {
		req = req.Clone(ctx)
	}
	if err := c.waitForRateSlot(ctx); err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", c.apiKey)
	}
	return c.http.Do(req)
}

func (c *Client) waitForRateSlot(ctx context.Context) error {
	return c.ratePacer.wait(ctx)
}

type AccountKind string

const (
	AccountValidUnactivated AccountKind = "VALID_UNACTIVATED"
	AccountEOA              AccountKind = "EOA"
	AccountContract         AccountKind = "CONTRACT"
	AccountUnknown          AccountKind = "UNKNOWN"
)

// AccountState is deliberately separate from address validation: an empty
// TronGrid account response means valid-but-unactivated, never invalid.
func (c *Client) AccountState(ctx context.Context, address string) (AccountKind, error) {
	canonical, err := NormalizeTronAddress(address)
	if err != nil {
		return AccountUnknown, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/accounts/"+url.PathEscape(canonical), nil)
	started := time.Now()
	resp, err := c.Do(ctx, req)
	if err != nil {
		class, _ := classify(0, err)
		c.record(ctx, provider.FetchRequest{Chain: "tron", Dataset: "account"}, "account", started, 0, 0, "error", string(class), 0, 0)
		return AccountUnknown, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		class, _ := classify(resp.StatusCode, nil)
		c.record(ctx, provider.FetchRequest{Chain: "tron", Dataset: "account"}, "account", started, resp.StatusCode, 0, "error", string(class), 0, 0)
		return AccountUnknown, fmt.Errorf("TronGrid account HTTP %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		c.record(ctx, provider.FetchRequest{Chain: "tron", Dataset: "account"}, "account", started, resp.StatusCode, 0, "error", string(provider.ClassProviderInvalid), 0, 0)
		return AccountUnknown, err
	}
	var body struct {
		Success bool             `json:"success"`
		Data    []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		c.record(ctx, provider.FetchRequest{Chain: "tron", Dataset: "account"}, "account", started, resp.StatusCode, 0, "error", string(provider.ClassInvalidJSON), 0, len(bodyBytes))
		return AccountUnknown, err
	}
	if len(body.Data) == 0 {
		c.record(ctx, provider.FetchRequest{Chain: "tron", Dataset: "account"}, "account", started, resp.StatusCode, 0, "success", "", 0, len(bodyBytes))
		return AccountValidUnactivated, nil
	}
	typ := strings.ToLower(stringValue(body.Data[0]["type"]))
	if strings.Contains(typ, "contract") {
		c.record(ctx, provider.FetchRequest{Chain: "tron", Dataset: "account"}, "contract", started, resp.StatusCode, 0, "success", "", 1, len(bodyBytes))
		return AccountContract, nil
	}
	c.record(ctx, provider.FetchRequest{Chain: "tron", Dataset: "account"}, "account", started, resp.StatusCode, 0, "success", "", 1, len(bodyBytes))
	return AccountEOA, nil
}

func (c *Client) ResolveAddressTypes(ctx context.Context, chain string, addresses []string) (map[string]bool, error) {
	if chain != "tron" {
		return map[string]bool{}, nil
	}
	out := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		canonical, err := NormalizeTronAddress(address)
		if err != nil {
			continue
		}
		kind, err := c.AccountState(ctx, canonical)
		if err != nil {
			return out, err
		}
		out[canonical] = kind == AccountContract
	}
	return out, nil
}

func (c *Client) setHealth(state provider.HealthState, message string) {
	c.mu.Lock()
	c.health.State, c.health.Error, c.health.LastCheck = state, message, time.Now()
	c.mu.Unlock()
}

func classify(status int, err error) (provider.ErrorClass, bool) {
	if status == 429 {
		return provider.ClassRateLimit, true
	}
	if status == 403 || status == 401 {
		return provider.ClassAuth, status == 403
	}
	if status == 408 {
		return provider.ClassRequestTimeout, true
	}
	if status >= 500 {
		return provider.ClassUpstream, true
	}
	if status >= 400 {
		return provider.ClassInvalidRequest, false
	}
	if errors.Is(err, context.Canceled) {
		return provider.ClassCancelled, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return provider.ClassRequestTimeout, true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return provider.ClassUpstream, ne.Timeout() || ne.Temporary()
	}
	return provider.ClassProviderInvalid, false
}

func boundedError(status int, body []byte, err error) string {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if len(body) > 0 {
		if len(body) > 4096 {
			body = body[:4096]
		}
		msg = strings.TrimSpace(string(body))
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return fmt.Sprintf("TronGrid HTTP %d: %s", status, msg)
}

func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	n, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
	return time.Duration(n) * time.Second
}
