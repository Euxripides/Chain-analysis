package ankr

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
	"strconv"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// MockProvider 无 API Key 时用于开发与端到端演示的确定性数据源。
type MockProvider struct {
	pageSize    int
	usageHook   provider.UsageHook
	requestSeq  int64
	maxPages    int
}

func NewMock(pageSize int, hook provider.UsageHook) *MockProvider {
	if pageSize <= 0 {
		pageSize = 1000
	}
	return &MockProvider{pageSize: pageSize, usageHook: hook, maxPages: 5}
}

func (m *MockProvider) Name() string { return "ankr-mock" }

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
				row = map[string]any{
					"blockHeight": fmt.Sprintf("%d", b), "blockHash": hex(b), "transactionHash": hex(b*1000+i),
					"timestamp": int64(time.Now().UnixMilli()) - int64((to-b)*12*1000),
					"fromAddress": req.Address, "toAddress": hex(seed + i), "value": hex(rng.Uint64() % 1000000),
					"nonce": fmt.Sprintf("%d", i), "gasLimit": fmt.Sprintf("%d", 21000), "gasUsed": fmt.Sprintf("%d", 21000),
					"gasPrice": hex(1000000000 + rng.Uint64()%100), "type": "0x0", "status": "0x1",
					"input": "0x", "methodId": "0x",
				}
			case "token_transfers":
				row = map[string]any{
					"blockHeight": fmt.Sprintf("%d", b), "timestamp": int64(time.Now().UnixMilli()) - int64((to-b)*12*1000),
					"transactionHash": hex(b*1000+i), "logIndex": fmt.Sprintf("%d", i),
					"fromAddress": req.Address, "toAddress": hex(seed+i), "value": fmt.Sprintf("%d", rng.Uint64()%1000000000000),
					"tokenName": "Mock Token", "tokenSymbol": "MCK", "tokenDecimals": 18, "contractAddress": hex(seed+7),
				}
			case "nft_transfers":
				row = map[string]any{
					"blockHeight": fmt.Sprintf("%d", b), "timestamp": int64(time.Now().UnixMilli()) - int64((to-b)*12*1000),
					"transactionHash": hex(b*1000+i), "logIndex": fmt.Sprintf("%d", i),
					"fromAddress": req.Address, "toAddress": hex(seed+i), "contractAddress": hex(seed+9),
					"tokenId": fmt.Sprintf("%d", i+1), "tokenName": "MockNFT", "tokenSymbol": "MNFT", "value": "1",
				}
			case "logs":
				row = map[string]any{
					"address": req.Address, "topics": []string{hex(rng.Uint64()), hex(rng.Uint64()%16)},
					"data": "0x" + strconv.FormatUint(rng.Uint64()%4096, 16),
					"blockNumber": fmt.Sprintf("%d", b), "transactionHash": hex(b*1000+i),
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
