package api

import (
	"fmt"
	"regexp"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

var contractAuthorityAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// Uses the existing bounded state reads. It does not enumerate roles or make
// current state stand in for transaction prestate.
func contractEnrichAuthoritySnapshot(result *ca.Result) {
	reads := map[string]map[string]any{}
	for _, raw := range result.Sections["storage"] {
		r := rowMap(raw)
		reads[fmt.Sprint(r["storage_id"])] = r
	}
	guards := map[string]map[string]any{}
	for i, raw := range result.Sections["permissions"] {
		guard := rowMap(raw)
		var constraints []map[string]any
		if nextDecode(ca.Row(guard["caller_constraints"]), &constraints) != nil {
			continue
		}
		for _, c := range constraints {
			c["snapshot_resolution_status"] = "requires_pinned_state_or_expression_evaluation"
			if literal, ok := c["resolved_literal"].(string); ok && contractAuthorityAddress.MatchString(literal) {
				c["snapshot_target_address"] = strings.ToLower(literal)
				c["snapshot_resolution_status"] = "verified_source_address_expression"
				c["snapshot_basis"] = c["target_resolution_basis"]
			} else if c["target_value_resolution"] == "direct_state_address" && result.Snapshot.StateGranularity == "block_end" {
				addresses := map[string]bool{}
				observations := []map[string]any{}
				for _, key := range stringList(c["state_read_ids"]) {
					r := reads[key]
					address, ok := r["snapshot_address"].(string)
					if !ok || !contractAuthorityAddress.MatchString(address) || r["snapshot_read_status"] != "slot_word" || r["snapshot_block_hash"] != result.Snapshot.BlockHash || fmt.Sprint(r["snapshot_block_number"]) != fmt.Sprint(result.Snapshot.BlockNumber) || r["storage_address"] != result.Snapshot.StorageAddress {
						continue
					}
					addresses[strings.ToLower(address)] = true
					observations = append(observations, map[string]any{"storage_id": key, "slot": r["slot"], "offset": r["offset"], "slot_hex": r["snapshot_slot_hex"], "storage_address": r["storage_address"], "block_number": r["snapshot_block_number"], "block_hash": r["snapshot_block_hash"], "evidence_ids": r["evidence_ids"]})
				}
				if len(addresses) == 1 {
					for address := range addresses {
						c["snapshot_target_address"] = address
					}
					c["snapshot_resolution_status"] = "observed_direct_state_address"
					c["snapshot_basis"] = "verified_layout_and_pinned_slot"
					c["snapshot_observations"] = observations
				} else if len(addresses) > 1 {
					c["snapshot_resolution_status"] = "conflicting_pinned_address_values"
				}
			}
			c["authorization_sufficiency_verified"] = false
		}
		guard["caller_constraints"] = constraints
		result.Sections["permissions"][i] = ca.Row(guard)
		guards[fmt.Sprint(guard["guard_id"])] = guard
	}
	for i, raw := range result.Sections["control_effects"] {
		effect := rowMap(raw)
		constraints := []map[string]any{}
		observed := 0
		for _, key := range stringList(effect["guard_ids"]) {
			var cs []map[string]any
			if nextDecode(ca.Row(guards[key]["caller_constraints"]), &cs) != nil {
				continue
			}
			for _, c := range cs {
				constraints = append(constraints, c)
				if c["snapshot_target_address"] != nil {
					observed++
				}
			}
		}
		effect["caller_constraints"] = constraints
		if observed > 0 {
			effect["current_controller_status"] = "observed_address_conditions"
		}
		effect["authorization_sufficiency_verified"] = false
		result.Sections["control_effects"][i] = ca.Row(effect)
	}
}
