package ankr

import (
	encodinghex "encoding/hex"
	"strings"
	"testing"
)

func TestDecodeTokenStringDynamicAndBytes32(t *testing.T) {
	word := func(v string) string { return strings.Repeat("0", 64-len(v)) + v }
	dynamic := "0x" + word("20") + word("a") + encodinghex.EncodeToString([]byte("Tether USD")) + strings.Repeat("0", 44)
	if got, err := decodeTokenString(dynamic); err != nil || got != "Tether USD" {
		t.Fatalf("dynamic=%q err=%v", got, err)
	}
	bytes32 := "0x" + encodinghex.EncodeToString(append([]byte("USDT"), make([]byte, 28)...))
	if got, err := decodeTokenString(bytes32); err != nil || got != "USDT" {
		t.Fatalf("bytes32=%q err=%v", got, err)
	}
}
func TestDecodeTokenDecimalsBounds(t *testing.T) {
	for _, v := range []int{0, 6, 18, 255} {
		raw := "0x" + strings.Repeat("0", 62) + fmtHex(v)
		got, err := decodeTokenDecimals(raw)
		if err != nil || got != v {
			t.Fatalf("%d got=%d err=%v", v, got, err)
		}
	}
	if _, err := decodeTokenDecimals("0x" + strings.Repeat("0", 61) + "100"); err == nil {
		t.Fatal("256 must be rejected")
	}
}
func fmtHex(v int) string {
	const h = "0123456789abcdef"
	if v < 16 {
		return "0" + string(h[v])
	}
	return string(h[v/16]) + string(h[v%16])
}
