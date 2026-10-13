package download

import (
	"context"
	"os"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func (sc *Scheduler) autoRetryLoop(ctx context.Context) {
	// The fixed deployment cutoff protects historical tasks from uncontrolled
	// paid replay; explicit admission persists their budget in download_retry.
	after := time.Now()
	if cutoff, err := time.Parse(time.RFC3339, os.Getenv("DOWNLOAD_AUTO_RETRY_AFTER")); err == nil {
		after = cutoff
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if os.Getenv("DOWNLOAD_AUTO_RETRY_ENABLED") == "false" {
				continue
			}
			ids, err := sc.store.DownloadRetryCandidates(ctx, after)
			if err != nil {
				sc.log.Error("recovery scan failed", "err", err)
				continue
			}
			for _, id := range ids {
				if n, err := sc.store.AdvanceDownloadRetry(ctx, id, time.Now()); err != nil {
					sc.log.Error("recovery admission failed", "job_id", id, "err", err)
				} else if n > 0 {
					sc.log.Info("budgeted recovery scheduled", "job_id", id, "shards", n)
					sc.publish("events.jobs."+id, map[string]any{"type": "job_status", "job_id": id, "status": "QUEUED", "ts": time.Now()})
				}
			}
		}
	}
}

func (sc *Scheduler) withRetryRequestBudget(ctx context.Context, jobID string, stats map[string]any) context.Context {
	if store.RetryState(stats).State == "" {
		return ctx
	}
	return provider.WithDownloadBudget(ctx, func(ctx context.Context, calls int64, credits, cost float64) error {
		return sc.store.ReserveDownloadRetryRequest(ctx, jobID, calls, credits, cost)
	})
}
