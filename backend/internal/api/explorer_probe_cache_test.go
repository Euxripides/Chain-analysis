package api

import (
	"context"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExplorerProbeCacheExpiryAndBound(t *testing.T) {
	var c explorerProbeCache
	c.put("bsc/address", false)
	if found, ok := c.get("bsc/address"); !ok || found {
		t.Fatal("negative probe not cached")
	}
	c.entries["bsc/address"] = explorerProbeEntry{Found: true, Until: time.Now().Add(-time.Second)}
	if _, ok := c.get("bsc/address"); ok {
		t.Fatal("expired result reused")
	}
	for i := 0; i < 1100; i++ {
		c.put(time.Unix(int64(i), 0).String(), true)
	}
	if len(c.entries) > 1024 {
		t.Fatal("unbounded probe cache")
	}
}
func TestExplorerLookupUpstreamErrorsAreNotNotFound(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{context.DeadlineExceeded, 504}, {&rpc.Error{Class: rpc.ClassRateLimit, RetryAfter: 3 * time.Second}, 429}, {&rpc.Error{Class: rpc.ClassBudgetExceeded}, 429}, {&rpc.Error{Class: rpc.ClassUpstream}, 503},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		respondLookupFailure(rec, req, tc.err, "RPC")
		if rec.Code != tc.status {
			t.Fatalf("got %d want %d", rec.Code, tc.status)
		}
	}
}
