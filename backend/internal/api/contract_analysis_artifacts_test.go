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
