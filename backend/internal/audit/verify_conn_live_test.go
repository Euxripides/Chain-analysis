//go:build live

// Real-PostgreSQL evidence for P4-ANCHOR-01.
//
// The black-box probe (scripts/s7/p4-audit-anchor-negative-probe-v1.sh) proves
// the end-to-end consequence: every legitimate admin request to
// POST /api/admin/v1/audit/anchors was refused 409 AUTHZ_WORKSPACE_SCOPE_STALE
// with detail gate_error "failed to deallocate cached statement(s): conn busy".
//
// This test proves the MECHANISM against the real database and the real
// production function, inside a real request transaction -- the state that
// cannot be produced from outside the process:
//
//  1. open a transaction on a raw connection and put it in the context with
//     dbtx.WithTx, exactly like the commit gate does for a gated request;
//  2. run the PRODUCTION function audit.VerifyChain through the dbtx pool, the
//     same call the audit-anchor handler makes (via VerifyAnchors);
//  3. then run one more statement on that same transaction -- the gate's release
//     re-read does exactly this immediately after the handler returns.
//
// Before the fix step 2 left the connection busy (a nested QueryRow inside an
// open Query result set), so step 3 failed with the gate's exact error and the
// request was refused. After the fix both steps succeed.
//
// It is READ ONLY: it writes no row, it rolls the transaction back, and it never
// touches an existing anchor.
//
// Run on the VM (blockchain_api has no direct table privileges; use the
// migration DSN):
//
//	set -a; . deploy/.env; set +a
//	P4ANCHOR_LIVE_DSN="$MIGRATION_DATABASE_URL" go test -tags live -count=1 -v \
//	  -run TestVerifyChainKeepsTheRequestTransactionUsable ./internal/audit/
package audit

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
)

func TestVerifyChainKeepsTheRequestTransactionUsable(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("P4ANCHOR_LIVE_DSN"))
	if dsn == "" {
		t.Skip("P4ANCHOR_LIVE_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	raw, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer raw.Close()
	pool := dbtx.New(raw)

	// Pick the anchor window the way the API does: the newest audit anchor whose
	// range does not start at 1, so the predecessor read is required.
	var fromSeq, toSeq int64
	anchorSQL := "SELECT from_seq, to_seq FROM audit_chain_anchors WHERE chain = $1 AND from_seq > 1 ORDER BY anchor_date DESC LIMIT 1"
	if err := raw.QueryRow(ctx, anchorSQL, ChainAudit).Scan(&fromSeq, &toSeq); err != nil {
		t.Fatalf("read an audit anchor range: %v", err)
	}
	t.Logf("anchor range under test: chain=%s from_seq=%d to_seq=%d", ChainAudit, fromSeq, toSeq)

	tx, err := raw.Begin(ctx)
	if err != nil {
		t.Fatalf("begin request transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := dbtx.WithTx(ctx, tx)

	res, verifyErr := VerifyChain(txCtx, pool, ChainAudit, fromSeq, toSeq)

	// The gate's release re-read: one more statement on the SAME transaction.
	// This is exactly what scopeCommitGateReadToken does right after the handler
	// returns, and its error becomes audit_events_v2.detail.gate_error.
	var one int
	releaseErr := tx.QueryRow(ctx, "SELECT 1").Scan(&one)

	if verifyErr != nil || releaseErr != nil {
		busy := false
		for _, e := range []error{verifyErr, releaseErr} {
			if e != nil && strings.Contains(e.Error(), "conn busy") {
				busy = true
			}
		}
		if busy {
			t.Fatalf("P4-ANCHOR-01 reproduced: the handler statement and the gate's release "+
				"re-read share one connection. VerifyChain err=%v ; post-handler statement on "+
				"the same request transaction err=%v", verifyErr, releaseErr)
		}
		t.Fatalf("VerifyChain err=%v ; post-handler statement err=%v", verifyErr, releaseErr)
	}
	if !res.Valid {
		t.Fatalf("VerifyChain reported an invalid chain for a real anchor range: %v", res.Errors)
	}
	if one != 1 {
		t.Fatalf("unexpected value from the post-handler statement: %d", one)
	}
	t.Logf("VerifyChain ok: checked=%d valid=%t integrity=%s", res.Checked, res.Valid, res.IntegrityStatus)

	tag, err := tx.Exec(ctx, "SELECT 1 WHERE $1 = 1", 1)
	if err != nil {
		t.Fatalf("the request transaction cannot execute after VerifyChain: %v", err)
	}
	if tag.String() == "" {
		t.Fatalf("empty command tag from the post-handler statement")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Logf("rollback note: %v", err)
	}
	t.Log("PASS: VerifyChain left the request transaction idle and reusable")
}
