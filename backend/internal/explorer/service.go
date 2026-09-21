// Package explorer：统一数据浏览查询（`61/`62：cursor 分页 + 多地址去重 + Token 富化）。
package explorer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	soladdr "github.com/blockchain-platform/blockchain-platform/internal/solana"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/internal/tokenmeta"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

var ErrInvalidCursor = errors.New("invalid cursor")
var ErrInvalidFilter = errors.New("invalid filter")
var decimalFilterRe = regexp.MustCompile(`^(?:\d+(?:\.\d*)?|\.\d+)$`)
var addressFilterRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

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

const (
	// addressTypeSuccessTTL：成功解析的缓存时长（与原实现一致，24h）。
	addressTypeSuccessTTL = 24 * time.Hour
	// addressTypeResolveBudget：单次请求允许花在"地址类型解析"上的**墙钟**上限。
	// 原实现给 8s，一页上百个候选地址逐个串行外呼必然耗尽预算 ⇒ 每个请求都卡满 8s。
	// 现在压到 500ms：到点就返回已解析的部分，未解析项交给后续请求（缓存逐轮填充）。
	addressTypeResolveBudget = 500 * time.Millisecond
	// addressTypeMaxResolve：每次请求最多解析多少个地址（按地址排序保证确定性）。
	// TRON 的 AccountState 是"每地址一次 HTTP"，全量解析上百个地址在交互预算内不可能完成；
	// 每请求解析 16 个（并发 8 ⇒ 约 2 轮），配合 24h 成功缓存，几次请求后整页类型就都齐了。
	addressTypeMaxResolve = 16
	// addressTypeNegativeTTL：未解析成功（超时/上游失败）时的负缓存时长。
	// 目的：原实现超时后**一条缓存都不写**，导致每个请求都重新烧满 8s；
	// 这里缓存"这一页没解析出来"这个事实 2 分钟，显示语义与超时后缺失一致（按非合约呈现），过期后自然重试。
	addressTypeNegativeTTL = 2 * time.Minute
)

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
	// Light 只跳过逐行 Contract/Case/Tag 富化，用于客户端“加载全部”性能路径；
	// 不改变数据来源和行数，字段仍来自本地 ClickHouse（§30.1）。
	Light bool `json:"-"`

	// OrgID / WSID 是**服务端注入**的租户作用域（json:"-"：客户端无法提交）。
	//
	// P0 修复：enrich 会把案件名/标签名附加到每一行；旧实现用
	// Store.DefaultOrgWorkspace 取默认工作区，等于把默认组织的案件名泄露给
	// 任何其他组织的调用者。作用域为空时直接跳过富化（fail closed）。
	OrgID string `json:"-"`
	WSID  string `json:"-"`
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
	if err := validateFiltersForChain(req.Chain, req.Filters); err != nil {
		return nil, err
	}
	for i := range req.Addresses {
		// Solana 地址大小写敏感（ADR-023）：规范化只保留 base58 原文，绝不折叠；
		// 折叠后查询条件会变成另一个地址，表现为"明明有数据却查不到"。
		normalized, err := addressnorm.Normalize(req.Chain, req.Addresses[i])
		if err != nil {
			// 归一到 ErrInvalidFilter：HTTP 层据此返回 400 而不是 500。
			return nil, fmt.Errorf("%w: %v", ErrInvalidFilter, err)
		}
		req.Addresses[i] = normalized
	}
	rows, err := s.queryDataset(ctx, req)
	if err != nil {
		return nil, err
	}
	if !req.Light {
		s.enrich(ctx, req.Chain, req.Addresses, rows, req.WSID)
	}
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

func validateFilters(f Filter) error { return validateFiltersForChain("", f) }

func validateFiltersForChain(chain string, f Filter) error {
	if f.FromTime != nil && f.ToTime != nil && f.FromTime.After(*f.ToTime) {
		return fmt.Errorf("%w: from_time is after to_time", ErrInvalidFilter)
	}
	if (f.BlockFrom != nil && *f.BlockFrom < 0) || (f.BlockTo != nil && *f.BlockTo < 0) {
		return fmt.Errorf("%w: block range cannot be negative", ErrInvalidFilter)
	}
	if f.BlockFrom != nil && f.BlockTo != nil && *f.BlockFrom > *f.BlockTo {
		return fmt.Errorf("%w: block_from is greater than block_to", ErrInvalidFilter)
	}
	if f.Direction != "" && f.Direction != "in" && f.Direction != "out" && f.Direction != "self" {
		return fmt.Errorf("%w: unsupported direction", ErrInvalidFilter)
	}
	for name, value := range map[string]string{"from_address": f.FromAddress, "to_address": f.ToAddress} {
		if value != "" {
			if chain == "tron" {
				if tron.ValidateTronAddress(value) != nil {
					return fmt.Errorf("%w: %s is not a TRON address", ErrInvalidFilter, name)
				}
			} else if chain == "solana" {
				if _, err := soladdr.NormalizeSolanaAddress(value); err != nil {
					return fmt.Errorf("%w: %s is not a Solana address", ErrInvalidFilter, name)
				}
			} else if !addressFilterRe.MatchString(value) {
				return fmt.Errorf("%w: %s is not an EVM address", ErrInvalidFilter, name)
			}
		}
	}
	var min, max *big.Rat
	for name, value := range map[string]string{"min_amount": f.MinAmount, "max_amount": f.MaxAmount} {
		if value == "" {
			continue
		}
		if !decimalFilterRe.MatchString(value) {
			return fmt.Errorf("%w: %s is not a non-negative decimal", ErrInvalidFilter, name)
		}
		parsed, ok := new(big.Rat).SetString(value)
		if !ok {
			return fmt.Errorf("%w: %s is not a decimal", ErrInvalidFilter, name)
		}
		if name == "min_amount" {
			min = parsed
		} else {
			max = parsed
		}
	}
	if min != nil && max != nil && min.Cmp(max) > 0 {
		return fmt.Errorf("%w: min_amount is greater than max_amount", ErrInvalidFilter)
	}
	return nil
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
		appendPartyFilters(&conds, &args, req.Chain, f)
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
		appendPartyFilters(&conds, &args, req.Chain, f)
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
		appendPartyFilters(&conds, &args, req.Chain, f)
		if f.Token != "" {
			appendTokenFilter(&conds, &args, f.Token, "token_name", "token_symbol", "contract_address")
		}
	case "logs":
		if f.Token != "" {
			appendTokenFilter(&conds, &args, f.Token, "address")
		}
	case "activity":
		if f.FromAddress != "" {
			from := addressnorm.NormalizeKey(req.Chain, f.FromAddress)
			conds = append(conds, "((direction IN ('out','self') AND address = ?) OR (direction = 'in' AND counterparty = ?))")
			args = append(args, from, from)
		}
		if f.ToAddress != "" {
			to := addressnorm.NormalizeKey(req.Chain, f.ToAddress)
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
	if req.Cursor != "" {
		cur, err := decodeCursor(req.Cursor)
		if err != nil || cur.Ts.IsZero() || (cur.Dataset != "" && cur.Dataset != req.Dataset) {
			return nil, ErrInvalidCursor
		}
		qCursor = &query.ExplorerCursor{
			Timestamp: cur.Ts, TxHash: cur.Hash, LogIndex: cur.LogIndex,
			TokenAddress: cur.TokenAddress, ContractAddress: cur.ContractAddress,
			TokenID: cur.TokenID, ActivityType: cur.ActivityType, Counterparty: cur.Counterparty,
		}
	}
	return s.Q.Explorer(ctx, req.Dataset, req.Chain, req.Addresses, filterSQL, args, qCursor, req.Limit)
}

func appendPartyFilters(conds *[]string, args *[]any, chain string, f Filter) {
	// 查询键归一化必须与写入侧同源：合法地址结果一致，非法输入按原样绑定
	// （历史 tron 分支同样在规范化失败时回退原值）。
	normalize := func(v string) string { return addressnorm.NormalizeKey(chain, v) }
	if f.FromAddress != "" {
		*conds = append(*conds, "from_address = ?")
		*args = append(*args, normalize(f.FromAddress))
	}
	if f.ToAddress != "" {
		*conds = append(*conds, "to_address = ?")
		*args = append(*args, normalize(f.ToAddress))
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
// EnrichTokenMetadata 只补代币元数据（name/symbol/decimals/logo），不做需要 RPC 的地址类型解析。
//
// 活动读默认走 Light 路径（handler 只在 full=1 时才跑完整 enrich），因此此前活动行拿不到
// token_name/logo；OKLink 式列表需要"符号 + 全称 + 图标"，而这三样都在 tokenmeta 里。
// 单页只查一次 GetMany（按本页 distinct 合约地址），代价可控，故无条件执行。
func (s *Service) EnrichTokenMetadata(ctx context.Context, chain string, rows []map[string]any) {
	if s.Tokens == nil || len(rows) == 0 {
		return
	}
	contracts := make([]string, 0, 8)
	seen := map[string]bool{}
	for _, row := range rows {
		addr, _ := row["token_address"].(string)
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		contracts = append(contracts, addr)
	}
	if len(contracts) == 0 {
		return
	}
	metas, err := s.Tokens.GetMany(ctx, chain, contracts)
	if err != nil {
		return
	}
	for _, row := range rows {
		addr, _ := row["token_address"].(string)
		if meta, ok := metas[addr]; ok {
			applyTokenMetadata(chain, row, addr, meta)
		}
	}
}

// applyTokenMetadata 把 tokenmeta 条目写进一条活动/NFT 记录。
//
// 完整 enrich 与 EnrichTokenMetadata 共用它，避免两条路径的字段名漂移：
// 图标只有 LogoObjectKey（自托管对象）或"已审核 + LogoURL"才给，其余留空由前端兜底。
func applyTokenMetadata(chain string, row map[string]any, contract string, meta tokenmeta.Metadata) {
	row["token_symbol"] = meta.Symbol
	row["token_name"] = meta.Name
	if meta.Decimals != nil {
		row["token_decimals"] = *meta.Decimals
	} else {
		row["token_decimals"] = nil
	}
	row["token_type"] = meta.TokenType
	if meta.LogoObjectKey != "" {
		row["token_logo"] = "/api/v1/tokens/" + chain + "/" + contract + "/logo"
	} else if meta.Verified && meta.LogoURL != "" {
		row["token_logo"] = meta.LogoURL
	} else {
		row["token_logo"] = ""
	}
	row["token_metadata_status"] = meta.MetadataStatus
	row["token_metadata_source"] = meta.MetadataSource
	row["token_metadata_confidence"] = meta.Confidence
	row["token_verified"] = meta.Verified
}

// rowDecimals 兼容 ClickHouse 扫描出来的各种整数形态；拿不到就当 0。
func rowDecimals(row map[string]any) int64 {
	switch v := row["token_decimals"].(type) {
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		return int64(v)
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func (s *Service) enrich(ctx context.Context, chain string, addresses []string, rows []map[string]any, wsID string) {
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
		r["from_is_contract"] = addressTypes[addressTypeKey(chain, from)]
		r["to_is_contract"] = addressTypes[addressTypeKey(chain, to)]
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
				applyTokenMetadata(chain, r, ta, meta)
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
	// wsID 由调用方（handler）从已认证主体解析后传入；为空表示拿不到可信作用域，
	// 此时不附加案件/标签名，避免跨租户泄露。
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
			value := addressTypeKey(chain, valueString(row[field]))
			if (chain == "tron" && len(value) == 34 && strings.HasPrefix(value, "T")) ||
				(chain != "tron" && len(value) == 42 && strings.HasPrefix(value, "0x")) {
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
	// 冷请求的候选地址可能有上百个（一页 200 行 × from/to/address/counterparty 去重）。
	// 每请求最多解析 addressTypeMaxResolve 个，其余留给后续请求（不要把它们写成负缓存，
	// 否则会在 TTL 内一直按"非合约"呈现）。
	sort.Strings(missing)
	attempt := missing
	if len(attempt) > addressTypeMaxResolve {
		attempt = attempt[:addressTypeMaxResolve]
	}
	resolveCtx, cancel := context.WithTimeout(ctx, addressTypeResolveBudget)
	defer cancel()
	resolved, err := s.AddressTypes.ResolveAddressTypes(resolveCtx, chain, attempt)
	// 2026-09-17 修复（activity 端点稳定 8–10 s）：原实现在 err != nil 时**直接返回、一条缓存都不写**。
	// 而一页最多 200 个对手方地址逐个串行外呼几乎必然耗尽 8s 预算，于是每个请求都重新烧满 8s。
	// 现在：成功项写 24h 缓存；未解析项写短 TTL 负缓存（显示语义与"超时后缺失"一致：按非合约呈现），
	// 重复请求因此降到毫秒级。
	s.typeMu.Lock()
	defer s.typeMu.Unlock()
	for _, address := range attempt {
		key := strings.ToLower(chain) + ":" + address
		if isContract, ok := resolved[address]; ok {
			result[address] = isContract
			s.typeCache[key] = cachedAddressType{isContract: isContract, expiresAt: now.Add(addressTypeSuccessTTL)}
			continue
		}
		if err != nil || resolveCtx.Err() != nil {
			s.typeCache[key] = cachedAddressType{isContract: false, expiresAt: now.Add(addressTypeNegativeTTL)}
		}
	}
	return result
}

func addressTypeKey(chain, address string) string {
	return addressnorm.NormalizeKey(chain, address)
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
	index := valueUint64(row["log_index"])
	if index == 0 {
		index = valueUint64(row["internal_index"])
	}
	return pageCursor{
		Ts: ts, Hash: valueString(row["tx_hash"]), LogIndex: index,
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
