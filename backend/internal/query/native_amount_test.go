package query

import "testing"

func TestNativeAmountBoundKeepsFractionalThresholdsExact(t *testing.T) {
	for _, tc := range []struct {
		amount   string
		decimals int
		min, max string
	}{
		{"0.1", 18, "100000000000000000", "100000000000000000"},
		{"0.1000000000000000001", 18, "100000000000000001", "100000000000000000"},
		{"1.0000001", 6, "1000001", "1000000"},
	} {
		min, err := nativeAmountBound(tc.amount, tc.decimals, true)
		if err != nil || min != tc.min {
			t.Errorf("min %s = %q, %v", tc.amount, min, err)
		}
		max, err := nativeAmountBound(tc.amount, tc.decimals, false)
		if err != nil || max != tc.max {
			t.Errorf("max %s = %q, %v", tc.amount, max, err)
		}
	}
}

func TestNativeRawHexAndDecimalFormatIdentically(t *testing.T) {
	for _, raw := range []string{"0xde0b6b3a7640000", "1000000000000000000"} {
		amount, err := formatNativeRaw(raw, 18)
		if err != nil || amount != "1" {
			t.Errorf("%s = %q, %v", raw, amount, err)
		}
	}
	if _, err := parseNativeRaw("0xnot-hex"); err == nil {
		t.Fatal("malformed raw value accepted")
	}
}

func TestNativeAmountBoundRejectsNonDecimal(t *testing.T) {
	for _, amount := range []string{"1/2", "-1", "1e3", "0x10"} {
		if _, err := nativeAmountBound(amount, 18, true); err == nil {
			t.Errorf("accepted %q", amount)
		}
	}
}
