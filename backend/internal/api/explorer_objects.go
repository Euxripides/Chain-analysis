package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	chainmodel "github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleExplorerBlock(w http.ResponseWriter, r *http.Request) {
	chain := strings.ToLower(chi.URLParam(r, "chain"))
	block := chi.URLParam(r, "block")
	if err := s.validateChain(chain); err != nil {
		validationError(w, r, "不支持的链", nil)
		return
	}
	if _, err := strconv.ParseUint(block, 10, 64); err != nil && !hexHashRe.MatchString(strings.TrimPrefix(block, "0x")) {
		validationError(w, r, "区块高度或哈希不合法", nil)
		return
	}
	limit, err := parseOptionalLimit(r, 50, 200)
	if err != nil {
		validationError(w, r, "limit 不合法", nil)
		return
	}
	if s.deps.CH == nil {
		respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "本地索引暂不可用", nil)
		return
	}
	ctx, cancel := explorerReadContext(r.Context())
	defer cancel()
	items, err := query.New(s.deps.CH).BlockTransactions(ctx, chain, block, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		if explorerScanRefusal(w, r, ctx, err, "区块查询") {
			return
		}
		respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "区块数据暂不可用", nil)
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = objectString(items[len(items)-1]["tx_hash"])
	}
	summary := map[string]any{"chain": chain, "local_index": true}
	if len(items) > 0 {
		summary["block_number"] = items[0]["block_number"]
		summary["block_hash"] = items[0]["block_hash"]
		summary["timestamp"] = items[0]["timestamp"]
	}
	source := "local_index"
	if len(items) == 0 && r.URL.Query().Get("lookup") == "auto" && s.deps.ChainRead != nil && chain != "tron" && chain != "solana" {
		rpcCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		var header *chainread.Block
		if height, e := strconv.ParseUint(block, 10, 64); e == nil {
			header, err = s.deps.ChainRead.Block(rpcCtx, chain, chainmodel.AtBlock(height), false, lookupRPCOptions(rpcCtx))
		} else {
			h, e := chainmodel.ParseHash("0x" + strings.TrimPrefix(block, "0x"))
			if e != nil {
				validationError(w, r, "区块哈希不合法", nil)
				return
			}
			header, err = s.deps.ChainRead.BlockByHash(rpcCtx, chain, h, lookupRPCOptions(rpcCtx))
		}
		if err != nil {
			respondError(w, r, 503, "PROVIDER_UNAVAILABLE", "区块 RPC 暂不可用", nil)
			return
		}
		if header == nil {
			respondError(w, r, 404, "RESOURCE_NOT_FOUND", "未找到该区块", nil)
			return
		}
		summary = map[string]any{"chain": chain, "local_index": false, "block_number": header.Number, "block_hash": header.Hash, "timestamp": time.Unix(int64(header.Timestamp), 0).UTC(), "transaction_count": len(header.Transactions)}
		source = "rpc"
		items = []map[string]any{}
		// The RPC block returns hashes, not invented transaction details.
		start := 0
		if cursor := r.URL.Query().Get("cursor"); cursor != "" {
			for i, h := range header.Transactions {
				if h == cursor {
					start = i + 1
					break
				}
			}
		}
		for i := start; i < len(header.Transactions) && i < start+limit; i++ {
			items = append(items, map[string]any{"tx_hash": header.Transactions[i], "block_number": header.Number, "timestamp": summary["timestamp"]})
		}
		if start+len(items) < len(header.Transactions) && len(items) > 0 {
			next = objectString(items[len(items)-1]["tx_hash"])
		}
	}
	respondList(w, r, map[string]any{"summary": summary, "items": items, "source": source}, -1, &next)
}

func (s *Server) handleExplorerTokenSearch(w http.ResponseWriter, r *http.Request) {
	chain := strings.ToLower(r.URL.Query().Get("chain"))
	if err := s.validateChain(chain); err != nil {
		validationError(w, r, "不支持的链", nil)
		return
	}
	limit, err := parseOptionalLimit(r, 20, 50)
	if err != nil {
		validationError(w, r, "limit 不合法", nil)
		return
	}
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(term) > 160 {
		validationError(w, r, "搜索内容过长", nil)
		return
	}
	if s.deps.TokenMeta == nil {
		respondOK(w, r, map[string]any{"items": []any{}})
		return
	}
	items, err := s.deps.TokenMeta.Search(r.Context(), chain, term, limit)
	if err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "代币元数据暂不可用", nil)
		return
	}
	respondOK(w, r, map[string]any{"items": items})
}

func (s *Server) handleExplorerToken(w http.ResponseWriter, r *http.Request) {
	chain := strings.ToLower(chi.URLParam(r, "chain"))
	contract := canonicalExplorerAddress(chain, chi.URLParam(r, "contract"))
	if err := s.validateChain(chain); err != nil {
		validationError(w, r, "不支持的链", nil)
		return
	}
	if !validExplorerContract(chain, contract) {
		validationError(w, r, "代币合约不合法", nil)
		return
	}
	var metadata any
	if s.deps.TokenMeta != nil {
		item, err := s.deps.TokenMeta.Get(r.Context(), chain, contract)
		if err != nil {
			respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "代币元数据暂不可用", nil)
			return
		}
		metadata = item
	}
	respondOK(w, r, map[string]any{"chain": chain, "contract_address": contract, "metadata": metadata, "source": "token_metadata"})
}

func (s *Server) handleExplorerTokenTransfers(w http.ResponseWriter, r *http.Request) {
	chain := strings.ToLower(chi.URLParam(r, "chain"))
	contract := canonicalExplorerAddress(chain, chi.URLParam(r, "contract"))
	limit, err := parseOptionalLimit(r, 50, 200)
	if err != nil || s.validateChain(chain) != nil {
		validationError(w, r, "查询参数不合法", nil)
		return
	}
	if !validExplorerContract(chain, contract) {
		validationError(w, r, "代币合约不合法", nil)
		return
	}
	if s.deps.CH == nil {
		respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "本地索引暂不可用", nil)
		return
	}
	ctx, cancel := explorerReadContext(r.Context())
	defer cancel()
	var cursor struct {
		Hash  string    `json:"hash"`
		Index uint32    `json:"index"`
		Time  time.Time `json:"time"`
	}
	if err := decodeCursor(r.URL.Query().Get("cursor"), &cursor); err != nil {
		validationError(w, r, "cursor 不合法", nil)
		return
	}
	if cursor.Hash != "" && cursor.Time.IsZero() {
		validationError(w, r, "cursor 不合法", nil)
		return
	}
	items, err := query.New(s.deps.CH).TokenTransfers(ctx, chain, contract, cursor.Hash, cursor.Index, cursor.Time, limit)
	if err != nil {
		if explorerScanRefusal(w, r, ctx, err, "代币转账查询") {
			return
		}
		respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "代币转账暂不可用", nil)
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		cursor.Hash = objectString(last["tx_hash"])
		cursor.Time, err = explorerObjectTime(last["timestamp"])
		if err != nil {
			respondError(w, r, 503, "DATA_UNAVAILABLE", "转账游标时间缺失", nil)
			return
		}
		switch index := last["log_index"].(type) {
		case uint32:
			cursor.Index = index
		case uint64:
			cursor.Index = uint32(index)
		case float64:
			cursor.Index = uint32(index)
		}
		next = encodeCursor(cursor)
	}
	respondList(w, r, map[string]any{"items": items, "source": "local_index"}, -1, &next)
}

func (s *Server) handleExplorerTokenHolders(w http.ResponseWriter, r *http.Request) {
	chain := strings.ToLower(chi.URLParam(r, "chain"))
	contract := canonicalExplorerAddress(chain, chi.URLParam(r, "contract"))
	if err := s.validateChain(chain); err != nil {
		validationError(w, r, "不支持的链", nil)
		return
	}
	if !validExplorerContract(chain, contract) {
		validationError(w, r, "代币合约不合法", nil)
		return
	}
	if s.deps.CH == nil {
		respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "本地索引暂不可用", nil)
		return
	}
	ctx, cancel := explorerReadContext(r.Context())
	defer cancel()
	items, err := query.New(s.deps.CH).LocalTokenHolders(ctx, chain, contract, 200)
	if err != nil {
		if explorerScanRefusal(w, r, ctx, err, "代币持仓查询") {
			return
		}
		respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "本地持仓暂不可用", nil)
		return
	}
	respondOK(w, r, map[string]any{"items": items, "source": "local_balance_snapshots", "limit": 200})
}

func validExplorerContract(chain, value string) bool {
	items, err := dataset.NormalizeAndDedupeAddressesForChain(chain, []string{value})
	return err == nil && len(items) == 1
}

func explorerObjectTime(value any) (time.Time, error) {
	if t, ok := value.(time.Time); ok {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, objectString(value))
}
