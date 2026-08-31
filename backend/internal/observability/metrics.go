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
