package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Owns only a UUID-named schema; application tables are never fixtures.
func TestNextPostgresScopePaginationIdempotencyAndCancel(t *testing.T) {
	dsn := os.Getenv("NEXT_LIVE_PG_URL")
	if dsn == "" {
		t.Skip("set NEXT_LIVE_PG_URL for isolated PostgreSQL integration")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := "qa_next_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+quoted); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); e != nil {
			t.Error(e)
		}
	}()
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	setup, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	migration, e := os.ReadFile("../../migrations/postgres/0081_investigation_next.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = setup.Exec(ctx, string(migration)); e != nil {
		t.Fatal(e)
	}
	setup.Close()
	if _, e = admin.Exec(ctx, "GRANT USAGE ON SCHEMA "+quoted+" TO blockchain_app"); e != nil {
		t.Fatal(e)
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, e := c.Exec(ctx, "SET ROLE blockchain_app"); return e }
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	s := &Server{deps: Deps{Store: &store.Store{Pool: dbtx.New(pool)}, Raw: new(rawstore.Store)}}
	a := tenantScope{OrganizationID: uuid.NewString(), WorkspaceID: uuid.NewString()}
	b := tenantScope{OrganizationID: uuid.NewString(), WorkspaceID: uuid.NewString()}
	contextA := context.WithValue(ctx, nextWorkerKey{}, &nextWorkerState{Scope: a})
	contextB := context.WithValue(ctx, nextWorkerKey{}, &nextWorkerState{Scope: b})
	q := nextRequest{Chain: "bsc", TxHash: "0x" + strings.Repeat("1", 64), IdempotencyKey: "same-key"}
	submitted, e := s.nextSubmit(contextA, "explain_transaction", q)
	if e != nil {
		t.Fatal(e)
	}
	id := submitted.(map[string]any)["analysis_id"].(string)
	again, e := s.nextSubmit(contextA, "explain_transaction", q)
	if e != nil || again.(map[string]any)["analysis_id"] != id {
		t.Fatal("idempotency failed", e)
	}
	changed := q
	changed.TxHash = "0x" + strings.Repeat("2", 64)
	if _, e = s.nextSubmit(contextA, "explain_transaction", changed); e == nil {
		t.Fatal("idempotency conflict accepted")
	}
	other, e := s.nextSubmit(contextB, "explain_transaction", q)
	if e != nil || other.(map[string]any)["analysis_id"] == id {
		t.Fatal("scope collision", e)
	}
	if _, e = s.nextGetJob(contextB, id); e == nil {
		t.Fatal("foreign analysis visible")
	}
	cancel, e := s.nextJobOperation(contextA, nextRequest{AnalysisID: id}, "cancel_analysis")
	if e != nil || cancel.(map[string]any)["status"] != "cancelled" {
		t.Fatal(cancel, e)
	}
	resumed, e := s.nextJobOperation(contextA, nextRequest{AnalysisID: id}, "resume_analysis")
	if e != nil || resumed.(map[string]any)["status"] != "queued" {
		t.Fatal(resumed, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='completed',result_sha='fixed',result_meta='{"sections":{"evidence":1001}}' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO investigation_analysis_rows(analysis_id,section,ordinal,data) SELECT $1,'evidence',g,jsonb_build_object('ordinal',g,'amount_raw','900719925474099312345678901234567890') FROM generate_series(0,1000) AS g`, id); e != nil {
		t.Fatal(e)
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		page, e := s.nextJobOperation(contextA, nextRequest{AnalysisID: id, Section: "evidence", PageSize: 137, Cursor: cursor}, "get_analysis_results")
		if e != nil {
			t.Fatal(e)
		}
		m := page.(map[string]any)
		if m["record_count"] != int64(1001) {
			t.Fatal(m["record_count"])
		}
		for _, row := range m["items"].([]any) {
			r := row.(map[string]any)
			if r["amount_raw"] != "900719925474099312345678901234567890" {
				t.Fatal("lost amount precision")
			}
			ordinal := r["ordinal"].(json.Number).String()
			if seen[ordinal] {
				t.Fatal("duplicate page row")
			}
			seen[ordinal] = true
		}
		if m["next_cursor"] == nil {
			break
		}
		cursor = m["next_cursor"].(string)
	}
	if len(seen) != 1001 {
		t.Fatal("top-N cutoff", len(seen))
	}
	// Compact pages keep exact counts and the same cursor contract.
	compact := false
	page, e := s.nextJobOperation(contextA, nextRequest{AnalysisID: id, Section: "evidence", PageSize: 1, IncludeMetadata: &compact}, "get_analysis_results")
	if e != nil {
		t.Fatal(e)
	}
	light := page.(map[string]any)
	if _, exists := light["metadata"]; exists {
		t.Fatal("compact mode returned heavy metadata")
	}
	if light["record_count"] != int64(1001) || light["returned_records"] != 1 || light["has_more"] != true {
		t.Fatal(light)
	}
	if _, e = s.nextJobOperation(contextA, nextRequest{AnalysisID: id, Section: "evidence", PageSize: 1, Cursor: light["next_cursor"].(string)}, "get_analysis_results"); e != nil {
		t.Fatal("compact cursor incompatible", e)
	}
	// Metadata count is immutable and avoids re-counting the rows on each page.
	if _, e = pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET result_meta='{"sections":{"evidence":1001},"heavy":"summary"}' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	// Retries retrieve persisted state even if current RPC/object storage is down.
	oldRaw := s.deps.Raw
	s.deps.Raw = nil
	reused, e := s.nextSubmit(contextA, "explain_transaction", q)
	if e != nil || reused.(map[string]any)["status"] != "completed" || reused.(map[string]any)["reused"] != true {
		t.Fatal("outage broke idempotent reuse", reused, e)
	}
	qState := nextRequest{Chain: "bsc", Address: "0x1111111111111111111111111111111111111111", TokenContract: "0x2222222222222222222222222222222222222222", Spenders: []string{"0x3333333333333333333333333333333333333333"}, IdempotencyKey: "state-outage"}
	semantic := qState
	semantic.Mode = "inline"
	semantic.IdempotencyKey = ""
	sha := nextHash(map[string]any{"operation": "get_token_allowances", "version": nextVersion, "request": semantic})
	var stateID string
	if e = pool.QueryRow(ctx, `INSERT INTO investigation_analysis_jobs(organization_id,workspace_id,operation,version,request,request_sha,idempotency_key,boundary,cursor_key,status) VALUES($1,$2,'get_token_allowances',$3,'{}',$4,'state-outage','{"point":{"as_of_block":123}}',decode(repeat('ab',32),'hex'),'completed') RETURNING id::text`, a.OrganizationID, a.WorkspaceID, nextVersion, sha).Scan(&stateID); e != nil {
		t.Fatal(e)
	}
	stateReuse, e := s.nextSubmit(contextA, "get_token_allowances", qState)
	if e != nil || stateReuse.(map[string]any)["analysis_id"] != stateID {
		t.Fatal("state retry consulted unavailable RPC", stateReuse, e)
	}
	qState.Spenders = []string{"0x4444444444444444444444444444444444444444"}
	if _, e = s.nextSubmit(contextA, "get_token_allowances", qState); e == nil {
		t.Fatal("outage bypassed idempotency conflict")
	}
	s.deps.Raw = oldRaw
	// Exhausted leases release ownership and increment fencing exactly once.
	if _, e = pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='running',attempts=3,fence=30,lease_owner=$1,lease_until=now()-interval '1 second' WHERE id=$2`, uuid.NewString(), stateID); e != nil {
		t.Fatal(e)
	}
	if e = s.nextExpireAnalysisLeases(ctx); e != nil {
		t.Fatal(e)
	}
	expired, e := s.nextGetJob(contextA, stateID)
	if e != nil || expired.Status != "failed" || expired.Fence != 31 || expired.ErrorCode == nil || *expired.ErrorCode != "WORKER_RETRY_EXHAUSTED" {
		t.Fatal(expired, e)
	}
	if e = s.nextExpireAnalysisLeases(ctx); e != nil {
		t.Fatal(e)
	}
	expired, e = s.nextGetJob(contextA, stateID)
	if e != nil || expired.Fence != 31 {
		t.Fatal("non-idempotent expiration", expired, e)
	}
	stale, e := pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='completed' WHERE id=$1 AND status='running' AND fence=30`, stateID)
	if e != nil || stale.RowsAffected() != 0 {
		t.Fatal("expired worker published", e)
	}
	if _, e = s.nextJobOperation(contextA, nextRequest{AnalysisID: stateID}, "resume_analysis"); e != nil {
		t.Fatal("exhausted task cannot resume", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='running',attempts=3,lease_until=now()+interval '30 seconds' WHERE id=$1`, stateID); e != nil {
		t.Fatal(e)
	}
	if e = s.nextExpireAnalysisLeases(ctx); e != nil {
		t.Fatal(e)
	}
	safe, e := s.nextGetJob(contextA, stateID)
	if e != nil || safe.Status != "running" {
		t.Fatal("unexpired lease invalidated", safe, e)
	}
	foreign := other.(map[string]any)["analysis_id"].(string)
	owner := uuid.NewString()
	if _, e = pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='running',lease_owner=$1,lease_until=now()+interval '30 seconds',fence=9 WHERE id=$2`, owner, foreign); e != nil {
		t.Fatal(e)
	}
	if _, e = s.nextJobOperation(contextB, nextRequest{AnalysisID: foreign}, "cancel_analysis"); e != nil {
		t.Fatal(e)
	}
	tag, e := pool.Exec(ctx, `UPDATE investigation_analysis_jobs SET status='completed' WHERE id=$1 AND lease_owner=$2 AND fence=9 AND status='running'`, foreign, owner)
	if e != nil || tag.RowsAffected() != 0 {
		t.Fatal("stale worker published after cancellation", e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO investigation_analysis_jobs(organization_id,workspace_id,operation,version,request,request_sha,idempotency_key,boundary,cursor_key) SELECT $1,$2,'explain_transaction',$3,'{}','queue-test','queue-test-'||g,'{}',decode(repeat('ab',32),'hex') FROM generate_series(1,20) AS g`, b.OrganizationID, b.WorkspaceID, nextVersion); e != nil {
		t.Fatal(e)
	}
	if _, e = s.nextJobOperation(contextB, nextRequest{AnalysisID: foreign}, "resume_analysis"); e == nil {
		t.Fatal("resume bypassed workspace queue capacity")
	}
	if _, e = s.nextJobOperation(contextA, nextRequest{AnalysisID: id, Section: "misspelled", PageSize: 100}, "get_analysis_results"); e == nil {
		t.Fatal("unknown section falsely returned empty success")
	}
	if _, e = pool.Exec(ctx, `INSERT INTO investigation_watch_rules(organization_id,workspace_id,name,rule_type,request,state,interval_seconds,confirmations) SELECT $1,$2,'capacity','balance_change','{}','paused',60,12 FROM generate_series(1,100)`, a.OrganizationID, a.WorkspaceID); e != nil {
		t.Fatal(e)
	}
	var ruleID string
	if e = pool.QueryRow(ctx, `INSERT INTO investigation_watch_rules(organization_id,workspace_id,name,rule_type,request,state,interval_seconds,confirmations) VALUES($1,$2,'disabled','balance_change','{}','disabled',60,12) RETURNING id::text`, a.OrganizationID, a.WorkspaceID).Scan(&ruleID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.nextWatch(contextA, nextRequest{RuleID: ruleID, ExpectedRevision: 1, State: "active"}, "set_watch_rule_state"); e == nil {
		t.Fatal("reactivation bypassed workspace rule capacity")
	}
}
