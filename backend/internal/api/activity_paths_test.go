package api

import "testing"

func TestActivityPathsPreserveEveryHop(t *testing.T) {
	paths := activityPaths("bsc", "a", []map[string]any{{"from": "a", "to": "b", "amount": "50000"}, {"from": "b", "to": "c", "amount": "679771.158390787859739922"}, {"from": "b", "to": "d", "amount": "1"}, {"from": "c", "to": "a", "amount": "2"}})
	if len(paths) != 4 {
		t.Fatal(paths)
	}
	for _, p := range paths {
		addresses := p["addresses"].([]string)
		hops := p["hops"].([]any)
		if len(hops) != len(addresses)-1 || addresses[0] != "a" {
			t.Fatal(p)
		}
		for i, h := range hops {
			v := h.(map[string]any)
			if v["from"] != addresses[i] || v["to"] != addresses[i+1] || v["depth"] != i+1 {
				t.Fatal(p)
			}
		}
	}
	second := paths[1]["hops"].([]any)
	if len(second) != 2 || second[0].(map[string]any)["amount"] != "50000" || second[1].(map[string]any)["amount"] != "679771.158390787859739922" {
		t.Fatal(paths[1])
	}
}
