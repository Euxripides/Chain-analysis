// V1.3 RateScope / RateBucketPolicy（§26-§29：并行能力的关键抽象）。
package count

// RateScope 限流作用域（§27）。未验证 Key Scope 默认按 ACCOUNT/GLOBAL shared bucket（§43）。
type RateScope string

const (
	ScopeGlobal   RateScope = "GLOBAL"
	ScopeAccount  RateScope = "ACCOUNT"
	ScopeProject  RateScope = "PROJECT"
	ScopeAPIKey   RateScope = "API_KEY"
	ScopeEndpoint RateScope = "ENDPOINT"
	ScopeChain    RateScope = "CHAIN"
	ScopeDataset  RateScope = "DATASET"
	ScopeIP       RateScope = "IP"
	ScopeUnknown  RateScope = "UNKNOWN"
)

// RateBucketPolicy 单层限流策略。未来 Provider 可能同时存在 Account 100 RPS +
// API Key 20 RPS + Endpoint 10 RPS → 允许多层 Bucket（§27）。
type RateBucketPolicy struct {
	Scope    RateScope
	ScopeKey string // 例如 "account-1" / "dataset-token_transfers" / ""(GLOBAL)

	MaxConcurrency      int
	MaxRPS              float64
	MaxCreditsPerSecond float64
	Burst               int
}
