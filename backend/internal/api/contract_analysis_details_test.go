package api

import (
	"bytes"
	"encoding/json"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"strings"
	"testing"
)

func TestLargeMethodDetailRetainsOriginalBytesAndEveryPagedStep(t *testing.T) {
	sequence := make([]map[string]any, 450)
	steps := make([]json.RawMessage, 450)
	for i := range sequence {
		sequence[i] = map[string]any{"step_id": ca.ID("step", strings.Repeat("x", i)), "method_id": "method", "order": i, "operation": strings.Repeat("amount >= limit; ", 700)}
		steps[i] = ca.Row(sequence[i])
	}
	original := ca.Row(map[string]any{"method_id": "method", "sequence": sequence, "schema_version": "contract-method-details-v1", "asset_effects": []any{map[string]any{"amount": "900719925474099312345678901234567890", "recipient": "0x" + strings.Repeat("a", 40)}}})
	result := ca.Result{Sections: map[string][]json.RawMessage{"method_details": {original}, "steps": steps, "artifacts": {}}}
	if err := contractCompactMethodDetails(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Sections["method_details"][0]) > 1<<20 || len(result.Sections["steps"]) != 450 {
		t.Fatal("bounded view lost steps")
	}
	var artifact ca.Artifact
	if err := json.Unmarshal(result.Sections["artifacts"][0], &artifact); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(artifact.Text), original) || artifact.SHA256 != ca.SHA256(original) {
		t.Fatal("original details or precision changed")
	}
	row := rowMap(result.Sections["method_details"][0])
	if row["full_view_sha256"] != artifact.SHA256 || row["representation"] == nil {
		t.Fatal("complete view reference missing")
	}
}
