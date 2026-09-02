package ankrmgmt

import (
	"bytes"
	"testing"
)

func TestCredentialEncryptionRoundTrip(t *testing.T) {
	aead, err := newAEAD("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("short-lived-oauth-bearer")
	ciphertext, nonce, err := seal(aead, secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, secret) {
		t.Fatal("ciphertext contains plaintext credential")
	}
	plaintext, err := open(aead, ciphertext, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext, secret) {
		t.Fatalf("credential changed after decrypt: %q", plaintext)
	}
	if _, err := open(aead, ciphertext, append([]byte(nil), nonce[:len(nonce)-1]...)); err == nil {
		t.Fatal("invalid nonce must be rejected")
	}
}

func TestParseBalanceStructuredAndText(t *testing.T) {
	structured := parseBalance(map[string]any{"account": map[string]any{
		"remainingCredits": "93000000", "balanceUsd": 9.26, "level": "Premium", "address": "0xabc",
	}})
	if structured.RemainingCredits == nil || *structured.RemainingCredits != 93000000 {
		t.Fatalf("credits not parsed: %+v", structured)
	}
	if structured.BalanceUSD == nil || *structured.BalanceUSD != 9.26 || structured.Plan != "Premium" || structured.AccountAddress != "0xabc" {
		t.Fatalf("structured balance not parsed: %+v", structured)
	}

	text := parseBalance(map[string]any{"text": "Current balance: 93,000,000 API Credits (approximately $9.26)"})
	if text.RemainingCredits == nil || *text.RemainingCredits != 93000000 || text.BalanceUSD == nil || *text.BalanceUSD != 9.26 {
		t.Fatalf("text balance not parsed: %+v", text)
	}
}
