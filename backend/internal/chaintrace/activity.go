package chaintrace

import (
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

// ExtractInternalActivity 由规范化调用树派生内部活动（§104/§105）。
//
// 硬约束：Provider 原始 Trace 不等价于 Internal Transaction，
// 必须经过 Normalize → Derive 两步，且派生语义固定可枚举。
func ExtractInternalActivity(result *TraceResult) []InternalActivity {
	if result == nil || result.Root == nil {
		return nil
	}
	out := make([]InternalActivity, 0, result.FrameCount)
	var walk func(frame *CallFrame, depth int, path []int)
	index := uint32(0)
	walk = func(frame *CallFrame, depth int, path []int) {
		item := InternalActivity{
			Chain: result.Chain, TxHash: result.TxHash, Index: index, Depth: uint16(depth),
			TracePath: pathKey(path), Type: frame.Type, From: frame.From,
			Value: frame.Value, Gas: frame.Gas, GasUsed: frame.GasUsed,
			Error: frame.Error, Success: frame.Error == "",
		}
		if frame.To != nil {
			item.To = *frame.To
		}
		item.Activity = deriveActivity(frame)
		if selector := method.ExtractMethodID(frame.Input); selector != "" {
			item.MethodID = selector
		}
		out = append(out, item)
		index++
		for i := range frame.Children {
			walk(&frame.Children[i], depth+1, append(append([]int{}, path...), i))
		}
	}
	walk(result.Root, 0, nil)
	return out
}

// deriveActivity 派生语义（§105）。
func deriveActivity(frame *CallFrame) string {
	switch frame.Type {
	case CallTypeCreate, CallTypeCreate2:
		return ActivityContractCreation
	case CallTypeSelfDestruct:
		return ActivitySelfDestruct
	case CallTypeDelegateCall:
		return ActivityDelegateCall
	}
	if hasValue(frame.Value) && !hasCalldata(frame.Input) {
		return ActivityNativeTransfer
	}
	return ActivityContractCall
}

func hasValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && trimmed != "0"
}

func hasCalldata(input string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(input))
	trimmed = strings.TrimPrefix(trimmed, "0x")
	return len(trimmed) >= 8
}

// ValueTransfers 只保留带原生价值转移的活动（资金分析用）。
func ValueTransfers(items []InternalActivity) []InternalActivity {
	out := make([]InternalActivity, 0, len(items))
	for _, item := range items {
		if hasValue(item.Value) && item.Success {
			out = append(out, item)
		}
	}
	return out
}

// Participants 调用树里出现的全部地址（去重，供调查图谱使用）。
func Participants(items []InternalActivity) []chain.Address {
	seen := map[chain.Address]bool{}
	out := make([]chain.Address, 0, len(items)*2)
	for _, item := range items {
		for _, address := range []chain.Address{item.From, item.To} {
			if address == "" || seen[address] {
				continue
			}
			seen[address] = true
			out = append(out, address)
		}
	}
	return out
}
