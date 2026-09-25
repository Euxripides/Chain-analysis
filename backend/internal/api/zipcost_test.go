package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

// TestZipPathCost 量真实 Actions 导出路径（csv+zip）各入口的成本。
func TestZipPathCost(t *testing.T) {
	subject := os.Getenv("EXPORT_FLOW_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_FLOW_SUBJECT not set")
	}
	limit := 200000
	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log, TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})

	start := time.Now()
	multi, err := srv.query.RowsForExportRangeMulti(ctx, "token_transfers", "bsc", []string{subject}, limit, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("multi: %v", err)
	}
	t.Logf("A RowsForExportRangeMulti  %8s  %d 行", time.Since(start).Round(time.Millisecond), len(multi))

	start = time.Now()
	written, ok, err := srv.exportColumnarStream(ctx, "token_transfers", "bsc", subject, io.Discard, limit)
	if err != nil || !ok {
		t.Fatalf("columnar: ok=%v err=%v", ok, err)
	}
	t.Logf("C 列式直写                 %8s  %d 行", time.Since(start).Round(time.Millisecond), written)
}
