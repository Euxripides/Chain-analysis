package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"os"
	"strconv"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

/*
 * 导出流水线各段的稳定基准（重复运行取 ns/op，避免一次性测量的堆状态噪声）。
 *
 *	EXPORT_FLOW_SUBJECT=0x... go test ./internal/api/ -run '^$' \
 *	  -bench 'BenchmarkExport' -benchtime 3x -timeout 30m
 */
func benchServer(b *testing.B) (*Server, context.Context, string) {
	b.Helper()
	subject := os.Getenv("EXPORT_FLOW_SUBJECT")
	if subject == "" {
		b.Skip("EXPORT_FLOW_SUBJECT not set")
	}
	ctx := context.Background()
	// 与生产启动一致：cmd/api/main.go 在任何大批量分配之前调用 TuneExportGC。
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		b.Skipf("bootstrap unavailable: %v", err)
	}
	srv := New(Deps{
		Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData,
	})
	return srv, ctx, subject
}

func benchLimit() int {
	if raw := os.Getenv("EXPORT_FLOW_LIMIT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			return parsed
		}
	}
	return 200000
}

// BenchmarkExportAttachTokenMetadata 只量 token 元数据富化那一段。
func BenchmarkExportAttachTokenMetadata(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	rows, err := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, benchLimit(), 0, nil, nil, nil, nil)
	if err != nil {
		b.Fatalf("fetch: %v", err)
	}
	if len(rows) == 0 {
		b.Skip("no rows")
	}
	b.Logf("rows=%d", len(rows))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv.attachTokenMetadata(ctx, "bsc", rows)
	}
}

// BenchmarkExportStreamCSV 只量 CSV 序列化那一段（写 io.Discard）。
func BenchmarkExportStreamCSV(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	rows, err := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, benchLimit(), 0, nil, nil, nil, nil)
	if err != nil {
		b.Fatalf("fetch: %v", err)
	}
	if len(rows) == 0 {
		b.Skip("no rows")
	}
	srv.attachTokenMetadata(ctx, "bsc", rows)
	b.Logf("rows=%d", len(rows))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := streamCSV(io.Discard, rows); err != nil {
			b.Fatalf("csv: %v", err)
		}
	}
}

// BenchmarkExportQuery 只量查询层（无 FINAL + 快路径 + 流式去重）。
func BenchmarkExportQuery(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	limit := benchLimit()
	// 预热
	if _, err := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, 1000, 0, nil, nil, nil, nil); err != nil {
		b.Fatalf("warmup: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, limit, 0, nil, nil, nil, nil)
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		b.ReportMetric(float64(len(rows)), "rows")
	}
}

// BenchmarkFormatTokenAmount 只量金额格式化（导出里最贵的逐行纯计算）。
func BenchmarkFormatTokenAmount(b *testing.B) {
	plan := tokenMetaPlan{decimals: 18, decimalsKnown: true, divisor: new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)}
	raw := "1764025991317028396"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = formatTokenAmount(raw, plan)
	}
}

var _ = fmt.Sprint
