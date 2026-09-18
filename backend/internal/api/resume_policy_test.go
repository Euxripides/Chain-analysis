package api

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// "暂停之后不能恢复下载" 的回归测试：这些状态必须允许恢复。
func TestCanResumeJobAllowsPausedAndTerminalFailure(t *testing.T) {
	allowed := []string{
		model.JobPaused,
		model.JobPausedFallbackTooExpensive,
		model.JobPauseReq, // 暂停收敛中的状态，旧实现固定 409
		model.JobFailed,
		model.JobPartialFailed,
	}
	for _, status := range allowed {
		if !canResumeJob(status) {
			t.Fatalf("status %q must be resumable", status)
		}
	}
	denied := []string{
		model.JobRunning, model.JobQueued, model.JobCompleted,
		model.JobCancelled, model.JobAbortedByUser, model.JobCreated,
	}
	for _, status := range denied {
		if canResumeJob(status) {
			t.Fatalf("status %q must not be resumable", status)
		}
	}
}
