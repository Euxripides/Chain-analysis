package ingest

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/shopspring/decimal"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
)

type Writer struct {
	conn      *clickhouse.Conn
	batchSize int
}

func NewWriter(conn *clickhouse.Conn, batchSize int) *Writer {
	if batchSize <= 0 {
		batchSize = 5000
	}
	return &Writer{conn: conn, batchSize: batchSize}
}

// batched 泛型分批写入，并记录 ClickHouse 写入指标（§74）。
func batched[T any](ctx context.Context, batchSize, n int, flush func(batch []T) error, rows []T, table string) error {
	start := time.Now()
	for s := 0; s < n; s += batchSize {
		e := s + batchSize
		if e > n {
			e = n
		}
		if err := flush(rows[s:e]); err != nil {
			observability.ClickhouseInsertLatencyMs.WithLabelValues(table).Observe(float64(time.Since(start).Milliseconds()))
			return fmt.Errorf("clickhouse batch: %w", err)
		}
	}
	observability.ClickhouseInsertLatencyMs.WithLabelValues(table).Observe(float64(time.Since(start).Milliseconds()))
	observability.ClickhouseInsertRows.WithLabelValues(table).Add(float64(n))
	return nil
}

func (w *Writer) WriteTransactions(ctx context.Context, rows []TxRow) error {
	return batched(ctx, w.batchSize, len(rows), func(batch []TxRow) error {
		stmt, err := w.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.transactions "+
			"(chain, block_number, block_hash, tx_hash, timestamp, from_address, to_address, value_raw, nonce, gas, gas_used, gas_price, tx_type, status, input, method_id, source_provider, "+
			"method_name, method_signature, method_source, method_verified, method_confidence, method_type, method_resolved_at, native_symbol, confirmation, raw_type, raw_status) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			source := r.MethodSource
			if source == "" {
				source = "unknown"
			}
			confidence := r.MethodConfidence
			if confidence == "" {
				confidence = "unknown"
			}
			if err := stmt.Append(r.Chain, r.BlockNumber, r.BlockHash, r.TxHash, r.Timestamp, r.From, r.To,
				r.ValueRaw, r.Nonce, r.Gas, r.GasUsed, r.GasPrice, r.TxType, r.Status, r.Input, r.MethodID, r.Provider,
				r.MethodName, r.MethodSignature, source, r.MethodVerified, confidence, r.MethodType,
				r.MethodResolvedAt, r.NativeSymbol, r.Confirmation, r.RawType, r.RawStatus); err != nil {
				return err
			}
		}
		return stmt.Send()
	}, rows, "transactions")
}

func (w *Writer) WriteInternalTransfers(ctx context.Context, rows []InternalTransferRow) error {
	return batched(ctx, w.batchSize, len(rows), func(batch []InternalTransferRow) error {
		stmt, err := w.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.internal_calls "+
			"(chain, tx_hash, call_index, depth, trace_path, call_type, activity_type, from_address, to_address, value_raw, method_id, gas, gas_used, success, error, block_number, timestamp, source_provider) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			success := uint8(1)
			errText := ""
			if r.Rejected {
				success, errText = 0, "rejected"
			}
			if err := stmt.Append(r.Chain, r.TxHash, r.CallIndex, uint16(0), fmt.Sprintf("%d", r.CallIndex), r.CallType,
				"internal_transfer", r.From, r.To, r.ValueRaw, "", uint64(0), uint64(0), success, errText,
				r.BlockNumber, r.Timestamp, r.Provider); err != nil {
				return err
			}
		}
		return stmt.Send()
	}, rows, "internal_calls")
}

func (w *Writer) WriteTokenTransfers(ctx context.Context, rows []TokenTransferRow) error {
	return batched(ctx, w.batchSize, len(rows), func(batch []TokenTransferRow) error {
		stmt, err := w.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.token_transfers "+
			"(chain, block_number, timestamp, tx_hash, log_index, token_address, token_symbol, token_decimals, from_address, to_address, amount_raw, amount, source_provider) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			amt := decOrZero(r.Amount, r.TokenDecimals)
			if err := stmt.Append(r.Chain, r.BlockNumber, r.Timestamp, r.TxHash, r.LogIndex, r.TokenAddress,
				r.TokenSymbol, r.TokenDecimals, r.From, r.To, r.AmountRaw, amt, r.Provider); err != nil {
				return err
			}
		}
		return stmt.Send()
	}, rows, "token_transfers")
}

func (w *Writer) WriteNftTransfers(ctx context.Context, rows []NftTransferRow) error {
	return batched(ctx, w.batchSize, len(rows), func(batch []NftTransferRow) error {
		stmt, err := w.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.nft_transfers "+
			"(chain, block_number, timestamp, tx_hash, log_index, contract_address, token_id, from_address, to_address, token_name, token_symbol, amount_raw, source_provider) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			if err := stmt.Append(r.Chain, r.BlockNumber, r.Timestamp, r.TxHash, r.LogIndex, r.Contract, r.TokenID,
				r.From, r.To, r.TokenName, r.TokenSymbol, r.AmountRaw, r.Provider); err != nil {
				return err
			}
		}
		return stmt.Send()
	}, rows, "nft_transfers")
}

func (w *Writer) WriteLogs(ctx context.Context, rows []LogRow) error {
	return batched(ctx, w.batchSize, len(rows), func(batch []LogRow) error {
		stmt, err := w.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.logs "+
			"(chain, block_number, block_hash, tx_hash, log_index, address, topics, data, removed, timestamp, source_provider, "+
			"event_name, event_signature, event_source, event_verified) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			source := r.EventSource
			if source == "" {
				source = "unknown"
			}
			if err := stmt.Append(r.Chain, r.BlockNumber, r.BlockHash, r.TxHash, r.LogIndex, r.Address,
				r.Topics, r.Data, r.Removed, r.Timestamp, r.Provider,
				r.EventName, r.EventSignature, source, r.EventVerified); err != nil {
				return err
			}
		}
		return stmt.Send()
	}, rows, "logs")
}

func (w *Writer) WriteBalances(ctx context.Context, rows []BalanceRow) error {
	return batched(ctx, w.batchSize, len(rows), func(batch []BalanceRow) error {
		stmt, err := w.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.address_balances "+
			"(chain, address, token_symbol, token_name, token_address, token_type, token_decimals, balance_raw, balance, balance_usd, token_price, snapshot_block, timestamp, source_provider) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			if err := stmt.Append(r.Chain, r.Address, r.TokenSymbol, r.TokenName, r.TokenAddress, r.TokenType,
				r.TokenDecimals, r.BalanceRaw, decOrZero(r.Balance, r.TokenDecimals), r.BalanceUSD, r.TokenPrice,
				r.SnapshotBlock, r.Timestamp, r.Provider); err != nil {
				return err
			}
		}
		return stmt.Send()
	}, rows, "address_balances")
}

// WriteActivity 地址活动汇总（§65/§87：资金分析统一查询面）。
func (w *Writer) WriteActivity(ctx context.Context, rows []ActivityRow) error {
	return batched(ctx, w.batchSize, len(rows), func(batch []ActivityRow) error {
		stmt, err := w.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.address_activity "+
			"(chain, address, counterparty, direction, activity_type, timestamp, block_number, tx_hash, log_index, token_address, token_symbol, token_decimals, amount_raw, amount, amount_usd, dataset, source_provider) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			if err := stmt.Append(r.Chain, r.Address, r.Counterparty, r.Direction, r.ActivityType, r.Timestamp,
				r.BlockNumber, r.TxHash, r.LogIndex, r.TokenAddress, r.TokenSymbol, r.TokenDecimals, r.AmountRaw,
				decOrZero(r.Amount, r.TokenDecimals), r.AmountUSD, r.Dataset, r.Provider); err != nil {
				return err
			}
		}
		return stmt.Send()
	}, rows, "address_activity")
}

// CountFinal 校验证据（§60/§63）：FINAL 去重后的唯一行数（只读，不阻塞数据面）。
func (w *Writer) CountFinal(ctx context.Context, dataset, chain, address string, from, to uint64) (uint64, error) {
	var q string
	switch dataset {
	case "transactions":
		q = "SELECT count() FROM blockchain.transactions FINAL WHERE chain = ? AND block_number >= ? AND block_number <= ? AND (from_address = ? OR to_address = ?)"
	case "token_transfers":
		q = "SELECT count() FROM blockchain.token_transfers FINAL WHERE chain = ? AND block_number >= ? AND block_number <= ? AND (from_address = ? OR to_address = ?)"
	case "nft_transfers":
		q = "SELECT count() FROM blockchain.nft_transfers FINAL WHERE chain = ? AND block_number >= ? AND block_number <= ? AND (from_address = ? OR to_address = ?)"
	case "internal_transactions":
		q = "SELECT count() FROM blockchain.internal_calls FINAL WHERE chain = ? AND block_number >= ? AND block_number <= ? AND (from_address = ? OR to_address = ?)"
	case "logs":
		q = "SELECT count() FROM blockchain.logs FINAL WHERE chain = ? AND block_number >= ? AND block_number <= ? AND address = ?"
	default:
		return 0, fmt.Errorf("CountFinal unsupported for %s", dataset)
	}
	args := []any{chain, from, to, address}
	if dataset == "transactions" || dataset == "token_transfers" || dataset == "nft_transfers" || dataset == "internal_transactions" {
		args = append(args, address) // WHERE 中 address 出现两次（from/to 双侧匹配）
	}
	var n uint64
	row := w.conn.DB().QueryRow(ctx, q, args...)
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// BoundaryHashes 边界证据（V1.1 §16）：区间内首/末交易哈希。
func (w *Writer) BoundaryHashes(ctx context.Context, dataset, chain, address string, from, to uint64) (string, string, error) {
	var firstQ, lastQ string
	switch dataset {
	case "token_transfers":
		firstQ = "SELECT tx_hash FROM blockchain.token_transfers WHERE chain = ? AND block_number >= ? AND block_number <= ? AND (from_address = ? OR to_address = ?) ORDER BY block_number, tx_hash LIMIT 1"
		lastQ = "SELECT tx_hash FROM blockchain.token_transfers WHERE chain = ? AND block_number >= ? AND block_number <= ? AND (from_address = ? OR to_address = ?) ORDER BY block_number DESC, tx_hash DESC LIMIT 1"
	case "internal_transactions":
		firstQ = "SELECT tx_hash FROM blockchain.internal_calls WHERE chain = ? AND block_number >= ? AND block_number <= ? AND (from_address = ? OR to_address = ?) ORDER BY block_number, tx_hash, call_index LIMIT 1"
		lastQ = "SELECT tx_hash FROM blockchain.internal_calls WHERE chain = ? AND block_number >= ? AND block_number <= ? AND (from_address = ? OR to_address = ?) ORDER BY block_number DESC, tx_hash DESC, call_index DESC LIMIT 1"
	case "logs":
		firstQ = "SELECT tx_hash FROM blockchain.logs WHERE chain = ? AND block_number >= ? AND block_number <= ? AND address = ? ORDER BY block_number, tx_hash LIMIT 1"
		lastQ = "SELECT tx_hash FROM blockchain.logs WHERE chain = ? AND block_number >= ? AND block_number <= ? AND address = ? ORDER BY block_number DESC, tx_hash DESC LIMIT 1"
	default:
		return "", "", fmt.Errorf("BoundaryHashes unsupported for %s", dataset)
	}
	args := []any{chain, from, to, address}
	if dataset == "token_transfers" || dataset == "internal_transactions" {
		args = append(args, address) // WHERE 中 address 出现两次（from/to 双侧匹配）
	}
	var first, last string
	if err := w.conn.DB().QueryRow(ctx, firstQ, args...).Scan(&first); err != nil && err.Error() != "sql: no rows in result set" {
		return "", "", err
	}
	if err := w.conn.DB().QueryRow(ctx, lastQ, args...).Scan(&last); err != nil && err.Error() != "sql: no rows in result set" {
		return "", "", err
	}
	return first, last, nil
}

func bigIntOrZero(b *big.Int) *big.Int {
	if b == nil {
		return big.NewInt(0)
	}
	return b
}

// decOrZero 将链上原始整数按 Token decimals 转成人类可读金额。
// ClickHouse 列本身的 38 是存储精度，不是所有 Token 的小数位。
func decOrZero(b *big.Int, decimals uint16) decimal.Decimal {
	if b == nil {
		return decimal.NewFromInt(0)
	}
	digits := len(b.Text(10))
	if b.Sign() < 0 {
		digits--
	}
	// Decimal256(38) has 38 integer digits. Preserve the authoritative raw
	// value, but do not let a pathological token amount fail the whole batch.
	if digits-int(decimals) > 38 {
		return decimal.NewFromInt(0)
	}
	return decimal.NewFromBigInt(b, -int32(decimals))
}
