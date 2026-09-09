// V1.4 Ankr Fetch：Lease Epoch CAS Checkpoint、Range Certification、Global Profiles、Concurrency Runs、Queue Shadow（§33/§14/§44/§53/§56/§59）。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---- §33/§59：Checkpoint 必须 CAS lease_epoch（旧 Worker epoch 不匹配 → 拒绝提交，防 split-brain） ----

// CheckpointShardCAS 同步链路 checkpoint（epoch CAS；epoch 不匹配 → error，绝不推进 pageToken）。
func (s *Store) CheckpointShardCAS(ctx context.Context, id string, epoch int64, pageToken string, pageNo, pageSize int, rows, bytes int64, pageState map[string]any) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := checkpointRangeGuard(ctx, tx, id); err != nil {
		return err
	}
	ps, _ := json.Marshal(pageState)
	if len(ps) == 0 {
		ps = []byte("{}")
	}
	tag, err := tx.Exec(ctx, "UPDATE download_shards "+
		"SET status='RUNNING', page_token=$2, page_no=$3, page_size=$4, page_state=$5::jsonb, downloaded_rows=$6, downloaded_bytes=$7, lease_owner=$8, leased_until=$9, error='', updated_at=now() "+
		"WHERE id=$1 AND lease_epoch=$10",
		id, pageToken, pageNo, pageSize, ps, rows, bytes, "checkpoint", time.Now().Add(10*time.Minute), epoch)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseEpochMismatch
	}
	var jobID string
	var from, to int64
	err = tx.QueryRow(ctx, "SELECT job_id, from_block, to_block FROM download_shards WHERE id=$1", id).Scan(&jobID, &from, &to)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO checkpoints (shard_id, job_id, range_start, range_end, next_page_token, page_no, downloaded_rows, downloaded_bytes, last_success_at, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now(),now()) "+
		"ON CONFLICT (shard_id) DO UPDATE SET next_page_token=$5, page_no=$6, downloaded_rows=$7, downloaded_bytes=$8, last_success_at=now(), updated_at=now()",
		id, jobID, from, to, pageToken, pageNo, rows, bytes)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PageDurableCheckpointCAS 异步链路 durable checkpoint（epoch CAS）。
func (s *Store) PageDurableCheckpointCAS(ctx context.Context, p model.Page, epoch int64, pageToken string, pageNo, pageSize int, rows, bytes int64, pageState map[string]any) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := checkpointRangeGuard(ctx, tx, p.ShardID); err != nil {
		return "", err
	}
	ps, _ := json.Marshal(pageState)
	if len(ps) == 0 {
		ps = []byte("{}")
	}
	tag, err := tx.Exec(ctx, "UPDATE download_shards "+
		"SET status='RUNNING', page_token=$2, page_no=$3, page_size=$4, page_state=$5::jsonb, downloaded_rows=$6, downloaded_bytes=$7, lease_owner=$8, leased_until=$9, error='', updated_at=now() "+
		"WHERE id=$1 AND lease_epoch=$10",
		p.ShardID, pageToken, pageNo, pageSize, ps, rows, bytes, "checkpoint", time.Now().Add(10*time.Minute), epoch)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrLeaseEpochMismatch
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

// SetShardFetchedCAS 标记 Fetch 完成（epoch CAS）。
func (s *Store) SetShardFetchedCAS(ctx context.Context, shardID string, epoch int64, pageCount int, wireBytes, decodedBytes int64, fetchDurationMs, pageGapAvgMs int64) error {
	tag, err := s.Pool.Exec(ctx, "UPDATE download_shards SET status=$2, fetched_at=now(), page_count=$3, wire_bytes=$4, decoded_bytes=$5, fetch_duration_ms=$6, page_gap_avg_ms=$7, updated_at=now() "+
		"WHERE id=$1 AND lease_epoch=$8", shardID, model.ShardFetched, pageCount, wireBytes, decodedBytes, fetchDurationMs, pageGapAvgMs, epoch)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseEpochMismatch
	}
	return nil
}

// ErrLeaseEpochMismatch epoch 不匹配：worker 已失去 Shard 所有权（reaper/其它 worker 接管），立即停止提交。
var ErrLeaseEpochMismatch = &epochMismatchError{}
var ErrCheckpointRangeMismatch = errors.New("checkpoint range mismatch")

func checkpointRangeGuard(ctx context.Context, tx pgx.Tx, shardID string) error {
	var sf, st int64
	if err := tx.QueryRow(ctx, "SELECT from_block,to_block FROM download_shards WHERE id=$1 FOR UPDATE", shardID).Scan(&sf, &st); err != nil {
		return err
	}
	var cf, ct int64
	err := tx.QueryRow(ctx, "SELECT range_start,range_end FROM checkpoints WHERE shard_id=$1 FOR UPDATE", shardID).Scan(&cf, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if sf != cf || st != ct {
		return fmt.Errorf("%w: shard=%s shard_range=%d..%d checkpoint_range=%d..%d", ErrCheckpointRangeMismatch, shardID, sf, st, cf, ct)
	}
	return nil
}

type epochMismatchError struct{}

func (e *epochMismatchError) Error() string {
	return "lease epoch mismatch: worker lost shard ownership (split-brain prevented)"
}

// IsLeaseEpochMismatch 判断是否为 epoch CAS 失败。
func IsLeaseEpochMismatch(err error) bool      { return err == ErrLeaseEpochMismatch }
func IsCheckpointRangeMismatch(err error) bool { return errors.Is(err, ErrCheckpointRangeMismatch) }

// ---- §14 Range Certification ----

func (s *Store) UpsertRangeCertification(ctx context.Context, c *model.RangeCertification) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_range_certifications "+
		"(chain, dataset, range_supported, range_verified, reference_run, missing, real_duplicates, boundary_violations, union_rows, verification_version, verified_at, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now(), now()) "+
		"ON CONFLICT (chain, dataset) DO UPDATE SET range_supported=$3, range_verified=$4, reference_run=$5, missing=$6, real_duplicates=$7, boundary_violations=$8, union_rows=$9, verification_version=$10, verified_at=now(), updated_at=now()",
		c.Chain, c.Dataset, c.RangeSupported, c.RangeVerified, c.ReferenceRun, c.Missing, c.RealDuplicates, c.BoundaryViolations, c.UnionRows, c.VerificationVersion)
	return err
}

func (s *Store) GetRangeCertification(ctx context.Context, chain, dataset string) (*model.RangeCertification, error) {
	var c model.RangeCertification
	var vt pgtype.Timestamptz
	err := s.Pool.QueryRow(ctx, "SELECT chain, dataset, range_supported, range_verified, reference_run, missing, real_duplicates, boundary_violations, union_rows, verification_version, verified_at, updated_at "+
		"FROM ankr_range_certifications WHERE chain=$1 AND dataset=$2", chain, dataset).
		Scan(&c.Chain, &c.Dataset, &c.RangeSupported, &c.RangeVerified, &c.ReferenceRun, &c.Missing, &c.RealDuplicates, &c.BoundaryViolations, &c.UnionRows, &c.VerificationVersion, &vt, &c.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if vt.Valid {
		c.VerifiedAt = &vt.Time
	}
	return &c, nil
}

func (s *Store) ListRangeCertifications(ctx context.Context) ([]model.RangeCertification, error) {
	rows, err := s.Pool.Query(ctx, "SELECT chain, dataset, range_supported, range_verified, reference_run, missing, real_duplicates, boundary_violations, union_rows, verification_version, verified_at, updated_at "+
		"FROM ankr_range_certifications ORDER BY chain, dataset")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RangeCertification
	for rows.Next() {
		var c model.RangeCertification
		var vt pgtype.Timestamptz
		if err := rows.Scan(&c.Chain, &c.Dataset, &c.RangeSupported, &c.RangeVerified, &c.ReferenceRun, &c.Missing, &c.RealDuplicates, &c.BoundaryViolations, &c.UnionRows, &c.VerificationVersion, &vt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if vt.Valid {
			c.VerifiedAt = &vt.Time
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- §44 Global Fetch Profiles ----

func (s *Store) UpsertGlobalFetchProfile(ctx context.Context, f *model.GlobalFetchProfile) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_global_fetch_profiles "+
		"(chain, dataset_mix, page_size_class, safe_concurrency, best_goodput, p50_ms, p95_ms, rate_limit_rate, timeout_rate, retry_amp, avg_wire_bytes, avg_decoded_bytes, samples, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13, now()) "+
		"ON CONFLICT (chain, dataset_mix, page_size_class) DO UPDATE SET "+
		"safe_concurrency=$4, best_goodput=GREATEST(best_goodput,$5), p50_ms=$6, p95_ms=$7, rate_limit_rate=$8, timeout_rate=$9, retry_amp=$10, avg_wire_bytes=$11, avg_decoded_bytes=$12, samples=$13, updated_at=now()",
		f.Chain, f.DatasetMix, f.PageSizeClass, f.SafeConcurrency, f.BestGoodput, f.P50Ms, f.P95Ms, f.RateLimitRate, f.TimeoutRate, f.RetryAmp, f.AvgWireBytes, f.AvgDecodedBytes, f.Samples)
	return err
}

func (s *Store) ListGlobalFetchProfiles(ctx context.Context) ([]model.GlobalFetchProfile, error) {
	rows, err := s.Pool.Query(ctx, "SELECT chain, dataset_mix, page_size_class, safe_concurrency, best_goodput, p50_ms, p95_ms, rate_limit_rate, timeout_rate, retry_amp, avg_wire_bytes, avg_decoded_bytes, samples, updated_at FROM ankr_global_fetch_profiles ORDER BY chain, dataset_mix")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.GlobalFetchProfile
	for rows.Next() {
		var f model.GlobalFetchProfile
		if err := rows.Scan(&f.Chain, &f.DatasetMix, &f.PageSizeClass, &f.SafeConcurrency, &f.BestGoodput, &f.P50Ms, &f.P95Ms, &f.RateLimitRate, &f.TimeoutRate, &f.RetryAmp, &f.AvgWireBytes, &f.AvgDecodedBytes, &f.Samples, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---- §53 Fetch Concurrency Runs ----

func (s *Store) InsertFetchConcurrencyRun(ctx context.Context, r *model.FetchConcurrencyRun) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_fetch_concurrency_runs "+
		"(id, run_id, chain, dataset_mix, concurrency_target, wall_ms, rows, unique_rows, requests, credits, ttfb_p50_ms, ttfb_p95_ms, body_p50_ms, body_p95_ms, retry_amp, r429_count, timeout_count, wire_bytes, decoded_bytes, max_memory, max_spool, pass, exclude_reason) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)",
		r.ID, r.RunID, r.Chain, r.DatasetMix, r.ConcurrencyTarget, r.WallMs, r.Rows, r.UniqueRows, r.Requests, r.Credits,
		r.TTFBP50Ms, r.TTFBP95Ms, r.BodyP50Ms, r.BodyP95Ms, r.RetryAmp, r.R429Count, r.TimeoutCount, r.WireBytes, r.DecodedBytes, r.MaxMemory, r.MaxSpool, r.Pass, r.ExcludeReason)
	return err
}

func (s *Store) ListFetchConcurrencyRuns(ctx context.Context, limit int) ([]model.FetchConcurrencyRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, "SELECT id, run_id, chain, dataset_mix, concurrency_target, wall_ms, rows, unique_rows, requests, credits, ttfb_p50_ms, ttfb_p95_ms, body_p50_ms, body_p95_ms, retry_amp, r429_count, timeout_count, wire_bytes, decoded_bytes, max_memory, max_spool, pass, exclude_reason, created_at FROM ankr_fetch_concurrency_runs ORDER BY created_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FetchConcurrencyRun
	for rows.Next() {
		var r model.FetchConcurrencyRun
		if err := rows.Scan(&r.ID, &r.RunID, &r.Chain, &r.DatasetMix, &r.ConcurrencyTarget, &r.WallMs, &r.Rows, &r.UniqueRows, &r.Requests, &r.Credits, &r.TTFBP50Ms, &r.TTFBP95Ms, &r.BodyP50Ms, &r.BodyP95Ms, &r.RetryAmp, &r.R429Count, &r.TimeoutCount, &r.WireBytes, &r.DecodedBytes, &r.MaxMemory, &r.MaxSpool, &r.Pass, &r.ExcludeReason, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- §56 Queue Shadow / Ready Queue Stats / Tail Boost ----

func (s *Store) InsertFetchQueueDiff(ctx context.Context, d *model.FetchQueueDiff) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_fetch_queue_diffs (id, sampled_ready, actual_first_shard, v14_first_shard, order_mismatch, mismatch_rank) "+
		"VALUES ($1,$2,$3,$4,$5,$6)", d.ID, d.SampledReady, d.ActualFirstShard, d.V14FirstShard, d.OrderMismatch, d.MismatchRank)
	return err
}

// ReadyQueueStats 可调度/重试队列统计（§52 API/Metrics 数据源）。
func (s *Store) ReadyQueueStats(ctx context.Context) (pending, retryReady, leased int64, err error) {
	err = s.Pool.QueryRow(ctx, "SELECT COUNT(*) FILTER (WHERE status='PENDING'), "+
		"COUNT(*) FILTER (WHERE status='FAILED_RETRYABLE' AND updated_at < now() - make_interval(secs => least(retries*30, 300))), "+
		"COUNT(*) FILTER (WHERE status IN ('LEASED','RUNNING')) FROM download_shards").Scan(&pending, &retryReady, &leased)
	return
}

// PreviewLeaseOrder 只读预览 V1.4 顺序前 n 个可调度 shard（Shadow §56 对照用，不改状态）。
func (s *Store) PreviewLeaseOrder(ctx context.Context, n int, mode string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id FROM download_shards "+
		"WHERE (status='PENDING' OR (status='FAILED_RETRYABLE' AND updated_at < now() - make_interval(secs => least(retries*30, 300)))) "+
		"AND NOT EXISTS (SELECT 1 FROM download_jobs j WHERE j.id = download_shards.job_id AND j.status NOT IN ('RUNNING','QUEUED','RESUMING')) "+
		leaseOrderSQL(mode)+" LIMIT $1", n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MarkTailBoostShards 数据集进入 tail（剩 <=n 个未完成 shard）时提升其 PENDING shard（§9 Tail Boost）。
func (s *Store) MarkTailBoostShards(ctx context.Context, datasetID string) (int64, error) {
	tag, err := s.Pool.Exec(ctx, "UPDATE download_shards SET tail_boost=true, updated_at=now() WHERE dataset_id=$1 AND status='PENDING'", datasetID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ClearTailBoostByJob 其它数据集恢复并行后清除 boost 标记。
func (s *Store) ClearTailBoostByJob(ctx context.Context, jobID string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_shards SET tail_boost=false, updated_at=now() WHERE job_id=$1 AND status='PENDING'", jobID)
	return err
}

// RemainingPendingShards 返回 dataset 内未完成 shard 数（tail 检测）。
func (s *Store) RemainingPendingShards(ctx context.Context, datasetID string) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM download_shards WHERE dataset_id=$1 AND status IN ('PENDING','FAILED_RETRYABLE','LEASED','RUNNING','FETCHED','INGESTED')", datasetID).Scan(&n)
	return n, err
}
