package model

import (
	"fmt"
	"strings"
	"time"
)

type PerformanceMode string

const (
	PerformanceSafe  PerformanceMode = "safe"
	PerformanceFast  PerformanceMode = "fast"
	PerformanceTurbo PerformanceMode = "turbo"
)

func ParsePerformanceMode(value string) (PerformanceMode, error) {
	mode := PerformanceMode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		return PerformanceFast, nil
	}
	switch mode {
	case PerformanceSafe, PerformanceFast, PerformanceTurbo:
		return mode, nil
	default:
		return "", fmt.Errorf("performance_mode must be safe, fast, or turbo")
	}
}

func (m PerformanceMode) Level() int {
	switch m {
	case PerformanceTurbo:
		return 3
	case PerformanceFast:
		return 2
	default:
		return 1
	}
}

func LowerPerformanceMode(mode PerformanceMode) PerformanceMode {
	switch mode {
	case PerformanceTurbo:
		return PerformanceFast
	case PerformanceFast:
		return PerformanceSafe
	default:
		return PerformanceSafe
	}
}

type DownloadPerformanceProfile struct {
	ID                    string          `json:"id,omitempty"`
	Scope                 string          `json:"scope"`
	Chain                 string          `json:"chain"`
	Dataset               string          `json:"dataset"`
	DefaultMode           PerformanceMode `json:"default_mode"`
	SafeConcurrency       int             `json:"safe_concurrency"`
	FastConcurrency       int             `json:"fast_concurrency"`
	TurboConcurrency      int             `json:"turbo_concurrency"`
	HardCeiling           int             `json:"hard_ceiling"`
	RejectBoundary        int             `json:"reject_boundary"`
	MemoryBudgetBytes     int64           `json:"memory_budget_bytes"`
	AbsoluteRSSLimitBytes int64           `json:"absolute_rss_limit_bytes"`
	PageSizeDefault       int             `json:"page_size_default"`
	RequestTimeoutSeconds int             `json:"request_timeout_seconds"`
	RangeShardingEnabled  bool            `json:"range_sharding_enabled"`
	Version               string          `json:"version"`
	VerifiedAt            time.Time       `json:"verified_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
	UpdatedBy             string          `json:"updated_by"`
}

func (p DownloadPerformanceProfile) Resolve(mode PerformanceMode) int {
	target := p.FastConcurrency
	if mode == PerformanceSafe {
		target = p.SafeConcurrency
	}
	if mode == PerformanceTurbo {
		target = p.TurboConcurrency
	}
	if target < 1 {
		target = 1
	}
	if p.HardCeiling > 0 && target > p.HardCeiling {
		return p.HardCeiling
	}
	return target
}

type PerformanceRuntimeState struct {
	RequestedMode     PerformanceMode `json:"requested_mode"`
	EffectiveMode     PerformanceMode `json:"effective_mode"`
	TargetConcurrency int             `json:"target_concurrency"`
	ActiveConcurrency int             `json:"active_concurrency"`
	ModeReason        string          `json:"mode_reason"`
	ModeChangedAt     time.Time       `json:"mode_changed_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}
