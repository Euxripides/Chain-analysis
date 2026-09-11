// Package investigation owns durable investigation control-plane state. It
// deliberately depends only on PostgreSQL and shared contracts; provider and
// analytics implementations are invoked later through capabilities.
package investigation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/platform/contract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("investigation not found")

type Investigation struct {
	ID              string              `json:"id"`
	OrganizationID  string              `json:"organization_id"`
	WorkspaceID     *string             `json:"workspace_id,omitempty"`
	CaseID          *string             `json:"case_id,omitempty"`
	Subject         contract.SubjectRef `json:"subject"`
	Goal            string              `json:"goal"`
	Mode            string              `json:"mode"`
	Status          string              `json:"status"`
	Budget          map[string]any      `json:"budget"`
	StopConditions  map[string]any      `json:"stop_conditions"`
	State           map[string]any      `json:"state"`
	ContractVersion string              `json:"contract_version"`
	Revision        int64               `json:"revision"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

type CreateRequest struct {
	Organization    string              `json:"organization,omitempty"`
	WorkspaceID     *string             `json:"workspace_id,omitempty"`
	CaseID          *string             `json:"case_id,omitempty"`
	Subject         contract.SubjectRef `json:"subject"`
	Goal            string              `json:"goal"`
	Mode            string              `json:"mode,omitempty"`
	Budget          map[string]any      `json:"budget,omitempty"`
	StopConditions  map[string]any      `json:"stop_conditions,omitempty"`
	IdempotencyKey  string              `json:"-"`
	DataRequirement map[string]any      `json:"data_requirement,omitempty"`
}

type Service struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Create(ctx context.Context, req CreateRequest) (Investigation, bool, error) {
	if err := req.Subject.Validate(); err != nil {
		return Investigation{}, false, err
	}
	if req.Goal == "" {
		return Investigation{}, false, fmt.Errorf("goal is required")
	}
	if req.Mode == "" {
		req.Mode = "assisted"
	}
	if req.Mode != "manual" && req.Mode != "assisted" && req.Mode != "auto" {
		return Investigation{}, false, fmt.Errorf("mode must be manual, assisted, or auto")
	}
	if req.Organization == "" {
		req.Organization = "default"
	}
	if req.Budget == nil {
		req.Budget = map[string]any{}
	}
	if req.StopConditions == nil {
		req.StopConditions = map[string]any{}
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Investigation{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var orgID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations WHERE slug=$1 OR id::text=$1`, req.Organization).Scan(&orgID); err != nil {
		return Investigation{}, false, fmt.Errorf("organization not found")
	}
	if req.IdempotencyKey != "" {
		var existing string
		err = tx.QueryRow(ctx, `SELECT id::text FROM investigations WHERE organization_id=$1 AND idempotency_key=$2`, orgID, req.IdempotencyKey).Scan(&existing)
		if err == nil {
			_ = tx.Commit(ctx)
			got, getErr := s.Get(ctx, existing)
			return got, true, getErr
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Investigation{}, false, err
		}
	}

	id := uuid.NewString()
	subject, _ := json.Marshal(req.Subject)
	budget, _ := json.Marshal(req.Budget)
	stops, _ := json.Marshal(req.StopConditions)
	requirement, _ := json.Marshal(req.DataRequirement)
	row := tx.QueryRow(ctx, `INSERT INTO investigations
		(id,organization_id,workspace_id,case_id,subject,goal,mode,budget,stop_conditions,idempotency_key,data_requirement)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11)
		RETURNING id::text,organization_id::text,workspace_id::text,case_id::text,subject,goal,mode,status,budget,stop_conditions,state,contract_version,revision,created_at,updated_at`,
		id, orgID, req.WorkspaceID, req.CaseID, subject, req.Goal, req.Mode, budget, stops, req.IdempotencyKey, requirement)
	inv, err := scan(row)
	if err != nil {
		return Investigation{}, false, err
	}
	if err := enqueue(ctx, tx, inv, "investigation.created", map[string]any{"status": inv.Status}); err != nil {
		return Investigation{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Investigation{}, false, err
	}
	return inv, false, nil
}

func (s *Service) Get(ctx context.Context, id string) (Investigation, error) {
	inv, err := scan(s.pool.QueryRow(ctx, `SELECT id::text,organization_id::text,workspace_id::text,case_id::text,subject,goal,mode,status,budget,stop_conditions,state,contract_version,revision,created_at,updated_at FROM investigations WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Investigation{}, ErrNotFound
	}
	return inv, err
}

func (s *Service) List(ctx context.Context, limit int) ([]Investigation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,organization_id::text,workspace_id::text,case_id::text,subject,goal,mode,status,budget,stop_conditions,state,contract_version,revision,created_at,updated_at FROM investigations ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Investigation, 0)
	for rows.Next() {
		inv, scanErr := scan(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, inv)
	}
	return items, rows.Err()
}

func (s *Service) RuntimeView(ctx context.Context, id string) (map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.id::text,r.status,r.objective,r.plan,r.checkpoint,r.pending_data_jobs,r.started_at,r.finished_at,COALESCE(jsonb_agg(jsonb_build_object('id',s.id::text,'ordinal',s.ordinal,'action',s.action,'capability',s.capability,'status',s.status,'external_job_id',s.external_job_id::text,'error',s.error,'output',s.output,'output_hash',s.output_hash,'algorithm_version',s.algorithm_version,'started_at',s.started_at,'finished_at',s.finished_at) ORDER BY s.ordinal) FILTER (WHERE s.id IS NOT NULL),'[]') FROM investigation_runs r LEFT JOIN investigation_steps s ON s.run_id=r.id WHERE r.investigation_id=$1 GROUP BY r.id ORDER BY r.created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]map[string]any, 0)
	for rows.Next() {
		var runID, status, objective string
		var plan, checkpoint, pending, steps []byte
		var started, finished *time.Time
		if err = rows.Scan(&runID, &status, &objective, &plan, &checkpoint, &pending, &started, &finished, &steps); err != nil {
			return nil, err
		}
		var p, c, j, st any
		_ = json.Unmarshal(plan, &p)
		_ = json.Unmarshal(checkpoint, &c)
		_ = json.Unmarshal(pending, &j)
		_ = json.Unmarshal(steps, &st)
		runs = append(runs, map[string]any{"id": runID, "status": status, "objective": objective, "plan": p, "checkpoint": c, "pending_data_jobs": j, "steps": st, "started_at": started, "finished_at": finished})
	}
	return map[string]any{"runs": runs}, rows.Err()
}

func (s *Service) Artifacts(ctx context.Context, id string) (map[string]any, error) {
	findings := make([]map[string]any, 0)
	rows, err := s.pool.Query(ctx, `SELECT id::text,finding_type,title,summary,confidence,status,properties,algorithm,algorithm_version,created_at FROM investigation_findings WHERE investigation_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var fid, kind, title, summary, status, algorithm, version string
		var confidence float64
		var properties []byte
		var created time.Time
		if err = rows.Scan(&fid, &kind, &title, &summary, &confidence, &status, &properties, &algorithm, &version, &created); err != nil {
			rows.Close()
			return nil, err
		}
		var props any
		_ = json.Unmarshal(properties, &props)
		findings = append(findings, map[string]any{"id": fid, "type": kind, "title": title, "summary": summary, "confidence": confidence, "status": status, "properties": props, "algorithm": algorithm, "algorithm_version": version, "created_at": created})
	}
	rows.Close()
	evidence := make([]map[string]any, 0)
	rows, err = s.pool.Query(ctx, `SELECT id::text,evidence_type,title,source_refs,content_hash,provenance,created_at FROM investigation_evidence WHERE investigation_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var eid, kind, title, hash string
		var refs, provenance []byte
		var created time.Time
		if err = rows.Scan(&eid, &kind, &title, &refs, &hash, &provenance, &created); err != nil {
			rows.Close()
			return nil, err
		}
		var sourceRefs, prov any
		_ = json.Unmarshal(refs, &sourceRefs)
		_ = json.Unmarshal(provenance, &prov)
		evidence = append(evidence, map[string]any{"id": eid, "type": kind, "title": title, "source_refs": sourceRefs, "content_hash": hash, "provenance": prov, "created_at": created})
	}
	rows.Close()
	patches := make([]map[string]any, 0)
	rows, err = s.pool.Query(ctx, `SELECT id::text,base_revision,revision,patch,created_at FROM graph_patches WHERE investigation_id=$1 ORDER BY revision`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid string
		var base, revision int64
		var raw []byte
		var created time.Time
		if err = rows.Scan(&pid, &base, &revision, &raw, &created); err != nil {
			rows.Close()
			return nil, err
		}
		var patch any
		_ = json.Unmarshal(raw, &patch)
		patches = append(patches, map[string]any{"id": pid, "base_revision": base, "revision": revision, "patch": patch, "created_at": created})
	}
	rows.Close()
	return map[string]any{"findings": findings, "evidence": evidence, "graph_patches": patches}, rows.Err()
}

func (s *Service) Transition(ctx context.Context, id, action string, expectedRevision int64) (Investigation, error) {
	targets := map[string]string{"start": "running", "analyze": "running", "pause": "paused", "resume": "running", "retry": "running", "cancel": "cancelled", "approve": "completed"}
	allowed := map[string][]string{"start": {"created"}, "analyze": {"created", "waiting_review"}, "pause": {"running", "waiting_data"}, "resume": {"paused"}, "retry": {"failed"}, "cancel": {"created", "running", "waiting_data", "waiting_review", "paused", "failed"}, "approve": {"waiting_review"}}
	target, ok := targets[action]
	if !ok {
		return Investigation{}, fmt.Errorf("unsupported action")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Investigation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := `UPDATE investigations SET status=$2, revision=revision+1, updated_at=now() WHERE id=$1 AND status=ANY($3)`
	args := []any{id, target, allowed[action]}
	if expectedRevision > 0 {
		query += ` AND revision=$4`
		args = append(args, expectedRevision)
	}
	query += ` RETURNING id::text,organization_id::text,workspace_id::text,case_id::text,subject,goal,mode,status,budget,stop_conditions,state,contract_version,revision,created_at,updated_at`
	inv, err := scan(tx.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Investigation{}, ErrNotFound
	}
	if err != nil {
		return Investigation{}, err
	}
	runStatus := target
	finished := target == "completed" || target == "cancelled"
	_, err = tx.Exec(ctx, `UPDATE investigation_runs SET status=$2,updated_at=now(),finished_at=CASE WHEN $3 THEN now() ELSE finished_at END WHERE id=(SELECT active_run_id FROM investigations WHERE id=$1)`, id, runStatus, finished)
	if err != nil {
		return Investigation{}, err
	}
	if err := enqueue(ctx, tx, inv, "investigation."+target, map[string]any{"status": target}); err != nil {
		return Investigation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Investigation{}, err
	}
	return inv, nil
}

// MarkRuntimeFailed closes a background run after the HTTP request that
// launched it has already returned. The failure remains visible and retryable.
func (s *Service) MarkRuntimeFailed(ctx context.Context, id string, cause error) {
	message := "runtime failed"
	if cause != nil {
		message = cause.Error()
	}
	_, _ = s.pool.Exec(ctx, `UPDATE investigations SET status='failed',state=jsonb_set(state,'{runtime_error}',to_jsonb($2::text),true),revision=revision+1,updated_at=now() WHERE id=$1 AND status IN ('running','waiting_data')`, id, message)
	_, _ = s.pool.Exec(ctx, `UPDATE investigation_runs SET status='failed',finished_at=now(),updated_at=now() WHERE id=(SELECT active_run_id FROM investigations WHERE id=$1) AND status NOT IN ('completed','cancelled','failed')`, id)
}

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Investigation, error) {
	var inv Investigation
	var subject, budget, stops, state []byte
	if err := row.Scan(&inv.ID, &inv.OrganizationID, &inv.WorkspaceID, &inv.CaseID, &subject, &inv.Goal, &inv.Mode, &inv.Status, &budget, &stops, &state, &inv.ContractVersion, &inv.Revision, &inv.CreatedAt, &inv.UpdatedAt); err != nil {
		return inv, err
	}
	_ = json.Unmarshal(subject, &inv.Subject)
	_ = json.Unmarshal(budget, &inv.Budget)
	_ = json.Unmarshal(stops, &inv.StopConditions)
	_ = json.Unmarshal(state, &inv.State)
	return inv, nil
}

func enqueue(ctx context.Context, tx pgx.Tx, inv Investigation, eventType string, payload any) error {
	payloadJSON, _ := json.Marshal(payload)
	env := contract.EventEnvelope{ID: uuid.NewString(), Type: eventType, Version: contract.Version, AggregateType: "investigation", AggregateID: inv.ID, Sequence: inv.Revision, OccurredAt: time.Now().UTC(), RequestContext: contract.RequestContext{OrganizationID: inv.OrganizationID, WorkspaceID: value(inv.WorkspaceID), CaseID: value(inv.CaseID), InvestigationID: inv.ID}, Payload: payloadJSON}
	body, _ := json.Marshal(env)
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,event_type,event_version,aggregate_type,aggregate_id,sequence,subject,envelope) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, env.ID, env.Type, env.Version, env.AggregateType, env.AggregateID, env.Sequence, "events."+eventType, body)
	return err
}

func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

type ArchitectureStatus struct {
	ContractVersion string            `json:"contract_version"`
	FrozenContracts []string          `json:"frozen_contracts"`
	Components      map[string]bool   `json:"components"`
	Schemas         map[string]string `json:"schemas"`
}

func (s *Service) ArchitectureStatus(ctx context.Context) (ArchitectureStatus, error) {
	status := ArchitectureStatus{ContractVersion: contract.Version, FrozenContracts: contract.FrozenTypes, Components: map[string]bool{}, Schemas: map[string]string{}}
	for _, table := range []string{"investigations", "platform_jobs", "schema_registry", "projection_registry", "investigation_findings", "investigation_evidence", "graph_patches", "investigation_canvases", "canvas_snapshots", "semantic_events", "semantic_projection_state", "advanced_analysis_results", "investigation_reports", "investigation_subjects", "entity_relationships", "audit_events", "outbox_events"} {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil {
			return status, err
		}
		status.Components[table] = exists
	}
	rows, err := s.pool.Query(ctx, `SELECT schema_name,schema_version FROM schema_registry WHERE active ORDER BY schema_name,schema_version`)
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, version string
		if err := rows.Scan(&name, &version); err != nil {
			return status, err
		}
		status.Schemas[name] = version
	}
	return status, rows.Err()
}
