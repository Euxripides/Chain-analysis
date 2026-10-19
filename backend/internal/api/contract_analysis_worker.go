package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RunContractAnalysisWorker is a dedicated processor. It never claims jobs from
// the existing 300-second investigation worker or starts download/watch loops.
func (s *Server) RunContractAnalysisWorker(ctx context.Context) {
	owner := uuid.NewString()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if e := s.contractWorkOne(ctx, owner); e != nil && ctx.Err() == nil {
				s.deps.Log.Error("contract analysis worker", "error", e)
			}
		}
	}
}
func (s *Server) contractWorkOne(parent context.Context, owner string) error {
	// A connection-scoped global lock bounds heavy work across duplicate worker
	// processes, including a standby candidate during release verification.
	connection, err := s.deps.Store.Pool.Acquire(parent)
	if err != nil {
		return err
	}
	var admitted bool
	if err = connection.QueryRow(parent, `SELECT pg_try_advisory_lock(hashtextextended('contract-analysis-heavy-v1',0))`).Scan(&admitted); err != nil {
		connection.Release()
		return err
	}
	if !admitted {
		connection.Release()
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		if _, e := connection.Exec(cleanup, `SELECT pg_advisory_unlock(hashtextextended('contract-analysis-heavy-v1',0))`); e != nil {
			_ = connection.Hijack().Close(cleanup)
		} else {
			connection.Release()
		}
	}()
	_, e := s.deps.Store.Pool.Exec(parent, `UPDATE investigation_analysis_jobs SET status='failed',phase='failed',fence=fence+1,lease_owner=NULL,lease_until=NULL,error_code='WORKER_RETRY_EXHAUSTED',error_message='Contract worker lease expired after three attempts',updated_at=now() WHERE version=$1 AND operation=$2 AND status='running' AND lease_until<now() AND attempts>=3`, ca.Version, ca.Operation)
	if e != nil {
		return e
	}
	w := nextWorkerState{Owner: owner}
	var request, boundary []byte
	e = s.deps.Store.Pool.QueryRow(parent, `WITH candidate AS (SELECT id FROM investigation_analysis_jobs WHERE version=$1 AND operation=$2 AND (status='queued' OR (status='running' AND lease_until<now() AND attempts<3)) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE investigation_analysis_jobs j SET status='running',phase='contract_inputs',lease_owner=$3,lease_until=now()+interval '30 seconds',attempts=attempts+1,fence=fence+1,updated_at=now() FROM candidate WHERE j.id=candidate.id RETURNING j.id::text,j.request,j.boundary,j.organization_id::text,j.workspace_id::text,j.fence,j.input_object,j.input_sha`, ca.Version, ca.Operation, owner).Scan(&w.JobID, &request, &boundary, &w.Scope.OrganizationID, &w.Scope.WorkspaceID, &w.Fence, &w.InputObject, &w.InputSHA)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	q, e := ca.Decode(request)
	if e != nil {
		return e
	}
	q.Defaults()
	ctx, cancel := context.WithTimeout(parent, time.Duration(q.Budget.TimeoutSeconds)*time.Second)
	ctx = context.WithValue(ctx, nextWorkerKey{}, &w)
	defer cancel()
	ctx, e = s.contractBudgetContext(ctx, w.Scope, w.JobID, q)
	var usedMs int64
	if e == nil {
		e = s.deps.Store.Pool.QueryRow(ctx, `SELECT processing_ms FROM contract_analysis_budgets WHERE analysis_id=$1`, w.JobID).Scan(&usedMs)
	}
	remaining := time.Duration(q.Budget.TimeoutSeconds)*time.Second - time.Duration(usedMs)*time.Millisecond
	ctx, workCancel := context.WithTimeout(ctx, max(remaining, 0))
	defer workCancel()
	heartbeatCtx := ctx
	var pulse atomic.Int64
	pulse.Store(time.Now().UnixMilli())
	accountTime := func(accountCtx context.Context) error {
		now := time.Now().UnixMilli()
		delta := max(now-pulse.Swap(now), 0)
		_, err := s.deps.Store.Pool.Exec(accountCtx, `UPDATE contract_analysis_budgets SET processing_ms=processing_ms+$2,updated_at=now() WHERE analysis_id=$1 AND EXISTS(SELECT 1 FROM investigation_analysis_jobs j WHERE j.id=$1 AND j.fence=$3)`, w.JobID, delta, w.Fence)
		return err
	}
	defer func() {
		finish, c := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
		defer c()
		_ = accountTime(finish)
	}()
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-heartbeatCtx.Done():
				return
			case <-tick.C:
				tag, err := s.deps.Store.Pool.Exec(heartbeatCtx, `UPDATE investigation_analysis_jobs SET lease_until=now()+interval '30 seconds',updated_at=now() WHERE id=$1 AND lease_owner=$2 AND fence=$3 AND status='running' AND lease_until>now()`, w.JobID, w.Owner, w.Fence)
				if err != nil || tag.RowsAffected() != 1 {
					workCancel()
					return
				}
				if accountTime(heartbeatCtx) != nil {
					workCancel()
					return
				}
			}
		}
	}()
	var b struct {
		Engine string               `json:"engine_digest"`
		Point  chainread.StatePoint `json:"point"`
	}
	decodeErr := json.Unmarshal(boundary, &b)
	if e == nil {
		e = decodeErr
	}
	if e == nil && !contractEngineAllowed(b.Engine) {
		e = summaryError(409, "ANALYSIS_VERSION_CHANGED", "The queued task requires its original engine digest")
	}
	var frozen ca.FrozenInput
	if e == nil {
		cleanup, stop := context.WithTimeout(ctx, 10*time.Second)
		e = (ca.Runner{Image: b.Engine}).RemoveStale(cleanup, w.JobID, w.Fence)
		stop()
	}
	if e == nil {
		frozen, e = s.contractMaterialize(ctx, q, boundary)
	}
	var result ca.Result
	if e == nil {
		result, e = s.contractProcess(ctx, w, q, frozen, b.Engine)
	}
	if e == nil {
		if q.Program != nil {
			e = s.programBlockGuard(ctx, q, b.Point)
		} else if !q.CodeOnly() {
			e = s.contractBlockGuard(ctx, q, b.Point, true)
		}
	}
	if e == nil {
		e = accountTime(ctx)
	}
	if e == nil {
		e = s.contractUsageMetrics(ctx, w.JobID, &result)
	}
	if e == nil {
		e = s.contractPublish(ctx, w, frozen, result)
	}
	if e != nil {
		code, message := "CONTRACT_ANALYSIS_FAILED", "Contract analysis failed; saved input can be resumed"
		var se *exportSummaryError
		if errors.As(e, &se) {
			code, message = se.code, se.message
		}
		var re *rpc.Error
		if errors.As(e, &re) {
			code, message = "RPC_"+strings.ToUpper(string(re.Class)), re.Public()
		}
		if errors.Is(e, context.DeadlineExceeded) {
			code = "CONTRACT_ANALYSIS_TIMEOUT"
		}
		if errors.Is(e, context.Canceled) {
			code = "CONTRACT_ANALYSIS_CANCELLED"
		}
		finish, c := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer c()
		_, err := s.deps.Store.Pool.Exec(finish, `UPDATE investigation_analysis_jobs SET status='failed',phase='failed',lease_owner=NULL,lease_until=NULL,error_code=$1,error_message=$2,updated_at=now() WHERE id=$3 AND lease_owner=$4 AND fence=$5 AND status='running'`, code, message, w.JobID, w.Owner, w.Fence)
		s.deps.Log.Error("contract analysis failed", "analysis_id", w.JobID, "error", e)
		return err
	}
	return nil
}

// Persisted jobs retain their pinned image across a compatible processor release.
// Only operator configured digests are accepted; request input cannot select one.
func contractEngineAllowed(image string) bool {
	if !(ca.Runner{Image: image}).Ready() {
		return false
	}
	if image == os.Getenv("CONTRACT_ENGINE_IMAGE") {
		return true
	}
	for _, allowed := range strings.Split(os.Getenv("CONTRACT_ENGINE_PREVIOUS_IMAGES"), ",") {
		if image == strings.TrimSpace(allowed) {
			return true
		}
	}
	return false
}
func (s *Server) contractPhase(ctx context.Context, w nextWorkerState, phase string) error {
	tag, e := s.deps.Store.Pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET phase=$1,updated_at=now() WHERE id=$2 AND lease_owner=$3 AND fence=$4 AND status='running' AND lease_until>now()`, phase, w.JobID, w.Owner, w.Fence)
	if e == nil && tag.RowsAffected() != 1 {
		e = fmt.Errorf("contract analysis lease lost")
	}
	return e
}
func (s *Server) contractObject(ctx context.Context, object ca.Object, limit int) ([]byte, error) {
	r, n, e := s.deps.Raw.OpenNamed(ctx, s.deps.Raw.Bucket(), object.Key)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	if n != object.Bytes || n > int64(limit) {
		return nil, fmt.Errorf("saved contract object length invalid")
	}
	b, e := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) != n || ca.SHA256(b) != object.SHA256 {
		return nil, summaryError(422, "EVIDENCE_INTEGRITY_FAILED", "Saved contract object checksum mismatch")
	}
	return b, nil
}

type contractCache struct {
	Snapshot    ca.Snapshot            `json:"snapshot"`
	Metrics     map[string]any         `json:"metrics"`
	Sections    map[string][]ca.Object `json:"sections"`
	Version     string                 `json:"version"`
	RuleVersion string                 `json:"rule_version"`
}

func (s *Server) contractCacheResult(ctx context.Context, result ca.Result) (contractCache, error) {
	manifest := contractCache{Snapshot: result.Snapshot, Metrics: result.Metrics, Sections: map[string][]ca.Object{}, Version: result.Version, RuleVersion: result.RuleVersion}
	for section, rows := range result.Sections {
		manifest.Sections[section] = []ca.Object{}
		batch := []json.RawMessage{}
		size := 0
		save := func() error {
			if len(batch) == 0 {
				return nil
			}
			object, e := s.contractSaveObject(ctx, "section-"+section, ca.Row(batch), "application/json")
			if e != nil {
				return e
			}
			manifest.Sections[section] = append(manifest.Sections[section], object)
			batch = nil
			size = 0
			return nil
		}
		for _, row := range rows {
			if len(row) > 4<<20 {
				return manifest, summaryError(422, "CONTRACT_ROW_BUDGET_EXCEEDED", fmt.Sprintf("One analysis row exceeds 4 MiB: section=%s bytes=%d", section, len(row)))
			}
			if size+len(row) > 16<<20 {
				if e := save(); e != nil {
					return manifest, e
				}
			}
			batch = append(batch, row)
			size += len(row)
		}
		if e := save(); e != nil {
			return manifest, e
		}
	}
	return manifest, nil
}
func (s *Server) contractRestoreCache(ctx context.Context, manifest contractCache) (ca.Result, error) {
	result := ca.Result{Snapshot: manifest.Snapshot, Metrics: manifest.Metrics, Version: manifest.Version, RuleVersion: manifest.RuleVersion, Sections: map[string][]json.RawMessage{}}
	for section, objects := range manifest.Sections {
		result.Sections[section] = []json.RawMessage{}
		for _, object := range objects {
			raw, e := s.contractObject(ctx, object, 32<<20)
			if e != nil {
				return result, e
			}
			var rows []json.RawMessage
			if e = json.Unmarshal(raw, &rows); e != nil {
				return result, e
			}
			result.Sections[section] = append(result.Sections[section], rows...)
		}
	}
	return result, nil
}
func (s *Server) contractRunInput(ctx context.Context, w nextWorkerState, q ca.Request, input ca.Input, frozen ca.FrozenInput, engine string) (ca.Result, error) {
	if object, ok := frozen.SourceObjects[input.Snapshot.CodeAddress+":"+input.Snapshot.CodeHash]; ok {
		source, e := s.contractObject(ctx, object, 16<<20)
		if e != nil {
			return ca.Result{}, e
		}
		input.Source = source
	}
	options := nextHash(map[string]any{"input": input, "engine": engine, "adapter": ca.Version})
	var cached []byte
	e := s.deps.Store.Pool.QueryRow(ctx, `SELECT output_manifest FROM contract_analysis_engine_runs WHERE analysis_id=$1 AND options_hash=$2 AND status IN ('completed','partial') ORDER BY finished_at DESC LIMIT 1`, w.JobID, options).Scan(&cached)
	if e == nil {
		var manifest contractCache
		if e = json.Unmarshal(cached, &manifest); e != nil {
			return ca.Result{}, e
		}
		part, err := s.contractRestoreCache(ctx, manifest)
		if err == nil {
			part.Metrics["checkpoint_reused"] = true
		}
		return part, err
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return ca.Result{}, e
	}
	var runID string
	e = s.deps.Store.Pool.QueryRow(ctx, `INSERT INTO contract_analysis_engine_runs(analysis_id,engine,engine_digest,adapter_version,options_hash,status,attempt,fence) SELECT id,'offline_bundle',$2,$3,$4,'running',attempts,$5 FROM investigation_analysis_jobs WHERE id=$1 AND lease_owner=$6 AND fence=$5 AND status='running' RETURNING id::text`, w.JobID, engine, ca.Version, options, w.Fence, w.Owner).Scan(&runID)
	if e != nil {
		return ca.Result{}, e
	}
	if e = s.contractPhase(ctx, w, "decompiling:"+input.Snapshot.CodeAddress); e != nil {
		return ca.Result{}, e
	}
	engineCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	started := time.Now()
	result, e := (ca.Runner{Image: engine}).Run(engineCtx, w.JobID, w.Fence, input)
	if e != nil {
		_, _ = s.deps.Store.Pool.Exec(ctx, `UPDATE contract_analysis_engine_runs SET status='failed',finished_at=now(),duration_ms=$1,error_code='ENGINE_RUN_FAILED',timed_out=$2 WHERE id=$3 AND fence=$4`, time.Since(started).Milliseconds(), errors.Is(engineCtx.Err(), context.DeadlineExceeded), runID, w.Fence)
		return result, e
	}
	if e = ca.ValidateInputResult(input, result.Snapshot); e != nil {
		return ca.Result{}, e
	}
	if !q.CodeOnly() && (result.Snapshot.CodeHash != input.Snapshot.CodeHash || result.Snapshot.BlockHash != input.Snapshot.BlockHash || result.Snapshot.StorageAddress != input.Snapshot.StorageAddress || result.Snapshot.CodeAddress != input.Snapshot.CodeAddress || result.Snapshot.ChainID != input.Snapshot.ChainID || result.Snapshot.EntryAddress != input.Snapshot.EntryAddress || result.Snapshot.StateGranularity != input.Snapshot.StateGranularity) {
		return ca.Result{}, fmt.Errorf("engine snapshot identity mismatch")
	}
	if e = contractCompactMethodDetails(&result); e != nil {
		return result, e
	}
	if e = s.contractSaveArtifacts(ctx, &result); e != nil {
		return result, e
	}
	if q.Program == nil && !q.CodeOnly() {
		s.contractReadState(ctx, q, &result)
		contractEnrichAuthoritySnapshot(&result)
		contractEnrichCallTargets(&result)
	}
	manifest, e := s.contractCacheResult(ctx, result)
	if e != nil {
		return result, e
	}
	peak := int64(0)
	if n, ok := result.Metrics["peak_rss_bytes"].(json.Number); ok {
		peak, _ = n.Int64()
	}
	tag, e := s.deps.Store.Pool.Exec(ctx, `UPDATE contract_analysis_engine_runs SET status='completed',finished_at=now(),duration_ms=$1,peak_rss_bytes=$2,exit_code=0,output_manifest=$3 WHERE id=$4 AND fence=$5 AND EXISTS(SELECT 1 FROM investigation_analysis_jobs WHERE id=$6 AND lease_owner=$7 AND fence=$5 AND status='running' AND lease_until>now())`, time.Since(started).Milliseconds(), peak, ca.Row(manifest), runID, w.Fence, w.JobID, w.Owner)
	if e == nil && tag.RowsAffected() != 1 {
		e = fmt.Errorf("lease lost before engine checkpoint")
	}
	return result, e
}
func (s *Server) contractSaveArtifacts(ctx context.Context, result *ca.Result) error {
	rows := []json.RawMessage{}
	remap := map[string]string{}
	// Build the entire identity map before rewriting embedded proof references.
	for _, raw := range result.Sections["artifacts"] {
		var artifact ca.Artifact
		if e := json.Unmarshal(raw, &artifact); e != nil {
			return e
		}
		if !ca.Identifier.MatchString(artifact.ArtifactID) {
			return fmt.Errorf("invalid engine artifact ID")
		}
		remap[artifact.ArtifactID] = ca.ID(artifact.ArtifactID, result.Snapshot.CodeAddress, result.Snapshot.StorageAddress, result.Snapshot.BlockHash)
	}
	for _, raw := range result.Sections["artifacts"] {
		var artifact ca.Artifact
		if e := json.Unmarshal(raw, &artifact); e != nil {
			return e
		}
		if !ca.Identifier.MatchString(artifact.ArtifactID) {
			return fmt.Errorf("invalid engine artifact ID")
		}
		originalID := artifact.ArtifactID
		artifact.ArtifactID = remap[originalID]
		data := []byte(artifact.Text)
		if artifact.ContentBase64 != "" {
			var e error
			data, e = base64.StdEncoding.DecodeString(artifact.ContentBase64)
			if e != nil {
				return fmt.Errorf("invalid binary artifact encoding")
			}
			artifact.ContentBase64 = ""
		}
		if len(data) > ca.ArtifactLimit {
			return summaryError(422, "ARTIFACT_BUDGET_EXCEEDED", "One engine artifact exceeds 32 MiB")
		}
		if ca.SHA256(data) != artifact.SHA256 {
			return fmt.Errorf("engine artifact checksum mismatch")
		}
		if artifact.Kind == "property_proof" {
			var e error
			data, e = contractScopePropertyProof(data, originalID, remap)
			if e != nil {
				return e
			}
		}
		if artifact.Kind == "state_transition_semantics" {
			var e error
			data, e = contractScopeStateSemantics(data, remap)
			if e != nil {
				return e
			}
		}
		if artifact.Kind == "source_pc_map" {
			var e error
			data, e = contractScopePCMap(data, originalID, remap)
			if e != nil {
				return e
			}
		}
		if artifact.Kind == "conclusion_dependencies" {
			var e error
			data, e = contractScopeDependencyArchive(data, originalID, remap, result)
			if e != nil {
				return e
			}
		}
		object, e := s.contractSaveObject(ctx, "artifact", data, artifact.MediaType)
		if e != nil {
			return e
		}
		artifact.Text = ""
		artifact.SHA256 = object.SHA256
		artifact.Bytes = object.Bytes
		artifact.ObjectKey = object.Key
		rows = append(rows, ca.Row(artifact))
	}
	result.Sections["artifacts"] = rows
	for section, items := range result.Sections {
		if section == "artifacts" {
			continue
		}
		for i, item := range items {
			for oldID, newID := range remap {
				item = json.RawMessage(strings.ReplaceAll(string(item), `"`+oldID+`"`, `"`+newID+`"`))
			}
			result.Sections[section][i] = item
		}
	}
	return nil
}
func mergeContractResult(result *ca.Result, part ca.Result) {
	if part.RuleVersion != "" {
		result.RuleVersion = part.RuleVersion
	}
	for key, rows := range part.Sections {
		if result.Sections[key] == nil {
			result.Sections[key] = []json.RawMessage{}
		}
		result.Sections[key] = append(result.Sections[key], rows...)
	}
	if result.Metrics != nil && part.Metrics != nil {
		runs, _ := result.Metrics["engine_resources"].([]any)
		result.Metrics["engine_resources"] = append(runs, map[string]any{"code_address": part.Snapshot.CodeAddress, "storage_address": part.Snapshot.StorageAddress, "code_hash": part.Snapshot.CodeHash, "metrics": part.Metrics})
	}
}
func (s *Server) contractProcess(ctx context.Context, w nextWorkerState, q ca.Request, frozen ca.FrozenInput, engine string) (ca.Result, error) {
	result := ca.Result{Version: ca.Version, RuleVersion: "funds-semantics-1", Snapshot: frozen.Snapshot, Sections: map[string][]json.RawMessage{}, Metrics: map[string]any{"engine_digest": engine, "rpc_usage_job_id": w.JobID, "credits": nil, "estimated_usd": nil}}
	if q.Program != nil || q.CodeOnly() {
		if len(frozen.Contracts) != 1 {
			return result, fmt.Errorf("invalid frozen program input")
		}
		part, err := s.contractRunInput(ctx, w, q, frozen.Contracts[0], frozen, engine)
		if err != nil {
			return result, err
		}
		mergeContractResult(&result, part)
		if q.CodeOnly() {
			result.Snapshot = part.Snapshot
		}
		return result, nil
	}
	inputs := append([]ca.Input{}, frozen.Contracts...)
	seen := map[string]bool{}
	frontier := append([]map[string]any{}, frozen.Frontier...)
	for index := 0; index < len(inputs); index++ {
		input := inputs[index]
		identity := input.Snapshot.CodeAddress + ":" + input.Snapshot.CodeHash + ":" + input.Snapshot.StorageAddress
		if seen[identity] {
			continue
		}
		seen[identity] = true
		part, e := s.contractRunInput(ctx, w, q, input, frozen, engine)
		if e != nil {
			return result, e
		}
		mergeContractResult(&result, part)
		var facets []map[string]any
		if json.Unmarshal(ca.Row(input.Snapshot.Proxy["facets"]), &facets) == nil {
			for _, facet := range facets {
				address, _ := facet["address"].(string)
				if address == input.Snapshot.CodeAddress {
					continue
				}
				duplicate := false
				for _, known := range inputs {
					if known.Snapshot.CodeAddress == address && known.Snapshot.StorageAddress == input.Snapshot.StorageAddress {
						duplicate = true
					}
				}
				if duplicate {
					continue
				}
				if len(inputs) >= q.Budget.MaxContracts {
					frontier = append(frontier, map[string]any{"kind": "diamond_facet", "address": address, "reason": "max_contracts"})
					continue
				}
				point := chainread.StatePoint{Block: input.Snapshot.BlockNumber, Hash: input.Snapshot.BlockHash, Time: input.Snapshot.BlockTime, Ref: chain.AtBlock(input.Snapshot.BlockNumber)}
				next, e := s.contractCodeInput(ctx, q, point, address, input.Snapshot.StorageAddress, q.Address, engine)
				if e != nil {
					frontier = append(frontier, map[string]any{"kind": "diamond_facet", "address": address, "reason": "code_unavailable"})
					continue
				}
				next.Snapshot.Proxy = map[string]any{"proxy_type": "eip2535_facet", "parent": input.Snapshot.StorageAddress, "selectors": facet["selectors"], "routes": facet["routes"]}
				if q.IncludeSource != nil && *q.IncludeSource {
					if source, err := s.contractSource(ctx, next); err == nil {
						obj, err := s.contractSaveObject(ctx, "source", source, "application/json")
						if err != nil {
							return result, err
						}
						frozen.SourceObjects[next.Snapshot.CodeAddress+":"+next.Snapshot.CodeHash] = obj
					}
				}
				inputs = append(inputs, next)
			}
		}
		// Dependencies are resolved only from literal addresses or type-checked
		// snapshot address slots; unknown expressions remain a resumable frontier.
		for _, row := range part.Sections["calls"] {
			var call map[string]any
			if nextDecode(row, &call) != nil {
				continue
			}
			target, _ := call["target_expression"].(string)
			address, _ := call["resolved_target_address"].(string)
			if address == "" {
				frontier = append(frontier, map[string]any{"kind": "external_expression", "expression": target, "call_id": call["call_id"], "reason": "dynamic_target"})
				continue
			}
			storage := contractCallStorageContext(call, input.Snapshot, address)
			if address == "0x0000000000000000000000000000000000000000" || address == input.Snapshot.CodeAddress && storage == input.Snapshot.StorageAddress {
				continue
			}
			already := false
			for _, known := range inputs {
				if known.Snapshot.CodeAddress == address && known.Snapshot.StorageAddress == storage {
					already = true
					break
				}
			}
			if already {
				continue
			}
			if len(inputs) >= q.Budget.MaxContracts {
				frontier = append(frontier, map[string]any{"kind": "external_contract", "address": address, "reason": "max_contracts"})
				continue
			}
			point := chainread.StatePoint{Block: input.Snapshot.BlockNumber, Hash: input.Snapshot.BlockHash, Time: input.Snapshot.BlockTime, Ref: chain.AtBlock(input.Snapshot.BlockNumber)}
			next, e := s.contractCodeInput(ctx, q, point, address, storage, q.Address, engine)
			if e != nil {
				frontier = append(frontier, map[string]any{"kind": "external_target", "address": address, "reason": "no_code_or_unavailable"})
				continue
			}
			if q.IncludeSource != nil && *q.IncludeSource {
				source, err := s.contractSource(ctx, next)
				if err == nil {
					obj, err := s.contractSaveObject(ctx, "source", source, "application/json")
					if err != nil {
						return result, err
					}
					frozen.SourceObjects[next.Snapshot.CodeAddress+":"+next.Snapshot.CodeHash] = obj
				}
			}
			inputs = append(inputs, next)
		}
	}
	if e := s.contractPhase(ctx, w, "execution_evidence"); e != nil {
		return result, e
	}
	for _, raw := range frozen.Transactions {
		var saved struct {
			TxHash string    `json:"tx_hash"`
			Object ca.Object `json:"object"`
		}
		if e := json.Unmarshal(raw, &saved); e != nil {
			return result, e
		}
		data, e := s.contractObject(ctx, saved.Object, 32<<20)
		if e != nil {
			return result, e
		}
		if e = s.contractExecutionRows(ctx, q, &result, saved.TxHash, data, engine, w, frozen); e != nil {
			return result, e
		}
	}
	for _, c := range frozen.Coverage {
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(c))
	}
	for _, node := range frontier {
		node["frontier_id"] = ca.ID(fmt.Sprint(node))
		result.Sections["frontier"] = append(result.Sections["frontier"], ca.Row(node))
	}
	result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "dependencies", "complete": len(frontier) == 0, "analyzed_code_identities": len(seen), "frontier_count": len(frontier)}))
	if len(q.TxHashes) == 0 {
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "observed_execution", "complete": false, "reason": "no_transactions_selected"}))
	}
	s.contractHistory(ctx, q, &result)
	contractLinkDependencies(&result)
	contractEnrichExternalReturns(&result)
	contractBindReturnCallsites(&result)
	result.Metrics["analyzed_codes"] = len(result.Sections["contracts"])
	result.Metrics["method_count"] = len(result.Sections["methods"])
	return result, nil
}
func (s *Server) contractExecutionRows(ctx context.Context, q ca.Request, result *ca.Result, txHash string, raw []byte, engine string, w nextWorkerState, frozen ca.FrozenInput) error {
	var record struct {
		Transaction   chainread.Transaction `json:"transaction"`
		Receipt       chainread.Receipt     `json:"receipt"`
		Block         chainread.Block       `json:"block"`
		Trace         json.RawMessage       `json:"call_trace"`
		Prestate      json.RawMessage       `json:"prestate"`
		Diff          json.RawMessage       `json:"state_diff"`
		Coverage      []json.RawMessage     `json:"coverage"`
		NetworkFee    string                `json:"network_fee_raw"`
		FeeComponents map[string]any        `json:"network_fee_components"`
		Reads         []json.RawMessage     `json:"provider_reads"`
	}
	if e := json.Unmarshal(raw, &record); e != nil {
		return e
	}
	success := record.Receipt.Status != nil && *record.Receipt.Status == 1
	summary := map[string]any{"tx_hash": txHash, "chain": q.Chain, "from": record.Transaction.From, "to": record.Transaction.To, "input": record.Transaction.Input, "value_raw": record.Transaction.ValueRaw, "success": success, "block_number": record.Block.Number, "block_hash": record.Block.Hash, "transaction_index": record.Transaction.TransactionIndex, "gas_used": record.Receipt.GasUsed, "execution_gas_fee_raw": record.NetworkFee, "network_fee_components": record.FeeComponents, "coverage": record.Coverage, "provider_reads": record.Reads, "method_ids": []string{}}
	artifactID := ca.ID(txHash, "transaction-input")
	object, e := s.contractSaveObject(ctx, "artifact", raw, "application/json")
	if e != nil {
		return e
	}
	result.Sections["artifacts"] = append(result.Sections["artifacts"], ca.Row(ca.Artifact{ArtifactID: artifactID, Kind: "transaction_evidence", Filename: txHash + ".json", MediaType: "application/json", SHA256: object.SHA256, Bytes: object.Bytes, ObjectKey: object.Key}))
	if len(record.Trace) > 0 {
		rows, e := ca.TraceRows(record.Trace, txHash, success, q.Budget.MaxRecords)
		if e != nil {
			result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "call_trace", "tx_hash": txHash, "complete": false, "reason": "trace_rows_exceed_budget_or_invalid"}))
		} else {
			for key, rs := range rows {
				if key == "execution_evidence" {
					for i, raw := range rs {
						ev := rowMap(raw)
						ev["artifact_id"] = artifactID
						rs[i] = ca.Row(ev)
					}
				}
				result.Sections[key] = append(result.Sections[key], rs...)
			}
		}
	}
	if len(record.Diff) > 0 {
		rows, e := ca.StateDiffRows(record.Diff, txHash, q.Budget.MaxRecords)
		if e != nil {
			result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "state_diff", "tx_hash": txHash, "complete": false, "reason": "state_diff_rows_exceed_budget_or_invalid"}))
		} else {
			result.Sections["state_changes"] = append(result.Sections["state_changes"], rows...)
			result.Sections["execution_evidence"] = append(result.Sections["execution_evidence"], ca.Row(map[string]any{"evidence_id": ca.ID(txHash, "state-diff"), "tx_hash": txHash, "kind": "observed_state_change", "artifact_id": artifactID, "source": "prestateTracer/diffMode"}))
		}
	}
	if len(record.Prestate) > 0 {
		var pre map[string]ca.StateAccount
		if e = json.Unmarshal(record.Prestate, &pre); e != nil {
			return e
		}
		for address, account := range pre {
			if account.Code == "" || account.Code == "0x" {
				continue
			}
			if !strings.EqualFold(address, q.Address) {
				inCalls := false
				for _, row := range result.Sections["execution_calls"] {
					var call map[string]any
					_ = nextDecode(row, &call)
					if call["tx_hash"] == txHash && strings.EqualFold(fmt.Sprint(call["code_address"]), address) {
						inCalls = true
						break
					}
				}
				if !inCalls {
					continue
				}
			}
			code, _ := chain.HexToBytes(account.Code)
			codeHash := contractKeccak(code)
			result.Sections["versions"] = append(result.Sections["versions"], ca.Row(map[string]any{"version_id": ca.ID(txHash, address, codeHash), "tx_hash": txHash, "code_address": address, "code_hash": codeHash, "block_number": record.Block.Number, "block_hash": record.Block.Hash, "transaction_index": record.Transaction.TransactionIndex, "granularity": "transaction_prestate", "artifact_id": artifactID}))
			// Historical code is taken from transaction prestate, including changes
			// earlier in the same block; it is never replaced with end-of-block code.
			contexts := map[string]bool{}
			for _, row := range result.Sections["execution_calls"] {
				call := rowMap(row)
				if call["tx_hash"] == txHash && strings.EqualFold(fmt.Sprint(call["code_address"]), address) {
					contexts[fmt.Sprint(call["storage_address"])] = true
				}
			}
			if len(contexts) == 0 {
				contexts[address] = true
			}
			for storage := range contexts {
				matched := false
				for _, row := range result.Sections["contracts"] {
					var c ca.Snapshot
					_ = json.Unmarshal(row, &c)
					if c.CodeHash == codeHash && strings.EqualFold(c.CodeAddress, address) && strings.EqualFold(c.StorageAddress, storage) {
						matched = true
						break
					}
				}
				if matched {
					continue
				}
				if len(result.Sections["contracts"]) >= q.Budget.MaxContracts {
					result.Sections["frontier"] = append(result.Sections["frontier"], ca.Row(map[string]any{"kind": "historical_code", "tx_hash": txHash, "code_address": address, "storage_address": storage, "code_hash": codeHash, "reason": "max_contracts"}))
					continue
				}
				input := ca.Input{Snapshot: ca.Snapshot{Chain: q.Chain, ChainID: frozen.Snapshot.ChainID, BlockNumber: record.Block.Number, BlockHash: record.Block.Hash, BlockTime: time.Unix(int64(record.Block.Timestamp), 0).UTC(), EntryAddress: q.Address, CodeAddress: address, StorageAddress: storage, CodeHash: codeHash, CodeSHA256: ca.SHA256(code), EVMRules: frozen.Snapshot.EVMRules, EngineDigest: engine, StateGranularity: "transaction_prestate", FetchedAt: frozen.Snapshot.FetchedAt}, Bytecode: account.Code, Engines: q.Engines}
				part, err := s.contractRunInput(ctx, w, q, input, frozen, engine)
				if err != nil {
					return err
				}
				mergeContractResult(result, part)
			}
		}
		for i, row := range result.Sections["execution_calls"] {
			call := rowMap(row)
			if call["tx_hash"] != txHash {
				continue
			}
			codeAddress := fmt.Sprint(call["code_address"])
			storage := fmt.Sprint(call["storage_address"])
			account := pre[codeAddress]
			code, _ := chain.HexToBytes(account.Code)
			codeHash := contractKeccak(code)
			ids := []string{}
			for j, mrow := range result.Sections["methods"] {
				m := rowMap(mrow)
				if len(code) > 0 && contractDispatchCandidate(m, call, result.Sections["methods"]) && m["code_hash"] == codeHash && strings.EqualFold(fmt.Sprint(m["code_address"]), codeAddress) && strings.EqualFold(fmt.Sprint(m["storage_address"]), storage) {
					mid := fmt.Sprint(m["method_id"])
					ids = append(ids, mid)
					transactions := stringList(m["observed_transactions"])
					exists := false
					for _, t := range transactions {
						if t == txHash {
							exists = true
						}
					}
					if !exists {
						transactions = append(transactions, txHash)
					}
					m["observed_transactions"] = transactions
					result.Sections["methods"][j] = ca.Row(m)
					if m["verified_signature"] == true {
						signature, _ := m["signature"].(string)
						if abi, err := signatureMethod(signature, nil); err == nil {
							if decoded, err := method.DecodeInput(abi, fmt.Sprint(call["input"])); err == nil {
								call["decoded_parameters"] = decoded
								call["decode_basis"] = "verified_code_and_signature"
							}
						}
					}
				}
			}
			call["method_ids"] = ids
			call["code_hash"] = codeHash
			call["method_identity_status"] = "transaction_prestate_code_and_storage_context"
			result.Sections["execution_calls"][i] = ca.Row(call)
			if call["trace_path"] == "0" {
				summary["method_ids"] = ids
				summary["method_identity_status"] = call["method_identity_status"]
				summary["decoded_parameters"] = call["decoded_parameters"]
			}
		}
	}
	if len(record.Transaction.Input) >= 10 && len(record.Prestate) == 0 {
		selector := strings.ToLower(record.Transaction.Input[:10])
		methods := []string{}
		for _, row := range result.Sections["methods"] {
			var m map[string]any
			_ = nextDecode(row, &m)
			if m["selector"] == selector && m["dispatch_role"] != "overridden_source_definition" {
				methods = append(methods, fmt.Sprint(m["method_id"]))
			}
		}
		summary["method_ids"] = methods
		summary["method_identity_status"] = "selector_candidates_check_execution_code_version"
	}
	if success {
		contractReceiptEvents(result, txHash, artifactID, record.Receipt.Logs, record.Prestate, q.Budget.MaxRecords)
	}
	result.Sections["transactions"] = append(result.Sections["transactions"], ca.Row(summary))
	contractTokenReconciliation(result, txHash, record.Prestate, record.Diff)
	var witness map[string]json.RawMessage
	if e = json.Unmarshal(raw, &witness); e == nil && len(record.Prestate) > 0 {
		_, hasTransaction := witness["raw_transaction"]
		_, hasParent := witness["parent_block"]
		if hasTransaction && hasParent {
			fork := ca.EthereumHardfork(frozen.Snapshot.ChainID, record.Block.Number, record.Block.Timestamp)
			witness["hardfork"] = ca.Row(fork)
			txSnapshot := result.Snapshot
			txSnapshot.BlockNumber, txSnapshot.BlockHash = record.Block.Number, record.Block.Hash
			txSnapshot.BlockTime = time.Unix(int64(record.Block.Timestamp), 0).UTC()
			txSnapshot.StateGranularity, txSnapshot.EVMRules = "transaction_prestate", fork
			input := ca.Input{Operation: "replay", Snapshot: txSnapshot, Witness: ca.Row(witness), Engines: []string{}}
			bounded, cancel := context.WithTimeout(ctx, 180*time.Second)
			part, err := (ca.Runner{Image: engine}).Run(bounded, w.JobID, w.Fence, input)
			cancel()
			if err != nil {
				result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "replay", "tx_hash": txHash, "complete": false, "reason": "isolated_replay_unavailable"}))
			} else {
				if err = s.contractBindCreation(ctx, result, raw, part, artifactID); err != nil {
					return err
				}
				for _, raw := range part.Sections["replays"] {
					for _, eid := range stringList(rowMap(raw)["evidence_ids"]) {
						part.Sections["execution_evidence"] = append(part.Sections["execution_evidence"], ca.Row(map[string]any{"evidence_id": eid, "tx_hash": txHash, "kind": "replay_witness", "artifact_id": artifactID, "execution_rules": fork, "source": "original_transaction_receipt_blocks_prestate_and_diff"}))
					}
				}
				if err = s.contractSaveArtifacts(ctx, &part); err != nil {
					return err
				}
				mergeContractResult(result, part)
			}
		}
	}
	return nil
}
func contractDispatchCandidate(m, call map[string]any, methods []json.RawMessage) bool {
	if call["call_type"] == "SELFDESTRUCT" || call["call_type"] == "CREATE" || call["call_type"] == "CREATE2" {
		return false
	}
	if m["selector"] != call["selector"] || m["dispatch_role"] == "overridden_source_definition" {
		return false
	}
	if fmt.Sprint(call["selector"]) != "" {
		return true
	}
	kind, _ := m["entry_kind"].(string)
	if kind == "receive" {
		return len(fmt.Sprint(call["input"])) <= 2
	}
	if kind == "fallback" {
		if len(fmt.Sprint(call["input"])) <= 2 {
			for _, raw := range methods {
				other := rowMap(raw)
				if other["code_hash"] == m["code_hash"] && other["storage_address"] == m["storage_address"] && other["entry_kind"] == "receive" {
					return false
				}
			}
		}
		return true
	}
	return m["entry"] == "dispatch"
}
func (s *Server) contractPublish(ctx context.Context, w nextWorkerState, frozen ca.FrozenInput, result ca.Result) error {
	if e := s.contractPhase(ctx, w, "publishing"); e != nil {
		return e
	}
	if frozen.Request.Input != nil {
		job, err := s.contractJob(ctx, w.JobID)
		if err != nil {
			return err
		}
		var boundary contractCodeBoundary
		if err = json.Unmarshal(job.Boundary, &boundary); err != nil {
			return err
		}
		if boundary.InputArtifact.ArtifactID == "" {
			return fmt.Errorf("submitted input artifact missing")
		}
		result.Sections["artifacts"] = append(result.Sections["artifacts"], ca.Row(boundary.InputArtifact))
	}
	if e := s.contractExportArtifacts(ctx, &result, frozen); e != nil {
		return e
	}
	cache, e := s.contractCacheResult(ctx, result)
	if e != nil {
		return e
	}
	manifestRaw := ca.Row(cache)
	object, e := s.contractSaveObject(ctx, "result-manifest", manifestRaw, "application/json")
	if e != nil {
		return e
	}
	counts := map[string]int{}
	for k, rows := range result.Sections {
		counts[k] = len(rows)
	}
	meta := map[string]any{"sections": counts, "snapshot": result.Snapshot, "metrics": result.Metrics, "completeness": "partial", "rule_version": result.RuleVersion, "result_format": "section_objects_v1", "coverage_dimensions": len(result.Sections["coverage"])}
	tx, e := s.deps.Store.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var owned bool
	if e = tx.QueryRow(ctx, `SELECT true FROM investigation_analysis_jobs WHERE id=$1 AND lease_owner=$2 AND fence=$3 AND status='running' AND lease_until>now() FOR UPDATE`, w.JobID, w.Owner, w.Fence).Scan(&owned); e != nil {
		return fmt.Errorf("contract publication lease lost: %w", e)
	}
	if _, e = tx.Exec(ctx, `DELETE FROM investigation_analysis_rows WHERE analysis_id=$1`, w.JobID); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM contract_analysis_artifacts WHERE analysis_id=$1`, w.JobID); e != nil {
		return e
	}
	sections := make([]string, 0, len(result.Sections))
	for name := range result.Sections {
		sections = append(sections, name)
	}
	sort.Strings(sections)
	for _, section := range sections {
		batch := &pgx.Batch{}
		for ordinal, row := range result.Sections[section] {
			batch.Queue(`INSERT INTO investigation_analysis_rows(analysis_id,section,ordinal,data) VALUES($1,$2,$3,$4)`, w.JobID, section, ordinal, row)
		}
		if e = tx.SendBatch(ctx, batch).Close(); e != nil {
			return e
		}
	}
	artifacts := map[string]ca.Artifact{}
	for _, row := range result.Sections["artifacts"] {
		var a ca.Artifact
		if e = json.Unmarshal(row, &a); e != nil {
			return e
		}
		if old, ok := artifacts[a.ArtifactID]; ok && old.SHA256 != a.SHA256 {
			return fmt.Errorf("artifact identity conflict")
		}
		artifacts[a.ArtifactID] = a
	}
	for _, a := range artifacts {
		_, e = tx.Exec(ctx, `INSERT INTO contract_analysis_artifacts(analysis_id,artifact_id,organization_id,workspace_id,kind,filename,media_type,object_key,byte_size,sha256,method_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, w.JobID, a.ArtifactID, w.Scope.OrganizationID, w.Scope.WorkspaceID, a.Kind, a.Filename, a.MediaType, a.ObjectKey, a.Bytes, a.SHA256, a.MethodID)
		if e != nil {
			return e
		}
	}
	if frozen.Snapshot.IdentityKind != "code_only" {
		snap := frozen.Snapshot
		_, e = tx.Exec(ctx, `INSERT INTO contract_analysis_snapshots(organization_id,workspace_id,analysis_id,chain_id,block_number,block_hash,entry_address,code_address,storage_address,code_hash,snapshot_manifest_object,sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(analysis_id) DO NOTHING`, w.Scope.OrganizationID, w.Scope.WorkspaceID, w.JobID, snap.ChainID, snap.BlockNumber, snap.BlockHash, snap.EntryAddress, snap.CodeAddress, snap.StorageAddress, snap.CodeHash, w.InputObject, w.InputSHA)
		if e != nil {
			return e
		}
	}
	_, e = tx.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='completed',phase='completed',result_object=$1,result_sha=$2,result_meta=$3,completed_at=now(),updated_at=now(),lease_owner=NULL,lease_until=NULL,error_code=NULL,error_message=NULL WHERE id=$4 AND fence=$5`, object.Key, object.SHA256, ca.Row(meta), w.JobID, w.Fence)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
