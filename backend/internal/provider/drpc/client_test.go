package drpc

// 本文件提供测试基础设施，并覆盖客户端级行为：健康检查与 30s 缓存、额度闸门、
// 层级钳制、限流顺序与脱敏。所有测试都不访问网络（httptest.Server + 假时钟）。
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

const testKey = "drpc-test-key-abc123"

// testAddress 是合法的 Solana 公钥（base58，32 字节），大小写混合以便验证不被改写。
const testAddress = "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM"

// num 读取 map 里的数值字段（int/int64/float64 都可能出现）。
func num(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	switch value := m[key].(type) {
	case float64:
		return value
	case int64:
		return float64(value)
	case int:
		return float64(value)
	default:
		t.Fatalf("字段 %s 不是数值：%#v", key, m[key])
		return 0
	}
}

// ---- 假时钟：限流等待不真的 sleep（规范要求"不要 sleep 60 秒"）----

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	f.Advance(d)
	return nil
}

type fakeSecrets struct {
	key string
	err error
}

func (f fakeSecrets) Resolve(ctx context.Context, ref string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.key, nil
}

type usageSink struct {
	mu   sync.Mutex
	rows []model.UsageRow
}

func (s *usageSink) hook(row model.UsageRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, row)
}

func (s *usageSink) all() []model.UsageRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.UsageRow, len(s.rows))
	copy(out, s.rows)
	return out
}

type testEnv struct {
	client  *Client
	clock   *fakeClock
	usage   *usageSink
	baseURL string
}

func newTestEnv(t *testing.T, server *httptest.Server, mutate func(*Options)) *testEnv {
	t.Helper()
	clock := newFakeClock()
	sink := &usageSink{}
	opts := Options{
		Enabled:         true,
		Tier:            TierPaid,
		BaseURL:         server.URL + "/solana",
		RuntimeKeyRef:   "store://drpc/runtime_api_key",
		Secrets:         fakeSecrets{key: testKey},
		HistoryPageSize: DefaultHistoryPageSize,
		RPCTimeout:      5 * time.Second,
		DataTimeout:     5 * time.Second,
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:             clock.Now,
	}
	if mutate != nil {
		mutate(&opts)
	}
	client := New(opts, sink.hook)
	// 让限流等待走假时钟（生产环境是定时器）。
	client.cfg.sleep = clock.Sleep
	return &testEnv{client: client, clock: clock, usage: sink, baseURL: opts.BaseURL}
}

// requireCode 断言错误是带指定错误码前缀的 provider.Error。
func requireCode(t *testing.T, err error, code string) *provider.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", code)
	}
	var perr *provider.Error
	if !errors.As(err, &perr) {
		t.Fatalf("expected *provider.Error, got %T: %v", err, err)
	}
	if !strings.HasPrefix(perr.Message, code) {
		t.Fatalf("expected message prefix %q, got %q", code, perr.Message)
	}
	return perr
}

func rowObject(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("row is not a json object: %v (%s)", err, raw)
	}
	return m
}

func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// ---- 未配置 / 配置非法 ----

func TestFetchNotConfiguredWithoutKeyRef(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("未配置 Key 时不应该发出任何 HTTP 请求")
	}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.RuntimeKeyRef = "" })

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	perr := requireCode(t, err, CodeNotConfigured)
	if perr.Class == provider.ClassAuth || perr.Class == provider.ClassQuota {
		t.Fatalf("未配置必须是 ClassAuth/ClassQuota 之外的错误，得到 %s", perr.Class)
	}
	if perr.Retryable {
		t.Fatalf("未配置不应重试")
	}
}

func TestFetchNotConfiguredWhenResolverFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.Secrets = fakeSecrets{err: ErrNotConfigured} })

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	requireCode(t, err, CodeNotConfigured)
}

func TestHealthDegradedWhenNotConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.RuntimeKeyRef = "" })

	status := env.client.Health(context.Background())
	if status.State != provider.HealthDegraded {
		t.Fatalf("未配置时状态必须是 DEGRADED，得到 %s", status.State)
	}
	if !strings.Contains(status.Error, CodeNotConfigured) {
		t.Fatalf("Health.Error 必须包含 DRPC_NOT_CONFIGURED，得到 %q", status.Error)
	}
}

func TestEndpointAllowlistBlocksInternalHosts(t *testing.T) {
	env := newTestEnv(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		func(o *Options) { o.BaseURL = "http://169.254.169.254/solana" })
	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	requireCode(t, err, CodeNotConfigured)
	if !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("应说明 Endpoint 不在允许列表：%v", err)
	}
}

// ---- 健康检查与 30s 缓存 ----

func TestDisableProbesKeepsRealFetchAvailable(t *testing.T) {
	requests := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		_, _ = io.WriteString(w, `{"next_page_token":"","data":[]}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.DisableProbes = true })
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		status := env.client.Health(ctx)
		if status.State != provider.HealthDegraded || !strings.Contains(status.Error, "DRPC_PROBES_DISABLED") {
			t.Fatalf("disabled health status = %#v", status)
		}
		if env.client.HistoryChannelAvailable(ctx) {
			t.Fatal("history channel must not be marked available without a probe")
		}
		channels := env.client.ProbeChannels(ctx)
		for _, channel := range []string{ChannelDataAPI, ChannelSolanaRPC} {
			entry := channels[channel].(channelProbe)
			if entry.OK || !strings.Contains(entry.Message, "DRPC_PROBES_DISABLED") {
				t.Fatalf("disabled channel %s = %#v", channel, entry)
			}
		}
	}
	if len(requests) != 0 || len(env.usage.all()) != 0 {
		t.Fatalf("probes made HTTP requests or usage rows: requests=%d usage=%d", len(requests), len(env.usage.all()))
	}

	if _, err := env.client.Fetch(ctx, provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	}); err != nil {
		t.Fatalf("real fetch must remain available: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("real fetch made %d HTTP requests, want 1", len(requests))
	}
}

func TestHealthNormalAndCached(t *testing.T) {
	var dataCalls, rpcCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			rpcCalls++
			writeRPCResult(t, w, json.RawMessage(`"ok"`))
			return
		}
		dataCalls++
		_, _ = io.WriteString(w, `{"next_page_token":"","data":[]}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	status := env.client.Health(context.Background())
	if status.State != provider.HealthNormal {
		t.Fatalf("两条通道都正常时状态应为 NORMAL，得到 %s (%s)", status.State, status.Error)
	}
	// 付费层 FAST：data_api 并发 4。
	if status.CurrentConcurrency != 4 {
		t.Fatalf("Health 应上报钳制后的有效并发 4，得到 %d", status.CurrentConcurrency)
	}
	if dataCalls != 1 || rpcCalls != 1 {
		t.Fatalf("首次 Health 应各探针一次，得到 data=%d rpc=%d", dataCalls, rpcCalls)
	}
	// 30s 内复用缓存，不再打上游。
	env.client.Health(context.Background())
	if dataCalls != 1 || rpcCalls != 1 {
		t.Fatalf("30s 内的 Health 必须命中缓存，得到 data=%d rpc=%d", dataCalls, rpcCalls)
	}
	env.clock.Advance(probeCacheTTL + time.Second)
	env.client.Health(context.Background())
	if dataCalls != 2 || rpcCalls != 2 {
		t.Fatalf("缓存过期后应重新探针，得到 data=%d rpc=%d", dataCalls, rpcCalls)
	}
	// ProbeChannels 是后台手工命令：即使缓存新鲜也必须真实探测。
	if _, err := json.Marshal(env.client.ProbeChannels(context.Background())); err != nil {
		t.Fatalf("ProbeChannels 必须可 JSON 序列化: %v", err)
	}
	if dataCalls != 3 || rpcCalls != 3 {
		t.Fatalf("ProbeChannels 必须真实探测，得到 data=%d rpc=%d", dataCalls, rpcCalls)
	}
}

func TestProbeChannelsShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeRPCResult(t, w, json.RawMessage(`"ok"`))
			return
		}
		_, _ = io.WriteString(w, `{"next_page_token":"","data":[]}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	probes := env.client.ProbeChannels(context.Background())
	for _, channel := range []string{ChannelDataAPI, ChannelSolanaRPC} {
		entry, ok := probes[channel].(channelProbe)
		if !ok {
			t.Fatalf("探针缺少通道 %s: %#v", channel, probes)
		}
		if !entry.OK || entry.State != string(provider.HealthNormal) {
			t.Fatalf("通道 %s 应探测成功，得到 %#v", channel, entry)
		}
	}
	if _, ok := probes["checked_at"]; !ok {
		t.Fatalf("探针结果必须带 checked_at: %#v", probes)
	}
}

func TestHealthProbeAuthFailureOpensChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid key"}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	status := env.client.Health(context.Background())
	if status.State != provider.HealthOpen {
		t.Fatalf("401 时状态应为 OPEN，得到 %s", status.State)
	}
	if !strings.Contains(status.Error, CodeAuthFailed) {
		t.Fatalf("Health.Error 应包含 DRPC_AUTH_FAILED，得到 %q", status.Error)
	}
	probe := env.client.ProbeChannels(context.Background())[ChannelDataAPI].(channelProbe)
	if probe.OK || probe.ErrorClass != string(provider.ClassAuth) {
		t.Fatalf("401 探针应归 auth，得到 %#v", probe)
	}
}

// ---- 额度闸门 ----

func TestQuotaGateBlocksFetchAtStopPercent(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"next_page_token":"","data":[]}`)
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	spent, limit := int64(45000), int64(45000)
	env.client.SetQuotaSnapshot(QuotaSnapshot{
		Tier: TierPaid, CUSpentToday: &spent, CUDayLimit: &limit,
		SyncedAt: env.clock.Now(), Source: "keys_api",
	})
	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	perr := requireCode(t, err, CodeQuotaExhausted)
	if perr.Class != provider.ClassQuota {
		t.Fatalf("额度用尽必须是 ClassQuota，得到 %s", perr.Class)
	}
	if perr.Retryable {
		t.Fatalf("额度用尽不应重试")
	}
	if calls != 0 {
		t.Fatalf("闸门关闭时不得发出 HTTP 请求，实际 %d 次", calls)
	}
	status := env.client.Health(context.Background())
	if status.State != provider.HealthOpen {
		t.Fatalf("额度用尽时 Health 应为 OPEN，得到 %s", status.State)
	}
	if !strings.Contains(status.Error, CodeQuotaExhausted) {
		t.Fatalf("Health.Error 应包含 DRPC_QUOTA_EXHAUSTED，得到 %q", status.Error)
	}
}

func TestQuotaGateBlocksDisabledKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	disabled := false
	env.client.SetQuotaSnapshot(QuotaSnapshot{Tier: TierPaid, KeyEnabled: &disabled, SyncedAt: env.clock.Now()})
	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	perr := requireCode(t, err, CodeQuotaExhausted)
	if perr.Class != provider.ClassQuota {
		t.Fatalf("Key 停用必须是 ClassQuota，得到 %s", perr.Class)
	}
}

// ---- 层级钳制 ----

func TestUnknownTierClampsEffectiveRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.Tier = TierUnknown })

	status := env.client.QuotaStatus()
	effective, ok := status["effective"].(map[string]any)
	if !ok {
		t.Fatalf("QuotaStatus 必须包含 effective: %#v", status)
	}
	dataAPI, ok := effective[ChannelDataAPI].(map[string]any)
	if !ok {
		t.Fatalf("effective 缺少 data_api: %#v", effective)
	}
	// floor(45000/1837) = 24；CU = 24*1837 = 44088（§6.2 / §10.2）。
	if rpm := num(t, dataAPI, "requests_per_minute"); rpm != 24 {
		t.Fatalf("未知层级 data_api RPM 必须钳到 24，得到 %v", dataAPI["requests_per_minute"])
	}
	if cu := num(t, dataAPI, "cu_per_minute"); cu != 44088 {
		t.Fatalf("未知层级 data_api CU/分钟必须为 44088，得到 %v", dataAPI["cu_per_minute"])
	}
	if reason, _ := dataAPI["effective_reason"].(string); reason != "cu_cap_unknown_tier" {
		t.Fatalf("降档原因应为 cu_cap_unknown_tier，得到 %v", dataAPI["effective_reason"])
	}
	rpc, ok := effective[ChannelSolanaRPC].(map[string]any)
	if !ok {
		t.Fatalf("effective 缺少 solana_rpc: %#v", effective)
	}
	if rps := num(t, rpc, "requests_per_second"); rps != FreeTierMaxRPS {
		t.Fatalf("未知层级 RPC RPS 必须钳到 %d，得到 %v", FreeTierMaxRPS, rpc["requests_per_second"])
	}
	if cu := num(t, rpc, "cu_per_minute"); cu != float64(FreeTierMaxRPS*60*CURPCCall) {
		t.Fatalf("未知层级 RPC CU/分钟必须为 %d，得到 %v", FreeTierMaxRPS*60*CURPCCall, rpc["cu_per_minute"])
	}
}

func TestPaidTierKeepsProfileRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	effective := env.client.QuotaStatus()["effective"].(map[string]any)
	dataAPI := effective[ChannelDataAPI].(map[string]any)
	if rpm := num(t, dataAPI, "requests_per_minute"); rpm != 30 {
		t.Fatalf("付费层 FAST 应保持 RPM 30，得到 %v", dataAPI["requests_per_minute"])
	}
	if dataAPI["effective_reason"] != "" {
		t.Fatalf("付费层新鲜快照不应有降档原因，得到 %v", dataAPI["effective_reason"])
	}
}

func TestProfileSourceAndModeResolverWin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	source := &fakeProfileSource{profiles: map[string]Profile{
		"solana_history|turbo": {
			ProviderKey: ProviderKey, Chain: ChainSolana, RequestsPerMinute: 120, RequestsPerSecond: 0,
			RequestConcurrency: 9, CUPerMinute: 120 * CUDataAPIHistory, HardCeiling: 16, Revision: 7,
		},
	}}
	env := newTestEnv(t, server, func(o *Options) {
		o.ProfileSource = source
		o.ModeResolver = func(ctx context.Context) string { return ModeTurbo }
	})

	status := env.client.QuotaStatus()
	dataAPI := status["effective"].(map[string]any)[ChannelDataAPI].(map[string]any)
	if rpm := num(t, dataAPI, "requests_per_minute"); rpm != 120 {
		t.Fatalf("ProfileSource 的 RPM 应生效，得到 %v", dataAPI["requests_per_minute"])
	}
	if dataAPI["mode"] != ModeTurbo {
		t.Fatalf("ModeResolver 应提供 turbo，得到 %v", dataAPI["mode"])
	}
	if rev := num(t, dataAPI, "profile_revision"); rev != 7 {
		t.Fatalf("Profile revision 应透传，得到 %v", dataAPI["profile_revision"])
	}
	if source.calls == 0 {
		t.Fatalf("应调用 ProfileSource")
	}
}

type fakeProfileSource struct {
	mu       sync.Mutex
	calls    int
	profiles map[string]Profile
}

func (f *fakeProfileSource) Profile(ctx context.Context, providerKey, chain, dataset, operation, mode string) (Profile, bool) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	profile, ok := f.profiles[operation+"|"+mode]
	return profile, ok
}

// ---- 限流原语：CU 上限把 RPM 钳住 ----

func TestLimiterCUBoundCapsRequestRate(t *testing.T) {
	clock := newFakeClock()
	limiter := newChannelLimiter(2, 60, 45000, float64(CUDataAPIHistory), clock.Now, clock.Sleep)
	if got := limiter.effectiveRPM(); got < 24.4 || got > 24.5 {
		t.Fatalf("45000 CU/分钟 + 1837 CU/请求 => 24.49 次/分钟，得到 %v", got)
	}

	// 连续三次请求：第一次用初始令牌，后两次必须等待假时钟推进（不真的 sleep）。
	start := clock.Now()
	for i := 0; i < 3; i++ {
		release, err := limiter.acquire(context.Background(), float64(CUDataAPIHistory))
		if err != nil {
			t.Fatalf("acquire %d 失败: %v", i, err)
		}
		release()
	}
	elapsed := clock.Now().Sub(start)
	// 两次间隔合计约 2 * (60/24.49) 秒。
	if elapsed < 4*time.Second || elapsed > 6*time.Second {
		t.Fatalf("三次请求应被 CU 上限摊到约 4.9 秒，得到 %v", elapsed)
	}
}

func TestLimiterZeroRateWaitsForContext(t *testing.T) {
	clock := newFakeClock()
	limiter := newChannelLimiter(1, 0, 0, float64(CUDataAPIHistory), clock.Now, clock.Sleep)
	// 初始令牌允许一次请求（桶容量 = 单次请求所需）。
	release, err := limiter.acquire(context.Background(), float64(CUDataAPIHistory))
	if err != nil {
		t.Fatalf("第一次请求应放行: %v", err)
	}
	release()
	// 速率为 0 时后续请求只能等到 ctx 结束，绝不忙等或凭空放行。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := limiter.acquire(ctx, float64(CUDataAPIHistory)); err == nil {
		t.Fatalf("速率为 0 时应该等到 ctx 结束")
	}
}

// ---- 脱敏 ----

func TestMaskURLAndSanitizeText(t *testing.T) {
	masked := maskURL("https://lb.drpc.live/solana/" + testKey + "/lambda/v1/transactions/abc/history?limit=100&page_token=secret-token")
	if strings.Contains(masked, testKey) || strings.Contains(masked, "secret-token") {
		t.Fatalf("maskURL 必须隐藏 key 与 page_token：%s", masked)
	}
	if !strings.HasPrefix(masked, "https://lb.drpc.live/solana/***/lambda/v1/transactions/abc/history?") {
		t.Fatalf("maskURL 形状不符合规范：%s", masked)
	}
	text := sanitizeText("Get \"https://lb.drpc.live/solana/"+testKey+"/lambda/x\": dial tcp: timeout", testKey)
	if strings.Contains(text, testKey) {
		t.Fatalf("sanitizeText 必须移除 key：%s", text)
	}
}

func TestTransportErrorNeverLeaksKey(t *testing.T) {
	// 指向一个已关闭的端口：错误字符串里通常带完整 URL（含 key）。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := server.URL
	server.Close()

	env := newTestEnv(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		func(o *Options) { o.BaseURL = base + "/solana" })
	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	if err == nil {
		t.Fatalf("连接失败必须返回错误")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("错误信息泄露了 key：%v", err)
	}
	rows := env.usage.all()
	if len(rows) != 1 {
		t.Fatalf("每个 HTTP 请求都要有一条用量行，得到 %d", len(rows))
	}
	if strings.Contains(rows[0].APIError, testKey) {
		t.Fatalf("用量行的 APIError 泄露了 key：%s", rows[0].APIError)
	}
}
