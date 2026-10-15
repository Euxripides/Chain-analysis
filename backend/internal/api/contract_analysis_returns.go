package api

import (
	"encoding/json"
	"fmt"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

// A returned value belongs to one exact execution frame. Source callsites are
// candidates without a program-counter witness; they never receive a global
// constant binding from a transaction or an end-of-block snapshot.
func contractEnrichExternalReturns(result *ca.Result) {
	methods := map[string]map[string]any{}
	frames := map[string]map[string]any{}
	transactions := map[string]map[string]any{}
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		methods[fmt.Sprint(m["method_id"])] = m
	}
	for _, raw := range result.Sections["execution_calls"] {
		c := rowMap(raw)
		frames[fmt.Sprint(c["tx_hash"])+":"+fmt.Sprint(c["trace_path"])] = c
	}
	for _, raw := range result.Sections["transactions"] {
		t := rowMap(raw)
		transactions[fmt.Sprint(t["tx_hash"])] = t
	}
	result.Sections["external_return_observations"] = []json.RawMessage{}
	for _, raw := range result.Sections["execution_calls"] {
		c := rowMap(raw)
		if c["call_type"] != "CALL" && c["call_type"] != "STATICCALL" && c["call_type"] != "DELEGATECALL" && c["call_type"] != "CALLCODE" {
			continue
		}
		tx := transactions[fmt.Sprint(c["tx_hash"])]
		observation := map[string]any{"observation_id": ca.ID(fmt.Sprint(c["call_id"]), "return"), "execution_call_id": c["call_id"], "tx_hash": c["tx_hash"],
			"trace_path": c["trace_path"], "call_type": c["call_type"], "code_address": c["code_address"], "code_hash": c["code_hash"], "storage_address": c["storage_address"],
			"effective_caller": c["effective_caller"], "value_raw": c["value_raw"], "input": c["input"], "return_data": c["output"],
			"block_number": tx["block_number"], "block_hash": tx["block_hash"], "transaction_index": tx["transaction_index"],
			"settled": c["settled"], "evidence_ids": c["evidence_ids"], "scope": "execution_frame_only", "status": "raw_return_without_verified_output_schema",
			"source_binding_status": "requires_program_counter_witness", "candidate_dependency_ids": []string{}}
		path := fmt.Sprint(c["trace_path"])
		if pos := strings.LastIndex(path, "."); pos >= 0 {
			parent := frames[fmt.Sprint(c["tx_hash"])+":"+path[:pos]]
			parentIDs := stringList(parent["method_ids"])
			candidates := []string{}
			for _, depRaw := range result.Sections["external_returns"] {
				dep := rowMap(depRaw)
				callerMatches := false
				for _, mid := range parentIDs {
					if dep["method_id"] == mid {
						callerMatches = true
					}
				}
				if callerMatches && method.SelectorOf(fmt.Sprint(dep["signature"])) == c["selector"] && dep["call_type"] == c["call_type"] {
					candidates = append(candidates, fmt.Sprint(dep["dependency_id"]))
				}
			}
			observation["candidate_dependency_ids"] = candidates
		}
		ids := stringList(c["method_ids"])
		if c["error"] != "" && c["error"] != nil {
			observation["status"] = "reverted_frame_return_data"
		} else if len(ids) == 1 && c["method_identity_status"] == "transaction_prestate_code_and_storage_context" {
			m := methods[ids[0]]
			if m != nil && m["verified_signature"] == true && m["code_hash"] == c["code_hash"] && m["code_address"] == c["code_address"] && m["storage_address"] == c["storage_address"] {
				observation["method_id"] = ids[0]
				var outputs []map[string]any
				if nextDecode(ca.Row(m["outputs"]), &outputs) == nil && len(outputs) > 0 {
					types := []string{}
					for _, o := range outputs {
						t := fmt.Sprint(o["type"])
						for _, suffix := range []string{" memory", " calldata", " storage", " ref", " pointer"} {
							t = strings.ReplaceAll(t, suffix, "")
						}
						if strings.HasPrefix(t, "contract ") {
							suffix := ""
							if at := strings.IndexByte(t, '['); at >= 0 {
								suffix = t[at:]
							}
							t = "address" + suffix
						}
						t = strings.ReplaceAll(t, "address payable", "address")
						types = append(types, t)
					}
					observation["return_types"] = types
					abi, err := signatureMethod(fmt.Sprint(m["signature"]), types)
					if err == nil {
						var decoded []method.DecodedParam
						decoded, err = method.DecodeOutput(abi, fmt.Sprint(c["output"]))
						if err == nil {
							observation["decoded_values"] = decoded
							observation["status"] = "decoded_execution_frame_return"
							if c["settled"] != true {
								observation["status"] = "decoded_return_on_reverted_ancestor_path"
							}
						} else {
							observation["status"] = "return_data_does_not_match_verified_schema"
						}
					} else {
						observation["status"] = "output_schema_requires_tuple_or_custom_type_resolution"
					}
				}
			}
		}
		result.Sections["external_return_observations"] = append(result.Sections["external_return_observations"], ca.Row(observation))
	}
}
