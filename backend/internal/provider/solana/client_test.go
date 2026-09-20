package solana

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// allSignatures 按 slot 从新到旧排列，含两条指定区间内、一条更新、一条更旧。
var allSignatures = []map[string]any{
	{"signature": "SIG-NEW", "slot": 120, "blockTime": 1700000200, "err": nil},
	{"signature": "SIG-IN-2", "slot": 100, "blockTime": 1700000000, "err": nil},
	{"signature": "SIG-IN-1", "slot": 99, "blockTime": 1699999900, "err": nil},
	{"signature": "SIG-OLD", "slot": 50, "blockTime": 1699990000, "err": nil},
}

// fakeRPC 模拟标准 Solana JSON-RPC：按 method 返回预定结果，并记录调用次数。
func fakeRPC(t *testing.T, calls map[string]int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if calls != nil {
			calls[req.Method]++
		}
		w.Header().Set("Content-Type", "application/json")
		var result string
		switch req.Method {
		case MethodSignatures:
			// 真实 RPC 会按 limit 截断：fixture 必须同样截断，否则分页语义测不出来。
			limit := len(allSignatures)
			if len(req.Params) > 1 {
				if options, ok := req.Params[1].(map[string]any); ok {
					if v, ok := options["limit"].(float64); ok && int(v) < limit {
						limit = int(v)
					}
				}
			}
			payload, _ := json.Marshal(allSignatures[:limit])
			result = string(payload)
		case MethodTransaction:
			signature, _ := req.Params[0].(string)
			slot := 99
			if signature == "SIG-IN-2" {
				slot = 100
			}
			result = fmt.Sprintf(`{
				"slot": %d,
				"blockTime": 1699999900,
				"transaction": {"signatures":["%s"],"message":{"recentBlockhash":"BLOCKHASH","accountKeys":[{"pubkey":"FromPubkey1111111111111111111111111111111"},{"pubkey":"ToPubkey111111111111111111111111111111111"}]}},
				"meta": {"err": null, "fee": 5000, "preBalances": [1000000, 0], "postBalances": [900000, 95000]}
			}`, slot, signature)
		default:
			result = `{"solana-core":"test"}`
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + result + `}`))
	}))
}

func newTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	client := New(Options{Endpoint: endpoint, MaxPageSize: 3, Concurrency: 2}, nil)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	return client
}

func TestFetchTransactionsMapsToAnkrShapedRows(t *testing.T) {
	calls := map[string]int{}
	server := fakeRPC(t, calls)
	defer server.Close()
	client := newTestClient(t, server.URL)

	from, to := uint64(99), uint64(100)
	res, err := client.Fetch(context.Background(), provider.FetchRequest{
		Chain: "solana", Dataset: "transactions", Address: "SomeAddress", PageSize: 3,
		FromBlock: &from, ToBlock: &to,
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("got %d rows, want 2 (slot 120 must be filtered out)", len(res.Rows))
	}
	if calls[MethodSignatures] != 1 || calls[MethodTransaction] != 2 {
		t.Fatalf("unexpected call counts: %v", calls)
	}
	// 页码：整页命中且未越过 from → 允许继续向更旧翻页，token 是最旧一条的签名。
	if !res.HasMore || res.NextPageToken != "SIG-IN-1" {
		t.Fatalf("unexpected pagination: hasMore=%v token=%q", res.HasMore, res.NextPageToken)
	}
	// 字节口径必须真实：shard 的 downloaded_bytes 直接来自 DecodedBytes。
	if res.DecodedBytes <= 0 {
		t.Fatalf("decoded bytes must be accounted, got %d", res.DecodedBytes)
	}
	if res.WireBytes != 0 {
		t.Fatalf("wire bytes are not measured and must stay 0, got %d", res.WireBytes)
	}

	var row struct {
		BlockNumber string `json:"blockNumber"`
		Hash        string `json:"hash"`
		From        string `json:"from"`
		To          string `json:"to"`
		Value       string `json:"value"`
		Status      string `json:"status"`
		Type        string `json:"type"`
	}
	if err := json.Unmarshal(res.Rows[0], &row); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	if row.BlockNumber != "100" || row.Hash != "SIG-IN-2" {
		t.Fatalf("unexpected row identity: %+v", row)
	}
	if row.From == "" || row.To != "ToPubkey111111111111111111111111111111111" {
		t.Fatalf("unexpected addresses: %+v", row)
	}
	if row.Value != "95000" {
		t.Fatalf("value = %q, want 95000 (net outflow minus fee)", row.Value)
	}
	if row.Status != "0x1" || row.Type != "solana" {
		t.Fatalf("unexpected status/type: %+v", row)
	}
}

func TestFetchRejectsUnsupportedInputs(t *testing.T) {
	server := fakeRPC(t, nil)
	defer server.Close()
	client := newTestClient(t, server.URL)

	cases := []provider.FetchRequest{
		{Chain: "bsc", Dataset: "transactions"},
		{Chain: "solana", Dataset: "nft_transfers"},
		{Chain: "solana", Dataset: "balances"},
	}
	for _, req := range cases {
		if _, err := client.Fetch(context.Background(), req); err == nil {
			t.Fatalf("expected %+v to be rejected", req)
		}
	}
	now := time.Now().UTC()
	if _, err := client.Fetch(context.Background(), provider.FetchRequest{
		Chain: "solana", Dataset: "transactions", FromTime: &now,
	}); err == nil {
		t.Fatal("expected time range to be rejected")
	}
}

func TestCapabilitiesAdvertiseImplementedDatasetsOnly(t *testing.T) {
	server := fakeRPC(t, nil)
	defer server.Close()
	client := newTestClient(t, server.URL)
	caps := client.Capabilities()
	want := map[string]bool{"transactions": true, "token_transfers": true, "internal_transactions": true}
	if len(caps.Datasets) != len(want) {
		t.Fatalf("unexpected datasets: %v", caps.Datasets)
	}
	for _, dataset := range caps.Datasets {
		if !want[dataset] {
			t.Fatalf("dataset %s is advertised but not implemented", dataset)
		}
	}
	// 未实现的数据集绝不能被声明（ADR-023 D9）。
	for _, dataset := range []string{"nft_transfers", "logs", "balances"} {
		for _, advertised := range caps.Datasets {
			if advertised == dataset {
				t.Fatalf("dataset %s must not be advertised", dataset)
			}
		}
	}
	if caps.Name != "solana" {
		t.Fatalf("unexpected name: %s", caps.Name)
	}
}
