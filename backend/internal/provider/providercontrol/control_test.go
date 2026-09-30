package providercontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateEndpointsRejectsEmbeddedCredentialsAndOtherHosts(t *testing.T) {
	tests := []struct {
		provider, channel, endpoint string
		valid                       bool
	}{
		{"ankr", "advanced", "https://rpc.ankr.com/multichain", true},
		{"ankr", "rpc", "https://rpc.ankr.com/{chain}/{key}", true},
		{"ankr", "rpc", "https://rpc.ankr.com/bsc/literal-secret", false},
		{"ankr", "rpc", "https://rpc.ankr.com.evil.test/{chain}/{key}", false},
		{"ankr", "tron_rest", "https://rpc.ankr.com/premium-http/tron/{key}", true},
		{"drpc", "solana_rpc", "https://lb.drpc.live/solana/{key}", true},
		{"drpc", "solana_rpc", "https://lb.drpc.live:8080/solana/{key}", false},
		{"drpc", "solana_rpc", "https://lb.drpc.live/solana/a-real-key", false},
	}
	for _, tt := range tests {
		err := ValidateEndpoints(tt.provider, map[string]string{tt.channel: tt.endpoint})
		if (err == nil) != tt.valid {
			t.Errorf("ValidateEndpoints(%q, %q) valid=%v, want %v", tt.channel, tt.endpoint, err == nil, tt.valid)
		}
	}
	public := PublicEndpoints("drpc", map[string]string{"solana_rpc": "https://lb.drpc.live/solana/a-real-key"})
	if public["solana_rpc"] != "https://lb.drpc.live/solana/{key}" {
		t.Fatalf("legacy endpoint was not redacted: %q", public["solana_rpc"])
	}
}

func TestDRPCFetcherRetainsExactUsageWithoutExposingKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("Bearer token missing")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/stats" {
			_, _ = w.Write([]byte(`{"balance":12.345678901234567890,"data":[{"network":"solana","method":"getHealth","request_count":2,"cu":0.125,"client_key":"never-store"},{"network":"solana","method":"getBlock","request_count":3,"cu":0.375}]}`))
		} else {
			_, _ = w.Write([]byte(`[{"cu_spent_today":1.25,"key":{"api_key":"live-key","key_id":"id-1","name":"main","status":true,"cu_day_limit":100}}]`))
		}
	}))
	defer server.Close()
	fetcher := DRPCFetcher{Client: server.Client(), StatisticsURL: server.URL + "/stats", KeysURL: server.URL + "/keys"}
	snapshot := fetcher.Fetch(context.Background(), "test-token", "test-token", "live-key")
	if snapshot.ConnectionStatus != "available" || snapshot.Balance != "12.345678901234567890" || snapshot.RequestsDay == nil || *snapshot.RequestsDay != 5 || snapshot.CUDay != "0.5" {
		t.Fatalf("incorrect official snapshot: %+v", snapshot)
	}
	if len(snapshot.Keys) != 1 || snapshot.Keys[0]["selected"] != true {
		t.Fatalf("selected key was not identified: %+v", snapshot.Keys)
	}
	usageRows, ok := snapshot.Usage["data"].([]map[string]any)
	if !ok || len(usageRows) != 2 || usageRows[0]["cu"] != "0.125" || usageRows[0]["request_count"] != "2" {
		t.Fatalf("usage values lost precision: %+v", snapshot.Usage)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "live-key") || strings.Contains(string(encoded), "never-store") {
		t.Fatal("credential was retained in official snapshot")
	}
}

func TestDRPCFetcherDoesNotReportIncompleteUsageAsZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/stats" {
			_, _ = w.Write([]byte(`{"balance":1,"data":[{"request_count":"missing","cu":5}]}`))
		} else {
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	snapshot := (DRPCFetcher{Client: server.Client(), StatisticsURL: server.URL + "/stats", KeysURL: server.URL + "/keys"}).Fetch(context.Background(), "token", "token", "")
	if snapshot.ConnectionStatus != "partial" || snapshot.RequestsDay != nil || snapshot.CUDay != "" {
		t.Fatalf("incomplete official statistics were reported as usable: %+v", snapshot)
	}
}
