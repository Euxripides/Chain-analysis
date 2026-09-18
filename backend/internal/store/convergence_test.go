package store

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// 终态收敛矩阵：只要所有有效 Shard 都是终态，Dataset 必须离开 RUNNING。
func TestDatasetStatusForTerminal(t *testing.T) {
	cases := []struct {
		name                        string
		completed, terminal, total  int64
		want                        string
	}{
		{"no shards planned", 0, 0, 0, model.ItemPending},
		{"all completed", 5, 5, 5, model.ItemCompleted},
		{"all failed final", 0, 25, 25, model.ItemFailed},
		{"mixed completed and failed final", 75, 100, 100, model.ItemFailed},
		{"work still in flight", 3, 4, 10, model.ItemRunning},
		{"nothing failed yet", 0, 0, 10, model.ItemRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := datasetStatusForTerminal(tc.completed, tc.terminal, tc.total); got != tc.want {
				t.Fatalf("datasetStatusForTerminal(%d,%d,%d)=%q want %q", tc.completed, tc.terminal, tc.total, got, tc.want)
			}
		})
	}
}

func TestSubjectStatusForTerminal(t *testing.T) {
	cases := []struct {
		name                    string
		all, done, terminal     int
		anyFailed               bool
		want                    string
	}{
		{"all datasets completed", 4, 4, 4, false, model.ItemCompleted},
		{"all terminal, one failed", 4, 3, 4, true, model.ItemFailed},
		{"all datasets failed", 2, 0, 2, true, model.ItemFailed},
		{"still running", 4, 1, 2, false, model.ItemRunning},
		{"failure but one dataset still running", 4, 3, 3, true, model.ItemRunning},
		{"no datasets", 0, 0, 0, false, model.ItemRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subjectStatusForTerminal(tc.all, tc.done, tc.terminal, tc.anyFailed); got != tc.want {
				t.Fatalf("subjectStatusForTerminal(%d,%d,%d,%v)=%q want %q", tc.all, tc.done, tc.terminal, tc.anyFailed, got, tc.want)
			}
		})
	}
}

func TestJobStatusForTerminal(t *testing.T) {
	cases := []struct {
		name                        string
		all, done, terminal, subs   int
		anyFailed                   bool
		progress                    float32
		want                        string
	}{
		{"all addresses completed", 10, 10, 10, 10, false, 1, model.JobCompleted},
		{"partial: some addresses failed", 10, 7, 10, 10, true, 0.7, model.JobPartialFailed},
		{"all addresses failed but rows were produced", 25, 0, 25, 25, true, 0.75, model.JobPartialFailed},
		{"all addresses failed with no data", 3, 0, 3, 3, true, 0, model.JobFailed},
		{"still running", 10, 2, 3, 10, false, 0.2, model.JobRunning},
		{"no subjects yet", 0, 0, 0, 0, false, 0, model.JobQueued},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jobStatusForTerminal(tc.all, tc.done, tc.terminal, tc.anyFailed, tc.subs, tc.progress); got != tc.want {
				t.Fatalf("jobStatusForTerminal(%d,%d,%d,%v,%d,%v)=%q want %q", tc.all, tc.done, tc.terminal, tc.anyFailed, tc.subs, tc.progress, got, tc.want)
			}
		})
	}
}
