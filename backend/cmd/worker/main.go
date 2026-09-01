// Worker 进程：Scheduler + Downloader + Ingestion。
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/download"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/logger"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)

	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		log.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}

	writer := ingest.NewWriter(infra.CH, 5000)
	sched := download.NewScheduler(cfg, infra.Store, infra.Prov, infra.Raw, writer, infra.Bus, log)

	// 先同步占用指标端口。端口冲突通常意味着重复 Worker，必须在参与调度前失败退出。
	metricsAddr := ":" + strconv.Itoa(cfg.WorkerMetricsPort)
	listener, err := net.Listen("tcp", metricsAddr)
	if err != nil {
		log.Error("worker metrics bind failed; another worker may already be running", "addr", metricsAddr, "err", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	metricsServer := &http.Server{Addr: metricsAddr, Handler: mux}
	go func() {
		log.Info("worker metrics listening", "addr", metricsAddr)
		if err := metricsServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("worker metrics server failed", "err", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	runCtx, cancel := context.WithCancel(ctx)

	// V1.3：Method 解析栈（Fast Resolver + 异步补全 + Backfill，§16-§19/§35）
	sched.SetMethodResolver(infra.Resolver, infra.Registry)
	infra.StartMethodBackground(runCtx, cfg, log, true)

	errCh := make(chan error, 1)
	go func() { errCh <- sched.Run(runCtx) }()

	schedulerStopped := false
	select {
	case <-stop:
		log.Info("worker stopping")
	case err := <-errCh:
		schedulerStopped = true
		if err != nil {
			log.Error("worker stopped with error", "err", err)
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = metricsServer.Shutdown(shutdownCtx)
	shutdownCancel()
	if !schedulerStopped {
		select {
		case err := <-errCh:
			if err != nil {
				log.Error("worker stopped with error", "err", err)
			}
		case <-time.After(15 * time.Second):
			log.Warn("worker shutdown timed out with in-flight shards")
		}
	}
	infra.Store.Close()
	_ = infra.CH.Close()
	infra.Bus.Close()
	log.Info("worker stopped")
}
