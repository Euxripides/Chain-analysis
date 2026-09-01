package explorer

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type fakeAddressTypeResolver struct {
	calls int
}

func (f *fakeAddressTypeResolver) ResolveAddressTypes(_ context.Context, _ string, addresses []string) (map[string]bool, error) {
	f.calls++
	out := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		out[address] = address == "0x55d398326f99059ff775485246999027b3197955"
	}
	return out, nil
}

func TestCursorRoundTripFromJSONRow(t *testing.T) {
	ts := "2026-09-01T02:03:04.123456789Z"
	row := map[string]any{
		"timestamp": ts, "tx_hash": "0xabc", "log_index": json.Number("42"),
		"token_address": "0xtoken",
	}
	cur, ok := cursorFromRow(row)
	if !ok {
		t.Fatal("cursorFromRow rejected RFC3339 timestamp")
	}
	cur.Dataset = "token_transfers"
	encoded := encodeCursor(cur)
	decoded, err := decodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := time.Parse(time.RFC3339Nano, ts)
	if !decoded.Ts.Equal(want) || decoded.Hash != "0xabc" || decoded.LogIndex != 42 || decoded.TokenAddress != "0xtoken" {
		t.Fatalf("decoded cursor = %#v", decoded)
	}
}

func TestResolveAddressTypesCachesContractFlags(t *testing.T) {
	resolver := &fakeAddressTypeResolver{}
	service := New(nil, nil).WithAddressTypeResolver(resolver)
	rows := []map[string]any{{
		"from_address": "0x9b77e3b4f85c7cea7da451f454895edbd10b80dd",
		"to_address":   "0x55d398326f99059ff775485246999027b3197955",
	}}
	first := service.resolveAddressTypes(context.Background(), "bsc", rows)
	second := service.resolveAddressTypes(context.Background(), "bsc", rows)
	if !first["0x55d398326f99059ff775485246999027b3197955"] || first["0x9b77e3b4f85c7cea7da451f454895edbd10b80dd"] {
		t.Fatalf("unexpected first result: %#v", first)
	}
	if !second["0x55d398326f99059ff775485246999027b3197955"] || resolver.calls != 1 {
		t.Fatalf("cache miss: result=%#v calls=%d", second, resolver.calls)
	}
}
