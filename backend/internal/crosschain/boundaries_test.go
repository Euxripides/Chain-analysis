package crosschain

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestQueryIdentifierConstraintsBeforeIO(t *testing.T) {
	for _, r := range []QueryRequest{{Bridge: "near_intents", OrderID: "x"}, {Bridge: "relay", TransferID: strings.TrimPrefix(testHash, "0x")}, {Bridge: "lifi", Chain: "bsc", TxHash: testHash, DepositAddress: "x"}, {Bridge: "across", OrderID: "x"}, {Bridge: "near_intents", Chain: "bsc", DepositAddress: "x"}, {Bridge: "relay", OrderID: "x", DepositMemo: "memo"}} {
		if r.Validate() == nil {
			t.Fatalf("invalid selector accepted %+v", r)
		}
	}
	if h, e := Hash("tron", strings.TrimPrefix(testHash, "0x")); e != nil || h != testHash {
		t.Fatalf("TRON storage identity %s %v", h, e)
	}
	for _, value := range []string{".1", "1."} {
		if _, e := Amount(value); e == nil {
			t.Fatal("noncanonical decimal accepted", value)
		}
	}
}

func TestCandidateSlotsAreBoundedAndCancelable(t *testing.T) {
	s := New(Config{})
	a, e := s.AcquireCandidate(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.AcquireCandidate(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.AcquireCandidate(ctx); e == nil {
		t.Fatal("third query exceeded the concurrency budget")
	}
	a()
	b()
	if _, e = s.AcquireCandidate(ctx); e == nil {
		t.Fatal("already canceled query acquired a free slot")
	}
	c, e := s.AcquireCandidate(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	c()
}

func TestResponseTimeoutAndRelayContinuation(t *testing.T) {
	s := providerFixture(t, "lifi", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Write([]byte(`{"status":"NOT_FOUND"}`))
	})
	s.client.Timeout = time.Millisecond
	out, e := s.Query(context.Background(), QueryRequest{Bridge: "lifi", Chain: "bsc", TxHash: testHash})
	if e != nil || out.Attempts[0].Status != "timeout" || out.SearchedComplete {
		t.Fatal(out, e)
	}
	relay := providerFixture(t, "relay", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"continuation": "next", "requests": []any{map[string]any{"status": "success", "protocol": map[string]any{"orderId": "order", "deposit": map[string]any{"origin": map[string]any{"chainId": 56, "transactionId": testHash}}}}}})
	})
	out, e = relay.Query(context.Background(), QueryRequest{Bridge: "relay", Chain: "bsc", TxHash: testHash})
	if e != nil || len(out.Transfers) != 1 || !out.Truncated || out.SearchedComplete || !out.Transfers[0].Evidence.PageTruncated {
		t.Fatal(out, e)
	}
}

func TestNearRefundUsesOriginChainAndKeepsMultiLegTotals(t *testing.T) {
	s := providerFixture(t, "near_intents", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/tokens" {
			w.Write([]byte(`[{"assetId":"source","blockchain":"bsc","symbol":"BNB","decimals":18},{"assetId":"target","blockchain":"eth","symbol":"ETH","decimals":18}]`))
			return
		}
		json.NewEncoder(w).Encode([]any{map[string]any{"depositAddress": "deposit", "depositAddressAndMemo": "deposit", "originAsset": "source", "destinationAsset": "target", "status": "REFUNDED", "refundType": "ORIGIN_CHAIN", "refundTo": testAddress, "originChainTxHashes": []string{testHash, "0x" + strings.Repeat("2", 64)}, "destinationChainTxHashes": []string{"0x" + strings.Repeat("3", 64)}, "amountIn": "100", "amountInFormatted": "0.0000000000000001"}})
	})
	out, e := s.Query(context.Background(), QueryRequest{Bridge: "near_intents", DepositAddress: "deposit"})
	if e != nil || len(out.Transfers) != 1 {
		t.Fatal(out, e)
	}
	tx := out.Transfers[0]
	if len(tx.Destination) != 0 || tx.Refund[0].Chain != "bsc" || tx.Refund[0].Amount != "" || tx.Source[0].AmountsScope != "order_total_not_per_transaction" {
		t.Fatal(tx)
	}
}
