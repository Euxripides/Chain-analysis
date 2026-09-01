package ankrrpc

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// ---- Provider-specific → 统一错误映射（§146/§147）----

// mapContextError context 错误优先（取消不重试）。
func mapContextError(ctx context.Context, err error) *rpc.Error {
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return &rpc.Error{Class: rpc.ClassCancelled, Message: "request cancelled", Retryable: false, Cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &rpc.Error{Class: rpc.ClassTimeout, Message: "request deadline exceeded", Retryable: true, Cause: err}
	}
	return &rpc.Error{Class: rpc.ClassUpstream, Message: err.Error(), Retryable: true, Cause: err}
}

// mapHTTPError 非 200 响应。
func mapHTTPError(status int, body []byte, retryAfter time.Duration) *rpc.Error {
	switch {
	case status == http.StatusTooManyRequests:
		return &rpc.Error{Class: rpc.ClassRateLimit, HTTPStatus: status, Retryable: true,
			ReduceRate: true, RetryAfter: retryAfter, Message: "provider rate limited the request"}
	case status == http.StatusRequestEntityTooLarge:
		return &rpc.Error{Class: rpc.ClassRequestTooLarge, HTTPStatus: status, Message: "request entity too large"}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &rpc.Error{Class: rpc.ClassAuth, HTTPStatus: status, Message: "provider rejected the credential"}
	case status == http.StatusBadRequest:
		return &rpc.Error{Class: rpc.ClassInvalidParams, HTTPStatus: status, Message: "provider rejected the request"}
	case status >= 500:
		return &rpc.Error{Class: rpc.ClassUpstream, HTTPStatus: status, Retryable: true,
			Message: "provider http " + strconv.Itoa(status)}
	default:
		return &rpc.Error{Class: rpc.ClassUpstream, HTTPStatus: status, Retryable: true,
			Message: "provider http " + strconv.Itoa(status)}
	}
}

// mapRPCError JSON-RPC error 对象 → 统一分类。
// Ankr 官方错误码与常见 EVM 客户端错误一起处理，字符串匹配只作兜底（§25）。
func mapRPCError(code int, message string, methodClass rpc.MethodClass) *rpc.Error {
	rpcCode := code
	err := &rpc.Error{RPCCode: &rpcCode, Message: strings.TrimSpace(message)}
	lower := strings.ToLower(message)

	switch code {
	case -32090, -32005:
		err.Class = rpc.ClassRateLimit
		err.Retryable = true
		err.ReduceRate = true
		return err
	case -32067, -32068, -32069, -32070, -32077:
		err.Class = rpc.ClassConnectionLimit
		err.Retryable = true
		err.ReduceConcurrency = true
		return err
	case -32062:
		err.Class = rpc.ClassRequestTooLarge
		return err
	case -32071:
		err.Class = rpc.ClassTimeout
		err.Retryable = true
		return err
	case -32601:
		// method not found：Trace/Debug 场景要区分成能力缺失（§222）
		if methodClass == rpc.ClassTrace || methodClass == rpc.ClassDebugTrace {
			err.Class = rpc.ClassTraceUnsupported
		} else {
			err.Class = rpc.ClassMethodUnsupported
		}
		return err
	case -32602:
		err.Class = rpc.ClassInvalidParams
		return err
	case 3:
		err.Class = rpc.ClassExecutionReverted
		return err
	}

	switch {
	case strings.Contains(lower, "execution reverted"), strings.Contains(lower, "revert"):
		err.Class = rpc.ClassExecutionReverted
	case strings.Contains(lower, "missing trie node"), strings.Contains(lower, "state is not available"),
		strings.Contains(lower, "state not available"), strings.Contains(lower, "archive"),
		strings.Contains(lower, "older than"):
		err.Class = rpc.ClassArchiveRequired
	case strings.Contains(lower, "query returned more than"), strings.Contains(lower, "block range"),
		strings.Contains(lower, "range too large"), strings.Contains(lower, "too many results"),
		strings.Contains(lower, "limit exceeded") && methodClass == rpc.ClassLogs:
		err.Class = rpc.ClassRangeTooLarge
		err.ReduceRange = true
	case strings.Contains(lower, "not supported"), strings.Contains(lower, "unsupported"),
		strings.Contains(lower, "not available"), strings.Contains(lower, "does not exist/is not available"):
		if methodClass == rpc.ClassTrace || methodClass == rpc.ClassDebugTrace {
			err.Class = rpc.ClassTraceUnsupported
		} else {
			err.Class = rpc.ClassMethodUnsupported
		}
	case strings.Contains(lower, "rate"), strings.Contains(lower, "too many requests"):
		err.Class = rpc.ClassRateLimit
		err.Retryable = true
		err.ReduceRate = true
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "timed out"):
		err.Class = rpc.ClassTimeout
		err.Retryable = true
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "api key"),
		strings.Contains(lower, "forbidden"):
		err.Class = rpc.ClassAuth
	default:
		err.Class = rpc.ClassUpstream
		err.Retryable = true
	}
	return err
}

// parseRetryAfter 遵守 Retry-After（§60）。
func parseRetryAfter(header http.Header) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}
