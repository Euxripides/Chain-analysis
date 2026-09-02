package rpc

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
)

// ---- RPC Gateway（§14/§248）----
//
// 所有 RPC 都必须经过这里：Provider 选择、限流、并发、熔断、重试、
// 超时、缓存、Singleflight、预算、用量、指标、脱敏日志。
// 业务层只面对 ChainReader 等类型化服务，不直接构造 JSON（§13）。

// GatewayOptions 装配参数。
type GatewayOptions struct {
	Chains       *chain.Registry
	Methods      *MethodRegistry
	Router       *Router
	Capabilities *CapabilityStore
	Cache        *Cache
	Budgets      *BudgetManager
	Rate         *RateController
	Concurrency  *ConcurrencyController
	Breakers     *Breakers
	Health       *HealthTracker

	Usage   UsageHook
	Pricing Pricing
	Retry   RetryPolicy
	Log     *slog.Logger

	// Credential 是凭证引用（不是密钥本身），用于限流维度（§32/§190）
	Credential string
	// Secrets 需要从日志/错误中抹掉的明文（§31/§224）
	Secrets []string

	MaxRequestBytes int
	MaxBatchSize    int
}

// Gateway 统一 RPC 网关。
type Gateway struct {
	chains       *chain.Registry
	methods      *MethodRegistry
	router       *Router
	caps         *CapabilityStore
	cache        *Cache
	flight       *Singleflight
	budgets      *BudgetManager
	rate         *RateController
	conc         *ConcurrencyController
	breakers     *Breakers
	health       *HealthTracker
	usage        UsageHook
	pricing      Pricing
	retry        RetryPolicy
	log          *slog.Logger
	credential   string
	secrets      []string
	maxReqBytes  int
	maxBatchSize int
}

func NewGateway(opts GatewayOptions) *Gateway {
	if opts.Chains == nil {
		opts.Chains = chain.DefaultRegistry()
	}
	if opts.Methods == nil {
		opts.Methods = DefaultMethodRegistry()
	}
	if opts.Capabilities == nil {
		opts.Capabilities = NewCapabilityStore()
	}
	if opts.Breakers == nil {
		opts.Breakers = NewBreakers(40, 0.5, 30*time.Second)
	}
	if opts.Health == nil {
		opts.Health = NewHealthTracker()
	}
	if opts.Concurrency == nil {
		opts.Concurrency = NewConcurrencyController(64)
	}
	if opts.Rate == nil {
		opts.Rate = NewRateController(25)
	}
	if opts.Cache == nil {
		opts.Cache = NewCache(100000, true)
	}
	if opts.Router == nil {
		opts.Router = NewRouter(opts.Capabilities, opts.Breakers, opts.Health, opts.Concurrency)
	}
	if opts.Budgets == nil {
		opts.Budgets = NewBudgetManager(24*time.Hour, nil, true)
	}
	if opts.Retry.MaxAttempts == 0 {
		opts.Retry = DefaultRetryPolicy()
	}
	if opts.Pricing.CreditsPerCall == 0 {
		opts.Pricing = DefaultPricing()
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.MaxRequestBytes <= 0 {
		opts.MaxRequestBytes = 1 << 20
	}
	if opts.MaxBatchSize <= 0 {
		opts.MaxBatchSize = 100
	}
	return &Gateway{
		chains: opts.Chains, methods: opts.Methods, router: opts.Router, caps: opts.Capabilities,
		cache: opts.Cache, flight: NewSingleflight(), budgets: opts.Budgets, rate: opts.Rate,
		conc: opts.Concurrency, breakers: opts.Breakers, health: opts.Health, usage: opts.Usage,
		pricing: opts.Pricing, retry: opts.Retry, log: opts.Log, credential: opts.Credential,
		secrets: opts.Secrets, maxReqBytes: opts.MaxRequestBytes, maxBatchSize: opts.MaxBatchSize,
	}
}

// Router 暴露给装配层注册 Provider。
func (g *Gateway) Router() *Router { return g.router }

// Capabilities 能力存储（供探测与管理接口使用）。
func (g *Gateway) Capabilities() *CapabilityStore { return g.caps }

// Methods 方法登记表。
func (g *Gateway) Methods() *MethodRegistry { return g.methods }

// Chains 链注册表。
func (g *Gateway) Chains() *chain.Registry { return g.chains }

// Budgets 预算管理器。
func (g *Gateway) Budgets() *BudgetManager { return g.budgets }

// HealthSnapshot 全部 Provider/Chain 健康画像（Admin Dashboard，§144）。
func (g *Gateway) HealthSnapshot() []ProviderHealth {
	var out []ProviderHealth
	for _, chainSlug := range g.router.Chains() {
		for _, name := range g.router.Providers(chainSlug) {
			state := g.breakers.State(BreakerKey(name, chainSlug, ClassChainHead))
			key := LimiterKey{Provider: name, Credential: g.credential, Chain: chainSlug}
			out = append(out, g.health.Snapshot(name, chainSlug, state, g.conc.InFlight(key)))
		}
	}
	return out
}

// CacheSize 当前缓存条目数（观测）。
func (g *Gateway) CacheSize() int { return g.cache.Len() }

// prepare 请求前置校验：链、方法登记、Allowlist、只读守卫、请求体上限。
func (g *Gateway) prepare(req *Request) (MethodSpec, *Error) {
	if _, err := g.chains.Require(req.Chain); err != nil {
		return MethodSpec{}, &Error{Class: ClassInvalidParams, Chain: req.Chain, Method: req.Method,
			Message: err.Error()}
	}
	spec, known := g.methods.Spec(req.Method)
	if !known {
		MethodDeniedTotal.WithLabelValues(req.Method).Inc()
		return MethodSpec{}, &Error{Class: ClassMethodDenied, Chain: req.Chain, Method: req.Method,
			Message: "method is not registered in the RPC method registry"}
	}
	// 只读守卫 + Allowlist：写方法在到达 Provider 之前就被拒绝（§116/§223/§247-10）
	if !g.methods.Allowed(req.Method) {
		MethodDeniedTotal.WithLabelValues(req.Method).Inc()
		reason := "method is not on the RPC allowlist"
		if !spec.ReadOnly {
			reason = "write methods are disabled (read-only RPC)"
		}
		return MethodSpec{}, &Error{Class: ClassMethodDenied, Chain: req.Chain, Method: req.Method, Message: reason}
	}
	if req.Class == "" {
		req.Class = spec.Class
	}
	// 请求体上限（§119）
	body, err := json.Marshal(req.Params)
	if err != nil {
		return MethodSpec{}, &Error{Class: ClassInvalidParams, Chain: req.Chain, Method: req.Method,
			Message: "params are not serialisable"}
	}
	limit := spec.MaxRequestBytes
	if limit <= 0 || limit > g.maxReqBytes {
		limit = g.maxReqBytes
	}
	if len(body) > limit {
		return MethodSpec{}, &Error{Class: ClassRequestTooLarge, Chain: req.Chain, Method: req.Method,
			Message: "request params exceed the configured size limit"}
	}
	if !ValidPurpose(req.Budget.Purpose) {
		return MethodSpec{}, &Error{Class: ClassInvalidParams, Chain: req.Chain, Method: req.Method,
			Message: "unknown rpc purpose " + req.Budget.Purpose}
	}
	return spec, nil
}

// Call 执行一次 RPC（§14）。
func (g *Gateway) Call(ctx context.Context, req Request) (Response, error) {
	req = req.Normalize()
	spec, guardErr := g.prepare(&req)
	if guardErr != nil {
		return Response{}, guardErr
	}

	cacheKey := CacheKey(req)
	cacheable := spec.Cacheable && !req.CachePolicy.Disabled
	if cacheable && req.Consistency != ConsistencyForceRefresh {
		if cached, ok := g.cache.Get(cacheKey); ok {
			CacheHitTotal.WithLabelValues(req.Chain, req.Method).Inc()
			cached.CacheHit = true
			cached.Chain, cached.Method = req.Chain, req.Method
			g.record(usageInput{Provider: cached.Provider, Chain: req.Chain, Method: req.Method,
				Class: spec.Class, Purpose: req.Budget.Purpose, Priority: req.Priority,
				MethodCalls: 1, Status: "cache_hit", CacheHit: true, JobID: req.Budget.JobID,
				Investigation: req.Budget.InvestigationID, TraceID: req.TraceID})
			return cached, nil
		}
		CacheMissTotal.WithLabelValues(req.Chain, req.Method).Inc()
	}

	// local_only：绝不发 Provider 请求（§204/§247）
	if req.Consistency == ConsistencyLocalOnly {
		return Response{}, &Error{Class: ClassLocalMiss, Chain: req.Chain, Method: req.Method,
			Message: "local_only requested but no cached result is available"}
	}

	response, err, shared := g.flight.Do(cacheKey, func() (Response, error) {
		return g.execute(ctx, req, spec, cacheKey)
	})
	if shared {
		SingleflightHitTotal.WithLabelValues(req.Chain, req.Method).Inc()
		if err == nil {
			response.SingleflightHit = true
			g.record(usageInput{Provider: response.Provider, Chain: req.Chain, Method: req.Method,
				Class: spec.Class, Purpose: req.Budget.Purpose, Priority: req.Priority,
				MethodCalls: 1, Status: "singleflight_hit", Singleflight: true, JobID: req.Budget.JobID,
				Investigation: req.Budget.InvestigationID, TraceID: req.TraceID})
		}
	}
	return response, err
}

func (g *Gateway) execute(ctx context.Context, req Request, spec MethodSpec, cacheKey string) (Response, error) {
	// 预算准入必须在任何 Provider 请求之前（§167/§214）
	isTrace := spec.Class == ClassTrace || spec.Class == ClassDebugTrace
	if err := g.budgets.Allow(req.Budget, int64(spec.EstimatedCostWeight), isTrace); err != nil {
		BudgetRejectedTotal.WithLabelValues(req.Budget.Purpose).Inc()
		return Response{}, err
	}
	return g.executeAdmitted(ctx, req, spec, cacheKey)
}

// executeAdmitted 执行已经通过预算准入的请求。Batch Provider 不可用时逐项
// 降级会走这里，避免同一个 method call 被预算系统重复扣减。
func (g *Gateway) executeAdmitted(ctx context.Context, req Request, spec MethodSpec, cacheKey string) (Response, error) {
	candidates, routeErr := g.router.Candidates(ctx, req, spec)
	if routeErr != nil {
		return Response{}, routeErr
	}

	var lastErr *Error
	for _, candidate := range candidates {
		response, err := g.callProvider(ctx, req, spec, candidate)
		if err == nil {
			if spec.Cacheable && !req.CachePolicy.Disabled {
				ttl := spec.CacheTTL(req.BlockRef)
				if req.CachePolicy.TTLOverride > 0 {
					ttl = time.Duration(req.CachePolicy.TTLOverride)
				}
				g.cache.Put(cacheKey, response, ttl)
			}
			return response, nil
		}
		lastErr = err
		// 能力类错误：换下一个 Provider（§222）
		if IsCapability(err.Class) {
			continue
		}
		// 其它致命错误没有换 Provider 的意义
		if IsFatal(err.Class) {
			return Response{}, err
		}
		// 可重试错误在该 Provider 上已用尽重试 → failover（§229）
	}
	if lastErr == nil {
		lastErr = &Error{Class: ClassNoProvider, Chain: req.Chain, Method: req.Method,
			Message: "no provider produced a response"}
	}
	return Response{}, lastErr
}

func (g *Gateway) callProvider(ctx context.Context, req Request, spec MethodSpec, candidate ProviderCandidate) (Response, *Error) {
	key := LimiterKey{Provider: candidate.Name, Credential: g.credential, Chain: req.Chain, Class: spec.Class}
	breakerKey := BreakerKey(candidate.Name, req.Chain, spec.Class)

	for attempt := 0; ; attempt++ {
		allowed, state := g.breakers.Allow(breakerKey)
		CircuitStateGauge.WithLabelValues(candidate.Name, req.Chain, string(spec.Class)).Set(circuitStateValue(state))
		if !allowed {
			return Response{}, &Error{Class: ClassCircuitOpen, Provider: candidate.Name, Chain: req.Chain,
				Method: req.Method, Message: "circuit breaker is open", Retryable: true}
		}
		if err := g.rate.Acquire(ctx, key); err != nil {
			return Response{}, AsError(err)
		}
		release, err := g.conc.Acquire(ctx, key)
		if err != nil {
			return Response{}, AsError(err)
		}

		InFlight.WithLabelValues(candidate.Name, req.Chain).Set(float64(g.conc.InFlight(key)))
		timeout := spec.DefaultTimeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		started := time.Now()
		response, callErr := candidate.Provider.Call(callCtx, req)
		latency := time.Since(started)
		cancel()
		release()
		InFlight.WithLabelValues(candidate.Name, req.Chain).Set(float64(g.conc.InFlight(key)))

		perr := AsError(callErr)
		if perr != nil {
			perr.Provider, perr.Chain, perr.Method = candidate.Name, req.Chain, req.Method
			perr.Message = Redact(perr.Message, g.secrets...)
		}
		g.observe(candidate.Name, req, spec, latency, response, perr, attempt)
		g.breakers.Record(breakerKey, perr == nil)

		if perr == nil {
			g.rate.OnSuccess(key)
			g.conc.Grow(key)
			response.Provider = candidate.Name
			response.Chain, response.Method = req.Chain, req.Method
			response.Latency = latency
			response.Attempts = attempt + 1
			g.trackHead(candidate.Name, req, spec, response)
			return response, nil
		}

		// 控制信号：Rate 与 Concurrency 分离（§35/§60/§61/§221）
		if perr.Class == ClassRateLimit || perr.ReduceRate {
			g.rate.OnRateLimited(key, perr.RetryAfter)
			RateLimitTotal.WithLabelValues(candidate.Name, req.Chain).Inc()
		}
		if perr.Class == ClassConnectionLimit || perr.ReduceConcurrency {
			g.conc.Reduce(key)
			ConnectionLimitTotal.WithLabelValues(candidate.Name, req.Chain).Inc()
		}
		if perr.Class == ClassTimeout {
			TimeoutTotal.WithLabelValues(candidate.Name, req.Chain, string(spec.Class)).Inc()
		}
		// 运行时能力学习：Provider 明确不支持时关掉该能力（§20）
		if IsCapability(perr.Class) {
			g.caps.LearnUnsupported(candidate.Name, req.Chain, spec.Class, perr.Class)
			return Response{}, perr
		}

		retry, backoff := g.retry.Decide(perr, attempt)
		if !retry {
			return Response{}, perr
		}
		RetriesTotal.WithLabelValues(candidate.Name, req.Chain, string(spec.Class)).Inc()
		select {
		case <-ctx.Done():
			return Response{}, &Error{Class: ClassCancelled, Provider: candidate.Name, Chain: req.Chain,
				Method: req.Method, Message: "request cancelled during retry backoff", Cause: ctx.Err()}
		case <-time.After(backoff):
		}
	}
}

// observe 指标 + 结构化日志 + 用量（§140/§142）。
func (g *Gateway) observe(providerName string, req Request, spec MethodSpec, latency time.Duration,
	response Response, perr *Error, attempt int) {
	status := "ok"
	errorClass := ""
	rpcCode, httpStatus := 0, 0
	message := ""
	if perr != nil {
		status = "error"
		errorClass = string(perr.Class)
		httpStatus = perr.HTTPStatus
		if perr.RPCCode != nil {
			rpcCode = *perr.RPCCode
		}
		message = perr.Message
		ErrorsTotal.WithLabelValues(providerName, req.Chain, string(spec.Class), errorClass).Inc()
	}
	RequestsTotal.WithLabelValues(providerName, req.Chain, string(spec.Class), status).Inc()
	MethodCallsTotal.WithLabelValues(providerName, req.Chain, req.Method, status).Inc()
	Duration.WithLabelValues(providerName, req.Chain, string(spec.Class)).Observe(latency.Seconds())
	if response.Bytes > 0 {
		ResponseBytes.WithLabelValues(providerName, req.Chain, string(spec.Class)).Observe(float64(response.Bytes))
	}
	g.health.Observe(providerName, req.Chain, latency, perr)

	// 结构化日志：绝不出现 endpoint 明文或 Secret（§31/§142）
	g.log.Debug("rpc",
		"trace_id", req.TraceID, "provider", providerName, "chain", req.Chain,
		"method", req.Method, "method_class", string(spec.Class), "latency_ms", latency.Milliseconds(),
		"attempt", attempt, "usage_type", req.Budget.Purpose, "status", status, "error_class", errorClass)

	g.record(usageInput{
		Provider: providerName, Chain: req.Chain, Method: req.Method, Class: spec.Class,
		Purpose: req.Budget.Purpose, Priority: req.Priority, MethodCalls: 1, BatchSize: 0,
		Status: status, HTTPStatus: httpStatus, RPCCode: rpcCode, ErrorClass: errorClass,
		ErrorMsg: message, Latency: latency, Bytes: response.Bytes, Attempt: attempt,
		JobID: req.Budget.JobID, Investigation: req.Budget.InvestigationID, TraceID: req.TraceID,
	})
}

func (g *Gateway) record(in usageInput) {
	if g.usage == nil {
		return
	}
	in.ErrorMsg = Redact(in.ErrorMsg, g.secrets...)
	g.usage(buildUsageRow(in, g.pricing))
}

// trackHead 成功的 eth_blockNumber 用于维护 head lag（§68）。
func (g *Gateway) trackHead(providerName string, req Request, spec MethodSpec, response Response) {
	if spec.Class != ClassChainHead || len(response.Result) == 0 {
		return
	}
	var hexValue string
	if err := json.Unmarshal(response.Result, &hexValue); err != nil {
		return
	}
	head, err := chain.HexToUint64(hexValue)
	if err != nil {
		return
	}
	g.health.SetHead(providerName, strings.ToLower(req.Chain), head)
}
