package solana

import "testing"

func TestNormalizeSolanaAddress(t *testing.T) {
	valid := []string{
		"11111111111111111111111111111111",             // System Program：32 个零字节
		"So11111111111111111111111111111111111111112",  // Wrapped SOL
		"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", // USDC mint
	}
	for _, address := range valid {
		got, err := NormalizeSolanaAddress("  " + address + "  ")
		if err != nil {
			t.Fatalf("expected %q to be valid, got error %v", address, err)
		}
		if got != address {
			t.Fatalf("address case must be preserved: got %q want %q", got, address)
		}
	}

	invalid := []string{
		"",
		"   ",
		"abc",  // 解码长度不足 32
		"0OIl", // base58 不含 0 O I l
		"0x6808c73d7e9f1e1905c0618bb47bd6947ed4a6ae", // EVM 地址
		"1111111111111111111111111111111111",         // 34 个 '1' → 34 字节
	}
	for _, address := range invalid {
		if _, err := NormalizeSolanaAddress(address); err == nil {
			t.Fatalf("expected %q to be rejected", address)
		}
	}
}

func TestDecodeBase58PreservesLeadingZeros(t *testing.T) {
	decoded, err := DecodeBase58("11111111111111111111111111111111")
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(decoded) != AddressBytes {
		t.Fatalf("got %d bytes, want %d", len(decoded), AddressBytes)
	}
	for i, b := range decoded {
		if b != 0 {
			t.Fatalf("byte %d = %d, want 0", i, b)
		}
	}
}
