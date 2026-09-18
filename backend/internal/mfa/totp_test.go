package mfa

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 附录 B 的官方测试向量（SHA1、8 位、种子 "12345678901234567890"）。
// 用它钉住实现，避免"自己算自己验"的自证循环。
func rfcSecretB32(t *testing.T) string {
	t.Helper()
	return "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
}

func TestTOTPMatchesRFC6238Vectors(t *testing.T) {
	secret := rfcSecretB32(t)
	vectors := []struct {
		unix int64
		code string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, v := range vectors {
		got, err := CodeAt(secret, uint64(v.unix/PeriodSeconds), 8)
		if err != nil {
			t.Fatalf("CodeAt(%d): %v", v.unix, err)
		}
		if got != v.code {
			t.Errorf("T=%d: got %s want %s", v.unix, got, v.code)
		}
		if !Verify(secret, v.code, v.unix, 0) {
			t.Errorf("T=%d: %s must verify at its own step", v.unix, v.code)
		}
	}
}

func TestVerifyAcceptsClockSkewAndRejectsOthers(t *testing.T) {
	secret := rfcSecretB32(t)
	now := int64(59)
	code, err := CodeAt(secret, uint64(now/PeriodSeconds), Digits)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(secret, code, now, 1) {
		t.Fatal("the current step must verify")
	}
	if !Verify(secret, code, now+PeriodSeconds, 1) {
		t.Fatal("one step of drift must be tolerated")
	}
	if Verify(secret, code, now+3*PeriodSeconds, 1) {
		t.Fatal("three steps of drift must be rejected")
	}
	if Verify(secret, "000000", now, 1) && code != "000000" {
		t.Fatal("a wrong code must be rejected")
	}
	if Verify(secret, "", now, 1) {
		t.Fatal("an empty code must be rejected")
	}
}

func TestNewSecretIsUsableAndNormalized(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 {
		t.Fatalf("secret length = %d, want 32 base32 chars", len(secret))
	}
	normalized, err := NormalizeSecret(" " + secret[:10] + " " + secret[10:] + "==")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if normalized != secret {
		t.Fatalf("normalized = %q, want %q", normalized, secret)
	}
	if _, err := NormalizeSecret("not-base32!!"); err == nil {
		t.Fatal("invalid base32 must be rejected")
	}
	code, err := CodeAt(secret, uint64(time.Now().Unix()/PeriodSeconds), Digits)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(secret, code, time.Now().Unix(), DefaultSkewSteps) {
		t.Fatal("a freshly generated secret must verify its own current code")
	}
}

func TestRecoveryCodesAreHashedAndComparable(t *testing.T) {
	plain, digests, err := RecoveryCodes(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 10 || len(digests) != 10 {
		t.Fatalf("want 10 codes and 10 digests, got %d/%d", len(plain), len(digests))
	}
	seen := map[string]bool{}
	for i, code := range plain {
		if seen[code] {
			t.Fatalf("duplicate recovery code %q", code)
		}
		seen[code] = true
		if len(code) != 11 {
			t.Fatalf("code %q must be 5-5 with a dash", code)
		}
		if !RecoveryCodeMatches(code, digests[i]) {
			t.Fatalf("code %q must match its own digest", code)
		}
		if !RecoveryCodeMatches(" "+code[:5]+"-"+code[6:]+" ", digests[i]) {
			t.Fatal("whitespace must not defeat matching")
		}
		for j := range plain {
			if j != i && RecoveryCodeMatches(code, digests[j]) {
				t.Fatalf("code %q must not match another digest", code)
			}
		}
	}
}

func TestProvisioningURI(t *testing.T) {
	uri := ProvisioningURI("ChainSight", "admin@local", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
	for _, want := range []string{"otpauth://totp/", "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "issuer=ChainSight", "digits=6", "period=30"} {
		if !strings.Contains(uri, want) {
			t.Errorf("provisioning URI %q must contain %q", uri, want)
		}
	}
}
