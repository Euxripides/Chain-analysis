package crosschain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
)

func TestAcrossPublicReadAndMultipleDeposits(t *testing.T) {
	hash := testHash
	other := "0x" + strings.Repeat("2", 64)
	calls := 0
	deposit := func(id, tx string) map[string]any {
		return map[string]any{"depositId": id, "status": "filled", "originChainId": 56, "destinationChainId": 137, "depositor": testAddress, "inputToken": testAddress, "inputAmount": "1000000", "depositTxnRef": tx, "recipient": testAddress, "outputToken": testAddress, "outputAmount": "999000", "fillTxnRef": other}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Error("public lookup unexpectedly writes or requires a credential")
		}
		if r.URL.Path == "/api/deposit" {
			json.NewEncoder(w).Encode(map[string]any{"deposit": deposit("1", hash), "pagination": map[string]any{"currentIndex": 0, "maxIndex": 1}})
			return
		}
		if r.URL.Path != "/api/deposits" || r.URL.Query().Get("limit") != "21" {
			t.Error("unbounded or wrong enumeration")
		}
		json.NewEncoder(w).Encode([]any{deposit("1", hash), deposit("2", hash), deposit("3", other)})
	}))
	defer server.Close()
	s := New(Config{Bases: map[string]string{"across": server.URL}})
	result, err := s.Query(context.Background(), QueryRequest{Bridge: "across", Chain: "bsc", TxHash: hash})
	if err != nil || len(result.Transfers) != 2 || !result.Truncated || calls != 2 {
		t.Fatalf("batch=%+v error=%v calls=%d", result, err, calls)
	}
	for _, transfer := range result.Transfers {
		if transfer.Source[0].TxHash != hash || transfer.OrderID == "3" || transfer.OnChainVerified {
			t.Fatal("unrelated deposit or unverified money accepted", transfer)
		}
	}
	for _, cap := range s.Capabilities() {
		if cap.Bridge == "across" && !cap.Configured {
			t.Fatal("public lookup still blocked by missing optional key")
		}
	}
}

func TestAutoContinuesAfterMessageOnlyToFundingProtocol(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/messages/") {
			calls = append(calls, "layerzero")
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"guid": "guid", "pathway": map[string]any{"sender": map[string]any{"chain": "bsc"}, "receiver": map[string]any{"chain": "ethereum"}}, "source": map[string]any{"tx": map[string]any{"txHash": testHash}}, "destination": map[string]any{"tx": map[string]any{"txHash": testHash}}, "status": map[string]any{"name": "DELIVERED"}}}})
			return
		}
		calls = append(calls, "lifi")
		json.NewEncoder(w).Encode(map[string]any{"status": "DONE", "fromAddress": testAddress, "toAddress": testAddress, "sending": map[string]any{"txHash": testHash, "chainId": 56, "amount": "1000000", "token": map[string]any{"decimals": 6, "address": testAddress}}, "receiving": map[string]any{"txHash": testHash, "chainId": 1, "amount": "999000", "token": map[string]any{"decimals": 6, "address": testAddress}}})
	}))
	defer server.Close()
	service := New(Config{Bases: map[string]string{"layerzero": server.URL, "lifi": server.URL}})
	out, err := service.Query(context.Background(), QueryRequest{Bridge: "auto", Chain: "bsc", TxHash: testHash, Hints: []ProtocolHint{{Bridge: "layerzero"}}})
	if err != nil || len(out.Transfers) != 2 || len(calls) != 2 || calls[0] != "layerzero" || calls[1] != "lifi" || out.Transfers[1].Source[0].AmountRaw != "1000000" {
		t.Fatal("message-only result hid the available funding record", out, err, calls)
	}
}

func TestWormholeReverseIdentityAndNormalizedAmount(t *testing.T) {
	emitter := strings.Repeat("0", 24) + strings.TrimPrefix(testAddress, "0x")
	item := map[string]any{"id": "4/" + emitter + "/9", "emitterChain": 4, "emitterAddress": map[string]any{"hex": emitter, "native": testAddress}, "sequence": 9, "sourceChain": map[string]any{"chainId": 4, "from": testAddress, "transaction": map[string]any{"txHash": testHash}}, "targetChain": map[string]any{"chainId": 5, "status": "completed", "transaction": map[string]any{"txHash": testHash}}, "content": map[string]any{"payload": map[string]any{"amount": "123456789", "tokenChain": 4, "tokenAddress": "0x" + emitter, "toAddress": "0x" + emitter}, "standarizedProperties": map[string]any{"tokenChain": 4, "tokenAddress": testAddress, "toAddress": testAddress}}}
	s := providerFixture(t, "wormhole", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("txHash") != testHash {
			t.Error("no exact query hash")
		}
		json.NewEncoder(w).Encode(map[string]any{"operations": []any{item}})
	})
	result, err := s.Query(context.Background(), QueryRequest{Bridge: "wormhole", Chain: "polygon", TxHash: testHash})
	if err != nil || len(result.Transfers) != 1 || result.Transfers[0].Source[0].AmountRaw != "" || result.Transfers[0].Source[0].NormalizedAmountRaw != "123456789" {
		t.Fatal("normalized amount was confused with raw token units", result, err)
	}
	result, _ = s.Query(context.Background(), QueryRequest{Bridge: "wormhole", Chain: "base", TxHash: testHash})
	if len(result.Transfers) != 0 {
		t.Fatal("wrong chain paired")
	}
	item["sequence"] = "18446744073709551616"
	result, _ = s.Query(context.Background(), QueryRequest{Bridge: "wormhole", Chain: "polygon", TxHash: testHash})
	if len(result.Transfers) != 0 {
		t.Fatal("invalid emitter sequence accepted")
	}
}

func TestCCTPAttestationDoesNotFabricateMintOrSource(t *testing.T) {
	destination := "0x" + strings.Repeat("2", 64)
	nonce := "0x" + strings.Repeat("0", 63) + "9"
	payload := map[string]any{"sourceTxHash": testHash, "messages": []any{map[string]any{"eventNonce": nonce, "cctpVersion": 2, "status": "complete", "decodedMessage": map[string]any{"sourceDomain": 0, "destinationDomain": 6, "decodedMessageBody": map[string]any{"amount": "1000000", "feeExecuted": "1000", "burnToken": CCTPUSDC["ethereum"], "messageSender": testAddress, "mintRecipient": "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(testAddress, "0x")}}}}}
	s := providerFixture(t, "cctp", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/messages/0" {
			t.Error("domain mismatch")
		}
		json.NewEncoder(w).Encode(payload)
	})
	result, _ := s.Query(context.Background(), QueryRequest{Bridge: "cctp", Chain: "ethereum", TxHash: testHash})
	if len(result.Transfers) != 1 || result.Transfers[0].Status != "pending" || result.Transfers[0].Destination[0].TxHash != "" || result.Transfers[0].Destination[0].AmountRaw != "999000" {
		t.Fatal("attestation was reported as completed funds", result)
	}
	hint := ProtocolHint{Bridge: "cctp", Kind: "received", SourceDomain: "0", Identifier: nonce, Contract: CCTPTransmitterV2}
	result, _ = s.Query(context.Background(), QueryRequest{Bridge: "cctp", Chain: "base", TxHash: destination, Hints: []ProtocolHint{hint}})
	if len(result.Transfers) != 1 || result.Transfers[0].Destination[0].TxHash != destination || result.Transfers[0].Status != "completed" {
		t.Fatal("receipt nonce did not link reverse transaction", result)
	}
	delete(payload, "sourceTxHash")
	result, _ = s.Query(context.Background(), QueryRequest{Bridge: "cctp", Chain: "ethereum", TxHash: testHash})
	if len(result.Transfers) > 0 {
		t.Fatal("missing source identity synthesized from request")
	}
}

func TestProtocolHintsRejectMalformedAndRemovedEvents(t *testing.T) {
	logs := []chainread.Log{{Address: chain.Address(testAddress), Topics: []string{EventTopic("TransferRedeemed(uint16,bytes32,uint64)"), "0x01", "bad", "0x02"}}, {Address: chain.Address(testAddress), Topics: []string{EventTopic("MessageSent(bytes)")}, Data: "0x" + strings.Repeat("f", 128)}, {Address: chain.Address(testAddress), Removed: true, Topics: []string{EventTopic("OFTReceived(bytes32,uint32,address,uint256)"), testHash, testHash}, Data: "0x" + strings.Repeat("0", 128)}}
	if hints := DetectProtocolHints("bsc", logs); len(hints) != 0 {
		t.Fatal("malformed events yielded protocol identifiers", hints)
	}
}
