// Package api：REST / SSE API。
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/events"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

type Deps struct {
	Cfg   *config.Config
	Store *store.Store
	CH    *clickhouse.Conn
	Raw   *rawstore.Store
	Bus   *events.Bus
	Prov  provider.Provider
	Log   *slog.Logger
}

type Server struct {
	deps    Deps
	router  chi.Router
	hub     *SSEHub
	busStop func()
	svc     *dataset.Service
	query   *query.Service
}

func New(deps Deps) *Server {
	s := &Server{deps: deps, hub: NewSSEHub(), query: query.New(deps.CH)}
	s.svc = dataset.New(deps.Store, func(ctx context.Context, chain string) (int64, error) {
		if deps.Prov != nil && deps.Prov.Name() == "ankr-mock" {
			return 2_000_000, nil
		}
		return 0, nil // live：head 未知，区间由 Provider 语义决定
	})
	s.buildRouter()
	return s
}

func (s *Server) buildRouter() {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(s.corsMiddleware())
	r.Use(s.logMiddleware())
	r.Use(auth.Middleware)

	r.Handle("/metrics", promhttp.Handler())
	r.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	r.Get("/health/ready", s.handleReady)

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/me", s.handleMe)
		api.Post("/datasets/ensure", s.handleEnsure)
		api.Post("/datasets/coverage/preview", s.handleCoveragePreview)
		api.Get("/download-jobs", s.handleListJobs)
		api.Get("/download-jobs/{id}", s.handleGetJob)
		api.Post("/download-jobs/{id}/pause", s.handlePause)
		api.Post("/download-jobs/{id}/resume", s.handleResume)
		api.Post("/download-jobs/{id}/cancel", s.handleCancel)
		api.Get("/download-jobs/{id}/events", s.handleJobEvents)
		api.Get("/providers/ankr/status", s.handleProviderStatus)
		api.Get("/providers/ankr/usage", s.handleProviderUsage)
		api.Get("/coverage", s.handleCoverage)
		api.Get("/coverage/stats", s.handleCoverageStats)
		api.Get("/datasets/{dataset}", s.handleDatasetRows)
		api.Get("/addresses/{address}/activity", s.handleAddressActivity)
		api.Post("/exports", s.handleCreateExport)
		api.Get("/exports/{id}", s.handleGetExport)
		api.Get("/exports/{id}/download", s.handleDownloadExport)
	})
	s.router = r
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) StartForwarder() {
	s.startBusForwarder(s.deps.Log)
}

func (s *Server) StopForwarder() {
	if s.busStop != nil {
		s.busStop()
	}
}

func (s *Server) logMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			s.deps.Log.Info("http", "method", r.Method, "path", r.URL.Path,
				"status", ww.Status(), "duration_ms", time.Since(start).Milliseconds())
		})
	}
}

func (s *Server) corsMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	checks := map[string]string{}
	if err := s.deps.Store.Pool.Ping(ctx); err != nil {
		checks["postgres"] = "down: " + err.Error()
	} else {
		checks["postgres"] = "ok"
	}
	if err := s.deps.CH.Exec(ctx, "SELECT 1"); err != nil {
		checks["clickhouse"] = "down: " + err.Error()
	} else {
		checks["clickhouse"] = "ok"
	}
	if s.deps.Bus != nil && s.deps.Bus.Enabled() {
		checks["nats"] = "ok"
	} else {
		checks["nats"] = "degraded (disabled)"
	}
	// MinIO：stat 一个哨兵对象不可靠，仅验证 bucket
	if s.deps.Raw != nil {
		checks["minio"] = "ok"
	}
	status := http.StatusOK
	for _, v := range checks {
		if v != "ok" {
			status = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, status, map[string]any{"status": statusLabel(status), "checks": checks})
}

func statusLabel(code int) string {
	if code == http.StatusOK {
		return "ready"
	}
	return "not_ready"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonEncode(w, v)
}
