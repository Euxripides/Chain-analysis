package api

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func TestContractExactReturnCallsites(t *testing.T) {
	caller, callee := "0x"+strings.Repeat("1", 40), "0x"+strings.Repeat("2", 40)
	code, tx, block := "0x"+strings.Repeat("a", 64), "0x"+strings.Repeat("b", 64), "0x"+strings.Repeat("c", 64)
	for _, scenario := range []string{"two_sites", "same_ast_representations", "internal_context", "wrong_pc", "wrong_value", "wrong_effective_caller", "wrong_input", "wrong_output", "wrong_code", "wrong_storage", "wrong_block", "wrong_index", "wrong_settlement", "unverified", "creation", "ambiguous", "duplicate_witness", "generated", "unknown_parent"} {
		t.Run(scenario, func(t *testing.T) {
			result := ca.Result{Sections: map[string][]json.RawMessage{}}
			parent := map[string]any{"tx_hash": tx, "trace_path": "0", "method_ids": []string{"caller"}, "method_identity_status": "transaction_prestate_code_and_storage_context", "code_hash": code, "code_address": caller, "storage_address": caller}
			if scenario == "unknown_parent" {
				delete(parent, "method_identity_status")
			}
			result.Sections["execution_calls"] = []json.RawMessage{ca.Row(parent)}
			for i := 0; i < 2; i++ {
				path, dep := fmt.Sprintf("0.%d", i), fmt.Sprintf("dep-%d", i)
				pc := 10 + i*20
				o := map[string]any{"trace_path": path, "tx_hash": tx, "value_raw": "0", "effective_caller": caller, "call_type": "STATICCALL", "code_address": callee, "input": "0x01020304", "return_data": "0x07", "settled": true, "block_hash": block, "transaction_index": 2, "source_binding_status": "requires_program_counter_witness"}
				w := map[string]any{"trace_path": path, "tx_hash": tx, "parent_trace_path": "0", "pc": pc, "value_raw": "0", "effective_caller": caller, "call_type": "STATICCALL", "caller_code_address": caller, "caller_storage_address": caller, "caller_code_hash": code, "caller_code_phase": "runtime", "target": callee, "input": "0x01020304", "return_data": "0x07", "settled": true, "block_hash": block, "transaction_index": "0x2", "verified": true, "execution_identity_verified": true, "input_memory_matches": true, "bytecode_opcode_matches": true, "opcode_call_id": "op", "witness_id": "w", "artifact_id": "a"}
				m := map[string]any{"code_hash": code, "code_address": caller, "storage_address": caller, "pc": pc, "call_type": "STATICCALL", "status": "unique_source_callsite", "source_dependency_ids": []string{dep}}
				if scenario != "two_sites" {
					switch scenario {
					case "wrong_pc":
						w["pc"] = pc + 1
					case "wrong_value":
						w["value_raw"] = "1"
					case "wrong_effective_caller":
						w["effective_caller"] = callee
					case "wrong_input":
						w["input"] = "0x05060708"
					case "wrong_output":
						w["return_data"] = "0x08"
					case "wrong_code":
						w["caller_code_hash"] = "other"
					case "wrong_storage":
						w["caller_storage_address"] = callee
					case "wrong_block":
						w["block_hash"] = code
					case "wrong_index":
						w["transaction_index"] = "0x3"
					case "wrong_settlement":
						w["settled"] = false
					case "unverified":
						w["execution_identity_verified"] = false
					case "creation":
						w["caller_code_phase"] = "creation"
					case "generated":
						m["status"] = "generated_or_unmapped_code"
					case "ambiguous":
						m["source_dependency_ids"] = []string{dep, dep + "-other"}
						result.Sections["external_returns"] = append(result.Sections["external_returns"], ca.Row(map[string]any{"dependency_id": dep + "-other", "method_id": "caller", "call_type": "STATICCALL"}))
					}
				}
				result.Sections["external_return_observations"] = append(result.Sections["external_return_observations"], ca.Row(o))
				d := map[string]any{"dependency_id": dep, "expression_id": "e-" + dep, "method_id": "caller", "call_type": "STATICCALL"}
				if scenario == "same_ast_representations" || scenario == "internal_context" {
					d["source_callsite_id"] = "ast-" + dep
					if scenario == "same_ast_representations" {
						d["representation_scope"] = "source_statement"
					}
					m["source_dependency_ids"] = []string{dep, dep + "-expanded"}
					result.Sections["external_returns"] = append(result.Sections["external_returns"], ca.Row(map[string]any{"dependency_id": dep + "-expanded", "source_callsite_id": "ast-" + dep, "method_id": "caller", "call_type": "STATICCALL", "representation_scope": "internal_return"}))
				}
				result.Sections["external_returns"] = append(result.Sections["external_returns"], ca.Row(d))
				result.Sections["source_pc_maps"] = append(result.Sections["source_pc_maps"], ca.Row(m))
				result.Sections["execution_callsite_witnesses"] = append(result.Sections["execution_callsite_witnesses"], ca.Row(w))
				if scenario == "duplicate_witness" {
					result.Sections["execution_callsite_witnesses"] = append(result.Sections["execution_callsite_witnesses"], ca.Row(w))
				}
			}
			contractBindReturnCallsites(&result)
			for i, raw := range result.Sections["external_return_observations"] {
				o := rowMap(raw)
				if scenario == "two_sites" || scenario == "same_ast_representations" {
					if o["source_binding_status"] != "exact_program_counter_and_execution_context" || o["source_dependency_id"] != fmt.Sprintf("dep-%d", i) {
						t.Fatalf("wrong callsite for frame %d: %s", i, raw)
					}
				} else if scenario == "internal_context" {
					if o["source_binding_status"] != "exact_program_counter_and_execution_context" || o["source_dependency_id"] != nil || o["expression_binding_status"] != "source_callsite_known_internal_path_context_required" {
						t.Fatalf("ambiguous expression context: %s", raw)
					}
				} else if o["source_binding_status"] != "requires_program_counter_witness" {
					t.Fatalf("accepted invalid witness: %s", raw)
				}
			}
		})
	}
}

func TestContractObservedEmptyEVMBytesNormalize(t *testing.T) {
	rows, err := ca.TraceRows([]byte(`{"type":"CALL","from":"sender","to":"caller","calls":[{"type":"CALL","from":"caller","to":"recipient"}]}`), "tx", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	result := ca.Result{Sections: rows}
	contractEnrichExternalReturns(&result)
	if len(result.Sections["external_return_observations"]) != 2 {
		t.Fatal("missing observed frames")
	}
	for _, raw := range result.Sections["external_return_observations"] {
		row := rowMap(raw)
		if row["input"] != "0x" || row["return_data"] != "0x" {
			t.Fatalf("empty EVM bytes differ from their canonical representation: %s", raw)
		}
	}
}

func TestContractExactCallsitesSavedRuntime(t *testing.T) {
	dir := os.Getenv("CONTRACT_VERIFICATION_RUNTIME")
	if dir == "" {
		t.Skip("requires actual local EVM opcode, source and replay records")
	}
	result := ca.Result{Sections: map[string][]json.RawMessage{}}
	for _, name := range []string{"Verification-false-analysis.json", "Verification-true-analysis.json", "Oracle-analysis.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		part, err := ca.ParseResult(raw)
		if err != nil {
			t.Fatal(err)
		}
		mergeContractResult(&result, part)
	}
	expected := 0
	returns := map[string]string{}
	sitesByTx := map[string]map[string]bool{}
	expectedSites := map[string]int{}
	for _, name := range []string{"two-sites-false", "two-sites-true", "repeated-false", "repeated-true"} {
		raw, err := os.ReadFile(filepath.Join(dir, name+"-execution.json"))
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			Trace    json.RawMessage            `json:"trace"`
			Prestate map[string]ca.StateAccount `json:"prestate"`
			Receipt  struct {
				Hash      string `json:"transactionHash"`
				BlockHash string `json:"blockHash"`
				Number    string `json:"blockNumber"`
				Index     string `json:"transactionIndex"`
				Status    string `json:"status"`
			} `json:"receipt"`
			Witness struct {
				Complete bool             `json:"complete"`
				Calls    []map[string]any `json:"calls"`
			} `json:"witness"`
		}
		if err = json.Unmarshal(raw, &record); err != nil || !record.Witness.Complete {
			t.Fatalf("invalid actual witness %s: %v", name, err)
		}
		blockNumber, ok := new(big.Int).SetString(strings.TrimPrefix(record.Receipt.Number, "0x"), 16)
		if !ok {
			t.Fatal("actual receipt block number missing")
		}
		for _, call := range record.Witness.Calls {
			input := fmt.Sprint(call["input"])
			if len(input) != 74 {
				t.Fatalf("unexpected actual quote input: %s", input)
			}
			arg, ok := new(big.Int).SetString(input[10:], 16)
			if !ok {
				t.Fatal("actual quote argument missing")
			}
			returns[record.Receipt.Hash+":"+fmt.Sprint(call["trace_path"])] = new(big.Int).Add(arg, blockNumber).String()
		}
		sitesByTx[record.Receipt.Hash] = map[string]bool{}
		expectedSites[record.Receipt.Hash] = 2
		if strings.HasPrefix(name, "repeated-") {
			expectedSites[record.Receipt.Hash] = 1
		}
		rows, err := ca.TraceRows(record.Trace, record.Receipt.Hash, record.Receipt.Status == "0x1", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows["execution_calls"] {
			c := rowMap(row)
			code, err := chain.HexToBytes(record.Prestate[fmt.Sprint(c["code_address"])].Code)
			if err != nil || len(code) == 0 {
				t.Fatal("actual code missing")
			}
			codeHash := contractKeccak(code)
			ids := []string{}
			for _, raw := range result.Sections["methods"] {
				m := rowMap(raw)
				if m["code_hash"] == codeHash && m["code_address"] == c["code_address"] && m["storage_address"] == c["storage_address"] && m["selector"] == c["selector"] {
					ids = append(ids, fmt.Sprint(m["method_id"]))
				}
			}
			c["method_ids"], c["code_hash"], c["method_identity_status"] = ids, codeHash, "transaction_prestate_code_and_storage_context"
			result.Sections["execution_calls"] = append(result.Sections["execution_calls"], ca.Row(c))
		}
		result.Sections["transactions"] = append(result.Sections["transactions"], ca.Row(map[string]any{"tx_hash": record.Receipt.Hash, "block_hash": record.Receipt.BlockHash, "transaction_index": record.Receipt.Index, "simulation": true}))
		replayed, err := os.ReadFile(filepath.Join(dir, name+"-replay.json"))
		if err != nil {
			t.Fatal(err)
		}
		part, err := ca.ParseResult(replayed)
		if err != nil || len(part.Sections["opcode_witnesses"]) != 1 || rowMap(part.Sections["opcode_witnesses"][0])["calls_complete"] != true {
			t.Fatalf("complete replay opcode evidence missing: %s %v", name, err)
		}
		mergeContractResult(&result, part)
		expected += len(record.Witness.Calls)
	}
	contractEnrichExternalReturns(&result)
	contractBindReturnCallsites(&result)
	bound := 0
	for _, raw := range result.Sections["external_return_observations"] {
		o := rowMap(raw)
		if !strings.Contains(fmt.Sprint(o["trace_path"]), ".") {
			continue
		}
		if o["source_binding_status"] != "exact_program_counter_and_execution_context" || o["source_callsite_id"] == nil || len(stringList(o["source_dependency_ids"])) == 0 || o["source_callsite"] == nil {
			t.Fatalf("actual source/return mismatch: %s", raw)
		}
		if o["source_dependency_id"] == nil && (o["source_expression_id"] != nil || o["expression_binding_status"] != "source_callsite_known_internal_path_context_required") {
			t.Fatalf("ambiguous expression must retain its context requirement: %s", raw)
		}
		var decoded []struct {
			Value string `json:"value"`
		}
		key := fmt.Sprint(o["tx_hash"]) + ":" + fmt.Sprint(o["trace_path"])
		if err := json.Unmarshal(ca.Row(o["decoded_values"]), &decoded); err != nil || len(decoded) != 1 || decoded[0].Value != returns[key] {
			t.Fatalf("return differs from the actual call argument and block: %s", raw)
		}
		sitesByTx[fmt.Sprint(o["tx_hash"])][fmt.Sprint(o["source_callsite_id"])] = true
		bound++
	}
	if bound != expected || bound != 10 {
		t.Fatalf("missing exact invocations: %d / %d", bound, expected)
	}
	for tx, sites := range sitesByTx {
		if len(sites) != expectedSites[tx] {
			t.Fatalf("wrong number of actual source callsites in %s: %d / %d", tx, len(sites), expectedSites[tx])
		}
	}
	for _, name := range []string{"delegate-storage", "callcode-storage"} {
		raw, err := os.ReadFile(filepath.Join(dir, name+"-execution.json"))
		if err != nil {
			t.Fatal(err)
		}
		var actual struct {
			Trace   json.RawMessage `json:"trace"`
			Receipt struct {
				Hash string `json:"transactionHash"`
			} `json:"receipt"`
			Witness struct {
				Calls []map[string]any `json:"calls"`
			} `json:"witness"`
		}
		if err = json.Unmarshal(raw, &actual); err != nil || len(actual.Witness.Calls) != 1 {
			t.Fatal("actual context witness missing", name, err)
		}
		rows, err := ca.TraceRows(actual.Trace, actual.Receipt.Hash, true, 10)
		if err != nil || len(rows["execution_calls"]) != 2 {
			t.Fatal("actual context trace missing", name, err)
		}
		frame, witness := rowMap(rows["execution_calls"][1]), actual.Witness.Calls[0]
		if frame["value_raw"] != witness["value_raw"] || frame["effective_caller"] != witness["effective_caller"] {
			t.Fatalf("actual opcode and API context differ in %s: %s", name, rows["execution_calls"][1])
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "exact-return-observations.json"), ca.Row(result.Sections["external_return_observations"]), 0o600); err != nil {
		t.Fatal(err)
	}
}
