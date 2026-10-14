// Dedicated EVM contract processor; it runs no HTTP, downloader or watch loop.
package main

import (
	"context"
	"github.com/blockchain-platform/blockchain-platform/internal/api"
	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/logger"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)
	infra, e := app.Bootstrap(ctx, cfg, log)
	if e != nil {
		log.Error("contract worker bootstrap failed", "error", e)
		os.Exit(1)
	}
	defer infra.Store.Close()
	defer infra.CH.Close()
	defer infra.Bus.Close()
	if infra.RPCBatcher != nil {
		defer infra.RPCBatcher.Close()
	}
	srv := api.New(api.Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Bus: infra.Bus, Prov: infra.Prov, ProviderControl: infra.ProviderControl, Log: log, Registry: infra.Registry, MethodData: infra.MethodData, DeepDecode: infra.DeepDecode, TokenMeta: infra.TokenMeta, TokenBackfill: infra.TokenBackfill, Head: infra.Head, AddressType: infra.AddressType, BalanceRead: infra.BalanceRead, ChainRead: infra.ChainRead, Trace: infra.Trace, RPCGateway: infra.RPCGateway, Capabilities: infra.Capabilities, Investigations: infra.Investigations, Canvases: infra.Canvases, Routes: infra.Routes, Secrets: infra.Secrets, DRPCManagement: infra.DRPCManagement, DRPC: infra.DRPC})
	log.Info("contract worker ready", "processor_version", "contract-analysis-v1")
	srv.RunContractAnalysisWorker(ctx)
}
