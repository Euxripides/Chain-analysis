package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/blockchain-platform/blockchain-platform/internal/observability"
)

// Sprint 6 / S6-OBS-01：§25 可观测性 API（dashboard、SLO、alerts）。
//
// 设计原则（§19.2 / §25）：
//   - 每个指标都返回 as_of、窗口、来源与查询状态，不同窗口的数字不混用；
//   - 吞吐拆分 Provider 拉取 / Pipeline / 端到端 / 导出 / 下载，不用单值代替；
//   - 系统健康只展示真实探针结果；
//   - 进程内计数器重启归零，因此窗口起点必须显式返回（window_start / resets_on_restart）。

func (s *Server) registerAdminObservabilityRoutes(r chi.Router) {
	r.Get("/observability/overview", s.require("admin:dashboard:read", s.handleAdminObservabilityOverview))
	r.Get("/observability/slo", s.require("admin:metrics:read", s.handleAdminSLO))
	r.Get("/observability/metrics", s.require("admin:metrics:read", s.handleAdminMetrics))
	r.Get("/observability/alerts", s.require("admin:metrics:read", s.handleAdminObservabilityAlerts))
	r.Post("/observability/alerts/evaluate", s.require("admin:metrics:read", s.handleAdminEvaluateAlerts))
}

// ---- 观测总览（A-HOME-01 的数据源） ----

func (s *Server) handleAdminObservabilityOverview(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	now := s6Now()
	windowMinutes := s6Limit(r.URL.Query().Get("window_minutes"), 60, 10080)
	since := now.Add(-time.Duration(windowMinutes) * time.Minute)

	overview := map[string]any{
		"as_of": now.Format(time.RFC3339), "window_minutes": windowMinutes,
		"window_start": since.Format(time.RFC3339),
	}
	health := s.s6HealthCheck(ctx)
	overview["health"] = health

	// 请求速率 / 错误率 / 延迟：直接读运行时指标。
	overview["api"] = s.s6APISummary()

	// 吞吐拆分：每类都有独立来源，禁止合并成一个数字（§25.2）。
	var providerCalls, providerErrors, providerRows int64
	var providerBytes int64
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status <> 'success'), coalesce(sum(coalesce(rows,0)),0), coalesce(sum(coalesce(bytes,0)),0)
		FROM provider_usage WHERE request_time >= $1`, since).Scan(&providerCalls, &providerErrors, &providerRows, &providerBytes)
	var pipelineRows int64
	// download_jobs 没有 downloaded_rows 列（分页行数记在 download_shards 上）。
	// 写错列名会让语句失败并把事务打成 aborted，进而把整个请求变成 409（见 sprint6_control.go 注释）。
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT coalesce(sum(downloaded_rows),0) FROM download_shards WHERE updated_at >= $1`, since).Scan(&pipelineRows)
	var jobWallSeconds float64
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT coalesce(avg(extract(epoch from (updated_at - created_at))),0)::float8 FROM download_jobs
		WHERE status = 'COMPLETED' AND updated_at >= $1`, since).Scan(&jobWallSeconds)
	var exportCount int64
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT count(*) FROM exports WHERE created_at >= $1`, since).Scan(&exportCount)
	var downloadsServed int64
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events_v2 WHERE action LIKE '%download%' AND occurred_at >= $1`, since).Scan(&downloadsServed)
	overview["throughput"] = map[string]any{
		"provider_fetch":  map[string]any{"calls": providerCalls, "errors": providerErrors, "rows": providerRows, "bytes": providerBytes, "source": "postgres:provider_usage"},
		"pipeline":        map[string]any{"rows": pipelineRows, "source": "postgres:download_jobs.downloaded_rows"},
		"job_wall_clock":  map[string]any{"avg_seconds": jobWallSeconds, "completed_jobs_source": "postgres:download_jobs"},
		"export":          map[string]any{"jobs": exportCount, "source": "postgres:exports"},
		"download_events": map[string]any{"audit_events": downloadsServed, "source": "postgres:audit_events_v2"},
	}

	// 队列与失败任务。
	var running, waiting, failed, paused int
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status IN ('RUNNING','RESUMING')),
		count(*) FILTER (WHERE status IN ('CREATED','PLANNING','QUEUED','PAUSE_REQUESTED')),
		count(*) FILTER (WHERE status = 'FAILED'),
		count(*) FILTER (WHERE status = 'PAUSED') FROM download_jobs`).Scan(&running, &waiting, &failed, &paused)
	overview["queue"] = map[string]any{"running": running, "waiting": waiting, "failed": failed, "paused": paused}

	// 告警（P0 必须位于首屏数据中）。
	alertRows := s.s6AlertPrioritySummary(ctx)
	overview["alerts"] = alertRows

	// 成本（字符串金额）。
	var cost24 float64
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT coalesce(sum(estimated_cost_usd),0)::float8 FROM provider_usage WHERE request_time > now() - interval '24 hours'`).Scan(&cost24)
	overview["cost"] = map[string]any{"cost_24h": formatUSD(cost24), "amount_type": "decimal-string", "source": "postgres:provider_usage"}

	// 最近高风险动作与最近登录异常。
	highRisk := make([]map[string]any, 0, 5)
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT action, coalesce(actor_email,''), coalesce(resource_type,''), coalesce(resource_id,''), risk_level, occurred_at, coalesce(trace_id,'')
		FROM audit_events_v2 WHERE risk_level IN ('high','critical') ORDER BY occurred_at DESC LIMIT 5`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var action, actor, resourceType, resourceID, risk, traceID string
			var occurredAt time.Time
			if scanErr := rows.Scan(&action, &actor, &resourceType, &resourceID, &risk, &occurredAt, &traceID); scanErr == nil {
				highRisk = append(highRisk, map[string]any{
					"action": action, "actor_email": actor, "resource_type": resourceType,
					"resource_id": resourceID, "risk_level": risk, "occurred_at": occurredAt, "trace_id": traceID,
				})
			}
		}
	}
	overview["recent_high_risk"] = highRisk
	overview["data_source"] = map[string]string{
		"health": "runtime probes", "api": "prometheus:cs_http_*", "alerts": "postgres:api_control_alerts",
		"audit": "postgres:audit_events_v2",
	}
	respondOK(w, r, overview)
}

// s6APISummary 从进程内 Prometheus 指标汇总 API 层请求/错误/延迟。
func (s *Server) s6APISummary() map[string]any {
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return map[string]any{"status": "unavailable", "reason": "指标注册表不可读"}
	}
	var total, errors5xx, errors4xx, rateLimited, authFailures float64
	latencyByRoute := map[string]float64{}
	statuses := map[string]float64{}
	for _, family := range families {
		switch family.GetName() {
		case "cs_http_requests_total":
			for _, metric := range family.GetMetric() {
				value := metric.GetCounter().GetValue()
				total += value
				for _, pair := range metric.GetLabel() {
					switch pair.GetName() {
					case "status_class":
						statuses[pair.GetValue()] += value
						if pair.GetValue() == "5xx" {
							errors5xx += value
						}
						if pair.GetValue() == "4xx" {
							errors4xx += value
						}
					}
				}
			}
		case "cs_http_rate_limited_total":
			for _, metric := range family.GetMetric() {
				rateLimited += metric.GetCounter().GetValue()
			}
		case "cs_http_auth_failures_total":
			for _, metric := range family.GetMetric() {
				authFailures += metric.GetCounter().GetValue()
			}
		case "cs_http_request_duration_seconds":
			for _, metric := range family.GetMetric() {
				route := ""
				for _, pair := range metric.GetLabel() {
					if pair.GetName() == "route_template" {
						route = pair.GetValue()
					}
				}
				if route == "" {
					continue
				}
				if p95, ok := histogramQuantile(metric.GetHistogram(), 0.95); ok {
					if existing, exists := latencyByRoute[route]; !exists || p95 > existing {
						latencyByRoute[route] = p95
					}
				}
			}
		}
	}
	errorRate := 0.0
	if total > 0 {
		errorRate = errors5xx / total
	}
	routes := make([]map[string]any, 0, len(latencyByRoute))
	for route, p95 := range latencyByRoute {
		routes = append(routes, map[string]any{"route_template": route, "p95_seconds": p95})
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i]["p95_seconds"].(float64) > routes[j]["p95_seconds"].(float64)
	})
	start := observability.ProcessStartTimeValue()
	return map[string]any{
		"requests_total": total, "error_rate": errorRate, "errors_5xx": errors5xx, "errors_4xx": errors4xx,
		"status_classes": statuses, "rate_limited": rateLimited, "auth_failures": authFailures,
		"p95_by_route": routes, "inflight": observability.InflightValue(),
		"window_start": start.Format(time.RFC3339), "window": "process_lifetime",
		"resets_on_restart": true, "source": "prometheus:cs_http_*",
	}
}

// histogramQuantile 从 Prometheus 直方图估算分位数（线性插值）。
func histogramQuantile(histogram *dto.Histogram, quantile float64) (float64, bool) {
	if histogram == nil || histogram.GetSampleCount() == 0 {
		return 0, false
	}
	target := quantile * float64(histogram.GetSampleCount())
	var cumulative float64
	var lower float64
	for _, bucket := range histogram.GetBucket() {
		cumulative += float64(bucket.GetCumulativeCount())
		upper := bucket.GetUpperBound()
		if cumulative >= target {
			if cumulative == 0 {
				return upper, true
			}
			span := upper - lower
			if span <= 0 {
				return upper, true
			}
			ratio := (target - (cumulative - float64(bucket.GetCumulativeCount()))) / float64(bucket.GetCumulativeCount())
			if ratio < 0 {
				ratio = 0
			}
			if ratio > 1 {
				ratio = 1
			}
			return lower + span*ratio, true
		}
		lower = upper
	}
	return lower, true
}

// ---- SLO（§25.3） ----

type s6SLO struct {
	Key          string  `json:"key"`
	Name         string  `json:"name"`
	Target       string  `json:"target"`
	TargetValue  float64 `json:"target_value"`
	Comparator   string  `json:"comparator"`
	Window       string  `json:"window"`
	Current      float64 `json:"current_value"`
	CurrentLabel string  `json:"current_label"`
	Unit         string  `json:"unit"`
	Status       string  `json:"status"`
	Priority     string  `json:"alert_priority"`
	Samples      int64   `json:"samples"`
	Source       string  `json:"source"`
	QueryStatus  string  `json:"query_status"`
}

func (s *Server) handleAdminSLO(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	slos := s.s6EvaluateSLOs(r.Context())
	breached := 0
	noData := 0
	for _, slo := range slos {
		switch slo.Status {
		case "breach":
			breached++
		case "no_data":
			noData++
		}
	}
	respondOK(w, r, map[string]any{
		"as_of": s6Now().Format(time.RFC3339), "items": slos,
		"breached": breached, "no_data": noData,
		"note": "进程内计数窗口从进程启动开始（resets_on_restart=true），数据库来源使用各自的时间窗口。",
	})
}

func (s *Server) s6EvaluateSLOs(ctx context.Context) []s6SLO {
	slos := make([]s6SLO, 0, 10)
	now := s6Now()

	// 1) 用户 API 可用性（进程窗口）。
	api := s.s6APISummary()
	apiSamples := asInt64(api["requests_total"])
	apiAvailability := 1.0
	if apiSamples > 0 {
		apiAvailability = 1 - asFloat(api["error_rate"])
	}
	status := "ok"
	queryStatus := "ok"
	if apiSamples == 0 {
		status = "no_data"
		queryStatus = "no_traffic"
	} else if apiAvailability < 0.999 {
		status = "breach"
	}
	slos = append(slos, s6SLO{
		Key: "api_availability_user", Name: "用户 API 可用性", Target: "99.9%", TargetValue: 0.999,
		Comparator: ">=", Window: "进程窗口（30 天目标）", Current: apiAvailability,
		CurrentLabel: fmt.Sprintf("%.4f%%", apiAvailability*100), Unit: "ratio", Status: status,
		Priority: "P2", Samples: apiSamples, Source: "prometheus:cs_http_requests_total", QueryStatus: queryStatus,
	})

	// 2) 登录成功请求 P95 < 800ms（1 小时）。
	loginP95, loginSamples := s.s6RouteP95(api, "/api/auth/v1/login")
	slos = append(slos, s6SLO{
		Key: "login_p95", Name: "登录成功请求 P95", Target: "<800ms", TargetValue: 0.8, Comparator: "<=",
		Window: "1 小时（使用进程窗口）", Current: loginP95, CurrentLabel: fmt.Sprintf("%.0fms", loginP95*1000),
		Unit: "seconds", Status: s6SLOStatus(loginSamples, loginP95, 0.8), Priority: "P2",
		Samples: loginSamples, Source: "prometheus:cs_http_request_duration_seconds{route=/api/auth/v1/login}",
		QueryStatus: s6QueryStatus(loginSamples),
	})

	// 3) 常规查询 P95 < 2s。
	queryP95, querySamples := s.s6PrefixP95(api, "/api/v2/")
	slos = append(slos, s6SLO{
		Key: "query_p95", Name: "常规查询 P95", Target: "<2s", TargetValue: 2, Comparator: "<=",
		Window: "1 小时（使用进程窗口）", Current: queryP95, CurrentLabel: fmt.Sprintf("%.0fms", queryP95*1000),
		Unit: "seconds", Status: s6SLOStatus(querySamples, queryP95, 2), Priority: "P2",
		Samples: querySamples, Source: "prometheus:cs_http_request_duration_seconds{route=/api/v2/*}",
		QueryStatus: s6QueryStatus(querySamples),
	})

	// 4) 后台列表 P95 < 1.5s。
	adminP95, adminSamples := s.s6PrefixP95(api, "/api/admin/v1/")
	slos = append(slos, s6SLO{
		Key: "admin_list_p95", Name: "后台列表 P95", Target: "<1.5s", TargetValue: 1.5, Comparator: "<=",
		Window: "1 小时（使用进程窗口）", Current: adminP95, CurrentLabel: fmt.Sprintf("%.0fms", adminP95*1000),
		Unit: "seconds", Status: s6SLOStatus(adminSamples, adminP95, 1.5), Priority: "P2",
		Samples: adminSamples, Source: "prometheus:cs_http_request_duration_seconds{route=/api/admin/v1/*}",
		QueryStatus: s6QueryStatus(adminSamples),
	})

	// 5) 审计落库：失败 0 且最新事件新鲜度 < 2s。
	var auditFailures float64
	var latestAudit *time.Time
	var auditCount int64
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT count(*), max(occurred_at) FROM audit_events_v2 WHERE occurred_at > now() - interval '1 hour'`).Scan(&auditCount, &latestAudit)
	families, err := prometheus.DefaultGatherer.Gather()
	if err == nil {
		for _, family := range families {
			if family.GetName() != "cs_audit_writes_total" {
				continue
			}
			for _, metric := range family.GetMetric() {
				for _, pair := range metric.GetLabel() {
					if pair.GetName() == "result" && pair.GetValue() == "failed" {
						auditFailures += metric.GetCounter().GetValue()
					}
				}
			}
		}
	}
	// 审计落库 SLO 用真实写入耗时直方图（cs_audit_write_duration_seconds）。
	// 「最新事件距今很久」在系统空闲时属正常，只作为附加信息，不作为违约判据。
	auditP99, auditLatencySamples := s6HistogramQuantile("cs_audit_write_duration_seconds", 0.99)
	freshness := 0.0
	if latestAudit != nil {
		freshness = now.Sub(*latestAudit).Seconds()
	}
	auditStatus := "no_data"
	auditQuery := "no_writes_recorded"
	if auditLatencySamples > 0 || auditCount > 0 {
		auditQuery = "ok"
		auditStatus = "ok"
		if auditFailures > 0 || (auditLatencySamples > 0 && auditP99 > 2) {
			auditStatus = "breach"
		}
	}
	auditValue := auditP99
	if auditValue == 0 {
		auditValue = 2
	}
	slos = append(slos, s6SLO{
		Key: "audit_write", Name: "审计落库 P99（失败 0 且 <2s）", Target: "99.99% <2s", TargetValue: 2, Comparator: "<=",
		Window: "1 小时（进程窗口）", Current: auditValue,
		CurrentLabel: fmt.Sprintf("P99 %.0fms / failures=%d / 最新事件 %.1fs 前", auditP99*1000, int(auditFailures), freshness),
		Unit:         "seconds", Status: auditStatus, Priority: "P0", Samples: auditLatencySamples,
		Source: "prometheus:cs_audit_write_duration_seconds + cs_audit_writes_total", QueryStatus: auditQuery,
	})

	// 6) Outbox 最老未发布 < 30s。
	var outboxLag float64
	var outboxPending int64
	var oldest *time.Time
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL`).Scan(&outboxPending, &oldest)
	outboxStatus := "ok"
	outboxQuery := "ok"
	if oldest != nil {
		outboxLag = now.Sub(*oldest).Seconds()
		if outboxLag > 30 {
			outboxStatus = "breach"
		}
	} else if outboxPending == 0 {
		outboxStatus = "ok"
		outboxQuery = "empty_queue"
	}
	slos = append(slos, s6SLO{
		Key: "outbox_oldest", Name: "Outbox 最老未发布", Target: "<30s", TargetValue: 30, Comparator: "<=",
		Window: "持续", Current: outboxLag, CurrentLabel: fmt.Sprintf("%.1fs / pending=%d", outboxLag, outboxPending),
		Unit: "seconds", Status: outboxStatus, Priority: "P1", Samples: outboxPending,
		Source: "postgres:outbox_events", QueryStatus: outboxQuery,
	})

	// 7) Session 撤销生效 < 5s（从审计事件到撤销时间）。
	var revokeLatency float64
	var revokeSamples int64
	// sessions.id 是 uuid、audit_events_v2.resource_id 是 text：不转型会直接报
	// "operator does not exist: uuid = text"，语句失败 -> 请求事务 aborted -> 整个 SLO
	// 请求被提交门改写成 409。用 ::text 比较（而不是把 resource_id 转 uuid）：
	// resource_id 并不总是合法 UUID。
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT count(*), coalesce(avg(extract(epoch from (s.revoked_at - a.occurred_at))), 0)::float8
		FROM audit_events_v2 a JOIN sessions s ON s.id::text = a.resource_id
		WHERE a.action = 'admin:session:revoke' AND a.occurred_at > now() - interval '7 days' AND s.revoked_at IS NOT NULL`).
		Scan(&revokeSamples, &revokeLatency)
	slos = append(slos, s6SLO{
		Key: "session_revoke_latency", Name: "Session/权限撤销生效", Target: "<5s", TargetValue: 5, Comparator: "<=",
		Window: "7 天", Current: revokeLatency, CurrentLabel: fmt.Sprintf("%.2fs / n=%d", revokeLatency, revokeSamples),
		Unit: "seconds", Status: s6SLOStatus(revokeSamples, revokeLatency, 5), Priority: "P1",
		Samples: revokeSamples, Source: "postgres:audit_events_v2 JOIN sessions", QueryStatus: s6QueryStatus(revokeSamples),
	})

	// 8) RPO：最近一次成功备份的时间差。
	var lastBackup *time.Time
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT max(finished_at) FROM backup_jobs WHERE kind='backup' AND status IN ('completed','verified')`).Scan(&lastBackup)
	rpoStatus := "no_data"
	rpoSeconds := 0.0
	rpoQuery := "no_successful_backup"
	if lastBackup != nil {
		rpoSeconds = now.Sub(*lastBackup).Seconds()
		rpoStatus = "ok"
		rpoQuery = "ok"
		if rpoSeconds > 900 {
			rpoStatus = "breach"
		}
	}
	slos = append(slos, s6SLO{
		Key: "rpo", Name: "RPO（最近成功备份距现在）", Target: "<=15 分钟", TargetValue: 900, Comparator: "<=",
		Window: "灾难", Current: rpoSeconds, CurrentLabel: fmt.Sprintf("%.0f 分钟", rpoSeconds/60),
		Unit: "seconds", Status: rpoStatus, Priority: "P1", Samples: 1,
		Source: "postgres:backup_jobs", QueryStatus: rpoQuery,
	})

	// 9) 跨租户越权尝试（P0 触发条件）。
	var tenantDenied float64
	for _, family := range families {
		if family.GetName() != "cs_tenant_scope_denied_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			tenantDenied += metric.GetCounter().GetValue()
		}
	}
	tenancyStatus := "ok"
	if tenantDenied > 0 {
		tenancyStatus = "breach"
	}
	slos = append(slos, s6SLO{
		Key: "tenant_isolation", Name: "跨租户越权尝试（应为 0）", Target: "=0", TargetValue: 0, Comparator: "<=",
		Window: "进程窗口", Current: tenantDenied, CurrentLabel: fmt.Sprintf("%.0f 次", tenantDenied),
		Unit: "count", Status: tenancyStatus, Priority: "P0", Samples: int64(tenantDenied),
		Source: "prometheus:cs_tenant_scope_denied_total", QueryStatus: "ok",
	})
	return slos
}

func s6SLOStatus(samples int64, value, threshold float64) string {
	if samples == 0 {
		return "no_data"
	}
	if value > threshold {
		return "breach"
	}
	return "ok"
}

func s6QueryStatus(samples int64) string {
	if samples == 0 {
		return "no_traffic"
	}
	return "ok"
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// s6HistogramQuantile 从默认注册表读取指定直方图的分位数与样本数。
func s6HistogramQuantile(name string, quantile float64) (float64, int64) {
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return 0, 0
	}
	var worst float64
	var samples int64
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			samples += int64(metric.GetHistogram().GetSampleCount())
			if value, ok := histogramQuantile(metric.GetHistogram(), quantile); ok && value > worst {
				worst = value
			}
		}
	}
	return worst, samples
}

// s6RouteP95 从 API 汇总里找精确路由的 P95。
func (s *Server) s6RouteP95(api map[string]any, route string) (float64, int64) {
	routes, _ := api["p95_by_route"].([]map[string]any)
	for _, item := range routes {
		if item["route_template"] == route {
			return asFloat(item["p95_seconds"]), 1
		}
	}
	return 0, 0
}

// s6PrefixP95 返回以某前缀开头的路由中最差的 P95。
func (s *Server) s6PrefixP95(api map[string]any, prefix string) (float64, int64) {
	routes, _ := api["p95_by_route"].([]map[string]any)
	worst := 0.0
	samples := int64(0)
	for _, item := range routes {
		route, _ := item["route_template"].(string)
		if strings.HasPrefix(route, prefix) {
			value := asFloat(item["p95_seconds"])
			if value > worst {
				worst = value
			}
			samples++
		}
	}
	return worst, samples
}

// s6AlertPrioritySummary 统计各优先级开放告警数量。
func (s *Server) s6AlertPrioritySummary(ctx context.Context) map[string]any {
	out := map[string]any{"P0": 0, "P1": 0, "P2": 0, "P3": 0, "open_total": 0}
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT CASE
		         WHEN coalesce(nullif(priority,''), '') <> '' THEN priority
		         WHEN severity IN ('CRITICAL','P0') THEN 'P0'
		         WHEN severity IN ('HIGH','P1') THEN 'P1'
		         WHEN severity IN ('WARNING','P2') THEN 'P2'
		         ELSE 'P3' END AS level,
		       count(*)
		FROM api_control_alerts WHERE status IN ('OPEN','ACK') GROUP BY 1`)
	if err != nil {
		out["query_status"] = "failed"
		return out
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var level string
		var count int
		if err := rows.Scan(&level, &count); err != nil {
			continue
		}
		out[level] = count
		total += count
	}
	out["open_total"] = total
	out["source"] = "postgres:api_control_alerts"
	return out
}

// ---- 指标直读（供排障） ----

func (s *Server) handleAdminMetrics(w http.ResponseWriter, r *http.Request) {
	filter := strings.TrimSpace(r.URL.Query().Get("name"))
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "指标注册表不可读", nil)
		return
	}
	items := make([]map[string]any, 0, 64)
	for _, family := range families {
		name := family.GetName()
		if filter != "" && !strings.HasPrefix(name, filter) {
			continue
		}
		if !strings.HasPrefix(name, "cs_") && !strings.HasPrefix(name, "ankr_") &&
			!strings.HasPrefix(name, "rpc_") && !strings.HasPrefix(name, "tron_") &&
			!strings.HasPrefix(name, "download_") && !strings.HasPrefix(name, "coverage_") {
			continue
		}
		entry := map[string]any{"name": name, "type": family.GetType().String(), "help": family.GetHelp()}
		samples := make([]map[string]any, 0, 4)
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, pair := range metric.GetLabel() {
				labels[pair.GetName()] = pair.GetValue()
			}
			value := 0.0
			switch {
			case metric.GetCounter() != nil:
				value = metric.GetCounter().GetValue()
			case metric.GetGauge() != nil:
				value = metric.GetGauge().GetValue()
			case metric.GetHistogram() != nil:
				value = float64(metric.GetHistogram().GetSampleCount())
			case metric.GetSummary() != nil:
				value = float64(metric.GetSummary().GetSampleCount())
			}
			samples = append(samples, map[string]any{"labels": labels, "value": value})
			if len(samples) >= 20 {
				break
			}
		}
		entry["samples"] = samples
		items = append(items, entry)
		if len(items) >= 200 {
			break
		}
	}
	respondList(w, r, map[string]any{"items": items, "filter": filter,
		"window_start": observability.ProcessStartTimeValue().Format(time.RFC3339)}, len(items), nil)
}

// ---- 告警评估（§25.4） ----

func (s *Server) handleAdminObservabilityAlerts(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	slos := s.s6EvaluateSLOs(r.Context())
	breaches := make([]s6SLO, 0, len(slos))
	for _, slo := range slos {
		if slo.Status == "breach" {
			breaches = append(breaches, slo)
		}
	}
	respondOK(w, r, map[string]any{
		"as_of": s6Now().Format(time.RFC3339), "breaches": breaches, "items": slos,
		"rules": s6AlertRules(),
		"note":  "POST /observability/alerts/evaluate 会把违约项写入告警中心（含 priority 与 slo_key）。",
	})
}

// s6AlertRules 返回 §25.4 的告警级别定义（供 UI 解释与运维对照）。
func s6AlertRules() []map[string]any {
	return []map[string]any{
		{"priority": "P0", "trigger": "跨租户数据泄露 / 审计无法写入 / 密钥泄露 / 数据不可恢复 / 全局不可用",
			"slo_keys": []string{"tenant_isolation", "audit_write"}},
		{"priority": "P1", "trigger": "登录大面积失败 / 权限撤销不生效 / Provider 全不可用 / 任务进度不前进 / 导出数据损坏",
			"slo_keys": []string{"outbox_oldest", "session_revoke_latency", "rpo"}},
		{"priority": "P2", "trigger": "单模块降级 / SLO 违约 / 延迟升高 / 有 fallback 的 Provider 故障",
			"slo_keys": []string{"api_availability_user", "login_p95", "query_p95", "admin_list_p95"}},
		{"priority": "P3", "trigger": "容量趋势 / 非关键 enrichment 失败 / 低风险 UI 错误", "slo_keys": []string{}},
	}
}

// handleAdminEvaluateAlerts 评估 SLO 并把违约写成告警（幂等：按 fingerprint upsert）。
func (s *Server) handleAdminEvaluateAlerts(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	slos := s.s6EvaluateSLOs(ctx)
	created := 0
	resolved := 0
	unchanged := 0
	for _, slo := range slos {
		fingerprint := "slo:" + slo.Key
		if slo.Status == "breach" {
			severity := slo.Priority
			title := "SLO 违约：" + slo.Name
			message := fmt.Sprintf("当前值 %s，目标 %s（窗口 %s）", slo.CurrentLabel, slo.Target, slo.Window)
			labels := mustJSON(map[string]any{"slo_key": slo.Key, "source": slo.Source, "current": slo.Current, "target": slo.TargetValue})
			links := mustJSON(map[string]any{"slo_key": slo.Key, "route_template": slo.Source})
			tag, err := s.deps.Store.Pool.Exec(ctx, `
				INSERT INTO api_control_alerts (severity, priority, title, message, fingerprint, status, source, slo_key, labels, links, occurrence_count)
				VALUES ($1,$2,$3,$4,$5,'OPEN','observability',$6,$7::jsonb,$8::jsonb,1)
				ON CONFLICT (fingerprint) DO UPDATE SET
				  severity=EXCLUDED.severity, priority=EXCLUDED.priority, title=EXCLUDED.title, message=EXCLUDED.message,
				  status=CASE WHEN api_control_alerts.status='RESOLVED' THEN 'OPEN' ELSE api_control_alerts.status END,
				  resolved_at=NULL, last_seen_at=now(), occurrence_count=api_control_alerts.occurrence_count+1,
				  labels=EXCLUDED.labels, links=EXCLUDED.links, updated_at=now()`,
				severity, slo.Priority, title, message, fingerprint, slo.Key, labels, links)
			if err != nil {
				continue
			}
			if tag.RowsAffected() > 0 {
				created++
			}
			observability.RecordAlertEvaluation("breach", slo.Priority)
		} else if slo.Status == "ok" {
			tag, err := s.deps.Store.Pool.Exec(ctx, `
				UPDATE api_control_alerts SET status='RESOLVED', resolved_at=now(), resolution_note='SLO 已恢复', updated_at=now()
				WHERE fingerprint=$1 AND status IN ('OPEN','ACK')`, fingerprint)
			if err == nil && tag.RowsAffected() > 0 {
				resolved++
				observability.RecordAlertEvaluation("resolved", slo.Priority)
			} else {
				unchanged++
			}
		} else {
			unchanged++
			observability.RecordAlertEvaluation("no_data", slo.Priority)
		}
	}
	s.s6Audit(r, "admin.observability.evaluate", "admin.ops", "observability", "slo", map[string]any{
		"breaches": created, "resolved": resolved, "unchanged": unchanged,
	})
	respondOK(w, r, map[string]any{
		"as_of": s6Now().Format(time.RFC3339), "breaches_written": created,
		"resolved": resolved, "unchanged": unchanged, "items": slos,
	})
}
