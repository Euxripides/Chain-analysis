package observability

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Sprint 6 / S6-OBS-01：§25.2 中仍然缺失的运行时指标。
//
// 覆盖范围：
//   - API：请求速率、错误率、延迟直方图（可算 P50/P95/P99）、inflight、429、401/403。
//   - IAM：登录成功/失败、会话撤销次数与撤销耗时。
//   - Audit：审计写入结果（cs_* 已有）之外的导出耗时。
//
// 说明：进程内计数器在重启后归零，因此 SLO 接口必须同时返回窗口起点，
// 不能把「进程内统计」包装成「30 天可用性」。

var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cs_http_requests_total",
		Help: "API 请求总数（按路由模板、方法、状态类别与受众）",
	}, []string{"route_template", "method", "status_class", "audience"})

	HTTPRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cs_http_request_duration_seconds",
		Help:    "API 请求耗时（秒），用于计算 P50/P95/P99",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.35, 0.5, 0.8, 1.2, 1.5, 2, 3, 5, 8, 13, 21, 34},
	}, []string{"route_template", "method"})

	HTTPInflight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "cs_http_inflight_requests",
		Help: "当前处理中的 API 请求数",
	})

	HTTPRateLimited = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cs_http_rate_limited_total",
		Help: "被限流的请求数（HTTP 429）",
	}, []string{"route_template"})

	HTTPAuthFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cs_http_auth_failures_total",
		Help: "认证与授权失败（401/403）",
	}, []string{"route_template", "status"})

	LoginTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cs_login_total",
		Help: "登录尝试结果",
	}, []string{"result", "audience"})

	SessionRevocations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cs_session_revocations_total",
		Help: "会话撤销次数",
	}, []string{"scope"})

	SessionRevokeLatencySeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "cs_session_revoke_latency_seconds",
		Help:    "会话撤销生效耗时（秒）",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
	})

	AuditExportDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cs_audit_export_duration_seconds",
		Help:    "审计导出耗时（秒）",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"format", "result"})

	AlertEvaluationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cs_alert_evaluations_total",
		Help: "SLO 告警评估结果",
	}, []string{"result", "priority"})

	ProcessStartTime = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "cs_process_start_time_seconds",
		Help: "进程启动时间（Unix 秒），用于 SLO 窗口起点",
	})
)

func init() {
	ProcessStartTime.Set(float64(time.Now().Unix()))
	// 预注册常见标签组合，保证 /metrics 在无流量时也暴露零值。
	for _, class := range []string{"2xx", "3xx", "4xx", "5xx"} {
		HTTPRequestsTotal.WithLabelValues("other", "GET", class, "unknown")
	}
}

// RecordHTTP 记录一次 API 请求。
func RecordHTTP(routeTemplate, method string, status int, audience string, duration time.Duration) {
	if routeTemplate == "" {
		routeTemplate = "unmatched"
	}
	class := StatusClass(status)
	HTTPRequestsTotal.WithLabelValues(routeTemplate, method, class, audience).Inc()
	HTTPRequestDurationSeconds.WithLabelValues(routeTemplate, method).Observe(duration.Seconds())
	if status == 429 {
		HTTPRateLimited.WithLabelValues(routeTemplate).Inc()
	}
	if status == 401 || status == 403 {
		HTTPAuthFailures.WithLabelValues(routeTemplate, strconv.Itoa(status)).Inc()
	}
}

// StatusClass 返回 2xx/3xx/4xx/5xx 类别。
func StatusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	}
	return "other"
}

// RecordLogin 记录登录结果。
func RecordLogin(result, audience string) {
	if result == "" {
		result = "unknown"
	}
	if audience == "" {
		audience = "unknown"
	}
	LoginTotal.WithLabelValues(result, audience).Inc()
}

// RecordSessionRevocation 记录会话撤销与生效耗时。
func RecordSessionRevocation(scope string, latency time.Duration) {
	if scope == "" {
		scope = "single"
	}
	SessionRevocations.WithLabelValues(scope).Inc()
	if latency > 0 {
		SessionRevokeLatencySeconds.Observe(latency.Seconds())
	}
}

// RecordAuditExport 记录审计导出结果与耗时。
func RecordAuditExport(format, result string, duration time.Duration) {
	if format == "" {
		format = "jsonl"
	}
	if result == "" {
		result = "unknown"
	}
	AuditExportDurationSeconds.WithLabelValues(format, result).Observe(duration.Seconds())
}

// RecordAlertEvaluation 记录 SLO 告警评估。
func RecordAlertEvaluation(result, priority string) {
	if result == "" {
		result = "unknown"
	}
	if priority == "" {
		priority = "none"
	}
	AlertEvaluationsTotal.WithLabelValues(result, priority).Inc()
}

// ReadGaugeFamily 从默认注册表读取指定 Gauge 指标族，返回 name -> value。
func ReadGaugeFamily(name string) map[string]float64 {
	out := map[string]float64{}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return out
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			label := ""
			for _, pair := range metric.GetLabel() {
				if pair.GetName() == "result" || pair.GetName() == "state" || pair.GetName() == "chain" {
					label = pair.GetValue()
				}
			}
			out[label] = metric.GetGauge().GetValue()
		}
	}
	return out
}

// CounterTotal 返回某计数器的全标签合计。
func CounterTotal(name string) float64 {
	total := 0.0
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return total
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			total += metric.GetCounter().GetValue()
		}
	}
	return total
}

// ProcessStartTimeValue 返回进程启动时间（SLO 窗口起点）。
func ProcessStartTimeValue() time.Time {
	families, err := prometheus.DefaultGatherer.Gather()
	if err == nil {
		for _, family := range families {
			if family.GetName() != "cs_process_start_time_seconds" {
				continue
			}
			for _, metric := range family.GetMetric() {
				return time.Unix(int64(metric.GetGauge().GetValue()), 0).UTC()
			}
		}
	}
	return time.Now().UTC()
}

// InflightValue 返回当前处理中的请求数。
func InflightValue() float64 {
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return 0
	}
	for _, family := range families {
		if family.GetName() != "cs_http_inflight_requests" {
			continue
		}
		for _, metric := range family.GetMetric() {
			return metric.GetGauge().GetValue()
		}
	}
	return 0
}
