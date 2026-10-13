package ankr

// Response Guard 故障注入测试（文档 §24/§25/§66/§93）：
// Oversize → ErrResponseTooLarge；截断 → ErrBodyTruncated；Body Idle → ErrBodyIdleTimeout；
// 只有完整 body 自身 JSON 非法才是 invalid_json。

import (
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func newGuardClient(t *testing.T, hardMB int64, softMB int64, bodyIdle time.Duration) *Client {
	t.Helper()
	c := New(Options{
		Endpoint:               "http://unused",
		APIKey:                 "k",
		MaxPageSize:            10000,
		ResponseHardLimitBytes: hardMB << 20,
		ResponseSoftLimitBytes: softMB << 20,
		BodyIdleTimeout:        bodyIdle,
		RequestTimeout:         30 * time.Second,
	}, nil)
	return c
}

func jsonRPCBody(rows string) []byte {
	return []byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"transactions\":" + rows + "}}")
}

func TestBodyDeadlineAndCancellationPreserveContextClass(t *testing.T) {
	for _, mode := range []string{"context_deadline", "client_deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "100000")
				_, _ = w.Write([]byte(`{"result":{"transactions":[`))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer srv.Close()
			c := newGuardClient(t, 1, 0, time.Minute)
			c.endpoint = srv.URL
			ctx := context.Background()
			cancel := func() {}
			if mode == "context_deadline" {
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
			} else if mode == "client_deadline" {
				c.http.Timeout = 100 * time.Millisecond
			} else {
				ctx, cancel = context.WithCancel(ctx)
				time.AfterFunc(100*time.Millisecond, cancel)
			}
			defer cancel()
			_, err := c.Fetch(ctx, provider.FetchRequest{Chain: "bsc", Dataset: "transactions", PageSize: 2000})
			pe, ok := err.(*provider.Error)
			if !ok {
				t.Fatalf("expected classified body error, got %v", err)
			}
			if mode == "cancel" {
				if pe.Class != provider.ClassCancelled || pe.Retryable {
					t.Fatalf("cancel must stop retries: %#v", pe)
				}
			} else if pe.Class != provider.ClassRequestTimeout || !pe.ReducePageSize {
				t.Fatalf("body deadline must downgrade page size: %#v", pe)
			}
		})
	}
}

func TestUsageDurationIncludesBodyTransfer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write(jsonRPCBody(`[{"blockNumber":"0x1"}]`))
	}))
	defer srv.Close()
	c := newGuardClient(t, 1, 0, time.Minute)
	c.endpoint = srv.URL
	var durationMs, bodyMs int
	c.usageHook = func(u model.UsageRow) { durationMs, bodyMs = u.DurationMs, u.BodyMs }
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", PageSize: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if durationMs < 90 || durationMs < bodyMs {
		t.Fatalf("duration %dms must include body %dms", durationMs, bodyMs)
	}
}

func TestLargeCompleteGzipWithIncompleteJSONIsTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte(`{"jsonrpc":"2.0","result":{"transactions":[{"blockNumber":"0x1","input":"` + strings.Repeat("x", 1<<20) + `"}`))
		_ = gz.Close()
	}))
	defer srv.Close()
	c := newGuardClient(t, 2, 0, time.Minute)
	c.endpoint = srv.URL
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", PageSize: 2000})
	pe, ok := err.(*provider.Error)
	if !ok || pe.Class != provider.ClassBodyTruncated || !pe.Retryable {
		t.Fatalf("incomplete document is a truncated response: %v", err)
	}
}

func TestResponseTooLargeNotInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2MB 完整合法 JSON（超过 hard=1MB）→ 必须 ErrResponseTooLarge，绝不进入 JSON Parser
		w.Header().Set("Content-Type", "application/json")
		big := strings.Repeat("x", 2<<20)
		_, _ = w.Write([]byte("{\"result\":{\"a\":"))
		_, _ = w.Write([]byte(big))
		_, _ = w.Write([]byte("}}"))
	}))
	defer srv.Close()
	c := newGuardClient(t, 1, 0, time.Minute)
	c.endpoint = srv.URL
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Dataset: "transactions", PageSize: 10000, Chain: "bsc"})
	if err == nil {
		t.Fatal("expected error")
	}
	pe, ok := err.(*provider.Error)
	if !ok {
		t.Fatalf("expected provider.Error, got %T", err)
	}
	if pe.Class != provider.ClassResponseTooLarge {
		t.Fatalf("expected response_too_large, got %s (%s)", pe.Class, pe.Message)
	}
	if strings.Contains(pe.Message, "unexpected end of JSON") || pe.Class == provider.ClassInvalidJSON {
		t.Fatalf("oversized body must never be reported as invalid JSON: %s", pe.Message)
	}
}

func TestTruncatedBodyNotInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "100000") // 承诺 100KB
		_, _ = w.Write(jsonRPCBody("[{\"a\":"))    // 只写几个字节后断开
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 通过 Hijack 断开连接，模拟 connection reset
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
				return
			}
		}
	}))
	defer srv.Close()
	c := newGuardClient(t, 64, 32, time.Minute)
	c.endpoint = srv.URL
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Dataset: "transactions", PageSize: 10000, Chain: "bsc"})
	if err == nil {
		t.Fatal("expected error")
	}
	pe, ok := err.(*provider.Error)
	if !ok {
		t.Fatalf("expected provider.Error, got %T", err)
	}
	if pe.Class != provider.ClassBodyTruncated {
		t.Fatalf("expected body_truncated, got %s (%s)", pe.Class, pe.Message)
	}
}

func TestBodyIdleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jsonRPCBody("[{\"a\":1},"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(5 * time.Second) // 超过 bodyIdle=1s，且连接保持打开
		_, _ = w.Write(jsonRPCBody("{\"a\":2}]"))
	}))
	defer srv.Close()
	c := newGuardClient(t, 64, 32, 1*time.Second)
	c.endpoint = srv.URL
	start := time.Now()
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Dataset: "transactions", PageSize: 10000, Chain: "bsc"})
	if err == nil {
		t.Fatal("expected error")
	}
	pe, ok := err.(*provider.Error)
	if !ok {
		t.Fatalf("expected provider.Error, got %T", err)
	}
	if pe.Class != provider.ClassBodyIdleTimeout {
		t.Fatalf("expected body_idle_timeout, got %s (%s)", pe.Class, pe.Message)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("body idle timeout must fire early (took %s)", time.Since(start))
	}
}

func TestWireDecodedBytesSplit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		rows := "["
		for i := 0; i < 10000; i++ {
			if i > 0 {
				rows += ","
			}
			rows += "{\"blockNumber\":\"0x1\",\"txHash\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"}"
		}
		rows += "]"
		_, _ = gz.Write(jsonRPCBody(rows))
		_ = gz.Close()
	}))
	defer srv.Close()
	c := newGuardClient(t, 64, 32, time.Minute)
	c.endpoint = srv.URL
	res, err := c.Fetch(context.Background(), provider.FetchRequest{Dataset: "transactions", PageSize: 10000, Chain: "bsc"})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if res.WireBytes <= 0 || res.DecodedBytes <= 0 {
		t.Fatalf("wire/decoded must both be recorded: wire=%d decoded=%d", res.WireBytes, res.DecodedBytes)
	}
	if res.WireBytes >= res.DecodedBytes {
		t.Fatalf("gzip wire must be smaller than decoded: wire=%d decoded=%d", res.WireBytes, res.DecodedBytes)
	}
	if len(res.Rows) != 10000 {
		t.Fatalf("expected 10000 rows, got %d", len(res.Rows))
	}
}

func TestInvalidJSONStillClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("this is not json at all"))
	}))
	defer srv.Close()
	c := newGuardClient(t, 64, 32, time.Minute)
	c.endpoint = srv.URL
	_, err := c.Fetch(context.Background(), provider.FetchRequest{Dataset: "transactions", PageSize: 10000, Chain: "bsc"})
	if err == nil {
		t.Fatal("expected error")
	}
	pe, ok := err.(*provider.Error)
	if !ok {
		t.Fatalf("expected provider.Error, got %T", err)
	}
	if pe.Class != provider.ClassInvalidJSON {
		t.Fatalf("complete-but-invalid body must be invalid_json, got %s", pe.Class)
	}
}
