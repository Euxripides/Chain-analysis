package query

// Contract: address-scoped action reads never merge a whole chain again.
//
// Measured 2026-09-21 on the real instance (blockchain.token_transfers: 92,274,258
// rows / 8.37 GiB, ReplacingMergeTree ORDER BY (chain, tx_hash, log_index,
// token_address), bloom_filter skip indexes on from_address/to_address only):
//
//   SELECT count() FROM blockchain.token_transfers FINAL
//    WHERE chain='bsc' AND (from_address='0x7696…' OR to_address='0x7696…')
//   → 27.2 s, whole-chain merge; the skip indexes cannot be used because FINAL
//     merges by the sorting key first. The same question through the explicit
//     dedup below → 0.27 s, same answer (4877).
//
//   page shape (ORDER BY timestamp DESC LIMIT 3): 11.0 s with FINAL → 0.16 s
//     through the row_number subquery, identical rows column by column.
//
//   status-filtered token transfers: 70.6 s with FINAL → 42.4 s explicit
//   (the remainder is the 23.6 M tx_hash IN-set; the status aggregate alone is
//   3.9 s, 0.84 s with the caller's time window pushed down).
//
// The live equivalence needs the real ClickHouse instance (that is what the
// 2026-09-21 calibration above is). What this test pins is that the shape cannot
// silently regress to FINAL (whole-chain merge, 60 s gateway 500) or lose its
// interactive budget.

import (
	"os"
	"strings"
	"testing"
)

func sourceFunc(t *testing.T, file, signature string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	body := string(raw)
	start := strings.Index(body, signature)
	if start < 0 {
		t.Fatalf("function %s not found in %s", signature, file)
	}
	body = body[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	return body
}

func TestActionRowsDeduplicatesInsteadOfMergingTheWholeChain(t *testing.T) {
	body := sourceFunc(t, "actions.go", "func (s *Service) actionRows(")
	if strings.Contains(body, "FINAL WHERE") && !strings.Contains(body, "dedupSupported(dataset)") {
		t.Error("actionRows still runs FINAL unconditionally: on token_transfers that merges the " +
			"whole chain (27.2 s for one address count, measured 2026-09-21) and the request ends " +
			"as a 60 s gateway 500")
	}
	for _, want := range []string{"dedupCount(dataset, whereSQL)", "dedupSource(dataset, whereSQL)", "clickhouse.InteractiveScanContext(ctx)"} {
		if !strings.Contains(body, want) {
			t.Errorf("actionRows must use %q (explicit dedup + bounded interactive scan)", want)
		}
	}
	if !strings.Contains(body, "clickhouse.DescribeInteractiveScanError(err)") {
		t.Error("actionRows must translate a bounded-scan refusal into a diagnosable error " +
			"instead of letting it surface as a blanket 500")
	}
}

func TestDedupSourceMatchesEveryTableSortingKey(t *testing.T) {
	cases := []struct {
		dataset   string
		table     string
		partition string
		version   string
	}{
		{"transactions", "blockchain.transactions", "tx_hash", "source_ingested_at"},
		{"token_transfers", "blockchain.token_transfers", "tx_hash, log_index, token_address", "source_ingested_at"},
		{"nft_transfers", "blockchain.nft_transfers", "tx_hash, log_index, contract_address, token_id", "source_ingested_at"},
		{"internal_transactions", "blockchain.internal_calls", "tx_hash, call_index", "traced_at"},
		{"logs", "blockchain.logs", "tx_hash, log_index", "source_ingested_at"},
	}
	for _, c := range cases {
		sql := dedupSource(c.dataset, "chain=? AND address=?")
		if strings.Contains(sql, "FINAL") {
			t.Errorf("dedupSource(%s) must not use FINAL", c.dataset)
		}
		wantWindow := "row_number() OVER (PARTITION BY " + c.partition + " ORDER BY " + c.version + " DESC)"
		if !strings.Contains(sql, wantWindow) {
			t.Errorf("dedupSource(%s) must deduplicate on the table sorting key with the table "+
				"version column: want %q in %q", c.dataset, wantWindow, sql)
		}
		if !strings.Contains(sql, c.table) {
			t.Errorf("dedupSource(%s) must read %s", c.dataset, c.table)
		}
		if !strings.Contains(sql, "SELECT * EXCEPT _rn") {
			t.Errorf("dedupSource(%s) must hide the internal marker column: %q", c.dataset, sql)
		}
		countSQL := dedupCount(c.dataset, "chain=? AND address=?")
		if strings.Contains(countSQL, "FINAL") || !strings.Contains(countSQL, "uniqExact(tuple("+c.partition+"))") {
			t.Errorf("dedupCount(%s) must count distinct sorting keys, got %q", c.dataset, countSQL)
		}
		if !strings.Contains(countSQL, c.table) {
			t.Errorf("dedupCount(%s) must read %s", c.dataset, c.table)
		}
	}
}

func TestBalancesKeepThePlainFinalRead(t *testing.T) {
	// address_balances holds a few thousand rows; FINAL there is cheap and the
	// dedup key is not the one the transfer tables use, so it must not be routed
	// through dedupSource by accident.
	if dedupSupported("balances") {
		t.Error("balances must not be routed through the transfer-table dedup: its sorting key differs")
	}
	if dedupVersion("balances") != "source_ingested_at" {
		t.Error("balances has no traced_at column; the default version column must stay source_ingested_at")
	}
}

func TestActionOverviewReusesTheBatchAggregate(t *testing.T) {
	body := sourceFunc(t, "actions.go", "func (s *Service) ActionOverview(")
	if !strings.Contains(body, "ActionBatchOverview(") {
		t.Error("ActionOverview must reuse ActionBatchOverview: the previous 7 scalar subqueries " +
			"each merged a whole chain (16.5-27.2 s per count, measured 2026-09-21)")
	}
	if strings.Contains(body, "blockchain.transactions FINAL") || strings.Contains(body, "blockchain.token_transfers FINAL") {
		t.Error("ActionOverview still contains whole-chain FINAL scalar subqueries")
	}
}

func TestRemainingAddressScopedAggregatesAvoidWholeChainFinal(t *testing.T) {
	cases := []struct{ signature, label string }{
		{"func (s *Service) ActionCounterparties(", "ActionCounterparties"},
		{"func (s *Service) ActionFirstFunding(", "ActionFirstFunding"},
		{"func (s *Service) ActionEdges(", "ActionEdges"},
	}
	for _, c := range cases {
		body := sourceFunc(t, "actions.go", c.signature)
		if strings.Contains(body, "token_transfers FINAL") || strings.Contains(body, "transactions FINAL") {
			t.Errorf("%s still merges a whole chain with FINAL on an address-scoped read", c.label)
		}
		if !strings.Contains(body, "clickhouse.InteractiveScanContext(ctx)") {
			t.Errorf("%s must run on the bounded interactive scan budget", c.label)
		}
	}
}

// The first candidate of the dedup rewrite answered 500 on real data:
//
//	code: 184, message: Aggregate function argMax(from_address, source_ingested_at)
//	                    AS from_address is found in WHERE in query
//
// ClickHouse substitutes an alias that shadows a column name into the surrounding
// predicates, and the HAVING pushdown moved it into the aggregate subquery's WHERE.
// The aliases therefore must not collide with the column names they replace
// (the same trap ActionFlowSummary documents with fr_addr/to_addr/amt/ts).
func TestCounterpartiesAliasesDoNotShadowColumnNames(t *testing.T) {
	body := sourceFunc(t, "actions.go", "func (s *Service) ActionCounterparties(")
	for _, want := range []string{"AS amt", "AS fr_addr", "AS to_addr", "sum(amt)", "fr_addr=target", "to_addr=target"} {
		if !strings.Contains(body, want) {
			t.Errorf("ActionCounterparties must express its aggregate reads and predicates on the "+
				"deduplicated aliases (want %q)", want)
		}
	}
	for _, forbidden := range []string{"sum(amount)", "sumIf(amount,", "if(to_address=target", "from_address!=target"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("ActionCounterparties still reads the raw column through %q: an aggregate alias "+
				"that shadows a column name makes ClickHouse substitute it into the subquery WHERE "+
				"(code 184, measured 2026-09-22 on the running API)", forbidden)
		}
	}
}

func TestFirstFundingAliasesDoNotShadowColumnNames(t *testing.T) {
	body := sourceFunc(t, "actions.go", "func (s *Service) ActionFirstFunding(")
	if !strings.Contains(body, "AS rawv") || !strings.Contains(body, "AS txh") {
		t.Error("ActionFirstFunding must alias raw_value/tx_hash away from the column names (code 184 trap)")
	}

}
func TestRowsAndExplorerUseDedupForAddressScopedDatasets(t *testing.T) {
	for _, c := range []struct{ signature, label string }{
		{"func (s *Service) Rows(", "Rows"},
		{"func (s *Service) Explorer(", "Explorer"},
	} {
		body := sourceFunc(t, "query.go", c.signature)
		if !strings.Contains(body, "dedupSource(dataset, where)") && !strings.Contains(body, "dedupSource(dataset, whereSQL)") {
			t.Errorf("%s must route address-scoped datasets through dedupSource", c.label)
		}
		if !strings.Contains(body, "dedupSupported(dataset)") {
			t.Errorf("%s must keep the FINAL fallback only for datasets without a dedup key", c.label)
		}
	}
}

// The status filter on token transfers cannot be expressed as `tx_hash IN (SELECT …)`
// on a large chain: measured 2026-09-22 on bsc the IN form took 45.7 s (the status
// aggregate itself is 3.9 s; the rest is building and probing a 23.6 M value set)
// and the request died on the 60 s gateway. The same answer through
// `LEFT ANY JOIN (…) USING (tx_hash)` took 0.30-0.83 s and projects exactly the 22
// token_transfer columns (a plain `SELECT *` over a LEFT SEMI JOIN leaks the right
// side's tx_hash as a 23rd column, which breaks the strict struct scan).
func TestTokenTransferStatusFilterUsesASemiJoinNotAnInSubquery(t *testing.T) {
	body := sourceFunc(t, "actions.go", "func (s *Service) actionRows(")
	if strings.Contains(body, "tx_hash IN (SELECT tx_hash FROM") {
		t.Error("the token_transfers status filter regressed to tx_hash IN (subquery): " +
			"measured 45.7 s unbounded on bsc (23.6 M value set) and it ends as a 60 s 500")
	}
	for _, want := range []string{"INNER JOIN", "USING (tx_hash)", "statusJoin"} {
		if !strings.Contains(body, want) {
			t.Errorf("the token_transfers status filter must be a semi join (want %q)", want)
		}
	}
}
