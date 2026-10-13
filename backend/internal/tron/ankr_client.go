package tron

import (
	"bytes"
	"context"
	"encoding/base64"
	encodinghex "encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"golang.org/x/sync/singleflight"
)

const transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

const (
	// A 101-block shared-scan gate must survive several scheduler waves. These
	// limits remain bounded while retaining enough upstream responses for 100+
	// address shards to reuse one scan instead of refetching every wave.
	blockCacheEntries   = 256
	receiptCacheEntries = 256
	// TRC20 pages are ordered and adjacent, so their boundary log windows
	// overlap. Keep a bounded short-lived cache of canonical fixed windows;
	// this is intentionally independent from the much larger receipt cache.
	trc20LogWindowCacheEntries = 2048
	trc20LogWindowCacheTTL     = 30 * time.Minute
	// Empty eth_getLogs results for a range that contains known TronGrid TRC20
	// rows prove that this Ankr archive epoch cannot serve address-topic logs.
	// Remember that capability failure across addresses so later pages go
	// directly to the correctness receipt path instead of paying for the same
	// doomed probe. The cache is bounded by time and process lifetime.
	trc20LogCapabilityEpochBlocks = uint64(1_000_000)
	trc20LogCapabilityTTL         = 30 * time.Minute
	// Ankr accepts at most 5,000 blocks per eth_getLogs call and approximately
	// 10,000 cumulative blocks per JSON-RPC batch. Batch width is derived from
	// the configured span below; a fixed width of three becomes unsafe when the
	// span is raised to 5,000 and produces a paid -32062 + single-call replay.
	trc20LogsBatchMaxCalls     = 3
	trc20LogsBatchMaxBlockSpan = uint64(10_000)
	// Live TRON boundary tests: receipt batches of 25/50/100 all succeeded;
	// 50 had the best latency/payload balance while 200 was rejected.
	trc20ReceiptBatchSize = 50
)

type trc20LogWindowCacheEntry struct {
	logs    []ankrLog
	expires time.Time
}

type trc20LogWindow struct{ from, to uint64 }

type trc20LogCapabilityResult struct {
	address  string
	from     uint64
	to       uint64
	logs     []ankrLog
	requests int
}

type ankrRPCBatchCall struct {
	method string
	params any
	out    any
}

func trc20LogsBatchSize(span uint64) int {
	if span == 0 {
		span = 5_000
	}
	width := int(trc20LogsBatchMaxBlockSpan / span)
	if width < 1 {
		return 1
	}
	return min(width, trc20LogsBatchMaxCalls)
}

func trc20LogCapabilityEpoch(from, to uint64) (uint64, bool) {
	if to < from {
		from, to = to, from
	}
	fromEpoch := from / trc20LogCapabilityEpochBlocks
	return fromEpoch, fromEpoch == to/trc20LogCapabilityEpochBlocks
}

type AnkrOptions struct {
	APIKey                    string
	AllowMissingKey           bool
	RequestTimeout            time.Duration
	ConnectTimeout            time.Duration
	MaxRetries                int
	Concurrency               int
	LogInitialSpan            uint64
	LogMinSpan                uint64
	LogMaxSpan                uint64
	BlockSpan                 uint64
	ReceiptSpan               uint64
	MaxScanBlocks             uint64
	MaxTxAutoScan             uint64
	MaxInternalAutoScan       uint64
	TimeBlockCacheTTL         time.Duration
	TimeBlockProbeRetry       int
	ReceiptCacheMaxEntries    int
	ReceiptCacheTTL           time.Duration
	ReceiptInitialConcurrency int
	ReceiptMaxConcurrency     int
	ReceiptAdaptiveEnabled    bool
	ReceiptAdaptiveHardMax    int
	TRC20AdaptiveEnrichment   bool
	TRC20LogEnrichmentRatio   float64
	TRC20EnrichLogMaxWindows  int
	TRC20EnrichLogSpan        uint64
	TRC20BatchReceipts        bool
}

type ankrCursor struct {
	Provider  string `json:"provider"`
	Type      string `json:"type"`
	NextBlock uint64 `json:"next_block"`
	EndBlock  uint64 `json:"end_block"`
	Span      uint64 `json:"span"`
}

type AnkrCapabilities struct {
	JSONRPC        bool `json:"jsonrpc"`
	NativeREST     bool `json:"native_rest"`
	Logs           bool `json:"logs"`
	BlockRange     bool `json:"block_range"`
	ReceiptByBlock bool `json:"receipt_by_block"`
	Solidity       bool `json:"solidity"`
}

type ankrTokenMetadata struct {
	Name     string
	Symbol   string
	Decimals *uint8
}

// AnkrClient is isolated inside the TRON package. It never invokes Ankr
// Advanced API methods; address history is built from logs and block scans.
type AnkrClient struct {
	rpcURL     string
	restURL    string
	runtime    func(context.Context) (rpcURL, restURL string, enabled bool, err error)
	http       *http.Client
	maxRetries int
	usage      provider.UsageHook
	log        *slog.Logger
	sem        chan struct{}
	opts       AnkrOptions

	mu                      sync.RWMutex
	health                  provider.HealthStatus
	timeCache               map[int64]uint64
	timeCacheExpires        map[int64]time.Time
	timestampCache          map[uint64]time.Time
	timestampCacheExpires   map[uint64]time.Time
	blockCache              map[string][]byte
	receiptCache            map[uint64][]byte
	cacheOrder              []string
	receiptOrder            []uint64
	metaCache               map[string]ankrTokenMetadata
	blockGroup              singleflight.Group
	receiptGroup            singleflight.Group
	trc20ReceiptGroup       singleflight.Group
	trc20LogWindowGroup     singleflight.Group
	trc20LogCapabilityGroup singleflight.Group
	timestampGroup          singleflight.Group
	trc20Receipts           *trc20ReceiptCache
	trc20LogMu              sync.Mutex
	trc20LogWindows         map[string]trc20LogWindowCacheEntry
	trc20LogWindowOrder     []string
	trc20EmptyLogEpochs     map[uint64]time.Time
	receiptSem              chan struct{}
	receiptCtl              *receiptController
}

func NewAnkr(opts AnkrOptions, usage provider.UsageHook, log *slog.Logger) (*AnkrClient, error) {
	key := strings.TrimSpace(opts.APIKey)
	if key == "" && !opts.AllowMissingKey {
		return nil, errors.New("ANKR_API_KEY is required for TRON_PROVIDER=ankr")
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 60 * time.Second
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = 8 * time.Second
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 2
	}
	if opts.LogInitialSpan == 0 {
		opts.LogInitialSpan = 5_000
	}
	if opts.LogMinSpan == 0 {
		opts.LogMinSpan = 10
	}
	if opts.LogMaxSpan < opts.LogInitialSpan {
		opts.LogMaxSpan = 20_000
	}
	if opts.BlockSpan == 0 {
		opts.BlockSpan = 25
	}
	if opts.ReceiptSpan == 0 {
		opts.ReceiptSpan = 4
	}
	if opts.MaxScanBlocks == 0 {
		opts.MaxScanBlocks = 2_000_000
	}
	if opts.MaxTxAutoScan == 0 {
		opts.MaxTxAutoScan = 1_000
	}
	if opts.MaxInternalAutoScan == 0 {
		opts.MaxInternalAutoScan = 5_000
	}
	if opts.TimeBlockCacheTTL <= 0 {
		opts.TimeBlockCacheTTL = 24 * time.Hour
	}
	if opts.TimeBlockProbeRetry < 0 {
		opts.TimeBlockProbeRetry = 0
	}
	if opts.ReceiptCacheMaxEntries <= 0 {
		opts.ReceiptCacheMaxEntries = 50_000
	}
	if opts.ReceiptCacheTTL <= 0 {
		opts.ReceiptCacheTTL = 30 * time.Minute
	}
	if opts.ReceiptInitialConcurrency <= 0 {
		opts.ReceiptInitialConcurrency = 4
	}
	if opts.ReceiptMaxConcurrency < opts.ReceiptInitialConcurrency {
		opts.ReceiptMaxConcurrency = opts.ReceiptInitialConcurrency
	}
	if opts.ReceiptAdaptiveHardMax < opts.ReceiptMaxConcurrency {
		opts.ReceiptAdaptiveHardMax = opts.ReceiptMaxConcurrency
	}
	if opts.TRC20EnrichLogSpan == 0 {
		opts.TRC20EnrichLogSpan = 3_000
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: opts.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: opts.RequestTimeout,
		MaxIdleConns: max(8, opts.Concurrency*2), MaxIdleConnsPerHost: max(4, opts.Concurrency), MaxConnsPerHost: max(4, opts.Concurrency),
	}
	escaped := url.PathEscape(key)
	c := &AnkrClient{
		rpcURL:  "https://rpc.ankr.com/tron_jsonrpc/" + escaped,
		restURL: "https://rpc.ankr.com/premium-http/tron/" + escaped,
		http:    &http.Client{Transport: transport, Timeout: opts.RequestTimeout}, maxRetries: opts.MaxRetries,
		usage: usage, log: log, sem: make(chan struct{}, opts.Concurrency), opts: opts,
		timeCache: map[int64]uint64{}, timeCacheExpires: map[int64]time.Time{}, timestampCache: map[uint64]time.Time{}, timestampCacheExpires: map[uint64]time.Time{}, blockCache: map[string][]byte{}, receiptCache: map[uint64][]byte{}, metaCache: map[string]ankrTokenMetadata{},
		trc20Receipts:       newTRC20ReceiptCache(opts.ReceiptCacheMaxEntries, opts.ReceiptCacheTTL),
		trc20LogWindows:     make(map[string]trc20LogWindowCacheEntry),
		trc20EmptyLogEpochs: make(map[uint64]time.Time),
		receiptSem:          make(chan struct{}, opts.ReceiptMaxConcurrency),
		receiptCtl:          newReceiptController(opts.ReceiptInitialConcurrency, opts.ReceiptMaxConcurrency, opts.ReceiptAdaptiveHardMax, opts.ReceiptAdaptiveEnabled),
	}
	c.health = provider.HealthStatus{Provider: "ankr_tron_node", State: provider.HealthNormal, LastCheck: time.Now(), CurrentConcurrency: opts.Concurrency}
	return c, nil
}

func (c *AnkrClient) Name() string { return "ankr" }

// SetRuntime connects the control-plane configuration after the startup probe.
func (c *AnkrClient) SetRuntime(resolve func(context.Context) (string, string, bool, error)) {
	c.runtime = resolve
}
func (c *AnkrClient) Capabilities() provider.Capabilities {
	return provider.Capabilities{Name: "ankr_tron_node", Datasets: []string{"transactions", "token_transfers", "internal_transactions"}, MaxPageSize: int(c.opts.LogMaxSpan), SupportsTimeRange: map[string]bool{"transactions": true, "token_transfers": true, "internal_transactions": true}}
}
func (c *AnkrClient) Health(ctx context.Context) provider.HealthStatus {
	if c.runtime != nil {
		_, _, enabled, err := c.runtime(ctx)
		if err != nil || !enabled {
			return provider.HealthStatus{Provider: c.Name(), State: provider.HealthOpen, LastCheck: time.Now(), Error: "provider configuration unavailable or disabled"}
		}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

// TxDetail implements the optional, user-triggered transaction-detail path for
// TRON. Bulk address-history downloads never call this method. The transaction
// and (when requested) receipt are both fetched from Ankr's TRON Node API, then
// adapted to the small provider-neutral shape consumed by the deep resolver.
func (c *AnkrClient) TxDetail(ctx context.Context, chain, txHash string, decodeTxData, decodeLogs bool) (json.RawMessage, error) {
	if chain != "tron" {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassUnsupported, Message: "Ankr TRON detail only supports chain=tron"}
	}
	txHash = strings.TrimPrefix(strings.TrimSpace(txHash), "0x")
	if len(txHash) != 64 {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: "TRON transaction hash must be 64 hex characters"}
	}
	if _, err := encodinghex.DecodeString(txHash); err != nil {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: "invalid TRON transaction hash", Cause: err}
	}

	var detail struct {
		TxID    string `json:"txID"`
		RawData struct {
			Contracts []struct {
				Parameter struct {
					Value struct {
						Data string `json:"data"`
					} `json:"value"`
				} `json:"parameter"`
			} `json:"contract"`
		} `json:"raw_data"`
	}
	req := provider.FetchRequest{Chain: "tron", Dataset: "transaction_detail"}
	if _, err := c.rest(ctx, req, "wallet/gettransactionbyid", map[string]any{"value": txHash, "visible": false}, "tx_detail", &detail); err != nil {
		return nil, err
	}
	if detail.TxID == "" {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassUpstream, Message: "TRON transaction not found at Ankr"}
	}

	transaction := map[string]any{"hash": detail.TxID}
	if decodeTxData && len(detail.RawData.Contracts) > 0 {
		data := strings.TrimPrefix(detail.RawData.Contracts[0].Parameter.Value.Data, "0x")
		if len(data) >= 8 {
			transaction["method"] = map[string]any{"id": "0x" + data[:8], "verified": false}
		}
	}
	if decodeLogs {
		var receipt struct {
			ID   string `json:"id"`
			Logs []struct {
				Address string   `json:"address"`
				Topics  []string `json:"topics"`
				Data    string   `json:"data"`
			} `json:"log"`
		}
		if _, err := c.rest(ctx, req, "wallet/gettransactioninfobyid", map[string]any{"value": txHash, "visible": false}, "receipt", &receipt); err != nil {
			return nil, err
		}
		logs := make([]map[string]any, 0, len(receipt.Logs))
		for i, item := range receipt.Logs {
			logs = append(logs, map[string]any{
				"address": item.Address, "topics": item.Topics, "data": item.Data,
				"logIndex": fmt.Sprintf("0x%x", i),
			})
		}
		transaction["logs"] = logs
	}
	return json.Marshal(map[string]any{"transactions": []any{transaction}})
}

// TxReceipt fetches one explicitly requested TRON receipt from Ankr. It is not
// used by bulk address-history downloads.
func (c *AnkrClient) TxReceipt(ctx context.Context, chain, txHash string) (json.RawMessage, error) {
	if chain != "tron" {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassUnsupported, Message: "Ankr TRON receipt only supports chain=tron"}
	}
	txHash = strings.TrimPrefix(strings.TrimSpace(txHash), "0x")
	if len(txHash) != 64 {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: "TRON transaction hash must be 64 hex characters"}
	}
	if _, err := encodinghex.DecodeString(txHash); err != nil {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: "invalid TRON transaction hash", Cause: err}
	}
	var receipt json.RawMessage
	req := provider.FetchRequest{Chain: "tron", Dataset: "transaction_receipt"}
	if _, err := c.rest(ctx, req, "wallet/gettransactioninfobyid", map[string]any{"value": txHash, "visible": true}, "receipt", &receipt); err != nil {
		return nil, err
	}
	if len(receipt) == 0 || string(receipt) == "{}" {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassUpstream, Message: "TRON transaction receipt not found at Ankr"}
	}
	return receipt, nil
}

func (c *AnkrClient) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	if req.Chain != "tron" {
		return provider.FetchResult{}, &provider.Error{Provider: c.Name(), Class: provider.ClassUnsupported, Message: "Ankr TRON provider only supports chain=tron"}
	}
	address, err := NormalizeTronAddress(req.Address)
	if err != nil {
		return provider.FetchResult{}, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: err.Error()}
	}
	select {
	case <-ctx.Done():
		return provider.FetchResult{}, provider.NewCancelled(ctx.Err().Error())
	case c.sem <- struct{}{}:
	}
	defer func() { <-c.sem }()

	start, end, span, err := c.resolveScan(ctx, req)
	if err != nil {
		return provider.FetchResult{}, err
	}
	windowEnd := min(start+span-1, end)
	var rows []json.RawMessage
	var raw []byte
	for {
		switch req.Dataset {
		case "token_transfers":
			rows, raw, err = c.fetchTRC20(ctx, req, address, start, windowEnd)
		case "transactions":
			rows, raw, err = c.fetchTransactions(ctx, req, address, start, windowEnd)
		case "internal_transactions":
			rows, raw, err = c.fetchInternal(ctx, req, address, start, windowEnd)
		default:
			return provider.FetchResult{}, &provider.Error{Provider: c.Name(), Class: provider.ClassUnsupported, Message: "unsupported TRON dataset: " + req.Dataset}
		}
		if err == nil {
			break
		}
		if !shouldShrink(err) || span <= c.opts.LogMinSpan {
			return provider.FetchResult{}, err
		}
		span = max(c.opts.LogMinSpan, span/2)
		windowEnd = min(start+span-1, end)
		if c.log != nil {
			c.log.Warn("TRON Ankr scan span reduced", "dataset", req.Dataset, "from_block", start, "to_block", windowEnd, "span", span, "error", err)
		}
	}

	next := ""
	hasMore := windowEnd < end
	if hasMore {
		nextSpan := span
		if req.Dataset == "token_transfers" && len(rows) < 200 && len(raw) < 4<<20 {
			nextSpan = min(span*2, c.opts.LogMaxSpan)
		}
		next = encodeAnkrCursor(ankrCursor{Provider: "ankr", Type: "block", NextBlock: windowEnd + 1, EndBlock: end, Span: nextSpan})
	}
	from, to := start, windowEnd
	return provider.FetchResult{Rows: rows, NextPageToken: next, HasMore: hasMore, WireBytes: int64(len(raw)), DecodedBytes: int64(len(raw)), RequestID: req.RequestID, RequestFromBlock: &from, RequestToBlock: &to, RequestPageSize: int(span), AttemptNo: req.AttemptNo + 1, RawResponseBody: raw, StartedAt: time.Now(), BodyCompleteAt: time.Now()}, nil
}

func (c *AnkrClient) resolveScan(ctx context.Context, req provider.FetchRequest) (uint64, uint64, uint64, error) {
	if req.PageToken != "" {
		cur, err := decodeAnkrCursor(req.PageToken)
		if err != nil || cur.Provider != "ankr" || cur.Type != "block" {
			return 0, 0, 0, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: "invalid Ankr TRON block cursor"}
		}
		return cur.NextBlock, cur.EndBlock, max(1, cur.Span), nil
	}
	if req.FromBlock == nil && req.FromTime == nil {
		return 0, 0, 0, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: "Ankr TRON full-history scan requires an explicit block or time range"}
	}
	latest, err := c.LatestBlock(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	start, end := uint64(0), latest
	if req.FromTime != nil && req.ToTime != nil {
		start, err = c.ResolveBlockByTimestamp(ctx, *req.FromTime)
		if err == nil {
			end, err = c.resolveEndBlockByTimestamp(ctx, *req.FromTime, *req.ToTime, start, latest)
		}
		if err != nil {
			return 0, 0, 0, err
		}
	} else if req.FromBlock != nil {
		start = *req.FromBlock
	} else if req.FromTime != nil {
		start, err = c.ResolveBlockByTimestamp(ctx, *req.FromTime)
		if err != nil {
			return 0, 0, 0, err
		}
	}
	if req.FromTime != nil && req.ToTime != nil {
		// Resolved together above so the end lookup is bounded near the start.
	} else if req.ToBlock != nil {
		end = min(*req.ToBlock, latest)
	} else if req.ToTime != nil {
		end, err = c.ResolveBlockByTimestamp(ctx, *req.ToTime)
		if err != nil {
			return 0, 0, 0, err
		}
		end = min(end, latest)
	}
	if start > end {
		return 0, 0, 0, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: "TRON scan start exceeds end"}
	}
	if end-start+1 > c.opts.MaxScanBlocks {
		return 0, 0, 0, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: fmt.Sprintf("TRON scan estimates %d blocks; configured safety limit is %d", end-start+1, c.opts.MaxScanBlocks)}
	}
	scanBlocks := end - start + 1
	if req.Dataset == "transactions" && scanBlocks > c.opts.MaxTxAutoScan {
		return 0, 0, 0, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: fmt.Sprintf("TRON transactions scan estimates %d blocks; production auto-scan limit is %d", scanBlocks, c.opts.MaxTxAutoScan)}
	}
	if req.Dataset == "internal_transactions" && scanBlocks > c.opts.MaxInternalAutoScan {
		return 0, 0, 0, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: fmt.Sprintf("TRON internal scan estimates %d blocks; production auto-scan limit is %d", scanBlocks, c.opts.MaxInternalAutoScan)}
	}
	span := c.opts.LogInitialSpan
	if req.Dataset == "transactions" {
		span = c.opts.BlockSpan
	}
	if req.Dataset == "internal_transactions" {
		span = c.opts.ReceiptSpan
	}
	return start, end, max(1, span), nil
}

func (c *AnkrClient) resolveEndBlockByTimestamp(ctx context.Context, fromTime, toTime time.Time, start, latest uint64) (uint64, error) {
	if !toTime.After(fromTime) {
		return start, nil
	}
	estimatedBlocks := uint64(toTime.Sub(fromTime).Seconds()/3) + 64
	hi := min(latest, start+max(uint64(2), estimatedBlocks))
	timestamp, err := c.blockTimestamp(ctx, hi)
	if err != nil {
		return 0, err
	}
	step := max(uint64(64), estimatedBlocks)
	for timestamp.Before(toTime) && hi < latest {
		step = min(step*2, latest-hi)
		hi += step
		timestamp, err = c.blockTimestamp(ctx, hi)
		if err != nil {
			return 0, err
		}
		if hi == latest && timestamp.Before(toTime) {
			return latest, nil
		}
	}
	lo := start
	for lo < hi {
		mid := lo + (hi-lo)/2
		timestamp, err = c.blockTimestamp(ctx, mid)
		if err != nil {
			return 0, err
		}
		if timestamp.Before(toTime) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, nil
}

func encodeAnkrCursor(cur ankrCursor) string {
	b, _ := json.Marshal(cur)
	return "ankr:block:" + base64.RawURLEncoding.EncodeToString(b)
}
func decodeAnkrCursor(token string) (ankrCursor, error) {
	var cur ankrCursor
	if !strings.HasPrefix(token, "ankr:block:") {
		return cur, errors.New("not an Ankr block cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "ankr:block:"))
	if err != nil {
		return cur, err
	}
	err = json.Unmarshal(b, &cur)
	return cur, err
}

func (c *AnkrClient) LatestBlock(ctx context.Context) (uint64, error) {
	var result string
	if _, err := c.rpc(ctx, provider.FetchRequest{Chain: "tron", Dataset: "latest_block"}, "eth_blockNumber", []any{}, "tron_eth_blockNumber", &result); err != nil {
		return 0, err
	}
	return parseUint(result)
}

func (c *AnkrClient) ResolveBlockByTimestamp(ctx context.Context, target time.Time) (uint64, error) {
	key := target.UTC().Unix()
	now := time.Now()
	c.mu.RLock()
	v, ok := c.timeCache[key]
	expires := c.timeCacheExpires[key]
	if ok && (expires.IsZero() || now.Before(expires)) {
		c.mu.RUnlock()
		return v, nil
	}
	c.mu.RUnlock()
	latest, err := c.LatestBlock(ctx)
	if err != nil {
		return 0, err
	}
	latestTime, err := c.blockTimestamp(ctx, latest)
	if err != nil {
		return 0, err
	}
	if !target.Before(latestTime) {
		return latest, nil
	}
	deltaSeconds := latestTime.Sub(target).Seconds()
	estimatedDelta := uint64(deltaSeconds / 3)
	estimate := uint64(0)
	if estimatedDelta < latest {
		estimate = latest - estimatedDelta
	}
	window := uint64(512)
	lo := estimate - min(estimate, window)
	hi := min(latest, estimate+window)
	loTime, err := c.blockTimestamp(ctx, lo)
	if err != nil {
		return 0, err
	}
	hiTime, err := c.blockTimestamp(ctx, hi)
	if err != nil {
		return 0, err
	}
	for target.Before(loTime) && lo > 0 {
		window = min(window*2, lo)
		lo -= window
		loTime, err = c.blockTimestamp(ctx, lo)
		if err != nil {
			return 0, err
		}
	}
	for target.After(hiTime) && hi < latest {
		window = min(window*2, latest-hi)
		hi += window
		hiTime, err = c.blockTimestamp(ctx, hi)
		if err != nil {
			return 0, err
		}
	}
	for lo < hi {
		mid := lo + (hi-lo)/2
		ts, err := c.blockTimestamp(ctx, mid)
		if err != nil {
			return 0, err
		}
		if ts.Before(target) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	c.mu.Lock()
	c.timeCache[key] = lo
	if c.timeCacheExpires == nil {
		c.timeCacheExpires = map[int64]time.Time{}
	}
	c.timeCacheExpires[key] = time.Now().Add(c.opts.TimeBlockCacheTTL)
	c.mu.Unlock()
	return lo, nil
}

func (c *AnkrClient) blockTimestamp(ctx context.Context, block uint64) (time.Time, error) {
	now := time.Now()
	c.mu.RLock()
	timestamp, ok := c.timestampCache[block]
	expires := c.timestampCacheExpires[block]
	if ok && (expires.IsZero() || now.Before(expires)) {
		c.mu.RUnlock()
		return timestamp, nil
	}
	c.mu.RUnlock()
	value, err, _ := c.timestampGroup.Do(strconv.FormatUint(block, 10), func() (any, error) {
		return c.probeBlockTimestamp(ctx, block)
	})
	if err != nil {
		return time.Time{}, err
	}
	return value.(time.Time), nil
}

func (c *AnkrClient) probeBlockTimestamp(ctx context.Context, block uint64) (time.Time, error) {
	var lastErr error
	for attempt := 0; attempt <= c.opts.TimeBlockProbeRetry; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(min(attempt-1, 4))) * 250 * time.Millisecond
			select {
			case <-ctx.Done():
				return time.Time{}, ctx.Err()
			case <-time.After(backoff):
			}
		}
		probeTimeout := 30 * time.Second
		if c.opts.RequestTimeout > 0 && c.opts.RequestTimeout < probeTimeout {
			probeTimeout = c.opts.RequestTimeout
		}
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		timestamp, err := c.blockTimestampOnce(probeCtx, block)
		cancel()
		if err == nil {
			return timestamp, nil
		}
		lastErr = err
	}
	return time.Time{}, fmt.Errorf("TRON time-to-block probe failed after %d attempts at block %d: %w", c.opts.TimeBlockProbeRetry+1, block, lastErr)
}

func (c *AnkrClient) blockTimestampOnce(ctx context.Context, block uint64) (time.Time, error) {
	var out struct {
		BlockHeader struct {
			RawData struct {
				Timestamp int64 `json:"timestamp"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}
	_, err := c.rest(ctx, provider.FetchRequest{Chain: "tron", Dataset: "block_timestamp"}, "wallet/getblockbynum", map[string]any{"num": block, "visible": true}, "tron_getblockbynum", &out)
	if err != nil {
		return time.Time{}, err
	}
	if out.BlockHeader.RawData.Timestamp <= 0 {
		return time.Time{}, errors.New("Ankr TRON block response missing timestamp")
	}
	timestamp := time.UnixMilli(out.BlockHeader.RawData.Timestamp).UTC()
	c.mu.Lock()
	if c.timestampCache == nil {
		c.timestampCache = map[uint64]time.Time{}
	}
	if c.timestampCacheExpires == nil {
		c.timestampCacheExpires = map[uint64]time.Time{}
	}
	c.timestampCache[block] = timestamp
	c.timestampCacheExpires[block] = time.Now().Add(c.opts.TimeBlockCacheTTL)
	c.mu.Unlock()
	return timestamp, nil
}

type ankrLog struct {
	Address, BlockNumber, BlockTimestamp, Data, LogIndex, TransactionHash string
	Removed                                                               bool
	Topics                                                                []string
}

type ankrReceipt struct {
	BlockNumber     string    `json:"blockNumber"`
	TransactionHash string    `json:"transactionHash"`
	Logs            []ankrLog `json:"logs"`
}

type trc20EnrichmentStats struct {
	Rows                int
	LogsRequests        int
	LogsMatchedRows     int
	ReceiptLogicalCalls int
	ReceiptHTTPRequests int
	FallbackRows        int
	SavedReceiptCalls   int
	FailureReasons      map[string]int
}

func (c *AnkrClient) recordTRC20Enrichment(ctx context.Context, req provider.FetchRequest, stats trc20EnrichmentStats) {
	if c.usage == nil {
		return
	}
	c.usage(model.UsageRow{
		Provider: "internal", Method: "trc20_enrichment_summary", Dataset: "token_transfers",
		JobID: contextValue(ctx, "job_id"), ShardID: contextValue(ctx, "shard_id"), RequestTime: time.Now(),
		Status: "summary", Rows: stats.Rows, ReturnedRows: stats.Rows, RequestID: req.RequestID,
		UsageType: "trc20_enrichment_summary", APIType: "internal", Chain: "tron", MethodClass: "enrichment_summary",
		LogicalCalls: stats.ReceiptLogicalCalls, HTTPRequests: stats.ReceiptHTTPRequests, LogsRequests: stats.LogsRequests,
		MatchedRows: stats.LogsMatchedRows, FallbackRows: stats.FallbackRows, SavedCalls: stats.SavedReceiptCalls,
		FailureReasons: stats.FailureReasons,
	})
}

// EnrichTRC20Identity maps address-index rows to the exact block-wide
// transaction-receipt logIndex. TronGrid's event_index is only the decoded
// event position and is not interchangeable with the EVM-compatible logIndex.
func (c *AnkrClient) EnrichTRC20IdentityBatch(ctx context.Context, req provider.FetchRequest, pages [][]json.RawMessage) ([][]json.RawMessage, error) {
	total := 0
	for _, page := range pages {
		total += len(page)
	}
	flat := make([]json.RawMessage, 0, total)
	lengths := make([]int, len(pages))
	for i, page := range pages {
		lengths[i] = len(page)
		flat = append(flat, page...)
	}
	if len(flat) == 0 {
		return pages, nil
	}
	enriched, err := c.EnrichTRC20Identity(ctx, req, flat)
	if err != nil {
		return nil, err
	}
	out := make([][]json.RawMessage, len(pages))
	offset := 0
	for i, length := range lengths {
		out[i] = append([]json.RawMessage(nil), enriched[offset:offset+length]...)
		offset += length
	}
	return out, nil
}

func (c *AnkrClient) EnrichTRC20Identity(ctx context.Context, req provider.FetchRequest, rows []json.RawMessage) ([]json.RawMessage, error) {
	decoded := make([]map[string]any, len(rows))
	byTx := make(map[string][]int)
	for i, raw := range rows {
		if err := json.Unmarshal(raw, &decoded[i]); err != nil {
			return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidJSON, Message: "invalid TronGrid TRC20 row", Cause: err}
		}
		txID := strings.TrimPrefix(stringValue(decoded[i]["transaction_id"]), "0x")
		if txID == "" {
			return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassProviderInvalid, Message: "TronGrid TRC20 row is missing transaction_id"}
		}
		byTx[txID] = append(byTx[txID], i)
	}
	observability.TronTRC20UniqueTX.Add(float64(len(byTx)))
	if len(rows) > 0 {
		observability.TronTRC20ReceiptAmplification.Observe(float64(len(byTx)) / float64(len(rows)))
	}
	if c.opts.TRC20AdaptiveEnrichment && len(rows) > 0 {
		if stats, ok, enrichErr := c.enrichTRC20IdentityWithLogs(ctx, req, decoded, rows, byTx); ok {
			observability.TronTRC20LogEnrichmentPages.Inc()
			observability.TronTRC20SavedRequests.Add(float64(stats.SavedReceiptCalls))
			c.recordTRC20Enrichment(ctx, req, stats)
			if enrichErr != nil {
				return nil, enrichErr
			}
			return rows, nil
		}
		observability.TronTRC20AdaptiveFallbacks.Inc()
	}
	// Batch receipts are the bounded correctness fallback, not the primary
	// enrichment path. Keeping this branch after adaptive Logs is essential:
	// when both production flags are enabled, placing it first makes the Logs
	// path unreachable for every ordinary page and restores 1 receipt/row
	// amplification despite batching the physical HTTP requests.
	if c.opts.TRC20BatchReceipts && len(byTx) >= 8 {
		httpRequests, err := c.enrichTRC20ByReceiptBatches(ctx, req, decoded, rows, byTx)
		if err != nil {
			return nil, err
		}
		observability.TronTRC20ReceiptEnrichmentPages.Inc()
		c.recordTRC20Enrichment(ctx, req, trc20EnrichmentStats{Rows: len(rows), ReceiptLogicalCalls: len(byTx), ReceiptHTTPRequests: httpRequests, FallbackRows: len(rows), FailureReasons: map[string]int{"BATCH_RECEIPT_SELECTED": len(rows)}})
		return rows, nil
	}
	observability.TronTRC20ReceiptEnrichmentPages.Inc()
	if err := c.enrichTRC20ByReceipts(ctx, req, decoded, rows, byTx, nil); err != nil {
		return nil, err
	}
	c.recordTRC20Enrichment(ctx, req, trc20EnrichmentStats{Rows: len(rows), ReceiptLogicalCalls: len(byTx), ReceiptHTTPRequests: len(byTx), FallbackRows: len(rows), FailureReasons: map[string]int{"LOGS_NOT_SELECTED": len(rows)}})
	return rows, nil
}

func (c *AnkrClient) enrichTRC20ByReceiptBatches(ctx context.Context, req provider.FetchRequest, decoded []map[string]any, rows []json.RawMessage, byTx map[string][]int) (int, error) {
	type receiptCall struct {
		txID    string
		indexes []int
		receipt ankrReceipt
	}
	items := make([]*receiptCall, 0, len(byTx))
	missing := make([]*receiptCall, 0, len(byTx))
	for txID, indexes := range byTx {
		item := &receiptCall{txID: txID, indexes: append([]int(nil), indexes...)}
		if receipt, ok := c.trc20Receipts.Get(trc20ReceiptCacheKey(txID)); ok {
			item.receipt = receipt
			observability.TronReceiptCacheHits.Inc()
		} else {
			missing = append(missing, item)
			observability.TronReceiptCacheMisses.Inc()
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].txID < items[j].txID })
	sort.Slice(missing, func(i, j int) bool { return missing[i].txID < missing[j].txID })
	type receiptBatch struct{ start, end int }
	batches := make([]receiptBatch, 0, (len(missing)+trc20ReceiptBatchSize-1)/trc20ReceiptBatchSize)
	for start := 0; start < len(missing); start += trc20ReceiptBatchSize {
		batches = append(batches, receiptBatch{start: start, end: min(start+trc20ReceiptBatchSize, len(missing))})
	}
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan receiptBatch, len(batches))
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	var fallbackHTTP int
	workers := min(len(batches), max(1, c.opts.Concurrency))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				calls := make([]ankrRPCBatchCall, 0, batch.end-batch.start)
				for _, item := range missing[batch.start:batch.end] {
					calls = append(calls, ankrRPCBatchCall{method: "eth_getTransactionReceipt", params: []any{"0x" + item.txID}, out: &item.receipt})
				}
				failed, err := c.rpcBatch(batchCtx, req, calls, "trc20_receipts_batch")
				if err != nil {
					failed = calls
				}
				if len(failed) > 0 {
					if err := c.rpcBatchFallback(batchCtx, req, failed, "trc20_receipt_fallback"); err != nil {
						errMu.Lock()
						if firstErr == nil {
							firstErr = err
							cancel()
						}
						errMu.Unlock()
						return
					}
					errMu.Lock()
					fallbackHTTP += len(failed)
					errMu.Unlock()
				}
			}
		}()
	}
	for _, batch := range batches {
		jobs <- batch
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return 0, firstErr
	}
	for _, item := range missing {
		c.trc20Receipts.Add(trc20ReceiptCacheKey(item.txID), item.receipt)
	}
	for _, item := range items {
		if err := c.enrichTRC20TransactionFromReceipt(item.txID, item.indexes, decoded, rows, nil, item.receipt); err != nil {
			return 0, err
		}
	}
	return len(batches) + fallbackHTTP, nil
}

func (c *AnkrClient) enrichTRC20ByReceipts(ctx context.Context, req provider.FetchRequest, decoded []map[string]any, rows []json.RawMessage, byTx map[string][]int, usedByTx map[string]map[uint64]bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type receiptJob struct {
		txID    string
		indexes []int
		used    map[uint64]bool
	}
	jobs := make(chan receiptJob, min(len(byTx), c.opts.ReceiptInitialConcurrency))
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	workerLimit := c.opts.ReceiptInitialConcurrency
	if c.receiptCtl != nil {
		workerLimit = c.receiptCtl.workerLimit()
	}
	workers := min(len(byTx), workerLimit)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := c.enrichTRC20TransactionWithUsed(ctx, req, job.txID, job.indexes, decoded, rows, job.used); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
						cancel()
					}
					errMu.Unlock()
					return
				}
			}
		}()
	}
queueRows:
	for txID, indexes := range byTx {
		select {
		case <-ctx.Done():
			break queueRows
		case jobs <- receiptJob{txID: txID, indexes: append([]int(nil), indexes...), used: usedByTx[txID]}:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return nil
}

// enrichTRC20IdentityWithLogs selects block-window log enrichment only when its
// estimated request cost is materially lower than receipt-per-transaction. It
// stages mutations and returns ok=false on every lookup or match failure so the
// established receipt path remains the correctness fallback.
func (c *AnkrClient) enrichTRC20IdentityWithLogs(ctx context.Context, req provider.FetchRequest, decoded []map[string]any, rows []json.RawMessage, byTx map[string][]int) (trc20EnrichmentStats, bool, error) {
	stats := trc20EnrichmentStats{Rows: len(rows), FailureReasons: map[string]int{}}
	if req.Address == "" || len(decoded) == 0 {
		return stats, false, nil
	}
	var oldest, newest int64
	var oldestTx, newestTx string
	for _, row := range decoded {
		ts := int64(uintValue(row["block_timestamp"]))
		if ts <= 0 {
			return stats, false, nil
		}
		txID := strings.TrimPrefix(stringValue(row["transaction_id"]), "0x")
		if txID == "" {
			return stats, false, nil
		}
		if oldest == 0 || ts < oldest {
			oldest = ts
			oldestTx = txID
		}
		if ts > newest {
			newest = ts
			newestTx = txID
		}
	}
	// The boundary transactions give an exact block range with at most two
	// receipt calls. Those receipts are cached and reused by the correctness
	// fallback, unlike timestamp-to-block binary search which can add dozens of
	// getblockbynum calls before still falling back to every receipt.
	var firstReceipt, lastReceipt ankrReceipt
	var firstErr, lastErr error
	boundaryHTTPRequests := 0
	if strings.EqualFold(oldestTx, newestTx) {
		if cached, ok := c.trc20Receipts.Get(trc20ReceiptCacheKey(oldestTx)); ok {
			firstReceipt = cached
			observability.TronReceiptCacheHits.Inc()
		} else {
			firstReceipt, firstErr = c.getTRC20Receipt(ctx, req, oldestTx)
			boundaryHTTPRequests = 1
		}
		lastReceipt = firstReceipt
	} else {
		calls := make([]ankrRPCBatchCall, 0, 2)
		if cached, ok := c.trc20Receipts.Get(trc20ReceiptCacheKey(oldestTx)); ok {
			firstReceipt = cached
			observability.TronReceiptCacheHits.Inc()
		} else {
			observability.TronReceiptCacheMisses.Inc()
			calls = append(calls, ankrRPCBatchCall{method: "eth_getTransactionReceipt", params: []any{"0x" + oldestTx}, out: &firstReceipt})
		}
		if cached, ok := c.trc20Receipts.Get(trc20ReceiptCacheKey(newestTx)); ok {
			lastReceipt = cached
			observability.TronReceiptCacheHits.Inc()
		} else {
			observability.TronReceiptCacheMisses.Inc()
			calls = append(calls, ankrRPCBatchCall{method: "eth_getTransactionReceipt", params: []any{"0x" + newestTx}, out: &lastReceipt})
		}
		if len(calls) > 0 {
			boundaryHTTPRequests = 1
			failed, batchErr := c.rpcBatch(ctx, req, calls, "trc20_boundary_receipts_batch")
			if batchErr != nil {
				failed = calls
			}
			if len(failed) > 0 {
				boundaryHTTPRequests += len(failed)
				if fallbackErr := c.rpcBatchFallback(ctx, req, failed, "trc20_boundary_receipt_fallback"); fallbackErr != nil {
					firstErr = fallbackErr
				}
			}
			if firstErr == nil {
				c.trc20Receipts.Add(trc20ReceiptCacheKey(oldestTx), firstReceipt)
				c.trc20Receipts.Add(trc20ReceiptCacheKey(newestTx), lastReceipt)
			}
		}
	}
	if firstErr != nil || lastErr != nil {
		return stats, false, nil
	}
	from, err := parseUint(firstReceipt.BlockNumber)
	if err != nil {
		return stats, false, nil
	}
	to := from
	boundaryRequests := 1
	if !strings.EqualFold(oldestTx, newestTx) {
		to, err = parseUint(lastReceipt.BlockNumber)
		if err != nil {
			return stats, false, nil
		}
		boundaryRequests++
	}
	if to < from {
		from, to = to, from
	}
	// Include neighbouring blocks for equal-timestamp boundary rows.
	if from > 0 {
		from--
	}
	to++
	span := c.opts.TRC20EnrichLogSpan
	if span == 0 {
		span = 3_000
	}
	rawWindows := int((to-from)/span) + 1
	// Canonical alignment makes adjacent multi-window pages address the same
	// cache key at their shared boundary instead of issuing shifted ranges. A
	// page already fitting in one window stays exact so alignment cannot double
	// its cold request cost.
	alignedFrom, alignedTo := from, to
	if rawWindows > 1 {
		alignedFrom = from - from%span
		alignedTo = to - to%span
	}
	windows := rawWindows
	if rawWindows > 1 {
		windows = int((alignedTo-alignedFrom)/span) + 1
	}
	ratio := c.opts.TRC20LogEnrichmentRatio
	if ratio <= 0 {
		ratio = .5
	}
	maxWindows := c.opts.TRC20EnrichLogMaxWindows
	if maxWindows <= 0 {
		maxWindows = 500
	}
	// Ankr bills/counts each JSON-RPC child call, not merely the containing HTTP
	// request. Batching remains a latency optimization, but it must never make
	// an expensive Logs scan look cheap to the adaptive selector.
	estimatedRequests := boundaryRequests + windows*2
	if windows > maxWindows || float64(estimatedRequests) > float64(len(byTx))*ratio {
		return stats, false, nil
	}
	ranges := make([]trc20LogWindow, 0, windows)
	if rawWindows == 1 {
		ranges = append(ranges, trc20LogWindow{from: from, to: to})
	}
	for start := alignedFrom; start <= alignedTo; {
		if rawWindows == 1 {
			break
		}
		end := start + span - 1
		if end < start {
			end = ^uint64(0)
		}
		// Never ask the provider for blocks beyond the exact newest boundary.
		// In particular, the first address-index page is normally at chain head
		// and a fully aligned final window can point into the future.
		if start == alignedTo && end > to {
			end = to
		}
		ranges = append(ranges, trc20LogWindow{from: start, to: end})
		if start == alignedTo {
			break
		}
		start += span
	}
	var logs []ankrLog
	var logicalLogRequests int
	var fetchErr error
	if epoch, singleEpoch := trc20LogCapabilityEpoch(from, to); singleEpoch {
		if c.trc20LogEpochKnownEmpty(epoch) {
			return stats, false, nil
		}
		value, groupErr, shared := c.trc20LogCapabilityGroup.Do(strconv.FormatUint(epoch, 10), func() (any, error) {
			fetched, requests, err := c.fetchTRC20EnrichmentLogWindows(ctx, req, req.Address, ranges)
			if err == nil && len(fetched) == 0 {
				c.markTRC20LogEpochEmpty(epoch)
			}
			return trc20LogCapabilityResult{address: req.Address, from: from, to: to, logs: fetched, requests: requests}, err
		})
		fetchErr = groupErr
		if groupErr == nil {
			result := value.(trc20LogCapabilityResult)
			logs, logicalLogRequests = result.logs, result.requests
			sameRange := strings.EqualFold(result.address, req.Address) && result.from == from && result.to == to
			if shared && len(result.logs) == 0 && !sameRange {
				// The leader paid for the capability proof. Followers consume no
				// RPC calls and proceed directly to receipts.
				logicalLogRequests = 0
			}
			if shared && len(result.logs) > 0 && !sameRange {
				// A positive result is range-specific and cannot be reused for a
				// different follower page in the same coarse epoch.
				logs, logicalLogRequests, fetchErr = c.fetchTRC20EnrichmentLogWindows(ctx, req, req.Address, ranges)
			}
		}
	} else {
		logs, logicalLogRequests, fetchErr = c.fetchTRC20EnrichmentLogWindows(ctx, req, req.Address, ranges)
	}
	if fetchErr != nil {
		return stats, false, nil
	}
	stats.LogsRequests = logicalLogRequests
	byHash := make(map[string][]ankrLog)
	seen := make(map[string]bool)
	for _, item := range logs {
		idx, e := parseUint(item.LogIndex)
		key := strings.ToLower(strings.TrimPrefix(item.TransactionHash, "0x")) + ":" + strconv.FormatUint(idx, 10)
		if e != nil || seen[key] {
			continue
		}
		seen[key] = true
		tx := strings.ToLower(strings.TrimPrefix(item.TransactionHash, "0x"))
		byHash[tx] = append(byHash[tx], item)
	}
	staged := make([]json.RawMessage, len(rows))
	copy(staged, rows)
	unmatched := make(map[string][]int)
	usedByTx := make(map[string]map[uint64]bool)
	for txID, indexes := range byTx {
		used := make(map[uint64]bool)
		usedByTx[txID] = used
		for _, rowIndex := range indexes {
			matched := false
			for _, item := range byHash[strings.ToLower(txID)] {
				idx, e := parseUint(item.LogIndex)
				block, be := parseUint(item.BlockNumber)
				if e != nil || be != nil || used[idx] || !trc20ReceiptLogMatches(decoded[rowIndex], item) {
					continue
				}
				clone := make(map[string]any, len(decoded[rowIndex])+3)
				for k, v := range decoded[rowIndex] {
					clone[k] = v
				}
				clone["block_number"], clone["_tron_event_index"], clone["_provider"] = block, idx, "trongrid"
				staged[rowIndex], _ = json.Marshal(clone)
				used[idx], matched = true, true
				break
			}
			if !matched {
				unmatched[txID] = append(unmatched[txID], rowIndex)
				reason := trc20LogsMismatchReason(decoded[rowIndex], byHash[strings.ToLower(txID)], used, len(logs) == 0)
				stats.FailureReasons[reason]++
				continue
			}
			stats.LogsMatchedRows++
		}
	}
	copy(rows, staged)
	stats.FallbackRows = len(rows) - stats.LogsMatchedRows
	receiptTx := make(map[string]bool, len(unmatched)+2)
	receiptTx[oldestTx], receiptTx[newestTx] = true, true
	for txID := range unmatched {
		receiptTx[txID] = true
	}
	stats.ReceiptLogicalCalls = len(receiptTx)
	unmatchedHTTPRequests := 0
	for txID := range unmatched {
		if !strings.EqualFold(txID, oldestTx) && !strings.EqualFold(txID, newestTx) {
			unmatchedHTTPRequests++
		}
	}
	stats.ReceiptHTTPRequests = boundaryHTTPRequests + unmatchedHTTPRequests
	stats.SavedReceiptCalls = max(0, len(byTx)-len(receiptTx))
	if len(unmatched) > 0 {
		if err := c.enrichTRC20ByReceipts(ctx, req, decoded, rows, unmatched, usedByTx); err != nil {
			return stats, true, err
		}
	}
	return stats, true, nil
}

func trc20LogsMismatchReason(row map[string]any, candidates []ankrLog, used map[uint64]bool, providerEmpty bool) string {
	if providerEmpty {
		return "LOGS_PROVIDER_EMPTY"
	}
	if len(candidates) == 0 {
		return "TX_NOT_FOUND"
	}
	info := mapValue(row["token_info"])
	wantToken := canonicalAddress(info["address"])
	wantFrom := canonicalAddress(row["from"])
	wantTo := canonicalAddress(row["to"])
	wantValue := stringValue(row["value"])
	tokenMatched, fromMatched, toMatched, valueMatched := false, false, false, false
	exactButUsed := false
	for _, item := range candidates {
		if item.Removed || len(item.Topics) < 3 || !strings.EqualFold(item.Topics[0], transferTopic) {
			continue
		}
		token, err1 := EVMHex20ToBase58T(item.Address)
		from, err2 := topicToTron(item.Topics[1])
		to, err3 := topicToTron(item.Topics[2])
		amount, ok := new(big.Int).SetString(strings.TrimPrefix(item.Data, "0x"), 16)
		if err1 != nil || err2 != nil || err3 != nil || !ok || !strings.EqualFold(wantToken, token) {
			continue
		}
		tokenMatched = true
		if !strings.EqualFold(wantFrom, from) {
			continue
		}
		fromMatched = true
		if !strings.EqualFold(wantTo, to) {
			continue
		}
		toMatched = true
		if amount.String() != wantValue {
			continue
		}
		valueMatched = true
		idx, err := parseUint(item.LogIndex)
		if err == nil && used[idx] {
			exactButUsed = true
		}
	}
	if !tokenMatched {
		return "TOKEN_MISMATCH"
	}
	if !fromMatched {
		return "FROM_MISMATCH"
	}
	if !toMatched {
		return "TO_MISMATCH"
	}
	if !valueMatched {
		return "VALUE_MISMATCH"
	}
	if exactButUsed {
		return "MULTI_EVENT_AMBIGUOUS"
	}
	return "EVENT_CONTENT_MISMATCH"
}

func (c *AnkrClient) fetchTRC20EnrichmentLogWindows(ctx context.Context, req provider.FetchRequest, address string, ranges []trc20LogWindow) ([]ankrLog, int, error) {
	type missingWindow struct {
		key                string
		from, to           uint64
		incoming, outgoing []ankrLog
	}
	logs := make([]ankrLog, 0)
	missing := make([]missingWindow, 0, len(ranges))
	for _, window := range ranges {
		key := strings.ToLower(strings.TrimSpace(address)) + ":" + strconv.FormatUint(window.from, 10) + ":" + strconv.FormatUint(window.to, 10)
		if cached, ok := c.getTRC20LogWindow(key); ok {
			logs = append(logs, cached...)
			continue
		}
		missing = append(missing, missingWindow{key: key, from: window.from, to: window.to})
	}
	if len(missing) == 0 {
		return logs, 0, nil
	}
	if len(missing) == 1 {
		windowLogs, requests, err := c.fetchTRC20EnrichmentLogs(ctx, req, address, missing[0].from, missing[0].to)
		return append(logs, windowLogs...), requests, err
	}
	evm, err := Base58TToEVMHex20(address)
	if err != nil {
		return nil, 0, err
	}
	topic := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(evm, "0x")
	calls := make([]ankrRPCBatchCall, 0, len(missing)*2)
	for i := range missing {
		incomingFilter := map[string]any{"fromBlock": fmt.Sprintf("0x%x", missing[i].from), "toBlock": fmt.Sprintf("0x%x", missing[i].to), "topics": []any{transferTopic, nil, topic}}
		outgoingFilter := map[string]any{"fromBlock": fmt.Sprintf("0x%x", missing[i].from), "toBlock": fmt.Sprintf("0x%x", missing[i].to), "topics": []any{transferTopic, topic, nil}}
		calls = append(calls,
			ankrRPCBatchCall{method: "eth_getLogs", params: []any{incomingFilter}, out: &missing[i].incoming},
			ankrRPCBatchCall{method: "eth_getLogs", params: []any{outgoingFilter}, out: &missing[i].outgoing})
	}
	type batchRange struct{ start, end int }
	batchSize := trc20LogsBatchSize(c.opts.TRC20EnrichLogSpan)
	batches := make([]batchRange, 0, (len(calls)+batchSize-1)/batchSize)
	for start := 0; start < len(calls); start += batchSize {
		batches = append(batches, batchRange{start: start, end: min(start+batchSize, len(calls))})
	}
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan batchRange, len(batches))
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	workers := min(len(batches), max(1, c.opts.Concurrency))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				batchCalls := calls[batch.start:batch.end]
				failedCalls, err := c.rpcBatch(batchCtx, req, batchCalls, "trc20_enrichment_logs_batch")
				if err != nil {
					// A JSON-RPC batch can contain one provider-level error while
					// every other range is valid. Retry only this small batch as
					// individual calls rather than discarding the whole page and
					// amplifying it into hundreds of receipts.
					failedCalls = batchCalls
				}
				if len(failedCalls) > 0 {
					fallbackErr := c.rpcBatchFallback(batchCtx, req, failedCalls, "trc20_logs_batch_fallback")
					if fallbackErr == nil {
						continue
					}
					errMu.Lock()
					if firstErr == nil {
						firstErr = fallbackErr
						cancel()
					}
					errMu.Unlock()
					return
				}
			}
		}()
	}
	for _, batch := range batches {
		jobs <- batch
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, 0, firstErr
	}
	for i := range missing {
		windowLogs := append(missing[i].incoming, missing[i].outgoing...)
		c.putTRC20LogWindow(missing[i].key, windowLogs)
		logs = append(logs, windowLogs...)
	}
	return logs, len(calls), nil
}

func (c *AnkrClient) rpcBatchFallback(ctx context.Context, req provider.FetchRequest, calls []ankrRPCBatchCall, usageType string) error {
	fallbackCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan ankrRPCBatchCall, len(calls))
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	workers := min(len(calls), max(1, c.opts.Concurrency))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for call := range jobs {
				if _, err := c.rpc(fallbackCtx, req, call.method, call.params, usageType, call.out); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
						cancel()
					}
					errMu.Unlock()
					return
				}
			}
		}()
	}
	for _, call := range calls {
		jobs <- call
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

func (c *AnkrClient) fetchTRC20EnrichmentLogs(ctx context.Context, req provider.FetchRequest, address string, from, to uint64) ([]ankrLog, int, error) {
	key := strings.ToLower(strings.TrimSpace(address)) + ":" + strconv.FormatUint(from, 10) + ":" + strconv.FormatUint(to, 10)
	if logs, ok := c.getTRC20LogWindow(key); ok {
		return logs, 0, nil
	}
	value, err, _ := c.trc20LogWindowGroup.Do(key, func() (any, error) {
		if logs, ok := c.getTRC20LogWindow(key); ok {
			return struct {
				logs     []ankrLog
				requests int
			}{logs: logs}, nil
		}
		logs, fetchErr := c.fetchTRC20EnrichmentLogsUncached(ctx, req, address, from, to)
		if fetchErr != nil {
			return nil, fetchErr
		}
		c.putTRC20LogWindow(key, logs)
		return struct {
			logs     []ankrLog
			requests int
		}{logs: logs, requests: 2}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	result := value.(struct {
		logs     []ankrLog
		requests int
	})
	return result.logs, result.requests, nil
}

func (c *AnkrClient) getTRC20LogWindow(key string) ([]ankrLog, bool) {
	c.trc20LogMu.Lock()
	defer c.trc20LogMu.Unlock()
	entry, ok := c.trc20LogWindows[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expires) {
		delete(c.trc20LogWindows, key)
		return nil, false
	}
	return entry.logs, true
}

func (c *AnkrClient) putTRC20LogWindow(key string, logs []ankrLog) {
	c.trc20LogMu.Lock()
	defer c.trc20LogMu.Unlock()
	if c.trc20LogWindows == nil {
		c.trc20LogWindows = make(map[string]trc20LogWindowCacheEntry)
	}
	if _, exists := c.trc20LogWindows[key]; !exists {
		for len(c.trc20LogWindows) >= trc20LogWindowCacheEntries && len(c.trc20LogWindowOrder) > 0 {
			oldest := c.trc20LogWindowOrder[0]
			c.trc20LogWindowOrder = c.trc20LogWindowOrder[1:]
			delete(c.trc20LogWindows, oldest)
		}
		c.trc20LogWindowOrder = append(c.trc20LogWindowOrder, key)
	}
	c.trc20LogWindows[key] = trc20LogWindowCacheEntry{logs: append([]ankrLog(nil), logs...), expires: time.Now().Add(trc20LogWindowCacheTTL)}
}

func (c *AnkrClient) trc20LogEpochKnownEmpty(epoch uint64) bool {
	c.trc20LogMu.Lock()
	defer c.trc20LogMu.Unlock()
	expires, ok := c.trc20EmptyLogEpochs[epoch]
	if !ok {
		return false
	}
	if time.Now().After(expires) {
		delete(c.trc20EmptyLogEpochs, epoch)
		return false
	}
	return true
}

func (c *AnkrClient) markTRC20LogEpochEmpty(epoch uint64) {
	c.trc20LogMu.Lock()
	defer c.trc20LogMu.Unlock()
	if c.trc20EmptyLogEpochs == nil {
		c.trc20EmptyLogEpochs = make(map[uint64]time.Time)
	}
	now := time.Now()
	for key, expires := range c.trc20EmptyLogEpochs {
		if now.After(expires) {
			delete(c.trc20EmptyLogEpochs, key)
		}
	}
	c.trc20EmptyLogEpochs[epoch] = now.Add(trc20LogCapabilityTTL)
}

func (c *AnkrClient) fetchTRC20EnrichmentLogsUncached(ctx context.Context, req provider.FetchRequest, address string, from, to uint64) ([]ankrLog, error) {
	evm, err := Base58TToEVMHex20(address)
	if err != nil {
		return nil, err
	}
	topic := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(evm, "0x")
	logReq := req
	logReq.Dataset = "token_transfers"
	var incoming, outgoing []ankrLog
	incomingFilter := map[string]any{"fromBlock": fmt.Sprintf("0x%x", from), "toBlock": fmt.Sprintf("0x%x", to), "topics": []any{transferTopic, nil, topic}}
	outgoingFilter := map[string]any{"fromBlock": fmt.Sprintf("0x%x", from), "toBlock": fmt.Sprintf("0x%x", to), "topics": []any{transferTopic, topic, nil}}
	logCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var incomingErr, outgoingErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, incomingErr = c.rpc(logCtx, logReq, "eth_getLogs", []any{incomingFilter}, "trc20_enrichment_logs", &incoming)
		if incomingErr != nil {
			cancel()
		}
	}()
	go func() {
		defer wg.Done()
		_, outgoingErr = c.rpc(logCtx, logReq, "eth_getLogs", []any{outgoingFilter}, "trc20_enrichment_logs", &outgoing)
		if outgoingErr != nil {
			cancel()
		}
	}()
	wg.Wait()
	if incomingErr != nil {
		return nil, incomingErr
	}
	if outgoingErr != nil {
		return nil, outgoingErr
	}
	return append(incoming, outgoing...), nil
}

func (c *AnkrClient) enrichTRC20Transaction(ctx context.Context, req provider.FetchRequest, txID string, indexes []int, decoded []map[string]any, rows []json.RawMessage) error {
	return c.enrichTRC20TransactionWithUsed(ctx, req, txID, indexes, decoded, rows, nil)
}

func (c *AnkrClient) enrichTRC20TransactionWithUsed(ctx context.Context, req provider.FetchRequest, txID string, indexes []int, decoded []map[string]any, rows []json.RawMessage, reserved map[uint64]bool) error {
	receipt, err := c.getTRC20Receipt(ctx, req, txID)
	if err != nil {
		return err
	}
	return c.enrichTRC20TransactionFromReceipt(txID, indexes, decoded, rows, reserved, receipt)
}

func (c *AnkrClient) enrichTRC20TransactionFromReceipt(txID string, indexes []int, decoded []map[string]any, rows []json.RawMessage, reserved map[uint64]bool, receipt ankrReceipt) error {
	block, err := parseUint(receipt.BlockNumber)
	if err != nil || len(receipt.Logs) == 0 {
		return &provider.Error{Provider: c.Name(), Class: provider.ClassProviderInvalid, Message: "Ankr TRON receipt is missing block/log identity"}
	}
	used := make(map[uint64]bool)
	for idx := range reserved {
		used[idx] = true
	}
	for _, rowIndex := range indexes {
		row := decoded[rowIndex]
		matched := false
		for _, item := range receipt.Logs {
			idx, parseErr := parseUint(item.LogIndex)
			if parseErr != nil || used[idx] || !trc20ReceiptLogMatches(row, item) {
				continue
			}
			row["block_number"], row["_tron_event_index"], row["_provider"] = block, idx, "trongrid"
			rows[rowIndex], _ = json.Marshal(row)
			used[idx], matched = true, true
			break
		}
		if !matched {
			return &provider.Error{Provider: c.Name(), Class: provider.ClassProviderInvalid, Message: "TronGrid TRC20 row does not match its Ankr transaction receipt"}
		}
	}
	return nil
}

func (c *AnkrClient) getTRC20Receipt(ctx context.Context, req provider.FetchRequest, txID string) (ankrReceipt, error) {
	key := trc20ReceiptCacheKey(txID)
	if receipt, ok := c.trc20Receipts.Get(key); ok {
		observability.TronReceiptCacheHits.Inc()
		return receipt, nil
	}
	observability.TronReceiptCacheMisses.Inc()
	value, err, shared := c.trc20ReceiptGroup.Do(key, func() (any, error) {
		if receipt, ok := c.trc20Receipts.Get(key); ok {
			observability.TronReceiptCacheHits.Inc()
			return receipt, nil
		}
		if c.receiptCtl != nil {
			if err := c.receiptCtl.acquire(ctx); err != nil {
				return ankrReceipt{}, err
			}
			defer c.receiptCtl.release()
		} else {
			select {
			case <-ctx.Done():
				return ankrReceipt{}, ctx.Err()
			case c.receiptSem <- struct{}{}:
			}
			defer func() { <-c.receiptSem }()
		}
		receiptReq := req
		receiptReq.Dataset = "receipt"
		var receipt ankrReceipt
		observability.TronReceiptRequests.Inc()
		started := time.Now()
		body, fetchErr := c.rpc(ctx, receiptReq, "eth_getTransactionReceipt", []any{"0x" + txID}, "receipt", &receipt)
		latency := time.Since(started)
		observability.TronReceiptLatencyMs.Observe(float64(latency.Milliseconds()))
		observability.TronReceiptBytes.Add(float64(len(body)))
		if c.receiptCtl != nil {
			class, _ := classifyAnkr(0, fetchErr)
			status := 200
			if pe, ok := fetchErr.(*provider.Error); ok {
				status, class = pe.HTTPStatus, pe.Class
			}
			c.receiptCtl.observe(latency, status, class, false)
		}
		if fetchErr != nil {
			class, _ := classifyAnkr(0, fetchErr)
			observability.TronReceiptErrors.WithLabelValues(string(class)).Inc()
			return ankrReceipt{}, fetchErr
		}
		c.trc20Receipts.Add(key, receipt)
		return receipt, nil
	})
	if shared {
		observability.TronReceiptSingleflightShared.Inc()
	}
	if err != nil {
		return ankrReceipt{}, err
	}
	return value.(ankrReceipt), nil
}

func trc20ReceiptCacheKey(txID string) string {
	return "tron:" + strings.ToLower(strings.TrimPrefix(txID, "0x"))
}

func trc20ReceiptLogMatches(row map[string]any, item ankrLog) bool {
	if item.Removed || len(item.Topics) < 3 || !strings.EqualFold(item.Topics[0], transferTopic) {
		return false
	}
	token, err1 := EVMHex20ToBase58T(item.Address)
	from, err2 := topicToTron(item.Topics[1])
	to, err3 := topicToTron(item.Topics[2])
	amount, ok := new(big.Int).SetString(strings.TrimPrefix(item.Data, "0x"), 16)
	info := mapValue(row["token_info"])
	return err1 == nil && err2 == nil && err3 == nil && ok &&
		strings.EqualFold(canonicalAddress(info["address"]), token) &&
		strings.EqualFold(canonicalAddress(row["from"]), from) &&
		strings.EqualFold(canonicalAddress(row["to"]), to) &&
		amount.String() == stringValue(row["value"])
}

func (c *AnkrClient) fetchTRC20(ctx context.Context, req provider.FetchRequest, address string, from, to uint64) ([]json.RawMessage, []byte, error) {
	evm, err := Base58TToEVMHex20(address)
	if err != nil {
		return nil, nil, err
	}
	topic := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(evm, "0x")
	base := map[string]any{"fromBlock": fmt.Sprintf("0x%x", from), "toBlock": fmt.Sprintf("0x%x", to), "topics": []any{transferTopic, nil, topic}}
	var incoming, outgoing []ankrLog
	b1, err := c.rpc(ctx, req, "eth_getLogs", []any{base}, "tron_eth_getLogs", &incoming)
	if err != nil {
		return nil, b1, err
	}
	base["topics"] = []any{transferTopic, topic, nil}
	b2, err := c.rpc(ctx, req, "eth_getLogs", []any{base}, "tron_eth_getLogs", &outgoing)
	if err != nil {
		return nil, append(b1, b2...), err
	}
	seen := map[string]bool{}
	logs := append(incoming, outgoing...)
	sort.Slice(logs, func(i, j int) bool {
		bi, _ := parseUint(logs[i].BlockNumber)
		bj, _ := parseUint(logs[j].BlockNumber)
		if bi == bj {
			li, _ := parseUint(logs[i].LogIndex)
			lj, _ := parseUint(logs[j].LogIndex)
			return li < lj
		}
		return bi < bj
	})
	rows := make([]json.RawMessage, 0, len(logs))
	for _, item := range logs {
		if item.Removed || len(item.Topics) < 3 {
			continue
		}
		idx, e1 := parseUint(item.LogIndex)
		block, e2 := parseUint(item.BlockNumber)
		amount, ok := new(big.Int).SetString(strings.TrimPrefix(item.Data, "0x"), 16)
		fromAddr, e3 := topicToTron(item.Topics[1])
		toAddr, e4 := topicToTron(item.Topics[2])
		token, e5 := EVMHex20ToBase58T(item.Address)
		if e1 != nil || e2 != nil || !ok || e3 != nil || e4 != nil || e5 != nil {
			continue
		}
		key := strings.ToLower(item.TransactionHash) + ":" + strconv.FormatUint(idx, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		ts, _ := parseUint(item.BlockTimestamp)
		if ts > 0 && ts < 1_000_000_000_000 {
			ts *= 1000
		}
		metadata := c.tokenMetadata(ctx, req, item.Address)
		tokenInfo := map[string]any{"address": token}
		if metadata.Name != "" {
			tokenInfo["name"] = metadata.Name
		}
		if metadata.Symbol != "" {
			tokenInfo["symbol"] = metadata.Symbol
		}
		if metadata.Decimals != nil {
			tokenInfo["decimals"] = *metadata.Decimals
		}
		m := map[string]any{"transaction_id": strings.TrimPrefix(item.TransactionHash, "0x"), "block_number": block, "block_timestamp": ts, "from": fromAddr, "to": toAddr, "value": amount.String(), "token_info": tokenInfo, "_tron_event_index": idx, "_provider": "ankr"}
		b, _ := json.Marshal(m)
		rows = append(rows, b)
	}
	raw, _ := json.Marshal(map[string]any{"incoming": json.RawMessage(b1), "outgoing": json.RawMessage(b2)})
	return rows, raw, nil
}

func (c *AnkrClient) tokenMetadata(ctx context.Context, req provider.FetchRequest, contract string) ankrTokenMetadata {
	key := strings.ToLower(contract)
	c.mu.RLock()
	metadata, ok := c.metaCache[key]
	c.mu.RUnlock()
	if ok {
		return metadata
	}
	if raw, err := c.ethCall(ctx, req, contract, "0x06fdde03"); err == nil {
		metadata.Name, _ = decodeAnkrTokenString(raw)
	}
	if raw, err := c.ethCall(ctx, req, contract, "0x95d89b41"); err == nil {
		metadata.Symbol, _ = decodeAnkrTokenString(raw)
	}
	if raw, err := c.ethCall(ctx, req, contract, "0x313ce567"); err == nil {
		if value, decodeErr := decodeAnkrTokenUint8(raw); decodeErr == nil {
			metadata.Decimals = &value
		}
	}
	c.mu.Lock()
	if c.metaCache == nil {
		c.metaCache = map[string]ankrTokenMetadata{}
	}
	c.metaCache[key] = metadata
	c.mu.Unlock()
	return metadata
}

func (c *AnkrClient) ethCall(ctx context.Context, req provider.FetchRequest, contract, data string) (string, error) {
	var result string
	_, err := c.rpc(ctx, req, "eth_call", []any{map[string]string{"to": contract, "data": data}, "latest"}, "tron_eth_call", &result)
	if err != nil {
		return "", err
	}
	if result == "" || result == "0x" {
		return "", errors.New("empty eth_call result")
	}
	return result, nil
}

func decodeAnkrTokenString(value string) (string, error) {
	b, err := encodinghex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(b) < 32 {
		return "", errors.New("invalid token string result")
	}
	if len(b) >= 64 {
		offsetWord := new(big.Int).SetBytes(b[:32])
		if !offsetWord.IsUint64() || offsetWord.Uint64() > uint64(len(b)) {
			return "", errors.New("invalid token string offset")
		}
		offset := int(offsetWord.Uint64())
		if offset >= 0 && offset+32 <= len(b) {
			lengthWord := new(big.Int).SetBytes(b[offset : offset+32])
			if !lengthWord.IsUint64() || lengthWord.Uint64() > uint64(len(b)) {
				return "", errors.New("invalid token string length")
			}
			length := int(lengthWord.Uint64())
			if length >= 0 && offset+32+length <= len(b) {
				return strings.TrimRight(string(b[offset+32:offset+32+length]), "\x00"), nil
			}
		}
	}
	return strings.TrimRight(string(b[:32]), "\x00"), nil
}

func decodeAnkrTokenUint8(value string) (uint8, error) {
	b, err := encodinghex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(b) < 32 {
		return 0, errors.New("invalid token decimals result")
	}
	n := new(big.Int).SetBytes(b[len(b)-32:])
	if !n.IsUint64() || n.Uint64() > 255 {
		return 0, errors.New("token decimals outside 0..255")
	}
	return uint8(n.Uint64()), nil
}

func topicToTron(topic string) (string, error) {
	s := strings.TrimPrefix(topic, "0x")
	if len(s) != 64 {
		return "", errors.New("invalid TRON log topic address")
	}
	return EVMHex20ToBase58T("0x" + s[24:])
}

type ankrBlockRange struct {
	Blocks []map[string]any `json:"block"`
}

func (c *AnkrClient) fetchTransactions(ctx context.Context, req provider.FetchRequest, target string, from, to uint64) ([]json.RawMessage, []byte, error) {
	cacheKey := fmt.Sprintf("blocks:%d:%d", from, to)
	value, err, _ := c.blockGroup.Do(cacheKey, func() (any, error) {
		c.mu.RLock()
		raw := append([]byte(nil), c.blockCache[cacheKey]...)
		c.mu.RUnlock()
		if len(raw) > 0 {
			return raw, nil
		}
		var fetched ankrBlockRange
		raw, fetchErr := c.rest(ctx, req, "wallet/getblockbylimitnext", map[string]any{"startNum": from, "endNum": to + 1, "visible": true}, "tron_getblockbylimitnext", &fetched)
		if fetchErr == nil {
			c.cacheBlockRange(cacheKey, raw)
		}
		return raw, fetchErr
	})
	raw, _ := value.([]byte)
	if err != nil {
		return nil, raw, err
	}
	var result ankrBlockRange
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, raw, err
	}
	var rows []json.RawMessage
	for _, block := range result.Blocks {
		header := mapValue(block["block_header"])
		headerRaw := mapValue(header["raw_data"])
		blockNo := uintValue(headerRaw["number"])
		ts := uintValue(headerRaw["timestamp"])
		txs, _ := block["transactions"].([]any)
		for _, txAny := range txs {
			tx := mapValue(txAny)
			rawData := mapValue(tx["raw_data"])
			contracts, _ := rawData["contract"].([]any)
			related := -1
			for i, contractAny := range contracts {
				contract := mapValue(contractAny)
				typ := stringValue(contract["type"])
				value := mapValue(mapValue(contract["parameter"])["value"])
				owner := canonicalAddress(value["owner_address"])
				toAddr := canonicalAddress(value["to_address"])
				if toAddr == "" {
					toAddr = canonicalAddress(value["contract_address"])
				}
				if (typ == "TransferContract" || typ == "TransferAssetContract") && (owner == target || toAddr == target) {
					related = i
					break
				}
				if typ == "TriggerSmartContract" && (owner == target || toAddr == target) {
					related = i
					break
				}
				if owner == target {
					related = i
					break
				}
			}
			if related < 0 {
				continue
			}
			tx["blockNumber"], tx["block_timestamp"], tx["_tron_target_contract_index"], tx["_provider"] = blockNo, ts, related, "ankr"
			b, _ := json.Marshal(tx)
			rows = append(rows, b)
		}
	}
	return rows, raw, nil
}

func (c *AnkrClient) fetchInternal(ctx context.Context, req provider.FetchRequest, target string, from, to uint64) ([]json.RawMessage, []byte, error) {
	var rows []json.RawMessage
	rawPages := make([]json.RawMessage, 0, to-from+1)
	for block := from; block <= to; block++ {
		var receipts []map[string]any
		raw, err := c.receiptsByBlock(ctx, req, block)
		if err != nil {
			return nil, joinRaw(rawPages), err
		}
		if err = json.Unmarshal(raw, &receipts); err != nil {
			return nil, joinRaw(rawPages), err
		}
		rawPages = append(rawPages, append([]byte(nil), raw...))
		for _, receipt := range receipts {
			txID := stringValue(receipt["id"])
			timestamp := uintValue(receipt["blockTimeStamp"])
			receiptResult := strings.ToUpper(stringValue(mapValue(receipt["receipt"])["result"]))
			failed := receiptResult != "" && receiptResult != "SUCCESS" && receiptResult != "SUCESS"
			internals, _ := receipt["internal_transactions"].([]any)
			for internalIndex, internalAny := range internals {
				internal := mapValue(internalAny)
				caller := canonicalAddress(internal["caller_address"])
				recipient := canonicalAddress(internal["transferTo_address"])
				if caller != target && recipient != target {
					continue
				}
				rejected, _ := internal["rejected"].(bool)
				rejected = rejected || failed
				values, _ := internal["callValueInfo"].([]any)
				if len(values) == 0 {
					values = []any{map[string]any{}}
				}
				for valueIndex, valueAny := range values {
					value := mapValue(valueAny)
					callValue := stringValue(value["callValue"])
					if callValue == "" {
						callValue = "0"
					}
					tokenID := stringValue(value["tokenId"])
					identity := internalIndex*1000 + valueIndex
					m := map[string]any{"tx_id": txID, "internal_tx_id": stringValue(internal["hash"]), "block_number": block, "block_timestamp": timestamp, "from_address": caller, "to_address": recipient, "_tron_internal_index": identity, "_provider": "ankr", "data": map[string]any{"call_value": map[string]any{"_": callValue}, "token_id": tokenID, "rejected": rejected, "note": stringValue(internal["note"])}}
					b, _ := json.Marshal(m)
					rows = append(rows, b)
				}
			}
		}
		if block == ^uint64(0) {
			break
		}
	}
	return rows, joinRaw(rawPages), nil
}

func (c *AnkrClient) receiptsByBlock(ctx context.Context, req provider.FetchRequest, block uint64) ([]byte, error) {
	key := strconv.FormatUint(block, 10)
	value, err, _ := c.receiptGroup.Do(key, func() (any, error) {
		c.mu.RLock()
		raw := append([]byte(nil), c.receiptCache[block]...)
		c.mu.RUnlock()
		if len(raw) > 0 {
			return raw, nil
		}
		var receipts []map[string]any
		raw, fetchErr := c.rest(ctx, req, "wallet/gettransactioninfobyblocknum", map[string]any{"num": block, "visible": true}, "tron_receipt_by_block", &receipts)
		if fetchErr == nil {
			c.cacheReceiptBlock(block, raw)
		}
		return raw, fetchErr
	})
	raw, _ := value.([]byte)
	return raw, err
}

func joinRaw(items []json.RawMessage) []byte { b, _ := json.Marshal(items); return b }

func (c *AnkrClient) cacheBlockRange(key string, raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.blockCache[key]; !ok {
		c.cacheOrder = append(c.cacheOrder, key)
	}
	c.blockCache[key] = append([]byte(nil), raw...)
	for len(c.cacheOrder) > blockCacheEntries {
		old := c.cacheOrder[0]
		c.cacheOrder = c.cacheOrder[1:]
		delete(c.blockCache, old)
	}
}

func (c *AnkrClient) cacheReceiptBlock(block uint64, raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.receiptCache == nil {
		c.receiptCache = map[uint64][]byte{}
	}
	if _, ok := c.receiptCache[block]; !ok {
		c.receiptOrder = append(c.receiptOrder, block)
	}
	c.receiptCache[block] = append([]byte(nil), raw...)
	for len(c.receiptOrder) > receiptCacheEntries {
		old := c.receiptOrder[0]
		c.receiptOrder = c.receiptOrder[1:]
		delete(c.receiptCache, old)
	}
}

func (c *AnkrClient) AccountState(ctx context.Context, address string) (AccountKind, error) {
	canonical, err := NormalizeTronAddress(address)
	if err != nil {
		return AccountUnknown, err
	}
	var contract map[string]any
	_, contractErr := c.rest(ctx, provider.FetchRequest{Chain: "tron", Dataset: "contract"}, "wallet/getcontract", map[string]any{"value": canonical, "visible": true}, "tron_getcontract", &contract)
	if contractErr == nil && len(contract) > 0 && stringValue(contract["contract_address"]) != "" {
		return AccountContract, nil
	}
	var account map[string]any
	_, err = c.rest(ctx, provider.FetchRequest{Chain: "tron", Dataset: "account"}, "wallet/getaccount", map[string]any{"address": canonical, "visible": true}, "tron_getaccount", &account)
	if err != nil {
		return AccountUnknown, err
	}
	if len(account) == 0 || stringValue(account["address"]) == "" {
		return AccountValidUnactivated, nil
	}
	return AccountEOA, nil
}

func (c *AnkrClient) ResolveAddressTypes(ctx context.Context, chain string, addresses []string) (map[string]bool, error) {
	out := make(map[string]bool, len(addresses))
	if chain != "tron" {
		return out, nil
	}
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

func (c *AnkrClient) Probe(ctx context.Context) (AnkrCapabilities, error) {
	var caps AnkrCapabilities
	latest, err := c.LatestBlock(ctx)
	if err != nil {
		return caps, err
	}
	caps.JSONRPC = true
	probeBlock := latest
	if probeBlock > 20 {
		probeBlock -= 20
	}
	var logs []ankrLog
	if _, err = c.rpc(ctx, provider.FetchRequest{Chain: "tron", Dataset: "probe"}, "eth_getLogs", []any{map[string]any{"fromBlock": fmt.Sprintf("0x%x", probeBlock), "toBlock": fmt.Sprintf("0x%x", probeBlock), "topics": []any{transferTopic}}}, "tron_eth_getLogs", &logs); err != nil {
		return caps, err
	}
	caps.Logs = true
	var blocks ankrBlockRange
	if _, err = c.rest(ctx, provider.FetchRequest{Chain: "tron", Dataset: "probe"}, "wallet/getblockbylimitnext", map[string]any{"startNum": probeBlock, "endNum": probeBlock + 1, "visible": true}, "tron_getblockbylimitnext", &blocks); err != nil {
		return caps, err
	}
	caps.NativeREST, caps.BlockRange = true, true
	var receipts []map[string]any
	if _, err = c.rest(ctx, provider.FetchRequest{Chain: "tron", Dataset: "probe"}, "wallet/gettransactioninfobyblocknum", map[string]any{"num": probeBlock, "visible": true}, "tron_receipt_by_block", &receipts); err != nil {
		return caps, err
	}
	caps.ReceiptByBlock = true
	var solid map[string]any
	if _, err = c.rest(ctx, provider.FetchRequest{Chain: "tron", Dataset: "probe"}, "walletsolidity/getnowblock", map[string]any{"visible": true}, "tron_solidity_getnowblock", &solid); err == nil && len(solid) > 0 {
		caps.Solidity = true
	}
	return caps, nil
}

func (c *AnkrClient) rpc(ctx context.Context, req provider.FetchRequest, method string, params any, usageType string, out any) ([]byte, error) {
	payload := map[string]any{"jsonrpc": "2.0", "method": method, "params": params, "id": 1}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body, err := c.call(ctx, req, c.rpcURL, payload, usageType, &envelope)
	if err != nil {
		return body, err
	}
	if envelope.Error != nil {
		return body, &provider.Error{Provider: c.Name(), Class: rpcErrorClass(envelope.Error.Code), RPCCode: envelope.Error.Code, Message: "Ankr TRON JSON-RPC: " + envelope.Error.Message, Retryable: envelope.Error.Code == -32005, SplitRange: envelope.Error.Code == -32005}
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return body, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidJSON, Message: "Ankr TRON invalid JSON-RPC result", Cause: err}
	}
	return body, nil
}

func (c *AnkrClient) rpcBatch(ctx context.Context, req provider.FetchRequest, calls []ankrRPCBatchCall, usageType string) ([]ankrRPCBatchCall, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	payload := make([]map[string]any, len(calls))
	for i, call := range calls {
		payload[i] = map[string]any{"jsonrpc": "2.0", "method": call.method, "params": call.params, "id": i + 1}
	}
	type envelope struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	var responses []envelope
	_, err := c.call(ctx, req, c.rpcURL, payload, usageType, &responses)
	if err != nil {
		return nil, err
	}
	if len(responses) != len(calls) {
		return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassProviderInvalid, Message: fmt.Sprintf("Ankr TRON JSON-RPC batch returned %d/%d responses", len(responses), len(calls))}
	}
	seen := make([]bool, len(calls))
	failed := make([]ankrRPCBatchCall, 0)
	errorCodes := make(map[int]int)
	firstMessage := ""
	for _, response := range responses {
		if response.ID < 1 || response.ID > len(calls) || seen[response.ID-1] {
			return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassProviderInvalid, Message: "Ankr TRON JSON-RPC batch returned an invalid response id"}
		}
		seen[response.ID-1] = true
		if response.Error != nil {
			failed = append(failed, calls[response.ID-1])
			errorCodes[response.Error.Code]++
			if firstMessage == "" {
				firstMessage = c.sanitizeError(response.Error.Message)
			}
			continue
		}
		if err := json.Unmarshal(response.Result, calls[response.ID-1].out); err != nil {
			failed = append(failed, calls[response.ID-1])
		}
	}
	if len(failed) > 0 && c.log != nil {
		c.log.Warn("TRC20 JSON-RPC batch partial failure", "failed", len(failed), "total", len(calls), "codes", errorCodes, "sample", firstMessage)
	}
	return failed, nil
}

func (c *AnkrClient) rest(ctx context.Context, req provider.FetchRequest, path string, payload any, usageType string, out any) ([]byte, error) {
	return c.call(ctx, req, c.restURL+"/"+strings.TrimLeft(path, "/"), payload, usageType, out)
}

func (c *AnkrClient) call(ctx context.Context, fetchReq provider.FetchRequest, endpoint string, payload any, usageType string, out any) ([]byte, error) {
	if c.runtime != nil {
		rpcURL, restURL, enabled, err := c.runtime(ctx)
		if err != nil {
			return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassUpstream, Message: "Ankr control state unavailable", Retryable: true}
		}
		if !enabled {
			return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassUpstream, Message: "Ankr is disabled in provider control", Retryable: true}
		}
		if strings.HasPrefix(endpoint, c.rpcURL) {
			endpoint = rpcURL + strings.TrimPrefix(endpoint, c.rpcURL)
		}
		if strings.HasPrefix(endpoint, c.restURL) {
			endpoint = restURL + strings.TrimPrefix(endpoint, c.restURL)
		}
	}
	encoded, _ := json.Marshal(payload)
	methodCalls, batchSize := 1, 0
	if batch, ok := payload.([]map[string]any); ok {
		methodCalls, batchSize = len(batch), len(batch)
	}
	var last error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<min(attempt-1, 5))*250*time.Millisecond + time.Duration(rand.Intn(150))*time.Millisecond
			select {
			case <-ctx.Done():
				return nil, provider.NewCancelled(ctx.Err().Error())
			case <-time.After(delay):
			}
		}
		started := time.Now()
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
		if requestErr != nil {
			return nil, &provider.Error{Provider: c.Name(), Class: provider.ClassInvalidRequest, Message: "Ankr TRON endpoint is invalid"}
		}
		request.Header.Set("content-type", "application/json")
		if err := provider.ReserveDownloadRequest(ctx, int64(methodCalls), float64(methodCalls)*200, float64(methodCalls)*0.00002); err != nil {
			return nil, err
		}
		response, err := c.http.Do(request)
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		var body []byte
		if err == nil {
			body, err = io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
			response.Body.Close()
			if len(body) > maxResponseBytes {
				err = errors.New("Ankr TRON response exceeds 32 MiB")
			}
		}
		if err == nil && status >= 200 && status < 300 {
			if err = json.Unmarshal(body, out); err == nil {
				c.recordAnkr(ctx, fetchReq, usageType, started, status, attempt, "success", "", 0, len(body), methodCalls, batchSize)
				c.setAnkrHealth(provider.HealthNormal, "")
				return body, nil
			}
		}
		class, retryable := classifyAnkr(status, err)
		message := c.ankrBoundedError(status, body, err, endpoint)
		c.recordAnkr(ctx, fetchReq, usageType, started, status, attempt, "error", string(class), 0, len(body), methodCalls, batchSize)
		last = &provider.Error{Provider: c.Name(), Class: class, Message: message, HTTPStatus: status, Retryable: retryable, ReduceConcurrency: status == 429, SplitRange: class == provider.ClassResponseTooLarge || class == provider.ClassRequestTimeout || status >= 500}
		c.setAnkrHealth(provider.HealthDegraded, message)
		if !retryable {
			break
		}
	}
	return nil, last
}

func (c *AnkrClient) recordAnkr(ctx context.Context, req provider.FetchRequest, usageType string, started time.Time, status, attempt int, outcome, class string, rows, size, methodCalls, batchSize int) {
	if c.usage == nil {
		return
	}
	c.usage(model.UsageRow{Provider: "ankr", Method: usageType, Dataset: req.Dataset, JobID: contextValue(ctx, "job_id"), ShardID: contextValue(ctx, "shard_id"), RequestTime: started, DurationMs: int(time.Since(started).Milliseconds()), Status: outcome, HTTPStatus: status, Rows: rows, Bytes: size, WireBytes: int64(size), DecodedBytes: int64(size), ErrorClass: class, PageSize: req.PageSize, ReturnedRows: rows, ResponseBytes: size, RetryAttempt: attempt, RequestID: req.RequestID, UsageType: usageType, APIType: "node", Chain: "tron", MethodClass: usageType, MethodCalls: methodCalls, BatchSize: batchSize, LogicalCalls: methodCalls, HTTPRequests: 1, Credits: float64(methodCalls) * 200, CostUSD: float64(methodCalls) * 0.00002})
}

func (c *AnkrClient) setAnkrHealth(state provider.HealthState, message string) {
	c.mu.Lock()
	c.health.State, c.health.Error, c.health.LastCheck = state, message, time.Now()
	c.mu.Unlock()
}

func shouldShrink(err error) bool {
	var pe *provider.Error
	return errors.As(err, &pe) && (pe.SplitRange || pe.Class == provider.ClassResponseTooLarge || pe.Class == provider.ClassRequestTimeout || pe.Class == provider.ClassUpstream)
}
func rpcErrorClass(code int) provider.ErrorClass {
	if code == -32005 {
		return provider.ClassResponseTooLarge
	}
	if code == -32601 {
		return provider.ClassUnsupported
	}
	if code == -32602 {
		return provider.ClassInvalidRequest
	}
	return provider.ClassUpstream
}
func classifyAnkr(status int, err error) (provider.ErrorClass, bool) {
	if status == 429 {
		return provider.ClassRateLimit, true
	}
	if status == 401 || status == 403 {
		return provider.ClassAuth, false
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
	if err != nil && strings.Contains(err.Error(), "exceeds 32 MiB") {
		return provider.ClassResponseTooLarge, true
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
func (c *AnkrClient) ankrBoundedError(status int, body []byte, err error, endpoint string) string {
	msg := ""
	if err != nil {
		msg = c.sanitizeError(err.Error())
	}
	if len(body) > 0 {
		if len(body) > 4096 {
			body = body[:4096]
		}
		msg = c.sanitizeError(strings.TrimSpace(string(body)))
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	msg = strings.ReplaceAll(msg, endpoint, "https://rpc.ankr.com/***")
	for _, marker := range []string{"/tron_jsonrpc/", "/premium-http/tron/"} {
		if start := strings.Index(endpoint, marker); start >= 0 {
			key := strings.SplitN(endpoint[start+len(marker):], "/", 2)[0]
			if len(key) >= 8 {
				msg = strings.ReplaceAll(msg, key, "***")
			}
		}
	}
	return fmt.Sprintf("Ankr TRON HTTP %d: %s", status, msg)
}

func (c *AnkrClient) sanitizeError(message string) string {
	message = strings.ReplaceAll(message, c.rpcURL, "https://rpc.ankr.com/tron_jsonrpc/***")
	message = strings.ReplaceAll(message, c.restURL, "https://rpc.ankr.com/premium-http/tron/***")
	return message
}
func parseUint(value string) (uint64, error) {
	s := strings.TrimSpace(value)
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		base, s = 16, s[2:]
	}
	return strconv.ParseUint(s, base, 64)
}

var _ provider.Provider = (*AnkrClient)(nil)
var _ provider.AddressTypeResolver = (*AnkrClient)(nil)
