package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/secret"
)

// This opt-in release probe uses the server's existing managed credential.
// It never prints or persists the credential or the constructed RPC URL.
func TestLiveDRPCSolanaSourceRead(t *testing.T) {
	if os.Getenv("DRPC_LIVE_TEST") != "1" {
		t.Skip("set DRPC_LIVE_TEST=1 for release probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ref := os.Getenv("DRPC_RUNTIME_KEY_REF")
	if ref == "" {
		ref = "store://drpc/runtime_api_key"
	}
	manager := secret.NewManager(secret.ManagerOptions{FilePath: os.Getenv("PROVIDER_SECRETS_FILE")})
	key, err := manager.Resolve(ctx, ref)
	if err != nil || key == "" {
		t.Fatalf("dRPC credential reference is not resolvable: %v", err)
	}
	t.Setenv("DRPC_ENABLED", "true")
	t.Setenv("SOLANA_PROVIDER", "drpc")
	t.Setenv("DRPC_API_KEY", "")
	t.Setenv("DRPC_RUNTIME_KEY_REF", ref)
	t.Setenv("DRPC_SOLANA_RPC_ENDPOINT", "")
	t.Setenv("DRPC_SOLANA_ARCHIVE_RPC_ENDPOINT", "")
	t.Setenv("DRPC_BASE_URL", "https://lb.drpc.live/solana")

	record, err := solanaTransaction(ctx, sampleSolanaSignature)
	if err != nil {
		t.Fatalf("dRPC getTransaction could not read the supplied signature: %v", err)
	}
	if record["source_record_status"] != "readable" || record["rpc_source"] != "drpc_solana_rpc" {
		t.Fatalf("unexpected source record status: %v", record["source_record_status"])
	}
	t.Logf("getTransaction verified: slot=%v", record["slot"])

	tx, ok := record["transaction"].(map[string]any)
	if !ok {
		t.Fatal("transaction object missing")
	}
	message, ok := tx["message"].(map[string]any)
	if !ok {
		t.Fatal("transaction message missing")
	}
	accounts, ok := message["accountKeys"].([]any)
	if !ok || len(accounts) == 0 {
		t.Fatal("transaction account keys missing")
	}
	first, ok := accounts[0].(map[string]any)
	if !ok {
		t.Fatal("parsed account key missing")
	}
	address, ok := first["pubkey"].(string)
	if !ok || !validSolanaBase58(address, 32) {
		t.Fatal("first account key is invalid")
	}
	page, source, err := solanaRPCCall(ctx, "getSignaturesForAddress", []any{address, map[string]any{"limit": 1, "commitment": "finalized"}})
	if err != nil {
		t.Fatalf("dRPC getSignaturesForAddress unavailable: %v", err)
	}
	var signatures []map[string]any
	if err := json.Unmarshal(page, &signatures); err != nil || len(signatures) == 0 || source != "drpc_solana_rpc" {
		t.Fatalf("dRPC signature page unreadable: %v", err)
	}
	t.Logf("getSignaturesForAddress verified: entries=%d", len(signatures))
}

// The fixture is a finalized v1 transaction that the same managed dRPC
// endpoint rejects when maxSupportedTransactionVersion is left at zero.
func TestLiveDRPCSolanaVersionOneSourceRead(t *testing.T) {
	if os.Getenv("DRPC_LIVE_TEST") != "1" {
		t.Skip("set DRPC_LIVE_TEST=1 for release probe")
	}
	const signature = "Ho9cGwyniv7Ea8EUr8oDfu2nDvqruVAGcbscwwHbJ3LdxvSmmH8o8eGQvFNFBTxN3Y3g1357L6sLH3Nqd9ViZLE"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	record, err := solanaTransaction(ctx, signature)
	if err != nil {
		t.Fatalf("v1 transaction must be readable through the Action path: %v", err)
	}
	if record["version"] != json.Number("1") || record["slot"] != json.Number("451610692") || record["source_record_status"] != "readable" {
		t.Fatalf("unexpected v1 source record: slot=%v version=%v status=%v", record["slot"], record["version"], record["source_record_status"])
	}
	if os.Getenv("SOLANA_DIAGNOSTIC") == "1" {
		meta := record["meta"].(map[string]any)
		t.Logf("preTokenBalances=%v", meta["preTokenBalances"])
		t.Logf("postTokenBalances=%v", meta["postTokenBalances"])
		for _, group := range meta["innerInstructions"].([]any) {
			entry := group.(map[string]any)
			for _, raw := range entry["instructions"].([]any) {
				ix := raw.(map[string]any)
				if program, _ := ix["programId"].(string); program == "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA" || program == "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb" {
					t.Logf("token inner outer=%v instruction=%v", entry["index"], ix)
				}
			}
		}
	}
}

// Report the upstream HTTP status for a recent signature without exposing the
// managed dRPC credential or a potentially large transaction response.
func TestLiveDRPCSolanaRecentStatus(t *testing.T) {
	if os.Getenv("DRPC_LIVE_TEST") != "1" {
		t.Skip("set DRPC_LIVE_TEST=1 for release probe")
	}
	endpoints, err := solanaRPCEndpoints()
	if err != nil || len(endpoints) == 0 {
		t.Fatalf("dRPC endpoint unavailable: %v", err)
	}
	const signature = "Ho9cGwyniv7Ea8EUr8oDfu2nDvqruVAGcbscwwHbJ3LdxvSmmH8o8eGQvFNFBTxN3Y3g1357L6sLH3Nqd9ViZLE"
	probes := []struct {
		name, method string
		params       []any
	}{
		{"transaction-jsonParsed-finalized", "getTransaction", []any{signature, map[string]any{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 0}}},
		{"transaction-jsonParsed-version1", "getTransaction", []any{signature, map[string]any{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 1}}},
		{"transaction-json-finalized", "getTransaction", []any{signature, map[string]any{"encoding": "json", "commitment": "finalized", "maxSupportedTransactionVersion": 0}}},
		{"transaction-base64-finalized", "getTransaction", []any{signature, map[string]any{"encoding": "base64", "commitment": "finalized", "maxSupportedTransactionVersion": 0}}},
		{"transaction-jsonParsed-confirmed", "getTransaction", []any{signature, map[string]any{"encoding": "jsonParsed", "commitment": "confirmed", "maxSupportedTransactionVersion": 0}}},
		{"confirmed-transaction-legacy", "getConfirmedTransaction", []any{signature, "jsonParsed"}},
		{"block-signatures", "getBlock", []any{451610692, map[string]any{"encoding": "jsonParsed", "transactionDetails": "signatures", "rewards": false, "maxSupportedTransactionVersion": 0}}},
		{"block-full", "getBlock", []any{451610692, map[string]any{"encoding": "jsonParsed", "transactionDetails": "full", "rewards": false, "maxSupportedTransactionVersion": 0}}},
		{"block-full-base64", "getBlock", []any{451610692, map[string]any{"encoding": "base64", "transactionDetails": "full", "rewards": false, "maxSupportedTransactionVersion": 0}}},
	}
	for _, probe := range probes {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": probe.method, "params": probe.params})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints[0].url, bytes.NewReader(body))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		req.Header.Set("content-type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Fatalf("recent dRPC request failed: %v", err)
		}
		t.Logf("recent dRPC probe=%s upstream HTTP status=%d content_length=%d", probe.name, resp.StatusCode, resp.ContentLength)
		if probe.name == "transaction-jsonParsed-version1" && resp.StatusCode == http.StatusOK {
			var envelope struct {
				Result struct {
					Slot        uint64 `json:"slot"`
					Version     any    `json:"version"`
					Transaction struct {
						Message struct {
							AccountKeys []any `json:"accountKeys"`
						} `json:"message"`
					} `json:"transaction"`
				} `json:"result"`
				Error *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
				t.Fatalf("version 1 probe decode: %v", err)
			}
			t.Logf("version 1 result: slot=%d version=%v account_keys=%d rpc_error=%+v", envelope.Result.Slot, envelope.Result.Version, len(envelope.Result.Transaction.Message.AccountKeys), envelope.Error)
		}
		if probe.name == "confirmed-transaction-legacy" && resp.StatusCode == http.StatusOK {
			var envelope struct {
				Error *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
				Result struct {
					Slot        uint64 `json:"slot"`
					Transaction struct {
						Message struct {
							AccountKeys []any `json:"accountKeys"`
						} `json:"message"`
					} `json:"transaction"`
				} `json:"result"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
				t.Fatalf("legacy confirmed response cannot be decoded: %v", err)
			}
			t.Logf("legacy confirmed result slot=%d keys=%d rpc_error=%+v", envelope.Result.Slot, len(envelope.Result.Transaction.Message.AccountKeys), envelope.Error)
		}
		resp.Body.Close()
		cancel()
	}
	// dRPC also documents a public Solana endpoint. Probe it separately;
	// production must not silently bypass its managed-key quota channel.
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "getTransaction", "params": []any{signature, map[string]any{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 0}}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://solana.drpc.org/", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("public dRPC probe transport error: %v", err)
		return
	}
	defer resp.Body.Close()
	var envelope struct {
		Result struct {
			Slot uint64 `json:"slot"`
		} `json:"result"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(resp.Body).Decode(&envelope)
	}
	t.Logf("public dRPC recent getTransaction status=%d slot=%d rpc_error=%v", resp.StatusCode, envelope.Result.Slot, envelope.Error)
}
