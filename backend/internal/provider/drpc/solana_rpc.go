package drpc

// 本文件实现 Solana JSON-RPC 通道（logs / balances），以及单笔交易详情的 RPC 取数。
//
// 请求形态（开发文档 §7.4）：
//
//	POST {base}/{key}  {"jsonrpc":"2.0","id":"<request-id>","method":"getTransaction",
//	                    "params":["<signature>",{"encoding":"jsonParsed","commitment":"finalized",
//	                    "maxSupportedTransactionVersion":0}]}
//
// 复用的解析逻辑来源：internal/provider/solana/client.go 与 instructions.go
// （txResult / counterparty / nativeValue / buildProgramLogRows）。本包刻意复制而不是 import，
// 避免与旧传输层耦合（规范 §0：旧实现保留但不再共用代码）。
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// RPC 方法名（进入 UsageRow.Method 与 Raw manifest）。
const (
	MethodSignatures    = "getSignaturesForAddress"
	MethodTransaction   = "getTransaction"
	MethodSlot          = "getSlot"
	MethodBalance       = "getBalance"
	MethodTokenAccounts = "getTokenAccountsByOwner"
	MethodHealth        = "getHealth"
	MethodVersion       = "getVersion"
)

// 代币程序 id（来源：internal/provider/solana/instructions.go）。
const (
	ProgramSPLToken  = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	ProgramToken2022 = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
)

// APITypeSolanaRPC / APITypeRPC 的别名，便于行契约里写 "solana_rpc" 字面量。
const APITypeSolanaRPC = ChannelSolanaRPC

// ---- JSON-RPC 传输 ----

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage  `json:"result"`
	Error  *rpcErrorPayload `json:"error"`
}

// rpcCallSpec 描述一次 JSON-RPC 方法调用（每次调用写一条用量行）。
type rpcCallSpec struct {
	method    string
	params    any
	cu        int64
	usageType string
	dataset   string
	txHash    string
	rows      int
}

func (c *Client) rpcURL(key string) string { return c.cfg.baseURL + "/" + key }

// callRPC 执行一次 JSON-RPC 调用：通道限流 -> quotaGate -> HTTP -> 信封解码 -> 用量行。
func (c *Client) callRPC(ctx context.Context, exec plan, requestID, key string, spec rpcCallSpec) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0", ID: c.rpcCallID(requestID), Method: spec.method, Params: spec.params,
	})
	if err != nil {
		perr := newError(provider.ClassInvalidRequest, CodeInvalidResponse, sanitizeText(err.Error(), key))
		perr.Retryable = false
		return nil, perr
	}
	call := httpCall{
		apiType: APITypeRPC, dataset: spec.dataset, method: spec.method, usageTyp: spec.usageType,
		cu: spec.cu, key: key, requestID: requestID, httpMethod: http.MethodPost,
		url: c.rpcURL(key), body: body, contentType: "application/json",
		client: c.httpRPC, txHash: spec.txHash,
	}
	started := c.cfg.now()
	release, err := exec.limiter.acquire(ctx, float64(spec.cu))
	if err != nil {
		return nil, c.limiterError(ctx, err)
	}
	defer release()
	if dec := c.quota.decision(); dec.Blocked {
		return nil, c.quota.blockedError(dec)
	}
	result, err := c.doHTTP(ctx, call)
	if err != nil {
		c.finishUsage(call, result, started, 0, err)
		return nil, err
	}
	payload, err := decodeRPCResult(c.cfg.maxTxVersion, result.raw, key)
	if err != nil {
		c.finishUsage(call, result, started, 0, err)
		return nil, err
	}
	c.finishUsage(call, result, started, spec.rows, nil)
	return payload, nil
}

// decodeRPCResult 解码 JSON-RPC 信封：error 按 §7.5 映射，result 原样返回。
func decodeRPCResult(maxTxVersion int, raw []byte, key string) (json.RawMessage, error) {
	var envelope rpcResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
			strings.Contains(err.Error(), "unexpected end of JSON") {
			return nil, bodyTruncated("dRPC rpc response is incomplete: " + err.Error())
		}
		return nil, invalidJSON("dRPC rpc response decode failed: " + err.Error())
	}
	if envelope.Error != nil {
		message := sanitizeText(envelope.Error.Message, key)
		if envelope.Error.Code == -32015 || isTransactionVersionError(message) {
			perr := txVersionUnsupported(maxTxVersion, message)
			// 未知/上游错误码必须原样保留，便于对账与排查（§7.5）。
			perr.RPCCode = envelope.Error.Code
			return nil, perr
		}
		return nil, classifyRPCError(envelope.Error.Code, envelope.Error.Message, maxTxVersion, key)
	}
	return envelope.Result, nil
}

// ---- Fetch 分派 ----

func (c *Client) fetchSolanaRPC(ctx context.Context, req provider.FetchRequest, exec plan) (provider.FetchResult, error) {
	address, err := validateAddress(req.Address)
	if err != nil {
		return provider.FetchResult{}, err
	}
	if err := c.configError(); err != nil {
		return provider.FetchResult{}, err
	}
	requestID := c.requestID(req)
	key, err := c.runtimeKey(ctx)
	if err != nil {
		return provider.FetchResult{}, err
	}
	if exec.dataset == DatasetBalances {
		return c.fetchBalances(ctx, req, exec, requestID, address, key)
	}
	return c.fetchLogs(ctx, req, exec, requestID, address, key)
}

// ---- logs ----

// signatureInfo 是 getSignaturesForAddress 的一条记录（来源：internal/provider/solana/client.go）。
type signatureInfo struct {
	Signature string `json:"signature"`
	Slot      uint64 `json:"slot"`
	BlockTime *int64 `json:"blockTime"`
	Err       any    `json:"err"`
	Memo      string `json:"memo"`
}

// accountKey / txResult 是 getTransaction 的响应子集（来源同上，字段按 jsonParsed 编码）。
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
		Err             any      `json:"err"`
		Fee             uint64   `json:"fee"`
		LogMessages     []string `json:"logMessages"`
		LoadedAddresses struct {
			Writable []string `json:"writable"`
			Readonly []string `json:"readonly"`
		} `json:"loadedAddresses"`
	} `json:"meta"`
}

func (tx *txResult) failed() bool { return tx.Meta == nil || tx.Meta.Err != nil }

// fetchLogs 抓取一页签名，再逐签名取 getTransaction 解出程序日志。
func (c *Client) fetchLogs(ctx context.Context, req provider.FetchRequest, exec plan, requestID, address, key string) (provider.FetchResult, error) {
	pageSize := exec.pageSize
	if req.PageSize > 0 && req.PageSize < pageSize {
		pageSize = req.PageSize
	}
	if pageSize > maxSignaturesPerCall {
		pageSize = maxSignaturesPerCall
	}
	params := []any{address, map[string]any{"limit": pageSize}}
	if req.PageToken != "" {
		params[1].(map[string]any)["before"] = req.PageToken
	}
	started := c.cfg.now()
	signaturesRaw, err := c.callRPC(ctx, exec, requestID, key, rpcCallSpec{
		method: MethodSignatures, params: params, cu: CURPCSignatures,
		usageType: UsageDatasetDownload, dataset: req.Dataset,
	})
	if err != nil {
		return provider.FetchResult{}, err
	}
	var signatures []signatureInfo
	if err := json.Unmarshal(signaturesRaw, &signatures); err != nil {
		perr := invalidJSON("dRPC getSignaturesForAddress result decode failed: " + err.Error())
		return provider.FetchResult{}, perr
	}

	var fromSlot, toSlot uint64
	if req.FromBlock != nil {
		fromSlot = *req.FromBlock
	}
	if req.ToBlock != nil {
		toSlot = *req.ToBlock
	}
	selected := make([]signatureInfo, 0, len(signatures))
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
	nextToken := ""
	if !reachedOlder && len(signatures) == pageSize && len(signatures) > 0 {
		nextToken = signatures[len(signatures)-1].Signature
	}
	var rows []json.RawMessage
	if len(selected) > 0 {
		rows, err = c.fetchLogRows(ctx, exec, req, requestID, key, selected)
		if err != nil {
			return provider.FetchResult{}, err
		}
	}
	// 即使区间内没有交易（空页）也要走游标校验：否则 Provider 反复返回同一页时无法识别死循环。
	state, hasState := PageStateFromContext(ctx)
	outcome, err := c.cursors.validate(cursorKey(req), req.PageToken, nextToken, signaturePageDigest(signatures), rows, state, hasState)
	if err != nil {
		return provider.FetchResult{}, err
	}
	c.log.Info("drpc logs page",
		"address", address, "signatures", len(signatures), "selected", len(selected), "rows", len(rows),
		"has_more", nextToken != "", "next_page_token_hash", outcome.NextPageTokenHash,
		"overlap_rows", outcome.OverlapRows, "mode", exec.mode, "reason", exec.clampReason,
		"effective_rps", exec.limiter.effectiveRPS())
	// 注意：RawResponseBody 只保存**签名页**响应；每笔交易的 getTransaction 响应各自有独立
	// UsageRow 与 request_id，逐笔原始体不属于同一份 SHA-256 载荷（开发文档 §7.3 的单页 Raw 语义）。
	return c.fetchResult(req, exec, requestID, rows, nextToken, nextToken != "", started,
		httpResult{}, signaturesRaw), nil
}

// fetchLogRows 用页内有界 Worker Pool 取详情；任一交易失败即整页失败
// （静默丢行会让 Coverage 误判"已完整下载"，与旧实现同一原则）。
func (c *Client) fetchLogRows(ctx context.Context, exec plan, req provider.FetchRequest, requestID, key string, selected []signatureInfo) ([]json.RawMessage, error) {
	workers := exec.concurrency
	if workers <= 0 {
		workers = 1
	}
	if workers > len(selected) {
		workers = len(selected)
	}
	type outcome struct {
		rows []programLogRow
		err  error
	}
	outcomes := make([]outcome, len(selected))
	work := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range work {
				rows, err := c.fetchLogRowsFor(ctx, exec, req, requestID, key, selected[index])
				outcomes[index] = outcome{rows: rows, err: err}
			}
		}()
	}
	for index := range selected {
		work <- index
	}
	close(work)
	wg.Wait()

	rows := make([]json.RawMessage, 0, len(selected)*4)
	for index, result := range outcomes {
		if result.err != nil {
			return nil, result.err
		}
		for _, row := range result.rows {
			rows = append(rows, encodeOne(row))
		}
		_ = index
	}
	return rows, nil
}

func (c *Client) fetchLogRowsFor(ctx context.Context, exec plan, req provider.FetchRequest, requestID, key string, info signatureInfo) ([]programLogRow, error) {
	params := []any{info.Signature, map[string]any{
		"encoding": "jsonParsed", "commitment": "finalized",
		"maxSupportedTransactionVersion": c.cfg.maxTxVersion,
	}}
	result, err := c.callRPC(ctx, exec, requestID, key, rpcCallSpec{
		method: MethodTransaction, params: params, cu: CURPCCall,
		usageType: UsageDatasetDownload, dataset: req.Dataset, txHash: info.Signature, rows: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(result) == 0 || string(result) == "null" {
		// result=null 无法区分"尚不可用/节点无数据/确实不存在"（§7.4）：
		// 这里显式失败并给出签名，绝不写成成功空行。
		perr := newError(provider.ClassUpstream, CodeInvalidResponse,
			"dRPC getTransaction returned null for "+info.Signature)
		perr.Retryable = true
		return nil, perr
	}
	var tx txResult
	if err := json.Unmarshal(result, &tx); err != nil {
		return nil, invalidJSON("dRPC getTransaction result decode failed: " + err.Error())
	}
	slot := tx.Slot
	if slot == 0 {
		slot = info.Slot
	}
	blockTime := int64(0)
	if tx.BlockTime != nil {
		blockTime = *tx.BlockTime
	} else if info.BlockTime != nil {
		blockTime = *info.BlockTime
	}
	var logs []string
	if tx.Meta != nil {
		logs = tx.Meta.LogMessages
	}
	return buildProgramLogRows(slot, blockTime, info.Signature, logs, !tx.failed(), requestID), nil
}

// buildProgramLogRows 把 meta.logMessages 逐行转成日志行。
//
// 来源：internal/provider/solana/instructions.go 的同名函数，按 §3 契约补齐
// invokeDepth / success / sourceApi / sourceRequestId，并去掉 EVM 风格的 topics 等字段。
// logIndex 从 1 开始，按签名内日志顺序稳定编号（§9.3 的幂等键 chain+signature+log_index）。
func buildProgramLogRows(slot uint64, blockTime int64, signature string, logs []string, success bool, requestID string) []programLogRow {
	rows := make([]programLogRow, 0, len(logs))
	var stack []string
	index := uint32(0)
	for _, raw := range logs {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Program ") {
			rest := strings.TrimPrefix(line, "Program ")
			if idx := strings.Index(rest, " invoke ["); idx >= 0 {
				stack = append(stack, strings.TrimSpace(rest[:idx]))
				continue
			}
			if strings.HasSuffix(rest, " success") || strings.Contains(rest, " failed") {
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				continue
			}
		}
		program := ""
		if len(stack) > 0 {
			program = stack[len(stack)-1]
		}
		var data, eventSource string
		switch {
		case strings.HasPrefix(line, "Program log: "):
			data, eventSource = strings.TrimPrefix(line, "Program log: "), "solana_program_log"
		case strings.HasPrefix(line, "Program data: "):
			data, eventSource = strings.TrimPrefix(line, "Program data: "), "solana_program_data"
		default:
			continue
		}
		index++
		rows = append(rows, programLogRow{
			TransactionHash: signature, BlockNumber: strconv.FormatUint(slot, 10),
			Timestamp: blockTime, LogIndex: index, Address: program, Data: data,
			EventSource: eventSource, ProgramID: "", InvokeDepth: uint16(len(stack)),
			Success: success, Removed: false,
			SourceAPI: APITypeSolanaRPC, SourceRequestID: requestID,
		})
	}
	return rows
}

// ---- balances ----

type slotValue uint64

type balanceResult struct {
	Context struct {
		Slot uint64 `json:"slot"`
	} `json:"context"`
	Value uint64 `json:"value"`
}

type tokenAccountsResult struct {
	Context struct {
		Slot uint64 `json:"slot"`
	} `json:"context"`
	Value []struct {
		Pubkey  string `json:"pubkey"`
		Account struct {
			Owner string          `json:"owner"`
			Data  json.RawMessage `json:"data"`
		} `json:"account"`
	} `json:"value"`
}

type parsedTokenAccount struct {
	Parsed struct {
		Info struct {
			Mint        string `json:"mint"`
			Owner       string `json:"owner"`
			TokenAmount struct {
				Amount   string `json:"amount"`
				Decimals uint8  `json:"decimals"`
			} `json:"tokenAmount"`
		} `json:"info"`
	} `json:"parsed"`
}

// fetchBalances 抓取一次余额快照（单页，不分页）。
//
// 来源：internal/provider/solana/balances.go 的取数逻辑，输出改为 §3 的 balanceRow
// （tokenType 用小写 native|spl|spl-token-2022）。
func (c *Client) fetchBalances(ctx context.Context, req provider.FetchRequest, exec plan, requestID, address, key string) (provider.FetchResult, error) {
	started := c.cfg.now()
	slotRaw, err := c.callRPC(ctx, exec, requestID, key, rpcCallSpec{
		method: MethodSlot, params: []any{}, cu: CURPCCall,
		usageType: UsageBalanceSnapshot, dataset: req.Dataset, rows: 1,
	})
	if err != nil {
		return provider.FetchResult{}, err
	}
	var slot slotValue
	if err := json.Unmarshal(slotRaw, &slot); err != nil {
		return provider.FetchResult{}, invalidJSON("dRPC getSlot result decode failed: " + err.Error())
	}
	snapshot := uint64(slot)
	timestamp := c.cfg.now().Unix()

	rows := make([]balanceRow, 0, 16)
	nativeRaw, err := c.callRPC(ctx, exec, requestID, key, rpcCallSpec{
		method: MethodBalance, params: []any{address}, cu: CURPCCall,
		usageType: UsageBalanceSnapshot, dataset: req.Dataset, rows: 1,
	})
	if err != nil {
		return provider.FetchResult{}, err
	}
	var native balanceResult
	if err := json.Unmarshal(nativeRaw, &native); err != nil {
		return provider.FetchResult{}, invalidJSON("dRPC getBalance result decode failed: " + err.Error())
	}
	rows = append(rows, balanceRow{
		HolderAddress: address, ContractAddress: "", TokenType: "native", TokenDecimals: 9,
		BalanceRawInteger: strconv.FormatUint(native.Value, 10),
		SnapshotBlock:     strconv.FormatUint(snapshot, 10), Timestamp: timestamp,
	})

	for _, program := range []struct{ id, kind string }{
		{ProgramSPLToken, "spl"},
		{ProgramToken2022, "spl-token-2022"},
	} {
		params := []any{address, map[string]any{"programId": program.id}, map[string]any{"encoding": "jsonParsed"}}
		result, err := c.callRPC(ctx, exec, requestID, key, rpcCallSpec{
			method: MethodTokenAccounts, params: params, cu: CURPCCall,
			usageType: UsageBalanceSnapshot, dataset: req.Dataset,
		})
		if err != nil {
			return provider.FetchResult{}, err
		}
		var accounts tokenAccountsResult
		if err := json.Unmarshal(result, &accounts); err != nil {
			return provider.FetchResult{}, invalidJSON("dRPC getTokenAccountsByOwner result decode failed: " + err.Error())
		}
		// 同一 owner 对同一 mint 可能有多个代币账户（ATA）：按 mint 求和后只落一行，
		// 否则 ReplacingMergeTree 的去重键会把多出来的账户静默合并（来源：旧实现实测）。
		perMint := map[string]*big.Int{}
		decimalsByMint := map[string]uint16{}
		for _, account := range accounts.Value {
			var parsed parsedTokenAccount
			if err := json.Unmarshal(account.Account.Data, &parsed); err != nil {
				// 单个账户解析不了就跳过，不丢整次快照（与旧实现一致）。
				continue
			}
			info := parsed.Parsed.Info
			if info.Mint == "" {
				continue
			}
			amount, ok := new(big.Int).SetString(info.TokenAmount.Amount, 10)
			if !ok {
				continue
			}
			if current, exists := perMint[info.Mint]; exists {
				current.Add(current, amount)
			} else {
				perMint[info.Mint] = amount
			}
			decimalsByMint[info.Mint] = uint16(info.TokenAmount.Decimals)
		}
		mints := make([]string, 0, len(perMint))
		for mint := range perMint {
			mints = append(mints, mint)
		}
		sort.Strings(mints)
		for _, mint := range mints {
			rows = append(rows, balanceRow{
				HolderAddress: address, ContractAddress: mint, TokenType: program.kind,
				TokenDecimals: decimalsByMint[mint], BalanceRawInteger: perMint[mint].String(),
				SnapshotBlock: strconv.FormatUint(snapshot, 10), Timestamp: timestamp,
			})
		}
	}

	if len(rows) > maxBalanceAccounts {
		perr := newError(provider.ClassResponseTooLarge, CodeInvalidResponse, fmt.Sprintf(
			"dRPC balance snapshot for %s has %d rows, above the %d cap; refusing to truncate",
			address, len(rows), maxBalanceAccounts))
		perr.Retryable = false
		// 用量已在各次 RPC 调用里记录；这里只补一条失败信号。
		c.recordUsage(usageMeta{
			dataset: req.Dataset, apiType: APITypeRPC, method: MethodTokenAccounts,
			usageTyp: UsageBalanceSnapshot, rows: len(rows), requestID: requestID,
			started: started, err: perr,
		})
		return provider.FetchResult{}, perr
	}
	encoded := encodeRows(rows)
	c.log.Info("drpc balance snapshot", "address", address, "rows", len(encoded),
		"snapshot_slot", snapshot)
	return c.fetchResult(req, exec, requestID, encoded, "", false, started, httpResult{}, nil), nil
}

// ---- 探针 ----

// probeRPC 用 getHealth 探测 JSON-RPC 通道（官方 CU 表：0 CU）。
func (c *Client) probeRPC(ctx context.Context) channelProbe {
	started := c.cfg.now()
	exec, err := c.plan(ctx, DatasetLogs, ChannelSolanaRPC, OperationSolanaDetail)
	if err != nil {
		return c.probeFromError(0, err)
	}
	key, err := c.runtimeKey(ctx)
	if err != nil {
		return c.probeFromError(0, err)
	}
	payload, err := c.callRPC(ctx, exec, "drpc-probe-"+strconv.FormatInt(started.UnixNano(), 10), key, rpcCallSpec{
		method: MethodHealth, params: []any{}, cu: CURPCHealth, usageType: UsageProviderTest,
	})
	if err != nil {
		return c.probeFromError(c.cfg.now().Sub(started), err)
	}
	// getHealth 成功时返回字符串 "ok"；返回其它值说明节点不健康。
	var health string
	if err := json.Unmarshal(payload, &health); err != nil || !strings.EqualFold(health, "ok") {
		perr := newError(provider.ClassUpstream, CodeInvalidResponse,
			"dRPC getHealth returned an unhealthy payload: "+truncate(string(payload), 80))
		perr.Retryable = true
		return c.probeFromError(c.cfg.now().Sub(started), perr)
	}
	return c.probeFromError(c.cfg.now().Sub(started), nil)
}

// ---- 链头 ----

// ChainHead 返回 Solana 当前 finalized slot，用于"全量历史"任务固化目标 Slot
// （开发文档 §5.3：全量 = Data API 不传 start/end，任务创建时用 getSlot 固化目标 Slot）。
//
// 走 JSON-RPC getSlot（params 指定 commitment=finalized），方法与限流、额度闸门、
// 用量记录（UsageType=chain_head）共用 callRPC；未配置密钥时返回 DRPC_NOT_CONFIGURED。
func (c *Client) ChainHead(ctx context.Context) (uint64, error) {
	if err := c.configError(); err != nil {
		return 0, err
	}
	exec, err := c.plan(ctx, DatasetBalances, ChannelSolanaRPC, OperationSolanaBalance)
	if err != nil {
		return 0, err
	}
	key, err := c.runtimeKey(ctx)
	if err != nil {
		return 0, err
	}
	payload, err := c.callRPC(ctx, exec, c.requestID(provider.FetchRequest{}), key, rpcCallSpec{
		method: MethodSlot, params: []any{map[string]any{"commitment": "finalized"}},
		cu: CURPCCall, usageType: UsageChainHead, dataset: DatasetBalances, rows: 1,
	})
	if err != nil {
		return 0, err
	}
	var slot slotValue
	if err := json.Unmarshal(payload, &slot); err != nil {
		return 0, invalidJSON("dRPC getSlot result decode failed: " + err.Error())
	}
	return uint64(slot), nil
}

// signaturePageDigest 用签名列表（含 slot 与失败标记）作为一页内容的摘要。
//
// 日志页的内容就是这批签名对应的交易；签名列表相同即同一页，可用于识别死循环。
func signaturePageDigest(infos []signatureInfo) string {
	hash := sha256.New()
	for _, info := range infos {
		hash.Write([]byte(info.Signature))
		hash.Write([]byte("|"))
		hash.Write([]byte(strconv.FormatUint(info.Slot, 10)))
		if info.Err != nil {
			hash.Write([]byte("|err"))
		}
		hash.Write([]byte(";"))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
