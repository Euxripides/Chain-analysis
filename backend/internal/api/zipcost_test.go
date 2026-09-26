package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// TestZipPathCost 量用户真实路径（Actions 导出 = csv + 强制 zip）的分段成本。
//
// 这条路径走 writeZipExport → 每 (地址,数据集) 一个 worker：
// 取数 → 去重 → 富化 → deflate 到临时文件 → 写进 ZIP → 上传。
// 此前所有优化（列式直写、流式、两阶段定界）都不经过它。
func TestZipPathCost(t *testing.T) {
	subject := os.Getenv("EXPORT_FLOW_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_FLOW_SUBJECT not set")
	}
	limit := 200000
	if raw := os.Getenv("EXPORT_FLOW_LIMIT"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log, TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})

	// 用真实导出行：先入库一个 export 行（putExportStream 的 intent gate 需要它）。
	scope, scopeErr := srv.systemScope(ctx)
	if scopeErr != nil {
		t.Skipf("system scope: %v", scopeErr)
	}
	exp, err := srv.deps.Store.CreateExportV2(ctx, scope.WorkspaceID, scope.OrganizationID, nil, nil, "token_transfers", "csv", true,
		[]byte(`{"chain":"bsc","addresses":["`+subject+`"],"datasets":["token_transfers"],"format":"csv","zip":true}`))
	if err != nil {
		t.Skipf("create export row: %v", err)
	}
	key := "bench/zipcost-" + exp.ID + ".zip"
	if prof := os.Getenv("EXPORT_FLOW_CPUPROFILE"); prof != "" {
		if f, ferr := os.Create(prof); ferr == nil {
			pprof.StartCPUProfile(f)
			defer pprof.StopCPUProfile()
		}
	}
	// 分段测量：map 路径（取数+去重 / 富化 / deflate）与列式路径对照。
	stageStart := time.Now()
	raw, qErr := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, maxExportRows, 0, nil, nil, nil, nil)
	if qErr != nil {
		t.Fatalf("query: %v", qErr)
	}
	raw = dedupeExportRows("token_transfers", raw)
	t.Logf("阶段1 取数+去重        %8s  %d 行", time.Since(stageStart).Round(time.Millisecond), len(raw))

	stageStart = time.Now()
	srv.attachTokenMetadata(ctx, "bsc", raw)
	t.Logf("阶段2 attachTokenMetadata %5s", time.Since(stageStart).Round(time.Millisecond))

	stageStart = time.Now()
	prep, pErr := prepareZipEntry("csv", raw)
	if pErr != nil {
		t.Fatalf("prepare: %v", pErr)
	}
	t.Logf("阶段3 deflate(csv)      %8s  %d MiB -> %d MiB", time.Since(stageStart).Round(time.Millisecond),
		prep.uncompressed/1048576, prep.compressed/1048576)
	prep.remove()

	stageStart = time.Now()
	colEntry, colRows, colErr := srv.prepareColumnarZipEntry(ctx, "token_transfers", "bsc", subject, maxExportRows, true, "")
	if colErr != nil {
		t.Fatalf("columnar entry: %v", colErr)
	}
	t.Logf("阶段1' 列式取数+富化+deflate %s  %d 行 %d MiB", time.Since(stageStart).Round(time.Millisecond),
		colRows, colEntry.compressed/1048576)
	colEntry.remove()

	start := time.Now()
	rows, sha, loc, err := srv.writeZipExport(ctx, exp, nil, key, "bsc", "", []string{subject}, []string{"token_transfers"}, "csv", true, false)
	dur := time.Since(start)
	if err != nil {
		t.Fatalf("writeZipExport: %v", err)
	}
	t.Logf("writeZipExport  %s  rows=%d sha=%s bucket=%s", dur.Round(time.Millisecond), rows, sha[:12], loc.Name)
	_ = limit
	_ = model.JobCompleted
	_ = io.Discard
}

// BenchmarkZipEntryCost 只量"一个 zip 条目"的成本：取数 → 去重 → 富化 → deflate。
// 用来判断 GC 与序列化各占多少，而不必每次跑 6 分钟的全量 zip。
func BenchmarkZipEntryCost(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	limit := benchLimit()
	raw, err := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, limit, 0, nil, nil, nil, nil)
	if err != nil {
		b.Fatalf("query: %v", err)
	}
	b.Logf("rows=%d", len(raw))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows := dedupeExportRows("token_transfers", raw)
		srv.attachTokenMetadata(ctx, "bsc", rows)
		prepared, err := prepareZipEntry("csv", rows)
		if err != nil {
			b.Fatalf("prepare: %v", err)
		}
		prepared.remove()
	}
}

// BenchmarkColumnarZipEntry 直接量列式 zip 条目准备（173 万行规模），
// 判断"是否值得为 zip 路径解除 include_meta 限制"。
func BenchmarkColumnarZipEntry(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	limit := benchLimit()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entry, rows, err := srv.prepareColumnarZipEntry(ctx, "token_transfers", "bsc", subject, limit, true, "")
		if err != nil {
			b.Fatalf("prepare: %v", err)
		}
		entry.remove()
		b.ReportMetric(float64(rows), "rows")
		b.ReportMetric(float64(entry.compressed)/1048576, "compressed_mib")
	}
}
