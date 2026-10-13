// Package chaintrace：Trace / Internal Transactions（V1.5 §99-§110）。
//
// 不同 Provider/客户端的 trace 格式不同（parity trace_* 与 geth debug_trace*），
// 业务层不得感知：统一 Normalize 成 Canonical Call Tree（§101）。
package chaintrace

import (
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
)

// Call Type 统一取值（§103）。
const (
	CallTypeCall         = "CALL"
	CallTypeStaticCall   = "STATICCALL"
	CallTypeDelegateCall = "DELEGATECALL"
	CallTypeCallCode     = "CALLCODE"
	CallTypeCreate       = "CREATE"
	CallTypeCreate2      = "CREATE2"
	CallTypeSelfDestruct = "SELFDESTRUCT"
)

// CallFrame 规范化调用帧（§102）。数值一律字符串，禁止 float64（§155）。
type CallFrame struct {
	// Persisted on the root so legacy incomplete normalized caches are not reused.
	NormalizationVersion string `json:"normalization_version,omitempty"`
	Type                 string `json:"type"`

	From chain.Address  `json:"from"`
	To   *chain.Address `json:"to,omitempty"`

	// Value 十进制字符串（wei）
	Value string `json:"value"`

	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`

	Gas     uint64 `json:"gas"`
	GasUsed uint64 `json:"gas_used"`

	Error string `json:"error,omitempty"`

	Children []CallFrame `json:"children,omitempty"`
}

// TraceSource 归一化来源。
const (
	SourceParity = "trace_transaction"
	SourceGeth   = "debug_traceTransaction"
	SourceLocal  = "local_cache"
)

// TraceResult 一笔交易的规范化调用树。
type TraceResult struct {
	Chain  string     `json:"chain"`
	TxHash chain.Hash `json:"tx_hash"`

	Root *CallFrame `json:"root"`

	Source     string    `json:"source"`
	FrameCount int       `json:"frame_count"`
	MaxDepth   int       `json:"max_depth"`
	FetchedAt  time.Time `json:"fetched_at"`
	CacheHit   bool      `json:"cache_hit"`

	// RawObjectKey 原始响应在对象存储中的位置（§107）
	RawObjectKey string `json:"raw_object_key,omitempty"`
	RawBytes     int    `json:"raw_bytes,omitempty"`
}

// InternalActivity 由 Call Tree 派生的内部活动（§104/§105）。
// 注意：Provider 原始 Trace ≠ Internal Transaction，必须经过归一化与派生。
type InternalActivity struct {
	Chain     string     `json:"chain"`
	TxHash    chain.Hash `json:"tx_hash"`
	Index     uint32     `json:"call_index"`
	Depth     uint16     `json:"depth"`
	TracePath string     `json:"trace_path"`

	Type string `json:"call_type"`
	// Activity 派生语义：native_transfer / contract_call / contract_creation / delegate_call / selfdestruct
	Activity string `json:"activity_type"`

	From chain.Address `json:"from"`
	To   chain.Address `json:"to"`

	Value    string `json:"value"`
	MethodID string `json:"method_id,omitempty"`

	Gas     uint64 `json:"gas"`
	GasUsed uint64 `json:"gas_used"`

	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// 派生活动类型（§105）。
const (
	ActivityNativeTransfer   = "native_transfer"
	ActivityContractCall     = "contract_call"
	ActivityContractCreation = "contract_creation"
	ActivityDelegateCall     = "delegate_call"
	ActivitySelfDestruct     = "selfdestruct_transfer"
)

// Options 单次 Trace 请求参数。
type Options struct {
	// Consistency：local_only 只读本地；auto 缺失才请求 Provider；force_refresh 强制重取（§202/§203）
	Consistency string
	// InvestigationID / JobID 用于预算归属与审计（§111/§112）
	InvestigationID string
	JobID           string
	MaxTraceCalls   int64
}
