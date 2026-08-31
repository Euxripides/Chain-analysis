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
