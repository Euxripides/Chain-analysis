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
	ID             string         `json:"id"`
	JobNo          string         `json:"job_no"`
	Status         string         `json:"status"`
	Priority       int            `json:"priority"`
	Source         string         `json:"source"`
	Chain          string         `json:"chain"`
	RangeType      string         `json:"range_type"`
	FromBlock      *int64         `json:"from_block"`
	ToBlock        *int64         `json:"to_block"`
	FromTimestamp  *time.Time     `json:"from_timestamp"`
	ToTimestamp    *time.Time     `json:"to_timestamp"`
	Datasets       []string       `json:"datasets"`
	ReusePolicy    string         `json:"reuse_policy"`
	HeadBlock      int64          `json:"head_block"`
	CreatedBy      *string        `json:"created_by"`
	OrganizationID *string        `json:"organization_id"`
	WorkspaceID    *string        `json:"workspace_id"`
	ProjectID      *string        `json:"project_id"`
	ResolvedAt     *time.Time     `json:"resolved_at"`
	Error          string         `json:"error"`
	Stats          map[string]any `json:"stats"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
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
	ID              string         `json:"id"`
	JobID           string         `json:"job_id"`
	SubjectID       string         `json:"subject_id"`
	DatasetID       string         `json:"dataset_id"`
	Dataset         string         `json:"dataset"`
	FromBlock       int64          `json:"from_block"`
	ToBlock         int64          `json:"to_block"`
	FromTimestamp   *time.Time     `json:"from_timestamp"`
	ToTimestamp     *time.Time     `json:"to_timestamp"`
	PageToken       string         `json:"page_token"`
	PageNo          int            `json:"page_no"`
	PageSize        int            `json:"page_size"`
	PageState       map[string]any `json:"page_state"`
	Status          string         `json:"status"`
	Priority        int            `json:"priority"`
	Retries         int            `json:"retries"`
	MaxRetries      int            `json:"max_retries"`
	LeaseOwner      string         `json:"lease_owner"`
	LeasedUntil     *time.Time     `json:"leased_until"`
	DownloadedRows  int64          `json:"downloaded_rows"`
	DownloadedBytes int64          `json:"downloaded_bytes"`
	RawObject       string         `json:"raw_object"`
	Error           string         `json:"error"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
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

	// V1.1（§34）
	ErrorClass    string
	RPCCode       int
	TraceID       string
	PageSize      int
	ReturnedRows  int
	ResponseBytes int
	RetryAttempt  int
	RetryReason   string
	RequestID     string

	// V1.3（§49/§50）：Deep Decode 成本归属与预算预留
	UsageType      string
	TxHash         string
	CacheHit       bool
	DecodePriority string
	DecodeBudget   *int

	// V1.5（§39/§40）：统一 RPC 用量。
	// 成本以 method call 为基准：100 个 call 的 Batch 不得记成 1 次调用。
	APIType         string
	Chain           string
	MethodClass     string
	MethodCalls     int
	BatchSize       int
	SingleflightHit bool
	InvestigationID string
	Priority        string
}

// Provider 用量类型（§49）。
const (
	UsageTypeDatasetDownload = "dataset_download"
	UsageTypeTxDeepDecode    = "tx_deep_decode"
)

// Provider API 类型（V1.5 §39）：索引型 Advanced API 与原始链 RPC 分开统计。
const (
	APITypeAdvanced = "advanced"
	APITypeRPC      = "rpc"
)

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
	ID        string    `json:"id"`
	JobID     *string   `json:"job_id"`
	Dataset   string    `json:"dataset"`
	Format    string    `json:"format"`
	Zip       bool      `json:"zip"`
	Status    string    `json:"status"`
	ObjectRef string    `json:"object_ref"`
	Rows      int64     `json:"rows"`
	SHA256    string    `json:"sha256"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---- V1.2 元数据模型 ----

type CaseInfo struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	WorkspaceID    string    `json:"workspace_id"`
	CaseNo         string    `json:"case_no"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Status         string    `json:"status"`
	Color          string    `json:"color"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type TagInfo struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	WorkspaceID    string    `json:"workspace_id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Color          string    `json:"color"`
	Source         string    `json:"source"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type WorkspaceAddress struct {
	ID                 string     `json:"id"`
	Chain              string     `json:"chain"`
	Address            string     `json:"address"`
	NormalizedAddress  string     `json:"normalized_address"`
	DisplayName        string     `json:"display_name"`
	PrimaryCaseID      *string    `json:"primary_case_id"`
	TransactionCount   int64      `json:"transaction_count"`
	TokenTransferCount int64      `json:"token_transfer_count"`
	NftTransferCount   int64      `json:"nft_transfer_count"`
	LogCount           int64      `json:"log_count"`
	TotalActivityCount int64      `json:"total_activity_count"`
	FirstDataBlock     *int64     `json:"first_data_block"`
	LastDataBlock      *int64     `json:"last_data_block"`
	FirstDataAt        *time.Time `json:"first_data_at"`
	LastDataAt         *time.Time `json:"last_data_at"`
	LastSyncAt         *time.Time `json:"last_sync_at"`
	SyncStatus         string     `json:"sync_status"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type TokenMetadata struct {
	Chain           string `json:"chain"`
	ContractAddress string `json:"contract_address"`
	Name            string `json:"name"`
	Symbol          string `json:"symbol"`
	Decimals        *int   `json:"decimals"`
	LogoURL         string `json:"logo_url"`
	TokenType       string `json:"token_type"`
	Verified        bool   `json:"verified"`
	MetadataSource  string `json:"metadata_source"`
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
