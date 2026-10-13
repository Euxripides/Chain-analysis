package fundtrace

import (
	"context"
	"errors"
	"math/big"
	"math/rand"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/query"
)

func jointTx(hash string, block uint64, from, to, raw string) query.LedgerTransfer {
	return query.LedgerTransfer{Hash: hash, Block: block, From: from, To: to, Raw: raw}
}
func TestJointSharedLedgerActivatesSourcesChronologically(t *testing.T) {
	txs := []query.LedgerTransfer{jointTx("A", 1, "S", "M", "10"), jointTx("beforeB", 2, "M", "X", "50"), jointTx("B", 3, "S", "M", "20"), jointTx("afterB", 4, "M", "Y", "80")}
	seeds := []JointSeed{{"A", "A", 0, "10"}, {"B", "B", 0, "20"}}
	for _, model := range []string{"fifo", "proportional"} {
		r, e := AllocateJoint(context.Background(), model, txs, map[string]string{"S": "30", "M": "100"}, seeds, 3)
		if e != nil {
			t.Fatal(e)
		}
		if len(r.Anomalies) != 0 {
			t.Fatal(r.Anomalies)
		}
		for id, source := range r.Sources {
			if source.SeedRaw != source.AccountedRaw {
				t.Fatalf("%s: %#v", id, source)
			}
		}
		for _, edge := range r.Edges {
			actual, _ := nextInt(edge.Transfer.Raw)
			attributed, _ := nextInt(edge.AttributedRaw)
			if attributed.Cmp(actual) > 0 {
				t.Fatal("overattributed edge")
			}
			if edge.Transfer.Hash == "beforeB" {
				if _, ok := edge.BySource["B"]; ok {
					t.Fatal("future source activated early")
				}
			}
		}
		if model == "fifo" && r.Sources["A"].Remaining["Y"] != "10" {
			t.Fatal(r.Sources)
		}
	}
}
func nextInt(s string) (*big.Int, bool) { return new(big.Int).SetString(s, 10) }
func TestJointRejectsSourceOverlapAndDuplicateEvents(t *testing.T) {
	rows := []query.LedgerTransfer{jointTx("A", 1, "S", "M", "10"), jointTx("B", 2, "M", "X", "10")}
	seeds := []JointSeed{{"A", "A", 0, "10"}, {"B", "B", 0, "10"}}
	for _, model := range []string{"fifo", "proportional"} {
		_, e := AllocateJoint(context.Background(), model, rows, map[string]string{"S": "10"}, seeds, 3)
		var overlap *SourceOverlapError
		if !errors.As(e, &overlap) {
			t.Fatalf("expected overlap, got %v", e)
		}
	}
	_, e := AllocateJoint(context.Background(), "fifo", append(rows, rows[0]), map[string]string{"S": "10"}, seeds[:1], 3)
	if e == nil {
		t.Fatal("duplicate event accepted")
	}
}
func TestJointBurnAndHopFrontierAreConserved(t *testing.T) {
	zero := "0x" + strings.Repeat("0", 40)
	rows := []query.LedgerTransfer{jointTx("A", 1, "S", "M", "10"), jointTx("burn", 2, "M", zero, "3"), jointTx("move", 3, "M", "X", "7"), jointTx("frontier", 4, "X", "Y", "7")}
	r, e := AllocateJoint(context.Background(), "fifo", rows, map[string]string{"S": "10"}, []JointSeed{{"A", "A", 0, "10"}}, 1)
	if e != nil {
		t.Fatal(e)
	}
	s := r.Sources["A"]
	if s.BurnedRaw != "3" || s.FrontierRaw != "7" || s.AccountedRaw != "10" || len(s.Remaining) != 0 {
		t.Fatal(s)
	}
}
func TestJointProportionalPrecisionDeterminismAndConservation(t *testing.T) {
	random := rand.New(rand.NewSource(17))
	for trial := 0; trial < 50; trial++ {
		base := new(big.Int).Lsh(big.NewInt(1), 200)
		rows := []query.LedgerTransfer{}
		seeds := []JointSeed{}
		total := new(big.Int)
		for i := 0; i < 8; i++ {
			v := new(big.Int).Add(base, big.NewInt(int64(random.Intn(1000)+1)))
			hash := string(rune('A' + i))
			rows = append(rows, jointTx(hash, uint64(i+1), "S", "M", v.String()))
			seeds = append(seeds, JointSeed{hash, hash, 0, v.String()})
			total.Add(total, v)
		}
		rows = append(rows, jointTx("out", 9, "M", "X", new(big.Int).Sub(total, big.NewInt(3)).String()))
		baseline := map[string]string{"S": total.String()}
		one, e := AllocateJoint(context.Background(), "proportional", rows, baseline, seeds, 3)
		if e != nil {
			t.Fatal(e)
		}
		two, e := AllocateJoint(context.Background(), "proportional", rows, baseline, seeds, 3)
		if e != nil {
			t.Fatal(e)
		}
		for id, s := range one.Sources {
			if s.AccountedRaw != s.SeedRaw || two.Sources[id].Remaining["X"] != s.Remaining["X"] {
				t.Fatal("precision/determinism failure")
			}
		}
	}
}
func TestJointCancellationAndMissingSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := AllocateJoint(ctx, "fifo", nil, nil, []JointSeed{{"a", "A", 0, "1"}}, 3)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	_, e = AllocateJoint(context.Background(), "fifo", nil, nil, []JointSeed{{"a", "A", 0, "1"}}, 3)
	if e == nil {
		t.Fatal("missing source accepted")
	}
}
func TestJointExternalInflowsDoNotRequireUnrelatedBaselines(t *testing.T) {
	rows := []query.LedgerTransfer{jointTx("A", 1, "S", "M", "10"), jointTx("external", 2, "U", "M", "1000000"), jointTx("out", 3, "M", "X", "1000010")}
	seeds := []JointSeed{{"A", "A", 0, "10"}}
	for _, model := range []string{"fifo", "proportional"} {
		r, e := AllocateJoint(context.Background(), model, rows, map[string]string{"M": "0"}, seeds, 3, map[string]bool{"S": true, "U": true})
		if e != nil {
			t.Fatal(e)
		}
		if len(r.Anomalies) != 0 || r.Sources["A"].Remaining["X"] != "10" || r.Edges[len(r.Edges)-1].AttributedRaw != "10" {
			t.Fatal(r)
		}
	}
}
