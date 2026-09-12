// Package dataset：Dataset Service —— 所有业务统一入口（Coverage 检查 → 只下载缺口）。
package dataset

import (
	"encoding/json"
	"time"
)

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
	Mode            string    `json:"mode"` // economy|balanced|turbo（V1.1 §41-44）
	PerformanceMode string    `json:"performance_mode"`
	Concurrency     *int      `json:"concurrency,omitempty"`
	Subjects        []string  `json:"subjects"`
	Chain           string    `json:"chain"`
	Datasets        []string  `json:"datasets"`
	Range           RangeSpec `json:"range"`
	ReusePolicy     string    `json:"reuse_policy"`

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

// RangeSpecFromMap 把 v2 API 的 JSON range 对象转换为 RangeSpec。
// 只接受文档允许的字段；无法解析时返回零值，由 ResolveRange/Preview 统一报错。
func RangeSpecFromMap(m map[string]any) RangeSpec {
	out := RangeSpec{}
	if m == nil {
		return out
	}
	if v, ok := m["type"].(string); ok {
		out.Type = v
	}
	if v, ok := numberToInt64(m["from_block"]); ok {
		out.FromBlock = &v
	}
	if v, ok := numberToInt64(m["to_block"]); ok {
		out.ToBlock = &v
	}
	if v, ok := m["from"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			out.From = &t
		}
	}
	if v, ok := m["to"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			out.To = &t
		}
	}
	return out
}

func numberToInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}
