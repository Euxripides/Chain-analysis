// Package dataset：Dataset Service —— 所有业务统一入口（Coverage 检查 → 只下载缺口）。
package dataset

import "time"

type RangeSpec struct {
	Type      string     `json:"type"` // full | block | time
	FromBlock *int64     `json:"from_block"`
	ToBlock   *int64     `json:"to_block"`
	From      *time.Time `json:"from"`
	To        *time.Time `json:"to"`
}

type EnsureRequest struct {
	Source          string    `json:"source"`
	Priority        int       `json:"priority"`
	Subjects        []string  `json:"subjects"`
	Chain           string    `json:"chain"`
	Datasets        []string  `json:"datasets"`
	Range           RangeSpec `json:"range"`
	ReusePolicy     string    `json:"reuse_policy"`
	PerformanceMode string    `json:"performance_mode"`
	// Concurrency exists only so legacy numeric requests can be rejected explicitly.
	Concurrency *int `json:"concurrency,omitempty"`

	// V1.2：案件/标签（§22/§23）
	CaseName          string                 `json:"case_name"`
	Tags              []string               `json:"tags"`
	CreateMissingMeta bool                   `json:"create_missing_meta"`
	SubjectMeta       map[string]SubjectMeta `json:"subject_meta"`
}

// SubjectMeta 批量地址 Per-Address Override（§23）。
type SubjectMeta struct {
	CaseName string   `json:"case_name"`
	Tags     []string `json:"tags"`
}

type Gap struct {
	FromBlock int64 `json:"from_block"`
	ToBlock   int64 `json:"to_block"`
}

type SubjectDatasetCoverage struct {
	Subject       string  `json:"subject"`
	Dataset       string  `json:"dataset"`
	CoveredBlocks int64   `json:"covered_blocks"`
	TotalBlocks   int64   `json:"total_blocks"`
	Ratio         float64 `json:"ratio"`
	Gaps          []Gap   `json:"gaps"`
}

type CoveragePreviewResponse struct {
	Chain        string                   `json:"chain"`
	Datasets     []string                 `json:"datasets"`
	Items        []SubjectDatasetCoverage `json:"items"`
	OverallRatio float64                  `json:"overall_ratio"`
}

type EnsureResponse struct {
	Status     string  `json:"status"` // planned | ready
	DatasetRef string  `json:"dataset_ref"`
	JobID      *string `json:"job_id"`
	Coverage   float64 `json:"coverage"`
}
