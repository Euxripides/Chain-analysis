package contractanalysis

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The integration image runs an intentionally stalled CLI. It is built only by
// the isolated QA script and never configured on a production worker.
func TestRunnerTimeoutAndOwnedOrphanCleanup(t *testing.T) {
	image := os.Getenv("CONTRACT_RUNNER_FAULT_IMAGE")
	if image == "" {
		t.Skip("isolated integration image not configured")
	}
	runner := Runner{Image: image}
	if !runner.Ready() {
		t.Fatal("unpinned fixture image")
	}
	job := "00000000-0000-4000-8000-0000000000a1"
	other := "00000000-0000-4000-8000-0000000000a2"
	name := "contract-analysis-" + job + "-2"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := runner.Run(ctx, job, 2, Input{}); err == nil {
		t.Fatal("stalled engine unexpectedly returned")
	}
	inspect := func(name string) bool {
		raw, err := exec.Command("/usr/bin/docker", "ps", "-a", "--filter", "name=^/"+name+"$", "--format", "{{.Names}}").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(raw)) == name
	}
	if inspect(name) {
		t.Fatal("timeout retained owned container")
	}
	names := []string{"contract-analysis-" + job + "-1", "contract-analysis-" + job + "-3", "contract-analysis-" + other + "-1"}
	for _, name := range names {
		if inspect(name) {
			t.Fatal("QA container collision", name)
		}
		if err := exec.Command("/usr/bin/docker", "run", "-d", "--name", name, "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", "64m", "--pids-limit", "32", image).Run(); err != nil {
			t.Fatal(err)
		}
		defer exec.Command("/usr/bin/docker", "rm", "-f", name).Run()
	}
	if err := runner.RemoveStale(context.Background(), job, 2); err != nil {
		t.Fatal(err)
	}
	if inspect(names[0]) || !inspect(names[1]) || !inspect(names[2]) {
		t.Fatal("orphan cleanup violated fencing or ownership")
	}
}
