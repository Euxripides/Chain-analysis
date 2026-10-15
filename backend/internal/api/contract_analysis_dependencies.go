package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func contractDependencyOrder(value any) (int, bool) {
	switch n := value.(type) {
	case json.Number:
		v, e := strconv.Atoi(n.String())
		return v, e == nil
	case int:
		return n, true
	case float64:
		v := int(n)
		return v, float64(v) == n
	default:
		return 0, false
	}
}

// Addresses come from typed expressions and the exact saved state context.
// No RPC, current-state substitution or address extraction from arbitrary text.
func contractEnrichCallTargets(result *ca.Result) {
	expressions := map[string]map[string]any{}
	definitions := map[string]map[string]any{}
	derivations := map[string]map[string]any{}
	for _, raw := range result.Sections["expressions"] {
		r := rowMap(raw)
		expressions[fmt.Sprint(r["expression_id"])] = r
	}
	for _, raw := range result.Sections["definitions"] {
		r := rowMap(raw)
		definitions[fmt.Sprint(r["definition_id"])] = r
	}
	for _, raw := range result.Sections["dataflow"] {
		r := rowMap(raw)
		derivations[fmt.Sprint(r["dataflow_id"])] = r
	}
	for i, raw := range result.Sections["calls"] {
		call := rowMap(raw)
		call["target_resolution_status"] = "dynamic_or_unobserved_target"
		target := fmt.Sprint(call["target_expression"])
		address, basis := "", ""
		observations := []map[string]any{}
		if ca.CompleteAddress.MatchString(target) {
			address = strings.ToLower(target)
			basis = "represented_complete_address"
		}
		selected := map[string]map[string]any{}
		derivation := derivations[fmt.Sprint(call["target_derivation_id"])]
		for _, key := range stringList(derivation["definition_ids"]) {
			if d := definitions[key]; d != nil {
				selected[key] = d
			}
		}
		visits := 0
		var resolve func(string, map[string]bool, int) (string, string)
		resolve = func(key string, seen map[string]bool, before int) (string, string) {
			if seen[key] || len(seen) > 32 || visits > 512 {
				return "", ""
			}
			visits++
			next := map[string]bool{}
			for k, v := range seen {
				next[k] = v
			}
			next[key] = true
			e := expressions[key]
			if e == nil {
				return "", ""
			}
			if v, ok := e["resolved_value"].(string); ok && ca.CompleteAddress.MatchString(v) {
				return strings.ToLower(v), fmt.Sprint(e["value_basis"])
			}
			if e["op"] == "Literal" {
				if v, ok := e["raw_literal"].(string); ok && ca.CompleteAddress.MatchString(v) {
					return strings.ToLower(v), "verified_source_literal"
				}
			}
			if bound, ok := e["binding_expression_id"].(string); ok && bound != "" {
				return resolve(bound, next, before)
			}
			operands := stringList(e["operands"])
			if e["call_kind"] == "typeConversion" && e["op"] == "FunctionCall" && (strings.HasPrefix(fmt.Sprint(e["type"]), "address") || strings.HasPrefix(fmt.Sprint(e["type"]), "contract ")) {
				arguments := stringList(e["argument_expression_ids"])
				if e["argument_expression_ids"] == nil {
					for _, k := range operands {
						child := expressions[k]
						if child != nil && !strings.HasPrefix(fmt.Sprint(child["type"]), "type(") {
							arguments = append(arguments, k)
						}
					}
				}
				if len(arguments) == 1 {
					return resolve(arguments[0], next, before)
				}
				return "", ""
			}
			if e["symbolic_format"] == "return_cases" {
				var paths []map[string]any
				if nextDecode(ca.Row(e["paths"]), &paths) == nil && len(paths) == 1 {
					var conditions []any
					if nextDecode(ca.Row(paths[0]["conditions"]), &conditions) == nil && len(conditions) == 0 {
						return resolve(fmt.Sprint(paths[0]["expression_id"]), next, before)
					}
				}
			}
			if e["op"] != "Identifier" {
				return "", ""
			}
			if result.Snapshot.StateGranularity == "block_end" {
				found := map[string]bool{}
				observed := []map[string]any{}
				for _, raw := range result.Sections["storage"] {
					read := rowMap(raw)
					value, ok := read["snapshot_address"].(string)
					if !ok || !ca.CompleteAddress.MatchString(value) || read["method_id"] != e["method_id"] || read["slot_role"] != "direct_slot" || read["access"] != "read" || read["snapshot_read_status"] != "slot_word" || fmt.Sprint(read["state_ast_id"]) != fmt.Sprint(e["referenced_declaration"]) {
						continue
					}
					if read["storage_address"] != result.Snapshot.StorageAddress || read["snapshot_block_hash"] != result.Snapshot.BlockHash || fmt.Sprint(read["snapshot_block_number"]) != fmt.Sprint(result.Snapshot.BlockNumber) {
						continue
					}
					if !strings.HasPrefix(fmt.Sprint(read["type"]), "address") && !strings.HasPrefix(fmt.Sprint(read["type"]), "contract ") {
						continue
					}
					found[strings.ToLower(value)] = true
					observed = append(observed, map[string]any{"storage_id": read["storage_id"], "storage_address": read["storage_address"], "slot": read["slot"], "offset": read["offset"], "slot_hex": read["snapshot_slot_hex"], "block_number": read["snapshot_block_number"], "block_hash": read["snapshot_block_hash"], "evidence_ids": read["evidence_ids"]})
				}
				if len(found) == 1 {
					observations = append(observations, observed...)
					for v := range found {
						return v, "verified_layout_and_pinned_slot"
					}
				}
				if len(found) > 1 {
					call["target_resolution_status"] = "conflicting_pinned_address_values"
					return "", ""
				}
			}
			if derivation["status"] != "represented_source_dependencies_resolved" {
				return "", ""
			}
			order := before
			if n, ok := contractDependencyOrder(e["step_order"]); ok && n < order {
				order = n
			}
			chosen := map[string]any(nil)
			chosenOrder := -1
			for _, d := range selected {
				n, ok := contractDependencyOrder(d["order"])
				if !ok || n >= order || n <= chosenOrder || d["method_id"] != e["method_id"] || fmt.Sprint(d["declaration_id"]) != fmt.Sprint(e["referenced_declaration"]) || d["operation"] != "=" {
					continue
				}
				chosen = d
				chosenOrder = n
			}
			if chosen != nil {
				return resolve(fmt.Sprint(chosen["value_expression_id"]), next, chosenOrder)
			}
			return "", ""
		}
		if address == "" {
			address, basis = resolve(fmt.Sprint(call["target_expression_id"]), map[string]bool{}, int(^uint(0)>>1))
		}
		call["caller_storage_address"] = result.Snapshot.StorageAddress
		call["caller_code_address"] = result.Snapshot.CodeAddress
		call["caller_block_hash"] = result.Snapshot.BlockHash
		if address != "" {
			call["resolved_target_address"] = address
			call["target_resolution_status"] = "resolved_saved_expression_or_state"
			call["target_resolution_basis"] = basis
			call["target_snapshot_observations"] = observations
		}
		result.Sections["calls"][i] = ca.Row(call)
	}
}

func contractCallStorageContext(call map[string]any, caller ca.Snapshot, address string) string {
	if call["call_type"] == "DELEGATECALL" || call["call_type"] == "CALLCODE" {
		return caller.StorageAddress
	}
	return address
}

func contractLinkDependencies(result *ca.Result) {
	calls := map[string]map[string]any{}
	for i, raw := range result.Sections["calls"] {
		c := rowMap(raw)
		address, _ := c["resolved_target_address"].(string)
		if address != "" {
			caller := ca.Snapshot{StorageAddress: fmt.Sprint(c["caller_storage_address"])}
			storage := contractCallStorageContext(c, caller, address)
			c["dependency_storage_address"] = storage
			c["dependency_status"] = "target_code_not_analyzed_in_context"
			for _, raw := range result.Sections["contracts"] {
				contract := rowMap(raw)
				if !strings.EqualFold(fmt.Sprint(contract["code_address"]), address) || !strings.EqualFold(fmt.Sprint(contract["storage_address"]), storage) || contract["block_hash"] != c["caller_block_hash"] {
					continue
				}
				c["dependency_code_hash"] = contract["code_hash"]
				c["dependency_code_address"] = contract["code_address"]
				c["dependency_status"] = "analyzed_code_and_storage_context"
				methodIDs := []string{}
				for _, raw := range result.Sections["methods"] {
					m := rowMap(raw)
					if m["code_hash"] != contract["code_hash"] || !strings.EqualFold(fmt.Sprint(m["code_address"]), address) || !strings.EqualFold(fmt.Sprint(m["storage_address"]), storage) || m["dispatch_role"] == "overridden_source_definition" {
						continue
					}
					selector, _ := c["selector"].(string)
					if selector != "" && m["selector"] == selector {
						methodIDs = append(methodIDs, fmt.Sprint(m["method_id"]))
					}
				}
				c["resolved_target_method_ids"] = methodIDs
				result.Sections["flows"] = append(result.Sections["flows"], ca.Row(map[string]any{"edge_id": ca.ID(fmt.Sprint(c["call_id"]), "resolved-dependency"), "edge_type": "resolved_call_dependency", "from": c["method_id"], "to": address, "call_id": c["call_id"], "call_type": c["call_type"], "code_hash": contract["code_hash"], "storage_address": storage, "target_method_ids": methodIDs, "evidence_ids": c["evidence_ids"]}))
				break
			}
		}
		calls[fmt.Sprint(c["call_id"])] = c
		result.Sections["calls"][i] = ca.Row(c)
	}
	for i, raw := range result.Sections["method_details"] {
		d := rowMap(raw)
		var items []map[string]any
		if nextDecode(ca.Row(d["external_calls"]), &items) != nil {
			continue
		}
		for _, item := range items {
			c := calls[fmt.Sprint(item["call_id"])]
			for _, key := range []string{"expanded_target_expression", "resolved_target_address", "target_resolution_status", "target_resolution_basis", "target_snapshot_observations", "dependency_code_hash", "dependency_code_address", "dependency_storage_address", "dependency_status", "resolved_target_method_ids"} {
				if c[key] != nil {
					item[key] = c[key]
				}
			}
		}
		d["external_calls"] = items
		result.Sections["method_details"][i] = ca.Row(d)
	}
}
