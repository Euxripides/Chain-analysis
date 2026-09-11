package advanced

import (
	"testing"
	"time"
)

func TestAnalyzeRowsSeparatesFactsHeuristicsAndProfitBoundary(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{}
	for i := 0; i < 6; i++ {
		rows = append(rows, map[string]any{"from_address": "in" + string(rune('a'+i)), "to_address": "target", "token_symbol": "USDT", "amount": "100", "tx_hash": "in-tx" + string(rune('a'+i)), "timestamp": base.Add(time.Duration(i) * time.Minute), "source_ingested_at": base})
	}
	for i := 0; i < 6; i++ {
		rows = append(rows, map[string]any{"from_address": "target", "to_address": "out" + string(rune('a'+i)), "token_symbol": "USDT", "amount": "90", "tx_hash": "out-tx" + string(rune('a'+i)), "timestamp": base.Add(time.Hour + time.Duration(i)*time.Minute), "source_ingested_at": base})
	}
	res := analyzeRows("tron", "target", rows)
	want := map[string]bool{"FAN_IN": false, "FAN_OUT": false, "DISTRIBUTION": false, "RAPID_FORWARD": false}
	for _, s := range res.Signals {
		if _, ok := want[s.Type]; ok {
			want[s.Type] = true
		}
	}
	for kind, found := range want {
		if !found {
			t.Fatalf("expected signal %s, got %+v", kind, res.Signals)
		}
	}
	if len(res.AssetFlows) != 1 || res.AssetFlows[0].BasisStatus != "NET_FLOW_ONLY" || res.AssetFlows[0].NetFlow != "60" {
		t.Fatalf("unexpected asset flow: %+v", res.AssetFlows)
	}
	if len(res.Limitations) != 3 {
		t.Fatalf("limitations must preserve evidence boundaries: %+v", res.Limitations)
	}
}
