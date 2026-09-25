package query

import (
	"context"
	"fmt"
	"os"
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

func BenchmarkExportFetch(b *testing.B) {
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
