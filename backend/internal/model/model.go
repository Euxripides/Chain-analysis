package model

import "time"

// DownloadJob 状态（必须持久化在 PostgreSQL）
const (
	JobCreated       = "CREATED"
	JobPlanning      = "PLANNING"
	JobQueued        = "QUEUED"
	JobRunning       = "RUNNING"
	JobPauseReq      = "PAUSE_REQUESTED"
	JobPaused        = "PAUSED"
	JobResuming      = "RESUMING"
	JobCancelReq     = "CANCEL_REQUESTED"
	JobCancelled     = "CANCELLED"
	JobCompleted     = "COMPLETED"
	JobPartialFailed = "PARTIAL_FAILED"
	JobFailed        = "FAILED"
)

// Shard 状态机
const (
	ShardPending     = "PENDING"
	ShardLeased      = "LEASED"
	ShardRunning     = "RUNNING"
	ShardPersisting  = "PERSISTING"
	ShardVerifying   = "VERIFYING"
	ShardCompleted   = "COMPLETED"
	ShardFailedRetry = "FAILED_RETRYABLE"
	ShardFailedFinal = "FAILED_FINAL"
	ShardPauseReq    = "PAUSE_REQUESTED"
	ShardPaused      = "PAUSED"
	ShardCancelReq   = "CANCEL_REQUESTED"
	ShardCancelled   = "CANCELLED"
)

// Subject / Dataset 状态
const (
	ItemPending   = "PENDING"
	ItemRunning   = "RUNNING"
	ItemPaused    = "PAUSED"
	ItemCompleted = "COMPLETED"
	ItemFailed    = "FAILED"
	ItemSkipped   = "SKIPPED"
)

var AllDatasets = []string{"transactions", "token_transfers", "nft_transfers", "logs", "balances"}
var DefaultDatasets = []string{"transactions", "token_transfers", "nft_transfers", "logs"}

func ValidDataset(d string) bool {
	for _, x := range AllDatasets {
		if x == d {
			return true
		}
	}
	return false
}

type DownloadJob struct {
	ID            string         `json:"id"`
	JobNo         string         `json:"job_no"`
	Status        string         `json:"status"`
	Priority      int            `json:"priority"`
	Source        string         `json:"source"`
	Chain         string         `json:"chain"`
	RangeType     string         `json:"range_type"`
	FromBlock     *int64         `json:"from_block"`
	ToBlock       *int64         `json:"to_block"`
	FromTimestamp *time.Time     `json:"from_timestamp"`
	ToTimestamp   *time.Time     `json:"to_timestamp"`
	Datasets      []string       `json:"datasets"`
	ReusePolicy   string         `json:"reuse_policy"`
	HeadBlock     int64          `json:"head_block"`
	CreatedBy     *string        `json:"created_by"`
	OrganizationID *string       `json:"organization_id"`
	WorkspaceID   *string        `json:"workspace_id"`
	ProjectID     *string        `json:"project_id"`
	ResolvedAt    *time.Time     `json:"resolved_at"`
	Error         string         `json:"error"`
	Stats         map[string]any `json:"stats"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type Subject struct {
	ID          string         `json:"id"`
	JobID       string         `json:"job_id"`
	SubjectType string         `json:"subject_type"`
	Subject     string         `json:"subject"`
	Status      string         `json:"status"`
	Progress    float32        `json:"progress"`
	Stats       map[string]any `json:"stats"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type DatasetItem struct {
	ID        string         `json:"id"`
	JobID     string         `json:"job_id"`
	SubjectID string         `json:"subject_id"`
	Dataset   string         `json:"dataset"`
	Status    string         `json:"status"`
	Progress  float32        `json:"progress"`
	Weight    float32        `json:"weight"`
	Stats     map[string]any `json:"stats"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Shard struct {
	ID              string     `json:"id"`
	JobID           string     `json:"job_id"`
	SubjectID       string     `json:"subject_id"`
	DatasetID       string     `json:"dataset_id"`
	Dataset         string     `json:"dataset"`
	FromBlock       int64      `json:"from_block"`
	ToBlock         int64      `json:"to_block"`
	FromTimestamp   *time.Time `json:"from_timestamp"`
	ToTimestamp     *time.Time `json:"to_timestamp"`
	PageToken       string     `json:"page_token"`
	PageNo          int        `json:"page_no"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	Retries         int        `json:"retries"`
	MaxRetries      int        `json:"max_retries"`
	LeaseOwner      string     `json:"lease_owner"`
	LeasedUntil     *time.Time `json:"leased_until"`
	DownloadedRows  int64      `json:"downloaded_rows"`
	DownloadedBytes int64      `json:"downloaded_bytes"`
	RawObject       string     `json:"raw_object"`
	Error           string     `json:"error"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type CoverageRow struct {
	ID          string    `json:"id"`
	Chain       string    `json:"chain"`
	SubjectType string    `json:"subject_type"`
	Subject     string    `json:"subject"`
	Dataset     string    `json:"dataset"`
	FromBlock   int64     `json:"from_block"`
	ToBlock     int64     `json:"to_block"`
	Status      string    `json:"status"`
	Provider    string    `json:"provider"`
	RowCount    int64     `json:"row_count"`
	RawBytes    int64     `json:"raw_bytes"`
	HeadBlock   int64     `json:"head_block"`
	CommittedAt time.Time `json:"committed_at"`
}

type UsageRow struct {
	Provider    string
	Method      string
	Dataset     string
	JobID       string
	ShardID     string
	TenantID    string
	RequestTime time.Time
	DurationMs  int
	Status      string
	HTTPStatus  int
	APIError    string
	Rows        int
	Bytes       int
	Credits     float64
	CostUSD     float64
}

type AuditEvent struct {
	ID           string         `json:"id"`
	UserID       *string        `json:"user_id"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Detail       map[string]any `json:"detail"`
	CreatedAt    time.Time      `json:"created_at"`
}

type Export struct {
	ID        string     `json:"id"`
	JobID     *string    `json:"job_id"`
	Dataset   string     `json:"dataset"`
	Format    string     `json:"format"`
	Status    string     `json:"status"`
	ObjectRef string     `json:"object_ref"`
	Rows      int64      `json:"rows"`
	Error     string     `json:"error"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type JobStats struct {
	DownloadedRows  int64      `json:"downloaded_rows"`
	IngestedRows    int64      `json:"ingested_rows"`
	APIRequests     int64      `json:"api_requests"`
	APICostUSD      float64    `json:"api_cost_usd"`
	HitRatio        float64    `json:"hit_ratio"`
	RowsPerSec      float64    `json:"rows_per_sec"`
	WriteRowsPerSec float64    `json:"write_rows_per_sec"`
	BytesPerSec     float64    `json:"bytes_per_sec"`
	ReqPerSec       float64    `json:"req_per_sec"`
	CoverageBefore  float64    `json:"coverage_before"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
}
