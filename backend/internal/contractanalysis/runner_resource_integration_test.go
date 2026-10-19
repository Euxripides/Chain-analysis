package contractanalysis

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestContractRunnerCompletedResourceObservation(t *testing.T) {
	image := os.Getenv("CONTRACT_TEST_IMAGE")
	if image == "" {
		t.Skip("pinned candidate image not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := (Runner{Image: image}).Run(ctx, "00000000-0000-4000-8000-0000000000b3", 1, Input{Operation: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	resource, ok := result.Metrics["container_execution"].(map[string]any)
	if !ok || resource["scope"] != "after_engine_exit_and_complete_output_forwarding" {
		t.Fatal("completed resource observation not retained", result.Metrics)
	}
	peak, ok := resource["memory_peak_bytes"].(json.Number)
	if !ok {
		t.Fatal("memory precision lost", resource)
	}
	if n, err := peak.Int64(); err != nil || n <= 0 {
		t.Fatal("complete memory peak absent", resource)
	}
	if len(resource["stdout_sha256"].(string)) != 64 {
		t.Fatal("wire binding absent")
	}
}
