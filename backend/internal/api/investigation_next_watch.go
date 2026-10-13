package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"
	"unicode/utf8"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func newBigIntBytes(raw []byte) string { return new(big.Int).SetBytes(raw).String() }

func (s *Server) nextWatchWriteTx(ctx context.Context, scope tenantScope, id, state string) (pgx.Tx, error) {
	tx, e := s.deps.Store.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			_ = tx.Rollback(ctx)
		}
	}()
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, scope.OrganizationID+":"+scope.WorkspaceID); e != nil {
		return nil, e
	}
	var previous string
	e = tx.QueryRow(ctx, `SELECT state FROM investigation_watch_rules WHERE id=$1 AND organization_id=$2 AND workspace_id=$3`, id, scope.OrganizationID, scope.WorkspaceID).Scan(&previous)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if previous == "disabled" && state != "disabled" {
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM investigation_watch_rules WHERE organization_id=$1 AND workspace_id=$2 AND state!='disabled'`, scope.OrganizationID, scope.WorkspaceID).Scan(&count); e != nil {
			return nil, e
		}
		if count >= 100 {
			return nil, summaryError(429, "WATCH_RULE_LIMIT", "At most 100 enabled/paused rules per workspace")
		}
	}
	ok = true
	return tx, nil
}

func validateWatch(q *nextRequest) error {
	if q.RuleType != "balance_change" && q.RuleType != "allowance_change" && q.RuleType != "control_change" {
		return fmt.Errorf("rule_type must be balance_change, allowance_change or control_change")
	}
	if q.IntervalSeconds == 0 {
		q.IntervalSeconds = 300
	}
	if q.IntervalSeconds < 60 || q.IntervalSeconds > 86400 {
		return fmt.Errorf("interval_seconds must be 60-86400")
	}
	if q.Confirmations == 0 {
		q.Confirmations = 12
	}
	if q.Confirmations < 2 || q.Confirmations > 1000 {
		return fmt.Errorf("confirmations must be 2-1000")
	}
	if !utf8.ValidString(q.Name) || utf8.RuneCountInString(q.Name) == 0 || utf8.RuneCountInString(q.Name) > 200 {
		return fmt.Errorf("name must contain 1-200 characters")
	}
	if q.State == "" {
		q.State = "paused"
	}
	if q.State != "active" && q.State != "paused" && q.State != "disabled" {
		return fmt.Errorf("invalid rule state")
	}
	if q.RuleType == "allowance_change" && (q.TokenContract == "" || len(q.Spenders) == 0) {
		return fmt.Errorf("allowance watch requires token_contract and explicit spenders")
	}
	return nil
}
func (s *Server) workspaceCursorJob(ctx context.Context, scope tenantScope, op, filter string) (nextJob, error) {
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		return nextJob{}, e
	}
	if _, e := s.deps.Store.Pool.Exec(ctx, `INSERT INTO investigation_workspace_cursors(organization_id,workspace_id,cursor_key) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, scope.OrganizationID, scope.WorkspaceID, key); e != nil {
		return nextJob{}, e
	}
	if e := s.deps.Store.Pool.QueryRow(ctx, `SELECT cursor_key FROM investigation_workspace_cursors WHERE organization_id=$1 AND workspace_id=$2`, scope.OrganizationID, scope.WorkspaceID).Scan(&key); e != nil {
		return nextJob{}, e
	}
	return nextJob{ID: scope.OrganizationID + ":" + scope.WorkspaceID, ResultSHA: nextHash(filter), CursorKey: key}, nil
}
func (s *Server) nextWatch(ctx context.Context, q nextRequest, op string) (any, error) {
	scope, e := s.nextTenant(ctx)
	if e != nil {
		return nil, e
	}
	if op == "create_watch_rule" || op == "update_watch_rule" {
		if e = validateWatch(&q); e != nil {
			return nil, e
		}
		config := nextRequest{Chain: q.Chain, Address: q.Address, TokenContract: q.TokenContract, Spenders: q.Spenders, RuleType: q.RuleType, MaxRecords: 5000, MaxNodes: 50, MaxHops: 3, PageSize: 100}
		raw, _ := json.Marshal(config)
		var id string
		var revision int
		if op == "create_watch_rule" {
			tx, e := s.deps.Store.Pool.Begin(ctx)
			if e != nil {
				return nil, e
			}
			defer tx.Rollback(ctx)
			if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, scope.OrganizationID+":"+scope.WorkspaceID); e != nil {
				return nil, e
			}
			var count int
			if e = tx.QueryRow(ctx, `SELECT count(*) FROM investigation_watch_rules WHERE organization_id=$1 AND workspace_id=$2 AND state!='disabled'`, scope.OrganizationID, scope.WorkspaceID).Scan(&count); e != nil {
				return nil, e
			}
			if count >= 100 {
				return nil, summaryError(429, "WATCH_RULE_LIMIT", "At most 100 enabled/paused rules per workspace")
			}
			e = tx.QueryRow(ctx, `INSERT INTO investigation_watch_rules(organization_id,workspace_id,name,rule_type,request,state,interval_seconds,confirmations) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text,revision`, scope.OrganizationID, scope.WorkspaceID, q.Name, q.RuleType, raw, q.State, q.IntervalSeconds, q.Confirmations).Scan(&id, &revision)
			if e != nil {
				return nil, e
			}
			if e = tx.Commit(ctx); e != nil {
				return nil, e
			}
		} else {
			if q.RuleID == "" || q.ExpectedRevision < 1 {
				return nil, fmt.Errorf("rule_id and expected_revision required")
			}
			tx, err := s.nextWatchWriteTx(ctx, scope, q.RuleID, q.State)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback(ctx)
			e = tx.QueryRow(ctx, `UPDATE investigation_watch_rules SET name=$1,rule_type=$2,request=$3,state=$4,interval_seconds=$5,confirmations=$6,revision=revision+1,last_snapshot=NULL,last_hash=NULL,lease_owner=NULL,lease_until=NULL,next_run=now(),updated_at=now() WHERE id=$7 AND organization_id=$8 AND workspace_id=$9 AND revision=$10 RETURNING id::text,revision`, q.Name, q.RuleType, raw, q.State, q.IntervalSeconds, q.Confirmations, q.RuleID, scope.OrganizationID, scope.WorkspaceID, q.ExpectedRevision).Scan(&id, &revision)
			if errors.Is(e, pgx.ErrNoRows) {
				return nil, summaryError(409, "WATCH_REVISION_CONFLICT", "Rule not found or revision changed")
			}
			if e != nil {
				return nil, e
			}
			if e = tx.Commit(ctx); e != nil {
				return nil, e
			}
		}
		return map[string]any{"rule_id": id, "revision": revision, "state": q.State, "delivery": "workspace_event_inbox", "interval_seconds": q.IntervalSeconds, "confirmations": q.Confirmations, "first_evaluation": "Establishes baseline; no change event is fabricated"}, nil
	}
	if op == "set_watch_rule_state" {
		if q.RuleID == "" || q.ExpectedRevision < 1 || (q.State != "active" && q.State != "paused" && q.State != "disabled") {
			return nil, fmt.Errorf("rule_id, expected_revision and valid state required")
		}
		var revision int
		tx, err := s.nextWatchWriteTx(ctx, scope, q.RuleID, q.State)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		e = tx.QueryRow(ctx, `UPDATE investigation_watch_rules SET last_snapshot=CASE WHEN $1='active' AND state!=$1 THEN NULL ELSE last_snapshot END,last_hash=CASE WHEN $1='active' AND state!=$1 THEN NULL ELSE last_hash END,state=$1,revision=revision+CASE WHEN state=$1 THEN 0 ELSE 1 END,lease_owner=NULL,lease_until=NULL,next_run=now(),updated_at=now() WHERE id=$2 AND organization_id=$3 AND workspace_id=$4 AND revision=$5 RETURNING revision`, q.State, q.RuleID, scope.OrganizationID, scope.WorkspaceID, q.ExpectedRevision).Scan(&revision)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, summaryError(409, "WATCH_REVISION_CONFLICT", "Rule not found or revision changed")
		}
		if e != nil {
			return nil, e
		}
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return map[string]any{"rule_id": q.RuleID, "state": q.State, "revision": revision}, nil
	}
	if q.State != "" && q.State != "active" && q.State != "paused" && q.State != "disabled" {
		return nil, fmt.Errorf("invalid state filter")
	}
	cursorJob, e := s.workspaceCursorJob(ctx, scope, op, op+":"+q.RuleID+":"+q.State)
	if e != nil {
		return nil, e
	}
	after := int64(-1)
	if q.Cursor != "" {
		after, e = parseNextCursor(q.Cursor, cursorJob, op)
		if e != nil {
			return nil, e
		}
	}
	var rows pgx.Rows
	if op == "list_watch_rules" {
		rows, e = s.deps.Store.Pool.Query(ctx, `SELECT sequence,jsonb_build_object('rule_id',id,'name',name,'rule_type',rule_type,'request',request,'revision',revision,'state',state,'interval_seconds',interval_seconds,'confirmations',confirmations,'next_run',next_run,'last_error',last_error,'last_snapshot_block',last_snapshot->'point'->'as_of_block','last_snapshot_hash',last_hash,'created_at',created_at) FROM investigation_watch_rules WHERE organization_id=$1 AND workspace_id=$2 AND sequence>$3 AND ($4='' OR state=$4) ORDER BY sequence LIMIT $5`, scope.OrganizationID, scope.WorkspaceID, after, q.State, q.PageSize+1)
	} else {
		rows, e = s.deps.Store.Pool.Query(ctx, `SELECT sequence,jsonb_build_object('sequence',sequence,'rule_id',rule_id,'revision',revision,'kind',kind,'data',data,'created_at',created_at) FROM investigation_watch_events WHERE organization_id=$1 AND workspace_id=$2 AND sequence>$3 AND ($4='' OR rule_id::text=$4) ORDER BY sequence LIMIT $5`, scope.OrganizationID, scope.WorkspaceID, after, q.RuleID, q.PageSize+1)
	}
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []any{}
	last := after
	more := false
	for rows.Next() {
		var seq int64
		var raw []byte
		if e = rows.Scan(&seq, &raw); e != nil {
			return nil, e
		}
		if len(items) == q.PageSize {
			more = true
			break
		}
		var value any
		if e = nextDecode(raw, &value); e != nil {
			return nil, e
		}
		items = append(items, value)
		last = seq
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	var next any
	if more {
		next = signNextCursor(nextCursor{cursorJob.ID, op, cursorJob.ResultSHA, last}, cursorJob.CursorKey)
	}
	return map[string]any{"items": items, "next_cursor": next, "last_sequence": last, "tail_cursor": func() any {
		if last < 0 {
			return nil
		}
		return signNextCursor(nextCursor{cursorJob.ID, op, cursorJob.ResultSHA, last}, cursorJob.CursorKey)
	}(), "pagination": "Live sequence keyset; tail_cursor can poll later events; rule states may change between reads"}, nil
}
func (s *Server) nextWatchLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	owner := uuid.NewString()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if e := s.nextWatchOne(ctx, owner); e != nil && ctx.Err() == nil {
				s.deps.Log.Error("investigation watch worker", "error", e)
			}
		}
	}
}
func (s *Server) nextWatchValue(ctx context.Context, q nextRequest, point chainread.StatePoint) (any, any, error) {
	holder, _ := chain.ParseAddress(q.Address)
	token, _ := chain.ParseAddress(q.TokenContract)
	if q.RuleType == "balance_change" {
		b, e := s.deps.ChainRead.SnapshotBalance(ctx, q.Chain, holder, token, point, investigationOptions())
		return map[string]any{"balance_raw": b.Raw, "token_contract": q.TokenContract}, b, e
	}
	if q.RuleType == "allowance_change" {
		values := map[string]string{}
		results, e := s.nextAllowanceBatch(ctx, q, q.Spenders, point)
		if e != nil {
			return nil, nil, e
		}
		for i, spender := range q.Spenders {
			result := results[i]
			if result.Err != nil {
				return nil, nil, result.Err
			}
			if len(result.Output) != 32 {
				return nil, nil, fmt.Errorf("invalid allowance response")
			}
			values[spender] = newBigIntBytes(result.Output)
		}
		return values, map[string]any{"snapshot": point, "allowances_raw": values}, nil
	}
	value, e := s.nextWatchControl(ctx, q, point)
	if e != nil {
		return nil, nil, e
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("invalid control snapshot")
	}
	projection := map[string]any{}
	for _, field := range []string{"owner", "admin", "proxy_admin", "proxy_admin_owner", "implementation", "beacon", "multisig_signers", "multisig_threshold"} {
		projection[field] = m[field]
	}
	return projection, value, nil
}

func (s *Server) nextWatchControl(ctx context.Context, q nextRequest, p chainread.StatePoint) (any, error) {
	proxy, e := s.investigationProxy(ctx, investigationRequest{Chain: q.Chain, Address: q.Address}, p)
	if e != nil {
		return nil, e
	}
	if proxy["resolution_complete"] != true {
		return nil, fmt.Errorf("proxy state read incomplete")
	}
	out := map[string]any{"implementation": proxy["implementation"], "proxy_admin": proxy["proxy_admin"], "beacon": proxy["beacon"], "evidence": proxy["evidence"]}
	for _, spec := range []struct{ name, signature, output string }{{"owner", "owner()", "address"}, {"admin", "admin()", "address"}, {"multisig_signers", "getOwners()", "address[]"}, {"multisig_threshold", "getThreshold()", "uint256"}} {
		raw, e := s.probeContract(ctx, q.Chain, q.Address, spec.signature, nil, p)
		if e != nil {
			if rpc.AsError(e).Class == rpc.ClassExecutionReverted {
				out[spec.name] = map[string]any{"status": "unavailable", "basis": "method reverted"}
				continue
			}
			return nil, e
		}
		m, e := signatureMethod(spec.signature, []string{spec.output})
		if e != nil {
			return nil, e
		}
		decoded, e := method.DecodeOutput(m, chain.BytesToHex(raw))
		if e != nil || len(decoded) != 1 {
			return nil, fmt.Errorf("invalid control state output")
		}
		out[spec.name] = decoded[0].Value
	}
	if admin, ok := proxy["proxy_admin"].(string); ok {
		raw, e := s.probeContract(ctx, q.Chain, admin, "owner()", nil, p)
		if e != nil && rpc.AsError(e).Class != rpc.ClassExecutionReverted {
			return nil, e
		}
		if e == nil && len(raw) == 32 {
			out["proxy_admin_owner"] = nonzeroAddress(raw)
		}
	}
	return out, nil
}
func (s *Server) nextWatchOne(parent context.Context, owner string) error {
	var id string
	var raw, lastRaw []byte
	var revision, interval int
	var confirmations uint64
	var scope tenantScope
	e := s.deps.Store.Pool.QueryRow(parent, `WITH candidate AS (SELECT id FROM investigation_watch_rules WHERE state='active' AND next_run<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY next_run FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE investigation_watch_rules r SET lease_owner=$1,lease_until=now()+interval '150 seconds' FROM candidate WHERE r.id=candidate.id RETURNING r.id::text,r.request,r.last_snapshot,r.revision,r.interval_seconds,r.confirmations,r.organization_id::text,r.workspace_id::text`, owner).Scan(&id, &raw, &lastRaw, &revision, &interval, &confirmations, &scope.OrganizationID, &scope.WorkspaceID)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	q, e := decodeNext(raw)
	var point chainread.StatePoint
	var value, evidence any
	reorg := false
	if e == nil {
		head, err := s.deps.ChainRead.Block(ctx, q.Chain, chain.Latest(), false, investigationOptions())
		if err != nil {
			e = err
		} else if head == nil || head.Number < confirmations {
			e = fmt.Errorf("confirmed state not yet available")
		} else {
			point, e = s.nextPoint(ctx, nextRequest{Chain: q.Chain, Block: json.RawMessage(fmt.Sprintf("%d", head.Number-confirmations))})
		}
	}
	var previous struct {
		Point    chainread.StatePoint `json:"point"`
		Value    any                  `json:"value"`
		Evidence any                  `json:"evidence"`
	}
	if e == nil && len(lastRaw) > 0 {
		e = nextDecode(lastRaw, &previous)
		if e == nil {
			opt := investigationOptions()
			opt.Consistency = rpc.ConsistencyForceRefresh
			b, err := s.deps.ChainRead.Block(ctx, q.Chain, chain.AtBlock(previous.Point.Block), false, opt)
			if err != nil {
				e = err
			} else if b == nil || b.Hash != previous.Point.Hash {
				reorg = true
			}
		}
	}
	if e == nil {
		value, evidence, e = s.nextWatchValue(ctx, q, point)
	}
	if e == nil {
		e = s.nextBlockGuard(ctx, q.Chain, point)
	}
	if e != nil {
		_, updateErr := s.deps.Store.Pool.Exec(parent, `UPDATE investigation_watch_rules SET last_error='Snapshot read unavailable; baseline retained',next_run=now()+($1*interval '1 second'),lease_owner=NULL,lease_until=NULL WHERE id=$2 AND lease_owner=$3 AND revision=$4`, interval, id, owner, revision)
		if updateErr != nil {
			return updateErr
		}
		s.deps.Log.Warn("investigation watch read failed", "rule_id", id, "error", e)
		return nil
	}
	snapshot := map[string]any{"point": point, "value": value, "evidence": evidence}
	encoded, _ := json.Marshal(snapshot)
	tx, e := s.deps.Store.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var owned bool
	if e = tx.QueryRow(ctx, `SELECT true FROM investigation_watch_rules WHERE id=$1 AND lease_owner=$2 AND revision=$3 AND state='active' AND lease_until>now() FOR UPDATE`, id, owner, revision).Scan(&owned); e != nil {
		return nil
	}
	kind := ""
	if reorg {
		kind = "reorg_baseline_replaced"
	} else if len(lastRaw) > 0 && nextHash(previous.Value) != nextHash(value) {
		kind = q.RuleType
	}
	if kind != "" {
		event := map[string]any{"previous": previous, "current": snapshot, "basis": "Confirmed snapshot comparison; changes between polls may be missed", "retraction_of_previous_snapshot": reorg}
		data, _ := json.Marshal(event)
		key := nextHash(map[string]any{"rule": id, "revision": revision, "before": previous.Point.Hash, "after": point.Hash, "value": value, "kind": kind})
		if _, e = tx.Exec(ctx, `INSERT INTO investigation_watch_events(rule_id,revision,organization_id,workspace_id,event_key,kind,data) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(event_key) DO NOTHING`, id, revision, scope.OrganizationID, scope.WorkspaceID, key, kind, data); e != nil {
			return e
		}
	}
	_, e = tx.Exec(ctx, `UPDATE investigation_watch_rules SET last_snapshot=$1,last_hash=$2,last_error=NULL,next_run=now()+($3*interval '1 second'),lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE id=$4 AND lease_owner=$5 AND revision=$6`, encoded, point.Hash, interval, id, owner, revision)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
