// V1.3 ProviderCapabilities / DatasetCapability / CapabilityStatus（§22-§25/§50）。
package count

// CapabilityStatus Runtime 能力状态（§24：Router 不能选择 BLOCKED）。
type CapabilityStatus string

const (
	StatusAvailable CapabilityStatus = "AVAILABLE"
	StatusDegraded  CapabilityStatus = "DEGRADED"
	StatusThrottled CapabilityStatus = "THROTTLED"
	StatusBlocked   CapabilityStatus = "BLOCKED"
	StatusDisabled  CapabilityStatus = "DISABLED"
	StatusUnknown   CapabilityStatus = "UNKNOWN"
	StatusShadow    CapabilityStatus = "SHADOW"
)

// DatasetKey provider 能力按 (chain_id, dataset) 细分。
type DatasetKey struct {
	ChainID uint64
	Dataset DatasetType
}

// DatasetCapability 细到数据集的能力（§23：不能只存 provider enabled=true）。
type DatasetCapability struct {
	Enabled bool

	SupportsRange bool
	RangeVerified bool // §50：只有 RangeVerified=true 才能用于 Range Sharding

	Semantics CountSemantics
	Accuracy  CountAccuracy

	MaxRangeBlocks uint64

	RuntimeStatus CapabilityStatus // 工程决策以 Runtime 为准（§25）
	Reason        string           // 例如 token transfers BLOCKED: Free Plan endpoint effective rate block
}

// ProviderCapabilities（§22）。
type ProviderCapabilities struct {
	ProviderID string

	Chains   map[uint64]bool // 空 = 由部署矩阵决定（Chainbase 由 chain_capabilities 矩阵）
	Datasets map[DatasetKey]DatasetCapability

	SupportsBatch bool
	MaxBatchSize  int

	SupportsMultiDataset bool

	RateLimits []RateBucketPolicy

	CostPerRequestCredits float64
	CostPerRequestUSD     float64
}

// DatasetCap 便捷获取（nil-safe）。
func (p ProviderCapabilities) DatasetCap(chainID uint64, ds DatasetType) (DatasetCapability, bool) {
	c, ok := p.Datasets[DatasetKey{ChainID: chainID, Dataset: ds}]
	if ok {
		return c, true
	}
	c, ok = p.Datasets[DatasetKey{ChainID: 0, Dataset: ds}]
	return c, ok
}
