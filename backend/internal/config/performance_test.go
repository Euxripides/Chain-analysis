package config

import "testing"

func productionTestConfig(t *testing.T) *Config {
	t.Helper()
	values := map[string]string{
		"ANKR_INITIAL_CONCURRENCY": "32", "ANKR_MAX_CONCURRENCY": "48",
		"ANKR_PROD_SAFE_CONCURRENCY": "24", "ANKR_PROD_FAST_CONCURRENCY": "32",
		"ANKR_PROD_TURBO_CONCURRENCY": "48", "ANKR_PROD_HARD_CEILING": "48",
		"ANKR_PROD_REJECT_BOUNDARY": "64", "ANKR_MAX_PAGE_SIZE": "10000",
		"ANKR_PAGE_LEVELS": "1000,2000,5000,10000", "ANKR_MANAGEMENT_ENABLED": "false",
		"RPC_ENABLED": "false",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	return Load()
}

func TestProductionDefaultsStayInsideCertifiedBoundary(t *testing.T) {
	cfg := productionTestConfig(t)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	profile := cfg.AnkrProductionProfile()
	if profile.SafeConcurrency != 24 || profile.FastConcurrency != 32 || profile.TurboConcurrency != 48 {
		t.Fatalf("unexpected production modes: %+v", profile)
	}
	if profile.HardCeiling != 48 || profile.RejectBoundary != 64 {
		t.Fatalf("unexpected boundaries: ceiling=%d reject=%d", profile.HardCeiling, profile.RejectBoundary)
	}
}

func TestProductionConfigRejectsConcurrencyAbove48(t *testing.T) {
	cfg := productionTestConfig(t)
	cfg.Ankr.MaxConcurrency = 64
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected production concurrency above 48 to be rejected")
	}
}
