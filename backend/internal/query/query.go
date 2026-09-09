// Package query：统一数据面查询（业务模块只查 ClickHouse，不查 Raw JSON）。
package query

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

type Service struct {
	CH *clickhouse.Conn
}

func New(ch *clickhouse.Conn) *Service { return &Service{CH: ch} }

// 各表结构体（列顺序与建表一致；clickhouse-go ScanStruct 使用 ch tag）
type txRow struct {
	Chain            string    `ch:"chain" json:"chain"`
	BlockNumber      uint64    `ch:"block_number" json:"block_number"`
	BlockHash        string    `ch:"block_hash" json:"block_hash"`
	TxHash           string    `ch:"tx_hash" json:"tx_hash"`
	Timestamp        time.Time `ch:"timestamp" json:"timestamp"`
	FromAddress      string    `ch:"from_address" json:"from_address"`
	ToAddress        string    `ch:"to_address" json:"to_address"`
	ValueRaw         string    `ch:"value_raw" json:"value_raw"`
	Nonce            uint64    `ch:"nonce" json:"nonce"`
	Gas              uint64    `ch:"gas" json:"gas"`
	GasUsed          uint64    `ch:"gas_used" json:"gas_used"`
	GasPrice         string    `ch:"gas_price" json:"gas_price"`
	TxType           string    `ch:"tx_type" json:"tx_type"`
	Status           uint8     `ch:"status" json:"status"`
	Input            string    `ch:"input" json:"input"`
	MethodID         string    `ch:"method_id" json:"method_id"`
	SourceProvider   string    `ch:"source_provider" json:"source_provider"`
	SourceIngestedAt time.Time `ch:"source_ingested_at" json:"source_ingested_at"`

	// V1.3 Method 解析结果（§7）。SELECT * 依赖结构体完整覆盖全部列。
	MethodName       string     `ch:"method_name" json:"method_name"`
	MethodSignature  string     `ch:"method_signature" json:"method_signature"`
	MethodSource     string     `ch:"method_source" json:"method_source"`
	MethodVerified   uint8      `ch:"method_verified" json:"method_verified"`
	MethodConfidence string     `ch:"method_confidence" json:"method_confidence"`
	MethodType       string     `ch:"method_type" json:"method_type"`
	MethodResolvedAt *time.Time `ch:"method_resolved_at" json:"method_resolved_at"`
	NativeSymbol     string     `ch:"native_symbol" json:"native_symbol"`
	Confirmation     string     `ch:"confirmation" json:"confirmation"`
	RawType          string     `ch:"raw_type" json:"raw_type"`
	RawStatus        string     `ch:"raw_status" json:"raw_status"`
}

type internalTransferRow struct {
	Chain          string    `ch:"chain" json:"chain"`
	TxHash         string    `ch:"tx_hash" json:"tx_hash"`
	CallIndex      uint32    `ch:"call_index" json:"internal_index"`
	Depth          uint16    `ch:"depth" json:"depth"`
	TracePath      string    `ch:"trace_path" json:"trace_path"`
	CallType       string    `ch:"call_type" json:"call_type"`
	ActivityType   string    `ch:"activity_type" json:"activity_type"`
	FromAddress    string    `ch:"from_address" json:"from_address"`
	ToAddress      string    `ch:"to_address" json:"to_address"`
	ValueRaw       string    `ch:"value_raw" json:"value_raw"`
	MethodID       string    `ch:"method_id" json:"method_id"`
	Gas            uint64    `ch:"gas" json:"gas"`
	GasUsed        uint64    `ch:"gas_used" json:"gas_used"`
	Success        uint8     `ch:"success" json:"success"`
	Error          string    `ch:"error" json:"error"`
	TracedAt       time.Time `ch:"traced_at" json:"traced_at"`
	BlockNumber    uint64    `ch:"block_number" json:"block_number"`
	Timestamp      time.Time `ch:"timestamp" json:"timestamp"`
	SourceProvider string    `ch:"source_provider" json:"source_provider"`
}

type tokenTransferRow struct {
	Chain          string          `ch:"chain" json:"chain"`
	BlockNumber    uint64          `ch:"block_number" json:"block_number"`
	Timestamp      time.Time       `ch:"timestamp" json:"timestamp"`
	TxHash         string          `ch:"tx_hash" json:"tx_hash"`
	LogIndex       uint32          `ch:"log_index" json:"log_index"`
	TokenAddress   string          `ch:"token_address" json:"token_address"`
	TokenSymbol    string          `ch:"token_symbol" json:"token_symbol"`
	TokenDecimals  uint16          `ch:"token_decimals" json:"token_decimals"`
	FromAddress    string          `ch:"from_address" json:"from_address"`
	ToAddress      string          `ch:"to_address" json:"to_address"`
	AmountRaw      string          `ch:"amount_raw" json:"amount_raw"`
	Amount         decimal.Decimal `ch:"amount" json:"amount"`
	SourceProvider string          `ch:"source_provider" json:"source_provider"`
	SourceIngested time.Time       `ch:"source_ingested_at" json:"source_ingested_at"`
}

type nftTransferRow struct {
	Chain          string    `ch:"chain" json:"chain"`
	BlockNumber    uint64    `ch:"block_number" json:"block_number"`
	Timestamp      time.Time `ch:"timestamp" json:"timestamp"`
	TxHash         string    `ch:"tx_hash" json:"tx_hash"`
	LogIndex       uint32    `ch:"log_index" json:"log_index"`
	Contract       string    `ch:"contract_address" json:"contract_address"`
	TokenID        string    `ch:"token_id" json:"token_id"`
	FromAddress    string    `ch:"from_address" json:"from_address"`
	ToAddress      string    `ch:"to_address" json:"to_address"`
	TokenName      string    `ch:"token_name" json:"token_name"`
	TokenSymbol    string    `ch:"token_symbol" json:"token_symbol"`
	AmountRaw      string    `ch:"amount_raw" json:"amount_raw"`
	SourceProvider string    `ch:"source_provider" json:"source_provider"`
	SourceIngested time.Time `ch:"source_ingested_at" json:"source_ingested_at"`
}

type logRow struct {
	Chain          string    `ch:"chain" json:"chain"`
	BlockNumber    uint64    `ch:"block_number" json:"block_number"`
	BlockHash      string    `ch:"block_hash" json:"block_hash"`
	TxHash         string    `ch:"tx_hash" json:"tx_hash"`
	LogIndex       uint32    `ch:"log_index" json:"log_index"`
	Address        string    `ch:"address" json:"address"`
	Topics         []string  `ch:"topics" json:"topics"`
	Data           string    `ch:"data" json:"data"`
	Removed        uint8     `ch:"removed" json:"removed"`
	Timestamp      time.Time `ch:"timestamp" json:"timestamp"`
	SourceProvider string    `ch:"source_provider" json:"source_provider"`
	SourceIngested time.Time `ch:"source_ingested_at" json:"source_ingested_at"`

	// V1.3 事件解析（§29）
	EventName      string `ch:"event_name" json:"event_name"`
	EventSignature string `ch:"event_signature" json:"event_signature"`
	EventSource    string `ch:"event_source" json:"event_source"`
	EventVerified  uint8  `ch:"event_verified" json:"event_verified"`
}

type balanceRow struct {
	Chain          string          `ch:"chain" json:"chain"`
	Address        string          `ch:"address" json:"address"`
	TokenSymbol    string          `ch:"token_symbol" json:"token_symbol"`
	TokenName      string          `ch:"token_name" json:"token_name"`
	TokenAddress   string          `ch:"token_address" json:"token_address"`
	TokenType      string          `ch:"token_type" json:"token_type"`
	TokenDecimals  uint16          `ch:"token_decimals" json:"token_decimals"`
	BalanceRaw     string          `ch:"balance_raw" json:"balance_raw"`
	Balance        decimal.Decimal `ch:"balance" json:"balance"`
	BalanceUSD     string          `ch:"balance_usd" json:"balance_usd"`
	TokenPrice     string          `ch:"token_price" json:"token_price"`
	SnapshotBlock  uint64          `ch:"snapshot_block" json:"snapshot_block"`
	Timestamp      time.Time       `ch:"timestamp" json:"timestamp"`
	SourceProvider string          `ch:"source_provider" json:"source_provider"`
	SourceIngested time.Time       `ch:"source_ingested_at" json:"source_ingested_at"`
}

type activityRow struct {
	Chain          string          `ch:"chain" json:"chain"`
	Address        string          `ch:"address" json:"address"`
	Counterparty   string          `ch:"counterparty" json:"counterparty"`
	Direction      string          `ch:"direction" json:"direction"`
	ActivityType   string          `ch:"activity_type" json:"activity_type"`
	Timestamp      time.Time       `ch:"timestamp" json:"timestamp"`
	BlockNumber    uint64          `ch:"block_number" json:"block_number"`
	TxHash         string          `ch:"tx_hash" json:"tx_hash"`
	LogIndex       uint32          `ch:"log_index" json:"log_index"`
	TokenAddress   string          `ch:"token_address" json:"token_address"`
	TokenSymbol    string          `ch:"token_symbol" json:"token_symbol"`
	TokenDecimals  uint16          `ch:"token_decimals" json:"token_decimals"`
	AmountRaw      string          `ch:"amount_raw" json:"amount_raw"`
	Amount         decimal.Decimal `ch:"amount" json:"amount"`
	AmountUSD      string          `ch:"amount_usd" json:"amount_usd"`
	Dataset        string          `ch:"dataset" json:"dataset"`
	SourceProvider string          `ch:"source_provider" json:"source_provider"`
	SourceIngested time.Time       `ch:"source_ingested_at" json:"source_ingested_at"`
}

// tableName dataset 逻辑名 → ClickHouse 表名。
func tableName(dataset string) string {
	if dataset == "balances" {
		return "address_balances"
	}
	if dataset == "internal_transactions" {
		return "internal_calls"
	}
	return dataset
}

func (s *Service) Rows(ctx context.Context, dataset, chain, subject string, limit, offset int) ([]map[string]any, error) {
	var where string
	var args []any
	switch dataset {
	case "transactions", "token_transfers", "nft_transfers", "internal_transactions":
		where = "chain=? AND (from_address=? OR to_address=?)"
		args = []any{chain, subject, subject}
	case "logs":
		where = "chain=? AND address=?"
		args = []any{chain, subject}
	case "balances":
		where = "chain=? AND address=?"
		args = []any{chain, subject}
	default:
		return nil, fmt.Errorf("unknown dataset %s", dataset)
	}
	args = append(args, limit, offset)
	q := "SELECT * FROM blockchain." + tableName(dataset) + " FINAL WHERE " + where + " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	return s.queryRows(ctx, dataset, q, args...)
}

// Explorer 统一浏览查询：地址集合 + 附加过滤 + keyset cursor + 多地址 QUALIFY 去重（§29/§58）。
type ExplorerCursor struct {
	Timestamp       time.Time
	TxHash          string
	LogIndex        uint64
	TokenAddress    string
	ContractAddress string
	TokenID         string
	ActivityType    string
	Counterparty    string
}

func (s *Service) Explorer(ctx context.Context, dataset, chain string, addresses []string, filterSQL string, filterArgs []any, cursor *ExplorerCursor, limit int) ([]map[string]any, error) {
	if len(addresses) == 0 {
		return nil, fmt.Errorf("addresses required")
	}
	table := tableName(dataset)
	if dataset == "activity" {
		table = "address_activity"
	}
	args := []any{chain}
	in := "(?)" // clickhouse-go expands a slice bound to one IN placeholder.
	var base string
	switch dataset {
	case "transactions", "token_transfers", "nft_transfers", "internal_transactions":
		base = "chain=? AND (from_address IN " + in + " OR to_address IN " + in + ")"
		args = append(args, addresses, addresses)
	case "logs", "activity":
		base = "chain=? AND address IN " + in
		args = append(args, addresses)
	default:
		return nil, fmt.Errorf("unknown dataset %s", dataset)
	}
	where := base
	if filterSQL != "" {
		where += " AND " + filterSQL
		args = append(args, filterArgs...)
	}
	if cursor != nil {
		condition, values := explorerCursorCondition(dataset, cursor)
		where += " AND " + condition
		args = append(args, values...)
	}
	q := "SELECT * FROM blockchain." + table + " FINAL WHERE " + where
	if len(addresses) > 1 && dataset != "activity" {
		// 多地址：A→B 交易只显示一次（QUALIFY 窗口去重，不加额外列）
		q += " QUALIFY row_number() OVER (PARTITION BY " + explorerPartition(dataset) + " ORDER BY timestamp DESC) = 1"
	}
	args = append(args, limit+1)
	q += " ORDER BY " + explorerOrder(dataset) + " LIMIT ?"
	rows, err := s.queryRows(ctx, dataset, q, args...)
	if err != nil {
		return nil, err
	}
	if dataset == "activity" {
		if err := s.hydrateActivityAmounts(ctx, chain, rows); err != nil {
			return nil, err
		}
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func explorerOrder(dataset string) string {
	switch dataset {
	case "token_transfers":
		return "timestamp DESC, tx_hash DESC, log_index DESC, token_address DESC"
	case "nft_transfers":
		return "timestamp DESC, tx_hash DESC, log_index DESC, contract_address DESC, token_id DESC"
	case "logs":
		return "timestamp DESC, tx_hash DESC, log_index DESC"
	case "internal_transactions":
		return "timestamp DESC, tx_hash DESC, call_index DESC"
	case "activity":
		return "timestamp DESC, tx_hash DESC, activity_type DESC, counterparty DESC, token_address DESC"
	default:
		return "timestamp DESC, tx_hash DESC"
	}
}

func explorerCursorCondition(dataset string, cursor *ExplorerCursor) (string, []any) {
	ts := cursor.Timestamp.UTC()
	switch dataset {
	case "token_transfers":
		return "(timestamp, tx_hash, log_index, token_address) < (?, ?, ?, ?)",
			[]any{ts, cursor.TxHash, cursor.LogIndex, cursor.TokenAddress}
	case "nft_transfers":
		return "(timestamp, tx_hash, log_index, contract_address, token_id) < (?, ?, ?, ?, ?)",
			[]any{ts, cursor.TxHash, cursor.LogIndex, cursor.ContractAddress, cursor.TokenID}
	case "logs":
		return "(timestamp, tx_hash, log_index) < (?, ?, ?)", []any{ts, cursor.TxHash, cursor.LogIndex}
	case "internal_transactions":
		return "(timestamp, tx_hash, call_index) < (?, ?, ?)", []any{ts, cursor.TxHash, cursor.LogIndex}
	case "activity":
		return "(timestamp, tx_hash, activity_type, counterparty, token_address) < (?, ?, ?, ?, ?)",
			[]any{ts, cursor.TxHash, cursor.ActivityType, cursor.Counterparty, cursor.TokenAddress}
	default:
		return "(timestamp, tx_hash) < (?, ?)", []any{ts, cursor.TxHash}
	}
}

func explorerPartition(dataset string) string {
	switch dataset {
	case "transactions":
		return "tx_hash"
	case "internal_transactions":
		return "tx_hash, call_index"
	case "logs":
		return "tx_hash, log_index"
	case "nft_transfers":
		return "tx_hash, log_index, contract_address, token_id"
	default:
		return "tx_hash, log_index, token_address"
	}
}

// Activity 地址活动（§87：资金分析/智能调查统一入口）。
func (s *Service) Activity(ctx context.Context, chain, address string, limit, offset int) ([]map[string]any, error) {
	if chain != "tron" {
		address = strings.ToLower(address)
	}
	q := "SELECT * FROM blockchain.address_activity FINAL WHERE chain=? AND address=? ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	rows, err := s.queryRows(ctx, "activity", q, chain, address, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateActivityAmounts(ctx, chain, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// RowsForExport 导出查询：按链过滤，可选按地址过滤，流式上限。
func (s *Service) RowsForExport(ctx context.Context, dataset, chain, subject string, limit, offset int) ([]map[string]any, error) {
	return s.RowsForExportRange(ctx, dataset, chain, subject, limit, offset, nil, nil, nil, nil)
}

// RowsForExportRange keeps job exports scoped to both their subjects and the
// requested block/time range. Nil bounds preserve the legacy address export.
func (s *Service) RowsForExportRange(ctx context.Context, dataset, chain, subject string, limit, offset int,
	fromBlock, toBlock *int64, fromTime, toTime *time.Time) ([]map[string]any, error) {
	switch dataset {
	case "transactions", "token_transfers", "nft_transfers", "logs", "balances", "internal_transactions":
	default:
		return nil, fmt.Errorf("unknown dataset %s", dataset)
	}
	if chain != "tron" {
		subject = strings.ToLower(subject)
	}
	var where string
	var args []any
	if subject != "" {
		switch dataset {
		case "transactions", "token_transfers", "nft_transfers", "internal_transactions":
			where = "chain=? AND (from_address=? OR to_address=?)"
			args = []any{chain, subject, subject}
		case "logs", "balances":
			where = "chain=? AND address=?"
			args = []any{chain, subject}
		}
	} else {
		where = "chain=?"
		args = []any{chain}
	}
	if fromBlock != nil {
		where += " AND block_number>=?"
		args = append(args, *fromBlock)
	}
	if toBlock != nil {
		where += " AND block_number<=?"
		args = append(args, *toBlock)
	}
	if fromTime != nil {
		where += " AND timestamp>=?"
		args = append(args, *fromTime)
	}
	if toTime != nil {
		where += " AND timestamp<=?"
		args = append(args, *toTime)
	}
	args = append(args, limit, offset)
	q := "SELECT * FROM blockchain." + tableName(dataset) + " FINAL WHERE " + where + " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	return s.queryRows(ctx, dataset, q, args...)
}

// RowsForExportRangeMulti scans ClickHouse once for all requested addresses.
// Job ZIP export then fans the result into per-address files in memory, avoiding
// one full FINAL table scan per address.
func (s *Service) RowsForExportRangeMulti(ctx context.Context, dataset, chain string, subjects []string, limit int,
	fromBlock, toBlock *int64, fromTime, toTime *time.Time) ([]map[string]any, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	if chain != "tron" {
		for i := range subjects {
			subjects[i] = strings.ToLower(subjects[i])
		}
	}
	var where string
	switch dataset {
	case "transactions", "token_transfers", "nft_transfers", "internal_transactions":
		where = "chain=? AND (from_address IN (?) OR to_address IN (?))"
	case "logs", "balances":
		where = "chain=? AND address IN (?)"
	default:
		return nil, fmt.Errorf("unknown dataset %s", dataset)
	}
	args := []any{chain, subjects}
	if strings.Contains(where, "to_address") {
		args = append(args, subjects)
	}
	if fromBlock != nil {
		where += " AND block_number>=?"
		args = append(args, *fromBlock)
	}
	if toBlock != nil {
		where += " AND block_number<=?"
		args = append(args, *toBlock)
	}
	if fromTime != nil {
		where += " AND timestamp>=?"
		args = append(args, *fromTime)
	}
	if toTime != nil {
		where += " AND timestamp<=?"
		args = append(args, *toTime)
	}
	args = append(args, limit)
	q := "SELECT * FROM blockchain." + tableName(dataset) + " FINAL WHERE " + where + " ORDER BY timestamp DESC LIMIT ?"
	return s.queryRows(ctx, dataset, q, args...)
}

func (s *Service) queryRows(ctx context.Context, dataset, q string, args ...any) ([]map[string]any, error) {
	rows, err := s.CH.DB().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		item, err := scanOne(dataset, rows)
		if err != nil {
			return nil, fmt.Errorf("clickhouse [ScanRow]: %w", err)
		}
		normalizeStoredAmount(dataset, item)
		out = append(out, item)
	}
	return out, rows.Err()
}

func normalizeStoredAmount(dataset string, item map[string]any) {
	var rawKey, amountKey, decimalsKey string
	switch dataset {
	case "token_transfers", "activity":
		rawKey, amountKey, decimalsKey = "amount_raw", "amount", "token_decimals"
	case "balances":
		rawKey, amountKey, decimalsKey = "balance_raw", "balance", "token_decimals"
	default:
		return
	}
	raw := strings.TrimSpace(fmt.Sprint(item[rawKey]))
	if raw == "" {
		return
	}
	decimals, err := strconv.ParseUint(fmt.Sprint(item[decimalsKey]), 10, 16)
	if err != nil || decimals > 76 {
		return
	}
	integer, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return
	}
	item[amountKey] = decimal.NewFromBigInt(integer, -int32(decimals)).String()
}

// hydrateActivityAmounts repairs legacy activity rows from authoritative token
// transfers. New rows already carry amount_raw/token_decimals and skip this path.
func (s *Service) hydrateActivityAmounts(ctx context.Context, chain string, rows []map[string]any) error {
	hashSet := map[string]bool{}
	for _, row := range rows {
		if fmt.Sprint(row["activity_type"]) == "transfer" && strings.TrimSpace(fmt.Sprint(row["amount_raw"])) == "" {
			hashSet[fmt.Sprint(row["tx_hash"])] = true
		}
	}
	if len(hashSet) == 0 {
		return nil
	}
	hashes := make([]string, 0, len(hashSet))
	for hash := range hashSet {
		hashes = append(hashes, hash)
	}
	candidates, err := s.queryRows(ctx, "token_transfers",
		"SELECT * FROM blockchain.token_transfers FINAL WHERE chain=? AND tx_hash IN (?)", chain, hashes)
	if err != nil {
		return err
	}
	byHashToken := make(map[string][]map[string]any, len(candidates))
	for _, candidate := range candidates {
		key := fmt.Sprint(candidate["tx_hash"]) + "|" + strings.ToLower(fmt.Sprint(candidate["token_address"]))
		byHashToken[key] = append(byHashToken[key], candidate)
	}
	for _, row := range rows {
		if fmt.Sprint(row["activity_type"]) != "transfer" || strings.TrimSpace(fmt.Sprint(row["amount_raw"])) != "" {
			continue
		}
		key := fmt.Sprint(row["tx_hash"]) + "|" + strings.ToLower(fmt.Sprint(row["token_address"]))
		for _, candidate := range byHashToken[key] {
			if !activityCandidateMatches(row, candidate) {
				continue
			}
			for _, field := range []string{"amount", "amount_raw", "token_decimals", "token_symbol", "log_index"} {
				row[field] = candidate[field]
			}
			break
		}
	}
	return nil
}

func activityCandidateMatches(activity, transfer map[string]any) bool {
	address := strings.ToLower(fmt.Sprint(activity["address"]))
	counterparty := strings.ToLower(fmt.Sprint(activity["counterparty"]))
	from := strings.ToLower(fmt.Sprint(transfer["from_address"]))
	to := strings.ToLower(fmt.Sprint(transfer["to_address"]))
	switch fmt.Sprint(activity["direction"]) {
	case "out":
		return from == address && to == counterparty
	case "self":
		return from == address && to == address
	default:
		return to == address && from == counterparty
	}
}

// scanOne 按 dataset 用对应结构体 ScanStruct，再转 map（json 往返，decimal → 字符串）。
func scanOne(dataset string, rows interface{ ScanStruct(dest any) error }) (map[string]any, error) {
	var v any
	switch dataset {
	case "transactions":
		v = &txRow{}
	case "token_transfers":
		v = &tokenTransferRow{}
	case "nft_transfers":
		v = &nftTransferRow{}
	case "logs":
		v = &logRow{}
	case "balances":
		v = &balanceRow{}
	case "activity":
		v = &activityRow{}
	case "internal_transactions":
		v = &internalTransferRow{}
	default:
		return nil, fmt.Errorf("unknown dataset %s", dataset)
	}
	if err := rows.ScanStruct(v); err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// UseNumber：整数（块号/日志索引等）保持精确，避免科学计数法与精度丢失
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if dataset == "internal_transactions" {
		success := fmt.Sprint(m["success"])
		m["rejected"] = success == "0" || strings.EqualFold(fmt.Sprint(m["error"]), "rejected")
		if strings.EqualFold(fmt.Sprint(m["chain"]), "tron") {
			m["asset_type"] = "TRX"
		}
	}
	return m, nil
}
