package api

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/explorer"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

// ---- U-EXP-01/02/03 + U-SEARCH-01（§18.9 / §21.5）----

var (
	evmAddressRe = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	evmTxHashRe  = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	hexHashRe    = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

type explorerSubject struct {
	Chain      string `json:"chain"`
	Address    string `json:"address"`
	Normalized string `json:"normalized_address"`
}

func (s *Server) resolveExplorerSubject(w http.ResponseWriter, r *http.Request) (explorerSubject, bool) {
	chainSlug := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "chain")))
	raw := strings.TrimSpace(chi.URLParam(r, "address"))
	if err := s.validateChain(chainSlug); err != nil {
		validationError(w, r, "不支持的链", []fieldError{{Field: "chain", Code: "invalid", Message: err.Error()}})
		return explorerSubject{}, false
	}
	var normalized string
	if chainSlug == "tron" {
		n, err := tron.NormalizeTronAddress(raw)
		if err != nil {
			validationError(w, r, "TRON 地址不合法", []fieldError{{Field: "address", Code: "invalid", Message: "请检查 Base58 校验和"}})
			return explorerSubject{}, false
		}
		normalized = n
	} else {
		normalized = strings.ToLower(raw)
		if !evmAddressRe.MatchString(normalized) {
			validationError(w, r, "EVM 地址不合法", []fieldError{{Field: "address", Code: "invalid", Message: "需要 0x + 40 位十六进制"}})
			return explorerSubject{}, false
		}
	}
	return explorerSubject{Chain: chainSlug, Address: raw, Normalized: normalized}, true
}

// explorerEnvelope 统一 Explorer 数据来源与口径（§18.9）。
func explorerEnvelope(subject map[string]any, items any, summary map[string]any, coverage map[string]any, caliber map[string]any, tables ...string) map[string]any {
	sources := make([]string, 0, len(tables))
	for _, t := range tables {
		if strings.TrimSpace(t) != "" {
			sources = append(sources, t)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return map[string]any{
		"subject":  subject,
		"items":    items,
		"summary":  summary,
		"coverage": coverage,
		"caliber":  caliber,
		"source": map[string]any{
			"provider":     "local_index",
			"tables":       sources,
			"data_version": "as_of:" + now,
			"as_of":        now,
		},
	}
}

func (s *Server) handleExplorerOverview(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveExplorerSubject(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var txCount, transferCount int64
	var txFirst, txLast, tkFirst, tkLast time.Time
	var counterparties uint64
	// 已同步行数先取 PostgreSQL coverage 的 row_count（索引查询，不扫 ClickHouse）。
	if s.deps.Store != nil {
		_ = s.deps.Store.Pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(row_count) FILTER (WHERE dataset='transactions'),0),
			       COALESCE(SUM(row_count) FILTER (WHERE dataset='token_transfers'),0)
			FROM dataset_coverage WHERE chain=$1 AND subject=$2`, sub.Chain, sub.Normalized).Scan(&txCount, &transferCount)
	}
	if s.deps.CH != nil {
		// address_activity 主键前缀 (chain,address) 上一跳聚合，热路径 <1s。
		var activityCount, activityTx, activityTransfer int64
		err := s.deps.CH.DB().QueryRow(ctx, `
			SELECT toInt64(count()), min(timestamp), max(timestamp), toUInt64(uniqExact(counterparty)),
			       toInt64(countIf(dataset='transactions')), toInt64(countIf(dataset='token_transfers'))
			FROM blockchain.address_activity FINAL
			WHERE chain = ? AND address = ?`, sub.Chain, sub.Normalized).
			Scan(&activityCount, &tkFirst, &tkLast, &counterparties, &activityTx, &activityTransfer)
		if err == nil && activityCount > 0 {
			if txCount == 0 {
				txCount = activityTx
			}
			if transferCount == 0 {
				transferCount = activityTransfer
			}
		} else {
			// 旧数据/未写入 activity 的地址走回退查询；明确标注为 local_index 全表聚合。
			_ = s.deps.CH.DB().QueryRow(ctx, `
				SELECT count(), min(timestamp), max(timestamp),
				       uniqExact(if(from_address = ?, to_address, from_address))
				FROM blockchain.transactions FINAL
				WHERE chain = ? AND (from_address = ? OR to_address = ?)`,
				sub.Normalized, sub.Chain, sub.Normalized, sub.Normalized).Scan(&txCount, &txFirst, &txLast, &counterparties)
			_ = s.deps.CH.DB().QueryRow(ctx, `
				SELECT count(), min(timestamp), max(timestamp)
				FROM blockchain.token_transfers FINAL
				WHERE chain = ? AND (from_address = ? OR to_address = ?)`,
				sub.Chain, sub.Normalized, sub.Normalized).Scan(&transferCount, &tkFirst, &tkLast)
		}
	}
	balances, observedAt := s.localBalanceSnapshot(ctx, sub)
	netFlow, _ := s.localHistoricalNetFlow(ctx, sub)
	coverage := s.localExplorerCoverage(ctx, sub)
	subject := map[string]any{"type": "address", "chain": sub.Chain, "address": sub.Address, "normalized_address": sub.Normalized}
	summary := map[string]any{
		"transaction_count": txCount, "token_transfer_count": transferCount,
		"counterparty_count": counterparties,
		"first_seen":         earliestTime(txFirst, tkFirst), "last_seen": latestTime(txLast, tkLast),
		"current_balance": map[string]any{
			"source": "provider_snapshot", "is_current_balance": true, "observed_at": observedAt, "items": balances,
		},
		"historical_net_flow": map[string]any{
			"source": "local_index", "is_current_balance": false, "items": netFlow,
			"note": "由本地已入库交易聚合，不代表当前余额。",
		},
	}
	caliber := map[string]any{
		"balance":  "provider_snapshot 只表示最近一次入库的 Provider 快照；local_index 是本地历史净流量，两者不混用。",
		"amounts":  "raw_amount 永远为十进制字符串，不经 JSON number。",
		"coverage": "Coverage 来自 PostgreSQL dataset_coverage 与 address_dataset_sync_state，不猜测未同步区间。",
	}
	respondOK(w, r, explorerEnvelope(subject, nil, summary, coverage, caliber,
		"clickhouse:blockchain.transactions", "clickhouse:blockchain.token_transfers",
		"clickhouse:blockchain.address_balances", "postgres:dataset_coverage"))
}

func earliestTime(a, b time.Time) any {
	switch {
	case plausibleTime(a) && plausibleTime(b):
		if a.Before(b) {
			return a
		}
		return b
	case plausibleTime(a):
		return a
	case plausibleTime(b):
		return b
	default:
		return nil
	}
}

func latestTime(a, b time.Time) any {
	switch {
	case plausibleTime(a) && plausibleTime(b):
		if a.After(b) {
			return a
		}
		return b
	case plausibleTime(a):
		return a
	case plausibleTime(b):
		return b
	default:
		return nil
	}
}

func plausibleTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 2000 }

func (s *Server) localBalanceSnapshot(ctx context.Context, sub explorerSubject) ([]map[string]any, any) {
	out := []map[string]any{}
	if s.deps.CH == nil {
		return out, nil
	}
	rows, err := s.deps.CH.DB().Query(ctx, `
		SELECT token_symbol, token_name, token_address, token_type, token_decimals,
		       balance_raw, toString(balance), balance_usd, token_price, snapshot_block, timestamp, source_provider
		FROM blockchain.address_balances FINAL
		WHERE chain = ? AND address = ?
		ORDER BY timestamp DESC LIMIT 100`, sub.Chain, sub.Normalized)
	if err != nil {
		return out, nil
	}
	defer rows.Close()
	var observed *time.Time
	for rows.Next() {
		var symbol, name, addr, typ, raw, balance, usd, price, provider string
		var decimals uint16
		var block uint64
		var ts time.Time
		if err := rows.Scan(&symbol, &name, &addr, &typ, &decimals, &raw, &balance, &usd, &price, &block, &ts, &provider); err != nil {
			continue
		}
		if observed == nil || ts.After(*observed) {
			v := ts
			observed = &v
		}
		out = append(out, map[string]any{
			"token_symbol": symbol, "token_name": name, "token_address": addr, "token_type": typ,
			"decimals": decimals, "raw_amount": raw, "display_amount": balance, "amount_usd": usd,
			"token_price": price, "snapshot_block": block, "observed_at": ts,
			"source_provider": provider, "source": "provider_snapshot", "is_current_balance": true,
		})
	}
	return out, observed
}

func (s *Server) localHistoricalNetFlow(ctx context.Context, sub explorerSubject) ([]map[string]any, error) {
	if s.deps.CH == nil {
		return []map[string]any{}, nil
	}
	// 热路径：address_activity 以 (chain,address) 为主键前缀，避免对 8M+ 转账做全量聚合。
	rows, err := s.deps.CH.DB().Query(ctx, `
		SELECT token_address, any(token_symbol) AS symbol, any(token_decimals) AS decimals,
		       any(dataset) AS dataset,
		       toString(sumIf(amount, direction IN ('in','self'))) AS in_raw,
		       toString(sumIf(amount, direction IN ('out','self'))) AS out_raw,
		       toInt64(count()) AS cnt
		FROM blockchain.address_activity FINAL
		WHERE chain = ? AND address = ?
		GROUP BY token_address ORDER BY cnt DESC LIMIT 100`, sub.Chain, sub.Normalized)
	if err == nil {
		defer rows.Close()
		out := []map[string]any{}
		scanErrors := 0
		for rows.Next() {
			var addr, symbol, dataset, inAmt, outAmt string
			var decimals uint16
			var cnt int64
			if err := rows.Scan(&addr, &symbol, &decimals, &dataset, &inAmt, &outAmt, &cnt); err != nil {
				scanErrors++
				continue
			}
			asset := "token"
			if dataset == "transactions" || addr == "" {
				asset = "native"
				if symbol == "" {
					symbol = nativeSymbol(sub.Chain)
				}
				if decimals == 0 {
					decimals = uint16(nativeDecimals(sub.Chain))
				}
			}
			out = append(out, map[string]any{
				"asset": asset, "token_symbol": symbol, "token_address": addr, "decimals": decimals,
				"in_raw": inAmt, "out_raw": outAmt, "net_raw": subtractDecimalStrings(inAmt, outAmt),
				"transaction_count": cnt, "source": "local_index", "is_current_balance": false,
			})
		}
		if len(out) > 0 {
			return out, nil
		}
		s.deps.Log.Warn("netflow.activity.empty", "scan_errors", scanErrors)
	} else {
		s.deps.Log.Warn("netflow.activity.query_failed", "err", err)
	}
	return s.localHistoricalNetFlowHeavy(ctx, sub)
}

// localHistoricalNetFlowHeavy 是未写入 address_activity 的旧地址回退路径。
func (s *Server) localHistoricalNetFlowHeavy(ctx context.Context, sub explorerSubject) ([]map[string]any, error) {
	out := []map[string]any{}
	if s.deps.CH == nil {
		return out, nil
	}
	// 原生币：value_raw 是十进制字符串，用 Int256 聚合后转字符串，不能走 float。
	var inRaw, outRaw string
	var nativeCount int64
	err := s.deps.CH.DB().QueryRow(ctx, `
		SELECT toString(sumIf(toInt256OrZero(value_raw), to_address = ?)) AS in_raw,
		       toString(sumIf(toInt256OrZero(value_raw), from_address = ?)) AS out_raw,
		       toInt64(countIf(from_address = ? OR to_address = ?)) AS cnt
		FROM blockchain.transactions FINAL
		WHERE chain = ? AND (from_address = ? OR to_address = ?)`,
		sub.Normalized, sub.Normalized, sub.Normalized, sub.Normalized,
		sub.Chain, sub.Normalized, sub.Normalized).Scan(&inRaw, &outRaw, &nativeCount)
	if err == nil && nativeCount > 0 {
		out = append(out, map[string]any{
			"asset": "native", "token_symbol": nativeSymbol(sub.Chain), "token_address": "",
			"decimals": nativeDecimals(sub.Chain), "in_raw": inRaw, "out_raw": outRaw,
			"net_raw":           subtractDecimalStrings(inRaw, outRaw),
			"transaction_count": nativeCount, "source": "local_index", "is_current_balance": false,
		})
	}
	rows, qerr := s.deps.CH.DB().Query(ctx, `
		SELECT token_address, any(token_symbol) AS symbol, any(token_decimals) AS decimals,
		       toString(sumIf(amount, to_address = ?)) AS in_raw,
		       toString(sumIf(amount, from_address = ?)) AS out_raw,
		       toInt64(count()) AS cnt
		FROM blockchain.token_transfers FINAL
		WHERE chain = ? AND (from_address = ? OR to_address = ?)
		GROUP BY token_address ORDER BY cnt DESC LIMIT 100`,
		sub.Normalized, sub.Normalized, sub.Chain, sub.Normalized, sub.Normalized)
	if qerr != nil {
		return out, qerr
	}
	defer rows.Close()
	for rows.Next() {
		var addr, symbol, inAmt, outAmt string
		var decimals uint16
		var cnt int64
		if err := rows.Scan(&addr, &symbol, &decimals, &inAmt, &outAmt, &cnt); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"asset": "token", "token_symbol": symbol, "token_address": addr, "decimals": decimals,
			"in_raw": inAmt, "out_raw": outAmt, "net_raw": subtractDecimalStrings(inAmt, outAmt),
			"transaction_count": cnt, "source": "local_index", "is_current_balance": false,
		})
	}
	return out, nil
}

func nativeSymbol(chainSlug string) string {
	switch chainSlug {
	case "tron":
		return "TRX"
	case "bsc":
		return "BNB"
	case "ethereum":
		return "ETH"
	case "polygon":
		return "POL"
	default:
		return "NATIVE"
	}
}

func nativeDecimals(chainSlug string) int {
	if chainSlug == "tron" {
		return 6
	}
	return 18
}

func subtractDecimalStrings(a, b string) string {
	trim := func(v string) string {
		v = strings.TrimSpace(v)
		if v == "" {
			return "0"
		}
		return v
	}
	ai, ok1 := new(big.Int).SetString(trim(a), 10)
	bi, ok2 := new(big.Int).SetString(trim(b), 10)
	if !ok1 || !ok2 {
		return ""
	}
	return ai.Sub(ai, bi).String()
}

func (s *Server) localExplorerCoverage(ctx context.Context, sub explorerSubject) map[string]any {
	items := []map[string]any{}
	if s.deps.Store == nil {
		return map[string]any{"items": items, "source": "postgres:dataset_coverage"}
	}
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT dataset, min(from_block), max(to_block), min(from_timestamp), max(to_timestamp), max(updated_at)
		FROM dataset_coverage WHERE chain=$1 AND subject=$2
		GROUP BY dataset ORDER BY dataset`, sub.Chain, sub.Normalized)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ds string
			var fromBlock, toBlock *int64
			var fromTS, toTS, updated *time.Time
			if err := rows.Scan(&ds, &fromBlock, &toBlock, &fromTS, &toTS, &updated); err != nil {
				continue
			}
			items = append(items, map[string]any{"dataset": ds, "from_block": fromBlock, "to_block": toBlock,
				"from_timestamp": fromTS, "to_timestamp": toTS, "updated_at": updated,
				"status": "local_index", "is_current_balance": false})
		}
	}
	syncRows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT dataset, complete_through FROM address_dataset_sync_state
		WHERE chain=$1 AND subject=$2 ORDER BY dataset`, sub.Chain, sub.Normalized)
	if err == nil {
		defer syncRows.Close()
		for syncRows.Next() {
			var ds string
			var complete *time.Time
			if err := syncRows.Scan(&ds, &complete); err != nil {
				continue
			}
			items = append(items, map[string]any{"dataset": ds, "complete_through": complete,
				"status": "synced", "source": "address_dataset_sync_state"})
		}
	}
	return map[string]any{"items": items, "source": "postgres:dataset_coverage,address_dataset_sync_state"}
}

func (s *Server) handleExplorerAssets(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveExplorerSubject(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	balances, observed := s.localBalanceSnapshot(ctx, sub)
	netFlow, _ := s.localHistoricalNetFlow(ctx, sub)
	subject := map[string]any{"type": "address", "chain": sub.Chain, "address": sub.Address, "normalized_address": sub.Normalized}
	summary := map[string]any{"current_balance_items": len(balances), "historical_net_flow_items": len(netFlow), "observed_at": observed}
	caliber := map[string]any{
		"current_balance":     "source=provider_snapshot,is_current_balance=true",
		"historical_net_flow": "source=local_index,is_current_balance=false",
		"amounts":             "raw_amount/display_amount 均为字符串",
	}
	respondOK(w, r, explorerEnvelope(subject,
		map[string]any{"current_balance": balances, "historical_net_flow": netFlow},
		summary, nil, caliber,
		"clickhouse:blockchain.address_balances", "clickhouse:blockchain.token_transfers", "clickhouse:blockchain.transactions"))
}

func (s *Server) handleExplorerActivity(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveExplorerSubject(w, r)
	if !ok {
		return
	}
	limit, err := parseOptionalLimit(r, 50, 200)
	if err != nil {
		validationError(w, r, "limit 不合法", nil)
		return
	}
	req := explorer.QueryRequest{
		Chain: sub.Chain, Addresses: []string{sub.Normalized}, Dataset: "activity",
		Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")), Limit: limit,
		Light: r.URL.Query().Get("full") == "1",
		Filters: explorer.Filter{
			Direction: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("direction"))),
			Token:     strings.TrimSpace(r.URL.Query().Get("token")),
			MinAmount: strings.TrimSpace(r.URL.Query().Get("min_amount")),
			MaxAmount: strings.TrimSpace(r.URL.Query().Get("max_amount")),
		},
	}
	res, qerr := s.explorer.Query(r.Context(), req)
	if qerr != nil {
		if errors.Is(qerr, explorer.ErrInvalidCursor) || errors.Is(qerr, explorer.ErrInvalidFilter) {
			validationError(w, r, "查询参数不合法", []fieldError{{Field: "cursor", Code: "invalid", Message: qerr.Error()}})
			return
		}
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "查询活动失败", nil)
		return
	}
	if res.Rows == nil {
		res.Rows = []map[string]any{}
	}
	subject := map[string]any{"type": "address", "chain": sub.Chain, "address": sub.Address, "normalized_address": sub.Normalized}
	summary := map[string]any{"count": res.Count, "limit": limit}
	caliber := map[string]any{
		"dataset": "blockchain.address_activity（地址 × 方向 × 对手方口径）",
		"amount":  "amount_raw 为原始整数；未知 decimals 不擅自换算。",
	}
	next := res.NextCursor
	respondList(w, r, explorerEnvelope(subject, res.Rows, summary, nil, caliber, "clickhouse:blockchain.address_activity"), -1, &next)
}

func (s *Server) handleExplorerCounterparties(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveExplorerSubject(w, r)
	if !ok {
		return
	}
	limit, err := parseOptionalLimit(r, 50, 200)
	if err != nil {
		validationError(w, r, "limit 不合法", nil)
		return
	}
	items := []map[string]any{}
	if s.deps.CH != nil {
		rows, qerr := s.deps.CH.DB().Query(r.Context(), `
			SELECT counterparty,
			       sum(tx_count) AS tx_count,
			       toString(sum(in_raw)) AS in_raw,
			       toString(sum(out_raw)) AS out_raw,
			       min(first_seen) AS first_seen,
			       max(last_seen) AS last_seen
			FROM (
				SELECT if(from_address = ?, to_address, from_address) AS counterparty,
				       count() AS tx_count,
				       sumIf(toInt256OrZero(value_raw), to_address = ?) AS in_raw,
				       sumIf(toInt256OrZero(value_raw), from_address = ?) AS out_raw,
				       min(timestamp) AS first_seen, max(timestamp) AS last_seen
				FROM blockchain.transactions FINAL
				WHERE chain = ? AND (from_address = ? OR to_address = ?)
				GROUP BY counterparty
				UNION ALL
				SELECT if(from_address = ?, to_address, from_address) AS counterparty,
				       count() AS tx_count,
				       sumIf(amount, to_address = ?) AS in_raw,
				       sumIf(amount, from_address = ?) AS out_raw,
				       min(timestamp) AS first_seen, max(timestamp) AS last_seen
				FROM blockchain.token_transfers FINAL
				WHERE chain = ? AND (from_address = ? OR to_address = ?)
				GROUP BY counterparty
			)
			GROUP BY counterparty ORDER BY tx_count DESC LIMIT ?`,
			sub.Normalized, sub.Normalized, sub.Normalized, sub.Chain, sub.Normalized, sub.Normalized,
			sub.Normalized, sub.Normalized, sub.Normalized, sub.Chain, sub.Normalized, sub.Normalized, limit)
		if qerr != nil {
			respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "本地索引暂不可用", nil)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var counterparty, inRaw, outRaw string
			var txCount int64
			var firstSeen, lastSeen *time.Time
			if err := rows.Scan(&counterparty, &txCount, &inRaw, &outRaw, &firstSeen, &lastSeen); err != nil {
				continue
			}
			items = append(items, map[string]any{
				"counterparty": counterparty, "transaction_count": txCount,
				"in_raw": inRaw, "out_raw": outRaw, "net_raw": subtractDecimalStrings(inRaw, outRaw),
				"first_seen": firstSeen, "last_seen": lastSeen,
			})
		}
	}
	subject := map[string]any{"type": "address", "chain": sub.Chain, "address": sub.Address, "normalized_address": sub.Normalized}
	respondOK(w, r, explorerEnvelope(subject, items, map[string]any{"count": len(items)}, nil,
		map[string]any{"scope": "本地已入库 transactions + token_transfers 按对手方聚合"},
		"clickhouse:blockchain.transactions", "clickhouse:blockchain.token_transfers"))
}

func (s *Server) handleExplorerSemantic(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveExplorerSubject(w, r)
	if !ok {
		return
	}
	if s.semantic == nil {
		respondError(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "语义引擎未启用", nil)
		return
	}
	result, err := s.semantic.Get(r.Context(), sub.Chain, sub.Normalized)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取语义事件失败", nil)
		return
	}
	subject := map[string]any{"type": "address", "chain": sub.Chain, "address": sub.Address, "normalized_address": sub.Normalized}
	respondOK(w, r, explorerEnvelope(subject, result, nil, nil,
		map[string]any{"algorithm_version": "protocol-semantic-v1.0", "nature": "DETERMINISTIC"},
		"postgres:semantic_events"))
}

func (s *Server) resolveExplorerTx(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	chainSlug := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "chain")))
	raw := strings.TrimSpace(chi.URLParam(r, "hash"))
	if err := s.validateChain(chainSlug); err != nil {
		validationError(w, r, "不支持的链", []fieldError{{Field: "chain", Code: "invalid", Message: err.Error()}})
		return "", "", false
	}
	// TRON 交易哈希在链上/Provider 中通常不带 0x 前缀；不做 EVM 式拒绝。
	if chainSlug == "tron" && hexHashRe.MatchString(raw) && !strings.HasPrefix(strings.ToLower(raw), "0x") {
		raw = "0x" + raw
	}
	hash, err := chain.ParseHash(raw)
	if err != nil {
		validationError(w, r, "交易哈希不合法", []fieldError{{Field: "hash", Code: "invalid", Message: err.Error()}})
		return "", "", false
	}
	return chainSlug, hash.String(), true
}

func (s *Server) explorerDecodeMode(r *http.Request) (method.DecodeMode, bool) {
	p := PrincipalFrom(r.Context())
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("decode")))
	switch raw {
	case "", "local_only", "local":
		return method.DecodeLocalOnly, true
	case "auto":
		if !p.Has("explorer:fetch") {
			return "", false
		}
		return method.DecodeAuto, true
	default:
		return "", false
	}
}

func (s *Server) handleExplorerTransaction(w http.ResponseWriter, r *http.Request) {
	subChain, hash, ok := s.resolveExplorerTx(w, r)
	if !ok {
		return
	}
	mode, ok := s.explorerDecodeMode(r)
	if !ok {
		respondError(w, r, http.StatusForbidden, "AUTHZ_FORBIDDEN", "当前账号不能触发 Provider 深度解码；请使用默认本地解码", nil)
		return
	}
	if s.detail == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "交易详情服务未启用", nil)
		return
	}
	detail, err := s.detail.Detail(r.Context(), subChain, hash, mode)
	if errors.Is(err, method.ErrTransactionNotFound) {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "本地数据集中不存在该交易", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusBadGateway, "PROVIDER_UNAVAILABLE", "交易详情暂不可用", nil)
		return
	}
	subject := map[string]any{"type": "transaction", "chain": subChain, "hash": hash}
	respondOK(w, r, explorerEnvelope(subject, detail,
		map[string]any{"decode_source": detail.DecodeSource, "decode_verified": detail.DecodeVerified}, nil,
		map[string]any{"decode": "local_only 不产生 Provider 请求；auto 先本地 ABI，失败才向 Provider 补取"},
		"clickhouse:blockchain.transactions", "clickhouse:blockchain.logs", "postgres:method_abi"))
}

func (s *Server) handleExplorerTransactionLogs(w http.ResponseWriter, r *http.Request) {
	subChain, hash, ok := s.resolveExplorerTx(w, r)
	if !ok {
		return
	}
	if s.detail == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "交易详情服务未启用", nil)
		return
	}
	detail, err := s.detail.Detail(r.Context(), subChain, hash, method.DecodeLocalOnly)
	if errors.Is(err, method.ErrTransactionNotFound) {
		respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "本地数据集中不存在该交易", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusBadGateway, "DATA_UNAVAILABLE", "日志暂不可用", nil)
		return
	}
	logs := detail.Logs
	if logs == nil {
		logs = []map[string]any{}
	}
	subject := map[string]any{"type": "transaction", "chain": subChain, "hash": hash}
	respondOK(w, r, explorerEnvelope(subject, logs, map[string]any{"count": len(logs)}, nil,
		map[string]any{"source": "clickhouse:blockchain.logs", "provider_fetch": false},
		"clickhouse:blockchain.logs"))
}

func (s *Server) handleExplorerTransactionTrace(w http.ResponseWriter, r *http.Request) {
	subChain, hash, ok := s.resolveExplorerTx(w, r)
	if !ok {
		return
	}
	p := PrincipalFrom(r.Context())
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	switch mode {
	case "", "local_only", "local":
		mode = "local_only"
	case "auto", "force_refresh":
		if !p.Has("explorer:fetch") {
			respondError(w, r, http.StatusForbidden, "AUTHZ_FORBIDDEN", "Trace 补取需要 explorer:fetch 权限", nil)
			return
		}
	default:
		validationError(w, r, "mode 不合法", nil)
		return
	}
	if s.deps.Trace == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "Trace 服务未启用", nil)
		return
	}
	parsed, err := chain.ParseHash(hash)
	if err != nil {
		validationError(w, r, "交易哈希不合法", nil)
		return
	}
	result, err := s.deps.Trace.TransactionTrace(r.Context(), subChain, parsed,
		chaintrace.Options{Consistency: mode, JobID: strings.TrimSpace(r.URL.Query().Get("job_id"))})
	if errors.Is(err, chaintrace.ErrTraceNotAvailableLocally) {
		respondError(w, r, http.StatusNotFound, "DATA_NOT_AVAILABLE_LOCALLY", "本地没有 Trace，需在知情后触发 Provider 补取",
			map[string]any{"provider_fetch_required": true})
		return
	}
	if err != nil {
		respondError(w, r, http.StatusBadGateway, "PROVIDER_UNAVAILABLE", "Trace 暂不可用", nil)
		return
	}
	subject := map[string]any{"type": "transaction", "chain": subChain, "hash": hash}
	respondOK(w, r, explorerEnvelope(subject, result, map[string]any{"mode": mode}, nil,
		map[string]any{"source": "本地缓存优先", "provider_fetch": mode != "local_only"},
		"clickhouse:blockchain.transaction_traces"))
}

// ---- 全局搜索（USER-009）----

type searchHit struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Chain    string `json:"chain,omitempty"`
	URL      string `json:"url"`
}

func (s *Server) handleUserSearch(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFrom(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		validationError(w, r, "搜索词至少 2 个字符", []fieldError{{Field: "q", Code: "too_short", Message: "至少 2 个字符"}})
		return
	}
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	limit := parseLimit(r, 10, 25)
	types := map[string]bool{}
	for _, raw := range r.URL.Query()["type"] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part != "" {
				types[part] = true
			}
		}
	}
	want := func(t string) bool { return len(types) == 0 || types[t] }
	like := "%" + strings.ToLower(q) + "%"
	items := []searchHit{}
	scopes := []string{}
	ctx := r.Context()

	if want("case") && p.Has("case:read") {
		scopes = append(scopes, "case")
		rows, err := s.deps.Store.Pool.Query(ctx, `
			SELECT id::text, coalesce(case_no,''), name FROM cases
			WHERE organization_id=$1 AND workspace_id=$2
			  AND (lower(name) LIKE $3 OR lower(coalesce(case_no,'')) LIKE $3)
			ORDER BY updated_at DESC LIMIT $4`, p.OrganizationID, p.WorkspaceID, like, limit)
		if err == nil {
			for rows.Next() {
				var id, no, name string
				if rows.Scan(&id, &no, &name) == nil {
					items = append(items, searchHit{Type: "case", ID: id, Title: name, Subtitle: no, URL: "/cases/" + id + "/overview"})
				}
			}
			rows.Close()
		}
	}
	if want("investigation") && p.Has("investigation:read") {
		scopes = append(scopes, "investigation")
		rows, err := s.deps.Store.Pool.Query(ctx, `
			SELECT i.id::text, coalesce(i.goal,''), coalesce(c.name,'') FROM investigations i
			LEFT JOIN cases c ON c.id=i.case_id
			WHERE i.organization_id=$1 AND i.workspace_id=$2 AND lower(coalesce(i.goal,'')) LIKE $3
			ORDER BY i.updated_at DESC LIMIT $4`, p.OrganizationID, p.WorkspaceID, like, limit)
		if err == nil {
			for rows.Next() {
				var id, goal, caseName string
				if rows.Scan(&id, &goal, &caseName) == nil {
					items = append(items, searchHit{Type: "investigation", ID: id, Title: goal, Subtitle: caseName, URL: "/investigations/" + id + "/workbench"})
				}
			}
			rows.Close()
		}
	}
	if want("job") && p.Has("job:read") {
		scopes = append(scopes, "job")
		rows, err := s.deps.Store.Pool.Query(ctx, `
			SELECT id::text, job_no, status, chain FROM download_jobs
			WHERE organization_id=$1 AND workspace_id=$2
			  AND ($4 OR created_by_identity::text=$3
			       OR created_by = (SELECT legacy_user_id FROM identities WHERE id=NULLIF($3,'')::uuid))
			  AND (lower(job_no) LIKE $5 OR id::text LIKE $5)
			ORDER BY updated_at DESC LIMIT $6`,
			p.OrganizationID, p.WorkspaceID, p.IdentityID, p.Has("job:list:workspace"), like, limit)
		if err == nil {
			for rows.Next() {
				var id, no, status, chainSlug string
				if rows.Scan(&id, &no, &status, &chainSlug) == nil {
					items = append(items, searchHit{Type: "job", ID: id, Title: no, Subtitle: status + " · " + chainSlug, Chain: chainSlug, URL: "/jobs/" + id})
				}
			}
			rows.Close()
		}
	}
	if want("report") && p.Has("report:read") {
		scopes = append(scopes, "report")
		rows, err := s.deps.Store.Pool.Query(ctx, `
			SELECT r.id::text, coalesce(i.goal,''), r.status
			FROM investigation_reports r
			LEFT JOIN investigations i ON i.id=r.investigation_id
			WHERE r.organization_id=$1 AND r.workspace_id=$2
			  AND (lower(coalesce(i.goal,'')) LIKE $3 OR r.id::text LIKE $3)
			ORDER BY r.generated_at DESC LIMIT $4`, p.OrganizationID, p.WorkspaceID, like, limit)
		if err == nil {
			for rows.Next() {
				var id, title, status string
				if rows.Scan(&id, &title, &status) == nil {
					items = append(items, searchHit{Type: "report", ID: id, Title: title, Subtitle: status, URL: "/reports/" + id})
				}
			}
			rows.Close()
		}
	}
	if (want("address") || want("transaction")) && p.Has("explorer:read") {
		s.searchChainIndex(ctx, q, want, &items, &scopes)
	}
	groups := map[string][]searchHit{}
	for _, hit := range items {
		groups[hit.Type] = append(groups[hit.Type], hit)
	}
	respondList(w, r, map[string]any{
		"query": q, "items": items, "groups": groups, "scopes": scopes,
		"caliber": map[string]any{
			"authorization": "case/investigation/job/report 由服务端 organization+workspace+权限裁剪；address/transaction 只返回 explorer:read 可见的本地索引。",
			"source":        "postgres + clickhouse local index，不触发 Provider 请求。",
		},
	}, len(items), nil)
}

func (s *Server) searchChainIndex(ctx context.Context, q string, want func(string) bool, items *[]searchHit, scopes *[]string) {
	if s.deps.CH == nil {
		return
	}
	lower := strings.ToLower(q)
	if want("address") && (evmAddressRe.MatchString(lower) || tron.ValidateTronAddress(q) == nil) {
		*scopes = append(*scopes, "address")
		addr := lower
		if tron.ValidateTronAddress(q) == nil {
			if n, err := tron.NormalizeTronAddress(q); err == nil {
				addr = n
			}
		}
		rows, err := s.deps.CH.DB().Query(ctx, `
			SELECT chain, count() AS c FROM (
				SELECT chain FROM blockchain.transactions FINAL WHERE from_address=? OR to_address=?
				UNION ALL
				SELECT chain FROM blockchain.token_transfers FINAL WHERE from_address=? OR to_address=?
			) GROUP BY chain ORDER BY c DESC LIMIT 5`, addr, addr, addr, addr)
		if err == nil {
			for rows.Next() {
				var chainSlug string
				var count uint64
				if rows.Scan(&chainSlug, &count) == nil {
					*items = append(*items, searchHit{Type: "address", ID: chainSlug + ":" + addr,
						Title: addr, Subtitle: chainSlug + " · " + fmt.Sprint(count) + " 条本地索引记录",
						Chain: chainSlug, URL: "/addresses/" + chainSlug + "/" + addr})
				}
			}
			rows.Close()
		}
	}
	if want("transaction") && (evmTxHashRe.MatchString(lower) || hexHashRe.MatchString(q)) {
		*scopes = append(*scopes, "transaction")
		hash := strings.TrimPrefix(lower, "0x")
		rows, err := s.deps.CH.DB().Query(ctx, `
			SELECT DISTINCT chain FROM (
				SELECT chain FROM blockchain.transactions FINAL WHERE lower(tx_hash)=?
				UNION ALL
				SELECT chain FROM blockchain.token_transfers FINAL WHERE lower(tx_hash)=?
				UNION ALL
				SELECT chain FROM blockchain.logs FINAL WHERE lower(tx_hash)=?
			) LIMIT 5`, hash, hash, hash)
		if err == nil {
			for rows.Next() {
				var chainSlug string
				if rows.Scan(&chainSlug) == nil {
					*items = append(*items, searchHit{Type: "transaction", ID: chainSlug + ":" + hash,
						Title: hash, Subtitle: chainSlug, Chain: chainSlug, URL: "/transactions/" + chainSlug + "/" + hash})
				}
			}
			rows.Close()
		}
	}
}
