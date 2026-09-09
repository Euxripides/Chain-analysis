// V1.2 Chainbase Count：TX / Token Transfers 计数（§13/§14）。
// 两个 Count 必须并行（§15）——由 CountOracle Service 负责并发调度，这里只做单路请求与解析。
// 语义：Chainbase /v1/* 响应 { code, message, data, next_page, count }；
// count 优先按总行数理解（§13 page=1&limit=1 → count 即总量），不明确时用轻量二分探测兜底。
package chainbase

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// countPage 单页解析结果。
type countPage struct {
	rows          int
	hasNext       bool
	explicitTotal int64 // total/total_count/header/X-Total-Count
	explicitOK    bool
	pageCount     int64 // body.count
	pageCountOK   bool
}

// endpointPath 数据集 → Chainbase 端点（§13/§14）。
func endpointPath(dataset string) string {
	switch dataset {
	case "token_transfers":
		return "/v1/token/transfers"
	case "transactions":
		return "/v1/account/txs"
	default:
		return ""
	}
}

func countMethod(dataset string) string {
	switch dataset {
	case "token_transfers":
		return "chainbase_token_transfers"
	default:
		return "chainbase_account_txs"
	}
}

// Count 返回 dataset 在 [from,to] 上的总行数（from=0/to=0 → 不带边界）。
// 返回 (rawCount, 请求数, error)。成功/失败的全部副作用（breaker/usage/健康窗口）在 fetchPage 内完成。
func (c *Client) Count(ctx context.Context, chainID int64, dataset, address string, from, to uint64, probeBudget int) (int64, int, error) {
	if c.apiKey == "" {
		return -1, 0, cbError(provider.ClassAuth, "chainbase api key not configured", false)
	}
	path := endpointPath(dataset)
	if path == "" {
		return -1, 0, cbError(provider.ClassInvalidRequest, "dataset not supported by chainbase: "+dataset, false)
	}
	if ok, state := c.breaker.allow(); !ok {
		return -1, 0, &provider.Error{Provider: "chainbase", Class: provider.ClassUpstream,
			Message: fmt.Sprintf("chainbase circuit %s", state), Retryable: true, ReduceConcurrency: true}
	}
	if err := c.limiter.Acquire(ctx); err != nil {
		return -1, 0, provider.NewTimeout("chainbase count rate limit wait", 0, err)
	}
	if probeBudget <= 0 {
		probeBudget = 22
	}

	first, _, err := c.fetchPage(ctx, chainID, dataset, address, from, to, 1, 1)
	if err != nil {
		return -1, 1, err
	}
	requests := 1
	// 1) 显式总数（total/total_count）
	if first.explicitOK {
		return first.explicitTotal, requests, nil
	}
	// 2) count=0 且空数据 → 0
	if first.pageCountOK && first.pageCount == 0 && first.rows == 0 && !first.hasNext {
		return 0, requests, nil
	}
	// 3) count > 页行数 → 总行数语义（limit=1 时 count>1 不可能是页大小）
	if first.pageCountOK && first.pageCount > int64(first.rows) {
		return first.pageCount, requests, nil
	}
	// 4) 无后续页（或未提供分页字段且页未满）→ 数据即全部
	if !first.hasNext {
		total := int64(first.rows)
		if first.pageCountOK && first.pageCount > total {
			total = first.pageCount
		}
		return total, requests, nil
	}
	// 5) 首页满（limit=1）且有后续 → count 语义不明确：二分探测最后一个有数据的页
	total, probeReqs, err := c.probeTotal(ctx, chainID, dataset, address, from, to, probeBudget)
	requests += probeReqs
	if err != nil {
		return -1, requests, err
	}
	return total, requests, nil
}

// probeTotal 倍增+二分探测最后一个有数据的页（limit=1 → 页数 = 总行数）。
// 每次探测都是一次轻量请求（预算 = CHAINBASE_COUNT_PROBE_MAX_REQUESTS）。
func (c *Client) probeTotal(ctx context.Context, chainID int64, dataset, address string, from, to uint64, budget int) (int64, int, error) {
	requests := 0
	hasRow := func(page int) (bool, error) {
		p, _, err := c.fetchPage(ctx, chainID, dataset, address, from, to, page, 1)
		requests++
		if err != nil {
			return false, err
		}
		return p.rows > 0 || (p.pageCountOK && p.pageCount > 0), nil
	}
	// 倍增找上界
	hi := 1
	foundAny := false
	for requests < budget-1 {
		ok, err := hasRow(hi)
		if err != nil {
			return -1, requests, err
		}
		if !ok {
			break
		}
		foundAny = true
		if hi >= 4_000_000 {
			break
		}
		hi *= 2
	}
	if !foundAny {
		return 0, requests, nil // 第 1 页就没有数据
	}
	if hi >= 4_000_000 {
		return int64(hi), requests, nil // 超预算上限：下界估计仍可用于 Planning
	}
	// [1, hi] 中最后一个有数据的页
	lo, last := 1, 1
	for lo <= hi && requests < budget {
		mid := (lo + hi) / 2
		ok, err := hasRow(mid)
		if err != nil {
			return -1, requests, err
		}
		if ok {
			last = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return int64(last), requests, nil
}

// fetchPage 单页 GET。调用方只负责 breaker/limiter 准入；本函数完成全部记录
// （健康窗口、breaker、usage 行、观测指标）。返回 (页信息, 请求耗时ms, error)。
func (c *Client) fetchPage(ctx context.Context, chainID int64, dataset, address string, from, to uint64, page, limit int) (countPage, int64, error) {
	u, err := c.buildURL(chainID, dataset, address, from, to, page, limit)
	if err != nil {
		return countPage{}, 0, err
	}
	start := time.Now()
	body, _, _, err := c.doGet(ctx, u)
	elapsedMs := time.Since(start).Milliseconds()
	method := countMethod(dataset)
	if err != nil {
		var pe *provider.Error
		if pe2, ok := err.(*provider.Error); ok {
			pe = pe2
		} else {
			pe = cbError(provider.ClassUpstream, err.Error(), true)
		}
		pe.Provider = "chainbase"
		c.recordNetworkResult(false, elapsedMs)
		c.recordFailure(pe)
		c.recordUsage(ctx, dataset, method, "error", pe, time.Duration(elapsedMs)*time.Millisecond, 0, 0, 0)
		return countPage{}, elapsedMs, pe
	}
	if pe, perr := c.parseEnvelopeError(body); perr != nil {
		// 响应体本身不可解析 → InvalidJSON
		ipe := &provider.Error{Provider: "chainbase", Class: provider.ClassInvalidJSON,
			Message: "invalid chainbase count json: " + perr.Error(), Retryable: true}
		c.recordNetworkResult(false, elapsedMs)
		c.recordFailure(ipe)
		c.recordUsage(ctx, dataset, method, "error", ipe, time.Duration(elapsedMs)*time.Millisecond, 0, 0, 0)
		return countPage{}, elapsedMs, ipe
	} else if pe != nil {
		// HTTP 200 但业务 code != 0（限流/配额/鉴权等，§17：Fail Open）
		pe.Provider = "chainbase"
		c.recordNetworkResult(false, elapsedMs)
		c.recordFailure(pe)
		c.recordUsage(ctx, dataset, method, "error", pe, time.Duration(elapsedMs)*time.Millisecond, 0, 0, 0)
		return countPage{}, elapsedMs, pe
	}
	pg, jerr := c.parseCountPage(body)
	if jerr != nil {
		pe := &provider.Error{Provider: "chainbase", Class: provider.ClassInvalidJSON,
			Message: "invalid chainbase count json: " + jerr.Error(), Retryable: true}
		c.recordNetworkResult(false, elapsedMs)
		c.recordFailure(pe)
		c.recordUsage(ctx, dataset, method, "error", pe, time.Duration(elapsedMs)*time.Millisecond, 0, 0, 0)
		return countPage{}, elapsedMs, pe
	}
	c.recordNetworkResult(true, elapsedMs)
	c.recordSuccess()
	c.recordUsage(ctx, dataset, method, "ok", nil, time.Duration(elapsedMs)*time.Millisecond, 0, 0, pg.rows)
	return pg, elapsedMs, nil
}

// buildURL 构造 count 请求 URL（§13/§14：chain_id/address/page=1/limit=1 [+from_block/to_block]）。
func (c *Client) buildURL(chainID int64, dataset, address string, from, to uint64, page, limit int) (string, error) {
	if chainID <= 0 {
		return "", cbError(provider.ClassInvalidRequest, "chain_id required for chainbase count", false)
	}
	if address == "" {
		return "", cbError(provider.ClassInvalidRequest, "address required for chainbase count", false)
	}
	if limit <= 0 {
		limit = 1
	}
	if page < 1 {
		page = 1
	}
	q := url.Values{}
	q.Set("chain_id", strconv.FormatInt(chainID, 10))
	q.Set("address", strings.ToLower(address))
	q.Set("page", strconv.Itoa(page))
	q.Set("limit", strconv.Itoa(limit))
	if dataset == "transactions" || dataset == "token_transfers" {
		if from > 0 {
			q.Set("from_block", strconv.FormatUint(from, 10))
		}
		if to > 0 {
			q.Set("to_block", strconv.FormatUint(to, 10))
		}
	}
	return c.endpoint + endpointPath(dataset) + "?" + q.Encode(), nil
}

// ---- 响应解析：宽容模式（code/count 可能是 int、float 或 string）----

type cbEnvelope struct {
	Code       json.RawMessage   `json:"code"`
	Message    string            `json:"message"`
	Data       []json.RawMessage `json:"data"`
	NextPage   json.RawMessage   `json:"next_page"`
	Count      json.RawMessage   `json:"count"`
	Total      json.RawMessage   `json:"total"`
	TotalCount json.RawMessage   `json:"total_count"`
}

func rawInt(r json.RawMessage) (int64, bool) {
	if len(r) == 0 || string(r) == "null" {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(r, &f); err == nil {
		return int64(f), true
	}
	var s string
	if err := json.Unmarshal(r, &s); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// parseEnvelopeError code != 0 / 非 ok message → provider.Error（HTTP 200 但业务错误）。
func (c *Client) parseEnvelopeError(body []byte) (*provider.Error, error) {
	var env cbEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if code, ok := rawInt(env.Code); ok && code == 0 {
		return nil, nil
	}
	codeStr := ""
	if len(env.Code) > 0 {
		codeStr = string(env.Code)
	}
	msg := env.Message
	if msg == "" || strings.EqualFold(msg, "ok") || strings.EqualFold(msg, "success") {
		msg = "chainbase returned code " + codeStr
	}
	pe := cbError(provider.ClassUpstream, msg, true)
	classifyByMessage(pe, codeStr, msg)
	if pe.Class == provider.ClassAuth {
		pe.HTTPStatus = http.StatusForbidden
	}
	return pe, nil
}

func (c *Client) parseCountPage(body []byte) (countPage, error) {
	var env cbEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return countPage{}, err
	}
	pg := countPage{rows: len(env.Data)}
	if n, ok := rawInt(env.NextPage); ok {
		pg.hasNext = n >= 2 // 我们总是请求 page=1
	} else {
		// 无 next_page 字段：数据满页 → 保守视为可能还有（二分探测兜底）；空数据 → 结束
		pg.hasNext = len(env.Data) > 0
	}
	if n, ok := rawInt(env.Total); ok {
		pg.explicitTotal, pg.explicitOK = n, true
	}
	if n, ok := rawInt(env.TotalCount); ok {
		pg.explicitTotal, pg.explicitOK = n, true
	}
	if n, ok := rawInt(env.Count); ok {
		pg.pageCount, pg.pageCountOK = n, true
	}
	return pg, nil
}
