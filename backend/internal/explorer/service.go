// Package explorer：统一数据浏览查询（`61/`62：cursor 分页 + 多地址去重 + Token 富化）。
package explorer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/tokenmeta"
)

type Service struct {
	CH           *clickhouse.Conn
	Store        *store.Store
	Q            *query.Service
	Tokens       *tokenmeta.Service
	AddressTypes provider.AddressTypeResolver
	typeMu       sync.Mutex
	typeCache    map[string]cachedAddressType
}

type cachedAddressType struct {
	isContract bool
	expiresAt  time.Time
}

func New(ch *clickhouse.Conn, st *store.Store, tokens ...*tokenmeta.Service) *Service {
	var svc *tokenmeta.Service
	if len(tokens) > 0 {
		svc = tokens[0]
	}
	return &Service{CH: ch, Store: st, Q: query.New(ch), Tokens: svc, typeCache: map[string]cachedAddressType{}}
}

func (s *Service) WithAddressTypeResolver(resolver provider.AddressTypeResolver) *Service {
	s.AddressTypes = resolver
	return s
}

type Filter struct {
	FromTime    *time.Time `json:"from_time"`
	ToTime      *time.Time `json:"to_time"`
	Direction   string     `json:"direction"`
	Token       string     `json:"token"`
	FromAddress string     `json:"from_address"`
	ToAddress   string     `json:"to_address"`
	MethodID    string     `json:"method_id"`
	// V1.3 Method 搜索/筛选（§28）：名称、签名与统一搜索框
	MethodName      string `json:"method_name"`
	MethodSignature string `json:"method_signature"`
	Method          string `json:"method"`
	MethodSource    string `json:"method_source"`
	MethodVerified  *bool  `json:"method_verified"`
	MinAmount       string `json:"min_amount"`
	MaxAmount       string `json:"max_amount"`
	BlockFrom       *int64 `json:"block_from"`
	BlockTo         *int64 `json:"block_to"`
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
		appendPartyFilters(&conds, &args, f)
		if single && f.Direction != "" {
			if f.Direction == "out" {
				conds = append(conds, "from_address = ?")
			} else {
				conds = append(conds, "to_address = ?")
			}
			args = append(args, req.Addresses[0])
		}
		appendMethodFilters(&conds, &args, f)
	case "token_transfers":
		appendPartyFilters(&conds, &args, f)
		if single && f.Direction != "" {
			if f.Direction == "out" {
				conds = append(conds, "from_address = ?")
			} else {
				conds = append(conds, "to_address = ?")
			}
			args = append(args, req.Addresses[0])
		}
		if f.Token != "" {
			appendTokenFilter(&conds, &args, f.Token, "token_symbol", "token_address")
		}
		if f.MinAmount != "" {
			conds = append(conds, "length(amount_raw) <= toUInt64(token_decimals) + 38 AND "+
				"divideDecimal(toDecimal256(amount_raw,0), toDecimal256(concat('1',repeat('0',toUInt64(token_decimals))),0),38) >= toDecimal256(?,38)")
			args = append(args, f.MinAmount)
		}
		if f.MaxAmount != "" {
			conds = append(conds, "length(amount_raw) <= toUInt64(token_decimals) + 38 AND "+
				"divideDecimal(toDecimal256(amount_raw,0), toDecimal256(concat('1',repeat('0',toUInt64(token_decimals))),0),38) <= toDecimal256(?,38)")
			args = append(args, f.MaxAmount)
		}
	case "nft_transfers":
		appendPartyFilters(&conds, &args, f)
		if f.Token != "" {
			appendTokenFilter(&conds, &args, f.Token, "token_name", "token_symbol", "contract_address")
		}
	case "logs":
		if f.Token != "" {
			appendTokenFilter(&conds, &args, f.Token, "address")
		}
	case "activity":
		if f.FromAddress != "" {
			from := strings.ToLower(f.FromAddress)
			conds = append(conds, "((direction IN ('out','self') AND address = ?) OR (direction = 'in' AND counterparty = ?))")
			args = append(args, from, from)
		}
		if f.ToAddress != "" {
			to := strings.ToLower(f.ToAddress)
			conds = append(conds, "((direction IN ('in','self') AND address = ?) OR (direction = 'out' AND counterparty = ?))")
			args = append(args, to, to)
		}
		if f.Token != "" {
			appendTokenFilter(&conds, &args, f.Token, "token_symbol", "token_address")
		}
		amountExpr := "if(amount_raw != '', divideDecimal(toDecimal256(amount_raw,0), toDecimal256(concat('1',repeat('0',toUInt64(token_decimals))),0),38), amount)"
		if f.MinAmount != "" {
			conds = append(conds, amountExpr+" >= toDecimal256(?,38)")
			args = append(args, f.MinAmount)
		}
		if f.MaxAmount != "" {
			conds = append(conds, amountExpr+" <= toDecimal256(?,38)")
			args = append(args, f.MaxAmount)
		}
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

func appendPartyFilters(conds *[]string, args *[]any, f Filter) {
	if f.FromAddress != "" {
		*conds = append(*conds, "from_address = ?")
		*args = append(*args, strings.ToLower(f.FromAddress))
	}
	if f.ToAddress != "" {
		*conds = append(*conds, "to_address = ?")
		*args = append(*args, strings.ToLower(f.ToAddress))
	}
}

// appendMethodFilters Method 搜索与筛选（§28）。
//
// 支持：
//   - method_id：0xa9059cbb（可逗号分隔多个）
//   - method_name / method_signature：子串匹配
//   - method：统一搜索框，自动识别 selector / 名称 / 签名 / "unknown"
//   - method_source / method_verified：来源与验证状态筛选（§27）
func appendMethodFilters(conds *[]string, args *[]any, f Filter) {
	if f.MethodID != "" {
		var selectorConds []string
		for _, raw := range strings.Split(f.MethodID, ",") {
			selector := method.NormalizeSelector(raw)
			if selector == "" {
				continue
			}
			selectorConds = append(selectorConds, "method_id = ?")
			*args = append(*args, selector)
		}
		if len(selectorConds) > 0 {
			*conds = append(*conds, "("+strings.Join(selectorConds, " OR ")+")")
		}
	}
	if term := strings.TrimSpace(f.MethodName); term != "" {
		*conds = append(*conds, "positionCaseInsensitive(method_name, ?) > 0")
		*args = append(*args, term)
	}
	if term := strings.TrimSpace(f.MethodSignature); term != "" {
		*conds = append(*conds, "positionCaseInsensitive(method_signature, ?) > 0")
		*args = append(*args, term)
	}
	if term := strings.TrimSpace(f.Method); term != "" {
		if selector := method.NormalizeSelector(term); selector != "" {
			*conds = append(*conds, "method_id = ?")
			*args = append(*args, selector)
		} else if strings.EqualFold(term, "unknown") {
			// Unknown 必须可筛选（§62-19/§62-20）
			*conds = append(*conds, "method_name = '' AND method_id != ''")
		} else {
			*conds = append(*conds, "(positionCaseInsensitive(method_name, ?) > 0 OR positionCaseInsensitive(method_signature, ?) > 0)")
			*args = append(*args, term, term)
		}
	}
	if source := strings.TrimSpace(f.MethodSource); source != "" {
		*conds = append(*conds, "method_source = ?")
		*args = append(*args, source)
	}
	if f.MethodVerified != nil {
		verified := uint8(0)
		if *f.MethodVerified {
			verified = 1
		}
		*conds = append(*conds, "method_verified = ?")
		*args = append(*args, verified)
	}
}

func appendTokenFilter(conds *[]string, args *[]any, input string, columns ...string) {
	var tokenConds []string
	for _, raw := range strings.Split(input, ",") {
		term := strings.TrimSpace(raw)
		if term == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(term), "0x") && len(term) == 42 {
			contractColumn := "token_address"
			if len(columns) > 0 && columns[len(columns)-1] == "contract_address" {
				contractColumn = "contract_address"
			}
			if len(columns) == 1 && columns[0] == "address" {
				contractColumn = "address"
			}
			tokenConds = append(tokenConds, contractColumn+" = ?")
			*args = append(*args, strings.ToLower(term))
			continue
		}
		var columnConds []string
		for _, column := range columns {
			columnConds = append(columnConds, "positionCaseInsensitive("+column+", ?) > 0")
			*args = append(*args, term)
		}
		tokenConds = append(tokenConds, "("+strings.Join(columnConds, " OR ")+")")
	}
	if len(tokenConds) > 0 {
		*conds = append(*conds, "("+strings.Join(tokenConds, " OR ")+")")
	}
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
	addressTypes := s.resolveAddressTypes(ctx, chain, rows)
	metas := map[string]tokenmeta.Metadata{}
	if s.Tokens != nil {
		metas, _ = s.Tokens.GetMany(ctx, chain, contracts)
	}
	// 案件/标签（当前页涉及的地址）
	needMeta := map[string]bool{}
	for _, r := range rows {
		from, _ := r["from_address"].(string)
		to, _ := r["to_address"].(string)
		if from == "" && to == "" {
			direction, _ := r["direction"].(string)
			own, _ := r["address"].(string)
			counterparty, _ := r["counterparty"].(string)
			if direction == "out" {
				from, to = own, counterparty
			} else if direction == "in" {
				from, to = counterparty, own
			} else {
				from, to = own, own
			}
		}
		r["from_is_contract"] = addressTypes[strings.ToLower(from)]
		r["to_is_contract"] = addressTypes[strings.ToLower(to)]
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
				if meta.Decimals != nil {
					r["token_decimals"] = *meta.Decimals
				} else {
					r["token_decimals"] = nil
				}
				r["token_type"] = meta.TokenType
				if meta.LogoObjectKey != "" {
					r["token_logo"] = "/api/v1/tokens/" + chain + "/" + ta + "/logo"
				} else if meta.Verified && meta.LogoURL != "" {
					// Only reviewed registry entries may fall back to a remote logo.
					r["token_logo"] = meta.LogoURL
				} else {
					r["token_logo"] = ""
				}
				r["token_metadata_status"] = meta.MetadataStatus
				r["token_metadata_source"] = meta.MetadataSource
				r["token_metadata_confidence"] = meta.Confidence
				r["token_verified"] = meta.Verified
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

func (s *Service) resolveAddressTypes(ctx context.Context, chain string, rows []map[string]any) map[string]bool {
	result := map[string]bool{}
	if s.AddressTypes == nil {
		return result
	}
	candidates := map[string]bool{}
	for _, row := range rows {
		for _, field := range []string{"from_address", "to_address", "address", "counterparty"} {
			value := strings.ToLower(strings.TrimSpace(valueString(row[field])))
			if len(value) == 42 && strings.HasPrefix(value, "0x") {
				candidates[value] = true
			}
		}
	}
	now := time.Now()
	missing := make([]string, 0, len(candidates))
	s.typeMu.Lock()
	for address := range candidates {
		key := strings.ToLower(chain) + ":" + address
		if cached, ok := s.typeCache[key]; ok && now.Before(cached.expiresAt) {
			result[address] = cached.isContract
		} else {
			missing = append(missing, address)
		}
	}
	s.typeMu.Unlock()
	if len(missing) == 0 {
		return result
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resolved, err := s.AddressTypes.ResolveAddressTypes(resolveCtx, chain, missing)
	if err != nil {
		return result
	}
	s.typeMu.Lock()
	defer s.typeMu.Unlock()
	for _, address := range missing {
		isContract := resolved[address]
		result[address] = isContract
		s.typeCache[strings.ToLower(chain)+":"+address] = cachedAddressType{isContract: isContract, expiresAt: now.Add(24 * time.Hour)}
	}
	return result
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
