// V1.3 Count Oracle Provider Port：统一 Error Model（§55/§56）。
package count

// ErrorClass 统一错误分类（§55）。Adapter 负责把 HTTP 429 / provider code
// 转成统一 Error Class；Planner 永远不看 Provider-specific error。
type ErrorClass string

const (
	ErrRateLimit         ErrorClass = "RATE_LIMIT"
	ErrTimeout           ErrorClass = "TIMEOUT"
	ErrConnection        ErrorClass = "CONNECTION"
	ErrAuth              ErrorClass = "AUTH"
	ErrQuota             ErrorClass = "QUOTA"
	ErrUnsupported       ErrorClass = "UNSUPPORTED"
	ErrInvalidResponse   ErrorClass = "INVALID_RESPONSE"
	ErrSemanticsMismatch ErrorClass = "SEMANTICS_MISMATCH"
	ErrProvider5xx       ErrorClass = "PROVIDER_5XX"
	ErrCircuitOpen       ErrorClass = "CIRCUIT_OPEN"
	ErrDeadline          ErrorClass = "DEADLINE"
	ErrCancelled         ErrorClass = "CANCELLED"
	ErrNoCredential      ErrorClass = "NO_CREDENTIAL"
	ErrUnknown           ErrorClass = "UNKNOWN"
)

// CountError 统一 Provider 错误。
type CountError struct {
	ProviderID string
	Class      ErrorClass
	Message    string
	Retryable  bool // RATE_LIMIT/TIMEOUT/CONNECTION/5XX 可按 provider retry policy 重试
	Cause      error
}

func (e *CountError) Error() string {
	if e.Message != "" {
		return string(e.Class) + ": " + e.Message
	}
	return string(e.Class)
}

func (e *CountError) Unwrap() error { return e.Cause }

func NewCountError(providerID string, class ErrorClass, msg string, retryable bool) *CountError {
	return &CountError{ProviderID: providerID, Class: class, Message: msg, Retryable: retryable}
}

// 常用构造。
func RateLimit(providerID, msg string) *CountError {
	return NewCountError(providerID, ErrRateLimit, msg, true)
}

func Timeout(providerID, msg string) *CountError {
	return NewCountError(providerID, ErrTimeout, msg, true)
}

func Connection(providerID, msg string) *CountError {
	return NewCountError(providerID, ErrConnection, msg, true)
}

func Auth(providerID, msg string) *CountError {
	return NewCountError(providerID, ErrAuth, msg, false)
}

func Quota(providerID, msg string) *CountError {
	return NewCountError(providerID, ErrQuota, msg, false)
}

func Provider5xx(providerID, msg string) *CountError {
	return NewCountError(providerID, ErrProvider5xx, msg, true)
}

func Unsupported(providerID, msg string) *CountError {
	return NewCountError(providerID, ErrUnsupported, msg, false)
}

func Deadline(providerID, msg string) *CountError {
	return NewCountError(providerID, ErrDeadline, msg, false)
}

// Class 断言错误类型（nil-safe）。
func Class(err error) ErrorClass {
	if err == nil {
		return ""
	}
	if ce, ok := err.(*CountError); ok {
		return ce.Class
	}
	return ErrUnknown
}

// Retryable 断言错误是否可重试（nil-safe：nil 成功 → false）。
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if ce, ok := err.(*CountError); ok {
		return ce.Retryable
	}
	return false
}
