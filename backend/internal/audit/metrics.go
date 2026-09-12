package audit

import "github.com/prometheus/client_golang/prometheus"

// auditWriteCounter 由 internal/api 在启动时通过 RegisterAuditMetrics 注入，
// 使 audit 包不反向依赖 api 包（保持单向依赖）。
var auditWriteCounter *prometheus.CounterVec

// RegisterAuditMetrics 由进程入口调用一次。
func RegisterAuditMetrics(c *prometheus.CounterVec) { auditWriteCounter = c }

func recordWrite(chain string, err error) {
	if auditWriteCounter == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "failed"
	}
	auditWriteCounter.WithLabelValues(chain, result).Inc()
}
