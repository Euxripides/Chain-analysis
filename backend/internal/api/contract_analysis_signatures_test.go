package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

func TestNameSelectorsUsesOpenchainThenFourByteAndRejectsMismatches(t *testing.T) {
	bind, record, legacy, unknown := method.SelectorOf("bindReferrer(address)"), method.SelectorOf("recordClaim(address,uint8,uint256)"), method.SelectorOf("withdraw()"), "0x01453f9e"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("filter") != "true" || !strings.Contains(r.URL.Query().Get("function"), bind) {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"function": map[string]any{
			bind:   []map[string]string{{"name": "bindReferrer(address)"}, {"name": "notTheSameSelector(uint256)"}},
			record: []map[string]string{{"name": "recordClaim(address,uint8,uint256)"}},
		}}})
	}))
	defer server.Close()
	oldURL, oldFourByte := contractOpenchainURL, contractFourByte
	contractOpenchainURL = server.URL
	contractFourByte = func(_ context.Context, sel string) []string {
		if sel == legacy {
			return []string{"withdraw()", "bogus(address)"}
		}
		return nil
	}
	defer func() { contractOpenchainURL, contractFourByte = oldURL, oldFourByte }()
	contractSignatureCache.Lock()
	contractSignatureCache.items = map[string]selectorCacheEntry{}
	contractSignatureCache.Unlock()

	result := &ca.Result{Sections: map[string][]json.RawMessage{}}
	for _, sel := range []string{bind, record, legacy, unknown} {
		result.Sections["methods"] = append(result.Sections["methods"], ca.Row(map[string]any{"method_id": "m" + sel, "selector": sel, "signature": "", "signature_candidates": []any{}}))
	}
	result.Sections["methods"] = append(result.Sections["methods"], ca.Row(map[string]any{"method_id": "verified", "selector": "0xa9059cbb", "signature": "transfer(address,uint256)"}))
	contractNameSelectors(context.Background(), result)
	got := map[string]map[string]any{}
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		got[m["method_id"].(string)] = m
	}
	check := func(id, name, source string) {
		t.Helper()
		m := got[id]
		list, _ := m["signature_candidates"].([]any)
		if name == "" {
			if len(list) != 0 || m["candidate_name"] != nil {
				t.Fatalf("%s should stay unnamed: %v", id, m)
			}
			return
		}
		if len(list) != 1 || m["candidate_name"] != name || list[0].(map[string]any)["source"] != source || list[0].(map[string]any)["verified"] != false {
			t.Fatalf("%s = %v", id, m)
		}
	}
	check("m"+bind, "bindReferrer", "public_signature_openchain")
	check("m"+record, "recordClaim", "public_signature_openchain")
	check("m"+legacy, "withdraw", "public_signature_4byte")
	check("m"+unknown, "", "")
	if got["verified"]["candidate_name"] != nil {
		t.Fatal("verified signature must not get candidates")
	}
	cov := rowMap(result.Sections["coverage"][0])
	if cov["dimension"] != "selector_names" || cov["named_by_public_signatures"] != json.Number("3") {
		t.Fatalf("coverage = %v", cov)
	}
	// A second analysis is served from the cache, including the remembered miss.
	contractNameSelectors(context.Background(), &ca.Result{Sections: map[string][]json.RawMessage{"methods": result.Sections["methods"][:4]}})
	if requests != 1 {
		t.Fatalf("openchain requests = %d", requests)
	}
}

func TestNameEventsFromPush32TopicsOnly(t *testing.T) {
	transfer, claimed := method.Topic0Of("Transfer(address,address,uint256)"), method.Topic0Of("RewardsClaimed(address,uint8,uint256,uint256,uint256)")
	unknown := "0x" + strings.Repeat("ab", 32)
	push32 := func(topic string) string { return "7f" + strings.TrimPrefix(topic, "0x") }
	// PUSH2 0x7f00 hides a 0x7f byte inside push data; it must not be read as PUSH32.
	code := "0x617f00" + push32(transfer) + "50" + push32(claimed) + push32(unknown) + push32(transfer)
	if got := contractCodeTopics(mustHexString(t, code), 10); len(got) != 3 || got[0] != transfer || got[1] != claimed || got[2] != unknown {
		t.Fatalf("topics = %v", got)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("event") == "" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"event": map[string]any{
			transfer: []map[string]string{{"name": "Transfer(address,address,uint256)"}},
			claimed:  []map[string]string{{"name": "RewardsClaimed(address,uint8,uint256,uint256,uint256)"}, {"name": "Wrong(uint256)"}},
			unknown:  nil,
		}}})
	}))
	defer server.Close()
	old := contractOpenchainURL
	contractOpenchainURL = server.URL
	defer func() { contractOpenchainURL = old }()
	contractSignatureCache.Lock()
	contractSignatureCache.items = map[string]selectorCacheEntry{}
	contractSignatureCache.Unlock()
	result := &ca.Result{Sections: map[string][]json.RawMessage{}}
	contractNameEvents(context.Background(), result, code)
	got := map[string]map[string]any{}
	for _, raw := range result.Sections["event_signatures"] {
		r := rowMap(raw)
		got[r["topic0"].(string)] = r
	}
	if len(got) != 2 || got[claimed]["signature"] != "RewardsClaimed(address,uint8,uint256,uint256,uint256)" || len(got[claimed]["candidates"].([]any)) != 1 || got[transfer] == nil {
		t.Fatalf("events = %v", got)
	}
}

func mustHexString(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
