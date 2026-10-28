package contractanalysis

import "testing"

func TestEngineMemoryLimitDefaultsAndOverride(t *testing.T) {
	for value, want := range map[string]string{"": "2g", "6g": "6g", " 6G ": "6g", "4096m": "4096m", "0g": "2g", "6gb": "2g", "-1g": "2g", "6": "2g", "9999999g": "2g"} {
		t.Setenv("CONTRACT_ENGINE_MEMORY", value)
		if got := EngineMemoryLimit(); got != want {
			t.Fatalf("CONTRACT_ENGINE_MEMORY=%q: got %q, want %q", value, got, want)
		}
	}
}
