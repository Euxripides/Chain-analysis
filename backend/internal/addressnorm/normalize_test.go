package addressnorm

import (
	"errors"
	"testing"
)

const (
	solanaAddress = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	tronAddress   = "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	tronHex41     = "41EA51342D9B1EA6EAE9E7CBE6B7AB93B2E0D27AF8"
	// tronHex41 的 Base58Check 形式（与 tronAddress 是**不同**地址，仅用于验证 41-hex 归一化）。
	tronHex41Base58 = "TXLAQ63JFRMerpHdBLokkLyH8GtW9ASxvu"
	evmAddress      = "0xABCDEF0123456789ABCDEF0123456789ABCDEF01"
)

func TestNormalizeSolanaPreservesCase(t *testing.T) {
	got, err := Normalize("solana", "  "+solanaAddress+"  ")
	if err != nil {
		t.Fatalf("valid solana address rejected: %v", err)
	}
	if got != solanaAddress {
		t.Fatalf("solana case changed: got %q want %q", got, solanaAddress)
	}
	// 链名别名大小写不敏感，但地址本身必须原样保留。
	aliased, err := Normalize("Solana", solanaAddress)
	if err != nil || aliased != solanaAddress {
		t.Fatalf("chain alias must not change solana case, got %q err=%v", aliased, err)
	}
	if _, err := Normalize("solana", "0x6808c73d7e9f1e1905c0618bb47bd6947ed4a6ae"); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("EVM address must be rejected on solana, got %v", err)
	}
}

func TestNormalizeEVMLowercases(t *testing.T) {
	for _, in := range []string{evmAddress, "0x" + evmAddress[2:], "  " + evmAddress + "  ", "0X" + evmAddress[2:]} {
		got, err := Normalize("bsc", in)
		if err != nil {
			t.Fatalf("valid evm address %q rejected: %v", in, err)
		}
		if got != "0xabcdef0123456789abcdef0123456789abcdef01" {
			t.Fatalf("evm address must be lowercased, got %q", got)
		}
	}
	for _, bad := range []string{"0x123", "abcdef0123456789abcdef0123456789abcdef01", "0xZZcdef0123456789abcdef0123456789abcdef01"} {
		if _, err := Normalize("bsc", bad); !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("expected %q to be rejected, got %v", bad, err)
		}
	}
}

func TestNormalizeTron(t *testing.T) {
	got, err := Normalize("tron", "  "+tronAddress+"  ")
	if err != nil || got != tronAddress {
		t.Fatalf("TRON base58 must be preserved: got %q err=%v", got, err)
	}
	// 41... hex 必须归一到 Base58Check（与 dataset 历史语义一致）。
	fromHex, err := Normalize("TRON", tronHex41)
	if err != nil || fromHex != tronHex41Base58 {
		t.Fatalf("TRON hex41 must normalize to base58: got %q err=%v", fromHex, err)
	}
	if _, err := Normalize("tron", "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdk"); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("bad checksum must be rejected, got %v", err)
	}
}

func TestNormalizeRejectsEmpty(t *testing.T) {
	for _, chain := range []string{"solana", "tron", "bsc", ""} {
		if _, err := Normalize(chain, "   "); !errors.Is(err, ErrEmptyAddress) {
			t.Fatalf("chain %q: empty address error = %v", chain, err)
		}
	}
}

func TestNormalizeListDedupesKeepingFirstOrder(t *testing.T) {
	second := "0x2222222222222222222222222222222222222222"
	got, err := NormalizeList("bsc", []string{evmAddress, " " + evmAddress + " ", second})
	if err != nil {
		t.Fatalf("valid list rejected: %v", err)
	}
	if len(got) != 2 || got[0] != "0xabcdef0123456789abcdef0123456789abcdef01" || got[1] != second {
		t.Fatalf("unexpected list: %v", got)
	}
	if _, err := NormalizeList("bsc", []string{evmAddress, "bad"}); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("invalid element must fail the whole list, got %v", err)
	}
	solana, err := NormalizeList("solana", []string{solanaAddress, " " + solanaAddress + " "})
	if err != nil || len(solana) != 1 || solana[0] != solanaAddress {
		t.Fatalf("solana list must keep case and dedupe: %v err=%v", solana, err)
	}
}

func TestChainClassification(t *testing.T) {
	if !IsSolana(" Solana ") || IsSolana("bsc") || IsSolana("") {
		t.Fatal("IsSolana mismatch")
	}
	if !IsTron("TRON") || IsTron("solana") {
		t.Fatal("IsTron mismatch")
	}
	if !IsEVM("") || !IsEVM("bsc") || IsEVM("solana") || IsEVM("tron") {
		t.Fatal("IsEVM mismatch")
	}
}

func TestNormalizeKeyIsLenientButNeverFoldsSolana(t *testing.T) {
	if got := NormalizeKey("solana", "  "+solanaAddress+" "); got != solanaAddress {
		t.Fatalf("NormalizeKey changed solana case: %q", got)
	}
	// 非法 Solana 输入原样返回（只去空白），绝不能小写化。
	if got := NormalizeKey("solana", " NotBase58! "); got != "NotBase58!" {
		t.Fatalf("NormalizeKey must not fold invalid solana input: %q", got)
	}
	if got := NormalizeKey("bsc", evmAddress); got != "0xabcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("NormalizeKey evm = %q", got)
	}
	if got := NormalizeKey("tron", tronHex41); got != tronHex41Base58 {
		t.Fatalf("NormalizeKey tron = %q", got)
	}
	if got := NormalizeKey("bsc", "   "); got != "" {
		t.Fatalf("NormalizeKey empty = %q", got)
	}
}
