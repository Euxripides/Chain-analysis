package ingest

import (
	"math/big"
	"testing"
)

func TestDecOrZeroUsesTokenDecimals(t *testing.T) {
	raw, ok := new(big.Int).SetString("50225000000000000000000", 10)
	if !ok {
		t.Fatal("invalid fixture")
	}
	if got := decOrZero(raw, 18).String(); got != "50225" {
		t.Fatalf("amount = %s, want 50225", got)
	}

	raw, _ = new(big.Int).SetString("16175000597016007137477", 10)
	if got := decOrZero(raw, 18).String(); got != "16175.000597016007137477" {
		t.Fatalf("amount = %s", got)
	}
}

func TestDecOrZeroRejectsUnrepresentableIntegerPart(t *testing.T) {
	raw, _ := new(big.Int).SetString("64518700000000000000000000000000000000000000000000000000000000000000000000", 10)
	if got := decOrZero(raw, 3).String(); got != "0" {
		t.Fatalf("unrepresentable amount = %s, want 0", got)
	}
}
