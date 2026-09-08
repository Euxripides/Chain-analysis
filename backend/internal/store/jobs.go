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

func scanJob(rows pgx.Rows) (*model.DownloadJob, error) {
	j := &model.DownloadJob{}
	var fb, tb pgtype.Int8
	var ft, tt, rt, modeChanged pgtype.Timestamptz
	var stats []byte
	var cb *string
	err := rows.Scan(&j.ID, &j.JobNo, &j.Status, &j.Priority, &j.Source, &j.Chain, &j.RangeType,
		&fb, &tb, &ft, &tt, &j.Datasets, &j.ReusePolicy, &j.HeadBlock, &cb, &j.Error, &stats,
		&j.OrganizationID, &j.WorkspaceID, &j.ProjectID, &j.RequestedMode, &j.EffectiveMode,
		&j.ModeReason, &modeChanged, &rt, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if rt.Valid {
		v := rt.Time
		j.ResolvedAt = &v
	}
	if modeChanged.Valid {
		v := modeChanged.Time
		j.ModeChangedAt = &v
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

const jobCols = "id, job_no, status, priority, source, chain, range_type, from_block, to_block, " +
	"from_timestamp, to_timestamp, datasets, reuse_policy, head_block, created_by, error, stats, " +
	"organization_id, workspace_id, project_id, requested_mode, effective_mode, mode_reason, mode_changed_at, resolved_at, created_at, updated_at"

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

	_, err = tx.Exec(ctx, "INSERT INTO download_jobs "+
		"(id, job_no, status, priority, source, chain, range_type, from_block, to_block, from_timestamp, to_timestamp, "+
		" datasets, reuse_policy, head_block, created_by, organization_id, workspace_id, project_id, requested_mode, effective_mode, mode_reason, mode_changed_at, resolved_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)",
		j.ID, j.JobNo, j.Status, j.Priority, j.Source, j.Chain, j.RangeType, j.FromBlock, j.ToBlock,
		j.FromTimestamp, j.ToTimestamp, j.Datasets, j.ReusePolicy, j.HeadBlock, j.CreatedBy,
		j.OrganizationID, j.WorkspaceID, j.ProjectID, j.RequestedMode, j.EffectiveMode, j.ModeReason, time.Now(), j.ResolvedAt)
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

func (s *Store) ListJobs(ctx context.Context, status string, limit, offset int) ([]model.DownloadJob, error) {
	q := "SELECT j.id, j.job_no, j.status, j.priority, j.source, j.chain, j.range_type, j.from_block, j.to_block, " +
		"j.from_timestamp, j.to_timestamp, j.datasets, j.reuse_policy, j.head_block, j.created_by, j.error, j.stats, " +
		"j.organization_id, j.workspace_id, j.project_id, j.requested_mode, j.effective_mode, j.mode_reason, j.mode_changed_at, j.resolved_at, j.created_at, j.updated_at, " +
		"COALESCE(SUM(sh.downloaded_rows),0), COUNT(sh.id), COUNT(sh.id) FILTER (WHERE sh.status='COMPLETED'), " +
		"COALESCE(u.req,0), COALESCE(u.cost,0) " +
		"FROM download_jobs j " +
		"LEFT JOIN download_shards sh ON sh.job_id = j.id " +
		"LEFT JOIN (SELECT job_id, COUNT(*) AS req, COALESCE(SUM(estimated_cost_usd),0) AS cost FROM provider_usage GROUP BY job_id) u ON u.job_id = j.id "
	args := []any{}
	if status != "" {
		q += " WHERE j.status=$1"
		args = append(args, status)
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
		var ft, tt, rt, modeChanged pgtype.Timestamptz
		var stats []byte
		var cb *string
		var rowsSum, total, done, reqs int64
		var cost float64
		err := rows.Scan(&j.ID, &j.JobNo, &j.Status, &j.Priority, &j.Source, &j.Chain, &j.RangeType,
			&fb, &tb, &ft, &tt, &j.Datasets, &j.ReusePolicy, &j.HeadBlock, &cb, &j.Error, &stats,
			&j.OrganizationID, &j.WorkspaceID, &j.ProjectID, &j.RequestedMode, &j.EffectiveMode,
			&j.ModeReason, &modeChanged, &rt, &j.CreatedAt, &j.UpdatedAt,
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
		if modeChanged.Valid {
			v := modeChanged.Time
			j.ModeChangedAt = &v
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
	err := s.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM download_jobs WHERE created_at >= $1 AND created_at < $1::date + interval '1 day'",
		day.Format("2006-01-02")).Scan(&n)
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
	"raw_object, error, created_at, updated_at"

func scanShard(rows pgx.Rows) (*model.Shard, error) {
	var r model.Shard
	var lu, fts, tts pgtype.Timestamptz
	var ps []byte
	err := rows.Scan(&r.ID, &r.JobID, &r.SubjectID, &r.DatasetID, &r.Dataset, &r.FromBlock, &r.ToBlock,
		&fts, &tts, &r.PageToken, &r.PageNo, &r.PageSize, &ps, &r.Status, &r.Priority, &r.Retries, &r.MaxRetries, &r.LeaseOwner, &lu,
		&r.DownloadedRows, &r.DownloadedBytes, &r.RawObject, &r.Error, &r.CreatedAt, &r.UpdatedAt)
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
		_, err := tx.Exec(ctx, "INSERT INTO download_shards "+
			"(id, job_id, subject_id, dataset_id, dataset, from_block, to_block, from_timestamp, to_timestamp, page_token, page_no, page_size, page_state, status, priority, max_retries) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)",
			shards[i].ID, shards[i].JobID, shards[i].SubjectID, shards[i].DatasetID, shards[i].Dataset,
			shards[i].FromBlock, shards[i].ToBlock, shards[i].FromTimestamp, shards[i].ToTimestamp,
			shards[i].PageToken, shards[i].PageNo, shards[i].PageSize, "{}", shards[i].Status,
			shards[i].Priority, shards[i].MaxRetries)
		if err != nil {
			return fmt.Errorf("insert shard: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// LeaseShards 领取最多 n 个可调度 Shard（SKIP LOCKED，多 Worker 安全）。
func (s *Store) LeaseShards(ctx context.Context, worker string, n int, leaseFor time.Duration) ([]model.Shard, error) {
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
		"ORDER BY priority DESC, created_at "+
		"LIMIT $1 "+
		"FOR UPDATE SKIP LOCKED "+
		") "+
		"UPDATE download_shards s SET status='LEASED', lease_owner=$2, leased_until=$3, updated_at=now() "+
		"FROM picked WHERE s.id = picked.id "+
		"RETURNING s.id, s.job_id, s.subject_id, s.dataset_id, s.dataset, s.from_block, s.to_block, s.from_timestamp, s.to_timestamp, s.page_token, s.page_no, "+
		"s.page_size, s.page_state, s.status, s.priority, s.retries, s.max_retries, s.lease_owner, s.leased_until, s.downloaded_rows, s.downloaded_bytes, "+
		"s.raw_object, s.error, s.created_at, s.updated_at", n, worker, until)
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
		"WHERE id=$1",
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
		"WHERE id=$1", id, rawObject, rows, bytes)
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
func (s *Store) RecomputeDataset(ctx context.Context, datasetID string) error {
	var completed, total int64
	var rows int64
	err := s.Pool.QueryRow(ctx, "SELECT COUNT(*) FILTER (WHERE status='COMPLETED'), COUNT(*), "+
		"COALESCE(SUM(downloaded_rows),0) FROM download_shards WHERE dataset_id=$1", datasetID).
		Scan(&completed, &total, &rows)
	if err != nil {
		return err
	}
	var status string
	switch {
	case total == 0:
		status = model.ItemPending
	case completed == total:
		status = model.ItemCompleted
	default:
		status = model.ItemRunning
	}
	var progress float32
	if total > 0 {
		progress = float32(completed) / float32(total)
	}
	stats, _ := json.Marshal(map[string]any{"downloaded_rows": rows})
	_, err = s.Pool.Exec(ctx, "UPDATE download_datasets SET status=$2, progress=$3, stats=$4::jsonb, updated_at=now() WHERE id=$1",
		datasetID, status, progress, stats)
	return err
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
	var done, all int
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
		if status == model.ItemFailed {
			anyFailed = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	status := model.ItemPending
	switch {
	case all > 0 && done == all && !anyFailed:
		status = model.ItemCompleted
	case anyFailed && done == all:
		status = model.ItemFailed
	default:
		status = model.ItemRunning
	}
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
	var done, all int
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
	var status string
	switch {
	case all > 0 && done == all && !anyFailed:
		status = model.JobCompleted
	case all > 0 && done == all && anyFailed:
		status = model.JobPartialFailed
	case n > 0:
		status = model.JobRunning
	default:
		status = model.JobQueued
	}
	var jobStatus string
	err = s.Pool.QueryRow(ctx, "SELECT status FROM download_jobs WHERE id=$1", jobID).Scan(&jobStatus)
	if err != nil {
		return 0, err
	}
	// 终态由 Recompute 决定；运行态不受 pause/cancel 影响
	switch jobStatus {
	case model.JobCompleted, model.JobPartialFailed, model.JobFailed, model.JobCancelled, model.JobPaused, model.JobPauseReq:
		return progress, nil
	}
	if status == model.JobCompleted || status == model.JobPartialFailed {
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
