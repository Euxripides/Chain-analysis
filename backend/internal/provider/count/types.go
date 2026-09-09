// V1.3 Count Oracle Provider Port：Canonical 类型（§6-§21/§28/§45-§48）。
package count

import "time"

// DatasetType 数据集（§8：当前 Provider 不支持没关系，由 Capability 决定是否路由）。
type DatasetType string

const (
	DatasetTransactions      DatasetType = "transactions"
	DatasetTokenTransfers    DatasetType = "token_transfers"
	DatasetNFTTransfers      DatasetType = "nft_transfers"
	DatasetLogs              DatasetType = "logs"
	DatasetTraces            DatasetType = "traces"
	DatasetInternalTransfers DatasetType = "internal_transactions"
	DatasetContractCreations DatasetType = "contract_creations"
)

// SubjectType 主题类型（§7：端口不写死成地址）。
type SubjectType string

const (
	SubjectAddress    SubjectType = "address"
	SubjectContract   SubjectType = "contract"
	SubjectToken      SubjectType = "token"
	SubjectTopic      SubjectType = "topic"
	SubjectAddressSet SubjectType = "address_set"
)

// SubjectRef 主题引用。
type SubjectRef struct {
	Type  SubjectType
	Value string
}

// BlockRange 统一内部区间语义 [Start, End)（§9：End exclusive）。
// Provider Adapter 自行转换为外部 API 的 fromBlock/toBlock inclusive。
type BlockRange struct {
	Start uint64 // inclusive
	End   uint64 // exclusive
}

// CountPurpose 用途（§10：Router 可按用途选择精度/成本策略）。
type CountPurpose string

const (
	PurposePlanning        CountPurpose = "PLANNING"
	PurposeSharding        CountPurpose = "SHARDING"
	PurposeETA             CountPurpose = "ETA"
	PurposeBatchOrdering   CountPurpose = "BATCH_ORDERING"
	PurposeZeroHint        CountPurpose = "ZERO_HINT"
	PurposeBackgroundCalib CountPurpose = "BACKGROUND_CALIBRATION"
)

// CountPriority 调度优先级（§39）。
type CountPriority string

const (
	PriorityInteractive CountPriority = "INTERACTIVE"
	PriorityPlanner     CountPriority = "PLANNER"
	PriorityBackground  CountPriority = "BACKGROUND"
	PriorityCalibration CountPriority = "CALIBRATION"
)

// CountSemantics 显式计数语义（§12：不同 API 的 count 可能代表不同东西）。
type CountSemantics string

const (
	SemanticsTotalRows          CountSemantics = "TOTAL_ROWS"
	SemanticsUniqueTransactions CountSemantics = "UNIQUE_TRANSACTIONS"
	SemanticsUniqueEvents       CountSemantics = "UNIQUE_EVENTS"
	SemanticsEstimatedRows      CountSemantics = "ESTIMATED_ROWS"
	SemanticsUnknown            CountSemantics = "UNKNOWN"
)

// CountAccuracy 精度（§13：Planner 不得把 Approximate 当 Exact）。
type CountAccuracy string

const (
	AccuracyExact              CountAccuracy = "EXACT"
	AccuracyApproximate        CountAccuracy = "APPROXIMATE"
	AccuracyCalibratedEstimate CountAccuracy = "CALIBRATED_ESTIMATE"
	AccuracyUnknown            CountAccuracy = "UNKNOWN"
)

// CountSource 结果来源（§15，前端 Estimate Source 显示）。
type CountSource string

const (
	SourceLiveProvider CountSource = "LIVE_PROVIDER"
	SourceCache        CountSource = "CACHE"
	SourceProfile      CountSource = "PROFILE"
	SourceCalibration  CountSource = "CALIBRATION"
	SourceConsensus    CountSource = "CONSENSUS"
	SourceFallback     CountSource = "FALLBACK"
)

// CountCost 原生成本（§16：valid counts / credit / USD 是未来 Provider 选择维度）。
type CountCost struct {
	Requests int64
	Credits  float64
	USD      float64
}

// ProviderMeta 透传 Provider 细节（禁止放 secret；禁止高基数指标引用）。
type ProviderMeta struct {
	Fields map[string]string
}

// CountRequest 规范 Count 请求（§6）。
type CountRequest struct {
	RequestID string

	ChainID uint64
	Dataset DatasetType

	Subject SubjectRef
	Range   BlockRange

	Purpose  CountPurpose
	Priority CountPriority

	Deadline time.Time

	MinConfidence     float64
	QueryShapeVersion int // §54：QueryShape 必须版本化，避免旧 Cache 错 HIT
}

// CountResult 规范 Count 结果（§11）。
type CountResult struct {
	RequestID string

	ProviderID string

	ChainID uint64
	Dataset DatasetType

	Subject SubjectRef
	Range   BlockRange

	Count      int64
	Semantics  CountSemantics
	Accuracy   CountAccuracy
	Confidence float64 // 0.0~1.0（§14，由 Trust/Calibration 管理）

	Source     CountSource
	ObservedAt time.Time
	IndexedAt  *time.Time

	Latency time.Duration

	Cost CountCost

	ProviderMeta ProviderMeta
}
