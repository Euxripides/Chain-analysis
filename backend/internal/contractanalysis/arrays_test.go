package contractanalysis

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestBoundedCanonicalABIArray(t *testing.T) {
	raw, _ := hex.DecodeString(strings.Repeat("0", 62) + "20" + strings.Repeat("0", 63) + "1" + strings.Repeat("0", 24) + strings.Repeat("a", 40))
	values, e := DecodePrimitiveArray(raw, "address", 1)
	if e != nil || len(values) != 1 || values[0] != "0x"+strings.Repeat("a", 40) {
		t.Fatal(values, e)
	}
	if _, e = DecodePrimitiveArray(raw, "address", 0); e == nil {
		t.Fatal("array bound ignored")
	}
	raw[0] = 255
	if _, e = DecodePrimitiveArray(raw, "address", 1); e == nil {
		t.Fatal("huge offset accepted")
	}
	raw[0] = 0
	raw[64] = 1
	if _, e = DecodePrimitiveArray(raw, "address", 1); e == nil {
		t.Fatal("noncanonical address accepted")
	}
	selectors, _ := hex.DecodeString(strings.Repeat("0", 62) + "20" + strings.Repeat("0", 63) + "1" + "12345678" + strings.Repeat("0", 56))
	values, e = DecodePrimitiveArray(selectors, "bytes4", 1)
	if e != nil || values[0] != "0x12345678" {
		t.Fatal(values, e)
	}
	selectors[95] = 1
	if _, e = DecodePrimitiveArray(selectors, "bytes4", 1); e == nil {
		t.Fatal("noncanonical bytes4 accepted")
	}
}
