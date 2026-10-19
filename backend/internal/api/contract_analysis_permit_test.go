package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func TestContractPermitActualRuntimeProofsSurviveBackendParsingAndScoping(t *testing.T) {
	base := os.Getenv("CONTRACT_PERMIT_RUNTIME")
	if base == "" {
		t.Skip("frozen nonce EVM corpus required")
	}
	raw, err := os.ReadFile(filepath.Join(base, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Complete bool `json:"complete"`
		Runs     []struct {
			Label string `json:"label"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(raw, &report); err != nil || !report.Complete || len(report.Runs) != 40 {
		t.Fatalf("incomplete frozen runtime corpus: %v", err)
	}
	for _, run := range report.Runs {
		t.Run(run.Label, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(base, run.Label+"-analysis.json"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := ca.ParseResult(raw)
			if err != nil || result.RuleVersion != "funds-semantics-13" {
				t.Fatalf("candidate result: %v", err)
			}
			models := map[string]map[string]any{}
			for _, row := range result.Sections["authorization_models"] {
				m := rowMap(row)
				models[m["kind"].(string)] = m
			}
			nonce, permit := models["permit_nonce_monotonicity"], models["erc2612_permit_integrity"]
			if nonce == nil || permit == nil || nonce["permit_model_id"] != permit["model_id"] || nonce["code_hash"] != result.Snapshot.CodeHash || len(nonce["entry_coverage"].([]any)) != len(result.Sections["state_transitions"]) {
				t.Fatal("nonce model lost its permit, code or complete entry-coverage identity")
			}
			if nonce["whole_authorization_proved"] != false {
				t.Fatal("local nonce proposition became whole authorization")
			}
			artifacts := map[string]ca.Artifact{}
			for _, row := range result.Sections["artifacts"] {
				var a ca.Artifact
				if err := json.Unmarshal(row, &a); err != nil {
					t.Fatal(err)
				}
				artifacts[a.ArtifactID] = a
			}
			checked := 0
			for _, row := range result.Sections["verification_obligations"] {
				p := rowMap(row)
				if p["model_id"] != nonce["model_id"] {
					continue
				}
				original := p["proof_artifact_id"].(string)
				a, ok := artifacts[original]
				if !ok || a.Kind != "property_proof" || ca.SHA256([]byte(a.Text)) != a.SHA256 {
					t.Fatal("missing or corrupt nonce proof artifact")
				}
				scoped := ca.ID(original, result.Snapshot.CodeAddress, result.Snapshot.StorageAddress, result.Snapshot.BlockHash)
				data, err := contractScopePropertyProof([]byte(a.Text), original, map[string]string{original: scoped})
				if err != nil {
					t.Fatal(err)
				}
				var before, after map[string]any
				if err := json.Unmarshal([]byte(a.Text), &before); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &after); err != nil {
					t.Fatal(err)
				}
				published := after["property"].(map[string]any)
				if published["proof_artifact_id"] != scoped || published["property_id"] != p["property_id"] || published["status"] != p["status"] || string(ca.Row(after["query"])) != string(ca.Row(before["query"])) || string(ca.Row(published["counterexample"])) != string(ca.Row(before["property"].(map[string]any)["counterexample"])) {
					t.Fatal("proof scoping changed the nonce property, SMT query or counterexample")
				}
				checked++
			}
			if checked == 0 {
				t.Fatal("no nonce proof obligations reached backend")
			}
		})
	}
}
