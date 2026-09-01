package ankr

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// MockProvider 无 API Key 时用于开发与端到端演示的确定性数据源。
type MockProvider struct {
	pageSize   int
	usageHook  provider.UsageHook
	requestSeq int64
	maxPages   int
}

func NewMock(pageSize int, hook provider.UsageHook) *MockProvider {
	if pageSize <= 0 {
		pageSize = 1000
	}
	return &MockProvider{pageSize: pageSize, usageHook: hook, maxPages: 5}
}

func (m *MockProvider) Name() string { return "ankr-mock" }

func (m *MockProvider) ResolveTokenMetadata(_ context.Context, _ string, contract string) (provider.TokenMetadataResult, error) {
	name, symbol, decimals := "Mock Token", "MOCK", 18
	if strings.HasSuffix(strings.ToLower(contract), "55d398326f99059ff775485246999027b3197955") {
		name, symbol, decimals = "Tether USD", "USDT", 18
	}
	return provider.TokenMetadataResult{Name: &name, Symbol: &symbol, Decimals: &decimals}, nil
}

func (m *MockProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Name:        "ankr-mock",
		Datasets:    model.AllDatasets,
		MaxPageSize: m.pageSize,
		SupportsTimeRange: map[string]bool{
			"transactions": true, "token_transfers": true, "nft_transfers": false, "logs": false, "balances": false,
		},
	}
}

func (m *MockProvider) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return provider.FetchResult{}, err
	}
	if req.Dataset == "balances" {
		rows := m.mockBalances(req.Chain, req.Address)
		m.hook(ctx, req, "ankr_getAccountBalance", "ok", time.Since(start), len(rows))
		return provider.FetchResult{Rows: rows, HasMore: false}, nil
	}

	from, to := uint64(1), uint64(1000)
	if req.FromBlock != nil {
		from = *req.FromBlock
	}
	if req.ToBlock != nil && *req.ToBlock >= from {
		to = *req.ToBlock
	}
	if to-from > 10_000_000 {
		to = from + 10_000_000
	}
	span := to - from + 1
	blocksPerPage := (span + uint64(m.maxPages) - 1) / uint64(m.maxPages)
	if blocksPerPage < 1 {
		blocksPerPage = 1
	}
	page := 0
	if req.PageToken != "" {
		if n, err := strconv.Atoi(req.PageToken); err == nil {
			page = n
		}
	}
	pFrom := from + uint64(page)*blocksPerPage
	pTo := pFrom + blocksPerPage - 1
	if pTo > to {
		pTo = to
	}
	if pFrom > to {
		return provider.FetchResult{Rows: nil, HasMore: false}, nil
	}
	rows := m.mockRows(req, pFrom, pTo)
	next := ""
	hasMore := pTo < to
	if hasMore {
		next = strconv.Itoa(page + 1)
	}
	m.hook(ctx, req, methodByDataset[req.Dataset], "ok", time.Since(start), len(rows))
	// 模拟 Provider 延迟
	select {
	case <-ctx.Done():
		return provider.FetchResult{}, ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}
	return provider.FetchResult{Rows: rows, NextPageToken: next, HasMore: hasMore}, nil
}

func (m *MockProvider) hook(ctx context.Context, req provider.FetchRequest, method, status string, elapsed time.Duration, rows int) {
	if m.usageHook == nil {
		return
	}
	var jobID, shardID string
	if v := ctx.Value("job_id"); v != nil {
		jobID, _ = v.(string)
	}
	if v := ctx.Value("shard_id"); v != nil {
		shardID, _ = v.(string)
	}
	m.usageHook(model.UsageRow{
		Provider: "ankr-mock", Method: method, Dataset: req.Dataset,
		JobID: jobID, ShardID: shardID, RequestTime: time.Now(),
		DurationMs: int(elapsed.Milliseconds()), Status: status, HTTPStatus: 200,
		Rows: rows, Bytes: rows * 256, Credits: 700, CostUSD: 0.00007,
	})
}

func hash64(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

func hex(v uint64) string { return "0x" + strconv.FormatUint(v, 16) }

func (m *MockProvider) mockRows(req provider.FetchRequest, from, to uint64) []json.RawMessage {
	seed := hash64(req.Dataset, req.Address, strconv.FormatUint(from, 16))
	rng := rand.New(rand.NewSource(int64(seed)))
	var rows []json.RawMessage
	for b := from; b <= to; b++ {
		var n uint64
		switch req.Dataset {
		case "nft_transfers":
			n = rng.Uint64() % 3
		default:
			n = rng.Uint64() % 7
		}
		for i := uint64(0); i < n; i++ {
			var row map[string]any
			switch req.Dataset {
			case "transactions":
				// V1.3：Mock 必须覆盖 Method 解析的全部分支
				// （原生转账 / ERC20 / Swap / 未知 Selector / 合约创建），
				// 否则本地开发看不到 Method 列的真实行为。
				input := mockInput(rng, seed+i)
				value := hex(rng.Uint64() % 1000000)
				if input != "0x" {
					value = "0x0"
				}
				toAddress := any(hex(seed + i))
				if rng.Uint64()%37 == 0 {
					toAddress = nil // 合约创建（§38）
					input = "0x60806040523480156100" + strconv.FormatUint(rng.Uint64(), 16)
				}
				row = map[string]any{
					"blockHeight": fmt.Sprintf("%d", b), "blockHash": hex(b), "transactionHash": hex(b*1000 + i),
					"timestamp":   int64(time.Now().UnixMilli()) - int64((to-b)*12*1000),
					"fromAddress": req.Address, "toAddress": toAddress, "value": value,
					"nonce": fmt.Sprintf("%d", i), "gasLimit": fmt.Sprintf("%d", 21000), "gasUsed": fmt.Sprintf("%d", 21000),
					"gasPrice": hex(1000000000 + rng.Uint64()%100), "type": "0x0", "status": "0x1",
					"input": input,
				}
			case "token_transfers":
				row = map[string]any{
					"blockHeight": fmt.Sprintf("%d", b), "timestamp": int64(time.Now().UnixMilli()) - int64((to-b)*12*1000),
					"transactionHash": hex(b*1000 + i), "logIndex": fmt.Sprintf("%d", i),
					"fromAddress": req.Address, "toAddress": hex(seed + i), "value": fmt.Sprintf("%d", rng.Uint64()%1000000000000),
					"tokenName": "Mock Token", "tokenSymbol": "MCK", "tokenDecimals": 18, "contractAddress": hex(seed + 7),
				}
			case "nft_transfers":
				row = map[string]any{
					"blockHeight": fmt.Sprintf("%d", b), "timestamp": int64(time.Now().UnixMilli()) - int64((to-b)*12*1000),
					"transactionHash": hex(b*1000 + i), "logIndex": fmt.Sprintf("%d", i),
					"fromAddress": req.Address, "toAddress": hex(seed + i), "contractAddress": hex(seed + 9),
					"tokenId": fmt.Sprintf("%d", i+1), "tokenName": "MockNFT", "tokenSymbol": "MNFT", "value": "1",
				}
			case "logs":
				row = map[string]any{
					"address": req.Address, "topics": []string{hex(rng.Uint64()), hex(rng.Uint64() % 16)},
					"data":        "0x" + strconv.FormatUint(rng.Uint64()%4096, 16),
					"blockNumber": fmt.Sprintf("%d", b), "transactionHash": hex(b*1000 + i),
					"transactionIndex": fmt.Sprintf("%d", i), "blockHash": hex(b), "logIndex": fmt.Sprintf("%d", i),
					"removed": false, "timestamp": int64(time.Now().UnixMilli()) - int64((to-b)*12*1000),
				}
			}
			b, _ := json.Marshal(row)
			rows = append(rows, b)
		}
	}
	return rows
}

// word 32 字节 ABI 字（十六进制，无 0x）。
func word(value string) string {
	if len(value) >= 64 {
		return value[len(value)-64:]
	}
	return strings.Repeat("0", 64-len(value)) + value
}

func addressWord(v uint64) string { return word("00000000000000000000" + strconv.FormatUint(v, 16)) }

// mockInput 生成覆盖各类 Method 分支的 input（§55 用例形态）。
func mockInput(rng *rand.Rand, salt uint64) string {
	switch rng.Uint64() % 10 {
	case 0, 1, 2, 3:
		return "0x" // 原生转账
	case 4, 5:
		// transfer(address,uint256)
		return "0xa9059cbb" + addressWord(salt) + word(strconv.FormatUint(rng.Uint64()%1e12, 16))
	case 6:
		// approve(address,uint256)
		return "0x095ea7b3" + addressWord(salt+1) + word("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	case 7:
		// transferFrom(address,address,uint256)
		return "0x23b872dd" + addressWord(salt+2) + addressWord(salt+3) + word(strconv.FormatUint(rng.Uint64()%1e9, 16))
	case 8:
		// swapExactTokensForTokens(uint256,uint256,address[],address,uint256)
		return "0x38ed1739" +
			word(strconv.FormatUint(rng.Uint64()%1e12, 16)) +
			word("0") +
			word("a0") + // path 偏移量
			addressWord(salt+4) +
			word(strconv.FormatUint(uint64(time.Now().Unix()), 16)) +
			word("2") + addressWord(salt+5) + addressWord(salt+6)
	default:
		// 未知 Selector：Data Explorer 必须原样显示（§26/§62-19）
		return "0x12ab34cd" + addressWord(salt+7) + word(strconv.FormatUint(rng.Uint64()%1e6, 16))
	}
}

// TxDetail Mock 深度解码（§23）：让本地开发也能验证详情页与缓存路径。
func (m *MockProvider) TxDetail(ctx context.Context, chain, txHash string, decodeTxData, decodeLogs bool) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start := time.Now()
	detail := map[string]any{
		"hash": txHash, "blockchain": chain,
		"method": map[string]any{
			"id": "0xa9059cbb", "name": "transfer", "verified": true,
			"string": "transfer(address to, uint256 value)",
			"inputs": []any{
				map[string]any{"name": "to", "type": "address", "value": hex(hash64(txHash))},
				map[string]any{"name": "value", "type": "uint256", "size": 256, "value": "1000000000000000000"},
			},
		},
	}
	if decodeLogs {
		detail["logs"] = []any{map[string]any{
			"address": hex(hash64(txHash, "log")), "logIndex": "0x0",
			"topics": []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"},
			"data":   "0x",
			"event": map[string]any{
				"name": "Transfer", "verified": true,
				"string": "Transfer(address from, address to, uint256 value)",
				"inputs": []any{
					map[string]any{"name": "from", "type": "address", "indexed": true, "value": hex(hash64(txHash, "from"))},
					map[string]any{"name": "to", "type": "address", "indexed": true, "value": hex(hash64(txHash, "to"))},
					map[string]any{"name": "value", "type": "uint256", "value": "1000000000000000000"},
				},
			},
		}}
	}
	raw, err := json.Marshal(map[string]any{"transactions": []any{detail}})
	if err != nil {
		return nil, err
	}
	if m.usageHook != nil {
		m.usageHook(model.UsageRow{
			Provider: "ankr-mock", Method: MethodTxDetail, Dataset: "transaction_detail",
			RequestTime: time.Now(), DurationMs: int(time.Since(start).Milliseconds()), Status: "ok",
			HTTPStatus: 200, Bytes: len(raw), Credits: 700, CostUSD: 0.00007,
			UsageType: model.UsageTypeTxDeepDecode, TxHash: txHash,
		})
	}
	return raw, nil
}

var _ provider.TxDetailFetcher = (*MockProvider)(nil)

func (m *MockProvider) mockBalances(chain, address string) []json.RawMessage {
	var rows []json.RawMessage
	for i := 0; i < 3; i++ {
		row, _ := json.Marshal(map[string]any{
			"blockchain": chain, "tokenName": "Mock Token " + strconv.Itoa(i), "tokenSymbol": "MCK" + strconv.Itoa(i),
			"tokenDecimals": 18, "tokenType": "ERC20", "holderAddress": address,
			"balance": "100.5", "balanceRawInteger": "100500000000000000000", "balanceUsd": "123.45",
			"tokenPrice": "1.23", "thumbnail": "",
		})
		rows = append(rows, row)
	}
	return rows
}

func (m *MockProvider) Health(ctx context.Context) provider.HealthStatus {
	_ = ctx
	return provider.HealthStatus{Provider: "ankr-mock", State: provider.HealthNormal, LastCheck: time.Now()}
}
