// Worker 进程：Scheduler + Downloader + Ingestion。
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() { errCh <- sched.Run(ctx) }()

	select {
	case <-stop:
		log.Info("worker stopping")
		<-errCh
	case err := <-errCh:
		if err != nil {
			log.Error("worker stopped with error", "err", err)
		}
	}
	infra.Store.Close()
	_ = infra.CH.Close()
	infra.Bus.Close()
	log.Info("worker stopped")
}
