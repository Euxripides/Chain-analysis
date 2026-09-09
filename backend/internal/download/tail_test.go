package download

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestTailSplitAllowed(t *testing.T) {
	if tailSplitAllowed(&model.DownloadJob{Source: "benchmark"}) {
		t.Fatal("fixed-shard benchmark must never be tail-split")
	}
	job := &model.DownloadJob{Source: "user_download", Stats: map[string]any{}}
	if !tailSplitAllowed(job) {
		t.Fatal("ordinary job must allow one tail split")
	}
	job.Stats["tail_split_done"] = true
	if tailSplitAllowed(job) {
		t.Fatal("ordinary job must not be tail-split repeatedly in one tail phase")
	}
}
