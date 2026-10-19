package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

type contractCodeBoundary struct {
	InputObject   ca.Object   `json:"submitted_input_object"`
	InputArtifact ca.Artifact `json:"input_artifact"`
	Engine        string      `json:"engine_digest"`
}

func (s *Server) contractPinSubmittedInput(ctx context.Context, q ca.Request, engine string, boundary map[string]any) error {
	if q.Input == nil {
		return nil
	}
	raw, err := q.Input.JSON()
	if err != nil {
		return err
	}
	obj, err := s.contractSaveObject(ctx, "submitted-input", raw, "application/json")
	if err != nil {
		return err
	}
	boundary["submitted_input_object"] = obj
	boundary["input_kind"] = q.Input.Kind
	boundary["input_sha256"] = obj.SHA256
	boundary["input_artifact"] = ca.Artifact{ArtifactID: ca.ID("submitted-input", obj.Key, obj.SHA256), Kind: "submitted_input", Filename: "submitted-input.json", MediaType: "application/json", SHA256: obj.SHA256, Bytes: obj.Bytes, ObjectKey: obj.Key}
	if q.CodeOnly() {
		boundary["identity_kind"] = "code_only"
	}
	return nil
}

func contractPublicBoundary(raw []byte) any {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return json.RawMessage(raw)
	}
	if _, ok := fields["submitted_input_object"]; !ok {
		return json.RawMessage(raw)
	}
	delete(fields, "submitted_input_object")
	var a ca.Artifact
	if json.Unmarshal(fields["input_artifact"], &a) == nil {
		a.ObjectKey = ""
		fields["input_artifact"] = ca.Row(a)
	}
	return fields
}

func contractPublicReuse(data any) any {
	if value, ok := data.(map[string]any); ok {
		value["snapshot"] = contractPublicBoundary(ca.Row(value["snapshot"]))
	}
	return data
}

func (s *Server) contractSubmittedMaterial(ctx context.Context, q ca.Request, boundary []byte, snapshot ca.Snapshot) (ca.Input, error) {
	var b contractCodeBoundary
	if err := json.Unmarshal(boundary, &b); err != nil {
		return ca.Input{}, err
	}
	raw, err := s.contractObject(ctx, b.InputObject, 2<<20)
	if err != nil {
		return ca.Input{}, err
	}
	var submitted ca.SubmittedCode
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&submitted); err != nil {
		return ca.Input{}, err
	}
	if q.Input == nil || q.Input.Kind != submitted.Kind {
		return ca.Input{}, fmt.Errorf("frozen submitted input kind mismatch")
	}
	snapshot.InputKind = submitted.Kind
	snapshot.InputSHA256 = b.InputObject.SHA256
	snapshot.DeclaredEnvironment = submitted.Environment
	snapshot.CodePhase = submitted.CodePhase
	if q.CodeOnly() {
		snapshot.IdentityKind = "code_only"
		snapshot.EngineDigest = b.Engine
		snapshot.StateGranularity = "symbolic_state"
		snapshot.EVMRules = "opcode_model_shanghai_with_explicit_later_opcode_gaps"
		if submitted.Environment != nil && submitted.Environment.EVMRules != "" {
			snapshot.EVMRules = submitted.Environment.EVMRules
		}
		if submitted.Bytecode != "" {
			code, _ := hex.DecodeString(submitted.Bytecode[2:])
			snapshot.CodeHash = contractKeccak(code)
			snapshot.CodeSHA256 = ca.SHA256(code)
		}
	} else {
		snapshot.IdentityKind = "observed_deployment"
	}
	return ca.Input{Snapshot: snapshot, SubmittedInputJSON: string(raw), Engines: q.Engines}, nil
}
