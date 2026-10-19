package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"io"
	"strings"
	"testing"
)

func TestContractSubmittedBoundaryRedactionAndOriginalIdentity(t *testing.T) {
	artifact := ca.Artifact{ArtifactID: strings.Repeat("a", 40), Kind: "submitted_input", ObjectKey: "private/store/key", SHA256: strings.Repeat("b", 64), Bytes: 12}
	raw := ca.Row(map[string]any{"engine_digest": "sha256:engine", "submitted_input_object": ca.Object{Key: artifact.ObjectKey}, "input_artifact": artifact, "input_sha256": artifact.SHA256})
	for _, value := range []any{contractPublicBoundary(raw), contractPublicReuse(map[string]any{"snapshot": json.RawMessage(raw)})} {
		text := string(ca.Row(value))
		if strings.Contains(text, "private/store/key") || strings.Contains(text, "object_key") {
			t.Fatal("private object identity leaked")
		}
		if !strings.Contains(text, artifact.ArtifactID) || !strings.Contains(text, artifact.SHA256) {
			t.Fatal("public input evidence identity lost")
		}
	}
	old := []byte(`{"chain":"ethereum","point":{"as_of_block":19000000},"engine_digest":"sha256:legacy"}`)
	if string(ca.Row(contractPublicBoundary(old))) != string(old) {
		t.Fatal("legacy boundary changed")
	}
}

func TestContractCompressedDependencyScopeKeepsAllReferencesAndChecksums(t *testing.T) {
	original, scoped := strings.Repeat("a", 40), strings.Repeat("b", 40)
	id := strings.Repeat("c", 40)
	raw := ca.Row(map[string]any{"schema_version": "conclusion-dependencies-v1", "conclusions": []any{map[string]any{"conclusion_id": id, "exact_integer": "115792089237316195423570985008687907853269984665640564039457584007913129639935"}}, "analysis_gaps": []any{map[string]any{"location": map[string]any{"artifact_id": original}}}})
	var buffer bytes.Buffer
	w := gzip.NewWriter(&buffer)
	_, _ = w.Write(raw)
	_ = w.Close()
	result := ca.Result{Sections: map[string][]json.RawMessage{"dependency_artifacts": {ca.Row(map[string]any{"artifact_id": id, "uncompressed_sha256": ca.SHA256(raw), "uncompressed_bytes": len(raw)})}}}
	saved, err := contractScopeDependencyArchive(buffer.Bytes(), id, map[string]string{original: scoped}, &result)
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(saved))
	if err != nil {
		t.Fatal(err)
	}
	after, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if bytes.Contains(after, []byte(original)) || !bytes.Contains(after, []byte(scoped)) || !bytes.Contains(after, []byte("115792089237316195423570985008687907853269984665640564039457584007913129639935")) {
		t.Fatal("dependency scope lost references or precision")
	}
	var descriptor map[string]any
	_ = json.Unmarshal(result.Sections["dependency_artifacts"][0], &descriptor)
	if descriptor["uncompressed_sha256"] != ca.SHA256(after) || int(descriptor["uncompressed_bytes"].(float64)) != len(after) {
		t.Fatal("dependency descriptor checksum stale")
	}
	corrupt := append([]byte{}, buffer.Bytes()...)
	corrupt[len(corrupt)-5] ^= 1
	if _, err = contractScopeDependencyArchive(corrupt, id, map[string]string{}, &result); err == nil {
		t.Fatal("corrupt dependency accepted")
	}
}
