package rpc

import (
	"context"
	"encoding/json"
	"time"
)

// CapabilitySet Provider 在某条链上的能力（§18/§19）。
// 能力必须按 Provider + Chain 管理，不能写成 "Ankr supports trace = true"。
type CapabilitySet struct {
	StandardRPC bool `json:"standard"`
	Archive     bool `json:"archive"`

	Batch bool `json:"batch"`

	Trace bool `json:"trace"`
	Debug bool `json:"debug"`

	WebSocket bool `json:"websocket"`
	GRPC      bool `json:"grpc"`

	MaxBatchSize       int    `json:"max_batch_size"`
	MaxLogBlockRange   uint64 `json:"max_log_block_range"`
	MaxTraceBlockRange uint64 `json:"max_trace_block_range"`

	// Source 能力来源：static / probe / runtime / admin（§20）
	Source    string    `json:"source,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

// Supports 判断该能力集能否执行某方法。返回不支持的具体原因类别（§212/§213/§222）。
func (c CapabilitySet) Supports(spec MethodSpec, requiresArchive bool) (bool, ErrorClass) {
	if !c.StandardRPC {
		return false, ClassMethodUnsupported
	}
	if spec.RequiresTrace && !c.Trace {
		return false, ClassTraceUnsupported
	}
	if spec.RequiresDebug && !c.Debug {
		return false, ClassTraceUnsupported
	}
	if (requiresArchive || spec.RequiresArchive) && !c.Archive {
		return false, ClassArchiveRequired
	}
	return true, ""
}

// CircuitState 熔断状态（§66）。
type CircuitState string

const (
	CircuitClosed   CircuitState = "CLOSED"
	CircuitDegraded CircuitState = "DEGRADED"
	CircuitOpen     CircuitState = "OPEN"
	CircuitHalfOpen CircuitState = "HALF_OPEN"
)

// ProviderHealth Provider 健康画像（§67/§68）。
type ProviderHealth struct {
	Provider string `json:"provider"`
	Chain    string `json:"chain"`

	State CircuitState `json:"state"`

	Requests    int64   `json:"requests"`
	SuccessRate float64 `json:"success_rate"`
	TimeoutRate float64 `json:"timeout_rate"`
	RateLimited int64   `json:"rate_limited"`
	ServerError int64   `json:"server_error"`

	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`

	InFlight int `json:"in_flight"`

	// HeadBlock / HeadLag：仅 HTTP 200 不足以判定健康（§68）
	HeadBlock uint64 `json:"head_block"`
	HeadLag   uint64 `json:"head_lag"`

	LastError string    `json:"last_error,omitempty"`
	LastCheck time.Time `json:"last_check"`
}

// Provider 统一 RPC Provider 接口（§11）。
//
// 未来接入 QuickNode / dRPC / 自建 Erigon 只需实现本接口，
// 业务层与 Gateway 都不需要修改（§247-30）。
type Provider interface {
	Name() string

	Call(ctx context.Context, req Request) (Response, error)

	BatchCall(ctx context.Context, reqs []Request) ([]Response, error)

	Capabilities(ctx context.Context, chain string) (CapabilitySet, error)

	Health(ctx context.Context, chain string) ProviderHealth
}

// Response 统一 RPC 响应。
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`

	Provider string `json:"provider,omitempty"`
	Chain    string `json:"chain,omitempty"`
	Method   string `json:"method,omitempty"`

	// 观测字段：Gateway 负责填充
	CacheHit        bool          `json:"cache_hit"`
	SingleflightHit bool          `json:"singleflight_hit"`
	Latency         time.Duration `json:"-"`
	Bytes           int           `json:"bytes,omitempty"`
	BatchSize       int           `json:"batch_size,omitempty"`
	Attempts        int           `json:"attempts,omitempty"`

	// Error 仅用于 Batch 部分失败：整批不得因个别失败而失败（§46/§218）
	Error *Error `json:"error,omitempty"`
}

// IsNull JSON-RPC 的 null 结果（例如未找到交易）。
func (r Response) IsNull() bool {
	trimmed := string(r.Result)
	return trimmed == "" || trimmed == "null"
}

// ProviderCandidate Router 的候选项（§28）。
type ProviderCandidate struct {
	Provider   Provider
	Name       string
	Weight     int
	Priority   int
	Capability CapabilitySet
	Health     ProviderHealth
}
