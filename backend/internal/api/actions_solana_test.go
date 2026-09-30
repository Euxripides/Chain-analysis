package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleSolanaSignature = "5eDPXxyeaHuK9YJKPXpXbWUne9At11oQzdJv6hLmuVqQqneiiYdAMFDn7CYn9Edc9RQWbULAnv33GK34mnoSinfZ"

func TestSolanaBase58ValidationPreservesCaseAndWidth(t *testing.T) {
	if !validSolanaBase58(sampleSolanaSignature, 64) {
		t.Fatal("complete Solana signature rejected")
	}
	if !validSolanaBase58("11111111111111111111111111111111", 32) {
		t.Fatal("system account rejected")
	}
	for _, bad := range []string{"", "0", "1111111111111111111111111111111"} {
		if validSolanaBase58(bad, 64) {
			t.Fatalf("invalid signature accepted: %q", bad)
		}
	}
}

func TestSolanaSourceRecordAndBridgeOrderRemainDistinct(t *testing.T) {
	t.Setenv("DRPC_ENABLED", "true")
	t.Setenv("SOLANA_PROVIDER", "drpc")
	t.Setenv("DRPC_API_KEY", "")
	t.Setenv("DRPC_SOLANA_MAX_TX_VERSION", "")
	var methodName string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		methodName = request.Method
		if request.Method != "getTransaction" || request.Params[0] != sampleSolanaSignature {
			t.Errorf("RPC request = %#v", request)
		}
		if options, ok := request.Params[1].(map[string]any); !ok || options["maxSupportedTransactionVersion"] != float64(1) {
			t.Errorf("v1 transaction support missing from request: %#v", request.Params)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"slot":123456789,"blockTime":1790000000,"version":1,"transaction":{"signatures":["` + sampleSolanaSignature + `"],"message":{"accountKeys":[],"instructions":[]}},"meta":{"err":null,"fee":5000,"innerInstructions":[],"logMessages":["Program log: test"]}}}`))
	}))
	defer upstream.Close()
	t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", upstream.URL)
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleActionSolanaTransaction(rec, httptest.NewRequest(http.MethodGet, "/?signature="+sampleSolanaSignature, nil))
	if rec.Code != http.StatusOK || methodName != "getTransaction" {
		t.Fatalf("transaction status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data map[string]any `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data["signature"] != sampleSolanaSignature || response.Data["source_record_status"] != "readable" {
		t.Fatalf("source record = %#v", response.Data)
	}
	if response.Data["version"] != float64(1) {
		t.Fatalf("version 1 record was not preserved: %#v", response.Data)
	}
	if response.Meta["source"] != "drpc_solana_rpc" || response.Meta["coverage"].(map[string]any)["complete"] != false {
		t.Fatalf("evidence metadata = %#v", response.Meta)
	}
	rec = httptest.NewRecorder()
	s.handleActionBridgeSourceTransaction(rec, httptest.NewRequest(http.MethodGet, "/?source_chain=solana&source_tx_hash="+sampleSolanaSignature+"&bridge_order_id=order-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("bridge source status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data["bridge_order_association_verified"] != false || response.Data["bridge_order_id"] != "order-1" {
		t.Fatalf("bridge association was overstated: %#v", response.Data)
	}
	rec = httptest.NewRecorder()
	s.handleActionBridgeSourceTransaction(rec, httptest.NewRequest(http.MethodGet, "/?source_chain=solana&bridge_order_id=order-1", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "SOURCE_TX_REQUIRED") {
		t.Fatalf("order-only status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSolanaRPCUsesDRPCAndOptionalDRPCArchive(t *testing.T) {
	t.Setenv("DRPC_ENABLED", "true")
	t.Setenv("SOLANA_PROVIDER", "drpc")
	t.Setenv("DRPC_API_KEY", "test-key")
	t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", "https://lb.drpc.live/solana/{key}")
	t.Setenv("DRPC_SOLANA_ARCHIVE_RPC_ENDPOINT", "https://archive.drpc.live/solana/{key}")
	endpoints, err := solanaRPCEndpoints()
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 || endpoints[0].source != "drpc_solana_rpc" || endpoints[0].url != "https://lb.drpc.live/solana/test-key" || endpoints[1].source != "drpc_solana_archive_rpc" {
		t.Fatalf("Solana RPC routing = %#v", endpoints)
	}
}

func TestSolanaDoesNotFallBackToAnkrOrPublicRPC(t *testing.T) {
	t.Setenv("DRPC_ENABLED", "true")
	t.Setenv("SOLANA_PROVIDER", "drpc")
	t.Setenv("DRPC_API_KEY", "")
	t.Setenv("DRPC_RUNTIME_KEY_REF", "")
	t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", "")
	t.Setenv("DRPC_BASE_URL", "")
	t.Setenv("DRPC_SOLANA_ARCHIVE_RPC_ENDPOINT", "")
	t.Setenv("ANKR_API_KEY", "not-a-drpc-key")
	if _, err := solanaRPCEndpoints(); err == nil {
		t.Fatal("Solana must not silently use the Ankr key or public RPC when dRPC credentials are missing")
	}
}

func TestSolanaDRPCBaseAlreadyIncludesChain(t *testing.T) {
	t.Setenv("DRPC_ENABLED", "true")
	t.Setenv("SOLANA_PROVIDER", "drpc")
	t.Setenv("DRPC_API_KEY", "test-key")
	t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", "")
	t.Setenv("DRPC_BASE_URL", "https://lb.drpc.live/solana")
	t.Setenv("DRPC_SOLANA_ARCHIVE_RPC_ENDPOINT", "")
	endpoints, err := solanaRPCEndpoints()
	if err != nil || len(endpoints) != 1 || endpoints[0].url != "https://lb.drpc.live/solana/test-key" {
		t.Fatalf("dRPC endpoint = %#v, %v", endpoints, err)
	}
}

func TestSolanaRejectsNonDRPCEndpoint(t *testing.T) {
	t.Setenv("DRPC_ENABLED", "true")
	t.Setenv("SOLANA_PROVIDER", "drpc")
	t.Setenv("DRPC_API_KEY", "test-key")
	t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", "https://api.mainnet.solana.com")
	t.Setenv("DRPC_SOLANA_ARCHIVE_RPC_ENDPOINT", "")
	if _, err := solanaRPCEndpoints(); err == nil {
		t.Fatal("Solana provider must not silently switch from dRPC to a public or unrelated RPC")
	}
}

func TestSolanaOldTransactionFallsBackToDRPCArchive(t *testing.T) {
	primaryCalls, archiveCalls := 0, 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls++
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":null}`))
	}))
	defer primary.Close()
	archive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		archiveCalls++
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"slot":420305276,"blockTime":1779008541,"transaction":{"signatures":["` + sampleSolanaSignature + `"],"message":{"accountKeys":[],"instructions":[]}},"meta":{"err":null,"fee":14120}}}`))
	}))
	defer archive.Close()
	t.Setenv("DRPC_ENABLED", "true")
	t.Setenv("SOLANA_PROVIDER", "drpc")
	t.Setenv("DRPC_API_KEY", "test-key")
	t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", primary.URL+"/{key}")
	t.Setenv("DRPC_SOLANA_ARCHIVE_RPC_ENDPOINT", archive.URL)
	data, err := solanaTransaction(httptest.NewRequest(http.MethodGet, "/", nil).Context(), sampleSolanaSignature)
	if err != nil {
		t.Fatal(err)
	}
	if primaryCalls != 1 || archiveCalls != 1 || data["rpc_source"] != "drpc_solana_archive_rpc" {
		t.Fatalf("fallback calls=%d/%d, source=%v", primaryCalls, archiveCalls, data["rpc_source"])
	}
}

func TestSolanaTransactionDoesNotTreatDRPCHTTP500AsReadable(t *testing.T) {
	methods := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&call)
		methods = append(methods, call.Method)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()
	t.Setenv("DRPC_ENABLED", "true")
	t.Setenv("SOLANA_PROVIDER", "drpc")
	t.Setenv("DRPC_API_KEY", "test-key")
	t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", upstream.URL)
	t.Setenv("DRPC_SOLANA_ARCHIVE_RPC_ENDPOINT", "")
	record, err := solanaTransaction(context.Background(), sampleSolanaSignature)
	if err == nil || record != nil || len(methods) != 1 || methods[0] != "getTransaction" {
		t.Fatalf("HTTP 500 must remain unreadable: methods=%v record=%v err=%v", methods, record, err)
	}
}

func TestSolanaSignatureListIsBoundedAndNeverCompleteCoverage(t *testing.T) {
	t.Setenv("DRPC_ENABLED", "true")
	t.Setenv("SOLANA_PROVIDER", "drpc")
	t.Setenv("DRPC_API_KEY", "")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Method != "getSignaturesForAddress" || request.Params[0] != "11111111111111111111111111111111" {
			t.Errorf("RPC request = %#v", request)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":[{"signature":"` + sampleSolanaSignature + `","slot":123,"blockTime":1790000000,"err":null}]}`))
	}))
	defer upstream.Close()
	t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", upstream.URL)
	rec := httptest.NewRecorder()
	(&Server{}).handleActionSolanaSignatures(rec, httptest.NewRequest(http.MethodGet, "/?address=11111111111111111111111111111111&limit=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Meta["coverage"].(map[string]any)["complete"] != false {
		t.Fatalf("signature page falsely asserted full coverage: %#v", response.Meta)
	}
	page := response.Meta["pagination"].(map[string]any)
	if page["next_before"] != sampleSolanaSignature || page["has_more"] != true {
		t.Fatalf("pagination = %#v", page)
	}
	rec = httptest.NewRecorder()
	(&Server{}).handleActionSolanaSignatures(rec, httptest.NewRequest(http.MethodGet, "/?address=11111111111111111111111111111111&limit=101", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unbounded page status=%d", rec.Code)
	}
}

func TestSolanaRPCNullAndRateLimitAreNotEmptyEvidence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantCode string
	}{
		{"null", 200, `{"jsonrpc":"2.0","result":null}`, "SOURCE_RECORD_UNAVAILABLE"},
		{"rate_limit", 429, ``, "SOLANA_RPC_RATE_LIMITED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DRPC_ENABLED", "true")
			t.Setenv("SOLANA_PROVIDER", "drpc")
			t.Setenv("DRPC_API_KEY", "")
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", upstream.URL)
			rec := httptest.NewRecorder()
			(&Server{}).handleActionSolanaTransaction(rec, httptest.NewRequest(http.MethodGet, "/?signature="+sampleSolanaSignature, nil))
			if rec.Code == 200 || !strings.Contains(rec.Body.String(), tc.wantCode) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
