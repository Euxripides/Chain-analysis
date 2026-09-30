package drpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func TestRPCHistoryPagesAndNativeMovementsThroughManagedDRPC(t *testing.T) {
	const sig = "SolanaSignatureCaseSensitive111111111111111111111111111111111111111111"
	var mu sync.Mutex
	methods := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		mu.Lock()
		methods[call.Method]++
		mu.Unlock()
		switch call.Method {
		case MethodSignatures:
			var options map[string]any
			_ = json.Unmarshal(call.Params[1], &options)
			if options["before"] == sig {
				writeRPCResult(t, w, json.RawMessage(`[]`))
				return
			}
			writeRPCResult(t, w, mustJSON(t, []map[string]any{{"signature": sig, "slot": 420305276, "blockTime": 1789875226, "err": nil}}))
		case MethodTransaction:
			var options map[string]any
			_ = json.Unmarshal(call.Params[1], &options)
			if options["maxSupportedTransactionVersion"] != float64(1) {
				t.Errorf("managed dRPC request cannot read v1: %#v", options)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeRPCResult(t, w, json.RawMessage(`{"slot":420305276,"version":1,"blockTime":1789875226,"transaction":{"signatures":["`+sig+`"],"message":{"accountKeys":[{"pubkey":"`+testAddress+`","signer":true},{"pubkey":"Bob"}],"instructions":[{"programId":"11111111111111111111111111111111","parsed":{"type":"transfer","info":{"source":"`+testAddress+`","destination":"Bob","lamports":"1000000001"}}}]}},"meta":{"err":null,"fee":5000,"preBalances":[2000000000,0],"postBalances":[999994999,1000000001],"innerInstructions":[],"logMessages":[]}}`))
		default:
			t.Errorf("unexpected RPC method %s", call.Method)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.RPCHistory = true; o.HistoryPageSize = 2 })
	to := uint64(420305276)
	for _, dataset := range []string{DatasetTransactions, DatasetInternalTransactions} {
		request := provider.FetchRequest{Chain: ChainSolana, Dataset: dataset, Address: testAddress, ToBlock: &to, PageSize: 2}
		first, err := env.client.Fetch(context.Background(), request)
		if err != nil {
			t.Fatalf("%s first page: %v", dataset, err)
		}
		if len(first.Rows) != 1 || first.NextPageToken != "" || first.HasMore {
			t.Fatalf("%s page contract: %+v", dataset, first)
		}
		row := rowObject(t, first.Rows[0])
		if dataset == DatasetTransactions && (row["value"] != "" || row["to"] != "") {
			t.Fatalf("transaction summary invented one native destination/value: %v", row)
		}
		if dataset == DatasetInternalTransactions && (row["from"] != testAddress || row["to"] != "Bob" || row["valueRaw"] != "1000000001") {
			t.Fatalf("native movement not decoded: %v", row)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if methods[MethodSignatures] < 4 || methods[MethodTransaction] != 2 {
		t.Fatalf("unexpected RPC usage: %v", methods)
	}
	if len(env.usage.all()) < 6 {
		t.Fatalf("each RPC call must be recorded in usage ledger")
	}
}

func TestRPCHistoryFailsClosedOnDRPCHTTP500(t *testing.T) {
	const sig = "SolanaSignatureCaseSensitive111111111111111111111111111111111111111111"
	var mu sync.Mutex
	methods := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		mu.Lock()
		methods[call.Method]++
		mu.Unlock()
		switch call.Method {
		case MethodSignatures:
			writeRPCResult(t, w, mustJSON(t, []map[string]any{{"signature": sig, "slot": 420305276, "blockTime": 1789875226, "err": nil}}))
		case MethodTransaction:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected RPC method %s", call.Method)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.RPCHistory = true; o.HistoryPageSize = 2 })
	to := uint64(420305276)
	result, err := env.client.Fetch(context.Background(), provider.FetchRequest{Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress, ToBlock: &to, PageSize: 2})
	if err == nil || len(result.Rows) != 0 {
		t.Fatalf("HTTP 500 must not write a partial transaction page: rows=%d err=%v", len(result.Rows), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if methods[MethodTransaction] != 1 || methods[MethodSignatures] < 1 || len(methods) != 2 {
		t.Fatalf("failed page must stay within the configured dRPC methods: %v", methods)
	}
}

func TestRPCHistoryDoesNotAdvertiseUnverifiedNFTClassification(t *testing.T) {
	client := New(Options{RPCHistory: true}, nil)
	for _, dataset := range client.Capabilities().Datasets {
		if dataset == DatasetNFTTransfers {
			t.Fatal("RPC history must not advertise unverified NFT transfers")
		}
	}
	if _, err := client.Fetch(context.Background(), provider.FetchRequest{Chain: ChainSolana, Dataset: DatasetNFTTransfers, Address: testAddress}); err == nil {
		t.Fatal("RPC history must reject NFT downloads before touching Data API")
	}
}
