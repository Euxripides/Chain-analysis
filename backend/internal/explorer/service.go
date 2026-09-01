// Package explorer：统一数据浏览查询（`61/`62：cursor 分页 + 多地址去重 + Token 富化）。
package explorer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

type Service struct {
	CH    *clickhouse.Conn
	Store *store.Store
	Q     *query.Service
}

func New(ch *clickhouse.Conn, st *store.Store) *Service {
	return &Service{CH: ch, Store: st, Q: query.New(ch)}
}

type Filter struct {
	FromTime  *time.Time `json:"from_time"`
	ToTime    *time.Time `json:"to_time"`
	Direction string     `json:"direction"`
	Token     string     `json:"token"`
	MethodID  string     `json:"method_id"`
	MinAmount string     `json:"min_amount"`
	MaxAmount string     `json:"max_amount"`
	BlockFrom *int64     `json:"block_from"`
	BlockTo   *int64     `json:"block_to"`
}

type QueryRequest struct {
	Chain     string   `json:"chain"`
	Addresses []string `json:"addresses"`
	Dataset   string   `json:"dataset"`
	Filters   Filter   `json:"filters"`
	Cursor    string   `json:"cursor"`
	Limit     int      `json:"limit"`
}

type Result struct {
	Rows       []map[string]any `json:"rows"`
	NextCursor string           `json:"next_cursor"`
	Count      int              `json:"count"`
}

func (s *Service) Query(ctx context.Context, req QueryRequest) (*Result, error) {
	if req.Limit <= 0 || req.Limit > 500 {
		req.Limit = 100
	}
	if len(req.Addresses) == 0 || req.Chain == "" {
		return nil, fmt.Errorf("chain and addresses required")
	}
	for i := range req.Addresses {
		req.Addresses[i] = strings.ToLower(req.Addresses[i])
	}
	rows, err := s.queryDataset(ctx, req)
	if err != nil {
		return nil, err
	}
	s.enrich(ctx, req.Chain, req.Addresses, rows)
	next := ""
	if len(rows) == req.Limit {
		last := rows[len(rows)-1]
		if cur, ok := cursorFromRow(last); ok {
			cur.Dataset = req.Dataset
			next = encodeCursor(cur)
		}
	}
	return &Result{Rows: rows, NextCursor: next, Count: len(rows)}, nil
}

func (s *Service) queryDataset(ctx context.Context, req QueryRequest) ([]map[string]any, error) {
	f := req.Filters
	var conds []string
	var args []any
	if f.FromTime != nil {
		conds = append(conds, "timestamp >= ?")
		args = append(args, f.FromTime.UTC())
	}
	if f.ToTime != nil {
		conds = append(conds, "timestamp <= ?")
		args = append(args, f.ToTime.UTC())
	}
	if f.BlockFrom != nil {
		conds = append(conds, "block_number >= ?")
		args = append(args, *f.BlockFrom)
	}
	if f.BlockTo != nil {
		conds = append(conds, "block_number <= ?")
		args = append(args, *f.BlockTo)
	}
	single := len(req.Addresses) == 1
	switch req.Dataset {
	case "transactions":
		if single && f.Direction != "" {
			if f.Direction == "out" {
				conds = append(conds, "from_address = ?")
			} else {
				conds = append(conds, "to_address = ?")
			}
			args = append(args, req.Addresses[0])
		}
		if f.MethodID != "" {
			conds = append(conds, "method_id = ?")
			args = append(args, f.MethodID)
		}
	case "token_transfers":
		if single && f.Direction != "" {
			if f.Direction == "out" {
				conds = append(conds, "from_address = ?")
			} else {
				conds = append(conds, "to_address = ?")
			}
			args = append(args, req.Addresses[0])
		}
		if f.Token != "" {
			conds = append(conds, "token_address = ?")
			args = append(args, strings.ToLower(f.Token))
		}
		if f.MinAmount != "" {
			conds = append(conds, "amount >= toDecimal256(?,38)")
			args = append(args, f.MinAmount)
		}
		if f.MaxAmount != "" {
			conds = append(conds, "amount <= toDecimal256(?,38)")
			args = append(args, f.MaxAmount)
		}
	case "nft_transfers":
		if f.Token != "" {
			conds = append(conds, "contract_address = ?")
			args = append(args, strings.ToLower(f.Token))
		}
	case "logs":
		if f.Token != "" {
			conds = append(conds, "address = ?")
			args = append(args, strings.ToLower(f.Token))
		}
	case "activity":
		if f.Direction != "" {
			conds = append(conds, "direction = ?")
			args = append(args, f.Direction)
		}
	}
	filterSQL := strings.Join(conds, " AND ")
	var qCursor *query.ExplorerCursor
	if cur, err := decodeCursor(req.Cursor); err == nil && !cur.Ts.IsZero() {
		qCursor = &query.ExplorerCursor{
			Timestamp: cur.Ts, TxHash: cur.Hash, LogIndex: cur.LogIndex,
			TokenAddress: cur.TokenAddress, ContractAddress: cur.ContractAddress,
			TokenID: cur.TokenID, ActivityType: cur.ActivityType, Counterparty: cur.Counterparty,
		}
	}
	return s.Q.Explorer(ctx, req.Dataset, req.Chain, req.Addresses, filterSQL, args, qCursor, req.Limit)
}

func partitionBy(ds string) string {
	switch ds {
	case "transactions":
		return "tx_hash"
	case "logs":
		return "tx_hash, log_index"
	case "nft_transfers":
		return "tx_hash, log_index, contract_address, token_id"
	default:
		return "tx_hash, log_index, token_address"
	}
}

// enrich Token Symbol/Name/Logo + matched_addresses + 案件/标签（`12/`21/`29/`50）。
func (s *Service) enrich(ctx context.Context, chain string, addresses []string, rows []map[string]any) {
	set := map[string]bool{}
	for _, a := range addresses {
		set[a] = true
	}
	var contracts []string
	seen := map[string]bool{}
	for _, r := range rows {
		if ta, ok := r["token_address"].(string); ok && ta != "" && !seen[ta] {
			seen[ta] = true
			contracts = append(contracts, ta)
		}
	}
	metas, _ := s.Store.TokenMetadatas(ctx, chain, contracts)
	// 案件/标签（当前页涉及的地址）
	needMeta := map[string]bool{}
	for _, r := range rows {
		from, _ := r["from_address"].(string)
		to, _ := r["to_address"].(string)
		matched := []string{}
		if set[from] {
			matched = append(matched, from)
		}
		if set[to] && to != from {
			matched = append(matched, to)
		}
		if len(matched) == 0 {
			if a, ok := r["address"].(string); ok && set[a] {
				matched = append(matched, a)
			}
		}
		r["matched_addresses"] = matched
		if ta, ok := r["token_address"].(string); ok && ta != "" {
			if meta, ok2 := metas[ta]; ok2 {
				r["token_symbol"] = meta.Symbol
				r["token_name"] = meta.Name
				r["token_logo"] = meta.LogoURL
			} else {
				if sym, ok3 := r["token_symbol"].(string); ok3 && sym != "" {
					r["token_symbol"] = sym
				}
			}
		}
		for _, a := range matched {
			needMeta[a] = true
		}
	}
	if len(needMeta) == 0 {
		return
	}
	wsID := ""
	if _, w, err := s.Store.DefaultOrgWorkspace(ctx); err == nil {
		wsID = w
	}
	caseCache := map[string][]string{}
	tagCache := map[string][]string{}
	for a := range needMeta {
		if wsID != "" {
			cases, _ := s.Store.AddressCaseNames(ctx, wsID, chain, a)
			tags, _ := s.Store.AddressTagNames(ctx, wsID, chain, a)
			caseCache[a] = cases
			tagCache[a] = tags
		}
	}
	for _, r := range rows {
		matched, _ := r["matched_addresses"].([]string)
		var cases, tags []string
		for _, a := range matched {
			cases = append(cases, caseCache[a]...)
			tags = append(tags, tagCache[a]...)
		}
		r["case_names"] = dedupe(cases)
		r["tag_names"] = dedupe(tags)
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// ---- cursor ----

type pageCursor struct {
	Ts              time.Time `json:"ts"`
	Hash            string    `json:"hash"`
	Dataset         string    `json:"dataset,omitempty"`
	LogIndex        uint64    `json:"log_index,omitempty"`
	TokenAddress    string    `json:"token_address,omitempty"`
	ContractAddress string    `json:"contract_address,omitempty"`
	TokenID         string    `json:"token_id,omitempty"`
	ActivityType    string    `json:"activity_type,omitempty"`
	Counterparty    string    `json:"counterparty,omitempty"`
}

func encodeCursor(cur pageCursor) string {
	raw, _ := json.Marshal(cur)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(c string) (cur pageCursor, err error) {
	if c == "" {
		return cur, fmt.Errorf("empty cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return cur, err
	}
	if len(b) > 0 && b[0] == '{' {
		if err := json.Unmarshal(b, &cur); err != nil {
			return cur, err
		}
		return cur, nil
	}
	// Backward compatibility with the original timestamp|hash cursor.
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return cur, fmt.Errorf("bad cursor")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return cur, err
	}
	cur.Ts = ts
	cur.Hash = parts[1]
	return cur, nil
}

func cursorFromRow(row map[string]any) (pageCursor, bool) {
	ts, ok := valueTime(row["timestamp"])
	if !ok {
		return pageCursor{}, false
	}
	return pageCursor{
		Ts: ts, Hash: valueString(row["tx_hash"]), LogIndex: valueUint64(row["log_index"]),
		TokenAddress: valueString(row["token_address"]), ContractAddress: valueString(row["contract_address"]),
		TokenID: valueString(row["token_id"]), ActivityType: valueString(row["activity_type"]),
		Counterparty: valueString(row["counterparty"]),
	}, true
}

func valueTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		t, err := time.Parse(time.RFC3339Nano, x)
		return t, err == nil
	default:
		return time.Time{}, false
	}
}

func valueString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func valueUint64(v any) uint64 {
	switch x := v.(type) {
	case json.Number:
		n, _ := strconv.ParseUint(string(x), 10, 64)
		return n
	case uint64:
		return x
	case uint32:
		return uint64(x)
	case int:
		return uint64(x)
	case float64:
		return uint64(x)
	default:
		n, _ := strconv.ParseUint(fmt.Sprint(v), 10, 64)
		return n
	}
}
