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
	ctx := context.Background()
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)

	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		log.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}

	// V1.3：API 侧只需要 Method 索引刷新（异步补全与回填在 Worker 进程执行）
	runCtx, cancelBackground := context.WithCancel(ctx)
	defer cancelBackground()
	infra.StartMethodBackground(runCtx, cfg, log, false)

	srv := api.New(api.Deps{
		Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Bus: infra.Bus, Prov: infra.Prov, Log: log,
		Registry: infra.Registry, MethodData: infra.MethodData, DeepDecode: infra.DeepDecode,
		TokenMeta: infra.TokenMeta, TokenBackfill: infra.TokenBackfill,
	})
	srv.StartForwarder()

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
