// V1.2 Count Oracle 数据模型（§8-§10/§23/§42/§61/§87/§88）。
package model

import "time"

// CountRequest（§9）：一次 Count Oracle 查询（Chainbase 只负责"这个地址大概有多少"）。
type CountRequest struct {
	Chain       string
	ChainID     int64
	Dataset     string // transactions | token_transfers
	Address     string
	FromBlock   uint64
	ToBlock     uint64
	TargetBlock uint64 // 冻结 target（接近链头 → 短 TTL，§20）
}

// CountResult（§10）。
type CountResult struct {
	Provider      string
	Chain         string
	Dataset       string
	RawCount      int64 // -1 = 未取得
	AdjustedCount int64 // Raw × Calibration（只用于 Planning，§24/§25）
	FromBlock     uint64
	ToBlock       uint64
	LatencyMS     int64
	CacheHit      bool
	Confidence    float64
	Calibration   float64
	Source        string // chainbase | cache
	ProbeRequests int

	// 失败语义（Fail Open，§17）：RawCount=-1 时携带分类。
	FailureClass string
	FailureMsg   string
}

// CountOracleStatus 记录观测的入库形态（§87 列模型）。
type CountObservation struct {
	ID            string
	Provider      string
	Chain         string
	Dataset       string
	AddressHash   string
	FromBlock     int64
	ToBlock       int64
	RawCount      int64
	AdjustedCount int64
	LatencyMS     int
	CacheHit      bool
	Confidence    float64
	Status        string // ok | error | fallback | zero_verify_found_data | zero_verify_empty
	ErrorClass    string
	JobID         string
	CreatedAt     time.Time
}

// CountCalibration（§23/§51 V1.3：provider + chain + dataset 独立维护，只用于 Planning）。
type CountCalibration struct {
	Provider         string    `json:"provider"` // V1.3：默认 chainbase；不能让 Provider B 的偏差污染 Provider C
	Chain            string    `json:"chain"`
	Dataset          string    `json:"dataset"`
	SampleCount      int       `json:"sample_count"`
	RatioP50         float64   `json:"ratio_p50"`
	RatioP95         float64   `json:"ratio_p95"`
	AbsoluteErrorP50 float64   `json:"absolute_error_p50"`
	AbsoluteErrorP95 float64   `json:"absolute_error_p95"`
	FalseZeroCount   int       `json:"false_zero_count"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CalibrationKey 校准聚合键（V1.3 §51：(provider_id, chain_id, dataset, query_shape_hash)）。
type CalibrationKey struct {
	Provider string
	Chain    string
	Dataset  string
}

// CalibrationSample predicted vs actual 单样本。
type CalibrationSample struct {
	ID             string
	Provider       string // V1.3：来源 Provider（默认 chainbase）
	Chain          string
	Dataset        string
	AddressHash    string
	PredictedCount int64
	ActualCount    int64
	CreatedAt      time.Time
}

// PlanningDecision（§88：Planner 为什么给出 initial=n）。
type PlanningDecision struct {
	ID                string
	JobID             string
	SubjectID         string
	Dataset           string
	CountSource       string
	PredictedRows     int64
	PredictedPages    int64
	ProfileHit        bool
	PreferredShards   int
	InitialShards     int
	ProviderSafeSlots int
	GlobalActiveSlots int
	DecisionReason    string
	CreatedAt         time.Time
}

// ChainCapability（§61）。
type ChainCapability struct {
	Chain               string    `json:"chain"`
	EVMChainID          *int64    `json:"evm_chain_id"`
	AnkrAlias           string    `json:"ankr_alias"`
	ChainType           string    `json:"chain_type"`
	NativeSymbol        string    `json:"native_symbol"`
	ChainbaseTxCount    bool      `json:"chainbase_tx_count"`
	ChainbaseTokenCount bool      `json:"chainbase_token_count"`
	AnkrTransactions    bool      `json:"ankr_transactions"`
	AnkrTokenTransfers  bool      `json:"ankr_token_transfers"`
	RangeSupported      bool      `json:"range_supported"`
	Enabled             bool      `json:"enabled"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// GlobalConcurrencyProfile（§42：chain mix × dataset mix 的全局并发画像）。
type GlobalConcurrencyProfile struct {
	ChainMix                   string    `json:"chain_mix"`
	DatasetMix                 string    `json:"dataset_mix"`
	PreferredGlobalConcurrency int       `json:"preferred_global_concurrency"`
	P50Ms                      int       `json:"p50_ms"`
	P95Ms                      int       `json:"p95_ms"`
	Goodput                    float64   `json:"goodput"`
	RetryRate                  float64   `json:"retry_rate"`
	TimeoutRate                float64   `json:"timeout_rate"`
	RateLimitRate              float64   `json:"rate_limit_rate"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

// SchedulerState worker 心跳（§80 Global Concurrency Card 数据源）。
type SchedulerState struct {
	WorkerID string `json:"worker_id"`
	// Configured* are the values read by the worker process at startup.  They
	// are persisted with the heartbeat so the API never has to infer worker
	// truth from its own (potentially different) environment.
	ConfiguredInitialConcurrency int       `json:"configured_initial_concurrency"`
	ConfiguredMaxConcurrency     int       `json:"configured_max_concurrency"`
	GlobalHardCeiling            int       `json:"global_hard_ceiling"`
	ReadyQueueEnabled            bool      `json:"ready_queue_enabled"`
	ReadyQueueShadow             bool      `json:"ready_queue_shadow"`
	ReadyQueueMode               string    `json:"ready_queue_mode"`
	AutoTuneEnabled              bool      `json:"auto_tune_enabled"`
	ControllerMode               string    `json:"controller_mode"`
	ActiveSlots                  int       `json:"active_slots"`
	WeightedSlots                int       `json:"weighted_slots"`
	ConcurrencyTarget            int       `json:"concurrency_target"`
	HardCeiling                  int       `json:"hard_ceiling"`
	RampLevel                    int       `json:"ramp_level"`
	RampPhase                    string    `json:"ramp_phase"`
	ControlState                 string    `json:"control_state"`
	ControlReason                string    `json:"control_reason"`
	Goodput                      float64   `json:"goodput"`
	TTFBP50Ms                    int       `json:"ttfb_p50_ms"`
	MemUsedBytes                 int64     `json:"mem_used_bytes"`
	SpoolUsedBytes               int64     `json:"spool_used_bytes"`
	TimeToFullSafeConcurrencyMs  int64     `json:"time_to_full_safe_concurrency_ms"`
	UpdatedAt                    time.Time `json:"updated_at"`
}

// CountOracleHealth 快照（§82 Chainbase Health）。
type CountOracleHealth struct {
	Provider           string    `json:"provider"`
	CircuitState       string    `json:"circuit_state"`
	Requests           int64     `json:"requests"`
	SuccessCount       int64     `json:"success_count"`
	P50Ms              int       `json:"p50_ms"`
	P95Ms              int       `json:"p95_ms"`
	SuccessRate        float64   `json:"success_rate"`
	CacheHitRate       float64   `json:"cache_hit_rate"`
	QuotaExhausted     bool      `json:"quota_exhausted"`
	LastError          string    `json:"last_error"`
	FalseZeroCount     int       `json:"false_zero_count"`
	PredictionErrorP50 float64   `json:"prediction_error_p50"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// CountDataset 参与 Count Oracle 的首期数据集（§9：transactions / token_transfers）。
var CountDatasets = []string{"transactions", "token_transfers"}

// UsageTypeForCountDataset 返回 Count 请求的用量类型（§83）。
func UsageTypeForCountDataset(dataset string) string {
	if dataset == "token_transfers" {
		return UsageTypeCountOracleTokenTransfers
	}
	return UsageTypeCountOracleTransactions
}

// ---- V1.3 Count Provider Port 持久化模型（§66-§70）----

// CountProviderRegistryRow count_provider_registry 行。
type CountProviderRegistryRow struct {
	ProviderID  string    `json:"provider_id"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	AdapterType string    `json:"adapter_type"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CountProviderCapabilityRow count_provider_capabilities 行。
type CountProviderCapabilityRow struct {
	ProviderID    string    `json:"provider_id"`
	ChainID       int64     `json:"chain_id"`
	Dataset       string    `json:"dataset"`
	Enabled       bool      `json:"enabled"`
	RuntimeStatus string    `json:"runtime_status"`
	SupportsRange bool      `json:"supports_range"`
	RangeVerified bool      `json:"range_verified"`
	SupportsBatch bool      `json:"supports_batch"`
	MaxBatchSize  int       `json:"max_batch_size"`
	Semantics     string    `json:"semantics"`
	Accuracy      string    `json:"accuracy"`
	Reason        string    `json:"reason"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CountProviderRuntimeProfile count_provider_runtime_profiles 行（§57/§73）。
type CountProviderRuntimeProfile struct {
	ProviderID      string    `json:"provider_id"`
	ChainID         int64     `json:"chain_id"` // 0 = all（provider 级聚合）
	Dataset         string    `json:"dataset"`
	State           string    `json:"state"`
	Active          int       `json:"active"`
	QueueDepth      int       `json:"queue_depth"`
	SafeConcurrency int       `json:"safe_concurrency"`
	HardCeiling     int       `json:"hard_ceiling"`
	SuccessRPS      float64   `json:"success_rps"`
	ValidCountRPS   float64   `json:"valid_count_rps"`
	P50Ms           int       `json:"p50_ms"`
	P95Ms           int       `json:"p95_ms"`
	SuccessRate     float64   `json:"success_rate"`
	RateLimitRate   float64   `json:"rate_limit_rate"`
	TimeoutRate     float64   `json:"timeout_rate"`
	CostPer1KCounts float64   `json:"cost_per_1k_counts"`
	Requests        int64     `json:"requests"`
	SuccessCount    int64     `json:"success_count"`
	LastError       string    `json:"last_error"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CountProviderAttempt count_provider_attempts 行（§69：每次真实 Attempt）。
type CountProviderAttempt struct {
	ID             string
	RequestHash    string
	ProviderID     string
	CredentialSlot string
	ChainID        int64
	Dataset        string
	Endpoint       string
	Status         string // ok | error
	ErrorClass     string
	LatencyMS      int
	Count          int64
	RequestsCost   int64
	CreditsCost    float64
	USDCost        float64
	CreatedAt      time.Time
}

// ShadowDiff Shadow Diff（§97/§98：old/new 决策对照，放量 Gate 证据）。
type ShadowDiff struct {
	ID                  string
	RequestHash         string
	ChainID             int64
	Dataset             string
	SubjectHash         string
	RangeStart          int64
	RangeEnd            int64
	Status              string // resolution status（§45）
	Reason              string
	ProviderID          string
	Count               int64
	LatencyMS           int
	FallbackRecommended bool
	OldSource           string
	NewSource           string
	OldCount            int64
	NewCount            int64
	OldInitial          int
	NewInitial          int
	DecisionDiffReason  string
	CreatedAt           time.Time
}
