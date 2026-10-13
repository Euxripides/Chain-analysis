package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// Job progress streams stay open until the client or live session closes them.
// Their existing per-frame and periodic scope checks remain in the handler.
// Ordinary API requests keep the normal request deadline.
func downloadRequestTimeout(duration time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		bounded := middleware.Timeout(duration)(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
			if r.Method == http.MethodGet && len(parts) == 5 && parts[0] == "api" && parts[1] == "v2" && parts[2] == "jobs" && parts[4] == "events" {
				if _, err := uuid.Parse(parts[3]); err == nil {
					next.ServeHTTP(w, r)
					return
				}
			}
			bounded.ServeHTTP(w, r)
		})
	}
}
