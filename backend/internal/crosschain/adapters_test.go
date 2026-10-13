package crosschain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func providerFixture(t *testing.T, protocol string, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(Config{NearJWT: "private-near-key", RelayKey: "private-relay-key", AcrossKey: "private-across-key", Bases: map[string]string{protocol: server.URL, "near_tokens": server.URL}})
}

func TestLifiReverseQueryRejectsWrongHashAndChain(t *testing.T) {
	sending := "3QvRpJyWnMomnxPTApBeMHdZvb5BAeWd9Bu8A4rDBQnJ8hMvbX49GUrwFJuBVNQmhH7u4EopmS3MJQSzf34ouma5"
	body := map[string]any{"status": "DONE", "tool": "near", "fromAddress": "7MPS6zxmdf4DQFGm95vr9XGBQvdxRhrk36RznYQpttDS", "toAddress": testAddress, "sending": map[string]any{"txHash": sending, "chainId": 1151111081099710, "amount": "5000000", "token": map[string]any{"decimals": 6, "address": "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", "symbol": "USDT"}}, "receiving": map[string]any{"txHash": testHash, "chainId": 56, "amount": "7631125135013813", "token": map[string]any{"decimals": 18, "address": "0x0000000000000000000000000000000000000000", "symbol": "BNB"}}}
	s := providerFixture(t, "lifi", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("txHash") == "" {
			t.Error("invalid lookup")
		}
		json.NewEncoder(w).Encode(body)
	})
	for _, request := range []QueryRequest{{Bridge: "lifi", Chain: "bsc", TxHash: testHash}, {Bridge: "lifi", Chain: "solana", TxHash: sending}} {
		out, e := s.Query(context.Background(), request)
		if e != nil || len(out.Transfers) != 1 || out.Transfers[0].Destination[0].Amount != "0.007631125135013813" || out.Transfers[0].Source[0].Amount != "5" || out.Transfers[0].OnChainVerified || len(out.Transfers[0].Evidence.SHA256) != 64 {
			t.Fatalf("pairing/precision failed %+v %v", out, e)
		}
	}
	out, _ := s.Query(context.Background(), QueryRequest{Bridge: "lifi", Chain: "ethereum", TxHash: testHash})
	if len(out.Transfers) != 0 || out.Attempts[0].Status != "unrelated_response" {
		t.Fatalf("wrong chain accepted %+v", out)
	}
	out, _ = s.Query(context.Background(), QueryRequest{Bridge: "lifi", Chain: "bsc", TxHash: "0x" + strings.Repeat("2", 64)})
	if len(out.Transfers) != 0 {
		t.Fatal("unrelated response paired")
	}
}

func TestUpstreamStatesAndSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		code         int
		body, status string
	}{{200, `{"status":"NOT_FOUND"}`, "not_found"}, {401, `private-key`, "unauthorized"}, {429, `private-key`, "rate_limited"}, {500, `private-key`, "upstream_error"}, {200, `not-json`, "invalid_response"}, {200, `{} {}`, "invalid_response"}, {200, strings.Repeat("x", (2<<20)+1), "invalid_response"}} {
		t.Run(tc.status, func(t *testing.T) {
			s := providerFixture(t, "lifi", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); w.Write([]byte(tc.body)) })
			out, e := s.Query(context.Background(), QueryRequest{Bridge: "lifi", Chain: "bsc", TxHash: testHash})
			encoded, _ := json.Marshal(out)
			if e != nil || len(out.Transfers) != 0 || out.Attempts[0].Status != tc.status || strings.Contains(string(encoded), "private-key") {
				t.Fatalf("wrong state/leak %+v %v", out, e)
			}
		})
	}
	s := New(Config{})
	out, _ := s.Query(context.Background(), QueryRequest{Bridge: "near_intents", Chain: "bsc", TxHash: testHash})
	if out.Attempts[0].Status != "not_configured" || out.SearchedComplete {
		t.Fatal(out)
	}
}

func TestBridgersOnlyReadsExistingOrderAndSeparatesRefund(t *testing.T) {
	s := providerFixture(t, "bridgers", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/exchangeRecord/getTransDataById" || r.Method != "POST" {
			t.Error("write endpoint called")
		}
		json.NewEncoder(w).Encode(map[string]any{"resCode": 100, "data": map[string]any{"orderId": "order-1", "status": "refund_complete", "fromChain": "BSC", "toChain": "ETH", "hash": testHash, "refundHash": "0x" + strings.Repeat("2", 64), "refundCoinAmt": "99", "fromTokenAmount": "100", "toTokenAmount": "98"}})
	})
	out, e := s.Query(context.Background(), QueryRequest{Bridge: "bridgers", OrderID: "order-1"})
	if e != nil || len(out.Transfers) != 1 || out.Transfers[0].Status != "refunded" || len(out.Transfers[0].Destination) != 0 || len(out.Transfers[0].Refund) != 1 {
		t.Fatalf("refund mixed with delivery %+v %v", out, e)
	}
	if _, e = s.Query(context.Background(), QueryRequest{Bridge: "bridgers", Chain: "bsc", TxHash: testHash}); e == nil {
		t.Fatal("orderless BRIDGERS accepted")
	}
}

func TestNearExactDepositMemoAndMetadata(t *testing.T) {
	s := providerFixture(t, "near_intents", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/tokens" {
			w.Write([]byte(`[{"assetId":"asset-a","blockchain":"bsc","address":"native","decimals":18}]`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer private-near-key" {
			t.Error("missing auth")
		}
		json.NewEncoder(w).Encode([]any{map[string]any{"depositAddress": "deposit-a", "depositMemo": "memo-a", "depositAddressAndMemo": "deposit-a_memo-a", "originAsset": "asset-a", "destinationAsset": "asset-a", "status": "SUCCESS", "originChainTxHashes": []string{testHash}, "destinationChainTxHashes": []string{testHash}, "amountIn": "1", "amountInFormatted": "0.000000000000000001", "amountOut": "1", "amountOutFormatted": "0.000000000000000001"}})
	})
	out, e := s.Query(context.Background(), QueryRequest{Bridge: "near_intents", DepositAddress: "deposit-a", DepositMemo: "memo-a"})
	if e != nil || len(out.Transfers) != 1 || out.Transfers[0].Source[0].Chain != "bsc" {
		t.Fatalf("deposit lookup failed %+v %v", out, e)
	}
	out, _ = s.Query(context.Background(), QueryRequest{Bridge: "near_intents", DepositAddress: "deposit-a", DepositMemo: "wrong"})
	if out.Attempts[0].Status != "rate_limited" {
		t.Fatal("NEAR rate budget not enforced")
	}
}

func TestLayerzeroAndCbridgeReadAssociation(t *testing.T) {
	lz := providerFixture(t, "layerzero", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"guid": "guid", "pathway": map[string]any{"sender": map[string]any{"chain": "bsc"}, "receiver": map[string]any{"chain": "ethereum"}}, "source": map[string]any{"tx": map[string]any{"txHash": testHash}}, "destination": map[string]any{"tx": map[string]any{"txHash": testHash}}, "status": map[string]any{"name": "DELIVERED"}}}})
	})
	out, e := lz.Query(context.Background(), QueryRequest{Bridge: "layerzero", Chain: "bsc", TxHash: testHash})
	if e != nil || len(out.Transfers) != 1 || out.Transfers[0].Source[0].AmountsScope != "message_only" || out.Transfers[0].OnChainVerified {
		t.Fatal(out, e)
	}
	cb := providerFixture(t, "cbridge", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"err": nil, "status": 5, "src_block_tx_link": "https://bscscan.com/tx/" + testHash, "dst_block_tx_link": "https://etherscan.io/tx/" + testHash})
	})
	out, e = cb.Query(context.Background(), QueryRequest{Bridge: "cbridge", TransferID: strings.TrimPrefix(testHash, "0x")})
	if e != nil || len(out.Transfers) != 1 || out.Transfers[0].Destination[0].Chain != "ethereum" {
		t.Fatal(out, e)
	}
}

func TestAcrossAndRelayRequestsV3(t *testing.T) {
	ac := providerFixture(t, "across", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("depositTxnRef") != testHash || r.Header.Get("Authorization") == "" {
			t.Error("Across lookup/auth")
		}
		json.NewEncoder(w).Encode(map[string]any{"deposit": map[string]any{"depositId": "deposit-1", "originChainId": 56, "destinationChainId": 1, "depositTxnRef": testHash, "fillTxnRef": testHash, "status": "filled", "inputAmount": "100", "outputAmount": "99"}})
	})
	out, e := ac.Query(context.Background(), QueryRequest{Bridge: "across", Chain: "bsc", TxHash: testHash})
	if e != nil || len(out.Transfers) != 1 || out.Transfers[0].Status != "completed" {
		t.Fatal(out, e)
	}
	relay := providerFixture(t, "relay", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/requests/v3" || r.Header.Get("x-api-key") == "" {
			t.Error("obsolete/unauthenticated Relay API")
		}
		json.NewEncoder(w).Encode(map[string]any{"requests": []any{map[string]any{"id": "request-a", "status": "success", "protocol": map[string]any{"orderId": "order-a", "deposit": map[string]any{"origin": map[string]any{"chainId": 56, "transactionId": testHash}}, "settlement": map[string]any{"destination": map[string]any{"fills": []any{map[string]any{"chainId": 1, "transactionId": testHash}}}}}}}})
	})
	out, e = relay.Query(context.Background(), QueryRequest{Bridge: "relay", Chain: "ethereum", TxHash: testHash})
	if e != nil || len(out.Transfers) != 1 || out.Transfers[0].OrderID != "order-a" {
		t.Fatal(out, e)
	}
}
