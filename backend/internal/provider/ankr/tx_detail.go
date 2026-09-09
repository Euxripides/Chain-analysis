package ankr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// MethodTxDetail Ankr 单笔交易深度接口（§23）。
const MethodTxDetail = "ankr_getTransactionsByHash"

// TxDetail 按需深度解码单笔交易（§23/§25）。
//
// 硬约束：
//   - 只允许交易详情 / 智能调查按需调用，批量下载禁止逐笔调用（§62-1）；
//   - 每次请求都写 provider_usage，usage_type = tx_deep_decode（§49/§62-10）。
func (c *Client) TxDetail(ctx context.Context, chain, txHash string, decodeTxData, decodeLogs bool) (json.RawMessage, error) {
	ok, state := c.breaker.allow()
	if !ok {
		observability.AnkrCircuitRejection.WithLabelValues(chain, "tx_detail").Inc()
		return nil, &provider.Error{Provider: "ankr", Class: provider.ClassUpstream,
			Message: fmt.Sprintf("provider circuit breaker %s", state), Retryable: true}
	}
	if err := c.limiter.Acquire(ctx); err != nil {
		return nil, c.classifyCtxErr(ctx, err, 0)
	}
	params := map[string]any{
		"blockchain":      chain,
		"transactionHash": txHash,
		"decodeTxData":    decodeTxData,
		"decodeLogs":      decodeLogs,
	}
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: int(c.idCounter.Add(1)), Method: MethodTxDetail, Params: params})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/"+c.apiKey, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept-Encoding", "gzip")

	start := time.Now()
	observability.AnkrInflight.Inc()
	res, err := c.http.Do(httpReq)
	observability.AnkrInflight.Dec()
	elapsed := time.Since(start)
	if err != nil {
		pe := c.classifyCtxErr(ctx, err, 0)
		pe.Duration = elapsed
		c.recordFailure(pe)
		c.recordDeepUsage(txHash, "error", pe, elapsed, 0)
		return nil, pe
	}
	defer res.Body.Close()

	respBody, _, _, exceeded, _, rerr := c.readBody(res)
	if rerr != nil {
		pe := &provider.Error{}
		if pe2, ok := rerr.(*provider.Error); ok {
			pe = pe2
		} else {
			pe = &provider.Error{Provider: "ankr", Class: provider.ClassUpstream,
				Message: "read response: " + rerr.Error(), Retryable: true}
		}
		pe.Provider = "ankr"
		pe.Duration = elapsed
		c.recordFailure(pe)
		c.recordDeepUsage(txHash, "error", pe, elapsed, 0)
		return nil, pe
	}
	if exceeded {
		pe := provider.NewResponseTooLarge("tx detail response exceeded hard limit", 0, c.hardLimit)
		pe.Provider = "ankr"
		c.recordFailure(pe)
		c.recordDeepUsage(txHash, "error", pe, elapsed, 0)
		return nil, pe
	}
	if res.StatusCode != http.StatusOK {
		pe := c.classifyHTTPError(res.StatusCode, respBody, parseRetryAfter(res.Header))
		pe.Duration = elapsed
		c.recordFailure(pe)
		c.recordDeepUsage(txHash, "error", pe, elapsed, len(respBody))
		return nil, pe
	}
	var rpc rpcResponse
	if err := json.Unmarshal(respBody, &rpc); err != nil {
		pe := &provider.Error{Provider: "ankr", Class: provider.ClassInvalidJSON,
			Message: "invalid json: " + err.Error(), Retryable: true, Cause: err}
		c.recordFailure(pe)
		c.recordDeepUsage(txHash, "error", pe, elapsed, len(respBody))
		return nil, pe
	}
	if rpc.Error != nil {
		pe := c.classifyRPCError(rpc.Error, parseRetryAfter(res.Header))
		pe.Duration = elapsed
		c.recordFailure(pe)
		c.recordDeepUsage(txHash, "error", pe, elapsed, len(respBody))
		return nil, pe
	}
	c.recordSuccess()
	c.recordDeepUsage(txHash, "ok", nil, elapsed, len(respBody))
	return rpc.Result, nil
}

// recordDeepUsage 深度解码用量（§49：tx_hash / credits / cost / duration / status / cache_hit）。
func (c *Client) recordDeepUsage(txHash, status string, pe *provider.Error, elapsed time.Duration, respBytes int) {
	observability.AnkrRequests.WithLabelValues("ankr", "transaction_detail", status).Inc()
	observability.AnkrLatencyMs.WithLabelValues("ankr", "transaction_detail").Observe(float64(elapsed.Milliseconds()))
	if status == "ok" {
		observability.AnkrRequestsSuccess.WithLabelValues("ankr", "transaction_detail").Inc()
	} else {
		observability.AnkrRequestsFailed.WithLabelValues("ankr", "transaction_detail").Inc()
	}
	if c.usageHook == nil {
		return
	}
	requestBudget := 1 // 交易详情单次操作最多允许一次真实深度请求
	u := model.UsageRow{
		Provider: "ankr", Method: MethodTxDetail, Dataset: "transaction_detail",
		RequestTime: time.Now(), DurationMs: int(elapsed.Milliseconds()), Status: status,
		Bytes: respBytes, ResponseBytes: respBytes, Credits: c.credits, CostUSD: c.costUSD,
		UsageType: model.UsageTypeTxDeepDecode, TxHash: txHash, CacheHit: false,
		DecodePriority: "interactive", DecodeBudget: &requestBudget,
	}
	if pe != nil {
		u.HTTPStatus = pe.HTTPStatus
		u.APIError = pe.Message
		u.ErrorClass = string(pe.Class)
		u.RPCCode = pe.RPCCode
		u.TraceID = pe.TraceID
	}
	c.usageHook(u)
}

var _ provider.TxDetailFetcher = (*Client)(nil)
