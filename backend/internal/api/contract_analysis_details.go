package api

import (
	"fmt"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

// Large detail views repeat the same already paginated steps and effects.
// Preserve the original view as an artifact and expose exact section references.
// No executable row, amount, condition or source evidence is discarded.
func contractCompactMethodDetails(result *ca.Result) error {
	fields := map[string]string{"sequence": "steps", "conditions": "permissions", "asset_effects": "funds", "state_reads": "storage", "state_writes": "storage", "external_calls": "calls", "returned_values": "dataflow", "calculation_rules": "expressions"}
	for i, raw := range result.Sections["method_details"] {
		if len(raw) <= 1<<20 {
			continue
		}
		if len(raw) > ca.ArtifactLimit {
			return fmt.Errorf("method detail view exceeds artifact limit")
		}
		row := rowMap(raw)
		mid := fmt.Sprint(row["method_id"])
		artifactID := ca.ID("complete-method-details", mid, ca.SHA256(raw))
		result.Sections["artifacts"] = append(result.Sections["artifacts"], ca.Row(ca.Artifact{ArtifactID: artifactID, Kind: "complete_method_details", Filename: "method-" + mid + "-details.json", MediaType: "application/json", SHA256: ca.SHA256(raw), Text: string(raw), MethodID: mid}))
		refs := map[string]any{}
		for field, section := range fields {
			values, _ := row[field].([]any)
			refs[field] = map[string]any{"section": section, "method_id": mid, "represented_items": len(values), "selection": field}
			row[field] = []any{}
		}
		row["evidence_ids"] = []string{}
		row["evidence_reference"] = map[string]any{"source_sections": []string{"steps", "funds", "storage", "permissions", "calls"}, "method_id": mid, "lookup": "follow_each_saved_row_evidence_ids"}
		row["field_references"] = refs
		row["representation"] = "complete_view_in_artifact_and_paginated_method_sections"
		row["full_view_artifact_id"] = artifactID
		row["full_view_sha256"] = ca.SHA256(raw)
		row["full_view_byte_size"] = len(raw)
		result.Sections["method_details"][i] = ca.Row(row)
		if len(result.Sections["method_details"][i]) > 4<<20 {
			return fmt.Errorf("method detail scalar fields exceed row budget")
		}
	}
	return nil
}
