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
			"(chain, block_number, block_hash, tx_hash, timestamp, from_address, to_address, value_raw, nonce, gas, gas_used, gas_price, tx_type, status, input, method_id, source_provider) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			if err := stmt.Append(r.Chain, r.BlockNumber, r.BlockHash, r.TxHash, r.Timestamp, r.From, r.To,
				r.ValueRaw, r.Nonce, r.Gas, r.GasUsed, r.GasPrice, r.TxType, r.Status, r.Input, r.MethodID, r.Provider); err != nil {
				return err
			}
		}
		return stmt.Send()
	}, rows, "transactions")
}

func (w *Writer) WriteTokenTransfers(ctx context.Context, rows []TokenTransferRow) error {
	return batched(ctx, w.batchSize, len(rows), func(batch []TokenTransferRow) error {
		stmt, err := w.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.token_transfers "+
			"(chain, block_number, timestamp, tx_hash, log_index, token_address, token_symbol, token_decimals, from_address, to_address, amount_raw, amount, source_provider) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			amt := decOrZero(r.Amount)
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
			"(chain, block_number, block_hash, tx_hash, log_index, address, topics, data, removed, timestamp, source_provider) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			if err := stmt.Append(r.Chain, r.BlockNumber, r.BlockHash, r.TxHash, r.LogIndex, r.Address,
				r.Topics, r.Data, r.Removed, r.Timestamp, r.Provider); err != nil {
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
				r.TokenDecimals, r.BalanceRaw, decOrZero(r.Balance), r.BalanceUSD, r.TokenPrice,
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
			"(chain, address, counterparty, direction, activity_type, timestamp, block_number, tx_hash, token_address, amount, amount_usd, dataset, source_provider) VALUES")
		if err != nil {
			return err
		}
		for _, r := range batch {
			if err := stmt.Append(r.Chain, r.Address, r.Counterparty, r.Direction, r.ActivityType, r.Timestamp,
				r.BlockNumber, r.TxHash, r.TokenAddress, decOrZero(r.Amount), r.AmountUSD, r.Dataset, r.Provider); err != nil {
				return err
			}
		}
		return stmt.Send()
	}, rows, "address_activity")
}

func bigIntOrZero(b *big.Int) *big.Int {
	if b == nil {
		return big.NewInt(0)
	}
	return b
}

// decOrZero Decimal256(38)：clickhouse-go 以 shopspring/decimal 表达 Decimal 列。
func decOrZero(b *big.Int) decimal.Decimal {
	if b == nil {
		return decimal.NewFromInt(0)
	}
	return decimal.NewFromBigInt(b, -38)
}
