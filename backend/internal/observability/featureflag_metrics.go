package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// §7.3 / §16.7：功能开关在真实请求路径上的求值必须可观测。
//
// 为什么单独一个指标：开关被关掉时接口返回的是 503 FEATURE_DISABLED，
// 在通用 HTTP 指标里与"服务不可用"完全同形；按 flag 维度计数才能回答
// "到底是哪个开关关掉了多少真实流量"。
var (
	FeatureFlagEvaluated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "feature_flag_evaluated_total",
		Help: "Feature flag evaluations performed on real request paths (by flag and result)",
	}, []string{"flag", "result", "reason"})

	FeatureFlagLookupFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "feature_flag_lookup_failed_total",
		Help: "Feature flag lookups that failed and therefore failed closed",
	}, []string{"flag"})
)
