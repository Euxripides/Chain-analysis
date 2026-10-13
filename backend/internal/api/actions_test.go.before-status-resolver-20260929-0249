package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func TestActionAuthRequiresConfiguredBearerKey(t *testing.T) {
	t.Setenv("GPT_ACTIONS_API_KEYS", "alpha,beta")
	a := newActionAuth(nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	missing := httptest.NewRecorder()
	a.middleware(next).ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d", missing.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer beta")
	valid := httptest.NewRecorder()
	a.middleware(next).ServeHTTP(valid, req)
	if valid.Code != http.StatusNoContent {
		t.Fatalf("valid key status = %d body=%s", valid.Code, valid.Body.String())
	}
}

func TestActionAuthFailsClosedWhenNotConfigured(t *testing.T) {
	t.Setenv("GPT_ACTIONS_API_KEYS", "")
	t.Setenv("GPT_ACTIONS_API_KEY", "")
	a := newActionAuth(nil)
	rec := httptest.NewRecorder()
	a.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler must not run") })).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestActionsOpenAPI31HasUniqueOperations(t *testing.T) {
	doc := actionsOpenAPIDocument()
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v", doc["openapi"])
	}
	seen := map[string]bool{}
	for path, raw := range doc["paths"].(map[string]any) {
		for _, operation := range raw.(map[string]any) {
			op := operation.(map[string]any)
			id := op["operationId"].(string)
			if id == "" || seen[id] {
				t.Fatalf("path %s has missing/duplicate operationId %q", path, id)
			}
			seen[id] = true
			if op["description"] == "" || op["responses"] == nil {
				t.Fatalf("operation %s incomplete", id)
			}
			responses := op["responses"].(map[string]any)
			status := "200"
			if id == "createExport" {
				status = "202"
			}
			ref := responses[status].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"]
			if ref == "#/components/schemas/Envelope" || ref == nil {
				t.Fatalf("operation %s lacks a concrete response schema", id)
			}
		}
	}
	if len(seen) != len(actionOperations) {
		t.Fatalf("operations = %d want %d", len(seen), len(actionOperations))
	}
	if _, err := json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
}

func TestActionsOpenAPIUsesAIFacingBoundsAndEnums(t *testing.T) {
	params := actionParameters("getTokenTransfers")
	found := map[string]map[string]any{}
	for _, raw := range params {
		p := raw.(map[string]any)
		found[p["name"].(string)] = p["schema"].(map[string]any)
	}
	if found["page_size"]["maximum"] != 500 || found["page_size"]["default"] != 100 {
		t.Fatalf("page_size schema = %#v", found["page_size"])
	}
	if len(found["direction"]["enum"].([]string)) != 3 {
		t.Fatalf("direction enum = %#v", found["direction"])
	}
	if len(found["status"]["enum"].([]string)) != 2 {
		t.Fatalf("status enum = %#v", found["status"])
	}
	schemas := actionSchemas()
	for _, name := range []string{"EvidenceMeta", "AddressAssetsResponse", "FirstFundingResponse", "ExportStatusResponse"} {
		if schemas[name] == nil {
			t.Fatalf("missing schema %s", name)
		}
	}
}

func TestActionsOpenAPIDeepEvidenceSchemasExposeRequiredFields(t *testing.T) {
	schemas := actionSchemas()
	itemProps := func(response string) map[string]any {
		envelope := schemas[response].(map[string]any)
		data := envelope["properties"].(map[string]any)["data"].(map[string]any)
		return data["items"].(map[string]any)["properties"].(map[string]any)
	}
	checks := map[string][]string{
		"TokenTransferListResponse":       {"block_number", "log_index", "token_symbol", "token_contract", "raw_value", "amount"},
		"TransactionListResponse":         {"native_value", "gas", "gas_used", "fee", "method_id", "input"},
		"InternalTransactionListResponse": {"trace_id", "trace_address", "call_type", "value", "error"},
		"TransactionLogsResponse":         {"log_index", "contract_address", "topic0", "data", "decoded_args"},
	}
	for response, fields := range checks {
		props := itemProps(response)
		for _, field := range fields {
			if props[field] == nil {
				t.Fatalf("%s missing %s", response, field)
			}
		}
	}
	for _, response := range []string{"TransactionTraceResponse", "DecodeTransactionResponse", "TransactionDetailResponse", "FirstFundingResponse", "FundPathResponse", "AddressNetworkResponse"} {
		if schemas[response] == nil {
			t.Fatalf("missing %s", response)
		}
	}
}

func TestActionsOpenAPIPaginationAndTraversalSafety(t *testing.T) {
	schemas := actionSchemas()
	metaProps := schemas["EvidenceMeta"].(map[string]any)["properties"].(map[string]any)
	if metaProps["pagination"] == nil {
		t.Fatal("EvidenceMeta missing pagination")
	}
	pathData := schemas["FundPathResponse"].(map[string]any)["properties"].(map[string]any)["data"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"truncated", "node_count", "edge_count", "max_depth_requested", "max_depth_reached", "node_limit_reached"} {
		if pathData[field] == nil {
			t.Fatalf("FundPathResponse missing %s", field)
		}
	}
	params := actionParameters("getTokenTransfers")
	for _, raw := range params {
		p := raw.(map[string]any)
		if p["name"] == "start_time" && p["schema"].(map[string]any)["format"] != "date-time" {
			t.Fatal("start_time missing date-time format")
		}
	}
}

func TestActionsOpenAPISingleAssetAndExportPairConstraints(t *testing.T) {
	for _, id := range []string{"findFundPaths", "buildAddressNetwork"} {
		if len(actionRequestSchema(id)["anyOf"].([]any)) != 2 {
			t.Fatalf("%s must require token or token_contract", id)
		}
	}
	if len(actionRequestSchema("createExport")["oneOf"].([]any)) != 2 {
		t.Fatal("createExport must expose exactly two supported format pairs")
	}
	required := actionRequestSchema("createExport")["required"].([]string)
	for _, field := range []string{"format", "compression"} {
		found := false
		for _, value := range required {
			if value == field {
				found = true
			}
		}
		if !found {
			t.Fatalf("createExport must require %s", field)
		}
	}
	job := actionParameters("getExportStatus")[0].(map[string]any)["schema"].(map[string]any)
	if job["format"] != "uuid" {
		t.Fatalf("job_id format = %v", job["format"])
	}
	assetProps := actionSchemas()["AddressAssetsResponse"].(map[string]any)["properties"].(map[string]any)["data"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"native_balance_source", "native_is_current_balance", "native_as_of_block", "native_as_of_time"} {
		if assetProps[field] == nil {
			t.Fatalf("AddressAssetsResponse missing %s", field)
		}
	}
}

func TestActionsOpenAPIAlwaysUsesPublicServer(t *testing.T) {
	t.Setenv("GPT_ACTIONS_PUBLIC_BASE_URL", "")
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/openapi.json", nil)
	rec := httptest.NewRecorder()
	(&Server{}).handleActionsOpenAPI(rec, req)
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	server := doc["servers"].([]any)[0].(map[string]any)["url"]
	if server != "https://aurore.online" {
		t.Fatalf("public server = %v", server)
	}
}

func TestActionGzip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	actionGzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { actionOK(w, map[string]any{"ok": true}, nil) })).ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("content-encoding = %q", rec.Header().Get("Content-Encoding"))
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty compressed response")
	}
}

func TestApplyCurrentBalanceSnapshotReplacesObservedNetFlow(t *testing.T) {
	now := time.Date(2026, 9, 10, 3, 40, 0, 0, time.UTC)
	assets := []map[string]any{{
		"token_address": "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "token_symbol": "USDT", "token_decimals": 6,
		"balance": "1545.196958", "balance_source": "observed_net_flow", "is_current_balance": false,
	}}
	native, source, current, _, asOf, got := applyCurrentBalanceSnapshot([]provider.CurrentBalance{
		{Native: true, RawBalance: "2500000", AsOfTime: now},
		{TokenAddress: "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", RawBalance: "1234567", AsOfTime: now},
	}, assets)
	if native != "2.5" || source != "balance_snapshot" || !current || asOf != now {
		t.Fatalf("unexpected native snapshot: %s %s %v %v", native, source, current, asOf)
	}
	if got[0]["balance"] != "1.234567" || got[0]["raw_balance"] != "1234567" || got[0]["balance_source"] != "balance_snapshot" || got[0]["is_current_balance"] != true {
		t.Fatalf("unexpected token snapshot: %#v", got[0])
	}
}
