package audit

// Contract: VerifyChain keeps EXACTLY ONE in-flight statement on its
// connection, always.
//
// pgx allows one in-flight statement per connection. A second statement issued
// while a Query result set is still open fails with "conn busy", and the failing
// Close marks the still-open statement as invalidated in pgx's statement cache.
// On a *dbtx.Pool whose context carries the request transaction -- which is what
// the commit gate does for every gated write route -- both statements run on the
// SAME connection, so the gate's release re-read then fails with
//
//	failed to deallocate cached statement(s): conn busy
//
// and the gate fails closed: EVERY legitimate request to
// POST /api/admin/v1/audit/anchors was refused 409 AUTHZ_WORKSPACE_SCOPE_STALE
// (P4-ANCHOR-01, audit_events_v2 seq 54387..54390). The fix is ordering, not a
// weakened check: read the predecessor hash BEFORE opening the range result set.
//
// This test pins the ordering in the source so the defect cannot come back
// silently. It is deliberately source-level: a behavioural test needs a real
// PostgreSQL inside a request transaction, which is what the live test
// (verify_conn_live_test.go, build tag "live") does.

import (
	"os"
	"strings"
	"testing"
)

const (
	verifyChainOpen     = "func VerifyChain("
	verifyChainRangeQ   = "rows, err := pool.Query(ctx, query, args...)"
	verifyChainPredRead = "SELECT event_hash FROM %s WHERE chain = $1 AND seq = $2"
	verifyChainLoop     = "for rows.Next()"
)

func verifyChainSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("verify.go")
	if err != nil {
		t.Fatalf("read verify.go: %v", err)
	}
	body := string(raw)
	start := strings.Index(body, verifyChainOpen)
	if start < 0 {
		t.Fatalf("function %s not found in verify.go", verifyChainOpen)
	}
	body = body[start:]
	nl := string(rune(10))
	if end := strings.Index(body, nl+"}"+nl); end >= 0 {
		body = body[:end]
	}
	return body
}

func TestVerifyChainReadsThePredecessorBeforeOpeningTheRangeResultSet(t *testing.T) {
	body := verifyChainSource(t)
	rangeAt := strings.Index(body, verifyChainRangeQ)
	if rangeAt < 0 {
		t.Fatalf("the range query %s is gone from VerifyChain", verifyChainRangeQ)
	}
	predAt := strings.Index(body, verifyChainPredRead)
	if predAt < 0 {
		t.Fatalf("the predecessor read %s is gone from VerifyChain", verifyChainPredRead)
	}
	if predAt > rangeAt {
		t.Errorf("the predecessor hash is read AFTER the range result set is opened "+
			"(predecessor at byte %d, range query at byte %d); a second statement on an "+
			"open result set is conn-busy and fail-closes every gated request (P4-ANCHOR-01)",
			predAt, rangeAt)
	}
}

func TestVerifyChainIssuesNoStatementWhileTheRangeResultSetIsOpen(t *testing.T) {
	body := verifyChainSource(t)
	rangeAt := strings.Index(body, verifyChainRangeQ)
	if rangeAt < 0 {
		t.Fatalf("the range query %s is gone from VerifyChain", verifyChainRangeQ)
	}
	tail := body[rangeAt:]
	nl := string(rune(10))
	if cut := strings.Index(tail, nl); cut >= 0 {
		tail = tail[cut+len(nl):]
	}
	loopAt := strings.Index(tail, verifyChainLoop)
	if loopAt < 0 {
		t.Fatalf("the %s consumption loop is gone from VerifyChain", verifyChainLoop)
	}
	between := tail[:loopAt]
	for _, forbidden := range []string{
		"pool.Query(", "pool.QueryRow(", "pool.Exec(", "pool.SendBatch(", "pool.CopyFrom(",
	} {
		if strings.Contains(between, forbidden) {
			t.Errorf("VerifyChain issues %s between the range query and its consumption loop; "+
				"the connection is busy and the request will be refused (P4-ANCHOR-01)", forbidden)
		}
	}
}
