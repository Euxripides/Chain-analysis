package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

func TestContractBlockSystemStateBoundsAndIdentity(t *testing.T) {
	hash := "0x" + strings.Repeat("1", 64)
	block := map[string]any{"number": "0xa", "timestamp": "0x64", "parentHash": hash}
	parent := map[string]any{"number": "0x9", "hash": hash}
	for _, scenario := range []string{"pre-cancun", "ordinary", "nonempty-queues", "slot-budget", "wrong-parent", "unknown-rules", "provider-error"} {
		t.Run(scenario, func(t *testing.T) {
			p := rowMap(ca.Row(parent))
			fork, limit, calls := "osaka", 96, 0
			budget := rpc.BudgetContext{JobID: "bounded-environment", Purpose: rpc.PurposeContractAnalysis, MaxRequests: 100, MaxCredits: 100000}
			if scenario == "pre-cancun" {
				fork = "shanghai"
			}
			if scenario == "unknown-rules" {
				fork = "chain_rules_unavailable"
			}
			if scenario == "slot-budget" {
				limit = 10
			}
			if scenario == "wrong-parent" {
				p["hash"] = "0x" + strings.Repeat("2", 64)
			}
			call := func(_ context.Context, req rpc.Request) (rpc.Response, error) {
				calls++
				ref, ok := req.Params[len(req.Params)-1].(map[string]any)
				if !ok || ref["blockHash"] != hash || ref["requireCanonical"] != true || req.Budget != budget || req.BlockRef.String() != "0x9" {
					t.Fatalf("unfenced state request %+v", req)
				}
				if scenario == "provider-error" {
					return rpc.Response{}, &rpc.Error{Class: rpc.ClassInvalidParams}
				}
				value := "0x0"
				if req.Method == "eth_getCode" {
					value = "0x00"
				}
				if req.Method == "eth_getTransactionCount" {
					value = "0x1"
				}
				if scenario == "nonempty-queues" && req.Method == "eth_getStorageAt" && req.Params[1] == "0x"+strings.Repeat("0", 63)+"3" {
					value = "0x3e8"
				}
				return rpc.Response{Result: ca.Row(value), Attempts: 1, Bytes: len(value) + 2}, nil
			}
			row, reads := contractReadBlockSystemState(context.Background(), call, "eth", fork, block, p, limit, budget)
			switch scenario {
			case "ordinary":
				if row["complete"] != true || row["account_count"] != 5 || row["slot_reads"] != 11 || calls != 26 {
					t.Fatalf("ordinary: %+v calls=%d", row, calls)
				}
			case "nonempty-queues":
				if row["complete"] != true || row["slot_reads"] != 67 || calls != 82 {
					t.Fatalf("unbounded queue reads: %+v calls=%d", row, calls)
				}
			case "slot-budget":
				if row["complete"] != false || row["reason"] != "BLOCK_SYSTEM_SLOT_BUDGET" || row["slot_reads"] != 10 {
					t.Fatalf("slot budget %+v", row)
				}
			case "wrong-parent", "unknown-rules":
				if row["complete"] != false || calls != 0 {
					t.Fatalf("unfenced parent accepted: %+v", row)
				}
			case "provider-error":
				if row["complete"] != false || calls != 1 {
					t.Fatalf("retried or invented parent state: %+v calls=%d", row, calls)
				}
			case "pre-cancun":
				if row["complete"] != true || calls != 0 || row["required"] != false {
					t.Fatalf("extra historical RPCs: %+v", row)
				}
			}
			if len(reads) != calls || row["whole_block_execution_verified"] != false {
				t.Fatalf("reads or scope lost: %+v", row)
			}
		})
	}
}

func TestContractBlockSystemStateSharesTaskStateReservations(t *testing.T) {
	hash := "0x" + strings.Repeat("1", 64)
	block := map[string]any{"number": "0xa", "timestamp": "0x64", "parentHash": hash}
	parent := map[string]any{"number": "0x9", "hash": hash}
	ctx, used, calls := context.WithValue(context.Background(), contractFinalGuardKey{}, true), 1, 0
	reserve := func(got context.Context) error {
		if got != ctx {
			t.Fatal("state reservation lost task context")
		}
		if used >= 2 {
			return fmt.Errorf("task state limit")
		}
		used++
		return nil
	}
	call := func(got context.Context, req rpc.Request) (rpc.Response, error) {
		if got != ctx {
			t.Fatal("state request lost task context")
		}
		calls++
		value := "0x0"
		if req.Method == "eth_getCode" {
			value = "0x00"
		}
		return rpc.Response{Result: ca.Row(value), Attempts: 1}, nil
	}
	row, reads := contractReadBlockSystemState(ctx, contractSystemStateGateway(call, reserve), "eth", "osaka", block, parent, 96, rpc.BudgetContext{})
	if row["complete"] != false || row["reason"] != "BLOCK_SYSTEM_STATE_budget_exceeded" || used != 2 || calls != 4 || len(reads) != 5 {
		t.Fatalf("system reads bypassed shared task cap: %+v used=%d calls=%d reads=%d", row, used, calls, len(reads))
	}
}

// The runtime harness starts its private Geth node, submits local transactions,
// and selects this test while that node is still running. No public RPC is used.
func TestContractBlockSystemStateLiveGeth(t *testing.T) {
	file := os.Getenv("CONTRACT_GETH_LIVE_WITNESS")
	if file == "" {
		t.Skip("private live Geth witness not selected")
	}
	raw, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	w := rowMap(raw)
	block, parent := contractCreationMap(w["raw_block"]), contractCreationMap(w["parent_block"])
	requests := 0
	client := &http.Client{Timeout: 10 * time.Second}
	budget := rpc.BudgetContext{JobID: "local-geth-environment", Purpose: rpc.PurposeContractAnalysis, MaxRequests: 100, MaxCredits: 100000}
	call := func(ctx context.Context, req rpc.Request) (rpc.Response, error) {
		if req.Budget != budget {
			t.Fatal("live collection changed budget identity")
		}
		switch req.Method {
		case "eth_getBalance", "eth_getTransactionCount", "eth_getCode", "eth_getStorageAt", "debug_traceTransaction":
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
		requests++
		body := ca.Row(map[string]any{"jsonrpc": "2.0", "id": requests, "method": req.Method, "params": req.Params})
		h, e := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:18777", bytes.NewReader(body))
		if e != nil {
			return rpc.Response{}, e
		}
		h.Header.Set("Content-Type", "application/json")
		response, e := client.Do(h)
		if e != nil {
			return rpc.Response{}, e
		}
		defer response.Body.Close()
		data, e := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		if e != nil {
			return rpc.Response{}, e
		}
		var result struct {
			Result json.RawMessage `json:"result"`
			Error  any             `json:"error"`
		}
		if e = json.Unmarshal(data, &result); e != nil {
			return rpc.Response{}, e
		}
		if result.Error != nil {
			return rpc.Response{}, fmt.Errorf("local Geth method error %s", req.Method)
		}
		return rpc.Response{Result: result.Result, Provider: "isolated-local-geth", Attempts: 1, Bytes: len(data)}, nil
	}
	reservations := 0
	reserve := func(context.Context) error { reservations++; return nil }
	row, reads := contractReadBlockSystemState(context.Background(), contractSystemStateGateway(call, reserve), "eth", "osaka", block, parent, 96, budget)
	if row["complete"] != true || requests != 26 || len(reads) != 26 || reservations != 11 || row["whole_block_execution_verified"] != false {
		t.Fatalf("actual collection failed: %+v requests=%d", row, requests)
	}
	prestate, meta, e := contractReadTrace(context.Background(), call, "eth", contractCreationText(contractCreationMap(w["raw_transaction"])["hash"]), "prestateTracer", false, budget)
	if e != nil || meta["include_empty_requested"] != true || meta["include_empty_compatibility_fallback"] != false || contractCreationPrestateCoverage(ca.Row(w["call_trace"]), prestate)["complete"] != true {
		t.Fatalf("actual transaction prestate: meta=%+v error=%v", meta, e)
	}
	actual := rowMap(prestate)
	expected := contractCreationMap(w["prestate"])
	if !bytes.Equal(ca.Row(actual), ca.Row(expected)) {
		t.Fatal("production trace request differs from original captured transaction prestate")
	}
	name := strings.TrimSuffix(file, "-witness.json") + "-production-collector.json"
	if filepath.Dir(name) != filepath.Dir(file) {
		t.Fatal("collector output escaped corpus")
	}
	if e = os.WriteFile(name, ca.Row(map[string]any{"block_system_prestate": row, "provider_reads": reads, "prestate": actual, "prestate_meta": meta, "local_rpc_requests": requests, "task_state_reservations": reservations}), 0600); e != nil {
		t.Fatal(e)
	}
}
