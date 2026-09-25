package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

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

// BenchmarkExportStreamPipeline 量"边取边写"整条链：分块取数 + 逐块富化 + 逐块 CSV。
//
// 与 BenchmarkExportQuery + BenchmarkExportAttachTokenMetadata + BenchmarkExportStreamCSV
// 三个批量段之和对比，就是流式省下来的内存搬运与合并开销。
func BenchmarkExportStreamPipeline(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	limit := benchLimit()
	sql := exportStreamSQL("token_transfers", "bsc", subject)
	raw := exportStreamRawLimit(limit)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enrich := newStreamEnrichment(srv, "bsc", "", false)
		writer := newStreamCSVWriter(io.Discard)
		if _, err := srv.query.ForEachExportChunk(ctx, "token_transfers", sql, raw, exportStreamChunk,
			func(chunk []map[string]any) error {
				enrich.enrich(ctx, subject, chunk)
				return writer.write(chunk)
			}, "bsc", subject, subject, raw, 0); err != nil {
			b.Fatalf("stream: %v", err)
		}
		if err := writer.close(); err != nil {
			b.Fatalf("close: %v", err)
		}
	}
}

// BenchmarkExportStreamPipelineNoEnrich 与 BenchmarkExportStreamPipeline 同链，
// 但富化回调为空。两者之差就是逐块富化的真实成本，用来判断该不该继续压富化。
func BenchmarkExportStreamPipelineNoEnrich(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	limit := benchLimit()
	// 两条形状的参数不同（无界形状 WHERE 里带 address，带界形状把下界也写进 SQL），
	// 必须用各自配套的参数，不能混用。
	sql := exportStreamSQL("token_transfers", "bsc", subject)
	sqlArgs := exportStreamArgs("bsc", subject)
	raw := exportStreamRawLimit(limit)
	if sel, selArgs, fetch, ok := srv.query.BoundedExportSelect(ctx, "token_transfers", "bsc", subject, limit); ok {
		sql, sqlArgs, raw = sel, selArgs, fetch
		b.Logf("bounded select applied, fetch=%d args=%d", fetch, len(selArgs))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writer := newStreamCSVWriter(io.Discard)
		if _, err := srv.query.ForEachExportChunk(ctx, "token_transfers", sql, raw, exportStreamChunk,
			func(chunk []map[string]any) error {
				return writer.write(chunk)
			}, sqlArgs...); err != nil {
			b.Fatalf("stream: %v", err)
		}
		if err := writer.close(); err != nil {
			b.Fatalf("close: %v", err)
		}
	}
}

// BenchmarkExportBatchPipeline 量生产默认路径（批量）：查询 + 一次富化 + CSV 序列化。
// 与 BenchmarkExportStreamPipeline 是同一件事的两种做法，用来支撑"默认走哪条"的决定。
func BenchmarkExportBatchPipeline(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	limit := benchLimit()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, limit, 0, nil, nil, nil, nil)
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		srv.attachTokenMetadata(ctx, "bsc", rows)
		if err := streamCSV(io.Discard, rows); err != nil {
			b.Fatalf("csv: %v", err)
		}
	}
}

// BenchmarkExportBatchPipelineFull 在 BenchmarkExportBatchPipeline 之上补上传与 SHA 回读，
// 即生产路径的完整"点击到可下载"（不含 HTTP 与票据）。
func BenchmarkExportBatchPipelineFull(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	limit := benchLimit()
	if srv.deps.Raw == nil {
		b.Skip("no object store configured")
	}
	bucket := os.Getenv("EXPORT_BENCH_BUCKET")
	if bucket == "" {
		b.Skip("EXPORT_BENCH_BUCKET not set")
	}
	client := srv.deps.Raw.Client()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, limit, 0, nil, nil, nil, nil)
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		srv.attachTokenMetadata(ctx, "bsc", rows)
		pr, pw := io.Pipe()
		go func() { _ = pw.CloseWithError(streamCSV(pw, rows)) }()
		key := fmt.Sprintf("bench/full-%d.csv", time.Now().UnixNano())
		if _, err := client.PutObject(ctx, bucket, key, pr, -1, minio.PutObjectOptions{ContentType: "text/csv"}); err != nil {
			b.Fatalf("put: %v", err)
		}
		obj, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
		if err != nil {
			b.Fatalf("get: %v", err)
		}
		h := sha256.New()
		if _, err := io.Copy(h, obj); err != nil {
			b.Fatalf("sha: %v", err)
		}
		obj.Close()
		client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
	}
}

// BenchmarkExportColumnarPipeline 量列式直写路径（生产默认已启用）：
// 分块取数 + 逐块富化 + 直写 CSV，不建 []map[string]any。
func BenchmarkExportColumnarPipeline(b *testing.B) {
	srv, ctx, subject := benchServer(b)
	limit := benchLimit()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		written, ok, err := srv.exportColumnarStream(ctx, "token_transfers", "bsc", subject, io.Discard, limit)
		if err != nil {
			b.Fatalf("columnar: %v", err)
		}
		if !ok {
			b.Fatal("columnar unsupported")
		}
		b.ReportMetric(float64(written), "rows")
	}
}
