package api

import (
	"encoding/json"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractFactoryCreationActualCorpus(t *testing.T) {
	root := os.Getenv("CONTRACT_FACTORY_BINDING_CORPUS")
	if root == "" {
		t.Skip("actual factory creation corpus not selected")
	}
	files, e := filepath.Glob(filepath.Join(root, "*-binding-case.json"))
	if e != nil || len(files) == 0 {
		t.Fatalf("actual factory corpus: %v count=%d", e, len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			var sample struct {
				Snapshot        ca.Snapshot     `json:"snapshot"`
				Model           map[string]any  `json:"model"`
				Witness         json.RawMessage `json:"witness"`
				Replay          ca.Result       `json:"replay"`
				ExpectedError   string          `json:"expected_error"`
				ExpectedScope   string          `json:"expected_scope"`
				ExpectedCreator string          `json:"expected_creator"`
			}
			raw, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			if e = nextDecode(raw, &sample); e != nil {
				t.Fatal(e)
			}
			binding, e := contractCreationBinding(sample.Snapshot, sample.Model, sample.Witness, sample.Replay)
			if sample.ExpectedError != "" {
				if e == nil || e.Error() != sample.ExpectedError {
					t.Fatalf("expected %s, got binding=%+v error=%v", sample.ExpectedError, binding, e)
				}
				return
			}
			if e != nil || binding["complete"] != true || binding["scope"] != sample.ExpectedScope || !contractCreationEqual(binding["creator"], sample.ExpectedCreator) || binding["later_history_and_upgrades_verified"] != false || binding["snapshot_lifecycle_continuity_verified"] != false {
				t.Fatalf("actual factory binding: %+v error=%v", binding, e)
			}
		})
	}
}

func contractFactoryTestFixture(t *testing.T, kind string) (ca.Snapshot, map[string]any, map[string]any, ca.Result) {
	t.Helper()
	snapshot, model, w, replayed := contractCreationTestFixture(t)
	tx, receipt := contractCreationMap(w["raw_transaction"]), contractCreationMap(w["raw_receipt"])
	factory := "0x" + strings.Repeat("5", 40)
	init := contractCreationText(contractCreationMap(w["call_trace"])["input"])
	target, e := contractCreationAddress(factory, "0x1")
	if e != nil {
		t.Fatal(e)
	}
	if kind == "CREATE2" {
		raw, e := contractCreationBytes(init)
		if e != nil {
			t.Fatal(e)
		}
		target, e = contractCreate2Address(factory, "7", raw)
		if e != nil {
			t.Fatal(e)
		}
	}
	snapshot.CodeAddress, snapshot.StorageAddress = target, target
	model["code_address"], model["storage_address"] = target, target
	tx["to"], tx["input"] = factory, "0x01020304"
	receipt["contractAddress"] = nil
	child := map[string]any{"type": kind, "from": factory, "to": target, "input": init, "output": "0x6000", "value": "0x0"}
	w["call_trace"] = map[string]any{"type": "CALL", "from": tx["from"], "to": factory, "input": tx["input"], "output": "0x", "calls": []any{child}}
	w["state_diff"] = map[string]any{"post": map[string]any{target: map[string]any{"code": "0x6000"}}}
	proof := rowMap(replayed.Sections["replays"][0])
	proof["observed"], proof["replayed"] = map[string]any{"contract_address": nil}, map[string]any{"contract_address": nil}
	proof["call_trace"] = rowMap(ca.Row(w["call_trace"]))
	replayed.Sections["replays"][0] = ca.Row(proof)
	opcode := map[string]any{"opcode_call_id": "creation-op", "trace_path": "0.0", "parent_trace_path": "0", "pc": 0, "instruction_order": 1, "call_type": kind, "caller_code_address": factory, "caller_storage_address": factory, "caller_code_hash": contractKeccak([]byte{0xf0}), "caller_code_phase": "runtime", "target": target, "input": init, "return_data": "0x6000", "execution_opcode_identity_verified": true, "input_memory_matches": true, "bytecode_opcode_matches": true, "settled": true}
	if kind == "CREATE2" {
		opcode["create2_salt_raw"], opcode["derived_create2_address"] = "7", target
	}
	opcode["tx_hash"], opcode["block_hash"], opcode["artifact_id"] = tx["hash"], contractCreationMap(w["raw_block"])["hash"], "creation-op-artifact"
	replayed.Sections["execution_callsite_witnesses"] = []json.RawMessage{ca.Row(opcode)}
	contractFactoryTestRefreshArtifact(&replayed)
	return snapshot, model, w, replayed
}
func contractFactoryTestRefreshArtifact(replayed *ca.Result) {
	opcode := rowMap(replayed.Sections["execution_callsite_witnesses"][0])
	proof := rowMap(replayed.Sections["replays"][0])
	text := ca.Row(map[string]any{"schema_version": "contract-opcode-evidence-v1", "tx_hash": proof["tx_hash"], "block_hash": proof["block_hash"], "complete_replay": true, "original_call_trace_matches": true, "normalized": map[string]any{"calls": []any{opcode}}})
	replayed.Sections["artifacts"] = []json.RawMessage{ca.Row(map[string]any{"artifact_id": opcode["artifact_id"], "kind": "opcode_witness", "text": string(text), "sha256": ca.SHA256(text)})}
}
func TestContractFactoryCreationBinding(t *testing.T) {
	for _, kind := range []string{"CREATE", "CREATE2"} {
		t.Run(kind, func(t *testing.T) {
			for _, scenario := range []string{"good", "missing_replay", "failed_receipt", "wrong_chain", "source_initcode", "canonical_arguments", "runtime", "reverted_child", "reverted_ancestor", "replay_frame", "duplicate_creation", "missing_opcode", "opcode_identity", "opcode_parent_storage", "opcode_parent_code", "opcode_parent_path", "opcode_phase", "opcode_hash", "opcode_order", "opcode_negative_pc", "opcode_pc", "artifact_hash", "artifact_record", "changed_salt"} {
				t.Run(scenario, func(t *testing.T) {
					snapshot, model, w, replayed := contractFactoryTestFixture(t, kind)
					root := contractCreationMap(w["call_trace"])
					child := contractCreationMap(root["calls"].([]any)[0])
					proof := rowMap(replayed.Sections["replays"][0])
					opcode := rowMap(replayed.Sections["execution_callsite_witnesses"][0])
					switch scenario {
					case "missing_replay":
						proof["complete"] = false
					case "failed_receipt":
						contractCreationMap(w["raw_receipt"])["status"] = "0x0"
					case "wrong_chain":
						contractCreationMap(w["raw_transaction"])["chainId"] = "0x2"
					case "source_initcode":
						contractCreationMap(model["creation_identity"])["initcode"] = "0x6002"
					case "canonical_arguments":
						contractCreationMap(model["creation_identity"])["constructor_parameters"] = []any{map[string]any{"type": "bool"}}
					case "runtime":
						model["code_hash"] = "0x" + strings.Repeat("7", 64)
					case "reverted_child":
						child["error"] = "execution reverted"
					case "reverted_ancestor":
						root["calls"] = []any{map[string]any{"type": "CALL", "from": root["to"], "to": child["from"], "input": "0x", "output": "0x", "error": "execution reverted", "calls": []any{child}}}
					case "replay_frame":
						actual := contractCreationMap(proof["call_trace"])
						contractCreationMap(actual["calls"].([]any)[0])["input"] = "0x00"
					case "duplicate_creation":
						root["calls"] = append(root["calls"].([]any), child)
					case "missing_opcode":
						replayed.Sections["execution_callsite_witnesses"] = nil
					case "opcode_identity":
						opcode["execution_opcode_identity_verified"] = false
					case "opcode_parent_storage":
						opcode["caller_storage_address"] = "0x" + strings.Repeat("8", 40)
					case "opcode_parent_code":
						opcode["caller_code_address"] = "0x" + strings.Repeat("8", 40)
					case "opcode_parent_path":
						opcode["parent_trace_path"] = "0.1"
					case "opcode_phase":
						opcode["caller_code_phase"] = "creation"
					case "opcode_hash":
						opcode["caller_code_hash"] = nil
					case "opcode_order":
						opcode["instruction_order"] = 20000
					case "opcode_negative_pc":
						opcode["pc"] = -1
					case "opcode_pc":
						opcode["pc"] = 999
					case "artifact_hash":
						artifact := rowMap(replayed.Sections["artifacts"][0])
						artifact["sha256"] = "bad"
						replayed.Sections["artifacts"][0] = ca.Row(artifact)
					case "artifact_record":
						opcode["opcode_call_id"] = "not-recorded"
					case "changed_salt":
						if kind == "CREATE2" {
							opcode["create2_salt_raw"] = "8"
						} else {
							opcode["input_memory_matches"] = false
						}
					}
					replayed.Sections["replays"][0] = ca.Row(proof)
					if scenario != "missing_opcode" {
						replayed.Sections["execution_callsite_witnesses"][0] = ca.Row(opcode)
					}
					if scenario == "changed_salt" || strings.HasPrefix(scenario, "opcode_parent_") || scenario == "opcode_phase" || scenario == "opcode_hash" || scenario == "opcode_order" || scenario == "opcode_negative_pc" {
						contractFactoryTestRefreshArtifact(&replayed)
					}
					binding, e := contractCreationBinding(snapshot, model, ca.Row(w), replayed)
					if scenario != "good" {
						if e == nil {
							t.Fatalf("accepted %s %s: %+v", kind, scenario, binding)
						}
						return
					}
					if e != nil || binding["complete"] != true || binding["scope"] != "one_recompiled_and_replayed_factory_creation_frame" || binding["later_history_and_upgrades_verified"] != false || binding["snapshot_lifecycle_continuity_verified"] != false {
						t.Fatalf("binding %+v error %v", binding, e)
					}
				})
			}
		})
	}
}
func TestContractCreate2AddressEIP1014(t *testing.T) {
	address, e := contractCreate2Address("0x"+strings.Repeat("0", 40), "0", []byte{0})
	if e != nil || address != "0x4d1a2e2bb4f88f0250f26ffff098b0b30b26bf38" {
		t.Fatalf("address %s: %v", address, e)
	}
}
