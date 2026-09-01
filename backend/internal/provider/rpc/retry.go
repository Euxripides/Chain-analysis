package rpc

import (
	"math/rand"
	"time"
)

// ---- Retry / Failover 决策（§59/§64/§222/§229）----

// RetryPolicy 重试策略。
type RetryPolicy struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseBackoff: 200 * time.Millisecond, MaxBackoff: 5 * time.Second}
}

// fatalClasses 明确不应盲目重试（§64）。
var fatalClasses = map[ErrorClass]bool{
	ClassExecutionReverted: true,
	ClassInvalidParams:     true,
	ClassMethodUnsupported: true,
	ClassTraceUnsupported:  true,
	ClassArchiveRequired:   true,
	ClassMethodDenied:      true,
	ClassBudgetExceeded:    true,
	ClassRequestTooLarge:   true,
	ClassRangeTooLarge:     true,
	ClassResponseTooLarge:  true,
	ClassAuth:              true,
	ClassCancelled:         true,
}

// capabilityClasses 属于"换一个 Provider 也许可以"的错误：
// 不在同一 Provider 上重试，直接 failover（§222）。
var capabilityClasses = map[ErrorClass]bool{
	ClassMethodUnsupported: true,
	ClassTraceUnsupported:  true,
	ClassArchiveRequired:   true,
	ClassCircuitOpen:       true,
}

// IsFatal 是否终止（既不重试也不换 Provider 就能解决的错误）。
func IsFatal(class ErrorClass) bool { return fatalClasses[class] }

// IsCapability 是否是能力类错误（应立即 failover）。
func IsCapability(class ErrorClass) bool { return capabilityClasses[class] }

// Decide 同一 Provider 上是否重试，以及退避时长。
func (p RetryPolicy) Decide(err *Error, attempt int) (bool, time.Duration) {
	if err == nil || attempt+1 >= p.MaxAttempts {
		return false, 0
	}
	if IsCapability(err.Class) || IsFatal(err.Class) || !err.Retryable {
		return false, 0
	}
	// 429 优先遵守 Retry-After（§60）
	if err.RetryAfter > 0 {
		return true, capBackoff(err.RetryAfter, p.MaxBackoff)
	}
	backoff := p.BaseBackoff * time.Duration(1<<uint(attempt))
	jitter := time.Duration(rand.Int63n(int64(p.BaseBackoff) + 1))
	return true, capBackoff(backoff+jitter, p.MaxBackoff)
}

func capBackoff(d, max time.Duration) time.Duration {
	if max > 0 && d > max {
		return max
	}
	return d
}
