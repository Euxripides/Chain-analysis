package api

// ---- 风险事件（§19.2 A-RISK-01，admin:risk:read） ----
//
// 数据源是真实存在的 login_events（risk_level / result / reason_code / device / ip /
// entry_host / trace_id 都由认证链路写入），没有任何占位值。判"风险事件"的口径：
// risk_level 命中所选等级，或 result 为 blocked/denied —— token_reuse、rate_limited、
// account_locked 等都落在里面。窗口默认 7 天，等级默认 high+，都可由查询参数切换。

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	riskDefaultWindowHours = 168
	riskMaxWindowHours     = 24 * 90
	riskMaxLimit           = 200
)

func (s *Server) registerAdminRiskRoutes(r chi.Router) {
	r.Get("/risk/summary", s.require("admin:risk:read", s.handleAdminRiskSummary))
	r.Get("/risk/events", s.require("admin:risk:read", s.handleAdminRiskEvents))
}

func riskWindowHours(r *http.Request) int {
	hours, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("window_hours")))
	if err != nil || hours <= 0 {
		return riskDefaultWindowHours
	}
	if hours > riskMaxWindowHours {
		return riskMaxWindowHours
	}
	return hours
}

// riskLevelsForFilter 把 ?level= 收窄成允许的等级集合；未识别一律回落 high+，绝不等于 all。
func riskLevelsForFilter(r *http.Request) []string {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("level"))) {
	case "all":
		return []string{"low", "medium", "high", "critical"}
	case "medium":
		return []string{"medium", "high", "critical"}
	case "critical":
		return []string{"critical"}
	default:
		return []string{"high", "critical"}
	}
}

// riskScope 追加组织谓词（非平台范围的主体只能看本组织），所有风险查询都必须经过它。
func (s *Server) riskScope(r *http.Request, args []any) (string, []any) {
	if s.isPlatformScope(r) {
		return "", args
	}
	args = append(args, PrincipalFrom(r.Context()).OrganizationID)
	return " AND organization_id = $" + strconv.Itoa(len(args)), args
}

// riskBase 是汇总与明细共用的 FROM/WHERE 前缀（$1 窗口小时数，$2 等级数组）。
func riskBase(scope string) string {
	// 异常认证事件的判据：不是"成功"（blocked/denied/failure），或者所属等级命中所选窗口。
	// 这样 level=high+ 只留下真正需要看的行，level=all 也不会退化成"全部登录流水"。
	return "FROM login_events WHERE occurred_at > now() - make_interval(hours => $1)" +
		" AND (result <> 'success' OR risk_level = ANY($2))" + scope
}

type riskTotals struct {
	Events             int `json:"events"`
	Critical           int `json:"critical"`
	High               int `json:"high"`
	Medium             int `json:"medium"`
	Blocked            int `json:"blocked"`
	Denied             int `json:"denied"`
	Failure            int `json:"failure"`
	AffectedIdentities int `json:"affected_identities"`
	AffectedIPs        int `json:"affected_ips"`
}

type riskReasonRow struct {
	ReasonCode string    `json:"reason_code"`
	Events     int       `json:"events"`
	Critical   int       `json:"critical"`
	Blocked    int       `json:"blocked"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type riskHourRow struct {
	Hour    time.Time `json:"hour"`
	Events  int       `json:"events"`
	Blocked int       `json:"blocked"`
}

type riskIPRow struct {
	ClientIP   string    `json:"client_ip"`
	Events     int       `json:"events"`
	Critical   int       `json:"critical"`
	Subjects   int       `json:"subjects"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type riskEventRow struct {
	ID          string    `json:"id"`
	OccurredAt  time.Time `json:"occurred_at"`
	RiskLevel   string    `json:"risk_level"`
	Result      string    `json:"result"`
	ReasonCode  string    `json:"reason_code"`
	RiskAction  string    `json:"risk_action"`
	Subject     string    `json:"subject_masked"`
	IdentityID  string    `json:"identity_id,omitempty"`
	Audience    string    `json:"audience"`
	AuthMethod  string    `json:"auth_method"`
	ClientIP    string    `json:"client_ip"`
	EntryHost   string    `json:"entry_host"`
	DeviceLabel string    `json:"device_label"`
	Browser     string    `json:"browser_name"`
	OSName      string    `json:"os_name"`
	IsNewDevice bool      `json:"is_new_device"`
	SessionID   string    `json:"session_id,omitempty"`
	RequestID   string    `json:"request_id"`
	TraceID     string    `json:"trace_id"`
}

func (s *Server) handleAdminRiskSummary(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	hours := riskWindowHours(r)
	levels := riskLevelsForFilter(r)
	ctx := r.Context()
	pool := s.deps.Store.Pool

	scope, args := s.riskScope(r, []any{hours, levels})
	base := riskBase(scope)

	var totals riskTotals
	if err := pool.QueryRow(ctx, "SELECT count(*),"+
		" count(*) FILTER (WHERE risk_level = 'critical'),"+
		" count(*) FILTER (WHERE risk_level = 'high'),"+
		" count(*) FILTER (WHERE risk_level = 'medium'),"+
		" count(*) FILTER (WHERE result = 'blocked'),"+
		" count(*) FILTER (WHERE result = 'denied'),"+
		" count(*) FILTER (WHERE result = 'failure'),"+
		" count(DISTINCT identity_id), count(DISTINCT client_ip) "+base, args...).Scan(
		&totals.Events, &totals.Critical, &totals.High, &totals.Medium, &totals.Blocked,
		&totals.Denied, &totals.Failure, &totals.AffectedIdentities, &totals.AffectedIPs); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险事件汇总失败", nil)
		return
	}

	byReason := []riskReasonRow{}
	reasonArgs := append(append([]any{}, args...), 8)
	reasonRows, err := pool.Query(ctx, "SELECT coalesce(reason_code,''), count(*),"+
		" count(*) FILTER (WHERE risk_level = 'critical'),"+
		" count(*) FILTER (WHERE result = 'blocked'), max(occurred_at) "+base+
		" GROUP BY 1 ORDER BY 2 DESC LIMIT $"+strconv.Itoa(len(reasonArgs)), reasonArgs...)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险原因分布失败", nil)
		return
	}
	for reasonRows.Next() {
		var item riskReasonRow
		if err := reasonRows.Scan(&item.ReasonCode, &item.Events, &item.Critical, &item.Blocked, &item.LastSeenAt); err != nil {
			reasonRows.Close()
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险原因分布失败", nil)
			return
		}
		byReason = append(byReason, item)
	}
	reasonRows.Close()

	byHour := []riskHourRow{}
	hourRows, err := pool.Query(ctx, "SELECT date_trunc('hour', occurred_at), count(*),"+
		" count(*) FILTER (WHERE result = 'blocked') "+base+" GROUP BY 1 ORDER BY 1", args...)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险时间线失败", nil)
		return
	}
	for hourRows.Next() {
		var item riskHourRow
		if err := hourRows.Scan(&item.Hour, &item.Events, &item.Blocked); err != nil {
			hourRows.Close()
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险时间线失败", nil)
			return
		}
		byHour = append(byHour, item)
	}
	hourRows.Close()

	topIPs := []riskIPRow{}
	ipArgs := append(append([]any{}, args...), 8)
	ipRows, err := pool.Query(ctx, "SELECT coalesce(host(client_ip),''), count(*),"+
		" count(*) FILTER (WHERE risk_level = 'critical'),"+
		" count(DISTINCT subject_digest), max(occurred_at) "+base+
		" GROUP BY 1 ORDER BY 2 DESC LIMIT $"+strconv.Itoa(len(ipArgs)), ipArgs...)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险来源分布失败", nil)
		return
	}
	for ipRows.Next() {
		var item riskIPRow
		if err := ipRows.Scan(&item.ClientIP, &item.Events, &item.Critical, &item.Subjects, &item.LastSeenAt); err != nil {
			ipRows.Close()
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险来源分布失败", nil)
			return
		}
		topIPs = append(topIPs, item)
	}
	ipRows.Close()

	s.writeAuditNow(r, "admin:risk:read", "admin.security", "risk_event", "",
		map[string]any{"window_hours": hours, "level": levels, "result_count": totals.Events})

	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"as_of":        time.Now().UTC(),
		"window_hours": hours,
		"levels":       levels,
		"totals":       totals,
		"by_reason":    byReason,
		"by_hour":      byHour,
		"top_ips":      topIPs,
	}})
}

func (s *Server) handleAdminRiskEvents(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	hours := riskWindowHours(r)
	levels := riskLevelsForFilter(r)
	limit, offset := pagination(r)
	if limit <= 0 || limit > riskMaxLimit {
		limit = 50
	}
	ctx := r.Context()
	pool := s.deps.Store.Pool

	scope, args := s.riskScope(r, []any{hours, levels})
	base := riskBase(scope)
	if reason := strings.TrimSpace(r.URL.Query().Get("reason_code")); reason != "" {
		args = append(args, reason)
		base += " AND reason_code = $" + strconv.Itoa(len(args))
	}
	if result := strings.TrimSpace(r.URL.Query().Get("result")); result != "" {
		args = append(args, result)
		base += " AND result = $" + strconv.Itoa(len(args))
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT count(*) "+base, args...).Scan(&total); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险事件失败", nil)
		return
	}

	listArgs := append(append([]any{}, args...), limit, offset)
	rows, err := pool.Query(ctx, "SELECT id::text, occurred_at, coalesce(risk_level,''), result,"+
		" coalesce(reason_code,''), coalesce(risk_action,''), coalesce(subject_masked,''),"+
		" coalesce(identity_id::text,''), coalesce(audience,''), coalesce(auth_method,''),"+
		" coalesce(host(client_ip),''), coalesce(entry_host,''), coalesce(device_label,''),"+
		" coalesce(browser_name,''), coalesce(os_name,''), is_new_device,"+
		" coalesce(session_id::text,''), coalesce(request_id,''), coalesce(trace_id,'') "+base+
		" ORDER BY occurred_at DESC LIMIT $"+strconv.Itoa(len(listArgs)-1)+
		" OFFSET $"+strconv.Itoa(len(listArgs)), listArgs...)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险事件失败", nil)
		return
	}
	defer rows.Close()

	list := []riskEventRow{}
	for rows.Next() {
		var item riskEventRow
		if err := rows.Scan(&item.ID, &item.OccurredAt, &item.RiskLevel, &item.Result, &item.ReasonCode,
			&item.RiskAction, &item.Subject, &item.IdentityID, &item.Audience, &item.AuthMethod,
			&item.ClientIP, &item.EntryHost, &item.DeviceLabel, &item.Browser, &item.OSName,
			&item.IsNewDevice, &item.SessionID, &item.RequestID, &item.TraceID); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取风险事件失败", nil)
			return
		}
		list = append(list, item)
	}

	s.writeAuditNow(r, "admin:risk:read", "admin.security", "risk_event", "",
		map[string]any{"window_hours": hours, "level": levels, "result_count": total})

	respondList(w, r, list, total, nil)
}
