package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	soladdr "github.com/blockchain-platform/blockchain-platform/internal/solana"
)

var lookupDigits = regexp.MustCompile(`^[0-9]+$`)

func classifyExplorerInput(slug, raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len([]rune(raw)) > 160 {
		return "", "", fmt.Errorf("请输入有效的地址、哈希、区块高度或代币名称")
	}
	if slug == "solana" {
		if decoded, err := soladdr.DecodeBase58(raw); err == nil {
			if len(decoded) == 32 {
				return "address", raw, nil
			}
			if len(decoded) == 64 {
				return "transaction", raw, nil
			}
		}
	}
	if lookupDigits.MatchString(raw) {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return "", "", fmt.Errorf("区块高度超出范围")
		}
		return "block", strconv.FormatUint(n, 10), nil
	}
	if slug == "solana" {
		if decoded, err := soladdr.DecodeBase58(raw); err == nil && len(decoded) == 64 {
			return "transaction", raw, nil
		}
	}
	if hexHashRe.MatchString(strings.TrimPrefix(strings.ToLower(raw), "0x")) && slug != "solana" {
		hash := strings.ToLower(strings.TrimPrefix(strings.ToLower(raw), "0x"))
		if slug != "tron" {
			hash = "0x" + hash
		}
		return "transaction", hash, nil
	}
	if normalized, err := addressnorm.Normalize(slug, raw); err == nil {
		return "address", normalized, nil
	}
	if strings.HasPrefix(strings.ToLower(raw), "0x") || (strings.HasPrefix(raw, "T") && len(raw) >= 30) || len(raw) == 64 || (slug == "solana" && len(raw) > 40) {
		return "", "", fmt.Errorf("地址或哈希格式不合法，请检查所选链及输入内容")
	}
	for _, r := range raw {
		if r < ' ' || r == '\u007f' {
			return "", "", fmt.Errorf("查询内容包含无效字符")
		}
	}
	return "token", raw, nil
}

func (s *Server) historyDatasets(slug string) []string {
	out := []string{}
	for _, c := range model.ChainDatasetCapabilities(slug) {
		if c.Status == model.DatasetAvailable && c.SupportsFull && c.ID != "logs" && (s.svc == nil || s.svc.SupportsFullHistory(slug, c.ID)) {
			out = append(out, c.ID)
		}
	}
	return out
}

func lookupRPCOptions(ctx context.Context) chainread.Options {
	p := PrincipalFrom(ctx)
	return chainread.Options{TransactionTTL: 15 * time.Second, Purpose: rpc.PurposeInvestigation, Priority: rpc.PriorityInteractive, Budget: rpc.BudgetContext{OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID, Purpose: rpc.PurposeInvestigation}}
}

// resolve never creates download work. Creation remains behind download:create,
// the established scoped preview/create APIs, idempotency and audit path.
func (s *Server) handleExplorerResolve(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chain")))
	if s.validateChain(slug) != nil {
		validationError(w, r, "不支持的链", nil)
		return
	}
	kind, value, err := classifyExplorerInput(slug, r.URL.Query().Get("value"))
	if err != nil {
		validationError(w, r, err.Error(), nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	result := map[string]any{"chain": slug, "kind": kind, "value": value, "status": "FOUND", "source": "local_index"}
	if cfg, ok := chain.DefaultRegistry().Get(slug); ok {
		result["chain_name"] = cfg.Name
	}
	if kind == "token" {
		respondOK(w, r, result)
		return
	}
	if kind == "address" {
		// A registered token contract has a distinct detail route. Resolve it
		// before wallet coverage so removing the type picker keeps that route usable.
		if s.deps.TokenMeta != nil {
			meta, e := s.deps.TokenMeta.Get(ctx, slug, value)
			if e != nil {
				respondError(w, r, 503, "DATA_UNAVAILABLE", "代币元数据暂不可用", nil)
				return
			}
			if meta != nil && (meta.Symbol != "" || meta.Name != "" || meta.TokenType != "") {
				result["kind"] = "token"
				result["source"] = "token_metadata"
				respondOK(w, r, result)
				return
			}
		}
		datasets := s.historyDatasets(slug)
		result["datasets"] = datasets
		if s.deps.Store == nil {
			respondError(w, r, 503, "DATA_UNAVAILABLE", "本地覆盖记录暂不可用", nil)
			return
		}
		coverage, local, err := s.deps.Store.ExplorerHistory(ctx, slug, value, datasets, time.Now().UTC())
		if err != nil {
			respondError(w, r, 503, "DATA_UNAVAILABLE", "本地覆盖记录暂不可用", nil)
			return
		}
		result["coverage"] = coverage
		if !local && s.deps.CH != nil {
			var n uint8
			err = s.deps.CH.DB().QueryRow(ctx, `SELECT toUInt8(1) FROM blockchain.address_activity WHERE chain=? AND address=? LIMIT 1`, slug, value).Scan(&n)
			// No row is an ordinary miss; other failures must not trigger paid work.
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				respondError(w, r, 503, "DATA_UNAVAILABLE", "本地地址索引暂不可用", nil)
				return
			}
			local = n == 1
		}
		usable := len(coverage) > 0
		for _, item := range coverage {
			usable = usable && item.Usable
		}
		result["local_data"] = local
		result["needs_completion"] = !usable
		if local {
			respondOK(w, r, result)
			return
		}
		found := false
		if cached, ok := s.lookupProbes.get(slug + "/" + value); ok {
			result["source"] = "chain_probe_cache"
			if !cached {
				result["status"] = "UNCONFIRMED"
				result["needs_completion"] = false
			}
			respondOK(w, r, result)
			return
		}
		if slug != "tron" && slug != "solana" && s.deps.ChainRead != nil {
			address, _ := chain.ParseAddress(value)
			found, err = s.deps.ChainRead.AddressHasState(ctx, slug, address, lookupRPCOptions(ctx))
			if err != nil {
				respondLookupFailure(w, r, err, "RPC 探测")
				return
			}
		}
		// Token-only recipients can have zero native balance/nonce/code. Fetch
		// at most one history page per supported dataset; never scan chain blocks.
		if !found && s.deps.Prov != nil {
			for _, ds := range []string{"transactions", "token_transfers"} {
				page, e := s.deps.Prov.Fetch(ctx, provider.FetchRequest{Chain: slug, Dataset: ds, Address: value, QueryMode: "full", PageSize: 1, Priority: "interactive"})
				if e != nil {
					respondLookupFailure(w, r, e, "链上历史探测")
					return
				}
				if len(page.Rows) > 0 {
					found = true
					break
				}
			}
		}
		result["source"] = "chain_probe"
		if s.deps.Prov != nil || s.deps.ChainRead != nil {
			s.lookupProbes.put(slug+"/"+value, found)
		}
		if !found {
			result["status"] = "UNCONFIRMED"
			result["needs_completion"] = false
		}
		respondOK(w, r, result)
		return
	}
	if kind == "transaction" {
		if s.detail != nil {
			row, e := s.detail.LocalTransaction(ctx, slug, value)
			if e != nil {
				respondError(w, r, 503, "DATA_UNAVAILABLE", "本地交易索引暂不可用", nil)
				return
			}
			if row != nil {
				respondOK(w, r, result)
				return
			}
			if _, found := s.explorerPartialTransactionDetail(ctx, slug, value); found {
				respondOK(w, r, result)
				return
			}
		}
		if slug == "tron" || slug == "solana" || s.deps.ChainRead == nil {
			result["status"] = "UNCONFIRMED"
			respondOK(w, r, result)
			return
		}
		hash, _ := chain.ParseHash(value)
		tx, e := s.deps.ChainRead.Transaction(ctx, slug, hash, lookupRPCOptions(ctx))
		if e != nil {
			respondLookupFailure(w, r, e, "交易 RPC")
			return
		}
		result["source"] = "rpc"
		if tx != nil {
			if tx.BlockNumber == nil {
				result["status"] = "PENDING"
			}
			respondOK(w, r, result)
			return
		}
		block, e := s.deps.ChainRead.BlockByHash(ctx, slug, hash, lookupRPCOptions(ctx))
		if e != nil {
			respondLookupFailure(w, r, e, "区块 RPC")
			return
		}
		if block != nil {
			result["kind"] = "block"
		} else {
			result["status"] = "NOT_FOUND"
		}
		respondOK(w, r, result)
		return
	}
	if s.deps.CH != nil {
		rows, e := query.New(s.deps.CH).BlockTransactions(ctx, slug, value, "", 1)
		if e != nil {
			respondError(w, r, 503, "DATA_UNAVAILABLE", "本地区块索引暂不可用", nil)
			return
		}
		if len(rows) > 0 {
			respondOK(w, r, result)
			return
		}
	}
	if slug == "tron" || slug == "solana" || s.deps.ChainRead == nil {
		result["status"] = "UNCONFIRMED"
		respondOK(w, r, result)
		return
	}
	n, _ := strconv.ParseUint(value, 10, 64)
	block, e := s.deps.ChainRead.Block(ctx, slug, chain.AtBlock(n), false, lookupRPCOptions(ctx))
	if e != nil {
		respondLookupFailure(w, r, e, "区块 RPC")
		return
	}
	result["source"] = "rpc"
	if block == nil {
		result["status"] = "NOT_FOUND"
	}
	respondOK(w, r, result)
}

// Coverage is also available to an already open address page after a task ends.
func (s *Server) handleExplorerHistory(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveExplorerSubject(w, r)
	if !ok {
		return
	}
	if s.deps.Store == nil {
		respondError(w, r, 503, "DATA_UNAVAILABLE", "覆盖记录暂不可用", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	items, _, err := s.deps.Store.ExplorerHistory(ctx, sub.Chain, sub.Normalized, s.historyDatasets(sub.Chain), time.Now().UTC())
	if err != nil {
		respondError(w, r, 503, "DATA_UNAVAILABLE", "覆盖记录暂不可用", nil)
		return
	}
	respondOK(w, r, map[string]any{"items": items, "source": "request_ranges_and_ingested_pages"})
}
