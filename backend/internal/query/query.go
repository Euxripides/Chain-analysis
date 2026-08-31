// Package query：统一数据面查询（业务模块只查 ClickHouse，不查 Raw JSON）。
package query

import (
	"context"
	"encoding/json"
	"fmt"
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

// tableName dataset 逻辑名 → ClickHouse 表名。
func tableName(dataset string) string {
	if dataset == "balances" {
		return "address_balances"
	}
	return dataset
}

func (s *Service) Rows(ctx context.Context, dataset, chain, subject string, limit, offset int) ([]map[string]any, error) {
	var where string
	var args []any
	switch dataset {
	case "transactions", "token_transfers", "nft_transfers":
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

// RowsForExport 导出查询：按链过滤，可选按地址过滤，流式上限。
func (s *Service) RowsForExport(ctx context.Context, dataset, chain, subject string, limit, offset int) ([]map[string]any, error) {
	switch dataset {
	case "transactions", "token_transfers", "nft_transfers", "logs", "balances":
	default:
		return nil, fmt.Errorf("unknown dataset %s", dataset)
	}
	subject = strings.ToLower(subject)
	var where string
	var args []any
	if subject != "" {
		switch dataset {
		case "transactions", "token_transfers", "nft_transfers":
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
	args = append(args, limit, offset)
	q := "SELECT * FROM blockchain." + tableName(dataset) + " FINAL WHERE " + where + " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
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
		out = append(out, item)
	}
	return out, rows.Err()
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
	return m, nil
}
