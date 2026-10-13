package api

import (
	"strings"
	"testing"
)

func TestClassifyExplorerInput(t *testing.T) {
	for _, tc := range []struct {
		chain, value, kind string
		valid              bool
	}{
		{"bsc", " 0x" + strings.Repeat("A", 40) + " ", "address", true},
		{"bsc", strings.Repeat("a", 64), "transaction", true},
		{"bsc", "0X" + strings.Repeat("A", 64), "transaction", true},
		{"bsc", "000123", "block", true},
		{"bsc", "18446744073709551616", "", false},
		{"bsc", "0x123", "", false},
		{"bsc", strings.Repeat("g", 64), "", false},
		{"bsc", "USDT", "token", true},
		{"bsc", "USD Coin", "token", true},
		{"tron", "0x" + strings.Repeat("a", 40), "", false},
		{"tron", "TInvalidAddressWithBadChecksum12345", "", false},
		{"bsc", "", "", false},
		{"bsc", "ab\ncd", "", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			kind, _, err := classifyExplorerInput(tc.chain, tc.value)
			if (err == nil) != tc.valid || kind != tc.kind {
				t.Fatalf("kind=%s err=%v", kind, err)
			}
		})
	}
}
