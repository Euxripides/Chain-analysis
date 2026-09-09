package dataset

import (
	"context"
	"reflect"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func TestComputeGaps(t *testing.T) {
	iv := func(f, t int64) store.BlockInterval { return store.BlockInterval{From: f, To: t} }
	cases := []struct {
		name    string
		covered []store.BlockInterval
		want    store.BlockInterval
		gaps    []store.BlockInterval
	}{
		{"no cover", nil, iv(0, 100), []store.BlockInterval{iv(0, 100)}},
		{"full hit", []store.BlockInterval{iv(0, 100)}, iv(0, 100), nil},
		{"middle gap", []store.BlockInterval{iv(0, 40), iv(60, 100)}, iv(0, 100), []store.BlockInterval{iv(41, 59)}},
		{"tail gap", []store.BlockInterval{iv(0, 50)}, iv(0, 100), []store.BlockInterval{iv(51, 100)}},
		{"head gap", []store.BlockInterval{iv(50, 100)}, iv(0, 100), []store.BlockInterval{iv(0, 49)}},
	}
	for _, c := range cases {
		got := ComputeGaps(c.covered, c.want)
		if !reflect.DeepEqual(got, c.gaps) {
			t.Errorf("%s: got %v want %v", c.name, got, c.gaps)
		}
	}
}

func TestCoveredBlocks(t *testing.T) {
	iv := func(f, t int64) store.BlockInterval { return store.BlockInterval{From: f, To: t} }
	covered := []store.BlockInterval{iv(0, 40), iv(60, 100), iv(200, 300)}
	if got := CoveredBlocks(covered, iv(0, 100)); got != 82 {
		t.Errorf("covered blocks = %d, want 82", got)
	}
}

func TestNormalizeAndDedupeAddresses(t *testing.T) {
	a1 := "0x1111111111111111111111111111111111111111"
	a2 := "0x2222222222222222222222222222222222222222"
	in := []string{" " + a1 + " ", a1, "bad", a2}
	out, err := NormalizeAndDedupeAddresses(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d addresses, want 2", len(out))
	}
	if out[0] != a1 {
		t.Errorf("expected %s, got %s", a1, out[0])
	}
}

func TestNormalizeAndDedupeTronAddressesPreservesBase58(t *testing.T) {
	const address = "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	out, err := NormalizeAndDedupeAddressesForChain("tron", []string{address, "41EA51342D9B1EA6EAE9E7CBE6B7AB93B2E0D27AF8"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 || out[0] != address {
		t.Fatalf("TRON address was changed: %#v", out)
	}
}

func TestSanitizeDatasetsRejectsUnknownAndDeduplicates(t *testing.T) {
	got, err := sanitizeDatasets([]string{" logs ", "LOGS", "transactions"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"logs", "transactions"}) {
		t.Fatalf("datasets = %v", got)
	}
	if _, err := sanitizeDatasets([]string{"log"}); err == nil {
		t.Fatal("unknown dataset was accepted")
	}
}

func TestResolveRangeRejectsNegativeBlocks(t *testing.T) {
	from, to := int64(-1), int64(1)
	if _, err := (&Service{}).ResolveRange(context.Background(), "bsc", RangeSpec{Type: "block", FromBlock: &from, ToBlock: &to}); err == nil {
		t.Fatal("negative block range was accepted")
	}
}
