package chaintrace

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

// ---- 持久化（§106/§107/§108）----
//
// metadata + call tree → ClickHouse；派生 internal_calls → ClickHouse；
// 原始超大响应 → MinIO。一旦确认，后续 Investigation 直接复用本地结果。

// Store ClickHouse 存储。
type Store struct {
	conn *clickhouse.Conn
}

func NewStore(conn *clickhouse.Conn) *Store { return &Store{conn: conn} }

// Get 读取已缓存的 Trace（§228：第二次读取不再请求 Provider）。
func (s *Store) Get(ctx context.Context, chainSlug string, hash chain.Hash) (*TraceResult, error) {
	rows, err := s.conn.DB().Query(ctx,
		"SELECT chain, tx_hash, source, frame_count, max_depth, root_json, raw_object_key, raw_bytes, traced_at "+
			"FROM blockchain.transaction_traces FINAL WHERE chain = ? AND tx_hash = ? LIMIT 1",
		strings.ToLower(chainSlug), strings.ToLower(string(hash)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var (
		chainValue, txHash, source, rootJSON, rawKey string
		frameCount, rawBytes                         uint32
		maxDepth                                     uint16
		tracedAt                                     time.Time
	)
	if err := rows.Scan(&chainValue, &txHash, &source, &frameCount, &maxDepth, &rootJSON, &rawKey, &rawBytes, &tracedAt); err != nil {
		return nil, err
	}
	if strings.TrimSpace(rootJSON) == "" {
		return nil, rows.Err()
	}
	var root CallFrame
	if err := json.Unmarshal([]byte(rootJSON), &root); err != nil {
		return nil, rows.Err()
	}
	return &TraceResult{
		Chain: chainValue, TxHash: chain.Hash(txHash), Root: &root, Source: source,
		FrameCount: int(frameCount), MaxDepth: int(maxDepth), FetchedAt: tracedAt,
		RawObjectKey: rawKey, RawBytes: int(rawBytes), CacheHit: true,
	}, rows.Err()
}

// Put 持久化 Trace 与派生的内部活动。
func (s *Store) Put(ctx context.Context, result *TraceResult, activities []InternalActivity) error {
	rootJSON, err := json.Marshal(result.Root)
	if err != nil {
		return err
	}
	batch, err := s.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.transaction_traces "+
		"(chain, tx_hash, source, frame_count, max_depth, root_json, raw_object_key, raw_bytes) VALUES")
	if err != nil {
		return err
	}
	if err := batch.Append(strings.ToLower(result.Chain), strings.ToLower(string(result.TxHash)),
		result.Source, uint32(result.FrameCount), uint16(result.MaxDepth), string(rootJSON),
		result.RawObjectKey, uint32(result.RawBytes)); err != nil {
		return err
	}
	if err := batch.Send(); err != nil {
		return err
	}
	return s.PutInternalCalls(ctx, activities)
}

// PutInternalCalls 写入派生的内部调用行（§106）。
func (s *Store) PutInternalCalls(ctx context.Context, activities []InternalActivity) error {
	if len(activities) == 0 {
		return nil
	}
	batch, err := s.conn.DB().PrepareBatch(ctx, "INSERT INTO blockchain.internal_calls "+
		"(chain, tx_hash, call_index, depth, trace_path, call_type, activity_type, from_address, to_address, "+
		"value_raw, method_id, gas, gas_used, success, error) VALUES")
	if err != nil {
		return err
	}
	for _, item := range activities {
		success := uint8(0)
		if item.Success {
			success = 1
		}
		if err := batch.Append(strings.ToLower(item.Chain), strings.ToLower(string(item.TxHash)),
			item.Index, item.Depth, item.TracePath, item.Type, item.Activity,
			string(item.From), string(item.To), item.Value, item.MethodID,
			item.Gas, item.GasUsed, success, item.Error); err != nil {
			return err
		}
	}
	return batch.Send()
}

// InternalCalls 读取一笔交易的内部调用（Data Explorer 用，§182）。
func (s *Store) InternalCalls(ctx context.Context, chainSlug string, hash chain.Hash) ([]InternalActivity, error) {
	rows, err := s.conn.DB().Query(ctx,
		"SELECT chain, tx_hash, call_index, depth, trace_path, call_type, activity_type, from_address, "+
			"to_address, value_raw, method_id, gas, gas_used, success, error "+
			"FROM blockchain.internal_calls FINAL WHERE chain = ? AND tx_hash = ? ORDER BY call_index",
		strings.ToLower(chainSlug), strings.ToLower(string(hash)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InternalActivity{}
	for rows.Next() {
		var (
			item                                    InternalActivity
			chainValue, txHash, from, to            string
			callType, activity, tracePath, methodID string
			value, errorMessage                     string
			success                                 uint8
		)
		if err := rows.Scan(&chainValue, &txHash, &item.Index, &item.Depth, &tracePath, &callType,
			&activity, &from, &to, &value, &methodID, &item.Gas, &item.GasUsed, &success, &errorMessage); err != nil {
			return nil, err
		}
		item.Chain = chainValue
		item.TxHash = chain.Hash(txHash)
		item.TracePath = tracePath
		item.Type = callType
		item.Activity = activity
		item.From = chain.Address(from)
		item.To = chain.Address(to)
		item.Value = value
		item.MethodID = methodID
		item.Success = success == 1
		item.Error = errorMessage
		out = append(out, item)
	}
	return out, rows.Err()
}
