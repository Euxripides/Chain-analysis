package drpc

// 本文件是 dRPC Provider 的组装层：Options -> Client、Fetch 分派、有效执行计划（plan）、
// 通道限流器缓存、HTTP 往返与用量收尾。
//
// 关键行为（规范 §1.4）：
//   - 每次请求前解析 RuntimeKeyRef（30s 缓存），未配置一律 DRPC_NOT_CONFIGURED；
//   - 限流顺序：通道信号量 -> CU 桶 -> RPM/RPS 桶 -> quotaGate；
//   - 日志/错误永不出现带 Key 的 URL（统一 maskURL / sanitizeText）。
import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	soladdr "github.com/blockchain-platform/blockchain-platform/internal/solana"
)

// Client 是 dRPC Solana 双通道 Provider（Data API + Solana JSON-RPC）。
type Client struct {
	cfg  config
	hook provider.UsageHook
	log  *slog.Logger

	httpData *http.Client
	httpRPC  *http.Client

	keys    *keyCache
	quota   *quotaGate
	cursors *cursorGuard
	health  *healthTracker

	// endpointErr 非空表示 BaseURL 不合法或不在允许列表（防 SSRF）：数据面返回
	// DRPC_NOT_CONFIGURED，后台据此显示"未配置"。
	endpointErr string

	seq atomic.Int64

	mu       sync.Mutex
	limiters map[string]*channelLimiter
}

// New 构造 dRPC Provider。
//
// 永不返回 nil：配置缺失/Endpoint 非法时仍然返回一个可用对象（Fetch/Health 返回
// DRPC_NOT_CONFIGURED，后台可以据此展示"未配置"而不是崩溃）。
func New(opts Options, hook provider.UsageHook) *Client {
	cfg := opts.normalize()
	if hook == nil {
		hook = cfg.hook
	}
	base := cfg.baseURL
	endpointErr := ""
	if base == "" {
		endpointErr = "dRPC base url is empty"
	} else if err := checkEndpointAllowed(base); err != nil {
		endpointErr = err.Error()
	}
	client := &Client{
		cfg:         cfg,
		hook:        hook,
		log:         cfg.log,
		httpData:    newHTTPClient(cfg.dataTimeout),
		httpRPC:     newHTTPClient(cfg.rpcTimeout),
		keys:        newKeyCache(keyCacheTTL, cfg.now),
		quota:       newQuotaGate(cfg),
		cursors:     newCursorGuard(),
		health:      newHealthTracker(cfg.now),
		limiters:    map[string]*channelLimiter{},
		endpointErr: endpointErr,
	}
	return client
}

func newHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// 开发文档 §16.8：带认证信息的请求不允许被自动重定向到其它 Host。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}
			if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				return http.ErrUseLastResponse
			}
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// checkEndpointAllowed 做 Endpoint 允许列表校验（开发文档 §16.7/§16.9，防 SSRF）。
//
// 允许：https + drpc.live（含子域）；环回地址（httptest/本地联调）。
// 其它主机一律拒绝——管理员把 Endpoint 改成内网地址是 SSRF 攻击面。
func checkEndpointAllowed(base string) error {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("dRPC base url is not a valid absolute url")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "127.0.0.1" || host == "::1" || host == "localhost" {
		return nil
	}
	if host != "drpc.live" && !strings.HasSuffix(host, ".drpc.live") {
		return fmt.Errorf("dRPC base url host %q is not in the allowlist", host)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("dRPC base url must use https, got %q", parsed.Scheme)
	}
	return nil
}

func (c *Client) Name() string { return ProviderKey }

func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Name: ProviderKey,
		// internal_transactions 不在其中：Solana 没有该数据集（规范 §1.4）。
		Datasets:    []string{DatasetTransactions, DatasetTokenTransfers, DatasetNFTTransfers, DatasetLogs, DatasetBalances},
		MaxPageSize: maxSignaturesPerCall,
		// Data API 数据集支持时间范围；logs 用 slot 区间，balances 是取数时刻快照。
		SupportsTimeRange: map[string]bool{
			DatasetTransactions:   true,
			DatasetTokenTransfers: true,
			DatasetNFTTransfers:   true,
			DatasetLogs:           false,
			DatasetBalances:       false,
		},
	}
}

// SetQuotaSnapshot 由 drpcmgmt 同步后注入（跨通道共享闸门）。
func (c *Client) SetQuotaSnapshot(s QuotaSnapshot) { c.quota.set(s) }

// Fetch 实现 provider.Provider：按 dataset 选择通道。
func (c *Client) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	if !strings.EqualFold(strings.TrimSpace(req.Chain), ChainSolana) {
		err := newError(provider.ClassInvalidRequest, CodeInvalidResponse,
			"dRPC Solana provider only serves chain=solana, got "+strings.TrimSpace(req.Chain))
		err.Retryable = false
		return provider.FetchResult{}, err
	}
	channel, operation, ok := channelForDataset(req.Dataset)
	if !ok {
		// internal_transactions 走这里：Message 以 SOLANA_DATASET_UNSUPPORTED 开头（规范 §1.4）。
		return provider.FetchResult{}, unsupportedDataset(req.Dataset)
	}
	exec, err := c.plan(ctx, req.Dataset, channel, operation)
	if err != nil {
		return provider.FetchResult{}, err
	}
	if channel == ChannelDataAPI {
		return c.fetchDataAPI(ctx, req, exec)
	}
	return c.fetchSolanaRPC(ctx, req, exec)
}

// ---- 有效执行计划 ----

// plan 是一次 Fetch 的有效执行参数（模式/速率都已按层级与额度钳制）。
type plan struct {
	channel       string
	dataset       string
	operation     string
	requestedMode string
	mode          string
	clampReason   string

	profile Profile

	concurrency  int
	pageSize     int
	cuPerRequest int64
	reqPerMinute float64
	cuPerMinute  int64
	timeout      time.Duration

	limiter *channelLimiter
}

func (c *Client) plan(ctx context.Context, dataset, channel, operation string) (plan, error) {
	if err := c.configError(); err != nil {
		return plan{}, err
	}
	requested := c.resolveMode(ctx)
	dec := c.quota.decision()
	if dec.Blocked {
		return plan{}, c.quota.blockedError(dec)
	}
	mode := lowerMode(requested, dec.AllowedMode)

	profile, ok := c.profile(ctx, dataset, operation, mode)
	if !ok {
		profile = defaultProfile(channel, mode)
	}
	profile.Dataset = dataset
	profile.OperationClass = operation
	profile.Mode = mode
	profile.EffectiveMode = mode
	// clampProfile 按账户层级/额度快照把速率与 CU 钳到"不假装满速"的值。
	profile = clampProfile(profile, channel, c.cfg, dec)

	cuPerRequest := int64(CUDataAPIHistory)
	if channel == ChannelSolanaRPC {
		cuPerRequest = CURPCCall
	}
	concurrency := profile.RequestConcurrency
	if profile.HardCeiling > 0 && concurrency > profile.HardCeiling {
		concurrency = profile.HardCeiling
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	reqPerMinute := profile.RequestsPerMinute
	if channel == ChannelSolanaRPC {
		reqPerMinute = profile.RequestsPerSecond * 60
	}
	timeout := c.cfg.dataTimeout
	if channel == ChannelSolanaRPC {
		timeout = c.cfg.rpcTimeout
	}
	if profile.RequestTimeoutSeconds > 0 {
		timeout = time.Duration(profile.RequestTimeoutSeconds) * time.Second
	}
	pageSize := profile.PageSize
	if channel == ChannelDataAPI {
		pageSize = clampInt(pageSize, 1, c.cfg.historyPageSize)
	} else {
		pageSize = clampInt(pageSize, 1, maxSignaturesPerCall)
	}

	exec := plan{
		channel: channel, dataset: dataset, operation: operation,
		requestedMode: requested, mode: mode, clampReason: profile.ClampReason,
		profile:      profile,
		concurrency:  concurrency,
		pageSize:     pageSize,
		cuPerRequest: cuPerRequest,
		reqPerMinute: reqPerMinute,
		cuPerMinute:  profile.CUPerMinute,
		timeout:      timeout,
	}
	exec.limiter = c.limiterFor(exec)
	return exec, nil
}

// profile 解析档位：ProfileSource（数据库/内置表）优先，未命中由调用方回退内置默认矩阵。
func (c *Client) profile(ctx context.Context, dataset, operation, mode string) (Profile, bool) {
	if c.cfg.profileSource == nil {
		return Profile{}, false
	}
	profile, ok := c.cfg.profileSource.Profile(ctx, ProviderKey, ChainSolana, dataset, operation, mode)
	if !ok {
		return Profile{}, false
	}
	return profile, true
}

// limiterFor 返回该（通道/模式/并发）组合的限流器。
//
// 键里带并发：信号量容量在创建时固定，Profile 调整并发后需要新的信号量；
// 代价是速率桶重置（容量只有 1 个令牌，不会造成突发）。
func (c *Client) limiterFor(exec plan) *channelLimiter {
	key := fmt.Sprintf("%s|%s|%d", exec.channel, exec.mode, exec.concurrency)
	c.mu.Lock()
	defer c.mu.Unlock()
	limiter := c.limiters[key]
	if limiter == nil {
		limiter = newChannelLimiter(exec.concurrency, exec.reqPerMinute, float64(exec.cuPerMinute),
			float64(exec.cuPerRequest), c.cfg.now, c.cfg.sleep)
		c.limiters[key] = limiter
		return limiter
	}
	limiter.configure(exec.reqPerMinute, float64(exec.cuPerMinute), float64(exec.cuPerRequest))
	return limiter
}

// resolveMode 取"当前任务请求模式"：由 app 注入的 ModeResolver 提供，缺省 fast。
func (c *Client) resolveMode(ctx context.Context) string {
	if c.cfg.modeResolver != nil {
		if mode := strings.TrimSpace(c.cfg.modeResolver(ctx)); mode != "" {
			return normalizeMode(mode)
		}
	}
	return DefaultMode
}

// ---- 密钥解析 ----

// runtimeKey 解析 RuntimeKeyRef（30s 缓存，后台加 Key 后不重启即生效）。
func (c *Client) runtimeKey(ctx context.Context) (string, error) {
	if c.cfg.keyRef == "" {
		return "", notConfigured("dRPC runtime key reference is not configured (DRPC_RUNTIME_KEY_REF is empty)")
	}
	if c.cfg.secrets == nil {
		return "", notConfigured("dRPC secret resolver is not configured")
	}
	key, err := c.keys.get(ctx, c.cfg.secrets, c.cfg.keyRef)
	if err != nil {
		return "", notConfigured("dRPC runtime key " + sanitizeText(c.cfg.keyRef, "") + " is not resolvable: " + sanitizeText(err.Error(), ""))
	}
	return key, nil
}

type keyCache struct {
	mu  sync.Mutex
	ttl time.Duration
	now func() time.Time

	ref   string
	value string
	err   error
	at    time.Time
	valid bool
}

func newKeyCache(ttl time.Duration, now func() time.Time) *keyCache {
	return &keyCache{ttl: ttl, now: now}
}

// get 返回缓存的明文 Key；解析失败同样缓存 TTL，避免打爆 secret store。
func (k *keyCache) get(ctx context.Context, resolver Resolver, ref string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	if k.valid && k.ref == ref && now.Sub(k.at) < k.ttl {
		return k.value, k.err
	}
	value, err := resolver.Resolve(ctx, ref)
	value = strings.TrimSpace(value)
	if err == nil && value == "" {
		err = ErrNotConfigured
	}
	k.ref, k.value, k.err, k.at, k.valid = ref, value, err, now, true
	if err != nil {
		return "", err
	}
	return value, nil
}

// ---- 模式展示 ----

// EffectiveMode 返回在 requested 模式下、结合当前额度/健康/新鲜度后的实际可运行模式与降档原因。
//
// 只读内存状态、无网络调用、并发安全；内部复用 quotaDecision/lowerMode，
// 不在本方法里重复任何阈值判断（否则后台显示与实际限流会漂移）。
//
// reason 词汇表（与 Health/QuotaStatus 的 effective_reason 同一套）：
//
//	cu_cap_free_tier / cu_cap_unknown_tier  免费或未知层级的 CU 上限（速率被钳，模式不变）
//	key_rate_limit                          Keys API 的 Key 级速率上限
//	management_stale                        管理快照超过 15 分钟：TURBO 不可用
//	management_very_stale                   管理快照超过 60 分钟：最多 SAFE
//	quota_warn                              当日 CU 达到告警线：最多 FAST
//	balance_low                             账户余额低于告警值：最多 FAST
//	quota_safe_only                         当日 CU 达到安全线：最多 SAFE
//	quota_stop                              当日 CU 用尽：暂停新请求
//	key_disabled                            Key 被停用：暂停新请求
//
// 没有额度快照时返回 (requested, "")；requested 为空按 DefaultMode（fast）。
func (c *Client) EffectiveMode(requested string) (string, string) {
	mode := normalizeMode(requested)
	dec := c.quota.decision()
	if dec.Blocked {
		// 闸门关闭：实际不会发出请求，返回额度允许的最低档与阻断原因，供后台显示"已暂停"。
		return lowerMode(mode, ModeSafe), dec.BlockCode
	}
	effective := lowerMode(mode, dec.AllowedMode)
	if dec.Reason != "" {
		return effective, dec.Reason
	}
	// 没有模式降档，但层级/快照仍可能把速率钳住：把原因一并上报，避免"看起来是满速"。
	if _, hasSnapshot := c.quota.snapshot(); hasSnapshot {
		switch {
		case c.cfg.tier == TierFree:
			return effective, "cu_cap_free_tier"
		case c.cfg.tier != TierPaid:
			return effective, "cu_cap_unknown_tier"
		}
	}
	return effective, ""
}

// ---- HTTP ----

// httpCall 描述一次 HTTP 往返；用量行由调用方在解码后经 finishUsage 写入
// （保证"每次 HTTP 请求恰好一条 UsageRow"，且 CU 只在真正拿到 200 时计入）。
type httpCall struct {
	// ctx 随调用一起传递，供用量归因读取 task/shard（见 usageAttribution）。
	ctx       context.Context
	channel   string
	apiType   string
	dataset   string
	method    string
	usageTyp  string
	cu        int64
	pageSize  int
	key       string
	requestID string
	priority  string
	attemptNo int

	httpMethod  string
	url         string
	body        []byte
	contentType string
	client      *http.Client
	// txHash 只用于用量归因（tx_detail）。
	txHash string
}

type httpResult struct {
	raw    []byte
	status int
	ttfb   time.Duration
	body   time.Duration
}

func (c *Client) doHTTP(ctx context.Context, call httpCall) (httpResult, error) {
	var result httpResult
	requestURL := call.url
	var reader io.Reader
	if call.body != nil {
		reader = bytes.NewReader(call.body)
	}
	req, err := http.NewRequestWithContext(ctx, call.httpMethod, requestURL, reader)
	if err != nil {
		perr := newError(provider.ClassInvalidRequest, CodeInvalidResponse, sanitizeText(err.Error(), call.key))
		perr.Retryable = false
		return result, perr
	}
	req.Header.Set("Accept", "application/json")
	if call.contentType != "" {
		req.Header.Set("Content-Type", call.contentType)
	}
	started := c.cfg.now()
	client := call.client
	if client == nil {
		client = c.httpRPC
	}
	res, err := client.Do(req)
	result.ttfb = c.cfg.now().Sub(started)
	if err != nil {
		return result, classifyTransport(ctx, err, call.key)
	}
	defer func() { _ = res.Body.Close() }()
	result.status = res.StatusCode
	if res.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		result.body = c.cfg.now().Sub(started) - result.ttfb
		result.raw = snippet
		return result, classifyHTTPStatus(res.StatusCode, res.Header, snippet, call.key)
	}
	bodyStarted := c.cfg.now()
	raw, readErr := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	result.body = c.cfg.now().Sub(bodyStarted)
	result.raw = raw
	if readErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, classifyTransport(ctx, ctxErr, call.key)
		}
		return result, bodyTruncated("dRPC response body read failed: " + sanitizeText(readErr.Error(), call.key))
	}
	if int64(len(raw)) > maxResponseBytes {
		return result, bodyTruncated(fmt.Sprintf("dRPC response body exceeds %d bytes", maxResponseBytes))
	}
	if res.ContentLength > 0 && int64(len(raw)) != res.ContentLength {
		return result, bodyTruncated(fmt.Sprintf("dRPC response body truncated: got %d of %d bytes", len(raw), res.ContentLength))
	}
	return result, nil
}

// finishUsage 写用量行；每个 HTTP 请求调用且只调用一次。
func (c *Client) finishUsage(call httpCall, result httpResult, started time.Time, rows int, err error) {
	// 归因：任务/分片来自 ctx，通道来自本次调用本身。
	cu := int64(0)
	if result.status == http.StatusOK {
		// 只有真正拿到响应的请求才计入 CU：HTTP 错误（401/402/429/5xx）无法确认
		// 上游是否扣费，宁可少记也不编造。
		cu = call.cu
	}
	c.recordUsage(usageMeta{
		ctx: call.ctx, channel: call.channel,
		dataset: call.dataset, apiType: call.apiType, method: call.method, usageTyp: call.usageTyp,
		rows: rows, cu: cu, httpStatus: result.status, err: err,
		requestID: call.requestID, started: started, ttfb: result.ttfb, body: result.body,
		decodedBytes: int64(len(result.raw)), pageSize: call.pageSize,
		retryAttempt: call.attemptNo, priority: call.priority, txHash: call.txHash,
	})
}

// limiterError 把限流等待的 ctx 错误转成 provider 分类错误。
func (c *Client) limiterError(ctx context.Context, err error) *provider.Error {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return provider.NewCancelled(CodeInvalidResponse + ": dRPC request cancelled while waiting for a rate limit slot")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		perr := newError(provider.ClassRequestTimeout, CodeInvalidResponse, "dRPC rate limit wait exceeded the request deadline")
		perr.Retryable, perr.ReduceConcurrency = true, true
		return perr
	}
	perr := newError(provider.ClassUpstream, CodeInvalidResponse, sanitizeText(err.Error(), ""))
	perr.Retryable = true
	return perr
}

// configError 返回配置级不可用错误（未启用 / Endpoint 非法 / 未配置 Key 引用）。
func (c *Client) configError() *provider.Error {
	if !c.cfg.enabled {
		return notConfigured("dRPC provider is disabled (DRPC_ENABLED=false)")
	}
	if c.endpointErr != "" {
		return notConfigured(c.endpointErr)
	}
	return nil
}

// configured 表示"具备发出请求的最低配置"（Enabled + Endpoint + Key 引用 + Resolver）。
//
// 它只看配置，不实际解析密钥；需要"Key 当前是否可解析"请用 Configured。
func (c *Client) configured() bool {
	if c.configError() != nil {
		return false
	}
	return c.cfg.keyRef != "" && c.cfg.secrets != nil
}

// Configured 报告 Provider 当前是否可用：配置齐备**并且 runtime key 能解析出明文**。
//
// 密钥走 30s 缓存（与数据面共用），因此可以按需调用；并发安全。
// 路由开关判定建议用这个入口，而不是各自去调 secret.Manager.Resolve：
// 缓存、错误分类与 DRPC_NOT_CONFIGURED 文案都保持一致。
func (c *Client) Configured(ctx context.Context) bool {
	return c.UnavailableReason(ctx) == ""
}

// UnavailableReason 返回不可用原因（Message 以错误码开头）；可用时返回空串。
func (c *Client) UnavailableReason(ctx context.Context) string {
	if reason := c.unavailableReason(); reason != "" {
		return reason
	}
	if _, err := c.runtimeKey(ctx); err != nil {
		return sanitizeText(err.Error(), "")
	}
	return ""
}

// requestID 生成内部请求关联 ID（进入 UsageRow.RequestID 与 sourceRequestId 兜底）。
func (c *Client) requestID(req provider.FetchRequest) string {
	if trimmed := strings.TrimSpace(req.RequestID); trimmed != "" {
		return trimmed
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "drpc-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("drpc-%d-%d", c.cfg.now().UnixNano(), c.seq.Add(1))
}

// rpcCallID 生成 JSON-RPC 请求 id（与内部 request id 关联，便于上游对账）。
func (c *Client) rpcCallID(requestID string) string {
	seq := c.seq.Add(1)
	if requestID == "" {
		return fmt.Sprintf("drpc-%d", seq)
	}
	return fmt.Sprintf("%s-%d", requestID, seq)
}

// validateAddress 校验 Solana 地址：base58 大小写敏感，原样返回（绝不 ToLower）。
func validateAddress(raw string) (string, error) {
	address, err := soladdr.NormalizeSolanaAddress(raw)
	if err != nil {
		perr := newError(provider.ClassInvalidRequest, CodeInvalidResponse, "invalid solana address: "+sanitizeText(err.Error(), ""))
		perr.Retryable = false
		return "", perr
	}
	return address, nil
}

// fetchResult 统一组装 FetchResult（RequestPageSize 用**有效**页大小，不假装满速）。
func (c *Client) fetchResult(req provider.FetchRequest, exec plan, requestID string, rows []json.RawMessage,
	nextToken string, hasMore bool, started time.Time, result httpResult, raw []byte) provider.FetchResult {
	pageSize := exec.pageSize
	if req.PageSize > 0 && req.PageSize < pageSize {
		pageSize = req.PageSize
	}
	return provider.FetchResult{
		Rows:             rows,
		NextPageToken:    nextToken,
		HasMore:          hasMore,
		DecodedBytes:     int64(len(raw)),
		RequestID:        requestID,
		RequestFromBlock: req.FromBlock,
		RequestToBlock:   req.ToBlock,
		RequestPageSize:  pageSize,
		AttemptNo:        req.AttemptNo,
		RawResponseBody:  raw,
		StartedAt:        started,
		BodyCompleteAt:   started.Add(result.ttfb + result.body),
	}
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if max > 0 && value > max {
		return max
	}
	return value
}
