package api

// Contract: a gated write route that reaches an external dependency BEFORE the
// commit decision must revalidate the caller scope immediately before that call.
//
// The commit gate rolls back PostgreSQL only. The P4 matrix measured a refused
// (409, rolled back) download preview charging a 21 177 ms ClickHouse FINAL scan
// to a scope that had already been invalidated (P4-CH-01). downloadEstimate is
// the only ClickHouse query on that path, so the recheck has to sit between the
// cheap local checks and the first ClickHouse statement.
//
// Source-level on purpose: the ordering is the property, and a behavioural test
// would need a real ClickHouse plus a real mid-handler scope mutation.

import (
	"os"
	"strings"
	"testing"
)

const (
	dlRecheckCall  = "s.scopeCommitGateRecheck(ctx, PrincipalFrom(ctx))"
	dlFirstCHQuery = "s.deps.CH.DB().QueryRow(ctx,"
	dlEstimateOpen = "func (s *Server) downloadEstimate("
)

func TestDownloadEstimateRevalidatesTheScopeBeforeItQueriesClickHouse(t *testing.T) {
	raw, err := os.ReadFile("sprint5_downloads.go")
	if err != nil {
		t.Fatalf("read sprint5_downloads.go: %v", err)
	}
	body := string(raw)
	start := strings.Index(body, dlEstimateOpen)
	if start < 0 {
		t.Fatalf("function %s not found", dlEstimateOpen)
	}
	body = body[start:]
	nl := string(rune(10))
	if end := strings.Index(body, nl+"}"+nl); end >= 0 {
		body = body[:end]
	}
	recheckAt := strings.Index(body, dlRecheckCall)
	if recheckAt < 0 {
		t.Fatalf("downloadEstimate does not call %s: a refused request would still "+
			"charge external ClickHouse work (P4-CH-01)", dlRecheckCall)
	}
	chAt := strings.Index(body, dlFirstCHQuery)
	if chAt < 0 {
		t.Fatalf("the ClickHouse statement %s is gone from downloadEstimate", dlFirstCHQuery)
	}
	if recheckAt > chAt {
		t.Errorf("downloadEstimate queries ClickHouse (byte %d) BEFORE it revalidates the "+
			"caller scope (byte %d); P4-CH-01", chAt, recheckAt)
	}
}

func TestScopeCommitGateRecheckReadsOutsideTheRequestTransaction(t *testing.T) {
	raw, err := os.ReadFile("scopecommit_gate.go")
	if err != nil {
		t.Fatalf("read scopecommit_gate.go: %v", err)
	}
	body := string(raw)
	start := strings.Index(body, "func (s *Server) scopeCommitGateRecheck(")
	if start < 0 {
		t.Fatalf("scopeCommitGateRecheck is gone: no reusable scope recheck helper exists (P4-CH-01)")
	}
	body = body[start:]
	nl := string(rune(10))
	if end := strings.Index(body, nl+"}"+nl); end >= 0 {
		body = body[:end]
	}
	for _, want := range []string{
		"dbtx.WithoutTx(ctx)",
		"scopeCommitGateTokenQuery",
		"errScopeCommitGateScopeStale",
		"pgx.ErrNoRows",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scopeCommitGateRecheck must contain %s (a recheck that joins the request "+
				"transaction cannot see the mutation it is checking)", want)
		}
	}
}
