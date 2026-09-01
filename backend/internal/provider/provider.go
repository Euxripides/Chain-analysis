// Package provider 定义数据 Provider 抽象。业务模块只依赖本接口，不依赖具体 Provider。
package provider

import (
	"context"
	"encoding/json"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

type FetchRequest struct {
	Chain     string
	Dataset   string
	Address   string
	FromBlock *uint64
	ToBlock   *uint64
	FromTime  *time.Time
	ToTime    *time.Time
	PageSize  int
	PageToken string
}

type FetchResult struct {
	Rows          []json.RawMessage
	NextPageToken string
	HasMore       bool
}

// UsageHook 由 Worker 注入，记录每次 Provider 请求的用量与成本。
type UsageHook func(u model.UsageRow)

type Capabilities struct {
	Name              string
	Datasets          []string
	MaxPageSize       int
	SupportsTimeRange map[string]bool
}

type HealthState string

const (
	HealthNormal   HealthState = "NORMAL"
	HealthDegraded HealthState = "DEGRADED"
	HealthOpen     HealthState = "OPEN"
	HealthHalfOpen HealthState = "HALF_OPEN"
)

type HealthStatus struct {
	Provider           string      `json:"provider"`
	State              HealthState `json:"state"`
	LastCheck          time.Time   `json:"last_check"`
	Error              string      `json:"error"`
	ConsecutiveFailure int         `json:"consecutive_failures"`
	RecentErrorRate    float64     `json:"recent_error_rate"`
	CurrentConcurrency int         `json:"current_concurrency"`
}

// ---- V1.1 错误模型（文档 §4/§21-§25）----

type ErrorClass string

const (
	ClassRateLimit        ErrorClass = "rate_limit"
	ClassConnectionLimit  ErrorClass = "connection_limit"
	ClassRequestTimeout   ErrorClass = "request_timeout"
	ClassResponseTooLarge ErrorClass = "response_too_large"
	ClassInvalidJSON      ErrorClass = "invalid_json"
	ClassUpstream         ErrorClass = "upstream"
	ClassRequestTooLarge  ErrorClass = "request_too_large"
	ClassInvalidRequest   ErrorClass = "invalid_request"
	ClassAuth             ErrorClass = "auth"
	ClassUnsupported      ErrorClass = "unsupported"
	ClassCancelled        ErrorClass = "cancelled"
)

// 旧分类（兼容保留；新代码使用 Class）
type ErrorKind int

const (
	ErrTemporary ErrorKind = iota
	ErrRateLimit
	ErrAuth
	ErrInvalidParams
	ErrCircuitOpen
)

// Error 结构化 Provider 错误：禁止依赖字符串匹配作为主要决策手段（§4）。
type Error struct {
	Provider  string
	Class     ErrorClass
	Message   string
	HTTPStatus int
	RPCCode   int
	TraceID   string

	Retryable bool
	Duration  time.Duration
	PageSize  int
	BodyBytes int64

	// 控制决策（§21 Retry 决策矩阵）
	ReduceRate        bool
	ReduceConcurrency bool
	ReducePageSize    bool
	SplitRange        bool
	RetryAfter        time.Duration

	Cause error
	Kind  ErrorKind // legacy compat
}

func (e *Error) Error() string { return e.Message }

// 常用构造
func NewCancelled(msg string) *Error {
	return &Error{Class: ClassCancelled, Message: msg, Retryable: false, Kind: ErrInvalidParams}
}

func NewTimeout(msg string, pageSize int, cause error) *Error {
	return &Error{
		Class: ClassRequestTimeout, Message: msg, Retryable: true,
		ReducePageSize: true, PageSize: pageSize, Cause: cause, Kind: ErrTemporary,
	}
}

func NewResponseTooLarge(msg string, pageSize int, bodyBytes int64) *Error {
	return &Error{
		Class: ClassResponseTooLarge, Message: msg, Retryable: true,
		ReducePageSize: true, PageSize: pageSize, BodyBytes: bodyBytes, Kind: ErrTemporary,
	}
}

type Provider interface {
	Name() string
	Capabilities() Capabilities
	Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
	Health(ctx context.Context) HealthStatus
}

// TxDetailFetcher 单笔交易深度解码能力（V1.3 §23）。
//
// 可选能力：调用方类型断言获取。只允许交易详情 / 智能调查按需调用，
// 批量下载链路禁止逐笔调用（§62-1）。
type TxDetailFetcher interface {
	TxDetail(ctx context.Context, chain, txHash string, decodeTxData, decodeLogs bool) (json.RawMessage, error)
}
