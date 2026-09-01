package rpc

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus 指标（§140）。
//
// Label 约束（§141）：允许 provider/chain/method_class/method/status/error_class；
// 严禁 address / tx_hash / contract 作为 Label。
var (
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_requests_total", Help: "RPC HTTP 请求数（一次 Batch 记为一次）",
	}, []string{"provider", "chain", "method_class", "status"})
	MethodCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_method_calls_total", Help: "RPC 方法调用数（Batch 内每个 call 单独计数）",
	}, []string{"provider", "chain", "method", "status"})
	ErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_errors_total", Help: "RPC 错误数（按统一错误分类）",
	}, []string{"provider", "chain", "method_class", "error_class"})
	RetriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_retries_total", Help: "RPC 重试次数",
	}, []string{"provider", "chain", "method_class"})

	RateLimitTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_429_total", Help: "RPC 限流次数",
	}, []string{"provider", "chain"})
	TimeoutTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_timeout_total", Help: "RPC 超时次数",
	}, []string{"provider", "chain", "method_class"})
	ConnectionLimitTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_connection_limit_total", Help: "RPC 连接数限制次数",
	}, []string{"provider", "chain"})

	InFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_inflight", Help: "RPC 在途请求数",
	}, []string{"provider", "chain"})
	Duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "rpc_duration_seconds", Help: "RPC 请求耗时(秒)",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"provider", "chain", "method_class"})
	ResponseBytes = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "rpc_response_bytes", Help: "RPC 响应体大小(字节)",
		Buckets: []float64{1 << 8, 1 << 10, 1 << 12, 1 << 14, 1 << 16, 1 << 18, 1 << 20, 1 << 22, 1 << 24},
	}, []string{"provider", "chain", "method_class"})

	BatchRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_batch_requests_total", Help: "JSON-RPC Batch HTTP 请求数",
	}, []string{"provider", "chain"})
	BatchCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_batch_calls_total", Help: "JSON-RPC Batch 内的方法调用数",
	}, []string{"provider", "chain"})

	CacheHitTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_cache_hit_total", Help: "RPC 缓存命中数",
	}, []string{"chain", "method"})
	CacheMissTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_cache_miss_total", Help: "RPC 缓存未命中数",
	}, []string{"chain", "method"})
	SingleflightHitTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_singleflight_hit_total", Help: "RPC Singleflight 合并命中数",
	}, []string{"chain", "method"})

	BudgetRejectedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_budget_rejected_total", Help: "因预算被拒绝的 RPC 请求数",
	}, []string{"purpose"})
	MethodDeniedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_method_denied_total", Help: "被 Allowlist / 只读守卫拒绝的 RPC 方法调用数",
	}, []string{"method"})

	CircuitStateGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_circuit_state", Help: "熔断状态 0=CLOSED 1=DEGRADED 2=HALF_OPEN 3=OPEN",
	}, []string{"provider", "chain", "method_class"})
	ProviderHeadLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_provider_head_lag", Help: "Provider 链头落后区块数",
	}, []string{"provider", "chain"})

	TraceRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_trace_requests_total", Help: "Trace 请求数",
	}, []string{"chain", "status"})
	TraceCacheHitTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_trace_cache_hit_total", Help: "Trace 本地缓存命中数",
	}, []string{"chain"})
)

// circuitStateValue 熔断状态数值化，便于告警阈值配置。
func circuitStateValue(state CircuitState) float64 {
	switch state {
	case CircuitDegraded:
		return 1
	case CircuitHalfOpen:
		return 2
	case CircuitOpen:
		return 3
	default:
		return 0
	}
}
