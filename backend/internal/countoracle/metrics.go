// V1.3 Count Provider Port：Prometheus 指标（§71：禁止高基数 label：address/job_id/request_id/api_key）。
package countoracle

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	countProviderRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "count_provider_requests_total", Help: "Count Provider 请求尝试数（含重试）",
	}, []string{"provider", "dataset", "status"})
	countProviderValidResults = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "count_provider_valid_results_total", Help: "有效 Count 结果数（有 count 语义的结果）",
	}, []string{"provider", "dataset"})
	countProviderLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "count_provider_latency_seconds", Help: "Count Provider 调用延迟",
		Buckets: []float64{0.025, 0.05, 0.1, 0.2, 0.4, 0.8, 1.5, 3, 5, 10},
	}, []string{"provider", "dataset"})
	countProviderInflight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "count_provider_inflight", Help: "Count Provider 活跃调用数",
	}, []string{"provider"})
	countProviderQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "count_provider_queue_depth", Help: "Count Provider 队列深度",
	}, []string{"provider"})
	countProviderSafeConcurrency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "count_provider_safe_concurrency", Help: "Count Provider 安全并发",
	}, []string{"provider"})
	countProviderRateLimit = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "count_provider_rate_limit_total", Help: "Count Provider RATE_LIMIT 次数",
	}, []string{"provider", "dataset"})
	countProviderTimeout = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "count_provider_timeout_total", Help: "Count Provider TIMEOUT 次数",
	}, []string{"provider", "dataset"})
	countProviderCostCredits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "count_provider_cost_credits_total", Help: "Count Provider 累计 credits 成本",
	}, []string{"provider", "dataset"})
	countProviderZero = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "count_provider_zero_total", Help: "Count Provider count=0 结果（触发 Zero Verify）",
	}, []string{"provider"})
	countProviderResolution = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "count_provider_resolution_total", Help: "Count Resolution 结果（按 source/status）",
	}, []string{"source", "status"})
	countCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "count_provider_cache_hits_total", Help: "Canonical Count Cache 命中",
	})
)
