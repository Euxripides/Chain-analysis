package query

import (
	"context"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBridgeSearchSQLUsesBoundScopeAndNoCount(t *testing.T) {
	r := crosschain.CandidateRequest{SearchChain: "solana", SearchAddresses: []string{"CaseSensitiveBase58"}, SearchDirection: "out", TokenContract: "MintWithCase", StartTime: time.Now(), EndTime: time.Now().Add(time.Minute)}
	q, args := bridgeSearchSQL(r, "token_transfers", 501)
	for _, required := range []string{"chain=?", "from_address IN (?)", "timestamp>=?", "timestamp<=?", "token_address=?", "LIMIT ?"} {
		if !strings.Contains(q, required) {
			t.Fatalf("missing constraint %s: %s", required, q)
		}
	}
	if strings.Contains(q, "CaseSensitive") || strings.Contains(q, "MintWithCase") || strings.Contains(q, "count(") || strings.Contains(q, " FINAL ") {
		t.Fatal("query must bind values and avoid count scans", q)
	}
	if args[1].([]string)[0] != "CaseSensitiveBase58" || args[len(args)-2] != "MintWithCase" {
		t.Fatal("case changed", args)
	}
}

func TestLiveCrossChainBoundedRead(t *testing.T) {
	if os.Getenv("CROSSCHAIN_LIVE_QUERY") != "1" {
		t.Skip("explicit read-only ClickHouse release probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	ch, e := clickhouse.New(ctx, os.Getenv("CLICKHOUSE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer ch.Close()
	s := New(ch)
	r := crosschain.CandidateRequest{Anchor: crosschain.Anchor{Chain: "solana", TxHash: "3QvRpJyWnMomnxPTApBeMHdZvb5BAeWd9Bu8A4rDBQnJ8hMvbX49GUrwFJuBVNQmhH7u4EopmS3MJQSzf34ouma5", Dataset: "token_transfers"}, SearchChain: "bsc", SearchAddresses: []string{"0x3d6539362034b8e4f4171fd69dc40563257f4ad9"}, SearchDirection: "in", Direction: "forward", AssetKind: "native", AssetRelation: "swap", ExpectedAmount: "0.007631125135013813", StartTime: time.Date(2026, 5, 17, 16, 17, 0, 0, time.UTC), EndTime: time.Date(2026, 5, 17, 16, 38, 0, 0, time.UTC), ScanLimit: 100}
	rows, truncated, e := s.CrossChainSearch(ctx, r)
	if e != nil {
		t.Fatal("bounded search", e)
	}
	t.Logf("candidate rows=%d truncated=%v", len(rows), truncated)
	rows, truncated, e = s.CrossChainAnchor(ctx, r.Anchor)
	if e != nil {
		t.Fatal("anchor", e)
	}
	if len(rows) == 0 || truncated {
		t.Fatalf("source anchor unavailable rows=%d truncated=%v", len(rows), truncated)
	}
	t.Logf("source anchor rows=%d time=%s", len(rows), rows[0].Timestamp)
}
