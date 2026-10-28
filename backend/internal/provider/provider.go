// Package provider 定义数据 Provider 抽象。业务模块只依赖本接口，不依赖具体 Provider。
package provider

import (
	"context"
	"encoding/json"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

type FetchRequest struct {
	Chain   string
	Dataset string
	Address string
	// QueryMode preserves the user's range intent after planning resolves full
	// history into a concrete [0, head] shard. Providers must not infer intent
	// from block bounds because full-history shards also carry resolved bounds.
	QueryMode string
	FromBlock *uint64
	ToBlock   *uint64
	FromTime  *time.Time
	ToTime    *time.Time
	PageSize  int
	PageToken string
	// RequestID is an internal immutable attempt correlation identifier. It is
	// never sent to Ankr and prevents response/shard attribution ambiguity.
	RequestID string
	AttemptNo int
	// Priority is propagated by the download scheduler so providers can apply
	// quota policy without coupling to job storage. Empty means normal.
	Priority string
	// Topics optionally narrows the logs dataset by event topic position (each
	// position is an OR list). Providers that cannot filter ignore it, so
	// callers must re-check topics on the returned rows.
	Topics [][]string
	// DescOrder asks for newest rows first where the provider supports it
	// (Ankr transactions and token transfers); others return their usual order.
	DescOrder bool
}

type FetchResult struct {
	Rows          []json.RawMessage
	NextPageToken string
	HasMore       bool
	// 响应字节拆分（文档 §28）：wire = 传输层压缩字节；decoded = 解压后 JSON 字节。
	WireBytes    int64
	DecodedBytes int64
	// SoftLimitExceeded 仅作为信号（文档 §26/§27），绝不强制降 pageSize。
	SoftLimitExceeded bool
	RequestID         string
	RequestFromBlock  *uint64
	RequestToBlock    *uint64
	RequestPageSize   int
	AttemptNo         int
	RawResponseBody   []byte
	StartedAt         time.Time
	BodyCompleteAt    time.Time
	// Total is a provider-reported total row count when the dataset exposes one
	// (token_holders); zero means unknown.
	Total int64
}

// TRC20ActivityProfile is a cheap planning sample from the address index. It
// deliberately carries a density estimate rather than a total-row promise:
// TronGrid does not expose an exact count for paginated address history.
type TRC20ActivityProfile struct {
	First          time.Time
	Latest         time.Time
	ExactRows      int
	PeakRowsPerDay float64
	HasMore        bool
	Empty          bool
}

// UsageHook 由 Worker 注入，记录每次 Provider 请求的用量与成本。
type UsageHook func(u model.UsageRow)

type Capabilities struct {
	Name              string
	Datasets          []string
	MaxPageSize       int
	SupportsTimeRange map[string]bool
}

// CurrentBalance is a provider-verified latest account balance. RawBalance is
// kept as the authoritative base-unit integer; callers apply token metadata
// before presenting a human-readable decimal value.
type CurrentBalance struct {
	TokenAddress string    `json:"token_address"`
	RawBalance   string    `json:"raw_balance"`
	Native       bool      `json:"native"`
	AsOfTime     time.Time `json:"as_of_time"`
}

// CurrentBalanceResolver exposes a bounded latest-state lookup without
// coupling API handlers to a concrete chain provider.
type CurrentBalanceResolver interface {
	CurrentBalances(context.Context, string, string) ([]CurrentBalance, error)
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
	ClassQuota            ErrorClass = "quota_exhausted"
	ClassUnsupported      ErrorClass = "unsupported"
	ClassCancelled        ErrorClass = "cancelled"

	// Ankr 引擎（文档 §65）：超时/截断必须精确分类。
	ClassConnectTimeout  ErrorClass = "connect_timeout"
	ClassHeaderTimeout   ErrorClass = "header_timeout"
	ClassBodyIdleTimeout ErrorClass = "body_idle_timeout"
	ClassOverallTimeout  ErrorClass = "overall_timeout"
	ClassBodyTruncated   ErrorClass = "body_truncated"
	ClassProviderInvalid ErrorClass = "provider_invalid_response"
	ClassUnknown         ErrorClass = "unknown"
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
	Provider   string
	Class      ErrorClass
	Message    string
	HTTPStatus int
	RPCCode    int
	TraceID    string

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

// NewBodyTruncated 传输层截断（连接在 body 中途断开）：禁止误报 invalid JSON（§66）。
func NewBodyTruncated(msg string, wireBytes int64, cause error) *Error {
	return &Error{
		Class: ClassBodyTruncated, Message: msg, Retryable: true,
		BodyBytes: wireBytes, Cause: cause, Kind: ErrTemporary,
	}
}

// NewBodyIdleTimeout body 持续无进展超时（§22）：Response 大 ≠ 卡死。
func NewBodyIdleTimeout(msg string, cause error) *Error {
	return &Error{
		Class: ClassBodyIdleTimeout, Message: msg, Retryable: true,
		Cause: cause, Kind: ErrTemporary,
	}
}

type Provider interface {
	Name() string
	Capabilities() Capabilities
	Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
	Health(ctx context.Context) HealthStatus
}

// ---- 请求模式传播（Provider 档位解析用）----
//
// FetchRequest 没有 mode 字段：模式是**任务级**意图，而 FetchRequest 是**页级**请求。
// 把它塞进 FetchRequest 会让每一页都重复携带同一份任务属性；反过来，多 Provider 的实际
// 档位必须按各自通道解析（开发文档 §6.3），所以 Provider 需要知道本任务请求的是哪一档。
// 用 context 传递是二者的最小耦合：调度器写入一次，Provider 读取一次。

type requestedModeKey struct{}

// WithRequestedMode 由调度器在发起 Fetch 前写入任务请求模式（safe|fast|turbo）。
func WithRequestedMode(ctx context.Context, mode string) context.Context {
	if mode == "" {
		return ctx
	}
	return context.WithValue(ctx, requestedModeKey{}, mode)
}

// RequestedModeFrom 读取任务请求模式；未设置时返回空串（Provider 自行回退默认档）。
func RequestedModeFrom(ctx context.Context) string {
	mode, _ := ctx.Value(requestedModeKey{}).(string)
	return mode
}

// TxDetailFetcher 单笔交易深度解码能力（V1.3 §23）。
//
// 可选能力：调用方类型断言获取。只允许交易详情 / 智能调查按需调用，
// 批量下载链路禁止逐笔调用（§62-1）。
type TxDetailFetcher interface {
	TxDetail(ctx context.Context, chain, txHash string, decodeTxData, decodeLogs bool) (json.RawMessage, error)
}

// TxReceiptFetcher is the optional native receipt capability. It is kept
// separate from Dataset Fetch so selecting an address-history provider cannot
// accidentally select the receipt provider.
type TxReceiptFetcher interface {
	TxReceipt(ctx context.Context, chain, txHash string) (json.RawMessage, error)
}

// TokenMetadataResult is the optional standard-contract RPC capability used by
// V1.4. Each field is independent so a reverting name() does not discard a
// successful symbol()/decimals() result.
type TokenMetadataResult struct {
	Name          *string
	Symbol        *string
	Decimals      *int
	NameError     string
	SymbolError   string
	DecimalsError string
}

// 注意（V1.5 §234）：Token Metadata 不再由 Advanced API Provider 直接解析。
// 解析路径固定为 TokenMetadataRPCResolver → ContractReadService → RPCGateway，
// 因此这里不再暴露 Provider 级别的 TokenMetadataResolver 接口。

// AddressTypeResolver is an optional raw-RPC capability used by the explorer
// to distinguish contracts from externally owned accounts. Implementations
// must key the result by normalized, lower-case address.
type AddressTypeResolver interface {
	ResolveAddressTypes(ctx context.Context, chain string, addresses []string) (map[string]bool, error)
}
