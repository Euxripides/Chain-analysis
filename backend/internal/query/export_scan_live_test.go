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

// TestForEachExportChunkMatchesSingleFetch 锁定流式分块与一次性取数的等价性。
//
// 流式导出（边取边写 MinIO）与批量导出必须产出同一批行、同一顺序：分块只应改变
// "什么时候拿到行"，不应改变"拿到哪些行"。跨块的重复键由同一个 seen 集合收敛，
// 因此这条不变式必须在真实数据上验一遍。
func TestForEachExportChunkMatchesSingleFetch(t *testing.T) {
	dsn := os.Getenv("CLICKHOUSE_URL")
	subject := os.Getenv("EXPORT_EQUIV_SUBJECT")
	if dsn == "" || subject == "" {
		t.Skip("CLICKHOUSE_URL / EXPORT_EQUIV_SUBJECT not set")
	}
	chain := os.Getenv("EXPORT_EQUIV_CHAIN")
	if chain == "" {
		chain = "bsc"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, err := clickhouse.New(ctx, dsn)
	if err != nil {
		t.Skipf("clickhouse unreachable: %v", err)
	}
	defer conn.Close()
	svc := New(conn)

	const limit = 20000
	single, err := svc.RowsForExportRange(ctx, "token_transfers", chain, subject, limit, 0, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("single fetch: %v", err)
	}
	if len(single) == 0 {
		t.Skip("no rows for the configured subject")
	}

	sql := "SELECT * FROM blockchain.token_transfers WHERE chain=? AND (from_address=? OR to_address=?)" +
		" ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	var chunked []map[string]any
	chunks := 0
	total, err := svc.ForEachExportChunk(ctx, "token_transfers", sql, limit, 512,
		func(rows []map[string]any) error {
			chunks++
			chunked = append(chunked, rows...)
			return nil
		}, chain, subject, subject, limit, 0)
	if err != nil {
		t.Fatalf("chunked fetch: %v", err)
	}
	if total != len(chunked) {
		t.Fatalf("reported %d rows but callback saw %d", total, len(chunked))
	}
	if len(chunked) != len(single) {
		t.Fatalf("chunked=%d single=%d", len(chunked), len(single))
	}
	if chunks < 2 {
		t.Fatalf("expected the result to span multiple chunks, got %d", chunks)
	}
	// 断言两条路径产出**同一集合**，且都按 timestamp 非增序。
	//
	// 不逐行比顺序：同一 timestamp 上的行没有稳定次序（实测同一查询两次执行，
	// 并列行的相对次序不同——集合差异为 0，仅次序互换）。这是 ORDER BY timestamp DESC
	// 本身的性质，不是流式改动引入的；导出契约里同秒行的次序本来就未定义。
	keyOf := func(r map[string]any) string {
		return fmt.Sprintf("%v|%v|%v", r["tx_hash"], r["log_index"], r["token_address"])
	}
	singleSet := make(map[string]struct{}, len(single))
	for _, r := range single {
		singleSet[keyOf(r)] = struct{}{}
	}
	chunkedSet := make(map[string]struct{}, len(chunked))
	for _, r := range chunked {
		chunkedSet[keyOf(r)] = struct{}{}
	}
	if len(singleSet) != len(chunkedSet) {
		t.Fatalf("distinct keys differ: single=%d chunked=%d", len(singleSet), len(chunkedSet))
	}
	for k := range singleSet {
		if _, ok := chunkedSet[k]; !ok {
			t.Fatalf("key %s present in single but missing from chunked", k)
		}
	}
	assertNonIncreasing := func(name string, rows []map[string]any) {
		for i := 1; i < len(rows); i++ {
			prev, err1 := time.Parse(time.RFC3339Nano, fmt.Sprint(rows[i-1]["timestamp"]))
			cur, err2 := time.Parse(time.RFC3339Nano, fmt.Sprint(rows[i]["timestamp"]))
			if err1 != nil || err2 != nil {
				t.Fatalf("%s: unparsable timestamp at %d: %v / %v", name, i, err1, err2)
			}
			if cur.After(prev) {
				t.Fatalf("%s: timestamp increased at row %d (%s -> %s)", name, i, prev, cur)
			}
		}
	}
	assertNonIncreasing("single", single)
	assertNonIncreasing("chunked", chunked)
	t.Logf("chunked == single over %d rows / %d chunks", len(single), chunks)
}

// TestBoundedFetchMatchesUnbounded 锁定两阶段形状（先定时间下界再带界取数）
// 与无界形状返回**同一个行集合**。
//
// 这是两阶段形状最危险的失效模式：下界定窄了会静默少给行，而定界用的是原始行数
// 聚合、目标是"不同转账键"的条数，两者之间隔着一个去重率（bsc 实测 31.4%）。
func TestBoundedFetchMatchesUnbounded(t *testing.T) {
	dsn := os.Getenv("CLICKHOUSE_URL")
	subject := os.Getenv("EXPORT_EQUIV_SUBJECT")
	if dsn == "" || subject == "" {
		t.Skip("CLICKHOUSE_URL / EXPORT_EQUIV_SUBJECT not set")
	}
	chain := os.Getenv("EXPORT_EQUIV_CHAIN")
	if chain == "" {
		chain = "bsc"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, err := clickhouse.New(ctx, dsn)
	if err != nil {
		t.Skipf("clickhouse unreachable: %v", err)
	}
	defer conn.Close()
	svc := New(conn)

	const limit = 30000
	// 走两阶段路径的生产入口。
	bounded, err := svc.RowsForExportRange(ctx, "token_transfers", chain, subject, limit, 0, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("bounded fetch: %v", err)
	}
	if len(bounded) != limit {
		t.Fatalf("bounded fetch returned %d rows, want %d", len(bounded), limit)
	}

	// 无界形状（与批量路径同一 SQL），再按同一去重键收敛。
	unbounded, err := slowExportRows(ctx, svc, "token_transfers", chain, subject, limit*2)
	if err != nil {
		t.Fatalf("unbounded fetch: %v", err)
	}
	unbounded = dedupeExportRows("token_transfers", unbounded, limit)
	if len(unbounded) != limit {
		t.Fatalf("unbounded fetch produced %d distinct rows, want %d", len(unbounded), limit)
	}

	keyOf := func(r map[string]any) string {
		return fmt.Sprintf("%v|%v|%v", r["tx_hash"], r["log_index"], r["token_address"])
	}
	want := make(map[string]struct{}, len(unbounded))
	for _, r := range unbounded {
		want[keyOf(r)] = struct{}{}
	}
	got := make(map[string]struct{}, len(bounded))
	for _, r := range bounded {
		got[keyOf(r)] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("distinct key count differs: bounded=%d unbounded=%d", len(got), len(want))
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Fatalf("key %s present in unbounded but missing from bounded", k)
		}
	}
	t.Logf("bounded == unbounded over %d distinct keys", len(got))
}
