package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

// The state artifact contains source locations also published as result rows.
// Scope those references before saving and checksumming the artifact. SMT text
// and numeric literals are data: they must not undergo string-wide replacement
// or a conversion through floating point.
func contractScopeStateSemantics(data []byte, remap map[string]string) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("invalid state semantics JSON: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("state semantics must contain one JSON object")
	}
	model, modelOK := document["model_id"].(string)
	method, methodOK := document["method_id"].(string)
	if document["schema_version"] != "source-state-transition-v1" || !modelOK || !methodOK ||
		!ca.Identifier.MatchString(model) || !ca.Identifier.MatchString(method) {
		return nil, fmt.Errorf("invalid state semantics schema or identity")
	}
	scoped := make(map[string]bool, len(remap))
	for _, value := range remap {
		scoped[value] = true
	}
	nodes := 0
	var visit func(any, int) error
	visit = func(value any, depth int) error {
		nodes++
		if depth > 64 || nodes > 128000 {
			return fmt.Errorf("state semantics reference budget exceeded")
		}
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "artifact_id" {
					original, ok := child.(string)
					if !ok || original == "" {
						return fmt.Errorf("invalid state semantics source artifact identity")
					}
					if mapped := remap[original]; mapped != "" {
						node[key] = mapped
					} else if !scoped[original] {
						return fmt.Errorf("state semantics source artifact identity missing")
					}
					continue
				}
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range node {
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(document, 0); err != nil {
		return nil, err
	}
	return json.Marshal(document)
}
