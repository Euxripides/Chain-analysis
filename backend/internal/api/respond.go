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

// writeErrorCode 供中间件使用（无 *http.Request 之外的上下文需求）。
func writeErrorCode(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeEnvelope(w, status, envelope{
		Success: false,
		Meta:    apiMeta{ServerTime: time.Now().UTC().Format("2006-01-02T15:04:05.000Z")},
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
		Error: &apiError{Code: "VALIDATION_INVALID_ARGUMENT", Message: message, FieldErrors: fields},
	})
}
