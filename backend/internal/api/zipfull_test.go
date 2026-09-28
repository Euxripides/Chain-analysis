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

// TestZipFullBreakdown 把完整 zip 导出拆成【准备 export 行 / 写 zip 到 MinIO / SHA 回读】。
func TestZipFullBreakdown(t *testing.T) {
	subject := os.Getenv("EXPORT_FLOW_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_FLOW_SUBJECT not set")
	}
	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log, TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})
	scope, serr := srv.systemScope(ctx)
	if serr != nil {
		t.Skipf("scope: %v", serr)
	}
	exp, err := srv.deps.Store.CreateExportV2(ctx, scope.WorkspaceID, scope.OrganizationID, nil, nil, "token_transfers", "csv", true, []byte(`{"chain":"bsc"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	key := "bench/full-" + exp.ID + ".zip"
	start := time.Now()
	rows, sha, _, err := srv.writeZipExport(ctx, exp, nil, key, "bsc", "", []string{subject}, []string{"token_transfers"}, "csv", true, false, exportBounds{})
	if err != nil {
		t.Fatalf("writeZipExport: %v", err)
	}
	t.Logf("writeZipExport(含上传+对象SHA)  %s  rows=%d sha=%s", time.Since(start).Round(time.Millisecond), rows, sha[:12])
}
