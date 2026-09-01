package ankr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveAddressTypesUsesRuntimeBytecode(t *testing.T) {
	contract := "0x55d398326f99059ff775485246999027b3197955"
	account := "0x9b77e3b4f85c7cea7da451f454895edbd10b80dd"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		if len(batch) != 2 || batch[0].Method != "eth_getCode" {
			t.Fatalf("unexpected batch: %#v", batch)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"jsonrpc": "2.0", "id": batch[0].ID, "result": "0x6080604052"},
			{"jsonrpc": "2.0", "id": batch[1].ID, "result": "0x"},
		})
	}))
	defer server.Close()

	client := New(Options{Endpoint: server.URL + "/multichain", RequestsPerMinute: 100}, nil)
	got, err := client.ResolveAddressTypes(context.Background(), "bsc", []string{contract, account, contract})
	if err != nil {
		t.Fatal(err)
	}
	if !got[contract] || got[account] {
		t.Fatalf("unexpected classification: %#v", got)
	}
}
