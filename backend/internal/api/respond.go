package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// §21.3 统一响应信封：success / data / meta / error。
// 错误码与 HTTP 状态分离且保持稳定（§21.3 错误码前缀）。
type apiMeta struct {
	RequestID   string  `json:"request_id"`
	TraceID     string  `json:"trace_id"`
	ServerTime  string  `json:"server_time"`
	DataVersion string  `json:"data_version,omitempty"`
	NextCursor  *string `json:"next_cursor"`
	Total       *int    `json:"total,omitempty"`
}

type fieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiError struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	FieldErrors []fieldError   `json:"field_errors"`
	Retryable   bool           `json:"retryable"`
	Details     map[string]any `json:"details,omitempty"`
}

type envelope struct {
	Success bool      `json:"success"`
	Data    any       `json:"data"`
	Meta    apiMeta   `json:"meta"`
	Error   *apiError `json:"error"`
}

// jsonStrings 保证数组字段永远序列化成 []（而不是 null）。
//
// 2026-09-16 真实缺陷：身份没有任何角色绑定时 Roles 为 nil，响应里是 "roles": null，
// 前端渲染期抛 Cannot read properties of null (reading 'join') 并整页白屏。
// 契约上 roles/permissions 是数组就不该出现 null —— 客户端归一化是第二道防线，不是唯一一道。
func jsonStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func metaFor(ctx context.Context) apiMeta {
	m := metaFrom(ctx)
	return apiMeta{
		RequestID:  m.RequestID,
		TraceID:    m.TraceID,
		ServerTime: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

func writeEnvelope(w http.ResponseWriter, status int, body envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// respondOK 返回 200 与数据体。
func respondOK(w http.ResponseWriter, r *http.Request, data any) {
	writeEnvelope(w, http.StatusOK, envelope{Success: true, Data: data, Meta: metaFor(r.Context())})
}

// respondList 返回带 total / next_cursor 的列表响应。
func respondList(w http.ResponseWriter, r *http.Request, data any, total int, nextCursor *string) {
	meta := metaFor(r.Context())
	meta.NextCursor = nextCursor
	if total >= 0 {
		meta.Total = &total
	}
	writeEnvelope(w, http.StatusOK, envelope{Success: true, Data: data, Meta: meta})
}

// respondAccepted 返回 202：请求已被接受但尚未完成（§21.3 的 202 语义；
// §21.4「需要 MFA 时返回 202」）。调用方不得把 202 当作登录成功。
func respondAccepted(w http.ResponseWriter, r *http.Request, data any) {
	writeEnvelope(w, http.StatusAccepted, envelope{Success: true, Data: data, Meta: metaFor(r.Context())})
}

func respondCreated(w http.ResponseWriter, r *http.Request, data any) {
	writeEnvelope(w, http.StatusCreated, envelope{Success: true, Data: data, Meta: metaFor(r.Context())})
}

func respondNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// respondError 返回稳定错误码。message 面向用户，不得包含 SQL/堆栈/密钥/Provider 原文。
func respondError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	writeEnvelope(w, status, envelope{
		Success: false,
		Meta:    metaFor(r.Context()),
		Error: &apiError{
			Code:        code,
			Message:     message,
			FieldErrors: []fieldError{},
			Retryable:   status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable,
			Details:     details,
		},
	})
}

// writeErrorCode 供中间件使用。
// [FIX 2026-09-13 / trace 契约] 必须从请求上下文取 meta，否则错误响应的
// meta.request_id / meta.trace_id 恒为空字符串（而响应头有值），§21.3/§27 的 trace 还原无法成立。
func writeErrorCode(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	writeEnvelope(w, status, envelope{
		Success: false,
		Meta:    metaFor(r.Context()),
		Error: &apiError{
			Code:        code,
			Message:     message,
			FieldErrors: []fieldError{},
			Retryable:   status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable,
			Details:     details,
		},
	})
}

// validationError 便捷封装 400。
func validationError(w http.ResponseWriter, r *http.Request, message string, fields []fieldError) {
	if fields == nil {
		fields = []fieldError{}
	}
	writeEnvelope(w, http.StatusBadRequest, envelope{
		Success: false,
		Meta:    metaFor(r.Context()),
		Error:   &apiError{Code: "VALIDATION_INVALID_ARGUMENT", Message: message, FieldErrors: fields},
	})
}
