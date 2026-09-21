package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// asInt 把 preview 结果里的数值安全转成 int（JSON 反序列化后可能是 float64）。
func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		parsed, err := n.Int64()
		if err != nil {
			return 0
		}
		return int(parsed)
	}
	return 0
}

// Sprint 6 Provider 与 API 治理（A-PROVIDER-01/02、A-ROUTING-01、A-KEY-01、A-REQ-01、A-COST-01、A-BUDGET-01）。
// 秘密规则（§19.7 / §23.6）：provider_secrets 只保存引用名 + 前 6 位摘要 + SHA-256 指纹，永不回显明文。

func (s *Server) registerAdminProviderRoutes(r chi.Router) {
	r.Get("/providers", s.require("admin:provider:read", s.handleAdminProviders))
	r.Get("/provider/accounts", s.require("admin:provider:read", s.handleAdminProviders))
	r.Get("/providers/{id}", s.require("admin:provider:read", s.handleAdminProviderDetail))
	r.Patch("/providers/{id}", s.require("admin:provider:update", s.handleAdminProviderUpdate))
	r.Get("/providers/{id}/secrets", s.require("admin:provider:read", s.handleAdminProviderSecrets))
	// S7 修复：入口放行"读 / 改配置 / 轮换密钥"三类主体，具体命令在 handler 内精确鉴权。
	// 仅写 admin:provider:read 会让被授予 admin:provider:key:rotate 的 security_admin 永远 403。
	r.Post("/providers/{id}/commands/{command}", s.requireAny(
		[]string{"admin:provider:read", "admin:provider:update", "admin:provider:key:rotate"},
		s.handleAdminProviderCommand))

	// ---- 工作流 E：dRPC 额度 / 性能档位 / 运行时状态 / 分类密钥（规范 §5） ----
	// 静态命令段优先于上面的 {command} 通配段（chi 静态边优先），因此这两条命令拿到
	// 精确的 admin:provider:update 权限，而不是通配路由的 requireAny 兜底。
	r.Get("/providers/{id}/quota", s.require("admin:cost:read", s.handleAdminProviderQuota))
	r.Get("/providers/{id}/quota-history", s.require("admin:cost:read", s.handleAdminProviderQuotaHistory))
	r.Get("/providers/{id}/performance-profiles", s.require("admin:provider:read", s.handleAdminProviderPerformanceProfiles))
	r.Patch("/providers/{id}/performance-profiles/{chain}/{operation}/{mode}", s.require("admin:performance:update", s.handleAdminProviderPerformanceProfileUpdate))
	r.Get("/providers/{id}/runtime-states", s.require("admin:provider:read", s.handleAdminProviderRuntimeStates))
	r.Post("/providers/{id}/commands/sync-quota", s.require("admin:provider:update", s.handleAdminProviderSyncQuota))
	r.Post("/providers/{id}/commands/probe-channels", s.require("admin:provider:update", s.handleAdminProviderProbeChannels))
	// 规范 §5 把 PUT secrets/{kind} 归到 admin:provider:update；同时放行 admin:provider:key:rotate，
	// 与既有 rotate-secret 命令的精确鉴权口径一致（生成器取首项，OpenAPI 仍显示 admin:provider:update）。
	r.Put("/providers/{id}/secrets/{kind}", s.requireAny(
		[]string{"admin:provider:update", "admin:provider:key:rotate"},
		s.handleAdminProviderSecretSet))

	r.Get("/routing-policies", s.require("admin:routing:read", s.handleAdminRoutingPolicies))
	r.Get("/routing-policies/{id}", s.require("admin:routing:read", s.handleAdminRoutingPolicyDetail))
	r.Patch("/routing-policies/{id}", s.require("admin:routing:update", s.handleAdminRoutingPolicyUpdate))

	r.Get("/api-keys", s.require("admin:apikey:read", s.handleAdminAPIKeys))
	r.Post("/api-keys", s.require("admin:apikey:create", s.handleAdminCreateAPIKey))
	r.Post("/api-keys/{id}/commands/{command}", s.require("admin:apikey:revoke", s.handleAdminAPIKeyCommand))

	r.Get("/requests", s.require("admin:request:read", s.handleAdminRequests))
	r.Get("/requests/{id}", s.require("admin:request:read", s.handleAdminRequestDetail))

	r.Get("/costs/summary", s.require("admin:cost:read", s.handleAdminCostSummary))
	r.Get("/costs", s.require("admin:cost:read", s.handleAdminCostSummary))
	r.Get("/budgets", s.require("admin:budget:read", s.handleAdminBudgets))
	r.Post("/budgets", s.require("admin:budget:update", s.handleAdminCreateBudget))
	r.Patch("/budgets/{id}", s.require("admin:budget:update", s.handleAdminUpdateBudget))
}

// ---- Provider 描述符 ----

type s6Provider struct {
	Key                string         `json:"key"`
	DisplayName        string         `json:"display_name"`
	Kind               string         `json:"kind"`
	Chains             []string       `json:"chains"`
	Datasets           []string       `json:"datasets"`
	Capabilities       []any          `json:"capabilities"`
	Endpoint           string         `json:"endpoint"`
	Status             string         `json:"status"`
	Concurrency        int            `json:"concurrency"`
	RateLimitPerMinute int            `json:"rate_limit_per_minute"`
	Priority           int            `json:"priority"`
	Revision           int64          `json:"revision"`
	CapabilityDrift    any            `json:"capability_drift,omitempty"`
	LastTestAt         *time.Time     `json:"last_test_at,omitempty"`
	LastTestResult     map[string]any `json:"last_test_result,omitempty"`
	RuntimeHealth      map[string]any `json:"runtime_health,omitempty"`
	Usage              map[string]any `json:"usage"`
	ActiveSecret       map[string]any `json:"active_secret,omitempty"`
	// Quota 只在有额度来源时出现（dRPC）：nil 表示"该 Provider 不适用"，
	// 而不是"额度为 0"，列表页必须能区分这两者。
	Quota map[string]any `json:"quota,omitempty"`
	// RuntimeStates 是逐通道的实际档位与并发（provider_runtime_states）。
	RuntimeStates []map[string]any `json:"runtime_states,omitempty"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

const s6ProviderSelect = `SELECT provider_key, display_name, kind, chains, datasets, capabilities, endpoint, status,
       concurrency, rate_limit_per_minute, priority, revision, capability_drift, last_test_at, last_test_result, updated_at
  FROM provider_descriptors`

func scanS6Provider(row pgx.Row) (s6Provider, error) {
	var p s6Provider
	var caps, drift, testResult []byte
	err := row.Scan(&p.Key, &p.DisplayName, &p.Kind, &p.Chains, &p.Datasets, &caps, &p.Endpoint, &p.Status,
		&p.Concurrency, &p.RateLimitPerMinute, &p.Priority, &p.Revision, &drift, &p.LastTestAt, &testResult, &p.UpdatedAt)
	if err != nil {
		return s6Provider{}, err
	}
	_ = json.Unmarshal(caps, &p.Capabilities)
	if len(drift) > 0 {
		_ = json.Unmarshal(drift, &p.CapabilityDrift)
	}
	if len(testResult) > 0 {
		_ = json.Unmarshal(testResult, &p.LastTestResult)
	}
	if p.Chains == nil {
		p.Chains = []string{}
	}
	if p.Datasets == nil {
		p.Datasets = []string{}
	}
	if p.Capabilities == nil {
		p.Capabilities = []any{}
	}
	return p, nil
}

func (s *Server) handleAdminProviders(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	rows, err := s.deps.Store.Pool.Query(ctx, s6ProviderSelect+" ORDER BY priority, provider_key")
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Provider 列表读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]s6Provider, 0, 8)
	for rows.Next() {
		p, err := scanS6Provider(rows)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Provider 列表读取失败", nil)
			return
		}
		items = append(items, p)
	}
	for i := range items {
		items[i].Usage = s.s6ProviderUsage(ctx, items[i].Key)
		items[i].ActiveSecret = s.s6ActiveSecretSummary(ctx, items[i].Key)
		if health := s.s6RuntimeHealth(ctx, items[i].Key); health != nil {
			items[i].RuntimeHealth = health
		}
		// 列表也要能看出额度（开发文档 §14.1）：余额、当日 CU 已用/上限、剩余百分比、
		// 同步时间与当前并发。只对有快照或运行时额度的 Provider 填充，
		// 避免 Ankr 这类没有额度来源的行显示误导性的"未设置日上限"。
		if quota := s.s6ProviderQuotaSummary(ctx, items[i].Key); quota != nil {
			items[i].Quota = quota
		}
		if states, err := s.deps.Store.RuntimeStates(ctx, items[i].Key); err == nil && len(states) > 0 {
			items[i].RuntimeStates = states
		}
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

// s6ProviderUsage 汇总最近 24 小时该 Provider 的调用、错误率与 P95（真实 usage 表）。
func (s *Server) s6ProviderUsage(ctx context.Context, key string) map[string]any {
	out := map[string]any{"calls_24h": 0, "errors_24h": 0, "error_rate_24h": 0.0, "p95_ms": 0, "credits_24h": 0.0, "cost_usd_24h": 0.0}
	var calls, errs, p95 int
	var credits, cost float64
	err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE status <> 'success')::int,
		       coalesce(percentile_disc(0.95) WITHIN GROUP (ORDER BY duration_ms), 0)::int,
		       coalesce(sum(credits), 0)::float8,
		       coalesce(sum(estimated_cost_usd), 0)::float8
		FROM provider_usage
		WHERE provider = $1 AND request_time > now() - interval '24 hours'`, key).Scan(&calls, &errs, &p95, &credits, &cost)
	if err != nil {
		return out
	}
	out["calls_24h"] = calls
	out["errors_24h"] = errs
	if calls > 0 {
		out["error_rate_24h"] = float64(errs) / float64(calls)
	}
	out["p95_ms"] = p95
	out["credits_24h"] = credits
	out["cost_usd_24h"] = cost
	out["window"] = "24h"
	out["source"] = "postgres:provider_usage"
	return out
}

// s6RuntimeHealth 读取 Provider 运行态健康（实现 provider.Provider 的 Provider）。
func (s *Server) s6RuntimeHealth(ctx context.Context, key string) map[string]any {
	if s.deps.Prov == nil || s.deps.Prov.Name() != key {
		return nil
	}
	health := s.deps.Prov.Health(ctx)
	caps := s.deps.Prov.Capabilities()
	return map[string]any{
		"state": string(health.State), "last_check": health.LastCheck, "error": health.Error,
		"consecutive_failures": health.ConsecutiveFailure, "recent_error_rate": health.RecentErrorRate,
		"current_concurrency": health.CurrentConcurrency,
		"declared_capabilities": map[string]any{
			"name": caps.Name, "datasets": caps.Datasets, "max_page_size": caps.MaxPageSize,
			"supports_time_range": caps.SupportsTimeRange,
		},
		"source": "runtime:provider.Health",
	}
}

func (s *Server) s6LoadProvider(ctx context.Context, key string) (s6Provider, error) {
	p, err := scanS6Provider(s.deps.Store.Pool.QueryRow(ctx, s6ProviderSelect+" WHERE provider_key=$1", key))
	if err != nil {
		return s6Provider{}, err
	}
	p.Usage = s.s6ProviderUsage(ctx, key)
	p.ActiveSecret = s.s6ActiveSecretSummary(ctx, key)
	p.RuntimeHealth = s.s6RuntimeHealth(ctx, key)
	return p, nil
}

func (s *Server) handleAdminProviderDetail(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	p, err := s.s6LoadProvider(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "Provider 不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Provider 读取失败", nil)
		return
	}
	// 只增不改：在既有 Provider 字段之上追加 quota / performance_profiles / secrets
	// 与 runtime_health.channels（规范 §5）。
	respondOK(w, r, s.s6ProviderDetailEnrich(r.Context(), p))
}

type s6ProviderUpdate struct {
	DisplayName        *string `json:"display_name"`
	Endpoint           *string `json:"endpoint"`
	Concurrency        *int    `json:"concurrency"`
	RateLimitPerMinute *int    `json:"rate_limit_per_minute"`
	Priority           *int    `json:"priority"`
	Status             *string `json:"status"`
	Reason             string  `json:"reason"`
	StepUpPassword     string  `json:"step_up_password"`
}

func (s *Server) handleAdminProviderUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "id")
	var in s6ProviderUpdate
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写修改原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	before, err := s.s6LoadProvider(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "Provider 不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Provider 读取失败", nil)
		return
	}
	if _, ok := s6RequireRevision(w, r, before.Revision, "Provider 配置"); !ok {
		return
	}
	if status := in.Status; status != nil {
		switch *status {
		case "connected", "disconnected", "degraded", "disabled":
		default:
			validationError(w, r, "status 取值非法", []fieldError{{Field: "status", Code: "invalid", Message: "connected|disconnected|degraded|disabled"}})
			return
		}
	}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE provider_descriptors SET
		  display_name = coalesce($2, display_name),
		  endpoint = coalesce($3, endpoint),
		  concurrency = coalesce($4, concurrency),
		  rate_limit_per_minute = coalesce($5, rate_limit_per_minute),
		  priority = coalesce($6, priority),
		  status = coalesce($7, status),
		  revision = revision + 1, updated_by = $8, updated_at = now()
		WHERE provider_key = $1`, key, in.DisplayName, in.Endpoint, in.Concurrency, in.RateLimitPerMinute, in.Priority, in.Status, s6NullUUID(PrincipalFrom(ctx).IdentityID)); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Provider 更新失败", nil)
		return
	}
	after, _ := s.s6LoadProvider(ctx, key)
	s.s6AuditChange(r, "admin.provider.update", "admin.provider", "provider", key,
		map[string]any{"display_name": before.DisplayName, "endpoint": before.Endpoint, "concurrency": before.Concurrency, "rate_limit_per_minute": before.RateLimitPerMinute, "priority": before.Priority, "status": before.Status, "revision": before.Revision},
		map[string]any{"display_name": after.DisplayName, "endpoint": after.Endpoint, "concurrency": after.Concurrency, "rate_limit_per_minute": after.RateLimitPerMinute, "priority": after.Priority, "status": after.Status, "revision": after.Revision},
		map[string]any{"reason": in.Reason})
	respondOK(w, r, after)
}

func (s *Server) handleAdminProviderSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	items, err := s.s6ListSecrets(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥摘要读取失败", nil)
		return
	}
	// 三类 secret_kind 必须各占一行：库里没有的 kind 补"未配置"占位（工作流 C 事实 2）。
	items = s6EnsureSecretKindRows(items)
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

// s6ListSecrets 只返回摘要；明文永不落库、永不回显。
func (s *Server) s6ListSecrets(ctx context.Context, key string) ([]map[string]any, error) {
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT id::text, secret_kind, secret_ref, prefix, fingerprint, status, note, rotated_at, last_used_at, expires_at
		FROM provider_secrets WHERE provider_key=$1 ORDER BY rotated_at DESC LIMIT 50`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0, 4)
	for rows.Next() {
		var id, kind, ref, prefix, fingerprint, status, note string
		var rotatedAt time.Time
		var lastUsed, expires *time.Time
		if err := rows.Scan(&id, &kind, &ref, &prefix, &fingerprint, &status, &note, &rotatedAt, &lastUsed, &expires); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "secret_kind": kind, "secret_ref": s6MaskRef(ref), "prefix": prefix, "fingerprint": fingerprint,
			"status": status, "note": note, "rotated_at": rotatedAt,
			"last_used_at": lastUsed, "expires_at": expires, "plaintext_available": false,
		})
	}
	return out, rows.Err()
}

func (s *Server) s6ActiveSecretSummary(ctx context.Context, key string) map[string]any {
	var prefix, fingerprint, ref string
	var rotatedAt time.Time
	var lastUsed *time.Time
	err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT prefix, fingerprint, secret_ref, rotated_at, last_used_at FROM provider_secrets
		WHERE provider_key=$1 AND status='active' ORDER BY rotated_at DESC LIMIT 1`, key).
		Scan(&prefix, &fingerprint, &ref, &rotatedAt, &lastUsed)
	if err != nil {
		return map[string]any{"configured": false, "plaintext_available": false}
	}
	return map[string]any{
		"configured": true, "secret_ref": s6MaskRef(ref), "prefix": prefix,
		"fingerprint": fingerprint, "rotated_at": rotatedAt, "last_used_at": lastUsed,
		"plaintext_available": false,
	}
}

type s6ProviderCommandInput struct {
	Reason         string `json:"reason"`
	StepUpPassword string `json:"step_up_password"`
	SecretRef      string `json:"secret_ref"`
	SecretPrefix   string `json:"secret_prefix"`
}

// handleAdminProviderCommand 处理 connect / disconnect / test / rotate-secret。
//
// 说明：secret_ref 是密钥库/环境变量引用名，服务端只保存引用与摘要；
// 需要写入 provider_secrets 时用 secret_ref + 由部署注入的前缀/指纹，绝不接受明文。
func (s *Server) handleAdminProviderCommand(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "id")
	command := chi.URLParam(r, "command")
	var in s6ProviderCommandInput
	if err := decodeJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	ctx := r.Context()
	// S7 审计修复：本路由用 admin:provider:read 兜底（因为 {command} 是路径变量），
	// 但 connect / disconnect / rotate-secret 都是**状态改写**，其中轮换密钥在
	// policy 目录里是 RiskCritical。修复前只持有 admin:provider:read 的主体即可轮换密钥，
	// 属于"读权限承载写动作"的越权路径。这里按命令做精确鉴权（与 catalog 键一致）。
	var needed string
	switch command {
	case "test":
		needed = "admin:provider:read"
	case "connect", "disconnect":
		needed = "admin:provider:update"
	case "rotate-secret":
		needed = "admin:provider:key:rotate"
	default:
		needed = ""
	}
	if needed != "" && !PrincipalFrom(ctx).Has(needed) {
		respondError(w, r, http.StatusForbidden, "FORBIDDEN", "缺少执行该 Provider 命令所需的权限", map[string]any{"required_permission": needed})
		return
	}
	provider, err := s.s6LoadProvider(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "Provider 不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Provider 读取失败", nil)
		return
	}
	switch command {
	case "test":
		result := s.s6ProviderProbe(ctx, key)
		body, _ := json.Marshal(result)
		_, _ = s.deps.Store.Pool.Exec(ctx, `UPDATE provider_descriptors SET last_test_at=now(), last_test_result=$2::jsonb, updated_at=now() WHERE provider_key=$1`, key, string(body))
		s.s6Audit(r, "admin.provider.test", "admin.provider", "provider", key, map[string]any{
			"method": result["method"], "ok": result["ok"], "latency_ms": result["latency_ms"],
			"error_class": result["error_class"], "trace_id": result["trace_id"], "reason": in.Reason,
		})
		respondOK(w, r, result)
	case "connect", "disconnect":
		if strings.TrimSpace(in.Reason) == "" {
			validationError(w, r, "必须填写操作原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
			return
		}
		if command == "disconnect" {
			impact := s.s6ProviderImpact(ctx, key)
			if asInt(impact["affected_running_jobs"]) > 0 {
				if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.provider.disconnect") {
					return
				}
			}
			if _, err := s.deps.Store.Pool.Exec(ctx, `UPDATE provider_descriptors SET status='disconnected', revision=revision+1, updated_at=now() WHERE provider_key=$1`, key); err != nil {
				respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Provider 状态更新失败", nil)
				return
			}
			s.s6AuditChange(r, "admin.provider.disconnect", "admin.provider", "provider", key,
				map[string]any{"status": provider.Status}, map[string]any{"status": "disconnected"},
				map[string]any{"reason": in.Reason, "impact": impact, "step_up": asInt(impact["affected_running_jobs"]) > 0})
			after, _ := s.s6LoadProvider(ctx, key)
			respondOK(w, r, map[string]any{"provider": after, "impact": impact})
			return
		}
		if _, err := s.deps.Store.Pool.Exec(ctx, `UPDATE provider_descriptors SET status='connected', revision=revision+1, updated_at=now() WHERE provider_key=$1`, key); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Provider 状态更新失败", nil)
			return
		}
		s.s6AuditChange(r, "admin.provider.connect", "admin.provider", "provider", key,
			map[string]any{"status": provider.Status}, map[string]any{"status": "connected"}, map[string]any{"reason": in.Reason})
		after, _ := s.s6LoadProvider(ctx, key)
		respondOK(w, r, after)
	case "rotate-secret":
		if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.provider.rotate_secret") {
			return
		}
		if strings.TrimSpace(in.SecretRef) == "" {
			validationError(w, r, "必须提供密钥引用名 secret_ref（不接受明文密钥）", []fieldError{{Field: "secret_ref", Code: "required", Message: "必填"}})
			return
		}
		prefix := strings.TrimSpace(in.SecretPrefix)
		if len(prefix) > 6 {
			prefix = prefix[:6]
		}
		fingerprint := s6Fingerprint(in.SecretRef, provider.Revision)
		tx, err := s.deps.Store.Pool.Begin(ctx)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥轮换失败", nil)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `UPDATE provider_secrets SET status='retiring' WHERE provider_key=$1 AND status='active'`, key); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥轮换失败", nil)
			return
		}
		var secretID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO provider_secrets (provider_key, secret_ref, prefix, fingerprint, status, note, created_by, rotated_by)
			VALUES ($1,$2,$3,$4,'active',$5,$6,$6) RETURNING id::text`,
			key, in.SecretRef, prefix, fingerprint, in.Reason, s6NullUUID(PrincipalFrom(ctx).IdentityID)).Scan(&secretID); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥轮换失败", nil)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥轮换失败", nil)
			return
		}
		summaries, _ := s.s6ListSecrets(ctx, key)
		s.s6Audit(r, "admin.provider.rotate_secret", "admin.provider", "provider_secret", secretID, map[string]any{
			"provider": key, "secret_ref_masked": s6MaskRef(in.SecretRef), "fingerprint": fingerprint,
			"prefix": prefix, "reason": in.Reason, "plaintext_stored": false,
		})
		respondOK(w, r, map[string]any{
			"provider": key, "rotated_secret_id": secretID, "fingerprint": fingerprint,
			"prefix": prefix, "plaintext_returned": false, "secrets": summaries,
		})
	default:
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "不支持的命令", map[string]any{"command": command})
	}
}

func s6Fingerprint(ref string, revision int64) string {
	sum := sha256.Sum256([]byte(ref + ":" + strconv.FormatInt(revision, 10)))
	return hex.EncodeToString(sum[:8])
}

// s6ProviderProbe 使用受控低成本探针执行连接测试（Provider.Health + 一条 usage 记录）。
func (s *Server) s6ProviderProbe(ctx context.Context, key string) map[string]any {
	meta := metaFrom(ctx)
	start := time.Now()
	result := map[string]any{
		"provider": key, "method": "Provider.Health", "trace_id": meta.TraceID,
		"checked_at": start.UTC().Format(time.RFC3339),
	}
	var errClass string
	prov, hasRuntime := s.s6RuntimeProvider(key)
	if !hasRuntime {
		result["ok"] = false
		result["error_class"] = "not_runtime_provider"
		result["message"] = "该 Provider 由配置声明但当前进程未加载运行时实现"
		errClass = "not_runtime_provider"
	} else {
		health := prov.Health(ctx)
		// 支持真实通道探针的实现（dRPC）：把两条通道的探针结果一并返回（规范 §5）。
		if prober, ok := prov.(s6ChannelProber); ok {
			result["channels"] = prober.ProbeChannels(ctx)
		}
		result["ok"] = string(health.State) == "healthy" || string(health.State) == "ok" || health.Error == ""
		result["state"] = string(health.State)
		result["error"] = health.Error
		result["recent_error_rate"] = health.RecentErrorRate
		result["current_concurrency"] = health.CurrentConcurrency
		if health.Error != "" {
			errClass = "upstream"
		}
	}
	latency := time.Since(start)
	result["latency_ms"] = latency.Milliseconds()
	result["error_class"] = errClass
	status := "success"
	if errClass != "" {
		status = "error"
	}
	if s.deps.Store != nil {
		_ = s.deps.Store.InsertUsage(ctx, model.UsageRow{
			Provider: key, Method: "provider_health_probe", Dataset: "control_plane", RequestTime: start,
			DurationMs: int(latency.Milliseconds()), Status: status, HTTPStatus: 200,
			UsageType: "provider_test", RequestID: meta.RequestID, TraceID: meta.TraceID,
			APIError: errClass,
		})
	}
	return result
}

// s6ProviderImpact 计算断开 Provider 的影响面（受影响链、Dataset、任务与 fallback 能力）。
func (s *Server) s6ProviderImpact(ctx context.Context, key string) map[string]any {
	impact := map[string]any{"provider": key}
	var chains, datasets []string
	rows, err := s.deps.Store.Pool.Query(ctx, `SELECT chain, dataset FROM routing_policies WHERE enabled AND (primary_provider=$1 OR $1 = ANY(fallback_providers))`, key)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var chain, dataset string
			if scanErr := rows.Scan(&chain, &dataset); scanErr == nil {
				if chain != "" {
					chains = append(chains, chain)
				}
				if dataset != "" {
					datasets = append(datasets, dataset)
				}
			}
		}
	}
	impact["affected_chains"] = chains
	impact["affected_datasets"] = datasets
	var running, waiting int
	_ = s.deps.Store.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status IN ('RUNNING','RESUMING')),
		count(*) FILTER (WHERE status IN ('CREATED','PLANNING','QUEUED')) FROM download_jobs`).Scan(&running, &waiting)
	impact["affected_running_jobs"] = running
	impact["affected_waiting_jobs"] = waiting
	fallbacks := []string{}
	fbRows, fbErr := s.deps.Store.Pool.Query(ctx, `SELECT policy_key, primary_provider, fallback_providers FROM routing_policies WHERE enabled`)
	if fbErr == nil {
		defer fbRows.Close()
		for fbRows.Next() {
			var policyKey, primary string
			var fallbackList []string
			if scanErr := fbRows.Scan(&policyKey, &primary, &fallbackList); scanErr == nil {
				if primary == key && len(fallbackList) == 0 {
					fallbacks = append(fallbacks, policyKey+": 无 fallback")
				}
			}
		}
	}
	impact["policies_without_fallback"] = fallbacks
	return impact
}

// ---- 路由策略 ----

type s6RoutingPolicy struct {
	ID                     string    `json:"id"`
	PolicyKey              string    `json:"policy_key"`
	Description            string    `json:"description"`
	Chain                  string    `json:"chain"`
	Dataset                string    `json:"dataset"`
	Enabled                bool      `json:"enabled"`
	PrimaryProvider        string    `json:"primary_provider"`
	FallbackProviders      []string  `json:"fallback_providers"`
	MaxConcurrency         int       `json:"max_concurrency"`
	RateLimitPerMinute     int       `json:"rate_limit_per_minute"`
	CircuitErrorRate       float64   `json:"circuit_error_rate"`
	CircuitP95Ms           int       `json:"circuit_p95_ms"`
	CircuitCooldownSeconds int       `json:"circuit_cooldown_seconds"`
	RolloutPercent         int       `json:"rollout_percent"`
	Revision               int64     `json:"revision"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (s *Server) handleAdminRoutingPolicies(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, policy_key, description, chain, dataset, enabled, primary_provider, fallback_providers,
		       max_concurrency, rate_limit_per_minute, circuit_error_rate, circuit_p95_ms, circuit_cooldown_seconds,
		       rollout_percent, revision, updated_at
		FROM routing_policies ORDER BY chain, dataset`)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "路由策略读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]s6RoutingPolicy, 0, 8)
	for rows.Next() {
		var p s6RoutingPolicy
		if err := rows.Scan(&p.ID, &p.PolicyKey, &p.Description, &p.Chain, &p.Dataset, &p.Enabled, &p.PrimaryProvider,
			&p.FallbackProviders, &p.MaxConcurrency, &p.RateLimitPerMinute, &p.CircuitErrorRate, &p.CircuitP95Ms,
			&p.CircuitCooldownSeconds, &p.RolloutPercent, &p.Revision, &p.UpdatedAt); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "路由策略读取失败", nil)
			return
		}
		if p.FallbackProviders == nil {
			p.FallbackProviders = []string{}
		}
		items = append(items, p)
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

func (s *Server) handleAdminRoutingPolicyDetail(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	var p s6RoutingPolicy
	err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT id::text, policy_key, description, chain, dataset, enabled, primary_provider, fallback_providers,
		       max_concurrency, rate_limit_per_minute, circuit_error_rate, circuit_p95_ms, circuit_cooldown_seconds,
		       rollout_percent, revision, updated_at FROM routing_policies WHERE id::text=$1 OR policy_key=$1`, chi.URLParam(r, "id")).
		Scan(&p.ID, &p.PolicyKey, &p.Description, &p.Chain, &p.Dataset, &p.Enabled, &p.PrimaryProvider,
			&p.FallbackProviders, &p.MaxConcurrency, &p.RateLimitPerMinute, &p.CircuitErrorRate, &p.CircuitP95Ms,
			&p.CircuitCooldownSeconds, &p.RolloutPercent, &p.Revision, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "路由策略不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "路由策略读取失败", nil)
		return
	}
	if p.FallbackProviders == nil {
		p.FallbackProviders = []string{}
	}
	history := make([]map[string]any, 0, 10)
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT revision, before, after, change_note, actor_email, created_at FROM routing_policy_revisions
		WHERE policy_key=$1 ORDER BY revision DESC LIMIT 20`, p.PolicyKey)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var revision int64
			var beforeRaw, afterRaw []byte
			var note, actor string
			var createdAt time.Time
			if scanErr := rows.Scan(&revision, &beforeRaw, &afterRaw, &note, &actor, &createdAt); scanErr == nil {
				var before, after any
				_ = json.Unmarshal(beforeRaw, &before)
				_ = json.Unmarshal(afterRaw, &after)
				history = append(history, map[string]any{"revision": revision, "before": before, "after": after, "change_note": note, "actor_email": actor, "created_at": createdAt})
			}
		}
	}
	respondOK(w, r, map[string]any{"policy": p, "history": history})
}

type s6RoutingUpdate struct {
	Enabled                *bool    `json:"enabled"`
	PrimaryProvider        *string  `json:"primary_provider"`
	FallbackProviders      []string `json:"fallback_providers"`
	MaxConcurrency         *int     `json:"max_concurrency"`
	RateLimitPerMinute     *int     `json:"rate_limit_per_minute"`
	CircuitErrorRate       *float64 `json:"circuit_error_rate"`
	CircuitP95Ms           *int     `json:"circuit_p95_ms"`
	CircuitCooldownSeconds *int     `json:"circuit_cooldown_seconds"`
	RolloutPercent         *int     `json:"rollout_percent"`
	Reason                 string   `json:"reason"`
}

func (s *Server) handleAdminRoutingPolicyUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in s6RoutingUpdate
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写修改原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	var current s6RoutingPolicy
	err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT id::text, policy_key, description, chain, dataset, enabled, primary_provider, fallback_providers,
		       max_concurrency, rate_limit_per_minute, circuit_error_rate, circuit_p95_ms, circuit_cooldown_seconds,
		       rollout_percent, revision, updated_at FROM routing_policies WHERE id::text=$1 OR policy_key=$1`, chi.URLParam(r, "id")).
		Scan(&current.ID, &current.PolicyKey, &current.Description, &current.Chain, &current.Dataset, &current.Enabled, &current.PrimaryProvider,
			&current.FallbackProviders, &current.MaxConcurrency, &current.RateLimitPerMinute, &current.CircuitErrorRate, &current.CircuitP95Ms,
			&current.CircuitCooldownSeconds, &current.RolloutPercent, &current.Revision, &current.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "路由策略不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "路由策略读取失败", nil)
		return
	}
	if _, ok := s6RequireRevision(w, r, current.Revision, "路由策略"); !ok {
		return
	}
	if in.RolloutPercent != nil && (*in.RolloutPercent < 0 || *in.RolloutPercent > 100) {
		validationError(w, r, "rollout_percent 必须在 0-100 之间", []fieldError{{Field: "rollout_percent", Code: "invalid", Message: "0-100"}})
		return
	}
	tx, err := s.deps.Store.Pool.Begin(ctx)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "路由策略更新失败", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE routing_policies SET
		  enabled = coalesce($2, enabled),
		  primary_provider = coalesce($3, primary_provider),
		  fallback_providers = coalesce($4, fallback_providers),
		  max_concurrency = coalesce($5, max_concurrency),
		  rate_limit_per_minute = coalesce($6, rate_limit_per_minute),
		  circuit_error_rate = coalesce($7, circuit_error_rate),
		  circuit_p95_ms = coalesce($8, circuit_p95_ms),
		  circuit_cooldown_seconds = coalesce($9, circuit_cooldown_seconds),
		  rollout_percent = coalesce($10, rollout_percent),
		  revision = revision + 1, updated_by = $11, updated_at = now()
		WHERE id = $1`, current.ID, in.Enabled, in.PrimaryProvider, in.FallbackProviders, in.MaxConcurrency,
		in.RateLimitPerMinute, in.CircuitErrorRate, in.CircuitP95Ms, in.CircuitCooldownSeconds, in.RolloutPercent,
		s6NullUUID(PrincipalFrom(ctx).IdentityID)); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "路由策略更新失败", nil)
		return
	}
	var updated s6RoutingPolicy
	if err := tx.QueryRow(ctx, `
		SELECT id::text, policy_key, description, chain, dataset, enabled, primary_provider, fallback_providers,
		       max_concurrency, rate_limit_per_minute, circuit_error_rate, circuit_p95_ms, circuit_cooldown_seconds,
		       rollout_percent, revision, updated_at FROM routing_policies WHERE id=$1`, current.ID).
		Scan(&updated.ID, &updated.PolicyKey, &updated.Description, &updated.Chain, &updated.Dataset, &updated.Enabled, &updated.PrimaryProvider,
			&updated.FallbackProviders, &updated.MaxConcurrency, &updated.RateLimitPerMinute, &updated.CircuitErrorRate, &updated.CircuitP95Ms,
			&updated.CircuitCooldownSeconds, &updated.RolloutPercent, &updated.Revision, &updated.UpdatedAt); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "路由策略更新失败", nil)
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO routing_policy_revisions (policy_key, revision, before, after, change_note, actor_id, actor_email)
		VALUES ($1,$2,$3::jsonb,$4::jsonb,$5,$6,$7) ON CONFLICT (policy_key, revision) DO NOTHING`,
		current.PolicyKey, updated.Revision, mustJSON(current), mustJSON(updated), in.Reason,
		s6NullUUID(PrincipalFrom(ctx).IdentityID), PrincipalFrom(ctx).Email); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "路由策略更新失败", nil)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "路由策略更新失败", nil)
		return
	}
	if updated.FallbackProviders == nil {
		updated.FallbackProviders = []string{}
	}
	s.s6AuditChange(r, "admin.routing.update", "admin.provider", "routing_policy", current.PolicyKey, current, updated, map[string]any{"reason": in.Reason})
	respondOK(w, r, updated)
}

// ---- API / MCP Key（轮换与吊销） ----

func (s *Server) handleAdminAPIKeys(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, name, key_prefix, scopes, rate_limit_per_minute, coalesce(allowed_ips, '{}'), expires_at,
		       disabled_at, revoked_at, revoke_reason, last_used_at, created_at, coalesce(description,''), revision
		FROM gpt_action_api_keys ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "API Key 列表读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, 16)
	for rows.Next() {
		var id, name, prefix, revokeReason, description string
		var scopes, ips []string
		var limit int
		var revision int64
		var expiresAt, disabledAt, revokedAt, lastUsed *time.Time
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &prefix, &scopes, &limit, &ips, &expiresAt, &disabledAt, &revokedAt, &revokeReason, &lastUsed, &createdAt, &description, &revision); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "API Key 列表读取失败", nil)
			return
		}
		status := "active"
		if revokedAt != nil {
			status = "revoked"
		} else if disabledAt != nil {
			status = "disabled"
		} else if expiresAt != nil && expiresAt.Before(time.Now()) {
			status = "expired"
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "key_prefix": prefix, "scopes": scopes, "rate_limit_per_minute": limit,
			"allowed_ips": ips, "expires_at": expiresAt, "last_used_at": lastUsed, "created_at": createdAt,
			"revoked_at": revokedAt, "revoke_reason": revokeReason, "status": status, "description": description,
			"revision": revision, "plaintext_available": false,
		})
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

func (s *Server) handleAdminCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		RateLimit     int      `json:"rate_limit_per_minute"`
		AllowedIPs    []string `json:"allowed_ips"`
		ExpiresInDays int      `json:"expires_in_days"`
		Description   string   `json:"description"`
		Reason        string   `json:"reason"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		validationError(w, r, "必须提供密钥名称", []fieldError{{Field: "name", Code: "required", Message: "必填"}})
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥生成失败", nil)
		return
	}
	plaintext := "sk_" + hex.EncodeToString(raw)
	prefix := plaintext[:10]
	sum := sha256.Sum256([]byte(plaintext))
	hash := sum[:]
	ctx := r.Context()
	var id string
	var expires any
	if in.ExpiresInDays > 0 {
		expires = time.Now().AddDate(0, 0, in.ExpiresInDays)
	}
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO gpt_action_api_keys (name, key_prefix, key_hash, scopes, rate_limit_per_minute, allowed_ips, expires_at, created_by, created_by_identity, description)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`,
		in.Name, prefix, hash, in.Scopes, in.RateLimit, in.AllowedIPs, expires, PrincipalFrom(ctx).Email,
		s6NullUUID(PrincipalFrom(ctx).IdentityID), in.Description).Scan(&id); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥创建失败", nil)
		return
	}
	s.s6Audit(r, "admin.apikey.create", "admin.security", "api_key", id, map[string]any{
		"name": in.Name, "key_prefix": prefix, "scopes": in.Scopes, "expires_in_days": in.ExpiresInDays, "reason": in.Reason,
	})
	// 明文只在创建响应中出现一次（§21.6）。
	respondCreated(w, r, map[string]any{
		"id": id, "name": in.Name, "key_prefix": prefix, "secret": plaintext,
		"plaintext_returned_once": true, "scopes": in.Scopes, "rate_limit_per_minute": in.RateLimit,
	})
}

func (s *Server) handleAdminAPIKeyCommand(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	command := chi.URLParam(r, "command")
	var in struct {
		Reason         string `json:"reason"`
		StepUpPassword string `json:"step_up_password"`
		ExpiresInDays  int    `json:"expires_in_days"`
	}
	_ = decodeJSON(r, &in)
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写操作原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "密钥不存在", nil)
		return
	}
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.apikey."+command) {
		return
	}
	ctx := r.Context()
	switch command {
	case "revoke":
		tag, err := s.deps.Store.Pool.Exec(ctx, `
			UPDATE gpt_action_api_keys SET revoked_at=now(), disabled_at=coalesce(disabled_at, now()),
			       revoked_by=$2, revoke_reason=$3, revision=revision+1
			WHERE id=$1 AND revoked_at IS NULL`, id, s6NullUUID(PrincipalFrom(ctx).IdentityID), in.Reason)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥吊销失败", nil)
			return
		}
		if tag.RowsAffected() == 0 {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "密钥已吊销或不存在", nil)
			return
		}
		s.s6Audit(r, "admin.apikey.revoke", "admin.security", "api_key", id, map[string]any{"reason": in.Reason})
		respondOK(w, r, map[string]any{"id": id, "status": "revoked"})
	case "rotate":
		var name, prefix string
		var scopes, ips []string
		var limit int
		if err := s.deps.Store.Pool.QueryRow(ctx, `SELECT name, key_prefix, scopes, rate_limit_per_minute, coalesce(allowed_ips,'{}') FROM gpt_action_api_keys WHERE id=$1`, id).
			Scan(&name, &prefix, &scopes, &limit, &ips); err != nil {
			respondError(w, r, http.StatusNotFound, "NOT_FOUND", "密钥不存在", nil)
			return
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥生成失败", nil)
			return
		}
		plaintext := "sk_" + hex.EncodeToString(raw)
		newPrefix := plaintext[:10]
		sum := sha256.Sum256([]byte(plaintext))
		var expires any
		if in.ExpiresInDays > 0 {
			expires = time.Now().AddDate(0, 0, in.ExpiresInDays)
		}
		tx, err := s.deps.Store.Pool.Begin(ctx)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥轮换失败", nil)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `UPDATE gpt_action_api_keys SET revoked_at=now(), disabled_at=coalesce(disabled_at, now()), revoke_reason=$2, revision=revision+1 WHERE id=$1`, id, "rotated: "+in.Reason); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥轮换失败", nil)
			return
		}
		var newID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO gpt_action_api_keys (name, key_prefix, key_hash, scopes, rate_limit_per_minute, allowed_ips, expires_at, created_by, created_by_identity, rotated_from, rotated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now()) RETURNING id::text`,
			name, newPrefix, sum[:], scopes, limit, ips, expires, PrincipalFrom(ctx).Email,
			s6NullUUID(PrincipalFrom(ctx).IdentityID), id).Scan(&newID); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥轮换失败", nil)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥轮换失败", nil)
			return
		}
		s.s6Audit(r, "admin.apikey.rotate", "admin.security", "api_key", newID, map[string]any{
			"rotated_from": id, "new_key_prefix": newPrefix, "reason": in.Reason,
		})
		respondOK(w, r, map[string]any{
			"id": newID, "rotated_from": id, "name": name, "key_prefix": newPrefix,
			"secret": plaintext, "plaintext_returned_once": true,
		})
	default:
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "不支持的命令", map[string]any{"command": command})
	}
}

// ---- 请求审计（A-REQ-01） ----

func (s *Server) handleAdminRequests(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	q := r.URL.Query()
	limit := s6Limit(q.Get("limit"), 50, 200)
	provider := strings.TrimSpace(q.Get("provider"))
	status := strings.TrimSpace(q.Get("status"))
	usageType := strings.TrimSpace(q.Get("usage_type"))
	cursorAt, cursorID, hasCursor := s6ParseCursor(q.Get("cursor"))
	rows, err := s.deps.Store.Pool.Query(r.Context(), `
		SELECT id::text, provider, method, coalesce(dataset,''), coalesce(status,''), coalesce(http_status,0),
		       coalesce(duration_ms,0), coalesce(api_error,''), coalesce(error_class,''), coalesce(trace_id,''),
		       coalesce(request_id,''), coalesce(usage_type,''), coalesce(rows,0), coalesce(bytes,0),
		       coalesce(estimated_cost_usd,0)::float8, request_time, coalesce(chain,''), coalesce(cache_hit,false),
		       coalesce(job_id::text,''), coalesce(tenant_id,'')
		FROM provider_usage
		WHERE ($1 = '' OR provider = $1)
		  AND ($2 = '' OR status = $2)
		  AND ($3 = '' OR usage_type = $3)
		  AND ($4::timestamptz IS NULL OR (request_time, id) < ($4::timestamptz, $5::uuid))
		ORDER BY request_time DESC, id DESC LIMIT $6`,
		provider, status, usageType, cursorTimeArg(cursorAt, hasCursor), cursorUUIDArg(cursorID, hasCursor), limit)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "请求审计读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	var lastTime time.Time
	var lastID string
	for rows.Next() {
		var id string
		var providerName, method, dataset, rowStatus, apiError, errorClass, traceID, requestID, usageTypeName, chain, jobID, tenantID string
		var httpStatus, durationMs int
		var rowCount, bytes int64
		var cost float64
		var requestTime time.Time
		var cacheHit bool
		if err := rows.Scan(&id, &providerName, &method, &dataset, &rowStatus, &httpStatus, &durationMs, &apiError, &errorClass,
			&traceID, &requestID, &usageTypeName, &rowCount, &bytes, &cost, &requestTime, &chain, &cacheHit, &jobID, &tenantID); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "请求审计读取失败", nil)
			return
		}
		lastTime, lastID = requestTime, id
		items = append(items, map[string]any{
			"id": id, "provider": providerName, "method": method, "dataset": dataset,
			"status": rowStatus, "http_status": httpStatus, "duration_ms": durationMs,
			"error": apiError, "error_class": errorClass, "trace_id": traceID, "request_id": requestID,
			"usage_type": usageTypeName, "rows": rowCount, "bytes": bytes, "cost_usd": cost,
			"request_time": requestTime, "chain": chain, "cache_hit": cacheHit, "job_id": jobID, "tenant_id": tenantID,
		})
	}
	var nextCursor *string
	if len(items) == limit {
		value := s6MakeCursor(lastTime, lastID)
		nextCursor = &value
	}
	respondList(w, r, map[string]any{"items": items}, -1, nextCursor)
}

func cursorTimeArg(t time.Time, ok bool) any {
	if !ok {
		return nil
	}
	return t
}

// cursorUUIDArg 校验 cursor 中的 uuid，非法时退化为 NULL（等价于首页）。
func cursorUUIDArg(id string, ok bool) any {
	if !ok {
		return nil
	}
	if _, err := parseS6UUID(id); err != nil {
		return nil
	}
	return id
}

func (s *Server) handleAdminRequestDetail(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id, err := parseS6UUID(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "请求记录不存在", nil)
		return
	}
	row := s.deps.Store.Pool.QueryRow(r.Context(), `
		SELECT id::text, provider, method, coalesce(dataset,''), coalesce(status,''), coalesce(http_status,0),
		       coalesce(duration_ms,0), coalesce(api_error,''), coalesce(error_class,''), coalesce(trace_id,''),
		       coalesce(request_id,''), coalesce(usage_type,''), coalesce(rows,0), coalesce(bytes,0),
		       coalesce(estimated_cost_usd,0)::float8, request_time, coalesce(chain,''), coalesce(cache_hit,false),
		       coalesce(job_id::text,''), coalesce(tenant_id,''), coalesce(shard_id::text,''), coalesce(retry_attempt,0),
		       coalesce(retry_reason,''), coalesce(page_size,0)
		FROM provider_usage WHERE id=$1`, id)
	var outID string
	var providerName, method, dataset, rowStatus, apiError, errorClass, traceID, requestID, usageTypeName, chain, jobID, tenantID, shardID, retryReason string
	var httpStatus, durationMs, retryAttempt, pageSize int
	var rowCount, bytes int64
	var cost float64
	var requestTime time.Time
	var cacheHit bool
	if err := row.Scan(&outID, &providerName, &method, &dataset, &rowStatus, &httpStatus, &durationMs, &apiError, &errorClass,
		&traceID, &requestID, &usageTypeName, &rowCount, &bytes, &cost, &requestTime, &chain, &cacheHit, &jobID, &tenantID,
		&shardID, &retryAttempt, &retryReason, &pageSize); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "请求记录不存在", nil)
		return
	}
	var auditCount int
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT count(*) FROM audit_events_v2 WHERE trace_id=$1`, traceID).Scan(&auditCount)
	respondOK(w, r, map[string]any{
		"id": outID, "provider": providerName, "method": method, "dataset": dataset,
		"status": rowStatus, "http_status": httpStatus, "duration_ms": durationMs, "error": apiError,
		"error_class": errorClass, "trace_id": traceID, "request_id": requestID, "usage_type": usageTypeName,
		"rows": rowCount, "bytes": bytes, "cost_usd": cost, "request_time": requestTime, "chain": chain,
		"cache_hit": cacheHit, "job_id": jobID, "tenant_id": tenantID, "shard_id": shardID,
		"retry_attempt": retryAttempt, "retry_reason": retryReason, "page_size": pageSize,
		"related_audit_events": auditCount,
		"data_source":          "postgres:provider_usage",
	})
}

// ---- 成本与预算（A-COST-01 / A-BUDGET-01） ----

// handleAdminCostSummary 返回字符串金额与来源（§21.6：金额不使用浮点比较）。
func (s *Server) handleAdminCostSummary(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	days := s6Limit(r.URL.Query().Get("days"), 30, 365)
	summary := map[string]any{}
	var total, last24, last7 float64
	var calls, errors int
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT coalesce(sum(estimated_cost_usd),0)::float8,
		       coalesce(sum(estimated_cost_usd) FILTER (WHERE request_time > now() - interval '24 hours'),0)::float8,
		       coalesce(sum(estimated_cost_usd) FILTER (WHERE request_time > now() - interval '7 days'),0)::float8,
		       count(*)::int,
		       count(*) FILTER (WHERE status <> 'success')::int
		FROM provider_usage WHERE request_time > now() - ($1 || ' days')::interval`, strconv.Itoa(days)).
		Scan(&total, &last24, &last7, &calls, &errors); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "成本汇总失败", nil)
		return
	}
	summary["window_days"] = days
	summary["total_cost"] = formatUSD(total)
	summary["cost_24h"] = formatUSD(last24)
	summary["cost_7d"] = formatUSD(last7)
	summary["calls"] = calls
	summary["errors"] = errors
	summary["currency"] = "USD"
	summary["amount_type"] = "decimal-string"

	byProvider := make([]map[string]any, 0, 8)
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT provider, coalesce(sum(estimated_cost_usd),0)::float8, count(*)::int,
		       coalesce(sum(credits),0)::float8
		FROM provider_usage WHERE request_time > now() - ($1 || ' days')::interval
		GROUP BY provider ORDER BY 2 DESC LIMIT 50`, strconv.Itoa(days))
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var provider string
			var cost, credits float64
			var count int
			if scanErr := rows.Scan(&provider, &cost, &count, &credits); scanErr == nil {
				byProvider = append(byProvider, map[string]any{
					"provider": provider, "cost": formatUSD(cost), "calls": count, "credits": credits,
				})
			}
		}
	}
	summary["by_provider"] = byProvider

	byUsage := make([]map[string]any, 0, 8)
	usageRows, usageErr := s.deps.Store.Pool.Query(ctx, `
		SELECT coalesce(usage_type,'unknown'), coalesce(sum(estimated_cost_usd),0)::float8, count(*)::int
		FROM provider_usage WHERE request_time > now() - ($1 || ' days')::interval
		GROUP BY 1 ORDER BY 2 DESC LIMIT 50`, strconv.Itoa(days))
	if usageErr == nil {
		defer usageRows.Close()
		for usageRows.Next() {
			var usageType string
			var cost float64
			var count int
			if scanErr := usageRows.Scan(&usageType, &cost, &count); scanErr == nil {
				byUsage = append(byUsage, map[string]any{"usage_type": usageType, "cost": formatUSD(cost), "calls": count})
			}
		}
	}
	summary["by_usage_type"] = byUsage

	daily := make([]map[string]any, 0, 31)
	dailyRows, dailyErr := s.deps.Store.Pool.Query(ctx, `
		SELECT date_trunc('day', request_time)::date::text, coalesce(sum(estimated_cost_usd),0)::float8, count(*)::int
		FROM provider_usage WHERE request_time > now() - ($1 || ' days')::interval
		GROUP BY 1 ORDER BY 1`, strconv.Itoa(days))
	if dailyErr == nil {
		defer dailyRows.Close()
		for dailyRows.Next() {
			var day string
			var cost float64
			var count int
			if scanErr := dailyRows.Scan(&day, &cost, &count); scanErr == nil {
				daily = append(daily, map[string]any{"day": day, "cost": formatUSD(cost), "calls": count})
			}
		}
	}
	summary["daily"] = daily
	summary["as_of"] = s6Now().Format(time.RFC3339)
	summary["data_source"] = "postgres:provider_usage + provider_pricing"
	respondOK(w, r, summary)
}

// formatUSD 以十进制字符串返回金额，避免 JS 安全整数与浮点误差（DATA-001 同类规则）。
func formatUSD(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}

func (s *Server) handleAdminBudgets(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	rows, err := s.deps.Store.Pool.Query(ctx, `
		SELECT b.id::text, b.name, b.provider, b.period, b.limit_usd, b.warn_percent, b.enabled,
		       b.dimension, b.scope_key, b.action, b.action_threshold, b.currency, b.notes, b.revision, b.updated_at,
		       coalesce((SELECT SUM(u.estimated_cost_usd) FROM provider_usage u
		                 WHERE (b.provider='all' OR u.provider=b.provider)
		                   AND u.request_time >= date_trunc('month', now())), 0)::float8
		FROM api_control_budgets b ORDER BY b.updated_at DESC`)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "预算列表读取失败", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, 8)
	for rows.Next() {
		var id, name, provider, period, dimension, scopeKey, action, currency, notes string
		var limit, warnPercent, actionThreshold int
		var enabled bool
		var revision int64
		var updatedAt time.Time
		var spent float64
		if err := rows.Scan(&id, &name, &provider, &period, &limit, &warnPercent, &enabled, &dimension, &scopeKey,
			&action, &actionThreshold, &currency, &notes, &revision, &updatedAt, &spent); err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "预算列表读取失败", nil)
			return
		}
		utilization := 0.0
		if limit > 0 {
			utilization = spent / float64(limit)
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "provider": provider, "period": period,
			"limit_usd": limit, "warn_percent": warnPercent, "enabled": enabled,
			"dimension": dimension, "scope_key": scopeKey, "action": action, "action_threshold": actionThreshold,
			"currency": currency, "notes": notes, "revision": revision, "updated_at": updatedAt,
			"spent_usd": formatUSD(spent), "limit_usd_display": formatUSD(float64(limit)),
			"utilization": utilization, "spent_is_string": true,
		})
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

func (s *Server) handleAdminCreateBudget(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		Name            string `json:"name"`
		Provider        string `json:"provider"`
		Period          string `json:"period"`
		LimitUSD        int    `json:"limit_usd"`
		WarnPercent     int    `json:"warn_percent"`
		Dimension       string `json:"dimension"`
		ScopeKey        string `json:"scope_key"`
		Action          string `json:"action"`
		ActionThreshold int    `json:"action_threshold"`
		Notes           string `json:"notes"`
		Reason          string `json:"reason"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Name) == "" || in.LimitUSD <= 0 {
		validationError(w, r, "必须提供预算名称与正数额度", []fieldError{{Field: "name", Code: "required", Message: "必填"}, {Field: "limit_usd", Code: "invalid", Message: "必须大于 0"}})
		return
	}
	if in.Period == "" {
		in.Period = "month"
	}
	switch in.Period {
	case "day", "week", "month", "quarter":
	default:
		validationError(w, r, "period 取值非法", []fieldError{{Field: "period", Code: "invalid", Message: "day|week|month|quarter"}})
		return
	}
	if in.Action == "" {
		in.Action = "alert"
	}
	switch in.Action {
	case "alert", "throttle", "pause", "block":
	default:
		validationError(w, r, "action 取值非法", []fieldError{{Field: "action", Code: "invalid", Message: "alert|throttle|pause|block"}})
		return
	}
	if in.Dimension == "" {
		in.Dimension = "provider"
	}
	if in.WarnPercent <= 0 || in.WarnPercent > 100 {
		in.WarnPercent = 80
	}
	if in.ActionThreshold <= 0 || in.ActionThreshold > 200 {
		in.ActionThreshold = 100
	}
	if in.Provider == "" {
		in.Provider = "all"
	}
	ctx := r.Context()
	var id string
	if err := s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO api_control_budgets (name, provider, period, limit_usd, warn_percent, dimension, scope_key, action, action_threshold, notes, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id::text`,
		in.Name, in.Provider, in.Period, in.LimitUSD, in.WarnPercent, in.Dimension, in.ScopeKey, in.Action,
		in.ActionThreshold, in.Notes, s6NullUUID(PrincipalFrom(ctx).IdentityID)).Scan(&id); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "预算创建失败", nil)
		return
	}
	s.s6Audit(r, "admin.budget.create", "admin.provider", "budget", id, map[string]any{
		"name": in.Name, "provider": in.Provider, "period": in.Period, "limit_usd": in.LimitUSD,
		"action": in.Action, "action_threshold": in.ActionThreshold, "reason": in.Reason,
	})
	respondCreated(w, r, map[string]any{"id": id, "name": in.Name, "limit_usd": in.LimitUSD, "period": in.Period})
}

func (s *Server) handleAdminUpdateBudget(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := parseS6UUID(id); err != nil {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "预算不存在", nil)
		return
	}
	var in struct {
		LimitUSD        *int    `json:"limit_usd"`
		WarnPercent     *int    `json:"warn_percent"`
		Enabled         *bool   `json:"enabled"`
		Action          *string `json:"action"`
		ActionThreshold *int    `json:"action_threshold"`
		Notes           *string `json:"notes"`
		Reason          string  `json:"reason"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写修改原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	var before map[string]any
	var revision int64
	var name, provider, period string
	var limit, warn, threshold int
	var enabled bool
	var action string
	err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT name, provider, period, limit_usd, warn_percent, enabled, action, action_threshold, revision
		FROM api_control_budgets WHERE id=$1`, id).Scan(&name, &provider, &period, &limit, &warn, &enabled, &action, &threshold, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "预算不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "预算读取失败", nil)
		return
	}
	if _, ok := s6RequireRevision(w, r, revision, "预算"); !ok {
		return
	}
	before = map[string]any{"name": name, "provider": provider, "period": period, "limit_usd": limit, "warn_percent": warn, "enabled": enabled, "action": action, "action_threshold": threshold, "revision": revision}
	if _, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE api_control_budgets SET
		  limit_usd = coalesce($2, limit_usd),
		  warn_percent = coalesce($3, warn_percent),
		  enabled = coalesce($4, enabled),
		  action = coalesce($5, action),
		  action_threshold = coalesce($6, action_threshold),
		  notes = coalesce($7, notes),
		  revision = revision + 1, updated_by = $9, updated_at = now()
		WHERE id=$1`, id, in.LimitUSD, in.WarnPercent, in.Enabled, in.Action, in.ActionThreshold, in.Notes,
		s6NullUUID(PrincipalFrom(ctx).IdentityID)); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "预算更新失败", nil)
		return
	}
	after := map[string]any{"limit_usd": limit, "warn_percent": warn, "enabled": enabled, "action": action, "action_threshold": threshold, "revision": revision + 1}
	if in.LimitUSD != nil {
		after["limit_usd"] = *in.LimitUSD
	}
	if in.WarnPercent != nil {
		after["warn_percent"] = *in.WarnPercent
	}
	if in.Enabled != nil {
		after["enabled"] = *in.Enabled
	}
	if in.Action != nil {
		after["action"] = *in.Action
	}
	if in.ActionThreshold != nil {
		after["action_threshold"] = *in.ActionThreshold
	}
	s.s6AuditChange(r, "admin.budget.update", "admin.provider", "budget", id, before, after, map[string]any{"reason": in.Reason})
	respondOK(w, r, map[string]any{"id": id, "before": before, "after": after})
}

// s6ProviderQuotaSummary 返回列表行需要的额度摘要。
//
// 返回 nil 表示"该 Provider 没有额度来源"（例如 Ankr），而不是"额度为 0"：
// 列表页必须能区分这两者，否则会把"不适用"渲染成"已耗尽"。
func (s *Server) s6ProviderQuotaSummary(ctx context.Context, key string) map[string]any {
	if s.deps.Store == nil {
		return nil
	}
	snap, ok, err := s.deps.Store.LatestAccountSnapshot(ctx, key)
	if err != nil || !ok || snap == nil {
		// 没有落库快照时，运行时额度（刚同步完、还没来得及写库）仍然可用。
		if s.deps.DRPC != nil && s.deps.DRPC.Name() == key {
			status := s.deps.DRPC.QuotaStatus()
			if configured, _ := status["configured"].(bool); configured || status["has_snapshot"] == true {
				return status
			}
		}
		return nil
	}
	staleAfter := s.deps.Cfg.DRPC.ManagementStaleAfter
	if staleAfter <= 0 {
		staleAfter = 15 * time.Minute
	}
	out := map[string]any{
		"provider": key, "account_tier": snap.AccountTier,
		"balance": snap.AccountBalance, "balance_unit": snap.AccountBalanceUnit,
		"cu_spent_today": snap.CUSpentToday, "cu_day_limit": snap.CUDayLimit,
		"cu_remaining_today": snap.CURemainingToday, "rate_limit": snap.RateLimit,
		"synced_at": snap.SyncedAt, "stale": time.Since(snap.SyncedAt) > staleAfter,
		"source": snap.SyncStatus, "sync_status": snap.SyncStatus,
		"sync_error_class": snap.SyncErrorClass,
	}
	// 剩余百分比只在日上限为正时计算：上限为空/0 时显示"未设置日上限"。
	if snap.CUDayLimit != nil && *snap.CUDayLimit > 0 && snap.CUSpentToday != nil {
		remaining := *snap.CUDayLimit - *snap.CUSpentToday
		if remaining < 0 {
			remaining = 0
		}
		out["cu_remaining_percent"] = float64(remaining) / float64(*snap.CUDayLimit) * 100
	} else {
		out["cu_remaining_percent"] = nil
	}
	return out
}
