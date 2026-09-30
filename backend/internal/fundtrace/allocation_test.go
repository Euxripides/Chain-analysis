package fundtrace

import (
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"testing"
)

func TestMixedFundsBothModelsConserve(t *testing.T) {
	tx := []query.LedgerTransfer{{Hash: "seed", Index: 1, From: "s", To: "a", Raw: "100", Block: 1}, {Hash: "out", Index: 2, From: "a", To: "b", Raw: "100", Block: 2}}
	for _, model := range []string{"fifo", "proportional"} {
		r, e := Allocate(model, tx, map[string]string{"s": "100", "a": "100"}, "seed", 1, "100", 3)
		if e != nil {
			t.Fatal(e)
		}
		if r.AccountedRaw != "100" {
			t.Fatal(r)
		}
		if model == "fifo" && r.Remaining["a"] != "100" {
			t.Fatal(r)
		}
		if model == "proportional" && (r.Remaining["a"] != "50" || r.Remaining["b"] != "50") {
			t.Fatal(r)
		}
	}
}
func TestLargeAmountsRoundingAndFrontier(t *testing.T) {
	tx := []query.LedgerTransfer{{Hash: "seed", Index: 1, From: "s", To: "a", Raw: "9007199254740993000000000001", Block: 1}, {Hash: "out", Index: 2, From: "a", To: "b", Raw: "2", Block: 2}, {Hash: "out2", Index: 3, From: "b", To: "c", Raw: "1", Block: 3}}
	r, e := Allocate("proportional", tx, map[string]string{"s": "9007199254740993000000000001", "a": "1"}, "seed", 1, "9007199254740993000000000001", 1)
	if e != nil {
		t.Fatal(e)
	}
	if r.AccountedRaw != r.SeedRaw || r.FrontierRaw != "1" {
		t.Fatal(r)
	}
}

func TestMixedHopFrontierPreservesEligibleEdge(t *testing.T) {
	tx := []query.LedgerTransfer{{Hash: "seed", Index: 1, Block: 1, From: "s", To: "a", Raw: "100"}, {Hash: "ab", Index: 2, Block: 2, From: "a", To: "b", Raw: "50"}, {Hash: "ba", Index: 3, Block: 3, From: "b", To: "a", Raw: "25"}, {Hash: "ac", Index: 4, Block: 4, From: "a", To: "c", Raw: "75"}}
	r, e := Allocate("proportional", tx, map[string]string{"s": "100"}, "seed", 1, "100", 2)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, edge := range r.Edges {
		if edge.Transfer.Hash == "ac" && edge.AttributedRaw == "50" && edge.Hops == 1 {
			found = true
		}
	}
	if !found || r.FrontierRaw != "25" || r.AccountedRaw != "100" {
		t.Fatal(r)
	}
}
