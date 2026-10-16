package api

import (
	"encoding/json"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func TestContractPropertyProofScopedIdentity(t *testing.T) {
	original, scoped := ca.ID("proof"), ca.ID("proof", "snapshot")
	property := map[string]any{"property_id": "property-1", "proof_artifact_id": original, "status": "proved_under_declared_model"}
	query := map[string]any{"property_id": "property-1", "declarations": "(set-option :timeout 300)", "inputs": []any{}}
	raw := ca.Row(map[string]any{"schema_version": "contract-property-proof-v1", "property": property, "query": query})
	data, err := contractScopePropertyProof(raw, original, map[string]string{original: scoped})
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	property["proof_artifact_id"] = scoped
	if string(ca.Row(saved["property"])) != string(ca.Row(property)) || string(ca.Row(saved["query"])) != string(ca.Row(query)) {
		t.Fatalf("saved proof differs from the scoped property or query: %s", data)
	}
	if ca.SHA256(raw) == ca.SHA256(data) {
		t.Fatal("rewritten artifact must be saved with its new checksum")
	}
	for name, blob := range map[string][]byte{"malformed": []byte("{"), "wrong-artifact": raw, "wrong-property": ca.Row(map[string]any{"schema_version": "contract-property-proof-v1", "property": property, "query": map[string]any{"property_id": "other"}})} {
		t.Run(name, func(t *testing.T) {
			if _, err := contractScopePropertyProof(blob, scoped, map[string]string{scoped: original}); err == nil {
				t.Fatal("accepted mismatched proof identity")
			}
		})
	}
}

func TestContractPCMapScopedIdentity(t *testing.T) {
	original, scoped := ca.ID("pc-map"), ca.ID("pc-map", "snapshot")
	raw := ca.Row(map[string]any{"schema_version": "contract-runtime-source-pc-v1", "source_map": "1:2:0;-1:0:-1", "call_instructions": []any{map[string]any{"pc": 12, "artifact_id": original}}})
	data, err := contractScopePCMap(raw, original, map[string]string{original: scoped})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document["call_instructions"].([]any)[0].(map[string]any)["artifact_id"] != scoped || document["source_map"] != "1:2:0;-1:0:-1" {
		t.Fatal(string(data))
	}
	if _, err = contractScopePCMap(raw, scoped, map[string]string{scoped: original}); err == nil {
		t.Fatal("accepted wrong PC artifact identity")
	}
}

func TestContractMergedEmptySectionExportsAsArray(t *testing.T) {
	result := ca.Result{Sections: map[string][]json.RawMessage{}}
	part := ca.Result{Sections: map[string][]json.RawMessage{"source_pc_maps": {}}}
	mergeContractResult(&result, part)
	if string(ca.Row(result.Sections["source_pc_maps"])) != "[]" {
		t.Fatalf("empty exported section differs from the paged API: %s", ca.Row(result.Sections))
	}
}
