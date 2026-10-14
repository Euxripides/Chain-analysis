package api

import (
	"encoding/json"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"strings"
	"testing"
)

func TestContractAuthoritySnapshotIdentity(t *testing.T) {
	address := "0x" + strings.Repeat("a", 40)
	snapshot := ca.Snapshot{BlockNumber: 17, BlockHash: "0x" + strings.Repeat("1", 64), StorageAddress: "0x" + strings.Repeat("2", 40), StateGranularity: "block_end"}
	for _, scenario := range []string{"observed", "stale_block", "wrong_storage_context", "transaction_prestate", "unknown_state", "opaque_expression", "conflict"} {
		t.Run(scenario, func(t *testing.T) {
			read := map[string]any{"storage_id": "owner-read", "storage_address": snapshot.StorageAddress, "snapshot_address": address, "snapshot_read_status": "slot_word", "snapshot_block_number": 17, "snapshot_block_hash": snapshot.BlockHash, "slot": "0", "offset": 0, "snapshot_slot_hex": "0x" + strings.Repeat("0", 24) + address[2:]}
			c := map[string]any{"constraint_id": "caller", "principal": "msg.sender", "relation": "equals", "condition_scope": "whole_guard", "target_value_resolution": "direct_state_address", "state_read_ids": []string{"owner-read"}}
			result := ca.Result{Snapshot: snapshot, Sections: map[string][]json.RawMessage{"storage": {ca.Row(read)}, "permissions": {ca.Row(map[string]any{"guard_id": "guard", "caller_constraints": []any{c}})}, "control_effects": {ca.Row(map[string]any{"guard_ids": []string{"guard"}, "current_controller_status": "requires_pinned_state"})}}}
			switch scenario {
			case "stale_block":
				read["snapshot_block_hash"] = "0x" + strings.Repeat("3", 64)
			case "wrong_storage_context":
				read["storage_address"] = "0x" + strings.Repeat("4", 40)
			case "transaction_prestate":
				result.Snapshot.StateGranularity = "transaction_prestate"
			case "unknown_state":
				delete(read, "snapshot_address")
			case "opaque_expression":
				c["target_value_resolution"] = "expression_requires_evaluation"
			case "conflict":
				second := map[string]any{}
				for k, v := range read {
					second[k] = v
				}
				second["storage_id"] = "other-read"
				second["snapshot_address"] = "0x" + strings.Repeat("b", 40)
				result.Sections["storage"] = append(result.Sections["storage"], ca.Row(second))
				c["state_read_ids"] = []string{"owner-read", "other-read"}
			}
			result.Sections["storage"][0] = ca.Row(read)
			result.Sections["permissions"][0] = ca.Row(map[string]any{"guard_id": "guard", "caller_constraints": []any{c}})
			contractEnrichAuthoritySnapshot(&result)
			guard := rowMap(result.Sections["permissions"][0])
			var cs []map[string]any
			if e := nextDecode(ca.Row(guard["caller_constraints"]), &cs); e != nil || len(cs) != 1 {
				t.Fatal(e, guard)
			}
			if scenario == "observed" {
				if cs[0]["snapshot_target_address"] != address || cs[0]["snapshot_resolution_status"] != "observed_direct_state_address" {
					t.Fatal(cs)
				}
				if rowMap(result.Sections["control_effects"][0])["current_controller_status"] != "observed_address_conditions" {
					t.Fatal("control effect was not enriched")
				}
			} else if cs[0]["snapshot_target_address"] != nil {
				t.Fatalf("incorrect address assertion for %s: %v", scenario, cs)
			}
			if scenario == "conflict" && cs[0]["snapshot_resolution_status"] != "conflicting_pinned_address_values" {
				t.Fatal(cs)
			}
			if cs[0]["authorization_sufficiency_verified"] != false {
				t.Fatal("address comparison became permission proof")
			}
		})
	}
}
