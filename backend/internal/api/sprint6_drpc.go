package api

// Solana dRPC 后台管理 API（工作流 E；实施规范 §5，开发文档 §10.2/§11.4）。
//
// 本文件承载：
//   - 额度与额度历史：drpc.Client.QuotaStatus()（运行时注入的实时额度）与
//     provider_account_snapshots（管理同步落库）按 synced_at 取新合并；
//   - 性能档位：provider_performance_profiles 的读取与 PATCH（If-Match + reason + Step-up）；
//   - 运行时状态：provider_runtime_states（没有行就返回空 items，不编造）；
//   - 命令：sync-quota（drpcmgmt 同步入口）与 probe-channels（dRPC 真实双通道探针）；
//   - 按 secret_kind 管理三类凭据（runtime_api_key / statistics_api_token / keys_api_token）。
//
// 秘密规则（规范 §11.1 / 文档 §16）：provider_secrets 只保存受控引用与摘要；
// 明文只进 0600 的受管 store，不落 PostgreSQL、不进日志、不进审计、不回显。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/drpc"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/drpcmgmt"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/route"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

const (
	// 三类凭据（规范 §11.1 / 迁移 0075 的 CHECK 约束）。
	s6SecretKindRuntimeAPIKey      = "runtime_api_key"
	s6SecretKindStatisticsAPIToken = "statistics_api_token"
	s6SecretKindKeysAPIToken       = "keys_api_token"

	// s6DefaultManagementStaleAfter 是配置缺失时的兜底新鲜度阈值（DRPC_MANAGEMENT_STALE_AFTER_SECONDS=900）。
	s6DefaultManagementStaleAfter = 15 * time.Minute

	s6QuotaHistoryDay  = 24 * time.Hour
	s6QuotaHistoryWeek = 7 * 24 * time.Hour
)

// s6SecretKinds 是固定的展示顺序：运行 Key -> 统计 Token -> Keys Token。
var s6SecretKinds = []string{s6SecretKindRuntimeAPIKey, s6SecretKindStatisticsAPIToken, s6SecretKindKeysAPIToken}

// s6ChannelProber 是真实通道探针能力（drpc.Client 实现）。
//
// 用窄接口而不是直接断言 *drpc.Client：/commands/test 与 /commands/probe-channels 共用同一能力，
// 未来别的 Provider 实现探针时无需改动调用点。
type s6ChannelProber interface {
	ProbeChannels(ctx context.Context) map[string]any
}

// ---- 路由 ----

// s6ProviderDetail 是 GET /providers/{id} 的响应：在既有 s6Provider 之上追加 dRPC 区块（只增不改）。
//
// 内嵌结构体的 json 字段会被提升，因此既有字段的形状与名称完全不变。
type s6ProviderDetail struct {
	s6Provider
	Quota                    map[string]any   `json:"quota"`
	PerformanceProfiles      []route.Profile  `json:"performance_profiles"`
	Secrets                  []map[string]any `json:"secrets"`
	ManagementSyncConfigured bool             `json:"management_sync_configured"`
}

// s6ProviderDetailEnrich 合并详情响应新增区块：quota / performance_profiles / secrets / runtime_health.channels。
func (s *Server) s6ProviderDetailEnrich(ctx context.Context, p s6Provider) s6ProviderDetail {
	profiles, err := s.s6PerformanceProfilesOrEmpty(ctx, p.Key)
	if err != nil {
		s.s6LogWarn("performance profiles read failed", "provider", p.Key, "err", err)
	}
	detail := s6ProviderDetail{
		s6Provider:               p,
		Quota:                    s.s6ProviderQuota(ctx, p.Key),
		PerformanceProfiles:      profiles,
		Secrets:                  s.s6ProviderSecretsByKind(ctx, p.Key),
		ManagementSyncConfigured: s.s6ManagementConfigured(ctx),
	}
	detail.RuntimeHealth = s.s6RuntimeHealthEnriched(ctx, p)
	return detail
}

// ---- 额度 ----

// handleAdminProviderQuota 返回 Provider 额度（规范 §5：admin:cost:read）。
func (s *Server) handleAdminProviderQuota(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	respondOK(w, r, s.s6ProviderQuota(r.Context(), chi.URLParam(r, "id")))
}

// handleAdminProviderQuotaHistory 返回额度快照历史（range=day|week）。
func (s *Server) handleAdminProviderQuotaHistory(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "id")
	window := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	since := time.Now().UTC().Add(-s6QuotaHistoryDay)
	switch window {
	case "", "day", "24h":
		window = "day"
	case "week", "7d":
		window, since = "week", time.Now().UTC().Add(-s6QuotaHistoryWeek)
	default:
		validationError(w, r, "range 取值非法", []fieldError{{Field: "range", Code: "invalid", Message: "取值 day|week"}})
		return
	}
	rows, err := s.deps.Store.AccountSnapshotHistory(r.Context(), key, since)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "额度历史读取失败", nil)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for i := range rows {
		item := s.s6QuotaFinalize(s6QuotaFromSnapshotRow(rows[i]))
		// 历史曲线需要保留失败/降级点：sync_error_class 一并透出。
		item["sync_error_class"] = rows[i].SyncErrorClass
		items = append(items, item)
	}
	respondList(w, r, map[string]any{"provider": key, "range": window, "items": items}, len(items), nil)
}

// s6ProviderQuota 合并运行时额度（QuotaStatus）与最近一次落库快照。
//
// 冲突时以更新的 synced_at 为准；落库快照缺失时也不会返回零值伪装成"没有额度"：
// stale=true + sync_status=never_synced 明确表达"还没同步过"（文档 §11.5）。
func (s *Server) s6ProviderQuota(ctx context.Context, key string) map[string]any {
	quota := s.s6QuotaFinalize(map[string]any{
		"provider":              key,
		"tier":                  "unknown",
		"account_tier":          "unknown",
		"balance":               nil,
		"balance_unit":          "",
		"cu_spent_today":        nil,
		"cu_day_limit":          nil,
		"cu_remaining_today":    nil,
		"rate_limit":            nil,
		"synced_at":             nil,
		"sync_status":           "never_synced",
		"source":                "none",
		"management_configured": s.s6ManagementConfigured(ctx),
	})

	var (
		snapshotQuota map[string]any
		snapshotAt    time.Time
		hasSnapshot   bool
	)
	if s.deps.Store != nil {
		row, ok, err := s.deps.Store.LatestAccountSnapshot(ctx, key)
		if err != nil {
			s.s6LogWarn("latest account snapshot read failed", "provider", key, "err", err)
		} else if ok && row != nil {
			snapshotQuota, snapshotAt, hasSnapshot = s6QuotaFromSnapshotRow(*row), row.SyncedAt, true
		}
	}

	var (
		runtimeQuota map[string]any
		runtimeAt    time.Time
		hasRuntime   bool
	)
	if client, ok := s.s6DRPCClient(key); ok {
		status := client.QuotaStatus()
		runtimeQuota = s6QuotaFromRuntimeStatus(status)
		if parsed, ok := s6TimeValue(runtimeQuota["synced_at"]); ok {
			runtimeAt = parsed
		}
		hasRuntime = true
	}

	base, baseAt, baseHas := snapshotQuota, snapshotAt, hasSnapshot
	fallback, fallbackAt, fallbackHas := runtimeQuota, runtimeAt, hasRuntime
	if fallbackHas && (!baseHas || fallbackAt.After(baseAt)) {
		base, fallback = fallback, base
		baseHas, fallbackHas = fallbackHas, baseHas
	}
	if baseHas {
		quota = s6QuotaMerge(base, fallback)
	} else if fallbackHas {
		quota = s6QuotaMerge(fallback, nil)
	}
	quota["provider"] = key
	quota["management_configured"] = s.s6ManagementConfigured(ctx)
	if baseHas {
		quota["source"] = s6StringValue(quota["source"])
	}
	return s.s6QuotaFinalize(quota)
}

// s6QuotaFromSnapshotRow 把 provider_account_snapshots 的一行投影成额度口径。
// balance 用十进制文本原样透出（numeric(38,18) 不能经 float64 丢精度）。
func s6QuotaFromSnapshotRow(row store.AccountSnapshotRow) map[string]any {
	tier := strings.TrimSpace(row.AccountTier)
	if tier == "" {
		tier = "unknown"
	}
	out := map[string]any{
		"tier":               tier,
		"account_tier":       tier,
		"balance":            nil,
		"balance_unit":       row.AccountBalanceUnit,
		"cu_spent_today":     nil,
		"cu_day_limit":       nil,
		"cu_remaining_today": nil,
		"rate_limit":         nil,
		"synced_at":          row.SyncedAt,
		"sync_status":        row.SyncStatus,
		"source":             row.Source,
		"key_id":             row.KeyID,
		"key_name":           row.KeyName,
		"key_enabled":        row.KeyEnabled,
		"source_updated_at":  row.SourceUpdatedAt,
	}
	if strings.TrimSpace(row.AccountBalance) != "" {
		out["balance"] = row.AccountBalance
	}
	if row.CUSpentToday != nil {
		out["cu_spent_today"] = *row.CUSpentToday
	}
	if row.CUDayLimit != nil {
		out["cu_day_limit"] = *row.CUDayLimit
	}
	if row.CURemainingToday != nil {
		out["cu_remaining_today"] = *row.CURemainingToday
	}
	if row.RateLimit != nil {
		out["rate_limit"] = *row.RateLimit
	}
	return out
}

// s6QuotaFromRuntimeStatus 把 drpc.Client.QuotaStatus() 投影成同一额度口径。
// tier 优先取快照层级（tier_source），它比配置层级（tier）更接近账户事实。
func s6QuotaFromRuntimeStatus(status map[string]any) map[string]any {
	if status == nil {
		return nil
	}
	tier := strings.TrimSpace(s6StringValue(status["tier_source"]))
	if tier == "" {
		tier = strings.TrimSpace(s6StringValue(status["tier"]))
	}
	if tier == "" {
		tier = "unknown"
	}
	out := map[string]any{
		"tier":               tier,
		"account_tier":       tier,
		"balance":            status["balance"],
		"balance_unit":       s6StringValue(status["balance_unit"]),
		"cu_spent_today":     status["cu_spent_today"],
		"cu_day_limit":       status["cu_day_limit"],
		"cu_remaining_today": status["cu_remaining_today"],
		"rate_limit":         status["rate_limit"],
		"key_enabled":        status["key_enabled"],
		"source":             s6StringValue(status["source"]),
		"sync_status":        "ok",
	}
	if syncedAt, ok := s6TimeValue(status["synced_at"]); ok {
		out["synced_at"] = syncedAt
	} else {
		out["synced_at"] = nil
		out["sync_status"] = "never_synced"
	}
	// 运行时有效档位（钳制后的并发/速率/CU 与降级原因）：档位表里的 cu_per_minute 是
	// "名义额度"，后台必须与这里的运行时有效值分栏展示，不能当成实际上限。
	for _, key := range []string{"effective", "blocked", "block_reason", "effective_reason", "used_percent"} {
		if value, ok := status[key]; ok {
			out[key] = value
		}
	}
	return out
}

// s6QuotaMerge 以 base 为准合并 fallback：base 里为 nil/空串的字段才由 fallback 补齐。
// 布尔 false 与数值 0 是有效业务值（如 key_enabled=false），必须保留 base 的判断。
func s6QuotaMerge(base, fallback map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(fallback))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range fallback {
		current, exists := out[k]
		if exists && current != nil && current != "" {
			continue
		}
		if v == nil || v == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// s6QuotaFinalize 统一收口：tier 别名、日上限为空/0 时剩余量为 null、stale 由
// ManagementStaleAfter 判定（规范 §5 / 文档 §11.2）。
func (s *Server) s6QuotaFinalize(quota map[string]any) map[string]any {
	if quota == nil {
		quota = map[string]any{}
	}
	tier := strings.TrimSpace(s6StringValue(quota["tier"]))
	if tier == "" {
		tier = strings.TrimSpace(s6StringValue(quota["account_tier"]))
	}
	if tier == "" {
		tier = "unknown"
	}
	quota["tier"], quota["account_tier"] = tier, tier

	if strings.TrimSpace(s6StringValue(quota["balance_unit"])) == "" && quota["balance"] != nil {
		quota["balance_unit"] = "provider_unit"
	}

	limit, hasLimit := s6Int64Value(quota["cu_day_limit"])
	if !hasLimit || limit <= 0 {
		// 没有设置日上限时不得显示"剩余 0"：剩余量恒为 null。
		quota["cu_remaining_today"] = nil
	} else if remaining, ok := s6Int64Value(quota["cu_remaining_today"]); !ok || remaining < 0 {
		if spent, ok := s6Int64Value(quota["cu_spent_today"]); ok {
			remaining = limit - spent
			if remaining < 0 {
				remaining = 0
			}
			quota["cu_remaining_today"] = remaining
		}
	}

	syncedAt, ok := s6TimeValue(quota["synced_at"])
	if !ok {
		quota["synced_at"] = nil
		quota["stale"] = true
		if strings.TrimSpace(s6StringValue(quota["sync_status"])) == "" {
			quota["sync_status"] = "never_synced"
		}
	} else {
		quota["synced_at"] = syncedAt.UTC()
		quota["stale"] = time.Since(syncedAt) > s.s6QuotaStaleAfter()
	}
	if strings.TrimSpace(s6StringValue(quota["source"])) == "" {
		quota["source"] = "none"
	}
	if strings.TrimSpace(s6StringValue(quota["sync_status"])) == "" {
		quota["sync_status"] = "ok"
	}
	return quota
}

// s6QuotaStaleAfter 取管理快照新鲜度阈值（DRPC_MANAGEMENT_STALE_AFTER_SECONDS，默认 15 分钟）。
func (s *Server) s6QuotaStaleAfter() time.Duration {
	if s.deps.Cfg != nil && s.deps.Cfg.DRPC.ManagementStaleAfter > 0 {
		return s.deps.Cfg.DRPC.ManagementStaleAfter
	}
	return s6DefaultManagementStaleAfter
}

// s6ManagementConfigured 判断管理同步是否具备两个管理 Token（Statistics + Keys）。
// 未接线密钥管理器时退化为"管理服务已装配"。
func (s *Server) s6ManagementConfigured(ctx context.Context) bool {
	if s.deps.DRPCManagement == nil {
		return false
	}
	if s.deps.Secrets == nil || s.deps.Cfg == nil {
		return true
	}
	stats := s.deps.Secrets.Describe(ctx, s.deps.Cfg.DRPC.StatisticsTokenRef)
	keys := s.deps.Secrets.Describe(ctx, s.deps.Cfg.DRPC.KeysTokenRef)
	return stats.Configured && keys.Configured
}

// ---- 性能档位 ----

// handleAdminProviderPerformanceProfiles 返回 Provider 的全部性能档位。
func (s *Server) handleAdminProviderPerformanceProfiles(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "id")
	items, err := s.s6PerformanceProfilesOrEmpty(r.Context(), key)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "性能档位读取失败", nil)
		return
	}
	respondList(w, r, map[string]any{"provider": key, "items": items}, len(items), nil)
}

// handleAdminProviderPerformanceProfileUpdate 更新一行档位：
// If-Match（缺失或不匹配 409 + 当前 revision）、reason 必填、TURBO 或提高硬上限需 Step-up。
func (s *Server) handleAdminProviderPerformanceProfileUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "id")
	chain := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "chain")))
	operation := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "operation")))
	mode := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "mode")))
	if !s6ValidProfileMode(mode) {
		validationError(w, r, "mode 取值非法", []fieldError{{Field: "mode", Code: "invalid", Message: "取值 safe|fast|turbo"}})
		return
	}
	var in s6PerformanceProfilePatch
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写修改原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	ctx := r.Context()
	current, found, err := s.s6FindPerformanceProfile(ctx, key, chain, operation, mode)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "性能档位读取失败", nil)
		return
	}
	if !found {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "性能档位不存在", map[string]any{
			"provider": key, "chain": chain, "operation": operation, "mode": mode,
		})
		return
	}

	// If-Match：缺失与不匹配都按 409 返回，并附当前 revision（规范 §5、文档 §10.2）。
	ifMatch, hasMatch := s6IfMatch(r)
	if !hasMatch || ifMatch != current.Revision {
		respondError(w, r, http.StatusConflict, "CONFLICT", "性能档位已在其他会话被修改，请刷新后重试", map[string]any{
			"current_revision":  current.Revision,
			"your_revision":     ifMatch,
			"if_match_required": !hasMatch,
		})
		return
	}

	updated, fields := in.apply(current)
	if len(fields) > 0 {
		validationError(w, r, "性能档位参数非法", fields)
		return
	}

	// TURBO 或提高硬上限是高风险变更：必须二次身份确认（文档 §10.2）。
	stepUp := mode == route.ModeTurbo || (in.HardCeiling != nil && *in.HardCeiling > current.HardCeiling)
	if stepUp && !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.provider.performance") {
		return
	}
	updated.ProviderKey, updated.Chain, updated.OperationClass, updated.Mode = current.ProviderKey, current.Chain, current.OperationClass, current.Mode

	after, err := s.deps.Store.UpdatePerformanceProfile(ctx, updated, strings.TrimSpace(in.Reason), PrincipalFrom(ctx).IdentityID)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "性能档位不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "性能档位更新失败", nil)
		return
	}
	// 审计不在 API 层重复写：store.UpdatePerformanceProfile 已把 reason / actor / before / after
	// 写入 audit_events（action=provider.performance.update）。
	respondOK(w, r, map[string]any{
		"provider": key, "profile": after, "items": []route.Profile{after}, "revision": after.Revision,
		"reason_recorded": true, "audit_action": "provider.performance.update", "step_up": stepUp,
	})
}

// s6PerformanceProfilePatch 是 PATCH 请求体：只接受显式给出的字段（PATCH 语义）。
type s6PerformanceProfilePatch struct {
	ShardConcurrency      *int     `json:"shard_concurrency"`
	RequestConcurrency    *int     `json:"request_concurrency"`
	RequestsPerMinute     *float64 `json:"requests_per_minute"`
	RequestsPerSecond     *float64 `json:"requests_per_second"`
	CUPerMinute           *int64   `json:"cu_per_minute"`
	PageSize              *int     `json:"page_size"`
	BatchSize             *int     `json:"batch_size"`
	RequestWeight         *int     `json:"request_weight"`
	RequestTimeoutSeconds *int     `json:"request_timeout_seconds"`
	HardCeiling           *int     `json:"hard_ceiling"`
	VerificationSource    *string  `json:"verification_source"`
	Reason                string   `json:"reason"`
	StepUpPassword        string   `json:"step_up_password"`
}

// apply 把请求体覆盖到现有档位上，返回字段级校验错误（无错误时 fields 为空）。
func (in s6PerformanceProfilePatch) apply(p route.Profile) (route.Profile, []fieldError) {
	fields := make([]fieldError, 0, 4)
	invalid := func(field, message string) {
		fields = append(fields, fieldError{Field: field, Code: "invalid", Message: message})
	}

	if in.ShardConcurrency != nil {
		if *in.ShardConcurrency < 0 {
			invalid("shard_concurrency", "必须 >= 0")
		} else {
			p.ShardConcurrency = *in.ShardConcurrency
		}
	}
	if in.RequestConcurrency != nil {
		if *in.RequestConcurrency < 1 {
			invalid("request_concurrency", "必须 >= 1")
		} else {
			p.RequestConcurrency = *in.RequestConcurrency
		}
	}
	if in.RequestsPerMinute != nil {
		if !s6ValidRate(*in.RequestsPerMinute) {
			invalid("requests_per_minute", "必须是非负有限数值")
		} else {
			p.RequestsPerMinute = *in.RequestsPerMinute
		}
	}
	if in.RequestsPerSecond != nil {
		if !s6ValidRate(*in.RequestsPerSecond) {
			invalid("requests_per_second", "必须是非负有限数值")
		} else {
			p.RequestsPerSecond = *in.RequestsPerSecond
		}
	}
	if in.CUPerMinute != nil {
		if *in.CUPerMinute < 0 {
			invalid("cu_per_minute", "必须 >= 0")
		} else {
			p.CUPerMinute = *in.CUPerMinute
		}
	}
	if in.PageSize != nil {
		if *in.PageSize < 0 {
			invalid("page_size", "必须 >= 0")
		} else {
			p.PageSize = *in.PageSize
		}
	}
	if in.BatchSize != nil {
		if *in.BatchSize < 1 {
			invalid("batch_size", "必须 >= 1")
		} else {
			p.BatchSize = *in.BatchSize
		}
	}
	if in.RequestWeight != nil {
		if *in.RequestWeight < 1 {
			invalid("request_weight", "必须 >= 1")
		} else {
			p.RequestWeight = *in.RequestWeight
		}
	}
	if in.RequestTimeoutSeconds != nil {
		if *in.RequestTimeoutSeconds < 1 {
			invalid("request_timeout_seconds", "必须 >= 1")
		} else {
			p.RequestTimeoutSeconds = *in.RequestTimeoutSeconds
		}
	}
	if in.HardCeiling != nil {
		if *in.HardCeiling < 1 {
			invalid("hard_ceiling", "必须 >= 1")
		} else {
			p.HardCeiling = *in.HardCeiling
		}
	}
	if in.VerificationSource != nil {
		p.VerificationSource = strings.TrimSpace(*in.VerificationSource)
	}
	return p, fields
}

// s6FindPerformanceProfile 按 (provider, chain, operation_class, mode) 定位一行档位。
// 路径不含 dataset：优先匹配 dataset='*' 的通配行，其次第一条（表按 chain/dataset/operation/mode 稳定排序）。
func (s *Server) s6FindPerformanceProfile(ctx context.Context, key, chain, operation, mode string) (route.Profile, bool, error) {
	profiles, err := s.s6PerformanceProfilesOrEmpty(ctx, key)
	if err != nil {
		return route.Profile{}, false, err
	}
	var match *route.Profile
	for i := range profiles {
		p := profiles[i]
		if !strings.EqualFold(p.Chain, chain) || !strings.EqualFold(p.OperationClass, operation) || !strings.EqualFold(p.Mode, mode) {
			continue
		}
		if match == nil || p.Dataset == "*" {
			match = &profiles[i]
		}
		if p.Dataset == "*" {
			break
		}
	}
	if match == nil {
		return route.Profile{}, false, nil
	}
	return *match, true, nil
}

// s6PerformanceProfilesOrEmpty 读取档位列表；无行时返回空切片（前端不需要区分 null 与空）。
func (s *Server) s6PerformanceProfilesOrEmpty(ctx context.Context, key string) ([]route.Profile, error) {
	if s.deps.Store == nil {
		return []route.Profile{}, nil
	}
	profiles, err := s.deps.Store.ListPerformanceProfiles(ctx, key)
	if err != nil {
		return nil, err
	}
	if profiles == nil {
		return []route.Profile{}, nil
	}
	return profiles, nil
}

func s6ValidProfileMode(mode string) bool {
	switch mode {
	case route.ModeSafe, route.ModeFast, route.ModeTurbo:
		return true
	}
	return false
}

func s6ValidRate(v float64) bool {
	return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// ---- 运行时状态 ----

// handleAdminProviderRuntimeStates 返回 provider_runtime_states 的运行档位与降级原因。
func (s *Server) handleAdminProviderRuntimeStates(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "id")
	items, err := s.s6RuntimeStates(r.Context(), key)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "运行状态读取失败", nil)
		return
	}
	respondList(w, r, map[string]any{"provider": key, "items": items}, len(items), nil)
}

// s6RuntimeStates 读取运行状态；没有行时返回空 items（不编造档位）。
func (s *Server) s6RuntimeStates(ctx context.Context, key string) ([]map[string]any, error) {
	if s.deps.Store == nil {
		return []map[string]any{}, nil
	}
	states, err := s.deps.Store.RuntimeStates(ctx, key)
	if err != nil {
		return nil, err
	}
	if states == nil {
		return []map[string]any{}, nil
	}
	return states, nil
}

// s6RuntimeHealthEnriched 在既有 runtime_health 上补 channels；没有既有健康结果时
// 经运行时注册表取实现并调用 Health（与 s6RuntimeHealth 同口径）。
func (s *Server) s6RuntimeHealthEnriched(ctx context.Context, p s6Provider) map[string]any {
	health := p.RuntimeHealth
	if health == nil {
		if prov, ok := s.s6RuntimeProvider(p.Key); ok {
			status := prov.Health(ctx)
			caps := prov.Capabilities()
			health = map[string]any{
				"state": string(status.State), "last_check": status.LastCheck, "error": status.Error,
				"consecutive_failures": status.ConsecutiveFailure, "recent_error_rate": status.RecentErrorRate,
				"current_concurrency": status.CurrentConcurrency,
				"declared_capabilities": map[string]any{
					"name": caps.Name, "datasets": caps.Datasets, "max_page_size": caps.MaxPageSize,
					"supports_time_range": caps.SupportsTimeRange,
				},
				"source": "runtime:provider.Health",
			}
		}
	}
	channels := s.s6RuntimeChannels(ctx, p.Key)
	if len(channels) == 0 {
		return health
	}
	if health == nil {
		health = map[string]any{"state": "", "source": "postgres:provider_runtime_states"}
	}
	health["channels"] = channels
	return health
}

// s6RuntimeChannels 汇总 Provider 的通道状态：
//  1. drpc.Client.QuotaStatus() 里若已给出 channels（运行时注入），优先采用；
//  2. provider_runtime_states.channel_states 是下载引擎心跳写入的真实通道状态；
//  3. 都没有时不编造 NORMAL：注册表声明的通道标 UNKNOWN。
func (s *Server) s6RuntimeChannels(ctx context.Context, key string) map[string]any {
	out := map[string]any{}
	if client, ok := s.s6DRPCClient(key); ok {
		if channels, ok := client.QuotaStatus()["channels"].(map[string]any); ok {
			for channel, state := range channels {
				out[channel] = s6ChannelState(state)
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	if states, err := s.s6RuntimeStates(ctx, key); err == nil {
		for _, state := range states {
			raw, _ := state["channel_states"].(map[string]any)
			for channel, value := range raw {
				if _, exists := out[channel]; !exists {
					out[channel] = s6ChannelState(value)
				}
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if s.deps.Routes != nil {
		for _, rp := range s.deps.Routes.List() {
			if !strings.EqualFold(rp.Key, strings.TrimSpace(key)) {
				continue
			}
			for _, channel := range rp.Channels {
				out[channel] = "UNKNOWN"
			}
		}
	}
	return out
}

// s6ChannelState 把 channel_states 的值归一为字符串状态，前端按状态上色，不能渲染对象。
func s6ChannelState(raw any) string {
	switch value := raw.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			return value
		}
	case map[string]any:
		for _, key := range []string{"state", "status", "health"} {
			if state, ok := value[key].(string); ok && strings.TrimSpace(state) != "" {
				return state
			}
		}
	default:
		// 结构体形态（如 drpc.channelProbe）：JSON 往返后再取 state。
		if probe := s6ProbeMap(raw); probe != nil {
			for _, key := range []string{"state", "status", "health"} {
				if state, ok := probe[key].(string); ok && strings.TrimSpace(state) != "" {
					return state
				}
			}
		}
	}
	return "UNKNOWN"
}

// ---- 命令：sync-quota ----

// handleAdminProviderSyncQuota 触发一次额度同步（admin:provider:update）。
//
// 文档 §11.3：手工同步 60 秒内返回上次快照；本响应用 debounced 明确告知本次是否复用了快照。
// 未配置管理 Token 时返回 200 + configured=false，而不是 500：这是配置缺失，不是故障。
func (s *Server) handleAdminProviderSyncQuota(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "id")
	if s.deps.DRPCManagement == nil {
		s.s6Audit(r, "admin.provider.sync_quota", "admin.provider", "provider", key, map[string]any{
			"configured": false, "result": "not_configured",
		})
		respondOK(w, r, s6SyncQuotaUnconfigured(key))
		return
	}
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	start := time.Now().UTC()
	// SyncDebounced 是后台手工同步入口：60 秒内直接返回上次快照（规范 §1.5）。
	// 运行时的额度注入由 app 经 drpcmgmt.Options.OnSnapshot 接线，这里不重复注入。
	snapshot, syncErr := s.deps.DRPCManagement.SyncDebounced(ctx)
	var summary map[string]any
	debounced := false
	if syncErr == nil {
		summary = s6QuotaSummaryFromMgmtSnapshot(snapshot)
		debounced = s6SnapshotReused(start, snapshot.SyncedAt)
	} else {
		last, lookErr := s.deps.DRPCManagement.LastSnapshot(ctx)
		if lookErr != nil || last == nil {
			s.s6Audit(r, "admin.provider.sync_quota", "admin.provider", "provider", key, map[string]any{
				"configured": true, "result": "failed", "error_class": "management_sync_failed",
			})
			respondError(w, r, http.StatusBadGateway, "DRPC_MANAGEMENT_SYNC_FAILED", "额度同步失败，请稍后重试", map[string]any{"provider": key})
			return
		}
		summary = s6QuotaSummaryFromMgmtSnapshot(*last)
		summary["sync_error_class"] = "management_sync_failed"
		summary["message"] = "额度同步失败，展示上次快照"
		debounced = true
	}
	status := strings.TrimSpace(s6StringValue(summary["sync_status"]))
	configured := status != drpcmgmt.SyncStatusNoToken
	s.s6Audit(r, "admin.provider.sync_quota", "admin.provider", "provider", key, map[string]any{
		"configured": configured, "result": status, "debounced": debounced,
		"synced_at": summary["synced_at"], "source": summary["source"],
		"cu_spent_today": summary["cu_spent_today"], "cu_day_limit": summary["cu_day_limit"],
		"sync_error_class": summary["sync_error_class"],
	})
	body := map[string]any{
		"provider":              key,
		"configured":            configured,
		"management_configured": configured,
		"debounced":             debounced,
		"snapshot":              summary,
		"quota":                 summary,
		"sync_status":           status,
		"synced_at":             summary["synced_at"],
		"source":                summary["source"],
	}
	if !configured {
		body["message"] = "额度同步未配置"
	}
	respondOK(w, r, body)
}

// s6SyncQuotaUnconfigured 是"管理同步未配置"的稳定兼容响应（HTTP 200）。
func s6SyncQuotaUnconfigured(key string) map[string]any {
	return map[string]any{
		"provider":              key,
		"configured":            false,
		"management_configured": false,
		"debounced":             false,
		"snapshot":              nil,
		"quota":                 nil,
		"sync_status":           "ok_no_management_token",
		"message":               "额度同步未配置",
	}
}

// s6QuotaSummaryFromMgmtSnapshot 把 drpcmgmt.Snapshot 投影成后台额度口径（不含任何凭据）。
func s6QuotaSummaryFromMgmtSnapshot(snapshot drpcmgmt.Snapshot) map[string]any {
	tier := strings.TrimSpace(snapshot.Tier)
	if tier == "" {
		tier = "unknown"
	}
	out := map[string]any{
		"tier":               tier,
		"account_tier":       tier,
		"balance":            nil,
		"balance_unit":       snapshot.BalanceUnit,
		"cu_spent_today":     nil,
		"cu_day_limit":       nil,
		"cu_remaining_today": nil,
		"rate_limit":         nil,
		"synced_at":          snapshot.SyncedAt,
		"sync_status":        snapshot.SyncStatus,
		"sync_error_class":   snapshot.SyncErrorClass,
		"source":             snapshot.Source,
		"key_id":             snapshot.KeyID,
		"key_name":           snapshot.KeyName,
		"key_enabled":        snapshot.KeyEnabled,
		"requests_hour":      snapshot.RequestsHour,
		"requests_day":       snapshot.RequestsDay,
		"cu_hour":            snapshot.CUHour,
		"cu_day":             snapshot.CUDay,
	}
	if balance := snapshot.BalanceString(); balance != "" {
		// BalanceString 优先返回上游原文（保住 numeric(38,18) 的尾零），decimal 规范化只作兜底。
		out["balance"] = balance
	}
	if snapshot.SourceUpdatedAt != nil {
		out["source_updated_at"] = *snapshot.SourceUpdatedAt
	}
	if snapshot.CUSpentToday != nil {
		out["cu_spent_today"] = *snapshot.CUSpentToday
	}
	if snapshot.CUDayLimit != nil {
		out["cu_day_limit"] = *snapshot.CUDayLimit
	}
	if snapshot.CURemainingToday != nil {
		out["cu_remaining_today"] = *snapshot.CURemainingToday
	}
	if snapshot.RateLimit != nil {
		out["rate_limit"] = *snapshot.RateLimit
	}
	return out
}

// s6SnapshotReused 判断 sync 返回的是否是缓存快照：本次调用开始后才写入的快照
// 一定晚于 start；早于 start 的（含零值）说明服务端做了 60 秒去重。
func s6SnapshotReused(start, syncedAt time.Time) bool {
	if syncedAt.IsZero() {
		return true
	}
	return syncedAt.Before(start.Add(-2 * time.Second))
}

// ---- 命令：probe-channels ----

// handleAdminProviderProbeChannels 执行 dRPC 真实双通道探针（Data API 低成本调用 + JSON-RPC getHealth）。
// 非运行时 Provider 返回 not_runtime_provider 兼容结构（与 /commands/test 同一错误语义）。
func (s *Server) handleAdminProviderProbeChannels(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	key := chi.URLParam(r, "id")
	var in s6ProviderCommandInput
	if err := decodeJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	ctx := r.Context()
	meta := metaFrom(ctx)
	start := time.Now()
	prober, ok := s.s6ChannelProber(key)
	if !ok {
		respondOK(w, r, map[string]any{
			"provider": key, "method": "Provider.ProbeChannels", "ok": false,
			"error_class": "not_runtime_provider", "state": "UNKNOWN",
			"message":    "该 Provider 由配置声明但当前进程未加载支持通道探针的运行时实现",
			"trace_id":   meta.TraceID,
			"checked_at": start.UTC().Format(time.RFC3339),
		})
		return
	}
	channels := prober.ProbeChannels(ctx)
	result := map[string]any{
		"provider": key, "method": "Provider.ProbeChannels", "trace_id": meta.TraceID,
		"checked_at": start.UTC().Format(time.RFC3339), "channels": channels,
		"ok": s6ChannelsOK(channels),
	}
	latency := time.Since(start)
	result["latency_ms"] = latency.Milliseconds()
	errClass := ""
	if okValue, _ := result["ok"].(bool); !okValue {
		errClass = "channel_probe_failed"
		result["error_class"] = errClass
	} else {
		result["error_class"] = ""
	}
	if body, err := json.Marshal(result); err == nil {
		_, _ = s.deps.Store.Pool.Exec(ctx, `UPDATE provider_descriptors SET last_test_at=now(), last_test_result=$2::jsonb, updated_at=now() WHERE provider_key=$1`, key, string(body))
	}
	status := "success"
	if errClass != "" {
		status = "error"
	}
	// 用量记录与 /commands/test 同一口径（usage_type=provider_test，provider=运行时 Key）。
	_ = s.deps.Store.InsertUsage(ctx, model.UsageRow{
		Provider: key, Method: "probe_channels", Dataset: "control_plane", RequestTime: start,
		DurationMs: int(latency.Milliseconds()), Status: status, HTTPStatus: 200,
		UsageType: "provider_test", RequestID: meta.RequestID, TraceID: meta.TraceID,
		APIError: errClass,
	})
	s.s6Audit(r, "admin.provider.probe_channels", "admin.provider", "provider", key, map[string]any{
		"ok": result["ok"], "channels": s6ChannelStates(channels), "latency_ms": result["latency_ms"],
		"error_class": errClass, "trace_id": meta.TraceID, "reason": in.Reason,
	})
	respondOK(w, r, result)
}

// s6ProbeMap 把通道探针结果归一成 map。
//
// drpc 的 ProbeChannels 返回的通道值是包内结构体 channelProbe（不是 map[string]any），
// 直接类型断言必然失败，会把"两条通道都失败"误判成成功；探针结果本来就是 JSON 契约，
// 因此对非 map 形态做一次 JSON 往返取值。
func s6ProbeMap(raw any) map[string]any {
	switch value := raw.(type) {
	case nil:
		return nil
	case map[string]any:
		return value
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		out := map[string]any{}
		if err := json.Unmarshal(encoded, &out); err != nil {
			return nil
		}
		return out
	}
}

// s6ChannelsOK 判断所有通道探针是否都显式成功（fail closed：拿不到 ok 字段就不算成功）。
func s6ChannelsOK(channels map[string]any) bool {
	if len(channels) == 0 {
		return false
	}
	checked, successes := 0, 0
	for channel, raw := range channels {
		if channel == "checked_at" || channel == "cached" {
			continue
		}
		probe := s6ProbeMap(raw)
		if probe == nil {
			continue
		}
		checked++
		if okValue, has := probe["ok"].(bool); has && okValue {
			successes++
		}
	}
	return checked > 0 && successes == checked
}

// s6ChannelStates 提取通道 -> 状态字符串的摘要，供审计（不含任何 Provider 原文）。
func s6ChannelStates(channels map[string]any) map[string]string {
	out := map[string]string{}
	for channel, raw := range channels {
		if channel == "checked_at" || channel == "cached" {
			continue
		}
		probe := s6ProbeMap(raw)
		state, _ := probe["state"].(string)
		out[channel] = s6ChannelState(state)
	}
	return out
}

// ---- 分类密钥 ----

// handleAdminProviderSecretSet 设置/轮换某个 secret_kind 的凭据（PUT /providers/{id}/secrets/{kind}）。
//
// body 二选一：
//   - value：明文，只写受管 store（0600），得到 store://<provider>/<kind> 引用；
//   - secret_ref：只接受 env:// 或 store:// 前缀，禁止把明文塞进 secret_ref。
//
// 明文永不落 PostgreSQL、永不进审计、永不回显。
func (s *Server) handleAdminProviderSecretSet(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "id")
	kind := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "kind")))
	if !s6ValidSecretKind(kind) {
		validationError(w, r, "secret_kind 取值非法", []fieldError{{Field: "secret_kind", Code: "invalid", Message: "取值 runtime_api_key|statistics_api_token|keys_api_token"}})
		return
	}
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		Value          string `json:"value"`
		SecretRef      string `json:"secret_ref"`
		Reason         string `json:"reason"`
		StepUpPassword string `json:"step_up_password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	// 密钥写入是高风险管理动作：Step-up 必须（规范 §5）。
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.provider.secret_set") {
		return
	}
	ctx := r.Context()
	plaintext := in.Value
	ref := strings.TrimSpace(in.SecretRef)
	hasValue := strings.TrimSpace(plaintext) != ""
	hasRef := ref != ""
	if hasValue == hasRef {
		validationError(w, r, "value 与 secret_ref 必须二选一", []fieldError{{Field: "value", Code: "invalid", Message: "二选一"}, {Field: "secret_ref", Code: "invalid", Message: "二选一"}})
		return
	}

	// Provider 必须存在：provider_secrets 有外键；也不能在明文已经写盘之后才发现 Provider 不存在。
	if !s.s6ProviderExists(ctx, key) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "Provider 不存在", nil)
		return
	}

	var configured bool
	noteParts := make([]string, 0, 2)
	if reason := strings.TrimSpace(in.Reason); reason != "" {
		noteParts = append(noteParts, reason)
	}
	if hasValue {
		if s.deps.Secrets == nil {
			respondError(w, r, http.StatusServiceUnavailable, "SECRET_STORE_UNAVAILABLE", "受管密钥存储未接线，无法写入明文密钥", nil)
			return
		}
		storedRef, _, err := s.deps.Secrets.Put(key, kind, plaintext)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "受管密钥写入失败", nil)
			return
		}
		ref, configured = storedRef, true
		noteParts = append(noteParts, "明文写入受管 store："+s6MaskRef(storedRef))
	} else {
		if !s6SecretRefAllowed(ref) {
			validationError(w, r, "secret_ref 只接受 env:// 或 store:// 引用", []fieldError{{Field: "secret_ref", Code: "invalid", Message: "env://NAME 或 store://<provider>/<kind>"}})
			return
		}
		if s.deps.Secrets == nil {
			// Describe 不可用时留空并在 note 标注：不假装已经校验过引用。
			noteParts = append(noteParts, "受管密钥解析器未接线：prefix/fingerprint 未校验")
		}
	}

	// 引用的前缀与指纹只从解析器取；解析器不可用时留空。
	prefix, fingerprint := "", ""
	if s.deps.Secrets != nil {
		status := s.deps.Secrets.Describe(ctx, ref)
		prefix, fingerprint = status.Prefix, status.Fingerprint
		if hasRef {
			configured = status.Configured
			if status.Configured {
				noteParts = append(noteParts, "prefix="+status.Prefix)
			} else {
				noteParts = append(noteParts, "引用当前解析不到明文（未配置）")
			}
		}
	}
	if len(noteParts) == 0 {
		noteParts = append(noteParts, "管理 API 设置")
	}
	if err := s.deps.Store.UpsertSecretOfKind(ctx, key, kind, ref, prefix, fingerprint, strings.Join(noteParts, "；"), PrincipalFrom(ctx).IdentityID); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "密钥写入失败", nil)
		return
	}
	source := "secret_ref"
	if hasValue {
		source = "value"
	}
	s.s6Audit(r, "admin.provider.secret_set", "admin.provider", "provider_secret", key+":"+kind, map[string]any{
		"provider": key, "secret_kind": kind, "secret_ref_masked": s6MaskRef(ref),
		"prefix": prefix, "fingerprint": fingerprint, "configured": configured,
		"source": source, "plaintext_stored": hasValue, "plaintext_returned": false,
		"reason": strings.TrimSpace(in.Reason),
	})
	respondOK(w, r, map[string]any{
		"secret_kind": kind,
		"ref":         s6MaskRef(ref),
		"secret_ref":  s6MaskRef(ref),
		"prefix":      prefix,
		"fingerprint": fingerprint,
		"configured":  configured,
	})
}

// s6ProviderSecretsByKind 返回三类凭据的摘要（每个 kind 最多一条，引用名已掩码）。
func (s *Server) s6ProviderSecretsByKind(ctx context.Context, key string) []map[string]any {
	items := make([]map[string]any, 0, len(s6SecretKinds))
	if s.deps.Store == nil {
		return items
	}
	byKind, err := s.deps.Store.ListProviderSecretsByKind(ctx, key)
	if err != nil {
		s.s6LogWarn("provider secrets by kind read failed", "provider", key, "err", err)
		return items
	}
	order := append([]string(nil), s6SecretKinds...)
	extra := make([]string, 0, len(byKind))
	for kind := range byKind {
		if !s6ValidSecretKind(kind) {
			extra = append(extra, kind)
		}
	}
	sort.Strings(extra)
	order = append(order, extra...)
	for _, kind := range order {
		row, ok := byKind[kind]
		if !ok {
			items = append(items, map[string]any{
				"secret_kind": kind, "configured": false, "status": "missing",
				"secret_ref": "", "prefix": "", "fingerprint": "",
				"rotated_at": nil, "last_used_at": nil, "plaintext_available": false,
			})
			continue
		}
		status := strings.TrimSpace(s6StringValue(row["status"]))
		ref := s6StringValue(row["secret_ref"])
		item := map[string]any{
			"secret_kind":         kind,
			"configured":          status == "active" && ref != "",
			"status":              status,
			"secret_ref":          s6MaskRef(ref),
			"prefix":              s6StringValue(row["prefix"]),
			"fingerprint":         s6StringValue(row["fingerprint"]),
			"rotated_at":          row["rotated_at"],
			"last_used_at":        row["last_used_at"],
			"plaintext_available": false,
		}
		items = append(items, item)
	}
	return items
}

// s6EnsureSecretKindRows 保证三类凭据在列表里各占一行：store.ListProviderSecretsByKind
// 只返回存在的 kind，缺失的 kind 必须补成"未配置"占位，否则后台密钥页会少一行。
func s6EnsureSecretKindRows(items []map[string]any) []map[string]any {
	present := make(map[string]bool, len(items))
	for _, item := range items {
		if kind := strings.TrimSpace(s6StringValue(item["secret_kind"])); kind != "" {
			present[kind] = true
		}
	}
	out := items
	for _, kind := range s6SecretKinds {
		if present[kind] {
			continue
		}
		out = append(out, map[string]any{
			"secret_kind": kind, "configured": false, "status": "missing",
			"secret_ref": "", "prefix": "", "fingerprint": "", "note": "",
			"rotated_at": nil, "last_used_at": nil, "expires_at": nil,
			"plaintext_available": false,
		})
	}
	return out
}

func s6ValidSecretKind(kind string) bool {
	switch kind {
	case s6SecretKindRuntimeAPIKey, s6SecretKindStatisticsAPIToken, s6SecretKindKeysAPIToken:
		return true
	}
	return false
}

// s6SecretRefAllowed 只接受受控引用前缀，禁止明文（含其它 scheme）混进 secret_ref。
func s6SecretRefAllowed(ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	return strings.HasPrefix(lower, "env://") || strings.HasPrefix(lower, "store://")
}

// s6ProviderExists 校验 Provider 描述符存在，避免直接撞 provider_secrets 的外键约束。
func (s *Server) s6ProviderExists(ctx context.Context, key string) bool {
	if s.deps.Store == nil || s.deps.Store.Pool == nil {
		return false
	}
	var exists bool
	if err := s.deps.Store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM provider_descriptors WHERE provider_key=$1)`, key).Scan(&exists); err != nil {
		return false
	}
	return exists
}

// ---- 运行时注册表与 dRPC 客户端 ----

// s6RuntimeProvider 经运行时注册表取实现（规范 §5：找不到才返回 not_runtime_provider）。
func (s *Server) s6RuntimeProvider(key string) (provider.Provider, bool) {
	if s.deps.Routes == nil {
		return nil, false
	}
	return s.deps.Routes.Get(key)
}

// s6ChannelProber 取支持通道探针的运行时实现。
func (s *Server) s6ChannelProber(key string) (s6ChannelProber, bool) {
	prov, ok := s.s6RuntimeProvider(key)
	if !ok {
		return nil, false
	}
	prober, ok := prov.(s6ChannelProber)
	return prober, ok
}

// s6DRPCClient 取 dRPC 运行时客户端：deps.DRPC 优先，其次注册表里的实现。
func (s *Server) s6DRPCClient(key string) (*drpc.Client, bool) {
	if !strings.EqualFold(strings.TrimSpace(key), drpc.ProviderKey) {
		return nil, false
	}
	if s.deps.DRPC != nil {
		return s.deps.DRPC, true
	}
	if s.deps.Routes == nil {
		return nil, false
	}
	prov, ok := s.deps.Routes.Get(key)
	if !ok {
		return nil, false
	}
	client, ok := prov.(*drpc.Client)
	return client, ok
}

// ---- 通用取值辅助 ----

// s6StringValue 把任意 JSON 值安全转成字符串（数字用紧凑十进制，不引入科学计数法噪音）。
func s6StringValue(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case bool:
		if value {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

// s6Int64Value 解析 int64：接受 int/int64/float64/json.Number/十进制字符串。
func s6Int64Value(v any) (int64, bool) {
	switch value := v.(type) {
	case nil:
		return 0, false
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, false
		}
		return int64(value), true
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			asFloat, floatErr := strconv.ParseFloat(trimmed, 64)
			if floatErr != nil {
				return 0, false
			}
			return int64(asFloat), true
		}
		return parsed, true
	default:
		return 0, false
	}
}

// s6TimeValue 解析时间：接受 time.Time、*time.Time 与 RFC3339(Nano) 字符串。
func s6TimeValue(v any) (time.Time, bool) {
	switch value := v.(type) {
	case time.Time:
		return value, !value.IsZero()
	case *time.Time:
		if value == nil || value.IsZero() {
			return time.Time{}, false
		}
		return *value, true
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return time.Time{}, false
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, trimmed); err == nil {
				return parsed, true
			}
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

// s6LogWarn 是读路径的可选日志出口：deps.Log 在测试里可能为空。
func (s *Server) s6LogWarn(message string, args ...any) {
	if s.deps.Log != nil {
		s.deps.Log.Warn(message, args...)
	}
}
