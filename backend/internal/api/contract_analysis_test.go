package api

import (
	"encoding/json"
	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/go-chi/chi/v5"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContractRoutesRejectBeforeDatabaseAndProvider(t *testing.T) {
	s := &Server{authMode: auth.ModeSession, policy: policy.NewEngine(nil, 0)}
	r := chi.NewRouter()
	s.registerContractUserRoutes(r)
	for _, target := range []struct{ method, path string }{{"POST", "/contract-analyses"}, {"GET", "/contract-analyses"}, {"GET", "/contract-analyses/id"}, {"GET", "/contract-analyses/id/methods"}, {"GET", "/contract-analyses/id/flow"}, {"GET", "/contract-analyses/id/transactions"}, {"GET", "/contract-analyses/id/methods/method"}, {"GET", "/contract-analyses/id/artifacts/artifact"}, {"GET", "/contract-analyses/id/artifacts/artifact/preview"}, {"POST", "/contract-analyses/id/cancel"}, {"POST", "/contract-analyses/id/resume"}} {
		for _, p := range []policy.Principal{{}, {Authenticated: true, Audience: "user", Permissions: []string{"dashboard:read"}}} {
			req := httptest.NewRequest(target.method, target.path, strings.NewReader(`{}`))
			req = req.WithContext(withPrincipal(req.Context(), p))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			want := 401
			if p.Authenticated {
				want = 403
			}
			if w.Code != want {
				t.Fatalf("%s %s got %d want %d", target.method, target.path, w.Code, want)
			}
		}
	}
}
func TestContractExecutionDispatchDoesNotMatchConstructorOrOverriddenDefinition(t *testing.T) {
	call := map[string]any{"selector": "", "input": "0x"}
	constructor := map[string]any{"selector": "", "entry_kind": "constructor"}
	internal := map[string]any{"selector": "", "entry_kind": "function", "visibility": "internal"}
	if contractDispatchCandidate(constructor, call, nil) || contractDispatchCandidate(internal, call, nil) {
		t.Fatal("non-runtime entry matched empty calldata")
	}
	receive := map[string]any{"selector": "", "entry_kind": "receive", "code_hash": "same", "storage_address": "proxy"}
	fallback := map[string]any{"selector": "", "entry_kind": "fallback", "code_hash": "same", "storage_address": "proxy"}
	methods := []json.RawMessage{ca.Row(receive), ca.Row(fallback)}
	if !contractDispatchCandidate(receive, call, methods) || contractDispatchCandidate(fallback, call, methods) {
		t.Fatal("empty calldata must select receive")
	}
	call["input"] = "0x0102"
	if contractDispatchCandidate(receive, call, methods) || !contractDispatchCandidate(fallback, call, methods) {
		t.Fatal("short nonempty calldata must select fallback")
	}
	call["selector"] = "0x12345678"
	if contractDispatchCandidate(map[string]any{"selector": "0x12345678", "dispatch_role": "overridden_source_definition"}, call, nil) {
		t.Fatal("overridden source definition attributed to dispatch")
	}
}
func TestContractActionScopesAndChunkBoundaries(t *testing.T) {
	for path, want := range map[string]string{"/api/v1/analysis/contracts/analyze_contract": "analysis", "/api/v1/analysis/contracts/explain_contract_transaction": "analysis", "/api/v1/analysis/contracts/get_contract_method_analysis": "read", "/api/v1/analysis/contracts/get_contract_analysis": "read", "/api/v1/export/contracts/get_contract_analysis_artifact": "export"} {
		if got := actionScopeForPath(path); got != want {
			t.Fatal(path, got, want)
		}
	}
	writer := contractChunkWriter{Offset: 4, End: 10}
	writer.Write([]byte("abcdef"))
	writer.Write([]byte("ghijklmnop"))
	if writer.Data.String() != "efghij" {
		t.Fatal(writer.Data.String())
	}
	q := ca.Request{TxHashes: []string{"0x" + strings.Repeat("1", 64), "0x" + strings.Repeat("1", 64)}}
	q.Defaults()
	if q.Validate() == nil {
		t.Fatal("duplicate transaction could double-count funds")
	}
}
