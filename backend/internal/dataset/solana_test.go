package dataset

import (
	"strings"
	"testing"
)

func TestNormalizeSolanaAddressesPreserveCase(t *testing.T) {
	const address = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	out, err := NormalizeAndDedupeAddressesForChain("solana", []string{"  " + address + "  ", address})
	if err != nil {
		t.Fatalf("valid solana address rejected: %v", err)
	}
	if len(out) != 1 || out[0] != address {
		t.Fatalf("case must be preserved and duplicates removed, got %v", out)
	}

	// EVM 行为不变：仍然小写折叠。
	evm, err := NormalizeAndDedupeAddressesForChain("bsc", []string{"0xABCDEF0123456789ABCDEF0123456789ABCDEF01"})
	if err != nil {
		t.Fatalf("valid evm address rejected: %v", err)
	}
	if evm[0] != strings.ToLower("0xABCDEF0123456789ABCDEF0123456789ABCDEF01") {
		t.Fatalf("evm address must be lowercased, got %s", evm[0])
	}

	// 无效/错链地址一律拒绝，绝不静默替换。
	for _, bad := range []string{"0x6808c73d7e9f1e1905c0618bb47bd6947ed4a6ae", "abc", "0OIl"} {
		if _, err := NormalizeAndDedupeAddressesForChain("solana", []string{bad}); err == nil {
			t.Fatalf("expected %q to be rejected for solana", bad)
		}
	}
}

func TestSolanaRangeGuardAllowsRPCPaginationRanges(t *testing.T) {
	for _, rangeType := range []string{"block", "full", "", "time"} {
		if err := solanaRangeGuard("solana", rangeType); err != nil {
			t.Fatalf("range type %q must be allowed for solana: %v", rangeType, err)
		}
	}
	if err := solanaRangeGuard("solana", "unknown"); err == nil {
		t.Fatal("unknown Solana range type must be rejected")
	}
	// 其他链完全不受影响。
	for _, rangeType := range []string{"full", "time", "block"} {
		if err := solanaRangeGuard("bsc", rangeType); err != nil {
			t.Fatalf("bsc range %q must be unaffected: %v", rangeType, err)
		}
	}
}
