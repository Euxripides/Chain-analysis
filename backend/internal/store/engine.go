package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// ---- Ankr 引擎（§6/§39/§80）：页面状态机 + Shard 三阶段时间点 + 画像 ----

// PageDurableCheckpoint 页面 Durable 落定与 Shard Checkpoint 同一事务（§41）：
// pageToken 只能在 DURABLE 后推进，崩溃后二者一致。
func (s *Store) PageDurableCheckpoint(ctx context.Context, p model.Page, pageToken string, pageNo, pageSize int, rows, bytes int64, pageState map[string]any) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ps, _ := json.Marshal(pageState)
	if len(ps) == 0 {
		ps = []byte("{}")
	}
	_, err = tx.Exec(ctx, "UPDATE download_shards "+
		"SET status='RUNNING', page_token=$2, page_no=$3, page_size=$4, page_state=$5::jsonb, downloaded_rows=$6, downloaded_bytes=$7, lease_owner=$8, leased_until=$9, error='', updated_at=now() "+
		"WHERE id=$1",
		p.ShardID, pageToken, pageNo, pageSize, ps, rows, bytes, "checkpoint", time.Now().Add(10*time.Minute))
	if err != nil {
		return "", err
	}
	var jobID string
	var from, to int64
	err = tx.QueryRow(ctx, "SELECT job_id, from_block, to_block FROM download_shards WHERE id=$1", p.ShardID).Scan(&jobID, &from, &to)
	if err != nil {
		return "", err
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	_, err = tx.Exec(ctx, "INSERT INTO download_pages "+
		"(id, job_id, shard_id, dataset, page_no, query_hash, status, page_size, wire_bytes, decoded_bytes, raw_path, raw_sha256, "+
		" rows_fetched, rows_parsed, rows_inserted, unique_rows, next_page_token_hash, from_block, to_block, "+
		" fetch_started_at, body_completed_at, durable_at, ingested_at, validated_at, error) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,0,0,0,$14,$15,$16,$17,$18,$19,NULL,NULL,$20) "+
		"ON CONFLICT (shard_id, page_no) DO UPDATE SET status='DURABLE', page_size=$8, wire_bytes=$9, decoded_bytes=$10, "+
		"raw_path=$11, raw_sha256=$12, rows_fetched=$13, next_page_token_hash=$14, from_block=$15, to_block=$16, "+
		"fetch_started_at=$17, body_completed_at=$18, durable_at=$19, error=$20, updated_at=now()",
		p.ID, jobID, p.ShardID, p.Dataset, p.PageNo, p.QueryHash, model.PageDurable, p.PageSize, p.WireBytes, p.DecodedBytes,
		p.RawPath, p.RawSHA256, p.RowsFetched, p.NextPageTokenHash, p.FromBlock, p.ToBlock,
		p.FetchStartedAt, p.BodyCompletedAt, time.Now(), p.Error)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, "INSERT INTO checkpoints (shard_id, job_id, range_start, range_end, next_page_token, page_no, downloaded_rows, downloaded_bytes, last_success_at, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now(),now()) "+
		"ON CONFLICT (shard_id) DO UPDATE SET next_page_token=$5, page_no=$6, downloaded_rows=$7, downloaded_bytes=$8, last_success_at=now(), updated_at=now()",
		p.ShardID, jobID, from, to, pageToken, pageNo, rows, bytes)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return p.ID, nil
}

// EnsurePageRow 恢复路径：页面 DURABLE 行缺失时补齐（ON CONFLICT DO NOTHING）。
func (s *Store) EnsurePageRow(ctx context.Context, p model.Page) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO download_pages "+
		"(id, job_id, shard_id, dataset, page_no, query_hash, status, page_size, wire_bytes, decoded_bytes, raw_path, raw_sha256, "+
		" rows_fetched, next_page_token_hash, from_block, to_block, fetch_started_at, body_completed_at, durable_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,'DURABLE',$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,now()) "+
		"ON CONFLICT (shard_id, page_no) DO NOTHING",
		p.ID, p.JobID, p.ShardID, p.Dataset, p.PageNo, p.QueryHash, p.PageSize, p.WireBytes, p.DecodedBytes,
		p.RawPath, p.RawSHA256, p.RowsFetched, p.NextPageTokenHash, p.FromBlock, p.ToBlock, p.FetchStartedAt, p.BodyCompletedAt)
	return err
}

func (s *Store) UpdatePageIngested(ctx context.Context, pageID string, parsed, inserted, unique int) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_pages SET status=$2, rows_parsed=$3, rows_inserted=$4, unique_rows=$5, ingested_at=now(), error='', updated_at=now() "+
		"WHERE id=$1", pageID, model.PageIngested, parsed, inserted, unique)
	return err
}

func (s *Store) FailPage(ctx context.Context, pageID, errMsg string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_pages SET status=$2, error=$3, updated_at=now() WHERE id=$1", pageID, model.PageFailed, errMsg)
	return err
}

// FailPageFinal 把一个不可恢复的页标记为终态失败：PagesNeedingIngest 只挑选
// DURABLE / 30 秒前的 FAILED，因此 FAILED_FINAL 页不会被无限重放。
func (s *Store) FailPageFinal(ctx context.Context, pageID, errMsg string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_pages SET status=$2, error=$3, updated_at=now() WHERE id=$1", pageID, model.PageFailedFinal, errMsg)
	return err
}

// PageStatus 返回单页当前状态。spool 文件缺失时用来区分"真的丢了原始对象"和
// "页面其实已经入库、文件只是被正常清理"（DL-20260921-0041 的良性竞态）。
func (s *Store) PageStatus(ctx context.Context, pageID string) (string, error) {
	var status string
	err := s.Pool.QueryRow(ctx, "SELECT status FROM download_pages WHERE id=$1", pageID).Scan(&status)
	return status, err
}

// PageIngestSummary 汇总一个 Shard 的页面终态：
//   ingested  = INGESTED/VALIDATED
//   pending   = 仍可被重放的页（DURABLE 或 30 秒前的 FAILED）
//   permanent = FAILED_FINAL（原始 spool 对象不可恢复）
//
// finalizer 用 permanent>0 && pending==0 判定"这个 Shard 永远不会再进展"。
func (s *Store) PageIngestSummary(ctx context.Context, shardID string) (ingested, pending, permanent int64, lastErr string, err error) {
	err = s.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FILTER (WHERE status IN ('INGESTED','VALIDATED')), "+
			"COUNT(*) FILTER (WHERE status='DURABLE' OR (status='FAILED' AND updated_at < now() - interval '30 seconds')), "+
			"COUNT(*) FILTER (WHERE status=$2), "+
			"COALESCE(MAX(error) FILTER (WHERE status=$2),'') "+
			"FROM download_pages WHERE shard_id=$1", shardID, model.PageFailedFinal).
		Scan(&ingested, &pending, &permanent, &lastErr)
	return
}

func (s *Store) PagesByShard(ctx context.Context, shardID string) ([]model.Page, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, job_id, shard_id, dataset, page_no, query_hash, status, page_size, wire_bytes, decoded_bytes, raw_path, raw_sha256, "+
		"rows_fetched, rows_parsed, rows_inserted, unique_rows, next_page_token_hash, from_block, to_block, "+
		"fetch_started_at, body_completed_at, durable_at, ingested_at, validated_at, error, created_at, updated_at "+
		"FROM download_pages WHERE shard_id=$1 ORDER BY page_no", shardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Page
	for rows.Next() {
		var p model.Page
		var fs, bc, du, ig, va pgtype.Timestamptz
		if err := rows.Scan(&p.ID, &p.JobID, &p.ShardID, &p.Dataset, &p.PageNo, &p.QueryHash, &p.Status, &p.PageSize,
			&p.WireBytes, &p.DecodedBytes, &p.RawPath, &p.RawSHA256, &p.RowsFetched, &p.RowsParsed, &p.RowsInserted,
			&p.UniqueRows, &p.NextPageTokenHash, &p.FromBlock, &p.ToBlock, &fs, &bc, &du, &ig, &va, &p.Error,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if fs.Valid {
			t := fs.Time
			p.FetchStartedAt = &t
		}
		if bc.Valid {
			t := bc.Time
			p.BodyCompletedAt = &t
		}
		if du.Valid {
			t := du.Time
			p.DurableAt = &t
		}
		if ig.Valid {
			t := ig.Time
			p.IngestedAt = &t
		}
		if va.Valid {
			t := va.Time
			p.ValidatedAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PageCountsByShard 返回 (total, ingested)：finalizer 判定 Ingest 完成度。
func (s *Store) PageCountsByShard(ctx context.Context, shardID string) (total, ingested, validated int, err error) {
	err = s.Pool.QueryRow(ctx, "SELECT COUNT(*), COUNT(*) FILTER (WHERE status IN ('INGESTED','VALIDATED')), "+
		"COUNT(*) FILTER (WHERE status='VALIDATED') FROM download_pages WHERE shard_id=$1", shardID).
		Scan(&total, &ingested, &validated)
	return total, ingested, validated, err
}

// PagesNeedingIngest 尚未 Ingest 的页面（crash replay，§43）。
func (s *Store) PagesNeedingIngest(ctx context.Context, shardID string) ([]model.Page, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, job_id, shard_id, dataset, page_no, query_hash, status, page_size, wire_bytes, decoded_bytes, raw_path, raw_sha256, "+
		"rows_fetched, rows_parsed, rows_inserted, unique_rows, next_page_token_hash, from_block, to_block, "+
		"fetch_started_at, body_completed_at, durable_at, ingested_at, validated_at, error, created_at, updated_at "+
		"FROM download_pages WHERE shard_id=$1 AND (status='DURABLE' OR (status='FAILED' AND updated_at < now() - interval '30 seconds')) ORDER BY page_no", shardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Page
	for rows.Next() {
		var p model.Page
		var fs, bc, du, ig, va pgtype.Timestamptz
		if err := rows.Scan(&p.ID, &p.JobID, &p.ShardID, &p.Dataset, &p.PageNo, &p.QueryHash, &p.Status, &p.PageSize,
			&p.WireBytes, &p.DecodedBytes, &p.RawPath, &p.RawSHA256, &p.RowsFetched, &p.RowsParsed, &p.RowsInserted,
			&p.UniqueRows, &p.NextPageTokenHash, &p.FromBlock, &p.ToBlock, &fs, &bc, &du, &ig, &va, &p.Error,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetShardFetched Fetch 完成（§62：FETCHED ≠ COMPLETE）。
func (s *Store) SetShardFetched(ctx context.Context, shardID string, pageCount int, wireBytes, decodedBytes int64, fetchDurationMs, pageGapAvgMs int64) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_shards SET status=$2, fetched_at=now(), page_count=$3, wire_bytes=$4, decoded_bytes=$5, fetch_duration_ms=$6, page_gap_avg_ms=$7, updated_at=now() "+
		"WHERE id=$1", shardID, model.ShardFetched, pageCount, wireBytes, decodedBytes, fetchDurationMs, pageGapAvgMs)
	return err
}

// SetShardIngested 全部页面 Ingest 完成（§62：INGESTED ≠ COMPLETE）。
func (s *Store) SetShardIngested(ctx context.Context, shardID string, ingestDurationMs int64) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_shards SET status=$2, ingested_at=now(), ingest_duration_ms=$3, updated_at=now() "+
		"WHERE id=$1", shardID, model.ShardIngested, ingestDurationMs)
	return err
}

// CompleteShardFinalized Validation 通过后才 COMPLETE（§63 Coverage Finalizer）。
func (s *Store) CompleteShardFinalized(ctx context.Context, shardID, rawObject string, rows, bytes int64, ingestDurationMs int64) error {
	// 状态守卫：只允许 FETCHED/INGESTED → COMPLETED（幂等，多 finalizer 轮次安全）。
	_, err := s.Pool.Exec(ctx, "UPDATE download_shards "+
		"SET status='COMPLETED', validated_at=now(), raw_object=$2, downloaded_rows=$3, downloaded_bytes=$4, ingest_duration_ms=$5, error='', updated_at=now() "+
		"WHERE id=$1 AND status IN ('FETCHED','INGESTED')", shardID, rawObject, rows, bytes, ingestDurationMs)
	return err
}

// ReleaseShard 崩溃恢复：把中断的 RUNNING/LEASED Shard 交还调度队列（checkpoint 续传）。
func (s *Store) ReleaseShard(ctx context.Context, id, reason string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_shards SET status='PENDING', lease_owner='', leased_until=NULL, error=$2, updated_at=now() "+
		"WHERE id=$1 AND status IN ('RUNNING','LEASED')", id, reason)
	return err
}

// SplitPendingShardsForDataset Tail Acceleration（§15）：只能拆尚未开始的明确 block range。
func (s *Store) SplitPendingShardsForDataset(ctx context.Context, datasetID string, maxSplits int, minWindow int64) (int, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, from_block, to_block FROM download_shards "+
		"WHERE dataset_id=$1 AND status='PENDING' AND to_block - from_block + 1 >= $3 ORDER BY to_block - from_block DESC LIMIT $2",
		datasetID, maxSplits, minWindow*2)
	if err != nil {
		return 0, err
	}
	type cand struct {
		id   string
		from int64
		to   int64
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.from, &c.to); err != nil {
			rows.Close()
			return 0, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if len(cands) == 0 {
		return 0, nil
	}
	split := 0
	for _, c := range cands {
		mid := c.from + (c.to-c.from)/2
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			return split, err
		}
		// PENDING 守卫：只有仍为 PENDING 才拆（并发安全）。
		tag, err := tx.Exec(ctx, "UPDATE download_shards SET status='CANCELLED', error=$2, updated_at=now() "+
			"WHERE id=$1 AND status='PENDING'", c.id, TailSplitSupersededError)
		if err != nil || tag.RowsAffected() == 0 {
			_ = tx.Rollback(ctx)
			if err != nil {
				return split, err
			}
			continue
		}
		_, err = tx.Exec(ctx, "INSERT INTO download_shards "+
			"(id, job_id, subject_id, dataset_id, dataset, from_block, to_block, from_timestamp, to_timestamp, status, priority, max_retries) "+
			"SELECT $1, job_id, subject_id, dataset_id, dataset, $2, $3, from_timestamp, to_timestamp, 'PENDING', priority, max_retries FROM download_shards WHERE id=$4",
			uuid.NewString(), c.from, mid, c.id)
		if err != nil {
			_ = tx.Rollback(ctx)
			return split, err
		}
		_, err = tx.Exec(ctx, "INSERT INTO download_shards "+
			"(id, job_id, subject_id, dataset_id, dataset, from_block, to_block, from_timestamp, to_timestamp, status, priority, max_retries) "+
			"SELECT $1, job_id, subject_id, dataset_id, dataset, $2, $3, from_timestamp, to_timestamp, 'PENDING', priority, max_retries FROM download_shards WHERE id=$4",
			uuid.NewString(), mid+1, c.to, c.id)
		if err != nil {
			_ = tx.Rollback(ctx)
			return split, err
		}
		if err := tx.Commit(ctx); err != nil {
			return split, err
		}
		split++
	}
	return split, nil
}

// SplitPendingTimeShardsForDataset accelerates timestamp-bounded provider
// streams such as TronGrid. Only untouched PENDING shards are split, so active
// cursors and durable checkpoints remain valid.
func (s *Store) SplitPendingTimeShardsForDataset(ctx context.Context, datasetID string, maxSplits int, minWindow time.Duration) (int, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, from_timestamp, to_timestamp FROM download_shards "+
		"WHERE dataset_id=$1 AND status='PENDING' AND from_timestamp IS NOT NULL AND to_timestamp IS NOT NULL "+
		"AND to_timestamp - from_timestamp >= make_interval(secs => $3) ORDER BY to_timestamp - from_timestamp DESC LIMIT $2",
		datasetID, maxSplits, minWindow.Seconds())
	if err != nil {
		return 0, err
	}
	type candidate struct {
		id       string
		from, to time.Time
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.from, &c.to); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	split := 0
	for _, c := range candidates {
		first, second, ok := splitTimeInterval(c.from, c.to)
		if !ok {
			continue
		}
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			return split, err
		}
		tag, err := tx.Exec(ctx, "UPDATE download_shards SET status='CANCELLED', error=$2, updated_at=now() WHERE id=$1 AND status='PENDING'", c.id, TailSplitSupersededError)
		if err != nil || tag.RowsAffected() == 0 {
			_ = tx.Rollback(ctx)
			if err != nil {
				return split, err
			}
			continue
		}
		for _, interval := range [][2]time.Time{first, second} {
			_, err = tx.Exec(ctx, "INSERT INTO download_shards "+
				"(id, job_id, subject_id, dataset_id, dataset, from_block, to_block, from_timestamp, to_timestamp, page_size, page_state, status, priority, max_retries, work_score, tail_boost) "+
				"SELECT $1, job_id, subject_id, dataset_id, dataset, from_block, to_block, $2, $3, page_size, page_state, 'PENDING', priority, max_retries, work_score/2, true FROM download_shards WHERE id=$4",
				uuid.NewString(), interval[0], interval[1], c.id)
			if err != nil {
				_ = tx.Rollback(ctx)
				return split, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return split, err
		}
		split++
	}
	return split, nil
}

func splitTimeInterval(from, to time.Time) (first, second [2]time.Time, ok bool) {
	if !from.Before(to) || to.Sub(from) < time.Millisecond {
		return first, second, false
	}
	mid := from.Add(to.Sub(from) / 2).UTC()
	secondFrom := mid.Add(time.Millisecond)
	if secondFrom.After(to) {
		return first, second, false
	}
	return [2]time.Time{from, mid}, [2]time.Time{secondFrom, to}, true
}

// EstimateRowsPerBlock 从 Shard 画像估计密度（Tail ETA / Density-aware Sharding，§12）。
func (s *Store) EstimateRowsPerBlock(ctx context.Context, chain, dataset string) (float64, error) {
	var v float64
	err := s.Pool.QueryRow(ctx, "SELECT COALESCE(AVG(rows_per_block),0) FROM ("+
		"SELECT rows_per_block FROM ankr_shard_profiles WHERE chain=$1 AND dataset=$2 AND rows_per_block > 0 ORDER BY created_at DESC LIMIT 20) t",
		chain, dataset).Scan(&v)
	return v, err
}

// MarkPagesValidated 校验通过后把页面状态升级为 VALIDATED。
func (s *Store) MarkPagesValidated(ctx context.Context, shardID string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_pages SET status='VALIDATED', validated_at=now(), updated_at=now() WHERE shard_id=$1 AND status='INGESTED'", shardID)
	return err
}

// ListShardsByStatus 供 finalizer（FETCHED）与恢复（RUNNING/LEASED）扫描。
func (s *Store) ListShardsByStatus(ctx context.Context, status string, limit int) ([]model.Shard, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+shardCols+" FROM download_shards WHERE status=$1 ORDER BY updated_at LIMIT $2", status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Shard
	for rows.Next() {
		r, err := scanShard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// IngestedRowsByJob 页面级 Ingest 行数（双进度展示，§61）。
func (s *Store) IngestedRowsByJob(ctx context.Context, jobID string) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(rows_inserted),0) FROM download_pages WHERE job_id=$1 AND status IN ('INGESTED','VALIDATED')", jobID).Scan(&n)
	return n, err
}

// FetchedRowsByJob 页面级 Fetch 行数（含正在 Ingest 的页面）。
func (s *Store) FetchedRowsByJob(ctx context.Context, jobID string) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(rows_fetched),0) FROM download_pages WHERE job_id=$1", jobID).Scan(&n)
	return n, err
}

// ---- 画像（§12/§32）----

func (s *Store) UpsertShardProfile(ctx context.Context, p model.ShardProfile) error {
	var rowsPerBlock, bytesPerRow float64
	if p.BlockTo > p.BlockFrom {
		rowsPerBlock = float64(p.Rows) / float64(p.BlockTo-p.BlockFrom+1)
	}
	if p.Rows > 0 {
		bytesPerRow = float64(p.DecodedBytes) / float64(p.Rows)
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_shard_profiles "+
		"(id, chain, dataset, subject_class, block_from, block_to, rows, pages, wire_bytes, decoded_bytes, fetch_duration_ms, ingest_duration_ms, "+
		" rows_per_block, bytes_per_row, page_latency_p50_ms, page_latency_p95_ms, page_gap_avg_ms) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)",
		uuid.NewString(), p.Chain, p.Dataset, p.SubjectClass, p.BlockFrom, p.BlockTo, p.Rows, p.Pages, p.WireBytes, p.DecodedBytes,
		p.FetchDurationMs, p.IngestDurationMs, rowsPerBlock, bytesPerRow, p.PageLatencyP50Ms, p.PageLatencyP95Ms, p.PageGapAvgMs)
	return err
}

func (s *Store) UpsertPageProfile(ctx context.Context, p model.PageProfile) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_page_profiles "+
		"(chain, dataset, query_shape, preferred_page_size, avg_rows, avg_wire_bytes, avg_decoded_bytes, avg_latency_ms, p95_latency_ms, timeout_rate, oversized_rate, pages_observed, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now()) "+
		"ON CONFLICT (chain, dataset, query_shape) DO UPDATE SET preferred_page_size=$4, avg_rows=$5, avg_wire_bytes=$6, avg_decoded_bytes=$7, "+
		"avg_latency_ms=$8, p95_latency_ms=$9, timeout_rate=$10, oversized_rate=$11, pages_observed=$12, updated_at=now()",
		p.Chain, p.Dataset, p.QueryShape, p.PreferredPageSize, p.AvgRows, p.AvgWireBytes, p.AvgDecodedBytes,
		p.AvgLatencyMs, p.P95LatencyMs, p.TimeoutRate, p.OversizedRate, p.PagesObserved)
	return err
}

func (s *Store) GetPageProfile(ctx context.Context, chain, dataset, queryShape string) (*model.PageProfile, error) {
	var p model.PageProfile
	err := s.Pool.QueryRow(ctx, "SELECT chain, dataset, query_shape, preferred_page_size, avg_rows, avg_wire_bytes, avg_decoded_bytes, "+
		"avg_latency_ms, p95_latency_ms, timeout_rate, oversized_rate, pages_observed FROM ankr_page_profiles "+
		"WHERE chain=$1 AND dataset=$2 AND query_shape=$3", chain, dataset, queryShape).
		Scan(&p.Chain, &p.Dataset, &p.QueryShape, &p.PreferredPageSize, &p.AvgRows, &p.AvgWireBytes, &p.AvgDecodedBytes,
			&p.AvgLatencyMs, &p.P95LatencyMs, &p.TimeoutRate, &p.OversizedRate, &p.PagesObserved)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

var _ = fmt.Sprintf
