package model

import "time"

const (
	GuardNormal       = "NORMAL"
	GuardLarge        = "LARGE"
	GuardHuge         = "HUGE"
	GuardExtreme      = "EXTREME"
	GuardSuperExtreme = "SUPER_EXTREME"

	GuardReasonActualRows    = "ACTUAL_ROWS_TOO_LARGE"
	GuardReasonProjectedRows = "PROJECTED_ROWS_TOO_LARGE"
)

// VolumeGuard is the durable state of one (chain, job, subject, dataset) guard unit.
type VolumeGuard struct {
	JobID                    string     `json:"job_id"`
	SubjectID                string     `json:"subject_id"`
	Subject                  string     `json:"subject"`
	DatasetID                string     `json:"dataset_id"`
	Dataset                  string     `json:"dataset"`
	State                    string     `json:"state"`
	Reason                   string     `json:"reason"`
	RiskLevel                string     `json:"risk_level"`
	ActualRows               int64      `json:"actual_rows"`
	ProjectedRowsLow         int64      `json:"projected_rows_low"`
	ProjectedRowsMid         int64      `json:"projected_rows_mid"`
	ProjectedRowsHigh        int64      `json:"projected_rows_high"`
	Confidence               float64    `json:"confidence"`
	ActualProviderCalls      int64      `json:"actual_provider_calls"`
	ProjectedProviderCalls   int64      `json:"projected_provider_calls"`
	ActualCostUSD            float64    `json:"actual_cost_usd"`
	ProjectedCostUSD         float64    `json:"projected_cost_usd"`
	ElapsedSeconds           float64    `json:"elapsed_seconds"`
	ProjectedDurationSeconds float64    `json:"projected_duration_seconds"`
	CompletedShards          int64      `json:"completed_shards"`
	TotalShards              int64      `json:"total_shards"`
	Override                 bool       `json:"guard_override"`
	TriggeredAt              *time.Time `json:"guard_triggered_at,omitempty"`
	ApprovedAt               *time.Time `json:"guard_approved_at,omitempty"`
	ApprovedBy               string     `json:"guard_approved_by,omitempty"`
	ApprovedProjection       int64      `json:"guard_approved_projection,omitempty"`
	UpdatedAt                time.Time  `json:"updated_at"`
}

type VolumeGuardThresholds struct {
	Enabled             bool
	WarnRows            int64
	HardActualRows      int64
	HardProjectedRows   int64
	ExtremeRows         int64
	SuperExtremeRows    int64
	ProjectedConfidence float64
	ExtremeConfidence   float64
}
