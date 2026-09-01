package rpc

import (
	"sync"
	"time"
)

// ---- Budget（§36/§37/§167-§170/§214）----
//
// RPC 便宜不等于可以无限调用。Agent/Investigation/Backfill/Trace 必须有预算。
// 预算检查发生在任何 Provider 请求之前：超额时 Provider Request = 0。

// BudgetLimits 默认限额（按用途）。
type BudgetLimits struct {
	MaxRequests int64
	MaxCredits  int64
	// MaxTraceRequests 单独限制 Trace（§109/§112）
	MaxTraceRequests int64
}

// BudgetUsage 单个预算键的用量快照。
type BudgetUsage struct {
	Key         string    `json:"key"`
	Purpose     string    `json:"purpose"`
	Requests    int64     `json:"requests"`
	Credits     int64     `json:"credits"`
	TraceCalls  int64     `json:"trace_calls"`
	MaxRequests int64     `json:"max_requests,omitempty"`
	MaxCredits  int64     `json:"max_credits,omitempty"`
	WindowStart time.Time `json:"window_start"`
}

type budgetCounter struct {
	requests    int64
	credits     int64
	traceCalls  int64
	purpose     string
	windowStart time.Time
}

// BudgetManager 内存预算计数器。
// 平台使用 Provider-independent 的内部成本单位（§170），换 Provider 不影响口径。
type BudgetManager struct {
	mu       sync.Mutex
	counters map[string]*budgetCounter
	defaults map[string]BudgetLimits
	window   time.Duration
	enabled  bool
}

func NewBudgetManager(window time.Duration, defaults map[string]BudgetLimits, enabled bool) *BudgetManager {
	if window <= 0 {
		window = 24 * time.Hour
	}
	if defaults == nil {
		defaults = map[string]BudgetLimits{}
	}
	return &BudgetManager{counters: map[string]*budgetCounter{}, defaults: defaults, window: window, enabled: enabled}
}

func (m *BudgetManager) limitsFor(b BudgetContext) BudgetLimits {
	limits := m.defaults[b.Purpose]
	// 请求显式声明的上限优先（更严格的一方生效）
	if b.MaxRequests > 0 && (limits.MaxRequests == 0 || b.MaxRequests < limits.MaxRequests) {
		limits.MaxRequests = b.MaxRequests
	}
	if b.MaxCredits > 0 && (limits.MaxCredits == 0 || b.MaxCredits < limits.MaxCredits) {
		limits.MaxCredits = b.MaxCredits
	}
	return limits
}

// Allow 预留一次调用的额度。超额返回 ErrBudgetExceeded，且不得发出 Provider 请求（§214）。
func (m *BudgetManager) Allow(b BudgetContext, weight int64, isTrace bool) error {
	if m == nil || !m.enabled {
		return nil
	}
	limits := m.limitsFor(b)
	if limits.MaxRequests == 0 && limits.MaxCredits == 0 && limits.MaxTraceRequests == 0 {
		// 无限额配置：仍然计数，便于成本可观测
		m.add(b, weight, isTrace)
		return nil
	}
	key := b.Key()
	m.mu.Lock()
	defer m.mu.Unlock()
	counter := m.counterLocked(key, b.Purpose)
	if limits.MaxRequests > 0 && counter.requests+1 > limits.MaxRequests {
		return &Error{Class: ClassBudgetExceeded,
			Message: "request budget exhausted for " + key, Retryable: false}
	}
	if limits.MaxCredits > 0 && counter.credits+weight > limits.MaxCredits {
		return &Error{Class: ClassBudgetExceeded,
			Message: "credit budget exhausted for " + key, Retryable: false}
	}
	if isTrace && limits.MaxTraceRequests > 0 && counter.traceCalls+1 > limits.MaxTraceRequests {
		return &Error{Class: ClassBudgetExceeded,
			Message: "trace budget exhausted for " + key, Retryable: false}
	}
	counter.requests++
	counter.credits += weight
	if isTrace {
		counter.traceCalls++
	}
	return nil
}

func (m *BudgetManager) add(b BudgetContext, weight int64, isTrace bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counter := m.counterLocked(b.Key(), b.Purpose)
	counter.requests++
	counter.credits += weight
	if isTrace {
		counter.traceCalls++
	}
}

func (m *BudgetManager) counterLocked(key, purpose string) *budgetCounter {
	counter, ok := m.counters[key]
	if !ok || time.Since(counter.windowStart) > m.window {
		counter = &budgetCounter{purpose: purpose, windowStart: time.Now()}
		m.counters[key] = counter
	}
	return counter
}

// Snapshot 当前预算用量（Admin/成本预估用，§174）。
func (m *BudgetManager) Snapshot() []BudgetUsage {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]BudgetUsage, 0, len(m.counters))
	for key, counter := range m.counters {
		limits := m.defaults[counter.purpose]
		out = append(out, BudgetUsage{
			Key: key, Purpose: counter.purpose, Requests: counter.requests, Credits: counter.credits,
			TraceCalls: counter.traceCalls, MaxRequests: limits.MaxRequests, MaxCredits: limits.MaxCredits,
			WindowStart: counter.windowStart,
		})
	}
	return out
}

// Reset 清空某预算键（任务结束后回收）。
func (m *BudgetManager) Reset(key string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.counters, key)
	m.mu.Unlock()
}
