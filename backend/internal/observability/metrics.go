// Package observability：Prometheus 指标（文档 §74）。
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Provider 层
	AnkrRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_requests_total", Help: "Ankr API 请求总数（按数据集与结果）",
	}, []string{"provider", "dataset", "status"})
	AnkrRequestsSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_requests_success_total", Help: "Ankr 成功请求数",
	}, []string{"provider", "dataset"})
	AnkrRequestsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_requests_failed_total", Help: "Ankr 失败请求数",
	}, []string{"provider", "dataset"})
	Ankr429 = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_429_total", Help: "Ankr 429 限流次数",
	}, []string{"provider", "dataset"})
	Ankr5xx = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_5xx_total", Help: "Ankr 5xx 错误次数",
	}, []string{"provider", "dataset"})
	AnkrLatencyMs = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "ankr_latency_ms", Help: "Ankr 请求延迟(ms)",
		Buckets: []float64{50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 120000},
	}, []string{"provider", "dataset"})
	AnkrRows = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_rows_total", Help: "Ankr 返回行数",
	}, []string{"provider", "dataset"})
	AnkrBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_bytes_total", Help: "Ankr 返回字节数",
	}, []string{"provider", "dataset"})
	AnkrProviderRootFailure         = promauto.NewCounterVec(prometheus.CounterOpts{Name: "ankr_provider_root_failure_total", Help: "Provider root failures; circuit rejections excluded"}, []string{"chain", "dataset", "error_class"})
	AnkrCircuitOpenTransition       = promauto.NewCounter(prometheus.CounterOpts{Name: "ankr_circuit_open_transition_total", Help: "Circuit transitions into OPEN"})
	AnkrCircuitRejection            = promauto.NewCounterVec(prometheus.CounterOpts{Name: "ankr_circuit_rejection_total", Help: "Requests rejected by an open circuit"}, []string{"chain", "dataset"})
	TronTRC20PrimaryRequests        = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_primary_requests_total", Help: "TronGrid TRC20 primary page requests"})
	TronTRC20PrimaryRows            = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_primary_rows_total", Help: "Rows returned by the TronGrid TRC20 primary path"})
	TronTRC20FallbackRequests       = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_fallback_requests_total", Help: "Estimated Ankr eth_getLogs calls used by TRC20 fallback"})
	TronTRC20FallbackRows           = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_fallback_rows_total", Help: "Rows returned by Ankr TRC20 fallback"})
	TronTRC20ProviderSwitches       = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_provider_switch_total", Help: "TRC20 primary to fallback provider switches"})
	TronTRC20FallbackWindows        = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_fallback_windows_total", Help: "TRC20 fallback block windows completed"})
	TronReceiptRequests             = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_receipt_requests_total", Help: "Ankr upstream receipt requests used by TRC20 enrichment"})
	TronReceiptErrors               = promauto.NewCounterVec(prometheus.CounterOpts{Name: "tron_receipt_errors_total", Help: "TRC20 receipt enrichment upstream errors by class"}, []string{"class"})
	TronReceiptBytes                = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_receipt_response_bytes_total", Help: "TRC20 receipt enrichment upstream response bytes"})
	TronReceiptLatencyMs            = promauto.NewHistogram(prometheus.HistogramOpts{Name: "tron_receipt_latency_ms", Help: "TRC20 receipt enrichment upstream latency in milliseconds", Buckets: []float64{50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000}})
	TronReceiptCacheHits            = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_receipt_cache_hit_total", Help: "TRC20 receipt enrichment cache hits"})
	TronReceiptCacheMisses          = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_receipt_cache_miss_total", Help: "TRC20 receipt enrichment cache misses"})
	TronReceiptSingleflightShared   = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_receipt_singleflight_shared_total", Help: "TRC20 receipt callers sharing an in-flight upstream request"})
	TronReceiptActiveWorkers        = promauto.NewGauge(prometheus.GaugeOpts{Name: "tron_receipt_active_workers", Help: "Active Ankr receipt enrichment workers"})
	TronReceiptTargetWorkers        = promauto.NewGauge(prometheus.GaugeOpts{Name: "tron_receipt_target_workers", Help: "Adaptive Ankr receipt concurrency target"})
	TronReceiptQueueDepth           = promauto.NewGauge(prometheus.GaugeOpts{Name: "tron_receipt_queue_depth", Help: "Receipt calls waiting for the adaptive gate"})
	TronReceiptQueueCapacity        = promauto.NewGauge(prometheus.GaugeOpts{Name: "tron_receipt_queue_capacity", Help: "Hard maximum receipt concurrency"})
	TronTRC20UniqueTX               = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_unique_tx_total", Help: "Unique transaction hashes observed during TRC20 enrichment"})
	TronTRC20ReceiptAmplification   = promauto.NewHistogram(prometheus.HistogramOpts{Name: "tron_trc20_receipt_amplification_ratio", Help: "Unique receipt requirements divided by TRC20 rows", Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 0.75, 1}})
	TronTRC20ReceiptEnrichmentPages = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_receipt_enrichment_pages_total", Help: "TRC20 pages enriched with transaction receipts"})
	TronTRC20LogEnrichmentPages     = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_log_enrichment_pages_total", Help: "TRC20 pages enriched with bounded eth_getLogs windows"})
	TronTRC20AdaptiveFallbacks      = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_adaptive_enrichment_fallback_total", Help: "Adaptive log decisions or matches that safely fell back to receipts"})
	TronTRC20SavedRequests          = promauto.NewCounter(prometheus.CounterOpts{Name: "tron_trc20_enrichment_saved_requests_total", Help: "Estimated receipt requests avoided by log enrichment"})

	// 下载调度
	DownloadActiveJobs = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "download_active_jobs", Help: "活动下载任务数",
	})
	DownloadActiveShards = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "download_active_shards", Help: "执行中 Shard 数",
	})
	DownloadRowsPerSec = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "download_rows_per_sec", Help: "下载速率 rows/s（EWMA）",
	})
	DownloadBytesPerSec = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "download_bytes_per_sec", Help: "下载速率 bytes/s（EWMA）",
	})
	DownloadReqPerSec = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "download_req_per_sec", Help: "API 请求速率 req/s（EWMA）",
	})

	// ClickHouse / Raw
	ClickhouseInsertRows = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clickhouse_insert_rows_total", Help: "ClickHouse 写入行数",
	}, []string{"table"})
	ClickhouseInsertLatencyMs = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "clickhouse_insert_latency_ms", Help: "ClickHouse 批次写入延迟(ms)",
		Buckets: []float64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
	}, []string{"table"})
	RawWriteLatencyMs = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "raw_write_latency_ms", Help: "Raw 对象写 MinIO 延迟(ms)",
		Buckets: []float64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
	}, []string{"bucket"})

	// V1.1 精细指标（文档 §35）
	AnkrInflight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_inflight_requests", Help: "Ankr 在途请求数",
	})
	AnkrRequestDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "ankr_request_duration_seconds", Help: "Ankr 请求耗时(秒)",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 180},
	})
	// V1.2（§56/§57）：TTFB 与 Body Download 分离 —— 24.5s 必须拆成 TTFB + Body。
	AnkrTTFBSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "ankr_ttfb_seconds", Help: "Ankr 首字节时间(秒)（headers 到达前，Provider Query Delay，§57）",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 40, 60, 120},
	}, []string{"provider", "dataset"})
	AnkrBodyDownloadSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "ankr_body_download_seconds", Help: "Ankr 响应体下载耗时(秒)（Body 主导 → PageSize 控制器参考，§58）",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 40, 60, 120},
	}, []string{"provider", "dataset"})
	AnkrResponseBytes = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "ankr_response_bytes", Help: "Ankr 响应体大小(字节)",
		Buckets: []float64{1 << 10, 1 << 15, 1 << 20, 1 << 22, 1 << 24, 1 << 25, 1 << 26},
	})
	AnkrResponseRows = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "ankr_response_rows", Help: "Ankr 响应行数",
		Buckets: []float64{10, 100, 500, 1000, 2000, 5000, 10000},
	})
	AnkrPageSizeGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ankr_page_size_current", Help: "当前自适应 pageSize（按策略 key）",
	}, []string{"key"})
	AnkrPageSizeDowngrade = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_page_size_downgrade_total", Help: "pageSize 降档次数",
	})
	AnkrPageSizeUpgrade = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_page_size_upgrade_total", Help: "pageSize 升档次数",
	})
	AnkrResponseTooLarge = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_response_too_large_total", Help: "响应体超硬限制次数",
	})
	AnkrRequestTimeout = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_request_timeout_total", Help: "请求超时次数",
	})
	AnkrInvalidJSON = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_invalid_json_total", Help: "真正 InvalidJSON 次数",
	})
	AnkrRateLimit = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_rate_limit_total", Help: "限流错误次数（-32090/429 等）",
	})
	AnkrConnectionLimit = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_connection_limit_total", Help: "连接数限制错误次数（-32067 等）",
	})
	AnkrRangeViolation = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_range_violation_total", Help: "Range invariant violations",
	}, []string{"chain", "dataset"})
	AnkrTokenCrossShard = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_token_cross_shard_total", Help: "Page token observed across shards",
	}, []string{"chain", "dataset"})
	AnkrCheckpointRangeMismatch = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_checkpoint_range_mismatch_total", Help: "Checkpoint range binding mismatches",
	}, []string{"chain", "dataset"})
	AnkrResponseAttributionMismatch = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_response_attribution_mismatch_total", Help: "Response attempt attribution mismatches",
	}, []string{"chain", "dataset"})
	AnkrOutOfRangeRows = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_out_of_range_rows_total", Help: "Rows rejected by immutable range envelope",
	}, []string{"chain", "dataset"})

	// ---- Ankr 引擎（文档 §85）----
	AnkrPageGapSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "ankr_page_gap_seconds", Help: "上一页 BODY_COMPLETE → 下一页请求发出的间隔(秒)（§35）",
		Buckets: []float64{0.01, 0.05, 0.1, 0.2, 0.5, 1, 2.5, 5, 10, 30},
	})
	AnkrFetchWireBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_fetch_wire_bytes_total", Help: "Fetch 传输层字节（压缩后，§28）",
	}, []string{"provider", "dataset"})
	AnkrFetchDecodedBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_fetch_decoded_bytes_total", Help: "Fetch 解码字节（JSON，§28）",
	}, []string{"provider", "dataset"})
	AnkrFetchGoodput = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_fetch_goodput_rows_per_second", Help: "Fetch Goodput（rows/s，§3）",
	})
	AnkrSoftLimitExceeded = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_soft_limit_exceeded_total", Help: "超过 Soft Limit（仅信号，不降 pageSize，§27）",
	})
	AnkrBodyTruncated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_body_truncated_total", Help: "传输层截断次数（§66）",
	})
	AnkrBodyIdleTimeout = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ankr_body_idle_timeout_total", Help: "Body Idle 超时次数（§22）",
	})
	AnkrFetchAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ankr_fetch_attempts_total", Help: "Fetch 尝试次数（含重试，用于 Retry Amplification）",
	}, []string{"provider", "dataset"})
	AnkrRetryAmplification = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_retry_amplification", Help: "attempts/requests（§3 Retry Amplification）",
	})

	IngestRowsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ingest_rows_total", Help: "异步 Ingest 解析并写入行数（§85）",
	})
	IngestLatencySeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "ingest_latency_seconds", Help: "页面级 Ingest 延迟(秒)",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	})
	IngestQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ingest_queue_depth", Help: "Ingest 队列待处理页面数（§71）",
	})
	SpoolPendingBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "spool_pending_bytes", Help: "Spool 尚未清理的字节数（§71）",
	})
	SpoolUsedRatio = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "spool_used_ratio", Help: "Spool 使用比例（§72）",
	})
	CHBatchRows = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "ch_batch_rows", Help: "ClickHouse 批次行数（§56/§57）",
		Buckets: []float64{100, 500, 1000, 5000, 10000, 25000, 50000, 100000},
	})
	CHPendingRows = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ch_pending_rows", Help: "聚合器中等待写入 ClickHouse 的行数（§71）",
	})
	DownloadTailEtaSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "download_tail_eta_seconds", Help: "尾数据集完成 ETA(秒)（§15）",
	})
	AnkrShardsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_shards_active", Help: "活动 Shard 数（FETCHING/INGESTING，§85）",
	})
	AnkrControlState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ankr_control_state", Help: "控制状态（§88：0=NORMAL 1=DEGRADED 2=RECOVERING 3=PROTECTED）",
	}, []string{"reason"})

	// V1.2 全局并发（§33/§80）：active / target / ceiling / goodput / ramp。
	AnkrGlobalActiveSlots = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_global_active_slots", Help: "全局 Ankr 活跃 Fetch 槽位（≤600，§36）",
	})
	AnkrGlobalTargetSlots = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_global_concurrency_target", Help: "当前安全并发目标（Ramp 自动学习，§39/§102）",
	})
	AnkrGlobalCeiling = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_global_concurrency_ceiling", Help: "全局硬 Ceiling（ANKR_GLOBAL_MAX_INFLIGHT，§33）",
	})
	AnkrGlobalGoodput = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_global_goodput_rows_per_second", Help: "全局 Fetch Goodput（rows/s，Ramp Gate 输入，§41）",
	})
	AnkrActualInflight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_actual_inflight", Help: "真实 HTTP Fetch 在途请求数（含 TTFB 与 Body）",
	})
	AnkrActiveFetch = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_active_fetch", Help: "当前活跃 Fetch Shard 数",
	})
	AnkrActiveNonzeroFetch = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_active_nonzero_fetch", Help: "首个非空页面已确认且仍在 Fetch 的 Shard 数",
	})
	AnkrEffectiveConcurrencyRatio = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_effective_concurrency_ratio", Help: "active_nonzero_fetch / target_concurrency",
	})
	AnkrBodyInflightActual = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_body_inflight_actual", Help: "正在读取响应 Body 的真实 HTTP 请求数",
	})
	AnkrConnectionReuseRatio = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_connection_reuse_ratio", Help: "HTTP GotConn reused / total",
	})
	WorkerRSSBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "worker_rss_bytes", Help: "Worker /proc/self/status VmRSS",
	})
	WorkerSwapBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "worker_vmswap_bytes", Help: "Worker /proc/self/status VmSwap",
	})
	VMMemAvailableBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vm_mem_available_bytes", Help: "VM /proc/meminfo MemAvailable",
	})
	VMSwapUsedBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vm_swap_used_bytes", Help: "VM SwapTotal - SwapFree",
	})
	VMPSWAPInTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vm_pswpin_total", Help: "VM /proc/vmstat pswpin",
	})
	VMPSWAPOutTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vm_pswpout_total", Help: "VM /proc/vmstat pswpout",
	})
	CgroupOOMTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "worker_cgroup_oom_total", Help: "Worker cgroup memory.events oom + oom_kill",
	})
	AnkrTimeToFullSafeConcurrencyMs = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ankr_time_to_full_safe_concurrency_ms", Help: "首次 Fetch → 全安全并发耗时（ms，§75）",
	})

	// Coverage / Export
	CoverageHitRatio = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "coverage_hit_ratio", Help: "本地数据命中率（最近任务）",
	})
	CoverageSavedRequests = promauto.NewCounter(prometheus.CounterOpts{
		Name: "coverage_saved_requests_total", Help: "因本地复用而省下的 Provider 请求数",
	})
	ExportJobs = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "export_jobs_total", Help: "导出任务数",
	}, []string{"format", "status"})
)

func init() {
	// Register label-vector descriptors at zero so Prometheus discovery sees
	// them before the first production failure or circuit rejection.
	AnkrProviderRootFailure.WithLabelValues("", "", "").Add(0)
	AnkrCircuitRejection.WithLabelValues("", "").Add(0)
}

// ---- V1.4（§52）：Global Ready Queue / 阶段 inflight / Sweet Spot ----
var (
	AnkrFetchSafeTarget                = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_fetch_safe_target", Help: "Global Fetch Safe Target（当前 ramp 目标）"})
	AnkrFetchReadyQueueDepth           = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_fetch_ready_queue_depth", Help: "READY 可调度 Range 数"})
	AnkrFetchRetryQueueDepth           = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_fetch_retry_queue_depth", Help: "FAILED_RETRYABLE 且已到重试窗口的 Range 数"})
	AnkrFetchWaitingTTFB               = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_fetch_waiting_ttfb", Help: "估算的 TTFB 等待中请求数（phase-aware，§23）"})
	AnkrFetchBodyInflight              = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_fetch_body_inflight", Help: "估算的 Body 下载中请求数（phase-aware，§23）"})
	AnkrFetchTailBoostTotal            = promauto.NewCounter(prometheus.CounterOpts{Name: "ankr_fetch_tail_boost_total", Help: "Tail Boost 触发次数（§9）"})
	AnkrFetchQueueShadowMismatchTotal  = promauto.NewCounter(prometheus.CounterOpts{Name: "ankr_fetch_queue_shadow_mismatch_total", Help: "Global Queue Shadow 顺序不一致采样次数（§56）"})
	AnkrFetchLeaseEpochMismatchTotal   = promauto.NewCounter(prometheus.CounterOpts{Name: "ankr_fetch_lease_epoch_mismatch_total", Help: "Lease Epoch CAS 拒绝次数（§33 split-brain 防护）"})
	AnkrFetchRetryBudgetExhaustedTotal = promauto.NewCounter(prometheus.CounterOpts{Name: "ankr_fetch_retry_budget_exhausted_total", Help: "Page Retry 时间/次数预算耗尽次数（§27）"})
	// Memory breakdown for V1.4.1 acceptance profiles (heap and reserved
	// decoded/body ownership are sampled independently from RSS/pprof).
	AnkrHeapAllocBytes          = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_heap_alloc_bytes", Help: "当前 Go heap alloc bytes"})
	AnkrHeapInuseBytes          = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_heap_inuse_bytes", Help: "当前 Go heap in-use bytes"})
	AnkrHeapSysBytes            = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_heap_sys_bytes", Help: "Go heap sys bytes"})
	AnkrFetchDecodedActiveBytes = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_fetch_decoded_active_bytes", Help: "Fetch decoded body bytes reserved by byte budget"})
	AnkrParserQueueBytes        = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_parser_queue_bytes", Help: "Parser queue estimated bytes"})
	AnkrSpoolPendingBytes       = promauto.NewGauge(prometheus.GaugeOpts{Name: "ankr_spool_pending_bytes", Help: "Spool pending bytes"})
)
