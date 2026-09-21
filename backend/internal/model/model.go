package model

import (
	"strings"
	"time"
)

// DownloadJob 状态（必须持久化在 PostgreSQL）
const (
	JobCreated                    = "CREATED"
	JobPlanning                   = "PLANNING"
	JobQueued                     = "QUEUED"
	JobRunning                    = "RUNNING"
	JobWaitingUserConfirmation    = "WAITING_USER_CONFIRMATION"
	JobPauseReq                   = "PAUSE_REQUESTED"
	JobPaused                     = "PAUSED"
	JobPausedFallbackTooExpensive = "PAUSED_FALLBACK_TOO_EXPENSIVE"
	JobResuming                   = "RESUMING"
	JobCancelReq                  = "CANCEL_REQUESTED"
	JobCancelled                  = "CANCELLED"
	JobCompleted                  = "COMPLETED"
	JobPartialFailed              = "PARTIAL_FAILED"
	JobFailed                     = "FAILED"
	JobAbortedByUser              = "ABORTED_BY_USER"
)

// Shard 状态机
const (
	ShardPending = "PENDING"
	ShardLeased  = "LEASED"
	ShardRunning = "RUNNING"
	// Ankr 引擎状态拆分（§6/§62）：Fetch 完成后进入 FETCHED，
	// 异步 Ingest 全部完成后进入 INGESTED，Validation 通过才 COMPLETE。
	ShardFetched     = "FETCHED"
	ShardIngested    = "INGESTED"
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
	ItemPending                 = "PENDING"
	ItemRunning                 = "RUNNING"
	ItemPaused                  = "PAUSED"
	ItemWaitingUserConfirmation = "WAITING_USER_CONFIRMATION"
	ItemAbortedByUser           = "ABORTED_BY_USER"
	ItemCompleted               = "COMPLETED"
	ItemFailed                  = "FAILED"
	ItemSkipped                 = "SKIPPED"
)

var AllDatasets = []string{"transactions", "token_transfers", "nft_transfers", "logs", "balances", "internal_transactions"}
var DefaultDatasets = []string{"transactions", "token_transfers", "nft_transfers", "logs"}

// chainDatasetsByChain 是「该链实际能下载哪些 Dataset」的唯一事实来源（ADR-023 D9）。
//
// 事实来源：internal/provider/ankr（EVM Advanced API，没有 internal transactions 方法）、
// internal/tron（TronGrid，internal_transactions 是一级数据集）、
// internal/provider/solana（标准 JSON-RPC：SPL 指令解码 + CPI 调用树）。
// /api/v2/meta/datasets 与下载入参校验都读这里，杜绝两处白名单漂移。
var chainDatasetsByChain = map[string][]string{
	"bsc":       {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"eth":       {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"polygon":   {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"arbitrum":  {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"base":      {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"optimism":  {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"avalanche": {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"tron":      {"transactions", "token_transfers", "internal_transactions"},
	// nft_transfers 的实现与单测已在位，但真链验收未通过（抽样的 NFT 转账解不出 transfer），
	// 按 ADR-023 D9 暂不进白名单；验收通过后再加回这一项。
	"solana": {"transactions", "token_transfers", "internal_transactions", "logs", "balances"},
}

// ChainDatasets 返回该链可下载的 Dataset；chain 为空或未登记时返回全量（向后兼容）。
//
// Solana 的白名单随实际通道变化：走 dRPC Data API 时 nft_transfers 有明确来源，
// 走旧的 JSON-RPC 猜测路径时按 ADR-023 D9 排除（见 capability.go 的说明）。
func ChainDatasets(chain string) []string {
	key := strings.ToLower(strings.TrimSpace(chain))
	if key == "" {
		return AllDatasets
	}
	if key == "solana" {
		return solanaDatasets()
	}
	if list, ok := chainDatasetsByChain[key]; ok {
		return list
	}
	return AllDatasets
}

// solanaDatasets 由 capability.go 的进程级能力开关决定。
//
// internal_transactions 两种模式下都在：它由旧的 JSON-RPC 解码器（CPI 调用树）产出，
// dRPC 不提供该数据集，但也不应该因此把一个既有能力删掉。
func solanaDatasets() []string {
	if SolanaDataAPIEnabled() {
		return []string{"transactions", "token_transfers", "nft_transfers", "internal_transactions", "logs", "balances"}
	}
	return []string{"transactions", "token_transfers", "internal_transactions", "logs", "balances"}
}

// ChainSupportsDataset 判断 (chain, dataset) 是否可用。
func ChainSupportsDataset(chain, dataset string) bool {
	for _, candidate := range ChainDatasets(chain) {
		if candidate == dataset {
			return true
		}
	}
	return false
}

func ValidDataset(d string) bool {
	for _, x := range AllDatasets {
		if x == d {
			return true
		}
	}
	return false
}

type DownloadJob struct {
	ID                 string          `json:"id"`
	JobNo              string          `json:"job_no"`
	Status             string          `json:"status"`
	Priority           int             `json:"priority"`
	Source             string          `json:"source"`
	Mode               string          `json:"mode"` // economy|balanced|turbo|benchmark（V1.1 §41-44）
	RequestedMode      PerformanceMode `json:"requested_mode"`
	EffectiveMode      PerformanceMode `json:"effective_mode"`
	ModeReason         string          `json:"mode_reason"`
	ModeChangedAt      time.Time       `json:"mode_changed_at"`
	Chain              string          `json:"chain"`
	RangeType          string          `json:"range_type"`
	FromBlock          *int64          `json:"from_block"`
	ToBlock            *int64          `json:"to_block"`
	FromTimestamp      *time.Time      `json:"from_timestamp"`
	ToTimestamp        *time.Time      `json:"to_timestamp"`
	Datasets           []string        `json:"datasets"`
	ReusePolicy        string          `json:"reuse_policy"`
	RequestFingerprint string          `json:"-"`
	HeadBlock          int64           `json:"head_block"`
	CreatedBy          *string         `json:"created_by"`
	// S5：新 IAM 系统的创建者 identity；legacy created_by 仍保留给旧任务/用户表。
	CreatedByIdentity *string        `json:"created_by_identity,omitempty"`
	OrganizationID    *string        `json:"organization_id"`
	WorkspaceID       *string        `json:"workspace_id"`
	ProjectID         *string        `json:"project_id"`
	ResolvedAt        *time.Time     `json:"resolved_at"`
	Error             string         `json:"error"`
	Stats             map[string]any `json:"stats"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
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
	ID            string         `json:"id"`
	JobID         string         `json:"job_id"`
	SubjectID     string         `json:"subject_id"`
	DatasetID     string         `json:"dataset_id"`
	Dataset       string         `json:"dataset"`
	FromBlock     int64          `json:"from_block"`
	ToBlock       int64          `json:"to_block"`
	FromTimestamp *time.Time     `json:"from_timestamp"`
	ToTimestamp   *time.Time     `json:"to_timestamp"`
	PageToken     string         `json:"page_token"`
	PageNo        int            `json:"page_no"`
	PageSize      int            `json:"page_size"`
	PageState     map[string]any `json:"page_state"`
	Status        string         `json:"status"`
	Priority      int            `json:"priority"`
	Retries       int            `json:"retries"`
	MaxRetries    int            `json:"max_retries"`
	LeaseOwner    string         `json:"lease_owner"`
	LeasedUntil   *time.Time     `json:"leased_until"`
	// V1.4：Global Ready Queue / Lease Epoch（§8/§33）
	LeaseEpoch      int64   `json:"lease_epoch"`
	WorkScore       float64 `json:"work_score"`
	TailBoost       bool    `json:"tail_boost"`
	DownloadedRows  int64   `json:"downloaded_rows"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	RawObject       string  `json:"raw_object"`
	Error           string  `json:"error"`
	// 路由固化（开发文档 §9.1）：Planner 创建 Shard 时写入，恢复旧 Shard 必须沿用，
	// 后台改路由只影响新 Shard。旧 Shard 这些字段为空，调用方回退到链级默认路由。
	ProviderKey           string `json:"provider_key"`
	APIChannel            string `json:"api_channel"`
	OperationClass        string `json:"operation_class"`
	RoutingPolicyKey      string `json:"routing_policy_key"`
	RoutingPolicyRevision int64  `json:"routing_policy_revision"`
	ProfileRevision       int64  `json:"profile_revision"`
	// Ankr 引擎（§6/§84）：Fetch/Ingest/Validate 三阶段时间点与字节拆分。
	FetchedAt        *time.Time `json:"fetched_at"`
	IngestedAt       *time.Time `json:"ingested_at"`
	ValidatedAt      *time.Time `json:"validated_at"`
	FetchDurationMs  int64      `json:"fetch_duration_ms"`
	IngestDurationMs int64      `json:"ingest_duration_ms"`
	WireBytes        int64      `json:"wire_bytes"`
	DecodedBytes     int64      `json:"decoded_bytes"`
	PageCount        int        `json:"page_count"`
	PageGapAvgMs     int64      `json:"page_gap_avg_ms"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// Page 页面级状态机（§6/§80）：DURABLE → INGESTED → VALIDATED。
type Page struct {
	ID                string     `json:"id"`
	JobID             string     `json:"job_id"`
	ShardID           string     `json:"shard_id"`
	Dataset           string     `json:"dataset"`
	PageNo            int        `json:"page_no"`
	QueryHash         string     `json:"query_hash"`
	Status            string     `json:"status"`
	PageSize          int        `json:"page_size"`
	WireBytes         int64      `json:"wire_bytes"`
	DecodedBytes      int64      `json:"decoded_bytes"`
	RawPath           string     `json:"raw_path"`
	RawSHA256         string     `json:"raw_sha256"`
	RowsFetched       int        `json:"rows_fetched"`
	RowsParsed        int        `json:"rows_parsed"`
	RowsInserted      int        `json:"rows_inserted"`
	UniqueRows        int        `json:"unique_rows"`
	NextPageTokenHash string     `json:"next_page_token_hash"`
	FromBlock         int64      `json:"from_block"`
	ToBlock           int64      `json:"to_block"`
	FetchStartedAt    *time.Time `json:"fetch_started_at,omitempty"`
	BodyCompletedAt   *time.Time `json:"body_completed_at,omitempty"`
	DurableAt         *time.Time `json:"durable_at,omitempty"`
	IngestedAt        *time.Time `json:"ingested_at,omitempty"`
	ValidatedAt       *time.Time `json:"validated_at,omitempty"`
	Error             string     `json:"error"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// 页面状态常量
const (
	PageDurable   = "DURABLE"
	PageIngested  = "INGESTED"
	PageValidated = "VALIDATED"
	PageFailed    = "FAILED"
	// PageFailedFinal 表示该页的原始 spool 对象已不可恢复（例如文件已被清理），
	// 不再参与重放 —— 否则 finalizer 会每 30 秒重放一次并永久失败。
	PageFailedFinal = "FAILED_FINAL"
)

// BenchmarkRun V1.1 极限性能 Benchmark 记录（§48）。
type BenchmarkRun struct {
	ID                   string         `json:"id"`
	Chain                string         `json:"chain"`
	Dataset              string         `json:"dataset"`
	AddressHash          string         `json:"address_hash"`
	TargetBlock          int64          `json:"target_block"`
	ShardCount           int            `json:"shard_count"`
	PageSize             int            `json:"page_size"`
	JobID                string         `json:"job_id"`
	Status               string         `json:"status"`
	WallClockMs          int64          `json:"wall_clock_ms"`
	Rows                 int64          `json:"rows"`
	UniqueRows           int64          `json:"unique_rows"`
	Requests             int            `json:"requests"`
	Attempts             int            `json:"attempts"`
	Credits              float64        `json:"credits"`
	Cost                 float64        `json:"cost"`
	P50Ms                int            `json:"p50_ms"`
	P95Ms                int            `json:"p95_ms"`
	P99Ms                int            `json:"p99_ms"`
	PageGapAvgMs         int64          `json:"page_gap_avg_ms"`
	PageGapP95Ms         int64          `json:"page_gap_p95_ms"`
	PeakActiveShards     int            `json:"peak_active_shards"`
	RetryRate            float64        `json:"retry_rate"`
	TimeoutRate          float64        `json:"timeout_rate"`
	RateLimitRate        float64        `json:"rate_limit_rate"`
	Error429             int            `json:"error_429"`
	ErrorConnLimit       int            `json:"error_conn_limit"`
	ErrorTimeout         int            `json:"error_timeout"`
	ErrorOversized       int            `json:"error_oversized"`
	PeakRSS              int64          `json:"peak_rss"`
	SpoolPeakBytes       int64          `json:"spool_peak_bytes"`
	CHP95Ms              int            `json:"ch_p95_ms"`
	RawOverlapRows       int64          `json:"raw_overlap_rows"`
	BoundaryMissing      int64          `json:"boundary_missing"`
	BoundaryEvidence     map[string]any `json:"boundary_evidence"`
	IntegrityStatus      string         `json:"integrity_status"`
	ExcludedFromLearning bool           `json:"excluded_from_learning"`
	Error                string         `json:"error"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`

	// V1.2（§55/§103-§105）：Provider Latency Context + Provider-latency 归一化。
	ProviderLatencyP50Ms  int     `json:"provider_latency_p50_ms"`
	ProviderLatencyP95Ms  int     `json:"provider_latency_p95_ms"`
	ProviderTTFBP50Ms     int     `json:"provider_ttfb_p50_ms"`
	ProviderTTFBP95Ms     int     `json:"provider_ttfb_p95_ms"`
	ProviderBodyP50Ms     int     `json:"provider_body_p50_ms"`
	ProviderBodyP95Ms     int     `json:"provider_body_p95_ms"`
	NormalizedWallClockMs int64   `json:"normalized_wall_clock_ms"`
	ProviderWindowRatio   float64 `json:"provider_window_ratio"`
	ExcludeReason         string  `json:"exclude_reason"`
}

// ShardProfile Shard 历史画像（§12）。
type ShardProfile struct {
	Chain            string
	Dataset          string
	SubjectClass     string
	BlockFrom        int64
	BlockTo          int64
	Rows             int64
	Pages            int
	WireBytes        int64
	DecodedBytes     int64
	FetchDurationMs  int64
	IngestDurationMs int64
	PageLatencyP50Ms int64
	PageLatencyP95Ms int64
	PageGapAvgMs     int64
}

// PageProfile PageSize 画像（§32/§33）。
type PageProfile struct {
	Chain             string
	Dataset           string
	QueryShape        string
	PreferredPageSize int
	AvgRows           int
	AvgWireBytes      int64
	AvgDecodedBytes   int64
	AvgLatencyMs      int64
	P95LatencyMs      int64
	TimeoutRate       float64
	OversizedRate     float64
	PagesObserved     int64
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

	// Ankr 引擎（§28）：wire / decoded 拆分
	WireBytes    int64
	DecodedBytes int64

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
	APIType string
	// APIChannel 是同一 Provider 内部的实际取数通道（drpc: data_api|solana_rpc；
	// Ankr/TronGrid 等单通道 Provider 留空）。同一 Provider 的两条通道并发与速率不同，
	// 因此用量必须按通道可分辨，否则后台无法核对额度。
	APIChannel      string
	Chain           string
	MethodClass     string
	MethodCalls     int
	BatchSize       int
	SingleflightHit bool
	InvestigationID string
	Priority        string

	// V1.2（§56/§57）：TTFB 与 Body Download 分离计量（provider latency context）。
	TTFBMs int
	BodyMs int

	// TRON V5.1 enrichment summary. These fields are populated only when
	// UsageType=trc20_enrichment_summary and are not upstream request counters.
	LogicalCalls   int
	HTTPRequests   int
	LogsRequests   int
	MatchedRows    int
	FallbackRows   int
	SavedCalls     int
	FailureReasons map[string]int
}

// Provider 用量类型（§49）。
const (
	UsageTypeDatasetDownload = "dataset_download"
	UsageTypeTxDeepDecode    = "tx_deep_decode"

	// V1.2（§83）：Count Oracle 用量类型（成本分账：Planning API Cost 独立统计）。
	UsageTypeCountOracleTransactions   = "count_oracle_transactions"
	UsageTypeCountOracleTokenTransfers = "count_oracle_token_transfers"
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
	FetchedRows     int64      `json:"fetched_rows"`
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
