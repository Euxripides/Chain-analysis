package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

func TestContractReturnFrameBinding(t *testing.T) {
	address := "0x" + strings.Repeat("a", 40)
	code := "0x" + strings.Repeat("b", 64)
	for _, scenario := range []string{"scalar", "array", "contract_array", "malformed", "failed_frame", "failed_ancestor", "wrong_code", "wrong_storage", "ambiguous", "unknown_identity"} {
		t.Run(scenario, func(t *testing.T) {
			outputType, output := "uint256", "0x"+fmt.Sprintf("%064x", 9007199254740993)
			if scenario == "array" || scenario == "contract_array" {
				outputType, output = "uint256[] memory", "0x"+fmt.Sprintf("%064x%064x%064x%064x", 32, 2, 7, 11)
				if scenario == "contract_array" {
					outputType = "contract Token[] memory"
				}
			}
			m := map[string]any{"method_id": "m", "verified_signature": true, "signature": "quote()", "code_hash": code, "code_address": address, "storage_address": address,
				"outputs": []map[string]any{{"type": outputType}}}
			call := map[string]any{"call_id": "c", "tx_hash": "tx", "trace_path": "0.0", "call_type": "STATICCALL", "code_hash": code, "code_address": address, "storage_address": address,
				"output": output, "method_ids": []string{"m"}, "method_identity_status": "transaction_prestate_code_and_storage_context", "settled": true, "error": "", "selector": method.SelectorOf("quote()")}
			switch scenario {
			case "malformed":
				call["output"] = "0x01"
			case "failed_frame":
				call["error"] = "execution reverted"
			case "failed_ancestor":
				call["settled"] = false
			case "wrong_code":
				call["code_hash"] = "different"
			case "wrong_storage":
				call["storage_address"] = "different"
			case "ambiguous":
				call["method_ids"] = []string{"m", "other"}
			case "unknown_identity":
				delete(call, "method_identity_status")
			}
			result := ca.Result{Sections: map[string][]json.RawMessage{"methods": {ca.Row(m)}, "execution_calls": {ca.Row(call)}, "transactions": {ca.Row(map[string]any{"tx_hash": "tx", "block_hash": "block", "transaction_index": 2})}}}
			contractEnrichExternalReturns(&result)
			observations := result.Sections["external_return_observations"]
			if len(observations) != 1 {
				t.Fatal("frame return missing")
			}
			observation := rowMap(observations[0])
			if observation["scope"] != "execution_frame_only" || observation["return_data"] != call["output"] || observation["transaction_index"] != json.Number("2") {
				t.Fatalf("lost frame identity: %s", observations[0])
			}
			switch scenario {
			case "scalar", "array", "contract_array":
				if observation["status"] != "decoded_execution_frame_return" || observation["decoded_values"] == nil {
					t.Fatal(string(observations[0]))
				}
				if scenario == "scalar" && !strings.Contains(string(observations[0]), "9007199254740993") {
					t.Fatal("integer precision lost")
				}
				if scenario == "contract_array" && !strings.Contains(string(observations[0]), "address[]") {
					t.Fatal("contract array lost its ABI shape")
				}
			case "failed_ancestor":
				if observation["status"] != "decoded_return_on_reverted_ancestor_path" {
					t.Fatal(string(observations[0]))
				}
			default:
				if observation["decoded_values"] != nil {
					t.Fatal("unverified frame was decoded")
				}
			}
			if m["binding_expression_id"] != nil || call["binding_expression_id"] != nil {
				t.Fatal("execution return escaped into static model")
			}
		})
	}
}

func TestContractReturnSavedRuntime(t *testing.T) {
	dir := os.Getenv("CONTRACT_DEEPENING_RUNTIME")
	if dir == "" {
		t.Skip("requires the local Anvil acceptance records")
	}
	result := ca.Result{Sections: map[string][]json.RawMessage{}}
	for _, name := range []string{"Deepening-true-analysis.json", "Quotes-analysis.json"} {
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
	raw, err := os.ReadFile(filepath.Join(dir, "external-return-traces.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		Name     string                     `json:"name"`
		Trace    json.RawMessage            `json:"trace"`
		Prestate map[string]ca.StateAccount `json:"prestate"`
		Receipt  struct {
			Hash      string `json:"transactionHash"`
			BlockHash string `json:"blockHash"`
			Status    string `json:"status"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(raw, &records); err != nil || len(records) != 4 {
		t.Fatalf("invalid runtime records: %v", err)
	}
	for _, record := range records {
		rows, err := ca.TraceRows(record.Trace, record.Receipt.Hash, record.Receipt.Status == "0x1", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range rows["execution_calls"] {
			c := rowMap(raw)
			code, err := chain.HexToBytes(record.Prestate[fmt.Sprint(c["code_address"])].Code)
			if err != nil || len(code) == 0 {
				t.Fatal("actual prestate code missing")
			}
			codeHash := contractKeccak(code)
			ids := []string{}
			for _, mraw := range result.Sections["methods"] {
				m := rowMap(mraw)
				if m["code_hash"] == codeHash && m["code_address"] == c["code_address"] && m["storage_address"] == c["storage_address"] && m["selector"] == c["selector"] {
					ids = append(ids, fmt.Sprint(m["method_id"]))
				}
			}
			c["method_ids"], c["code_hash"], c["method_identity_status"] = ids, codeHash, "transaction_prestate_code_and_storage_context"
			result.Sections["execution_calls"] = append(result.Sections["execution_calls"], ca.Row(c))
		}
		result.Sections["transactions"] = append(result.Sections["transactions"], ca.Row(map[string]any{"tx_hash": record.Receipt.Hash, "block_hash": record.Receipt.BlockHash, "simulation": true}))
	}
	contractEnrichExternalReturns(&result)
	counts := map[string]int{}
	for _, raw := range result.Sections["external_return_observations"] {
		observation := rowMap(raw)
		if strings.Contains(fmt.Sprint(observation["trace_path"]), ".") {
			if observation["decoded_values"] == nil {
				t.Fatal(string(raw))
			}
			if observation["source_binding_status"] != "requires_program_counter_witness" {
				t.Fatal("source expression was substituted without a PC witness")
			}
			counts[fmt.Sprint(observation["status"])]++
			if strings.Contains(string(raw), "uint256[]") && !strings.Contains(string(raw), "9007199254740993") {
				t.Fatal("dynamic return integer precision lost")
			}
		}
	}
	if counts["decoded_execution_frame_return"] != 4 || counts["decoded_return_on_reverted_ancestor_path"] != 1 {
		t.Fatalf("frame binding counts: %v", counts)
	}
	if err := os.WriteFile(filepath.Join(dir, "external-return-observations.json"), ca.Row(result.Sections["external_return_observations"]), 0o600); err != nil {
		t.Fatal(err)
	}
}
