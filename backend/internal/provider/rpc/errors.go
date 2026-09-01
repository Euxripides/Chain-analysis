// Package rpc：Provider-independent 的统一链 RPC 基础设施（V1.5）。
//
// 硬约束（§8/§247）：
//   - 业务模块不得直接访问 Provider URL，也不得持有 API Key；
//   - 所有 RPC 必须经过 Gateway；Router 负责选 Provider；
//   - 错误必须标准化；日志与错误响应不得泄露 Secret；
//   - 默认只读；写方法默认拒绝。
package rpc

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrorClass 统一错误分类（§59）。
type ErrorClass string

const (
	ClassRateLimit         ErrorClass = "rate_limit"
	ClassConnectionLimit   ErrorClass = "connection_limit"
	ClassTimeout           ErrorClass = "timeout"
	ClassUpstream          ErrorClass = "upstream"
	ClassInvalidResponse   ErrorClass = "invalid_response"
	ClassMethodUnsupported ErrorClass = "method_unsupported"
	ClassArchiveRequired   ErrorClass = "archive_required"
	ClassTraceUnsupported  ErrorClass = "trace_unsupported"
	ClassRequestTooLarge   ErrorClass = "request_too_large"
	ClassResponseTooLarge  ErrorClass = "response_too_large"
	ClassRangeTooLarge     ErrorClass = "range_too_large"
	ClassExecutionReverted ErrorClass = "execution_reverted"
	ClassInvalidParams     ErrorClass = "invalid_params"
	ClassBudgetExceeded    ErrorClass = "budget_exceeded"
	ClassCircuitOpen       ErrorClass = "circuit_open"
	ClassMethodDenied      ErrorClass = "method_denied"
	ClassNoProvider        ErrorClass = "no_provider"
	ClassAuth              ErrorClass = "auth"
	ClassCancelled         ErrorClass = "cancelled"
	// ClassLocalMiss：consistency=local_only 且本地无结果。绝不发 Provider 请求（§204）。
	ClassLocalMiss ErrorClass = "local_miss"
)

// Error 统一 Provider 错误（§146/§147）。
type Error struct {
	Class    ErrorClass
	Provider string
	Chain    string
	Method   string

	HTTPStatus int
	RPCCode    *int

	Retryable bool

	// 控制信号：与 Advanced API 一致，Rate 与 Concurrency 分离（§35/§60/§61）
	ReduceRate        bool
	ReduceConcurrency bool
	ReduceRange       bool
	RetryAfter        time.Duration

	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	parts := []string{string(e.Class)}
	if e.Provider != "" {
		parts = append(parts, "provider="+e.Provider)
	}
	if e.Chain != "" {
		parts = append(parts, "chain="+e.Chain)
	}
	if e.Method != "" {
		parts = append(parts, "method="+e.Method)
	}
	return "[" + strings.Join(parts, " ") + "] " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// publicMessages 对外文案（§148：不返回 Provider 原始栈/内部地址/Key）。
var publicMessages = map[ErrorClass]string{
	ClassRateLimit:         "Provider is rate limiting requests, please retry shortly",
	ClassConnectionLimit:   "Provider connection limit reached, please retry shortly",
	ClassTimeout:           "Chain provider request timed out",
	ClassUpstream:          "Provider temporarily unavailable",
	ClassInvalidResponse:   "Provider returned an unreadable response",
	ClassMethodUnsupported: "This chain method is not available on the configured provider",
	ClassArchiveRequired:   "Historical state requires an archive-capable provider",
	ClassTraceUnsupported:  "Trace is not available for this chain and provider",
	ClassRequestTooLarge:   "Request payload is too large",
	ClassResponseTooLarge:  "Provider response exceeded the configured size limit",
	ClassRangeTooLarge:     "Requested block range is too large",
	ClassExecutionReverted: "Contract call reverted",
	ClassInvalidParams:     "Invalid chain request parameters",
	ClassBudgetExceeded:    "RPC budget exceeded for this operation",
	ClassCircuitOpen:       "Provider circuit breaker is open",
	ClassMethodDenied:      "This RPC method is not permitted",
	ClassNoProvider:        "No provider available for this chain capability",
	ClassAuth:              "Provider authentication failed",
	ClassCancelled:         "Request cancelled",
	ClassLocalMiss:         "No locally cached result for this request",
}

// Public 面向前端的安全文案（§148/§247-24）。
func (e *Error) Public() string {
	if e == nil {
		return ""
	}
	if msg, ok := publicMessages[e.Class]; ok {
		return msg
	}
	return "Chain provider request failed"
}

// AsError 把任意 error 转成本包错误（未知归类为 upstream）。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	return &Error{Class: ClassUpstream, Message: err.Error(), Cause: err, Retryable: true}
}

// IsClass 判断错误类别。
func IsClass(err error, class ErrorClass) bool {
	pe := AsError(err)
	return pe != nil && pe.Class == class
}

func newError(class ErrorClass, format string, args ...any) *Error {
	return &Error{Class: class, Message: fmt.Sprintf(format, args...)}
}

// ---- Secret 脱敏（§31/§148/§224/§247-24）----

// secretMinLen 少于该长度的片段不当作 Secret，避免把普通路径全部打码。
const secretMinLen = 12

// RedactURL 把 endpoint 里的凭证段替换为 ***：
//
//	https://rpc.ankr.com/bsc/SECRET → rpc.ankr.com/bsc/***
func RedactURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "***"
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i, segment := range segments {
		if len(segment) >= secretMinLen {
			segments[i] = "***"
		}
	}
	path := strings.Join(segments, "/")
	if path != "" {
		path = "/" + path
	}
	return parsed.Host + path
}

// Redact 从任意文本中抹掉已知 Secret（错误信息可能内嵌完整 URL）。
func Redact(text string, secrets ...string) string {
	out := text
	for _, secret := range secrets {
		s := strings.TrimSpace(secret)
		if len(s) < secretMinLen {
			continue
		}
		out = strings.ReplaceAll(out, s, "***")
	}
	return redactInlineURLs(out)
}

// redactInlineURLs 兜底：文本中出现的 http(s) URL 一律走 RedactURL。
func redactInlineURLs(text string) string {
	if !strings.Contains(text, "http") {
		return text
	}
	var b strings.Builder
	rest := text
	for {
		idx := strings.Index(rest, "http")
		if idx < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:idx])
		tail := rest[idx:]
		end := strings.IndexAny(tail, " \t\n\"',)")
		if end < 0 {
			end = len(tail)
		}
		b.WriteString(RedactURL(tail[:end]))
		rest = tail[end:]
	}
}
