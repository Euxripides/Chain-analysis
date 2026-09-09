// V1.3 CountProvider 最小稳定接口 + 可选能力 + Registry（§17-§21/§32）。
package count

import (
	"context"
	"time"
)

// CountProvider 所有 Provider 的最小稳定接口（§17）。
type CountProvider interface {
	ID() string

	Capabilities(ctx context.Context) ProviderCapabilities

	Count(ctx context.Context, req CountRequest) (CountResult, error)

	Health(ctx context.Context) ProviderHealth
}

// BatchCountProvider 可选 Batch 能力（§19：Router/Scheduler 只有同时满足
// Capabilities.SupportsBatch=true + type assertion 成功才启用）。
type BatchCountProvider interface {
	CountBatch(ctx context.Context, reqs []CountRequest) ([]CountResult, error)
}

// MultiDatasetCountProvider 可选：一次返回多数据集 Count（§20）。
type MultiDatasetCountProvider interface {
	CountDatasets(ctx context.Context, subject SubjectRef, rng BlockRange, datasets []DatasetType) ([]CountResult, error)
}

// AsyncCountProvider 可选：Submit/Poll 异步 Job（§21，P1 预留）。
type AsyncCountProvider interface {
	SubmitCountJob(ctx context.Context, reqs []CountRequest) (ExternalJobRef, error)
	PollCountJob(ctx context.Context, ref ExternalJobRef) ([]CountResult, JobState, error)
}

// ExternalJobRef / JobState Async 预留类型。
type ExternalJobRef struct {
	ProviderID string
	JobID      string
}

type JobState string

const (
	JobPending   JobState = "PENDING"
	JobRunning   JobState = "RUNNING"
	JobComplete  JobState = "COMPLETE"
	JobFailed    JobState = "FAILED"
	JobCancelled JobState = "CANCELLED"
)

// ProviderHealth Provider 健康快照（§17：Health(ctx)）。
type ProviderHealth struct {
	ProviderID string
	State      string // DISABLED|SHADOW|AVAILABLE|DEGRADED|THROTTLED|OPEN|RECOVERING|BLOCKED（§91）
	Healthy    bool
	Message    string
	UpdatedAt  time.Time
}

// ProviderRegistry（§32：Planner 不直接持有 Provider）。
type ProviderRegistry interface {
	Register(provider CountProvider) error
	Get(providerID string) (CountProvider, bool)
	List() []CountProvider
}

// CredentialSlot / CredentialPool（§41-§44：P1 预留；未验证 Key Scope 默认 shared）。
type CredentialSlot struct {
	ID string

	Enabled bool

	RateLimits []RateBucketPolicy

	Runtime CredentialRuntime
}

type CredentialRuntime struct {
	State       string
	NextAllowed time.Time
	Cooldown    time.Duration
}

// CredentialPool 预留接口：未来只有当真实测试证明 per-key independent 才允许
// 把多个 Key 当独立 Slot（N-Key scaling efficiency >=70% → API_KEY_INDEPENDENT_LIKELY）。
type CredentialPool interface {
	Candidates(providerID string) []CredentialSlot
}
