package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"github.com/go-chi/chi/v5"
)

func TestCrossChainAuthenticatedRoutesAndProtocolResult(t *testing.T) {
	t.Setenv("GPT_ACTIONS_API_KEYS", "test-bridge-key")
	hash := "0x" + strings.Repeat("1", 64)
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		json.NewEncoder(w).Encode(map[string]any{"status": "DONE", "sending": map[string]any{"txHash": hash, "chainId": 1, "amount": "1000000", "token": map[string]any{"decimals": 6}}, "receiving": map[string]any{"txHash": hash, "chainId": 56, "amount": "1000000", "token": map[string]any{"decimals": 6}}})
	}))
	defer upstream.Close()
	s := &Server{crossChain: crosschain.New(crosschain.Config{Bases: map[string]string{"lifi": upstream.URL}})}
	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(newActionAuth(nil).middleware)
		s.registerCrossChainRoutes(r)
	})
	request := func(path, body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	body := `{"bridge":"lifi","chain":"bsc","tx_hash":"` + hash + `"}`
	if w := request("/bridge/transfer", body, ""); w.Code != 401 || hits != 0 {
		t.Fatalf("unauthenticated lookup: %d hits=%d", w.Code, hits)
	}
	for _, invalid := range []string{`{"bridge":"near_intents","order_id":"unsupported"}`, body + `{}`, strings.TrimSuffix(body, "}") + `,"url":"http://localhost"}`, strings.Repeat("x", 65537)} {
		if w := request("/bridge/transfer", invalid, "test-bridge-key"); w.Code != 400 || hits != 0 {
			t.Fatalf("invalid request reached provider: %d hits=%d %s", w.Code, hits, w.Body.String())
		}
	}
	w := request("/bridge/transfer", body, "test-bridge-key")
	var out struct {
		Success bool                   `json:"success"`
		Data    crosschain.QueryResult `json:"data"`
		Meta    map[string]any         `json:"meta"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil || w.Code != 200 || !out.Success || out.Data.VerificationMode != "funds" || len(out.Data.Transfers) != 0 || len(out.Data.PendingTransfers) != 1 || out.Data.PendingTransfers[0].FundsVerified || len(out.Data.PendingTransfers[0].Verification) != 2 || len(out.Data.PendingTransfers[0].BlockingReasons) == 0 {
		t.Fatalf("bad result %d %s err=%v", w.Code, w.Body.String(), e)
	}
	if hits != 1 {
		t.Fatalf("hits=%d", hits)
	}
	if out.Meta["cached"] != false || out.Meta["source"] != "bridge_protocol_api_and_exact_chain_evidence" {
		t.Fatal("protocol result mislabeled as local cache", out.Meta)
	}
	discoveryBody := strings.TrimSuffix(body, "}") + `,"verification_mode":"discovery"}`
	w = request("/bridge/transfer", discoveryBody, "test-bridge-key")
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil || out.Data.VerificationMode != "discovery" || len(out.Data.Transfers) != 1 || out.Data.Transfers[0].FundsVerified || len(out.Data.Transfers[0].BlockingReasons) == 0 {
		t.Fatal("discovery must preserve pending protocol evidence", w.Body.String())
	}
	for _, invalid := range []string{strings.TrimSuffix(body, "}") + `,"verification_mode":"relaxed"}`, strings.TrimSuffix(body, "}") + `,"funds_verified":true}`, strings.TrimSuffix(body, "}") + `,"proof":{}}`} {
		before := hits
		if w := request("/bridge/transfer", invalid, "test-bridge-key"); w.Code != 400 || hits != before {
			t.Fatal("untrusted evidence reached provider", w.Body.String())
		}
	}
}

func TestCrossChainScopesAndOpenAPIContracts(t *testing.T) {
	for path, scope := range map[string]string{"/api/v1/bridge/capabilities": "read", "/api/v1/bridge/transfer": "read", "/api/v1/bridge/candidates": "analysis"} {
		if actionScopeForPath(path) != scope || scope == "analysis" && actionScopeAllowed([]string{"read"}, scope) {
			t.Fatalf("incorrect scope for %s", path)
		}
	}
	doc := actionsOpenAPIDocument()
	paths := doc["paths"].(map[string]any)
	for path, method := range map[string]string{"/api/v1/bridge/capabilities": "get", "/api/v1/bridge/transfer": "post", "/api/v1/bridge/candidates": "post"} {
		if op, ok := paths[path].(map[string]any); !ok || op[method] == nil {
			t.Fatalf("OpenAPI path missing %s", path)
		}
	}
	if required := crossChainRequestSchema("queryCrossChainTransfer")["required"]; required == nil {
		t.Fatal("OpenAPI required cannot be null")
	}
	schema := crossChainRequestSchema("findCrossChainCandidates")
	props := schema["properties"].(map[string]any)
	if props["scan_limit"].(map[string]any)["maximum"] != 1000 || props["max_combinations"].(map[string]any)["maximum"] != 20000 {
		t.Fatal("bounds missing from schema")
	}
}

func TestBridgeDoesNotInferNativeFromMissingAsset(t *testing.T) {
	for _, tc := range []struct {
		leg  crosschain.Leg
		want bool
	}{
		{crosschain.Leg{Chain: "bsc"}, false},
		{crosschain.Leg{Chain: "bsc", Token: "USDT"}, false},
		{crosschain.Leg{Chain: "bsc", Token: "BNB"}, true},
		{crosschain.Leg{Chain: "solana", TokenContract: "11111111111111111111111111111111"}, true},
	} {
		if bridgeNativeAsset(tc.leg) != tc.want {
			t.Fatalf("native inference %+v", tc.leg)
		}
	}
}
