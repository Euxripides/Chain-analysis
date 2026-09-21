package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func scanJob(rows pgx.Rows) (*model.DownloadJob, error) {
	j := &model.DownloadJob{}
	var fb, tb pgtype.Int8
	var ft, tt, rt pgtype.Timestamptz
	var stats []byte
	var cb *string
	err := rows.Scan(&j.ID, &j.JobNo, &j.Status, &j.Priority, &j.Source, &j.Mode, &j.RequestedMode, &j.EffectiveMode, &j.ModeReason, &j.ModeChangedAt, &j.Chain, &j.RangeType,
		&fb, &tb, &ft, &tt, &j.Datasets, &j.ReusePolicy, &j.RequestFingerprint, &j.HeadBlock, &cb, &j.Error, &stats,
		&j.OrganizationID, &j.WorkspaceID, &j.ProjectID, &rt, &j.CreatedAt, &j.UpdatedAt, &j.CreatedByIdentity)
	if err != nil {
		return nil, err
	}
	if rt.Valid {
		v := rt.Time
		j.ResolvedAt = &v
	}
	if fb.Valid {
		v := fb.Int64
		j.FromBlock = &v
	}
	if tb.Valid {
		v := tb.Int64
		j.ToBlock = &v
	}
	if ft.Valid {
		v := ft.Time
		j.FromTimestamp = &v
	}
	if tt.Valid {
		v := tt.Time
		j.ToTimestamp = &v
	}
	if cb != nil {
		j.CreatedBy = cb
	}
	if len(stats) > 0 {
		m := map[string]any{}
		_ = json.Unmarshal(stats, &m)
		j.Stats = m
	}
	return j, nil
}

const jobCols = "id, job_no, status, priority, source, mode, requested_mode, effective_mode, mode_reason, mode_changed_at, chain, range_type, from_block, to_block, " +
	"from_timestamp, to_timestamp, datasets, reuse_policy, COALESCE(request_fingerprint,''), head_block, created_by, error, stats, " +
	"organization_id, workspace_id, project_id, resolved_at, created_at, updated_at, created_by_identity"

// CreateJobWithSubjects 创建 Job 与 Subject（四级模型前两级）。
func (s *Store) CreateJobWithSubjects(ctx context.Context, j *model.DownloadJob, subjects []string) ([]model.Subject, error) {
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	rows := make([]model.Subject, 0, len(subjects))
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	mode := j.Mode
	if mode == "" {
		mode = "balanced"
	}
	if j.RequestedMode == "" {
		j.RequestedMode = model.PerformanceFast
	}
	if j.EffectiveMode == "" {
		j.EffectiveMode = j.RequestedMode
	}
	if j.ModeReason == "" {
		j.ModeReason = "default_profile"
	}
	if j.ModeChangedAt.IsZero() {
		j.ModeChangedAt = time.Now()
	}
	stats, _ := json.Marshal(j.Stats)
	if len(stats) == 0 {
		stats = []byte("{}")
	}
	_, err = tx.Exec(ctx, "INSERT INTO download_jobs "+
		"(id, job_no, status, priority, source, mode, requested_mode, effective_mode, mode_reason, mode_changed_at, chain, range_type, from_block, to_block, from_timestamp, to_timestamp, "+
		" datasets, reuse_policy, request_fingerprint, head_block, created_by, created_by_identity, organization_id, workspace_id, project_id, resolved_at, stats) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,NULLIF($19,''),$20,$21,$22,$23,$24,$25,$26,$27::jsonb)",
		j.ID, j.JobNo, j.Status, j.Priority, j.Source, mode, j.RequestedMode, j.EffectiveMode, j.ModeReason, j.ModeChangedAt, j.Chain, j.RangeType, j.FromBlock, j.ToBlock,
		j.FromTimestamp, j.ToTimestamp, j.Datasets, j.ReusePolicy, j.RequestFingerprint, j.HeadBlock, j.CreatedBy, j.CreatedByIdentity,
		j.OrganizationID, j.WorkspaceID, j.ProjectID, j.ResolvedAt, stats)
	if err != nil {
		return nil, fmt.Errorf("insert job: %w", err)
	}

	for _, subj := range subjects {
		r := model.Subject{
			ID:          uuid.NewString(),
			JobID:       j.ID,
			SubjectType: "address",
			Subject:     subj,
			Status:      model.ItemPending,
			Progress:    0,
		}
		if _, err := tx.Exec(ctx, "INSERT INTO download_subjects (id, job_id, subject_type, subject, status, progress, stats) "+
			"VALUES ($1,$2,$3,$4,$5,$6,'{}')",
			r.ID, r.JobID, r.SubjectType, r.Subject, r.Status, r.Progress); err != nil {
			return nil, fmt.Errorf("insert subject: %w", err)
		}
		rows = append(rows, r)
	}
	return rows, tx.Commit(ctx)
}

// FindReusableJob returns an active or completed job with the same normalized
// request. The exact legacy match lets deployments reuse jobs created before
// request fingerprints were introduced.
func (s *Store) FindReusableJob(ctx context.Context, fingerprint, chain, rangeType string, fromBlock, toBlock *int64, fromTime, toTime *time.Time, datasets, subjects []string, orgID, wsID string) (*model.DownloadJob, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+jobCols+` FROM download_jobs j
		WHERE j.organization_id=$11::uuid AND j.workspace_id=$12::uuid
		AND (j.status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','PAUSED','RESUMING','WAITING_USER_CONFIRMATION')
			OR (j.status='COMPLETED' AND (j.range_type<>'full' OR NOT EXISTS (
				SELECT 1 FROM unnest($10::text[]) requested_subject(subject)
				CROSS JOIN unnest($8::text[]) requested_dataset(dataset)
				WHERE NOT EXISTS (SELECT 1 FROM address_dataset_sync_state sync
					WHERE sync.chain=$2 AND sync.subject=requested_subject.subject
					AND sync.dataset=requested_dataset.dataset AND sync.complete_through>=now())
			))))
		AND (j.request_fingerprint=$1 OR (
			j.request_fingerprint IS NULL AND j.chain=$2 AND j.range_type=$3
			AND j.from_block IS NOT DISTINCT FROM $4 AND j.to_block IS NOT DISTINCT FROM $5
			AND j.from_timestamp IS NOT DISTINCT FROM $6 AND j.to_timestamp IS NOT DISTINCT FROM $7
			AND j.datasets @> $8::text[] AND j.datasets <@ $8::text[]
			AND (SELECT COUNT(*) FROM download_subjects ds WHERE ds.job_id=j.id)=$9
			AND NOT EXISTS (SELECT 1 FROM unnest($10::text[]) wanted(subject)
				WHERE NOT EXISTS (SELECT 1 FROM download_subjects ds WHERE ds.job_id=j.id AND ds.subject=wanted.subject))
		)) ORDER BY (j.request_fingerprint=$1) DESC NULLS LAST, j.created_at DESC LIMIT 1`,
		fingerprint, chain, rangeType, fromBlock, toBlock, fromTime, toTime, datasets, len(subjects), subjects, orgID, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	job, err := scanJob(rows)
	// The fingerprint backfill below is a WRITE. Close the result set first: pgx
	// allows only one in-flight statement per connection, and on a *dbtx.Pool
	// bound to the request transaction both statements share one connection, so
	// the UPDATE would fail with "conn busy".
	rows.Close()
	if err == nil && job.RequestFingerprint == "" {
		_, _ = s.Pool.Exec(ctx, "UPDATE download_jobs SET request_fingerprint=$2 WHERE id=$1 AND request_fingerprint IS NULL", job.ID, fingerprint)
		job.RequestFingerprint = fingerprint
	}
	return job, err
}

func (s *Store) GetJob(ctx context.Context, id string) (*model.DownloadJob, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+jobCols+" FROM download_jobs WHERE id=$1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	return scanJob(rows)
}

// ListJobs 返回全局任务列表（下载引擎/后台对账使用，不面向 HTTP 请求）。
//
// HTTP 读取路径必须使用 ListJobsScoped：旧版 GET /api/v1/download-jobs 直接调用
// 本函数，把**所有组织**的任务连同 organization_id / workspace_id 一起返回，
// 是 P0 越权读取的一部分。
func (s *Store) ListJobs(ctx context.Context, status string, limit, offset int) ([]model.DownloadJob, error) {
	return s.listJobs(ctx, "", "", status, limit, offset)
}

// ListJobsScoped 按租户作用域列出任务。wsID 为空表示该组织下全部工作区。
func (s *Store) ListJobsScoped(ctx context.Context, orgID, wsID, status string, limit, offset int) ([]model.DownloadJob, error) {
	return s.listJobs(ctx, orgID, wsID, status, limit, offset)
}

func (s *Store) listJobs(ctx context.Context, orgID, wsID, status string, limit, offset int) ([]model.DownloadJob, error) {
	q := "SELECT j.id, j.job_no, j.status, j.priority, j.source, j.mode, j.requested_mode, j.effective_mode, j.mode_reason, j.mode_changed_at, j.chain, j.range_type, j.from_block, j.to_block, " +
		"j.from_timestamp, j.to_timestamp, j.datasets, j.reuse_policy, j.head_block, j.created_by, j.error, j.stats, " +
		"j.organization_id, j.workspace_id, j.project_id, j.resolved_at, j.created_at, j.updated_at, " +
		"COALESCE(SUM(sh.downloaded_rows) FILTER (WHERE " + EffectiveShardSQL("sh") + "),0), " +
		"COUNT(sh.id) FILTER (WHERE " + EffectiveShardSQL("sh") + "), " +
		"COUNT(sh.id) FILTER (WHERE sh.status='COMPLETED' AND " + EffectiveShardSQL("sh") + ") , " +
		"COALESCE(u.req,0), COALESCE(u.cost,0) " +
		"FROM download_jobs j " +
		"LEFT JOIN download_shards sh ON sh.job_id = j.id " +
		"LEFT JOIN (SELECT job_id, COUNT(*) AS req, COALESCE(SUM(estimated_cost_usd),0) AS cost FROM provider_usage WHERE usage_type<>'trc20_enrichment_summary' GROUP BY job_id) u ON u.job_id = j.id "
	args := []any{}
	conds := []string{}
	if orgID != "" {
		// [FIX 2026-09-13] 原实现两处缺陷：
		//   1) j.organization_id=$1 与 j.workspace_id::text=$1 让同一参数同时处于 uuid / text 上下文，
		//      PG 报 SQLSTATE 42883 "operator does not exist: text = uuid" → /api/v1/download-jobs 500；
		//   2) 第二处引用的是 orgID 的序号（$1），即使消除类型冲突也会留下"workspace 比 org"的**静默语义错误**。
		// 现改为：各自使用**独立且自增的参数序号**，列与参数两侧都显式 uuid 比较（不对索引列整体 ::text，
		// 保持 organization_id / workspace_id 上索引可用）；workspace 为空时**不追加条件**，
		// 由调用方（tenantScopeOr401 解析出的 scope）决定这是否合法的"组织级"范围。
		conds = append(conds, "j.organization_id=$"+fmt.Sprint(len(args)+1)+"::uuid")
		args = append(args, orgID)
		if wsID != "" {
			conds = append(conds, "j.workspace_id=$"+fmt.Sprint(len(args)+1)+"::uuid")
			args = append(args, wsID)
		}
	}
	if status != "" {
		conds = append(conds, "j.status=$"+fmt.Sprint(len(args)+1))
		args = append(args, status)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " GROUP BY j.id, u.req, u.cost ORDER BY j.created_at DESC LIMIT $" + fmt.Sprint(len(args)+1) + " OFFSET $" + fmt.Sprint(len(args)+2)
	args = append(args, limit, offset)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.DownloadJob{}
	for rows.Next() {
		var j model.DownloadJob
		var fb, tb pgtype.Int8
		var ft, tt, rt pgtype.Timestamptz
		var stats []byte
		var cb *string
		var rowsSum, total, done, reqs int64
		var cost float64
		err := rows.Scan(&j.ID, &j.JobNo, &j.Status, &j.Priority, &j.Source, &j.Mode, &j.RequestedMode, &j.EffectiveMode, &j.ModeReason, &j.ModeChangedAt, &j.Chain, &j.RangeType,
			&fb, &tb, &ft, &tt, &j.Datasets, &j.ReusePolicy, &j.HeadBlock, &cb, &j.Error, &stats,
			&j.OrganizationID, &j.WorkspaceID, &j.ProjectID, &rt, &j.CreatedAt, &j.UpdatedAt,
			&rowsSum, &total, &done, &reqs, &cost)
		if err != nil {
			return nil, err
		}
		if fb.Valid {
			v := fb.Int64
			j.FromBlock = &v
		}
		if tb.Valid {
			v := tb.Int64
			j.ToBlock = &v
		}
		if ft.Valid {
			v := ft.Time
			j.FromTimestamp = &v
		}
		if tt.Valid {
			v := tt.Time
			j.ToTimestamp = &v
		}
		if rt.Valid {
			v := rt.Time
			j.ResolvedAt = &v
		}
		if cb != nil {
			j.CreatedBy = cb
		}
		if len(stats) > 0 {
			_ = json.Unmarshal(stats, &j.Stats)
		}
		if j.Stats == nil {
			j.Stats = map[string]any{}
		}
		var progress float32
		if total > 0 {
			progress = float32(done) / float32(total)
		}
		j.Stats["downloaded_rows"] = rowsSum
		j.Stats["total_shards"] = total
		j.Stats["done_shards"] = done
		j.Stats["api_requests"] = reqs
		j.Stats["api_cost_usd"] = cost
		j.Stats["progress"] = progress
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) SetJobStatus(ctx context.Context, id, status string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_jobs SET status=$2, updated_at=now() WHERE id=$1", id, status)
	return err
}

func (s *Store) SetJobStatusError(ctx context.Context, id, status, errMsg string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_jobs SET status=$2, error=$3, updated_at=now() WHERE id=$1", id, status, errMsg)
	return err
}

func (s *Store) SetJobStats(ctx context.Context, id string, stats map[string]any) error {
	b, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, "UPDATE download_jobs SET stats=$2::jsonb, updated_at=now() WHERE id=$1", id, b)
	return err
}

func (s *Store) CountJobNoForDay(ctx context.Context, day time.Time) (int, error) {
	var n int
	// Keep the boundary in the caller's location.  Passing a date-only string
	// to PostgreSQL makes the comparison use the database session timezone;
	// the API job number, however, is formatted from time.Now() in the
	// process timezone.  Around UTC midnight that mismatch made every request
	// calculate zero and reuse DL-YYYYMMDD-0001.
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := start.AddDate(0, 0, 1)
	err := s.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM download_jobs WHERE created_at >= $1 AND created_at < $2",
		start, end).Scan(&n)
	return n, err
}

// ---- Subjects / Datasets ----

func (s *Store) SubjectsByJob(ctx context.Context, jobID string) ([]model.Subject, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, job_id, subject_type, subject, status, progress, stats, created_at, updated_at "+
		"FROM download_subjects WHERE job_id=$1 ORDER BY created_at, subject", jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Subject
	for rows.Next() {
		var r model.Subject
		var stats []byte
		if err := rows.Scan(&r.ID, &r.JobID, &r.SubjectType, &r.Subject, &r.Status, &r.Progress, &stats, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if len(stats) > 0 {
			_ = json.Unmarshal(stats, &r.Stats)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateDatasetsForSubject(ctx context.Context, jobID, subjectID string, datasets []string) ([]model.DatasetItem, error) {
	var out []model.DatasetItem
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, d := range datasets {
		newID := uuid.NewString()
		if _, err := tx.Exec(ctx, "INSERT INTO download_datasets (id, job_id, subject_id, dataset, status, progress, weight, stats) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,'{}') ON CONFLICT (subject_id, dataset) DO NOTHING",
			newID, jobID, subjectID, d, model.ItemPending, 0, 1); err != nil {
			return nil, err
		}
		// 冲突时返回已存在的行，保证 dataset_id 与实际行一致
		var r model.DatasetItem
		var stats []byte
		err := tx.QueryRow(ctx, "SELECT id, job_id, subject_id, dataset, status, progress, weight, stats, created_at, updated_at "+
			"FROM download_datasets WHERE subject_id=$1 AND dataset=$2", subjectID, d).
			Scan(&r.ID, &r.JobID, &r.SubjectID, &r.Dataset, &r.Status, &r.Progress, &r.Weight, &stats, &r.CreatedAt, &r.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if len(stats) > 0 {
			_ = json.Unmarshal(stats, &r.Stats)
		}
		out = append(out, r)
	}
	return out, tx.Commit(ctx)
}

func (s *Store) DatasetsBySubject(ctx context.Context, subjectID string) ([]model.DatasetItem, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, job_id, subject_id, dataset, status, progress, weight, stats, created_at, updated_at "+
		"FROM download_datasets WHERE subject_id=$1 ORDER BY dataset", subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DatasetItem
	for rows.Next() {
		var r model.DatasetItem
		var stats []byte
		if err := rows.Scan(&r.ID, &r.JobID, &r.SubjectID, &r.Dataset, &r.Status, &r.Progress, &r.Weight, &stats, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if len(stats) > 0 {
			_ = json.Unmarshal(stats, &r.Stats)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetDataset(ctx context.Context, id string) (*model.DatasetItem, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, job_id, subject_id, dataset, status, progress, weight, stats, created_at, updated_at "+
		"FROM download_datasets WHERE id=$1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	var r model.DatasetItem
	var stats []byte
	if err := rows.Scan(&r.ID, &r.JobID, &r.SubjectID, &r.Dataset, &r.Status, &r.Progress, &r.Weight, &stats, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	if len(stats) > 0 {
		_ = json.Unmarshal(stats, &r.Stats)
	}
	return &r, rows.Err()
}

func (s *Store) UpdateDatasetStatus(ctx context.Context, id, status string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_datasets SET status=$2, "+
		"progress=CASE WHEN $2 IN ('COMPLETED','SKIPPED') THEN 1 ELSE progress END, updated_at=now() WHERE id=$1", id, status)
	return err
}

// ---- Shards ----

const shardCols = "id, job_id, subject_id, dataset_id, dataset, from_block, to_block, from_timestamp, to_timestamp, page_token, page_no, " +
	"page_size, page_state, status, priority, retries, max_retries, lease_owner, leased_until, downloaded_rows, downloaded_bytes, " +
	"raw_object, error, fetched_at, ingested_at, validated_at, fetch_duration_ms, ingest_duration_ms, wire_bytes, decoded_bytes, " +
	"page_count, page_gap_avg_ms, lease_epoch, work_score, tail_boost, created_at, updated_at, " +
	"provider_key, api_channel, operation_class, routing_policy_key, routing_policy_revision, profile_revision"

func scanShard(rows pgx.Rows) (*model.Shard, error) {
	var r model.Shard
	var lu, fts, tts, fet, igt, vat pgtype.Timestamptz
	var ps []byte
	err := rows.Scan(&r.ID, &r.JobID, &r.SubjectID, &r.DatasetID, &r.Dataset, &r.FromBlock, &r.ToBlock,
		&fts, &tts, &r.PageToken, &r.PageNo, &r.PageSize, &ps, &r.Status, &r.Priority, &r.Retries, &r.MaxRetries, &r.LeaseOwner, &lu,
		&r.DownloadedRows, &r.DownloadedBytes, &r.RawObject, &r.Error, &fet, &igt, &vat,
		&r.FetchDurationMs, &r.IngestDurationMs, &r.WireBytes, &r.DecodedBytes, &r.PageCount, &r.PageGapAvgMs,
		&r.LeaseEpoch, &r.WorkScore, &r.TailBoost,
		&r.CreatedAt, &r.UpdatedAt,
		&r.ProviderKey, &r.APIChannel, &r.OperationClass,
		&r.RoutingPolicyKey, &r.RoutingPolicyRevision, &r.ProfileRevision)
	if err != nil {
		return nil, err
	}
	if len(ps) > 0 && string(ps) != "{}" {
		m := map[string]any{}
		_ = json.Unmarshal(ps, &m)
		r.PageState = m
	}
	if lu.Valid {
		t := lu.Time
		r.LeasedUntil = &t
	}
	if fts.Valid {
		t := fts.Time
		r.FromTimestamp = &t
	}
	if tts.Valid {
		t := tts.Time
		r.ToTimestamp = &t
	}
	if fet.Valid {
		t := fet.Time
		r.FetchedAt = &t
	}
	if igt.Valid {
		t := igt.Time
		r.IngestedAt = &t
	}
	if vat.Valid {
		t := vat.Time
		r.ValidatedAt = &t
	}
	return &r, nil
}

func (s *Store) CreateShards(ctx context.Context, shards []model.Shard) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i := range shards {
		if shards[i].ID == "" {
			shards[i].ID = uuid.NewString()
		}
		ws := shards[i].WorkScore
		if ws <= 0 {
			span := shards[i].ToBlock - shards[i].FromBlock + 1
			if span > 0 {
				ws = math.Round(float64(span)/10000*1000) / 1000 // V1.4 LPT 工作量（§9）
			}
		}
		pageState, _ := json.Marshal(shards[i].PageState)
		if len(pageState) == 0 || string(pageState) == "null" {
			pageState = []byte("{}")
		}
		_, err := tx.Exec(ctx, "INSERT INTO download_shards "+
			"(id, job_id, subject_id, dataset_id, dataset, from_block, to_block, from_timestamp, to_timestamp, page_token, page_no, page_size, page_state, status, priority, max_retries, work_score, tail_boost, "+
			"provider_key, api_channel, operation_class, routing_policy_key, routing_policy_revision, profile_revision) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)",
			shards[i].ID, shards[i].JobID, shards[i].SubjectID, shards[i].DatasetID, shards[i].Dataset,
			shards[i].FromBlock, shards[i].ToBlock, shards[i].FromTimestamp, shards[i].ToTimestamp,
			shards[i].PageToken, shards[i].PageNo, shards[i].PageSize, string(pageState), shards[i].Status,
			shards[i].Priority, shards[i].MaxRetries, ws, shards[i].TailBoost,
			shards[i].ProviderKey, shards[i].APIChannel, shards[i].OperationClass,
			shards[i].RoutingPolicyKey, shards[i].RoutingPolicyRevision, shards[i].ProfileRevision)
		if err != nil {
			return fmt.Errorf("insert shard: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// LeaseShards 领取最多 n 个可调度 Shard（SKIP LOCKED，多 Worker 安全）。
func (s *Store) LeaseShards(ctx context.Context, worker string, n int, leaseFor time.Duration) ([]model.Shard, error) {
	return s.LeaseShardsOrdered(ctx, worker, n, leaseFor, "fifo")
}

// leaseOrderSQL V1.4 Global Ready Queue 排序（§8/§9/§20）：
// fifo = V1.1-V1.3 行为（priority DESC, created_at）；
// lpt/weighted = 重任务优先 + Tail Boost + Aging（created_at 升序自带 FIFO，配合 work_score DESC）。
func leaseOrderSQL(mode string) string {
	switch mode {
	case "lpt", "weighted":
		return "ORDER BY priority DESC, tail_boost DESC, work_score DESC, created_at, id\n"
	default:
		return "ORDER BY priority DESC, created_at, id\n"
	}
}

// LeaseShardsOrdered 领取最多 n 个可调度 Shard（V1.4：mode 可选 fifo|lpt|weighted）。
func (s *Store) LeaseShardsOrdered(ctx context.Context, worker string, n int, leaseFor time.Duration, mode string) ([]model.Shard, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	until := time.Now().Add(leaseFor)
	rows, err := tx.Query(ctx, "WITH picked AS ( "+
		"SELECT id FROM download_shards "+
		"WHERE (status='PENDING' OR (status='FAILED_RETRYABLE' AND updated_at < now() - make_interval(secs => least(retries*30, 300)))) "+
		"AND NOT EXISTS (SELECT 1 FROM download_jobs j WHERE j.id = download_shards.job_id AND j.status NOT IN ('RUNNING','QUEUED','RESUMING')) "+
		"AND NOT EXISTS (SELECT 1 FROM download_subjects su WHERE su.id = download_shards.subject_id AND su.status NOT IN ('PENDING','RUNNING')) "+
		"AND NOT EXISTS (SELECT 1 FROM download_datasets ds WHERE ds.id = download_shards.dataset_id AND ds.status NOT IN ('PENDING','RUNNING')) "+
		"AND NOT EXISTS (SELECT 1 FROM download_volume_guards vg WHERE vg.dataset_id = download_shards.dataset_id AND vg.state IN ('WAITING_USER_CONFIRMATION','ABORTED_BY_USER')) "+
		leaseOrderSQL(mode)+
		"LIMIT $1 "+
		"FOR UPDATE SKIP LOCKED "+
		") "+
		"UPDATE download_shards s SET status='LEASED', lease_owner=$2, leased_until=$3, lease_epoch=lease_epoch+1, updated_at=now() "+
		"FROM picked WHERE s.id = picked.id "+
		"RETURNING s.id, s.job_id, s.subject_id, s.dataset_id, s.dataset, s.from_block, s.to_block, s.from_timestamp, s.to_timestamp, s.page_token, s.page_no, "+
		"s.page_size, s.page_state, s.status, s.priority, s.retries, s.max_retries, s.lease_owner, s.leased_until, s.downloaded_rows, s.downloaded_bytes, "+
		"s.raw_object, s.error, s.fetched_at, s.ingested_at, s.validated_at, s.fetch_duration_ms, s.ingest_duration_ms, s.wire_bytes, s.decoded_bytes, "+
		"s.page_count, s.page_gap_avg_ms, s.lease_epoch, s.work_score, s.tail_boost, s.created_at, s.updated_at, "+
		"s.provider_key, s.api_channel, s.operation_class, s.routing_policy_key, s.routing_policy_revision, s.profile_revision", n, worker, until)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

// SubjectStatus returns the independently controllable address state.
func (s *Store) SubjectStatus(ctx context.Context, jobID, subjectID string) (string, error) {
	var status string
	err := s.Pool.QueryRow(ctx, "SELECT status FROM download_subjects WHERE id=$1 AND job_id=$2", subjectID, jobID).Scan(&status)
	return status, err
}

// ControlSubject pauses, resumes, or force-completes one address without
// disturbing the other addresses in the same job. Force completion preserves
// every ingested row and invalidates active leases before marking the address done.
func (s *Store) ControlSubject(ctx context.Context, jobID, subjectID, action string) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current string
	if err = tx.QueryRow(ctx, "SELECT status FROM download_subjects WHERE id=$1 AND job_id=$2 FOR UPDATE", subjectID, jobID).Scan(&current); err != nil {
		return "", err
	}
	switch action {
	case "pause":
		if current == model.ItemCompleted || current == model.ItemSkipped {
			return "", fmt.Errorf("address is %s", current)
		}
		_, err = tx.Exec(ctx, "UPDATE download_subjects SET status='PAUSED', updated_at=now() WHERE id=$1", subjectID)
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE download_datasets SET status='PAUSED', updated_at=now() WHERE subject_id=$1 AND status NOT IN ('COMPLETED','SKIPPED')", subjectID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE download_shards SET status='PAUSED', lease_owner='', leased_until=NULL, updated_at=now() WHERE subject_id=$1 AND status IN ('PENDING','FAILED_RETRYABLE')", subjectID)
		}
		current = model.ItemPaused
	case "resume":
		if current != model.ItemPaused {
			return "", fmt.Errorf("address is %s", current)
		}
		_, err = tx.Exec(ctx, "UPDATE download_subjects SET status='RUNNING', updated_at=now() WHERE id=$1", subjectID)
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE download_datasets SET status='RUNNING', updated_at=now() WHERE subject_id=$1 AND status='PAUSED'", subjectID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE download_shards SET status='PENDING', updated_at=now() WHERE subject_id=$1 AND status='PAUSED'", subjectID)
		}
		current = model.ItemRunning
	case "complete":
		marker := `{"forced_completed":true,"partial":true}`
		_, err = tx.Exec(ctx, "UPDATE download_shards SET status='CANCELLED', error=$2, lease_epoch=lease_epoch+1, lease_owner='', leased_until=NULL, updated_at=now() WHERE subject_id=$1 AND status<>'COMPLETED'", subjectID, ForcedSubjectCompleteError)
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE download_datasets SET status='COMPLETED', progress=1, stats=COALESCE(stats,'{}'::jsonb)||$2::jsonb, updated_at=now() WHERE subject_id=$1", subjectID, marker)
		}
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE download_subjects SET status='COMPLETED', progress=1, stats=COALESCE(stats,'{}'::jsonb)||$2::jsonb, updated_at=now() WHERE id=$1", subjectID, marker)
		}
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE download_jobs SET stats=COALESCE(stats,'{}'::jsonb)||jsonb_build_object('partial',true), updated_at=now() WHERE id=$1", jobID)
		}
		current = model.ItemCompleted
	default:
		return "", fmt.Errorf("unsupported address action %q", action)
	}
	if err != nil {
		return "", err
	}
	return current, tx.Commit(ctx)
}

func (s *Store) GetShard(ctx context.Context, id string) (*model.Shard, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+shardCols+" FROM download_shards WHERE id=$1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	return scanShard(rows)
}

// CheckpointShard 每页成功后更新 Shard 与 Checkpoint（原子，崩溃安全；V1.1 保存 pageSize 与 PagePolicy 状态）。
func (s *Store) CheckpointShard(ctx context.Context, id string, pageToken string, pageNo, pageSize int, rows, bytes int64, pageState map[string]any) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ps, _ := json.Marshal(pageState)
	if len(ps) == 0 {
		ps = []byte("{}")
	}
	_, err = tx.Exec(ctx, "UPDATE download_shards "+
		"SET status='RUNNING', page_token=$2, page_no=$3, page_size=$4, page_state=$5::jsonb, downloaded_rows=$6, downloaded_bytes=$7, lease_owner=$8, leased_until=$9, error='', updated_at=now() "+
		"WHERE id=$1 AND status<>'CANCELLED' AND EXISTS (SELECT 1 FROM download_subjects su WHERE su.id=download_shards.subject_id AND su.status NOT IN ('COMPLETED','SKIPPED'))",
		id, pageToken, pageNo, pageSize, ps, rows, bytes, "checkpoint", time.Now().Add(10*time.Minute))
	if err != nil {
		return err
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

func (s *Store) CompleteShard(ctx context.Context, id, rawObject string, rows, bytes int64) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_shards "+
		"SET status='COMPLETED', raw_object=$2, downloaded_rows=$3, downloaded_bytes=$4, error='', updated_at=now() "+
		"WHERE id=$1 AND status<>'CANCELLED' AND EXISTS (SELECT 1 FROM download_subjects su WHERE su.id=download_shards.subject_id AND su.status NOT IN ('COMPLETED','SKIPPED'))", id, rawObject, rows, bytes)
	return err
}

func (s *Store) FailShard(ctx context.Context, id, errMsg string, retryable bool) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var retries, maxRetries int
	err = tx.QueryRow(ctx, "SELECT retries, max_retries FROM download_shards WHERE id=$1", id).Scan(&retries, &maxRetries)
	if err != nil {
		return "", err
	}
	newStatus := model.ShardFailedFinal
	if retryable && retries+1 < maxRetries {
		newStatus = model.ShardFailedRetry
	}
	_, err = tx.Exec(ctx, "UPDATE download_shards SET status=$2, retries=retries+1, error=$3, lease_owner='', updated_at=now() WHERE id=$1",
		id, newStatus, errMsg)
	if err != nil {
		return "", err
	}
	return newStatus, tx.Commit(ctx)
}

func (s *Store) PauseShard(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_shards SET status='PAUSED', updated_at=now() WHERE id=$1", id)
	return err
}

func (s *Store) CancelShard(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE download_shards SET status='CANCELLED', updated_at=now() WHERE id=$1", id)
	return err
}

func (s *Store) ShardsByJob(ctx context.Context, jobID string) ([]model.Shard, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+shardCols+" FROM download_shards WHERE job_id=$1 ORDER BY created_at", jobID)
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

func (s *Store) ShardsByDataset(ctx context.Context, datasetID string) ([]model.Shard, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+shardCols+" FROM download_shards WHERE dataset_id=$1 ORDER BY from_block", datasetID)
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

func (s *Store) ShardCountByStatus(ctx context.Context, jobID string) (map[string]int64, error) {
	rows, err := s.Pool.Query(ctx, "SELECT status, COUNT(*) FROM download_shards WHERE job_id=$1 GROUP BY status", jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ResetExpiredLeases 将过期租约的 Shard 收回（Reaper）。
func (s *Store) ResetExpiredLeases(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx, "UPDATE download_shards "+
		"SET status='PENDING', retries=retries+1, lease_owner='', leased_until=NULL, "+
		"error='lease expired, reaped', updated_at=now() "+
		"WHERE status IN ('LEASED','RUNNING') AND leased_until < $1", before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RecomputeDataset 汇总 Shard 得到 Dataset 进度。
//
// 终态必须可达：所有有效 Shard 都进入终态（COMPLETED / FAILED_FINAL /
// CANCELLED）但并非全部成功时，数据集判定为 FAILED —— 否则一个只有
// FAILED_FINAL Shard 的数据集会永远停在 RUNNING，任务永远无法收敛
// （2026-09-15 DL-20260915-0007 现场：25 个 bsc internal_transactions
// 数据集永久 RUNNING，任务卡在 93%）。
func (s *Store) RecomputeDataset(ctx context.Context, datasetID string) error {
	var completed, terminal, total int64
	var rows int64
	err := s.Pool.QueryRow(ctx, "SELECT COUNT(*) FILTER (WHERE status='COMPLETED' AND "+EffectiveShardSQL("")+"), "+
		"COUNT(*) FILTER (WHERE status IN ('COMPLETED','FAILED_FINAL','CANCELLED') AND "+EffectiveShardSQL("")+"), "+
		"COUNT(*) FILTER (WHERE "+EffectiveShardSQL("")+"), "+
		"COALESCE(SUM(downloaded_rows) FILTER (WHERE "+EffectiveShardSQL("")+"),0) "+
		"FROM download_shards WHERE dataset_id=$1", datasetID).
		Scan(&completed, &terminal, &total, &rows)
	if err != nil {
		return err
	}
	status := datasetStatusForTerminal(completed, terminal, total)
	var progress float32
	if total > 0 {
		progress = float32(completed) / float32(total)
	}
	extra := map[string]any{"downloaded_rows": rows}
	if status == model.ItemFailed {
		var reason string
		_ = s.Pool.QueryRow(ctx, "SELECT COALESCE(MAX(error),'') FROM download_shards WHERE dataset_id=$1 AND status IN ('FAILED_FINAL','CANCELLED')", datasetID).Scan(&reason)
		extra["failure_reason"] = reason
	}
	stats, _ := json.Marshal(extra)
	// V1.2：合并而不是覆盖 —— download_datasets.stats 同时承载 Count 预估/决策与 actuals 回写
	_, err = s.Pool.Exec(ctx, "UPDATE download_datasets SET status=CASE WHEN status IN ('WAITING_USER_CONFIRMATION','ABORTED_BY_USER','SKIPPED') THEN status ELSE $2 END, progress=$3, "+
		"stats = COALESCE(stats,'{}'::jsonb) || $4::jsonb, updated_at=now() WHERE id=$1",
		datasetID, status, progress, stats)
	if err != nil {
		return err
	}
	if status == model.ItemCompleted {
		return s.recordCompletedFullSync(ctx, datasetID)
	}
	return nil
}

// RecomputeSubject 按 Dataset 权重汇总 Subject 进度。
func (s *Store) RecomputeSubject(ctx context.Context, subjectID string) error {
	rows, err := s.Pool.Query(ctx, "SELECT status, progress, weight FROM download_datasets WHERE subject_id=$1", subjectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var totalW float64
	var sum float64
	var done, terminal, all int
	anyFailed := false
	for rows.Next() {
		var status string
		var progress, weight float32
		if err := rows.Scan(&status, &progress, &weight); err != nil {
			return err
		}
		totalW += float64(weight)
		sum += float64(progress) * float64(weight)
		all++
		if status == model.ItemCompleted || status == model.ItemSkipped {
			done++
		}
		if status == model.ItemCompleted || status == model.ItemSkipped || status == model.ItemFailed {
			terminal++
		}
		if status == model.ItemFailed {
			anyFailed = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	status := subjectStatusForTerminal(all, done, terminal, anyFailed)
	var progress float32
	if totalW > 0 {
		progress = float32(sum / totalW)
	}
	_, err = s.Pool.Exec(ctx, "UPDATE download_subjects SET status=$2, progress=$3, updated_at=now() WHERE id=$1",
		subjectID, status, progress)
	return err
}

// RecomputeJob 汇总 Subject 得到 Job 进度与状态。
func (s *Store) RecomputeJob(ctx context.Context, jobID string) (float32, error) {
	rows, err := s.Pool.Query(ctx, "SELECT status, progress FROM download_subjects WHERE job_id=$1", jobID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var sum float64
	var n int
	var done, terminal, all int
	anyFailed := false
	for rows.Next() {
		var status string
		var progress float32
		if err := rows.Scan(&status, &progress); err != nil {
			return 0, err
		}
		sum += float64(progress)
		n++
		all++
		if status == model.ItemCompleted || status == model.ItemSkipped {
			done++
		}
		if status == model.ItemCompleted || status == model.ItemSkipped || status == model.ItemFailed {
			terminal++
		}
		if status == model.ItemFailed {
			anyFailed = true
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var progress float32
	if n > 0 {
		progress = float32(sum / float64(n))
	}
	status := jobStatusForTerminal(all, done, terminal, anyFailed, n, progress)
	var jobStatus string
	err = s.Pool.QueryRow(ctx, "SELECT status FROM download_jobs WHERE id=$1", jobID).Scan(&jobStatus)
	if err != nil {
		return 0, err
	}
	// 终态任务也要把列表页使用的 stats.progress 同步到有效 Shard 集合。
	// Tail 加速会把父 Shard 标记为 superseded；若任务先进入终态再调用
	// RecomputeJob，旧的 stats.progress 可能停留在 6/7（85.71%），而实际
	// 有效集合已经是 6/6。同步 total/done/progress，避免 UI 显示假性卡住。
	if jobStatus == model.JobCompleted || jobStatus == model.JobPartialFailed || jobStatus == model.JobFailed {
		var shardTotal, shardDone int64
		if err := s.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FILTER (WHERE "+EffectiveShardSQL("")+"), COUNT(*) FILTER (WHERE status='COMPLETED' AND "+EffectiveShardSQL("")+") FROM download_shards WHERE job_id=$1",
			jobID).Scan(&shardTotal, &shardDone); err == nil {
			shardProgress := float32(0)
			if shardTotal > 0 {
				shardProgress = float32(shardDone) / float32(shardTotal)
			}
			_, err = s.Pool.Exec(ctx,
				"UPDATE download_jobs SET stats=COALESCE(stats,'{}'::jsonb) || jsonb_build_object('progress',$2::double precision,'done_shards',$3::bigint,'total_shards',$4::bigint), updated_at=updated_at WHERE id=$1",
				jobID, shardProgress, shardDone, shardTotal)
			if err != nil {
				return 0, err
			}
		}
	}
	// 终态由 Recompute 决定；运行态不受 pause/cancel 影响
	switch jobStatus {
	case model.JobCompleted, model.JobPartialFailed, model.JobFailed, model.JobCancelled, model.JobAbortedByUser, model.JobWaitingUserConfirmation, model.JobPaused, model.JobPauseReq:
		return progress, nil
	}
	if status == model.JobCompleted || status == model.JobPartialFailed || status == model.JobFailed {
		// Persist the authoritative effective-shard totals in the same update that
		// moves the job to a terminal state. Without this, the last progress sample
		// can remain at 99/100 even though all shards are already completed.
		var shardTotal, shardDone int64
		if qerr := s.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FILTER (WHERE "+EffectiveShardSQL("")+"), COUNT(*) FILTER (WHERE status='COMPLETED' AND "+EffectiveShardSQL("")+") FROM download_shards WHERE job_id=$1",
			jobID).Scan(&shardTotal, &shardDone); qerr == nil {
			shardProgress := float32(0)
			if shardTotal > 0 {
				shardProgress = float32(shardDone) / float32(shardTotal)
			}
			_, err = s.Pool.Exec(ctx,
				"UPDATE download_jobs SET status=$2, stats=COALESCE(stats,'{}'::jsonb) || jsonb_build_object('progress',$3::double precision,'done_shards',$4::bigint,'total_shards',$5::bigint), updated_at=now() WHERE id=$1",
				jobID, status, shardProgress, shardDone, shardTotal)
			return shardProgress, err
		}
		_, err = s.Pool.Exec(ctx, "UPDATE download_jobs SET status=$2, updated_at=now() WHERE id=$1", jobID, status)
		return progress, err
	}
	if jobStatus == model.JobQueued && n > 0 {
		// 有 Shard 开始产出：QUEUED → RUNNING
		_, err = s.Pool.Exec(ctx, "UPDATE download_jobs SET status='RUNNING', updated_at=now() WHERE id=$1", jobID)
		return progress, err
	}
	return progress, nil
}

// 终态收敛判定（纯函数，便于单测）。
//
// 规则：只要所有有效 Shard 都进入终态（COMPLETED / FAILED_FINAL /
// CANCELLED），数据集/地址/任务就必须收敛，而不是无限期停在 RUNNING。
// 2026-09-15 现场：bsc internal_transactions 的 Shard 全部 FAILED_FINAL，
// 数据集永远 RUNNING，DL-20260915-0007/0008/0009 三个任务永远不结束。
func datasetStatusForTerminal(completed, terminal, total int64) string {
	switch {
	case total == 0:
		return model.ItemPending
	case completed == total:
		return model.ItemCompleted
	case terminal == total:
		return model.ItemFailed
	default:
		return model.ItemRunning
	}
}

func subjectStatusForTerminal(all, done, terminal int, anyFailed bool) string {
	switch {
	case all > 0 && done == all && !anyFailed:
		return model.ItemCompleted
	case all > 0 && terminal == all && anyFailed:
		return model.ItemFailed
	default:
		return model.ItemRunning
	}
}

// progress 参与判定：部分地址虽然"有失败数据集"（例如某个 dataset 在当前链上
// 不可用），但整体已经产出了数据时，任务是 PARTIAL_FAILED 而不是 FAILED ——
// DL-20260915-0007 完成了 75/100 个数据集，把整单报成 FAILED 会误导用户。
func jobStatusForTerminal(all, done, terminal int, anyFailed bool, subjects int, progress float32) string {
	switch {
	case all > 0 && done == all && !anyFailed:
		return model.JobCompleted
	case all > 0 && terminal == all && anyFailed && (done > 0 || progress > 0):
		return model.JobPartialFailed
	case all > 0 && terminal == all && anyFailed:
		return model.JobFailed
	case subjects > 0:
		return model.JobRunning
	default:
		return model.JobQueued
	}
}
