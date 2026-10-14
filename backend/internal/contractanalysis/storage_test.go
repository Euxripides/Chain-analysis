package contractanalysis

import (
	"math/big"
	"testing"
)

func TestPackedSignedAndSharedSlot(t *testing.T) {
	value := new(big.Int).Lsh(big.NewInt(125), 160)
	value.Or(value, big.NewInt(42))
	word := value.FillBytes(make([]byte, 32))
	fee, e := DecodePacked(word, 20, "uint16")
	if e != nil || fee != "125" {
		t.Fatal(fee, e)
	}
	address, e := DecodePacked(word, 0, "address")
	if e != nil || address != "0x000000000000000000000000000000000000002a" {
		t.Fatal(address, e)
	}
	word[31] = 255
	signed, e := DecodePacked(word, 0, "int8")
	if e != nil || signed != "-1" {
		t.Fatal(signed, e)
	}
	if _, e = DecodePacked(word, 31, "uint16"); e == nil {
		t.Fatal("packed overflow accepted")
	}
}
