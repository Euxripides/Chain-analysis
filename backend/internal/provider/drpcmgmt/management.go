package drpcmgmt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/secret"
)

// managementError 是管理接口调用失败（HTTP/网络）的归类错误。
// 错误信息只含状态码与分类：响应体可能很长，也绝不允许把 Authorization 带进日志。
type managementError struct {
	class  string
	status int
	err    error
}

func (e *managementError) Error() string {
	if e.status > 0 {
		return fmt.Sprintf("drpc management request failed: HTTP %d (%s)", e.status, e.class)
	}
	return fmt.Sprintf("drpc management request failed: %s", e.class)
}

func (e *managementError) Unwrap() error { return e.err }

// classForStatus 把 HTTP 状态码映射成 SyncErrorClass。
func classForStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrClassUnauthorized
	case status >= 500:
		return ErrClassServerError
	default:
		return ErrClassClientError
	}
}

// errorClass 从错误里取分类，取不到按网络错误归类（fail closed：不会静默当成成功）。
func errorClass(err error) string {
	var managementErr *managementError
	if errors.As(err, &managementErr) && managementErr.class != "" {
		return managementErr.class
	}
	return ErrClassNetwork
}

// resolveToken 解析管理 Token。明文只在构造请求头时使用，不进日志、不进错误信息。
func (s *Service) resolveToken(ctx context.Context, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("secret ref is empty: %w", secret.ErrNotConfigured)
	}
	if s.opts.Secrets == nil {
		return "", fmt.Errorf("secret resolver is not configured: %w", secret.ErrNotConfigured)
	}
	value, err := s.opts.Secrets.Resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("secret %q is empty: %w", ref, secret.ErrNotConfigured)
	}
	return value, nil
}

// waitRequestSlot 保证两次管理调用之间至少间隔 minRequestSpacing。
// 调用方（syncOnce）本身串行，因此这里不需要复杂队列；它只是把"连续两次调用"
// 拉开到官方限额（10 请求/分钟）之内。
func (s *Service) waitRequestSlot(ctx context.Context) error {
	s.reqMu.Lock()
	wait := time.Duration(0)
	if !s.lastRequestAt.IsZero() {
		if elapsed := s.now().Sub(s.lastRequestAt); elapsed < minRequestSpacing {
			wait = minRequestSpacing - elapsed
		}
	}
	if wait <= 0 {
		s.lastRequestAt = s.now()
		s.reqMu.Unlock()
		return nil
	}
	s.reqMu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}

	s.reqMu.Lock()
	s.lastRequestAt = s.now()
	s.reqMu.Unlock()
	return nil
}

// getJSON 发起一次带 Bearer 的 GET，返回原始响应体（json.RawMessage）。
// 超时用 Options.HTTPTimeout；响应体有上限；非 2xx 归类成 managementError，不回显响应体。
func (s *Service) getJSON(ctx context.Context, path string, query url.Values, token string) (json.RawMessage, error) {
	if err := s.waitRequestSlot(ctx); err != nil {
		return nil, err
	}
	endpoint := s.opts.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	callCtx, cancel := context.WithTimeout(ctx, s.opts.HTTPTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &managementError{class: ErrClassClientError, err: err}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")

	response, err := s.client.Do(request)
	if err != nil {
		return nil, &managementError{class: ErrClassNetwork, err: err}
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, &managementError{class: ErrClassNetwork, err: err}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, &managementError{
			class:  classForStatus(response.StatusCode),
			status: response.StatusCode,
			err:    fmt.Errorf("unexpected status %d", response.StatusCode),
		}
	}
	return json.RawMessage(body), nil
}

// metricsFetch 是一次 Statistics API 调用的结果。
type metricsFetch struct {
	rangeName string
	totals    *metricsTotals
	missing   []string // 合法 JSON 但找不到的字段
	hash      string
	class     string // 非空表示本次调用失败
}

func (s *Service) fetchMetrics(ctx context.Context, token, rangeName string) metricsFetch {
	body, err := s.getJSON(ctx, StatisticsPath, url.Values{"range": {rangeName}}, token)
	if err != nil {
		return metricsFetch{rangeName: rangeName, class: errorClass(err)}
	}
	totals, missing, parseErr := parseMetrics(body)
	if parseErr != nil {
		// 响应根本不是 JSON：留零值并标注 invalid_json，绝不猜内容。
		return metricsFetch{rangeName: rangeName, hash: sha256Hex(body), class: ErrClassInvalidJSON}
	}
	return metricsFetch{rangeName: rangeName, totals: &totals, missing: missing, hash: sha256Hex(body)}
}

// keysFetch 是一次 Keys API 调用的结果。
type keysFetch struct {
	keys  []teamKey
	hash  string
	class string
}

func (s *Service) fetchKeys(ctx context.Context, token string) keysFetch {
	body, err := s.getJSON(ctx, KeysPath, nil, token)
	if err != nil {
		return keysFetch{class: errorClass(err)}
	}
	keys, parseErr := parseTeamKeys(body)
	if parseErr != nil {
		return keysFetch{hash: sha256Hex(body), class: ErrClassInvalidJSON}
	}
	return keysFetch{keys: keys, hash: sha256Hex(body)}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
