package query

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

/*
 * 导出取数的真实端到端基准：直接调用生产入口 RowsForExportRange，
 * 覆盖「无 FINAL 形状 + 快路径 scanRowMap + 流式去重早停」整条链。
 *
 * 需要真实数据面，未配置时跳过。用法：
 *
 *	CLICKHOUSE_URL=... EXPORT_BENCH_SUBJECT=0x... \
 *	  go test ./internal/query/ -run '^$' -bench BenchmarkExportFetch -benchtime 1x -v
 */
// scanFloor 是流式下限探针：只 Scan + 算去重键，不建 map。
func scanFloor(ctx context.Context, svc *Service, q string, args []any, _ int) (int, error) {
	scanCtx := clickhouse.ExportScanContext(ctx)
	rows, err := svc.CH.DB().Query(scanCtx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	names, holders := fastColumnHolders(rows.ColumnTypes())
	scanner, ok := rows.(interface{ Scan(dest ...any) error })
	if !ok {
		return 0, nil
	}
	keys := exportDedupeColumns("token_transfers")
	n := 0
	for rows.Next() {
		if err := scanner.Scan(holders...); err != nil {
			return n, err
		}
		_ = exportRowKey(keys, holders, names)
		n++
	}
	return n, rows.Err()
}

// tuneExportGC 复刻 app.TuneExportGC（query 包不能反向 import app，否则成环）。
// 不设这一层的话，查询层基准跑在默认 GC 下，与生产进程的实测不可比：
// 导出行物化是分配密集路径，实测 GC 标记能占掉一半 CPU。
func tuneExportGC() {
	debug.SetGCPercent(400)
	debug.SetMemoryLimit(int64(6*1024) << 20)
}

func BenchmarkExportFetch(b *testing.B) {
	tuneExportGC()
	dsn := os.Getenv("CLICKHOUSE_URL")
	subject := os.Getenv("EXPORT_BENCH_SUBJECT")
	if dsn == "" || subject == "" {
		b.Skip("CLICKHOUSE_URL / EXPORT_BENCH_SUBJECT not set")
	}
	chain := os.Getenv("EXPORT_BENCH_CHAIN")
	if chain == "" {
		chain = "bsc"
	}
	limit := 200000
	fmt.Sscanf(os.Getenv("EXPORT_BENCH_LIMIT"), "%d", &limit)

	ctx := context.Background()
	conn, err := clickhouse.New(ctx, dsn)
	if err != nil {
		b.Skipf("clickhouse unreachable: %v", err)
	}
	defer conn.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := NewWithLog(conn, log)

	// EXPORT_BENCH_MODE=floor 时只扫描不物化 map，用来量"流式直写"能到的最低下限：
	// 查询 + 驱动解码 + 去重判定，不含 map 与序列化。
	if os.Getenv("EXPORT_BENCH_MODE") == "floor" {
		sel := "SELECT * FROM blockchain.token_transfers WHERE chain = ? AND (from_address = ? OR to_address = ?)" +
			" ORDER BY timestamp DESC LIMIT ?"
		args := []any{chain, subject, subject, limit * 145 / 100}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			start := time.Now()
			n, err := scanFloor(ctx, svc, sel, args, 0)
			if err != nil {
				b.Fatalf("floor scan: %v", err)
			}
			b.ReportMetric(float64(time.Since(start).Milliseconds()), "ms/export")
			b.ReportMetric(float64(n), "rows")
		}
		return
	}

	// 预热一次，排除首次连接与缓存影响。
	if _, err := svc.RowsForExportRange(ctx, "token_transfers", chain, subject, 1000, 0, nil, nil, nil, nil); err != nil {
		b.Fatalf("warmup: %v", err)
	}

	if prof := os.Getenv("EXPORT_BENCH_CPUPROFILE"); prof != "" {
		f, err := os.Create(prof)
		if err == nil {
			pprof.StartCPUProfile(f)
			defer pprof.StopCPUProfile()
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		rows, err := svc.RowsForExportRange(ctx, "token_transfers", chain, subject, limit, 0, nil, nil, nil, nil)
		if err != nil {
			b.Fatalf("export: %v", err)
		}
		b.ReportMetric(float64(time.Since(start).Milliseconds()), "ms/export")
		b.ReportMetric(float64(len(rows)), "rows")
	}
}

// BenchmarkExportBoundedStages 把两阶段形状的两步分开计时，用来判断
// "定界"与"带界取数"各占多少（整条 BenchmarkExportFetch 的耗时差说明有一段没算清）。
func BenchmarkExportBoundedStages(b *testing.B) {
	dsn := os.Getenv("CLICKHOUSE_URL")
	subject := os.Getenv("EXPORT_BENCH_SUBJECT")
	if dsn == "" || subject == "" {
		b.Skip("CLICKHOUSE_URL / EXPORT_BENCH_SUBJECT not set")
	}
	chain := os.Getenv("EXPORT_BENCH_CHAIN")
	if chain == "" {
		chain = "bsc"
	}
	limit := 200000
	fmt.Sscanf(os.Getenv("EXPORT_BENCH_LIMIT"), "%d", &limit)
	ctx := context.Background()
	conn, err := clickhouse.New(ctx, dsn)
	if err != nil {
		b.Skipf("clickhouse unreachable: %v", err)
	}
	defer conn.Close()
	svc := New(conn)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t := time.Now()
		bound, available, ok := svc.exportTimeBoundary(ctx, "token_transfers", chain, subject, nil, nil, nil, nil, limit, 0, exportBoundaryGrowthInit)
		boundaryDur := time.Since(t)
		if !ok {
			b.Fatal("no boundary")
		}
		where, args := exportBoundedWhere("token_transfers", chain, subject, bound)
		sel, _ := exportSelectNoFinal("token_transfers", where)
		fetch := int(float64(limit) * dedupeExportRetryFactor)
		if uint64(fetch) > available {
			fetch = int(available)
		}
		t = time.Now()
		raw, err := svc.queryRowsExport(ctx, "token_transfers", sel, limit, append(append([]any{}, args...), fetch, 0)...)
		if err != nil {
			b.Fatal(err)
		}
		fetchDur := time.Since(t)
		t = time.Now()
		distinct := dedupeExportRows("token_transfers", raw, limit)
		dedupeDur := time.Since(t)
		b.ReportMetric(float64(boundaryDur.Milliseconds()), "boundary_ms")
		b.ReportMetric(float64(fetchDur.Milliseconds()), "fetch_ms")
		b.ReportMetric(float64(dedupeDur.Milliseconds()), "dedupe_ms")
		b.ReportMetric(float64(len(raw)), "raw")
		b.ReportMetric(float64(len(distinct)), "distinct")
	}
}

// appendAny 是 CSV 的最小取值写法（本基准只关心"不建 map"省下多少，
// 不追求与 api 包 appendCSVValue 逐字节一致；引号规则对耗时无影响）。
func appendAny(dst []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return dst
	case string:
		return append(dst, x...)
	default:
		return append(dst, fmt.Sprint(x)...)
	}
}

// BenchmarkExportDirectWriteFloor 量"完全不做 map 物化"的物理下限：
// 直接把列持有变量写成 CSV 行，不建 map、不做去重。
//
// 这不是可交付的实现（导出契约要求 API 层拿到 []map[string]any 做富化），
// 而是用来回答一个决策问题：现有行表示到底吃掉了多少时间。
//
// 实测（canonical 地址，bsc，全量历史，生产 GC 配置）：
//
//	20 万行   下限 1.11-1.18 s   生产完整链路 5.50-5.90 s（中位）
//	100 万行  下限 4.28 s        生产完整链路 23.9-26.9 s
//
// 即现状是下限的 4.3-5.2 倍，差额全部来自 map[string]any 行表示
// （每行 30 个键 / 30 个装箱值，20 万行实测分配 1524 MiB，GC 标记是主要 CPU 消耗）
// 以及它对地图化富化的强制要求。这个下限说明：继续在 Go 侧做边际优化
// （换哈希、调块大小、并行分片都已实测无效或负收益）无法弥合 100 万行的差距。
func BenchmarkExportDirectWriteFloor(b *testing.B) {
	dsn := os.Getenv("CLICKHOUSE_URL")
	subject := os.Getenv("EXPORT_BENCH_SUBJECT")
	if dsn == "" || subject == "" {
		b.Skip("CLICKHOUSE_URL / EXPORT_BENCH_SUBJECT not set")
	}
	chain := os.Getenv("EXPORT_BENCH_CHAIN")
	if chain == "" {
		chain = "bsc"
	}
	limit := 200000
	fmt.Sscanf(os.Getenv("EXPORT_BENCH_LIMIT"), "%d", &limit)
	tuneExportGC()
	ctx := context.Background()
	conn, err := clickhouse.New(ctx, dsn)
	if err != nil {
		b.Skipf("clickhouse unreachable: %v", err)
	}
	defer conn.Close()
	svc := New(conn)

	// 用与生产相同的两阶段形状，确保比较的是同一批行。
	sql := "SELECT * FROM blockchain.token_transfers WHERE chain=? AND (from_address=? OR to_address=?)" +
		" ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	args := []any{chain, subject, subject, limit, 0}
	if sel, selArgs, _, ok := svc.BoundedExportSelect(ctx, "token_transfers", chain, subject, limit); ok {
		sql, args = sel, selArgs
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := conn.DB().Query(clickhouse.ExportScanContext(ctx), sql, args...)
		if err != nil {
			b.Fatal(err)
		}
		names, holders := fastColumnHolders(rows.ColumnTypes())
		scanner, ok := rows.(interface{ Scan(dest ...any) error })
		if !ok {
			b.Fatal("driver rows do not support Scan")
		}
		bw := bufio.NewWriterSize(io.Discard, 1<<20)
		buf := make([]byte, 0, 4096)
		n := 0
		for rows.Next() {
			if err := scanner.Scan(holders...); err != nil {
				b.Fatal(err)
			}
			buf = buf[:0]
			for i := range names {
				if i > 0 {
					buf = append(buf, ',')
				}
				buf = appendAny(buf, exportValue(holders[i]))
			}
			buf = append(buf, '\n')
			if _, err := bw.Write(buf); err != nil {
				b.Fatal(err)
			}
			n++
		}
		rows.Close()
		bw.Flush()
		b.ReportMetric(float64(n), "rows")
	}
}
