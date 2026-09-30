package method

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTypedEncodingNestedRoundTripAndPrecision(t *testing.T) {
	raw := `[{"type":"function","name":"read","stateMutability":"view","inputs":[{"name":"n","type":"uint256"},{"name":"signed","type":"int8"},{"name":"nested","type":"tuple[]","components":[{"name":"who","type":"address"},{"name":"text","type":"string"}]}],"outputs":[{"name":"value","type":"uint256"}]}]`
	abi, err := ParseABI([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := abi.Lookup(SelectorOf("read(uint256,int8,(address,string)[])"))
	args := []any{"900719925474099312345678901234567890", json.Number("-127"), []any{map[string]any{"who": "0x1111111111111111111111111111111111111111", "text": "hello"}, map[string]any{"who": "0x2222222222222222222222222222222222222222", "text": "world"}}}
	input, err := EncodeInput(m, args)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeInput(m, input)
	if err != nil {
		t.Fatal(err)
	}
	if decoded[0].Value != args[0] || decoded[1].Value != "-127" {
		t.Fatalf("precision/signed value lost: %+v", decoded)
	}
	out, err := DecodeOutput(m, "0x"+strings.Repeat("0", 63)+"7")
	if err != nil || len(out) != 1 || out[0].Value != "7" {
		t.Fatalf("outputs=%+v error=%v", out, err)
	}
	if m.StateMutability != "view" {
		t.Fatal("mutability lost")
	}
}

func TestEncodingRejectsUnsafeNumbersAndTypeOverflow(t *testing.T) {
	abi, _ := ParseABI([]byte(`[{"type":"function","name":"read","inputs":[{"type":"uint8"},{"type":"int8"},{"type":"bool"}]}]`))
	m, _ := abi.Lookup(SelectorOf("read(uint8,int8,bool)"))
	for _, args := range [][]any{{"256", "0", true}, {"-1", "0", true}, {"1", "128", true}, {float64(1), "0", true}, {"1", "0", "true"}} {
		if _, err := EncodeInput(m, args); err == nil {
			t.Fatalf("unsafe arguments accepted: %v", args)
		}
	}
}
