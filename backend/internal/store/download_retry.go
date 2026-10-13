package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrDownloadRetryBudget = errors.New("download recovery budget exhausted")

type DownloadRetryState struct {
	State         string    `json:"state"`
	Rounds        int       `json:"rounds"`
	MaxRounds     int       `json:"max_rounds"`
	Requests      int64     `json:"requests"`
	LogicalCalls  int64     `json:"logical_calls"`
	MaxRequests   int64     `json:"max_requests"`
	Credits       float64   `json:"credits"`
	MaxCredits    float64   `json:"max_credits"`
	CostMicros    int64     `json:"cost_micros"`
	MaxCostMicros int64     `json:"max_cost_micros"`
	NextAt        time.Time `json:"next_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Reason        string    `json:"reason,omitempty"`
}

func RetryState(stats map[string]any) DownloadRetryState {
	var v DownloadRetryState
	b, _ := json.Marshal(stats["download_retry"])
	_ = json.Unmarshal(b, &v)
	return v
}

func defaultDownloadRetry(now time.Time) DownloadRetryState {
	return DownloadRetryState{State: "waiting", MaxRounds: 3, MaxRequests: 250, MaxCredits: 175000, MaxCostMicros: 50000, ExpiresAt: now.Add(24 * time.Hour)}
}

func saveRetryState(ctx context.Context, tx pgx.Tx, id string, v DownloadRetryState) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE download_jobs SET stats=jsonb_set(COALESCE(stats,'{}'),'{download_retry}',$2::jsonb),updated_at=now() WHERE id=$1`, id, b)
	return err
}

// This lock also serializes independent worker processes sharing the same job.
func (s *Store) ReserveDownloadRetryRequest(ctx context.Context, id string, calls int64, credits, cost float64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var raw []byte
	var status string
	if err = tx.QueryRow(ctx, "SELECT status,stats->'download_retry' FROM download_jobs WHERE id=$1 FOR UPDATE", id).Scan(&status, &raw); err != nil {
		return err
	}
	var v DownloadRetryState
	if json.Unmarshal(raw, &v) != nil {
		return ErrDownloadRetryBudget
	}
	if v.State != "active" || (status != "RUNNING" && status != "QUEUED" && status != "RESUMING") || time.Now().After(v.ExpiresAt) {
		return ErrDownloadRetryBudget
	}
	micros := int64(math.Ceil(cost * 1000000))
	if calls < 1 || credits < 0 || cost < 0 || math.IsNaN(credits) || math.IsNaN(cost) || math.IsInf(credits, 0) || math.IsInf(cost, 0) || v.Requests >= v.MaxRequests || v.Credits+credits > v.MaxCredits || v.CostMicros+micros > v.MaxCostMicros {
		v.State = "budget_exhausted"
		v.Reason = "request, credit or cost limit reached"
		if err = saveRetryState(ctx, tx, id, v); err != nil {
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		return ErrDownloadRetryBudget
	}
	v.Requests++
	v.LogicalCalls += calls
	v.Credits += credits
	v.CostMicros += micros
	if err = saveRetryState(ctx, tx, id, v); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Automatic retry preserves valid pages and checkpoints. It never repairs
// missing raw objects or deleted ranges by guessing where provider pages end.
func (s *Store) AdvanceDownloadRetry(ctx context.Context, id string, now time.Time) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var stats map[string]any
	if err = tx.QueryRow(ctx, "SELECT status,stats FROM download_jobs WHERE id=$1 FOR UPDATE", id).Scan(&status, &stats); err != nil {
		return 0, err
	}
	if status != "FAILED" && status != "PARTIAL_FAILED" {
		return 0, nil
	}
	v := RetryState(stats)
	if v.State == "" {
		v = defaultDownloadRetry(now)
		v.NextAt = now.Add(time.Minute)
		if err = saveRetryState(ctx, tx, id, v); err != nil {
			return 0, err
		}
		return 0, tx.Commit(ctx)
	}
	if v.State == "blocked" || v.State == "budget_exhausted" || v.State == "expired" {
		return 0, nil
	}
	if now.Before(v.NextAt) {
		return 0, nil
	}
	if now.After(v.ExpiresAt) || v.Rounds >= v.MaxRounds {
		v.State = "expired"
		v.Reason = "recovery time or round limit reached"
		if err = saveRetryState(ctx, tx, id, v); err != nil {
			return 0, err
		}
		return 0, tx.Commit(ctx)
	}
	var unsafe int64
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM download_shards s WHERE s.job_id=$1 AND s.status IN ('FAILED','FAILED_FINAL') AND
 (COALESCE((s.page_state->>'last_failure_retryable')::boolean,false)=false OR EXISTS(SELECT 1 FROM download_pages p WHERE p.shard_id=s.id AND p.status='FAILED_FINAL'))`, id).Scan(&unsafe); err != nil {
		return 0, err
	}
	if unsafe > 0 {
		v.State = "blocked"
		v.Reason = "failure requires request or range repair"
		if err = saveRetryState(ctx, tx, id, v); err != nil {
			return 0, err
		}
		return 0, tx.Commit(ctx)
	}
	var guards int64
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM download_volume_guards WHERE job_id=$1 AND state IN ('WAITING_USER_CONFIRMATION','ABORTED_BY_USER')`, id).Scan(&guards); err != nil {
		return 0, err
	}
	if guards > 0 {
		return 0, nil
	}
	tag, err := tx.Exec(ctx, `UPDATE download_shards SET status='PENDING',retries=0,error='',lease_owner='',leased_until=NULL,lease_epoch=lease_epoch+1,updated_at=now()
 WHERE job_id=$1 AND status IN ('FAILED','FAILED_FINAL') AND COALESCE((page_state->>'last_failure_retryable')::boolean,false)`, id)
	if err != nil {
		return 0, err
	}
	n := tag.RowsAffected()
	if n == 0 {
		v.State = "blocked"
		v.Reason = "no failed shard exists; missing planned range must be restored"
		if err = saveRetryState(ctx, tx, id, v); err != nil {
			return 0, err
		}
		return 0, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, "UPDATE download_datasets SET status='PENDING',updated_at=now() WHERE job_id=$1 AND status='FAILED'", id); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "UPDATE download_subjects s SET status='PENDING',updated_at=now() WHERE job_id=$1 AND (status='FAILED' OR EXISTS(SELECT 1 FROM download_datasets d WHERE d.subject_id=s.id AND d.status='PENDING'))", id); err != nil {
		return 0, err
	}
	v.Rounds++
	v.State = "active"
	v.NextAt = now.Add(time.Duration(1<<uint(v.Rounds))*time.Minute + time.Duration(v.Requests%17)*time.Second)
	if err = saveRetryState(ctx, tx, id, v); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "UPDATE download_jobs SET status='QUEUED',error='' WHERE id=$1", id); err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

func (s *Store) DownloadRetryCandidates(ctx context.Context, after time.Time) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM download_jobs WHERE status IN ('FAILED','PARTIAL_FAILED') AND
 (created_at>=$1 OR stats ? 'download_retry') AND COALESCE(stats->'download_retry'->>'state','') NOT IN ('blocked','expired','budget_exhausted')
 AND (NOT(stats ? 'download_retry') OR COALESCE((stats->'download_retry'->>'next_at')::timestamptz,now())<=now())
 ORDER BY updated_at,id LIMIT 32`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (v DownloadRetryState) String() string {
	return fmt.Sprintf("%s (%d/%d requests)", v.State, v.Requests, v.MaxRequests)
}
