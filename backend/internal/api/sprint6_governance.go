package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/featureflag"
)

// Sprint 6 治理面：告警生命周期、功能开关、配置版本、保留/删除/备份恢复、版本与审计导出。
// 全部挂在 /api/admin/v1，统一权限、preview/command、审计与 If-Match 并发控制。

func (s *Server) registerAdminGovernanceRoutes(r chi.Router) {
	// 告警生命周期（A-ALERT-01）。
	// §7.3：`admin.alert_center` 开关在**真实请求路径**上求值（不只是后台试算）。
	r.Get("/alerts", s.require("admin:alert:read", s.requireFlag("admin.alert_center", "admin:alert:read", s.handleAdminAlerts)))
	r.Get("/alerts/{id}", s.require("admin:alert:read", s.requireFlag("admin.alert_center", "admin:alert:read", s.handleAdminAlertDetail)))
	r.Post("/alerts/{id}/commands/{command}", s.require("admin:alert:manage", s.requireFlag("admin.alert_center", "admin:alert:manage", s.handleAdminAlertCommand)))

	// 功能开关（A-FLAG-01）
	r.Get("/feature-flags", s.require("admin:flag:read", s.handleAdminFeatureFlags))
	r.Post("/feature-flags", s.require("admin:flag:update", s.handleAdminUpsertFeatureFlag))
	r.Patch("/feature-flags/{key}", s.require("admin:flag:update", s.handleAdminUpsertFeatureFlag))
	r.Get("/feature-flags/{key}/evaluations", s.require("admin:flag:read", s.handleAdminFlagEvaluations))
	r.Post("/feature-flags/{key}/commands/{command}", s.require("admin:flag:update", s.handleAdminFlagCommand))

	// 配置与配置版本（A-CONFIG-01）
	r.Get("/settings", s.require("admin:config:read", s.handleAdminSettings))
	r.Get("/config/settings", s.require("admin:config:read", s.handleAdminSettings))
	r.Get("/config/revisions", s.require("admin:config:history:read", s.handleAdminConfigRevisions))
	r.Patch("/settings/{key}", s.require("admin:config:update", s.handleAdminUpdateSetting))
	r.Post("/settings/{key}/commands/{command}", s.require("admin:config:update", s.handleAdminSettingCommand))

	// 保留、删除、备份与恢复（A-DATA-04 / A-BACKUP-01 / S6-BE-04）
	r.Get("/retention-policies", s.require("admin:retention:read", s.handleAdminRetentionPolicies))
	r.Post("/retention-policies/preview", s.require("admin:retention:read", s.handleAdminRetentionPreview))
	r.Patch("/retention-policies/{id}", s.require("admin:retention:manage", s.handleAdminUpdateRetentionPolicy))
	r.Get("/deletion-requests", s.require("admin:deletion:read", s.handleAdminDeletionRequests))
	r.Post("/deletion-requests", s.require("admin:deletion:request", s.handleAdminCreateDeletionRequest))
	r.Post("/deletion-requests/{id}/commands/{command}", s.require("admin:deletion:approve", s.handleAdminDeletionCommand))
	r.Get("/backups", s.require("admin:backup:read", s.handleAdminBackups))
	r.Post("/backups", s.require("admin:backup:create", s.handleAdminCreateBackup))
	r.Post("/backups/{id}/commands/verify", s.require("admin:backup:read", s.handleAdminVerifyBackup))
	r.Post("/restores/preview", s.require("admin:backup:restore", s.handleAdminRestorePreview))
	r.Post("/restores", s.require("admin:backup:restore", s.handleAdminCreateRestore))
	r.Get("/restores", s.require("admin:backup:read", s.handleAdminRestores))

	// 版本与迁移（A-VERSION-01）
	r.Get("/system/version", s.require("admin:version:read", s.handleAdminSystemVersion))

	// 审计导出（A-AUDIT-03 / ADMIN-008）
	r.Post("/audit/export", s.require("admin:audit:export", s.handleAdminAuditExport))
	r.Get("/audit/exports", s.require("admin:audit:export", s.handleAdminAuditExports))
}

// ---- 告警生命周期（A-ALERT-01） ----

type s6Alert struct {
	ID              string         `json:"id"`
	Severity        string         `json:"severity"`
	Priority        string         `json:"priority"`
	Title           string         `json:"title"`
	Message         string         `json:"message"`
	Status          string         `json:"status"`
	Source          string         `json:"source"`
	SLOKey          string         `json:"slo_key"`
	Fingerprint     string         `json:"fingerprint"`
	TraceID         string         `json:"trace_id,omitempty"`
	Links           map[string]any `json:"links"`
	Labels          map[string]any `json:"labels"`
	OccurrenceCount int64          `json:"occurrence_count"`
	FirstSeenAt     time.Time      `json:"first_seen_at"`
	LastSeenAt      time.Time      `json:"last_seen_at"`
	MutedUntil      *time.Time     `json:"muted_until,omitempty"`
	ResolvedAt      *time.Time     `json:"resolved_at,omitempty"`
	AcknowledgedAt  *time.Time     `json:"acknowledged_at,omitempty"`
	AcknowledgedBy  string         `json:"acknowledged_by_email,omitempty"`
	AcknowledgeNote string         `json:"acknowledge_note,omitempty"`
	ResolvedBy      string         `json:"resolved_by_email,omitempty"`
	ResolutionNote  string         `json:"resolution_note,omitempty"`
}

const s6AlertSelect = `SELECT a.id::text, a.severity, coalesce(a.priority,''), a.title, coalesce(a.message,''), a.status,
       coalesce(a.source,''), coalesce(a.slo_key,''), coalesce(a.fingerprint,''), coalesce(a.trace_id,''),
       coalesce(a.links,'{}'::jsonb), coalesce(a.labels,'{}'::jsonb), coalesce(a.occurrence_count,1),
       a.first_seen_at, a.last_seen_at, a.muted_until, a.resolved_at, a.acknowledged_at,
       coalesce(ack.email,''), coalesce(a.acknowledge_note,''), coalesce(res.email,''), coalesce(a.resolution_note,'')
  FROM api_control_alerts a
  LEFT JOIN identities ack ON ack.id = a.acknowledged_by
  LEFT JOIN identities res ON res.id = a.resolved_by`

func scanS6Alert(row pgx.Row) (s6Alert, error) {
	var a s6Alert
	var links, labels []byte
	err := row.Scan(&a.ID, &a.Severity, &a.Priority, &a.Title, &a.Message, &a.Status, &a.Source, &a.SLOKey,
		&a.Fingerprint, &a.TraceID, &links, &labels, &a.OccurrenceCount, &a.FirstSeenAt, &a.LastSeenAt,
		&a.MutedUntil, &a.ResolvedAt, &a.AcknowledgedAt, &a.AcknowledgedBy, &a.AcknowledgeNote,
		&a.ResolvedBy, &a.ResolutionNote)
	if err != nil {
		return s6Alert{}, err
	}
	_ = json.Unmarshal(links, &a.Links)
	_ = json.Unmarshal(labels, &a.Labels)
	if a.Links == nil {
		a.Links = map[string]any{}
	}
	if a.Labels == nil {
		a.Labels = map[string]any{}
	}
	if a.Priority == "" {
		a.Priority = s6PriorityOf(a.Severity)
	}
	return a, nil
}

// s6PriorityOf 把历史 severity 值映射到 §25.4 的 P0–P3 级别。
func s6PriorityOf(severity string) string {
	switch strings.ToUpper(strings.TrimSpace(severity)) {
	case "P0", "CRITICAL":
		return "P0"
	case "P1", "HIGH", "ERROR":
		return "P1"
	case "P2", "WARNING", "WARN":
		return "P2"
	case "P3", "INFO":
		return "P3"
	}
	return "P3"
}

func (s *Server) handleAdminAlerts(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	priority := strings.TrimSpace(q.Get("priority"))
	limit := s6Limit(q.Get("limit"), 50, 200)
	rows, err := s.deps.Store.Pool.Query(r.Context(), s6AlertSelect+`
		WHERE ($1 = '' OR a.status = $1) AND ($2 = '' OR coalesce(a.priority,'') = $2 OR a.severity = $2)
		ORDER BY CASE coalesce(nullif(a.priority,''), a.severity)
		           WHEN 'P0' THEN 0 WHEN 'CRITICAL' THEN 0
		           WHEN 'P1' THEN 1 WHEN 'HIGH' THEN 1
		           WHEN 'P2' THEN 2 WHEN 'WARNING' THEN 2 ELSE 3 END,
		         a.last_seen_at DESC
		LIMIT $3`, status, priority, limit)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "告警列表读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]s6Alert, 0, limit)
	for rows.Next() {
		item, err := scanS6Alert(rows)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "告警列表读取失败", nil)
			return
		}
		items = append(items, item)
	}
	var openP0 int
	_ = s.deps.Store.Pool.QueryRow(r.Context(),
		"SELECT count(*) FROM api_control_alerts WHERE status='OPEN' AND (priority='P0' OR severity IN ('CRITICAL','P0'))").Scan(&openP0)
	respondList(w, r, map[string]any{"items": items, "open_p0": openP0}, len(items), nil)
}

func (s *Server) handleAdminAlertDetail(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "告警不存在", nil)
		return
	}
	alert, err := scanS6Alert(s.deps.Store.Pool.QueryRow(r.Context(), s6AlertSelect+" WHERE a.id=$1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "告警不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "告警读取失败", nil)
		return
	}
	// 关联链：从告警定位到 request / provider / shard / checkpoint / object（§19 验收）。
	chain := s.s6AlertRelatedChain(r.Context(), alert)
	respondOK(w, r, map[string]any{"alert": alert, "related": chain})
}

// s6AlertRelatedChain 依据 links/labels 真实回查下游对象，找不到的层显式给出原因。
func (s *Server) s6AlertRelatedChain(ctx context.Context, alert s6Alert) map[string]any {
	out := map[string]any{}
	pick := func(key string) string {
		if v, ok := alert.Links[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		if v, ok := alert.Labels[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	providerKey := pick("provider")
	if providerKey != "" {
		if p, err := s.s6LoadProvider(ctx, providerKey); err == nil {
			out["provider"] = map[string]any{"key": p.Key, "display_name": p.DisplayName, "status": p.Status, "usage": p.Usage}
		} else {
			out["provider"] = map[string]any{"key": providerKey, "found": false, "reason": "provider_descriptors 中没有该 Provider"}
		}
	}
	requestID := pick("request_id")
	if requestID == "" {
		requestID = alert.TraceID
	}
	if requestID != "" {
		row := s.deps.Store.Pool.QueryRow(ctx, `
			SELECT id::text, provider, method, coalesce(status,''), coalesce(duration_ms,0), coalesce(trace_id,''),
			       coalesce(job_id::text,''), coalesce(shard_id::text,'')
			FROM provider_usage WHERE request_id=$1 OR trace_id=$1 ORDER BY request_time DESC LIMIT 1`, requestID)
		var id string
		var provider, method, status, traceID, jobID, shardID string
		var duration int
		if err := row.Scan(&id, &provider, &method, &status, &duration, &traceID, &jobID, &shardID); err == nil {
			out["request"] = map[string]any{
				"id": id, "provider": provider, "method": method, "status": status,
				"duration_ms": duration, "trace_id": traceID,
			}
			if jobID == "" {
				jobID = pick("job_id")
			}
			if shardID == "" {
				shardID = pick("shard_id")
			}
			out["job"] = map[string]any{"id": jobID, "found": jobID != ""}
			out["shard"] = map[string]any{"id": shardID, "found": shardID != ""}
			if shardID != "" {
				var nextPageToken string
				var pageNo int
				var lastSuccess *time.Time
				if err := s.deps.Store.Pool.QueryRow(ctx, `
					SELECT coalesce(next_page_token,''), coalesce(page_no,0), last_success_at FROM checkpoints WHERE shard_id=$1`, shardID).
					Scan(&nextPageToken, &pageNo, &lastSuccess); err == nil {
					out["checkpoint"] = map[string]any{"shard_id": shardID, "next_page_token": nextPageToken, "page_no": pageNo, "last_success_at": lastSuccess}
				} else {
					out["checkpoint"] = map[string]any{"shard_id": shardID, "found": false, "reason": "该 Shard 没有 checkpoint 行"}
				}
				var objectRef string
				// 表名是 download_shards（不存在"shards"）：写错会让语句失败并把请求事务打成 aborted。
				if err := s.deps.Store.Pool.QueryRow(ctx, `SELECT coalesce(raw_object,'') FROM download_shards WHERE id=$1`, shardID).Scan(&objectRef); err == nil && objectRef != "" {
					entry := map[string]any{"object_ref": objectRef, "exists": false}
					if s.deps.Raw != nil {
						if size, err := s.deps.Raw.Stat(ctx, objectRef); err == nil {
							entry["exists"] = true
							entry["size_bytes"] = size
						}
					}
					out["object"] = entry
				}
			}
		} else {
			out["request"] = map[string]any{"id": requestID, "found": false, "reason": "provider_usage 中没有该 request_id/trace_id"}
		}
	}
	return out
}

func (s *Server) handleAdminAlertCommand(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	command := chi.URLParam(r, "command")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "告警不存在", nil)
		return
	}
	var in struct {
		Note       string `json:"note"`
		Reason     string `json:"reason"`
		TTLMinutes int    `json:"ttl_minutes"`
	}
	_ = decodeJSON(r, &in)
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = strings.TrimSpace(in.Reason)
	}
	if note == "" {
		validationError(w, r, "必须填写处置说明", []fieldError{{Field: "note", Code: "required", Message: "说明必填"}})
		return
	}
	ctx := r.Context()
	p := PrincipalFrom(ctx)
	var action string
	var tag interface{ RowsAffected() int64 }
	var err error
	switch command {
	case "acknowledge":
		tag, err = s.deps.Store.Pool.Exec(ctx, `
			UPDATE api_control_alerts SET status='ACK', acknowledged_at=now(), acknowledged_by=$2, acknowledge_note=$3, updated_at=now()
			WHERE id=$1 AND status='OPEN'`, id, s6NullUUID(p.IdentityID), note)
		action = "admin.alert.acknowledge"
	case "resolve":
		tag, err = s.deps.Store.Pool.Exec(ctx, `
			UPDATE api_control_alerts SET status='RESOLVED', resolved_at=now(), resolved_by=$2, resolution_note=$3, updated_at=now()
			WHERE id=$1 AND status IN ('OPEN','ACK','MUTED')`, id, s6NullUUID(p.IdentityID), note)
		action = "admin.alert.resolve"
	case "mute":
		ttl := in.TTLMinutes
		if ttl <= 0 || ttl > 10080 {
			ttl = 60
		}
		tag, err = s.deps.Store.Pool.Exec(ctx, `
			UPDATE api_control_alerts SET status='MUTED', muted_until=now()+($3 || ' minutes')::interval, muted_by=$2, mute_reason=$4, updated_at=now()
			WHERE id=$1 AND status IN ('OPEN','ACK')`, id, s6NullUUID(p.IdentityID), strconv.Itoa(ttl), note)
		action = "admin.alert.mute"
	default:
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "不支持的命令", map[string]any{"command": command})
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "告警状态更新失败", nil)
		return
	}
	if tag.RowsAffected() == 0 {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "告警当前状态不允许该命令", map[string]any{"command": command})
		return
	}
	alert, _ := scanS6Alert(s.deps.Store.Pool.QueryRow(ctx, s6AlertSelect+" WHERE a.id=$1", id))
	s.s6AuditChange(r, action, "admin.control", "alert", id,
		map[string]any{"status": "previous"}, map[string]any{"status": alert.Status},
		map[string]any{"note": note, "command": command, "fingerprint": alert.Fingerprint, "priority": alert.Priority})
	respondOK(w, r, alert)
}

// ---- 功能开关（A-FLAG-01） ----

type s6Flag struct {
	Key              string    `json:"key"`
	Description      string    `json:"description"`
	Environment      string    `json:"environment"`
	Enabled          bool      `json:"enabled"`
	DefaultValue     bool      `json:"default_value"`
	Percentage       int       `json:"percentage"`
	ScopeType        string    `json:"scope_type"`
	ScopeOrgID       string    `json:"scope_org_id,omitempty"`
	ScopeWorkspaceID string    `json:"scope_workspace_id,omitempty"`
	ScopeUserID      string    `json:"scope_user_id,omitempty"`
	Rules            []any     `json:"rules"`
	Revision         int64     `json:"revision"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const s6FlagSelect = `SELECT key, description, environment, enabled, default_value, percentage, scope_type,
       coalesce(scope_org_id::text,''), coalesce(scope_workspace_id::text,''), coalesce(scope_user_id::text,''),
       rules, revision, updated_at
  FROM feature_flags`

func scanS6Flag(row pgx.Row) (s6Flag, error) {
	var f s6Flag
	var rules []byte
	if err := row.Scan(&f.Key, &f.Description, &f.Environment, &f.Enabled, &f.DefaultValue, &f.Percentage,
		&f.ScopeType, &f.ScopeOrgID, &f.ScopeWorkspaceID, &f.ScopeUserID, &rules, &f.Revision, &f.UpdatedAt); err != nil {
		return s6Flag{}, err
	}
	_ = json.Unmarshal(rules, &f.Rules)
	if f.Rules == nil {
		f.Rules = []any{}
	}
	return f, nil
}

func (s *Server) handleAdminFeatureFlags(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), s6FlagSelect+" ORDER BY key")
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "功能开关读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]s6Flag, 0, 16)
	for rows.Next() {
		item, err := scanS6Flag(rows)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "功能开关读取失败", nil)
			return
		}
		items = append(items, item)
	}
	respondList(w, r, map[string]any{"items": items, "environment": s.cfg.Env}, len(items), nil)
}

type s6FlagInput struct {
	Key              string `json:"key"`
	Description      string `json:"description"`
	Environment      string `json:"environment"`
	Enabled          *bool  `json:"enabled"`
	DefaultValue     *bool  `json:"default_value"`
	Percentage       *int   `json:"percentage"`
	ScopeType        string `json:"scope_type"`
	ScopeOrgID       string `json:"scope_org_id"`
	ScopeWorkspaceID string `json:"scope_workspace_id"`
	ScopeUserID      string `json:"scope_user_id"`
	Reason           string `json:"reason"`
}

func (s *Server) handleAdminUpsertFeatureFlag(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	var in s6FlagInput
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if key == "" {
		key = strings.TrimSpace(in.Key)
	}
	if key == "" {
		validationError(w, r, "必须提供开关 key", []fieldError{{Field: "key", Code: "required", Message: "必填"}})
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写变更原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	var current s6Flag
	existing, err := scanS6Flag(s.deps.Store.Pool.QueryRow(ctx, s6FlagSelect+" WHERE key=$1", key))
	isNew := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !isNew {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "功能开关读取失败", nil)
		return
	}
	current = existing
	if !isNew {
		if _, ok := s6RequireRevision(w, r, current.Revision, "功能开关"); !ok {
			return
		}
	}
	scopeType := in.ScopeType
	if scopeType == "" {
		scopeType = current.ScopeType
	}
	if scopeType == "" {
		scopeType = "global"
	}
	switch scopeType {
	case "global", "organization", "workspace", "user":
	default:
		validationError(w, r, "scope_type 取值非法", []fieldError{{Field: "scope_type", Code: "invalid", Message: "global|organization|workspace|user"}})
		return
	}
	// S7-DB-01：作用域目标必须存在，且各作用域类型使用各自的目标列。
	// 修复前 scope_type='user' 没有任何列能承载目标用户，实现误用 scope_org_id（组织 UUID）
	// 与 subject_id 比较，用户级灰度恒不生效；这里补齐 scope_user_id 并做输入校验。
	scopeOrgID := strings.TrimSpace(in.ScopeOrgID)
	scopeWorkspaceID := strings.TrimSpace(in.ScopeWorkspaceID)
	scopeUserID := strings.TrimSpace(in.ScopeUserID)
	switch scopeType {
	case "organization":
		if scopeOrgID == "" {
			validationError(w, r, "organization 作用域必须提供 scope_org_id", []fieldError{{Field: "scope_org_id", Code: "required", Message: "必填"}})
			return
		}
	case "workspace":
		if scopeWorkspaceID == "" {
			validationError(w, r, "workspace 作用域必须提供 scope_workspace_id", []fieldError{{Field: "scope_workspace_id", Code: "required", Message: "必填"}})
			return
		}
		// 工作区作用域必须同时记录所属组织（0056 的 feature_flags_ws_implies_org_check）。
		if scopeOrgID == "" {
			if err := s.deps.Store.Pool.QueryRow(ctx, `SELECT organization_id::text FROM workspaces WHERE id=$1::uuid`, scopeWorkspaceID).Scan(&scopeOrgID); err != nil {
				validationError(w, r, "scope_workspace_id 不存在或无法解析所属组织", []fieldError{{Field: "scope_workspace_id", Code: "not_found", Message: "工作区不存在"}})
				return
			}
		}
	case "user":
		if scopeUserID == "" {
			validationError(w, r, "user 作用域必须提供 scope_user_id", []fieldError{{Field: "scope_user_id", Code: "required", Message: "必填"}})
			return
		}
	}
	if in.Percentage != nil && (*in.Percentage < 0 || *in.Percentage > 100) {
		validationError(w, r, "percentage 必须在 0-100 之间", []fieldError{{Field: "percentage", Code: "invalid", Message: "0-100"}})
		return
	}
	environment := in.Environment
	if environment == "" {
		environment = current.Environment
	}
	if environment == "" {
		environment = "all"
	}
	var after s6Flag
	if isNew {
		enabled := false
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		def := false
		if in.DefaultValue != nil {
			def = *in.DefaultValue
		}
		percentage := 0
		if in.Percentage != nil {
			percentage = *in.Percentage
		}
		if err := s.deps.Store.Pool.QueryRow(ctx, `
			INSERT INTO feature_flags (key, description, environment, enabled, default_value, percentage, scope_type, scope_org_id, scope_workspace_id, scope_user_id, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING key`,
			key, in.Description, environment, enabled, def, percentage, scopeType,
			s6NullUUID(scopeOrgID), s6NullUUID(scopeWorkspaceID), s6NullUUID(scopeUserID), s6NullUUID(PrincipalFrom(ctx).IdentityID)).Scan(&after.Key); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "功能开关创建失败", nil)
			return
		}
	} else {
		if _, err := s.deps.Store.Pool.Exec(ctx, `
			UPDATE feature_flags SET
			  description = CASE WHEN $2 = '' THEN description ELSE $2 END,
			  environment = $3,
			  enabled = coalesce($4, enabled),
			  default_value = coalesce($5, default_value),
			  percentage = coalesce($6, percentage),
			  scope_type = $7,
			  scope_org_id = $8,
			  scope_workspace_id = $9,
			  scope_user_id = $10,
			  revision = revision + 1, updated_by = $11, updated_at = now()
			WHERE key = $1`, key, in.Description, environment, in.Enabled, in.DefaultValue, in.Percentage,
			scopeType, s6NullUUID(scopeOrgID), s6NullUUID(scopeWorkspaceID), s6NullUUID(scopeUserID), s6NullUUID(PrincipalFrom(ctx).IdentityID)); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "功能开关更新失败", nil)
			return
		}
	}
	after, _ = scanS6Flag(s.deps.Store.Pool.QueryRow(ctx, s6FlagSelect+" WHERE key=$1", key))
	if isNew {
		s.invalidateFlagCache()
		s.s6Audit(r, "admin.flag.create", "admin.system", "feature_flag", key, map[string]any{"after": after, "reason": in.Reason})
	} else {
		s.invalidateFlagCache()
		s.s6AuditChange(r, "admin.flag.update", "admin.system", "feature_flag", key, current, after, map[string]any{"reason": in.Reason})
	}
	respondOK(w, r, after)
}

// s6ResolveEvalScope 推导评估记录应落库的组织/工作区作用域（S7-DB-01）。
// 规则：显式给出的值优先；否则仅当主体是真实用户 UUID 且其活跃成员关系唯一时推导；
// 推导不出来时保持为空（由 0056 的一致性约束保证"有工作区必有组织"）。
func s6ResolveEvalScope(ctx context.Context, s *Server, subjectType, subjectID, orgID, workspaceID string) (string, string) {
	if orgID != "" && workspaceID != "" {
		return orgID, workspaceID
	}
	if subjectType != "user" || subjectID == "" {
		return orgID, workspaceID
	}
	if _, err := parseS6UUID(subjectID); err != nil {
		return orgID, workspaceID
	}
	if orgID == "" {
		var derived string
		err := s.deps.Store.Pool.QueryRow(ctx, `
			SELECT (array_agg(organization_id::text))[1] FROM organization_memberships
			 WHERE identity_id=$1::uuid AND status='active'
			 HAVING count(*) = 1`, subjectID).Scan(&derived)
		if err == nil {
			orgID = derived
		}
	}
	if workspaceID == "" {
		var derived string
		err := s.deps.Store.Pool.QueryRow(ctx, `
			SELECT (array_agg(workspace_id::text))[1] FROM workspace_memberships
			 WHERE identity_id=$1::uuid AND status='active'
			 HAVING count(*) = 1`, subjectID).Scan(&derived)
		if err == nil {
			workspaceID = derived
		}
	}
	return orgID, workspaceID
}

// s6EvaluateFlag 计算开关对某个主体的取值（后台「试算」入口）。
//
// §7.3：算法只有一份 —— 它就是 internal/featureflag.Evaluate，真实请求路径用的是同一个函数。
// 这里保留薄封装只为让 api 包内的调用点与旧签名一致（试算接口的响应字段不变）。
func s6EvaluateFlag(flag s6Flag, subjectID, organizationID, workspaceID, environment string) (bool, string, int) {
	d := featureflag.Evaluate(featureflag.Flag{
		Key: flag.Key, Environment: flag.Environment, Enabled: flag.Enabled, DefaultValue: flag.DefaultValue,
		Percentage: flag.Percentage, ScopeType: flag.ScopeType, ScopeOrgID: flag.ScopeOrgID,
		ScopeWorkspaceID: flag.ScopeWorkspaceID, ScopeUserID: flag.ScopeUserID, Revision: flag.Revision,
	}, subjectID, organizationID, workspaceID, environment)
	return d.Allowed, d.Reason, d.Bucket
}

func (s *Server) handleAdminFlagEvaluations(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	limit := s6Limit(r.URL.Query().Get("limit"), 50, 200)
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT flag_key, flag_revision, environment, subject_type, subject_id, result, reason, bucket, evaluated_at
		FROM feature_flag_evaluations WHERE flag_key=$1 ORDER BY evaluated_at DESC LIMIT $2`, key, limit)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "开关评估记录读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		var flagKey, environment, subjectType, subjectID, reason string
		var revision int64
		var result bool
		var bucket int
		var evaluatedAt time.Time
		if err := rows.Scan(&flagKey, &revision, &environment, &subjectType, &subjectID, &result, &reason, &bucket, &evaluatedAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "开关评估记录读取失败", nil)
			return
		}
		items = append(items, map[string]any{
			"flag_key": flagKey, "flag_revision": revision, "environment": environment, "subject_type": subjectType,
			"subject_id": subjectID, "result": result, "reason": reason, "bucket": bucket, "evaluated_at": evaluatedAt,
		})
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

// handleAdminFlagCommand 支持 evaluate（按主体试算并落库）与 rollback（回滚到上一版本值）。
func (s *Server) handleAdminFlagCommand(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	command := chi.URLParam(r, "command")
	var in struct {
		SubjectID      string `json:"subject_id"`
		SubjectType    string `json:"subject_type"`
		OrganizationID string `json:"organization_id"`
		WorkspaceID    string `json:"workspace_id"`
		Enabled        *bool  `json:"enabled"`
		Percentage     *int   `json:"percentage"`
		Reason         string `json:"reason"`
	}
	_ = decodeJSON(r, &in)
	ctx := r.Context()
	flag, err := scanS6Flag(s.deps.Store.Pool.QueryRow(ctx, s6FlagSelect+" WHERE key=$1", key))
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "功能开关不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "功能开关读取失败", nil)
		return
	}
	switch command {
	case "evaluate":
		subjectType := in.SubjectType
		if subjectType == "" {
			subjectType = "user"
		}
		// S7-DB-01：评估记录必须落到可核对的作用域，否则无法证明"只影响目标 scope"。
		// 调用方未显式给出时，按主体用户的活跃成员关系推导（唯一才推导，不猜测、不编造）。
		evalOrgID, evalWorkspaceID := s6ResolveEvalScope(ctx, s, subjectType, strings.TrimSpace(in.SubjectID),
			strings.TrimSpace(in.OrganizationID), strings.TrimSpace(in.WorkspaceID))
		result, reason, bucket := s6EvaluateFlag(flag, in.SubjectID, evalOrgID, evalWorkspaceID, s.cfg.Env)
		if _, err := s.deps.Store.Pool.Exec(ctx, `
			INSERT INTO feature_flag_evaluations (flag_key, flag_revision, environment, subject_type, subject_id, organization_id, workspace_id, result, reason, bucket)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			flag.Key, flag.Revision, s.cfg.Env, subjectType, in.SubjectID,
			s6NullUUID(evalOrgID), s6NullUUID(evalWorkspaceID), result, reason, bucket); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "开关评估失败", nil)
			return
		}
		s.s6Audit(r, "admin.flag.evaluate", "admin.system", "feature_flag", flag.Key, map[string]any{
			"subject_id": in.SubjectID, "result": result, "reason": reason, "bucket": bucket, "revision": flag.Revision,
		})
		respondOK(w, r, map[string]any{
			"key": flag.Key, "revision": flag.Revision, "subject_id": in.SubjectID,
			"result": result, "reason": reason, "bucket": bucket, "percentage": flag.Percentage,
		})
	case "rollback":
		if strings.TrimSpace(in.Reason) == "" {
			validationError(w, r, "必须填写回滚原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
			return
		}
		enabled := flag.DefaultValue
		percentage := 0
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		if in.Percentage != nil {
			percentage = *in.Percentage
		}
		if _, err := s.deps.Store.Pool.Exec(ctx, `
			UPDATE feature_flags SET enabled=$2, percentage=$3, revision=revision+1, updated_by=$4, updated_at=now()
			WHERE key=$1`, key, enabled, percentage, s6NullUUID(PrincipalFrom(ctx).IdentityID)); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "开关回滚失败", nil)
			return
		}
		after, _ := scanS6Flag(s.deps.Store.Pool.QueryRow(ctx, s6FlagSelect+" WHERE key=$1", key))
		s.invalidateFlagCache()
		s.s6AuditChange(r, "admin.flag.rollback", "admin.system", "feature_flag", key, flag, after, map[string]any{"reason": in.Reason})
		respondOK(w, r, after)
	default:
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "不支持的命令", map[string]any{"command": command})
	}
}

// ---- 配置项与配置版本（A-CONFIG-01） ----

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT key FROM configuration_settings WHERE ($1 = '' OR category = $1) ORDER BY category, key`, category)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置读取失败", nil)
		return
	}
	keys := make([]string, 0, 32)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置读取失败", nil)
			return
		}
		keys = append(keys, key)
	}
	rows.Close()
	items := make([]s6Setting, 0, len(keys))
	for _, key := range keys {
		setting, err := s.s6LoadSetting(r.Context(), key)
		if err != nil {
			continue
		}
		items = append(items, setting)
	}
	respondList(w, r, map[string]any{"items": items, "environment": s.cfg.Env}, len(items), nil)
}

func (s *Server) handleAdminConfigRevisions(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	limit := s6Limit(r.URL.Query().Get("limit"), 100, 500)
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, key, revision, value, secret_ref, is_secret, before_hash, after_hash, change_note,
		       coalesce(actor_email,''), request_id, trace_id, coalesce(approval_request_id::text,''), created_at
		FROM configuration_revisions WHERE ($1 = '' OR key = $1) ORDER BY created_at DESC, revision DESC LIMIT $2`, key, limit)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置历史读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		var id, settingKey, secretRef, beforeHash, afterHash, note, actor, requestID, traceID, approvalID string
		var revision int64
		var value []byte
		var isSecret bool
		var createdAt time.Time
		if err := rows.Scan(&id, &settingKey, &revision, &value, &secretRef, &isSecret, &beforeHash, &afterHash,
			&note, &actor, &requestID, &traceID, &approvalID, &createdAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置历史读取失败", nil)
			return
		}
		var parsed any
		_ = json.Unmarshal(value, &parsed)
		if isSecret {
			parsed = nil
			secretRef = s6MaskRef(secretRef)
		}
		items = append(items, map[string]any{
			"id": id, "key": settingKey, "revision": revision, "value": parsed, "secret_ref": secretRef,
			"is_secret": isSecret, "before_hash": beforeHash, "after_hash": afterHash, "change_note": note,
			"actor_email": actor, "request_id": requestID, "trace_id": traceID, "approval_id": approvalID,
			"created_at": createdAt,
		})
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

type s6SettingUpdate struct {
	Value          any    `json:"value"`
	SecretRef      string `json:"secret_ref"`
	Reason         string `json:"reason"`
	StepUpPassword string `json:"step_up_password"`
}

func (s *Server) handleAdminUpdateSetting(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	var in s6SettingUpdate
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写修改原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	before, err := s.s6LoadSetting(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "配置项不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置读取失败", nil)
		return
	}
	if _, ok := s6RequireRevision(w, r, before.Revision, "配置项"); !ok {
		return
	}
	if before.IsSecret || before.ValueType == "secret-ref" {
		if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.config.update_secret") {
			return
		}
		if strings.TrimSpace(in.SecretRef) == "" {
			validationError(w, r, "秘密配置只接受 secret_ref 引用，不接受明文", []fieldError{{Field: "secret_ref", Code: "required", Message: "必填"}})
			return
		}
	} else if before.ValueType == "bool" {
		if _, ok := in.Value.(bool); !ok {
			validationError(w, r, "该配置项为布尔类型", []fieldError{{Field: "value", Code: "invalid", Message: "需要 true/false"}})
			return
		}
	} else if before.ValueType == "int" {
		if _, ok := in.Value.(float64); !ok {
			validationError(w, r, "该配置项为整数类型", []fieldError{{Field: "value", Code: "invalid", Message: "需要整数"}})
			return
		}
	}
	newValue := in.Value
	if before.IsSecret || before.ValueType == "secret-ref" {
		newValue = nil
	}
	afterHash := s6HashValue(newValue)
	if before.IsSecret || before.ValueType == "secret-ref" {
		afterHash = s6HashValue(in.SecretRef)
	}
	beforeHash := beforeHashOf(before)
	tx, err := s.deps.Store.Pool.Begin(ctx)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置更新失败", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	body, _ := json.Marshal(newValue)
	if _, err := tx.Exec(ctx, `
		UPDATE configuration_settings SET value=$2::jsonb, secret_ref=$3, revision=revision+1, updated_by=$4, updated_at=now()
		WHERE key=$1`, key, string(body), in.SecretRef, s6NullUUID(PrincipalFrom(ctx).IdentityID)); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置更新失败", nil)
		return
	}
	meta := metaFrom(ctx)
	p := PrincipalFrom(ctx)
	if err := s.s6WriteConfigRevision(ctx, key, before.Revision+1, newValue, in.SecretRef,
		before.IsSecret || before.ValueType == "secret-ref", beforeHash, afterHash, in.Reason,
		p.IdentityID, p.Email, meta.RequestID, meta.TraceID, ""); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置版本写入失败", nil)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置更新失败", nil)
		return
	}
	after, _ := s.s6LoadSetting(ctx, key)
	s.s6AuditChange(r, "admin.config.update", "admin.system", "configuration_setting", key,
		map[string]any{"revision": before.Revision, "value": before.Value, "secret_ref": before.SecretRef},
		map[string]any{"revision": after.Revision, "value": after.Value, "secret_ref": after.SecretRef, "after_hash": afterHash},
		map[string]any{"reason": in.Reason})
	respondOK(w, r, after)
}

func beforeHashOf(setting s6Setting) string {
	if setting.IsSecret || setting.ValueType == "secret-ref" {
		return s6HashValue(setting.SecretRef)
	}
	return s6HashValue(setting.Value)
}

// handleAdminSettingCommand 支持 diff 与 rollback（回滚到指定 revision）。
func (s *Server) handleAdminSettingCommand(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	command := chi.URLParam(r, "command")
	var in struct {
		Revision       int64  `json:"revision"`
		Reason         string `json:"reason"`
		StepUpPassword string `json:"step_up_password"`
	}
	_ = decodeJSON(r, &in)
	ctx := r.Context()
	current, err := s.s6LoadSetting(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "配置项不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置读取失败", nil)
		return
	}
	switch command {
	case "diff":
		rows, err := s.deps.Store.Pool.Query(ctx, `
			SELECT revision, value, secret_ref, is_secret, before_hash, after_hash, change_note, coalesce(actor_email,''), created_at
			FROM configuration_revisions WHERE key=$1 ORDER BY revision DESC LIMIT 20`, key)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置历史读取失败", nil)
			return
		}
		defer rows.Close()
		revisions := make([]map[string]any, 0, 20)
		for rows.Next() {
			var revision int64
			var value []byte
			var secretRef, beforeHash, afterHash, note, actor string
			var isSecret bool
			var createdAt time.Time
			if err := rows.Scan(&revision, &value, &secretRef, &isSecret, &beforeHash, &afterHash, &note, &actor, &createdAt); err != nil {
				respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置历史读取失败", nil)
				return
			}
			var parsed any
			_ = json.Unmarshal(value, &parsed)
			if isSecret {
				parsed = nil
				secretRef = s6MaskRef(secretRef)
			}
			revisions = append(revisions, map[string]any{
				"revision": revision, "value": parsed, "secret_ref": secretRef, "is_secret": isSecret,
				"before_hash": beforeHash, "after_hash": afterHash, "change_note": note,
				"actor_email": actor, "created_at": createdAt,
			})
		}
		respondOK(w, r, map[string]any{"key": key, "current": current, "revisions": revisions})
	case "rollback":
		if in.Revision <= 0 {
			validationError(w, r, "必须指定要回滚到的 revision", []fieldError{{Field: "revision", Code: "required", Message: "必填"}})
			return
		}
		if strings.TrimSpace(in.Reason) == "" {
			validationError(w, r, "必须填写回滚原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
			return
		}
		var value []byte
		var secretRef string
		var isSecret bool
		err := s.deps.Store.Pool.QueryRow(ctx, `
			SELECT value, secret_ref, is_secret FROM configuration_revisions WHERE key=$1 AND revision=$2`, key, in.Revision).
			Scan(&value, &secretRef, &isSecret)
		if errors.Is(err, pgx.ErrNoRows) {
			respondError(w, r, http.StatusNotFound, "NOT_FOUND", "指定的配置版本不存在", nil)
			return
		}
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置版本读取失败", nil)
			return
		}
		if isSecret {
			if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.config.rollback_secret") {
				return
			}
		}
		var parsed any
		_ = json.Unmarshal(value, &parsed)
		body, _ := json.Marshal(parsed)
		if _, err := s.deps.Store.Pool.Exec(ctx, `
			UPDATE configuration_settings SET value=$2::jsonb, secret_ref=$3, revision=revision+1, updated_by=$4, updated_at=now()
			WHERE key=$1`, key, string(body), secretRef, s6NullUUID(PrincipalFrom(ctx).IdentityID)); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "配置回滚失败", nil)
			return
		}
		meta := metaFrom(ctx)
		p := PrincipalFrom(ctx)
		_ = s.s6WriteConfigRevision(ctx, key, current.Revision+1, parsed, secretRef, isSecret,
			beforeHashOf(current), s6HashValue(parsed), "rollback to revision "+strconv.FormatInt(in.Revision, 10)+": "+in.Reason,
			p.IdentityID, p.Email, meta.RequestID, meta.TraceID, "")
		after, _ := s.s6LoadSetting(ctx, key)
		s.s6AuditChange(r, "admin.config.rollback", "admin.system", "configuration_setting", key, current, after,
			map[string]any{"reason": in.Reason, "target_revision": in.Revision})
		respondOK(w, r, after)
	default:
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "不支持的命令", map[string]any{"command": command})
	}
}

// ---- 保留策略（A-DATA-04） ----

func (s *Server) handleAdminRetentionPolicies(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, policy_key, description, resource_type, scope_type, retain_days, archive_after_days,
		       action, grace_days, legal_hold, enabled, last_run_at, next_run_at, revision, updated_at
		FROM retention_policies ORDER BY policy_key`)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "保留策略读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, 8)
	for rows.Next() {
		var id, policyKey, description, resourceType, scopeType, action string
		var retainDays, graceDays int
		var archiveAfter *int
		var legalHold, enabled bool
		var lastRun, nextRun *time.Time
		var revision int64
		var updatedAt time.Time
		if err := rows.Scan(&id, &policyKey, &description, &resourceType, &scopeType, &retainDays, &archiveAfter,
			&action, &graceDays, &legalHold, &enabled, &lastRun, &nextRun, &revision, &updatedAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "保留策略读取失败", nil)
			return
		}
		items = append(items, map[string]any{
			"id": id, "policy_key": policyKey, "description": description, "resource_type": resourceType,
			"scope_type": scopeType, "retain_days": retainDays, "archive_after_days": archiveAfter,
			"action": action, "grace_days": graceDays, "legal_hold": legalHold, "enabled": enabled,
			"last_run_at": lastRun, "next_run_at": nextRun, "revision": revision, "updated_at": updatedAt,
		})
	}
	executionEnabled := s.s6BoolSetting(r.Context(), "governance.deletion.execution_enabled", false)
	respondList(w, r, map[string]any{"items": items, "deletion_execution_enabled": executionEnabled,
		"note": "实际删除默认关闭；preview 不修改任何对象"}, len(items), nil)
}

func (s *Server) handleAdminUpdateRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "保留策略不存在", nil)
		return
	}
	var in struct {
		RetainDays *int    `json:"retain_days"`
		Action     *string `json:"action"`
		GraceDays  *int    `json:"grace_days"`
		Enabled    *bool   `json:"enabled"`
		LegalHold  *bool   `json:"legal_hold"`
		Reason     string  `json:"reason"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写修改原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	if in.Action != nil {
		switch *in.Action {
		case "archive", "delete", "none":
		default:
			validationError(w, r, "action 取值非法", []fieldError{{Field: "action", Code: "invalid", Message: "archive|delete|none"}})
			return
		}
	}
	ctx := r.Context()
	before := map[string]any{}
	var revision int64
	var policyKey, resourceType, action string
	var retainDays, graceDays int
	var legalHold, enabled bool
	err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT policy_key, resource_type, retain_days, action, grace_days, legal_hold, enabled, revision
		FROM retention_policies WHERE id=$1`, id).
		Scan(&policyKey, &resourceType, &retainDays, &action, &graceDays, &legalHold, &enabled, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "保留策略不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "保留策略读取失败", nil)
		return
	}
	if _, ok := s6RequireRevision(w, r, revision, "保留策略"); !ok {
		return
	}
	before = map[string]any{"policy_key": policyKey, "resource_type": resourceType, "retain_days": retainDays,
		"action": action, "grace_days": graceDays, "legal_hold": legalHold, "enabled": enabled, "revision": revision}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE retention_policies SET
		  retain_days = coalesce($2, retain_days),
		  action = coalesce($3, action),
		  grace_days = coalesce($4, grace_days),
		  enabled = coalesce($5, enabled),
		  legal_hold = coalesce($6, legal_hold),
		  revision = revision + 1, updated_by = $7, updated_at = now()
		WHERE id=$1`, id, in.RetainDays, in.Action, in.GraceDays, in.Enabled, in.LegalHold,
		s6NullUUID(PrincipalFrom(ctx).IdentityID)); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "保留策略更新失败", nil)
		return
	}
	after := map[string]any{"policy_key": policyKey, "retain_days": retainDays, "action": action,
		"grace_days": graceDays, "legal_hold": legalHold, "enabled": enabled, "revision": revision + 1}
	if in.RetainDays != nil {
		after["retain_days"] = *in.RetainDays
	}
	if in.Action != nil {
		after["action"] = *in.Action
	}
	if in.GraceDays != nil {
		after["grace_days"] = *in.GraceDays
	}
	if in.Enabled != nil {
		after["enabled"] = *in.Enabled
	}
	if in.LegalHold != nil {
		after["legal_hold"] = *in.LegalHold
	}
	s.s6AuditChange(r, "admin.retention.update", "admin.data", "retention_policy", policyKey, before, after, map[string]any{"reason": in.Reason})
	respondOK(w, r, map[string]any{"id": id, "before": before, "after": after})
}

// handleAdminRetentionPreview 只做统计与影响预览，绝不修改任何对象（§21.6：修改需 preview）。
func (s *Server) handleAdminRetentionPreview(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		PolicyKey    string `json:"policy_key"`
		ResourceType string `json:"resource_type"`
		RetainDays   *int   `json:"retain_days"`
	}
	_ = decodeJSON(r, &in)
	ctx := r.Context()
	preview, err := s.s6RetentionPreview(ctx, in.PolicyKey, in.ResourceType, in.RetainDays)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "保留影响预览失败", nil)
		return
	}
	meta := metaFrom(ctx)
	p := PrincipalFrom(ctx)
	runID := ""
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO retention_runs (policy_key, mode, status, dry_run, scanned_objects, matched_objects, matched_bytes, sample, impact, requested_by, requested_by_email, started_at, finished_at)
		VALUES ($1,'preview','preview',true,$2,$3,$4,$5::jsonb,$6::jsonb,$7,$8,now(),now()) RETURNING id::text`,
		preview["policy_key"], preview["scanned_objects"], preview["matched_objects"], preview["matched_bytes"],
		mustJSON(preview["sample"]), mustJSON(preview), s6NullUUID(p.IdentityID), p.Email).Scan(&runID)
	s.s6Audit(r, "admin.retention.preview", "admin.data", "retention_run", runID, map[string]any{
		"policy_key": preview["policy_key"], "matched_objects": preview["matched_objects"],
		"matched_bytes": preview["matched_bytes"], "request_id": meta.RequestID, "trace_id": meta.TraceID,
	})
	respondOK(w, r, map[string]any{"preview": preview, "run_id": runID, "dry_run": true})
}

// s6RetentionPreview 依据策略对真实数据源做只读统计。
func (s *Server) s6RetentionPreview(ctx context.Context, policyKey, resourceType string, retainDays *int) (map[string]any, error) {
	if policyKey == "" && resourceType == "" {
		return nil, errors.New("policy_key 或 resource_type 至少提供一个")
	}
	preview := map[string]any{"policy_key": policyKey, "resource_type": resourceType, "sample": []any{}, "block_reason": ""}
	days := 0
	if policyKey != "" {
		var storedDays int
		var storedAction, storedResource string
		var enabled, legalHold bool
		err := s.deps.Store.Pool.QueryRow(ctx, `
			SELECT retain_days, action, resource_type, enabled, legal_hold FROM retention_policies WHERE policy_key=$1`, policyKey).
			Scan(&storedDays, &storedAction, &storedResource, &enabled, &legalHold)
		if err != nil {
			return nil, err
		}
		days = storedDays
		preview["resource_type"] = storedResource
		preview["action"] = storedAction
		preview["policy_enabled"] = enabled
		preview["legal_hold"] = legalHold
		if legalHold {
			preview["block_reason"] = "该策略处于法务保留状态，任何删除都会被拒绝"
		}
	}
	if retainDays != nil {
		days = *retainDays
	}
	preview["retain_days"] = days
	resType := fmt.Sprint(preview["resource_type"])
	if resourceType != "" {
		resType = resourceType
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	switch resType {
	case "raw_object":
		var count int64
		var bytes int64
		// 原始对象的权威清单在 download_shards.raw_object（download_shards 才是真实表名）。
		// 这里曾经先查一张不存在的 raw_objects 表：语句失败 → 请求事务 aborted → 提交门
		// 把整个预览改写成 409，页面上永远拿不到保留/删除候选项（2026-09-16 定位）。
		if err := s.deps.Store.Pool.QueryRow(ctx, `
			SELECT count(*), coalesce(sum(coalesce(downloaded_bytes,0)),0) FROM download_shards
			WHERE raw_object IS NOT NULL AND raw_object <> '' AND updated_at < $1`, cutoff).Scan(&count, &bytes); err != nil {
			// 统计失败必须返回错误：保留预览是删除动作的前置依据，
			// 用 0 当"没有可删对象"会误导运维（且失败本身已经把事务打成 aborted）。
			return nil, fmt.Errorf("raw object retention scan: %w", err)
		}
		preview["scanned_objects"] = count
		preview["matched_objects"] = count
		preview["matched_bytes"] = bytes
		preview["source"] = "postgres:download_shards.raw_object"
	case "export_object":
		var count int64
		var bytes int64
		_ = s.deps.Store.Pool.QueryRow(ctx, `
			SELECT count(*), coalesce(sum(coalesce(size_bytes,0)),0) FROM exports WHERE created_at < $1`, cutoff).Scan(&count, &bytes)
		preview["scanned_objects"] = count
		preview["matched_objects"] = count
		preview["matched_bytes"] = bytes
		preview["source"] = "postgres:exports"
	case "user_activity":
		var count int64
		_ = s.deps.Store.Pool.QueryRow(ctx, `
			SELECT count(*) FROM user_activity_events WHERE occurred_at < $1`, cutoff).Scan(&count)
		preview["scanned_objects"] = count
		preview["matched_objects"] = count
		preview["source"] = "postgres:user_activity_events"
	case "audit_archive":
		var count int64
		var bytes int64
		_ = s.deps.Store.Pool.QueryRow(ctx, `
			SELECT count(*), coalesce(sum(event_count),0) FROM audit_chain_anchors WHERE anchor_date < $1::date`, cutoff).Scan(&count, &bytes)
		preview["scanned_objects"] = count
		preview["matched_objects"] = count
		preview["matched_bytes"] = bytes
		preview["block_reason"] = "审计归档不可删除；只允许转冷存储"
		preview["source"] = "postgres:audit_chain_anchors"
	case "clickhouse_partition":
		var rows int64
		_ = s.deps.Store.Pool.QueryRow(ctx, `
			SELECT coalesce(sum(rows),0) FROM dataset_coverage WHERE updated_at < $1`, cutoff).Scan(&rows)
		preview["scanned_objects"] = 0
		preview["matched_objects"] = 0
		preview["matched_bytes"] = 0
		preview["matched_rows"] = rows
		preview["source"] = "postgres:dataset_coverage"
	default:
		preview["scanned_objects"] = 0
		preview["matched_objects"] = 0
		preview["matched_bytes"] = 0
		preview["block_reason"] = "未知资源类型，未执行统计"
	}
	preview["cutoff"] = cutoff.UTC().Format(time.RFC3339)
	preview["deletion_execution_enabled"] = s.s6BoolSetting(ctx, "governance.deletion.execution_enabled", false)
	return preview, nil
}

// ---- 删除请求（S6-BE-04：实际删除默认关闭） ----

func (s *Server) handleAdminDeletionRequests(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, resource_type, coalesce(policy_id::text,''), target_scope, reason, status, dry_run, execution_enabled,
		       matched_objects, matched_bytes, preview, coalesce(approval_request_id::text,''), coalesce(requested_by_email,''),
		       restore_window_until, executed_at, result, created_at, updated_at
		FROM deletion_requests ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "删除请求读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, 16)
	for rows.Next() {
		var id, resourceType, policyID, reason, status, approvalID, requestedBy string
		var dryRun, executionEnabled bool
		var matchedObjects, matchedBytes int64
		var targetScope, preview, result []byte
		var restoreUntil, executedAt *time.Time
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &resourceType, &policyID, &targetScope, &reason, &status, &dryRun, &executionEnabled,
			&matchedObjects, &matchedBytes, &preview, &approvalID, &requestedBy, &restoreUntil, &executedAt, &result,
			&createdAt, &updatedAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "删除请求读取失败", nil)
			return
		}
		var scopeParsed, previewParsed, resultParsed any
		_ = json.Unmarshal(targetScope, &scopeParsed)
		_ = json.Unmarshal(preview, &previewParsed)
		_ = json.Unmarshal(result, &resultParsed)
		items = append(items, map[string]any{
			"id": id, "resource_type": resourceType, "policy_id": policyID, "target_scope": scopeParsed,
			"reason": reason, "status": status, "dry_run": dryRun, "execution_enabled": executionEnabled,
			"matched_objects": matchedObjects, "matched_bytes": matchedBytes, "preview": previewParsed,
			"approval_id": approvalID, "requested_by_email": requestedBy, "restore_window_until": restoreUntil,
			"executed_at": executedAt, "result": resultParsed, "created_at": createdAt, "updated_at": updatedAt,
		})
	}
	respondList(w, r, map[string]any{"items": items,
		"deletion_execution_enabled": s.s6BoolSetting(r.Context(), "governance.deletion.execution_enabled", false)}, len(items), nil)
}

func (s *Server) handleAdminCreateDeletionRequest(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		ResourceType   string `json:"resource_type"`
		PolicyKey      string `json:"policy_key"`
		Reason         string `json:"reason"`
		StepUpPassword string `json:"step_up_password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写删除原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	preview, err := s.s6RetentionPreview(ctx, in.PolicyKey, in.ResourceType, nil)
	if err != nil {
		validationError(w, r, "无法计算删除影响："+err.Error(), nil)
		return
	}
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.deletion.request") {
		return
	}
	p := PrincipalFrom(ctx)
	approval, err := s.s6CreateApproval(ctx, "data.deletion", "deletion_request", in.ResourceType,
		map[string]any{"resource_type": in.ResourceType, "policy_key": in.PolicyKey, "preview": preview},
		p.IdentityID, p.Email, 4*time.Hour)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "删除审批请求创建失败", nil)
		return
	}
	executionEnabled := s.s6BoolSetting(ctx, "governance.deletion.execution_enabled", false)
	var id string
	status := "pending_approval"
	if !executionEnabled {
		status = "blocked_disabled"
	}
	var policyID any
	if in.PolicyKey != "" {
		_ = s.deps.Store.Pool.QueryRow(ctx, "SELECT id FROM retention_policies WHERE policy_key=$1", in.PolicyKey).Scan(&policyID)
	}
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO deletion_requests (resource_type, policy_id, target_scope, reason, status, dry_run, execution_enabled,
		       matched_objects, matched_bytes, preview, approval_request_id, requested_by, requested_by_email, restore_window_until)
		VALUES ($1,$2,$3::jsonb,$4,$5,true,$6,$7,$8,$9::jsonb,$10,$11,$12, now() + interval '7 days')
		RETURNING id::text`, in.ResourceType, policyID, mustJSON(map[string]any{"policy_key": in.PolicyKey}), in.Reason, status,
		executionEnabled, asInt64(preview["matched_objects"]), asInt64(preview["matched_bytes"]), mustJSON(preview),
		approval.ID, s6NullUUID(p.IdentityID), p.Email).Scan(&id); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "删除请求创建失败", nil)
		return
	}
	s.s6AuditWithApproval(r, "admin.deletion.request", "admin.data", "deletion_request", id, approval.ID, map[string]any{
		"resource_type": in.ResourceType, "policy_key": in.PolicyKey, "reason": in.Reason,
		"preview": preview, "execution_enabled": executionEnabled, "status": status,
	})
	respondCreated(w, r, map[string]any{
		"id": id, "status": status, "approval": approval, "preview": preview,
		"execution_enabled": executionEnabled,
		"note":              "实际删除在专项验收前保持关闭；本请求只登记影响预览与审批链。",
	})
}

func (s *Server) handleAdminDeletionCommand(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	command := chi.URLParam(r, "command")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "删除请求不存在", nil)
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	_ = decodeJSON(r, &in)
	if strings.TrimSpace(in.Note) == "" {
		validationError(w, r, "必须填写处置说明", []fieldError{{Field: "note", Code: "required", Message: "说明必填"}})
		return
	}
	ctx := r.Context()
	var status, approvalID, resourceType string
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT status, coalesce(approval_request_id::text,''), resource_type FROM deletion_requests WHERE id=$1`, id).
		Scan(&status, &approvalID, &resourceType); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "删除请求不存在", nil)
		return
	}
	executionEnabled := s.s6BoolSetting(ctx, "governance.deletion.execution_enabled", false)
	p := PrincipalFrom(ctx)
	switch command {
	case "approve":
		if status != "pending_approval" {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "该请求当前状态不允许审批", map[string]any{"status": status})
			return
		}
		approval, err := s.s6DecideApproval(ctx, approvalID, "approved", in.Note, p.IdentityID, p.Email)
		if errors.Is(err, errS6TwoPerson) {
			respondError(w, r, http.StatusForbidden, "TWO_PERSON_REQUIRED", "请求人与审批人不能是同一人", nil)
			return
		}
		if err != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", err.Error(), nil)
			return
		}
		if _, err := s.deps.Store.Pool.Exec(ctx, `
			UPDATE deletion_requests SET status='approved', approved_by=$2, updated_at=now() WHERE id=$1`, id, s6NullUUID(p.IdentityID)); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "删除请求状态更新失败", nil)
			return
		}
		s.s6AuditWithApproval(r, "admin.deletion.approve", "admin.data", "deletion_request", id, approvalID,
			map[string]any{"note": in.Note, "resource_type": resourceType, "execution_enabled": executionEnabled})
		respondOK(w, r, map[string]any{"id": id, "status": "approved", "approval": approval, "execution_enabled": executionEnabled,
			"note": "审批通过只授权流程；实际删除仍受 governance.deletion.execution_enabled 控制。"})
	case "reject":
		approval, err := s.s6DecideApproval(ctx, approvalID, "rejected", in.Note, p.IdentityID, p.Email)
		if errors.Is(err, errS6TwoPerson) {
			respondError(w, r, http.StatusForbidden, "TWO_PERSON_REQUIRED", "请求人与审批人不能是同一人", nil)
			return
		}
		if err != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", err.Error(), nil)
			return
		}
		_, _ = s.deps.Store.Pool.Exec(ctx, `UPDATE deletion_requests SET status='rejected', approved_by=$2, updated_at=now() WHERE id=$1`, id, s6NullUUID(p.IdentityID))
		s.s6AuditWithApproval(r, "admin.deletion.reject", "admin.data", "deletion_request", id, approvalID, map[string]any{"note": in.Note})
		respondOK(w, r, map[string]any{"id": id, "status": "rejected", "approval": approval})
	case "cancel":
		_, _ = s.deps.Store.Pool.Exec(ctx, `UPDATE deletion_requests SET status='cancelled', updated_at=now() WHERE id=$1`, id)
		_ = s.s6FinishApproval(ctx, approvalID, "cancelled", map[string]any{"note": in.Note})
		s.s6Audit(r, "admin.deletion.cancel", "admin.data", "deletion_request", id, map[string]any{"note": in.Note})
		respondOK(w, r, map[string]any{"id": id, "status": "cancelled"})
	case "execute":
		if !executionEnabled {
			respondError(w, r, http.StatusPreconditionFailed, "DELETION_DISABLED",
				"实际删除当前处于关闭状态（governance.deletion.execution_enabled=false），专项验收前不会执行",
				map[string]any{"execution_enabled": false})
			return
		}
		respondError(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED",
			"实际删除执行需专项验收后开放", map[string]any{"feature": "deletion.execute"})
	default:
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "不支持的命令", map[string]any{"command": command})
	}
}

// ---- 备份 / 恢复（A-BACKUP-01） ----

func (s *Server) handleAdminBackups(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, kind, target, status, drill, coalesce(requested_by_email,''), coalesce(approval_request_id::text,''),
		       coalesce(backup_id::text,''), started_at, finished_at, size_bytes, coalesce(object_ref,''), coalesce(checksum,''),
		       coalesce(schema_version,''), coalesce(build_sha,''), coalesce(verify_status,''), verify_detail,
		       rpo_seconds, rto_seconds, coalesce(maintenance_window,''), coalesce(notes,''), result, created_at
		FROM backup_jobs ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "备份列表读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, 16)
	for rows.Next() {
		var id, kind, target, status, requestedBy, approvalID, backupID, objectRef, checksum, schemaVersion, buildSHA, verifyStatus, maintenanceWindow, notes string
		var drill bool
		var startedAt, finishedAt *time.Time
		var sizeBytes int64
		var rpo, rto *int64
		var verifyDetail, result []byte
		var createdAt time.Time
		if err := rows.Scan(&id, &kind, &target, &status, &drill, &requestedBy, &approvalID, &backupID, &startedAt,
			&finishedAt, &sizeBytes, &objectRef, &checksum, &schemaVersion, &buildSHA, &verifyStatus, &verifyDetail,
			&rpo, &rto, &maintenanceWindow, &notes, &result, &createdAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "备份列表读取失败", nil)
			return
		}
		var verifyParsed, resultParsed any
		_ = json.Unmarshal(verifyDetail, &verifyParsed)
		_ = json.Unmarshal(result, &resultParsed)
		items = append(items, map[string]any{
			"id": id, "kind": kind, "target": target, "status": status, "drill": drill,
			"requested_by_email": requestedBy, "approval_id": approvalID, "backup_id": backupID,
			"started_at": startedAt, "finished_at": finishedAt, "size_bytes": sizeBytes,
			"object_ref": objectRef, "checksum": checksum, "schema_version": schemaVersion, "build_sha": buildSHA,
			"verify_status": verifyStatus, "verify_detail": verifyParsed, "rpo_seconds": rpo, "rto_seconds": rto,
			"maintenance_window": maintenanceWindow, "notes": notes, "result": resultParsed, "created_at": createdAt,
		})
	}
	var lastBackup *time.Time
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `
		SELECT max(finished_at) FROM backup_jobs WHERE kind='backup' AND status IN ('completed','verified')`).Scan(&lastBackup)
	respondList(w, r, map[string]any{"items": items, "last_successful_backup_at": lastBackup,
		"data_source": "postgres:backup_jobs + MinIO 备份桶"}, len(items), nil)
}

func (s *Server) handleAdminCreateBackup(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		Target         string `json:"target"`
		Drill          bool   `json:"drill"`
		Reason         string `json:"reason"`
		StepUpPassword string `json:"step_up_password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	switch in.Target {
	case "postgres", "clickhouse", "minio", "config", "all":
	case "":
		in.Target = "config"
	default:
		validationError(w, r, "target 取值非法", []fieldError{{Field: "target", Code: "invalid", Message: "postgres|clickhouse|minio|config|all"}})
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写备份原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	p := PrincipalFrom(ctx)
	started := time.Now()
	var id string
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO backup_jobs (kind, target, status, drill, requested_by, requested_by_email, started_at, build_sha, notes)
		VALUES ('backup', $1, 'running', $2, $3, $4, $5, $6, $7) RETURNING id::text`,
		in.Target, in.Drill, s6NullUUID(p.IdentityID), p.Email, started, os.Getenv("BUILD_SHA"), in.Reason).Scan(&id); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "备份任务创建失败", nil)
		return
	}
	// 备份内容：平台配置快照（不含任何秘密值，只有 secret_ref）。
	artifact, err := s.s6ConfigSnapshot(ctx)
	status := "completed"
	verifyStatus := "verified"
	var sizeBytes int64
	checksum := ""
	objectRef := ""
	verifyDetail := map[string]any{}
	if err != nil {
		status = "failed"
		verifyStatus = "failed"
		verifyDetail["error"] = "配置快照生成失败"
	} else {
		raw := artifact
		sum := sha256.Sum256(raw)
		checksum = hex.EncodeToString(sum[:])
		sizeBytes = int64(len(raw))
		objectRef = fmt.Sprintf("backups/config/%s/%s.json", started.UTC().Format("2006/01/02"), id)
		if s.deps.Raw != nil {
			if _, err := s.deps.Raw.PutNamed(ctx, s.deps.Cfg.BackupsBucket, objectRef, raw, "application/json"); err != nil {
				status = "failed"
				verifyStatus = "failed"
				verifyDetail["error"] = "备份对象写入失败"
			}
		} else {
			verifyDetail["note"] = "对象存储未启用，快照只保留元数据"
		}
		if status == "completed" {
			if s.deps.Raw != nil {
				if size, err := s.deps.Raw.Stat(ctx, objectRef); err == nil {
					verifyDetail["stored_size_bytes"] = size
					verifyDetail["stored_matches"] = size == sizeBytes
					if size != sizeBytes {
						verifyStatus = "mismatch"
					}
				}
			}
			var schemaMax string
			_ = s.deps.Store.Pool.QueryRow(ctx, "SELECT coalesce(max(name),'') FROM schema_migrations").Scan(&schemaMax)
			verifyDetail["schema_migrations_max"] = schemaMax
			verifyDetail["checksum_algorithm"] = "sha256"
		}
	}
	finished := time.Now()
	rto := finished.Sub(started).Seconds()
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE backup_jobs SET status=$2, finished_at=$3, size_bytes=$4, object_ref=$5, checksum=$6,
		       verify_status=$7, verify_detail=$8::jsonb, rto_seconds=$9, schema_version=$10, updated_at=now()
		WHERE id=$1`, id, status, finished, sizeBytes, objectRef, checksum, verifyStatus, mustJSON(verifyDetail),
		int64(rto), verifyDetail["schema_migrations_max"]); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "备份结果写入失败", nil)
		return
	}
	s.s6Audit(r, "admin.backup.create", "admin.system", "backup_job", id, map[string]any{
		"target": in.Target, "drill": in.Drill, "status": status, "object_ref": objectRef,
		"checksum": checksum, "size_bytes": sizeBytes, "verify_status": verifyStatus, "reason": in.Reason,
	})
	respondCreated(w, r, map[string]any{
		"id": id, "status": status, "target": in.Target, "object_ref": objectRef, "checksum": checksum,
		"size_bytes": sizeBytes, "verify_status": verifyStatus, "verify_detail": verifyDetail,
	})
}

// s6ConfigSnapshot 生成配置快照（秘密只保存引用摘要）。
func (s *Server) s6ConfigSnapshot(ctx context.Context) ([]byte, error) {
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT key, category, value_type, value, secret_ref, is_secret, description, revision FROM configuration_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := make([]map[string]any, 0, 32)
	for rows.Next() {
		var key, category, valueType, secretRef, description string
		var raw []byte
		var isSecret bool
		var revision int64
		if err := rows.Scan(&key, &category, &valueType, &raw, &secretRef, &isSecret, &description, &revision); err != nil {
			return nil, err
		}
		var value any
		_ = json.Unmarshal(raw, &value)
		if isSecret {
			value = nil
			secretRef = s6MaskRef(secretRef)
		}
		settings = append(settings, map[string]any{
			"key": key, "category": category, "value_type": valueType, "value": value,
			"secret_ref": secretRef, "is_secret": isSecret, "description": description, "revision": revision,
		})
	}
	flagRows, err := s.deps.Store.Pool.Query(ctx, s6FlagSelect+" ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer flagRows.Close()
	flags := make([]s6Flag, 0, 16)
	for flagRows.Next() {
		flag, err := scanS6Flag(flagRows)
		if err != nil {
			return nil, err
		}
		flags = append(flags, flag)
	}
	var schemaMax string
	_ = s.deps.Store.Pool.QueryRow(ctx, "SELECT coalesce(max(name),'') FROM schema_migrations").Scan(&schemaMax)
	snapshot := map[string]any{
		"generated_at":          time.Now().UTC().Format(time.RFC3339),
		"environment":           s.cfg.Env,
		"schema_migrations_max": schemaMax,
		"settings":              settings,
		"feature_flags":         flags,
		"secrets_included":      false,
		"note":                  "配置快照不包含任何秘密明文，只包含 secret_ref 摘要。",
	}
	return json.MarshalIndent(snapshot, "", "  ")
}

func (s *Server) handleAdminVerifyBackup(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "备份不存在", nil)
		return
	}
	ctx := r.Context()
	var objectRef, checksum, status string
	var sizeBytes int64
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT coalesce(object_ref,''), coalesce(checksum,''), status, size_bytes FROM backup_jobs WHERE id=$1`, id).
		Scan(&objectRef, &checksum, &status, &sizeBytes); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "备份不存在", nil)
		return
	}
	detail := map[string]any{"object_ref": objectRef, "expected_checksum": checksum, "expected_size": sizeBytes}
	verifyStatus := "verified"
	if s.deps.Raw == nil || objectRef == "" {
		verifyStatus = "unverifiable"
		detail["reason"] = "对象存储未启用或该备份没有对象引用"
	} else {
		data, err := s.deps.Raw.Get(ctx, objectRef)
		if err != nil {
			verifyStatus = "missing"
			detail["reason"] = "备份对象不可读"
		} else {
			sum := sha256.Sum256(data)
			actual := hex.EncodeToString(sum[:])
			detail["actual_checksum"] = actual
			detail["actual_size"] = len(data)
			detail["checksum_matches"] = actual == checksum
			detail["size_matches"] = int64(len(data)) == sizeBytes
			if actual != checksum {
				verifyStatus = "mismatch"
			}
		}
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE backup_jobs SET verify_status=$2, verify_detail=$3::jsonb, status=CASE WHEN $2='verified' THEN 'verified' ELSE status END, updated_at=now()
		WHERE id=$1`, id, verifyStatus, mustJSON(detail)); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "备份校验更新失败", nil)
		return
	}
	s.s6Audit(r, "admin.backup.verify", "admin.system", "backup_job", id, map[string]any{"verify_status": verifyStatus, "detail": detail})
	respondOK(w, r, map[string]any{"id": id, "verify_status": verifyStatus, "detail": detail})
}

func (s *Server) handleAdminRestores(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, coalesce(backup_id::text,''), target, scope, impact, conflict_count, verified, verify_detail,
		       coalesce(maintenance_window,''), coalesce(approval_request_id::text,''), created_at
		FROM restore_previews ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "恢复记录读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, 16)
	for rows.Next() {
		var id, backupID, target, maintenanceWindow, approvalID string
		var scope, impact, verifyDetail []byte
		var conflictCount int64
		var verified bool
		var createdAt time.Time
		if err := rows.Scan(&id, &backupID, &target, &scope, &impact, &conflictCount, &verified, &verifyDetail,
			&maintenanceWindow, &approvalID, &createdAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "恢复记录读取失败", nil)
			return
		}
		var scopeParsed, impactParsed, verifyParsed any
		_ = json.Unmarshal(scope, &scopeParsed)
		_ = json.Unmarshal(impact, &impactParsed)
		_ = json.Unmarshal(verifyDetail, &verifyParsed)
		items = append(items, map[string]any{
			"id": id, "backup_id": backupID, "target": target, "scope": scopeParsed, "impact": impactParsed,
			"conflict_count": conflictCount, "verified": verified, "verify_detail": verifyParsed,
			"maintenance_window": maintenanceWindow, "approval_id": approvalID, "created_at": createdAt,
		})
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

func (s *Server) handleAdminRestorePreview(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		BackupID string `json:"backup_id"`
		Target   string `json:"target"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	ctx := r.Context()
	if _, err := parseS6UUID(in.BackupID); err != nil {
		validationError(w, r, "backup_id 必须是合法 UUID", []fieldError{{Field: "backup_id", Code: "invalid", Message: "UUID"}})
		return
	}
	var kind, status, target, objectRef, checksum, verifyStatus string
	var sizeBytes int64
	err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT kind, status, target, coalesce(object_ref,''), coalesce(checksum,''), coalesce(verify_status,''), size_bytes
		FROM backup_jobs WHERE id=$1`, in.BackupID).Scan(&kind, &status, &target, &objectRef, &checksum, &verifyStatus, &sizeBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "备份不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "备份读取失败", nil)
		return
	}
	if in.Target == "" {
		in.Target = target
	}
	impact := map[string]any{
		"target": in.Target, "backup_kind": kind, "backup_status": status,
		"overwrites":      []string{"configuration_settings", "feature_flags"},
		"merges":          []string{"provider_descriptors", "routing_policies"},
		"never_touched":   []string{"audit_events_v2", "download_jobs", "shards", "checkpoints"},
		"requires_window": true, "maintenance_window": "需要维护窗口；恢复期间 API 只读",
		"approval_required": true, "second_approver": true,
	}
	conflictCount := int64(0)
	if kind == "backup" && objectRef != "" && s.deps.Raw != nil {
		if data, err := s.deps.Raw.Get(ctx, objectRef); err == nil {
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != checksum {
				conflictCount++
				impact["checksum_mismatch"] = true
			}
			var snapshot struct {
				Settings []struct {
					Key      string `json:"key"`
					Revision int64  `json:"revision"`
				} `json:"settings"`
			}
			if json.Unmarshal(data, &snapshot) == nil {
				for _, item := range snapshot.Settings {
					var current int64
					if err := s.deps.Store.Pool.QueryRow(ctx, "SELECT revision FROM configuration_settings WHERE key=$1", item.Key).Scan(&current); err == nil && current != item.Revision {
						conflictCount++
					}
				}
				impact["settings_in_snapshot"] = len(snapshot.Settings)
			}
		}
	}
	impact["conflict_count"] = conflictCount
	var previewID string
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO restore_previews (backup_id, target, scope, impact, conflict_count, verified, verify_detail, maintenance_window, requested_by)
		VALUES ($1,$2,$3::jsonb,$4::jsonb,$5,$6,$7::jsonb,$8,$9) RETURNING id::text`,
		in.BackupID, in.Target, mustJSON(map[string]any{"target": in.Target}), mustJSON(impact), conflictCount,
		verifyStatus == "verified", mustJSON(map[string]any{"verify_status": verifyStatus, "object_ref": objectRef}),
		"需要维护窗口", s6NullUUID(PrincipalFrom(ctx).IdentityID)).Scan(&previewID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "恢复预览写入失败", nil)
		return
	}
	s.s6Audit(r, "admin.restore.preview", "admin.system", "restore_preview", previewID, map[string]any{
		"backup_id": in.BackupID, "impact": impact, "conflict_count": conflictCount, "size_bytes": sizeBytes,
	})
	respondOK(w, r, map[string]any{"preview_id": previewID, "backup_id": in.BackupID, "impact": impact,
		"verify_status": verifyStatus, "dry_run": true})
}

func (s *Server) handleAdminCreateRestore(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		BackupID       string `json:"backup_id"`
		PreviewID      string `json:"preview_id"`
		Reason         string `json:"reason"`
		StepUpPassword string `json:"step_up_password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写恢复原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	if _, err := parseS6UUID(in.BackupID); err != nil {
		validationError(w, r, "backup_id 必须是合法 UUID", []fieldError{{Field: "backup_id", Code: "invalid", Message: "UUID"}})
		return
	}
	ctx := r.Context()
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.restore.request") {
		return
	}
	p := PrincipalFrom(ctx)
	approval, err := s.s6CreateApproval(ctx, "system.restore", "backup_job", in.BackupID,
		map[string]any{"backup_id": in.BackupID, "preview_id": in.PreviewID, "reason": in.Reason},
		p.IdentityID, p.Email, 4*time.Hour)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "恢复审批请求创建失败", nil)
		return
	}
	var id string
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO backup_jobs (kind, target, status, requested_by, requested_by_email, approval_request_id, maintenance_window, notes)
		SELECT 'restore', b.target, 'queued', $2, $3, $4, '需要维护窗口', $5 FROM backup_jobs b WHERE b.id=$1
		RETURNING id::text`, in.BackupID, s6NullUUID(p.IdentityID), p.Email, approval.ID, in.Reason).Scan(&id); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "恢复请求创建失败", nil)
		return
	}
	s.s6AuditWithApproval(r, "admin.restore.request", "admin.system", "backup_job", id, approval.ID, map[string]any{
		"backup_id": in.BackupID, "reason": in.Reason, "preview_id": in.PreviewID,
	})
	respondCreated(w, r, map[string]any{
		"id": id, "status": "queued", "approval": approval,
		"note": "恢复必须在维护窗口内执行并需要第二位管理员审批；当前只登记请求。",
	})
}

// ---- 版本与迁移（A-VERSION-01） ----

func (s *Server) handleAdminSystemVersion(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	migrations := make([]map[string]any, 0, 64)
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT name, applied_at FROM schema_migrations ORDER BY name DESC LIMIT 12`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var appliedAt time.Time
			if scanErr := rows.Scan(&name, &appliedAt); scanErr == nil {
				migrations = append(migrations, map[string]any{"name": name, "applied_at": appliedAt})
			}
		}
	}
	var schemaCount int
	_ = s.deps.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&schemaCount)
	chTables := 0
	if s.deps.CH != nil {
		var count uint64
		if err := s.deps.CH.DB().QueryRow(ctx, "SELECT count() FROM system.tables WHERE database='blockchain'").Scan(&count); err == nil {
			chTables = int(count)
		}
	}
	components := map[string]any{
		"api":      map[string]any{"env": s.cfg.Env, "auth_mode": string(s.authMode), "status": "running"},
		"worker":   map[string]any{"status": "unknown", "note": "worker 为独立进程，健康状态见控制面"},
		"frontend": map[string]any{"admin_base": "/admin/", "user_base": "/app-v2/"},
		"storage": map[string]any{
			"postgres_migrations": schemaCount, "clickhouse_tables": chTables,
			"raw_bucket": s.cfg.RawBucket, "export_bucket": s.cfg.ExportBucket, "backups_bucket": s.cfg.BackupsBucket,
		},
	}
	respondOK(w, r, map[string]any{
		"build_sha": os.Getenv("BUILD_SHA"), "environment": s.cfg.Env,
		"go_version": runtimeVersion(), "schema_migrations_max": firstOr(migrations, "name"),
		"schema_migration_count": schemaCount, "recent_migrations": migrations, "components": components,
		"as_of": s6Now().Format(time.RFC3339),
	})
}

func firstOr(items []map[string]any, key string) string {
	if len(items) == 0 {
		return ""
	}
	if v, ok := items[0][key].(string); ok {
		return v
	}
	return ""
}

// ---- 审计导出（A-AUDIT-03 / ADMIN-008） ----

func (s *Server) handleAdminAuditExport(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		Format         string `json:"format"`
		Chain          string `json:"chain"`
		FromSeq        *int64 `json:"from_seq"`
		ToSeq          *int64 `json:"to_seq"`
		ActorEmail     string `json:"actor_email"`
		Action         string `json:"action"`
		Category       string `json:"category"`
		Result         string `json:"result"`
		From           string `json:"from"`
		To             string `json:"to"`
		Limit          int    `json:"limit"`
		Reason         string `json:"reason"`
		StepUpPassword string `json:"step_up_password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if in.Format == "" {
		in.Format = "jsonl"
	}
	if in.Format != "jsonl" && in.Format != "csv" {
		validationError(w, r, "format 取值非法", []fieldError{{Field: "format", Code: "invalid", Message: "jsonl|csv"}})
		return
	}
	if in.Chain == "" {
		in.Chain = "audit"
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写导出原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.audit.export") {
		return
	}
	ctx := r.Context()
	p := PrincipalFrom(ctx)
	meta := metaFrom(ctx)
	maxEvents := 200000
	var storedRaw []byte
	if err := s.deps.Store.Pool.QueryRow(ctx,
		"SELECT value FROM configuration_settings WHERE key='audit.export.max_events'").Scan(&storedRaw); err == nil {
		var limit int
		if json.Unmarshal(storedRaw, &limit) == nil && limit > 0 {
			maxEvents = limit
		}
	}
	if in.Limit <= 0 || in.Limit > maxEvents {
		in.Limit = maxEvents
	}
	filters := map[string]any{
		"chain": in.Chain, "from_seq": in.FromSeq, "to_seq": in.ToSeq, "actor_email": in.ActorEmail,
		"action": in.Action, "category": in.Category, "result": in.Result, "from": in.From, "to": in.To,
		"limit": in.Limit, "format": in.Format,
	}
	var exportID string
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO audit_exports (format, chain, filters, status, requested_by, requested_by_email, started_at)
		VALUES ($1,$2,$3::jsonb,'running',$4,$5,now()) RETURNING id::text`,
		in.Format, in.Chain, mustJSON(filters), s6NullUUID(p.IdentityID), p.Email).Scan(&exportID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审计导出任务创建失败", nil)
		return
	}

	query := `SELECT seq, occurred_at, actor_type, coalesce(actor_id::text,''), coalesce(actor_email,''),
	       coalesce(organization_id::text,''), coalesce(workspace_id::text,''), action, category,
	       coalesce(resource_type,''), coalesce(resource_id,''), result, coalesce(permission_key,''),
	       coalesce(policy_decision,''), coalesce(risk_level,''), sensitive, coalesce(trace_id,''),
	       coalesce(request_id,''), coalesce(route_template,''), prev_hash, event_hash, before, after, changes, detail,
	       coalesce(client_ip::text,'')
	FROM audit_events_v2
	WHERE chain = $1
	  AND ($2::bigint IS NULL OR seq >= $2)
	  AND ($3::bigint IS NULL OR seq <= $3)
	  AND ($4 = '' OR actor_email = $4)
	  AND ($5 = '' OR action = $5)
	  AND ($6 = '' OR category = $6)
	  AND ($7 = '' OR result = $7)
	  AND ($8::timestamptz IS NULL OR occurred_at >= $8::timestamptz)
	  AND ($9::timestamptz IS NULL OR occurred_at <= $9::timestamptz)
	ORDER BY seq ASC
	LIMIT $10`
	rows, err := s.deps.Store.Pool.Query(ctx, query, in.Chain, in.FromSeq, in.ToSeq, in.ActorEmail, in.Action,
		in.Category, in.Result, timeArg(in.From), timeArg(in.To), in.Limit)
	if err != nil {
		_, _ = s.deps.Store.Pool.Exec(ctx, "UPDATE audit_exports SET status='failed', error='query failed', completed_at=now() WHERE id=$1", exportID)
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审计事件查询失败", nil)
		return
	}
	defer rows.Close()
	var buffer bytes.Buffer
	var csvWriter *csv.Writer
	if in.Format == "csv" {
		csvWriter = csv.NewWriter(&buffer)
		_ = csvWriter.Write([]string{"seq", "occurred_at", "actor_type", "actor_id", "actor_email", "organization_id",
			"workspace_id", "action", "category", "resource_type", "resource_id", "result", "permission_key",
			"policy_decision", "risk_level", "sensitive", "trace_id", "request_id", "route_template", "prev_hash",
			"event_hash", "client_ip"})
	}
	eventCount := int64(0)
	var minSeq, maxSeq int64
	var firstAt, lastAt *time.Time
	for rows.Next() {
		var seq int64
		var occurredAt time.Time
		var actorType, actorID, actorEmail, orgID, workspaceID, action, category, resourceType, resourceID, result, permissionKey, policyDecision, riskLevel, traceID, requestID, routeTemplate, prevHash, eventHash, clientIP string
		var sensitive bool
		var before, after, changes, detail []byte
		if err := rows.Scan(&seq, &occurredAt, &actorType, &actorID, &actorEmail, &orgID, &workspaceID, &action,
			&category, &resourceType, &resourceID, &result, &permissionKey, &policyDecision, &riskLevel, &sensitive,
			&traceID, &requestID, &routeTemplate, &prevHash, &eventHash, &before, &after, &changes, &detail, &clientIP); err != nil {
			// Close the open result set before this UPDATE: pgx allows one
			// in-flight statement per connection and on a *dbtx.Pool bound to the
			// request transaction the UPDATE would otherwise fail with "conn busy".
			rows.Close()
			_, _ = s.deps.Store.Pool.Exec(ctx, "UPDATE audit_exports SET status='failed', error='scan failed', completed_at=now() WHERE id=$1", exportID)
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审计事件读取失败", nil)
			return
		}
		eventCount++
		if minSeq == 0 || seq < minSeq {
			minSeq = seq
		}
		maxSeq = seq
		if firstAt == nil {
			t := occurredAt
			firstAt = &t
		}
		t := occurredAt
		lastAt = &t
		if csvWriter != nil {
			_ = csvWriter.Write([]string{strconv.FormatInt(seq, 10), occurredAt.UTC().Format(time.RFC3339Nano),
				actorType, actorID, actorEmail, orgID, workspaceID, action, category, resourceType, resourceID, result,
				permissionKey, policyDecision, riskLevel, strconv.FormatBool(sensitive), traceID, requestID,
				routeTemplate, prevHash, eventHash, clientIP})
			continue
		}
		record := map[string]any{
			"chain": in.Chain, "seq": seq, "occurred_at": occurredAt.UTC().Format(time.RFC3339Nano),
			"actor_type": actorType, "actor_id": actorID, "actor_email": actorEmail,
			"organization_id": orgID, "workspace_id": workspaceID, "action": action, "category": category,
			"resource_type": resourceType, "resource_id": resourceID, "result": result,
			"permission_key": permissionKey, "policy_decision": policyDecision, "risk_level": riskLevel,
			"sensitive": sensitive, "trace_id": traceID, "request_id": requestID, "route_template": routeTemplate,
			"prev_hash": prevHash, "event_hash": eventHash, "client_ip": clientIP,
		}
		for name, raw := range map[string][]byte{"before": before, "after": after, "changes": changes, "detail": detail} {
			if len(raw) == 0 {
				continue
			}
			var parsed any
			if json.Unmarshal(raw, &parsed) == nil {
				record[name] = parsed
			}
		}
		line, err := json.Marshal(record)
		if err != nil {
			continue
		}
		buffer.Write(line)
		buffer.WriteByte('\n')
	}
	if rows.Err() != nil {
		_, _ = s.deps.Store.Pool.Exec(ctx, "UPDATE audit_exports SET status='failed', error='stream failed', completed_at=now() WHERE id=$1", exportID)
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审计事件读取中断", nil)
		return
	}
	if csvWriter != nil {
		csvWriter.Flush()
	}
	payload := buffer.Bytes()
	sum := sha256.Sum256(payload)
	checksum := hex.EncodeToString(sum[:])
	objectRef := fmt.Sprintf("audit-exports/%s/%s.%s", time.Now().UTC().Format("2006/01/02"), exportID, in.Format)
	manifestRef := fmt.Sprintf("audit-exports/%s/%s.manifest.json", time.Now().UTC().Format("2006/01/02"), exportID)
	contentType := "application/x-ndjson"
	if in.Format == "csv" {
		contentType = "text/csv"
	}
	manifest := map[string]any{
		"export_id": exportID, "generated_at": time.Now().UTC().Format(time.RFC3339),
		"build_sha": os.Getenv("BUILD_SHA"), "format": in.Format, "content_type": contentType,
		"filters": filters, "event_count": eventCount, "from_seq": minSeq, "to_seq": maxSeq,
		"first_event_at": firstAt, "last_event_at": lastAt,
		"object_ref": objectRef, "size_bytes": len(payload), "sha256": checksum,
		"chain": in.Chain, "requested_by": p.Email, "request_id": meta.RequestID, "trace_id": meta.TraceID,
		"requested_reason": in.Reason, "encoding": "utf-8", "line_delimited": in.Format == "jsonl",
		"secret_policy": "审计事件本身不保存秘密明文；导出不做额外脱敏，但 before/after 已在写入时按 §6.5 处理。",
	}
	status := "completed"
	failure := ""
	if s.deps.Raw != nil {
		if _, err := s.deps.Raw.PutNamed(ctx, s.deps.Cfg.ExportBucket, objectRef, payload, contentType); err != nil {
			status = "failed"
			failure = "导出对象写入对象存储失败"
		} else if _, err := s.deps.Raw.PutNamed(ctx, s.deps.Cfg.ExportBucket, manifestRef, mustJSONBytes(manifest), "application/json"); err != nil {
			status = "failed"
			failure = "manifest 写入对象存储失败"
		}
	} else {
		status = "failed"
		failure = "对象存储未启用"
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE audit_exports SET status=$2, event_count=$3, from_seq=$4, to_seq=$5, first_event_at=$6, last_event_at=$7,
		       object_ref=$8, manifest_ref=$9, size_bytes=$10, sha256=$11, manifest=$12::jsonb, error=$13,
		       completed_at=now(), expires_at=now() + interval '30 days'
		WHERE id=$1`, exportID, status, eventCount, minSeq, maxSeq, firstAt, lastAt, objectRef, manifestRef,
		len(payload), checksum, mustJSONBytes(manifest), failure); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审计导出结果写入失败", nil)
		return
	}
	s.s6Audit(r, "admin.audit.export", "admin.security", "audit_export", exportID, map[string]any{
		"filters": filters, "event_count": eventCount, "sha256": checksum, "object_ref": objectRef,
		"manifest_ref": manifestRef, "reason": in.Reason, "status": status,
	})
	if status != "completed" {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审计导出失败："+failure, map[string]any{"export_id": exportID})
		return
	}
	respondCreated(w, r, map[string]any{
		"id": exportID, "status": status, "format": in.Format, "event_count": eventCount,
		"from_seq": minSeq, "to_seq": maxSeq, "size_bytes": len(payload), "sha256": checksum,
		"object_ref": objectRef, "manifest_ref": manifestRef, "manifest": manifest,
	})
}

func (s *Server) handleAdminAuditExports(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, format, chain, filters, status, coalesce(requested_by_email,''), event_count, from_seq, to_seq,
		       coalesce(object_ref,''), coalesce(manifest_ref,''), size_bytes, sha256, coalesce(error,''), created_at, completed_at
		FROM audit_exports ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审计导出列表读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, 16)
	for rows.Next() {
		var id, format, chain, status, requestedBy, objectRef, manifestRef, checksum, failure string
		var filters []byte
		var eventCount int64
		var fromSeq, toSeq *int64
		var sizeBytes int64
		var createdAt time.Time
		var completedAt *time.Time
		if err := rows.Scan(&id, &format, &chain, &filters, &status, &requestedBy, &eventCount, &fromSeq, &toSeq,
			&objectRef, &manifestRef, &sizeBytes, &checksum, &failure, &createdAt, &completedAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审计导出列表读取失败", nil)
			return
		}
		var parsed any
		_ = json.Unmarshal(filters, &parsed)
		items = append(items, map[string]any{
			"id": id, "format": format, "chain": chain, "filters": parsed, "status": status,
			"requested_by_email": requestedBy, "event_count": eventCount, "from_seq": fromSeq, "to_seq": toSeq,
			"object_ref": objectRef, "manifest_ref": manifestRef, "size_bytes": sizeBytes, "sha256": checksum,
			"error": failure, "created_at": createdAt, "completed_at": completedAt,
		})
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

func timeArg(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	return t
}

func mustJSONBytes(v any) []byte {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

// runtimeVersion 返回 Go 运行时版本（不引入额外依赖）。
func runtimeVersion() string { return runtime.Version() }
