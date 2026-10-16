package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/mockrpc"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

func TestContractTraceEmptyAccountOptions(t *testing.T) {
	for _, c := range []struct {
		tracer        string
		diff, include bool
	}{{"prestateTracer", false, true}, {"prestateTracer", true, false}, {"callTracer", false, false}} {
		t.Run(c.tracer+map[bool]string{true: "-diff", false: "-ordinary"}[c.diff], func(t *testing.T) {
			count := 0
			call := func(_ context.Context, req rpc.Request) (rpc.Response, error) {
				count++
				config := req.Params[1].(map[string]any)
				options, _ := config["tracerConfig"].(map[string]any)
				if (options["includeEmpty"] == true) != c.include || c.tracer == "prestateTracer" && options["diffMode"] != c.diff || req.Method != "debug_traceTransaction" || req.Params[0] != "selected-tx" || req.Class != rpc.ClassDebugTrace {
					t.Fatalf("incorrect actual request %+v", req)
				}
				return rpc.Response{Result: json.RawMessage(`{}`), Attempts: 1, Bytes: 2}, nil
			}
			_, meta, e := contractReadTrace(context.Background(), call, "ethereum", "selected-tx", c.tracer, c.diff, rpc.BudgetContext{})
			if e != nil || count != 1 || meta["include_empty_requested"] != c.include || meta["include_empty_compatibility_fallback"] != false {
				t.Fatalf("meta=%+v error=%v", meta, e)
			}
		})
	}
}

func TestContractTraceOnlyInvalidParametersRetry(t *testing.T) {
	for _, class := range []rpc.ErrorClass{rpc.ClassInvalidParams, rpc.ClassRateLimit, rpc.ClassTimeout, rpc.ClassUpstream, rpc.ClassInvalidResponse, rpc.ClassArchiveRequired, rpc.ClassBudgetExceeded, rpc.ClassTraceUnsupported} {
		t.Run(string(class), func(t *testing.T) {
			budget := rpc.BudgetContext{JobID: "one-job", Purpose: rpc.PurposeContractAnalysis, MaxRequests: 2, MaxCredits: 1000}
			count := 0
			call := func(_ context.Context, req rpc.Request) (rpc.Response, error) {
				count++
				if req.Budget != budget {
					t.Fatal("fallback changed budget identity")
				}
				options := req.Params[1].(map[string]any)["tracerConfig"].(map[string]any)
				if count == 1 {
					if options["includeEmpty"] != true {
						t.Fatal("first request lost includeEmpty")
					}
					return rpc.Response{Attempts: 1, Bytes: 7}, &rpc.Error{Class: class}
				}
				if _, present := options["includeEmpty"]; present {
					t.Fatal("compatibility request retained unsupported option")
				}
				return rpc.Response{Result: json.RawMessage(`{}`), Attempts: 1, Bytes: 2}, nil
			}
			_, meta, e := contractReadTrace(context.Background(), call, "ethereum", "tx", "prestateTracer", false, budget)
			if class == rpc.ClassInvalidParams {
				if e != nil || count != 2 || meta["attempts"] != 2 || meta["bytes"] != 9 || meta["include_empty_compatibility_fallback"] != true {
					t.Fatalf("fallback: %+v %v calls=%d", meta, e, count)
				}
			} else if e == nil || count != 1 {
				t.Fatalf("retried %s: %+v %v calls=%d", class, meta, e, count)
			}
		})
	}
}

func TestContractTraceCompatibilityCannotExceedGatewayBudget(t *testing.T) {
	p := mockrpc.New("trace-options")
	p.Handle("debug_traceTransaction", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		return nil, &rpc.Error{Class: rpc.ClassInvalidParams}
	})
	caps := rpc.NewCapabilityStore()
	caps.Set(p.Name(), "eth", rpc.CapabilitySet{StandardRPC: true, Archive: true, Trace: true, Debug: true}, rpc.CapSourceStatic)
	breakers := rpc.NewBreakers(20, .9, time.Second)
	health := rpc.NewHealthTracker()
	conc := rpc.NewConcurrencyController(2)
	router := rpc.NewRouter(caps, breakers, health, conc)
	router.Register("eth", p, 100, 0)
	budgets := rpc.NewBudgetManager(time.Hour, nil, true)
	gateway := rpc.NewGateway(rpc.GatewayOptions{Router: router, Capabilities: caps, Breakers: breakers, Health: health, Concurrency: conc, Budgets: budgets, Rate: rpc.NewRateController(10000), Retry: rpc.RetryPolicy{MaxAttempts: 1}})
	_, meta, e := contractReadTrace(context.Background(), gateway.Call, "eth", "0x"+strings.Repeat("1", 64), "prestateTracer", false, rpc.BudgetContext{JobID: "bounded-trace", Purpose: rpc.PurposeContractAnalysis, MaxRequests: 1, MaxCredits: 10000})
	if !rpc.IsClass(e, rpc.ClassBudgetExceeded) || p.TotalCalls() != 1 || len(budgets.Snapshot()) != 1 || budgets.Snapshot()[0].Requests != 1 || meta["include_empty_compatibility_fallback"] != true {
		t.Fatalf("fallback bypassed budget: error=%v meta=%+v calls=%d usage=%+v", e, meta, p.TotalCalls(), budgets.Snapshot())
	}
}

func TestContractCreationPrestateCoverageIncludesRevertedFrames(t *testing.T) {
	address := "0x" + strings.Repeat("2", 40)
	trace := ca.Row(map[string]any{"type": "CALL", "calls": []any{map[string]any{"type": "CREATE", "to": address, "error": "reverted"}}})
	for _, c := range []struct {
		raw      string
		complete bool
	}{{`{}`, false}, {`{"` + address + `":null}`, false}, {`{"` + address + `":{}}`, false}, {`{"` + address + `":{"balance":"0x0"}}`, true}} {
		row := contractCreationPrestateCoverage(trace, []byte(c.raw))
		if row["complete"] != c.complete {
			t.Fatalf("raw=%s coverage=%+v", c.raw, row)
		}
	}
}

func TestContractCreationBindingRequiresObservedTargetAccount(t *testing.T) {
	for _, factory := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "factory"}[factory], func(t *testing.T) {
			snapshot, model, w, replayed := contractCreationTestFixture(t)
			if factory {
				snapshot, model, w, replayed = contractFactoryTestFixture(t, "CREATE2")
			}
			for _, invalid := range []any{nil, map[string]any{}, map[string]any{snapshot.StorageAddress: map[string]any{}}, map[string]any{snapshot.StorageAddress: nil}} {
				w["prestate"] = invalid
				binding, e := contractCreationBinding(snapshot, model, ca.Row(w), replayed)
				if e == nil || e.Error() != "CREATION_TRANSACTION_PRESTATE_INCOMPLETE" {
					t.Fatalf("unobserved account accepted: binding=%+v error=%v", binding, e)
				}
			}
		})
	}
}

func TestContractCreationPrestateActualGethCorpus(t *testing.T) {
	root := os.Getenv("CONTRACT_GETH_PRESTATE_CORPUS")
	if root == "" {
		t.Skip("actual pinned Geth corpus not selected")
	}
	for _, salt := range []string{"7", "8", "9"} {
		t.Run(salt, func(t *testing.T) {
			var witness struct {
				Trace    json.RawMessage `json:"call_trace"`
				Prestate json.RawMessage `json:"prestate"`
			}
			raw, e := os.ReadFile(filepath.Join(root, "salt-"+salt+"-witness.json"))
			if e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(raw, &witness); e != nil {
				t.Fatal(e)
			}
			before := append([]byte(nil), witness.Prestate...)
			positive := contractCreationPrestateCoverage(witness.Trace, witness.Prestate)
			if positive["complete"] != true || !reflect.DeepEqual(before, []byte(witness.Prestate)) {
				t.Fatalf("actual prestate altered or rejected: %+v", positive)
			}
			standard, e := os.ReadFile(filepath.Join(root, "salt-"+salt+"-default-prestate.json"))
			if e != nil {
				t.Fatal(e)
			}
			negative := contractCreationPrestateCoverage(witness.Trace, standard)
			if negative["complete"] != (salt == "8") {
				t.Fatalf("ordinary tracer coverage: %+v", negative)
			}
		})
	}
}
