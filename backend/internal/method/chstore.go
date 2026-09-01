package method

import (
	"context"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

// ClickHouseStore 数据面实现（TxDataStore + BackfillDataStore）。
// 直接使用显式列名，绝不依赖 SELECT * 的列顺序。
type ClickHouseStore struct {
	conn *clickhouse.Conn
}

func NewClickHouseStore(conn *clickhouse.Conn) *ClickHouseStore { return &ClickHouseStore{conn: conn} }

const txColumns = "chain, block_number, block_hash, tx_hash, timestamp, from_address, to_address, value_raw, " +
	"nonce, gas, gas_used, gas_price, tx_type, status, input, method_id, source_provider, " +
	"method_name, method_signature, method_source, method_verified, method_confidence, method_type, method_resolved_at"

func (s *ClickHouseStore) scanTx(rows interface {
	Scan(dest ...any) error
}) (TxRecord, error) {
	var r TxRecord
	err := rows.Scan(&r.Chain, &r.BlockNumber, &r.BlockHash, &r.TxHash, &r.Timestamp, &r.FromAddress, &r.ToAddress,
		&r.ValueRaw, &r.Nonce, &r.Gas, &r.GasUsed, &r.GasPrice, &r.TxType, &r.Status, &r.Input, &r.MethodID,
		&r.SourceProvider, &r.MethodName, &r.MethodSignature, &r.MethodSource, &r.MethodVerified,
		&r.MethodConfidence, &r.MethodType, &r.MethodResolvedAt)
	return r, err
}

// GetTransactionRecord 读取单笔交易（结构化）。
func (s *ClickHouseStore) GetTransactionRecord(ctx context.Context, chain, txHash string) (*TxRecord, error) {
	rows, err := s.conn.DB().Query(ctx,
		"SELECT "+txColumns+" FROM blockchain.transactions FINAL WHERE chain = ? AND tx_hash = ? LIMIT 1",
		strings.ToLower(chain), strings.ToLower(txHash))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	record, err := s.scanTx(rows)
	if err != nil {
		return nil, err
	}
	return &record, rows.Err()
}

// GetTransaction 读取单笔交易并转成前端一致的 map 形态。
func (s *ClickHouseStore) GetTransaction(ctx context.Context, chain, txHash string) (map[string]any, error) {
	record, err := s.GetTransactionRecord(ctx, chain, txHash)
	if err != nil || record == nil {
		return nil, err
	}
	return TxRecordToMap(*record), nil
}

// TxRecordToMap 与 query 包返回的字段名保持一致。
func TxRecordToMap(r TxRecord) map[string]any {
	out := map[string]any{
		"chain": r.Chain, "block_number": r.BlockNumber, "block_hash": r.BlockHash, "tx_hash": r.TxHash,
		"timestamp": r.Timestamp, "from_address": r.FromAddress, "to_address": r.ToAddress,
		"value_raw": r.ValueRaw, "nonce": r.Nonce, "gas": r.Gas, "gas_used": r.GasUsed,
		"gas_price": r.GasPrice, "tx_type": r.TxType, "status": r.Status, "input": r.Input,
		"method_id": r.MethodID, "source_provider": r.SourceProvider,
		"method_name": r.MethodName, "method_signature": r.MethodSignature,
		"method_source": r.MethodSource, "method_verified": r.MethodVerified,
		"method_confidence": r.MethodConfidence, "method_type": r.MethodType,
	}
	if r.MethodResolvedAt != nil {
		out["method_resolved_at"] = *r.MethodResolvedAt
	} else {
		out["method_resolved_at"] = nil
	}
	return out
}

// GetLogs 单笔交易的全部事件日志（含 §29 事件解析列）。
func (s *ClickHouseStore) GetLogs(ctx context.Context, chain, txHash string) ([]map[string]any, error) {
	rows, err := s.conn.DB().Query(ctx,
		"SELECT chain, block_number, block_hash, tx_hash, log_index, address, topics, data, removed, timestamp, "+
			"source_provider, event_name, event_signature, event_source, event_verified "+
			"FROM blockchain.logs FINAL WHERE chain = ? AND tx_hash = ? ORDER BY log_index",
		strings.ToLower(chain), strings.ToLower(txHash))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			chainValue, blockHash, hash, address, data, provider string
			eventName, eventSignature, eventSource               string
			blockNumber                                          uint64
			logIndex                                             uint32
			removed, eventVerified                               uint8
			topics                                               []string
			timestamp                                            time.Time
		)
		if err := rows.Scan(&chainValue, &blockNumber, &blockHash, &hash, &logIndex, &address, &topics, &data,
			&removed, &timestamp, &provider, &eventName, &eventSignature, &eventSource, &eventVerified); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"chain": chainValue, "block_number": blockNumber, "block_hash": blockHash, "tx_hash": hash,
			"log_index": logIndex, "address": address, "topics": topics, "data": data, "removed": removed,
			"timestamp": timestamp, "source_provider": provider, "event_name": eventName,
			"event_signature": eventSignature, "event_source": eventSource, "event_verified": eventVerified,
		})
	}
	return out, rows.Err()
}

// GetDecodedInput 本地深度解码缓存（§23：命中即不再请求 Provider）。
func (s *ClickHouseStore) GetDecodedInput(ctx context.Context, chain, txHash string) (*DecodedRecord, error) {
	rows, err := s.conn.DB().Query(ctx,
		"SELECT chain, tx_hash, method_id, method_name, method_signature, decoded_json, decoded_logs_json, "+
			"decode_source, verified, decoded_at FROM blockchain.transaction_decoded_inputs FINAL "+
			"WHERE chain = ? AND tx_hash = ? LIMIT 1",
		strings.ToLower(chain), strings.ToLower(txHash))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var record DecodedRecord
	var verified uint8
	if err := rows.Scan(&record.Chain, &record.TxHash, &record.MethodID, &record.MethodName,
		&record.MethodSignature, &record.DecodedJSON, &record.DecodedLogsJSON, &record.DecodeSource,
		&verified, &record.DecodedAt); err != nil {
		return nil, err
	}
	record.Verified = verified == 1
	if record.DecodedJSON == "" && record.DecodedLogsJSON == "" {
		return nil, rows.Err()
	}
	return &record, rows.Err()
}

// DecodedInputsByTxHashes 批量读取解码结果（导出用，§47）。
func (s *ClickHouseStore) DecodedInputsByTxHashes(ctx context.Context, chain string, hashes []string) (map[string]DecodedRecord, error) {
	out := map[string]DecodedRecord{}
	if len(hashes) == 0 {
		return out, nil
	}
	rows, err := s.conn.DB().Query(ctx,
		"SELECT chain, tx_hash, method_id, method_name, method_signature, decoded_json, decoded_logs_json, "+
			"decode_source, verified, decoded_at FROM blockchain.transaction_decoded_inputs FINAL "+
			"WHERE chain = ? AND tx_hash IN (?)", strings.ToLower(chain), hashes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var record DecodedRecord
		var verified uint8
		if err := rows.Scan(&record.Chain, &record.TxHash, &record.MethodID, &record.MethodName,
			&record.MethodSignature, &record.DecodedJSON, &record.DecodedLogsJSON, &record.DecodeSource,
			&verified, &record.DecodedAt); err != nil {
			return nil, err
		}
		record.Verified = verified == 1
		out[record.TxHash] = record
	}
	return out, rows.Err()
}

// PutDecodedInput 持久化解码结果（§23：同一 Tx Hash 后续不再重复请求 Provider）。
func (s *ClickHouseStore) PutDecodedInput(ctx context.Context, record DecodedRecord) error {
	batch, err := s.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.transaction_decoded_inputs "+
		"(chain, tx_hash, method_id, method_name, method_signature, decoded_json, decoded_logs_json, decode_source, verified, decoded_at) VALUES")
	if err != nil {
		return err
	}
	verified := uint8(0)
	if record.Verified {
		verified = 1
	}
	decodedAt := record.DecodedAt
	if decodedAt.IsZero() {
		decodedAt = time.Now().UTC()
	}
	if err := batch.Append(strings.ToLower(record.Chain), strings.ToLower(record.TxHash), record.MethodID,
		record.MethodName, record.MethodSignature, record.DecodedJSON, record.DecodedLogsJSON,
		record.DecodeSource, verified, decodedAt); err != nil {
		return err
	}
	return batch.Send()
}

// UpdateTransactionMethod 写入新版本行覆盖旧解析（ReplacingMergeTree，§33）。
// 绝不修改 Raw Input（§62-17）。
func (s *ClickHouseStore) UpdateTransactionMethod(ctx context.Context, chain, txHash string, resolution MethodResolution) error {
	record, err := s.GetTransactionRecord(ctx, chain, txHash)
	if err != nil || record == nil {
		return err
	}
	updated := applyResolution(*record, resolution)
	return s.RewriteTransactions(ctx, []TxRecord{updated})
}

// CountTransactions 与 ScanTransactions 使用相同的 FINAL 语义；否则历史版本行会
// 令任务显示 COMPLETED 但 scanned_rows < total_rows，进度永远无法到 100%。
func (s *ClickHouseStore) CountTransactions(ctx context.Context, chain string) (int64, error) {
	rows, err := s.conn.DB().Query(ctx, "SELECT count() FROM blockchain.transactions FINAL WHERE chain = ?", strings.ToLower(chain))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, rows.Err()
	}
	var total uint64
	if err := rows.Scan(&total); err != nil {
		return 0, err
	}
	return int64(total), rows.Err()
}

// ScanTransactions 按主键 (chain, tx_hash) keyset 分页，checkpoint 稳定可恢复（§35）。
func (s *ClickHouseStore) ScanTransactions(ctx context.Context, chain, afterTxHash string, limit int) ([]TxRecord, error) {
	if limit <= 0 {
		limit = 20000
	}
	query := "SELECT " + txColumns + " FROM blockchain.transactions FINAL WHERE chain = ?"
	args := []any{strings.ToLower(chain)}
	if afterTxHash != "" {
		query += " AND tx_hash > ?"
		args = append(args, afterTxHash)
	}
	query += " ORDER BY tx_hash ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.conn.DB().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TxRecord, 0, limit)
	for rows.Next() {
		record, err := s.scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// RewriteTransactions 批量写入新版本行。
func (s *ClickHouseStore) RewriteTransactions(ctx context.Context, records []TxRecord) error {
	if len(records) == 0 {
		return nil
	}
	batch, err := s.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.transactions ("+txColumns+") VALUES")
	if err != nil {
		return err
	}
	for _, r := range records {
		if err := batch.Append(r.Chain, r.BlockNumber, r.BlockHash, r.TxHash, r.Timestamp, r.FromAddress,
			r.ToAddress, r.ValueRaw, r.Nonce, r.Gas, r.GasUsed, r.GasPrice, r.TxType, r.Status, r.Input,
			r.MethodID, r.SourceProvider, r.MethodName, r.MethodSignature, r.MethodSource, r.MethodVerified,
			r.MethodConfidence, r.MethodType, r.MethodResolvedAt); err != nil {
			return err
		}
	}
	return batch.Send()
}

// EnrichmentStats Method 解析覆盖率（§37：与下载 Coverage 完全解耦）。
type EnrichmentStats struct {
	Chain        string  `json:"chain"`
	Total        int64   `json:"total"`
	Resolved     int64   `json:"resolved"`
	ABIVerified  int64   `json:"abi_verified"`
	Unknown      int64   `json:"unknown"`
	ResolvedRate float64 `json:"resolved_rate"`
	VerifiedRate float64 `json:"verified_rate"`
	UnknownRate  float64 `json:"unknown_rate"`
}

// Enrichment 统计 Method 解析情况。
func (s *ClickHouseStore) Enrichment(ctx context.Context, chain string) (*EnrichmentStats, error) {
	query := "SELECT count() AS total, " +
		"countIf(method_name != '') AS resolved, " +
		"countIf(method_verified = 1) AS verified, " +
		"countIf(method_name = '' AND method_id != '') AS unknown " +
		"FROM blockchain.transactions FINAL"
	args := []any{}
	if chain != "" {
		query += " WHERE chain = ?"
		args = append(args, strings.ToLower(chain))
	}
	rows, err := s.conn.DB().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stats := &EnrichmentStats{Chain: chain}
	if rows.Next() {
		var total, resolved, verified, unknown uint64
		if err := rows.Scan(&total, &resolved, &verified, &unknown); err != nil {
			return nil, err
		}
		stats.Total = int64(total)
		stats.Resolved = int64(resolved)
		stats.ABIVerified = int64(verified)
		stats.Unknown = int64(unknown)
		if total > 0 {
			stats.ResolvedRate = float64(resolved) / float64(total)
			stats.VerifiedRate = float64(verified) / float64(total)
			stats.UnknownRate = float64(unknown) / float64(total)
		}
	}
	return stats, rows.Err()
}

var _ TxDataStore = (*ClickHouseStore)(nil)
var _ BackfillDataStore = (*ClickHouseStore)(nil)
