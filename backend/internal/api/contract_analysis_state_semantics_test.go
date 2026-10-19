package api

import (
	"encoding/json"
	"strings"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func TestContractStateSemanticsScopedReferencesAndPrecision(t *testing.T) {
	original, scoped := ca.ID("source-file"), ca.ID("source-file", "snapshot")
	declarations := "(declare-const " + original + " Int)"
	raw := ca.Row(map[string]any{
		"schema_version": "source-state-transition-v1", "model_id": ca.ID("model"), "method_id": ca.ID("method"),
		"declarations": declarations, "opaque_value": original, "precise": json.Number("9007199254740993"),
		"paths":          []any{map[string]any{"writes": []any{map[string]any{"source_location": map[string]any{"artifact_id": original, "start": 17}}}}},
		"reverted_paths": []any{map[string]any{"attempted_writes": []any{map[string]any{"source_location": map[string]any{"artifact_id": original}}}}},
	})
	data, err := contractScopeStateSemantics(raw, map[string]string{original: scoped})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = nextDecode(data, &result); err != nil {
		t.Fatal(err)
	}
	if result["declarations"] != declarations || result["opaque_value"] != original || result["precise"].(json.Number).String() != "9007199254740993" {
		t.Fatal("changed opaque declarations or integer precision")
	}
	for _, key := range []string{"paths", "reverted_paths"} {
		path := result[key].([]any)[0].(map[string]any)
		writes := path["writes"]
		if key == "reverted_paths" {
			writes = path["attempted_writes"]
		}
		location := writes.([]any)[0].(map[string]any)["source_location"].(map[string]any)
		if location["artifact_id"] != scoped {
			t.Fatal("embedded reference differs from the published source identity")
		}
	}
	if ca.SHA256(data) == ca.SHA256(raw) {
		t.Fatal("rewritten bytes require a new saved checksum")
	}
	if _, err = contractScopeStateSemantics(data, map[string]string{original: scoped}); err != nil {
		t.Fatalf("known scoped references must remain valid: %v", err)
	}
}

func TestContractStateSemanticsRejectUnknownReferences(t *testing.T) {
	valid := `{"schema_version":"source-state-transition-v1","model_id":"model","method_id":"method"}`
	for name, raw := range map[string]string{
		"malformed": "{", "extra": valid + "{}", "schema": strings.ReplaceAll(valid, "source-state-transition-v1", "other"),
		"identity":       strings.ReplaceAll(valid, `"model"`, `""`),
		"unknown-source": strings.TrimSuffix(valid, "}") + `,"source_location":{"artifact_id":"unknown"}}`,
		"invalid-source": strings.TrimSuffix(valid, "}") + `,"source_location":{"artifact_id":42}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := contractScopeStateSemantics([]byte(raw), map[string]string{}); err == nil {
				t.Fatal("accepted invalid state semantics or a dangling source reference")
			}
		})
	}
}
