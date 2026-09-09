package download

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// ProgressSnapshot SSE/前端实时进度载荷（1s 聚合推送）。
type ProgressSnapshot struct {
	JobID            string  `json:"job_id"`
	Status           string  `json:"status"`
	Progress         float32 `json:"progress"`
	DownloadedRows   int64   `json:"downloaded_rows"`
	DownloadedBytes  int64   `json:"downloaded_bytes"`
	APIRequests      int64   `json:"api_requests"`
	APICostUSD       float64 `json:"api_cost_usd"`
	RowsPerSec       float64 `json:"rows_per_sec"`
	WriteRowsPerSec  float64 `json:"write_rows_per_sec"`
	BytesPerSec      float64 `json:"bytes_per_sec"`
	ReqPerSec        float64 `json:"req_per_sec"`
	CoverageHitRatio float64 `json:"coverage_hit_ratio"`
	ElapsedSeconds   float64 `json:"elapsed_seconds"`
	ETASeconds       float64 `json:"eta_seconds"`
	TotalShards      int64   `json:"total_shards"`
	DoneShards       int64   `json:"done_shards"`
	// Ankr 引擎（§61/§87）：Fetch / Ingest 双进度
	FetchedRows    int64 `json:"fetched_rows"`
	IngestedRows   int64 `json:"ingested_rows"`
	FetchedShards  int64 `json:"fetched_shards"`
	IngestedShards int64 `json:"ingested_shards"`
	// V1.1（§29-§38）：Validate 进度 + 控制状态 + 瓶颈 + Tail ETA + Page Gap
	ValidatedRows        int64     `json:"validated_rows"`
	ValidatedShards      int64     `json:"validated_shards"`
	ValidatedPages       int64     `json:"validated_pages"`
	ValidationStatus     string    `json:"validation_status"`
	ControlState         string    `json:"control_state"`
	ControlReason        string    `json:"control_reason"`
	Bottleneck           string    `json:"bottleneck"`
	BottleneckDetail     string    `json:"bottleneck_detail"`
	ProviderLatencyAvgMs int64     `json:"provider_latency_avg_ms"`
	PageGapAvgMs         int64     `json:"page_gap_avg_ms"`
	PageSizeCurrent      int       `json:"page_size_current"`
	TailEtaSeconds       float64   `json:"tail_eta_seconds"`
	RetryAmp             float64   `json:"retry_amp"`
	ActiveShards         int64     `json:"active_shards"`
	TS                   time.Time `json:"ts"`

	// V1.2（§74/§75/§80/§85）：全局并发 + 规划/采集成本分账
	GlobalActiveSlots           int64                  `json:"global_active_slots"`
	GlobalWeightedSlots         int64                  `json:"global_weighted_slots"`
	GlobalSafeTarget            int                    `json:"global_concurrency_target"`
	GlobalCeiling               int                    `json:"global_ceiling"`
	GlobalRampPhase             string                 `json:"global_ramp_phase"`
	PlanningAPIRequests         int64                  `json:"planning_api_requests"`
	PlanningAPICostUSD          float64                `json:"planning_api_cost_usd"`
	TimeToFirstFetchMs          int64                  `json:"time_to_first_fetch_ms"`
	TimeToFullSafeConcurrencyMs int64                  `json:"time_to_full_safe_concurrency_ms"`
	ProviderUsage60s            []ProviderUsageWindow  `json:"provider_usage_60s"`
	ProviderUsage5m             []ProviderUsageWindow  `json:"provider_usage_5m"`
	TRC20Metrics                TRC20EnrichmentMetrics `json:"trc20_metrics"`
}

type TRC20EnrichmentMetrics struct {
	Rows                int64          `json:"rows"`
	ReceiptLogicalCalls int64          `json:"receipt_logical_calls"`
	ReceiptHTTPRequests int64          `json:"receipt_http_requests"`
	ReceiptCallsPerRow  float64        `json:"receipt_calls_per_row"`
	LogsRequests        int64          `json:"logs_requests"`
	LogsMatchedRows     int64          `json:"logs_matched_rows"`
	LogsMatchRate       float64        `json:"logs_match_rate"`
	ReceiptFallbackRows int64          `json:"receipt_fallback_rows"`
	ReceiptFallbackPct  float64        `json:"receipt_fallback_pct"`
	SavedReceiptCalls   int64          `json:"saved_receipt_calls"`
	WallClockSeconds    float64        `json:"wall_clock_seconds"`
	RowsPerSecond       float64        `json:"rows_per_second"`
	FailureReasons      map[string]int `json:"failure_reasons"`
}

type ProviderUsageWindow struct {
	Provider   string  `json:"provider"`
	UsageType  string  `json:"usage_type"`
	Requests   int64   `json:"requests"`
	Rows       int64   `json:"rows"`
	QPS        float64 `json:"qps"`
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
	P99Ms      float64 `json:"p99_ms"`
	Errors     int64   `json:"errors"`
	RateLimits int64   `json:"rate_limits"`
	Retries    int64   `json:"retries"`
}

type ratePoint struct {
	rows, bytes, req int64
	at               time.Time
}

type rateTracker struct {
	lastRows  int64
	lastBytes int64
	lastReq   int64
	lastTime  time.Time
	rowsSec   float64
	bytesSec  float64
	reqSec    float64
	history   []ratePoint
}

func (t *rateTracker) update(rows, bytes, req int64, now time.Time) {
	dt := now.Sub(t.lastTime).Seconds()
	if dt < 0.05 {
		return
	}
	t.history = append(t.history, ratePoint{rows: rows, bytes: bytes, req: req, at: now})
	cutoff := now.Add(-60 * time.Second)
	first := 0
	for first+1 < len(t.history) && t.history[first+1].at.Before(cutoff) {
		first++
	}
	if first > 0 {
		t.history = append([]ratePoint(nil), t.history[first:]...)
	}
	base := t.history[0]
	window := now.Sub(base.at).Seconds()
	if window >= 1 {
		t.rowsSec = float64(rows-base.rows) / window
		t.bytesSec = float64(bytes-base.bytes) / window
		t.reqSec = float64(req-base.req) / window
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
			sc.v14RuntimeTick(ctx) // V1.4：safe target / queue depth / phase-aware inflight / retry amp（§23/§28/§52）
			if !sc.cfg.Global.AutoTuneEnabled {
				sc.persistStaticTelemetry(ctx) // gauges/heartbeat must move in AIMD mode too
			}
			observability.DownloadActiveJobs.Set(float64(activeJobs))
			observability.DownloadRowsPerSec.Set(rowsSec)
			observability.DownloadBytesPerSec.Set(bytesSec)
			observability.DownloadReqPerSec.Set(reqSec)
			var activeShards int64
			_ = sc.store.Pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM download_shards WHERE status IN ('LEASED','RUNNING')").Scan(&activeShards)
			observability.DownloadActiveShards.Set(float64(activeShards))
			var engineActive int64
			_ = sc.store.Pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM download_shards WHERE status IN ('LEASED','RUNNING','FETCHED','INGESTED')").Scan(&engineActive)
			observability.AnkrShardsActive.Set(float64(engineActive))
			setGoodput(rowsSec)
			observability.AnkrFetchGoodput.Set(rowsSec)
			if sc.pipeline != nil && sc.pipeline.agg != nil {
				observability.CHPendingRows.Set(float64(sc.pipeline.agg.PendingBytes()))
			}
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
		"SELECT COALESCE(SUM(downloaded_rows) FILTER (WHERE "+store.EffectiveShardSQL("")+"),0), "+
			"COALESCE(SUM(downloaded_bytes) FILTER (WHERE "+store.EffectiveShardSQL("")+"),0), "+
			"COUNT(*) FILTER (WHERE "+store.EffectiveShardSQL("")+"), "+
			"COUNT(*) FILTER (WHERE status='COMPLETED' AND "+store.EffectiveShardSQL("")+"), "+
			"COUNT(*) FILTER (WHERE status IN ('FETCHED','INGESTED','COMPLETED') AND "+store.EffectiveShardSQL("")+"), "+
			"COUNT(*) FILTER (WHERE status IN ('INGESTED','COMPLETED') AND "+store.EffectiveShardSQL("")+") "+
			"FROM download_shards WHERE job_id=$1", job.ID).
		Scan(&snap.DownloadedRows, &snap.DownloadedBytes, &snap.TotalShards, &snap.DoneShards, &snap.FetchedShards, &snap.IngestedShards)
	// 页面级 Fetch/Ingest 行数（§61 双进度）
	if fetched, err := sc.store.FetchedRowsByJob(ctx, job.ID); err == nil {
		snap.FetchedRows = fetched
	}
	if ingested, err := sc.store.IngestedRowsByJob(ctx, job.ID); err == nil {
		snap.IngestedRows = ingested
	}
	// V1.1 Validate 进度（§30）
	if vp, vs, vr, err := sc.store.ValidatedStatsByJob(ctx, job.ID); err == nil {
		snap.ValidatedPages = vp
		snap.ValidatedShards = vs
		snap.ValidatedRows = vr
	}
	if snap.TotalShards > 0 && snap.ValidatedShards == snap.TotalShards {
		snap.ValidationStatus = "COMPLETE"
	} else if snap.ValidatedPages > 0 || snap.ValidatedShards > 0 {
		snap.ValidationStatus = "VALIDATING"
	} else {
		snap.ValidationStatus = "PENDING"
	}
	// V1.1 控制状态 / 瓶颈 / Tail ETA / Page Gap（§31-§38）
	if sc.bp != nil {
		snap.ControlState = sc.bp.State().String()
		snap.ControlReason = sc.bp.Reason()
	}
	snap.Bottleneck, snap.BottleneckDetail, snap.ProviderLatencyAvgMs = sc.bottleneckClass(ctx, job.ID)
	if gap, _, err := sc.store.PageGapStatsByJob(ctx, job.ID); err == nil {
		snap.PageGapAvgMs = gap
	}
	snap.PageSizeCurrent = sc.currentPageSizeForJob(job.ID)
	if job.Stats != nil {
		if v, ok := job.Stats["tail_eta_seconds"].(float64); ok {
			snap.TailEtaSeconds = v
		}
	}
	// Retry Amplification（§61 Gate）
	var attempts, requests int64
	_ = sc.store.Pool.QueryRow(ctx, "SELECT COUNT(*), COUNT(*) FILTER (WHERE retry_attempt=0) FROM provider_usage WHERE job_id=$1 AND usage_type<>'trc20_enrichment_summary'", job.ID).Scan(&attempts, &requests)
	if requests > 0 {
		snap.RetryAmp = float64(attempts) / float64(requests)
	}
	_ = sc.store.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM download_shards WHERE job_id=$1 AND status IN ('LEASED','RUNNING','FETCHED','INGESTED')", job.ID).Scan(&snap.ActiveShards)
	_ = sc.store.Pool.QueryRow(ctx,
		"SELECT COUNT(*), COALESCE(SUM(estimated_cost_usd),0), "+
			"COUNT(*) FILTER (WHERE usage_type LIKE 'count_oracle%'), "+
			"COALESCE(SUM(estimated_cost_usd) FILTER (WHERE usage_type LIKE 'count_oracle%'),0) "+
			"FROM provider_usage WHERE job_id=$1 AND usage_type<>'trc20_enrichment_summary'", job.ID).
		Scan(&snap.APIRequests, &snap.APICostUSD, &snap.PlanningAPIRequests, &snap.PlanningAPICostUSD)
	snap.ProviderUsage60s = sc.providerUsageWindow(ctx, job.ID, time.Minute)
	snap.ProviderUsage5m = sc.providerUsageWindow(ctx, job.ID, 5*time.Minute)
	snap.TRC20Metrics = sc.trc20EnrichmentMetrics(ctx, job.ID)
	if snap.TRC20Metrics.ReceiptCallsPerRow > .8 {
		snap.Bottleneck, snap.BottleneckDetail = "SEVERE_RECEIPT_AMPLIFICATION", "Receipt logical calls exceed 0.8 per TRC20 row"
	} else if snap.TRC20Metrics.ReceiptCallsPerRow > .5 {
		snap.Bottleneck, snap.BottleneckDetail = "RECEIPT_AMPLIFICATION", "Receipt logical calls exceed 0.5 per TRC20 row"
	}
	if v, ok := job.Stats["time_to_first_fetch_ms"].(float64); ok {
		snap.TimeToFirstFetchMs = int64(v)
	}
	// V1.2：全局并发（§80 Global Concurrency Card）
	snap.GlobalActiveSlots = atomic.LoadInt64(&sc.active)
	snap.GlobalWeightedSlots = atomic.LoadInt64(&sc.weighted)
	snap.GlobalSafeTarget = sc.currentConcurrency()
	snap.GlobalCeiling = sc.ceiling()
	if v, ok := sc.rampPhase.Load().(string); ok {
		snap.GlobalRampPhase = v
	}
	if st, err := sc.store.GetSchedulerState(ctx); err == nil && st != nil {
		snap.TimeToFullSafeConcurrencyMs = st.TimeToFullSafeConcurrencyMs
	}

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
	if snap.TRC20Metrics.Rows > 0 {
		snap.TRC20Metrics.WallClockSeconds = snap.ElapsedSeconds
		if snap.ElapsedSeconds > 0 {
			snap.TRC20Metrics.RowsPerSecond = float64(snap.TRC20Metrics.Rows) / snap.ElapsedSeconds
		}
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

func (sc *Scheduler) providerUsageWindow(ctx context.Context, jobID string, window time.Duration) []ProviderUsageWindow {
	seconds := int64(window.Seconds())
	rows, err := sc.store.Pool.Query(ctx, `SELECT provider,COALESCE(usage_type,''),COUNT(*),COALESCE(SUM(returned_rows),0),
		COUNT(*)/$2::float8,COALESCE(percentile_cont(.50) WITHIN GROUP (ORDER BY duration_ms),0),
		COALESCE(percentile_cont(.95) WITHIN GROUP (ORDER BY duration_ms),0),COALESCE(percentile_cont(.99) WITHIN GROUP (ORDER BY duration_ms),0),
		COUNT(*) FILTER (WHERE status NOT IN ('ok','success','cache_hit','singleflight_hit') OR http_status>=400),
		COUNT(*) FILTER (WHERE http_status=429),COUNT(*) FILTER (WHERE retry_attempt>0)
		FROM provider_usage WHERE job_id=$1 AND usage_type<>'trc20_enrichment_summary' AND request_time>=now()-make_interval(secs=>$2) GROUP BY provider,usage_type ORDER BY COUNT(*) DESC`, jobID, seconds)
	if err != nil {
		return []ProviderUsageWindow{}
	}
	defer rows.Close()
	result := []ProviderUsageWindow{}
	for rows.Next() {
		var item ProviderUsageWindow
		if rows.Scan(&item.Provider, &item.UsageType, &item.Requests, &item.Rows, &item.QPS, &item.P50Ms, &item.P95Ms, &item.P99Ms, &item.Errors, &item.RateLimits, &item.Retries) == nil {
			result = append(result, item)
		}
	}
	return result
}

func (sc *Scheduler) trc20EnrichmentMetrics(ctx context.Context, jobID string) TRC20EnrichmentMetrics {
	result := TRC20EnrichmentMetrics{FailureReasons: map[string]int{}}
	_ = sc.store.Pool.QueryRow(ctx, `SELECT COALESCE(SUM(rows),0),COALESCE(SUM(logical_calls),0),COALESCE(SUM(http_requests),0),
		COALESCE(SUM(logs_requests),0),COALESCE(SUM(matched_rows),0),COALESCE(SUM(fallback_rows),0),COALESCE(SUM(saved_calls),0),
		COALESCE(EXTRACT(EPOCH FROM (MAX(request_time)-MIN(request_time))),0)
		FROM provider_usage WHERE job_id=$1 AND usage_type='trc20_enrichment_summary'`, jobID).
		Scan(&result.Rows, &result.ReceiptLogicalCalls, &result.ReceiptHTTPRequests, &result.LogsRequests, &result.LogsMatchedRows, &result.ReceiptFallbackRows, &result.SavedReceiptCalls, &result.WallClockSeconds)
	if result.Rows > 0 {
		result.ReceiptCallsPerRow = float64(result.ReceiptLogicalCalls) / float64(result.Rows)
		result.LogsMatchRate = float64(result.LogsMatchedRows) / float64(result.Rows)
		result.ReceiptFallbackPct = float64(result.ReceiptFallbackRows) / float64(result.Rows)
	}
	rows, err := sc.store.Pool.Query(ctx, "SELECT failure_reasons FROM provider_usage WHERE job_id=$1 AND usage_type='trc20_enrichment_summary'", jobID)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) == nil {
			var reasons map[string]int
			if json.Unmarshal(raw, &reasons) != nil {
				continue
			}
			for reason, count := range reasons {
				result.FailureReasons[reason] += count
			}
		}
	}
	return result
}

func isTerminalJob(status string) bool {
	switch status {
	case model.JobCompleted, model.JobPartialFailed, model.JobFailed, model.JobCancelled:
		return true
	}
	return false
}

// bottleneckClass V1.1 §36-§38：PROVIDER / LOCAL_SPOOL / PARSER / CLICKHOUSE / BACKPRESSURE / UNKNOWN。
func (sc *Scheduler) bottleneckClass(ctx context.Context, jobID string) (class, detail string, providerAvgMs int64) {
	var providerMs, count int64
	_ = sc.store.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(duration_ms),0), COUNT(*) FROM provider_usage WHERE job_id=$1 AND status='ok'", jobID).Scan(&providerMs, &count)
	if count > 0 {
		providerAvgMs = providerMs / count
	}
	if sc.bp != nil && sc.bp.State() >= stateProtected {
		return "BACKPRESSURE", sc.bp.Reason(), providerAvgMs
	}
	if sc.pipeline != nil && sc.pipeline.agg != nil && (!sc.pipeline.agg.Healthy() || sc.pipeline.agg.PendingBytes() > 0) {
		return "CLICKHOUSE", "ingest queue pending", providerAvgMs
	}
	// Provider 主导判断（§37）：provider_time / total_fetch_time >= 80%
	var fetchMs int64
	_ = sc.store.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(fetch_duration_ms),0) FROM download_shards WHERE job_id=$1", jobID).Scan(&fetchMs)
	if fetchMs > 0 && float64(providerMs) >= float64(fetchMs)*0.8 {
		return "PROVIDER", fmt.Sprintf("avg provider latency %dms", providerAvgMs), providerAvgMs
	}
	if sc.bp != nil {
		if r := sc.bp.cachedUsedRatio(); r > 0.5 {
			return "LOCAL_SPOOL", fmt.Sprintf("spool used %.0f%%", r*100), providerAvgMs
		}
	}
	return "PARSER", "", providerAvgMs
}

func (sc *Scheduler) currentPageSizeForJob(jobID string) int {
	// 活跃 Shard 中最大的 page_size（§31 UI）
	var n int
	_ = sc.store.Pool.QueryRow(context.Background(), "SELECT COALESCE(MAX(page_size),0) FROM download_shards WHERE job_id=$1 AND status IN ('LEASED','RUNNING','FETCHED','INGESTED')", jobID).Scan(&n)
	return n
}

func (sc *Scheduler) buildFinalSnapshot(ctx context.Context, job *model.DownloadJob) ProgressSnapshot {
	snap, err := sc.buildSnapshot(ctx, job)
	if err != nil {
		return ProgressSnapshot{JobID: job.ID, Status: job.Status, TS: time.Now()}
	}
	return snap
}
