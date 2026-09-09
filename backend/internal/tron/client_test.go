package tron

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

type fixedTRC20IdentityResolver struct{}

func (fixedTRC20IdentityResolver) EnrichTRC20Identity(_ context.Context, _ provider.FetchRequest, rows []json.RawMessage) ([]json.RawMessage, error) {
	for i := range rows {
		var row map[string]any
		if err := json.Unmarshal(rows[i], &row); err != nil {
			return nil, err
		}
		row["block_number"], row["_tron_event_index"], row["_provider"] = uint64(84912055), uint64(30), "trongrid"
		rows[i], _ = json.Marshal(row)
	}
	return rows, nil
}

type countingBatchTRC20Resolver struct {
	single atomic.Int32
	batch  atomic.Int32
	pages  atomic.Int32
}

func (r *countingBatchTRC20Resolver) EnrichTRC20Identity(_ context.Context, _ provider.FetchRequest, rows []json.RawMessage) ([]json.RawMessage, error) {
	r.single.Add(1)
	return rows, nil
}

func (r *countingBatchTRC20Resolver) EnrichTRC20IdentityBatch(_ context.Context, _ provider.FetchRequest, pages [][]json.RawMessage) ([][]json.RawMessage, error) {
	r.batch.Add(1)
	r.pages.Add(int32(len(pages)))
	return pages, nil
}

func TestClientPaginationRetryAndUsage(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("TRON-PRO-API-KEY") != "secret" {
			t.Error("missing API key")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"slow"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[{"txID":"tx","blockNumber":123,"block_timestamp":1700000000000,"ret":[{"contractRet":"SUCCESS"}],"raw_data":{"contract":[]}}],"meta":{"fingerprint":"next"}}`))
	}))
	defer server.Close()
	var usage []model.UsageRow
	c, err := New(Options{BaseURL: server.URL, APIKey: "secret", RequestTimeout: 2 * time.Second, ConnectTimeout: time.Second, MaxRetries: 1, InitialConcurrency: 1, MaxConcurrency: 1}, func(u model.UsageRow) { usage = append(usage, u) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	c.http.Timeout = 2 * time.Second
	res, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions", Address: "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", PageSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasMore || res.NextPageToken != "next" || len(res.Rows) != 1 {
		t.Fatalf("bad page: %#v", res)
	}
	if len(usage) != 2 || usage[0].HTTPStatus != 429 || usage[1].UsageType != "transactions" || usage[1].Status != "success" {
		t.Fatalf("bad usage: %#v", usage)
	}
	if strings.Contains(string(res.Rows[0]), `"block_number"`) || !strings.Contains(string(res.Rows[0]), `"blockNumber":123`) {
		t.Fatal("TronGrid page was unexpectedly enriched or lost its native blockNumber")
	}
}

func TestClientTransactionsUsesFingerprintAddressIndex(t *testing.T) {
	var gotPath string
	var gotQuery = make(chan string, 1)
	var receiptCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/walletsolidity/gettransactioninfobyid" {
			receiptCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		gotPath = r.URL.Path
		gotQuery <- r.URL.RawQuery
		_, _ = w.Write([]byte(`{"success":true,"data":[{"txID":"tx-grid","blockNumber":321,"block_timestamp":1700000000000,"ret":[{"contractRet":"SUCCESS"}],"raw_data":{"contract":[{"type":"TransferContract","parameter":{"value":{"owner_address":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","to_address":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","amount":1}}}]}}],"meta":{"fingerprint":"fp-next"}}`))
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, APIKey: "secret", RequestTimeout: time.Second, ConnectTimeout: time.Second, MaxRetries: 0, InitialConcurrency: 1, MaxConcurrency: 1, PageSize: 200}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	minTS := time.UnixMilli(1700000000000).UTC().Add(-time.Minute)
	maxTS := time.UnixMilli(1700000000000).UTC().Add(time.Minute)
	res, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions", Address: "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", PageSize: 200, PageToken: "fp-prev", FromTime: &minTS, ToTime: &maxTS})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/accounts/TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj/transactions" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	q := <-gotQuery
	for _, want := range []string{"limit=200", "fingerprint=fp-prev", "only_confirmed=true", "order_by=block_timestamp%2Cdesc"} {
		if !strings.Contains(q, want) {
			t.Fatalf("query %q missing %q", q, want)
		}
	}
	if !res.HasMore || res.NextPageToken != "fp-next" || len(res.Rows) != 1 || !strings.Contains(string(res.Rows[0]), `"blockNumber":321`) {
		t.Fatalf("unexpected transaction page: %#v", res)
	}
	if got := receiptCalls.Load(); got != 0 {
		t.Fatalf("TronGrid transaction history issued %d receipt requests", got)
	}
}

func TestClientRejectsNonTransactionDatasets(t *testing.T) {
	c, err := New(Options{BaseURL: "https://api.trongrid.io", APIKey: "secret"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, dataset := range []string{"nft_transfers", "internal_transactions"} {
		_, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: dataset, Address: "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"})
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Class != provider.ClassUnsupported {
			t.Fatalf("dataset %s error=%v", dataset, err)
		}
	}
}

func TestClientTRC20EnrichesExactBlockAndEventIndex(t *testing.T) {
	const txID = "9e9a6aae39bd653da37988c5fe11332dc2a130acf6588d7021785d767655e621"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/accounts/TJmmqjb1DK9TTZbQXzRQ2AuA94z4gKAPFh/transactions/trc20":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"transaction_id":"` + txID + `","token_info":{"symbol":"USDT","address":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t","decimals":6,"name":"Tether USD"},"block_timestamp":1785406890000,"from":"TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ","to":"TJmmqjb1DK9TTZbQXzRQ2AuA94z4gKAPFh","type":"Transfer","value":"1000000"}],"meta":{}}`))
		case "/v1/transactions/" + txID + "/events":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"block_number":84912055,"block_timestamp":1785406890000,"contract_address":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t","event_index":7,"event_name":"Transfer","transaction_id":"` + txID + `","result":{"from":"0xa1749d3046533425557e8e0a3b1bca43b94a299a","to":"0x608f8da72479edc7dd921e4c30bb7e7cddbe722e","value":"1000000"}}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, APIKey: "secret", RequestTimeout: time.Second, MaxRetries: 0, InitialConcurrency: 1, MaxConcurrency: 1, IdentityResolver: fixedTRC20IdentityResolver{}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	res, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: "TJmmqjb1DK9TTZbQXzRQ2AuA94z4gKAPFh"})
	if err != nil {
		t.Fatal(err)
	}
	row, err := NormalizeTRC20(res.Rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if row.BlockNumber != 84912055 || row.LogIndex != 30 || row.Provider != "trongrid" || row.AmountRaw != "1000000" {
		t.Fatalf("unexpected enriched row: %#v", row)
	}
}

func TestClientTRC20ExcludesApprovalBeforeIdentityEnrichment(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":[` +
			`{"transaction_id":"approval","type":"Approval","value":"10"},` +
			`{"transaction_id":"transfer","type":"Transfer","value":"20"}` +
			`],"meta":{}}`))
	}))
	defer server.Close()
	resolver := &countingBatchTRC20Resolver{}
	c, err := New(Options{BaseURL: server.URL, RequestTimeout: time.Second, InitialConcurrency: 1, MaxConcurrency: 1, IdentityResolver: resolver}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	res, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || !strings.Contains(string(res.Rows[0]), `"transaction_id":"transfer"`) {
		t.Fatalf("rows=%s want only Transfer", res.Rows)
	}
	if got := resolver.single.Load(); got != 1 {
		t.Fatalf("identity enrichment calls=%d want 1", got)
	}
}

func TestClientTRC20ActivityBoundsUsesOldestAndNewestProbes(t *testing.T) {
	const oldest = int64(1784192328000)
	const newest = int64(1788860085000)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts := newest
		if r.URL.Query().Get("order_by") == "block_timestamp,asc" {
			ts = oldest
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []any{map[string]any{"block_timestamp": ts}}, "meta": map[string]any{}})
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, RequestTimeout: time.Second, InitialConcurrency: 2, NormalConcurrency: 2, MaxConcurrency: 2, DailyRequestBudget: 10}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	first, last, err := c.TRC20ActivityBounds(context.Background(), testAddress)
	if err != nil {
		t.Fatal(err)
	}
	if first.UnixMilli() != oldest || last.UnixMilli() != newest {
		t.Fatalf("bounds=%d..%d want %d..%d", first.UnixMilli(), last.UnixMilli(), oldest, newest)
	}
}

func TestClientTRC20PrefetchesBoundedPagesWithoutAdvancingCallerCursor(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		next := ""
		switch r.URL.Query().Get("fingerprint") {
		case "":
			next = "p2"
		case "p2":
			next = "p3"
		case "p3":
			next = ""
		default:
			t.Fatalf("unexpected fingerprint %q", r.URL.Query().Get("fingerprint"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []any{}, "meta": map[string]any{"fingerprint": next}})
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, RequestTimeout: time.Second, InitialConcurrency: 2, NormalConcurrency: 2, MaxConcurrency: 2, IdentityResolver: fixedTRC20IdentityResolver{}, TRC20PrefetchPages: 3, TRC20PageQueueMax: 4, DailyRequestBudget: 100}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	req := provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress, PageSize: 200}
	first, err := c.Fetch(context.Background(), req)
	if err != nil || first.NextPageToken != "p2" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	time.Sleep(100 * time.Millisecond)
	req.PageToken = "p2"
	second, err := c.Fetch(context.Background(), req)
	if err != nil || second.NextPageToken != "p3" {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("HTTP calls=%d want 3 (one foreground plus two bounded prefetch)", got)
	}
}

func TestClientTRC20PrefetchBatchEnrichesAdjacentPagesOnce(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := ""
		switch r.URL.Query().Get("fingerprint") {
		case "":
			next = "p2"
		case "p2":
			next = "p3"
		case "p3":
		default:
			t.Fatalf("unexpected fingerprint %q", r.URL.Query().Get("fingerprint"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []any{map[string]any{"transaction_id": r.URL.Query().Get("fingerprint")}}, "meta": map[string]any{"fingerprint": next}})
	}))
	defer server.Close()
	resolver := &countingBatchTRC20Resolver{}
	c, err := New(Options{BaseURL: server.URL, RequestTimeout: time.Second, InitialConcurrency: 2, NormalConcurrency: 2, MaxConcurrency: 2, IdentityResolver: resolver, TRC20PrefetchPages: 3, TRC20PageQueueMax: 4, DailyRequestBudget: 100}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	req := provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress, PageSize: 200}
	first, err := c.Fetch(context.Background(), req)
	if err != nil || first.NextPageToken != "p2" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	req.PageToken = "p2"
	second, err := c.Fetch(context.Background(), req)
	if err != nil || second.NextPageToken != "p3" {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	req.PageToken = "p3"
	third, err := c.Fetch(context.Background(), req)
	if err != nil || third.NextPageToken != "" {
		t.Fatalf("third=%#v err=%v", third, err)
	}
	if got := resolver.single.Load(); got != 1 {
		t.Fatalf("single-page enrichment calls=%d want 1 foreground page only", got)
	}
	if got := resolver.batch.Load(); got != 1 {
		t.Fatalf("batch enrichment calls=%d want 1", got)
	}
	if got := resolver.pages.Load(); got != 2 {
		t.Fatalf("batch pages=%d want 2", got)
	}
}

func TestClientTRC20PrefetchCapacityStillReleasesFetchedPage(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := "p3"
		if r.URL.Query().Get("fingerprint") == "" {
			next = "p2"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []any{}, "meta": map[string]any{"fingerprint": next}})
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, RequestTimeout: time.Second, InitialConcurrency: 2, NormalConcurrency: 2, MaxConcurrency: 2, IdentityResolver: fixedTRC20IdentityResolver{}, TRC20PrefetchPages: 3, TRC20PageQueueMax: 4, DailyRequestBudget: 100}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	c.prefetchMax = 1 // force the look-ahead capacity branch after p2 is fetched
	req := provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress, PageSize: 200}
	first, err := c.Fetch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.PageToken = first.NextPageToken
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	second, err := c.Fetch(ctx, req)
	if err != nil {
		t.Fatalf("capacity-limited prefetched page was not released: %v", err)
	}
	if second.NextPageToken != "p3" {
		t.Fatalf("next=%q want p3", second.NextPageToken)
	}
}

func TestClientConcurrencyCeilingIsGlobal(t *testing.T) {
	var active, maxActive atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := active.Add(1)
		for {
			old := maxActive.Load()
			if cur <= old || maxActive.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		active.Add(-1)
		_, _ = w.Write([]byte(`{"success":true,"data":[],"meta":{}}`))
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, APIKey: "secret", RequestTimeout: time.Second, ConnectTimeout: time.Second, MaxRetries: 0, InitialConcurrency: 4, NormalConcurrency: 4, MaxConcurrency: 4, PageSize: 200}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions", Address: "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", PageSize: 200})
		}()
	}
	wg.Wait()
	if got := maxActive.Load(); got > 4 {
		t.Fatalf("global HTTP concurrency exceeded max: %d", got)
	}
}

func TestClient429DownshiftsInDocumentedSteps(t *testing.T) {
	c, err := New(Options{BaseURL: "https://api.trongrid.io", InitialConcurrency: 8, NormalConcurrency: 8, MaxConcurrency: 8}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{6, 4, 2, 2} {
		c.observeRateLimit()
		c.concMu.Lock()
		got := c.concurrency
		c.concMu.Unlock()
		if got != want {
			t.Fatalf("concurrency after 429=%d, want %d", got, want)
		}
	}
}

func TestClientInvalidFingerprintIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid fingerprint"}`))
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, APIKey: "secret", RequestTimeout: time.Second, ConnectTimeout: time.Second, MaxRetries: 3, InitialConcurrency: 1, MaxConcurrency: 1}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	_, err = c.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions", Address: "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", PageToken: "bad"})
	if err == nil {
		t.Fatal("expected invalid fingerprint error")
	}
	if calls.Load() != 1 {
		t.Fatalf("invalid fingerprint was retried: calls=%d", calls.Load())
	}
	pe, ok := err.(*provider.Error)
	if !ok || pe.Class != provider.ClassInvalidRequest || pe.Retryable {
		t.Fatalf("unexpected error classification: %#v", err)
	}
}

func TestSharedRatePacerSpacesConcurrentDatasetRequests(t *testing.T) {
	pacer := NewRatePacer(10 * time.Millisecond)
	started := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pacer.wait(context.Background()); err != nil {
				t.Errorf("wait: %v", err)
			}
		}()
	}
	wg.Wait()
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond {
		t.Fatalf("shared pacer released requests too quickly: %s", elapsed)
	}
}

func TestDispatcherLeavesEVMUnchanged(t *testing.T) {
	evm := &fakeProvider{name: "evm"}
	tr := &fakeProvider{name: "tron"}
	d := &Dispatcher{EVM: evm, Tron: tr}
	_, _ = d.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc"})
	if evm.calls != 1 || tr.calls != 0 {
		t.Fatalf("unexpected routing evm=%d tron=%d", evm.calls, tr.calls)
	}
}

func TestAccountStateKeepsValidUnactivatedSeparateFromInvalid(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
	}))
	defer server.Close()
	var usage []model.UsageRow
	c, err := New(Options{BaseURL: server.URL, APIKey: "secret", RequestTimeout: time.Second, ConnectTimeout: time.Second, InitialConcurrency: 1, MaxConcurrency: 1}, func(row model.UsageRow) { usage = append(usage, row) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.http = server.Client()
	kind, err := c.AccountState(context.Background(), "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj")
	if err != nil {
		t.Fatal(err)
	}
	if kind != AccountValidUnactivated {
		t.Fatalf("kind=%s", kind)
	}
	if len(usage) != 1 || usage[0].UsageType != "account" || usage[0].Status != "success" {
		t.Fatalf("usage=%#v", usage)
	}
}

type fakeProvider struct {
	name         string
	calls        int
	detailCalls  int
	receiptCalls int
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Name: f.name}
}
func (f *fakeProvider) Fetch(context.Context, provider.FetchRequest) (provider.FetchResult, error) {
	f.calls++
	return provider.FetchResult{}, nil
}
func (f *fakeProvider) Health(context.Context) provider.HealthStatus {
	return provider.HealthStatus{Provider: f.name}
}
func (f *fakeProvider) TxDetail(context.Context, string, string, bool, bool) (json.RawMessage, error) {
	f.detailCalls++
	return json.RawMessage(`{"provider":"` + f.name + `"}`), nil
}
func (f *fakeProvider) TxReceipt(context.Context, string, string) (json.RawMessage, error) {
	f.receiptCalls++
	return json.RawMessage(`{"provider":"` + f.name + `"}`), nil
}
