package contractanalysis

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func transportFixture(t *testing.T, raw []byte, snapshot Snapshot) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	_, _ = w.Write(raw)
	_ = w.Close()
	compressed := out.Bytes()
	return Row(map[string]any{"version": Version, "rule_version": "funds-semantics-14", "snapshot": snapshot, "sections": map[string]any{"engine_transport": []any{map[string]any{"encoding": "engine-transport-gzip-v1", "content_base64": base64.StdEncoding.EncodeToString(compressed), "byte_size": len(compressed), "sha256": SHA256(compressed), "expanded_bytes": len(raw), "expanded_sha256": SHA256(raw)}}}})
}
func TestEngineTransportPreservesCompleteRowsAndRejectsWrongIdentity(t *testing.T) {
	s := Snapshot{IdentityKind: "code_only", InputKind: "source", InputSHA256: strings.Repeat("a", 64), CodeHash: "0x" + strings.Repeat("b", 64)}
	r := Result{Version: Version, RuleVersion: "funds-semantics-14", Snapshot: s, Sections: map[string][]json.RawMessage{"evidence": {json.RawMessage(`{"exact":9007199254740993,"text":"中文"}`)}}}
	raw := Row(r)
	wire := transportFixture(t, raw, s)
	decoded, err := ParseResult(wire)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded.Sections["evidence"][0]) != string(r.Sections["evidence"][0]) {
		t.Fatal("transport changed exact rows")
	}
	wrong := s
	wrong.InputSHA256 = strings.Repeat("c", 64)
	if _, err = ParseResult(transportFixture(t, raw, wrong)); err == nil {
		t.Fatal("wrong identity accepted")
	}
	r.Sections["engine_transport"] = []json.RawMessage{Row(map[string]any{"encoding": "nested"})}
	if _, err = ParseResult(transportFixture(t, Row(r), s)); err == nil {
		t.Fatal("nested transport accepted")
	}
	var obj map[string]any
	_ = json.Unmarshal(wire, &obj)
	entry := obj["sections"].(map[string]any)["engine_transport"].([]any)[0].(map[string]any)
	entry["expanded_bytes"] = EngineExpandedLimit + 1
	if _, err = ParseResult(Row(obj)); err == nil {
		t.Fatal("oversized expansion accepted")
	}
	entry["expanded_bytes"] = len(raw)
	entry["expanded_sha256"] = strings.Repeat("0", 64)
	if _, err = ParseResult(Row(obj)); err == nil {
		t.Fatal("corrupt expansion checksum accepted")
	}
}
