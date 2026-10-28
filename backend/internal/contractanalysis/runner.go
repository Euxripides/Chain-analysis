package contractanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var imageIdentity = regexp.MustCompile(`^(sha256:[0-9a-f]{64}|[a-zA-Z0-9./_:-]+@sha256:[0-9a-f]{64})$`)
var memoryLimit = regexp.MustCompile(`^[1-9][0-9]{0,5}[mMgG]$`)

// EngineMemoryLimit is the container memory cap (swap disabled). CONTRACT_ENGINE_MEMORY overrides the 2g default; malformed values keep the default.
func EngineMemoryLimit() string {
	if v := strings.TrimSpace(os.Getenv("CONTRACT_ENGINE_MEMORY")); memoryLimit.MatchString(v) {
		return strings.ToLower(v)
	}
	return "2g"
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, fmt.Errorf("engine output exceeds %d bytes", b.max)
	}
	return b.Buffer.Write(p)
}

type Runner struct {
	Image  string
	Docker string
}

// RemoveStale removes only this task's containers from older fencing tokens.
// A crashed Docker client may leave its isolated workload running.
func (r Runner) RemoveStale(ctx context.Context, jobID string, fence int64) error {
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(jobID) || fence < 1 {
		return fmt.Errorf("invalid container ownership")
	}
	docker := r.Docker
	if docker == "" {
		docker = "/usr/bin/docker"
	}
	prefix := "contract-analysis-" + jobID + "-"
	out, err := exec.CommandContext(ctx, docker, "ps", "-a", "--filter", "name=^/"+prefix, "--format", "{{.Names}}").Output()
	if err != nil {
		return fmt.Errorf("inspect owned containers: %w", err)
	}
	for _, name := range strings.Fields(string(out)) {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		old, err := strconv.ParseInt(strings.TrimPrefix(name, prefix), 10, 64)
		if err != nil || old >= fence {
			continue
		}
		if err := exec.CommandContext(ctx, docker, "rm", "-f", name).Run(); err != nil {
			return fmt.Errorf("remove stale owned container: %w", err)
		}
	}
	return nil
}

func (r Runner) Ready() bool { return imageIdentity.MatchString(r.Image) }
func (r Runner) Run(ctx context.Context, jobID string, fence int64, input Input) (Result, error) {
	var result Result
	if !r.Ready() {
		return result, fmt.Errorf("CONTRACT_ENGINE_UNAVAILABLE: engine image must be digest-pinned")
	}
	docker := r.Docker
	if docker == "" {
		docker = "/usr/bin/docker"
	}
	name := fmt.Sprintf("contract-analysis-%s-%d", jobID, fence)
	memory := EngineMemoryLimit()
	// The container has no network, host secrets, mounted project or Docker socket.
	args := []string{"run", "--rm", "--pull", "never", "-i", "--name", name, "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "65532:65532", "--cpus", "2", "--memory", memory, "--memory-swap", memory, "--pids-limit", "128", "--log-driver", "none", "--tmpfs", "/tmp:rw,nosuid,size=192m"}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline).Milliseconds()
		if remaining <= 0 {
			return result, context.DeadlineExceeded
		}
		args = append(args, "--env", "CONTRACT_ANALYSIS_REMAINING_MS="+strconv.FormatInt(remaining, 10))
	}
	args = append(args, "--ulimit", "core=0", r.Image)
	raw := Row(input)
	if len(raw) > 24<<20 {
		return result, fmt.Errorf("engine input exceeds 24 MiB")
	}
	cmd := exec.CommandContext(ctx, docker, args...)
	cmd.Stdin = bytes.NewReader(raw)
	stdout := &limitedBuffer{max: EngineOutputLimit}
	stderr := &limitedBuffer{max: 64 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	// Killing the CLI alone does not kill a Docker workload; remove exactly this owned container.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	remove := exec.CommandContext(cleanup, docker, "rm", "-f", name)
	remove.Stdout = io.Discard
	remove.Stderr = io.Discard
	_ = remove.Run()
	if err != nil {
		return result, fmt.Errorf("ENGINE_RUN_FAILED: %w: %s", err, stderr.String())
	}
	result, err = ParseResult(stdout.Bytes())
	if err != nil {
		return result, err
	}
	resource, found, err := containerExecutionResource(stderr.Bytes(), stdout.Bytes())
	if err != nil {
		return result, err
	}
	rule, _ := strconv.Atoi(strings.TrimPrefix(result.RuleVersion, "funds-semantics-"))
	if rule >= 15 && !found {
		return result, fmt.Errorf("ENGINE_RESOURCE_MISSING: complete container metrics required")
	}
	if found {
		if result.Metrics == nil {
			result.Metrics = map[string]any{}
		}
		result.Metrics["container_execution"] = resource
	}
	return result, nil
}

func containerExecutionResource(stderr, stdout []byte) (map[string]any, bool, error) {
	const prefix = "CONTRACT_CONTAINER_RESOURCE_V1:"
	var resource map[string]any
	found := false
	for _, line := range strings.Split(string(stderr), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if found {
			return nil, false, fmt.Errorf("ENGINE_RESOURCE_CONFLICT: duplicate completed measurements")
		}
		found = true
		raw := []byte(strings.TrimPrefix(line, prefix))
		var identity struct {
			Version         string                 `json:"version"`
			Scope           string                 `json:"scope"`
			Bytes           int64                  `json:"stdout_bytes"`
			SHA256          string                 `json:"stdout_sha256"`
			MemoryPeakBytes uint64                 `json:"memory_peak_bytes"`
			ExitCode        int                    `json:"engine_exit_code"`
			CPUStat         map[string]json.Number `json:"cpu_stat"`
		}
		decodeErr := json.Unmarshal(raw, &identity)
		cpuRecorded := false
		for _, key := range []string{"usage_usec", "usage_nanoseconds"} {
			if value, err := strconv.ParseUint(string(identity.CPUStat[key]), 10, 64); err == nil && value > 0 {
				cpuRecorded = true
			}
		}
		if decodeErr != nil || identity.Version != "container-execution-resource-v1" || identity.Scope != "after_engine_exit_and_complete_output_forwarding" || identity.Bytes != int64(len(stdout)) || identity.SHA256 != SHA256(stdout) || identity.MemoryPeakBytes == 0 || identity.ExitCode != 0 || !cpuRecorded {
			return nil, false, fmt.Errorf("ENGINE_RESOURCE_INTEGRITY_FAILED: completed measurement does not bind engine output")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&resource); err != nil {
			return nil, false, err
		}
	}
	return resource, found, nil
}
