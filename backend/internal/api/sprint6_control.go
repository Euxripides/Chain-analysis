package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// Sprint 6 控制面（A-CONTROL-01 / A-KILL-01 / A-PERF-01）。
// 全部挂在 /api/admin/v1 下，统一权限、preview/command、审计与并发控制。

// registerAdminControlRoutes 挂载控制面端点（§21.6「任务、数据、Provider、控制」）。
func (s *Server) registerAdminControlRoutes(r chi.Router) {
	// 实时控制面（A-CONTROL-01）
	r.Get("/control/state", s.require("admin:killswitch:read", s.handleAdminControlState))
	r.Get("/control/actions", s.require("admin:killswitch:read", s.handleAdminControlActions))
	r.Get("/control/policies", s.require("admin:killswitch:read", s.handleAdminControlPolicies))
	r.Patch("/control/policies", s.require("admin:killswitch:engage", s.handleAdminUpdateControlPolicies))

	// Kill Switch（A-KILL-01）：影响预览 → 原因 → step-up → 第二审批人 → 执行 → 健康验证 → 恢复
	r.Post("/control/kill-switch/preview", s.require("admin:killswitch:engage", s.handleAdminKillSwitchPreview))
	r.Get("/control/kill-switch/requests", s.require("admin:killswitch:read", s.handleAdminKillSwitchRequests))
	r.Post("/control/kill-switch/requests", s.require("admin:killswitch:engage", s.handleAdminKillSwitchRequest))
	r.Get("/control/kill-switch/requests/{id}", s.require("admin:killswitch:read", s.handleAdminKillSwitchRequestDetail))
	r.Post("/control/kill-switch/requests/{id}/approve", s.require("admin:approval:decide", s.handleAdminKillSwitchApprove))
	r.Post("/control/kill-switch/requests/{id}/reject", s.require("admin:approval:decide", s.handleAdminKillSwitchReject))
	r.Post("/control/kill-switch/requests/{id}/execute", s.require("admin:killswitch:engage", s.handleAdminKillSwitchExecute))
	r.Post("/control/kill-switch/requests/{id}/restore", s.require("admin:killswitch:release", s.handleAdminKillSwitchRestore))
	r.Get("/control/kill-switch/health", s.require("admin:killswitch:read", s.handleAdminKillSwitchHealth))

	// 审批中心（通用，供 Kill Switch / 删除 / 恢复共用）
	r.Get("/approvals", s.require("admin:approval:read", s.handleAdminApprovals))

	// 性能控制（A-PERF-01）
	r.Get("/runtime/performance", s.require("admin:performance:read", s.handleAdminPerformance))
	r.Post("/runtime/performance/preview", s.require("admin:performance:update", s.handleAdminPerformancePreview))
	r.Post("/runtime/performance/commands/{command}", s.require("admin:performance:update", s.handleAdminPerformanceCommand))
}

// ---- 实时控制面 ----

type s6ControlState struct {
	AsOf                string            `json:"as_of"`
	State               string            `json:"state"`
	StateReason         string            `json:"state_reason"`
	StateSince          *time.Time        `json:"state_since"`
	AutomaticProtection bool              `json:"automatic_protection"`
	KillSwitch          bool              `json:"kill_switch"`
	BackgroundPaused    bool              `json:"background_paused"`
	ManualMode          string            `json:"manual_mode"`
	ManualReason        string            `json:"manual_reason"`
	ManualUntil         *time.Time        `json:"manual_until"`
	Queue               map[string]any    `json:"queue"`
	Circuits            map[string]any    `json:"circuits"`
	ActiveEngagement    *s6Engagement     `json:"active_engagement"`
	PendingApprovals    int               `json:"pending_approvals"`
	DataSource          map[string]string `json:"data_source"`
}

func (s *Server) handleAdminControlState(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	var st s6ControlState
	var since *time.Time
	var manualUntil *time.Time
	err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT automatic_protection, kill_switch, background_paused, state, state_reason, state_since,
		       manual_until, coalesce(manual_mode,''), coalesce(manual_reason,'')
		FROM api_control_settings WHERE id=1`).
		Scan(&st.AutomaticProtection, &st.KillSwitch, &st.BackgroundPaused, &st.State, &st.StateReason, &since, &manualUntil, &st.ManualMode, &st.ManualReason)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制状态读取失败", nil)
		return
	}
	st.AsOf = s6Now().Format(time.RFC3339)
	st.StateSince = since
	st.ManualUntil = manualUntil

	queue := map[string]any{}
	var running, waiting, failed, paused int
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status IN ('RUNNING','RESUMING')),
		count(*) FILTER (WHERE status IN ('CREATED','PLANNING','QUEUED','PAUSE_REQUESTED')),
		count(*) FILTER (WHERE status = 'FAILED'),
		count(*) FILTER (WHERE status = 'PAUSED')
		FROM download_jobs`).Scan(&running, &waiting, &failed, &paused)
	queue["running"] = running
	queue["waiting"] = waiting
	queue["failed"] = failed
	queue["paused"] = paused
	st.Queue = queue

	circuits := map[string]any{}
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT provider_key, status, capability_drift IS NOT NULL FROM provider_descriptors ORDER BY provider_key`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var key, status string
			var drift bool
			if scanErr := rows.Scan(&key, &status, &drift); scanErr == nil {
				circuits[key] = map[string]any{"status": status, "capability_drift": drift}
			}
		}
	}
	st.Circuits = circuits

	if engagement, err := s.s6ActiveEngagement(ctx); err == nil {
		st.ActiveEngagement = engagement
	}
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM approval_requests WHERE status='pending'`).Scan(&st.PendingApprovals)
	st.DataSource = map[string]string{
		"state": "postgres:api_control_settings", "queue": "postgres:download_jobs",
		"circuits": "postgres:provider_descriptors", "approvals": "postgres:approval_requests",
	}
	respondOK(w, r, st)
}

func (s *Server) handleAdminControlActions(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	limit := s6Limit(r.URL.Query().Get("limit"), 50, 200)
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, action, source, coalesce(from_state,''), coalesce(to_state,''), coalesce(reason,''), coalesce(detail,'{}'::jsonb), coalesce(actor,''), created_at
		FROM api_control_actions ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "控制动作历史读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		var id, action, source, fromState, toState, reason, actor string
		var detail []byte
		var createdAt time.Time
		if err := rows.Scan(&id, &action, &source, &fromState, &toState, &reason, &detail, &actor, &createdAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "控制动作历史读取失败", nil)
			return
		}
		var parsed any
		_ = json.Unmarshal(detail, &parsed)
		items = append(items, map[string]any{
			"id": id, "action": action, "source": source, "from_state": fromState, "to_state": toState,
			"reason": reason, "detail": parsed, "actor": actor, "created_at": createdAt,
		})
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

func (s *Server) handleAdminControlPolicies(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var id string
	var auto bool
	var warningRate, criticalRate float64
	var warningP95, criticalP95, cooldown int
	var updatedAt time.Time
	err := s.deps.Store.Pool.QueryRow(r.Context(), `
		SELECT id::text, enabled, warning_error_rate, critical_error_rate, warning_p95_ms, critical_p95_ms, cooldown_seconds, updated_at
		FROM api_control_policies ORDER BY updated_at DESC LIMIT 1`).
		Scan(&id, &auto, &warningRate, &criticalRate, &warningP95, &criticalP95, &cooldown, &updatedAt)
	if err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制策略读取失败", nil)
		return
	}
	var automaticProtection bool
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT automatic_protection FROM api_control_settings WHERE id=1`).Scan(&automaticProtection)
	respondOK(w, r, map[string]any{
		"id": id, "automatic_protection": automaticProtection, "policy_enabled": auto,
		"warning_error_rate": warningRate, "critical_error_rate": criticalRate,
		"warning_p95_ms": warningP95, "critical_p95_ms": criticalP95,
		"cooldown_seconds": cooldown, "updated_at": updatedAt,
		"data_source": "postgres:api_control_policies",
	})
}

type s6ControlPolicyInput struct {
	AutomaticProtection *bool    `json:"automatic_protection"`
	WarningErrorRate    *float64 `json:"warning_error_rate"`
	CriticalErrorRate   *float64 `json:"critical_error_rate"`
	WarningP95Ms        *int     `json:"warning_p95_ms"`
	CriticalP95Ms       *int     `json:"critical_p95_ms"`
	CooldownSeconds     *int     `json:"cooldown_seconds"`
	Reason              string   `json:"reason"`
	StepUpPassword      string   `json:"step_up_password"`
}

func (s *Server) handleAdminUpdateControlPolicies(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in s6ControlPolicyInput
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写修改原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.control.policies.update") {
		return
	}
	ctx := r.Context()
	before := map[string]any{}
	var id string
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT id::text, enabled, warning_error_rate, critical_error_rate, warning_p95_ms, critical_p95_ms, cooldown_seconds FROM api_control_policies ORDER BY updated_at DESC LIMIT 1`).
		Scan(&id, new(bool), new(float64), new(float64), new(int), new(int), new(int))
	var autoBefore bool
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT automatic_protection FROM api_control_settings WHERE id=1`).Scan(&autoBefore)
	before["automatic_protection"] = autoBefore

	if in.AutomaticProtection != nil {
		if _, err := s.deps.Store.Pool.Exec(ctx, `UPDATE api_control_settings SET automatic_protection=$1, updated_at=now() WHERE id=1`, *in.AutomaticProtection); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "自动保护开关更新失败", nil)
			return
		}
	}
	if id != "" {
		if _, err := s.deps.Store.Pool.Exec(ctx, `
			UPDATE api_control_policies SET
			  warning_error_rate=coalesce($2, warning_error_rate),
			  critical_error_rate=coalesce($3, critical_error_rate),
			  warning_p95_ms=coalesce($4, warning_p95_ms),
			  critical_p95_ms=coalesce($5, critical_p95_ms),
			  cooldown_seconds=coalesce($6, cooldown_seconds),
			  updated_at=now()
			WHERE id=$1`, id, in.WarningErrorRate, in.CriticalErrorRate, in.WarningP95Ms, in.CriticalP95Ms, in.CooldownSeconds); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "控制策略更新失败", nil)
			return
		}
	}
	after := map[string]any{}
	var autoAfter bool
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT automatic_protection FROM api_control_settings WHERE id=1`).Scan(&autoAfter)
	after["automatic_protection"] = autoAfter
	if id != "" {
		var enabled bool
		var wr, cr float64
		var wp, cp, cd int
		_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT enabled, warning_error_rate, critical_error_rate, warning_p95_ms, critical_p95_ms, cooldown_seconds FROM api_control_policies WHERE id=$1`, id).
			Scan(&enabled, &wr, &cr, &wp, &cp, &cd)
		after["warning_error_rate"] = wr
		after["critical_error_rate"] = cr
		after["warning_p95_ms"] = wp
		after["critical_p95_ms"] = cp
		after["cooldown_seconds"] = cd
	}
	s.s6AuditChange(r, "admin.control.policies.update", "admin.control", "control_policy", id, before, after, map[string]any{"reason": in.Reason})
	respondOK(w, r, map[string]any{"before": before, "after": after})
}

// ---- Kill Switch（双人审批 + 恢复） ----

type s6Engagement struct {
	ID             string         `json:"id"`
	Mode           string         `json:"mode"`
	Status         string         `json:"status"`
	Reason         string         `json:"reason"`
	Scope          map[string]any `json:"scope"`
	ImpactPreview  map[string]any `json:"impact_preview"`
	ApprovalID     string         `json:"approval_id,omitempty"`
	ApprovalStatus string         `json:"approval_status,omitempty"`
	RequestedBy    string         `json:"requested_by"`
	RequestedEmail string         `json:"requested_by_email"`
	ApprovedBy     string         `json:"approved_by,omitempty"`
	ApprovedEmail  string         `json:"approved_by_email,omitempty"`
	StepUpAt       *time.Time     `json:"step_up_at,omitempty"`
	StepUpMethod   string         `json:"step_up_method"`
	EngagedAt      *time.Time     `json:"engaged_at,omitempty"`
	RestoredAt     *time.Time     `json:"restored_at,omitempty"`
	HealthCheck    map[string]any `json:"health_check,omitempty"`
	BreakGlass     bool           `json:"break_glass"`
	CreatedAt      time.Time      `json:"created_at"`
	CanApprove     bool           `json:"can_approve"`
}

const s6EngagementSelect = `SELECT e.id::text, e.mode, e.status, e.reason, coalesce(e.scope,'{}'::jsonb),
       coalesce(e.impact_preview,'{}'::jsonb),
       coalesce(e.approval_request_id::text,''), coalesce(a.status,''),
       e.requested_by::text, coalesce(ri.email,''),
       coalesce(e.approved_by::text,''), coalesce(ai.email,''),
       e.step_up_at, e.step_up_method, e.engaged_at, e.restored_at, e.health_check, e.break_glass, e.created_at
  FROM kill_switch_engagements e
  LEFT JOIN approval_requests a ON a.id = e.approval_request_id
  LEFT JOIN identities ri ON ri.id = e.requested_by
  LEFT JOIN identities ai ON ai.id = e.approved_by`

func scanS6Engagement(row pgx.Row) (s6Engagement, error) {
	var e s6Engagement
	var scope, impact, health []byte
	err := row.Scan(&e.ID, &e.Mode, &e.Status, &e.Reason, &scope, &impact,
		&e.ApprovalID, &e.ApprovalStatus, &e.RequestedBy, &e.RequestedEmail,
		&e.ApprovedBy, &e.ApprovedEmail, &e.StepUpAt, &e.StepUpMethod,
		&e.EngagedAt, &e.RestoredAt, &health, &e.BreakGlass, &e.CreatedAt)
	if err != nil {
		return s6Engagement{}, err
	}
	_ = json.Unmarshal(scope, &e.Scope)
	_ = json.Unmarshal(impact, &e.ImpactPreview)
	if len(health) > 0 {
		_ = json.Unmarshal(health, &e.HealthCheck)
	}
	return e, nil
}

// s6ActiveEngagement 返回当前真正生效的 Kill Switch。
//
// 判定必须同时满足「存在 status=active 的请求」与「控制面 kill_switch=true」：
// 只按请求表状态判断会在恢复或异常中断后留下幽灵占用，导致后续请求被错误拒绝
// （S6 验收在 ADMIN-006 复跑时真实遇到该状态）。
func (s *Server) s6ActiveEngagement(ctx context.Context) (*s6Engagement, error) {
	var killSwitch bool
	if err := s.deps.Store.Pool.QueryRow(ctx, "SELECT kill_switch FROM api_control_settings WHERE id=1").Scan(&killSwitch); err != nil {
		return nil, err
	}
	if !killSwitch {
		return nil, nil
	}
	row := s.deps.Store.Pool.QueryRow(ctx, s6EngagementSelect+
		" WHERE e.status = 'active' AND e.mode = 'engage' ORDER BY e.engaged_at DESC NULLS LAST LIMIT 1")
	e, err := scanS6Engagement(row)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// s6KillSwitchScope 返回 Kill Switch 影响范围；默认不删除任务、不清空队列、不丢弃 Checkpoint。
func (s *Server) s6KillSwitchScope(ctx context.Context) map[string]any {
	scope := map[string]any{
		"apis": true, "jobs": true, "worker_poll": true, "actions_mcp": true,
		"delete_jobs": false, "purge_queue": false, "drop_checkpoints": false,
	}
	var raw []byte
	if err := s.deps.Store.Pool.QueryRow(ctx,
		"SELECT value FROM configuration_settings WHERE key='control.kill_switch.default_scope'").Scan(&raw); err == nil {
		var stored map[string]any
		if json.Unmarshal(raw, &stored) == nil {
			for k, v := range stored {
				scope[k] = v
			}
		}
	}
	return scope
}

// s6KillSwitchImpact 计算影响预览（真实计数，不是占位）。
func (s *Server) s6KillSwitchImpact(ctx context.Context, mode string) map[string]any {
	impact := map[string]any{}
	var running, waiting, paused, runningSubjects, runningShards int
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status IN ('RUNNING','RESUMING')),
		count(*) FILTER (WHERE status IN ('CREATED','PLANNING','QUEUED','PAUSE_REQUESTED')),
		count(*) FILTER (WHERE status = 'PAUSED') FROM download_jobs`).Scan(&running, &waiting, &paused)
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM dataset_jobs WHERE status IN ('RUNNING','PENDING')`).Scan(&runningSubjects)
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM shards WHERE status IN ('RUNNING','PENDING')`).Scan(&runningShards)
	impact["running_jobs"] = running
	impact["waiting_jobs"] = waiting
	impact["paused_jobs"] = paused
	impact["running_dataset_jobs"] = runningSubjects
	impact["running_shards"] = runningShards
	impact["stopped_apis"] = []string{"/api/v2/downloads", "/api/v2/jobs", "/api/admin/v1/jobs/{id}/commands"}
	impact["kept_apis"] = []string{"/api/auth/v1/*", "/api/admin/v1/control/state", "/api/admin/v1/audit/*", "/health/*"}
	impact["paused_workers"] = mode == "engage"
	impact["actions_mcp_blocked"] = mode == "engage"
	impact["deletes_jobs"] = false
	impact["purges_queue"] = false
	impact["drops_checkpoints"] = false
	impact["keeps_checkpoints"] = true
	impact["note"] = "Kill Switch 只停止新的拉取与派发，不删除任务、不清空队列、不丢弃 Checkpoint；恢复后按原游标继续。"
	return impact
}

func (s *Server) handleAdminKillSwitchPreview(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	_ = decodeJSON(r, &in)
	if in.Mode != "restore" {
		in.Mode = "engage"
	}
	ctx := r.Context()
	impact := s.s6KillSwitchImpact(ctx, in.Mode)
	scope := s.s6KillSwitchScope(ctx)
	s.s6Audit(r, "admin.kill_switch.preview", "admin.control", "kill_switch", in.Mode, map[string]any{"impact": impact})
	respondOK(w, r, map[string]any{
		"mode": in.Mode, "scope": scope, "impact": impact,
		"requires":             []string{"step_up_password", "second_approver"},
		"step_up_method":       "password",
		"approval_ttl_seconds": 1800,
		"data_source":          "postgres:download_jobs/dataset_jobs/shards",
	})
}

type s6KillSwitchRequestInput struct {
	Mode           string `json:"mode"`
	Reason         string `json:"reason"`
	StepUpPassword string `json:"step_up_password"`
	BreakGlass     bool   `json:"break_glass"`
}

func (s *Server) handleAdminKillSwitchRequest(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in s6KillSwitchRequestInput
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if in.Mode != "restore" {
		in.Mode = "engage"
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写操作原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	scope := s.s6KillSwitchScope(ctx)
	impact := s.s6KillSwitchImpact(ctx, in.Mode)
	if in.Mode == "engage" {
		if active, err := s.s6ActiveEngagement(ctx); err == nil && active != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "已存在生效中的 Kill Switch，请先恢复", map[string]any{"engagement_id": active.ID})
			return
		}
	} else {
		active, err := s.s6ActiveEngagement(ctx)
		if err != nil || active == nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "当前没有生效中的 Kill Switch", nil)
			return
		}
	}
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.kill_switch.request") {
		return
	}
	p := PrincipalFrom(ctx)
	operation := "kill_switch.engage"
	if in.Mode == "restore" {
		operation = "kill_switch.restore"
	}
	approval, err := s.s6CreateApproval(ctx, operation, "kill_switch", in.Mode,
		map[string]any{"mode": in.Mode, "scope": scope, "impact": impact, "break_glass": in.BreakGlass},
		p.IdentityID, p.Email, 30*time.Minute)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审批请求创建失败", nil)
		return
	}
	var id string
	err = s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO kill_switch_engagements (mode, status, reason, scope, impact_preview, approval_request_id, step_up_at, step_up_method, requested_by, break_glass)
		VALUES ($1, 'pending_approval', $2, $3::jsonb, $4::jsonb, $5, now(), 'password', $6, $7)
		RETURNING id::text`, in.Mode, in.Reason, mustJSON(scope), mustJSON(impact), approval.ID, p.IdentityID, in.BreakGlass).Scan(&id)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Kill Switch 请求创建失败", nil)
		return
	}
	s.s6AuditWithApproval(r, "admin.kill_switch.request", "admin.control", "kill_switch", id, approval.ID,
		map[string]any{"mode": in.Mode, "reason": in.Reason, "impact": impact, "step_up_method": "password", "break_glass": in.BreakGlass})
	engagement, _ := s.s6LoadEngagement(ctx, id)
	respondCreated(w, r, map[string]any{"engagement": engagement, "approval": approval})
}

func (s *Server) s6LoadEngagement(ctx context.Context, id string) (s6Engagement, error) {
	if _, err := parseS6UUID(id); err != nil {
		return s6Engagement{}, pgx.ErrNoRows
	}
	return scanS6Engagement(s.deps.Store.Pool.QueryRow(ctx, s6EngagementSelect+" WHERE e.id=$1", id))
}

func (s *Server) handleAdminKillSwitchRequests(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	limit := s6Limit(r.URL.Query().Get("limit"), 20, 100)
	rows, err := s.deps.Store.Pool.Query(r.Context(), s6EngagementSelect+" ORDER BY e.created_at DESC LIMIT $1", limit)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Kill Switch 请求读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]s6Engagement, 0, limit)
	for rows.Next() {
		item, err := scanS6Engagement(rows)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Kill Switch 请求读取失败", nil)
			return
		}
		items = append(items, item)
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

func (s *Server) handleAdminKillSwitchRequestDetail(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	engagement, err := s.s6LoadEngagement(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "Kill Switch 请求不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Kill Switch 请求读取失败", nil)
		return
	}
	respondOK(w, r, engagement)
}

func (s *Server) handleAdminKillSwitchApprove(w http.ResponseWriter, r *http.Request) {
	s.s6KillSwitchDecision(w, r, "approved")
}

func (s *Server) handleAdminKillSwitchReject(w http.ResponseWriter, r *http.Request) {
	s.s6KillSwitchDecision(w, r, "rejected")
}

func (s *Server) s6KillSwitchDecision(w http.ResponseWriter, r *http.Request, decision string) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		Note string `json:"note"`
	}
	_ = decodeJSON(r, &in)
	if strings.TrimSpace(in.Note) == "" {
		validationError(w, r, "必须填写审批意见", []fieldError{{Field: "note", Code: "required", Message: "审批意见必填"}})
		return
	}
	ctx := r.Context()
	engagement, err := s.s6LoadEngagement(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "Kill Switch 请求不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Kill Switch 请求读取失败", nil)
		return
	}
	if engagement.Status != "pending_approval" {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "该请求当前状态不允许审批", map[string]any{"status": engagement.Status})
		return
	}
	p := PrincipalFrom(ctx)
	approval, err := s.s6DecideApproval(ctx, engagement.ApprovalID, decision, in.Note, p.IdentityID, p.Email)
	if errors.Is(err, errS6TwoPerson) {
		respondError(w, r, http.StatusForbidden, "TWO_PERSON_REQUIRED",
			"请求人与审批人不能是同一人，请由第二位管理员审批", map[string]any{"requested_by": engagement.RequestedEmail})
		return
	}
	if err != nil {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", err.Error(), nil)
		return
	}
	nextStatus := "approved"
	if decision == "rejected" {
		nextStatus = "rejected"
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE kill_switch_engagements SET status=$2, approved_by=$3, updated_at=now() WHERE id=$1`, id, nextStatus, p.IdentityID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审批状态更新失败", nil)
		return
	}
	s.s6AuditWithApproval(r, "admin.kill_switch."+strings.TrimSuffix(decision, "d"), "admin.control", "kill_switch", id, approval.ID,
		map[string]any{"decision": decision, "note": in.Note, "requested_by": engagement.RequestedEmail, "decided_by": p.Email})
	updated, _ := s.s6LoadEngagement(ctx, id)
	respondOK(w, r, map[string]any{"engagement": updated, "approval": approval})
}

func (s *Server) handleAdminKillSwitchExecute(w http.ResponseWriter, r *http.Request) {
	s.s6KillSwitchApply(w, r, "engage")
}

func (s *Server) handleAdminKillSwitchRestore(w http.ResponseWriter, r *http.Request) {
	s.s6KillSwitchApply(w, r, "restore")
}

// s6KillSwitchApply 执行已批准的 Kill Switch 或恢复命令，并做健康验证。
func (s *Server) s6KillSwitchApply(w http.ResponseWriter, r *http.Request, mode string) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		StepUpPassword string `json:"step_up_password"`
	}
	_ = decodeJSON(r, &in)
	ctx := r.Context()
	engagement, err := s.s6LoadEngagement(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "Kill Switch 请求不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Kill Switch 请求读取失败", nil)
		return
	}
	if engagement.Mode != mode {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "请求模式与命令不匹配", map[string]any{"mode": engagement.Mode})
		return
	}
	operation := "kill_switch.engage"
	if mode == "restore" {
		operation = "kill_switch.restore"
	}
	if _, err := s.s6RequireApproved(ctx, engagement.ApprovalID, operation); err != nil {
		respondError(w, r, http.StatusPreconditionFailed, "APPROVAL_REQUIRED", "该命令需要先由第二位管理员审批通过", map[string]any{"reason": err.Error()})
		return
	}
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.kill_switch."+mode) {
		return
	}
	engage := mode == "engage"
	if _, err := s.deps.Store.Pool.Exec(ctx, `UPDATE api_control_settings SET kill_switch=$1, background_paused=$1, updated_at=now() WHERE id=1`, engage); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "控制状态更新失败", nil)
		return
	}
	action := "kill_switch_engage"
	if !engage {
		action = "kill_switch_restore"
	}
	_, _ = s.deps.Store.Pool.Exec(ctx, `
		INSERT INTO api_control_actions (action, source, from_state, to_state, reason, detail, actor)
		VALUES ($1, 'manual', $2, $3, $4, $5::jsonb, $6)`,
		action, boolState(!engage), boolState(engage), engagement.Reason,
		mustJSON(map[string]any{"engagement_id": id, "approval_id": engagement.ApprovalID, "scope": engagement.Scope}), PrincipalFrom(ctx).Email)

	health := s.s6HealthCheck(ctx)
	status := "active"
	finished := "executed"
	if !engage {
		status = "restored"
		finished = "restored"
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE kill_switch_engagements SET status=$2, engaged_at=coalesce(engaged_at, now()), restored_at=CASE WHEN $2='restored' THEN now() ELSE restored_at END,
		       health_check=$3::jsonb, updated_at=now() WHERE id=$1`, id, status, mustJSON(health)); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Kill Switch 状态更新失败", nil)
		return
	}
	_ = s.s6FinishApproval(ctx, engagement.ApprovalID, finished, map[string]any{"engagement_id": id, "status": status, "health": health})
	s.s6AuditWithApproval(r, "admin.kill_switch."+mode, "admin.control", "kill_switch", id, engagement.ApprovalID,
		map[string]any{"status": status, "reason": engagement.Reason, "health": health})
	updated, _ := s.s6LoadEngagement(ctx, id)
	respondOK(w, r, map[string]any{"engagement": updated, "health": health, "kill_switch": engage})
}

func boolState(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// s6HealthCheck 执行恢复后的健康检查（真实探针结果，不用前端假状态）。
func (s *Server) s6HealthCheck(ctx context.Context) map[string]any {
	checks := map[string]string{}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if s.deps.Store != nil {
		if err := s.deps.Store.Pool.Ping(probeCtx); err != nil {
			checks["postgres"] = "down"
		} else {
			checks["postgres"] = "ok"
		}
	}
	if s.deps.CH != nil {
		if err := s.deps.CH.Exec(probeCtx, "SELECT 1"); err != nil {
			checks["clickhouse"] = "down"
		} else {
			checks["clickhouse"] = "ok"
		}
	}
	if s.deps.Bus != nil && s.deps.Bus.Enabled() {
		checks["nats"] = "ok"
	} else {
		checks["nats"] = "degraded"
	}
	if s.deps.Raw != nil {
		checks["minio"] = "ok"
	}
	var openAlerts int
	if s.deps.Store != nil {
		_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM api_control_alerts WHERE status='OPEN'`).Scan(&openAlerts)
	}
	healthy := true
	for _, v := range checks {
		if v != "ok" {
			healthy = false
		}
	}
	return map[string]any{
		"checked_at": s6Now().Format(time.RFC3339), "checks": checks,
		"healthy": healthy, "open_alerts": openAlerts,
	}
}

func (s *Server) handleAdminKillSwitchHealth(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	health := s.s6HealthCheck(r.Context())
	active, _ := s.s6ActiveEngagement(r.Context())
	respondOK(w, r, map[string]any{"health": health, "active_engagement": active})
}

func (s *Server) handleAdminApprovals(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	items, err := s.s6ListApprovals(r.Context(), r.URL.Query().Get("operation"), s6Limit(r.URL.Query().Get("limit"), 50, 200))
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审批请求读取失败", nil)
		return
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

// ---- 性能控制（A-PERF-01） ----

func (s *Server) handleAdminPerformance(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	ctx := r.Context()
	state, err := s.deps.Store.GetAnkrRuntimeState(ctx)
	if err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "性能运行状态读取失败", nil)
		return
	}
	profile := s.productionProfile(r)
	configured, _ := s.deps.Store.GetAnkrConfiguredMode(ctx)
	respondOK(w, r, map[string]any{
		"requested_mode":           state.RequestedMode,
		"effective_mode":           state.EffectiveMode,
		"configured_mode":          configured,
		"reason":                   state.ModeReason,
		"changed_at":               state.ModeChangedAt,
		"target_concurrency":       state.TargetConcurrency,
		"active_concurrency":       state.ActiveConcurrency,
		"hard_ceiling":             profile.HardCeiling,
		"reject_boundary":          profile.RejectBoundary,
		"memory_budget_bytes":      profile.MemoryBudgetBytes,
		"absolute_rss_limit_bytes": profile.AbsoluteRSSLimitBytes,
		"page_size_default":        profile.PageSizeDefault,
		"request_timeout_seconds":  profile.RequestTimeoutSeconds,
		"range_sharding_enabled":   profile.RangeShardingEnabled,
		"modes": map[string]int{
			"safe":  profile.Resolve(model.PerformanceSafe),
			"fast":  profile.Resolve(model.PerformanceFast),
			"turbo": profile.Resolve(model.PerformanceTurbo),
		},
		"data_source": map[string]string{
			"runtime": "postgres:ankr_performance_control", "profile": "postgres:ankr_production_profiles",
		},
	})
}

func (s *Server) handleAdminPerformancePreview(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	mode, err := model.ParsePerformanceMode(in.Mode)
	if err != nil || in.Mode == "" {
		validationError(w, r, "mode 必须是 safe、fast 或 turbo", []fieldError{{Field: "mode", Code: "invalid", Message: "取值 safe|fast|turbo"}})
		return
	}
	ctx := r.Context()
	state, _ := s.deps.Store.GetAnkrRuntimeState(ctx)
	profile := s.productionProfile(r)
	highest, hasHighest, _ := s.deps.Store.HighestActiveRequestedMode(ctx)
	effectiveReason := "manual_admin_override"
	if hasHighest && highest != "" && modeOf(highest) > modeOf(mode) {
		effectiveReason = "active_job_requires_higher_mode"
	}
	var running int
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM download_jobs WHERE status IN ('RUNNING','RESUMING')`).Scan(&running)
	respondOK(w, r, map[string]any{
		"requested_mode": mode, "current_effective_mode": state.EffectiveMode,
		"current_target_concurrency":    state.TargetConcurrency,
		"next_target_concurrency":       profile.Resolve(mode),
		"hard_ceiling":                  profile.HardCeiling,
		"reject_boundary":               profile.RejectBoundary,
		"memory_budget_bytes":           profile.MemoryBudgetBytes,
		"effective_reason_preview":      effectiveReason,
		"active_jobs":                   running,
		"highest_active_requested_mode": highest,
		"requires_approval":             modeOf(mode) >= modeOf(model.PerformanceTurbo),
		"data_source":                   "postgres:ankr_performance_control/ankr_production_profiles",
	})
}

func modeOf(m model.PerformanceMode) int {
	switch m {
	case model.PerformanceTurbo:
		return 3
	case model.PerformanceFast:
		return 2
	case model.PerformanceSafe:
		return 1
	}
	return 0
}

func (s *Server) handleAdminPerformanceCommand(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
		return
	}
	command := chi.URLParam(r, "command")
	var in struct {
		Mode           string `json:"mode"`
		Reason         string `json:"reason"`
		StepUpPassword string `json:"step_up_password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写变更原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	before, _ := s.deps.Store.GetAnkrRuntimeState(ctx)
	target := in.Mode
	if command == "rollback" {
		target = string(before.RequestedMode)
	}
	mode, err := model.ParsePerformanceMode(target)
	if err != nil || target == "" {
		validationError(w, r, "mode 必须是 safe、fast 或 turbo", []fieldError{{Field: "mode", Code: "invalid", Message: "取值 safe|fast|turbo"}})
		return
	}
	if modeOf(mode) >= modeOf(model.PerformanceTurbo) && !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.performance.turbo") {
		return
	}
	reason := "manual_admin_override"
	if command == "rollback" {
		reason = "manual_admin_rollback"
	}
	if err := s.deps.Store.SetAnkrRequestedMode(ctx, mode, reason); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "性能模式更新失败", nil)
		return
	}
	profile := s.productionProfile(r)
	after, _ := s.deps.Store.GetAnkrRuntimeState(ctx)
	s.s6AuditChange(r, "admin.performance."+command, "admin.provider", "performance_mode", "global",
		map[string]any{"requested_mode": before.RequestedMode, "effective_mode": before.EffectiveMode, "target_concurrency": before.TargetConcurrency},
		map[string]any{"requested_mode": after.RequestedMode, "effective_mode": after.EffectiveMode, "target_concurrency": after.TargetConcurrency},
		map[string]any{"reason": in.Reason, "command": command, "request_note": in.Reason})
	respondOK(w, r, map[string]any{
		"command": command, "requested_mode": after.RequestedMode, "effective_mode": after.EffectiveMode,
		"reason": after.ModeReason, "target_concurrency": after.TargetConcurrency,
		"active_concurrency": after.ActiveConcurrency, "hard_ceiling": profile.HardCeiling,
		"requested_note": in.Reason,
	})
}

// mustJSON 把 map 序列化为 JSON 文本，供 ::jsonb 参数使用。
func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
