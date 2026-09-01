package rpc

import (
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
)

// MethodClass 方法类别（§16）。Timeout / 熔断 / 预算权重都按类别区分。
type MethodClass string

const (
	ClassChainHead    MethodClass = "CHAIN_HEAD"
	ClassBalance      MethodClass = "BALANCE"
	ClassContractRead MethodClass = "CONTRACT_READ"
	ClassCode         MethodClass = "CODE"
	ClassStorage      MethodClass = "STORAGE"
	ClassTransaction  MethodClass = "TRANSACTION"
	ClassReceipt      MethodClass = "RECEIPT"
	ClassBlock        MethodClass = "BLOCK"
	ClassLogs         MethodClass = "LOGS"
	ClassTrace        MethodClass = "TRACE"
	ClassDebugTrace   MethodClass = "DEBUG_TRACE"
	ClassWrite        MethodClass = "WRITE"
	ClassNetwork      MethodClass = "NETWORK"
)

// MethodSpec 方法特性登记（§15）。方法特性不得散落在业务代码里。
type MethodSpec struct {
	Name string

	Class MethodClass

	Cacheable bool
	Batchable bool

	DefaultTimeout time.Duration

	// CacheTTLLatest 针对 latest / 无区块引用；CacheTTLFixed 针对固定历史区块（不可变，§52）
	CacheTTLLatest time.Duration
	CacheTTLFixed  time.Duration

	RequiresArchive bool
	RequiresTrace   bool
	RequiresDebug   bool

	ReadOnly bool

	// EstimatedCostWeight 内部成本权重：即使 Provider 名义价格相同，
	// Trace 也必须更贵，防止 Agent 大规模 Trace（§109）
	EstimatedCostWeight int

	// BlockRefParamIndex 参数中区块引用的下标（-1 表示没有），
	// 用于自动判定 Archive 需求与缓存粒度（§152）
	BlockRefParamIndex int

	// MaxRequestBytes 单请求体上限（§119）
	MaxRequestBytes int
}

// CacheTTL 按区块引用返回 TTL（§52/§53：latest 短、固定区块长）。
func (s MethodSpec) CacheTTL(ref chain.BlockRef) time.Duration {
	if !s.Cacheable {
		return 0
	}
	if ref.IsFixed() {
		return s.CacheTTLFixed
	}
	return s.CacheTTLLatest
}

const (
	ttlImmutable = 720 * time.Hour // 固定区块结果不可变，缓存到淘汰为止
	maxCalldata  = 128 << 10       // eth_call calldata 上限（§119）
)

// defaultSpecs 首批开放方法（§249）+ 第二阶段 Trace/Debug（§250）+ 默认拒绝的写方法（§17）。
func defaultSpecs() []MethodSpec {
	return []MethodSpec{
		{Name: "eth_blockNumber", Class: ClassChainHead, Cacheable: true, Batchable: true,
			DefaultTimeout: 5 * time.Second, CacheTTLLatest: 2 * time.Second, CacheTTLFixed: 2 * time.Second,
			ReadOnly: true, EstimatedCostWeight: 1, BlockRefParamIndex: -1},
		{Name: "eth_chainId", Class: ClassNetwork, Cacheable: true, Batchable: true,
			DefaultTimeout: 5 * time.Second, CacheTTLLatest: time.Hour, CacheTTLFixed: time.Hour,
			ReadOnly: true, EstimatedCostWeight: 1, BlockRefParamIndex: -1},
		{Name: "net_version", Class: ClassNetwork, Cacheable: true, Batchable: true,
			DefaultTimeout: 5 * time.Second, CacheTTLLatest: time.Hour, CacheTTLFixed: time.Hour,
			ReadOnly: true, EstimatedCostWeight: 1, BlockRefParamIndex: -1},
		{Name: "eth_getBalance", Class: ClassBalance, Cacheable: true, Batchable: true,
			DefaultTimeout: 10 * time.Second, CacheTTLLatest: 15 * time.Second, CacheTTLFixed: ttlImmutable,
			ReadOnly: true, EstimatedCostWeight: 1, BlockRefParamIndex: 1},
		{Name: "eth_getCode", Class: ClassCode, Cacheable: true, Batchable: true,
			DefaultTimeout: 10 * time.Second, CacheTTLLatest: 10 * time.Minute, CacheTTLFixed: ttlImmutable,
			ReadOnly: true, EstimatedCostWeight: 1, BlockRefParamIndex: 1},
		{Name: "eth_getStorageAt", Class: ClassStorage, Cacheable: true, Batchable: true,
			DefaultTimeout: 10 * time.Second, CacheTTLLatest: 15 * time.Second, CacheTTLFixed: ttlImmutable,
			ReadOnly: true, EstimatedCostWeight: 1, BlockRefParamIndex: 2},
		{Name: "eth_call", Class: ClassContractRead, Cacheable: true, Batchable: true,
			DefaultTimeout: 15 * time.Second, CacheTTLLatest: 15 * time.Second, CacheTTLFixed: ttlImmutable,
			ReadOnly: true, EstimatedCostWeight: 2, BlockRefParamIndex: 1, MaxRequestBytes: maxCalldata},
		{Name: "eth_getTransactionByHash", Class: ClassTransaction, Cacheable: true, Batchable: true,
			DefaultTimeout: 15 * time.Second, CacheTTLLatest: 5 * time.Minute, CacheTTLFixed: ttlImmutable,
			ReadOnly: true, EstimatedCostWeight: 1, BlockRefParamIndex: -1},
		{Name: "eth_getTransactionReceipt", Class: ClassReceipt, Cacheable: true, Batchable: true,
			DefaultTimeout: 15 * time.Second, CacheTTLLatest: 5 * time.Minute, CacheTTLFixed: ttlImmutable,
			ReadOnly: true, EstimatedCostWeight: 1, BlockRefParamIndex: -1},
		{Name: "eth_getBlockByNumber", Class: ClassBlock, Cacheable: true, Batchable: true,
			DefaultTimeout: 15 * time.Second, CacheTTLLatest: 3 * time.Second, CacheTTLFixed: ttlImmutable,
			ReadOnly: true, EstimatedCostWeight: 2, BlockRefParamIndex: 0},
		{Name: "eth_getBlockByHash", Class: ClassBlock, Cacheable: true, Batchable: true,
			DefaultTimeout: 15 * time.Second, CacheTTLLatest: 5 * time.Minute, CacheTTLFixed: ttlImmutable,
			ReadOnly: true, EstimatedCostWeight: 2, BlockRefParamIndex: -1},
		// Logs 是区间敏感方法：不做通用缓存，由 Log Range Controller 控制（§95-§98）
		{Name: "eth_getLogs", Class: ClassLogs, Cacheable: false, Batchable: false,
			DefaultTimeout: 60 * time.Second, ReadOnly: true, EstimatedCostWeight: 5, BlockRefParamIndex: -1},

		// 第二阶段：Trace / Debug（能力以实际 Provider Plan 为准，§250）
		{Name: "trace_transaction", Class: ClassTrace, Cacheable: true, Batchable: false,
			DefaultTimeout: 60 * time.Second, CacheTTLLatest: ttlImmutable, CacheTTLFixed: ttlImmutable,
			RequiresTrace: true, ReadOnly: true, EstimatedCostWeight: 20, BlockRefParamIndex: -1},
		{Name: "trace_replayTransaction", Class: ClassTrace, Cacheable: true, Batchable: false,
			DefaultTimeout: 60 * time.Second, CacheTTLLatest: ttlImmutable, CacheTTLFixed: ttlImmutable,
			RequiresTrace: true, ReadOnly: true, EstimatedCostWeight: 25, BlockRefParamIndex: -1},
		{Name: "trace_block", Class: ClassTrace, Cacheable: true, Batchable: false,
			DefaultTimeout: 60 * time.Second, CacheTTLLatest: ttlImmutable, CacheTTLFixed: ttlImmutable,
			RequiresTrace: true, ReadOnly: true, EstimatedCostWeight: 30, BlockRefParamIndex: 0},
		{Name: "trace_filter", Class: ClassTrace, Cacheable: false, Batchable: false,
			DefaultTimeout: 60 * time.Second, RequiresTrace: true, ReadOnly: true,
			EstimatedCostWeight: 40, BlockRefParamIndex: -1},
		{Name: "debug_traceTransaction", Class: ClassDebugTrace, Cacheable: true, Batchable: false,
			DefaultTimeout: 120 * time.Second, CacheTTLLatest: ttlImmutable, CacheTTLFixed: ttlImmutable,
			RequiresDebug: true, ReadOnly: true, EstimatedCostWeight: 40, BlockRefParamIndex: -1},
		{Name: "debug_traceBlockByNumber", Class: ClassDebugTrace, Cacheable: false, Batchable: false,
			DefaultTimeout: 120 * time.Second, RequiresDebug: true, ReadOnly: true,
			EstimatedCostWeight: 60, BlockRefParamIndex: 0},
		{Name: "debug_traceBlockByHash", Class: ClassDebugTrace, Cacheable: false, Batchable: false,
			DefaultTimeout: 120 * time.Second, RequiresDebug: true, ReadOnly: true,
			EstimatedCostWeight: 60, BlockRefParamIndex: -1},

		// 写方法：登记只是为了给出明确错误，默认永远拒绝（§17/§116/§223）
		{Name: "eth_sendRawTransaction", Class: ClassWrite, DefaultTimeout: 15 * time.Second,
			ReadOnly: false, EstimatedCostWeight: 10, BlockRefParamIndex: -1},
		{Name: "eth_sendTransaction", Class: ClassWrite, DefaultTimeout: 15 * time.Second,
			ReadOnly: false, EstimatedCostWeight: 10, BlockRefParamIndex: -1},
	}
}

// RegistryOptions 运行期开关（§188）。
type RegistryOptions struct {
	TraceEnabled bool
	DebugEnabled bool
	// WriteEnabled 默认 false：写方法默认 DENY（§247-10）
	WriteEnabled bool
	// ExtraAllow / Deny 供运维覆盖 allowlist（§117）
	ExtraAllow []string
	Deny       []string
	// Timeouts 按类别覆盖默认超时（§62/§63）
	Timeouts map[MethodClass]time.Duration
}

// MethodRegistry 方法登记表 + Allowlist（§15/§117）。
type MethodRegistry struct {
	specs map[string]MethodSpec
	allow map[string]bool
}

// NewMethodRegistry 按配置构建。
func NewMethodRegistry(opts RegistryOptions) *MethodRegistry {
	r := &MethodRegistry{specs: map[string]MethodSpec{}, allow: map[string]bool{}}
	for _, spec := range defaultSpecs() {
		if timeout, ok := opts.Timeouts[spec.Class]; ok && timeout > 0 {
			spec.DefaultTimeout = timeout
		}
		r.specs[spec.Name] = spec
		switch {
		case !spec.ReadOnly:
			// 写方法只有显式开启才允许（默认 DENY）
			r.allow[spec.Name] = opts.WriteEnabled
		case spec.RequiresTrace:
			r.allow[spec.Name] = opts.TraceEnabled
		case spec.RequiresDebug:
			r.allow[spec.Name] = opts.DebugEnabled
		default:
			r.allow[spec.Name] = true
		}
	}
	for _, name := range opts.ExtraAllow {
		if name = strings.TrimSpace(name); name != "" {
			if _, known := r.specs[name]; known {
				r.allow[name] = true
			}
		}
	}
	for _, name := range opts.Deny {
		if name = strings.TrimSpace(name); name != "" {
			r.allow[name] = false
		}
	}
	return r
}

// DefaultMethodRegistry 首期：标准只读方法开放，Trace/Debug 开放（受 Provider Capability 再次约束），写方法拒绝。
func DefaultMethodRegistry() *MethodRegistry {
	return NewMethodRegistry(RegistryOptions{TraceEnabled: true, DebugEnabled: true, WriteEnabled: false})
}

func (r *MethodRegistry) Spec(method string) (MethodSpec, bool) {
	spec, ok := r.specs[method]
	return spec, ok
}

// Allowed 是否在 Allowlist 内。未登记方法一律拒绝（§117）。
func (r *MethodRegistry) Allowed(method string) bool { return r.allow[method] }

// Methods 返回全部登记方法（按名称排序，供能力接口展示）。
func (r *MethodRegistry) Methods() []MethodSpec {
	out := make([]MethodSpec, 0, len(r.specs))
	for _, spec := range r.specs {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AllowedMethods 返回当前允许的方法名。
func (r *MethodRegistry) AllowedMethods() []string {
	out := make([]string, 0, len(r.allow))
	for name, allowed := range r.allow {
		if allowed {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
