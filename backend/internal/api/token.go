package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/tokenmeta"
	"github.com/shopspring/decimal"
)

func (s *Server) handleTokenGet(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	m, err := s.deps.TokenMeta.Get(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract"))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if m == nil {
		writeJSON(w, 404, map[string]any{"error": "token not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"token": m})
}

func (s *Server) handleTokenLogo(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil || s.deps.Raw == nil {
		http.NotFound(w, r)
		return
	}
	m, err := s.deps.TokenMeta.Get(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract"))
	if err != nil || m == nil || m.LogoObjectKey == "" {
		http.NotFound(w, r)
		return
	}
	reader, size, err := s.deps.Raw.Open(r.Context(), m.LogoObjectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", m.LogoMimeType)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	_, _ = io.Copy(w, reader)
}

func (s *Server) handleTokenSearch(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 200, map[string]any{"tokens": []any{}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tokens, err := s.deps.TokenMeta.Search(r.Context(), r.URL.Query().Get("chain"), r.URL.Query().Get("q"), limit)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": tokens})
}

func (s *Server) handleTokenEnrichment(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	summary, err := s.deps.TokenMeta.Summary(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, summary)
}

func (s *Server) handleTokenResolve(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	contract := chi.URLParam(r, "contract")
	if !tokenmeta.IsValidContractAddress(contract) {
		writeJSON(w, 400, map[string]any{"error": "invalid token contract address"})
		return
	}
	if err := s.deps.TokenMeta.Enqueue(r.Context(), chi.URLParam(r, "chain"), contract); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]any{"status": "pending"})
}

func (s *Server) handleTokenOverride(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	var req struct {
		Name      string `json:"name"`
		Symbol    string `json:"symbol"`
		Decimals  *int   `json:"decimals"`
		TokenType string `json:"token_type"`
		Reason    string `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	err := s.deps.TokenMeta.Override(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract"), tokenmeta.Metadata{Name: req.Name, Symbol: req.Symbol, Decimals: req.Decimals, TokenType: req.TokenType}, req.Reason)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	m, _ := s.deps.TokenMeta.Get(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract"))
	writeJSON(w, 200, map[string]any{"token": m})
}

/*
 * handleTokenLogoSet 给单个代币指派图标（人工/外部来源），同步本地化后立即可见。
 *
 * 两种请求形态：
 *   - JSON  {"url": "https://…/logo.png"}：走与 worker 相同的 URL 校验（https + 公网 IP）；
 *   - 原始图片字节（Content-Type: image/png|jpeg|webp，≤ TOKEN_LOGO_MAX_BYTES）：直接落对象存储。
 * 需要 admin:dataset:manage（与 override 同一权限）。
 */
func (s *Server) handleTokenLogoSet(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	chain := chi.URLParam(r, "chain")
	contract := chi.URLParam(r, "contract")
	if !tokenmeta.IsValidContractAddress(contract) {
		writeJSON(w, 400, map[string]any{"error": "invalid token contract address"})
		return
	}
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "image/") {
		max := s.cfg.TokenMetadata.LogoMaxBytes
		if max <= 0 {
			max = 2 << 20
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, max+1))
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "read body failed"})
			return
		}
		if int64(len(body)) > max {
			writeJSON(w, 400, map[string]any{"error": "logo too large"})
			return
		}
		m, err := s.deps.TokenMeta.SetLogoBytes(r.Context(), chain, contract, body)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"token": m, "logo_url": "/api/v1/tokens/" + chain + "/" + contract + "/logo"})
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	m, err := s.deps.TokenMeta.SetLogoFromURL(r.Context(), chain, contract, req.URL)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"token": m, "logo_url": "/api/v1/tokens/" + chain + "/" + contract + "/logo"})
}

/* handleTokenLogoClear 清除图标（logo_url + 自托管对象键），用于纠正错误图标。 */
func (s *Server) handleTokenLogoClear(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	if err := s.deps.TokenMeta.ClearLogo(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract")); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "cleared"})
}

func (s *Server) handleTokenBackfill(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenBackfill == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata backfill disabled"})
		return
	}
	var req struct {
		Chain string `json:"chain"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	id, err := s.deps.TokenBackfill.CreateJob(r.Context(), strings.TrimSpace(req.Chain))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]any{"job_id": id, "status": "pending"})
}

func (s *Server) handleTokenBackfillList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.Pool.Query(r.Context(), `SELECT id,COALESCE(chain,''),status,cursor_json,scanned_tokens,completed_tokens,partial_tokens,failed_tokens,raw_objects,raw_observations,raw_failed_objects,raw_failed_parts,started_at,updated_at,completed_at,COALESCE(last_error,'') FROM token_metadata_backfill_jobs ORDER BY updated_at DESC LIMIT 50`)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, chain, status, errorText string
		var cursor json.RawMessage
		var scanned, complete, partial, failed, rawObjects, rawObservations, rawFailedObjects, rawFailedParts int64
		var started, completed sql.NullTime
		var updated any
		if err := rows.Scan(&id, &chain, &status, &cursor, &scanned, &complete, &partial, &failed, &rawObjects, &rawObservations, &rawFailedObjects, &rawFailedParts, &started, &updated, &completed, &errorText); err != nil {
			continue
		}
		out = append(out, map[string]any{"id": id, "chain": chain, "status": status, "cursor": cursor, "scanned_tokens": scanned, "completed_tokens": complete, "partial_tokens": partial, "failed_tokens": failed, "raw_objects": rawObjects, "raw_observations": rawObservations, "raw_failed_objects": rawFailedObjects, "raw_failed_parts": rawFailedParts, "started_at": nullableTime(started), "updated_at": updated, "completed_at": nullableTime(completed), "last_error": errorText})
	}
	writeJSON(w, 200, map[string]any{"jobs": out})
}

func nullableTime(value sql.NullTime) any {
	if value.Valid {
		return value.Time
	}
	return nil
}

// amountKey 是 formatted_amount 记忆化表的键：(合约, raw 金额)。
type amountKey struct {
	contract string
	raw      string
}

// amountCacheLimit 是记忆化表的硬上限，防止高基数据把内存拉爆。
const amountCacheLimit = 1 << 19

// tokenMetaPlan 是一个代币合约在当前链上的导出派生值。
//
// divisor 预先是**除数**而不是每行现算的结果：raw 有 76 位精度，逐行
// new(big.Int).SetString + decimal.NewFromBigInt 是导出里最贵的逐行操作。
type tokenMetaPlan struct {
	name, symbol, tokenType, status, source, confidence string
	decimals                                            int
	decimalsKnown                                       bool
	divisor                                             *big.Int
}

// attachTokenMetadata 给导出行补 token_contract/raw_value/formatted_amount 等派生列。
//
// 实测（真实实例，20 万行 token_transfers / 203 个合约）：整函数 2.31 s，其中
// TokenMeta.GetMany 只占 4 ms——剩下的全在逐行计算，而不是查元数据。优化点：
//   - contract 归一化原本每行做两次（token_address 为空时再试 contract_address），
//     现在每行只归一化一次并把结果缓存到行序切片；
//   - 归一化不再走 fmt.Sprint(v)（每次分配 + 反射），string 直接返回；
//   - formatted_amount 的除数按合约预先构造，不再逐行解析 76 位 raw 并重建 big.Int。
func (s *Server) attachTokenMetadata(ctx context.Context, chain string, rows []map[string]any) {
	if s.deps.TokenMeta == nil || len(rows) == 0 {
		return
	}
	normalizeContract := func(value any) string {
		text, ok := value.(string)
		if !ok {
			if value == nil {
				return ""
			}
			text = fmt.Sprint(value)
		}
		if chain == "tron" {
			return strings.TrimSpace(text)
		}
		return strings.ToLower(strings.TrimSpace(text))
	}

	contracts := make([]string, 0, 64)
	rowContracts := make([]string, len(rows))
	rowRawText := make([]string, len(rows))
	seen := make(map[string]struct{}, 64)
	for i, row := range rows {
		contract := normalizeContract(row["token_address"])
		if contract == "" {
			contract = normalizeContract(row["contract_address"])
		}
		rowContracts[i] = contract
		rowRawText[i] = asString(row["amount_raw"])
		if contract == "" {
			continue
		}
		if _, ok := seen[contract]; !ok {
			seen[contract] = struct{}{}
			contracts = append(contracts, contract)
		}
	}
	metas, err := s.deps.TokenMeta.GetMany(ctx, chain, contracts)
	if err != nil {
		return
	}

	plans := make(map[string]tokenMetaPlan, len(metas))
	for contract, m := range metas {
		plan := tokenMetaPlan{
			name: m.Name, symbol: m.Symbol, tokenType: m.TokenType,
			status: m.MetadataStatus, source: m.MetadataSource, confidence: m.Confidence,
		}
		if m.Decimals != nil {
			plan.decimals = *m.Decimals
			plan.decimalsKnown = true
		}
		plans[contract] = plan
	}

	// formatted_amount 的记忆化表：键是 (合约, raw 值)。实测 formatTokenAmount
	// 单次 4.1 µs（20 万行 × 1 次 = 0.9 s），瓶颈是 76 位 big.Int 除法；而同一批
	// 导出里 (合约, 金额) 组合远少于行数，记忆化把这一列从 O(行数) 次除法降到
	// O(不同金额数) 次。表有硬上限，防止高基数据把内存拉爆。
	amountCache := make(map[amountKey]string, 1024)

	for i, row := range rows {
		contract := rowContracts[i]
		if contract == "" {
			continue
		}
		applyTokenMeta(row, contract, plans, amountCache, amountCacheLimit)
	}
}

// applyTokenMeta 把单个合约的派生值套用到一行上。抽出来是为了让流式导出
// （逐块写 MinIO）复用同一段逻辑——批量与流式两条路径必须产出相同的行。
func applyTokenMeta(row map[string]any, contract string, plans map[string]tokenMetaPlan,
	amountCache map[amountKey]string, amountCacheLimit int) {
	row["token_contract"] = contract
	row["raw_value"] = row["amount_raw"]

	plan, ok := plans[contract]
	if !ok {
		row["formatted_amount"] = nil
		return
	}
	if plan.name != "" {
		row["token_name"] = plan.name
	}
	if plan.symbol != "" {
		row["token_symbol"] = plan.symbol
	}
	// BUG-001 修复：Decimals 是指针，直接存入 row 会让 CSV/XLSX 以 fmt 输出
	// 非确定性指针地址（如 0x3dffac709738）。必须解引用后再写入；未知时明确置空，
	// 不伪造 18。formatted_amount 分支随后处理 nil。
	if plan.decimalsKnown {
		row["token_decimals"] = plan.decimals
	} else {
		row["token_decimals"] = nil
	}
	row["token_type"] = plan.tokenType
	row["token_metadata_status"] = plan.status
	row["token_metadata_source"] = plan.source
	row["token_metadata_confidence"] = plan.confidence

	rawText := asString(row["amount_raw"])
	cacheKey := amountKey{contract: contract, raw: rawText}
	formatted, cached := amountCache[cacheKey]
	if !cached {
		value, ok := formatTokenAmount(rawText, plan).(string)
		if !ok {
			// 精度未知/无法解析：保持 nil，不缓存（避免与"空值"混淆）。
			row["formatted_amount"] = nil
			return
		}
		formatted = value
		if len(amountCache) < amountCacheLimit {
			amountCache[cacheKey] = formatted
		}
	}
	row["formatted_amount"] = formatted
}

// asString 是不分配的类型化取值：string 直接用，其余回退 fmt.Sprint。
// 导出行的值是 scanRowMap 写入的（字符串/整数/时间/Decimal），string 是绝大多数。
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// formatTokenAmount 按合约精度把 raw 金额写成人类可读字符串；
// 无法解析或精度未知时返回 nil（与历史行为一致：不伪造 18 位精度）。
//
// 必须用 decimal.NewFromBigInt(integer, -decimals) 这种**无损**表示，
// 不能用 Div：Div 会按 DivisionPrecision（默认 16 位）四舍五入，
// 把 1.764025991317028396 变成 1.7640259913170284（实测由 token_format_test 抓到）。
func formatTokenAmount(rawValue any, plan tokenMetaPlan) any {
	if !plan.decimalsKnown {
		return nil
	}
	raw, ok := rawValue.(string)
	if !ok {
		if rawValue == nil {
			return nil
		}
		raw = fmt.Sprint(rawValue)
	}
	integer, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	if !ok {
		return nil
	}
	return decimal.NewFromBigInt(integer, -int32(plan.decimals)).String()
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
