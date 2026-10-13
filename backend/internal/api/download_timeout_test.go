package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDownloadStreamKeepsClientCancellationWithoutOrdinaryDeadline(t *testing.T) {
	path := "/api/v2/jobs/11111111-1111-4111-8111-111111111111/events"
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	started, done := make(chan struct{}), make(chan struct{})
	handler := downloadRequestTimeout(15 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, bounded := r.Context().Deadline(); bounded {
			t.Error("authenticated progress stream must not inherit the ordinary API deadline")
		}
		close(started)
		<-r.Context().Done()
		close(done)
	}))
	go handler.ServeHTTP(rec, req)
	<-started
	select {
	case <-done:
		t.Fatal("progress stream closed on ordinary timeout")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not close progress stream")
	}
}

func TestOrdinaryAPIAndInvalidStreamPathsRemainBounded(t *testing.T) {
	for _, path := range []string{
		"/api/v2/jobs/11111111-1111-4111-8111-111111111111",
		"/api/v2/downloads",
		"/api/v2/jobs/not-a-uuid/events",
		"/api/v2/jobs/11111111-1111-4111-8111-111111111111/events/extra",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler := downloadRequestTimeout(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, bounded := r.Context().Deadline(); !bounded {
					t.Fatal("ordinary request lost its deadline")
				}
				<-r.Context().Done()
			}))
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusGatewayTimeout {
				t.Fatalf("normal request timeout returned %d", rec.Code)
			}
		})
	}
}
