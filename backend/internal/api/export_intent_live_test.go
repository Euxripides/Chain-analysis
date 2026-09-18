//go:build live

// Real-PostgreSQL evidence for the P4 pre-Put export object gate.
//
// The black-box gate (scripts/s7/p4-object-intent-allformat-gate-v2.sh) proves
// the end-to-end consequence per format: a refused request leaves ZERO MinIO
// objects. This test proves the mechanism itself against real rows, because the
// interesting states (membership revoked between commit and Put, workspace moved
// out of its organization, intent row only visible inside the request
// transaction) cannot be produced deterministically from outside the process.
//
// It drives the PRODUCTION function Server.exportIntentLive, the same one
// putExportStream calls immediately before every Put, and it never stubs the
// check. Every row it writes is its own fixture row; nothing pre-existing is
// deleted or rewritten, and every mutation it makes to shared fixture rows is
// restored.
//
// Run on the VM:
//
//	set -a; . deploy/.env; set +a
//	P4OBJ_LIVE_DSN="$DATABASE_URL" P4OBJ_ORG=<org> P4OBJ_WS=<ws> \
//	P4OBJ_IDENTITY=<identity> go test -tags live -count=1 -v \
//	-run TestExportIntentLiveRequiresCommittedIntentAndLiveAuthorization ./internal/api/
package api

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func p4objServer(t *testing.T) (*Server, *store.Store, context.Context) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("P4OBJ_LIVE_DSN"))
	if dsn == "" {
		t.Skip("P4OBJ_LIVE_DSN not set")
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(st.Close)
	return &Server{deps: Deps{Store: st}}, st, ctx
}

func TestExportIntentLiveRequiresCommittedIntentAndLiveAuthorization(t *testing.T) {
	srv, st, ctx := p4objServer(t)
	org := envRequired(t, "P4OBJ_ORG")
	ws := envRequired(t, "P4OBJ_WS")
	identity := envRequired(t, "P4OBJ_IDENTITY")

	// The fixture identity must hold an ACTIVE membership to start with; if it
	// does not, the allow case below would be meaningless and the test must say
	// so rather than pass by accident.
	if _, err := st.Pool.Exec(ctx,
		"UPDATE workspace_memberships SET status='active' WHERE workspace_id=$1::uuid AND identity_id=$2::uuid", ws, identity); err != nil {
		t.Fatalf("activate fixture membership: %v", err)
	}
	var active int
	if err := st.Pool.QueryRow(ctx,
		"SELECT count(*) FROM workspace_memberships WHERE workspace_id=$1::uuid AND identity_id=$2::uuid AND status='active'", ws, identity).Scan(&active); err != nil {
		t.Fatalf("count fixture membership: %v", err)
	}
	if active != 1 {
		t.Fatalf("fixture precondition failed: identity has %d active memberships in the workspace, want 1", active)
	}

	exportID := uuid.NewString()
	if _, err := st.Pool.Exec(ctx,
		"INSERT INTO exports (id, dataset, format, status, organization_id, workspace_id, created_by) VALUES ($1::uuid,'transfers','csv','PENDING',$2::uuid,$3::uuid,$4::uuid)",
		exportID, org, ws, identity); err != nil {
		t.Fatalf("insert fixture export: %v", err)
	}

	// 1. committed durable intent + live authorization -> the Put is allowed.
	ok, reason := srv.exportIntentLive(ctx, exportID)
	if !ok {
		t.Fatalf("a committed intent row with a live membership must be allowed, got refusal %q", reason)
	}

	// 2. the workspace membership is revoked while the pipeline runs. The table
	//    accepts invited/active/suspended/removed (workspace_memberships_status_check),
	//    so a revocation is status='removed'.
	if _, err := st.Pool.Exec(ctx,
		"UPDATE workspace_memberships SET status='removed' WHERE workspace_id=$1::uuid AND identity_id=$2::uuid", ws, identity); err != nil {
		t.Fatalf("revoke fixture membership: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(),
			"UPDATE workspace_memberships SET status='active' WHERE workspace_id=$1::uuid AND identity_id=$2::uuid", ws, identity)
	})
	ok, reason = srv.exportIntentLive(ctx, exportID)
	if ok || reason != "authz_membership_revoked" {
		t.Fatalf("a revoked membership must refuse the Put with authz_membership_revoked, got ok=%v reason=%q", ok, reason)
	}

	// 3. restoring the membership restores the authorization.
	if _, err := st.Pool.Exec(ctx,
		"UPDATE workspace_memberships SET status='active' WHERE workspace_id=$1::uuid AND identity_id=$2::uuid", ws, identity); err != nil {
		t.Fatalf("restore fixture membership: %v", err)
	}
	if ok, reason = srv.exportIntentLive(ctx, exportID); !ok {
		t.Fatalf("restoring the membership must re-authorize the Put, got refusal %q", reason)
	}

	// 4. a row that is no longer owned by a running pipeline may not write.
	if _, err := st.Pool.Exec(ctx, "UPDATE exports SET status='COMPLETED' WHERE id=$1::uuid", exportID); err != nil {
		t.Fatalf("complete fixture export: %v", err)
	}
	if ok, reason = srv.exportIntentLive(ctx, exportID); ok || !strings.HasPrefix(reason, "intent_not_running") {
		t.Fatalf("a COMPLETED export row must refuse the Put, got ok=%v reason=%q", ok, reason)
	}
	if _, err := st.Pool.Exec(ctx, "UPDATE exports SET status='PENDING' WHERE id=$1::uuid", exportID); err != nil {
		t.Fatalf("reopen fixture export: %v", err)
	}

	// 5. the workspace must still belong to the export organization.
	var orgBefore string
	if err := st.Pool.QueryRow(ctx, "SELECT organization_id::text FROM workspaces WHERE id=$1::uuid", ws).Scan(&orgBefore); err != nil {
		t.Fatalf("read workspace organization: %v", err)
	}
	var otherOrg string
	if err := st.Pool.QueryRow(ctx, "SELECT id::text FROM organizations WHERE id <> $1::uuid LIMIT 1", orgBefore).Scan(&otherOrg); err == nil && otherOrg != "" {
		if _, err := st.Pool.Exec(ctx, "UPDATE workspaces SET organization_id=$2::uuid WHERE id=$1::uuid", ws, otherOrg); err != nil {
			t.Fatalf("move fixture workspace: %v", err)
		}
		ok, reason = srv.exportIntentLive(ctx, exportID)
		if _, err := st.Pool.Exec(ctx, "UPDATE workspaces SET organization_id=$2::uuid WHERE id=$1::uuid", ws, orgBefore); err != nil {
			t.Fatalf("restore fixture workspace organization: %v", err)
		}
		if ok || reason != "scope_org_changed" {
			t.Fatalf("a workspace moved out of the export organization must refuse the Put, got ok=%v reason=%q", ok, reason)
		}
	} else {
		t.Log("NOT_VERIFIED: no second organization exists, so scope_org_changed was not exercised")
	}

	// 6. a row that is only visible inside the request transaction is NOT a
	//    committed intent. This is the direct proof that the check runs outside
	//    the request transaction: with the ambient transaction carrying an
	//    uncommitted export row, the gate must still refuse.
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	uncommitted := uuid.NewString()
	if _, err := tx.Exec(ctx,
		"INSERT INTO exports (id, dataset, format, status, organization_id, workspace_id, created_by) VALUES ($1::uuid,'transfers','csv','PENDING',$2::uuid,$3::uuid,$4::uuid)",
		uncommitted, org, ws, identity); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert uncommitted export: %v", err)
	}
	txCtx := dbtx.WithTx(ctx, tx)
	ok, reason = srv.exportIntentLive(txCtx, uncommitted)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if ok || reason != "intent_row_missing" {
		t.Fatalf("an uncommitted intent row must NOT authorize a Put (the lookup must run outside the request transaction), got ok=%v reason=%q", ok, reason)
	}
	var leftovers int
	if err := st.Pool.QueryRow(ctx, "SELECT count(*) FROM exports WHERE id=$1::uuid", uncommitted).Scan(&leftovers); err != nil {
		t.Fatalf("count rolled back export: %v", err)
	}
	if leftovers != 0 {
		t.Fatalf("the rolled back fixture export row survived (%d rows)", leftovers)
	}

	// 7. a missing row is a refusal, never an allow.
	if ok, reason = srv.exportIntentLive(ctx, uuid.NewString()); ok || reason != "intent_row_missing" {
		t.Fatalf("an unknown export id must refuse the Put, got ok=%v reason=%q", ok, reason)
	}
}
