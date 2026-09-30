package providercontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnkrPlatformFetcherReadsAccountWithoutReturningJWT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer platform-test-secret" {
			t.Errorf("missing platform authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.String() {
		case "/api/v1/auth/balance":
			_, _ = w.Write([]byte(`{"balance_usd":"12.742240000000000001","balance_credit_usd":"12.74"}`))
		case "/api/v1/auth/stats?intervalType=h24":
			_, _ = w.Write([]byte(`{"total_requests":9007199254740993,"stats":{"bsc":{"total":{"total_cost":9007199254740994},"counts":{"rpc":{"top_requests":[{"method":"eth_getLogs","count":123,"total_cost":456}]}}}}}`))
		case "/api/v1/auth/jwt/all":
			_, _ = w.Write([]byte(`[{"index":1,"name":"default","is_encrypted":true,"jwt_data":"rpc-secret-must-not-leak","config":"test"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	snapshot := (AnkrPlatformFetcher{BaseURL: server.URL}).Fetch(context.Background(), "platform-test-secret")
	if snapshot.ConnectionStatus != "available" || snapshot.Balance != "12.742240000000000001" ||
		snapshot.RequestsDay == nil || *snapshot.RequestsDay != 9007199254740993 ||
		snapshot.CUDay != "9007199254740994" || len(snapshot.Keys) != 1 {
		t.Fatalf("unexpected account snapshot: %+v", snapshot)
	}
	if _, found := snapshot.Keys[0]["jwt_data"]; found {
		t.Fatal("RPC secret was returned in key metadata")
	}
	if rows, ok := snapshot.Usage["data"].([]map[string]any); !ok || len(rows) != 1 || rows[0]["method"] != "eth_getLogs" {
		t.Fatalf("usage details missing: %+v", snapshot.Usage)
	}
}

func TestAnkrPlatformFetcherRejectsExpiredKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("platform-test-secret"))
	}))
	defer server.Close()
	snapshot := (AnkrPlatformFetcher{BaseURL: server.URL}).Fetch(context.Background(), "platform-test-secret")
	if snapshot.ConnectionStatus != "expired" || snapshot.ErrorCode != "platform_key_rejected" {
		t.Fatalf("unexpected auth failure: %+v", snapshot)
	}
	if strings.Contains(snapshot.ErrorCode, "platform-test-secret") {
		t.Fatal("secret leaked in error")
	}
}
