// Package query：统一数据面查询（业务模块只查 ClickHouse，不查 Raw JSON）。
package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

type Service struct {
	CH  *clickhouse.Conn
	Log *slog.Logger
}

func New(ch *clickhouse.Conn) *Service { return &Service{CH: ch} }

// exportScanSlots serialises export scans against the shared ClickHouse
// instance. The export shape is the only query in this service that can hold
// gigabytes on the server (measured 7.19 GiB peak at the 2,000,000 row cap),
// so overlapping scans would push the 19.28 GiB server past its ceiling again
// and the OvercommitTracker would abort them arbitrarily. One slot keeps the
// whole export footprint inside the single-query cap in exportScanSettings.
var exportScanSlots = make(chan struct{}, 1)

// acquireExportScanSlot waits for the export scan slot, or gives up as soon as
// the request itself is cancelled so a queued export never outlives its caller.
func acquireExportScanSlot(ctx context.Context) (func(), error) {
	select {
	case exportScanSlots <- struct{}{}:
		return func() { <-exportScanSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// speculativeScanCtxKey 标记"这次导出是投机的"（预生成）。
type speculativeScanCtxKey struct{}

// WithSpeculativeScan 标记该 context 下的导出扫描是投机任务。
//
// 预生成（用户还没点导出）**绝不能排队占住扫描槽**：槽容量是 1，一个 20 秒的预生成
// 会把真正在等结果的用户挡在后面。带这个标记的扫描抢不到槽就直接放弃，投机任务失败
// 无所谓——用户点导出时照常走正常路径。
func WithSpeculativeScan(ctx context.Context) context.Context {
	return context.WithValue(ctx, speculativeScanCtxKey{}, true)
}

// isSpeculativeScan 报告该 context 是否被标记为投机扫描。
func isSpeculativeScan(ctx context.Context) bool {
	v, _ := ctx.Value(speculativeScanCtxKey{}).(bool)
	return v
}

// acquireExportScanSlotFor 按 context 决定"排队等槽"还是"抢不到就让路"。
func acquireExportScanSlotFor(ctx context.Context) (func(), error) {
	if !isSpeculativeScan(ctx) {
		return acquireExportScanSlot(ctx)
	}
	select {
	case exportScanSlots <- struct{}{}:
		return func() { <-exportScanSlots }, nil
	default:
		return nil, ErrSpeculativeScanBusy
	}
}

// ErrSpeculativeScanBusy 表示投机扫描（预生成）没抢到扫描槽，主动让路。
var ErrSpeculativeScanBusy = errors.New("speculative export scan skipped: export scan slot is busy")

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

	// 0010 迁移新增的 Solana 交易证据列。SELECT * 会把它们带出来，
	// 结构体缺一列 ScanStruct 就整条查询失败（表现为 getTransactions 500）。
	FeeRaw           string   `ch:"fee_raw" json:"fee_raw"`
	Signers          []string `ch:"signers" json:"signers"`
	AccountKeys      []string `ch:"account_keys" json:"account_keys"`
	ProgramIDs       []string `ch:"program_ids" json:"program_ids"`
	SourceAPI        string   `ch:"source_api" json:"source_api"`
	SourceMethod     string   `ch:"source_method" json:"source_method"`
	SourceRequestID  string   `ch:"source_request_id" json:"source_request_id"`
	ChainPayloadJSON string   `ch:"chain_payload_json" json:"chain_payload_json"`
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
	// 0008 迁移新增的转账证据列。SELECT *（hydrateActivityAmounts 的修复查询）要求结构体
	// 覆盖全部列，缺一列就 ScanStruct 失败并让整条活动读 500。
	GrossAmountRaw   string `ch:"gross_amount_raw" json:"gross_amount_raw"`
	FeeAmountRaw     string `ch:"fee_amount_raw" json:"fee_amount_raw"`
	FromTokenAccount string `ch:"from_token_account" json:"from_token_account"`
	ToTokenAccount   string `ch:"to_token_account" json:"to_token_account"`
	// 0010 迁移新增的 Solana 指令证据列（指令下标对齐 EVM 的 log_index 语义）。
	InstructionIndex      uint32 `ch:"instruction_index" json:"instruction_index"`
	InnerInstructionIndex int32  `ch:"inner_instruction_index" json:"inner_instruction_index"`
	SourceAPI             string `ch:"source_api" json:"source_api"`
	SourceRequestID       string `ch:"source_request_id" json:"source_request_id"`
}

type nftTransferRow struct {
	Chain       string    `ch:"chain" json:"chain"`
	BlockNumber uint64    `ch:"block_number" json:"block_number"`
	Timestamp   time.Time `ch:"timestamp" json:"timestamp"`
	TxHash      string    `ch:"tx_hash" json:"tx_hash"`
	LogIndex    uint32    `ch:"log_index" json:"log_index"`
	Contract    string    `ch:"contract_address" json:"contract_address"`
	TokenID     string    `ch:"token_id" json:"token_id"`
	FromAddress string    `ch:"from_address" json:"from_address"`
	ToAddress   string    `ch:"to_address" json:"to_address"`
	TokenName   string    `ch:"token_name" json:"token_name"`
	TokenSymbol string    `ch:"token_symbol" json:"token_symbol"`
	// token_standard 随 SELECT * 返回；结构体必须接住，否则整条查询会在扫描阶段失败。
	TokenStandard  string    `ch:"token_standard" json:"token_standard"`
	AmountRaw      string    `ch:"amount_raw" json:"amount_raw"`
	SourceProvider string    `ch:"source_provider" json:"source_provider"`
	SourceIngested time.Time `ch:"source_ingested_at" json:"source_ingested_at"`
	// 0010 迁移新增列同样随 SELECT * 返回（NFT 浏览/导出路径）。
	InstructionIndex      uint32 `ch:"instruction_index" json:"instruction_index"`
	InnerInstructionIndex int32  `ch:"inner_instruction_index" json:"inner_instruction_index"`
	SourceAPI             string `ch:"source_api" json:"source_api"`
	SourceRequestID       string `ch:"source_request_id" json:"source_request_id"`
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
	// Solana 证据字段（ADR-023 D10）：SELECT * 会带出这些列，结构体必须能接住，
	// 否则整条活动查询在扫描阶段失败（表现为 500）。
	TokenAccount             string `ch:"token_account" json:"token_account"`
	CounterpartyTokenAccount string `ch:"counterparty_token_account" json:"counterparty_token_account"`
	FeeAmountRaw             string `ch:"fee_amount_raw" json:"fee_amount_raw"`
}

/*
 * ---- ReplacingMergeTree 显式去重（替代 FINAL） -------------------------------------
 *
 * 为什么不能在按地址过滤的读路径上用 FINAL（2026-09-21 实测，blockchain.token_transfers
 * 92,274,258 行 / 8.37 GiB）：
 *
 *   SELECT count() FROM blockchain.token_transfers FINAL
 *    WHERE chain='bsc' AND (from_address='0x7696…' OR to_address='0x7696…')
 *   → 27.2 s，整链合并读；同一个问题在 from_address/to_address 上只有 bloom_filter 跳数
 *     索引，而 FINAL 会先按排序键 (chain, tx_hash, log_index, token_address) 合并整条链，
 *     跳数索引完全用不上。整个 AI 查询面（/api/v1 的明细、概览、对手方、首笔资金、网络边）
 *     因此经常 11–36 s，超过 60 s 时直接 500 INTERNAL_ERROR。
 *
 * 等价的显式去重（每个排序键保留 source_ingested_at 最大的一行，语义与 FINAL 完全一致）：
 *
 *   SELECT uniqExact(tuple(tx_hash, log_index, token_address)) FROM blockchain.token_transfers
 *    WHERE chain='bsc' AND (from_address='0x7696…' OR to_address='0x7696…')
 *   → 0.27 s，同一个答案 4877。
 *
 * 行取数同样：把 FINAL 换成 row_number() OVER (PARTITION BY <排序键> ORDER BY <版本列> DESC)=1
 * 的子查询，跳数索引即可生效（实测 11.0 s → 0.16 s，行内容逐列一致）。
 */

// dedupVersion 返回各表 ReplacingMergeTree 的版本列（建表 engine 的第二参数）。
// internal_calls / transaction_traces 用的是 traced_at，其余是 source_ingested_at。
func dedupVersion(dataset string) string {
	switch dataset {
	case "internal_transactions", "transaction_traces":
		return "traced_at"
	default:
		return "source_ingested_at"
	}
}

// dedupSupported 报出哪些数据集可以安全地走显式去重：它们都有明确的排序键
// （见 explorerPartition）与版本列。balances 只有几千行，保持 FINAL 不变。
func dedupSupported(dataset string) bool {
	switch dataset {
	case "transactions", "token_transfers", "nft_transfers", "internal_transactions", "logs", "activity":
		return true
	default:
		return false
	}
}

// dedupSource 返回「去重后的行集合」子查询（SELECT *，去掉内部标记列 _rn）。
// where 里的谓词留在子查询内，跳数索引才能在扫描前裁剪 granule。
func dedupSource(dataset, where string) string {
	return "SELECT * EXCEPT _rn FROM (SELECT *, row_number() OVER (PARTITION BY " +
		explorerPartition(dataset) + " ORDER BY " + dedupVersion(dataset) + " DESC) AS _rn FROM blockchain." +
		tableName(dataset) + " WHERE " + where + ") WHERE _rn = 1"
}

// dedupCount 返回去重后的行数，等价于 count() ... FINAL。
func dedupCount(dataset, where string) string {
	return "SELECT uniqExact(tuple(" + explorerPartition(dataset) + ")) FROM blockchain." +
		tableName(dataset) + " WHERE " + where
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
	// 按地址过滤的读路径必须走显式去重，不能 FINAL（见 dedupSource 的实测注释）。
	source := "SELECT * FROM blockchain." + tableName(dataset) + " FINAL WHERE " + where
	if dedupSupported(dataset) {
		source = dedupSource(dataset, where)
	}
	q := source + " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
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
	// 同一个理由：地址过滤的浏览查询走显式去重（token_transfers/transactions/…）。
	// activity 走 address_activity 的 (chain, address) 前缀，两种写法本来就快。
	source := "SELECT * FROM blockchain." + table + " FINAL WHERE " + where
	if dataset != "activity" && dedupSupported(dataset) {
		source = dedupSource(dataset, where)
	}
	q := source
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
	// 查询键必须与写入侧（dataset/ingest 的按链规范化）一致：Solana 保留 base58 大小写。
	address = addressnorm.NormalizeKey(chain, address)
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

// FlowTokenTransfers 是"资金流画像"这类分析的读路径：它**不走导出形状**。
//
// 背景（P1-1）：flow-profile 实测 9.9 秒，根因是它借用了 RowsForExport —— 导出形状是
// "SELECT * ... FINAL ... ORDER BY timestamp DESC LIMIT 50000"：
//   - FINAL 退化成整表合并读（实测 85,074,805 行 / 6.9-10.6 GiB），from_address/to_address 上的
//     bloom_filter 跳数索引完全用不上；
//   - 还要按 timestamp 排序 5 万行，而排序键是 (chain, tx_hash, log_index, token_address)。
//
// 这里改成：用 ReplacingMergeTree 排序键上的显式去重（argMax by source_ingested_at）代替 FINAL，
// 让跳数索引能把粒度裁到 2.4%（实测 181/7520），并且只投影分析真正用到的列。
// 语义与 FINAL 一致：每个 (chain, tx_hash, log_index, token_address) 保留版本最大的那一行
// （重复行来自多 Provider 重复摄取：实测同一地址原始 34,189 行 ↔ 17,691 个键）。
func (s *Service) FlowTokenTransfers(ctx context.Context, chain, subject string, limit int) ([]map[string]any, error) {
	// 查询键必须与写入侧一致：Solana 保留 base58 大小写（折叠后是另一个地址）。
	subject = addressnorm.NormalizeKey(chain, subject)
	if limit <= 0 || limit > 50000 {
		limit = 50000
	}
	// 交互读预算：到点必须有结论，超了由调用方翻译成可操作错误。
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	// 注意别名：去重列**不能**与真实列同名。同名时 ClickHouse 会把内层 WHERE 里的
	// from_address/to_address 解析成聚合别名，直接 ILLEGAL_AGGREGATION（Code 184，实测踩过）。
	// 因此内层用 fr_addr/to_addr/sym/amt/ts 这些独立别名，外层再改回分析器认识的列名。
	const q = `SELECT d.fr_addr AS from_address, d.to_addr AS to_address, d.sym AS token_symbol,
       d.token_address AS token_address, toString(d.amt) AS amount, d.tx_hash AS tx_hash, d.ts AS timestamp
FROM (
    SELECT tx_hash, log_index, token_address,
           argMax(from_address, source_ingested_at) AS fr_addr,
           argMax(to_address, source_ingested_at) AS to_addr,
           argMax(token_symbol, source_ingested_at) AS sym,
           argMax(amount, source_ingested_at) AS amt,
           argMax(timestamp, source_ingested_at) AS ts
    FROM blockchain.token_transfers
    WHERE chain = ? AND (from_address = ? OR to_address = ?)
    GROUP BY tx_hash, log_index, token_address
) AS d
ORDER BY d.ts DESC
LIMIT ?`
	rows, err := s.CH.DB().Query(ctx, q, chain, subject, subject, limit)
	if err != nil {
		return nil, clickhouse.DescribeInteractiveScanError(err)
	}
	defer rows.Close()
	out := make([]map[string]any, 0, 1024)
	for rows.Next() {
		var from, to, symbol, contract, amount, tx string
		var ts time.Time
		if err := rows.Scan(&from, &to, &symbol, &contract, &amount, &tx, &ts); err != nil {
			return nil, fmt.Errorf("clickhouse [ScanFlowRow]: %w", err)
		}
		out = append(out, map[string]any{
			"from_address": from, "to_address": to, "token_symbol": symbol,
			"token_address": contract, "amount": amount, "tx_hash": tx, "timestamp": ts,
		})
	}
	return out, rows.Err()
}

// RowsForExport 导出查询：按链过滤，可选按地址过滤，流式上限。
func (s *Service) RowsForExport(ctx context.Context, dataset, chain, subject string, limit, offset int) ([]map[string]any, error) {
	return s.RowsForExportRange(ctx, dataset, chain, subject, limit, offset, nil, nil, nil, nil)
}

// RowsForExportRange keeps job exports scoped to both their subjects and the
// requested block/time range. Nil bounds preserve the legacy address export.
func (s *Service) RowsForExportRange(ctx context.Context, dataset, chain, subject string, limit, offset int,
	fromBlock, toBlock *int64, fromTime, toTime *time.Time) ([]map[string]any, error) {
	// Export scans run one at a time with an explicit, bounded ClickHouse
	// memory budget; see exportScanSettings for the measured Code 241 defect.
	release, slotErr := acquireExportScanSlotFor(ctx)
	if slotErr != nil {
		return nil, slotErr
	}
	defer release()
	ctx = clickhouse.ExportScanContext(ctx)
	switch dataset {
	case "transactions", "token_transfers", "nft_transfers", "logs", "balances", "internal_transactions":
	default:
		return nil, fmt.Errorf("unknown dataset %s", dataset)
	}
	// 查询键必须与写入侧一致：Solana 保留 base58 大小写（折叠后是另一个地址）。
	subject = addressnorm.NormalizeKey(chain, subject)
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
	// 两阶段形状（见 export_bounded.go 的实测说明）：先用只读 timestamp 一列的按天聚合
	// 求出时间下界，再用它把候选行数压到目标量级后取全列。实测同一请求
	// 3.68 s → 约 0.75 s，因为成本主要在"物化 22 列宽行"而不是扫描或排序。
	if subject != "" {
		if rows, ok, err := s.rowsForExportBounded(ctx, dataset, chain, subject, limit, offset); ok || err != nil {
			return rows, err
		}
	}
	// 无 FINAL 形状（见 export_dedupe.go 的实测说明）：列集/过滤/排序与现网完全一致，
	// 只把"选版本"交给 Go 侧去重。FINAL 会先按排序键合并整条链，导致
	// from_address/to_address 上的 bloom_filter 完全用不上
	// （实测 19.6-23.8 s / 5.86 GiB → 5.7 s / 1.12 GiB，去重后 20 万行逐列相等）。
	if sel, ok := exportSelectNoFinal(dataset, where); ok {
		return s.rangeExportNoFinal(ctx, dataset, sel, where, args, limit, offset)
	}
	if subject != "" {
		where, args = exportIndexedFinalWhere(dataset, where, args)
	}
	args = append(args, limit, offset)
	q := "SELECT * FROM blockchain." + tableName(dataset) + " FINAL WHERE " + where + " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	return s.queryRows(ctx, dataset, q, args...)
}

// rangeExportNoFinal 执行不带 FINAL 的导出查询，并在 Go 侧完成 ReplacingMergeTree
// 语义的版本收敛。
//
// 重复行会占用 LIMIT 名额（bsc 实测重复率 31.4%），所以按 dedupeExportRetryFactor
// 起超采、去重后不足 limit 就放宽重试，最多 dedupeExportMaxAttempts 次；取不满时返回
// 已收敛的部分结果（API 层还会按同一组键再去重一次，因此文件内容不会因重复行变多）。
func (s *Service) rangeExportNoFinal(ctx context.Context, dataset, sel, where string, args []any,
	limit, offset int) ([]map[string]any, error) {
	// 流式去重只在两个条件同时满足时启用：
	//   - 单页取数（offset==0）：分页时"存活行数达到 limit 就停"会把后续页算错；
	//   - 该数据集确有去重键：没有键时 exportRowKey 返回同一个常量键，
	//     会把所有行当成重复行而只剩一行（比慢更糟）。
	wantLimit := 0
	if offset == 0 && len(exportDedupeColumns(dataset)) > 0 {
		wantLimit = limit
	}
	// 单次取数上限：数据不足时不要把重试放大成无界扫描。
	const maxDedupeFetch = 4_000_000
	overFetch := int(float64(limit) * dedupeExportRetryFactor)
	for attempt := 0; attempt < dedupeExportMaxAttempts; attempt++ {
		if overFetch > maxDedupeFetch {
			overFetch = maxDedupeFetch
		}
		queryArgs := append(append([]any{}, args...), overFetch, offset)
		raw, err := s.queryRowsExport(ctx, dataset, sel, wantLimit, queryArgs...)
		if err != nil {
			return nil, err
		}
		distinct := dedupeExportRows(dataset, raw, limit)
		if wantLimit > 0 {
			// 流式路径已按去重键收敛并攒够 limit 行；再走一次去重是幂等的，只做截断。
			// （LIMIT=0 或数据集没有去重键时 wantLimit 为 0，走下面的重试分支。）
			return distinct, nil
		}
		if len(distinct) >= limit || len(raw) < overFetch {
			return distinct, nil
		}
		if s.Log != nil {
			s.Log.Info("导出扫描去重后不足目标行数，放宽上限重试",
				"dataset", dataset, "want", limit, "got", len(distinct),
				"over_fetch", overFetch, "attempt", attempt+1)
		}
		overFetch += dedupeExportExtraRows
	}
	// 走到这里说明数据真的不够（地址/范围内没有 limit 个不同键），而不是去重没收敛：
	// 返回已取到的最大行集，而不是报错——API 层会按同一组键再去重一次，
	// 导出文件因此是"该地址在此范围内的全部行"，与历史行为一致。
	if s.Log != nil {
		s.Log.Info("导出取数不足目标行数，返回已取到的全部行",
			"dataset", dataset, "want", limit, "over_fetch", overFetch)
	}
	raw, err := s.queryRowsExport(ctx, dataset, sel, offsetCapture(offset, wantLimit, limit),
		append(append([]any{}, args...), overFetch, offset)...)
	if err != nil {
		return nil, err
	}
	return dedupeExportRows(dataset, raw, limit), nil
}

// offsetCapture 让最后一次兜底取数也启用流式早停（仅单页且有去重键时）。
func offsetCapture(offset, wantLimit, limit int) int {
	if offset == 0 && wantLimit > 0 {
		return limit
	}
	return 0
}

// RowsForExportRangeMulti scans ClickHouse once for all requested addresses.
// Job ZIP export then fans the result into per-address files in memory, avoiding
// one full FINAL table scan per address.
func (s *Service) RowsForExportRangeMulti(ctx context.Context, dataset, chain string, subjects []string, limit int,
	fromBlock, toBlock *int64, fromTime, toTime *time.Time) ([]map[string]any, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	// Bounded exactly like RowsForExportRange: one ZIP worker per dataset can
	// otherwise hold millions of rows in server memory at the same time.
	release, slotErr := acquireExportScanSlotFor(ctx)
	if slotErr != nil {
		return nil, slotErr
	}
	defer release()
	ctx = clickhouse.ExportScanContext(ctx)
	for i := range subjects {
		// 查询键必须与写入侧一致：Solana 保留 base58 大小写（折叠后是另一个地址）。
		subjects[i] = addressnorm.NormalizeKey(chain, subjects[i])
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
	// 该形状由调用方（API 层）自行去重，因此这里不做流式收敛，但**不能再用** FINAL：
	// FINAL 会先按排序键合并整条链，把 from_address/to_address 上的 bloom_filter
	// 跳数索引全部作废；它前面那层"键 IN 子查询"也没有 LIMIT，等价于先把该地址的
	// 全部键物化一遍。实测同一请求（20 万行目标、单地址）：本形状 28.5 s，
	// 去掉 FINAL 与键子查询后 4.86 s。
	//
	// 这与单地址路径（exportSelectNoFinal）是同一处修正；multi 路径此前一直没改，
	// 而它正是 Actions 导出（csv→强制 zip）实际走的路径。
	args = append(args, limit)
	q := "SELECT * FROM blockchain." + tableName(dataset) + " WHERE " + where + " ORDER BY timestamp DESC LIMIT ?"
	return s.queryRowsExport(ctx, dataset, q, 0, args...)
}

// FINAL expands address bloom-filter matches to all primary-key granules.
// The subquery gets candidate keys with the address index. The outer FINAL
// still selects the latest version, then reapplies all original filters.
func exportIndexedFinalWhere(dataset, where string, args []any) (string, []any) {
	var key string
	switch dataset {
	case "transactions":
		key = "tx_hash"
	case "token_transfers":
		key = "(tx_hash, log_index, token_address)"
	default:
		return where, args
	}
	table := "blockchain." + tableName(dataset)
	subquery := "SELECT " + strings.Trim(key, "()") + " FROM " + table + " WHERE " + where
	return where + " AND " + key + " IN (" + subquery + ")", append(args, args...)
}

func (s *Service) queryRows(ctx context.Context, dataset, q string, args ...any) ([]map[string]any, error) {
	rows, err := s.CH.DB().Query(ctx, q, args...)
	if err != nil {
		// Surface a server memory refusal as the typed ErrScanTooLarge instead
		// of an opaque driver error (measured Code 241 on the export scan).
		return nil, clickhouse.DescribeScanError(err)
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
		key := fmt.Sprint(candidate["tx_hash"]) + "|" + addressnorm.NormalizeKey(chain, fmt.Sprint(candidate["token_address"]))
		byHashToken[key] = append(byHashToken[key], candidate)
	}
	for _, row := range rows {
		if fmt.Sprint(row["activity_type"]) != "transfer" || strings.TrimSpace(fmt.Sprint(row["amount_raw"])) != "" {
			continue
		}
		key := fmt.Sprint(row["tx_hash"]) + "|" + addressnorm.NormalizeKey(chain, fmt.Sprint(row["token_address"]))
		for _, candidate := range byHashToken[key] {
			if !activityCandidateMatches(chain, row, candidate) {
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

// isEmptyCell 判断单元格是否为空。
//
// 不能直接用 fmt.Sprint：缺失的 key 会格式化成 "<nil>"，被误判成"已有值"而跳过回填。
func isEmptyCell(v any) bool {
	switch value := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(value) == ""
	default:
		return false
	}
}

// HydrateActivityMethods 给活动行补方法名：address_activity 不存方法，方法在 transactions 里。
//
// 只按本页 distinct 交易哈希回查一次。transactions 按月份分区，直接 FINAL 会在
// 多个月份的所有分区做合并，实测 BSC 一页扫描约 1700 万行 / 34-38 秒。
// 这里先用 (chain,tx_hash) 键过滤，再仅对命中的版本按 source_ingested_at 取最新，
// 与 ReplacingMergeTree 的版本选择一致，且只投影方法所需的三列。
func (s *Service) HydrateActivityMethods(ctx context.Context, chain string, rows []map[string]any) error {
	hashSet := map[string]bool{}
	for _, row := range rows {
		hash := strings.TrimSpace(fmt.Sprint(row["tx_hash"]))
		if hash == "" || !isEmptyCell(row["method_name"]) {
			continue
		}
		hashSet[hash] = true
	}
	if len(hashSet) == 0 {
		return nil
	}
	hashes := make([]string, 0, len(hashSet))
	for hash := range hashSet {
		hashes = append(hashes, hash)
	}
	candidates, err := s.queryRows(ctx, "transactions", `
		SELECT tx_hash,
		       tupleElement(method_fields, 1) AS method_id,
		       tupleElement(method_fields, 2) AS method_name,
		       tupleElement(method_fields, 3) AS method_signature
		FROM (
			SELECT tx_hash,
			       argMax(tuple(method_id, method_name, method_signature), source_ingested_at) AS method_fields
			FROM blockchain.transactions
			WHERE chain=? AND tx_hash IN (?)
			GROUP BY tx_hash
		)`, chain, hashes)
	if err != nil {
		return err
	}
	byHash := make(map[string]map[string]any, len(candidates))
	for _, candidate := range candidates {
		byHash[fmt.Sprint(candidate["tx_hash"])] = candidate
	}
	for _, row := range rows {
		if !isEmptyCell(row["method_name"]) {
			continue
		}
		candidate, ok := byHash[fmt.Sprint(row["tx_hash"])]
		if !ok {
			continue
		}
		row["method_id"] = candidate["method_id"]
		row["method_name"] = candidate["method_name"]
		row["method_signature"] = candidate["method_signature"]
	}
	return nil
}

func activityCandidateMatches(chain string, activity, transfer map[string]any) bool {
	// 按链归一化后再比较：Solana base58 大小写敏感，折叠会误判成另一个地址。
	address := addressnorm.NormalizeKey(chain, fmt.Sprint(activity["address"]))
	counterparty := addressnorm.NormalizeKey(chain, fmt.Sprint(activity["counterparty"]))
	from := addressnorm.NormalizeKey(chain, fmt.Sprint(transfer["from_address"]))
	to := addressnorm.NormalizeKey(chain, fmt.Sprint(transfer["to_address"]))
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
// newRowStruct 返回该数据集的行结构体指针。快路径（export_scan.go）用它的字段
// 建立列映射，因此这里是"哪些列属于哪个数据集"的唯一定义，新增列只改这一处。
func newRowStruct(dataset string) (any, error) {
	switch dataset {
	case "transactions":
		return &txRow{}, nil
	case "token_transfers":
		return &tokenTransferRow{}, nil
	case "nft_transfers":
		return &nftTransferRow{}, nil
	case "logs":
		return &logRow{}, nil
	case "balances":
		return &balanceRow{}, nil
	case "activity":
		return &activityRow{}, nil
	case "internal_transactions":
		return &internalTransferRow{}, nil
	default:
		return nil, fmt.Errorf("unknown dataset %s", dataset)
	}
}

func scanOne(dataset string, rows interface{ ScanStruct(dest any) error }) (map[string]any, error) {
	v, err := newRowStruct(dataset)
	if err != nil {
		return nil, err
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
