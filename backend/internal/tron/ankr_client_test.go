package tron

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

const testAddress = "TJmmqjb1DK9TTZbQXzRQ2AuA94z4gKAPFh"

func testAnkrClient(server *httptest.Server) *AnkrClient {
	return &AnkrClient{
		rpcURL: server.URL, restURL: server.URL, http: server.Client(), maxRetries: 0,
		sem: make(chan struct{}, 2), receiptSem: make(chan struct{}, 2),
		opts:      AnkrOptions{LogInitialSpan: 10, LogMinSpan: 1, LogMaxSpan: 100, BlockSpan: 2, ReceiptSpan: 2, MaxScanBlocks: 100, MaxTxAutoScan: 100, MaxInternalAutoScan: 100, TimeBlockCacheTTL: time.Hour, TimeBlockProbeRetry: 2, ReceiptInitialConcurrency: 2, ReceiptMaxConcurrency: 2},
		timeCache: map[int64]uint64{}, timeCacheExpires: map[int64]time.Time{}, timestampCache: map[uint64]time.Time{}, timestampCacheExpires: map[uint64]time.Time{}, blockCache: map[string][]byte{},
		trc20Receipts: newTRC20ReceiptCache(16, time.Hour),
	}
}

func TestAnkrEnrichTRC20IdentityUsesReceiptLogIndex(t *testing.T) {
	const txID = "9fe54145004fad9a7c917089108f2112bbc707b756a554fb22a9482d399c25a9"
	evmFrom, _ := Base58TToEVMHex20("TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ")
	evmTo, _ := Base58TToEVMHex20("TVXnjFSDgDh3cyuHHJPwQSkX9ncpt9jVYU")
	evmToken, _ := Base58TToEVMHex20("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")
	topic := func(address string) string { return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(address, "0x") }
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Method != "eth_getTransactionReceipt" {
			t.Fatalf("unexpected method %s", body.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
			"blockNumber": "0x52077bb", "transactionHash": "0x" + txID,
			"logs": []any{map[string]any{"address": evmToken, "logIndex": "0x6", "topics": []string{transferTopic, topic(evmFrom), topic(evmTo)}, "data": "0x1c9c380"}},
		}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	rows := []json.RawMessage{json.RawMessage(`{"transaction_id":"` + txID + `","token_info":{"address":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"},"block_timestamp":1788713403000,"from":"TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ","to":"TVXnjFSDgDh3cyuHHJPwQSkX9ncpt9jVYU","value":"30000000"}`)}
	got, err := c.EnrichTRC20Identity(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	row, err := NormalizeTRC20(got[0])
	if err != nil {
		t.Fatal(err)
	}
	if row.BlockNumber != 86013883 || row.LogIndex != 6 || row.Provider != "trongrid" || row.AmountRaw != "30000000" {
		t.Fatalf("unexpected receipt-enriched row: %#v", row)
	}
}

func TestAnkrEnrichTRC20IdentityGroupsSameTransactionAndCachesReceipt(t *testing.T) {
	const txID = "9fe54145004fad9a7c917089108f2112bbc707b756a554fb22a9482d399c25a9"
	from := "TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ"
	to := "TVXnjFSDgDh3cyuHHJPwQSkX9ncpt9jVYU"
	token := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	evmFrom, _ := Base58TToEVMHex20(from)
	evmTo, _ := Base58TToEVMHex20(to)
	evmToken, _ := Base58TToEVMHex20(token)
	topic := func(address string) string { return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(address, "0x") }
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
			"blockNumber": "0x52077bb", "transactionHash": "0x" + txID,
			"logs": []any{
				map[string]any{"address": evmToken, "logIndex": "0x6", "topics": []string{transferTopic, topic(evmFrom), topic(evmTo)}, "data": "0x1"},
				map[string]any{"address": evmToken, "logIndex": "0x9", "topics": []string{transferTopic, topic(evmFrom), topic(evmTo)}, "data": "0x1"},
			},
		}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	makeRows := func() []json.RawMessage {
		row := json.RawMessage(`{"transaction_id":"` + txID + `","token_info":{"address":"` + token + `"},"from":"` + from + `","to":"` + to + `","value":"1"}`)
		return []json.RawMessage{append(json.RawMessage(nil), row...), append(json.RawMessage(nil), row...)}
	}
	for run := 0; run < 2; run++ {
		got, err := c.EnrichTRC20Identity(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers"}, makeRows())
		if err != nil {
			t.Fatal(err)
		}
		first, _ := NormalizeTRC20(got[0])
		second, _ := NormalizeTRC20(got[1])
		if first.LogIndex != 6 || second.LogIndex != 9 {
			t.Fatalf("occurrence mapping = %d,%d", first.LogIndex, second.LogIndex)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("receipt upstream requests = %d, want 1", requests.Load())
	}
}

func TestAdaptiveLogEnrichmentUsesBoundedLogsAndExactIndexes(t *testing.T) {
	from := "TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ"
	to := "TVXnjFSDgDh3cyuHHJPwQSkX9ncpt9jVYU"
	token := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	evmFrom, _ := Base58TToEVMHex20(from)
	evmTo, _ := Base58TToEVMHex20(to)
	evmToken, _ := Base58TToEVMHex20(token)
	topic := func(address string) string { return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(address, "0x") }
	var methodsMu sync.Mutex
	var methods []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		methodsMu.Lock()
		methods = append(methods, body.Method)
		methodsMu.Unlock()
		if body.Method == "eth_getTransactionReceipt" {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"blockNumber": "0x64", "logs": []any{}}})
			return
		}
		if body.Method != "eth_getLogs" {
			t.Errorf("unexpected method %s", body.Method)
			return
		}
		logs := make([]map[string]any, 0, 10)
		for i := 0; i < 10; i++ {
			txID := strings.Repeat(strconv.FormatInt(int64(i+1), 16), 64)[:64]
			logs = append(logs, map[string]any{"address": evmToken, "blockNumber": "0x64", "transactionHash": "0x" + txID, "logIndex": fmt.Sprintf("0x%x", i), "topics": []string{transferTopic, topic(evmFrom), topic(evmTo)}, "data": "0x1"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": logs})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	c.opts.TRC20AdaptiveEnrichment, c.opts.TRC20LogEnrichmentRatio, c.opts.TRC20EnrichLogMaxWindows = true, .5, 20
	// Production enables both flags. Adaptive Logs must remain the primary
	// path; batched receipts are only the correctness fallback.
	c.opts.TRC20BatchReceipts = true
	c.opts.LogMaxSpan = 100
	rows := make([]json.RawMessage, 0, 10)
	for i := 0; i < 10; i++ {
		txID := strings.Repeat(strconv.FormatInt(int64(i+1), 16), 64)[:64]
		rows = append(rows, json.RawMessage(fmt.Sprintf(`{"transaction_id":"%s","token_info":{"address":"%s"},"block_timestamp":1700000000000,"from":"%s","to":"%s","value":"1"}`, txID, token, from, to)))
	}
	got, err := c.EnrichTRC20Identity(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress}, rows)
	if err != nil {
		t.Fatal(err)
	}
	for i, raw := range got {
		row, err := NormalizeTRC20(raw)
		if err != nil || row.BlockNumber != 100 || row.LogIndex != uint32(i) {
			t.Fatalf("row %d=%#v err=%v", i, row, err)
		}
	}
	logCalls, receiptCalls := 0, 0
	for _, method := range methods {
		switch method {
		case "eth_getLogs":
			logCalls++
		case "eth_getTransactionReceipt":
			receiptCalls++
		}
	}
	if logCalls != 2 || receiptCalls != 1 {
		t.Fatalf("calls logs=%d receipts=%d want logs=2 receipts=1", logCalls, receiptCalls)
	}
}

func TestTRC20BatchReceiptsResolveExactIdentitiesInOneHTTPCall(t *testing.T) {
	from := "TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ"
	to := "TVXnjFSDgDh3cyuHHJPwQSkX9ncpt9jVYU"
	token := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	evmFrom, _ := Base58TToEVMHex20(from)
	evmTo, _ := Base58TToEVMHex20(to)
	evmToken, _ := Base58TToEVMHex20(token)
	topic := func(address string) string { return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(address, "0x") }
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var calls []struct {
			ID     int      `json:"id"`
			Params []string `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&calls); err != nil {
			t.Fatal(err)
		}
		responses := make([]map[string]any, 0, len(calls))
		for i, call := range calls {
			responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": map[string]any{
				"blockNumber": "0x64", "transactionHash": call.Params[0], "logs": []any{map[string]any{
					"address": evmToken, "logIndex": fmt.Sprintf("0x%x", i), "topics": []string{transferTopic, topic(evmFrom), topic(evmTo)}, "data": "0x1",
				}},
			}})
		}
		_ = json.NewEncoder(w).Encode(responses)
	}))
	defer server.Close()
	c := testAnkrClient(server)
	c.opts.TRC20BatchReceipts = true
	rows := make([]json.RawMessage, 0, 10)
	for i := 0; i < 10; i++ {
		txID := fmt.Sprintf("%064x", i+1)
		rows = append(rows, json.RawMessage(fmt.Sprintf(`{"transaction_id":"%s","token_info":{"address":"%s"},"from":"%s","to":"%s","value":"1"}`, txID, token, from, to)))
	}
	got, err := c.EnrichTRC20Identity(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress}, rows)
	if err != nil {
		t.Fatal(err)
	}
	for i, raw := range got {
		row, normalizeErr := NormalizeTRC20(raw)
		if normalizeErr != nil || row.BlockNumber != 100 || row.LogIndex != uint32(i) {
			t.Fatalf("row %d=%#v err=%v", i, row, normalizeErr)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests=%d want 1 batched receipt request", requests.Load())
	}
	if _, err := c.EnrichTRC20Identity(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress}, rows); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests=%d want cached batch replay to issue no additional request", requests.Load())
	}
}

func TestAdaptiveLogEnrichmentKeepsMatchedRowsAndReceiptsOnlyUnmatchedEvent(t *testing.T) {
	from := "TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ"
	to := "TVXnjFSDgDh3cyuHHJPwQSkX9ncpt9jVYU"
	token := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	evmFrom, _ := Base58TToEVMHex20(from)
	evmTo, _ := Base58TToEVMHex20(to)
	evmToken, _ := Base58TToEVMHex20(token)
	topic := func(address string) string { return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(address, "0x") }
	txID := func(i int) string { return fmt.Sprintf("%064x", i) }
	var receiptCalls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		if len(raw) > 0 && raw[0] == '[' {
			var calls []struct {
				ID     int               `json:"id"`
				Method string            `json:"method"`
				Params []json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(raw, &calls); err != nil {
				t.Fatal(err)
			}
			receiptCalls.Add(1)
			responses := make([]map[string]any, 0, len(calls))
			for _, call := range calls {
				if call.Method != "eth_getTransactionReceipt" {
					t.Fatalf("unexpected batched method %s", call.Method)
				}
				var requested string
				_ = json.Unmarshal(call.Params[0], &requested)
				responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": map[string]any{
					"blockNumber": "0x64", "transactionHash": requested, "logs": []any{},
				}})
			}
			_ = json.NewEncoder(w).Encode(responses)
			return
		}
		var body struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(raw, &body)
		if body.Method == "eth_getLogs" {
			logs := make([]map[string]any, 0, 10)
			for i := 1; i <= 10; i++ {
				logs = append(logs, map[string]any{"address": evmToken, "blockNumber": "0x64", "transactionHash": "0x" + txID(i), "logIndex": "0x1", "topics": []string{transferTopic, topic(evmFrom), topic(evmTo)}, "data": "0x1"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": logs})
			return
		}
		if body.Method != "eth_getTransactionReceipt" {
			t.Fatalf("unexpected method %s", body.Method)
		}
		receiptCalls.Add(1)
		var requested string
		_ = json.Unmarshal(body.Params[0], &requested)
		logs := []any{map[string]any{"address": evmToken, "logIndex": "0x1", "topics": []string{transferTopic, topic(evmFrom), topic(evmTo)}, "data": "0x1"}}
		if strings.EqualFold(strings.TrimPrefix(requested, "0x"), txID(5)) {
			logs = append(logs, map[string]any{"address": evmToken, "logIndex": "0x7", "topics": []string{transferTopic, topic(evmFrom), topic(evmTo)}, "data": "0x2"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"blockNumber": "0x64", "transactionHash": requested, "logs": logs}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	c.opts.TRC20AdaptiveEnrichment, c.opts.TRC20LogEnrichmentRatio, c.opts.TRC20EnrichLogMaxWindows = true, .5, 20
	c.opts.LogMaxSpan = 100
	rows := make([]json.RawMessage, 0, 11)
	for i := 1; i <= 10; i++ {
		rows = append(rows, json.RawMessage(fmt.Sprintf(`{"transaction_id":"%s","token_info":{"address":"%s"},"block_timestamp":%d,"from":"%s","to":"%s","value":"1"}`, txID(i), token, int64(1700000000000)+int64(i), from, to)))
		if i == 5 {
			rows = append(rows, json.RawMessage(fmt.Sprintf(`{"transaction_id":"%s","token_info":{"address":"%s"},"block_timestamp":%d,"from":"%s","to":"%s","value":"2"}`, txID(i), token, int64(1700000000000)+int64(i), from, to)))
		}
	}
	got, err := c.EnrichTRC20Identity(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress}, rows)
	if err != nil {
		t.Fatal(err)
	}
	for i, raw := range got {
		row, normalizeErr := NormalizeTRC20(raw)
		wantIndex := uint32(1)
		if row.AmountRaw == "2" {
			wantIndex = 7
		}
		if normalizeErr != nil || row.LogIndex != wantIndex {
			t.Fatalf("row %d log_index=%d amount=%s err=%v", i, row.LogIndex, row.AmountRaw, normalizeErr)
		}
	}
	if receiptCalls.Load() != 2 {
		t.Fatalf("receipt HTTP requests=%d, want one boundary batch + one unmatched transaction", receiptCalls.Load())
	}
}

func TestTRC20ReceiptSingleflightSharesConcurrentFetch(t *testing.T) {
	const txID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(40 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"transactionHash": "0x" + txID}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := c.getTRC20Receipt(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers"}, txID)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("receipt upstream requests = %d, want 1", requests.Load())
	}
}

func TestTRC20LogWindowCacheAndSingleflightShareUpstreamRequests(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(30 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": []any{}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	req := provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := c.fetchTRC20EnrichmentLogs(context.Background(), req, testAddress, 10_000, 14_999)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("concurrent upstream requests=%d want 2 directions", got)
	}
	_, counted, err := c.fetchTRC20EnrichmentLogs(context.Background(), req, testAddress, 10_000, 14_999)
	if err != nil {
		t.Fatal(err)
	}
	if counted != 0 || requests.Load() != 2 {
		t.Fatalf("cache hit counted=%d upstream=%d want both unchanged", counted, requests.Load())
	}
}

func TestTRC20MultiWindowLogsUseOneJSONRPCBatch(t *testing.T) {
	var httpRequests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpRequests.Add(1)
		var calls []struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&calls); err != nil {
			t.Fatal(err)
		}
		responses := make([]map[string]any, 0, len(calls))
		for i := len(calls) - 1; i >= 0; i-- {
			if calls[i].Method != "eth_getLogs" {
				t.Fatalf("method=%s want eth_getLogs", calls[i].Method)
			}
			responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": calls[i].ID, "result": []any{}})
		}
		_ = json.NewEncoder(w).Encode(responses)
	}))
	defer server.Close()
	c := testAnkrClient(server)
	logs, logical, err := c.fetchTRC20EnrichmentLogWindows(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress}, testAddress, []trc20LogWindow{{from: 10_000, to: 14_999}, {from: 15_000, to: 19_999}})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 || logical != 4 || httpRequests.Load() != 2 {
		t.Fatalf("logs=%d logical=%d HTTP=%d want 0/4/2", len(logs), logical, httpRequests.Load())
	}
}

func TestTRC20LogsBatchWidthHonorsCumulativeBlockSpan(t *testing.T) {
	tests := []struct {
		span uint64
		want int
	}{
		{span: 3_000, want: 3},
		{span: 4_000, want: 2},
		{span: 5_000, want: 2},
		{span: 8_000, want: 1},
	}
	for _, tc := range tests {
		if got := trc20LogsBatchSize(tc.span); got != tc.want {
			t.Fatalf("span=%d batch width=%d want %d", tc.span, got, tc.want)
		}
	}
}

func TestTRC20LogCapabilityEpochOnlyCoversSingleEpoch(t *testing.T) {
	epoch, ok := trc20LogCapabilityEpoch(24_100_000, 24_900_000)
	if !ok || epoch != 24 {
		t.Fatalf("single epoch = %d,%v", epoch, ok)
	}
	if _, ok := trc20LogCapabilityEpoch(24_900_000, 25_100_000); ok {
		t.Fatal("cross-epoch range must not inherit a negative capability")
	}
}

func TestTRC20BatchFailureFallsBackWithBoundedParallelism(t *testing.T) {
	var requests, active, maxActive atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		if len(raw) > 0 && raw[0] == '[' {
			var calls []struct {
				ID int `json:"id"`
			}
			_ = json.Unmarshal(raw, &calls)
			responses := make([]map[string]any, 0, len(calls))
			for i, call := range calls {
				if i < 2 {
					responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": call.ID, "error": map[string]any{"code": -32000, "message": "retry singly"}})
				} else {
					responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": []any{}})
				}
			}
			_ = json.NewEncoder(w).Encode(responses)
			return
		}
		cur := active.Add(1)
		for {
			old := maxActive.Load()
			if cur <= old || maxActive.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		active.Add(-1)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": []any{}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	c.opts.Concurrency = 4
	_, logical, err := c.fetchTRC20EnrichmentLogWindows(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", Address: testAddress}, testAddress, []trc20LogWindow{{from: 20_000, to: 24_999}, {from: 25_000, to: 29_999}})
	if err != nil {
		t.Fatal(err)
	}
	if logical != 4 || requests.Load() != 6 || maxActive.Load() < 2 {
		t.Fatalf("logical=%d HTTP=%d max_parallel=%d want 4/6/>=2", logical, requests.Load(), maxActive.Load())
	}
}

func TestAnkrTxDetailUsesNodeDetailAndReceiptOnlyOnDemand(t *testing.T) {
	var paths []string
	var usageTypes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"txID": strings.Repeat("a", 64),
				"raw_data": map[string]any{"contract": []any{map[string]any{
					"parameter": map[string]any{"value": map[string]any{"data": "a9059cbb" + strings.Repeat("0", 64)}},
				}}},
			})
		case "/wallet/gettransactioninfobyid":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": strings.Repeat("a", 64), "log": []any{map[string]any{
				"address": "41" + strings.Repeat("b", 40), "topics": []string{transferTopic}, "data": "00",
			}}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := testAnkrClient(server)
	c.usage = func(row model.UsageRow) { usageTypes = append(usageTypes, row.UsageType) }

	raw, err := c.TxDetail(context.Background(), "tron", strings.Repeat("a", 64), true, true)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Transactions []struct {
			Hash   string `json:"hash"`
			Method struct {
				ID string `json:"id"`
			} `json:"method"`
			Logs []map[string]any `json:"logs"`
		} `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Transactions) != 1 || result.Transactions[0].Method.ID != "0xa9059cbb" || len(result.Transactions[0].Logs) != 1 {
		t.Fatalf("unexpected detail shape: %s", raw)
	}
	if strings.Join(paths, ",") != "/wallet/gettransactionbyid,/wallet/gettransactioninfobyid" {
		t.Fatalf("paths = %v", paths)
	}
	if strings.Join(usageTypes, ",") != "tx_detail,receipt" {
		t.Fatalf("usage types = %v", usageTypes)
	}

	paths = nil
	usageTypes = nil
	if _, err := c.TxDetail(context.Background(), "tron", strings.Repeat("a", 64), true, false); err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/wallet/gettransactionbyid" || strings.Join(usageTypes, ",") != "tx_detail" {
		t.Fatalf("minimal detail paths=%v usage=%v", paths, usageTypes)
	}

	paths = nil
	usageTypes = nil
	receipt, err := c.TxReceipt(context.Background(), "tron", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(receipt), `"id"`) || strings.Join(paths, ",") != "/wallet/gettransactioninfobyid" || strings.Join(usageTypes, ",") != "receipt" {
		t.Fatalf("receipt=%s paths=%v usage=%v", receipt, paths, usageTypes)
	}
}

func TestAnkrTRC20SelfTransferDedupAndExactIndex(t *testing.T) {
	evm, _ := Base58TToEVMHex20(testAddress)
	topic := "0x000000000000000000000000" + evm[2:]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] == "eth_call" {
			params := req["params"].([]any)
			call := params[0].(map[string]any)
			result := "0x" + strings.Repeat("0", 63) + "6"
			if call["data"] != "0x313ce567" {
				result = abiTestString("USDT")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
			return
		}
		if req["method"] != "eth_getLogs" {
			t.Fatalf("unexpected method %v", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": []any{map[string]any{
			"address": "0xa614f803b6fd780986a42c78ec9c7f77e6ded13c", "blockNumber": "0xa", "blockTimestamp": "0x6553f100",
			"transactionHash": "0xabc", "logIndex": "0x0", "removed": false,
			"topics": []string{transferTopic, topic, topic}, "data": "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		}}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	rows, _, err := c.fetchTRC20(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers"}, testAddress, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("self-transfer must dedupe to one row, got %d", len(rows))
	}
	row, err := NormalizeTRC20(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if row.LogIndex != 0 {
		t.Fatalf("provider logIndex=0 must be preserved, got %d", row.LogIndex)
	}
	want, _ := new(big.Int).SetString("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 16)
	if row.AmountRaw != want.String() || row.Provider != "ankr" {
		t.Fatalf("amount/provider mismatch: %s %s", row.AmountRaw, row.Provider)
	}
	if row.TokenSymbol != "USDT" || row.TokenDecimals != 6 {
		t.Fatalf("token metadata mismatch: %s/%d", row.TokenSymbol, row.TokenDecimals)
	}
}

func abiTestString(value string) string {
	return "0x" + strings.Repeat("0", 62) + "20" + strings.Repeat("0", 63) + strconv.FormatInt(int64(len(value)), 16) + hex.EncodeToString([]byte(value)) + strings.Repeat("0", (32-len(value))*2)
}

func TestAnkrBlockParserUsesRelevantContractAndPreservesUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first := map[string]any{"type": "TransferContract", "parameter": map[string]any{"value": map[string]any{"owner_address": "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "to_address": "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "amount": 1}}}
		second := map[string]any{"type": "TransferAssetContract", "parameter": map[string]any{"value": map[string]any{"owner_address": testAddress, "to_address": "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "amount": 9}}}
		tx := map[string]any{"txID": "tx1", "ret": []any{map[string]any{"contractRet": "SUCCESS"}}, "raw_data": map[string]any{"contract": []any{first, second}}}
		block := map[string]any{"block_header": map[string]any{"raw_data": map[string]any{"number": 10, "timestamp": int64(1700000000000)}}, "transactions": []any{tx}}
		_ = json.NewEncoder(w).Encode(map[string]any{"block": []any{block}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	rows, _, err := c.fetchTransactions(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions"}, testAddress, 10, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	row, err := NormalizeTransaction(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if row.From != testAddress || row.ValueRaw != "9" || row.RawType != "TransferAssetContract" || row.Provider != "ankr" {
		t.Fatalf("wrong related contract: %+v", row)
	}
}

func TestAnkrTriggerSmartContractMatchesContractRecipient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contract := map[string]any{"type": "TriggerSmartContract", "parameter": map[string]any{"value": map[string]any{"owner_address": "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "contract_address": testAddress, "data": "a9059cbb"}}}
		block := map[string]any{"block_header": map[string]any{"raw_data": map[string]any{"number": 10, "timestamp": int64(1700000000000)}}, "transactions": []any{map[string]any{"txID": "tx-contract", "raw_data": map[string]any{"contract": []any{contract}}}}}
		_ = json.NewEncoder(w).Encode(map[string]any{"block": []any{block}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	rows, _, err := c.fetchTransactions(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions"}, testAddress, 10, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("contract recipient rows=%d err=%v", len(rows), err)
	}
	row, err := NormalizeTransaction(rows[0])
	if err != nil || row.To != testAddress || row.RawType != "TriggerSmartContract" {
		t.Fatalf("contract recipient was not preserved: row=%+v err=%v", row, err)
	}
}

func TestAnkrInternalRecipientRejectedAndMultipleValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internal := map[string]any{"hash": "ih", "caller_address": "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "transferTo_address": testAddress, "callValueInfo": []any{map[string]any{"callValue": 7}, map[string]any{"callValue": 8, "tokenId": "1002000"}}}
		receipt := map[string]any{"id": "tx2", "blockNumber": 10, "blockTimeStamp": int64(1700000000000), "receipt": map[string]any{"result": "FAILED"}, "internal_transactions": []any{internal}}
		_ = json.NewEncoder(w).Encode([]any{receipt})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	rows, _, err := c.fetchInternal(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "internal_transactions"}, testAddress, 10, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	first, _ := NormalizeInternal(rows[0])
	second, _ := NormalizeInternal(rows[1])
	if !first.Rejected || !second.Rejected || first.CallIndex == second.CallIndex || first.AssetType != "TRX" || second.AssetType != "TRC10:1002000" {
		t.Fatalf("unexpected internal rows: %+v %+v", first, second)
	}
}

func TestAnkrCursorRoundTrip(t *testing.T) {
	want := ankrCursor{Provider: "ankr", Type: "block", NextBlock: 11, EndBlock: 99, Span: 20}
	got, err := decodeAnkrCursor(encodeAnkrCursor(want))
	if err != nil || got != want {
		t.Fatalf("cursor=%+v err=%v", got, err)
	}
	if _, err := decodeAnkrCursor("fingerprint"); err == nil {
		t.Fatal("TronGrid fingerprint must not be accepted as Ankr block cursor")
	}
}

func TestAnkrExplicitRangeGuard(t *testing.T) {
	c := &AnkrClient{opts: AnkrOptions{MaxScanBlocks: 10, MaxTxAutoScan: 10, MaxInternalAutoScan: 10}, timeCache: map[int64]uint64{}}
	_, _, _, err := c.resolveScan(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions"})
	if err == nil {
		t.Fatal("unbounded history must be rejected before scanning")
	}
}

func TestAnkrDatasetSpecificAutoScanLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "0x64"})
	}))
	defer server.Close()
	from, to := uint64(1), uint64(20)
	c := testAnkrClient(server)
	c.opts.MaxTxAutoScan = 10
	c.opts.MaxInternalAutoScan = 15
	if _, _, _, err := c.resolveScan(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions", FromBlock: &from, ToBlock: &to}); err == nil || !strings.Contains(err.Error(), "transactions") {
		t.Fatalf("transactions auto-scan limit not enforced: %v", err)
	}
	if _, _, _, err := c.resolveScan(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "internal_transactions", FromBlock: &from, ToBlock: &to}); err == nil || !strings.Contains(err.Error(), "internal") {
		t.Fatalf("internal auto-scan limit not enforced: %v", err)
	}
}

func TestBlockTimestampRetriesFailedProbe(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"block_header": map[string]any{"raw_data": map[string]any{"timestamp": int64(1700000000000)}}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	c.maxRetries = 0
	got, err := c.blockTimestamp(context.Background(), 10)
	if err != nil || got.UnixMilli() != 1700000000000 || calls.Load() != 2 {
		t.Fatalf("timestamp=%v calls=%d err=%v", got, calls.Load(), err)
	}
}

func TestResolveEndBlockUsesBoundedSearchFromStart(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		block := uint64(payload["num"].(float64))
		_ = json.NewEncoder(w).Encode(map[string]any{"block_header": map[string]any{"raw_data": map[string]any{"timestamp": base.Add(time.Duration(block-100) * 3 * time.Second).UnixMilli()}}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	got, err := c.resolveEndBlockByTimestamp(context.Background(), base, base.Add(10*time.Second), 100, 1_000)
	if err != nil || got != 104 {
		t.Fatalf("end block=%d err=%v", got, err)
	}
	if calls > 10 {
		t.Fatalf("bounded end search used too many calls: %d", calls)
	}
}

func TestAnkrBlockAndReceiptScansAreShared(t *testing.T) {
	var blockCalls atomic.Int32
	var receiptCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		time.Sleep(40 * time.Millisecond)
		if _, ok := payload["startNum"]; ok {
			blockCalls.Add(1)
			contract := map[string]any{"type": "TransferContract", "parameter": map[string]any{"value": map[string]any{"owner_address": testAddress, "to_address": "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "amount": 1}}}
			_ = json.NewEncoder(w).Encode(map[string]any{"block": []any{map[string]any{"block_header": map[string]any{"raw_data": map[string]any{"number": 10, "timestamp": int64(1700000000000)}}, "transactions": []any{map[string]any{"txID": "tx", "raw_data": map[string]any{"contract": []any{contract}}}}}}})
			return
		}
		receiptCalls.Add(1)
		internal := map[string]any{"hash": "ih", "caller_address": testAddress, "transferTo_address": "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "callValueInfo": []any{map[string]any{"callValue": 1}}}
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": "tx", "blockNumber": 10, "blockTimeStamp": int64(1700000000000), "internal_transactions": []any{internal}}})
	}))
	defer server.Close()
	c := testAnkrClient(server)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, _, err := c.fetchTransactions(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "transactions"}, testAddress, 10, 10)
			if err != nil || len(rows) != 1 {
				t.Errorf("shared transaction scan rows=%d err=%v", len(rows), err)
			}
		}()
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, _, err := c.fetchInternal(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "internal_transactions"}, testAddress, 10, 10)
			if err != nil || len(rows) != 1 {
				t.Errorf("shared receipt scan rows=%d err=%v", len(rows), err)
			}
		}()
	}
	wg.Wait()
	if blockCalls.Load() != 1 || receiptCalls.Load() != 1 {
		t.Fatalf("shared scans made block=%d receipt=%d upstream calls", blockCalls.Load(), receiptCalls.Load())
	}
}
