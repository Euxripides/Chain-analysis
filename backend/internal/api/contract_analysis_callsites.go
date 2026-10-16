package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

// A callsite association needs a complete, identity-checked opcode witness and
// one compiler-mapped AST call in the actual caller's code and storage context.
func contractBindReturnCallsites(result *ca.Result) {
	frames := map[string]map[string]any{}
	deps := map[string]map[string]any{}
	maps := map[string][]map[string]any{}
	witnesses := map[string][]map[string]any{}
	for _, raw := range result.Sections["execution_calls"] {
		f := rowMap(raw)
		frames[fmt.Sprint(f["tx_hash"])+":"+fmt.Sprint(f["trace_path"])] = f
	}
	for _, raw := range result.Sections["external_returns"] {
		d := rowMap(raw)
		deps[fmt.Sprint(d["dependency_id"])] = d
	}
	key := func(code, address, storage, pc any) string {
		return fmt.Sprint(code) + ":" + fmt.Sprint(address) + ":" + fmt.Sprint(storage) + ":" + fmt.Sprint(pc)
	}
	for _, raw := range result.Sections["source_pc_maps"] {
		m := rowMap(raw)
		k := key(m["code_hash"], m["code_address"], m["storage_address"], m["pc"])
		maps[k] = append(maps[k], m)
	}
	for _, raw := range result.Sections["execution_callsite_witnesses"] {
		w := rowMap(raw)
		k := fmt.Sprint(w["tx_hash"]) + ":" + fmt.Sprint(w["trace_path"])
		witnesses[k] = append(witnesses[k], w)
	}
	for index, raw := range result.Sections["external_return_observations"] {
		o := rowMap(raw)
		path := fmt.Sprint(o["trace_path"])
		pos := strings.LastIndex(path, ".")
		if pos < 0 {
			continue
		}
		tx := fmt.Sprint(o["tx_hash"])
		if !ca.CompleteHash.MatchString(tx) || !ca.CompleteHash.MatchString(fmt.Sprint(o["block_hash"])) {
			continue
		}
		parent := frames[tx+":"+path[:pos]]
		if parent == nil || parent["method_identity_status"] != "transaction_prestate_code_and_storage_context" {
			continue
		}
		matches := witnesses[tx+":"+path]
		if len(matches) != 1 {
			continue
		}
		w := matches[0]
		witnessIndex, witnessIndexErr := ca.RawInteger(fmt.Sprint(w["transaction_index"]))
		observedIndex, observedIndexErr := ca.RawInteger(fmt.Sprint(o["transaction_index"]))
		witnessValue, witnessValueErr := ca.RawInteger(fmt.Sprint(w["value_raw"]))
		observedValue, observedValueErr := ca.RawInteger(fmt.Sprint(o["value_raw"]))
		if w["verified"] != true || w["execution_identity_verified"] != true || w["caller_code_phase"] != "runtime" ||
			w["input_memory_matches"] != true || w["bytecode_opcode_matches"] != true || w["call_type"] != o["call_type"] ||
			w["parent_trace_path"] != path[:pos] || w["caller_code_hash"] != parent["code_hash"] || w["caller_code_address"] != parent["code_address"] ||
			w["caller_storage_address"] != parent["storage_address"] || w["target"] != o["code_address"] ||
			w["input"] != o["input"] || w["return_data"] != o["return_data"] || w["block_hash"] != o["block_hash"] || w["settled"] != o["settled"] ||
			w["effective_caller"] != o["effective_caller"] ||
			witnessIndexErr != nil || observedIndexErr != nil || witnessIndex != observedIndex || witnessValueErr != nil || observedValueErr != nil || witnessValue != observedValue {
			continue
		}
		if _, err := strconv.ParseUint(fmt.Sprint(w["pc"]), 10, 64); err != nil {
			continue
		}
		candidates := map[string]map[string]any{}
		for _, m := range maps[key(w["caller_code_hash"], w["caller_code_address"], w["caller_storage_address"], w["pc"])] {
			if m["status"] != "unique_source_callsite" || m["call_type"] != w["call_type"] {
				continue
			}
			for _, did := range stringList(m["source_dependency_ids"]) {
				d := deps[did]
				if d == nil {
					continue
				}
				for _, mid := range stringList(parent["method_ids"]) {
					if d["method_id"] == mid && d["call_type"] == w["call_type"] {
						candidates[did] = d
					}
				}
			}
		}
		groups := map[string][]string{}
		for did, d := range candidates {
			identity, _ := d["source_callsite_id"].(string)
			if identity == "" {
				identity = did
			}
			groups[identity] = append(groups[identity], did)
		}
		if len(groups) != 1 {
			continue
		}
		for identity, ids := range groups {
			sort.Strings(ids)
			canonical := candidates[ids[0]]
			primary := ""
			for _, did := range ids {
				if candidates[did]["representation_scope"] == "source_statement" {
					if primary != "" {
						primary = ""
						break
					}
					primary = did
				}
			}
			if len(ids) == 1 {
				primary = ids[0]
			}
			o["source_binding_status"] = "exact_program_counter_and_execution_context"
			o["source_callsite_id"] = identity
			o["source_dependency_ids"] = ids
			o["caller_method_id"] = canonical["method_id"]
			o["source_callsite"] = canonical["source_callsite"]
			if primary != "" {
				d := candidates[primary]
				o["source_dependency_id"] = primary
				o["source_expression_id"] = d["expression_id"]
				o["caller_method_id"] = d["method_id"]
				o["source_callsite"] = d["source_callsite"]
			} else {
				o["expression_binding_status"] = "source_callsite_known_internal_path_context_required"
			}
			o["program_counter"] = w["pc"]
			o["opcode_call_id"] = w["opcode_call_id"]
			o["opcode_witness_id"] = w["witness_id"]
			o["opcode_artifact_id"] = w["artifact_id"]
			ids := append(stringList(o["evidence_ids"]), stringList(w["evidence_ids"])...)
			o["evidence_ids"] = ids
		}
		result.Sections["external_return_observations"][index] = ca.Row(o)
	}
}
