package query

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBlueStatisticsPrecisionAndGroupEvents(t *testing.T) {
	a, b, c, d := "a", "b", "c", "d"
	f := StatisticsFilter{Chain: "bsc", Addresses: []string{a, b}, Datasets: []string{"token_transfers"}, Timezone: "Asia/Shanghai"}
	if err := f.Normalize(); err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC)
	event := func(hash string, index uint32, from, to, raw, token string, decimals uint16) CanonicalEvent {
		return CanonicalEvent{Dataset: "token_transfers", TxHash: hash, Index: index, From: from, To: to, AmountRaw: raw, TokenAddress: token, TokenSymbol: "USDT", Decimals: decimals, Timestamp: instant, Status: 1}
	}
	events := []CanonicalEvent{event("h0", 0, c, a, "100000000000000000001", "x", 18), event("h0", 1, c, a, "20000000000000000000", "x", 18), event("h1", 0, a, d, "30000000000000000000", "x", 18), event("h2", 0, a, b, "7000000000000000000", "x", 18), event("h3", 0, a, a, "5000000000000000000", "x", 18), event("h4", 0, c, b, "1000000", "y", 6), event("h5", 0, c, a, "", "x", 18), event("h6", 0, a, d, "9000000000000000000", "x", 18)}
	events[7].Status = 0
	result, err := calculateStatistics(f, func(visit func(CanonicalEvent) error) error {
		for _, e := range events {
			if err := visit(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Transactions != 7 || result.Events != 8 || result.Counterparties != 2 || len(result.Assets) != 2 {
		t.Fatalf("counts: %+v", result)
	}
	var x FlowTotal
	for _, asset := range result.Assets {
		if asset.Total.TokenAddress == "x" {
			x = asset.Total
		}
	}
	if x.InRaw != "120000000000000000001" || x.OutRaw != "30000000000000000000" || x.NetRaw != "90000000000000000001" || x.InternalRaw != "7000000000000000000" || x.SelfRaw != "5000000000000000000" || x.Missing != 1 || x.Failed != 1 || x.TxCount != 6 {
		t.Fatalf("exact group flows: %+v", x)
	}
	if result.Trend[0].Bucket != "2026-10-01T00:00:00+08:00" {
		t.Fatalf("timezone bucket %s", result.Trend[0].Bucket)
	}
	for _, cp := range result.CounterpartyItems {
		if cp.Counterparty == c && cp.Total.TokenAddress == "x" && cp.Share != "42.857143" {
			t.Fatalf("share %s", cp.Share)
		}
	}
	for _, asset := range result.Addresses {
		if asset.Address == a && asset.Total.TokenAddress == "x" && asset.Total.OutRaw != "37000000000000000000" {
			t.Fatalf("per-address internal transfer omitted: %+v", asset)
		}
	}
}

func TestBlueStatisticsEachAssetKeepsItsOwnTop(t *testing.T) {
	f := StatisticsFilter{Chain: "bsc", Addresses: []string{"a"}, Timezone: "UTC"}
	_ = f.Normalize()
	result, err := calculateStatistics(f, func(visit func(CanonicalEvent) error) error {
		for i := 0; i < 102; i++ {
			if err := visit(CanonicalEvent{Dataset: "token_transfers", TokenAddress: "a-token", TxHash: fmt.Sprint(i), From: "c", To: "a", AmountRaw: fmt.Sprint(i), Decimals: 6, Status: 1, Timestamp: time.Now()}); err != nil {
				return err
			}
		}
		return visit(CanonicalEvent{Dataset: "token_transfers", TokenAddress: "z-token", TxHash: "z", From: "c", To: "a", AmountRaw: "1", Decimals: 18, Status: 1, Timestamp: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.TopTransfers) != 101 || result.TopTransfers[100].TokenAddress != "z-token" || result.TopTransfers[0].AmountRaw != "101" {
		t.Fatalf("per-asset top list: %d", len(result.TopTransfers))
	}
	refused, err := calculateStatistics(f, func(visit func(CanonicalEvent) error) error {
		_ = visit(result.TopTransfers[0])
		return ErrStatisticsBudget
	})
	if !errors.Is(err, ErrStatisticsBudget) || refused.Events != 0 || len(refused.Assets) != 0 {
		t.Fatal("partial statistics returned as success")
	}
	_, err = New(nil).Statistics(context.Background(), f)
	if err == nil {
		t.Fatal("missing index accepted")
	}
}

func TestBlueFilterExactAmountAndNativeHex(t *testing.T) {
	f := StatisticsFilter{Chain: "bsc", Addresses: []string{"a"}, MinAmount: "1.000000000000000001", MaxAmount: "1.000000000000000001"}
	if err := f.Normalize(); err != nil {
		t.Fatal(err)
	}
	e := CanonicalEvent{Dataset: "token_transfers", From: "c", To: "a", AmountRaw: "1000000000000000001", Decimals: 18, Status: 1}
	if !eventMatches(f, e) {
		t.Fatal("exact boundary excluded")
	}
	e.AmountRaw = "1000000000000000000"
	if eventMatches(f, e) {
		t.Fatal("1 wei outside range included")
	}
	e.Dataset = "transactions"
	e.AmountRaw = "0xde0b6b3a7640001"
	if !eventMatches(f, e) {
		t.Fatal("native hexadecimal amount lost precision")
	}
	f.Direction = "self"
	e.From = "a"
	e.To = "a"
	if !eventMatches(f, e) {
		t.Fatal("self transfer missing")
	}
	f.Direction = "internal"
	f.Addresses = []string{"a", "b"}
	e.To = "b"
	if !eventMatches(f, e) {
		t.Fatal("group internal transfer missing")
	}
}
