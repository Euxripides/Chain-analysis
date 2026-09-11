package iam

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	params := Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, KeyLength: 32, SaltLength: 16}
	hash, err := HashPassword("correct horse battery staple", params)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if len(hash) < 40 || hash[:10] != "$argon2id$" {
		t.Fatalf("unexpected PHC format: %q", hash)
	}
	ok, got, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("expected verify success, got ok=%v err=%v", ok, err)
	}
	if got.Iterations != params.Iterations || got.Memory != params.Memory {
		t.Fatalf("parsed params mismatch: %+v", got)
	}
	ok, _, err = VerifyPassword(hash, "wrong password")
	if err != nil {
		t.Fatalf("verify wrong: %v", err)
	}
	if ok {
		t.Fatal("wrong password must not verify")
	}
}

func TestHashIsSaltedPerCall(t *testing.T) {
	params := Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, KeyLength: 32, SaltLength: 16}
	a, _ := HashPassword("same-password", params)
	b, _ := HashPassword("same-password", params)
	if a == b {
		t.Fatal("identical passwords must not produce identical hashes (salt missing)")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	if _, _, err := VerifyPassword("not-a-hash", "x"); err == nil {
		t.Fatal("expected error for malformed hash")
	}
	if _, _, err := VerifyPassword("$argon2i$v=19$m=1,t=1,p=1$aa$bb", "x"); err == nil {
		t.Fatal("expected error for unsupported algorithm")
	}
}

func TestNeedsRehash(t *testing.T) {
	weak := Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, KeyLength: 32, SaltLength: 16}
	hash, _ := HashPassword("x", weak)
	if !NeedsRehash(hash, DefaultParams()) {
		t.Fatal("weak hash should be flagged for rehash")
	}
	strong, _ := HashPassword("x", DefaultParams())
	if NeedsRehash(strong, DefaultParams()) {
		t.Fatal("matching params must not rehash")
	}
}

func TestPasswordTooShortIsCheckedByServiceConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MinPasswordLength < 8 {
		t.Fatalf("minimum password length is too low: %d", cfg.MinPasswordLength)
	}
}
