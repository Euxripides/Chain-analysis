package contractanalysis

import (
	"strings"
	"testing"
)

func TestContainerExecutionResourceBindsCompleteOutput(t *testing.T) {
	output := []byte(`{"complete":true}`)
	record := map[string]any{"version": "container-execution-resource-v1", "scope": "after_engine_exit_and_complete_output_forwarding", "stdout_bytes": len(output), "stdout_sha256": SHA256(output), "memory_peak_bytes": 123456789, "engine_exit_code": 0, "cpu_stat": map[string]any{"usage_usec": 1234}}
	line := func(record map[string]any) []byte {
		return []byte("CONTRACT_CONTAINER_RESOURCE_V1:" + string(Row(record)) + "\n")
	}
	if _, found, err := containerExecutionResource(line(record), output); err != nil || !found {
		t.Fatal("valid completed measurement rejected", found, err)
	}
	if _, found, err := containerExecutionResource(nil, output); err != nil || found {
		t.Fatal("legacy absence was not preserved")
	}
	if _, _, err := containerExecutionResource(append(line(record), line(record)...), output); err == nil || !strings.Contains(err.Error(), "CONFLICT") {
		t.Fatal("duplicate measurement accepted")
	}
	for _, fault := range []string{"stdout_sha256", "stdout_bytes", "memory_peak_bytes", "engine_exit_code", "scope", "cpu_stat"} {
		changed := map[string]any{}
		for key, value := range record {
			changed[key] = value
		}
		switch fault {
		case "stdout_sha256":
			changed[fault] = SHA256([]byte("different"))
		case "stdout_bytes":
			changed[fault] = len(output) + 1
		case "memory_peak_bytes":
			changed[fault] = 0
		case "engine_exit_code":
			changed[fault] = 1
		case "scope":
			changed[fault] = "before_serialization"
		case "cpu_stat":
			changed[fault] = nil
		}
		if _, _, err := containerExecutionResource(line(changed), output); err == nil {
			t.Fatal("invalid completed measurement accepted", fault)
		}
	}
}
