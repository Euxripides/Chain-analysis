package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func contractCreationTestFixture(t *testing.T) (ca.Snapshot, map[string]any, map[string]any, ca.Result) {
	t.Helper()
	from := "0x" + strings.Repeat("1", 40)
	address, e := contractCreationAddress(from, "0x7")
	if e != nil {
		t.Fatal(e)
	}
	tx, block, parent := "0x"+strings.Repeat("2", 64), "0x"+strings.Repeat("3", 64), "0x"+strings.Repeat("4", 64)
	code := "0x6000"
	hash := contractKeccak([]byte{0x60, 0x00})
	init := "0x600160005560006000f3"
	input := init + fmt.Sprintf("%064x", 17)
	snapshot := ca.Snapshot{VMFamily: "evm", ChainID: "1", BlockNumber: 20, CodeAddress: address, StorageAddress: address, CodeHash: hash}
	model := rowMap(ca.Row(map[string]any{"model_id": "initial", "code_address": address, "storage_address": address, "code_hash": hash, "complete": true, "creation_identity": map[string]any{"compiler": "0.8.30+commit.73712a01", "initcode": init, "constructor_parameters": []any{map[string]any{"source_ast_id": 7, "name": "seed", "type": "uint256"}}}}))
	witness := rowMap(ca.Row(map[string]any{
		"raw_transaction": map[string]any{"hash": tx, "from": from, "to": nil, "nonce": "0x7", "chainId": "0x1", "input": input, "blockHash": block, "blockNumber": "0xa", "transactionIndex": "0x0"},
		"raw_receipt":     map[string]any{"transactionHash": tx, "contractAddress": address, "status": "0x1", "blockHash": block, "blockNumber": "0xa", "transactionIndex": "0x0"},
		"raw_block":       map[string]any{"number": "0xa", "hash": block, "parentHash": parent}, "parent_block": map[string]any{"number": "0x9", "hash": parent},
		"call_trace": map[string]any{"type": "CREATE", "from": from, "to": address, "input": input, "output": code}, "state_diff": map[string]any{"post": map[string]any{address: map[string]any{"code": code}}},
	}))
	replayed := ca.Result{Sections: map[string][]json.RawMessage{"replays": {ca.Row(map[string]any{"tx_hash": tx, "block_hash": block, "chain_id": "1", "replay_id": "replay", "complete": true, "status": "matched_observations", "original_call_trace_matches": true, "state_diff_complete": true, "observed": map[string]any{"contract_address": address}, "replayed": map[string]any{"contract_address": address}})}}}
	return snapshot, model, witness, replayed
}
func TestContractCreationBinding(t *testing.T) {
	for _, scenario := range []string{"good", "initcode", "arguments_length", "noncanonical_address", "receipt_hash", "receipt_block", "wrong_chain", "nonce", "factory", "proxy_storage", "runtime", "trace", "trace_output", "failed_receipt", "parent", "future", "incomplete_replay", "replay_block", "replay_address", "replay_chain", "duplicate_replay"} {
		t.Run(scenario, func(t *testing.T) {
			snapshot, model, w, replayed := contractCreationTestFixture(t)
			tx, receipt := contractCreationMap(w["raw_transaction"]), contractCreationMap(w["raw_receipt"])
			trace := contractCreationMap(w["call_trace"])
			proof := rowMap(replayed.Sections["replays"][0])
			switch scenario {
			case "initcode":
				contractCreationMap(model["creation_identity"])["initcode"] = "0x6002"
			case "arguments_length":
				tx["input"] = fmt.Sprint(tx["input"]) + "00"
				trace["input"] = tx["input"]
			case "noncanonical_address":
				contractCreationMap(model["creation_identity"])["constructor_parameters"] = []any{map[string]any{"type": "address"}}
				tx["input"] = fmt.Sprint(contractCreationMap(model["creation_identity"])["initcode"]) + "ff" + strings.Repeat("00", 31)
				trace["input"] = tx["input"]
			case "receipt_hash":
				receipt["transactionHash"] = "0x" + strings.Repeat("5", 64)
			case "receipt_block":
				receipt["blockHash"] = "0x" + strings.Repeat("5", 64)
			case "wrong_chain":
				tx["chainId"] = "0x2"
			case "nonce":
				tx["nonce"] = "0x8"
			case "factory":
				tx["to"] = snapshot.CodeAddress
			case "proxy_storage":
				model["storage_address"] = "0x" + strings.Repeat("5", 40)
			case "runtime":
				model["code_hash"] = "0x" + strings.Repeat("5", 64)
			case "trace":
				trace["input"] = "0x00"
			case "trace_output":
				trace["output"] = "0x6001"
			case "failed_receipt":
				receipt["status"] = "0x0"
			case "parent":
				contractCreationMap(w["parent_block"])["number"] = "0x8"
			case "future":
				snapshot.BlockNumber = 9
			case "incomplete_replay":
				proof["complete"] = false
			case "replay_block":
				proof["block_hash"] = "0x" + strings.Repeat("5", 64)
			case "replay_address":
				contractCreationMap(proof["replayed"])["contract_address"] = "0x" + strings.Repeat("5", 40)
			case "replay_chain":
				proof["chain_id"] = "2"
			}
			replayed.Sections["replays"][0] = ca.Row(proof)
			if scenario == "duplicate_replay" {
				replayed.Sections["replays"] = append(replayed.Sections["replays"], ca.Row(proof))
			}
			binding, e := contractCreationBinding(snapshot, model, ca.Row(w), replayed)
			if scenario != "good" {
				if e == nil {
					t.Fatalf("invalid creation accepted: %s: %+v", scenario, binding)
				}
				return
			}
			if e != nil || binding["complete"] != true || binding["later_history_and_upgrades_verified"] != false {
				t.Fatalf("binding: %+v error %v", binding, e)
			}
			arguments := binding["constructor_arguments"].([]map[string]any)
			if arguments[0]["value"] != "17" {
				t.Fatalf("argument: %+v", arguments)
			}
		})
	}
}
func TestContractCreationCanonicalStaticArguments(t *testing.T) {
	for _, c := range []struct {
		typ, word string
		valid     bool
	}{
		{"bool", fmt.Sprintf("%064x", 1), true}, {"bool", fmt.Sprintf("%064x", 2), false},
		{"uint8", fmt.Sprintf("%064x", 255), true}, {"uint8", fmt.Sprintf("%064x", 256), false},
		{"int8", strings.Repeat("ff", 32), true}, {"int8", strings.Repeat("00", 31) + "ff", false},
		{"bytes2", "1234" + strings.Repeat("00", 30), true}, {"bytes2", "1234" + strings.Repeat("00", 29) + "01", false},
		{"uint256[]", strings.Repeat("00", 32), false},
	} {
		t.Run(c.typ+"-"+c.word, func(t *testing.T) {
			raw, e := contractCreationBytes("0x" + c.word)
			if e != nil {
				t.Fatal(e)
			}
			_, e = contractCreationArguments(raw, []any{map[string]any{"type": c.typ}})
			if (e == nil) != c.valid {
				t.Fatalf("valid=%v error=%v", c.valid, e)
			}
		})
	}
}

func TestContractCreationBindingPreservesTheoremAndHistoryBoundaries(t *testing.T) {
	for _, proved := range []bool{false, true} {
		t.Run(fmt.Sprint(proved), func(t *testing.T) {
			snapshot, model, _, _ := contractCreationTestFixture(t)
			result := ca.Result{Snapshot: snapshot, Sections: map[string][]json.RawMessage{
				"initial_state_models": {ca.Row(model)},
				"protocol_invariants":  {ca.Row(map[string]any{"model_id": "creation-property", "kind": "erc20_source_initialization", "source_transition_model_id": "initial", "code_hash": snapshot.CodeHash, "storage_address": snapshot.StorageAddress, "complete": proved, "obligations": []string{"original-source-proof"}}), ca.Row(map[string]any{"kind": "erc20_ledger_conservation", "source_initialization_model_id": "creation-property", "initial_invariant_basis": "declared_precondition"})},
				"verification_closure": {ca.Row(map[string]any{"whole_authorization_proved": false, "whole_protocol_economics_proved": false})},
			}}
			contractApplyCreationBinding(&result, 0, map[string]any{"complete": true, "later_history_and_upgrades_verified": false}, "binding-proof")
			initial, ledger := rowMap(result.Sections["protocol_invariants"][0]), rowMap(result.Sections["protocol_invariants"][1])
			if initial["complete"] != proved || initial["deployment_identity_verified"] != true {
				t.Fatalf("source theorem changed: %+v", initial)
			}
			want := "declared_precondition"
			if proved {
				want = "proved_source_creation_with_replayed_deployment_identity"
			}
			if ledger["initial_invariant_basis"] != want {
				t.Fatalf("ledger basis: %+v", ledger)
			}
			closure := rowMap(result.Sections["verification_closure"][0])
			if closure["whole_authorization_proved"] != false || closure["whole_protocol_economics_proved"] != false {
				t.Fatal("creation identity promoted the whole protocol")
			}
		})
	}
}

// The optional corpus is generated from actual deployments and isolated
// Anvil replays, never from a manually assigned replay success flag.
func TestContractCreationActualCorpus(t *testing.T) {
	root := os.Getenv("CONTRACT_CREATION_BINDING_CORPUS")
	if root == "" {
		t.Skip("actual creation corpus not selected")
	}
	files, e := filepath.Glob(filepath.Join(root, "*-binding-case.json"))
	if e != nil || len(files) == 0 {
		t.Fatalf("actual corpus: %v count=%d", e, len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			var sample struct {
				Snapshot ca.Snapshot     `json:"snapshot"`
				Model    map[string]any  `json:"model"`
				Witness  json.RawMessage `json:"witness"`
				Replay   ca.Result       `json:"replay"`
			}
			raw, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			if e = nextDecode(raw, &sample); e != nil {
				t.Fatal(e)
			}
			binding, e := contractCreationBinding(sample.Snapshot, sample.Model, sample.Witness, sample.Replay)
			if e != nil || binding["complete"] != true {
				t.Fatalf("actual binding: %+v %v", binding, e)
			}
		})
	}
}
