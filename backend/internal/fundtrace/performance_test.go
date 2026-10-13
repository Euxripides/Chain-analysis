package fundtrace

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/query"
)

// Alternating half-balance transfers used to create Fibonacci growth in lot
// fragments, even after tagged funds had passed the hop frontier.
func mixingFixture(rounds int) ([]query.LedgerTransfer, map[string]string, string) {
	s := new(big.Int).Lsh(big.NewInt(1), 200).String()
	rows := []query.LedgerTransfer{{Hash: "seed", Block: 1, From: "source", To: "a", Raw: s}}
	for i := 0; i < rounds; i++ {
		from, to := "a", "b"
		if i%2 == 1 {
			from, to = to, from
		}
		rows = append(rows, query.LedgerTransfer{Hash: fmt.Sprintf("mix-%03d", i), Block: uint64(i + 2), From: from, To: to, Raw: s})
	}
	return rows, map[string]string{"source": s, "a": s, "b": s}, s
}

func BenchmarkAllocateMixing(b *testing.B) {
	for _, rounds := range []int{20, 24} {
		for _, model := range []string{"proportional", "fifo"} {
			b.Run(fmt.Sprintf("%s/%d", model, rounds), func(b *testing.B) {
				rows, baselines, seed := mixingFixture(rounds)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					r, err := Allocate(model, rows, baselines, "seed", 0, seed, 5)
					if err != nil {
						b.Fatal(err)
					}
					if r.AccountedRaw != seed {
						b.Fatal("lost batch amount")
					}
				}
			})
		}
	}
}

func TestCompactUntaggedLotsPreserveFIFOOrder(t *testing.T) {
	rows := []query.LedgerTransfer{
		{Hash: "seed", Block: 1, From: "source", To: "middle", Raw: "10"},
		{Hash: "other", Block: 2, From: "other", To: "middle", Raw: "4"},
		{Hash: "out", Block: 3, From: "middle", To: "target", Raw: "6"},
	}
	r, err := Allocate("fifo", rows, map[string]string{"source": "10", "middle": "4", "other": "4"}, "seed", 0, "10", 5, "batch")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Edges) != 1 || r.Edges[0].AttributedRaw != "2" || r.Remaining["middle"] != "8" || r.Remaining["target"] != "2" {
		t.Fatalf("untagged funds crossed the tagged FIFO lot: %+v", r)
	}
	c := r.Edges[0].Consumptions
	if len(c) != 1 || c[0].LotID != "batch" || c[0].BeforeRaw != "10" || c[0].ConsumedRaw != "2" || c[0].AfterRaw != "8" {
		t.Fatalf("batch audit identity changed: %+v", c)
	}
}

func TestConsolidatedProportionalRoundingIsExplicitAndDeterministic(t *testing.T) {
	rows := []query.LedgerTransfer{
		{Hash: "other", Block: 1, From: "other", To: "middle", Raw: "1"},
		{Hash: "seed", Block: 2, From: "source", To: "middle", Raw: "2"},
		{Hash: "out", Block: 3, From: "middle", To: "target", Raw: "1"},
	}
	baselines := map[string]string{"other": "1", "middle": "1", "source": "2"}
	r, err := Allocate("proportional", rows, baselines, "seed", 0, "2", 5)
	if err != nil {
		t.Fatal(err)
	}
	// The consolidated older lot and source lot each hold 2/4 of the balance.
	// The single indivisible unit goes to the first lot on an equal remainder.
	// Without consolidation two older fragments of one unit would each have a
	// smaller remainder and send that unit from the source lot instead.
	if len(r.Edges) != 1 || r.Edges[0].AttributedRaw != "0" || r.Edges[0].SourceBeforeRaw != "2" || r.Edges[0].TotalBeforeRaw != "4" || r.AccountedRaw != "2" {
		t.Fatalf("unexpected consolidated rounding: %+v", r)
	}
	if !strings.Contains(strings.Join(r.Assumptions, " "), "consolidated lots") {
		t.Fatal("rounding granularity was not disclosed")
	}
	other, err := Allocate("proportional", rows, baselines, "seed", 0, "2", 5)
	if err != nil || !reflect.DeepEqual(r, other) {
		t.Fatal("rounding is nondeterministic", err)
	}
}

func TestZeroTransfersCannotFragmentOrEmitBatchEdges(t *testing.T) {
	rows := []query.LedgerTransfer{{Hash: "seed", Block: 1, From: "source", To: "middle", Raw: "10"}}
	for i := 0; i < 1000; i++ {
		rows = append(rows, query.LedgerTransfer{Hash: fmt.Sprintf("poison-%d", i), Block: uint64(i + 2), From: "middle", To: fmt.Sprintf("poison-%d", i), Raw: "0"})
	}
	for _, model := range []string{"proportional", "fifo"} {
		r, err := Allocate(model, rows, map[string]string{"source": "10"}, "seed", 0, "10", 5)
		if err != nil || len(r.Edges) != 0 || r.Remaining["middle"] != "10" || r.AccountedRaw != "10" {
			t.Fatal(model, err, r)
		}
	}
}

func TestCompactedMixingKeepsBatchEvidenceAndConservation(t *testing.T) {
	rows := []query.LedgerTransfer{{Hash: "seed", Block: 1, From: "source", To: "a", Raw: "50000"}}
	random := rand.New(rand.NewSource(42))
	addresses := []string{"a", "b", "c"}
	for i := 0; i < 80; i++ {
		from := random.Intn(len(addresses))
		to := (from + 1 + random.Intn(len(addresses)-1)) % len(addresses)
		rows = append(rows, query.LedgerTransfer{Hash: fmt.Sprintf("mixed-%03d", i), Block: uint64(i + 2), From: addresses[from], To: addresses[to], Raw: fmt.Sprint(1 + random.Intn(10000))})
	}
	for _, model := range []string{"proportional", "fifo"} {
		r, err := Allocate(model, rows, map[string]string{"source": "50000", "a": "100000", "b": "200000", "c": "30000"}, "seed", 0, "50000", 5, "audit-batch")
		if err != nil {
			t.Fatal(model, err)
		}
		if r.AccountedRaw != "50000" {
			t.Fatal(model, "batch inflation or loss", r.AccountedRaw)
		}
		for _, edge := range r.Edges {
			attributed, _ := integer(edge.AttributedRaw)
			actual, _ := integer(edge.ActualRaw)
			before, _ := integer(edge.SourceBeforeRaw)
			if attributed.Cmp(actual) > 0 || attributed.Cmp(before) > 0 || attributed.Cmp(big.NewInt(50000)) > 0 {
				t.Fatal("edge exceeds its evidence or source batch", edge)
			}
			consumed := big.NewInt(0)
			for _, audit := range edge.Consumptions {
				x, _ := integer(audit.BeforeRaw)
				y, _ := integer(audit.ConsumedRaw)
				z, _ := integer(audit.AfterRaw)
				if new(big.Int).Add(y, z).Cmp(x) != 0 || audit.SourceBatchID != "audit-batch" || audit.LotID == "" {
					t.Fatal("invalid fragment audit", audit)
				}
				consumed.Add(consumed, y)
			}
			if consumed.Cmp(attributed) != 0 {
				t.Fatal("edge and lot consumptions disagree", edge)
			}
		}
	}
}

func TestAllocationContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, baselines, seed := mixingFixture(24)
	r, err := AllocateContext(ctx, "proportional", rows, baselines, "seed", 0, seed, 5)
	if !errors.Is(err, context.Canceled) || r.AccountedRaw != "" {
		t.Fatal(err, r)
	}
}

type cancelAfterChecksContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
	after  int
}

func (c *cancelAfterChecksContext) Err() error {
	c.checks++
	if c.checks == c.after {
		c.cancel()
	}
	return c.Context.Err()
}

func TestAllocationCancellationDuringLotWork(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterChecksContext{Context: base, cancel: cancel, after: 200}
	rows, baselines, seed := mixingFixture(24)
	r, err := AllocateContext(ctx, "proportional", rows, baselines, "seed", 0, seed, 5)
	if !errors.Is(err, context.Canceled) || ctx.checks != ctx.after || r.AccountedRaw != "" {
		t.Fatal("lot loop did not stop at cancellation", err, ctx.checks, r.AccountedRaw)
	}
}

func TestAllocationBudgetStopsMixingWithExplicitError(t *testing.T) {
	rows, baselines, seed := mixingFixture(100)
	r, err := AllocateContext(context.Background(), "proportional", rows, baselines, "seed", 0, seed, 5)
	var budgetError *AllocationBudgetError
	if !errors.Is(err, ErrAllocationBudget) || !errors.As(err, &budgetError) || budgetError.Used <= budgetError.Limit || r.AccountedRaw != "" {
		t.Fatal("unbounded or falsely complete allocation", err, r.AccountedRaw)
	}
}

func TestAllocationRejectsExcessiveInputBeforeSort(t *testing.T) {
	rows := make([]query.LedgerTransfer, MaxAllocationTransfers+1)
	_, err := Allocate("fifo", rows, nil, "seed", 0, "1", 5)
	var budgetError *AllocationBudgetError
	if !errors.As(err, &budgetError) || budgetError.Dimension != "transfers" {
		t.Fatal(err)
	}
}

func TestLedgerAmountsAreBoundedUint256(t *testing.T) {
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()
	if _, err := ledgerInteger(max); err != nil {
		t.Fatal("lost full uint256 precision", err)
	}
	tooLarge := new(big.Int).Lsh(big.NewInt(1), 256).String()
	for _, raw := range []string{tooLarge, strings.Repeat("9", 10000)} {
		if _, err := ledgerInteger(raw); err == nil {
			t.Fatal("accepted unbounded raw amount")
		}
		rows := []query.LedgerTransfer{{Hash: "seed", Block: 1, From: "source", To: "a", Raw: raw}}
		if _, err := Allocate("fifo", rows, map[string]string{"source": "1"}, "seed", 0, "1", 5); err == nil {
			t.Fatal("unbounded transfer reached allocation")
		}
		if _, err := Allocate("fifo", nil, map[string]string{"source": raw}, "seed", 0, "1", 5); err == nil {
			t.Fatal("unbounded baseline reached allocation")
		}
	}
}
