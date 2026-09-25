package drpc

// 本文件实现健康检查与通道探针（规范 §1.4 关键行为 8/9）。
//
//   - ProbeChannels 是**真实探针**：Data API 用一次低成本 history 调用（limit=1），
//     JSON-RPC 用 getHealth（0 CU，官方 CU 表）；
//   - Health 有 30s 缓存，避免每 5 秒的探针把账户配额打爆；
//   - 未配置 Key 时 Health 返回 DEGRADED 且 Error 含 DRPC_NOT_CONFIGURED；
//   - 上报的并发/速率取**钳制后**的值，不假装是满速。
import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// channelProbe 是单通道探针结果（ProbeChannels 的返回结构，规范 §1.4）。
type channelProbe struct {
	OK         bool   `json:"ok"`
	State      string `json:"state"`
	LatencyMS  int64  `json:"latency_ms"`
	ErrorClass string `json:"error_class"`
	Message    string `json:"message"`
}

type healthTracker struct {
	mu     sync.Mutex
	now    func() time.Time
	recent []bool

	consecutiveFailures int
	lastCheck           time.Time
	lastError           string

	cachedProbes map[string]any
	cachedAt     time.Time
	hasCache     bool
}

// healthWindow 是错误率采样窗口长度（开发文档 §6.4："连续 3 个采样窗口错误率超过 10%"）。
const healthWindow = 10

func newHealthTracker(now func() time.Time) *healthTracker {
	return &healthTracker{now: now, recent: make([]bool, 0, healthWindow)}
}

func (h *healthTracker) record(ok bool, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastCheck = h.now().UTC()
	if ok {
		h.consecutiveFailures = 0
		h.lastError = ""
	} else {
		h.consecutiveFailures++
		h.lastError = message
	}
	h.recent = append(h.recent, ok)
	if len(h.recent) > healthWindow {
		h.recent = h.recent[len(h.recent)-healthWindow:]
	}
}

func (h *healthTracker) stats() (int, float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.recent) == 0 {
		return h.consecutiveFailures, 0
	}
	failures := 0
	for _, ok := range h.recent {
		if !ok {
			failures++
		}
	}
	return h.consecutiveFailures, float64(failures) / float64(len(h.recent))
}

func (h *healthTracker) cache(probes map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cachedProbes, h.cachedAt, h.hasCache = probes, h.now(), true
}

func (h *healthTracker) cached(ttl time.Duration) (map[string]any, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.hasCache || h.now().Sub(h.cachedAt) >= ttl {
		return nil, false
	}
	return h.cachedProbes, true
}

// ProbeChannels 真实探测两条通道，供后台"测试通道"命令使用（不使用缓存）。
func (c *Client) ProbeChannels(ctx context.Context) map[string]any {
	return c.probeAll(ctx, false)
}

// HistoryChannelAvailable 报告"地址历史枚举"通道（Data API）当前是否真的可用。
//
// 存在理由（2026-09-21 实测，trace-id 已留档）：密钥能解析 ≠ 这条链的历史能取。
// 实测同一把 Key：EVM 的 /lambda/v1/transactions/{addr}/history 正常返回，
// 而 solana 的同名端点稳定 HTTP 500；RPC 侧 getSignaturesForAddress 又返回 -32601
// "does not exist/is not available"。因此路由闸门必须按**通道可用性**判断，
// 否则会把 Solana 任务送进一个必然失败的通道 —— 比不接 dRPC 更差。
//
// 复用 Health 的 30s 探针缓存，因此可以按需调用，不会额外打请求。
func (c *Client) HistoryChannelAvailable(ctx context.Context) bool {
	if c.unavailableReason() != "" {
		return false
	}
	return probeAt(c.probeCached(ctx), ChannelDataAPI).OK
}

// probeCached 供 Health 使用：30s 内的结果直接复用。
func (c *Client) probeCached(ctx context.Context) map[string]any {
	if probes, ok := c.health.cached(probeCacheTTL); ok {
		return probes
	}
	return c.probeAll(ctx, true)
}

func (c *Client) probeAll(ctx context.Context, cached bool) map[string]any {
	probes := map[string]any{
		"checked_at": c.cfg.now().UTC().Format(time.RFC3339Nano),
	}
	if reason := c.unavailableReason(); reason != "" {
		// 未配置时不发任何请求：两条通道都报 DEGRADED + DRPC_NOT_CONFIGURED。
		failure := channelProbe{OK: false, State: string(provider.HealthDegraded), Message: reason}
		probes[ChannelDataAPI] = failure
		probes[ChannelSolanaRPC] = failure
		return probes
	}
	dataProbe := c.probeDataAPI(ctx)
	rpcProbe := c.probeRPC(ctx)
	probes[ChannelDataAPI] = dataProbe
	probes[ChannelSolanaRPC] = rpcProbe
	if cached {
		c.health.cache(probes)
	}
	return probes
}

// probeState 把探针错误分类映射到健康状态。
func probeState(class provider.ErrorClass, ok bool) provider.HealthState {
	if ok {
		return provider.HealthNormal
	}
	switch class {
	case provider.ClassAuth, provider.ClassQuota:
		// 密钥失效或额度耗尽：没有请求能成功，直接 OPEN。
		return provider.HealthOpen
	default:
		return provider.HealthDegraded
	}
}

// probeFromError 把一次探针调用结果转成 channelProbe。
func (c *Client) probeFromError(latency time.Duration, err error) channelProbe {
	probe := channelProbe{LatencyMS: latency.Milliseconds()}
	if err == nil {
		probe.OK, probe.State = true, string(provider.HealthNormal)
		return probe
	}
	probe.Message = sanitizeText(err.Error(), "")
	var perr *provider.Error
	if errors.As(err, &perr) {
		probe.ErrorClass = string(perr.Class)
		probe.State = string(probeState(perr.Class, false))
	} else {
		probe.State = string(provider.HealthDegraded)
	}
	return probe
}

// Health 返回 Provider 健康状态（规范 §1.4 关键行为 9）。
func (c *Client) Health(ctx context.Context) provider.HealthStatus {
	status := provider.HealthStatus{Provider: ProviderKey, LastCheck: c.cfg.now().UTC()}
	consecutive, errorRate := c.health.stats()
	status.ConsecutiveFailure = consecutive
	status.RecentErrorRate = errorRate

	if reason := c.UnavailableReason(ctx); reason != "" {
		status.State = provider.HealthDegraded
		status.Error = reason
		return status
	}
	// 额度闸门关闭（当日 CU 用尽 / Key 停用）：不再发探针请求，直接 OPEN。
	if dec := c.quota.decision(); dec.Blocked {
		status.State = provider.HealthOpen
		status.Error = dec.BlockReason
		return status
	}

	probes := c.probeCached(ctx)
	dataProbe := probeAt(probes, ChannelDataAPI)
	rpcProbe := probeAt(probes, ChannelSolanaRPC)
	switch {
	case dataProbe.OK && rpcProbe.OK:
		status.State = provider.HealthNormal
	case dataProbe.OK || rpcProbe.OK:
		// 单通道可用：整体降级，错误取失败通道的首个错误码前缀。
		status.State = provider.HealthDegraded
		status.Error = firstProbeError(dataProbe, rpcProbe)
	case dataProbe.State == string(provider.HealthOpen) || rpcProbe.State == string(provider.HealthOpen):
		status.State = provider.HealthOpen
		status.Error = firstProbeError(dataProbe, rpcProbe)
	default:
		status.State = provider.HealthDegraded
		status.Error = firstProbeError(dataProbe, rpcProbe)
	}
	// 上报的是钳制后的有效并发（不假装满速）。
	status.CurrentConcurrency = c.currentConcurrency(ctx)
	return status
}

func probeAt(probes map[string]any, channel string) channelProbe {
	if probe, ok := probes[channel].(channelProbe); ok {
		return probe
	}
	return channelProbe{State: string(provider.HealthDegraded)}
}

func firstProbeError(probes ...channelProbe) string {
	for _, probe := range probes {
		if !probe.OK && probe.Message != "" {
			return probe.Message
		}
	}
	return ""
}

// currentConcurrency 返回 data_api 通道当前的有效请求并发（Health.CurrentConcurrency）。
func (c *Client) currentConcurrency(ctx context.Context) int {
	exec, err := c.plan(ctx, DatasetTransactions, ChannelDataAPI, OperationSolanaHistory)
	if err != nil {
		return 0
	}
	return exec.concurrency
}

// unavailableReason 返回"不可用"的 DRPC_NOT_CONFIGURED 文本；可用时返回空串。
func (c *Client) unavailableReason() string {
	if err := c.configError(); err != nil {
		return err.Message
	}
	if c.cfg.keyRef == "" {
		return notConfigured("dRPC runtime key reference is not configured (DRPC_RUNTIME_KEY_REF is empty)").Message
	}
	if c.cfg.secrets == nil {
		return notConfigured("dRPC secret resolver is not configured").Message
	}
	return ""
}

// QuotaStatus 供后台展示（不含秘密）：账户档位、快照新鲜度、闸门状态与两通道的有效参数。
func (c *Client) QuotaStatus() map[string]any {
	snap, hasSnap := c.quota.snapshot()
	dec := c.quota.decision()
	status := map[string]any{
		"provider":         ProviderKey,
		"configured":       c.unavailableReason() == "",
		"enabled":          c.cfg.enabled,
		"tier":             c.cfg.tier,
		"base_url":         maskURL(c.cfg.baseURL + "/-"),
		"key_ref":          c.cfg.keyRef,
		"blocked":          dec.Blocked,
		"block_reason":     dec.BlockReason,
		"has_snapshot":     hasSnap,
		"stale":            dec.Stale,
		"very_stale":       dec.VeryStale,
		"used_percent":     dec.UsedPercent,
		"has_used_percent": dec.HasUsedPercent,
		"effective_reason": dec.Reason,
	}
	if hasSnap {
		status["tier_source"] = snap.Tier
		status["balance"] = snap.Balance
		status["balance_unit"] = snap.BalanceUnit
		status["key_enabled"] = snap.KeyEnabled
		status["rate_limit"] = snap.RateLimit
		status["source"] = snap.Source
		status["synced_at"] = snap.SyncedAt.UTC().Format(time.RFC3339Nano)
		if snap.CUDayLimit != nil {
			status["cu_day_limit"] = *snap.CUDayLimit
		}
		if snap.CUSpentToday != nil {
			status["cu_spent_today"] = *snap.CUSpentToday
			if snap.CUDayLimit != nil && *snap.CUDayLimit > 0 {
				remaining := *snap.CUDayLimit - *snap.CUSpentToday
				if remaining < 0 {
					remaining = 0
				}
				status["cu_remaining_today"] = remaining
			}
		}
	}
	status["effective"] = c.effectiveChannels()
	status["checked_at"] = c.cfg.now().UTC().Format(time.RFC3339Nano)
	return status
}

// effectiveChannels 汇总两条通道当前的有效模式/并发/速率/CU（钳制后）。
func (c *Client) effectiveChannels() map[string]any {
	out := map[string]any{}
	ctx := context.Background()
	for _, spec := range []struct {
		channel   string
		dataset   string
		operation string
	}{
		{ChannelDataAPI, DatasetTransactions, OperationSolanaHistory},
		{ChannelSolanaRPC, DatasetLogs, OperationSolanaDetail},
	} {
		exec, err := c.plan(ctx, spec.dataset, spec.channel, spec.operation)
		if err != nil {
			out[spec.channel] = map[string]any{"error": err.Error()}
			continue
		}
		entry := map[string]any{
			"mode":                exec.mode,
			"requested_mode":      exec.requestedMode,
			"request_concurrency": exec.concurrency,
			"cu_per_minute":       exec.cuPerMinute,
			"effective_reason":    exec.clampReason,
			"profile_revision":    exec.profile.Revision,
		}
		if spec.channel == ChannelDataAPI {
			entry["requests_per_minute"] = exec.reqPerMinute
			entry["page_size"] = exec.pageSize
		} else {
			entry["requests_per_second"] = exec.reqPerMinute / 60
		}
		out[spec.channel] = entry
	}
	return out
}
