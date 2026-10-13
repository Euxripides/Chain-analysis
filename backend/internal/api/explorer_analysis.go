package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	ta "github.com/blockchain-platform/blockchain-platform/internal/transactionanalysis"
	"golang.org/x/sync/singleflight"
)

func (s *Server) transactionAnalysis(ctx context.Context, slug, hash string, receipt bool) (ta.Result, error) {
	if s.detail == nil || s.query == nil {
		return ta.Result{}, errors.New("analysis service unavailable")
	}
	detail, err := s.detail.Detail(ctx, slug, hash, method.DecodeLocalOnly)
	if errors.Is(err, method.ErrTransactionNotFound) {
		detail, _ = s.explorerPartialTransactionDetail(ctx, slug, hash)
		if detail == nil {
			detail, err = s.explorerRPCDetail(ctx, slug, hash, false)
		} else {
			err = nil
		}
	}
	if err != nil {
		return ta.Result{}, err
	}
	row := map[string]any{}
	for k, v := range detail.Transaction {
		row[k] = v
	}
	if receipt && len(row) == 0 {
		basic, e := s.explorerRPCDetail(ctx, slug, hash, false)
		if e == nil && basic != nil {
			for k, v := range basic.Transaction {
				row[k] = v
			}
			detail.Method = basic.Method
		}
	}
	in := ta.Input{Chain: slug, Hash: hash, Transaction: row, Logs: detail.Logs, Metadata: map[string]ta.Asset{}, Issues: []string{}}
	d := s.nativeDecimals(slug)
	symbol := nativeSymbol(slug)
	if s.deps.ChainRead != nil {
		if cfg, ok := s.deps.ChainRead.Chains().Get(slug); ok {
			d = cfg.Native.Decimals
			symbol = cfg.Native.Symbol
		}
	}
	in.Native = ta.Asset{Kind: "native", Symbol: symbol, Decimals: &d, MetadataSource: "chain_registry"}
	in.MethodName = detail.Method.Name
	in.MethodVerified = detail.Method.Verified
	// Bounded transaction-only datasets. No address download or full-history query.
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, dataset := range []string{"token_transfers", "nft_transfers", "internal_transactions"} {
		wg.Add(1)
		go func(ds string) {
			defer wg.Done()
			rows, e := s.query.TransactionAnalysisRows(ctx, ds, slug, hash)
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				in.Issues = append(in.Issues, ds+" 读取失败")
				return
			}
			if len(rows) > 2000 {
				rows = rows[:2000]
				in.Issues = append(in.Issues, ds+" 超过 2000 条展示上限")
			}
			switch ds {
			case "token_transfers":
				in.Tokens = rows
			case "nft_transfers":
				in.NFTs = rows
			case "internal_transactions":
				in.Internals = rows
			}
		}(dataset)
	}
	if s.deps.Trace != nil && slug != "solana" && slug != "tron" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, e := chain.ParseHash(hash)
			if e != nil {
				return
			}
			tr, e := s.deps.Trace.TransactionTrace(ctx, slug, h, chaintrace.Options{Consistency: "local_only"})
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				in.Trace = tr
			} else if !errors.Is(e, chaintrace.ErrTraceNotAvailableLocally) {
				in.Issues = append(in.Issues, "本地 Trace 读取失败")
			}
		}()
	}
	wg.Wait()
	if ta.Text(row["method_id"]) == "" {
		row["method_id"] = detail.Method.ID
	}
	// Index completeness cannot be inferred from token rows. Read one standard
	// receipt (cached by ChainRead/RPCGateway), but never auto-fetch a trace.
	if receipt && s.deps.ChainRead != nil && slug != "solana" && slug != "tron" {
		rpcCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		h, _ := chain.ParseHash(hash)
		rec, e := s.deps.ChainRead.Receipt(rpcCtx, slug, h, lookupRPCOptions(ctx))
		if e != nil {
			in.Issues = append(in.Issues, "回执暂不可用")
		} else if rec != nil {
			in.ReceiptPresent = true
			row["status"] = rec.Status
			row["gas_used"] = rec.GasUsed
			row["effective_gas_price"] = rec.EffectiveGasPrice
			row["block_number"] = rec.BlockNumber
			if row["from_address"] == nil || row["from_address"] == "" {
				row["from_address"] = string(rec.From)
			}
			if rec.To != nil && (row["to_address"] == nil || row["to_address"] == "") {
				row["to_address"] = string(*rec.To)
			}
			if rec.ContractAddress != nil {
				row["created_contract"] = string(*rec.ContractAddress)
			}
			if row["timestamp"] == nil && rec.BlockNumber != nil {
				block, e := s.deps.ChainRead.Block(rpcCtx, slug, chain.AtBlock(*rec.BlockNumber), false, lookupRPCOptions(ctx))
				if e == nil && block != nil {
					row["timestamp"] = time.Unix(int64(block.Timestamp), 0).UTC()
				}
			}
			in.Logs = []map[string]any{}
			for _, l := range rec.Logs {
				in.Logs = append(in.Logs, map[string]any{"address": string(l.Address), "topics": l.Topics, "data": l.Data, "log_index": l.LogIndex, "removed": l.Removed, "source_provider": "rpc_receipt", "block_number": l.BlockNumber})
			}
		}
	}
	contracts := []string{}
	seen := map[string]bool{}
	for _, rows := range [][]map[string]any{in.Tokens, in.NFTs, in.Logs} {
		for _, r := range rows {
			c := ta.Text(r["token_address"])
			if c == "" {
				c = ta.Text(r["contract_address"])
			}
			if c == "" {
				c = ta.Text(r["address"])
			}
			if c != "" && !seen[c] && len(contracts) < 200 {
				seen[c] = true
				contracts = append(contracts, c)
			}
		}
	}
	if s.deps.TokenMeta != nil && len(contracts) > 0 {
		metadata, e := s.deps.TokenMeta.GetMany(ctx, slug, contracts)
		if e != nil {
			in.Issues = append(in.Issues, "代币元数据读取失败")
		} else {
			for c, m := range metadata {
				in.Metadata[c] = ta.Asset{Kind: "token", Contract: c, Symbol: m.Symbol, Decimals: m.Decimals, MetadataSource: m.MetadataSource}
			}
		}
	}
	return ta.Build(in), nil
}

func (s *Server) handleExplorerTransactionAnalysis(w http.ResponseWriter, r *http.Request) {
	slug, hash, ok := s.resolveExplorerTx(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result, e := s.transactionAnalysis(ctx, slug, hash, true)
	if e != nil {
		status := http.StatusBadGateway
		code := "DATA_UNAVAILABLE"
		if errors.Is(e, method.ErrTransactionNotFound) {
			status = http.StatusNotFound
			code = "RESOURCE_NOT_FOUND"
		}
		respondError(w, r, status, code, "交易资金数据暂不可用", nil)
		return
	}
	// Workspace labels always use the authenticated workspace under the request's
	// RLS transaction; no Actions/system scope and no shared private-label cache.
	p := PrincipalFrom(ctx)
	addresses := []string{}
	indices := map[string]int{}
	for i := range result.Participants {
		a := &result.Participants[i]
		indices[a.Address] = i
		if len(addresses) < 500 {
			addresses = append(addresses, a.Address)
		}
		for _, l := range exchangeLabels(slug, a.Address) {
			name := ta.Text(l["label"])
			if name == "" {
				name = ta.Text(l["entity"])
			}
			if name != "" {
				a.Labels = append(a.Labels, ta.Label{Name: name, Source: "public_registry"})
			}
		}
	}
	if s.deps.Store != nil && p.WorkspaceID != "" && p.OrganizationID != "" && len(addresses) > 0 {
		rows, e := s.deps.Store.Pool.Query(ctx, `SELECT normalized_address, display_name FROM workspace_addresses WHERE organization_id=$1 AND workspace_id=$2 AND chain=$3 AND normalized_address=ANY($4) AND display_name IS NOT NULL LIMIT 500`, p.OrganizationID, p.WorkspaceID, slug, addresses)
		if e != nil {
			result.Issues = append(result.Issues, "工作区地址标签读取失败")
		} else {
			defer rows.Close()
			for rows.Next() {
				var a, name string
				if e = rows.Scan(&a, &name); e != nil {
					break
				}
				if i, ok := indices[a]; ok && name != "" {
					result.Participants[i].Labels = append(result.Participants[i].Labels, ta.Label{Name: name, Source: "workspace:" + p.WorkspaceID})
				}
			}
			if e != nil || rows.Err() != nil {
				result.Issues = append(result.Issues, "工作区地址标签读取失败")
			}
		}
	}
	respondOK(w, r, map[string]any{"items": result})
}

type analysisBridgeEntry struct {
	Result crosschain.QueryResult
	Until  time.Time
}

var analysisBridges sync.Map
var analysisBridgeFlights singleflight.Group

func (s *Server) handleExplorerTransactionBridge(w http.ResponseWriter, r *http.Request) {
	slug, hash, ok := s.resolveExplorerTx(w, r)
	if !ok {
		return
	}
	bridge := r.URL.Query().Get("bridge")
	if bridge == "" {
		bridge = "auto"
	}
	bridgeChain := slug
	if slug == "eth" {
		bridgeChain = "ethereum"
	}
	req := crosschain.QueryRequest{Bridge: bridge, Chain: bridgeChain, TxHash: hash, MaxResults: 5}
	if e := req.Validate(); e != nil {
		validationError(w, r, "跨链查询参数不合法", nil)
		return
	}
	if s.crossChain == nil {
		respondError(w, r, 503, "SERVICE_UNAVAILABLE", "跨链服务未启用", nil)
		return
	}
	key := bridge + ":" + slug + ":" + hash
	now := time.Now()
	cached := false
	var result crosschain.QueryResult
	if v, ok := analysisBridges.Load(key); ok && v.(analysisBridgeEntry).Until.After(now) {
		result = v.(analysisBridgeEntry).Result
		cached = true
	} else {
		value, e, _ := analysisBridgeFlights.Do(key, func() (any, error) {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			out, e := s.crossChain.Query(ctx, req)
			if e != nil {
				return nil, e
			}
			count := 0
			analysisBridges.Range(func(k, v any) bool {
				count++
				if !v.(analysisBridgeEntry).Until.After(now) {
					analysisBridges.Delete(k)
				}
				return true
			})
			if count < 256 {
				analysisBridges.Store(key, analysisBridgeEntry{out, now.Add(5 * time.Minute)})
			}
			return out, nil
		})
		if e != nil {
			respondError(w, r, 502, "PROVIDER_UNAVAILABLE", "跨链关联暂不可用", nil)
			return
		}
		result = value.(crosschain.QueryResult)
	}
	// Verification is performed per request against current local evidence, never
	// cached with private workspace data. Copy slices before adding verification.
	result.Transfers = append([]crosschain.Transfer{}, result.Transfers...)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	for i := range result.Transfers {
		result.Transfers[i].Verification = nil
		s.verifyBridgeTransfer(ctx, &result.Transfers[i])
	}
	respondOK(w, r, map[string]any{"items": result, "cached": cached, "protocols": s.crossChain.Capabilities()})
}

func (s *Server) handleExplorerTransactionContext(w http.ResponseWriter, r *http.Request) {
	slug, hash, ok := s.resolveExplorerTx(w, r)
	if !ok {
		return
	}
	addr, e := addressnorm.Normalize(slug, r.URL.Query().Get("address"))
	if e != nil {
		validationError(w, r, "地址不合法", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	result, e := s.transactionAnalysis(ctx, slug, hash, true)
	if e != nil {
		respondError(w, r, 502, "DATA_UNAVAILABLE", "本地关联数据暂不可用", nil)
		return
	}
	matched := false
	for _, p := range result.Participants {
		if p.Address == addr {
			matched = true
		}
	}
	if !matched {
		validationError(w, r, "地址不是这笔交易的参与方", nil)
		return
	}
	var ts time.Time
	switch v := result.Transaction["timestamp"].(type) {
	case time.Time:
		ts = v
	case string:
		ts, _ = time.Parse(time.RFC3339, v)
	}
	if ts.IsZero() || s.deps.CH == nil {
		respondOK(w, r, map[string]any{"items": []any{}, "first_funding": nil, "scope": "local_index", "time_window_hours": 48})
		return
	}
	rows, e := s.deps.CH.DB().Query(ctx, `SELECT DISTINCT tx_hash, timestamp, counterparty, direction, amount_raw, token_address, token_symbol, token_decimals, dataset, log_index FROM blockchain.address_activity WHERE chain=? AND address=? AND timestamp>=? AND timestamp<=? AND tx_hash!=? AND dataset IN ('transactions','internal_transactions','token_transfers') ORDER BY timestamp DESC LIMIT 20`, slug, addr, ts.Add(-24*time.Hour), ts.Add(24*time.Hour), hash)
	if e != nil {
		respondError(w, r, 502, "DATA_UNAVAILABLE", "本地相邻交易读取失败", nil)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var h, cp, direction, raw, contract, symbol, ds string
		var t time.Time
		var decimals uint16
		var index uint32
		if e = rows.Scan(&h, &t, &cp, &direction, &raw, &contract, &symbol, &decimals, &ds, &index); e != nil {
			respondError(w, r, 502, "DATA_UNAVAILABLE", "本地相邻交易读取失败", nil)
			return
		}
		from, to := addr, cp
		if direction == "in" {
			from, to = cp, addr
		}
		asset := result.Native
		if ds == "token_transfers" && contract != "" {
			d := int(decimals)
			asset = ta.Asset{Kind: "token", Contract: contract, Symbol: symbol}
			if symbol != "" && d <= 77 {
				asset.Decimals = &d
			}
		}
		items = append(items, map[string]any{"hash": h, "timestamp": t, "from": from, "to": to, "amount_raw": raw, "asset": asset, "dataset": ds, "event_index": index, "seconds_from_anchor": int64(t.Sub(ts).Seconds())})
	}
	if e = rows.Err(); e != nil {
		respondError(w, r, 502, "DATA_UNAVAILABLE", "本地相邻交易读取失败", nil)
		return
	}
	rows.Close()
	first := map[string]any{"first_native_funding": nil}
	candidates, e := s.deps.CH.DB().Query(ctx, `SELECT DISTINCT tx_hash, timestamp FROM blockchain.address_activity WHERE chain=? AND address=? AND direction='in' AND token_address='' AND dataset IN ('transactions','internal_transactions') AND amount>0 ORDER BY timestamp ASC LIMIT 5`, slug, addr)
	if e != nil {
		respondError(w, r, 502, "DATA_UNAVAILABLE", "最早可见入金索引读取失败", nil)
		return
	}
	hashes := []string{}
	for candidates.Next() {
		var h string
		var t time.Time
		if e = candidates.Scan(&h, &t); e != nil {
			break
		}
		hashes = append(hashes, h)
	}
	candidateErr := candidates.Err()
	candidates.Close()
	if e != nil || candidateErr != nil {
		respondError(w, r, 502, "DATA_UNAVAILABLE", "最早可见入金索引读取失败", nil)
		return
	}
	// At most five indexed candidates, each verified against transaction-local
	// successful movement evidence. This never scans the whole chain history.
	for _, h := range hashes {
		record, e := s.query.ActionTransactionRecord(ctx, slug, h)
		if e != nil || record == nil {
			continue
		}
		observed, e := s.transactionAnalysis(ctx, slug, h, false)
		if e != nil || observed.Outcome != "success" {
			continue
		}
		for _, m := range observed.Movements {
			if m.Asset.Kind == "native" && m.To == addr && m.From != addr {
				first["first_native_funding"] = map[string]any{"from": m.From, "amount": m.Amount, "tx_hash": h, "timestamp": observed.Transaction["timestamp"]}
				break
			}
		}
		if first["first_native_funding"] != nil {
			break
		}
	}
	respondOK(w, r, map[string]any{"items": items, "first_funding": first, "scope": "local_address_activity", "time_window_hours": 48, "limit": 20, "first_funding_candidate_limit": 5})
}
