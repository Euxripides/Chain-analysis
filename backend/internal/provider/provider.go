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

type ErrorKind int

const (
	ErrTemporary ErrorKind = iota
	ErrRateLimit
	ErrAuth
	ErrInvalidParams
	ErrCircuitOpen
)

type Error struct {
	Kind       ErrorKind
	Message    string
	HTTPStatus int
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Retryable() bool {
	return e.Kind == ErrTemporary || e.Kind == ErrRateLimit
}

type Provider interface {
	Name() string
	Capabilities() Capabilities
	Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
	Health(ctx context.Context) HealthStatus
}
