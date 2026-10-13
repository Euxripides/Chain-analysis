package rpc

import (
	"context"
	downloadprovider "github.com/blockchain-platform/blockchain-platform/internal/provider"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
)

// ---- Batch RPC（§41-§46）----
//
// Batch 降低 TCP/TLS/HTTP 开销，但不会把 100 个 method call 变成 1 个成本单位（§42/§40）。
// 响应顺序不可假设，必须按 id 映射（§45）；部分失败不得让整批失败（§46）。

// BatchCall 批量执行。返回切片与入参一一对应，单项失败写在 Response.Error 上。
func (g *Gateway) BatchCall(ctx context.Context, reqs []Request) ([]Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	responses := make([]Response, len(reqs))
	specs := make([]MethodSpec, len(reqs))
	cacheKeys := make([]string, len(reqs))
	pending := make([]int, 0, len(reqs))

	chainSlug := ""
	for i := range reqs {
		reqs[i] = reqs[i].Normalize()
		if chainSlug == "" {
			chainSlug = reqs[i].Chain
		} else if reqs[i].Chain != chainSlug {
			return nil, &Error{Class: ClassInvalidParams,
				Message: "batch requests must target a single chain"}
		}
		spec, guardErr := g.prepare(&reqs[i])
		if guardErr != nil {
			responses[i] = Response{Chain: reqs[i].Chain, Method: reqs[i].Method, Error: guardErr}
			continue
		}
		specs[i] = spec
		cacheKeys[i] = CacheKey(reqs[i])
		if spec.Cacheable && !reqs[i].CachePolicy.Disabled && reqs[i].Consistency != ConsistencyForceRefresh {
			if cached, ok := g.cache.Get(cacheKeys[i]); ok {
				CacheHitTotal.WithLabelValues(reqs[i].Chain, reqs[i].Method).Inc()
				cached.CacheHit = true
				cached.Chain, cached.Method = reqs[i].Chain, reqs[i].Method
				responses[i] = cached
				g.record(usageInput{Provider: cached.Provider, Chain: reqs[i].Chain, Method: reqs[i].Method,
					Class: spec.Class, Purpose: reqs[i].Budget.Purpose, Priority: reqs[i].Priority,
					MethodCalls: 1, Status: "cache_hit", CacheHit: true, JobID: reqs[i].Budget.JobID,
					Investigation: reqs[i].Budget.InvestigationID, TraceID: reqs[i].TraceID})
				continue
			}
			CacheMissTotal.WithLabelValues(reqs[i].Chain, reqs[i].Method).Inc()
		}
		if reqs[i].Consistency == ConsistencyLocalOnly {
			responses[i] = Response{Chain: reqs[i].Chain, Method: reqs[i].Method,
				Error: &Error{Class: ClassLocalMiss, Chain: reqs[i].Chain, Method: reqs[i].Method,
					Message: "local_only requested but no cached result is available"}}
			continue
		}
		pending = append(pending, i)
	}
	if len(pending) == 0 {
		return responses, nil
	}

	// 预算：按每个 method call 计（§40/§167）
	for _, idx := range pending {
		spec := specs[idx]
		isTrace := spec.Class == ClassTrace || spec.Class == ClassDebugTrace
		if err := g.budgets.Allow(reqs[idx].Budget, int64(spec.EstimatedCostWeight), isTrace); err != nil {
			BudgetRejectedTotal.WithLabelValues(reqs[idx].Budget.Purpose).Inc()
			responses[idx] = Response{Chain: reqs[idx].Chain, Method: reqs[idx].Method, Error: AsError(err)}
		}
	}
	runnable := pending[:0]
	for _, idx := range pending {
		if responses[idx].Error == nil {
			runnable = append(runnable, idx)
		}
	}
	if len(runnable) == 0 {
		return responses, nil
	}

	representative, requiresArchive := batchProfile(reqs, specs, runnable)
	candidates, routeErr := g.router.Candidates(ctx, Request{
		Chain: chainSlug, Method: reqs[runnable[0]].Method,
		BlockRef: archiveBlockRef(requiresArchive),
	}, representative)
	if routeErr != nil {
		for _, idx := range runnable {
			responses[idx] = Response{Chain: reqs[idx].Chain, Method: reqs[idx].Method, Error: routeErr}
		}
		return responses, routeErr
	}

	for _, chunk := range chunkIndexes(runnable, g.batchLimit(candidates[0])) {
		g.runBatchChunk(ctx, reqs, specs, cacheKeys, responses, chunk, representative, candidates)
	}
	return responses, nil
}

// batchProfile 计算整批的代表性方法特性与 archive 需求（能力判定取并集）。
func batchProfile(reqs []Request, specs []MethodSpec, idx []int) (MethodSpec, bool) {
	representative := specs[idx[0]]
	requiresArchive := false
	for _, i := range idx {
		if reqs[i].RequiresArchive() || specs[i].RequiresArchive {
			requiresArchive = true
		}
		if specs[i].EstimatedCostWeight > representative.EstimatedCostWeight {
			representative = specs[i]
		}
		if specs[i].RequiresTrace {
			representative.RequiresTrace = true
		}
		if specs[i].RequiresDebug {
			representative.RequiresDebug = true
		}
		if specs[i].DefaultTimeout > representative.DefaultTimeout {
			representative.DefaultTimeout = specs[i].DefaultTimeout
		}
	}
	representative.RequiresArchive = requiresArchive
	return representative, requiresArchive
}

// archiveBlockRef 用一个固定区块引用向 Router 表达"整批需要 Archive 能力"（§88）。
func archiveBlockRef(requiresArchive bool) chain.BlockRef {
	if requiresArchive {
		return chain.AtBlock(1)
	}
	return chain.Latest()
}

func (g *Gateway) batchLimit(candidate ProviderCandidate) int {
	limit := g.maxBatchSize
	if candidate.Capability.MaxBatchSize > 0 && candidate.Capability.MaxBatchSize < limit {
		limit = candidate.Capability.MaxBatchSize
	}
	if limit <= 0 {
		limit = 1
	}
	return limit
}

func chunkIndexes(idx []int, size int) [][]int {
	if size <= 0 {
		size = 1
	}
	var out [][]int
	for start := 0; start < len(idx); start += size {
		end := start + size
		if end > len(idx) {
			end = len(idx)
		}
		chunk := make([]int, end-start)
		copy(chunk, idx[start:end])
		out = append(out, chunk)
	}
	return out
}

// runBatchChunk 在候选 Provider 上执行一个 chunk，失败时 failover。
func (g *Gateway) runBatchChunk(ctx context.Context, reqs []Request, specs []MethodSpec, cacheKeys []string,
	responses []Response, chunk []int, representative MethodSpec, candidates []ProviderCandidate) {
	batch := make([]Request, len(chunk))
	for i, idx := range chunk {
		batch[i] = reqs[idx]
	}
	chainSlug := batch[0].Chain

	var lastErr *Error
	for _, candidate := range candidates {
		if !candidate.Capability.Batch {
			// Provider 不支持 Batch：退化为逐个调用，语义保持一致
			for _, idx := range chunk {
				response, err := g.executeAdmitted(ctx, reqs[idx], specs[idx], cacheKeys[idx])
				if err != nil {
					responses[idx] = Response{Chain: reqs[idx].Chain, Method: reqs[idx].Method, Error: AsError(err)}
					continue
				}
				responses[idx] = response
			}
			return
		}
		key := LimiterKey{Provider: candidate.Name, Credential: g.credential, Chain: chainSlug, Class: representative.Class}
		breakerKey := BreakerKey(candidate.Name, chainSlug, representative.Class)
		allowed, state := g.breakers.Allow(breakerKey)
		CircuitStateGauge.WithLabelValues(candidate.Name, chainSlug, string(representative.Class)).Set(circuitStateValue(state))
		if !allowed {
			lastErr = &Error{Class: ClassCircuitOpen, Provider: candidate.Name, Chain: chainSlug,
				Message: "circuit breaker is open", Retryable: true}
			continue
		}
		if err := g.rate.Acquire(ctx, key); err != nil {
			lastErr = AsError(err)
			continue
		}
		release, err := g.conc.Acquire(ctx, key)
		if err != nil {
			lastErr = AsError(err)
			continue
		}
		timeout := representative.DefaultTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		started := time.Now()
		if err := downloadprovider.ReserveDownloadRequest(callCtx, int64(len(batch)), g.pricing.CreditsPerCall*float64(len(batch)), g.pricing.CostPerCallUSD*float64(len(batch))); err != nil {
			cancel()
			release()
			for _, idx := range chunk {
				responses[idx] = Response{Chain: reqs[idx].Chain, Method: reqs[idx].Method, Error: &Error{Class: ClassBudgetExceeded, Message: "download recovery budget exhausted", Cause: err}}
			}
			return
		}
		results, callErr := candidate.Provider.BatchCall(callCtx, batch)
		latency := time.Since(started)
		cancel()
		release()

		BatchRequestsTotal.WithLabelValues(candidate.Name, chainSlug).Inc()
		BatchCallsTotal.WithLabelValues(candidate.Name, chainSlug).Add(float64(len(batch)))

		perr := AsError(callErr)
		if perr != nil {
			perr.Provider, perr.Chain = candidate.Name, chainSlug
			perr.Message = Redact(perr.Message, g.secrets...)
		}
		g.breakers.Record(breakerKey, providerRespondedHealthy(perr))
		g.health.Observe(candidate.Name, chainSlug, latency, perr)
		bytes := 0
		for i := range results {
			bytes += results[i].Bytes
		}
		status := "ok"
		errorClass := ""
		message := ""
		if perr != nil {
			status = "error"
			errorClass = string(perr.Class)
			message = perr.Message
			ErrorsTotal.WithLabelValues(candidate.Name, chainSlug, string(representative.Class), errorClass).Inc()
		}
		RequestsTotal.WithLabelValues(candidate.Name, chainSlug, string(representative.Class), status).Inc()
		Duration.WithLabelValues(candidate.Name, chainSlug, string(representative.Class)).Observe(latency.Seconds())
		g.record(usageInput{
			Provider: candidate.Name, Chain: chainSlug, Method: representative.Name, Class: representative.Class,
			Purpose: batch[0].Budget.Purpose, Priority: batch[0].Priority, MethodCalls: len(batch),
			BatchSize: len(batch), Status: status, ErrorClass: errorClass, ErrorMsg: message,
			Latency: latency, Bytes: bytes, JobID: batch[0].Budget.JobID,
			Investigation: batch[0].Budget.InvestigationID, TraceID: batch[0].TraceID,
		})

		if perr != nil {
			if perr.Class == ClassRateLimit || perr.ReduceRate {
				g.rate.OnRateLimited(key, perr.RetryAfter)
				RateLimitTotal.WithLabelValues(candidate.Name, chainSlug).Inc()
			}
			if perr.Class == ClassConnectionLimit || perr.ReduceConcurrency {
				g.conc.Reduce(key)
				ConnectionLimitTotal.WithLabelValues(candidate.Name, chainSlug).Inc()
			}
			lastErr = perr
			if IsFatal(perr.Class) && !IsCapability(perr.Class) {
				break
			}
			continue
		}
		g.rate.OnSuccess(key)
		g.conc.Grow(key)

		// 逐项处理：部分失败不影响其它项（§46/§218）
		for i, idx := range chunk {
			if i >= len(results) {
				responses[idx] = Response{Chain: reqs[idx].Chain, Method: reqs[idx].Method,
					Error: &Error{Class: ClassInvalidResponse, Provider: candidate.Name, Chain: chainSlug,
						Method: reqs[idx].Method, Message: "batch response is missing this call"}}
				continue
			}
			item := results[i]
			item.Provider = candidate.Name
			item.Chain, item.Method = reqs[idx].Chain, reqs[idx].Method
			item.BatchSize = len(batch)
			if item.Error != nil {
				item.Error.Provider, item.Error.Chain, item.Error.Method = candidate.Name, chainSlug, reqs[idx].Method
				item.Error.Message = Redact(item.Error.Message, g.secrets...)
				MethodCallsTotal.WithLabelValues(candidate.Name, chainSlug, reqs[idx].Method, "error").Inc()
			} else {
				MethodCallsTotal.WithLabelValues(candidate.Name, chainSlug, reqs[idx].Method, "ok").Inc()
				if specs[idx].Cacheable && !reqs[idx].CachePolicy.Disabled {
					ttl := specs[idx].CacheTTL(reqs[idx].BlockRef)
					if reqs[idx].CachePolicy.TTLOverride > 0 {
						ttl = time.Duration(reqs[idx].CachePolicy.TTLOverride)
					}
					g.cache.Put(cacheKeys[idx], item, ttl)
				}
			}
			responses[idx] = item
		}
		return
	}

	if lastErr == nil {
		lastErr = &Error{Class: ClassNoProvider, Chain: chainSlug, Message: "no provider handled the batch"}
	}
	for _, idx := range chunk {
		if responses[idx].Error == nil && len(responses[idx].Result) == 0 {
			responses[idx] = Response{Chain: reqs[idx].Chain, Method: reqs[idx].Method, Error: lastErr}
		}
	}
}

// ---- 自动批处理器（§43/§44）----

// BatcherOptions 聚合参数。
type BatcherOptions struct {
	MaxSize      int
	MaxWait      time.Duration
	MaxBodyBytes int
}

type batchItem struct {
	req    Request
	result chan batchResult
}

type batchResult struct {
	response Response
	err      error
}

// Batcher 把短时间内来自不同调用方的单个请求聚合成一次 HTTP Batch。
// 例如 Token Metadata Worker 逐个产生 symbol()/name()/decimals()（§44）。
type Batcher struct {
	gateway *Gateway
	opts    BatcherOptions

	mu     sync.Mutex
	queues map[string]chan batchItem
	closed bool
	done   chan struct{}
}

func NewBatcher(gateway *Gateway, opts BatcherOptions) *Batcher {
	if opts.MaxSize <= 0 {
		opts.MaxSize = 50
	}
	if opts.MaxWait <= 0 {
		opts.MaxWait = 10 * time.Millisecond
	}
	return &Batcher{gateway: gateway, opts: opts, queues: map[string]chan batchItem{}, done: make(chan struct{})}
}

// Call 入队并等待结果。聚合失败时退化为直接调用，绝不丢请求。
func (b *Batcher) Call(ctx context.Context, req Request) (Response, error) {
	req = req.Normalize()
	if spec, ok := b.gateway.methods.Spec(req.Method); !ok || !spec.Batchable {
		return b.gateway.Call(ctx, req)
	}
	queue := b.queueFor(batchQueueKey(req))
	if queue == nil {
		return b.gateway.Call(ctx, req)
	}
	item := batchItem{req: req, result: make(chan batchResult, 1)}
	select {
	case queue <- item:
	case <-b.done:
		return b.gateway.Call(ctx, req)
	case <-ctx.Done():
		return Response{}, &Error{Class: ClassCancelled, Message: "batch enqueue cancelled", Cause: ctx.Err()}
	}
	select {
	case res := <-item.result:
		return res.response, res.err
	case <-b.done:
		return Response{}, &Error{Class: ClassCancelled, Message: "batcher is closed"}
	case <-ctx.Done():
		return Response{}, &Error{Class: ClassCancelled, Message: "batch wait cancelled", Cause: ctx.Err()}
	}
}

// batchQueueKey 防止不同任务、调查、优先级或一致性语义被合并后错误归账。
func batchQueueKey(req Request) string {
	return strings.Join([]string{
		strings.ToLower(req.Chain), req.Budget.Key(), req.Budget.Purpose,
		string(req.Priority), string(req.Consistency), req.BlockRef.String(),
	}, "|")
}

func (b *Batcher) queueFor(chainSlug string) chan batchItem {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	queue, ok := b.queues[chainSlug]
	if !ok {
		queue = make(chan batchItem, b.opts.MaxSize*4)
		b.queues[chainSlug] = queue
		go b.loop(queue)
	}
	return queue
}

func (b *Batcher) loop(queue chan batchItem) {
	for {
		var first batchItem
		select {
		case <-b.done:
			return
		case first = <-queue:
		}
		items := []batchItem{first}
		timer := time.NewTimer(b.opts.MaxWait)
	collect:
		for len(items) < b.opts.MaxSize {
			select {
			case <-b.done:
				timer.Stop()
				for _, item := range items {
					item.result <- batchResult{err: &Error{Class: ClassCancelled, Message: "batcher is closed"}}
				}
				return
			case next := <-queue:
				items = append(items, next)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		b.flush(items)
	}
}

func (b *Batcher) flush(items []batchItem) {
	reqs := make([]Request, len(items))
	for i := range items {
		reqs[i] = items[i].req
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	responses, err := b.gateway.BatchCall(ctx, reqs)
	cancel()
	for i := range items {
		switch {
		case err != nil && responses == nil:
			items[i].result <- batchResult{err: err}
		case i < len(responses) && responses[i].Error != nil:
			items[i].result <- batchResult{response: responses[i], err: responses[i].Error}
		case i < len(responses):
			items[i].result <- batchResult{response: responses[i]}
		default:
			items[i].result <- batchResult{err: &Error{Class: ClassInvalidResponse,
				Message: "batch produced no result for this call"}}
		}
		close(items[i].result)
	}
}

// Close 停止全部聚合队列。
func (b *Batcher) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.done)
}
