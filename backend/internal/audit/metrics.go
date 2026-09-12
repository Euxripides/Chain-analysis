package audit

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// auditWriteCounter 由 internal/api 在启动时通过 RegisterAuditMetrics 注入，
// 使 audit 包不反向依赖 api 包（保持单向依赖）。
var auditWriteCounter *prometheus.CounterVec

// auditWriteLatency 记录审计落库耗时，直接支撑 §25.3 的「审计落库 99.99% <2s」SLO。
// 用 promauto 自注册，避免再增加一条注入链路。
var auditWriteLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "cs_audit_write_duration_seconds",
	Help:    "审计事件写入 audit_events_v2 的耗时（秒）",
	Buckets: []float64{0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5},
}, []string{"chain", "result"})

// RegisterAuditMetrics 由进程入口调用一次。
func RegisterAuditMetrics(c *prometheus.CounterVec) { auditWriteCounter = c }

// recordWrite 记录一次审计写入的结果与耗时（latency <= 0 表示未测量）。
func recordWrite(chain string, err error, latency time.Duration) {
	result := "ok"
	if err != nil {
		result = "failed"
	}
	if auditWriteCounter != nil {
		auditWriteCounter.WithLabelValues(chain, result).Inc()
	}
	if latency > 0 {
		auditWriteLatency.WithLabelValues(chain, result).Observe(latency.Seconds())
	}
}
