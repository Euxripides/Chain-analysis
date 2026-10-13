package api

import (
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"strings"
	"testing"
)

func TestTraceFrontierRejectsPoisonAndReverseTime(t *testing.T) {
	seen := map[string]bool{"a": true}
	arrivals := map[string]tracePosition{"a": {Block: 10, Index: 10}}
	front := map[string]bool{"a": true}
	for i := 0; i < 500; i++ {
		if added, limited := extendTraceFrontier(query.LedgerTransfer{Block: 10, Index: uint64(11 + i), From: "a", To: fmt.Sprint(i), Raw: "0"}, front, seen, arrivals, 2, true); added || limited {
			t.Fatal("zero transfer used graph budget")
		}
	}
	if added, limited := extendTraceFrontier(query.LedgerTransfer{Block: 11, From: "a", To: "huge", Raw: strings.Repeat("9", 10000)}, front, seen, arrivals, 2, true); added || limited {
		t.Fatal("unbounded raw parsed by frontier")
	}
	for _, tx := range []query.LedgerTransfer{{Block: 10, Index: 9, From: "a", To: "b", Raw: "1"}, {Block: 11, Index: 1, From: "a", To: "a", Raw: "1"}} {
		if a, l := extendTraceFrontier(tx, front, seen, arrivals, 2, true); a || l {
			t.Fatal(tx)
		}
	}
	if a, l := extendTraceFrontier(query.LedgerTransfer{Block: 20, Index: 1, From: "a", To: "b", Raw: "1"}, front, seen, arrivals, 2, true); !a || l {
		t.Fatal("valid branch missing")
	}
	front = map[string]bool{"b": true}
	if a, l := extendTraceFrontier(query.LedgerTransfer{Block: 15, Index: 1, From: "b", To: "c", Raw: "1"}, front, seen, arrivals, 3, true); a || l {
		t.Fatal("reverse-time branch")
	}
	if a, l := extendTraceFrontier(query.LedgerTransfer{Block: 21, Index: 1, From: "b", To: "c", Raw: "1"}, front, seen, arrivals, 2, true); a || !l {
		t.Fatal("node cap ignored")
	}
	if len(seen) != 2 {
		t.Fatal(seen)
	}
}
func TestTraceModelsRunsOnlyRequestedModel(t *testing.T) {
	for _, m := range []string{"fifo", "proportional"} {
		got := traceModels(m)
		if len(got) != 1 || got[0] != m {
			t.Fatal(got)
		}
	}
	if len(traceModels("both")) != 2 {
		t.Fatal("both missing")
	}
}

func TestTraceRetainedRowsReopenEarlierArrivalPath(t *testing.T) {
	seen := map[string]bool{"a": true}
	arrivals := map[string]tracePosition{"a": {Block: 10}}
	ledger := map[string]query.LedgerTransfer{}
	row := func(from, to string, block uint64) query.LedgerTransfer {
		return query.LedgerTransfer{Hash: fmt.Sprintf("%s-%s", from, to), From: from, To: to, Block: block, Raw: "1"}
	}
	ab, ac, ce, eb, bd := row("a", "b", 100), row("a", "c", 20), row("c", "e", 25), row("e", "b", 30), row("b", "d", 50)
	layers := [][]query.LedgerTransfer{{ac, ab}, {ce, eb, bd}, {eb}, {bd}}
	front := map[string]bool{"a": true}
	for _, rows := range layers {
		next := map[string]bool{}
		for _, tx := range rows {
			if !retainTraceTransfer(ledger, tx, 5) {
				t.Fatal("duplicate exhausted the record budget")
			}
			if added, _ := extendTraceFrontier(tx, front, seen, arrivals, 6, true); added {
				next[tx.To] = true
			}
		}
		front = next
	}
	if arrivals["b"].Block != 30 || arrivals["d"].Block != 50 || len(ledger) != 5 {
		t.Fatal("earlier multi-hop path was lost", arrivals, len(ledger))
	}
	if retainTraceTransfer(ledger, row("d", "f", 60), 5) {
		t.Fatal("new row exceeded budget")
	}
}
func TestTraceSourceRejectsReceiptFromDifferentFork(t *testing.T) {
	n := uint64(1)
	receipt := &chainread.Receipt{TxHash: "tx", BlockNumber: &n, BlockHash: "fork-a"}
	block := &chainread.Block{Number: 1, Hash: "fork-a"}
	if validateTraceSource(receipt, block, "tx") != nil {
		t.Fatal("matching evidence rejected")
	}
	block.Hash = "fork-b"
	if validateTraceSource(receipt, block, "tx") == nil {
		t.Fatal("fork mismatch accepted")
	}
	block.Hash = "fork-a"
	receipt.TxHash = "other"
	if validateTraceSource(receipt, block, "tx") == nil {
		t.Fatal("wrong receipt accepted")
	}
}
