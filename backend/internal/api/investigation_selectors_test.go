package api

import "testing"

func TestBytecodeSelectorSkipsPushOperands(t *testing.T) {
	selector := "0x1991fe18"
	direct := []byte{0x63, 0x19, 0x91, 0xfe, 0x18, 0x14, 0x60, 0x10, 0x57}
	if len(bytecodeSelectorEvidence(direct, selector)) != 1 {
		t.Fatal("dispatcher candidate missing")
	}
	operand := append([]byte{0x68}, direct...)
	if len(bytecodeSelectorEvidence(operand, selector)) != 0 {
		t.Fatal("PUSH payload treated as executed instructions")
	}
}
