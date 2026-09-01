package explorer

import (
	"encoding/json"
	"testing"
	"time"
)

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
