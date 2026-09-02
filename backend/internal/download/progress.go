package download

import (
	"context"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
)

// ProgressSnapshot SSE/前端实时进度载荷（1s 聚合推送）。
type ProgressSnapshot struct {
	JobID            string    `json:"job_id"`
	Status           string    `json:"status"`
	Progress         float32   `json:"progress"`
	DownloadedRows   int64     `json:"downloaded_rows"`
	DownloadedBytes  int64     `json:"downloaded_bytes"`
	APIRequests      int64     `json:"api_requests"`
	APICostUSD       float64   `json:"api_cost_usd"`
	RowsPerSec       float64   `json:"rows_per_sec"`
	WriteRowsPerSec  float64   `json:"write_rows_per_sec"`
	BytesPerSec      float64   `json:"bytes_per_sec"`
	ReqPerSec        float64   `json:"req_per_sec"`
	CoverageHitRatio float64   `json:"coverage_hit_ratio"`
	ElapsedSeconds   float64   `json:"elapsed_seconds"`
	ETASeconds       float64   `json:"eta_seconds"`
	TotalShards      int64     `json:"total_shards"`
	DoneShards       int64     `json:"done_shards"`
	TS               time.Time `json:"ts"`
}

type rateTracker struct {
	lastRows  int64
	lastBytes int64
	lastReq   int64
	lastTime  time.Time
	rowsSec   float64
	bytesSec  float64
	reqSec    float64
}

func (t *rateTracker) update(rows, bytes, req int64, now time.Time) {
	dt := now.Sub(t.lastTime).Seconds()
	if dt < 0.05 {
		return
	}
	ir := float64(rows-t.lastRows) / dt
	ib := float64(bytes-t.lastBytes) / dt
	iq := float64(req-t.lastReq) / dt
	if t.lastRows == 0 {
		t.rowsSec, t.bytesSec, t.reqSec = ir, ib, iq
	} else {
		alpha := 0.3
		t.rowsSec = t.rowsSec*(1-alpha) + ir*alpha
		t.bytesSec = t.bytesSec*(1-alpha) + ib*alpha
		t.reqSec = t.reqSec*(1-alpha) + iq*alpha
	}
	t.lastRows, t.lastBytes, t.lastReq, t.lastTime = rows, bytes, req, now
}

func (sc *Scheduler) progressLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jobs, err := sc.store.ListJobs(ctx, "", 200, 0)
			if err != nil {
				continue
			}
			var activeJobs int
			var rowsSec, bytesSec, reqSec float64
			for i := range jobs {
				j := jobs[i]
				if !isActiveJob(j.Status) {
					continue
				}
				activeJobs++
				snap, err := sc.buildSnapshot(ctx, &j)
				if err != nil {
					continue
				}
				rowsSec += snap.RowsPerSec
				bytesSec += snap.BytesPerSec
				reqSec += snap.ReqPerSec
				if snap.CoverageHitRatio > 0 {
					observability.CoverageHitRatio.Set(snap.CoverageHitRatio)
				}
				sc.publish("events.progress."+j.ID, snap)
			}
			observability.DownloadActiveJobs.Set(float64(activeJobs))
			observability.DownloadRowsPerSec.Set(rowsSec)
			observability.DownloadBytesPerSec.Set(bytesSec)
			observability.DownloadReqPerSec.Set(reqSec)
			var activeShards int64
			_ = sc.store.Pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM download_shards WHERE status IN ('LEASED','RUNNING')").Scan(&activeShards)
			observability.DownloadActiveShards.Set(float64(activeShards))
		}
	}
}

func isActiveJob(status string) bool {
	switch status {
	case model.JobRunning, model.JobPauseReq, model.JobResuming, model.JobCancelReq:
		return true
	}
	return false
}

func (sc *Scheduler) buildSnapshot(ctx context.Context, job *model.DownloadJob) (ProgressSnapshot, error) {
	snap := ProgressSnapshot{
		JobID: job.ID, Status: job.Status, CoverageHitRatio: 0,
		TS: time.Now(),
	}
	if v, ok := job.Stats["coverage_before"].(float64); ok {
		snap.CoverageHitRatio = v
	}
	_ = sc.store.Pool.QueryRow(ctx,
		"SELECT COALESCE(SUM(downloaded_rows),0), COALESCE(SUM(downloaded_bytes),0), COUNT(*), "+
			"COUNT(*) FILTER (WHERE status='COMPLETED') FROM download_shards WHERE job_id=$1", job.ID).
		Scan(&snap.DownloadedRows, &snap.DownloadedBytes, &snap.TotalShards, &snap.DoneShards)
	_ = sc.store.Pool.QueryRow(ctx,
		"SELECT COUNT(*), COALESCE(SUM(estimated_cost_usd),0) FROM provider_usage WHERE job_id=$1", job.ID).
		Scan(&snap.APIRequests, &snap.APICostUSD)

	if snap.TotalShards > 0 {
		snap.Progress = float32(snap.DoneShards) / float32(snap.TotalShards)
	}
	// 行级进度（已有数据按区间进度，行数只是展示）
	if created := job.CreatedAt; !created.IsZero() {
		end := time.Now()
		if isTerminalJob(job.Status) && !job.UpdatedAt.IsZero() {
			end = job.UpdatedAt
		}
		snap.ElapsedSeconds = maxF(end.Sub(created).Seconds(), 0)
	}

	now := time.Now()
	var tr *rateTracker
	if v, ok := sc.rates.Load(job.ID); ok {
		tr = v.(*rateTracker)
	} else {
		tr = &rateTracker{lastTime: now}
		sc.rates.Store(job.ID, tr)
	}
	tr.update(snap.DownloadedRows, snap.DownloadedBytes, snap.APIRequests, now)
	snap.RowsPerSec = tr.rowsSec
	snap.BytesPerSec = tr.bytesSec
	snap.ReqPerSec = tr.reqSec
	snap.WriteRowsPerSec = tr.rowsSec // V1 展示同一速率，写入有独立 backlog 时再拆分
	if snap.Progress > 0 && snap.Progress < 1 {
		rate := float64(snap.Progress) / maxF(snap.ElapsedSeconds, 1)
		if rate > 0 {
			snap.ETASeconds = (1 - float64(snap.Progress)) / rate
		}
	}
	if job.Status == model.JobCompleted || job.Status == model.JobPartialFailed || job.Status == model.JobCancelled {
		snap.ETASeconds = 0
	}
	return snap, nil
}

func isTerminalJob(status string) bool {
	switch status {
	case model.JobCompleted, model.JobPartialFailed, model.JobFailed, model.JobCancelled:
		return true
	}
	return false
}

func (sc *Scheduler) buildFinalSnapshot(ctx context.Context, job *model.DownloadJob) ProgressSnapshot {
	snap, err := sc.buildSnapshot(ctx, job)
	if err != nil {
		return ProgressSnapshot{JobID: job.ID, Status: job.Status, TS: time.Now()}
	}
	return snap
}
