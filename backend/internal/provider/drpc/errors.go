package drpc

// 本文件集中 dRPC 的错误码、错误分类与脱敏输出（规范 §1.4 错误码 + 开发文档 §7.5 分类表）。
//
// 分类原则（§7.5）：
//  401/403 -> auth（不重试，开熔断）        429 -> rate_limit（遵守 Retry-After，降速）
//  402     -> quota_exhausted（不重试）      5xx -> upstream（可重试）
//  传输超时 -> 现有超时分类                  JSON 解码失败 -> invalid_json
//  body 截断 -> body_truncated               page_token 重复 -> provider_invalid_response
//  未知错误码 -> upstream，并保留 HTTP status / JSON-RPC code / 脱敏 message，绝不编造官方错误码。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// provider.Error.Message 的错误码前缀（前端可见，规范 §1.4）。
const (
	CodeNotConfigured        = "DRPC_NOT_CONFIGURED"
	CodeAuthFailed           = "DRPC_AUTH_FAILED"
	CodeQuotaExhausted       = "DRPC_QUOTA_EXHAUSTED"
	CodeRateLimited          = "DRPC_RATE_LIMITED"
	CodeManagementStale      = "DRPC_MANAGEMENT_STALE"
	CodePaginationInvalid    = "DRPC_PAGINATION_INVALID"
	CodeTxVersionUnsupported = "SOLANA_TRANSACTION_VERSION_UNSUPPORTED"
	CodeSlotTimeUnavailable  = "SOLANA_SLOT_TIME_UNAVAILABLE"
	CodeDatasetUnsupported   = "SOLANA_DATASET_UNSUPPORTED"
	CodeInvalidResponse      = "DRPC_INVALID_RESPONSE"
)

// ErrNotConfigured 表示受控密钥引用无法解析出明文（未配置 / resolver 缺失 / 引用失效）。
var ErrNotConfigured = errors.New("drpc runtime key not configured")

// newError 构造带错误码前缀的 provider.Error。
func newError(class provider.ErrorClass, code, detail string) *provider.Error {
	message := code
	if strings.TrimSpace(detail) != "" {
		message = code + ": " + detail
	}
	return &provider.Error{Provider: ProviderKey, Class: class, Message: message}
}

// notConfigured 是"未配置 runtime key"的统一错误。
//
// 规范要求它与 ClassAuth / ClassQuota 明确区分（否则前端会把"还没配 Key"显示成"密钥错误"或
// "额度用尽"）。规范没有写死具体 ErrorClass，这里取 invalid_request：它是本地配置状态，
// 重试不可能成功，也不应触发上游熔断。判定请始终使用 Message 前缀 DRPC_NOT_CONFIGURED。
func notConfigured(detail string) *provider.Error {
	err := newError(provider.ClassInvalidRequest, CodeNotConfigured, detail)
	err.Retryable = false
	return err
}

// unsupportedDataset 是 internal_transactions 等不受支持数据集的统一错误。
func unsupportedDataset(dataset string) *provider.Error {
	err := newError(provider.ClassUnsupported, CodeDatasetUnsupported,
		fmt.Sprintf("dataset %q is not served by the dRPC Solana provider", dataset))
	err.Retryable = false
	return err
}

// txVersionUnsupported 对应 maxSupportedTransactionVersion 过低的上游错误（§7.4）：
// 不得把任务标记为空数据，必须显式失败并提示升级 DRPC_SOLANA_MAX_TX_VERSION。
func txVersionUnsupported(maxVersion int, detail string) *provider.Error {
	err := newError(provider.ClassUnsupported, CodeTxVersionUnsupported,
		fmt.Sprintf("transaction version above maxSupportedTransactionVersion=%d: %s", maxVersion, detail))
	err.Retryable = false
	return err
}

// classifyHTTPStatus 按 §7.5 表把 HTTP 状态映射成 provider.Error。
//
// body 只用于附带脱敏摘要（截断到 200 字符），永远不整段回显。
func classifyHTTPStatus(status int, header http.Header, body []byte, key string) *provider.Error {
	snippet := sanitizeText(truncate(strings.TrimSpace(string(body)), 200), key)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		err := newError(provider.ClassAuth, CodeAuthFailed, fmt.Sprintf("dRPC rejected the runtime key (http %d) %s", status, snippet))
		err.HTTPStatus, err.Retryable = status, false
		return err
	case status == http.StatusPaymentRequired:
		err := newError(provider.ClassQuota, CodeQuotaExhausted, fmt.Sprintf("dRPC account quota or balance exhausted (http %d) %s", status, snippet))
		err.HTTPStatus, err.Retryable = status, false
		return err
	case status == http.StatusTooManyRequests:
		err := newError(provider.ClassRateLimit, CodeRateLimited, fmt.Sprintf("dRPC rate limited the request (http %d) %s", status, snippet))
		err.HTTPStatus, err.Retryable = status, true
		// 429 必须遵守 Retry-After，并让上层降速/降并发（§7.5、§6.4）。
		err.ReduceRate, err.ReduceConcurrency = true, true
		err.RetryAfter = parseRetryAfter(header)
		return err
	case status >= 500:
		err := newError(provider.ClassUpstream, CodeInvalidResponse, fmt.Sprintf("dRPC upstream error (http %d) %s", status, snippet))
		err.HTTPStatus, err.Retryable = status, true
		err.RetryAfter = parseRetryAfter(header)
		return err
	case status >= 400:
		// 400/404/405/422 等：请求本身有问题，重试无意义。
		err := newError(provider.ClassInvalidRequest, CodeInvalidResponse, fmt.Sprintf("dRPC rejected the request (http %d) %s", status, snippet))
		err.HTTPStatus, err.Retryable = status, false
		return err
	default:
		// 2xx/3xx 之外的未知状态：归 upstream 但保留真实状态码，不编造错误码。
		err := newError(provider.ClassUpstream, CodeInvalidResponse, fmt.Sprintf("unexpected dRPC http status %d %s", status, snippet))
		err.HTTPStatus, err.Retryable = status, true
		return err
	}
}

// classifyRPCError 按 §7.5 "JSON-RPC error 按 code/message 映射"处理。
//
// Solana 常见 code：-32015 交易版本不支持、-32602/-32600/-32601 参数或方法错误。
// 其余未知 code 归 upstream（可重试）并原样保留 code。
func classifyRPCError(code int, message string, maxTxVersion int, key string) *provider.Error {
	detail := fmt.Sprintf("rpc error %d: %s", code, sanitizeText(message, key))
	switch code {
	case -32015:
		return txVersionUnsupported(maxTxVersion, detail)
	case -32602, -32600, -32601:
		err := newError(provider.ClassInvalidRequest, CodeInvalidResponse, detail)
		err.RPCCode, err.Retryable = code, false
		return err
	case 429, -32090:
		err := newError(provider.ClassRateLimit, CodeRateLimited, detail)
		err.RPCCode, err.Retryable = code, true
		err.ReduceRate, err.ReduceConcurrency = true, true
		return err
	case 402:
		err := newError(provider.ClassQuota, CodeQuotaExhausted, detail)
		err.RPCCode, err.Retryable = code, false
		return err
	default:
		err := newError(provider.ClassUpstream, CodeInvalidResponse, detail)
		err.RPCCode, err.Retryable = code, true
		return err
	}
}

// isTransactionVersionError 判断上游错误文本是否是版本化交易不支持（用于二次确认 code 缺失的场景）。
func isTransactionVersionError(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "transaction version") && strings.Contains(lower, "not supported")
}

// classifyTransport 把 http.Do 的错误归类到现有超时分类（§7.5：不混成一个 timeout）。
func classifyTransport(ctx context.Context, err error, key string) *provider.Error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		cancelled := provider.NewCancelled(CodeInvalidResponse + ": dRPC request cancelled: " + ctxErr.Error())
		return cancelled
	}
	text := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		strings.Contains(text, "context deadline exceeded"),
		strings.Contains(text, "client.timeout"),
		strings.Contains(text, "i/o timeout"):
		perr := newError(provider.ClassRequestTimeout, CodeInvalidResponse, sanitizeText(err.Error(), key))
		perr.Retryable, perr.ReduceConcurrency = true, true
		perr.Cause = err
		return perr
	case strings.Contains(text, "connection refused"), strings.Contains(text, "no such host"),
		strings.Contains(text, "connection reset"), strings.Contains(text, "tls"):
		perr := newError(provider.ClassUpstream, CodeInvalidResponse, sanitizeText(err.Error(), key))
		perr.Retryable = true
		perr.Cause = err
		return perr
	default:
		perr := newError(provider.ClassUpstream, CodeInvalidResponse, sanitizeText(err.Error(), key))
		perr.Retryable = true
		perr.Cause = err
		return perr
	}
}

// invalidJSON 对应 JSON 解码失败（当前页不提交游标，§7.5）。
func invalidJSON(detail string) *provider.Error {
	err := newError(provider.ClassInvalidJSON, CodeInvalidResponse, detail)
	err.Retryable = true
	return err
}

// bodyTruncated 对应响应体被截断（连接中途断开或超过读取上限）。
func bodyTruncated(detail string) *provider.Error {
	err := newError(provider.ClassBodyTruncated, CodeInvalidResponse, detail)
	err.Retryable = true
	return err
}

// paginationInvalid 对应 page_token 重复/循环或连续两页完全相同（§7.3、§7.5）。
func paginationInvalid(detail string) *provider.Error {
	err := newError(provider.ClassProviderInvalid, CodePaginationInvalid, detail)
	err.Retryable = false
	return err
}

// slotTimeUnavailable 对应 slot 区间无法映射为 Data API 需要的时间区间。
//
// 不确定点：规范只给了错误码，没有写死 ErrorClass。这里取 upstream + 可重试，
// 因为失败原因通常是 getBlockTime 的瞬时不可用；调用方的重试预算会终止它。
func slotTimeUnavailable(detail string) *provider.Error {
	err := newError(provider.ClassUpstream, CodeSlotTimeUnavailable, detail)
	err.Retryable = true
	return err
}

// parseRetryAfter 解析 Retry-After（秒数或 HTTP 日期）。
// 来源：internal/provider/ankr/rpc/error_mapper.go 的同名实现（保持一致的行为）。
func parseRetryAfter(header http.Header) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delta := time.Until(when); delta > 0 {
			return delta
		}
	}
	return 0
}

// maskURL 输出可以安全写日志的 URL（规范 §1.4 关键行为 2）。
//
// 输出形如 https://lb.drpc.live/solana/***/lambda/v1/transactions/<addr>/history?start=..&limit=..&page_token=***
// 规则：第 2 个路径段（BaseURL 之后的 Key 段）固定替换为 ***，page_token 的值同样替换为 ***
// （§9.2 要求真实 Token 只留在 Checkpoint 与受控 Raw manifest 中）。
func maskURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "https://***"
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) >= 2 {
		segments[1] = "***"
	}
	masked := parsed.Scheme + "://" + parsed.Host + "/" + strings.Join(segments, "/")
	if query := parsed.Query(); len(query) > 0 {
		if query.Has("page_token") {
			query.Set("page_token", "***")
		}
		masked += "?" + query.Encode()
	}
	return masked
}

// sanitizeText 把文本里可能出现的 Key 与带 Key 的 URL 全部脱敏。
//
// Go 的 http.Client 会把完整 URL 放进错误字符串（Get "https://.../KEY/..."），
// 因此任何来自传输层的 error 都必须先过这里，再进日志/用量行/前端。
func sanitizeText(text, key string) string {
	if text == "" {
		return ""
	}
	if key != "" {
		text = strings.ReplaceAll(text, key, "***")
	}
	return maskURLsInText(text)
}

// maskURLsInText 扫描文本中的 http(s):// 片段并逐个 maskURL。
func maskURLsInText(text string) string {
	var b strings.Builder
	for {
		index := strings.Index(text, "http")
		if index < 0 {
			b.WriteString(text)
			break
		}
		b.WriteString(text[:index])
		rest := text[index:]
		if !strings.HasPrefix(rest, "http://") && !strings.HasPrefix(rest, "https://") {
			b.WriteString(rest[:4])
			text = rest[4:]
			continue
		}
		end := len(rest)
		if cut := strings.IndexAny(rest, " \t\n\r\""); cut >= 0 {
			end = cut
		}
		b.WriteString(maskURL(rest[:end]))
		text = rest[end:]
	}
	return b.String()
}

// truncate 按字符边界截断（避免切断多字节字符）。
func truncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}
