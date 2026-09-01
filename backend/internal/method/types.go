// Package method：Transaction Method / ABI 解析（V1.3）。
//
// 核心原则（方案 §3/§62）：
//   - Selector 从 input 本地提取，批量下载不逐笔调用 Provider 深度接口；
//   - Selector 不是唯一标识，必须保留全部候选并区分 Source / Verified / Confidence；
//   - Contract ABI 优先级永远高于公共 4-byte 签名库；
//   - Method 未解析属于 Enrichment，不得阻塞下载与 Coverage。
package method

import (
	"context"
	"time"
)

// Method Source（§8）。取值集合固定，禁止随意扩展。
const (
	SourceManualOverride  = "manual_override"
	SourceContractABI     = "contract_abi"
	SourceAnkr            = "ankr"
	SourceLocalSignature  = "local_signature"
	SourcePublicSignature = "public_signature"
	SourceHeuristic       = "heuristic"
	SourceUnknown         = "unknown"
)

// Method Confidence（§9）。
const (
	ConfidenceVerified = "verified"
	ConfidenceHigh     = "high"
	ConfidenceMedium   = "medium"
	ConfidenceLow      = "low"
	ConfidenceUnknown  = "unknown"
)

// Method Type（§38）：结构化交易分类，与 Selector 解析结果解耦。
const (
	TypeContractCall     = "contract_call"
	TypeNativeTransfer   = "native_transfer"
	TypeContractCreation = "contract_creation"
	TypeEmptyCall        = "empty_call"
	TypeInvalidInput     = "invalid_input"
)

// 结构化分类的展示名（§26：Method 列不能留空）。
const (
	NameContractCreation = "Contract Creation"
	NameNativeTransfer   = "Native Transfer"
	NameEmptyCall        = "Empty Call"
	NameInvalidInput     = "Invalid Input"
)

// sourcePriority 最终优先级（§30）：数值越大越可信。
var sourcePriority = map[string]int{
	SourceUnknown:         0,
	SourceHeuristic:       1,
	SourcePublicSignature: 2,
	SourceLocalSignature:  3,
	SourceAnkr:            4,
	SourceContractABI:     5,
	SourceManualOverride:  6,
}

// SourceRank 返回 Source 优先级；未知 Source 视为最低。
func SourceRank(source string) int { return sourcePriority[source] }

// Better 判断 candidate 是否比 current 更可信（§30）。
func Better(candidate, current string) bool { return SourceRank(candidate) > SourceRank(current) }

// MethodCandidate 一个 Selector 的候选签名（§10：碰撞必须全部保留）。
type MethodCandidate struct {
	Name      string `json:"name"`
	Signature string `json:"signature"`
	Source    string `json:"source"`
	Verified  bool   `json:"verified"`
}

// MethodResolution 解析结果（§39）。
type MethodResolution struct {
	MethodID   string            `json:"method_id"`
	Name       string            `json:"name"`
	Signature  string            `json:"signature"`
	Source     string            `json:"source"`
	Verified   bool              `json:"verified"`
	Confidence string            `json:"confidence"`
	MethodType string            `json:"method_type"`
	Candidates []MethodCandidate `json:"candidates,omitempty"`
	ResolvedAt time.Time         `json:"resolved_at"`
}

// Resolved 是否得到了可展示的方法名。Unknown 不得伪装成已解析（§62-20）。
func (r MethodResolution) Resolved() bool {
	return r.Name != "" && r.Source != SourceUnknown
}

// MethodResolveRequest 解析请求（§39）。
type MethodResolveRequest struct {
	Chain           string
	ContractAddress string // tx.to；合约创建为空
	MethodID        string
	Input           string
	TxHash          string
	Value           string
	ToIsNull        bool
}

// Resolver 解析器抽象（§39/§40）。
type Resolver interface {
	Resolve(ctx context.Context, req MethodResolveRequest) (MethodResolution, error)
}

// unresolved 构造一个"未解析但结构合法"的结果：保留 selector，不编造方法名（§12）。
func unresolved(methodID, methodType string, candidates []MethodCandidate) MethodResolution {
	confidence := ConfidenceUnknown
	if len(candidates) > 1 {
		// 有候选但无法判定：低置信度，绝不标 Verified（§62-4）
		confidence = ConfidenceLow
	}
	return MethodResolution{
		MethodID:   methodID,
		Source:     SourceUnknown,
		Confidence: confidence,
		MethodType: methodType,
		Candidates: candidates,
	}
}
