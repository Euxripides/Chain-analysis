package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func (s *Store) InsertUsage(ctx context.Context, u model.UsageRow) error {
	var jobID, shardID any
	if u.JobID != "" {
		jobID = u.JobID
	}
	if u.ShardID != "" {
		shardID = u.ShardID
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO provider_usage "+
		"(id, provider, method, dataset, job_id, shard_id, tenant_id, request_time, duration_ms, status, http_status, api_error, rows, bytes, credits, estimated_cost_usd) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)",
		uuid.NewString(), u.Provider, u.Method, u.Dataset, jobID, shardID, u.TenantID, u.RequestTime,
		u.DurationMs, u.Status, u.HTTPStatus, u.APIError, u.Rows, u.Bytes, u.Credits, u.CostUSD)
	return err
}

type UsageSummary struct {
	Requests int64   `json:"requests"`
	Credits  float64 `json:"credits"`
	CostUSD  float64 `json:"cost_usd"`
	Rows     int64   `json:"rows"`
	Bytes    int64   `json:"bytes"`
}

func (s *Store) UsageSummary(ctx context.Context, since time.Time) (map[string]UsageSummary, error) {
	rows, err := s.Pool.Query(ctx, "SELECT dataset, COUNT(*), COALESCE(SUM(credits),0), COALESCE(SUM(estimated_cost_usd),0), "+
		"COALESCE(SUM(rows),0), COALESCE(SUM(bytes),0) "+
		"FROM provider_usage WHERE request_time >= $1 GROUP BY dataset", since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]UsageSummary{}
	for rows.Next() {
		var d string
		var u UsageSummary
		if err := rows.Scan(&d, &u.Requests, &u.Credits, &u.CostUSD, &u.Rows, &u.Bytes); err != nil {
			return nil, err
		}
		out[d] = u
	}
	return out, rows.Err()
}

func (s *Store) InsertAudit(ctx context.Context, userID *string, action, resourceType, resourceID string, detail map[string]any) error {
	b, _ := json.Marshal(detail)
	_, err := s.Pool.Exec(ctx, "INSERT INTO audit_events (id, user_id, action, resource_type, resource_id, detail) "+
		"VALUES ($1,$2,$3,$4,$5,$6)", uuid.NewString(), userID, action, resourceType, resourceID, b)
	return err
}

// ---- Exports ----

func (s *Store) CreateExport(ctx context.Context, jobID *string, dataset, format string) (*model.Export, error) {
	e := &model.Export{
		ID:      uuid.NewString(),
		JobID:   jobID,
		Dataset: dataset,
		Format:  format,
		Status:  "PENDING",
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO exports (id, job_id, dataset, format, status) VALUES ($1,$2,$3,$4,$5)",
		e.ID, jobID, dataset, format, e.Status)
	return e, err
}

func (s *Store) GetExport(ctx context.Context, id string) (*model.Export, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, job_id, dataset, format, status, object_ref, rows, error, created_at, updated_at "+
		"FROM exports WHERE id=$1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var e model.Export
	if err := rows.Scan(&e.ID, &e.JobID, &e.Dataset, &e.Format, &e.Status, &e.ObjectRef, &e.Rows, &e.Error, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Store) UpdateExport(ctx context.Context, id, status, objectRef string, rows int64, errMsg string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE exports SET status=$2, object_ref=$3, rows=$4, error=$5, updated_at=now() WHERE id=$1",
		id, status, objectRef, rows, errMsg)
	return err
}
