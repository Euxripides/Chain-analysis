package contractanalysis

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var imageIdentity = regexp.MustCompile(`^(sha256:[0-9a-f]{64}|[a-zA-Z0-9./_:-]+@sha256:[0-9a-f]{64})$`)

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
	// The container has no network, host secrets, mounted project or Docker socket.
	args := []string{"run", "--rm", "--pull", "never", "-i", "--name", name, "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "65532:65532", "--cpus", "2", "--memory", "2g", "--memory-swap", "2g", "--pids-limit", "128", "--log-driver", "none", "--tmpfs", "/tmp:rw,nosuid,size=192m", r.Image}
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
	return ParseResult(stdout.Bytes())
}
