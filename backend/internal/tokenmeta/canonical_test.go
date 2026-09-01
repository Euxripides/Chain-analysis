package tokenmeta

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func intPtr(v int) *int { return &v }

func TestExtractPageDeduplicatesContractsAndValidatesDecimals(t *testing.T) {
	rows := make([]json.RawMessage, 10000)
	for i := range rows {
		rows[i] = json.RawMessage(`{"contractAddress":"0x55d398326f99059ff775485246999027b3197955","tokenName":"Tether USD","tokenSymbol":"USDT","tokenDecimals":18,"blockHeight":1}`)
	}
	got := ExtractPage("bsc", "ankr_token_transfer", rows)
	if len(got) != 1 {
		t.Fatalf("observations=%d want 1", len(got))
	}
	if !got[0].Valid || got[0].Name != "Tether USD" || got[0].Decimals == nil || *got[0].Decimals != 18 {
		t.Fatalf("unexpected observation: %+v", got[0])
	}
	for _, v := range []int{0, 6, 18, 255} {
		o := Observation{Chain: "bsc", ContractAddress: "0x55d398326f99059ff775485246999027b3197955", Source: "rpc", Decimals: intPtr(v)}
		Validate(&o)
		if !o.Valid {
			t.Fatalf("%d rejected: %s", v, o.ValidationError)
		}
	}
	for _, v := range []int{-1, 256} {
		o := Observation{Chain: "bsc", ContractAddress: "0x55d398326f99059ff775485246999027b3197955", Source: "rpc", Decimals: intPtr(v)}
		Validate(&o)
		if o.Valid {
			t.Fatalf("%d accepted", v)
		}
	}
}

func TestExtractPageRejectsTruncatedContractBeforeCatalog(t *testing.T) {
	rows := []json.RawMessage{
		json.RawMessage(`{"contractAddress":"0x609bfd9972ea3316","tokenSymbol":"BAD","tokenDecimals":18}`),
		json.RawMessage(`{"contractAddress":"0x55d398326f99059ff775485246999027b3197955","tokenSymbol":"USDT","tokenDecimals":18}`),
	}
	got := ExtractPage("bsc", "ankr_token_transfer", rows)
	if len(got) != 1 || got[0].ContractAddress != "0x55d398326f99059ff775485246999027b3197955" {
		t.Fatalf("invalid contract reached catalog: %+v", got)
	}
	counts := pageContractCounts(rows)
	if len(counts) != 1 || counts[got[0].ContractAddress] != 1 {
		t.Fatalf("invalid contract reached counts: %+v", counts)
	}
}

func TestCanonicalDeterministicConflictAndPartial(t *testing.T) {
	base := []Observation{
		{Chain: "bsc", ContractAddress: "0x55d398326f99059ff775485246999027b3197955", Symbol: "USDT", Decimals: intPtr(18), Source: "ankr_token_transfer", RawHash: "b", Valid: true},
		{Chain: "bsc", ContractAddress: "0x55d398326f99059ff775485246999027b3197955", Name: "Tether USD", Symbol: "USDT", Decimals: intPtr(6), Source: "rpc", RawHash: "a", Valid: true},
	}
	want := Resolve(base)
	if want.MetadataStatus != StatusConflict || want.Decimals == nil || *want.Decimals != 6 {
		t.Fatalf("unexpected conflict: %+v", want)
	}
	for i := 0; i < 100; i++ {
		shuffled := append([]Observation(nil), base...)
		rand.New(rand.NewSource(int64(i))).Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := Resolve(shuffled); !reflect.DeepEqual(got, want) {
			t.Fatalf("non deterministic iteration %d: got=%+v want=%+v", i, got, want)
		}
	}
	partial := Resolve([]Observation{{Chain: "bsc", ContractAddress: base[0].ContractAddress, Symbol: "USDT", Decimals: intPtr(18), Source: "rpc", ObservedAt: time.Now(), Valid: true}})
	if partial.MetadataStatus != StatusPartial || partial.Name != "" {
		t.Fatalf("unexpected partial: %+v", partial)
	}
}

func TestVerifiedRegistryIsTheOnlyLocalVerificationAuthority(t *testing.T) {
	registry := Observation{Chain: "bsc", ContractAddress: "0x55d398326f99059ff775485246999027b3197955", Name: "Tether USD", Symbol: "USDT", Decimals: intPtr(18), Source: "verified_registry", Valid: true}
	got := Resolve([]Observation{registry})
	if !got.Verified || got.MetadataStatus != StatusComplete || got.MetadataSource != "verified_registry" {
		t.Fatalf("registry token not verified: %+v", got)
	}
	manual := registry
	manual.Source = "admin_override"
	if got := Resolve([]Observation{manual}); got.Verified {
		t.Fatalf("manual metadata override must not certify token identity: %+v", got)
	}
}
