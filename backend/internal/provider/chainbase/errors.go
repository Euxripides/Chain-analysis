// V1.2 Chainbase Count Oracle：错误分类（§17：任何失败都必须 Fail Open）。
package chainbase

import (
	"net/http"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// cbError 构造 provider.Error 的快捷方式（与 Ankr 同一错误模型，供统一背压/重试/可观测性使用）。
func cbError(class provider.ErrorClass, msg string, retryable bool) *provider.Error {
	return &provider.Error{Provider: "chainbase", Class: class, Message: msg, Retryable: retryable}
}

func cbErrorHTTP(code int, msg string, retryAfter string) *provider.Error {
	pe := &provider.Error{Provider: "chainbase", HTTPStatus: code, Retryable: true}
	switch {
	case code == http.StatusTooManyRequests:
		pe.Class = provider.ClassRateLimit
		pe.Message = "chainbase rate limited: " + msg
		pe.ReduceRate = true
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		pe.Class = provider.ClassAuth
		pe.Message = "chainbase auth failed (check CHAINBASE_API_KEY): " + msg
		pe.Retryable = false
	case code >= 500:
		pe.Class = provider.ClassUpstream
		pe.Message = "chainbase upstream error: " + msg
	default:
		pe.Class = provider.ClassInvalidRequest
		pe.Message = "chainbase request error: " + msg
		pe.Retryable = false
	}
	return pe
}

// classifyByMessage 字符串兜底（优先级低于明确 code/status）。
func classifyByMessage(pe *provider.Error, codeStr, message string) {
	low := strings.ToLower(message)
	switch {
	case strings.Contains(low, "rate") || strings.Contains(low, "too many"):
		pe.Class = provider.ClassRateLimit
		pe.Retryable = true
		pe.ReduceRate = true
	case strings.Contains(low, "quota") || strings.Contains(low, "exhaust") || strings.Contains(low, "insufficient credit"):
		pe.Class = provider.ClassRateLimit
		pe.Message = "chainbase quota exhausted: " + message
		pe.Retryable = false // 配额耗尽：重试无意义 → Fail Open（§17）
	case strings.Contains(low, "auth") || strings.Contains(low, "api key") || strings.Contains(low, "unauthorized") || strings.Contains(low, "forbidden"):
		pe.Class = provider.ClassAuth
		pe.Retryable = false
	case strings.Contains(low, "invalid") || strings.Contains(low, "unsupported") || strings.Contains(low, "not found") || strings.Contains(low, "no data"):
		pe.Class = provider.ClassInvalidRequest
		pe.Retryable = false
	default:
		pe.Class = provider.ClassUpstream
		pe.Retryable = true
	}
	if codeStr == "" {
		codeStr = "unknown"
	}
	if pe.Message == "" {
		pe.Message = codeStr + ": " + message
	}
}
