package query

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

/*
 * 快路径（scanRowMap）与慢路径（scanOne 的 JSON 往返）必须产出**导出可见语义完全相同**的行。
 *
 * 这个测试是快路径的判据：两者对同一批行逐列比 fmt.Sprint（导出写 CSV 用的就是它），
 * 并逐行比 map 键集。任何一列不同都说明快路径不能替代慢路径。
 *
 * 需要真实数据面，未配置或数据面不可达时跳过（CI/无网环境不阻塞）：
 *
 *	POSTGRES/CH 凭据从环境读取，与 cmd/exportbench 相同。
 */
func TestFastScanMatchesJSONRoundTrip(t *testing.T) {
	dsn := os.Getenv("CLICKHOUSE_URL")
	if dsn == "" {
		t.Skip("CLICKHOUSE_URL not set; skipping live fast-scan equivalence check")
	}
	subject := os.Getenv("EXPORT_EQUIV_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_EQUIV_SUBJECT not set; skipping live fast-scan equivalence check")
	}
	chain := os.Getenv("EXPORT_EQUIV_CHAIN")
	if chain == "" {
		chain = "bsc"
	}
	_ = config.Load() // 读取 CLICKHOUSE_URL 的同名环境变量，保持与生产一致的配置路径

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, err := clickhouse.New(ctx, dsn)
	if err != nil {
		t.Skipf("clickhouse unreachable: %v", err)
	}
	defer conn.Close()

	svc := New(conn)
	rows, err := svc.RowsForExportRange(ctx, "token_transfers", chain, subject, 50000, 0, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("export fetch: %v", err)
	}
	if len(rows) == 0 {
		t.Skip("no rows for the configured subject")
	}

	// 慢路径：同样取出结果集，但逐行走 scanOne 的 JSON 往返。
	slowRows, err := slowExportRows(ctx, svc, "token_transfers", chain, subject, len(rows))
	if err != nil {
		t.Fatalf("slow fetch: %v", err)
	}
	if len(slowRows) != len(rows) {
		t.Fatalf("row count differs: fast=%d slow=%d", len(rows), len(slowRows))
	}

	keyOf := func(r map[string]any) string {
		return fmt.Sprintf("%v|%v|%v", r["tx_hash"], r["log_index"], r["token_address"])
	}
	sort.Slice(rows, func(i, j int) bool { return keyOf(rows[i]) < keyOf(rows[j]) })
	sort.Slice(slowRows, func(i, j int) bool { return keyOf(slowRows[i]) < keyOf(slowRows[j]) })

	diffCols := map[string]int{}
	var sample string
	diffRows := 0
	for i := range rows {
		rowDiff := false
		if len(rows[i]) != len(slowRows[i]) {
			t.Fatalf("row %d column count differs: fast=%d slow=%d", i, len(rows[i]), len(slowRows[i]))
		}
		for col, fastVal := range rows[i] {
			slowVal, ok := slowRows[i][col]
			if !ok {
				diffCols[col+"(missing)"]++
				rowDiff = true
				continue
			}
			// 版本列不受快路径影响，但同一排序键的多个版本在两次查询里可能各取一行；
			// 这里只比较导出真正写出的语义：fmt.Sprint 的字符串形式。
			if fmt.Sprint(fastVal) != fmt.Sprint(slowVal) {
				diffCols[col]++
				rowDiff = true
				if sample == "" {
					sample = fmt.Sprintf("row=%s col=%s fast=%q slow=%q",
						keyOf(rows[i]), col, fmt.Sprint(fastVal), fmt.Sprint(slowVal))
				}
			}
		}
		if rowDiff {
			diffRows++
		}
	}
	if diffRows > 0 {
		t.Fatalf("%d/%d rows differ (cols=%v); first: %s", diffRows, len(rows), diffCols, sample)
	}
	t.Logf("fast scan matches JSON round-trip on %d rows × %d columns", len(rows), len(rows[0]))
}

// slowExportRows 用同一查询但强制慢路径取数。
func slowExportRows(ctx context.Context, svc *Service, dataset, chain, subject string, limit int) ([]map[string]any, error) {
	where := "chain=? AND (from_address=? OR to_address=?)"
	args := []any{chain, subject, subject, limit}
	q := "SELECT * FROM blockchain." + tableName(dataset) + " WHERE " + where +
		" ORDER BY timestamp DESC LIMIT ?"
	return svc.queryRows(clickhouse.ExportScanContext(ctx), dataset, q, args...)
}
