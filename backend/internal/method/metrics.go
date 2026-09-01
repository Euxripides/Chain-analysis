package method

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus 指标（§48）。
var (
	SelectorExtractedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "method_selector_extracted_total", Help: "成功提取 Method Selector 的交易数（按 method_type）",
	}, []string{"method_type"})
	ResolvedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "method_resolved_total", Help: "解析出方法名的交易数（按 source）",
	}, []string{"source"})
	UnknownTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_unknown_total", Help: "未能解析的合约调用数",
	})

	ResolveContractABITotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_resolve_contract_abi_total", Help: "由 Contract ABI 解析的次数",
	})
	ResolveLocalSignatureTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_resolve_local_signature_total", Help: "由本地签名库解析的次数",
	})
	ResolvePublicSignatureTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_resolve_public_signature_total", Help: "由公共签名库解析的次数",
	})
	ResolveAnkrTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_resolve_ankr_total", Help: "由 Ankr 深度解码解析的次数",
	})

	CollisionTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_collision_total", Help: "Selector 出现多个候选签名的次数",
	})

	DeepDecodeRequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_deep_decode_requests_total", Help: "深度解码请求数（含缓存命中）",
	})
	DeepDecodeCacheHitTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_deep_decode_cache_hit_total", Help: "深度解码本地缓存命中数",
	})
	DeepDecodeErrorTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_deep_decode_error_total", Help: "深度解码失败数",
	})

	ResolveQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "method_resolve_queue_depth", Help: "未知 Selector 待补全队列长度",
	})
	BackfillRowsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "method_backfill_rows_total", Help: "Backfill 重写的交易行数",
	})
	RegistryEntries = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "method_registry_entries", Help: "内存解析索引条目数",
	}, []string{"kind"})
)
