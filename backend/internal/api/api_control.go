package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type apiControlFilter struct {
	Since, Until    time.Time
	Provider, Chain string
}

func parseControlFilter(r *http.Request) apiControlFilter {
	now := time.Now()
	hours := 24
	switch r.URL.Query().Get("range") {
	case "1h":
		hours = 1
	case "6h":
		hours = 6
	case "7d":
		hours = 24 * 7
	case "30d":
		hours = 24 * 30
	}
	f := apiControlFilter{Since: now.Add(-time.Duration(hours) * time.Hour), Until: now,
		Provider: strings.TrimSpace(r.URL.Query().Get("provider")), Chain: strings.TrimSpace(r.URL.Query().Get("chain"))}
	if v, err := time.Parse(time.RFC3339, r.URL.Query().Get("from")); err == nil {
		f.Since = v
	}
	if v, err := time.Parse(time.RFC3339, r.URL.Query().Get("to")); err == nil {
		f.Until = v
	}
	return f
}

func usageWhere(f apiControlFilter, start int) (string, []any) {
	args := []any{f.Since, f.Until}
	parts := []string{"usage_type<>'trc20_enrichment_summary'", fmt.Sprintf("request_time >= $%d", start), fmt.Sprintf("request_time < $%d", start+1)}
	n := start + 2
	if f.Provider != "" && f.Provider != "all" {
		parts = append(parts, fmt.Sprintf("provider=$%d", n))
		args = append(args, f.Provider)
		n++
	}
	if f.Chain != "" && f.Chain != "all" {
		parts = append(parts, fmt.Sprintf("chain=$%d", n))
		args = append(args, f.Chain)
	}
	return strings.Join(parts, " AND "), args
}

func (s *Server) handleAPIControlOverview(w http.ResponseWriter, r *http.Request) {
	f := parseControlFilter(r)
	where, args := usageWhere(f, 1)
	var calls, providerCalls, errors, retries, cacheHits int64
	var credits, cost, successRate, p50, p95, p99 float64
	err := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT
		COALESCE(SUM(method_calls),0)::bigint,
		COUNT(*) FILTER (WHERE NOT cache_hit AND NOT singleflight_hit),
		COUNT(*) FILTER (WHERE status NOT IN ('success','ok','cache_hit','singleflight_hit') OR http_status >= 400),
		COUNT(*) FILTER (WHERE retry_attempt > 0), COUNT(*) FILTER (WHERE cache_hit),
		COALESCE(SUM(credits),0)::float8, COALESCE(SUM(estimated_cost_usd),0)::float8,
		COALESCE(AVG(CASE WHEN status IN ('success','ok','cache_hit','singleflight_hit') AND http_status < 400 THEN 1.0 ELSE 0.0 END),1)::float8,
		COALESCE(percentile_cont(.50) WITHIN GROUP (ORDER BY duration_ms),0)::float8,
		COALESCE(percentile_cont(.95) WITHIN GROUP (ORDER BY duration_ms),0)::float8,
		COALESCE(percentile_cont(.99) WITHIN GROUP (ORDER BY duration_ms),0)::float8
		FROM provider_usage WHERE `+where, args...).Scan(&calls, &providerCalls, &errors, &retries, &cacheHits, &credits, &cost, &successRate, &p50, &p95, &p99)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}

	trendRows, err := s.deps.Store.Pool.Query(r.Context(), `SELECT date_trunc('hour', request_time) bucket,
		COUNT(*) FILTER (WHERE NOT cache_hit AND NOT singleflight_hit)::bigint,
		COUNT(*) FILTER (WHERE status NOT IN ('success','ok','cache_hit','singleflight_hit') OR http_status>=400)::bigint,
		COALESCE(SUM(estimated_cost_usd),0)::float8, COALESCE(percentile_cont(.95) WITHIN GROUP (ORDER BY duration_ms),0)::float8
		FROM provider_usage WHERE `+where+` GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	trend := make([]map[string]any, 0)
	for trendRows.Next() {
		var t time.Time
		var c, e int64
		var co, lat float64
		if trendRows.Scan(&t, &c, &e, &co, &lat) == nil {
			trend = append(trend, map[string]any{"time": t, "requests": c, "errors": e, "cost_usd": co, "p95_ms": lat})
		}
	}
	trendRows.Close()
	control, _ := s.refreshControlState(r.Context())
	var captureStartedAt *time.Time
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT MIN(request_time) FROM provider_usage WHERE usage_type<>'trc20_enrichment_summary'`).Scan(&captureStartedAt)
	writeJSON(w, 200, map[string]any{"window": map[string]any{"from": f.Since, "to": f.Until}, "freshness": time.Now(),
		"metrics": map[string]any{"logical_calls": calls, "provider_calls": providerCalls, "errors": errors, "retries": retries, "cache_hits": cacheHits, "success_rate": successRate, "credits": credits, "estimated_cost_usd": cost, "p50_ms": p50, "p95_ms": p95, "p99_ms": p99},
		"trend":   trend, "control": control,
		"scope": map[string]any{
			"usage":               "仅统计本平台已接入 provider_usage 审计链路的调用",
			"account":             "Ankr 官网按同一账户/项目/API Key 汇总全部客户端请求",
			"credits":             "本地按方法单价估算的已消耗积分，不是账户剩余积分",
			"capture_started_at":  captureStartedAt,
			"official_comparable": false,
			"comparison_reason":   "未接入 Ankr 官方遥测明细，且官网账户总账与本地捕获范围不同",
		},
		"sources": map[string]string{"usage": "本地审计", "cost": "本地估算", "account": "官方数据（如已配置）"}})
}

func envNumber(key string) (float64, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0, false
	}
	n, e := strconv.ParseFloat(v, 64)
	return n, e == nil
}

func (s *Server) handleAPIControlProviderAccounts(w http.ResponseWriter, r *http.Request) {
	if s.ankrMgmt != nil && s.deps.Cfg != nil && s.deps.Cfg.AnkrManagement.Enabled {
		writeJSON(w, 200, map[string]any{"accounts": []any{s.ankrMgmt.Status(r.Context(), false)}})
		return
	}
	remaining, rok := envNumber("ANKR_ACCOUNT_REMAINING_CREDITS")
	balance, bok := envNumber("ANKR_ACCOUNT_BALANCE_USD")
	plan := strings.TrimSpace(os.Getenv("ANKR_ACCOUNT_PLAN"))
	synced := strings.TrimSpace(os.Getenv("ANKR_ACCOUNT_SYNCED_AT"))
	status := "UNAVAILABLE"
	official := rok || bok || plan != ""
	stale := false
	if official {
		status = "AVAILABLE"
		if t, e := time.Parse(time.RFC3339, synced); e != nil || time.Since(t) > 24*time.Hour {
			stale = true
			status = "STALE"
		}
	}
	var remainingValue, balanceValue any
	if rok {
		remainingValue = remaining
	}
	if bok {
		balanceValue = balance
	}
	reason := ""
	if !official {
		reason = "未配置 Ankr 官方账户快照；本地估算消耗不能推导账户余额"
	}
	writeJSON(w, 200, map[string]any{"accounts": []map[string]any{{
		"provider": "ankr", "status": status, "official_available": official, "stale": stale,
		"plan": plan, "remaining_credits": remainingValue, "balance_usd": balanceValue,
		"balance_kind": "remaining", "synced_at": synced, "source": "Ankr 官方账户快照",
		"reason": reason, "console_url": os.Getenv("ANKR_PROVIDER_CONSOLE_URL"),
	}}})
}

func (s *Server) controlMetrics(ctx context.Context) (requests, failures int64, p95 float64, err error) {
	err = s.deps.Store.Pool.QueryRow(ctx, `SELECT COUNT(*)::bigint,
		COUNT(*) FILTER (WHERE status NOT IN ('success','ok','cache_hit','singleflight_hit') OR http_status>=400)::bigint,
		COALESCE(percentile_cont(.95) WITHIN GROUP (ORDER BY duration_ms),0)::float8
		FROM provider_usage WHERE request_time >= now()-interval '5 minutes'`).Scan(&requests, &failures, &p95)
	return
}

func (s *Server) refreshControlState(ctx context.Context) (map[string]any, error) {
	// TTL 到期必须自动归还控制权，不能让历史人工状态永久悬挂。
	_, _ = s.deps.Store.Pool.Exec(ctx, `UPDATE api_control_settings SET manual_until=NULL,manual_mode=NULL,manual_reason=NULL,updated_at=now() WHERE id=1 AND manual_until IS NOT NULL AND manual_until<=now()`)
	var auto, kill, paused bool
	var current, reason string
	var since time.Time
	var manualUntil *time.Time
	var manualMode, manualReason *string
	err := s.deps.Store.Pool.QueryRow(ctx, `SELECT automatic_protection,kill_switch,background_paused,state,state_reason,state_since,manual_until,manual_mode,manual_reason FROM api_control_settings WHERE id=1`).Scan(&auto, &kill, &paused, &current, &reason, &since, &manualUntil, &manualMode, &manualReason)
	if err != nil {
		return nil, err
	}
	reqs, fails, p95, metricErr := s.controlMetrics(ctx)
	warningRate, criticalRate := .03, .12
	warningP95, criticalP95 := 5000, 15000
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT warning_error_rate,critical_error_rate,warning_p95_ms,critical_p95_ms FROM api_control_policies WHERE enabled ORDER BY updated_at DESC LIMIT 1`).Scan(&warningRate, &criticalRate, &warningP95, &criticalP95)
	rate := 0.0
	if reqs > 0 {
		rate = float64(fails) / float64(reqs)
	}
	next := current
	nextReason := reason
	manualActive := manualUntil != nil && manualUntil.After(time.Now())
	if kill {
		next = "PROTECTED"
		nextReason = "全局停机开关已启用"
	} else if manualActive && manualMode != nil {
		next = *manualMode
		nextReason = "人工覆盖：" + stringValue(manualReason)
	} else if auto && metricErr == nil {
		switch {
		case reqs >= 10 && (rate >= criticalRate || p95 >= float64(criticalP95)):
			next = "ISOLATED"
			nextReason = "5 分钟错误率或延迟超过严重阈值"
		case reqs >= 10 && (rate >= warningRate || p95 >= float64(warningP95)):
			next = "DEGRADED"
			nextReason = "5 分钟错误率或延迟超过告警阈值"
		case reqs >= 5 && rate > 0:
			next = "OBSERVE"
			nextReason = "检测到少量异常，持续观察"
		default:
			next = "NORMAL"
			nextReason = "最近 5 分钟指标正常"
		}
	}
	if next != current || nextReason != reason {
		newPaused := kill || next == "ISOLATED" || next == "PROTECTED"
		_, err = s.deps.Store.Pool.Exec(ctx, `UPDATE api_control_settings SET state=$1::varchar,state_reason=$2,state_since=CASE WHEN state<>$1::varchar THEN now() ELSE state_since END,background_paused=$3,updated_at=now() WHERE id=1`, next, nextReason, newPaused)
		if err != nil {
			return nil, fmt.Errorf("update control state: %w", err)
		}
		if err == nil && next != current {
			if _, actionErr := s.deps.Store.Pool.Exec(ctx, `INSERT INTO api_control_actions(action,source,from_state,to_state,reason,detail) VALUES('state_transition','automatic',$1::varchar,$2::varchar,$3,jsonb_build_object('requests',$4::bigint,'failures',$5::bigint,'p95_ms',$6::float8))`, current, next, nextReason, reqs, fails, p95); actionErr != nil {
				s.deps.Log.Error("persist automatic control action failed", "err", actionErr)
			}
			if next == "DEGRADED" || next == "ISOLATED" || next == "PROTECTED" {
				severity := "WARNING"
				if next == "ISOLATED" || next == "PROTECTED" {
					severity = "CRITICAL"
				}
				if _, alertErr := s.deps.Store.Pool.Exec(ctx, `INSERT INTO api_control_alerts(severity,title,message,fingerprint) VALUES($1::varchar,$2::varchar,$3,'api-control-state') ON CONFLICT(fingerprint) DO UPDATE SET severity=EXCLUDED.severity,title=EXCLUDED.title,message=EXCLUDED.message,status='OPEN',last_seen_at=now(),resolved_at=NULL`, severity, "API 控制状态进入 "+next, nextReason); alertErr != nil {
					s.deps.Log.Error("persist automatic control alert failed", "err", alertErr)
				}
			} else if next == "NORMAL" {
				_, _ = s.deps.Store.Pool.Exec(ctx, `UPDATE api_control_alerts SET status='RESOLVED',resolved_at=now(),last_seen_at=now() WHERE fingerprint='api-control-state' AND status='OPEN'`)
			}
		}
		paused = newPaused
		current = next
		reason = nextReason
		since = time.Now()
	}
	return map[string]any{"state": current, "reason": reason, "since": since, "automatic_protection": auto, "kill_switch": kill, "background_paused": paused, "manual_until": manualUntil, "manual_mode": manualMode, "manual_reason": manualReason, "window_requests": reqs, "window_failures": fails, "error_rate": rate, "p95_ms": p95}, nil
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (s *Server) handleAPIControlState(w http.ResponseWriter, r *http.Request) {
	v, e := s.refreshControlState(r.Context())
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) handleAPIControlPolicies(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch {
		var q struct {
			AutomaticProtection *bool `json:"automatic_protection"`
		}
		if decodeJSON(r, &q) != nil || q.AutomaticProtection == nil {
			writeJSON(w, 400, map[string]any{"error": "automatic_protection required"})
			return
		}
		_, e := s.deps.Store.Pool.Exec(r.Context(), `UPDATE api_control_settings SET automatic_protection=$1,updated_at=now() WHERE id=1`, *q.AutomaticProtection)
		if e != nil {
			writeJSON(w, 500, map[string]any{"error": e.Error()})
			return
		}
		s.auditControl(r, "policy.update", map[string]any{"automatic_protection": *q.AutomaticProtection})
	}
	rows, e := s.deps.Store.Pool.Query(r.Context(), `SELECT id::text,name,enabled,warning_error_rate,critical_error_rate,warning_p95_ms,critical_p95_ms,cooldown_seconds,updated_at FROM api_control_policies ORDER BY name`)
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, n string
		var en bool
		var w, c float64
		var wp, cp, cd int
		var u time.Time
		if rows.Scan(&id, &n, &en, &w, &c, &wp, &cp, &cd, &u) == nil {
			out = append(out, map[string]any{"id": id, "name": n, "enabled": en, "warning_error_rate": w, "critical_error_rate": c, "warning_p95_ms": wp, "critical_p95_ms": cp, "cooldown_seconds": cd, "updated_at": u})
		}
	}
	writeJSON(w, 200, map[string]any{"policies": out})
}

func (s *Server) handleAPIControlOverride(w http.ResponseWriter, r *http.Request) {
	if !controlAdmin(r) {
		writeJSON(w, 403, map[string]any{"error": "需要 owner 或 admin 权限"})
		return
	}
	var q struct {
		Mode, Reason string
		TTLMinutes   int `json:"ttl_minutes"`
	}
	if decodeJSON(r, &q) != nil || q.Reason == "" {
		writeJSON(w, 400, map[string]any{"error": "mode、reason 和 ttl_minutes 必填"})
		return
	}
	q.Mode = strings.ToUpper(q.Mode)
	allowed := map[string]bool{"NORMAL": true, "OBSERVE": true, "DEGRADED": true, "ISOLATED": true, "PROTECTED": true, "RECOVERING": true}
	if !allowed[q.Mode] || q.TTLMinutes < 1 || q.TTLMinutes > 1440 {
		writeJSON(w, 400, map[string]any{"error": "无效模式或 TTL（1-1440 分钟）"})
		return
	}
	until := time.Now().Add(time.Duration(q.TTLMinutes) * time.Minute)
	_, e := s.deps.Store.Pool.Exec(r.Context(), `UPDATE api_control_settings SET manual_mode=$1,manual_reason=$2,manual_until=$3,updated_at=now() WHERE id=1`, q.Mode, q.Reason, until)
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	s.auditControl(r, "manual_override", map[string]any{"mode": q.Mode, "reason": q.Reason, "until": until})
	v, _ := s.refreshControlState(r.Context())
	writeJSON(w, 200, v)
}

func (s *Server) handleAPIControlKillSwitch(w http.ResponseWriter, r *http.Request) {
	if !controlAdmin(r) {
		writeJSON(w, 403, map[string]any{"error": "需要 owner 或 admin 权限"})
		return
	}
	var q struct {
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if decodeJSON(r, &q) != nil || q.Reason == "" {
		writeJSON(w, 400, map[string]any{"error": "enabled 和 reason 必填"})
		return
	}
	_, e := s.deps.Store.Pool.Exec(r.Context(), `UPDATE api_control_settings SET kill_switch=$1,updated_at=now() WHERE id=1`, q.Enabled)
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	s.auditControl(r, "kill_switch", map[string]any{"enabled": q.Enabled, "reason": q.Reason})
	v, _ := s.refreshControlState(r.Context())
	writeJSON(w, 200, v)
}

func controlAdmin(r *http.Request) bool {
	role := auth.FromContext(r.Context()).Role
	return role == "owner" || role == "admin"
}
func (s *Server) auditControl(r *http.Request, action string, detail map[string]any) {
	u := auth.FromContext(r.Context())
	detail["actor"] = u.Email
	_ = s.deps.Store.InsertAudit(r.Context(), nil, "api_control."+action, "api_control", "global", detail)
	_, _ = s.deps.Store.Pool.Exec(r.Context(), `INSERT INTO api_control_actions(action,source,reason,detail,actor) VALUES($1,'manual',$2,$3,$4)`, action, fmt.Sprint(detail["reason"]), detail, u.Email)
}

func (s *Server) handleAPIControlActions(w http.ResponseWriter, r *http.Request) {
	rows, e := s.deps.Store.Pool.Query(r.Context(), `SELECT id::text,action,source,from_state,to_state,reason,detail,actor,created_at FROM api_control_actions ORDER BY created_at DESC LIMIT 200`)
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, a, src, from, to, reason, actor string
		var detail []byte
		var at time.Time
		if rows.Scan(&id, &a, &src, &from, &to, &reason, &detail, &actor, &at) == nil {
			out = append(out, map[string]any{"id": id, "action": a, "source": src, "from_state": from, "to_state": to, "reason": reason, "detail": string(detail), "actor": actor, "created_at": at})
		}
	}
	writeJSON(w, 200, map[string]any{"actions": out})
}

func (s *Server) handleAPIControlRequests(w http.ResponseWriter, r *http.Request) {
	if id := chi.URLParam(r, "id"); id != "" {
		s.handleAPIControlRequestDetail(w, r, id)
		return
	}
	f := parseControlFilter(r)
	where, args := usageWhere(f, 1)
	filters := []struct{ k, c string }{{"operation", "method"}, {"usage_type", "usage_type"}, {"status", "status"}, {"error_class", "error_class"}}
	n := len(args) + 1
	for _, x := range filters {
		if v := strings.TrimSpace(r.URL.Query().Get(x.k)); v != "" && v != "all" {
			where += fmt.Sprintf(" AND %s=$%d", x.c, n)
			args = append(args, v)
			n++
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	q := `SELECT id::text,provider,COALESCE(chain,''),method,COALESCE(method_class,''),COALESCE(usage_type,''),request_time,duration_ms,status,http_status,COALESCE(error_class,''),COALESCE(api_error,''),credits::float8,estimated_cost_usd::float8,COALESCE(trace_id,''),COALESCE(request_id,''),retry_attempt,cache_hit,singleflight_hit,method_calls,batch_size FROM provider_usage WHERE ` + where + fmt.Sprintf(` ORDER BY request_time DESC LIMIT $%d OFFSET $%d`, n, n+1)
	rows, e := s.deps.Store.Pool.Query(r.Context(), q, args...)
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, p, c, m, mc, u, status, ec, ae, tr, req string
		var at time.Time
		var dur, http, retry, calls, batch int
		var cr, cost float64
		var cache, single bool
		if rows.Scan(&id, &p, &c, &m, &mc, &u, &at, &dur, &status, &http, &ec, &ae, &cr, &cost, &tr, &req, &retry, &cache, &single, &calls, &batch) == nil {
			out = append(out, map[string]any{"id": id, "provider": p, "chain": c, "method": m, "method_class": mc, "usage_type": u, "request_time": at, "duration_ms": dur, "status": status, "http_status": http, "error_class": ec, "api_error": ae, "credits": cr, "cost_usd": cost, "trace_id": tr, "request_id": req, "retry_attempt": retry, "cache_hit": cache, "singleflight_hit": single, "method_calls": calls, "batch_size": batch})
		}
	}
	var total int64
	countArgs := args[:len(args)-2]
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM provider_usage WHERE `+where, countArgs...).Scan(&total)
	writeJSON(w, 200, map[string]any{"requests": out, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleAPIControlRequestDetail(w http.ResponseWriter, r *http.Request, id string) {
	var row map[string]any
	_ = row
	var rid, p, c, m, mc, u, status, ec, ae, tr, req, dataset, job, shard string
	var at time.Time
	var dur, http, retry, calls, batch int
	var cr, cost float64
	var cache, single bool
	e := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT id::text,provider,COALESCE(chain,''),method,COALESCE(method_class,''),COALESCE(usage_type,''),request_time,duration_ms,status,http_status,COALESCE(error_class,''),COALESCE(api_error,''),credits::float8,estimated_cost_usd::float8,COALESCE(trace_id,''),COALESCE(request_id,''),retry_attempt,cache_hit,singleflight_hit,method_calls,batch_size,dataset,COALESCE(job_id::text,''),COALESCE(shard_id::text,'') FROM provider_usage WHERE id=$1`, id).Scan(&rid, &p, &c, &m, &mc, &u, &at, &dur, &status, &http, &ec, &ae, &cr, &cost, &tr, &req, &retry, &cache, &single, &calls, &batch, &dataset, &job, &shard)
	if e == pgx.ErrNoRows {
		writeJSON(w, 404, map[string]any{"error": "request not found"})
		return
	}
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	v := map[string]any{"id": rid, "provider": p, "chain": c, "method": m, "method_class": mc, "usage_type": u, "request_time": at, "duration_ms": dur, "status": status, "http_status": http, "error_class": ec, "api_error": ae, "credits": cr, "cost_usd": cost, "trace_id": tr, "request_id": req, "retry_attempt": retry, "cache_hit": cache, "singleflight_hit": single, "method_calls": calls, "batch_size": batch, "dataset": dataset, "job_id": job, "shard_id": shard}
	writeJSON(w, 200, map[string]any{"request": v, "attempts": []map[string]any{v}})
}

func (s *Server) aggregateUsage(w http.ResponseWriter, r *http.Request, kind string) {
	f := parseControlFilter(r)
	where, args := usageWhere(f, 1)
	group, label := "method", "operation"
	switch kind {
	case "errors":
		group, label = "COALESCE(NULLIF(error_class,''),'none')", "error_class"
	case "costs":
		group, label = "COALESCE(NULLIF(usage_type,''),'unknown')", "usage_type"
	case "providers":
		group, label = "provider", "provider"
	case "usage-types":
		group, label = "COALESCE(NULLIF(usage_type,''),'unknown')", "usage_type"
	}
	q := `SELECT ` + group + ` label,COUNT(*) FILTER (WHERE NOT cache_hit AND NOT singleflight_hit)::bigint,COALESCE(SUM(method_calls),0)::bigint,COUNT(*) FILTER (WHERE status NOT IN ('success','ok','cache_hit','singleflight_hit') OR http_status>=400)::bigint,COALESCE(AVG(duration_ms),0)::float8,COALESCE(percentile_cont(.95) WITHIN GROUP (ORDER BY duration_ms),0)::float8,COALESCE(SUM(credits),0)::float8,COALESCE(SUM(estimated_cost_usd),0)::float8 FROM provider_usage WHERE ` + where + ` GROUP BY 1 ORDER BY 8 DESC,2 DESC LIMIT 100`
	rows, e := s.deps.Store.Pool.Query(r.Context(), q, args...)
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var l string
		var requests, calls, errors int64
		var avg, p95, credits, cost float64
		if rows.Scan(&l, &requests, &calls, &errors, &avg, &p95, &credits, &cost) == nil {
			out = append(out, map[string]any{label: l, "requests": requests, "logical_calls": calls, "errors": errors, "avg_ms": avg, "p95_ms": p95, "credits": credits, "estimated_cost_usd": cost, "cost_source": "内部估算"})
		}
	}
	writeJSON(w, 200, map[string]any{"items": out, "freshness": time.Now()})
}

func (s *Server) handleAPIControlBudgets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !controlAdmin(r) {
			writeJSON(w, 403, map[string]any{"error": "需要 owner 或 admin 权限"})
			return
		}
		var q struct {
			Name, Provider, Period string
			LimitUSD               float64 `json:"limit_usd"`
			WarnPercent            int     `json:"warn_percent"`
		}
		if decodeJSON(r, &q) != nil || q.Name == "" || q.LimitUSD <= 0 {
			writeJSON(w, 400, map[string]any{"error": "name 和正数 limit_usd 必填"})
			return
		}
		if q.Provider == "" {
			q.Provider = "all"
		}
		if q.Period == "" {
			q.Period = "monthly"
		}
		if q.WarnPercent == 0 {
			q.WarnPercent = 80
		}
		_, e := s.deps.Store.Pool.Exec(r.Context(), `INSERT INTO api_control_budgets(name,provider,period,limit_usd,warn_percent) VALUES($1,$2,$3,$4,$5)`, q.Name, q.Provider, q.Period, q.LimitUSD, q.WarnPercent)
		if e != nil {
			writeJSON(w, 500, map[string]any{"error": e.Error()})
			return
		}
		s.auditControl(r, "budget.create", map[string]any{"name": q.Name, "limit_usd": q.LimitUSD})
	}
	rows, e := s.deps.Store.Pool.Query(r.Context(), `SELECT b.id::text,b.name,b.provider,b.period,b.limit_usd,b.warn_percent,b.enabled,b.updated_at,COALESCE((SELECT SUM(estimated_cost_usd) FROM provider_usage u WHERE (b.provider='all' OR u.provider=b.provider) AND u.request_time>=date_trunc('month',now())),0)::float8 FROM api_control_budgets b ORDER BY b.updated_at DESC`)
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, n, p, period string
		var limit, used float64
		var warn int
		var en bool
		var at time.Time
		if rows.Scan(&id, &n, &p, &period, &limit, &warn, &en, &at, &used) == nil {
			out = append(out, map[string]any{"id": id, "name": n, "provider": p, "period": period, "limit_usd": limit, "warn_percent": warn, "enabled": en, "used_usd": used, "utilization": used / limit, "updated_at": at, "source": "内部估算"})
		}
	}
	writeJSON(w, 200, map[string]any{"budgets": out})
}

func (s *Server) handleAPIControlAlerts(w http.ResponseWriter, r *http.Request) {
	if id := chi.URLParam(r, "id"); id != "" {
		if !controlAdmin(r) {
			writeJSON(w, 403, map[string]any{"error": "需要 owner 或 admin 权限"})
			return
		}
		var q struct {
			Action     string
			TTLMinutes int `json:"ttl_minutes"`
		}
		if decodeJSON(r, &q) != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid body"})
			return
		}
		var e error
		switch q.Action {
		case "resolve":
			_, e = s.deps.Store.Pool.Exec(r.Context(), `UPDATE api_control_alerts SET status='RESOLVED',resolved_at=now() WHERE id=$1`, id)
		case "mute":
			if q.TTLMinutes < 1 {
				q.TTLMinutes = 60
			}
			_, e = s.deps.Store.Pool.Exec(r.Context(), `UPDATE api_control_alerts SET status='MUTED',muted_until=now()+($2||' minutes')::interval WHERE id=$1`, id, q.TTLMinutes)
		default:
			writeJSON(w, 400, map[string]any{"error": "action must be resolve or mute"})
			return
		}
		if e != nil {
			writeJSON(w, 500, map[string]any{"error": e.Error()})
			return
		}
		s.auditControl(r, "alert."+q.Action, map[string]any{"alert_id": id})
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	rows, e := s.deps.Store.Pool.Query(r.Context(), `SELECT id::text,severity,title,message,status,first_seen_at,last_seen_at,muted_until,resolved_at FROM api_control_alerts ORDER BY last_seen_at DESC LIMIT 200`)
	if e != nil {
		writeJSON(w, 500, map[string]any{"error": e.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, sev, title, msg, status string
		var first, last time.Time
		var muted, resolved *time.Time
		if rows.Scan(&id, &sev, &title, &msg, &status, &first, &last, &muted, &resolved) == nil {
			out = append(out, map[string]any{"id": id, "severity": sev, "title": title, "message": msg, "status": status, "first_seen_at": first, "last_seen_at": last, "muted_until": muted, "resolved_at": resolved})
		}
	}
	writeJSON(w, 200, map[string]any{"alerts": out})
}
