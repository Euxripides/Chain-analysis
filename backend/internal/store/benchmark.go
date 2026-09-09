package store

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// ---- V1.1 Benchmark 持久化 + Sweet Spot 画像 ----

func (s *Store) CreateBenchmarkRun(ctx context.Context, b *model.BenchmarkRun) (string, error) {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	var jobID any
	if b.JobID != "" {
		jobID = b.JobID
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_benchmark_runs "+
		"(id, chain, dataset, address_hash, target_block, shard_count, page_size, job_id, status) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'RUNNING')",
		b.ID, b.Chain, b.Dataset, b.AddressHash, b.TargetBlock, b.ShardCount, b.PageSize, jobID)
	return b.ID, err
}

func scanBenchmark(rows pgx.Rows) (*model.BenchmarkRun, error) {
	var b model.BenchmarkRun
	var jobID pgtype.UUID
	var ev []byte
	err := rows.Scan(&b.ID, &b.Chain, &b.Dataset, &b.AddressHash, &b.TargetBlock, &b.ShardCount, &b.PageSize,
		&jobID, &b.Status, &b.WallClockMs, &b.Rows, &b.UniqueRows, &b.Requests, &b.Attempts, &b.Credits, &b.Cost,
		&b.P50Ms, &b.P95Ms, &b.P99Ms, &b.PageGapAvgMs, &b.PageGapP95Ms, &b.PeakActiveShards,
		&b.RetryRate, &b.TimeoutRate, &b.RateLimitRate, &b.Error429, &b.ErrorConnLimit, &b.ErrorTimeout, &b.ErrorOversized,
		&b.PeakRSS, &b.SpoolPeakBytes, &b.CHP95Ms, &b.RawOverlapRows, &b.BoundaryMissing, &ev,
		&b.IntegrityStatus, &b.ExcludedFromLearning, &b.Error,
		&b.ProviderLatencyP50Ms, &b.ProviderLatencyP95Ms, &b.ProviderTTFBP50Ms, &b.ProviderTTFBP95Ms,
		&b.ProviderBodyP50Ms, &b.ProviderBodyP95Ms, &b.NormalizedWallClockMs, &b.ProviderWindowRatio, &b.ExcludeReason,
		&b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if jobID.Valid {
		b.JobID = jobID.String()
	}
	if len(ev) > 0 {
		m := map[string]any{}
		_ = json.Unmarshal(ev, &m)
		b.BoundaryEvidence = m
	}
	return &b, nil
}

const benchCols = "id, chain, dataset, address_hash, target_block, shard_count, page_size, job_id, status, " +
	"wall_clock_ms, rows, unique_rows, requests, attempts, credits, cost, p50_ms, p95_ms, p99_ms, " +
	"page_gap_avg_ms, page_gap_p95_ms, peak_active_shards, retry_rate, timeout_rate, rate_limit_rate, " +
	"error_429, error_conn_limit, error_timeout, error_oversized, peak_rss, spool_peak_bytes, ch_p95_ms, " +
	"raw_overlap_rows, boundary_missing, boundary_evidence, integrity_status, excluded_from_learning, error, " +
	"provider_latency_p50_ms, provider_latency_p95_ms, provider_ttfb_p50_ms, provider_ttfb_p95_ms, " +
	"provider_body_p50_ms, provider_body_p95_ms, normalized_wall_clock_ms, provider_window_ratio, exclude_reason, created_at, updated_at"

func (s *Store) GetBenchmarkRun(ctx context.Context, id string) (*model.BenchmarkRun, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+benchCols+" FROM ankr_benchmark_runs WHERE id=$1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	return scanBenchmark(rows)
}

func (s *Store) ListBenchmarkRuns(ctx context.Context, chain, dataset string, limit int) ([]model.BenchmarkRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := "SELECT " + benchCols + " FROM ankr_benchmark_runs"
	args := []any{}
	if chain != "" || dataset != "" {
		q += " WHERE chain=$1 AND dataset=$2"
		args = append(args, chain, dataset)
	}
	q += " ORDER BY created_at DESC LIMIT $" + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.BenchmarkRun{}
	for rows.Next() {
		b, err := scanBenchmark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// UpdateBenchmarkRun 终局回写（指标 + 完整性 + 学习资格）。
func (s *Store) UpdateBenchmarkRun(ctx context.Context, b *model.BenchmarkRun) error {
	ev, _ := json.Marshal(b.BoundaryEvidence)
	if len(ev) == 0 {
		ev = []byte("{}")
	}
	_, err := s.Pool.Exec(ctx, "UPDATE ankr_benchmark_runs SET "+
		"status=$2, wall_clock_ms=$3, rows=$4, unique_rows=$5, requests=$6, attempts=$7, credits=$8, cost=$9, "+
		"p50_ms=$10, p95_ms=$11, p99_ms=$12, page_gap_avg_ms=$13, page_gap_p95_ms=$14, peak_active_shards=$15, "+
		"retry_rate=$16, timeout_rate=$17, rate_limit_rate=$18, error_429=$19, error_conn_limit=$20, error_timeout=$21, error_oversized=$22, "+
		"peak_rss=$23, spool_peak_bytes=$24, ch_p95_ms=$25, raw_overlap_rows=$26, boundary_missing=$27, boundary_evidence=$28::jsonb, "+
		"integrity_status=$29, excluded_from_learning=$30, error=$31, "+
		"provider_latency_p50_ms=$32, provider_latency_p95_ms=$33, provider_ttfb_p50_ms=$34, provider_ttfb_p95_ms=$35, "+
		"provider_body_p50_ms=$36, provider_body_p95_ms=$37, normalized_wall_clock_ms=$38, provider_window_ratio=$39, exclude_reason=$40, "+
		"updated_at=now() WHERE id=$1",
		b.ID, b.Status, b.WallClockMs, b.Rows, b.UniqueRows, b.Requests, b.Attempts, b.Credits, b.Cost,
		b.P50Ms, b.P95Ms, b.P99Ms, b.PageGapAvgMs, b.PageGapP95Ms, b.PeakActiveShards,
		b.RetryRate, b.TimeoutRate, b.RateLimitRate, b.Error429, b.ErrorConnLimit, b.ErrorTimeout, b.ErrorOversized,
		b.PeakRSS, b.SpoolPeakBytes, b.CHP95Ms, b.RawOverlapRows, b.BoundaryMissing, ev,
		b.IntegrityStatus, b.ExcludedFromLearning, b.Error,
		b.ProviderLatencyP50Ms, b.ProviderLatencyP95Ms, b.ProviderTTFBP50Ms, b.ProviderTTFBP95Ms,
		b.ProviderBodyP50Ms, b.ProviderBodyP95Ms, b.NormalizedWallClockMs, b.ProviderWindowRatio, b.ExcludeReason)
	return err
}

// IntegrityPassRunsForLearning 供 Sweet Spot 学习：同 (chain,dataset) 最近 PASS 且未排除的样本（§22/§23）。
func (s *Store) IntegrityPassRunsForLearning(ctx context.Context, chain, dataset string, limit int) ([]model.BenchmarkRun, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+benchCols+" FROM ankr_benchmark_runs "+
		"WHERE chain=$1 AND dataset=$2 AND integrity_status='PASS' AND excluded_from_learning=false "+
		"ORDER BY created_at DESC LIMIT $3", chain, dataset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.BenchmarkRun{}
	for rows.Next() {
		b, err := scanBenchmark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// UpsertSweetSpotProfile 聚合样本写 Sweet Spot 画像（§20：preferred_shard_count）。
func (s *Store) UpsertSweetSpotProfile(ctx context.Context, chain, dataset, shape, densityClass string,
	preferredShards int, avgWallMs, avgRows, avgPages, providerAvgMs, providerP95Ms int64,
	retryRate, timeoutRate, rateLimitRate float64, samples int) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO ankr_sweet_spot_profiles "+
		"(chain, dataset, query_shape, density_class, preferred_shard_count, avg_wall_clock_ms, avg_rows, avg_pages, "+
		" provider_latency_avg_ms, provider_latency_p95_ms, retry_rate, timeout_rate, rate_limit_rate, sample_count, updated_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,now()) "+
		"ON CONFLICT (chain, dataset, query_shape, density_class) DO UPDATE SET "+
		"preferred_shard_count=$5, avg_wall_clock_ms=$6, avg_rows=$7, avg_pages=$8, provider_latency_avg_ms=$9, provider_latency_p95_ms=$10, "+
		"retry_rate=$11, timeout_rate=$12, rate_limit_rate=$13, sample_count=$14, updated_at=now()",
		chain, dataset, shape, densityClass, preferredShards, avgWallMs, avgRows, avgPages,
		providerAvgMs, providerP95Ms, retryRate, timeoutRate, rateLimitRate, samples)
	return err
}

// GetSweetSpotProfile 生产规划用：Profile HIT → preferred_shard_count（§24）。
// 返回 (preferred, sample_count, err)：样本不足 3 个不参与生产决策（§23）。
func (s *Store) GetSweetSpotProfile(ctx context.Context, chain, dataset, shape, densityClass string) (int, int, error) {
	var n, samples int
	err := s.Pool.QueryRow(ctx, "SELECT preferred_shard_count, sample_count FROM ankr_sweet_spot_profiles "+
		"WHERE chain=$1 AND dataset=$2 AND query_shape=$3 AND density_class=$4", chain, dataset, shape, densityClass).Scan(&n, &samples)
	return n, samples, err
}

// ListBenchmarkRunning 运行中的 benchmark（worker 监控循环用）。
func (s *Store) ListBenchmarkRunning(ctx context.Context, limit int) ([]model.BenchmarkRun, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+benchCols+" FROM ankr_benchmark_runs WHERE status='RUNNING' ORDER BY created_at LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.BenchmarkRun{}
	for rows.Next() {
		b, err := scanBenchmark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (s *Store) CountActiveShardsForJob(ctx context.Context, jobID string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM download_shards WHERE job_id=$1 AND status IN ('LEASED','RUNNING','FETCHED','INGESTED')", jobID).Scan(&n)
	return n, err
}

// UsageStats 基准用量汇总。
type UsageStats struct {
	Requests       int
	Attempts       int
	Retries        int
	Credits        float64
	Cost           float64
	Error429       int
	ErrorConnLimit int
	ErrorTimeout   int
	ErrorOversized int
	Durations      []int
	// V1.2（§55）：Provider Latency Context —— TTFB / Body 拆分。
	TTFBDurations []int
	BodyDurations []int
}

func (s *Store) BenchUsageStats(ctx context.Context, jobID string) (*UsageStats, error) {
	u := &UsageStats{}
	err := s.Pool.QueryRow(ctx, "SELECT COUNT(*) FILTER (WHERE retry_attempt=0), COUNT(*), "+
		"COUNT(*) FILTER (WHERE status<>'ok'), COALESCE(SUM(credits),0), COALESCE(SUM(estimated_cost_usd),0), "+
		"COUNT(*) FILTER (WHERE error_class='rate_limit'), COUNT(*) FILTER (WHERE error_class='connection_limit'), "+
		"COUNT(*) FILTER (WHERE error_class IN ('request_timeout','body_idle_timeout','overall_timeout','connect_timeout','header_timeout')), "+
		"COUNT(*) FILTER (WHERE error_class='response_too_large') "+
		"FROM provider_usage WHERE job_id=$1 AND usage_type<>'trc20_enrichment_summary'", jobID).
		Scan(&u.Requests, &u.Attempts, &u.Retries, &u.Credits, &u.Cost,
			&u.Error429, &u.ErrorConnLimit, &u.ErrorTimeout, &u.ErrorOversized)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT duration_ms, ttfb_ms, body_ms FROM provider_usage WHERE job_id=$1 AND status='ok' AND usage_type<>'trc20_enrichment_summary'", jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d, ttfb, body int
		if err := rows.Scan(&d, &ttfb, &body); err != nil {
			return nil, err
		}
		u.Durations = append(u.Durations, d)
		if ttfb > 0 {
			u.TTFBDurations = append(u.TTFBDurations, ttfb)
		}
		if body > 0 {
			u.BodyDurations = append(u.BodyDurations, body)
		}
	}
	return u, rows.Err()
}

// BenchPageStats 页面行数与一致性 + Page Gap（均值 / 近似 p95）。
func (s *Store) BenchPageStats(ctx context.Context, jobID string) (rows, parsed, inserted, gapAvg, gapMax int64, err error) {
	err = s.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(rows_fetched),0), COALESCE(SUM(rows_parsed),0), COALESCE(SUM(rows_inserted),0) "+
		"FROM download_pages WHERE job_id=$1", jobID).Scan(&rows, &parsed, &inserted)
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	var pagesN int64
	err = s.Pool.QueryRow(ctx, "SELECT COALESCE(AVG(page_gap_avg_ms),0), COALESCE(MAX(page_gap_avg_ms),0), COUNT(*) "+
		"FROM download_shards WHERE job_id=$1 AND page_gap_avg_ms > 0", jobID).Scan(&gapAvg, &gapMax, &pagesN)
	if err != nil {
		return rows, parsed, inserted, 0, 0, nil
	}
	return rows, parsed, inserted, gapAvg, gapMax, nil
}

// ValidatedStatsByJob Validate 进度（V1.1 §30）：validated_pages/shards/rows + 状态。
func (s *Store) ValidatedStatsByJob(ctx context.Context, jobID string) (pages, shards, rows int64, err error) {
	err = s.Pool.QueryRow(ctx, "SELECT COUNT(*) FILTER (WHERE status='VALIDATED'), COALESCE(SUM(rows_inserted) FILTER (WHERE status='VALIDATED'),0) "+
		"FROM download_pages WHERE job_id=$1", jobID).Scan(&pages, &rows)
	if err != nil {
		return 0, 0, 0, err
	}
	var sh int64
	err = s.Pool.QueryRow(ctx, "SELECT COUNT(*) FILTER (WHERE status='COMPLETED') FROM download_shards WHERE job_id=$1", jobID).Scan(&sh)
	return pages, sh, rows, err
}

// PageGapStatsByJob 活跃页面 Page Gap 均值/P95（§14/§28）。
func (s *Store) PageGapStatsByJob(ctx context.Context, jobID string) (avgMs, p95Ms int64, err error) {
	var n int64
	err = s.Pool.QueryRow(ctx, "SELECT page_gap_avg_ms FROM download_shards WHERE job_id=$1 ORDER BY updated_at DESC LIMIT 1", jobID).Scan(&avgMs)
	if err != nil {
		return 0, 0, err
	}
	_ = n
	return avgMs, avgMs * 2, nil // p95 近似（详细 p95 由 Benchmark 页级计算）
}
