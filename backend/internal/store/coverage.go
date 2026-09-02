package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

type BlockInterval struct {
	From int64
	To   int64
}

type coveragePart struct {
	BlockInterval
	Rows      int64
	Bytes     int64
	HeadBlock int64
}

func mergeCoverageParts(parts []coveragePart) []coveragePart {
	if len(parts) == 0 {
		return nil
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].From < parts[j].From })
	out := []coveragePart{parts[0]}
	for _, part := range parts[1:] {
		last := &out[len(out)-1]
		if part.From <= last.To+1 {
			if part.To > last.To {
				last.To = part.To
			}
			last.Rows += part.Rows
			last.Bytes += part.Bytes
			if part.HeadBlock > last.HeadBlock {
				last.HeadBlock = part.HeadBlock
			}
			continue
		}
		out = append(out, part)
	}
	return out
}

// MergeBlockRanges 合并重叠与相邻区间。
func MergeBlockRanges(intervals []BlockInterval) []BlockInterval {
	if len(intervals) == 0 {
		return nil
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].From < intervals[j].From })
	out := []BlockInterval{intervals[0]}
	for _, iv := range intervals[1:] {
		last := &out[len(out)-1]
		if iv.From <= last.To+1 {
			if iv.To > last.To {
				last.To = iv.To
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

func (s *Store) ListCoverage(ctx context.Context, chain, subject, dataset string) ([]model.CoverageRow, error) {
	sql := "SELECT id, chain, subject_type, subject, dataset, from_block, to_block, status, " +
		"provider, row_count, raw_bytes, head_block, committed_at " +
		"FROM dataset_coverage WHERE chain=$1 AND subject=$2 AND status='COMPLETE'"
	args := []any{chain, subject}
	if dataset != "" {
		sql += " AND dataset=$3"
		args = append(args, dataset)
	}
	sql += " ORDER BY from_block"
	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CoverageRow
	for rows.Next() {
		var r model.CoverageRow
		if err := rows.Scan(&r.ID, &r.Chain, &r.SubjectType, &r.Subject, &r.Dataset, &r.FromBlock, &r.ToBlock,
			&r.Status, &r.Provider, &r.RowCount, &r.RawBytes, &r.HeadBlock, &r.CommittedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddCoverage 写入一条 Coverage 并合并该 key 下所有区间（幂等）。
func (s *Store) AddCoverage(ctx context.Context, chain, subject, dataset, provider string, from, to, rows, bytes, head int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := fmt.Sprintf("coverage:%s:%s:%s", chain, subject, dataset)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return err
	}
	rowsq, err := tx.Query(ctx, "SELECT from_block, to_block, row_count, raw_bytes, head_block FROM dataset_coverage "+
		"WHERE chain=$1 AND subject=$2 AND dataset=$3 AND status='COMPLETE' ORDER BY from_block",
		chain, subject, dataset)
	if err != nil {
		return err
	}
	var parts []coveragePart
	for rowsq.Next() {
		var part coveragePart
		if err := rowsq.Scan(&part.From, &part.To, &part.Rows, &part.Bytes, &part.HeadBlock); err != nil {
			rowsq.Close()
			return err
		}
		parts = append(parts, part)
	}
	rowsq.Close()
	if err := rowsq.Err(); err != nil {
		return err
	}
	parts = append(parts, coveragePart{BlockInterval: BlockInterval{From: from, To: to}, Rows: rows, Bytes: bytes, HeadBlock: head})
	merged := mergeCoverageParts(parts)
	if _, err := tx.Exec(ctx, "DELETE FROM dataset_coverage WHERE chain=$1 AND subject=$2 AND dataset=$3 AND status='COMPLETE'",
		chain, subject, dataset); err != nil {
		return err
	}
	for _, part := range merged {
		if _, err := tx.Exec(ctx, "INSERT INTO dataset_coverage "+
			"(id, chain, subject_type, subject, dataset, from_block, to_block, status, provider, row_count, raw_bytes, head_block) "+
			"VALUES ($1,$2,'address',$3,$4,$5,$6,'COMPLETE',$7,$8,$9,$10)",
			uuid.NewString(), chain, subject, dataset, part.From, part.To, provider, part.Rows, part.Bytes, part.HeadBlock); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) CoverageStats(ctx context.Context) (map[string]int64, error) {
	rows, err := s.Pool.Query(ctx, "SELECT dataset, COUNT(*), COALESCE(SUM(to_block-from_block+1),0), COALESCE(SUM(row_count),0) "+
		"FROM dataset_coverage GROUP BY dataset")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var d string
		var n, blocks, rc int64
		if err := rows.Scan(&d, &n, &blocks, &rc); err != nil {
			return nil, err
		}
		out[d+":ranges"] = n
		out[d+":blocks"] = blocks
		out[d+":rows"] = rc
	}
	return out, rows.Err()
}
