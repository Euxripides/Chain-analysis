package drpc

// 本文件实现 Data API 通道（transactions / token_transfers / nft_transfers 共用同一历史页）。
//
// 请求（开发文档 §7.3）：
//
//	GET {base}/{key}/lambda/v1/transactions/{address}/history?start&end&limit&page_token
//
// start/end 为毫秒；limit 默认 100（DRPC_SOLANA_HISTORY_PAGE_SIZE 钳制）。
// 三个数据集共用一次抓取，由本地投影出不同 Row 契约（§3），不重复请求上游。
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// MethodDataAPIHistory 是 Data API 历史页的方法名（§3 sourceMethod 固定值）。
const MethodDataAPIHistory = "get_transactions_history"

// probeAddress 是探针使用的已知稳定地址：System Program（base58 32 字节全零公钥）。
// 官方没有公开的 Data API health 端点，因此按规范用一次 limit=1 的低成本 history 调用。
const probeAddress = "11111111111111111111111111111111"

// probeLimit 是探针请求的 limit。
const probeLimit = 1

// dataAPIStats 记录一页映射过程中的可见计数：跳过的行必须能被解释，绝不静默丢数据。
type dataAPIStats struct {
	Transactions       int
	TokenTransfers     int
	NFTTransfers       int
	DuplicateRows      int
	SkippedNoSignature int
	SkippedNoMint      int
	SkippedNoAmount    int
	NativeTransfers    int
	NFTInTokenDataset  int
	OverlapRows        int
}

// fetchDataAPI 抓取一页历史并投影成目标数据集的 Row。
func (c *Client) fetchDataAPI(ctx context.Context, req provider.FetchRequest, exec plan) (provider.FetchResult, error) {
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
	baseURL, err := c.effectiveBaseURL(ctx)
	if err != nil {
		return provider.FetchResult{}, err
	}
	limit := exec.pageSize
	if req.PageSize > 0 && req.PageSize < limit {
		limit = req.PageSize
	}
	limit = clampInt(limit, 1, c.cfg.historyPageSize)
	startMS, endMS, err := c.dataAPIRange(ctx, req, requestID)
	if err != nil {
		return provider.FetchResult{}, err
	}
	call := httpCall{
		ctx: ctx, channel: ChannelDataAPI,
		apiType: APITypeDataAPI, dataset: req.Dataset, method: MethodDataAPIHistory,
		usageTyp: UsageDatasetDownload, cu: CUDataAPIHistory, pageSize: limit,
		key: key, requestID: requestID, priority: req.Priority, attemptNo: req.AttemptNo,
		httpMethod: http.MethodGet,
		url:        c.dataAPIURLAt(baseURL, key, address, limit, startMS, endMS, req.PageToken),
		client:     c.httpData,
	}

	started := c.cfg.now()
	release, err := exec.limiter.acquire(ctx, float64(CUDataAPIHistory))
	if err != nil {
		return provider.FetchResult{}, c.limiterError(ctx, err)
	}
	defer release()
	// quotaGate 排在信号量/CU/速率之后（规范 §1.4 的限流顺序）。
	if dec := c.quota.decision(); dec.Blocked {
		return provider.FetchResult{}, c.quota.blockedError(dec)
	}

	result, err := c.doHTTP(ctx, call)
	if err != nil {
		c.finishUsage(call, result, started, 0, err)
		return provider.FetchResult{}, err
	}
	page, err := decodeDataAPIPage(result.raw)
	if err != nil {
		c.finishUsage(call, result, started, 0, err)
		return provider.FetchResult{}, err
	}
	rows, stats := buildDataAPIRows(req.Dataset, page, requestID)

	state, hasState := PageStateFromContext(ctx)
	outcome, err := c.cursors.validate(cursorKey(req), req.PageToken, page.NextPageToken, providerPageDigest(page), rows, state, hasState)
	if err != nil {
		c.finishUsage(call, result, started, len(rows), err)
		return provider.FetchResult{}, err
	}
	stats.OverlapRows = outcome.OverlapRows
	c.log.Info("drpc data api page",
		"dataset", req.Dataset, "address", address, "rows", len(rows),
		"page_rows", len(page.Data), "page_size", limit,
		"has_more", page.NextPageToken != "", "next_page_token_hash", outcome.NextPageTokenHash,
		"duplicate_rows", stats.DuplicateRows, "overlap_rows", stats.OverlapRows,
		"skipped_no_signature", stats.SkippedNoSignature, "skipped_no_mint", stats.SkippedNoMint,
		"skipped_no_amount", stats.SkippedNoAmount, "native_transfers", stats.NativeTransfers,
		"effective_rpm", exec.limiter.effectiveRPM(), "mode", exec.mode, "reason", exec.clampReason,
		"url", maskURL(call.url))
	c.finishUsage(call, result, started, len(rows), nil)
	return c.fetchResult(req, exec, requestID, rows, page.NextPageToken, page.NextPageToken != "", started, result, result.raw), nil
}

func (c *Client) dataAPIURL(key, address string, limit int, startMS, endMS int64, pageToken string) string {
	return c.dataAPIURLAt(c.cfg.baseURL, key, address, limit, startMS, endMS, pageToken)
}

func (c *Client) dataAPIURLAt(baseURL, key, address string, limit int, startMS, endMS int64, pageToken string) string {
	query := url.Values{}
	if startMS > 0 {
		query.Set("start", strconv.FormatInt(startMS, 10))
	}
	if endMS > 0 {
		query.Set("end", strconv.FormatInt(endMS, 10))
	}
	query.Set("limit", strconv.Itoa(limit))
	if pageToken != "" {
		query.Set("page_token", pageToken)
	}
	return baseURL + "/" + key + "/lambda/v1/transactions/" + url.PathEscape(address) + "/history?" + query.Encode()
}

func decodeDataAPIPage(raw []byte) (*dataAPIPage, error) {
	var page dataAPIPage
	if err := json.Unmarshal(raw, &page); err != nil {
		// 不完整的 JSON 是 body 截断，不是"上游返回了非法 JSON"（§7.5 区分两类）。
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
			strings.Contains(err.Error(), "unexpected end of JSON") {
			return nil, bodyTruncated("dRPC data api page is incomplete: " + err.Error())
		}
		return nil, invalidJSON("dRPC data api page decode failed: " + err.Error())
	}
	return &page, nil
}

// buildDataAPIRows 把一页响应投影成目标数据集的 Row（§3 契约）。
func buildDataAPIRows(dataset string, page *dataAPIPage, requestID string) ([]json.RawMessage, dataAPIStats) {
	stats := dataAPIStats{}
	switch dataset {
	case DatasetTransactions:
		rows := make([]json.RawMessage, 0, len(page.Data))
		seen := map[string]struct{}{}
		for _, tx := range page.Data {
			signature := tx.signature()
			if signature == "" {
				stats.SkippedNoSignature++
				continue
			}
			if _, duplicate := seen[signature]; duplicate {
				// 同一页内重复签名：保留第一份，计数可见（不静默）。
				stats.DuplicateRows++
				continue
			}
			seen[signature] = struct{}{}
			rows = append(rows, encodeOne(buildTxRow(tx, signature, requestID)))
		}
		stats.Transactions = len(rows)
		return rows, stats
	case DatasetTokenTransfers:
		rows := make([]json.RawMessage, 0, len(page.Data))
		seen := map[string]struct{}{}
		for _, tx := range page.Data {
			signature := tx.signature()
			if signature == "" {
				stats.SkippedNoSignature++
				continue
			}
			if _, duplicate := seen[signature]; duplicate {
				stats.DuplicateRows++
				continue
			}
			seen[signature] = struct{}{}
			slot := strconv.FormatUint(tx.slot(), 10)
			timestamp := tx.unixSeconds()
			for index, transfer := range tx.Transfers {
				if transfer.isNative() {
					// 原生 SOL 写 address_activity 的 native transfer，不写成 SPL（§8.3）。
					stats.NativeTransfers++
					continue
				}
				if transfer.Token.isNFT() {
					// NFT 只进 nft_transfers，避免同一笔转账进两个数据集。
					stats.NFTInTokenDataset++
					continue
				}
				if transfer.Token.Address == "" {
					stats.SkippedNoMint++
					continue
				}
				amount := string(transfer.AmountString)
				if amount == "" {
					// 缺 amount_string 时不拿浮点 amount 兜底（会引入精度误差），跳过并计数。
					stats.SkippedNoAmount++
					continue
				}
				rows = append(rows, encodeOne(tokenTransferRow{
					BlockHeight: slot, Timestamp: timestamp, TransactionHash: signature,
					LogIndex:        uint32(index),
					ContractAddress: transfer.Token.Address,
					TokenDecimals:   uint16(transfer.Decimals),
					FromAddress:     transfer.SenderAddress,
					ToAddress:       transfer.RecipientAddress,
					ValueRawInteger: amount, Value: amount,
					InstructionIndex: 0, InnerInstructionIndex: -1,
					SourceAPI: APITypeDataAPI, SourceRequestID: requestID,
				}))
			}
		}
		stats.TokenTransfers = len(rows)
		return rows, stats
	default: // DatasetNFTTransfers
		rows := make([]json.RawMessage, 0, len(page.Data))
		seen := map[string]struct{}{}
		for _, tx := range page.Data {
			signature := tx.signature()
			if signature == "" {
				stats.SkippedNoSignature++
				continue
			}
			if _, duplicate := seen[signature]; duplicate {
				stats.DuplicateRows++
				continue
			}
			seen[signature] = struct{}{}
			slot := strconv.FormatUint(tx.slot(), 10)
			timestamp := tx.unixSeconds()
			for index, transfer := range tx.Transfers {
				if !transfer.Token.isNFT() {
					continue
				}
				if transfer.Token.Address == "" {
					stats.SkippedNoMint++
					continue
				}
				rows = append(rows, encodeOne(nftTransferRow{
					BlockNumber: slot, Timestamp: timestamp, TransactionHash: signature,
					LogIndex:        uint32(index),
					ContractAddress: transfer.Token.Address,
					FromAddress:     transfer.SenderAddress,
					ToAddress:       transfer.RecipientAddress,
					Value:           "1",
					TokenStandard:   "SPL",
					SourceAPI:       APITypeDataAPI, SourceRequestID: requestID,
				}))
			}
		}
		stats.NFTTransfers = len(rows)
		return rows, stats
	}
}

// buildTxRow 把一条 Data API 交易映射成 transactions Row（§3）。
//
// to/value 的取值规则（§3 + §8.2）：
//   - 恰好一笔原生转账 -> to=该转账收款方，value=该笔 lamports；
//   - 没有原生转账     -> to=Provider 给的 recipient_address，value 留空；
//   - 多笔原生转账     -> to/value 都留空（多接收方不压缩到单字段）。
func buildTxRow(tx dataAPITx, signature, requestID string) txRow {
	nativeTransferCount := 0
	var nativeTransfer dataAPITransfer
	for _, transfer := range tx.Transfers {
		if transfer.isNative() {
			nativeTransferCount++
			nativeTransfer = transfer
		}
	}
	to, value := tx.RecipientAddress, ""
	from := tx.sender()
	switch nativeTransferCount {
	case 1:
		// 单笔原生转账：from/to/value 都取转账本身的字段，比行级 owner 更准确
		// （owner 是"历史所属地址"，收款交易里它是收款方而不是发起方）。
		if nativeTransfer.SenderAddress != "" {
			from = nativeTransfer.SenderAddress
		}
		to = nativeTransfer.RecipientAddress
		value = string(nativeTransfer.AmountString)
	case 0:
		value = ""
	default:
		to, value = "", ""
	}
	// 手续费：数字形态（Solana）按 1 SOL = 1e9 lamports 精确换算；对象形态只在原生时沿用。
	feeRaw := tx.Fee.lamportsText()
	return txRow{
		BlockNumber: strconv.FormatUint(tx.slot(), 10),
		BlockHash:   "",
		Hash:        signature,
		Timestamp:   tx.unixSeconds(),
		From:        from,
		To:          to,
		Value:       value,
		// Solana 没有 EVM 的 nonce/gas：按契约写 "0"，不虚构含义。
		Nonce: "0", GasLimit: "0", GasUsed: "0", GasPrice: "0",
		Type:   tx.Type,
		Status: tx.succeeded(),
		Input:  "",
		FeeRaw: feeRaw,
		// Data API 不返回签名者/账户表/程序列表：给空数组而不是伪造（§3）。
		Signers:          []string{},
		AccountKeys:      []string{},
		ProgramIDs:       []string{},
		SourceAPI:        APITypeDataAPI,
		SourceMethod:     MethodDataAPIHistory,
		SourceRequestID:  requestID,
		ChainPayloadJSON: buildChainPayload(tx),
	}
}

// chainPayload 是 chainPayloadJson 的结构：只保存无法落到固定列、且来自 Provider 的字段。
type chainPayload struct {
	Chain               string `json:"chain"`
	ProviderType        string `json:"providerType,omitempty"`
	ProviderStatus      string `json:"providerStatus,omitempty"`
	TimestampMS         int64  `json:"timestampMs,omitempty"`
	TransferCount       int    `json:"transferCount"`
	NativeTransferCount int    `json:"nativeTransferCount"`
	NFTTransferCount    int    `json:"nftTransferCount"`
	FeeSymbol           string `json:"feeSymbol,omitempty"`
	DefiName            string `json:"defiName,omitempty"`
	IsTrash             *bool  `json:"isTrash,omitempty"`
}

func buildChainPayload(tx dataAPITx) string {
	payload := chainPayload{
		Chain:          ChainSolana,
		ProviderType:   tx.Type,
		ProviderStatus: tx.Status,
		TimestampMS:    int64(tx.Timestamp),
		TransferCount:  len(tx.Transfers),
	}
	for _, transfer := range tx.Transfers {
		switch {
		case transfer.isNative():
			payload.NativeTransferCount++
		case transfer.Token.isNFT():
			payload.NFTTransferCount++
		}
	}
	if tx.Fee != nil {
		payload.FeeSymbol = tx.Fee.TokenSymbol
	}
	if name, ok := tx.Attributes["defi_name"].(string); ok {
		payload.DefiName = name
	}
	if isTrash, ok := tx.Attributes["is_trash"].(bool); ok {
		payload.IsTrash = &isTrash
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func encodeOne[T any](row T) json.RawMessage {
	encoded, err := json.Marshal(row)
	if err != nil {
		// 结构体全是基础类型，实际不会失败；返回空对象好过返回 nil（调用方按行计数）。
		return json.RawMessage("{}")
	}
	return encoded
}

// ---- 时间 / slot 区间 ----

// dataAPIRange 计算 Data API 的 start/end（毫秒）。
//
// Data API 只接受时间范围，而 FetchRequest 可能给的是 slot 区间（Solana 的 block 区间就是 slot）。
// 这时用 getBlockTime 逐 slot 回溯取值，再把边界向外扩 SlotTimeMargin：
// slot 时间只是近似，不外扩会漏掉边界 slot 里的交易。
func (c *Client) dataAPIRange(ctx context.Context, req provider.FetchRequest, requestID string) (int64, int64, error) {
	if req.FromTime != nil || req.ToTime != nil {
		var start, end int64
		if req.FromTime != nil {
			start = req.FromTime.UTC().UnixMilli()
		}
		if req.ToTime != nil {
			end = req.ToTime.UTC().UnixMilli()
		}
		return start, end, nil
	}
	if req.FromBlock == nil && req.ToBlock == nil {
		// 全量历史：不带 start/end，由 Provider 返回完整历史。
		return 0, 0, nil
	}
	margin := c.cfg.slotTimeMargin.Milliseconds()
	var start, end int64
	if req.FromBlock != nil {
		seconds, err := c.slotTime(ctx, *req.FromBlock, req.Dataset, requestID)
		if err != nil {
			return 0, 0, err
		}
		start = seconds*1000 - margin
		if start < 0 {
			start = 0
		}
	}
	if req.ToBlock != nil {
		seconds, err := c.slotTime(ctx, *req.ToBlock, req.Dataset, requestID)
		if err != nil {
			return 0, 0, err
		}
		end = seconds*1000 + margin
	}
	return start, end, nil
}

// slotTime 求某个 slot 的 blockTime（秒）。
//
// 目标 slot 可能尚未产出（或已被节点裁剪），因此从该 slot 向前回溯，最多 SlotTimeSearchLimit 次；
// 仍然取不到就返回 SOLANA_SLOT_TIME_UNAVAILABLE（绝不拿本地时间冒充链上时间）。
func (c *Client) slotTime(ctx context.Context, slot uint64, dataset, requestID string) (int64, error) {
	rpcExec, err := c.plan(ctx, DatasetLogs, ChannelSolanaRPC, OperationSolanaDetail)
	if err != nil {
		return 0, err
	}
	key, err := c.runtimeKey(ctx)
	if err != nil {
		return 0, err
	}
	for attempt := 0; attempt < c.cfg.slotTimeLimit; attempt++ {
		if uint64(attempt) > slot {
			break
		}
		probe := slot - uint64(attempt)
		result, err := c.callRPC(ctx, rpcExec, requestID, key, rpcCallSpec{
			method: "getBlockTime", params: []any{probe}, cu: CURPCCall,
			usageType: UsageDatasetDownload, dataset: dataset,
		})
		if err != nil {
			return 0, err
		}
		if len(result) == 0 || string(result) == "null" {
			continue
		}
		var seconds int64
		if err := json.Unmarshal(result, &seconds); err != nil {
			return 0, invalidJSON("dRPC getBlockTime result decode failed: " + err.Error())
		}
		if seconds > 0 {
			return seconds, nil
		}
	}
	return 0, slotTimeUnavailable(fmt.Sprintf(
		"cannot resolve block time for slot %d within %d getBlockTime calls", slot, c.cfg.slotTimeLimit))
}

// ---- 探针 ----

// probeDataAPI 用一次 limit=1 的 history 调用探测 Data API 通道。
//
// 探针不走 quotaGate（诊断用途），但仍然经过通道限流器，避免绕过账户速率约束。
func (c *Client) probeDataAPI(ctx context.Context) channelProbe {
	started := c.cfg.now()
	exec, err := c.plan(ctx, DatasetTransactions, ChannelDataAPI, OperationSolanaHistory)
	if err != nil {
		return c.probeFromError(0, err)
	}
	key, err := c.runtimeKey(ctx)
	if err != nil {
		return c.probeFromError(0, err)
	}
	baseURL, err := c.effectiveBaseURL(ctx)
	if err != nil {
		return c.probeFromError(0, err)
	}
	call := httpCall{
		ctx: ctx, channel: ChannelDataAPI,
		apiType: APITypeDataAPI, method: MethodDataAPIHistory, usageTyp: UsageProviderTest,
		cu: CUDataAPIHistory, pageSize: probeLimit, key: key,
		requestID:  "drpc-probe-" + strconv.FormatInt(started.UnixNano(), 10),
		httpMethod: http.MethodGet,
		url:        c.dataAPIURLAt(baseURL, key, probeAddress, probeLimit, 0, 0, ""),
		client:     c.httpData,
	}
	release, err := exec.limiter.acquire(ctx, float64(CUDataAPIHistory))
	if err != nil {
		return c.probeFromError(c.cfg.now().Sub(started), c.limiterError(ctx, err))
	}
	defer release()
	result, err := c.doHTTP(ctx, call)
	if err == nil {
		if _, decodeErr := decodeDataAPIPage(result.raw); decodeErr != nil {
			err = decodeErr
		}
	}
	c.finishUsage(call, result, started, 0, err)
	return c.probeFromError(result.ttfb+result.body, err)
}

// providerPageDigest 计算 Provider 返回内容的摘要，用于识别"连续两页完全相同"。
//
// 不能直接摘要本地投影后的行：行里带每次请求都不同的 sourceRequestId。
func providerPageDigest(page *dataAPIPage) string {
	hash := sha256.New()
	for _, tx := range page.Data {
		encoded, err := json.Marshal(tx)
		if err != nil {
			continue
		}
		hash.Write(encoded)
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
