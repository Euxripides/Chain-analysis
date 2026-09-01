package rpc

import (
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// ---- Provider Usage（§39/§40）----
//
// 成本以 method call 为基准：100 个 call 的 Batch 不能统计成 1 次调用。

// UsageHook 由装配层注入，把用量写入 provider_usage。
type UsageHook func(model.UsageRow)

// Pricing Provider 计价（§172/§173：不得把文档数值当永久常量）。
type Pricing struct {
	CreditsPerCall float64
	CostPerCallUSD float64
}

// DefaultPricing Ankr EVM RPC 当前 PAYG 名义价（§6），可由配置覆盖。
func DefaultPricing() Pricing {
	return Pricing{CreditsPerCall: 200, CostPerCallUSD: 0.00002}
}

// usageInput 一次 HTTP 请求的用量输入。
type usageInput struct {
	Provider      string
	Chain         string
	Method        string
	Class         MethodClass
	Purpose       string
	Priority      Priority
	MethodCalls   int
	BatchSize     int
	Status        string
	HTTPStatus    int
	RPCCode       int
	ErrorClass    string
	ErrorMsg      string
	Latency       time.Duration
	Bytes         int
	CacheHit      bool
	Singleflight  bool
	Attempt       int
	JobID         string
	Investigation string
	TraceID       string
}

// buildUsageRow 构造 provider_usage 行。
// cache/singleflight 命中不产生 Provider 成本，credits/cost 记 0。
func buildUsageRow(in usageInput, pricing Pricing) model.UsageRow {
	calls := in.MethodCalls
	if calls <= 0 {
		calls = 1
	}
	credits := pricing.CreditsPerCall * float64(calls)
	cost := pricing.CostPerCallUSD * float64(calls)
	if in.CacheHit || in.Singleflight {
		credits, cost = 0, 0
	}
	row := model.UsageRow{
		Provider: in.Provider, Method: in.Method, Dataset: string(in.Class),
		JobID: in.JobID, RequestTime: time.Now(), DurationMs: int(in.Latency.Milliseconds()),
		Status: in.Status, HTTPStatus: in.HTTPStatus, APIError: in.ErrorMsg,
		Rows: calls, Bytes: in.Bytes, ResponseBytes: in.Bytes,
		Credits: credits, CostUSD: cost, ErrorClass: in.ErrorClass, RPCCode: in.RPCCode,
		RetryAttempt: in.Attempt, TraceID: in.TraceID,

		// V1.5（§39）
		APIType:         model.APITypeRPC,
		Chain:           in.Chain,
		MethodClass:     string(in.Class),
		MethodCalls:     calls,
		BatchSize:       in.BatchSize,
		CacheHit:        in.CacheHit,
		SingleflightHit: in.Singleflight,
		InvestigationID: in.Investigation,
		Priority:        string(in.Priority),
		UsageType:       in.Purpose,
	}
	if row.UsageType == "" {
		row.UsageType = PurposeAdmin
	}
	return row
}
