package fundtrace

import (
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"testing"
)

func TestSourceBatchDoesNotInheritMixedWalletTotal(t *testing.T) {
	rows := []query.LedgerTransfer{{Hash: "seed", Index: 1, Block: 1, From: "source", To: "middle", Raw: "50000000000000000000000"}, {Hash: "out", Index: 2, Block: 2, From: "middle", To: "exchange", Raw: "679771158390787859739922"}}
	for _, model := range []string{"proportional", "fifo"} {
		r, e := Allocate(model, rows, map[string]string{"source": "50000000000000000000000", "middle": "629771158390787859739922"}, "seed", 1, "50000000000000000000000", 5, "batch-50k")
		if e != nil {
			t.Fatal(e)
		}
		Format(&r, 18)
		if len(r.Edges) != 1 {
			t.Fatal(r)
		}
		edge := r.Edges[0]
		if edge.ActualAmount != "679771.158390787859739922" || edge.AttributedAmount != "50000" || edge.Type != "inferred" || edge.SourceBefore != "50000" || edge.TotalBefore != edge.ActualAmount {
			t.Fatal(edge)
		}
		if len(edge.Consumptions) != 1 || edge.Consumptions[0].BeforeRaw != edge.Consumptions[0].ConsumedRaw || edge.Consumptions[0].AfterRaw != "0" || edge.Confidence != nil {
			t.Fatal(edge)
		}
	}
}
func TestModelComparisonMeasuresTerminalRedistribution(t *testing.T) {
	rows := []query.LedgerTransfer{{Hash: "seed", Index: 1, Block: 1, From: "s", To: "m", Raw: "50"}, {Hash: "out", Index: 2, Block: 2, From: "m", To: "e", Raw: "100"}}
	a, e := Allocate("proportional", rows, map[string]string{"s": "50", "m": "100"}, "seed", 1, "50", 5)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Allocate("fifo", rows, map[string]string{"s": "50", "m": "100"}, "seed", 1, "50", 5)
	if e != nil {
		t.Fatal(e)
	}
	c := Compare(a, b, 0)
	if c["model_consistency"] != "divergent" || c["difference_amount_raw"] != "33" {
		t.Fatal(c)
	}
	if Compare(a, a, 0)["model_consistency"] != "consistent" {
		t.Fatal("identical ledgers diverged")
	}
}

func TestMissingBalanceGapIsExplicit(t *testing.T) {
	rows := []query.LedgerTransfer{{Hash: "seed", Index: 1, Block: 1, From: "s", To: "m", Raw: "50"}, {Hash: "out", Index: 2, Block: 2, From: "m", To: "d", Raw: "100"}}
	for _, model := range []string{"fifo", "proportional"} {
		r, err := Allocate(model, rows, map[string]string{"s": "50"}, "seed", 1, "50", 1)
		if err != nil {
			t.Fatal(err)
		}
		Format(&r, 0)
		e := r.Edges[0]
		if e.GapRaw != "50" || e.Gap != "50" || e.Hash != "out" || e.Block != 2 || e.Index != 2 || e.AttributedAmount != "50" || len(r.Anomalies) == 0 {
			t.Fatal(e, r.Anomalies)
		}
	}
}
