package api

import (
	"encoding/json"
	"strings"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func TestContractCallTargetSavedContext(t *testing.T) {
	address := "0x" + strings.Repeat("a", 40)
	snapshot := ca.Snapshot{BlockNumber: 17, BlockHash: "0x" + strings.Repeat("1", 64), CodeAddress: "0x" + strings.Repeat("2", 40), StorageAddress: "0x" + strings.Repeat("3", 40), StateGranularity: "block_end"}
	for _, scenario := range []string{"typed_state", "alias", "cast", "cast_reverse", "cast_legacy_reverse", "immutable", "literal", "stale", "wrong_storage", "prestate", "unobserved", "opaque", "conditional_return", "conflicting", "partial_alias"} {
		t.Run(scenario, func(t *testing.T) {
			e := map[string]any{"expression_id": "state", "method_id": "method", "op": "Identifier", "referenced_declaration": 7, "type": "contract Token", "step_order": 9}
			read := map[string]any{"storage_id": "read", "method_id": "method", "state_ast_id": 7, "access": "read", "slot_role": "direct_slot", "type": "contract Token", "snapshot_address": address, "snapshot_read_status": "slot_word", "storage_address": snapshot.StorageAddress, "snapshot_block_hash": snapshot.BlockHash, "snapshot_block_number": 17}
			call := map[string]any{"call_id": "call", "method_id": "method", "call_type": "CALL", "target_expression": "token", "target_expression_id": "state", "target_derivation_id": "derivation"}
			derivation := map[string]any{"dataflow_id": "derivation", "status": "represented_source_dependencies_resolved", "definition_ids": []string{"alias-definition"}}
			r := ca.Result{Snapshot: snapshot, Sections: map[string][]json.RawMessage{"expressions": {ca.Row(e)}, "storage": {ca.Row(read)}, "calls": {ca.Row(call)}, "dataflow": {ca.Row(derivation)}}}
			switch scenario {
			case "alias", "partial_alias":
				r.Sections["expressions"] = append(r.Sections["expressions"], ca.Row(map[string]any{"expression_id": "alias", "method_id": "method", "op": "Identifier", "referenced_declaration": 8, "type": "contract Token", "step_order": 9}))
				call["target_expression_id"] = "alias"
				r.Sections["definitions"] = []json.RawMessage{ca.Row(map[string]any{"definition_id": "alias-definition", "declaration_id": 8, "method_id": "method", "order": 3, "operation": "=", "value_expression_id": "state"})}
				if scenario == "partial_alias" {
					derivation["status"] = "partial"
				}
			case "cast", "cast_reverse", "cast_legacy_reverse":
				cast := map[string]any{"expression_id": "cast", "op": "FunctionCall", "call_kind": "typeConversion", "type": "contract Token", "operands": []string{"type", "state"}, "argument_expression_ids": []string{"state"}}
				if scenario != "cast" {
					cast["operands"] = []string{"state", "type"}
				}
				if scenario == "cast_legacy_reverse" {
					delete(cast, "argument_expression_ids")
				}
				r.Sections["expressions"] = append(r.Sections["expressions"], ca.Row(map[string]any{"expression_id": "type", "op": "Identifier", "type": "type(contract Token)"}), ca.Row(cast))
				call["target_expression_id"] = "cast"
			case "immutable":
				e["resolved_value"] = address
				e["value_basis"] = "compiler_immutable_reference_and_snapshot_bytes"
				delete(read, "snapshot_address")
			case "literal":
				call["target_expression"] = address
			case "stale":
				read["snapshot_block_hash"] = "0x" + strings.Repeat("4", 64)
			case "wrong_storage":
				read["storage_address"] = "0x" + strings.Repeat("4", 40)
			case "prestate":
				r.Snapshot.StateGranularity = "transaction_prestate"
			case "unobserved":
				delete(read, "snapshot_address")
			case "opaque":
				e["op"] = "^"
				e["operands"] = []string{"state"}
			case "conditional_return":
				r.Sections["expressions"] = append(r.Sections["expressions"], ca.Row(map[string]any{"expression_id": "conditional", "symbolic_format": "return_cases", "paths": []any{map[string]any{"expression_id": "state", "conditions": []any{map[string]any{"expression_id": "condition", "expected": true}}}}}))
				call["target_expression_id"] = "conditional"
			case "conflicting":
				second := map[string]any{}
				for k, v := range read {
					second[k] = v
				}
				second["snapshot_address"] = "0x" + strings.Repeat("b", 40)
				r.Sections["storage"] = append(r.Sections["storage"], ca.Row(second))
			}
			r.Sections["expressions"][0] = ca.Row(e)
			r.Sections["storage"][0] = ca.Row(read)
			r.Sections["calls"][0] = ca.Row(call)
			r.Sections["dataflow"][0] = ca.Row(derivation)
			contractEnrichCallTargets(&r)
			got := rowMap(r.Sections["calls"][0])
			resolved := scenario == "typed_state" || scenario == "alias" || scenario == "cast" || scenario == "cast_reverse" || scenario == "cast_legacy_reverse" || scenario == "immutable" || scenario == "literal"
			if resolved && got["resolved_target_address"] != address || !resolved && got["resolved_target_address"] != nil {
				t.Fatalf("%s: %v", scenario, got)
			}
		})
	}
}

func TestContractDependencyPreservesDelegateStorage(t *testing.T) {
	code := "0x" + strings.Repeat("a", 40)
	storage := "0x" + strings.Repeat("b", 40)
	block := "0x" + strings.Repeat("1", 64)
	for _, kind := range []string{"CALL", "STATICCALL", "DELEGATECALL", "CALLCODE"} {
		t.Run(kind, func(t *testing.T) {
			expected := code
			if kind == "DELEGATECALL" || kind == "CALLCODE" {
				expected = storage
			}
			call := map[string]any{"call_id": "call", "method_id": "source", "call_type": kind, "resolved_target_address": code, "caller_storage_address": storage, "caller_block_hash": block, "selector": "0x12345678"}
			if got := contractCallStorageContext(call, ca.Snapshot{StorageAddress: storage}, code); got != expected {
				t.Fatal(got, expected)
			}
			r := ca.Result{Sections: map[string][]json.RawMessage{"calls": {ca.Row(call)}, "contracts": {ca.Row(map[string]any{"code_address": code, "storage_address": code, "code_hash": "self-hash", "block_hash": block}), ca.Row(map[string]any{"code_address": code, "storage_address": storage, "code_hash": "delegate-hash", "block_hash": block})}, "methods": {ca.Row(map[string]any{"method_id": "self", "selector": "0x12345678", "code_address": code, "storage_address": code, "code_hash": "self-hash"}), ca.Row(map[string]any{"method_id": "delegate", "selector": "0x12345678", "code_address": code, "storage_address": storage, "code_hash": "delegate-hash"})}, "method_details": {ca.Row(map[string]any{"method_id": "source", "external_calls": []any{map[string]any{"call_id": "call"}}})}}}
			contractLinkDependencies(&r)
			got := rowMap(r.Sections["calls"][0])
			if got["dependency_storage_address"] != expected || got["dependency_status"] != "analyzed_code_and_storage_context" {
				t.Fatal(got)
			}
			methods := stringList(got["resolved_target_method_ids"])
			method := "self"
			if expected == storage {
				method = "delegate"
			}
			if len(methods) != 1 || methods[0] != method {
				t.Fatal(methods, method)
			}
			if len(r.Sections["flows"]) != 1 || !strings.Contains(string(r.Sections["method_details"][0]), expected) {
				t.Fatal("dependency detail missing")
			}
		})
	}
}
