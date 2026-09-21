package drpc

// 本文件实现账户级共享额度闸门（规范 §1.4 关键行为 3/4、开发文档 §6.4/§11.5）。
//
// 两条通道属于同一个 dRPC 账户，因此 CU 日用量、Key 状态与速率上限跨通道共享：
//   - 当日 CU 达到 StopPercent  -> 拒绝新请求（ClassQuota + DRPC_QUOTA_EXHAUSTED）；
//   - KeyEnabled=false          -> 同样拒绝（Key 被后台停用，重试没有意义）；
//   - 达到 SafeOnlyPercent      -> 最多 SAFE；达到 WarnPercent -> 最多 FAST；
//   - 管理快照过期（>15min / >60min）-> TURBO 不可用 / 最多 SAFE。
import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// QuotaSnapshot 由 drpcmgmt 同步后注入（规范 §1.4 冻结字段）。
type QuotaSnapshot struct {
	Tier         string
	Balance      string
	BalanceUnit  string
	CUSpentToday *int64
	CUDayLimit   *int64
	RateLimit    *float64
	SyncedAt     time.Time
	KeyEnabled   *bool
	Source       string
}

// quotaDecision 是一次额度判定的结果。
type quotaDecision struct {
	// Blocked 为真时不允许发出任何新请求。
	Blocked     bool
	BlockReason string
	// BlockCode 是阻断的原因码：quota_stop（当日 CU 用尽）或 key_disabled（Key 被停用）。
	BlockCode string
	// AllowedMode 是额度允许的最高模式（没有快照时为 TURBO，即不额外限制）。
	AllowedMode string
	// Reason 记录降档原因（effective_reason，开发文档 §6.2/§10.2）。
	Reason string
	// UsedPercent 仅在 CUDayLimit 与 CUSpentToday 都存在时有效（0/空上限不计算，规范 §1.5）。
	UsedPercent    float64
	HasUsedPercent bool
	// Stale 表示管理快照已过期（TURBO 不可用）；VeryStale 表示超过 SafeOnlyAfter（最多 SAFE）。
	Stale     bool
	VeryStale bool
	// KeyEnabled 为 nil 表示未知。
	KeyEnabled *bool
	// RateLimit 是 Keys API 返回的 Key 速率上限（每分钟请求数，单位不确定点见下）。
	RateLimit float64
	HasRate   bool
}

type quotaGate struct {
	mu      sync.Mutex
	cfg     config
	snap    QuotaSnapshot
	hasSnap bool
}

func newQuotaGate(cfg config) *quotaGate { return &quotaGate{cfg: cfg} }

// set 注入新快照（drpcmgmt 每次同步后调用）。
func (g *quotaGate) set(s QuotaSnapshot) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.snap = s
	g.hasSnap = true
}

func (g *quotaGate) snapshot() (QuotaSnapshot, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.snap, g.hasSnap
}

// 降档原因严重度：数值越大越严重，decision 只保留最严重的一条原因码。
const (
	severityNone          = iota
	severityStale         // management_stale：最多 FAST
	severityQuotaWarn     // quota_warn：最多 FAST
	severityBalanceLow    // balance_low：最多 FAST
	severityVeryStale     // management_very_stale：最多 SAFE
	severityQuotaSafeOnly // quota_safe_only：最多 SAFE
	severityKeyDisabled   // key_disabled：阻断
	severityQuotaStop     // quota_stop：阻断
)

// decision 计算当前额度状态。
//
// 这是**唯一**一份阈值实现：EffectiveMode / clampProfile / Health 都复用它的结果，
// 避免"后台显示"与"实际限流"出现两套规则。
func (g *quotaGate) decision() quotaDecision {
	snap, ok := g.snapshot()
	dec := quotaDecision{AllowedMode: ModeTurbo}
	if !ok {
		// 没有管理快照：只按配置层级钳制速率（见 clampProfile），模式不额外降档。
		return dec
	}
	dec.KeyEnabled = snap.KeyEnabled
	if snap.RateLimit != nil && *snap.RateLimit > 0 {
		// 不确定点：Keys API 的 rate_limit 单位官方文档没有写死，这里按"每分钟请求数"解释。
		// 拿不准时宁可少发请求（更慢，但不会触发 429），真实压测后再用 Profile 修正。
		dec.RateLimit, dec.HasRate = *snap.RateLimit, true
	}
	severity := severityNone
	setReason := func(level int, code string) {
		if level > severity {
			severity, dec.Reason = level, code
		}
	}
	// 快照新鲜度：SyncedAt 为零值视为最旧（宁可降档，也不假装满速）。
	age := time.Duration(math.MaxInt64)
	if !snap.SyncedAt.IsZero() {
		age = g.cfg.now().Sub(snap.SyncedAt)
		if age < 0 {
			age = 0
		}
	}
	switch {
	case age > g.cfg.safeOnlyAfter:
		dec.VeryStale, dec.Stale = true, true
		dec.AllowedMode = lowerMode(dec.AllowedMode, ModeSafe)
		setReason(severityVeryStale, "management_very_stale")
	case age > g.cfg.staleAfter:
		dec.Stale = true
		dec.AllowedMode = lowerMode(dec.AllowedMode, ModeFast)
		setReason(severityStale, "management_stale")
	}
	// 当日 CU 阶梯（开发文档 §6.4：80% 告警、95% 最多 SAFE、100% 暂停）。
	if snap.CUDayLimit != nil && *snap.CUDayLimit > 0 && snap.CUSpentToday != nil {
		dec.UsedPercent = float64(*snap.CUSpentToday) / float64(*snap.CUDayLimit) * 100
		dec.HasUsedPercent = true
		switch {
		case dec.UsedPercent >= float64(g.cfg.stopPercent):
			dec.Blocked = true
			dec.BlockCode = "quota_stop"
			dec.BlockReason = fmt.Sprintf("%s: dRPC daily CU limit reached (%.2f%% of %d)",
				CodeQuotaExhausted, dec.UsedPercent, *snap.CUDayLimit)
			setReason(severityQuotaStop, "quota_stop")
		case dec.UsedPercent >= float64(g.cfg.safeOnlyPercent):
			dec.AllowedMode = lowerMode(dec.AllowedMode, ModeSafe)
			setReason(severityQuotaSafeOnly, "quota_safe_only")
		case dec.UsedPercent >= float64(g.cfg.warnPercent):
			dec.AllowedMode = lowerMode(dec.AllowedMode, ModeFast)
			setReason(severityQuotaWarn, "quota_warn")
		}
	}
	// 余额低于管理员设置的告警值：最多 FAST（开发文档 §6.4）。
	// 只有两边都能解析成十进制数时才比较；解析失败一律忽略（不猜、不伪造）。
	if warn, ok := parseDecimal(g.cfg.balanceWarn); ok {
		if balance, ok := parseDecimal(snap.Balance); ok && balance.Cmp(warn) < 0 {
			dec.AllowedMode = lowerMode(dec.AllowedMode, ModeFast)
			setReason(severityBalanceLow, "balance_low")
		}
	}
	if snap.KeyEnabled != nil && !*snap.KeyEnabled {
		// Key 被停用：与额度用尽同样处理——暂停 dRPC 新请求，保留任务与游标（§7.5）。
		dec.Blocked = true
		if severity < severityKeyDisabled {
			dec.BlockCode = "key_disabled"
			dec.BlockReason = CodeQuotaExhausted + ": dRPC runtime key is disabled by the account (keys api status=false)"
		}
		setReason(severityKeyDisabled, "key_disabled")
	}
	return dec
}

// parseDecimal 解析十进制字符串（可带小数）；失败返回 ok=false。
//
// 用有理数而不是 float，避免余额这种 18 位小数的数字被浮点截断。
func parseDecimal(text string) (*big.Rat, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	value, ok := new(big.Rat).SetString(text)
	return value, ok
}

// blockedError 把额度判定转成 provider.Error（ClassQuota + DRPC_QUOTA_EXHAUSTED，不重试）。
func (g *quotaGate) blockedError(dec quotaDecision) *provider.Error {
	if dec.BlockReason == "" {
		dec.BlockReason = CodeQuotaExhausted + ": dRPC account gate is closed"
	}
	err := newError(provider.ClassQuota, CodeQuotaExhausted, "dRPC account gate is closed: "+dec.BlockReason)
	err.Retryable = false
	return err
}

// clampProfile 按账户层级、管理快照新鲜度与额度用量钳制 Profile（开发文档 §3.4/§6.2/§6.4）。
//
//   - paid 且快照新鲜：按 Profile 满速；
//   - free/unknown/空层级或快照过期：data_api CU/分钟 <= UnknownTierCUPerMinute，
//     RPM <= floor(cap/1837) = 24；solana_rpc RPS <= 36；
//   - Keys API 的 key 级 rate_limit 再取小（单位不确定点见 decision）。
//
// 钳制后的 RPM/RPS 与 CU/分钟必须自洽：CU = 整数速率 × 单请求 CU，绝不虚报
// （§10.2 的例子：requests_per_minute=24、cu_per_minute=44088）。
func clampProfile(p Profile, channel string, cfg config, dec quotaDecision) Profile {
	if p.EffectiveMode == "" {
		p.EffectiveMode = normalizeMode(p.Mode)
	}
	reason := dec.Reason
	freeTier := !cfg.paidTier() || dec.Stale
	freeTierReason := ""
	switch {
	case cfg.tier == TierFree:
		freeTierReason = "cu_cap_free_tier"
	case cfg.tier != TierPaid:
		freeTierReason = "cu_cap_unknown_tier"
	default:
		// paid 但快照过期：降档原因是快照陈旧，不是层级。
		freeTierReason = "management_stale"
	}
	switch channel {
	case ChannelDataAPI:
		if p.RequestsPerMinute < 0 {
			p.RequestsPerMinute = 0
		}
		if freeTier {
			capCU := cfg.unknownTierCUPerMinute
			if p.CUPerMinute > capCU || p.CUPerMinute <= 0 {
				p.CUPerMinute = capCU
			}
			maxRPM := math.Floor(float64(capCU) / float64(CUDataAPIHistory))
			if p.RequestsPerMinute > maxRPM {
				p.RequestsPerMinute = maxRPM
			}
			if reason == "" {
				reason = freeTierReason
			}
		}
		if dec.HasRate && dec.RateLimit < p.RequestsPerMinute {
			p.RequestsPerMinute = dec.RateLimit
			if reason == "" {
				reason = "key_rate_limit"
			}
		}
		rpm := math.Floor(p.RequestsPerMinute)
		p.RequestsPerMinute = rpm
		if bound := int64(rpm) * CUDataAPIHistory; p.CUPerMinute <= 0 || bound < p.CUPerMinute {
			p.CUPerMinute = bound
		}
	default: // solana_rpc
		if p.RequestsPerSecond < 0 {
			p.RequestsPerSecond = 0
		}
		if freeTier {
			capCU := cfg.unknownTierCUPerMinute
			if p.CUPerMinute > capCU || p.CUPerMinute <= 0 {
				p.CUPerMinute = capCU
			}
			if p.RequestsPerSecond > FreeTierMaxRPS {
				p.RequestsPerSecond = FreeTierMaxRPS
			}
			if reason == "" {
				reason = freeTierReason
			}
		}
		if dec.HasRate {
			if rps := dec.RateLimit / 60; rps < p.RequestsPerSecond {
				p.RequestsPerSecond = rps
				if reason == "" {
					reason = "key_rate_limit"
				}
			}
		}
		rps := math.Floor(p.RequestsPerSecond)
		p.RequestsPerSecond = rps
		if bound := int64(rps) * 60 * CURPCCall; p.CUPerMinute <= 0 || bound < p.CUPerMinute {
			p.CUPerMinute = bound
		}
	}
	p.ClampReason = reason
	return p
}
