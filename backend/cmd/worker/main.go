// Worker 进程：Scheduler + Downloader + Ingestion。
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/countoracle"
	"github.com/blockchain-platform/blockchain-platform/internal/download"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/logger"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/chainbase"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/mockparallel"
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
	sched := download.NewScheduler(cfg, infra.Store, infra.CH, infra.Prov, infra.Raw, writer, infra.Bus, log)
	// Shard 取数按创建时固化的路由选 Provider/通道（开发文档 §9.1）。
	if infra.Routes != nil {
		sched.SetRoutes(infra.Routes)
	}

	// V1.2→V1.3：Count Oracle Provider Port（§2/§75/§84：Chainbase 只是 Adapter；
	// MockParallel 只用于验收/测试环境，生产默认关闭）
	usageHook := app.UsageRecorder(infra.Store, log)
	svc := countoracle.NewService(cfg, infra.Store, log)
	svc.SetDeployment(context.Background(), infra.Store)
	registerChainbase := cfg.Chainbase.Enabled && cfg.CountProvider.ChainbaseEnabled
	registerMock := cfg.CountProvider.MockParallelEnabled
	if registerChainbase || registerMock {
		if registerChainbase {
			cbClient := chainbase.New(chainbase.Options{
				Endpoint: cfg.Chainbase.Endpoint, APIKey: cfg.Chainbase.APIKey,
				CountTimeout: cfg.Chainbase.CountTimeout, MaxRetries: cfg.Chainbase.MaxRetries,
				RequestsPerMinute:      cfg.Chainbase.RequestsPerMinute,
				ResponseHardLimitBytes: cfg.Chainbase.ResponseHardLimitBytes,
				ConnectTimeout:         cfg.Chainbase.ConnectTimeout,
				TLSHandshakeTimeout:    cfg.Chainbase.TLSHandshakeTimeout,
				BodyIdleTimeout:        cfg.Chainbase.BodyIdleTimeout,
				CreditsPerRequest:      cfg.Chainbase.CreditsPerRequest,
				CostPerRequestUSD:      cfg.Chainbase.CostPerRequestUSD,
			}, usageHook)
			cbAdapter := chainbase.NewAdapter(chainbase.AdapterOptions{
				Client:       cbClient,
				TXEnabled:    cfg.CountProvider.ChainbaseTXEnabled,
				TokenEnabled: cfg.CountProvider.ChainbaseTokenEnabled,
				ProbeBudget:  cfg.Chainbase.ProbeMaxRequests,
				DisplayName:  cfg.CountProvider.ChainbaseDisplayName,
			})
			if err := svc.Register(cbAdapter); err != nil {
				log.Error("register chainbase count provider failed", "err", err)
				os.Exit(1)
			}
		}
		if registerMock {
			mp := mockparallel.New(mockparallel.Options{
				MaxConcurrency: cfg.CountProvider.MockParallelMaxConcurrency,
				Latency:        time.Duration(cfg.CountProvider.MockParallelLatencyMS) * time.Millisecond,
			})
			if err := svc.Register(mp); err != nil {
				log.Error("register mockparallel count provider failed", "err", err)
				os.Exit(1)
			}
		}
		sched.SetCountOracle(svc)
		log.Info("count oracle port constructed", "enabled", svc.Enabled(),
			"aware", svc.Aware(), "configured", svc.Configured(),
			"chainbase", registerChainbase, "mockparallel", registerMock,
			"token_enabled", cfg.CountProvider.ChainbaseTokenEnabled)
	}

	// 先同步占用指标端口。端口冲突通常意味着重复 Worker，必须在参与调度前失败退出。
	metricsAddr := ":" + strconv.Itoa(cfg.WorkerMetricsPort)
	listener, err := net.Listen("tcp", metricsAddr)
	if err != nil {
		log.Error("worker metrics bind failed; another worker may already be running", "addr", metricsAddr, "err", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	// Runtime profiles are exposed only on the worker-local metrics listener;
	// this keeps heap/alloc evidence off the public API surface.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/pprof/goroutine", pprof.Handler("goroutine").ServeHTTP)
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
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
	infra.StartRPCBackground(runCtx, cfg, log, true)
	sched.SetTokenMetadata(infra.TokenMeta)
	infra.StartTokenMetadataBackground(runCtx, cfg, log, true)
	// §28.5 S3-BE-02：审计链定时校验 + 每日锚点归档 + Outbox 积压指标。
	// （Outbox 投递继续由 API 进程负责，避免同一职责出现在两个进程里。）
	infra.StartAuditBackground(runCtx, cfg.Env, log)
	if svc.Enabled() {
		go svc.Run(runCtx)
	}

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
	if infra.RPCBatcher != nil {
		infra.RPCBatcher.Close()
	}
	infra.Store.Close()
	_ = infra.CH.Close()
	infra.Bus.Close()
	log.Info("worker stopped")
}
