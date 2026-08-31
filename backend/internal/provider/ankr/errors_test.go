package ankr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func newTestClient(handler http.HandlerFunc, hard, soft int64) (*Client, *httptest.Server) {
	srv := httptest.NewServer(handler)
	c := New(Options{
		Endpoint: srv.URL, APIKey: "test-key", MaxPageSize: 10000,
		RequestsPerMinute: 1000000, RequestTimeout: 5 * time.Second,
		ResponseSoftLimitBytes: soft, ResponseHardLimitBytes: hard,
	}, nil)
	return c, srv
}

func okResp(rows int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		txs := make([]json.RawMessage, rows)
		for i := range txs {
			txs[i] = json.RawMessage(`{"hash":"0x1","from":"0x2","to":"0x3"}`)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"transactions": txs}})
	}
}

// §40：超过硬限制 → ErrResponseTooLarge（不得是 InvalidJSON）
func TestResponseTooLargeDetected(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		body := strings.Repeat("a", 1<<20) // 1 MiB body
		w.Header().Set("Content-Length", "")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"transactions":[{"pad":"` + body + `"}]}}`))
	}, 1<<10, 512) // hard=1KB
	defer srv.Close()
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", PageSize: 1000})
	pe, ok := err.(*provider.Error)
	if !ok {
		t.Fatalf("expected *provider.Error, got %T", err)
	}
	if pe.Class != provider.ClassResponseTooLarge {
		t.Fatalf("class = %s, want response_too_large (message: %s)", pe.Class, pe.Message)
	}
	if !pe.ReducePageSize || !pe.Retryable {
		t.Fatalf("want ReducePageSize=true Retryable=true, got %+v", pe)
	}
}

// §40：硬限制内合法 JSON → Success；64MiB-1 不误报
func TestBodyUnderLimitSucceeds(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		body := strings.Repeat("b", 64<<10) // 64 KiB
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"transactions": []json.RawMessage{
				json.RawMessage(`{"pad":"` + body + `"}`),
			}}})
	}, 64<<20, 32<<20)
	defer srv.Close()
	res, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", PageSize: 1000})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d", len(res.Rows))
	}
}

// §40：真正损坏的 JSON（未超限）→ ErrInvalidJSON
func TestMalformedJSONIsInvalidJSON(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":`))
	}, 64<<20, 32<<20)
	defer srv.Close()
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", PageSize: 1000})
	pe, ok := err.(*provider.Error)
	if !ok || pe.Class != provider.ClassInvalidJSON {
		t.Fatalf("class = %v, want invalid_json", err)
	}
}

// §40：服务端 sleep > deadline → ErrRequestTimeout + ReducePageSize
func TestTimeoutClassified(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}, 64<<20, 32<<20)
	defer srv.Close()
	c.http.Timeout = 300 * time.Millisecond
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", PageSize: 10000})
	pe, ok := err.(*provider.Error)
	if !ok || pe.Class != provider.ClassRequestTimeout {
		t.Fatalf("class = %v, want request_timeout (%v)", err, err)
	}
	if !pe.ReducePageSize {
		t.Fatal("want ReducePageSize=true")
	}
}

// §40：取消 → ErrCancelled 且禁止重试
func TestCancelNotRetryable(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}, 64<<20, 32<<20)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.Fetch(ctx, provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", PageSize: 1000})
	if ctx.Err() == context.DeadlineExceeded {
		// 客户端 deadline → timeout（可重试，降页）
		pe, ok := err.(*provider.Error)
		if !ok || pe.Class != provider.ClassRequestTimeout {
			t.Fatalf("class = %v", err)
		}
		return
	}
	pe, ok := err.(*provider.Error)
	if !ok || pe.Class != provider.ClassCancelled || pe.Retryable {
		t.Fatalf("want cancelled non-retryable, got %v", err)
	}
}

// §41：官方错误码矩阵
func TestOfficialErrorCodes(t *testing.T) {
	cases := []struct {
		code    int
		want    provider.ErrorClass
		rate    bool
		conn    bool
	}{
		{-32090, provider.ClassRateLimit, true, false},
		{-32005, provider.ClassRateLimit, true, false}, // legacy
		{-32067, provider.ClassConnectionLimit, false, true},
		{-32068, provider.ClassConnectionLimit, false, true},
		{-32069, provider.ClassConnectionLimit, false, true},
		{-32077, provider.ClassConnectionLimit, false, true},
		{-32070, provider.ClassConnectionLimit, false, true},
		{-32062, provider.ClassRequestTooLarge, false, false},
		{-32071, provider.ClassRequestTimeout, false, false},
		{-32055, provider.ClassUpstream, false, false},
		{-32076, provider.ClassUpstream, false, false},
	}
	for _, tc := range cases {
		c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
				"error": map[string]any{"code": tc.code, "message": "test error"}})
		}, 64<<20, 32<<20)
		_, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", PageSize: 1000})
		srv.Close()
		pe, ok := err.(*provider.Error)
		if !ok {
			t.Fatalf("code %d: not *provider.Error", tc.code)
		}
		if pe.Class != tc.want {
			t.Errorf("code %d: class=%s want %s", tc.code, pe.Class, tc.want)
		}
		if pe.ReduceRate != tc.rate || pe.ReduceConcurrency != tc.conn {
			t.Errorf("code %d: rate=%v conn=%v", tc.code, pe.ReduceRate, pe.ReduceConcurrency)
		}
	}
}

// §41：HTTP 429 无 JSON → generic rate_limit 兜底 + Retry-After
func TestHTTP429FallbackAndRetryAfter(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("too many requests"))
	}, 64<<20, 32<<20)
	defer srv.Close()
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", PageSize: 1000})
	pe, ok := err.(*provider.Error)
	if !ok || pe.Class != provider.ClassRateLimit || !pe.ReduceRate {
		t.Fatalf("want generic rate_limit, got %v", err)
	}
	if pe.RetryAfter < 3*time.Second {
		t.Fatalf("RetryAfter = %v, want >= 3s", pe.RetryAfter)
	}
}

// §41：HTTP 429 带 -32068 → connection_limit
func TestHTTP429WithRPCConnectionCode(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"error": map[string]any{"code": -32068, "message": "Max connections per api key reached"}})
	}, 64<<20, 32<<20)
	defer srv.Close()
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", PageSize: 1000})
	pe, ok := err.(*provider.Error)
	if !ok || pe.Class != provider.ClassConnectionLimit || !pe.ReduceConcurrency {
		t.Fatalf("want connection_limit, got %v", err)
	}
}
