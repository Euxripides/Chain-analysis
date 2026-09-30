package query

// Contract: ActionFlowSummary never merges a whole chain again.
//
// Measured 2026-09-15 on the real instance: with FINAL the address flow-summary
// aggregate read 85,074,805 rows and held 6.87-10.62 GiB per call (12 real calls
// in 30 minutes, 23.4 s to 52.3 s each), the bloom_filter skip indexes on
// from_address/to_address could not narrow it, and the server refused it with
// Code 241 against the 19.60 GiB ceiling, so the request ended as an opaque 500
// (P4-FLOW-01). The same answer through an explicit argMax group-by on the
// ReplacingMergeTree sorting key reads 1.0-1.4 M rows in 0.3-3.3 s and
// 11-36 MiB, verified equal to FINAL and to LIMIT 1 BY on real production rows.
//
// This test is deliberately source-level: proving the equivalence needs the real
// ClickHouse instance, which is what the 2026-09-15 calibration evidence does.
// What it pins here is that the shape cannot silently regress to FINAL (which
// would double-count the multi-provider duplicate rows) or lose its bound.

import (
	"os"
	"strings"
	"testing"
)

const (
	flowSummaryFunc      = "func (s *Service) ActionFlowSummary("
	flowSummaryDedup     = "argMax(` + amountExpr + `,` + versionColumn + `) AS amt"
	flowSummaryGroupBy   = "GROUP BY ` + key + `"
	flowSummaryBound     = "clickhouse.InteractiveScanContext(ctx)"
	flowSummaryTypedErr  = "clickhouse.DescribeInteractiveScanError(err)"
	flowSummaryAliasIn   = "to_addr=target AND fr_addr!=target"
	flowSummaryAliasOut  = "fr_addr=target AND to_addr!=target"
	flowSummaryFinalSeam = "token_transfers FINAL"
)

func flowSummarySource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("actions.go")
	if err != nil {
		t.Fatalf("read actions.go: %v", err)
	}
	body := string(raw)
	start := strings.Index(body, flowSummaryFunc)
	if start < 0 {
		t.Fatalf("function %s not found in actions.go", flowSummaryFunc)
	}
	body = body[start:]
	nl := string(rune(10))
	if end := strings.Index(body, nl+"}"+nl); end >= 0 {
		body = body[:end]
	}
	return body
}

func TestFlowSummaryDeduplicatesInsteadOfMergingTheWholeChain(t *testing.T) {
	body := flowSummarySource(t)
	if strings.Contains(body, flowSummaryFinalSeam) {
		t.Errorf("ActionFlowSummary runs %q again: FINAL cannot use the from_address/to_address "+
			"bloom_filter skip indexes, reads a whole chain (85,074,805 rows, 6.87-10.62 GiB, "+
			"Code 241 at the server ceiling) and turned into an opaque 500 (P4-FLOW-01)",
			flowSummaryFinalSeam)
	}
	if !strings.Contains(body, flowSummaryDedup) {
		t.Errorf("ActionFlowSummary lost the explicit %q deduplication: without it the "+
			"multi-provider duplicate rows are summed twice (measured ~1.87x on a real address)",
			flowSummaryDedup)
	}
	if !strings.Contains(body, `versionColumn := "source_ingested_at"`) || !strings.Contains(body, `versionColumn = "traced_at"`) {
		t.Error("flow summary must use each table's actual ReplacingMergeTree version column")
	}
	if !strings.Contains(body, flowSummaryGroupBy) {
		t.Errorf("ActionFlowSummary must group by the ReplacingMergeTree sorting key suffix %q, "+
			"which is what FINAL deduplicates on", flowSummaryGroupBy)
	}
	if !strings.Contains(body, `"blockchain.token_transfers", "amount", "tx_hash,log_index,token_address"`) {
		t.Error("EVM/TRON token transfer deduplication key must remain unchanged")
	}
	if !strings.Contains(body, "sumIf(amt,") || strings.Contains(body, "sumIf(amount,") {
		t.Error("the flow aggregate must read the deduplicated alias amt, not the raw amount column")
	}
	if !strings.Contains(body, "minIf(ts,") || !strings.Contains(body, "maxIf(ts,") {
		t.Error("the flow time bounds must read the deduplicated alias ts, not the raw timestamp column")
	}
	if !strings.Contains(body, "uniqExactIf(fr_addr,") || !strings.Contains(body, "uniqExactIf(to_addr,") {
		t.Error("the unique-counterparty counts must read the deduplicated aliases fr_addr/to_addr")
	}
	if !strings.Contains(body, flowSummaryAliasIn) || !strings.Contains(body, flowSummaryAliasOut) {
		t.Error("the in/out predicates must be expressed on the deduplicated aliases " +
			"(to_addr/fr_a): a predicate on a raw column is pushed into the aggregate subquery " +
			"by the planner, which ClickHouse rejects with ILLEGAL_AGGREGATION")
	}
}

// TestFlowSummaryPredicatesUseTheDeduplicatedAliases closes the gap that made the
// first candidate answer 500: the outer predicates named to_addr while the
// aggregate subquery aliased to_a, so ClickHouse answered
// "Code: 47 Unknown expression or function identifier to_addr" (measured on the
// candidate API at 2026-09-15 06:50:39 for both probe addresses). Asserting the
// condition strings alone is not enough -- every identifier a predicate reads
// must be one the aggregate subquery actually defines.
func TestFlowSummaryPredicatesUseTheDeduplicatedAliases(t *testing.T) {
	body := flowSummarySource(t)
	for _, alias := range []string{"amt", "ts", "fr_addr", "to_addr"} {
		if !strings.Contains(body, "AS "+alias) {
			t.Errorf("the aggregate subquery must define the alias %q, which the outer predicates read", alias)
		}
	}
	for _, cond := range []string{flowSummaryAliasIn, flowSummaryAliasOut} {
		normalized := strings.NewReplacer("!=", " ", "=", " ", " AND ", " ").Replace(cond)
		for _, token := range strings.Fields(normalized) {
			if token == "target" {
				continue
			}
			switch token {
			case "amt", "ts", "fr_addr", "to_addr":
			default:
				t.Errorf("predicate %q reads %q, which the aggregate subquery does not alias; "+
					"ClickHouse then answers Code 47 Unknown expression identifier (measured on the "+
					"candidate API at 2026-09-15 06:50:39)", cond, token)
			}
		}
	}
}

func TestFlowSummaryRunsOnTheBoundedInteractiveScanBudget(t *testing.T) {
	body := flowSummarySource(t)
	if !strings.Contains(body, flowSummaryBound) {
		t.Errorf("ActionFlowSummary must run on %q so a pathological caller is refused on its "+
			"own budget instead of loading the shared ClickHouse instance", flowSummaryBound)
	}
	if !strings.Contains(body, flowSummaryTypedErr) {
		t.Errorf("ActionFlowSummary must return %q so a bounded-scan refusal (Code 241 / Code 159) "+
			"reaches the caller as a diagnosable code instead of a blanket 500", flowSummaryTypedErr)
	}
}
