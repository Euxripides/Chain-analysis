package crosschain

import (
	"context"
	"strings"
	"testing"
	"time"
)

const testHash = "0x1111111111111111111111111111111111111111111111111111111111111111"
const testAddress = "0x1111111111111111111111111111111111111111"
const testOther = "0x2222222222222222222222222222222222222222"
const testToken = "0x3333333333333333333333333333333333333333"

func matchFixture() (CandidateRequest, Movement, Movement) {
	now := time.Date(2026, 5, 18, 0, 27, 33, 0, time.UTC)
	r := CandidateRequest{Anchor: Anchor{"bsc", testHash, "token_transfers", nil}, SearchChain: "ethereum", SearchAddresses: []string{testAddress}, SearchDirection: "in", Direction: "backward", AssetKind: "token", TokenContract: testToken, AssetRelation: "same_asset", StartTime: now.Add(-time.Hour), EndTime: now, MaxParts: 1, AbsoluteTolerance: "0"}
	a := Movement{Chain: "bsc", TxHash: testHash, Dataset: "token_transfers", Timestamp: now, Amount: "1000", Status: "success"}
	m := Movement{Chain: "ethereum", TxHash: testHash, Dataset: "token_transfers", Index: 1, Timestamp: now.Add(-30 * time.Second), From: testOther, To: testAddress, TokenContract: testToken, Token: "USDT", Decimals: 6, AmountRaw: "1000000000", Amount: "1000", Status: "success"}
	return r, a, m
}

func TestExactDecimalAndExplicitFeeTolerance(t *testing.T) {
	r, a, m := matchFixture()
	a.Amount = "0.007631125135013813"
	m.Amount = "0.007631125135013812"
	out, e := Match(context.Background(), r, a, []Movement{m}, false)
	if e != nil || len(out.Candidates) != 0 {
		t.Fatalf("one wei difference must not match exact search: %+v %v", out, e)
	}
	r.AbsoluteTolerance = "0.000000000000000001"
	out, e = Match(context.Background(), r, a, []Movement{m}, false)
	if e != nil || len(out.Candidates) != 1 || out.Candidates[0].AmountDifference != "0.000000000000000001" || out.Candidates[0].Association != "candidate" {
		t.Fatalf("precision/tolerance lost: %+v %v", out, e)
	}
}

func TestSplitDedupFailuresAndPartyIsolation(t *testing.T) {
	r, a, m := matchFixture()
	r.MaxParts = 3
	m.Amount = "400"
	second := m
	second.Index = 2
	second.Amount = "600"
	failed := m
	failed.Index = 3
	failed.Amount = "1000"
	failed.Status = "failed"
	other := second
	other.Index = 4
	other.From = testToken
	out, e := Match(context.Background(), r, a, []Movement{m, m, second, failed, other}, false)
	if e != nil || len(out.Candidates) != 1 || len(out.Candidates[0].Movements) != 2 || out.Candidates[0].TotalAmount != "1000" {
		t.Fatalf("split isolation failed: %+v %v", out, e)
	}
	second.Timestamp = m.Timestamp.Add(6 * time.Minute)
	out, e = Match(context.Background(), r, a, []Movement{m, second}, false)
	if e != nil || len(out.Candidates) != 0 {
		t.Fatalf("unrelated times grouped: %+v %v", out, e)
	}
}

func TestCandidateBoundsAssetIsolationAndCancellation(t *testing.T) {
	r, a, m := matchFixture()
	r.ScanLimit = 2
	r.MaxResults = 1
	r.MaxCombinations = 1
	another := m
	another.Index = 2
	badMint := m
	badMint.Index = 3
	badMint.TokenContract = testOther
	out, e := Match(context.Background(), r, a, []Movement{m, another, badMint}, true)
	if e != nil || len(out.Candidates) != 1 || !out.CombinationsTruncated || !out.ScanTruncated || out.AssetIdentityVerified {
		t.Fatalf("bounds lost: %+v %v", out, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Match(ctx, r, a, []Movement{m}, false); e == nil {
		t.Fatal("canceled query must stop")
	}
}

func TestSwapRequiresExplicitAmountAndPreservesBasis(t *testing.T) {
	r, a, m := matchFixture()
	r.AssetRelation = "swap"
	if _, e := Match(context.Background(), r, a, []Movement{m}, false); e == nil {
		t.Fatal("swap without expected amount accepted")
	}
	r.ExpectedAmount = "1000"
	out, e := Match(context.Background(), r, a, []Movement{m}, false)
	if e != nil || out.AmountBasis != "explicit_expected_amount" || out.AssetIdentityVerified {
		t.Fatalf("swap basis lost: %+v %v", out, e)
	}
}

func TestAnchorAmbiguityRequiresEvent(t *testing.T) {
	r, a, _ := matchFixture()
	a.Index = 1
	b := a
	b.Index = 2
	if _, e := SelectAnchor(r.Anchor, []Movement{a, b}); e == nil {
		t.Fatal("ambiguous anchor accepted")
	}
	r.Anchor.EventIndex = &a.Index
	selected, e := SelectAnchor(r.Anchor, []Movement{a, b})
	if e != nil || selected.Index != 1 {
		t.Fatalf("wrong event %+v %v", selected, e)
	}
	a.Timestamp = time.Unix(0, 0)
	if _, e = SelectAnchor(r.Anchor, []Movement{a}); e == nil {
		t.Fatal("missing block time accepted as 1970")
	}
}

func TestInvalidRequestsFailBeforeIO(t *testing.T) {
	r, _, _ := matchFixture()
	for _, change := range []func(*CandidateRequest){func(r *CandidateRequest) { r.SearchAddresses = nil }, func(r *CandidateRequest) { r.StartTime = time.Time{} }, func(r *CandidateRequest) { r.EndTime = r.StartTime.Add(25 * time.Hour) }, func(r *CandidateRequest) { r.TokenContract = "USDT" }, func(r *CandidateRequest) { r.RelativeToleranceBPS = 501 }, func(r *CandidateRequest) { r.MaxCombinations = 20001 }, func(r *CandidateRequest) { r.AbsoluteTolerance = "1e999999" }, func(r *CandidateRequest) { r.Anchor.TxHash = "0xabc" }} {
		copy := r
		change(&copy)
		if copy.Validate() == nil {
			t.Fatalf("invalid request accepted: %+v", copy)
		}
	}
	for _, value := range []string{"-1", "1e18", strings.Repeat("9", 101), "NaN", "1.2.3"} {
		if _, e := Amount(value); e == nil {
			t.Fatalf("unsafe amount %q", value)
		}
	}
}

func TestSolanaHashCaseAndThirtyEightDecimals(t *testing.T) {
	h := "3QvRpJyWnMomnxPTApBeMHdZvb5BAeWd9Bu8A4rDBQnJ8hMvbX49GUrwFJuBVNQmhH7u4EopmS3MJQSzf34ouma5"
	normalized, e := Hash("solana", h)
	if e != nil || normalized != h {
		t.Fatalf("Solana signature changed %s %v", normalized, e)
	}
	if got := scaled("1", 38); got != "0.00000000000000000000000000000000000001" {
		t.Fatal(got)
	}
}

func TestRelativeToleranceDoesNotRoundSmallAmounts(t *testing.T) {
	r, a, m := matchFixture()
	a.Amount = "0.000000000000000001"
	m.Amount = "0.00000000000000000104"
	r.RelativeToleranceBPS = 500
	out, e := Match(context.Background(), r, a, []Movement{m}, false)
	if e != nil || len(out.Candidates) != 1 || out.Candidates[0].AmountDifference != "0.00000000000000000004" {
		t.Fatal(out, e)
	}
	m.Amount = "0.00000000000000000106"
	out, e = Match(context.Background(), r, a, []Movement{m}, false)
	if e != nil || len(out.Candidates) != 0 {
		t.Fatal("out-of-tolerance amount accepted", out, e)
	}
}
