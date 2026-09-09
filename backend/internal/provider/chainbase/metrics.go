// V1.2 Chainbase Count Oracle：Prometheus 指标（§82：Count Oracle 独立可观测）。
package chainbase

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ChainbaseCountAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chainbase_count_attempts_total", Help: "Chainbase Count 请求尝试数（含探测/重试）",
	}, []string{"dataset", "status"})
	ChainbaseCountSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chainbase_count_success_total", Help: "Chainbase Count 成功请求数",
	}, []string{"dataset"})
	ChainbaseCountLatencyMs = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "chainbase_count_latency_ms", Help: "Chainbase Count 请求延迟(ms)",
		Buckets: []float64{25, 50, 100, 200, 400, 800, 1500, 3000, 5000, 10000},
	}, []string{"dataset"})
	ChainbaseCountCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "chainbase_count_cache_hits_total", Help: "Count Cache 命中次数",
	})
	ChainbaseCountErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chainbase_count_errors_total", Help: "Chainbase Count 错误次数（按 error class）",
	}, []string{"class"})
	ChainbaseCountZero = promauto.NewCounter(prometheus.CounterOpts{
		Name: "chainbase_count_zero_total", Help: "Chainbase Count=0 结果次数（触发 Zero Verify 判定）",
	})
	ChainbaseCircuitState = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "chainbase_circuit_state", Help: "Chainbase 断路器状态（0=CLOSED 1=DEGRADED 2=OPEN 3=HALF_OPEN）",
	})
)
