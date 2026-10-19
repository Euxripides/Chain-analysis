package contractanalysis

import (
	"encoding/json"
	"strings"
	"testing"
)

func codeRequest() Request {
	q := Request{Input: &SubmittedCode{Kind: "source", Source: &SubmittedSource{Language: "Solidity", Files: map[string]string{"Token.sol": "contract Token {}"}, CompilerVersion: "0.8.30+commit.73712a01", Target: "Token.sol:Token"}}}
	q.Defaults()
	return q
}
func TestSubmittedCodeStrictValidationAndLegacyCompatibility(t *testing.T) {
	q := codeRequest()
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	alias := codeRequest()
	alias.Input.Source.Files = map[string]string{"./Token.sol": "contract Token {}"}
	alias.Input.Source.Target = "./Token.sol:Token"
	if err := alias.Validate(); err != nil {
		t.Fatal(err)
	}
	legacy := Request{Chain: "ethereum", Address: "0x" + strings.Repeat("1", 40)}
	legacy.Defaults()
	if err := legacy.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Request){
		"absolute path":          func(q *Request) { q.Input.Source.Files = map[string]string{"/tmp/a.sol": "x"} },
		"parent path":            func(q *Request) { q.Input.Source.Files["a/../../b.sol"] = "x" },
		"host path":              func(q *Request) { q.Input.Source.Files["C:\\secret.sol"] = "x" },
		"missing target":         func(q *Request) { q.Input.Source.Target = "Other.sol:Token" },
		"floating compiler":      func(q *Request) { q.Input.Source.CompilerVersion = "latest" },
		"compiler model checker": func(q *Request) { q.Input.Source.Settings = Row(map[string]any{"modelChecker": map[string]any{}}) },
		"unbound block":          func(q *Request) { q.Block = Row(1) },
		"unbound tx":             func(q *Request) { q.TxHashes = []string{"0x" + strings.Repeat("f", 64)} },
		"half binding":           func(q *Request) { q.Chain = "ethereum" },
		"wrong phase":            func(q *Request) { q.Input.Bytecode = "0x6000" },
		"oversize chain":         func(q *Request) { q.Input.Environment = &DeclaredEnvironment{ChainID: strings.Repeat("9", 78)} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			q := codeRequest()
			change(&q)
			if q.Validate() == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	for _, raw := range []string{`{"input":{"kind":"bytecode","bytecode":"0x00","code_phase":"runtime","object_key":"secret"}}`, `{"input":{"kind":"source","source":{"language":"Solidity","files":{},"urls":["https://example.invalid"]}}}`} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatal("unknown input field accepted")
		}
	}
}
func TestCodeOnlyJSONNeverSerializesObservedZeroDefaults(t *testing.T) {
	s := Snapshot{IdentityKind: "code_only", InputKind: "source", InputSHA256: strings.Repeat("a", 64), EngineDigest: "sha256:" + strings.Repeat("b", 64), StateGranularity: "symbolic_state"}
	raw := Row(s)
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"chain", "chain_id", "block_number", "block_hash", "block_time", "entry_address", "code_address", "storage_address", "fetched_at"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("code-only observed default: %s", key)
		}
	}
	in := Input{SubmittedInputJSON: `{"kind":"source"}`, Snapshot: s}
	in.Snapshot.InputSHA256 = SHA256([]byte(in.SubmittedInputJSON))
	s.InputSHA256 = in.Snapshot.InputSHA256
	if err := ValidateInputResult(in, s); err != nil {
		t.Fatal(err)
	}
	forged := s
	forged.EntryAddress = "0x" + strings.Repeat("0", 40)
	if ValidateInputResult(in, forged) == nil {
		t.Fatal("forged observed address accepted")
	}
	forged = s
	forged.InputSHA256 = strings.Repeat("c", 64)
	if ValidateInputResult(in, forged) == nil {
		t.Fatal("changed input identity accepted")
	}
}
func TestSubmittedCompilerIdentityCanonicalizationPreservesSourceBytes(t *testing.T) {
	q := codeRequest()
	q.Input.Source.Files["Token.sol"] = "// 中文\r\ncontract Token {}"
	q.Input.Source.Settings = json.RawMessage(`{"optimizer":{"runs":200,"enabled":true},"evmVersion":"shanghai"}`)
	if err := q.Input.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	first := Row(q.Input)
	q.Input.Source.Settings = json.RawMessage(`{"evmVersion":"shanghai","optimizer":{"enabled":true,"runs":200}}`)
	if err := q.Input.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if string(first) != string(Row(q.Input)) {
		t.Fatal("equivalent compiler settings changed identity")
	}
	if q.Input.Source.Files["Token.sol"] != "// 中文\r\ncontract Token {}" {
		t.Fatal("source bytes changed")
	}
}
func TestSubmittedSourceSerializationCannotAmplifyHTMLBeyondItsInputBudget(t *testing.T) {
	q := codeRequest()
	text := "contract Token{}/*" + strings.Repeat("<", 1<<20) + "*/"
	q.Input.Source.Files["Token.sol"] = text
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := q.Input.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 2<<20 {
		t.Fatal("canonical input amplified beyond bound")
	}
	var restored SubmittedCode
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Source.Files["Token.sol"] != text {
		t.Fatal("source content changed")
	}
}
