// V1.4 Ankr Fetch 引擎：Range Certification / Global Fetch Profiles / Concurrency Runs / Queue Diff（§14/§44/§53/§56）。
package model

import "time"

// RangeCertification（§14）Transactions Multi-Range Certification 证据。
type RangeCertification struct {
	Chain               string     `json:"chain"`
	Dataset             string     `json:"dataset"`
	RangeSupported      bool       `json:"range_supported"`
	RangeVerified       bool       `json:"range_verified"`
	ReferenceRun        string     `json:"reference_run"`
	Missing             int64      `json:"missing"`
	RealDuplicates      int64      `json:"real_duplicates"`
	BoundaryViolations  int64      `json:"boundary_violations"`
	UnionRows           int64      `json:"union_rows"`
	VerificationVersion int        `json:"verification_version"`
	VerifiedAt          *time.Time `json:"verified_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// GlobalFetchProfile（§44）Concurrency Sweet Spot 画像。
type GlobalFetchProfile struct {
	Chain           string    `json:"chain"`
	DatasetMix      string    `json:"dataset_mix"`
	PageSizeClass   int       `json:"page_size_class"`
	SafeConcurrency int       `json:"safe_concurrency"`
	BestGoodput     float64   `json:"best_goodput"`
	P50Ms           int       `json:"p50_ms"`
	P95Ms           int       `json:"p95_ms"`
	RateLimitRate   float64   `json:"rate_limit_rate"`
	TimeoutRate     float64   `json:"timeout_rate"`
	RetryAmp        float64   `json:"retry_amp"`
	AvgWireBytes    int64     `json:"avg_wire_bytes"`
	AvgDecodedBytes int64     `json:"avg_decoded_bytes"`
	Samples         int       `json:"samples"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// FetchConcurrencyRun（§53）每次并发 Benchmark 实验。
type FetchConcurrencyRun struct {
	ID                string    `json:"id"`
	RunID             string    `json:"run_id"`
	Chain             string    `json:"chain"`
	DatasetMix        string    `json:"dataset_mix"`
	ConcurrencyTarget int       `json:"concurrency_target"`
	WallMs            int64     `json:"wall_ms"`
	Rows              int64     `json:"rows"`
	UniqueRows        int64     `json:"unique_rows"`
	Requests          int       `json:"requests"`
	Credits           float64   `json:"credits"`
	TTFBP50Ms         int       `json:"ttfb_p50_ms"`
	TTFBP95Ms         int       `json:"ttfb_p95_ms"`
	BodyP50Ms         int       `json:"body_p50_ms"`
	BodyP95Ms         int       `json:"body_p95_ms"`
	RetryAmp          float64   `json:"retry_amp"`
	R429Count         int64     `json:"r429_count"`
	TimeoutCount      int64     `json:"timeout_count"`
	WireBytes         int64     `json:"wire_bytes"`
	DecodedBytes      int64     `json:"decoded_bytes"`
	MaxMemory         int64     `json:"max_memory"`
	MaxSpool          int64     `json:"max_spool"`
	Pass              bool      `json:"pass"`
	ExcludeReason     string    `json:"exclude_reason"`
	CreatedAt         time.Time `json:"created_at"`
}

// FetchQueueDiff（§56）Global Ready Queue Shadow 对照。
type FetchQueueDiff struct {
	ID               string    `json:"id"`
	SampledReady     int       `json:"sampled_ready"`
	ActualFirstShard string    `json:"actual_first_shard"`
	V14FirstShard    string    `json:"v14_first_shard"`
	OrderMismatch    bool      `json:"order_mismatch"`
	MismatchRank     int       `json:"mismatch_rank"`
	CreatedAt        time.Time `json:"created_at"`
}
