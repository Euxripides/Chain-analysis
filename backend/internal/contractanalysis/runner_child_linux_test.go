//go:build linux

package contractanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func ownedProcess(pid string) (state, start string, err error) {
	raw, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return "", "", err
	}
	end := strings.LastIndex(string(raw), ") ")
	if end < 0 {
		return "", "", errors.New("invalid owned process stat")
	}
	fields := strings.Fields(string(raw)[end+2:])
	if len(fields) < 20 {
		return "", "", errors.New("incomplete owned process stat")
	}
	return fields[0], fields[19], nil
}

func TestRunnerDeadlineKillsOwnedChildAndSharesRemainingBudget(t *testing.T) {
	image := os.Getenv("CONTRACT_RUNNER_FAULT_IMAGE")
	if image == "" {
		t.Skip("isolated child fixture image not configured")
	}
	job := "00000000-0000-4000-8000-0000000000b1"
	name := "contract-analysis-" + job + "-1"
	if exec.Command("docker", "inspect", name).Run() == nil {
		t.Fatal("owned QA name already exists")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := (Runner{Image: image}).Run(ctx, job, 1, Input{}); done <- err }()
	var childPID, childStart string
	var environment []string
	var coreDisabled bool
	for until := time.Now().Add(4 * time.Second); time.Now().Before(until); {
		raw, err := exec.Command("docker", "inspect", name).Output()
		if err == nil {
			var list []struct {
				Config     struct{ Env []string }
				HostConfig struct {
					Ulimits []struct {
						Name       string
						Soft, Hard int64
					}
				}
			}
			if json.Unmarshal(raw, &list) == nil && len(list) == 1 {
				environment = list[0].Config.Env
				for _, limit := range list[0].HostConfig.Ulimits {
					if limit.Name == "core" && limit.Soft == 0 && limit.Hard == 0 {
						coreDisabled = true
					}
				}
			}
			top, _ := exec.Command("docker", "top", name, "-eo", "pid,comm").Output()
			for _, line := range strings.Split(string(top), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[1] == "sleep" {
					childPID = fields[0]
					_, childStart, err = ownedProcess(childPID)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		if childPID != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := <-done; err == nil {
		t.Fatal("stalled child unexpectedly returned")
	}
	if childPID == "" {
		t.Fatal("actual child process was not observed")
	}
	if !coreDisabled {
		t.Fatal("owned runtime core dump was not disabled")
	}
	shared := false
	for _, value := range environment {
		if strings.HasPrefix(value, "CONTRACT_ANALYSIS_REMAINING_MS=") {
			ms, err := strconv.ParseInt(strings.TrimPrefix(value, "CONTRACT_ANALYSIS_REMAINING_MS="), 10, 64)
			shared = err == nil && ms > 0 && ms <= 5000
		}
	}
	if !shared {
		t.Fatal("runner did not share the original remaining deadline")
	}
	if exec.Command("docker", "inspect", name).Run() == nil {
		t.Fatal("owned container retained after deadline")
	}
	state, start, err := ownedProcess(childPID)
	if err == nil && start == childStart && state != "Z" {
		t.Fatal("owned child survived container cleanup")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
