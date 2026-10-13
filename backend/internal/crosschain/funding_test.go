package crosschain

import (
	"encoding/hex"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"math/big"
	"strings"
	"testing"
)

func TestStrictSplitFundingRequiresFullConservationAndUniqueInput(t *testing.T) {
	x := fixtureFunds()
	p := x.Proof.Protocol
	a, _ := chain.ParseAddress(p.Source.Contract.Address)
	e, ok := DecodeAcross("ethereum", chainread.Log{Address: a, Topics: p.Source.RawTopics, Data: p.Source.RawData, BlockNumber: x.Proof.Legs[0].Block.Number})
	if !ok {
		t.Fatal("bad fixture")
	}
	first := x.Proof.Legs[0].Movements[0]
	first.To, first.AmountRaw, first.Amount = "0x"+strings.Repeat("7", 40), "101", "101"
	last := first
	last.From, last.To, last.Index, last.AmountRaw, last.Amount = first.To, e.Counterparty, 11, "100", "100"
	extra := last
	extra.To, extra.Index, extra.AmountRaw, extra.Amount = "0x"+strings.Repeat("8", 40), 12, "1", "1"
	rows := []Movement{first, last, extra}
	path, other := SplitTokenFunding(e, rows)
	if len(path) != 2 || len(other) != 1 {
		t.Fatal("balanced split rejected")
	}
	x.Proof.Legs[0].Movements, x.Proof.Legs[0].OtherDisbursements = path, other
	x.Proof.Legs[0].AmountRaw, x.Proof.Legs[0].Amount = "101", "101"
	ApplyFundsGate(&x)
	if !x.FundsVerified {
		t.Fatal(x.BlockingReasons)
	}
	for _, kind := range []string{"extra_input", "missing_output", "wrong_amount", "wrong_asset", "wrong_router", "wrong_order", "duplicate_output"} {
		t.Run(kind, func(t *testing.T) {
			bad := append([]Movement(nil), rows...)
			switch kind {
			case "extra_input":
				m := first
				m.Index = 9
				m.From = extra.To
				bad = append(bad, m)
			case "missing_output":
				bad = bad[:2]
			case "wrong_amount":
				bad[2].AmountRaw = "2"
			case "wrong_asset":
				bad[2].TokenContract = extra.To
			case "wrong_router":
				bad[2].From = extra.To
			case "wrong_order":
				bad[2].Index = first.Index - 1
			case "duplicate_output":
				bad = append(bad, extra)
			}
			if path, _ := SplitTokenFunding(e, bad); len(path) > 0 {
				t.Fatal("unsafe split accepted", kind)
			}
		})
	}
	x.Proof.Legs[0].OtherDisbursements[0].AmountRaw = "2"
	ApplyFundsGate(&x)
	if x.FundsVerified {
		t.Fatal("gate accepted altered split")
	}
}

func TestStrictNativeMintNeedsHistoricalGetterAndNativeCall(t *testing.T) {
	x := fixtureFunds()
	p := x.Proof.Protocol
	leg := &x.Proof.Legs[0]
	pool := p.Source.Contract.Address
	token := leg.Asset
	value := big.NewInt(100).FillBytes(make([]byte, 32))
	topics := []string{EventTopic("Transfer(address,address,uint256)"), "0x" + strings.Repeat("0", 64), "0x" + strings.Repeat("0", 24) + pool[2:]}
	owner, raw, ok := DecodeNativeConversion(topics, "0x"+hex.EncodeToString(value), true)
	if !ok || owner != pool || raw != "100" {
		t.Fatal("mint conversion missing")
	}
	leg.Asset, leg.Decimals, leg.Amount = "native", 18, "0.0000000000000001"
	leg.TraceSHA256 = strings.Repeat("a", 64)
	m := leg.Movements[0]
	m.Dataset, m.TokenContract, m.Decimals, m.Amount = "transactions", "native", 18, leg.Amount
	leg.Movements = []Movement{m}
	wrap := m
	wrap.Dataset, wrap.Index, wrap.From, wrap.To = "internal_transfers", 11, pool, token
	leg.NativeConversion = &NativeConversionProof{Token: token, Owner: pool, Pool: pool, RawTopics: topics, RawData: "0x" + hex.EncodeToString(value), HistoricalGetter: true, NativeFunding: []Movement{wrap}}
	ApplyFundsGate(&x)
	if !x.FundsVerified {
		t.Fatal(x.BlockingReasons)
	}
	for _, kind := range []string{"mint_only", "current_getter", "wrong_native_recipient", "wrong_wrapped_asset", "duplicate_wrap", "fake_mint_owner"} {
		t.Run(kind, func(t *testing.T) {
			bad := cloneFunds(x)
			c := bad.Proof.Legs[0].NativeConversion
			switch kind {
			case "mint_only":
				c.NativeFunding = nil
			case "current_getter":
				c.HistoricalGetter = false
			case "wrong_native_recipient":
				c.NativeFunding[0].To = m.From
			case "wrong_wrapped_asset":
				c.Token = m.From
			case "duplicate_wrap":
				c.NativeFunding = append(c.NativeFunding, c.NativeFunding[0])
			case "fake_mint_owner":
				c.RawTopics[2] = "0x" + strings.Repeat("0", 24) + m.From[2:]
			}
			ApplyFundsGate(&bad)
			if bad.FundsVerified {
				t.Fatal("unsafe native proof accepted", kind)
			}
		})
	}
}

func TestSourceNativeFundingBalancesEveryRouterAndPreservesGross(t *testing.T) {
	x := fixtureFunds()
	first := x.Proof.Legs[0].Movements[0]
	first.Dataset, first.TokenContract, first.AmountRaw, first.Amount = "transactions", "native", "101", "101"
	a, b, c, pool := first.From, "0x"+strings.Repeat("7", 40), "0x"+strings.Repeat("8", 40), first.To
	first.From, first.To, first.Index = a, b, 0
	second := first
	second.From, second.To, second.Index = b, c, 1
	last := first
	last.From, last.To, last.Index, last.AmountRaw, last.Amount = c, pool, 3, "100", "100"
	extra := last
	extra.To, extra.Index, extra.AmountRaw, extra.Amount = a, 2, "1", "1"
	e := DecodedBridgeEvent{Role: "source", Party: a, Counterparty: pool, AmountRaw: "100"}
	rows := []Movement{first, second, extra, last}
	path, other := SourceNativeFunding(e, rows)
	if len(path) != 3 || len(other) != 1 || path[0].AmountRaw != "101" {
		t.Fatal(path, other)
	}
	for _, kind := range []string{"wrong_sum", "extra_incoming", "missing_sibling", "reversed_call", "multiple_path"} {
		t.Run(kind, func(t *testing.T) {
			bad := append([]Movement(nil), rows...)
			switch kind {
			case "wrong_sum":
				bad[2].AmountRaw = "2"
			case "extra_incoming":
				m := second
				m.From = a
				m.Index = 2
				bad = append(bad, m)
			case "missing_sibling":
				bad = []Movement{first, second, last}
			case "reversed_call":
				bad[3].Index = 0
			case "multiple_path":
				m := last
				m.From = a
				m.Index = 4
				bad = append(bad, m)
			}
			if p, _ := SourceNativeFunding(e, bad); len(p) > 0 {
				t.Fatal("unsafe routing accepted", kind)
			}
		})
	}
}
