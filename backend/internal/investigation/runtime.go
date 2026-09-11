package investigation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/platform/contract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Runtime is deterministic orchestration. It never imports a provider or SQL
// implementation outside its own control-plane repository.
type Runtime struct {
	service  *Service
	ensure   func(context.Context, dataset.EnsureRequest) (*dataset.EnsureResponse, error)
	analyze  func(context.Context, string, string) (any, error)
	semantic func(context.Context, string, string) (any, error)
	advanced func(context.Context, string, string) (any, error)
}

func NewRuntime(s *Service, ensure func(context.Context, dataset.EnsureRequest) (*dataset.EnsureResponse, error)) *Runtime {
	return &Runtime{service: s, ensure: ensure}
}
func (r *Runtime) WithAnalyzer(fn func(context.Context, string, string) (any, error)) *Runtime {
	r.analyze = fn
	return r
}
func (r *Runtime) WithSemanticAnalyzer(fn func(context.Context, string, string) (any, error)) *Runtime {
	r.semantic = fn
	return r
}
func (r *Runtime) WithAdvancedAnalyzer(fn func(context.Context, string, string) (any, error)) *Runtime {
	r.advanced = fn
	return r
}

func (r *Runtime) Start(ctx context.Context, id string) (map[string]any, error) {
	inv, err := r.service.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var req dataset.EnsureRequest
	var raw []byte
	if err = r.service.pool.QueryRow(ctx, `SELECT data_requirement FROM investigations WHERE id=$1`, id).Scan(&raw); err != nil {
		return nil, err
	}
	if len(raw) > 2 {
		_ = json.Unmarshal(raw, &req)
	}
	if req.Chain == "" {
		req.Chain = inv.Subject.Chain
	}
	if len(req.Subjects) == 0 {
		req.Subjects = []string{inv.Subject.Value}
	}
	if len(req.Datasets) == 0 {
		req.Datasets = []string{"transactions", "token_transfers"}
	}
	if req.Range.Type == "" || (req.Range.Type == "time" && (req.Range.From == nil || req.Range.To == nil)) {
		to := time.Now().UTC()
		from := to.AddDate(0, -1, 0)
		req.Range = dataset.RangeSpec{Type: "time", From: &from, To: &to}
	}
	req.Source = "investigation"
	req.Priority = 100
	req.PerformanceMode = "safe"
	req.ReusePolicy = "prefer_local"
	runID, stageID, stepID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	plan := []map[string]any{{"stage": "ensure_data", "capability": "data.ensure"}, {"stage": "triage", "capability": "analysis.address_profile"}}
	planJSON, _ := json.Marshal(plan)
	input, _ := json.Marshal(req)
	tx, err := r.service.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO investigation_runs(id,investigation_id,status,objective,plan,remaining_budget,started_at) VALUES($1,$2,'running',$3,$4,$5,now())`, runID, id, inv.Goal, planJSON, mustJSON(inv.Budget))
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO investigation_stages(id,run_id,name,ordinal,status,started_at) VALUES($1,$2,'ensure_data',1,'running',now())`, stageID, runID)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO investigation_steps(id,run_id,stage_id,ordinal,action,capability,capability_version,status,input,started_at) VALUES($1,$2,$3,1,'ensure_data','data.ensure','1.0','running',$4,now())`, stepID, runID, stageID, input)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE investigations SET active_run_id=$2,status='running',revision=revision+1,updated_at=now() WHERE id=$1`, id, runID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	result, err := r.ensure(ctx, req)
	if err != nil {
		_, _ = r.service.pool.Exec(ctx, `UPDATE investigation_steps SET status='failed',error=$2,finished_at=now() WHERE id=$1`, stepID, err.Error())
		return nil, err
	}
	out := mustJSON(result)
	sum := sha256.Sum256(out)
	status := "waiting"
	if result.Status == "ready" {
		status = "completed"
	}
	_, err = r.service.pool.Exec(ctx, `UPDATE investigation_steps SET status=$2,output=$3,output_hash=$4,external_job_id=$5,finished_at=CASE WHEN $2='completed' THEN now() END WHERE id=$1`, stepID, status, out, hex.EncodeToString(sum[:]), result.JobID)
	if err != nil {
		return nil, err
	}
	pendingJobs := []string{}
	if result.JobID != nil {
		pendingJobs = append(pendingJobs, *result.JobID)
	}
	_, err = r.service.pool.Exec(ctx, `UPDATE investigation_runs SET status=$2,pending_data_jobs=$3,updated_at=now() WHERE id=$1`, runID, status, mustJSON(pendingJobs))
	if result.Status == "ready" {
		r.advanceReady(ctx, runID, stepID, result.DatasetRef)
	}
	if result.JobID != nil {
		_, _ = r.service.pool.Exec(ctx, `UPDATE investigations SET status='waiting_data',state=jsonb_set(state,'{pending_data_job}',to_jsonb($2::text),true),updated_at=now() WHERE id=$1`, id, *result.JobID)
	}
	return map[string]any{"run_id": runID, "step_id": stepID, "status": status, "data": result}, err
}

func (r *Runtime) AnalyzeExisting(ctx context.Context, id string) (map[string]any, error) {
	inv, err := r.service.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	runID, stage1, stage2, step1, step2 := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	plan := mustJSON([]map[string]any{{"stage": "ensure_data", "mode": "reuse_indexed"}, {"stage": "triage", "capability": "analysis.address_profile"}, {"stage": "fund_flow", "capability": "analysis.flow_stats"}})
	tx, err := r.service.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO investigation_runs(id,investigation_id,status,objective,plan,remaining_budget,started_at,checkpoint) VALUES($1,$2,'running',$3,$4,$5,now(),'{"data_reused":true}')`, runID, id, inv.Goal, plan, mustJSON(inv.Budget))
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO investigation_stages(id,run_id,name,ordinal,status,started_at,finished_at) VALUES($1,$3,'ensure_data',1,'completed',now(),now()),($2,$3,'triage',2,'pending',now(),NULL)`, stage1, stage2, runID)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO investigation_steps(id,run_id,stage_id,ordinal,action,capability,capability_version,status,input,output,started_at,finished_at) VALUES($1,$3,$4,1,'reuse_indexed_data','data.ensure','1.0','completed','{}','{"reuse":"indexed"}',now(),now()),($2,$3,$5,2,'address_profile','analysis.address_profile','1.0','pending','{}','{}',NULL,NULL)`, step1, step2, runID, stage1, stage2)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE investigations SET active_run_id=$2,status='running',revision=revision+1,updated_at=now() WHERE id=$1`, id, runID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	r.executeAnalysis(ctx, runID)
	return map[string]any{"run_id": runID, "status": "analyzed_indexed_data"}, nil
}

func (r *Runtime) advanceReady(ctx context.Context, runID, completedStepID, datasetRef string) {
	tx, err := r.service.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stageID, stepID := uuid.NewString(), uuid.NewString()
	_, err = tx.Exec(ctx, `UPDATE investigation_steps SET status='completed',finished_at=COALESCE(finished_at,now()) WHERE id=$1`, completedStepID)
	if err != nil {
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO investigation_stages(id,run_id,name,ordinal,status) VALUES($1,$2,'triage',2,'pending') ON CONFLICT(run_id,ordinal) DO NOTHING`, stageID, runID)
	if err != nil {
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO investigation_steps(id,run_id,stage_id,ordinal,action,capability,capability_version,status,input) SELECT $1,$2,id,2,'address_profile','analysis.address_profile','1.0','pending',jsonb_build_object('dataset_ref',$3::text) FROM investigation_stages WHERE run_id=$2 AND ordinal=2 ON CONFLICT(run_id,ordinal) DO NOTHING`, stepID, runID, datasetRef)
	if err != nil {
		return
	}
	_, err = tx.Exec(ctx, `UPDATE investigation_runs SET status='running',checkpoint=jsonb_set(checkpoint,'{data_ready}','true'::jsonb,true),updated_at=now() WHERE id=$1`, runID)
	if err != nil {
		return
	}
	_ = tx.Commit(ctx)
	r.executeAnalysis(ctx, runID)
}

func (r *Runtime) HandleJobEvent(ctx context.Context, subject string, data []byte) {
	jobID := strings.TrimPrefix(subject, "events.jobs.")
	if jobID == subject {
		return
	}
	var evt map[string]any
	if json.Unmarshal(data, &evt) != nil {
		return
	}
	status := strings.ToUpper(fmt.Sprint(evt["status"]))
	if status != "COMPLETED" && status != "FAILED" && status != "PARTIAL_FAILED" && status != "CANCELLED" {
		return
	}
	if status == "COMPLETED" {
		r.advanceAfterData(ctx, jobID)
		return
	}
	_, _ = r.service.pool.Exec(ctx, `WITH hit AS (UPDATE investigation_steps SET status='failed',error=$2,finished_at=now() WHERE external_job_id=$1 AND status='waiting' RETURNING run_id) UPDATE investigation_runs SET status='failed',finished_at=now(),updated_at=now() WHERE id IN (SELECT run_id FROM hit)`, jobID, status)
	_, _ = r.service.pool.Exec(ctx, `UPDATE investigations i SET status='failed',revision=revision+1,updated_at=now(),state=state-'pending_data_job' WHERE i.status NOT IN ('cancelled','completed','failed') AND EXISTS (SELECT 1 FROM investigation_runs r JOIN investigation_steps s ON s.run_id=r.id WHERE r.investigation_id=i.id AND s.external_job_id=$1)`, jobID)
}

func (r *Runtime) advanceAfterData(ctx context.Context, jobID string) {
	tx, err := r.service.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runID string
	err = tx.QueryRow(ctx, `UPDATE investigation_steps SET status='completed',finished_at=now() WHERE external_job_id=$1 AND status='waiting' RETURNING run_id::text`, jobID).Scan(&runID)
	if err != nil {
		return
	}
	stageID, stepID := uuid.NewString(), uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO investigation_stages(id,run_id,name,ordinal,status) VALUES($1,$2,'triage',2,'pending') ON CONFLICT(run_id,ordinal) DO NOTHING`, stageID, runID)
	if err != nil {
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO investigation_steps(id,run_id,stage_id,ordinal,action,capability,capability_version,status,input) SELECT $1,$2,id,2,'address_profile','analysis.address_profile','1.0','pending',jsonb_build_object('trigger_job_id',$3::text) FROM investigation_stages WHERE run_id=$2 AND ordinal=2 ON CONFLICT(run_id,ordinal) DO NOTHING`, stepID, runID, jobID)
	if err != nil {
		return
	}
	_, err = tx.Exec(ctx, `UPDATE investigation_runs SET status='running',pending_data_jobs='[]',last_event_id=$2,checkpoint=jsonb_set(checkpoint,'{data_ready}', 'true'::jsonb,true),updated_at=now() WHERE id=$1`, runID, "events.jobs."+jobID)
	if err != nil {
		return
	}
	_, err = tx.Exec(ctx, `UPDATE investigations SET status='running',revision=revision+1,updated_at=now(),state=jsonb_set(state-'pending_data_job','{resumed_from_event}',to_jsonb($2::text),true) WHERE active_run_id=$1 AND status NOT IN ('cancelled','completed')`, runID, "events.jobs."+jobID)
	if err != nil {
		return
	}
	_ = tx.Commit(ctx)
	r.executeAnalysis(ctx, runID)
}

func (r *Runtime) executeAnalysis(ctx context.Context, runID string) {
	if r.analyze == nil {
		return
	}
	var invID, chain, address, stepID string
	err := r.service.pool.QueryRow(ctx, `SELECT r.investigation_id::text,i.subject->>'chain',i.subject->>'value',s.id::text FROM investigation_runs r JOIN investigations i ON i.id=r.investigation_id JOIN investigation_steps s ON s.run_id=r.id AND s.ordinal=2 WHERE r.id=$1 AND s.status='pending'`, runID).Scan(&invID, &chain, &address, &stepID)
	if err != nil {
		return
	}
	_, _ = r.service.pool.Exec(ctx, `UPDATE investigation_steps SET status='running',started_at=now() WHERE id=$1 AND status='pending'`, stepID)
	result, err := r.analyze(ctx, chain, address)
	if err != nil {
		_, _ = r.service.pool.Exec(ctx, `UPDATE investigation_steps SET status='failed',error=$2,finished_at=now() WHERE id=$1`, stepID, err.Error())
		_, _ = r.service.pool.Exec(ctx, `UPDATE investigation_runs SET status='failed',finished_at=now(),updated_at=now() WHERE id=$1`, runID)
		_, _ = r.service.pool.Exec(ctx, `UPDATE investigations SET status='failed',revision=revision+1,updated_at=now() WHERE id=$1 AND status NOT IN ('cancelled','completed')`, invID)
		return
	}
	out := mustJSON(result)
	var semanticOut []byte
	if r.semantic != nil {
		semanticResult, semanticErr := r.semantic(ctx, chain, address)
		if semanticErr != nil {
			_, _ = r.service.pool.Exec(ctx, `UPDATE investigation_steps SET status='failed',error=$2,finished_at=now() WHERE id=$1`, stepID, semanticErr.Error())
			r.service.MarkRuntimeFailed(ctx, invID, semanticErr)
			return
		}
		semanticOut = mustJSON(semanticResult)
	}
	var advancedOut []byte
	if r.advanced != nil {
		advancedResult, advancedErr := r.advanced(ctx, chain, address)
		if advancedErr != nil {
			_, _ = r.service.pool.Exec(ctx, `UPDATE investigation_steps SET status='failed',error=$2,finished_at=now() WHERE id=$1`, stepID, advancedErr.Error())
			r.service.MarkRuntimeFailed(ctx, invID, advancedErr)
			return
		}
		advancedOut = mustJSON(advancedResult)
	}
	sum := sha256.Sum256(out)
	stageID, flowStepID, semanticStageID, semanticStepID, advancedStageID, advancedStepID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, e := r.service.pool.Begin(ctx)
	if e != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, e = tx.Exec(ctx, `UPDATE investigation_steps SET status='completed',output=$2,output_hash=$3,algorithm_version='basic-flow-v1.0',finished_at=now() WHERE id=$1`, stepID, out, hex.EncodeToString(sum[:]))
	if e != nil {
		return
	}
	_, e = tx.Exec(ctx, `UPDATE investigation_stages SET status='completed',finished_at=now() WHERE run_id=$1 AND ordinal=2`, runID)
	if e != nil {
		return
	}
	_, e = tx.Exec(ctx, `INSERT INTO investigation_stages(id,run_id,name,ordinal,status,started_at,finished_at) VALUES($1,$2,'fund_flow',3,'completed',now(),now()) ON CONFLICT(run_id,ordinal) DO NOTHING`, stageID, runID)
	if e != nil {
		return
	}
	_, e = tx.Exec(ctx, `INSERT INTO investigation_steps(id,run_id,stage_id,ordinal,action,capability,capability_version,status,input,output,output_hash,algorithm_version,started_at,finished_at) SELECT $1,$2,id,3,'flow_stats','analysis.flow_stats','1.0','completed','{}',$3,$4,'basic-flow-v1.0',now(),now() FROM investigation_stages WHERE run_id=$2 AND ordinal=3 ON CONFLICT(run_id,ordinal) DO NOTHING`, flowStepID, runID, out, hex.EncodeToString(sum[:]))
	if e != nil {
		return
	}
	if e = materializeAnalysis(ctx, tx, invID, chain, address, out, hex.EncodeToString(sum[:])); e != nil {
		return
	}
	if len(semanticOut) > 0 {
		semanticSum := sha256.Sum256(semanticOut)
		_, e = tx.Exec(ctx, `INSERT INTO investigation_stages(id,run_id,name,ordinal,status,started_at,finished_at) VALUES($1,$2,'semantic_protocol',4,'completed',now(),now()) ON CONFLICT(run_id,ordinal) DO NOTHING`, semanticStageID, runID)
		if e != nil {
			return
		}
		_, e = tx.Exec(ctx, `INSERT INTO investigation_steps(id,run_id,stage_id,ordinal,action,capability,capability_version,status,input,output,output_hash,algorithm_version,started_at,finished_at) SELECT $1,$2,id,4,'decode_protocol_semantics','protocol.semantic.decode','1.0','completed','{}',$3,$4,'protocol-semantic-v1.0',now(),now() FROM investigation_stages WHERE run_id=$2 AND ordinal=4 ON CONFLICT(run_id,ordinal) DO NOTHING`, semanticStepID, runID, semanticOut, hex.EncodeToString(semanticSum[:]))
		if e != nil {
			return
		}
	}
	if len(advancedOut) > 0 {
		advancedSum := sha256.Sum256(advancedOut)
		_, e = tx.Exec(ctx, `INSERT INTO investigation_stages(id,run_id,name,ordinal,status,started_at,finished_at) VALUES($1,$2,'advanced_investigation',5,'completed',now(),now()) ON CONFLICT(run_id,ordinal) DO NOTHING`, advancedStageID, runID)
		if e != nil {
			return
		}
		_, e = tx.Exec(ctx, `INSERT INTO investigation_steps(id,run_id,stage_id,ordinal,action,capability,capability_version,status,input,output,output_hash,algorithm_version,started_at,finished_at) SELECT $1,$2,id,5,'detect_advanced_patterns','analysis.advanced_patterns','1.0','completed','{}',$3,$4,'advanced-investigation-v1.0',now(),now() FROM investigation_stages WHERE run_id=$2 AND ordinal=5 ON CONFLICT(run_id,ordinal) DO NOTHING`, advancedStepID, runID, advancedOut, hex.EncodeToString(advancedSum[:]))
		if e != nil {
			return
		}
		if e = materializeAdvanced(ctx, tx, invID, advancedOut, hex.EncodeToString(advancedSum[:])); e != nil {
			return
		}
	}
	_, e = tx.Exec(ctx, `UPDATE investigation_runs SET status='waiting',checkpoint=jsonb_set(jsonb_set(jsonb_set(checkpoint,'{phase2_complete}','true'::jsonb,true),'{phase4_semantic_complete}',to_jsonb($2::boolean),true),'{phase5_advanced_complete}',to_jsonb($3::boolean),true),updated_at=now() WHERE id=$1`, runID, len(semanticOut) > 0, len(advancedOut) > 0)
	if e != nil {
		return
	}
	_, e = tx.Exec(ctx, `UPDATE investigations SET status='waiting_review',revision=revision+1,updated_at=now(),state=jsonb_set(state,'{phase2_complete}','true'::jsonb,true) WHERE id=$1 AND status NOT IN ('cancelled','completed')`, invID)
	if e != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// materializeAnalysis turns a capability result into durable control-plane
// artifacts. It consumes the stable JSON capability output and does not depend
// on the concrete analytics implementation.
func materializeAnalysis(ctx context.Context, tx pgx.Tx, investigationID, chain, address string, raw []byte, outputHash string) error {
	var result struct {
		TransactionCount     int    `json:"transaction_count"`
		UniqueCounterparties int    `json:"unique_counterparties"`
		AlgorithmVersion     string `json:"algorithm_version"`
		EvidenceBoundary     string `json:"evidence_boundary"`
		FirstFunder          *struct {
			Counterparty, Asset, Amount string
			Evidence                    []string `json:"evidence"`
		} `json:"first_funder"`
		CashoutCandidates []map[string]any `json:"cashout_candidates"`
		Edges             []struct {
			Direction    string    `json:"direction"`
			Counterparty string    `json:"counterparty"`
			Asset        string    `json:"asset"`
			Amount       string    `json:"amount"`
			FlowRatio    string    `json:"flow_ratio"`
			Transactions int       `json:"transactions"`
			FirstSeen    time.Time `json:"first_seen"`
			LastSeen     time.Time `json:"last_seen"`
			Evidence     []string  `json:"evidence"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.AlgorithmVersion == "" {
		result.AlgorithmVersion = "basic-flow-v1.0"
	}
	now := time.Now().UTC()
	patch := contract.GraphPatch{ID: uuid.NewString(), BaseRevision: 0, CreatedAt: now, Version: contract.Version}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM graph_patches WHERE investigation_id=$1`, investigationID).Scan(&patch.Revision); err != nil {
		return err
	}
	patch.UpsertNodes = append(patch.UpsertNodes, contract.GraphNode{ID: "address:" + chain + ":" + address, Type: "ADDRESS", Subject: &contract.SubjectRef{Type: "address", Chain: chain, Value: address}, Label: "调查核心地址", Properties: map[string]any{"transaction_count": result.TransactionCount, "unique_counterparties": result.UniqueCounterparties}, Version: contract.Version})
	for index, edge := range result.Edges {
		evidenceID := uuid.NewString()
		provenance := contract.Provenance{SourceType: "indexed_token_transfers", SourceRefs: edge.Evidence, Processor: "analysis.flow_stats", Version: result.AlgorithmVersion, ObservedAt: now}
		provenanceJSON, _ := json.Marshal(provenance)
		if _, err := tx.Exec(ctx, `INSERT INTO investigation_evidence(id,investigation_id,evidence_type,title,source_refs,content_hash,provenance) VALUES($1,$2,'transaction_bundle',$3,$4,$5,$6)`, evidenceID, investigationID, fmt.Sprintf("%s %s %s", edge.Direction, edge.Asset, edge.Counterparty), mustJSON(edge.Evidence), outputHash, provenanceJSON); err != nil {
			return err
		}
		counterpartyID := "address:" + chain + ":" + edge.Counterparty
		patch.UpsertNodes = append(patch.UpsertNodes, contract.GraphNode{ID: counterpartyID, Type: "ADDRESS", Subject: &contract.SubjectRef{Type: "address", Chain: chain, Value: edge.Counterparty}, Properties: map[string]any{"relation": edge.Direction}, Version: contract.Version})
		source, target := "address:"+chain+":"+address, counterpartyID
		if edge.Direction == "in" {
			source, target = counterpartyID, source
		}
		patch.UpsertEdges = append(patch.UpsertEdges, contract.GraphEdge{ID: fmt.Sprintf("flow:%s:%d", investigationID, index), Type: "ASSET_FLOW", Source: source, Target: target, Directed: true, Weight: float64(edge.Transactions), EvidenceIDs: []string{evidenceID}, Properties: map[string]any{"asset": edge.Asset, "amount": edge.Amount, "flow_ratio": edge.FlowRatio, "tx_count": edge.Transactions, "first_seen": edge.FirstSeen, "last_seen": edge.LastSeen}, Version: contract.Version})
	}
	patchJSON, _ := json.Marshal(patch)
	if _, err := tx.Exec(ctx, `INSERT INTO graph_patches(id,investigation_id,canvas_id,base_revision,revision,patch) VALUES($1,$2,NULL,$3,$4,$5)`, patch.ID, investigationID, patch.BaseRevision, patch.Revision, patchJSON); err != nil {
		return err
	}
	properties := map[string]any{"transaction_count": result.TransactionCount, "unique_counterparties": result.UniqueCounterparties, "evidence_boundary": result.EvidenceBoundary, "first_funder": result.FirstFunder, "top_outbound_by_asset": result.CashoutCandidates, "graph_patch_revision": patch.Revision}
	if _, err := tx.Exec(ctx, `INSERT INTO investigation_findings(id,investigation_id,finding_type,title,summary,confidence,status,properties,algorithm,algorithm_version) VALUES($1,$2,'direct_flow_summary','一跳资金关系已完成',$3,1,'confirmed',$4,'analysis.flow_stats',$5)`, uuid.NewString(), investigationID, fmt.Sprintf("已聚合 %d 笔代币转账，识别 %d 个直接对手方。", result.TransactionCount, result.UniqueCounterparties), mustJSON(properties), result.AlgorithmVersion); err != nil {
		return err
	}
	return nil
}

// materializeAdvanced converts the stable Phase 5 capability output into
// auditable candidate findings. Pattern evidence remains separate from
// heuristic wallet relationships and net flow is explicitly not called profit.
func materializeAdvanced(ctx context.Context, tx pgx.Tx, investigationID string, raw []byte, outputHash string) error {
	var result struct {
		AlgorithmVersion string           `json:"algorithm_version"`
		DataVersion      string           `json:"data_version"`
		EvidenceBoundary string           `json:"evidence_boundary"`
		Limitations      []string         `json:"limitations"`
		AssetFlows       []map[string]any `json:"asset_flows"`
		RelatedWallets   []map[string]any `json:"related_wallets"`
		Signals          []struct {
			Type       string         `json:"type"`
			Confidence float64        `json:"confidence"`
			Summary    string         `json:"summary"`
			Metrics    map[string]any `json:"metrics"`
			Evidence   []string       `json:"evidence"`
			Nature     string         `json:"nature"`
		} `json:"signals"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	provenanceBase := contract.Provenance{SourceType: "indexed_token_transfers", Processor: "analysis.advanced_patterns", Version: result.AlgorithmVersion, ObservedAt: time.Now().UTC()}
	for _, signal := range result.Signals {
		evidenceID := uuid.NewString()
		provenance := provenanceBase
		provenance.SourceRefs = signal.Evidence
		provenanceJSON, _ := json.Marshal(provenance)
		if _, err := tx.Exec(ctx, `INSERT INTO investigation_evidence(id,investigation_id,evidence_type,title,source_refs,content_hash,provenance) VALUES($1,$2,'deterministic_pattern',$3,$4,$5,$6)`, evidenceID, investigationID, signal.Type, mustJSON(signal.Evidence), outputHash, provenanceJSON); err != nil {
			return err
		}
		properties := map[string]any{"metrics": signal.Metrics, "nature": signal.Nature, "evidence_ids": []string{evidenceID}, "data_version": result.DataVersion, "evidence_boundary": result.EvidenceBoundary}
		if _, err := tx.Exec(ctx, `INSERT INTO investigation_findings(id,investigation_id,finding_type,title,summary,confidence,status,properties,algorithm,algorithm_version) VALUES($1,$2,$3,$4,$5,$6,'candidate',$7,'analysis.advanced_patterns',$8)`, uuid.NewString(), investigationID, strings.ToLower(signal.Type), signal.Type, signal.Summary, signal.Confidence, mustJSON(properties), result.AlgorithmVersion); err != nil {
			return err
		}
	}
	properties := map[string]any{"asset_flows": result.AssetFlows, "related_wallets": result.RelatedWallets, "basis_status": "NET_FLOW_ONLY", "limitations": result.Limitations, "evidence_boundary": result.EvidenceBoundary, "data_version": result.DataVersion}
	_, err := tx.Exec(ctx, `INSERT INTO investigation_findings(id,investigation_id,finding_type,title,summary,confidence,status,properties,algorithm,algorithm_version) VALUES($1,$2,'net_asset_flow','代币净流量已计算（非利润结论）','已计算各资产流入、流出与净流量；缺少成本基础和历史价格时不生成获利金额。',1,'confirmed',$3,'analysis.advanced_patterns',$4)`, uuid.NewString(), investigationID, mustJSON(properties), result.AlgorithmVersion)
	return err
}

// Recover reconciles terminal download jobs once at process startup. It is a
// checkpoint recovery pass, not a polling loop.
func (r *Runtime) Recover(ctx context.Context) {
	rows, err := r.service.pool.Query(ctx, `SELECT s.external_job_id::text,j.status FROM investigation_steps s JOIN download_jobs j ON j.id=s.external_job_id WHERE s.status='waiting' AND j.status IN ('COMPLETED','FAILED','PARTIAL_FAILED','CANCELLED')`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if rows.Scan(&id, &status) == nil {
			b, _ := json.Marshal(map[string]any{"status": status})
			r.HandleJobEvent(ctx, "events.jobs."+id, b)
		}
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
