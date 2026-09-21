package drpc

// 本文件覆盖额度闸门与层级/额度钳制：StopPercent 拒新请求、Key 停用、告警/安全档降级、
// 管理快照过期，以及免费/未知层级下 CU 上限把速率钳到 24 RPM / 36 RPS。
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func quotaConfig(t *testing.T, clock *fakeClock) config {
	t.Helper()
	return Options{Now: clock.Now}.normalize()
}

func TestQuotaDecisionWithoutSnapshotDoesNotClamp(t *testing.T) {
	clock := newFakeClock()
	gate := newQuotaGate(quotaConfig(t, clock))
	decision := gate.decision()
	if decision.Blocked || decision.AllowedMode != ModeTurbo || decision.Reason != "" {
		t.Fatalf("没有管理快照时不应额外限制: %#v", decision)
	}
}

func TestQuotaDecisionPercentLadder(t *testing.T) {
	cases := []struct {
		name        string
		spent       int64
		wantMode    string
		wantReason  string
		wantBlocked bool
		wantUsedPct float64
	}{
		{"50%", 50000, ModeTurbo, "", false, 50},
		{"80%", 80000, ModeFast, "quota_warn", false, 80},
		{"95%", 95000, ModeSafe, "quota_safe_only", false, 95},
		{"100%", 100000, ModeTurbo, "", true, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := newFakeClock()
			gate := newQuotaGate(quotaConfig(t, clock))
			limit := int64(100000)
			spent := tc.spent
			gate.set(QuotaSnapshot{Tier: TierPaid, CUSpentToday: &spent, CUDayLimit: &limit, SyncedAt: clock.Now()})
			decision := gate.decision()
			if decision.Blocked != tc.wantBlocked {
				t.Fatalf("blocked 应为 %v: %#v", tc.wantBlocked, decision)
			}
			if !tc.wantBlocked {
				if decision.AllowedMode != tc.wantMode || decision.Reason != tc.wantReason {
					t.Fatalf("应降档到 %s(%s)，得到 %#v", tc.wantMode, tc.wantReason, decision)
				}
			}
			if !decision.HasUsedPercent || decision.UsedPercent != tc.wantUsedPct {
				t.Fatalf("用量百分比应为 %v，得到 %#v", tc.wantUsedPct, decision)
			}
			if tc.wantBlocked {
				err := gate.blockedError(decision)
				if err.Class != provider.ClassQuota {
					t.Fatalf("必须归 ClassQuota: %#v", err)
				}
			}
		})
	}
}

func TestQuotaDecisionZeroDayLimitIsNotACap(t *testing.T) {
	clock := newFakeClock()
	gate := newQuotaGate(quotaConfig(t, clock))
	limit := int64(0)
	spent := int64(999999)
	gate.set(QuotaSnapshot{Tier: TierPaid, CUSpentToday: &spent, CUDayLimit: &limit, SyncedAt: clock.Now()})
	decision := gate.decision()
	if decision.Blocked || decision.HasUsedPercent {
		t.Fatalf("日上限为 0/空时不算百分比也不应阻断: %#v", decision)
	}
}

func TestQuotaDecisionKeyDisabled(t *testing.T) {
	clock := newFakeClock()
	gate := newQuotaGate(quotaConfig(t, clock))
	disabled := false
	gate.set(QuotaSnapshot{Tier: TierPaid, KeyEnabled: &disabled, SyncedAt: clock.Now()})
	decision := gate.decision()
	if !decision.Blocked {
		t.Fatalf("Key 停用必须阻断: %#v", decision)
	}
	err := gate.blockedError(decision)
	if err.Class != provider.ClassQuota || !strings.HasPrefix(err.Message, CodeQuotaExhausted) {
		t.Fatalf("Key 停用必须归 ClassQuota + DRPC_QUOTA_EXHAUSTED: %#v", err)
	}
	if err.Retryable {
		t.Fatalf("Key 停用不应重试")
	}
}

func TestQuotaDecisionStaleness(t *testing.T) {
	clock := newFakeClock()
	gate := newQuotaGate(quotaConfig(t, clock))

	// 10 分钟：新鲜。
	gate.set(QuotaSnapshot{Tier: TierPaid, SyncedAt: clock.Now().Add(-10 * time.Minute)})
	if decision := gate.decision(); decision.Stale || decision.AllowedMode != ModeTurbo {
		t.Fatalf("10 分钟内的快照应视为新鲜: %#v", decision)
	}
	// 20 分钟：TURBO 不可用。
	gate.set(QuotaSnapshot{Tier: TierPaid, SyncedAt: clock.Now().Add(-20 * time.Minute)})
	decision := gate.decision()
	if !decision.Stale || decision.VeryStale || decision.AllowedMode != ModeFast || decision.Reason != "management_stale" {
		t.Fatalf("过期 20 分钟应降 FAST: %#v", decision)
	}
	// 2 小时：最多 SAFE。
	gate.set(QuotaSnapshot{Tier: TierPaid, SyncedAt: clock.Now().Add(-2 * time.Hour)})
	decision = gate.decision()
	if !decision.VeryStale || decision.AllowedMode != ModeSafe || decision.Reason != "management_very_stale" {
		t.Fatalf("过期 2 小时应最多 SAFE: %#v", decision)
	}
	// 零值 SyncedAt：按最旧处理，不假装满速。
	gate.set(QuotaSnapshot{Tier: TierPaid})
	if decision := gate.decision(); !decision.VeryStale {
		t.Fatalf("零值 SyncedAt 应按最旧处理: %#v", decision)
	}
}

func TestClampProfileDataAPIUnknownTier(t *testing.T) {
	clock := newFakeClock()
	cfg := Options{Tier: TierUnknown, Now: clock.Now}.normalize()
	decision := quotaDecision{AllowedMode: ModeTurbo}
	profile := clampProfile(defaultProfile(ChannelDataAPI, ModeTurbo), ChannelDataAPI, cfg, decision)
	if profile.RequestsPerMinute != 24 {
		t.Fatalf("未知层级 RPM 必须钳到 24，得到 %v", profile.RequestsPerMinute)
	}
	if profile.CUPerMinute != 24*CUDataAPIHistory {
		t.Fatalf("CU/分钟必须与整数 RPM 自洽（44088），得到 %d", profile.CUPerMinute)
	}
	if profile.ClampReason != "cu_cap_unknown_tier" {
		t.Fatalf("降档原因应为 cu_cap_unknown_tier，得到 %q", profile.ClampReason)
	}
	// 付费层不受影响。
	paidCfg := Options{Tier: TierPaid, Now: clock.Now}.normalize()
	paid := clampProfile(defaultProfile(ChannelDataAPI, ModeTurbo), ChannelDataAPI, paidCfg, decision)
	if paid.RequestsPerMinute != 60 || paid.ClampReason != "" {
		t.Fatalf("付费层应保持 TURBO 60 RPM: %#v", paid)
	}
}

func TestClampProfileRPCFreeTier(t *testing.T) {
	clock := newFakeClock()
	cfg := Options{Tier: TierFree, Now: clock.Now}.normalize()
	profile := clampProfile(defaultProfile(ChannelSolanaRPC, ModeTurbo), ChannelSolanaRPC, cfg, quotaDecision{AllowedMode: ModeTurbo})
	if profile.RequestsPerSecond != FreeTierMaxRPS {
		t.Fatalf("免费层 RPS 必须钳到 %d，得到 %v", FreeTierMaxRPS, profile.RequestsPerSecond)
	}
	if profile.CUPerMinute != int64(FreeTierMaxRPS)*60*CURPCCall {
		t.Fatalf("免费层 CU/分钟应为 %d，得到 %d", FreeTierMaxRPS*60*CURPCCall, profile.CUPerMinute)
	}
	if profile.ClampReason != "cu_cap_free_tier" {
		t.Fatalf("降档原因应为 cu_cap_free_tier，得到 %q", profile.ClampReason)
	}
}

func TestClampProfileStalePaidTierIsCapped(t *testing.T) {
	clock := newFakeClock()
	cfg := Options{Tier: TierPaid, Now: clock.Now}.normalize()
	// 付费层但快照过期：仍然按未知层级钳制（不能假装满速）。
	profile := clampProfile(defaultProfile(ChannelDataAPI, ModeTurbo), ChannelDataAPI, cfg, quotaDecision{
		AllowedMode: ModeFast, Stale: true, Reason: "management_stale",
	})
	if profile.RequestsPerMinute != 24 {
		t.Fatalf("过期快照下必须按 45000 CU/分钟钳制，得到 %v", profile.RequestsPerMinute)
	}
	if profile.ClampReason != "management_stale" {
		t.Fatalf("降档原因应保留 management_stale，得到 %q", profile.ClampReason)
	}
}

func TestClampProfileKeyRateLimitWins(t *testing.T) {
	clock := newFakeClock()
	cfg := Options{Tier: TierPaid, Now: clock.Now}.normalize()
	rate := 5.0
	profile := clampProfile(defaultProfile(ChannelDataAPI, ModeFast), ChannelDataAPI, cfg, quotaDecision{
		AllowedMode: ModeTurbo, HasRate: true, RateLimit: rate,
	})
	if profile.RequestsPerMinute != 5 {
		t.Fatalf("Keys API 的 Key 速率上限应生效，得到 %v", profile.RequestsPerMinute)
	}
	if profile.ClampReason != "key_rate_limit" {
		t.Fatalf("降档原因应为 key_rate_limit，得到 %q", profile.ClampReason)
	}
}

func TestClampProfileWarnLowersModeThroughAllowedMode(t *testing.T) {
	clock := newFakeClock()
	cfg := Options{Tier: TierPaid, Now: clock.Now}.normalize()
	// 80% 告警：允许的最高模式是 FAST，因此 TURBO 的 Profile 应改用 FAST 的参数。
	allowed := lowerMode(ModeTurbo, ModeFast)
	if allowed != ModeFast {
		t.Fatalf("lowerMode 应取较低档: %s", allowed)
	}
	profile := clampProfile(defaultProfile(ChannelDataAPI, allowed), ChannelDataAPI, cfg, quotaDecision{
		AllowedMode: ModeFast, Reason: "quota_warn",
	})
	if profile.RequestsPerMinute != 30 || profile.ClampReason != "quota_warn" {
		t.Fatalf("告警档应使用 FAST 参数并保留原因: %#v", profile)
	}
}

// ---- 阻断原因码与余额告警 ----

func TestQuotaDecisionBlockCodes(t *testing.T) {
	clock := newFakeClock()
	gate := newQuotaGate(quotaConfig(t, clock))

	limit, spent := int64(1000), int64(1000)
	gate.set(QuotaSnapshot{Tier: TierPaid, CUSpentToday: &spent, CUDayLimit: &limit, SyncedAt: clock.Now()})
	if decision := gate.decision(); decision.BlockCode != "quota_stop" || decision.Reason != "quota_stop" {
		t.Fatalf("CU 用尽的原因码应为 quota_stop: %#v", decision)
	}

	disabled := false
	gate.set(QuotaSnapshot{Tier: TierPaid, KeyEnabled: &disabled, SyncedAt: clock.Now()})
	if decision := gate.decision(); decision.BlockCode != "key_disabled" || decision.Reason != "key_disabled" {
		t.Fatalf("Key 停用的原因码应为 key_disabled: %#v", decision)
	}
}

func TestQuotaDecisionBalanceLowClampsToFast(t *testing.T) {
	clock := newFakeClock()
	cfg := Options{Tier: TierPaid, BalanceWarn: "10.5", Now: clock.Now}.normalize()
	gate := newQuotaGate(cfg)
	gate.set(QuotaSnapshot{Tier: TierPaid, Balance: "3.25", BalanceUnit: "USD", SyncedAt: clock.Now()})
	decision := gate.decision()
	if decision.Blocked || decision.AllowedMode != ModeFast || decision.Reason != "balance_low" {
		t.Fatalf("余额低于告警值应最多 FAST: %#v", decision)
	}
	// 余额充足：不降档。
	gate.set(QuotaSnapshot{Tier: TierPaid, Balance: "99", SyncedAt: clock.Now()})
	if decision := gate.decision(); decision.Reason != "" || decision.AllowedMode != ModeTurbo {
		t.Fatalf("余额充足不应降档: %#v", decision)
	}
	// 无法解析的余额：忽略而不是猜。
	gate.set(QuotaSnapshot{Tier: TierPaid, Balance: "n/a", SyncedAt: clock.Now()})
	if decision := gate.decision(); decision.Reason != "" {
		t.Fatalf("无法解析的余额必须忽略: %#v", decision)
	}
}

// ---- EffectiveMode（后台展示用，复用同一套判定）----

func TestEffectiveModeWithoutSnapshotKeepsRequested(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.Tier = TierUnknown })

	if mode, reason := env.client.EffectiveMode(ModeTurbo); mode != ModeTurbo || reason != "" {
		t.Fatalf("没有额度快照时应保持请求模式且无原因: %s/%s", mode, reason)
	}
	if mode, _ := env.client.EffectiveMode(""); mode != DefaultMode {
		t.Fatalf("requested 为空应按默认档 %s，得到 %s", DefaultMode, mode)
	}
}

func TestEffectiveModeFollowsQuotaDecision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, nil)
	limit := int64(100)

	// 85%：最多 FAST。
	spent := int64(85)
	env.client.SetQuotaSnapshot(QuotaSnapshot{Tier: TierPaid, CUSpentToday: &spent, CUDayLimit: &limit, SyncedAt: env.clock.Now()})
	if mode, reason := env.client.EffectiveMode(ModeTurbo); mode != ModeFast || reason != "quota_warn" {
		t.Fatalf("85%% 应降 FAST/quota_warn: %s/%s", mode, reason)
	}
	// 96%：最多 SAFE。
	spent = 96
	env.client.SetQuotaSnapshot(QuotaSnapshot{Tier: TierPaid, CUSpentToday: &spent, CUDayLimit: &limit, SyncedAt: env.clock.Now()})
	if mode, reason := env.client.EffectiveMode(ModeTurbo); mode != ModeSafe || reason != "quota_safe_only" {
		t.Fatalf("96%% 应降 SAFE/quota_safe_only: %s/%s", mode, reason)
	}
	// 100%：暂停新请求。
	spent = 100
	env.client.SetQuotaSnapshot(QuotaSnapshot{Tier: TierPaid, CUSpentToday: &spent, CUDayLimit: &limit, SyncedAt: env.clock.Now()})
	if mode, reason := env.client.EffectiveMode(ModeTurbo); mode != ModeSafe || reason != "quota_stop" {
		t.Fatalf("100%% 应停 SAFE/quota_stop: %s/%s", mode, reason)
	}
	// Key 停用。
	disabled := false
	env.client.SetQuotaSnapshot(QuotaSnapshot{Tier: TierPaid, KeyEnabled: &disabled, SyncedAt: env.clock.Now()})
	if mode, reason := env.client.EffectiveMode(ModeTurbo); mode != ModeSafe || reason != "key_disabled" {
		t.Fatalf("Key 停用应停 SAFE/key_disabled: %s/%s", mode, reason)
	}
	// 快照过期：TURBO 不可用 / 最多 SAFE。
	env.client.SetQuotaSnapshot(QuotaSnapshot{Tier: TierPaid, SyncedAt: env.clock.Now().Add(-20 * time.Minute)})
	if mode, reason := env.client.EffectiveMode(ModeTurbo); mode != ModeFast || reason != "management_stale" {
		t.Fatalf("过期 20 分钟应降 FAST/management_stale: %s/%s", mode, reason)
	}
	env.client.SetQuotaSnapshot(QuotaSnapshot{Tier: TierPaid, SyncedAt: env.clock.Now().Add(-2 * time.Hour)})
	if mode, reason := env.client.EffectiveMode(ModeTurbo); mode != ModeSafe || reason != "management_very_stale" {
		t.Fatalf("过期 2 小时应降 SAFE/management_very_stale: %s/%s", mode, reason)
	}
}

func TestEffectiveModeReportsTierCapWithSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.Tier = TierUnknown })
	env.client.SetQuotaSnapshot(QuotaSnapshot{Tier: TierUnknown, SyncedAt: env.clock.Now()})

	mode, reason := env.client.EffectiveMode(ModeTurbo)
	if mode != ModeTurbo || reason != "cu_cap_unknown_tier" {
		t.Fatalf("未知层级：模式保持 TURBO，但必须暴露 CU 上限原因: %s/%s", mode, reason)
	}
}

func TestConfiguredReportsResolvableKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	secrets := &countingSecrets{key: testKey}
	env := newTestEnv(t, server, nil)
	// 用计数 resolver 验证 30s 缓存只解析一次。
	client := New(Options{
		Enabled: true, Tier: TierPaid, BaseURL: env.baseURL,
		RuntimeKeyRef: "store://drpc/runtime_api_key", Secrets: secrets,
		Now: env.clock.Now,
	}, nil)
	if !client.Configured(context.Background()) {
		t.Fatalf("可解析的 Key 必须报告已配置")
	}
	client.Configured(context.Background())
	if secrets.calls != 1 {
		t.Fatalf("30s 内应复用密钥缓存，得到 %d 次解析", secrets.calls)
	}
	env.clock.Advance(keyCacheTTL + time.Second)
	client.Configured(context.Background())
	if secrets.calls != 2 {
		t.Fatalf("缓存过期后应重新解析，得到 %d 次解析", secrets.calls)
	}

	broken := New(Options{
		Enabled: true, Tier: TierPaid, BaseURL: env.baseURL,
		RuntimeKeyRef: "store://drpc/runtime_api_key", Secrets: fakeSecrets{err: ErrNotConfigured},
		Now: env.clock.Now,
	}, nil)
	if broken.Configured(context.Background()) {
		t.Fatalf("无法解析的 Key 必须报告未配置")
	}
	if reason := broken.UnavailableReason(context.Background()); !strings.Contains(reason, CodeNotConfigured) {
		t.Fatalf("不可用原因必须带错误码前缀: %q", reason)
	}
}

type countingSecrets struct {
	mu    sync.Mutex
	calls int
	key   string
	err   error
}

func (c *countingSecrets) Resolve(ctx context.Context, ref string) (string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	if c.err != nil {
		return "", c.err
	}
	return c.key, nil
}

// ---- ChainHead ----

func TestChainHeadUsesFinalizedGetSlot(t *testing.T) {
	var mu sync.Mutex
	var calls []rpcCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
		writeRPCResult(t, w, json.RawMessage("310000000"))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	head, err := env.client.ChainHead(context.Background())
	if err != nil {
		t.Fatalf("ChainHead 失败: %v", err)
	}
	if head != 310000000 {
		t.Fatalf("链头 slot 不符: %d", head)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0].Method != MethodSlot {
		t.Fatalf("ChainHead 必须只调用一次 getSlot: %#v", calls)
	}
	if len(calls[0].Params) != 1 || !strings.Contains(string(calls[0].Params[0]), "finalized") {
		t.Fatalf("getSlot 必须带 commitment=finalized: %s", calls[0].Params)
	}
	found := false
	for _, row := range env.usage.all() {
		if row.Method == MethodSlot {
			found = true
			if row.UsageType != UsageChainHead || row.APIType != APITypeRPC || row.Chain != ChainSolana {
				t.Fatalf("ChainHead 用量行不符: %#v", row)
			}
		}
	}
	if !found {
		t.Fatalf("ChainHead 必须记录用量行")
	}
}

func TestChainHeadNotConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("未配置时不得发出请求")
	}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.RuntimeKeyRef = "" })

	if _, err := env.client.ChainHead(context.Background()); err == nil {
		t.Fatalf("未配置时必须返回错误，而不是 (0, nil)")
	} else {
		requireCode(t, err, CodeNotConfigured)
	}
}

func TestChainHeadQuotaBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("闸门关闭时不得发出请求")
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)
	limit, spent := int64(10), int64(10)
	env.client.SetQuotaSnapshot(QuotaSnapshot{Tier: TierPaid, CUSpentToday: &spent, CUDayLimit: &limit, SyncedAt: env.clock.Now()})
	if _, err := env.client.ChainHead(context.Background()); err == nil {
		t.Fatalf("闸门关闭时必须拒绝")
	} else {
		requireCode(t, err, CodeQuotaExhausted)
	}
}
