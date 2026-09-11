package download

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestTerminalJobStatuses(t *testing.T) {
	for _, status := range []string{model.JobCompleted, model.JobPartialFailed, model.JobFailed, model.JobCancelled, model.JobAbortedByUser} {
		if !isTerminalJob(status) {
			t.Fatalf("terminal status %q was not recognized", status)
		}
	}
	if isTerminalJob(model.JobRunning) {
		t.Fatal("running job must not be terminal")
	}
}
