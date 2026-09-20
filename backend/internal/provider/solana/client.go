// Package solana 提供 Solana 链的数据 Provider（标准 Solana JSON-RPC）。
//
// 背景（2026-09-20 实测）：Ankr 的 Advanced API（multichain）明确拒绝 solana
// （"invalid blockchain name: solana is not allowed"），但 Ankr 的 Solana 标准 RPC 端点可用
// （https://rpc.ankr.com/solana/<API_KEY> 实测 getVersion 200，solana-core 4.2.2）。
// 因此本 Provider 走标准 JSON-RPC，而不是 Ankr 的 getTransactionsByAddress。
//
// 输出行刻意与 Ankr 的 EVM 行同构（blockNumber/hash/from/to/value/timestamp/...），
// 这样 ingest.NormalizeTransaction 与 ClickHouse/MinIO/Raw 管线无需为 Solana 分叉。
//
// 语义边界（诚实标注，前端只暴露 transactions）：
//   - block 区间 = slot 区间；time 区间与 full 区间在 API 层被显式拒绝；
//   - "to" 取该笔交易中 lamport 净增最大的非 fee payer 账户，取不到时回退 accountKeys[1]；
//   - "value" = fee payer 的净流出减去 fee（原生 SOL，单位 lamports）；
//   - gas/gasUsed/gasPrice/nonce/input 恒为 0/空：Solana 没有 EVM 的 gas 与 nonce 概念，
//     绝不伪造不存在的链上字段。
package solana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

const (
	// MethodSignatures / MethodTransaction 是下载链路实际使用的 RPC 方法名。
	MethodSignatures  = "getSignaturesForAddress"
	MethodTransaction = "getTransaction"
	// MethodVersion 只用于健康检查。
	MethodVersion = "getVersion"

	// maxSignaturesPerCall 是 Solana RPC 对 getSignaturesForAddress limit 的硬上限。
	maxSignaturesPerCall = 1000
)

// Options 构造 Solana Provider。
type Options struct {
	Endpoint       string
	APIKey         string
	MaxPageSize    int
	Concurrency    int
	RequestTimeout time.Duration
	UsageHook      provider.UsageHook
	Log            *slog.Logger
}

// Client 是 Solana JSON-RPC Provider。
type Client struct {
	endpoint    string
	apiKey      string
	maxPageSize int
	concurrency int
	http        *http.Client
	hook        provider.UsageHook
	log         *slog.Logger

	mu      sync.Mutex
	lastErr string
}

// New 构造 Provider；endpoint 为空时返回 nil（调用方据此不注册该 Provider）。
func New(opts Options, hook provider.UsageHook) *Client {
	endpoint := strings.TrimSuffix(strings.TrimSpace(opts.Endpoint), "/")
	if endpoint == "" {
		return nil
	}
	timeout := opts.RequestTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxPage := opts.MaxPageSize
	if maxPage <= 0 {
		maxPage = 20
	}
	if maxPage > maxSignaturesPerCall {
		maxPage = maxSignaturesPerCall
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		endpoint: endpoint, apiKey: strings.TrimSpace(opts.APIKey),
		maxPageSize: maxPage, concurrency: concurrency,
		http: &http.Client{Timeout: timeout},
		hook: hook, log: log,
	}
}

func (c *Client) Name() string { return "solana" }

func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Name:        "solana",
		Datasets:    []string{"transactions"},
		MaxPageSize: c.maxPageSize,
		// Solana 没有可用的时间→slot 索引：时间区间在 API 层直接拒绝。
		SupportsTimeRange: map[string]bool{"transactions": false},
	}
}

func (c *Client) Health(ctx context.Context) provider.HealthStatus {
	status := provider.HealthStatus{Provider: "solana", State: provider.HealthNormal}
	_, err := c.call(ctx, MethodVersion, nil)
	status.LastCheck = time.Now().UTC()
	if err != nil {
		status.State = provider.HealthOpen
		status.Error = err.Error()
		status.ConsecutiveFailure = 1
		c.mu.Lock()
		c.lastErr = err.Error()
		c.mu.Unlock()
		return status
	}
	c.mu.Lock()
	c.lastErr = ""
	c.mu.Unlock()
	return status
}

// ---- JSON-RPC 传输 ----

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// call 执行一次 JSON-RPC 调用，返回 result 原始 JSON。
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, &provider.Error{Provider: "solana", Class: provider.ClassInvalidRequest, Message: err.Error()}
	}
	url := c.endpoint
	if c.apiKey != "" {
		url = c.endpoint + "/" + c.apiKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, &provider.Error{Provider: "solana", Class: provider.ClassInvalidRequest, Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, c.classifyTransport(ctx, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if readErr != nil {
		return nil, &provider.Error{Provider: "solana", Class: provider.ClassBodyTruncated,
			Message: "response body read failed: " + readErr.Error(), Retryable: true, Cause: readErr}
	}
	if res.StatusCode != http.StatusOK {
		class := provider.ClassUpstream
		retryable := true
		switch {
		case res.StatusCode == http.StatusTooManyRequests:
			class, retryable = provider.ClassRateLimit, true
		case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
			class, retryable = provider.ClassAuth, false
		case res.StatusCode >= 400 && res.StatusCode < 500:
			class, retryable = provider.ClassInvalidRequest, false
		}
		return nil, &provider.Error{Provider: "solana", Class: class, HTTPStatus: res.StatusCode,
			Message: fmt.Sprintf("solana rpc http %d", res.StatusCode), Retryable: retryable,
			ReduceRate: class == provider.ClassRateLimit}
	}
	var envelope rpcResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, &provider.Error{Provider: "solana", Class: provider.ClassInvalidJSON,
			Message: "invalid json: " + err.Error(), Retryable: true, Cause: err}
	}
	if envelope.Error != nil {
		class := provider.ClassUpstream
		retryable := true
		if envelope.Error.Code == -32602 || envelope.Error.Code == -32600 || envelope.Error.Code == -32601 {
			class, retryable = provider.ClassInvalidRequest, false
		}
		return nil, &provider.Error{Provider: "solana", Class: class, RPCCode: envelope.Error.Code,
			Message: fmt.Sprintf("solana rpc error %d: %s", envelope.Error.Code, envelope.Error.Message), Retryable: retryable}
	}
	return envelope.Result, nil
}

// callWithSize 与 call 相同，但额外返回 result 载荷字节数。
//
// 只声明 decoded 字节：Go 的 http.Transport 默认对 gzip 透明解压，客户端拿不到
// 真实 wire 字节；与其把 decoded 冒充 wire，不如让 wire 保持 0（诚实标注）。
func (c *Client) callWithSize(ctx context.Context, method string, params any) (json.RawMessage, int64, error) {
	result, err := c.call(ctx, method, params)
	if err != nil {
		return nil, 0, err
	}
	return result, int64(len(result)), nil
}

func (c *Client) classifyTransport(ctx context.Context, err error) *provider.Error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return provider.NewCancelled("solana request cancelled: " + ctxErr.Error())
	}
	if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "Client.Timeout") {
		return &provider.Error{Provider: "solana", Class: provider.ClassRequestTimeout,
			Message: err.Error(), Retryable: true, ReduceConcurrency: true, Cause: err}
	}
	return &provider.Error{Provider: "solana", Class: provider.ClassUpstream,
		Message: err.Error(), Retryable: true, Cause: err}
}

// ---- 数据类型 ----

type signatureInfo struct {
	Signature string `json:"signature"`
	Slot      uint64 `json:"slot"`
	BlockTime *int64 `json:"blockTime"`
	Err       any    `json:"err"`
	Memo      string `json:"memo"`
}

type accountKey struct {
	Pubkey   string `json:"pubkey"`
	Signer   bool   `json:"signer"`
	Writable bool   `json:"writable"`
}

type txResult struct {
	Slot        uint64 `json:"slot"`
	BlockTime   *int64 `json:"blockTime"`
	Transaction struct {
		Signatures []string `json:"signatures"`
		Message    struct {
			RecentBlockhash string       `json:"recentBlockhash"`
			AccountKeys     []accountKey `json:"accountKeys"`
		} `json:"message"`
	} `json:"transaction"`
	Meta *struct {
		Err          any     `json:"err"`
		Fee          uint64  `json:"fee"`
		PreBalances  []int64 `json:"preBalances"`
		PostBalances []int64 `json:"postBalances"`
	} `json:"meta"`
}

// txRow 与 Ankr EVM 交易行同构，供 ingest.NormalizeTransaction 直接消费。
type txRow struct {
	BlockNumber  string `json:"blockNumber"`
	BlockHash    string `json:"blockHash"`
	Hash         string `json:"hash"`
	Timestamp    int64  `json:"timestamp"`
	From         string `json:"from"`
	To           string `json:"to"`
	Value        string `json:"value"`
	Nonce        string `json:"nonce"`
	Gas          string `json:"gas"`
	GasUsed      string `json:"gasUsed"`
	GasPrice     string `json:"gasPrice"`
	Type         string `json:"type"`
	Input        string `json:"input"`
	Status       string `json:"status"`
	Slot         uint64 `json:"slot"`
	Fee          uint64 `json:"fee"`
	Confirmation string `json:"confirmation"`
}

// ---- Fetch ----

// Fetch 实现 provider.Provider。当前只支持 chain=solana + dataset=transactions。
func (c *Client) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	if !strings.EqualFold(req.Chain, "solana") {
		return provider.FetchResult{}, &provider.Error{Provider: "solana", Class: provider.ClassInvalidRequest,
			Message: "solana provider only serves chain=solana, got " + req.Chain}
	}
	if req.Dataset != "transactions" {
		return provider.FetchResult{}, &provider.Error{Provider: "solana", Class: provider.ClassUnsupported,
			Message: "solana provider does not serve dataset " + req.Dataset}
	}
	if req.FromTime != nil || req.ToTime != nil {
		return provider.FetchResult{}, &provider.Error{Provider: "solana", Class: provider.ClassInvalidRequest,
			Message: "solana provider does not support time ranges; use a slot (block) range"}
	}

	pageSize := req.PageSize
	if pageSize <= 0 || pageSize > c.maxPageSize {
		pageSize = c.maxPageSize
	}

	var fromSlot, toSlot uint64
	if req.FromBlock != nil {
		fromSlot = *req.FromBlock
	}
	if req.ToBlock != nil {
		toSlot = *req.ToBlock
	}

	started := time.Now()
	params := []any{req.Address, map[string]any{"limit": pageSize}}
	if req.PageToken != "" {
		params[1].(map[string]any)["before"] = req.PageToken
	}
	result, signatureBytes, err := c.callWithSize(ctx, MethodSignatures, params)
	if err != nil {
		c.recordUsage(req, MethodSignatures, 0, time.Since(started), "error", err)
		return provider.FetchResult{}, err
	}
	var signatures []signatureInfo
	if err := json.Unmarshal(result, &signatures); err != nil {
		perr := &provider.Error{Provider: "solana", Class: provider.ClassInvalidJSON,
			Message: "invalid getSignaturesForAddress result: " + err.Error(), Retryable: true, Cause: err}
		c.recordUsage(req, MethodSignatures, 0, time.Since(started), "error", perr)
		return provider.FetchResult{}, perr
	}
	if len(signatures) == 0 {
		c.recordUsage(req, MethodSignatures, 0, time.Since(started), "ok", nil)
		return provider.FetchResult{
			RequestID: req.RequestID, AttemptNo: req.AttemptNo, RequestPageSize: pageSize,
			RequestFromBlock: req.FromBlock, RequestToBlock: req.ToBlock,
			DecodedBytes: signatureBytes,
			StartedAt:    started, BodyCompleteAt: time.Now(),
		}, nil
	}

	// 区间过滤：signatures 按 slot 从新到旧。比 to 新的跳过，比 from 旧的即停止翻页。
	selected := make([]signatureInfo, 0, len(signatures))
	oldestSeen := signatures[len(signatures)-1].Signature
	reachedOlder := false
	for _, info := range signatures {
		if toSlot > 0 && info.Slot > toSlot {
			continue
		}
		if fromSlot > 0 && info.Slot < fromSlot {
			reachedOlder = true
			break
		}
		selected = append(selected, info)
	}
	hasMore := !reachedOlder && len(signatures) == pageSize
	nextToken := ""
	if hasMore {
		nextToken = oldestSeen
	}

	rows, transactionBytes, txErr := c.fetchTransactions(ctx, selected)
	if txErr != nil {
		c.recordUsage(req, MethodTransaction, 0, time.Since(started), "error", txErr)
		return provider.FetchResult{}, txErr
	}
	encoded := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		payload, err := json.Marshal(row)
		if err != nil {
			continue
		}
		encoded = append(encoded, payload)
	}
	c.recordUsage(req, MethodTransaction, len(encoded), time.Since(started), "ok", nil)
	return provider.FetchResult{
		Rows: encoded, NextPageToken: nextToken, HasMore: hasMore,
		RequestID: req.RequestID, AttemptNo: req.AttemptNo, RequestPageSize: pageSize,
		RequestFromBlock: req.FromBlock, RequestToBlock: req.ToBlock,
		DecodedBytes: signatureBytes + transactionBytes,
		StartedAt:    started, BodyCompleteAt: time.Now(),
	}, nil
}

// fetchTransactions 并发抓取已选 signature 的交易；任一失败即整页失败——
// 静默丢行会让 Coverage 误判为「已完整下载」。
func (c *Client) fetchTransactions(ctx context.Context, selected []signatureInfo) ([]txRow, int64, error) {
	if len(selected) == 0 {
		return nil, 0, nil
	}
	rows := make([]txRow, len(selected))
	errs := make([]error, len(selected))
	sizes := make([]int64, len(selected))
	sem := make(chan struct{}, c.concurrency)
	var wg sync.WaitGroup
	for i := range selected {
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			row, size, err := c.fetchTransaction(ctx, selected[i])
			if err != nil {
				errs[i] = err
				return
			}
			rows[i] = row
			sizes[i] = size
		}()
	}
	wg.Wait()
	var total int64
	for i, err := range errs {
		if err != nil {
			return nil, 0, fmt.Errorf("solana getTransaction %s failed: %w", selected[i].Signature, err)
		}
		total += sizes[i]
	}
	return rows, total, nil
}

func (c *Client) fetchTransaction(ctx context.Context, info signatureInfo) (txRow, int64, error) {
	params := []any{info.Signature, map[string]any{"encoding": "jsonParsed", "maxSupportedTransactionVersion": 0}}
	result, size, err := c.callWithSize(ctx, MethodTransaction, params)
	if err != nil {
		return txRow{}, 0, err
	}
	if len(result) == 0 || string(result) == "null" {
		return txRow{}, size, fmt.Errorf("transaction %s not found", info.Signature)
	}
	var tx txResult
	if err := json.Unmarshal(result, &tx); err != nil {
		return txRow{}, size, &provider.Error{Provider: "solana", Class: provider.ClassInvalidJSON,
			Message: "invalid getTransaction result: " + err.Error(), Retryable: true, Cause: err}
	}
	return buildRow(info, &tx), size, nil
}

func buildRow(info signatureInfo, tx *txResult) txRow {
	from := ""
	if len(tx.Transaction.Message.AccountKeys) > 0 {
		from = tx.Transaction.Message.AccountKeys[0].Pubkey
	}
	status := "0x1"
	if tx.Meta == nil || tx.Meta.Err != nil || info.Err != nil {
		status = "0x0"
	}
	blockTime := info.BlockTime
	if blockTime == nil {
		blockTime = tx.BlockTime
	}
	var timestamp int64
	if blockTime != nil {
		timestamp = *blockTime
	}
	if timestamp == 0 {
		timestamp = time.Now().Unix()
	}
	slot := tx.Slot
	if slot == 0 {
		slot = info.Slot
	}
	row := txRow{
		BlockNumber:  strconv.FormatUint(slot, 10),
		BlockHash:    tx.Transaction.Message.RecentBlockhash,
		Hash:         info.Signature,
		Timestamp:    timestamp,
		From:         from,
		To:           counterparty(tx),
		Value:        nativeValue(tx),
		Nonce:        "0",
		Gas:          "0",
		GasUsed:      "0",
		GasPrice:     "0",
		Type:         "solana",
		Input:        "",
		Status:       status,
		Slot:         slot,
		Confirmation: "finalized",
	}
	if tx.Meta != nil {
		row.Fee = tx.Meta.Fee
	}
	return row
}

// counterparty 取 lamport 净增最大的非 fee payer 账户；没有余额变化时回退 accountKeys[1]。
func counterparty(tx *txResult) string {
	keys := tx.Transaction.Message.AccountKeys
	if len(keys) == 0 {
		return ""
	}
	best, bestDelta := "", int64(0)
	if tx.Meta != nil {
		limit := len(keys)
		if len(tx.Meta.PreBalances) < limit {
			limit = len(tx.Meta.PreBalances)
		}
		if len(tx.Meta.PostBalances) < limit {
			limit = len(tx.Meta.PostBalances)
		}
		for i := 1; i < limit; i++ {
			delta := tx.Meta.PostBalances[i] - tx.Meta.PreBalances[i]
			if delta > bestDelta {
				bestDelta, best = delta, keys[i].Pubkey
			}
		}
	}
	if best == "" && len(keys) > 1 {
		best = keys[1].Pubkey
	}
	return best
}

// nativeValue 是 fee payer 的净流出减 fee（原生 SOL，单位 lamports）。
func nativeValue(tx *txResult) string {
	if tx.Meta == nil || len(tx.Meta.PreBalances) == 0 || len(tx.Meta.PostBalances) == 0 {
		return "0"
	}
	outflow := tx.Meta.PreBalances[0] - tx.Meta.PostBalances[0] - int64(tx.Meta.Fee)
	if outflow <= 0 {
		return "0"
	}
	return strconv.FormatInt(outflow, 10)
}

func (c *Client) recordUsage(req provider.FetchRequest, method string, rows int, elapsed time.Duration, status string, perr error) {
	if c.hook == nil {
		return
	}
	usage := model.UsageRow{
		Provider: "solana", Method: method, Dataset: req.Dataset,
		RequestTime: time.Now(), DurationMs: int(elapsed.Milliseconds()),
		Status: status, Rows: rows, PageSize: req.PageSize,
	}
	if perr != nil {
		usage.APIError = perr.Error()
		if pe, ok := perr.(*provider.Error); ok {
			usage.HTTPStatus = pe.HTTPStatus
			usage.RPCCode = pe.RPCCode
			usage.ErrorClass = string(pe.Class)
		}
	}
	c.hook(usage)
}
