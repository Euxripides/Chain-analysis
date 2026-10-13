// API 进程：REST / SSE / Export / Query。
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/api"
	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/logger"
)

func main() {
	// 导出高峰期的 GC 调优必须发生在任何大批量分配之前；见 app.TuneExportGC 的实测数据。
	app.TuneExportGC()

	ctx := context.Background()
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)

	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		log.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}
	// P1-B safe standby startup. A second API instance started beside the running
	// production process must not mutate anything while it boots. API_STANDBY_STARTUP=1
	// keeps the two things the cross-instance acceptance gate exercises -- the HTTP
	// API and the event-bus forwarder -- and skips the startup export reconciliation
	// plus the three background loops. The switch changes no route, no middleware,
	// no handler and no readiness check; that is exactly what the standby negative
	// calibration asserts. API_STARTUP_FAIL_INTERRUPTED_EXPORTS=0 disables only the
	// export reconciliation, for a normal instance started while exports are quiet.
	standby := os.Getenv("API_STANDBY_STARTUP") == "1"
	switch {
	case standby:
		log.Warn("standby startup: skipping interrupted-export reconciliation and background loops")
	case os.Getenv("API_STARTUP_FAIL_INTERRUPTED_EXPORTS") == "0":
		log.Warn("interrupted-export reconciliation disabled by API_STARTUP_FAIL_INTERRUPTED_EXPORTS=0")
	default:
		if recovered, err := infra.Store.FailInterruptedExports(ctx); err != nil {
			log.Error("failed to reconcile interrupted exports", "err", err)
		} else if recovered > 0 {
			log.Warn("reconciled interrupted exports", "count", recovered)
		}
	}

	// V1.3：API 侧只需要 Method 索引刷新（异步补全与回填在 Worker 进程执行）
	runCtx, cancelBackground := context.WithCancel(ctx)
	defer cancelBackground()
	if !standby {
		infra.StartMethodBackground(runCtx, cfg, log, false)
		infra.StartRPCBackground(runCtx, cfg, log, false)
		infra.StartOutboxBackground(runCtx, log)
	}

	srv := api.New(api.Deps{
		Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Bus: infra.Bus, Prov: infra.Prov, ProviderControl: infra.ProviderControl, Log: log,
		Registry: infra.Registry, MethodData: infra.MethodData, DeepDecode: infra.DeepDecode,
		TokenMeta: infra.TokenMeta, TokenBackfill: infra.TokenBackfill,
		Head: infra.Head, AddressType: infra.AddressType, BalanceRead: infra.BalanceRead,
		ChainRead: infra.ChainRead, Trace: infra.Trace, RPCGateway: infra.RPCGateway,
		Capabilities: infra.Capabilities, Investigations: infra.Investigations, Canvases: infra.Canvases,
		// 工作流 E：dRPC 后台管理（运行时路由注册表 / 分类密钥 / 管理同步 / dRPC 客户端）。
		Routes: infra.Routes, Secrets: infra.Secrets, DRPCManagement: infra.DRPCManagement, DRPC: infra.DRPC,
	})
	srv.StartForwarder()
	// 预生成产物的过期清理（投机产物，过一小时没人认领就连对象一起删）。
	srv.StartPrefetchGC(runCtx)
	if !standby {
		srv.StartProviderCenterSync(runCtx)
	}
	if !standby || os.Getenv("INVESTIGATION_WORKER_ENABLE") == "1" {
		srv.StartInvestigationWorkers(runCtx)
	}

	server := &http.Server{
		Addr:              ":" + itoa(cfg.HTTPPort),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("api listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	srv.StopForwarder()
	if infra.RPCBatcher != nil {
		infra.RPCBatcher.Close()
	}
	infra.Store.Close()
	_ = infra.CH.Close()
	infra.Bus.Close()
	log.Info("api stopped")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
