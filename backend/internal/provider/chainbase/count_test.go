// V1.2 Chainbase Count Oracle 单元测试：计数语义 / 探测兜底 / 错误分类。
package chainbase

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockCountServer 返回预置响应（计数语义可配）。
type mockCountServer struct {
	mu map[string]string // page -> body
}

func (m *mockCountServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	page := r.URL.Query().Get("page")
	if body, ok := m.mu[page]; ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func pageBody(items, count, nextPage int) string {
	rows := "[]"
	if items == 1 {
		rows = "[{\"block_number\":123,\"transaction_hash\":\"0xabc\"}]"
	}
	return fmt.Sprintf("{\"code\":0,\"message\":\"ok\",\"data\":%s,\"count\":%d,\"next_page\":%d}", rows, count, nextPage)
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	_ = t
	return New(Options{
		Endpoint: srv.URL, APIKey: "test-key",
		CountTimeout:           5e9,
		RequestsPerMinute:      10000,
		ResponseHardLimitBytes: 4 << 20,
		ConnectTimeout:         2e9,
		TLSHandshakeTimeout:    2e9,
		BodyIdleTimeout:        5e9,
	}, nil)
}

// 1) count=总行数语义（§13：page=1&limit=1 → count 即总量）
func TestCountSemanticsCountIsTotal(t *testing.T) {
	ms := &mockCountServer{mu: map[string]string{
		"1": "{\"code\":0,\"message\":\"ok\",\"data\":[{}],\"count\":140000,\"next_page\":2}",
	}}
	srv := httptest.NewServer(ms)
	defer srv.Close()
	c := newTestClient(t, srv)
	raw, reqs, err := c.Count(context.Background(), 56, "token_transfers", "0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28", 0, 119720000, 22)
	if err != nil {
		t.Fatalf("count err: %v", err)
	}
	if raw != 140000 {
		t.Fatalf("expected total 140000 got %d", raw)
	}
	if reqs != 1 {
		t.Fatalf("expected single request, got %d", reqs)
	}
}

// 2) count 语义不明确（count=1=页大小）→ 二分探测（探测到最后一个有数据的页）
func TestCountSemanticsProbeFallback(t *testing.T) {
	// 总共有 5 行（limit=1 → 5 页）；page>=6 为空
	mu := map[string]string{}
	for p := 1; p <= 5; p++ {
		mu[fmt.Sprintf("%d", p)] = pageBody(1, 1, p+1)
	}
	mu["6"] = pageBody(0, 0, 6)
	mu["7"] = pageBody(0, 0, 7)
	mu["8"] = pageBody(0, 0, 8)
	ms := &mockCountServer{mu: mu}
	srv := httptest.NewServer(ms)
	defer srv.Close()
	c := newTestClient(t, srv)
	raw, reqs, err := c.Count(context.Background(), 1, "transactions", "0x1111111111111111111111111111111111111111", 0, 0, 22)
	if err != nil {
		t.Fatalf("count err: %v", err)
	}
	if raw != 5 {
		t.Fatalf("expected probed total 5 got %d", raw)
	}
	if reqs > 12 {
		t.Fatalf("probe requests too many: %d", reqs)
	}
}

// 3) 空数据 → 0
func TestCountZero(t *testing.T) {
	ms := &mockCountServer{mu: map[string]string{"1": pageBody(0, 0, 1)}}
	srv := httptest.NewServer(ms)
	defer srv.Close()
	c := newTestClient(t, srv)
	raw, _, err := c.Count(context.Background(), 56, "token_transfers", "0xdead000000000000000000000000000000000000", 0, 119720000, 22)
	if err != nil {
		t.Fatalf("count err: %v", err)
	}
	if raw != 0 {
		t.Fatalf("expected 0 got %d", raw)
	}
}

// 4) HTTP 200 但业务错误（配额耗尽）→ 分类 Fail Open
func TestCountErrorEnvelope(t *testing.T) {
	ms := &mockCountServer{mu: map[string]string{"1": "{\"code\":40101,\"message\":\"quota exhausted\",\"data\":[],\"count\":0,\"next_page\":1}"}}
	srv := httptest.NewServer(ms)
	defer srv.Close()
	c := newTestClient(t, srv)
	_, _, err := c.Count(context.Background(), 1, "transactions", "0x2222222222222222222222222222222222222222", 0, 0, 22)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "quota") {
		t.Fatalf("expected quota error, got %v", err)
	}
}

// 5) HTTP 429 → rate_limit 分类
func TestCountHTTP429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("{\"error\":\"too many requests\"}"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, _, err := c.Count(context.Background(), 1, "transactions", "0x3333333333333333333333333333333333333333", 0, 0, 22)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "rate") {
		t.Fatalf("expected rate error, got %v", err)
	}
}

// 6) no key → immediate fail-open error
func TestCountNoKey(t *testing.T) {
	c := New(Options{Endpoint: "http://127.0.0.1:1", APIKey: ""}, nil)
	_, _, err := c.Count(context.Background(), 1, "transactions", "0x4444444444444444444444444444444444444444", 0, 0, 22)
	if err == nil || !strings.Contains(err.Error(), "api key") {
		t.Fatalf("expected api key error, got %v", err)
	}
}

// 7) JSON 解码宽容：code 为字符串 "0"
func TestCountStringCode(t *testing.T) {
	ms := &mockCountServer{mu: map[string]string{"1": "{\"code\":\"0\",\"message\":\"ok\",\"data\":[{\"x\":1}],\"count\":42,\"next_page\":2}"}}
	srv := httptest.NewServer(ms)
	defer srv.Close()
	c := newTestClient(t, srv)
	raw, _, err := c.Count(context.Background(), 1, "transactions", "0x5555555555555555555555555555555555555555", 0, 0, 22)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if raw != 42 {
		t.Fatalf("expected 42 got %d", raw)
	}
}

var _ = json.RawMessage{}
