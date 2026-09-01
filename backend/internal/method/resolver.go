package method

import (
	"context"
	"strings"
	"time"
)

// EnqueueFunc 未知 Selector 的异步补全入口（§17：发布 method.resolve.request）。
type EnqueueFunc func(job ResolveJob)

// FastResolver 主下载链路使用的快速解析器（§16/§17/§40）。
//
// 硬约束：
//   - 只读内存索引（Contract ABI Cache + Local Signature Cache）；
//   - 不访问外部网络、不做阻塞式数据库查询（§62-7）；
//   - 解析不到就写 unknown 并投递异步补全任务，绝不阻塞下载（§62-6）。
type FastResolver struct {
	registry *Registry
	abi      *ABIResolver
	enqueue  EnqueueFunc
	cache    *TTLCache[MethodResolution]
}

func NewFastResolver(registry *Registry, enqueue EnqueueFunc) *FastResolver {
	return &FastResolver{
		registry: registry,
		abi:      NewABIResolver(registry),
		enqueue:  enqueue,
		cache:    NewTTLCache[MethodResolution](5*time.Minute, 100000),
	}
}

// Registry 暴露底层索引（供详情服务与 Backfill 复用同一份缓存）。
func (f *FastResolver) Registry() *Registry { return f.registry }

// ABIResolver 暴露 ABI 解析器（Decoded Input 使用）。
func (f *FastResolver) ABIResolver() *ABIResolver { return f.abi }

// Resolve 解析单笔交易的 Method（§15 Pipeline）。永不返回错误：
// 解析失败必须退化为 unknown，而不是让入库失败。
func (f *FastResolver) Resolve(ctx context.Context, req MethodResolveRequest) (MethodResolution, error) {
	_ = ctx
	methodType, selector := Classify(req.Input, req.Value, req.ToIsNull)
	SelectorExtractedTotal.WithLabelValues(methodType).Inc()

	if res, ok := StructuralResolution(methodType); ok {
		res.ResolvedAt = time.Now().UTC()
		ResolvedTotal.WithLabelValues(res.Source).Inc()
		return res, nil
	}

	contract := strings.ToLower(strings.TrimSpace(req.ContractAddress))
	cacheKey := strings.ToLower(req.Chain) + "|" + contract + "|" + selector
	if cached, ok := f.cache.Get(cacheKey); ok {
		if cached.Resolved() {
			ResolvedTotal.WithLabelValues(cached.Source).Inc()
		} else {
			UnknownTotal.Inc()
			f.publishUnknown(req, selector)
		}
		return cached, nil
	}

	res := f.resolveContractCall(req, contract, selector)
	f.cache.Put(cacheKey, res)
	if res.Resolved() {
		ResolvedTotal.WithLabelValues(res.Source).Inc()
	} else {
		UnknownTotal.Inc()
		f.publishUnknown(req, selector)
	}
	return res, nil
}

func (f *FastResolver) resolveContractCall(req MethodResolveRequest, contract, selector string) MethodResolution {
	// 1) Contract ABI 精确匹配（§15/§30）
	if res, ok := f.abi.ResolveSelector(req.Chain, contract, selector); ok {
		return res
	}
	// 2) 本地签名候选（§15：候选数 1 → 解析；>1 → input 兼容性；0 → 未知队列）
	candidates := f.registry.Signatures(selector)
	res := ChooseCandidate(selector, ArgBytes(req.Input), candidates)
	if res.Resolved() {
		switch res.Source {
		case SourceLocalSignature:
			ResolveLocalSignatureTotal.Inc()
		case SourcePublicSignature:
			ResolvePublicSignatureTotal.Inc()
		}
		res.ResolvedAt = time.Now().UTC()
	}
	return res
}

func (f *FastResolver) publishUnknown(req MethodResolveRequest, selector string) {
	if f.enqueue == nil || selector == "" {
		return
	}
	f.enqueue(ResolveJob{
		Chain:           strings.ToLower(req.Chain),
		ContractAddress: strings.ToLower(strings.TrimSpace(req.ContractAddress)),
		MethodID:        selector,
		SampleTxHash:    req.TxHash,
		InputLength:     len(req.Input),
	})
}

var _ Resolver = (*FastResolver)(nil)
var _ Resolver = (*ABIResolver)(nil)
