package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

// V1.3 Method / ABI 控制面存储（实现 method.Store）。

func (s *Store) ListMethodSignatures(ctx context.Context) ([]method.SignatureRecord, error) {
	rows, err := s.Pool.Query(ctx,
		"SELECT selector, method_name, signature, source, verified FROM method_signatures")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []method.SignatureRecord{}
	for rows.Next() {
		var r method.SignatureRecord
		if err := rows.Scan(&r.Selector, &r.Name, &r.Signature, &r.Source, &r.Verified); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CandidatesBySelector(ctx context.Context, selector string) ([]method.SignatureRecord, error) {
	rows, err := s.Pool.Query(ctx,
		"SELECT selector, method_name, signature, source, verified FROM method_signatures WHERE selector = $1 "+
			"ORDER BY verified DESC, signature", strings.ToLower(selector))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []method.SignatureRecord{}
	for rows.Next() {
		var r method.SignatureRecord
		if err := rows.Scan(&r.Selector, &r.Name, &r.Signature, &r.Source, &r.Verified); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SearchMethodSignatures 支持 Method Name / Selector / Signature 三种查询（§28）。
func (s *Store) SearchMethodSignatures(ctx context.Context, query string, limit int) ([]method.SignatureRecord, error) {
	term := strings.TrimSpace(query)
	if term == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if selector := method.NormalizeSelector(term); selector != "" {
		return s.CandidatesBySelector(ctx, selector)
	}
	rows, err := s.Pool.Query(ctx,
		"SELECT selector, method_name, signature, source, verified FROM method_signatures "+
			"WHERE lower(method_name) LIKE $1 OR lower(signature) LIKE $1 "+
			"ORDER BY verified DESC, occurrence_count DESC, method_name LIMIT $2",
		"%"+strings.ToLower(term)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []method.SignatureRecord{}
	for rows.Next() {
		var r method.SignatureRecord
		if err := rows.Scan(&r.Selector, &r.Name, &r.Signature, &r.Source, &r.Verified); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertMethodSignatures 幂等写入。唯一约束是 (selector, signature)，
// 因此同一 selector 的多个候选会全部保留（§10）。
func (s *Store) UpsertMethodSignatures(ctx context.Context, records []method.SignatureRecord) (int64, error) {
	if len(records) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, r := range records {
		if r.Selector == "" || r.Signature == "" {
			continue
		}
		batch.Queue("INSERT INTO method_signatures (selector, method_name, signature, source, verified) "+
			"VALUES ($1,$2,$3,$4,$5) ON CONFLICT (selector, signature) DO UPDATE SET "+
			"method_name = EXCLUDED.method_name, "+
			"source = CASE WHEN method_signatures.source = 'manual_override' THEN method_signatures.source ELSE EXCLUDED.source END, "+
			"verified = method_signatures.verified OR EXCLUDED.verified, updated_at = now()",
			strings.ToLower(r.Selector), r.Name, r.Signature, r.Source, r.Verified)
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	results := s.Pool.SendBatch(ctx, batch)
	var affected int64
	for i := 0; i < batch.Len(); i++ {
		tag, err := results.Exec()
		if err != nil {
			_ = results.Close()
			return affected, err
		}
		affected += tag.RowsAffected()
	}
	return affected, results.Close()
}

func (s *Store) BumpSignatureOccurrence(ctx context.Context, selector string, delta int64) error {
	_, err := s.Pool.Exec(ctx,
		"UPDATE method_signatures SET occurrence_count = occurrence_count + $2, updated_at = now() WHERE selector = $1",
		strings.ToLower(selector), delta)
	return err
}

func (s *Store) ListContractABIs(ctx context.Context, limit int) ([]method.ABIRecord, error) {
	if limit <= 0 {
		limit = 20000
	}
	rows, err := s.Pool.Query(ctx,
		"SELECT chain, contract_address, abi_json, source, verified, COALESCE(implementation_address,''), updated_at "+
			"FROM contract_abis ORDER BY verified DESC, updated_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []method.ABIRecord{}
	for rows.Next() {
		var r method.ABIRecord
		if err := rows.Scan(&r.Chain, &r.ContractAddress, &r.ABIJSON, &r.Source, &r.Verified,
			&r.ImplementationAddress, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertContractABI(ctx context.Context, record method.ABIRecord) error {
	var implementation any
	if record.ImplementationAddress != "" {
		implementation = strings.ToLower(record.ImplementationAddress)
	}
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO contract_abis (chain, contract_address, abi_json, source, verified, implementation_address, fetched_at) "+
			"VALUES ($1,$2,$3,$4,$5,$6,now()) ON CONFLICT (chain, contract_address, source) DO UPDATE SET "+
			"abi_json = EXCLUDED.abi_json, verified = EXCLUDED.verified, "+
			"implementation_address = COALESCE(EXCLUDED.implementation_address, contract_abis.implementation_address), "+
			"fetched_at = now(), updated_at = now()",
		strings.ToLower(record.Chain), strings.ToLower(record.ContractAddress), record.ABIJSON,
		record.Source, record.Verified, implementation)
	return err
}

func (s *Store) ListEventSignatures(ctx context.Context) ([]method.EventSignatureRecord, error) {
	rows, err := s.Pool.Query(ctx, "SELECT topic0, event_name, signature, source, verified FROM event_signatures")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []method.EventSignatureRecord{}
	for rows.Next() {
		var r method.EventSignatureRecord
		if err := rows.Scan(&r.Topic0, &r.Name, &r.Signature, &r.Source, &r.Verified); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertEventSignatures(ctx context.Context, records []method.EventSignatureRecord) (int64, error) {
	if len(records) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, r := range records {
		if r.Topic0 == "" || r.Signature == "" {
			continue
		}
		batch.Queue("INSERT INTO event_signatures (topic0, event_name, signature, source, verified) "+
			"VALUES ($1,$2,$3,$4,$5) ON CONFLICT (topic0, signature) DO UPDATE SET "+
			"event_name = EXCLUDED.event_name, verified = event_signatures.verified OR EXCLUDED.verified, updated_at = now()",
			strings.ToLower(r.Topic0), r.Name, r.Signature, r.Source, r.Verified)
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	results := s.Pool.SendBatch(ctx, batch)
	var affected int64
	for i := 0; i < batch.Len(); i++ {
		tag, err := results.Exec()
		if err != nil {
			_ = results.Close()
			return affected, err
		}
		affected += tag.RowsAffected()
	}
	return affected, results.Close()
}

// EnqueueMethodResolve 去重入队（§19：唯一约束兜底，重复只累加计数）。
func (s *Store) EnqueueMethodResolve(ctx context.Context, jobs []method.ResolveJob) error {
	if len(jobs) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, job := range jobs {
		if job.MethodID == "" {
			continue
		}
		batch.Queue("INSERT INTO method_resolve_queue "+
			"(chain, contract_address, method_id, sample_tx_hash, input_length) VALUES ($1,$2,$3,$4,$5) "+
			"ON CONFLICT (chain, contract_address, method_id) DO UPDATE SET "+
			"occurrence_count = method_resolve_queue.occurrence_count + 1, updated_at = now()",
			strings.ToLower(job.Chain), strings.ToLower(job.ContractAddress), strings.ToLower(job.MethodID),
			job.SampleTxHash, job.InputLength)
	}
	if batch.Len() == 0 {
		return nil
	}
	results := s.Pool.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	return results.Close()
}

// LeaseMethodResolveJobs 领取补全任务；UNRESOLVED 任务延后重试，避免打爆公共签名库。
func (s *Store) LeaseMethodResolveJobs(ctx context.Context, limit int) ([]method.ResolveJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx,
		"UPDATE method_resolve_queue SET status='RUNNING', attempts = attempts + 1, updated_at = now() "+
			"WHERE id IN (SELECT id FROM method_resolve_queue WHERE "+
			"  status='PENDING' "+
			"  OR (status='RUNNING' AND updated_at < now() - interval '5 minutes') "+
			"  OR (status='UNRESOLVED' AND attempts < 5 AND updated_at < now() - interval '1 hour') "+
			"ORDER BY updated_at LIMIT $1 FOR UPDATE SKIP LOCKED) "+
			"RETURNING id, chain, contract_address, method_id, sample_tx_hash, input_length, attempts", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []method.ResolveJob{}
	for rows.Next() {
		var job method.ResolveJob
		if err := rows.Scan(&job.ID, &job.Chain, &job.ContractAddress, &job.MethodID,
			&job.SampleTxHash, &job.InputLength, &job.Attempts); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) FinishMethodResolveJob(ctx context.Context, id, status, message string) error {
	_, err := s.Pool.Exec(ctx,
		"UPDATE method_resolve_queue SET status=$2, last_error=$3, updated_at=now() WHERE id=$1", id, status, message)
	return err
}

func (s *Store) MethodResolveQueueDepth(ctx context.Context) (int64, error) {
	var depth int64
	err := s.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM method_resolve_queue WHERE status IN ('PENDING','RUNNING')").Scan(&depth)
	return depth, err
}

// ---- Backfill 任务（§35）----

const backfillColumns = "id, chain, status, priority, batch_size, checkpoint_timestamp, checkpoint_tx_hash, " +
	"total_rows, scanned_rows, rewritten_rows, resolved_rows, unknown_rows, error, created_at, started_at, finished_at, updated_at"

func scanBackfill(row pgx.Row) (*method.BackfillJob, error) {
	var job method.BackfillJob
	err := row.Scan(&job.ID, &job.Chain, &job.Status, &job.Priority, &job.BatchSize, &job.CheckpointTimestamp,
		&job.CheckpointTxHash, &job.TotalRows, &job.ScannedRows, &job.RewrittenRows, &job.ResolvedRows,
		&job.UnknownRows, &job.Error, &job.CreatedAt, &job.StartedAt, &job.FinishedAt, &job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Store) CreateBackfillJob(ctx context.Context, chain string, batchSize int) (*method.BackfillJob, error) {
	if batchSize <= 0 {
		batchSize = 20000
	}
	if batchSize > 100000 {
		batchSize = 100000 // §35：批次上限 100K
	}
	row := s.Pool.QueryRow(ctx,
		"INSERT INTO method_backfill_jobs (chain, batch_size) VALUES ($1,$2) RETURNING "+backfillColumns,
		strings.ToLower(chain), batchSize)
	return scanBackfill(row)
}

func (s *Store) GetBackfillJob(ctx context.Context, id string) (*method.BackfillJob, error) {
	row := s.Pool.QueryRow(ctx, "SELECT "+backfillColumns+" FROM method_backfill_jobs WHERE id=$1", id)
	job, err := scanBackfill(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return job, err
}

func (s *Store) ListBackfillJobs(ctx context.Context, limit int) ([]method.BackfillJob, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx,
		"SELECT "+backfillColumns+" FROM method_backfill_jobs ORDER BY created_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []method.BackfillJob{}
	for rows.Next() {
		job, err := scanBackfill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *job)
	}
	return out, rows.Err()
}

// NextRunnableBackfillJob 取一个待执行任务（含被中断的 RUNNING 任务）。
func (s *Store) NextRunnableBackfillJob(ctx context.Context) (*method.BackfillJob, error) {
	row := s.Pool.QueryRow(ctx, "SELECT "+backfillColumns+" FROM method_backfill_jobs "+
		"WHERE status='PENDING' OR (status='RUNNING' AND updated_at < now() - interval '2 minutes') "+
		"ORDER BY created_at LIMIT 1")
	job, err := scanBackfill(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return job, err
}

func (s *Store) SetBackfillStatus(ctx context.Context, id, status, errMessage string) error {
	_, err := s.Pool.Exec(ctx,
		"UPDATE method_backfill_jobs SET status=$2::varchar, error=$3, updated_at=now(), "+
			"started_at = CASE WHEN $2::varchar='RUNNING' AND started_at IS NULL THEN now() ELSE started_at END, "+
			"finished_at = CASE WHEN $2::varchar IN ('COMPLETED','FAILED','CANCELLED') THEN now() ELSE finished_at END "+
			"WHERE id=$1", id, status, errMessage)
	return err
}

func (s *Store) CheckpointBackfillJob(ctx context.Context, id string, ts *time.Time, txHash string,
	scanned, rewritten, resolved, unknown int64) error {
	_, err := s.Pool.Exec(ctx,
		"UPDATE method_backfill_jobs SET checkpoint_timestamp=$2, checkpoint_tx_hash=$3, scanned_rows=$4, "+
			"rewritten_rows=$5, resolved_rows=$6, unknown_rows=$7, updated_at=now() WHERE id=$1",
		id, ts, txHash, scanned, rewritten, resolved, unknown)
	return err
}

func (s *Store) SetBackfillTotal(ctx context.Context, id string, total int64) error {
	_, err := s.Pool.Exec(ctx, "UPDATE method_backfill_jobs SET total_rows=$2, updated_at=now() WHERE id=$1", id, total)
	return err
}

var _ method.Store = (*Store)(nil)
