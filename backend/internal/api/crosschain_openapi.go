package api

import "github.com/blockchain-platform/blockchain-platform/internal/crosschain"

func crossChainResponseName(id string) string {
	return map[string]string{"getCrossChainCapabilities": "CrossChainCapabilitiesResponse", "queryCrossChainTransfer": "CrossChainTransferResponse", "findCrossChainCandidates": "CrossChainCandidatesResponse"}[id]
}
func ccString() map[string]any               { return map[string]any{"type": "string", "maxLength": 256} }
func ccEnum(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
func ccInteger(min, max, def int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max, "default": def}
}
func ccObject(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
func ccArray(item any) map[string]any  { return map[string]any{"type": "array", "items": item} }
func ccRef(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
func ccAmount() map[string]any {
	return map[string]any{"type": "string", "maxLength": 100, "pattern": "^[0-9]+(\\.[0-9]+)?$"}
}

func crossChainRequestSchema(id string) map[string]any {
	if id == "queryCrossChainTransfer" {
		b := ccEnum(append([]string{"auto"}, crosschain.Protocols...)...)
		b["default"] = "auto"
		mode := ccEnum("funds", "discovery")
		mode["default"] = "funds"
		return ccObject(map[string]any{"bridge": b, "verification_mode": mode, "chain": ccEnum(crosschain.Chains...), "tx_hash": ccString(), "order_id": ccString(), "transfer_id": ccString(), "deposit_address": ccString(), "deposit_memo": ccString(), "max_results": ccInteger(1, 50, 20)})
	}
	if id != "findCrossChainCandidates" {
		return nil
	}
	addresses := ccArray(ccString())
	addresses["minItems"] = 1
	addresses["maxItems"] = 10
	return ccObject(map[string]any{"anchor": ccObject(map[string]any{"chain": ccEnum(crosschain.Chains...), "tx_hash": ccString(), "dataset": ccEnum("transactions", "internal_transactions", "token_transfers"), "event_index": map[string]any{"type": "integer", "minimum": 0, "maximum": 4294967295}}, "chain", "tx_hash", "dataset"), "search_chain": ccEnum(crosschain.Chains...), "search_addresses": addresses, "search_direction": ccEnum("in", "out"), "direction": ccEnum("forward", "backward"), "asset_kind": ccEnum("native", "token"), "token_contract": ccString(), "asset_relation": ccEnum("same_asset", "swap"), "expected_amount": ccAmount(), "absolute_tolerance": ccAmount(), "relative_tolerance_bps": ccInteger(0, 500, 0), "start_time": map[string]any{"type": "string", "format": "date-time"}, "end_time": map[string]any{"type": "string", "format": "date-time"}, "counterparty": ccString(), "max_parts": ccInteger(1, 3, 1), "max_results": ccInteger(1, 50, 20), "scan_limit": ccInteger(1, 1000, 500), "max_combinations": ccInteger(1, 20000, 5000)}, "anchor", "search_chain", "search_addresses", "search_direction", "direction", "asset_kind", "asset_relation", "start_time", "end_time")
}

func addCrossChainSchemas(schemas map[string]any) {
	boolean := map[string]any{"type": "boolean"}
	integer := map[string]any{"type": "integer"}
	strings := ccArray(ccString())
	loose := map[string]any{"type": "object", "additionalProperties": true}
	leg := map[string]any{"type": "object", "properties": map[string]any{"chain": ccString(), "chain_id": ccString(), "tx_hash": ccString(), "address": ccString(), "token_contract": ccString(), "token": ccString(), "amount_raw": ccString(), "amount": ccString(), "decimals": integer, "timestamp": ccString(), "amounts_scope": ccString(), "normalized_amount_raw": ccString(), "origin_chain_id": ccString(), "origin_token": ccString(), "bridge_contract": ccString(), "reported_address": ccString(), "address_scope": ccString(), "reported_token_contract": ccString(), "reported_amount_raw": ccString(), "reconciliation": loose}, "required": []string{"chain"}}
	schemas["CrossChainLeg"] = leg
	schemas["CrossChainTransfer"] = map[string]any{"type": "object", "properties": map[string]any{"bridge": ccString(), "underlying_bridge": ccString(), "order_id": ccString(), "status": ccString(), "raw_status": ccString(), "association": ccEnum("protocol_linked"), "source": ccArray(ccRef("CrossChainLeg")), "destination": ccArray(ccRef("CrossChainLeg")), "refund": ccArray(ccRef("CrossChainLeg")), "evidence": loose, "on_chain_verified": boolean, "funds_verified": boolean, "verification_state": ccEnum("verified", "pending", "unsupported", "conflict"), "verification_rule_version": ccString(), "blocking_reasons": strings, "proof": loose, "pairing_verified": boolean, "message_only": boolean, "verification": ccArray(loose)}}
	envelope := func(data map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{"success": boolean, "data": data, "meta": loose, "error": map[string]any{"type": []string{"object", "null"}}}, "required": []string{"success", "data", "meta", "error"}}
	}
	schemas["CrossChainCapabilitiesResponse"] = envelope(ccObject(map[string]any{"protocols": ccArray(loose), "chain_validation": strings, "candidate_chains": strings, "limits": loose, "candidate_matching": ccString(), "verification_modes": strings, "default_verification_mode": ccString(), "verification_rule_version": ccString(), "strict_verification": ccArray(loose)}, "protocols", "candidate_chains", "limits"))
	schemas["CrossChainTransferResponse"] = envelope(ccObject(map[string]any{"verification_mode": ccEnum("funds", "discovery"), "pending_transfers": ccArray(ccRef("CrossChainTransfer")), "transfers": ccArray(ccRef("CrossChainTransfer")), "attempts": ccArray(loose), "truncated": boolean, "searched_complete": boolean, "stopped_after_match": boolean, "protocol_hints": ccArray(loose), "candidate_searches": ccArray(loose)}, "verification_mode", "pending_transfers", "transfers", "attempts", "truncated", "searched_complete", "stopped_after_match", "protocol_hints", "candidate_searches"))
	schemas["CrossChainCandidatesResponse"] = envelope(ccObject(map[string]any{"anchor": loose, "candidates": ccArray(loose), "reference_amount": ccAmount(), "amount_basis": ccString(), "asset_relation": ccString(), "asset_identity_verified": boolean, "scanned": integer, "combinations_tested": integer, "scan_truncated": boolean, "result_truncated": boolean, "combinations_truncated": boolean}, "anchor", "candidates", "reference_amount", "scan_truncated", "result_truncated", "combinations_truncated"))
}
