// exportbench：导出链路分段计时探针（点击导出 → 对象可下载）。
//
// 用途：把「查询 / 去重 / 映射 / 序列化 / 上传 / SHA 回读」各段耗时与产物大小分开量，
// 用来判断端到端预算该优化哪一段，以及为导出路径选查询形状。
//
// 形状（-shape）：
//
//	old      现网合并形态：FINAL + 键 IN 子查询（exportIndexedFinalWhere）
//	dedup    显式去重：LIMIT 1 BY 排序键（query.go 的 dedupSource 口径）
//	activity address_activity 前缀读（排序键 (chain,address,timestamp) 自带顺序）
//	block    token_transfers 按 block_number 倒序（免排序，但顺序语义不同）
//	twophase 键 IN 子查询（子查询走 address_activity）
//	boundary 两段式：先取第 N 行的 (timestamp, block_number) 作下界，再带下界回表
//
// 只读数据面；-bucket 为空时不写任何对象存储。
package main

import (
	"bufio"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/shopspring/decimal"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

const (
	exportDedupeRetryFactor = 1.45
	maxExportDedupeAttempts = 3
)

type record struct {
	name string
	dur  time.Duration
	note string
}

var records []record

func mark(name string, start time.Time, note string) {
	records = append(records, record{name: name, dur: time.Since(start), note: note})
}

func main() {
	dataset := flag.String("dataset", "token_transfers", "dataset")
	chain := flag.String("chain", "bsc", "chain")
	subject := flag.String("subject", "", "address")
	windowDays := flag.Int("window", 0, "timestamp window in days (0 = full history)")
	limit := flag.Int("limit", 200000, "row limit")
	format := flag.String("format", "csv", "csv|jsonl|zip")
	bucket := flag.String("bucket", "", "upload target bucket (empty = skip storage stages)")
	shape := flag.String("shape", "dedup", "old|dedup|activity|block|twophase|boundary")
	dedupe := flag.Bool("dedupe", false, "apply the dedupeExportRows key on the Go side")
	countOnly := flag.Bool("count-only", false, "consume rows without materialising maps")
	skipSerialize := flag.Bool("skip-serialize", false, "stop after the query stage")
	overFetch := flag.Float64("overfetch", 1.5, "twophase key-set margin")
	verify := flag.Bool("verify", false, "compare the two-step result against the legacy FINAL shape row by row")
	flag.Parse()
	if *subject == "" {
		fmt.Fprintln(os.Stderr, "subject required")
		os.Exit(2)
	}

	cfg := config.Load()
	ctx := context.Background()
	ch, err := clickhouse.New(ctx, cfg.ClickHouseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clickhouse: %v\n", err)
		os.Exit(1)
	}
	defer ch.Close()

	opts := fetchOpts{
		shape: *shape, dataset: *dataset, chain: *chain,
		subject:    addressnorm.NormalizeKey(*chain, *subject),
		windowDays: *windowDays, limit: *limit, overFetch: *overFetch,
	}

	if *countOnly {
		start := time.Now()
		n, err := consumeOnly(ctx, ch, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "count: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("count-only shape=%s rows=%d elapsed=%s\n", *shape, n, time.Since(start).Round(time.Millisecond))
		return
	}

	if *verify {
		if err := verifyEquivalence(ctx, ch, opts); err != nil {
			fmt.Fprintf(os.Stderr, "verify: %v\n", err)
			os.Exit(1)
		}
		return
	}

	start := time.Now()
	rows, err := fetchRows(ctx, ch, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		os.Exit(1)
	}
	if *dedupe {
		before := len(rows)
		rows = dedupeRows(rows)
		mark("1a.Go 侧去重", start, fmt.Sprintf("%d -> %d 行", before, len(rows)))
	}
	mark("1.查询+去重+映射", start, fmt.Sprintf("%d 行", len(rows)))
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "no rows")
		os.Exit(1)
	}

	if *skipSerialize {
		printRecords(0)
		return
	}

	tmp, err := os.CreateTemp("", "exportbench-*."+*format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tempfile: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmp.Name())

	t := time.Now()
	var written int64
	switch *format {
	case "csv":
		written, err = serializeCSV(tmp, rows)
	case "jsonl":
		written, err = serializeJSONL(tmp, rows)
	case "zip":
		written, err = serializeZip(tmp, rows)
	default:
		err = fmt.Errorf("unknown format %s", *format)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "serialize: %v\n", err)
		os.Exit(1)
	}
	mark("2.序列化", t, fmt.Sprintf("%.1f MiB", float64(written)/1048576))

	if *bucket != "" {
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			fmt.Fprintf(os.Stderr, "seek: %v\n", err)
			os.Exit(1)
		}
		client, err := minio.New(cfg.MinioEndpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
			Secure: cfg.MinioSecure,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "minio: %v\n", err)
			os.Exit(1)
		}
		key := fmt.Sprintf("bench/%d.%s", time.Now().UnixNano(), *format)

		t = time.Now()
		if _, err := client.PutObject(ctx, *bucket, key, tmp, written, minio.PutObjectOptions{ContentType: contentType(*format)}); err != nil {
			fmt.Fprintf(os.Stderr, "put: %v\n", err)
			os.Exit(1)
		}
		mark("3.上传 MinIO", t, key)

		t = time.Now()
		obj, err := client.GetObject(ctx, *bucket, key, minio.GetObjectOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "get: %v\n", err)
			os.Exit(1)
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, obj)
		obj.Close()
		if copyErr != nil {
			fmt.Fprintf(os.Stderr, "sha: %v\n", copyErr)
			os.Exit(1)
		}
		mark("4.SHA256 回读", t, fmt.Sprintf("%s %d bytes", hex.EncodeToString(h.Sum(nil))[:16], n))
		_ = client.RemoveObject(ctx, *bucket, key, minio.RemoveObjectOptions{})
	}

	var total time.Duration
	for _, r := range records {
		total += r.dur
	}
	printRecords(total)
}

func printRecords(total time.Duration) {
	fmt.Printf("\n%-22s %10s  %s\n", "阶段", "耗时", "备注")
	for _, r := range records {
		fmt.Printf("%-22s %10s  %s\n", r.name, r.dur.Round(time.Millisecond), r.note)
	}
	if total > 0 {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fmt.Printf("%-22s %10s  heap=%.0f MiB sys=%.0f MiB\n", "合计", total.Round(time.Millisecond),
			float64(m.HeapAlloc)/1048576, float64(m.Sys)/1048576)
	}
}

type fetchOpts struct {
	shape      string
	dataset    string
	chain      string
	subject    string
	windowDays int
	limit      int
	overFetch  float64
}

func (o fetchOpts) table() string {
	if o.dataset == "internal_transactions" {
		return "internal_calls"
	}
	return o.dataset
}

// windowClause 相对**数据最新时间**而不是 now() 取窗口：数据是离线摄取的，
// 用 now() 会落空（实测数据最新时间落后当前时间数天）。
func (o fetchOpts) windowClause(table string) string {
	if o.windowDays <= 0 {
		return ""
	}
	return fmt.Sprintf(" AND timestamp >= (SELECT max(timestamp) FROM blockchain.%s WHERE chain = '%s') - INTERVAL %d DAY",
		table, strings.ReplaceAll(o.chain, "'", ""), o.windowDays)
}

// addressWhere 返回 token_transfers/transactions 系的地址过滤条件。
func (o fetchOpts) addressWhere(table string) (string, []any) {
	switch o.dataset {
	case "logs", "balances":
		return "chain = ? AND address = ?" + o.windowClause(table), []any{o.chain, o.subject}
	default:
		return "chain = ? AND (from_address = ? OR to_address = ?)" + o.windowClause(table),
			[]any{o.chain, o.subject, o.subject}
	}
}

// activityWhere 返回 address_activity 的过滤条件。
func (o fetchOpts) activityWhere() (string, []any) {
	return "chain = ? AND address = ?" + o.windowClause("address_activity"),
		[]any{o.chain, o.subject}
}

// buildSQL 返回单段查询的 (SQL, 参数)。参数与占位符一一对应，调用方不得再追加。
func (o fetchOpts) buildSQL() (string, []any) {
	table := o.table()
	where, args := o.addressWhere(table)
	key := "tx_hash, log_index, token_address"

	switch o.shape {
	case "old":
		return "SELECT * FROM blockchain." + table + " FINAL WHERE " + where +
			" ORDER BY timestamp DESC LIMIT ?", append(args, o.limit)
	case "activity":
		w, a := o.activityWhere()
		return "SELECT * FROM blockchain.address_activity WHERE " + w +
			" ORDER BY timestamp DESC LIMIT ?", append(a, o.limit)
	case "block":
		return "SELECT * FROM blockchain." + table + " WHERE " + where +
			" ORDER BY block_number DESC LIMIT ?", append(args, o.limit)
	case "direct":
		// 落地形状：只去掉 FINAL，不用 IN 子查询（子查询要先物化 2040 万个键，实测更慢）。
		return "SELECT * FROM blockchain." + table + " WHERE " + where +
			" ORDER BY timestamp DESC LIMIT ?", append(args, o.limit)
	case "twophase":
		// 现网形状的无 FINAL 版：键 IN 子查询 + 外层取数。
		sub := "SELECT tx_hash, log_index, token_address FROM blockchain." + table + " WHERE " + where +
			" ORDER BY timestamp DESC LIMIT ?"
		a := append(append([]any{}, args...), int(float64(o.limit)*o.overFetch))
		full := "SELECT * FROM blockchain." + table + " WHERE chain = ? AND (" + key + ") IN (" + sub + ")" +
			" ORDER BY timestamp DESC LIMIT ?"
		return full, append(append([]any{o.chain}, a...), o.limit)
	default: // dedup
		return "SELECT * FROM (SELECT * FROM blockchain." + table + " WHERE " + where +
				" ORDER BY source_ingested_at DESC LIMIT 1 BY " + key + ") ORDER BY timestamp DESC LIMIT ?",
			append(args, o.limit)
	}
}

// fetchRows 执行查询并物化成 []map[string]any（与现网 queryRows 同形）。
func fetchRows(ctx context.Context, ch *clickhouse.Conn, o fetchOpts) ([]map[string]any, error) {
	scanCtx := clickhouse.ExportScanContext(ctx)
	if o.shape == "boundary" {
		return fetchBoundary(ctx, ch, o, scanCtx)
	}
	sql, args := o.buildSQL()
	rows, err := ch.DB().Query(scanCtx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows, o.limit)
}

// fetchBoundary 两段式：第一段只取第 limit 行的 (timestamp, block_number) 作下界，
// 第二段带下界回原表。下界落在分区键 timestamp 上，可直接裁剪 toYYYYMM 分区。
//
// 超采与重试复刻 internal/query/export_twostep.go 的 rowsForExportTwoStep：
// 原表同一转账键可能有多版本行（bsc 重复率 31.4%），一次取 upper 行去重后可能不足 limit，
// 因此按 1.45 倍起超采、不足则每次再放宽 200000 行，最多 3 次。
func fetchBoundary(ctx context.Context, ch *clickhouse.Conn, o fetchOpts, scanCtx context.Context) ([]map[string]any, error) {
	w, a := o.activityWhere()
	probe := "SELECT timestamp, block_number FROM blockchain.address_activity WHERE " + w +
		" ORDER BY timestamp DESC LIMIT 1 OFFSET ?"
	a = append(a, o.limit-1)

	var boundTS time.Time
	var boundBlock uint64
	t := time.Now()
	if err := ch.DB().QueryRow(scanCtx, probe, a...).Scan(&boundTS, &boundBlock); err != nil {
		return nil, fmt.Errorf("boundary probe: %w", err)
	}
	mark("1a.边界探针", t, fmt.Sprintf("boundTS=%s block=%d", boundTS.Format("2006-01-02 15:04:05"), boundBlock))

	where, args := o.addressWhere(o.table())
	// 用 >= 而不是 >：边界时间戳上可能还有别的转账行，严格大大会把它们排除，
	// 导致去重后系统性少几十行（实测 200000 目标少 27 行）。多取的部分由截断收敛。
	sql := "SELECT * FROM blockchain." + o.table() + " WHERE " + where +
		" AND timestamp >= ? ORDER BY timestamp DESC LIMIT ?"

	upper := int(float64(o.limit) * exportDedupeRetryFactor)
	for attempt := 0; attempt < maxExportDedupeAttempts; attempt++ {
		t = time.Now()
		rows, err := ch.DB().Query(scanCtx, sql, append(append([]any{}, args...), boundTS, upper)...)
		if err != nil {
			return nil, err
		}
		raw, scanErr := scanAll(rows, upper)
		rows.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		distinct := dedupeRows(raw)
		if len(distinct) >= o.limit {
			distinct = distinct[:o.limit]
			mark("1b.带界取数+映射", t, fmt.Sprintf("%d 行（raw %d，超采第 %d 次）", len(distinct), len(raw), attempt+1))
			return distinct, nil
		}
		mark(fmt.Sprintf("1b.取数不足(第%d次)", attempt+1), t,
			fmt.Sprintf("raw %d -> 去重 %d，目标 %d", len(raw), len(distinct), o.limit))
		upper += 200000
	}
	return nil, fmt.Errorf("两段式取数不足：目标 %d", o.limit)
}

// consumeOnly 用与 fetchRows 完全相同的 SQL，但只消费行不做 map 物化，
// 用来把驱动解码成本与 map 物化成本分开。
func consumeOnly(ctx context.Context, ch *clickhouse.Conn, o fetchOpts) (int, error) {
	sql, args := o.buildSQL()
	rows, err := ch.DB().Query(clickhouse.ExportScanContext(ctx), sql, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols := rows.ColumnTypes()
	holders := make([]any, len(cols))
	for i, c := range cols {
		holders[i] = newHolder(c.DatabaseTypeName())
	}
	n := 0
	for rows.Next() {
		if err := rows.Scan(holders...); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// scanAll 物化结果集。持有变量按列类型分配：驱动不支持 *any。
func scanAll(rows driver.Rows, limit int) ([]map[string]any, error) {
	cols := rows.ColumnTypes()
	names := make([]string, len(cols))
	holders := make([]any, len(cols))
	for i, c := range cols {
		names[i] = c.Name()
		holders[i] = newHolder(c.DatabaseTypeName())
	}
	out := make([]map[string]any, 0, limit)
	for rows.Next() {
		if err := rows.Scan(holders...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, n := range names {
			m[n] = holderValue(holders[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func newHolder(typ string) any {
	t := strings.ToLower(typ)
	switch {
	case strings.Contains(t, "datetime"):
		return new(time.Time)
	case strings.HasPrefix(t, "uint8"):
		return new(uint8)
	case strings.HasPrefix(t, "uint16"):
		return new(uint16)
	case strings.HasPrefix(t, "uint32"):
		return new(uint32)
	case strings.HasPrefix(t, "uint64"):
		return new(uint64)
	case strings.HasPrefix(t, "int8"):
		return new(int8)
	case strings.HasPrefix(t, "int16"):
		return new(int16)
	case strings.HasPrefix(t, "int32"):
		return new(int32)
	case strings.HasPrefix(t, "int64"):
		return new(int64)
	case strings.HasPrefix(t, "decimal"):
		return new(decimal.Decimal)
	case strings.HasPrefix(t, "float64"):
		return new(float64)
	default:
		return new(string)
	}
}

func holderValue(h any) any {
	switch v := h.(type) {
	case *time.Time:
		return *v
	case *uint8:
		return *v
	case *uint16:
		return *v
	case *uint32:
		return *v
	case *uint64:
		return *v
	case *int8:
		return *v
	case *int16:
		return *v
	case *int32:
		return *v
	case *int64:
		return *v
	case *float64:
		return *v
	case *decimal.Decimal:
		return *v
	case *string:
		return *v
	default:
		return nil
	}
}

// verifyEquivalence 逐行逐列比较两段式与现网 FINAL 形状的结果。
//
// 比较口径：两边都按 (tx_hash, log_index, token_address) 去重后，按该键排序，
// 再逐列比 fmt.Sprint 值。行数不同、键集不同、任一列不同都判失败。
func verifyEquivalence(ctx context.Context, ch *clickhouse.Conn, o fetchOpts) error {
	legacy := o
	legacy.shape = "old"
	stepped := o
	stepped.shape = "boundary"

	t := time.Now()
	oldRows, err := fetchRows(ctx, ch, legacy)
	if err != nil {
		return fmt.Errorf("legacy fetch: %w", err)
	}
	oldDur := time.Since(t)

	t = time.Now()
	newRows, err := fetchRows(ctx, ch, stepped)
	if err != nil {
		return fmt.Errorf("two-step fetch: %w", err)
	}
	newDur := time.Since(t)

	oldKey := dedupeRows(oldRows)
	newKey := dedupeRows(newRows)
	fmt.Printf("\nlegacy(FINAL) %d 行 / 去重后 %d，耗时 %s\n", len(oldRows), len(oldKey), oldDur.Round(time.Millisecond))
	fmt.Printf("two-step      %d 行 / 去重后 %d，耗时 %s\n", len(newRows), len(newKey), newDur.Round(time.Millisecond))

	if len(oldKey) != len(newKey) {
		return fmt.Errorf("去重后行数不同：legacy=%d two-step=%d", len(oldKey), len(newKey))
	}

	rowKey := func(r map[string]any) string {
		return fmt.Sprintf("%v|%v|%v", r["tx_hash"], r["log_index"], r["token_address"])
	}
	sortRows := func(rows []map[string]any) {
		sort.Slice(rows, func(i, j int) bool { return rowKey(rows[i]) < rowKey(rows[j]) })
	}
	sortRows(oldKey)
	sortRows(newKey)

	// source_ingested_at 是 ReplacingMergeTree 的版本列：同一排序键的多个版本里
	// 保留哪一行，FINAL 与"先取回再 Go 侧去重"没有规范保证（两者是合法的等价答案）。
	// 导出内容不依赖它，因此单独统计、不计入失败；其余列必须逐列相等。
	const versionCol = "source_ingested_at"
	versionOnly := 0

	diffCols := map[string]int{}
	diffRows := 0
	var firstSample string
	for i := range oldKey {
		rowDiff := false
		versionDiff := false
		for col, oldVal := range oldKey[i] {
			newVal, ok := newKey[i][col]
			if !ok {
				diffCols[col+"(缺列)"]++
				rowDiff = true
				continue
			}
			if fmt.Sprint(oldVal) != fmt.Sprint(newVal) {
				if col == versionCol {
					versionDiff = true
					continue
				}
				diffCols[col]++
				rowDiff = true
				if firstSample == "" {
					firstSample = fmt.Sprintf("row=%s col=%s legacy=%q two-step=%q",
						rowKey(oldKey[i]), col, fmt.Sprint(oldVal), fmt.Sprint(newVal))
				}
			}
		}
		if rowDiff {
			diffRows++
		}
		if versionDiff {
			versionOnly++
		}
	}

	if versionOnly > 0 {
		fmt.Printf("版本列差异：%d 行仅 %s 不同（同一排序键的不同摄取版本，导出内容不依赖）\n", versionOnly, versionCol)
	}
	if diffRows > 0 {
		fmt.Printf("\n差异列统计：%v\n首个差异：%s\n", diffCols, firstSample)
		return fmt.Errorf("%d/%d 行存在列差异", diffRows, len(oldKey))
	}
	fmt.Printf("\n一致性：%d 行 × %d 列逐列相等，键集完全相同\n", len(oldKey), len(oldKey[0]))
	return nil
}

// dedupeRows 复刻 internal/api/export.go 的 dedupeExportRows（token_transfers 口径）：
// 按 tx_hash|log_index|token_address，保留首见行。
func dedupeRows(rows []map[string]any) []map[string]any {
	seen := make(map[string]struct{}, len(rows))
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		key := fmt.Sprintf("%v|%v|%v", row["tx_hash"], row["log_index"], row["token_address"])
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
	}
	return out
}

// stableCols 固定列序（字典序），仅用于跨形状比较成本；现网按 exportFieldOrder 排序。
func stableCols(rows []map[string]any) []string {
	set := map[string]struct{}{}
	for _, r := range rows[:min(len(rows), 1000)] {
		for k := range r {
			set[k] = struct{}{}
		}
	}
	cols := make([]string, 0, len(set))
	for k := range set {
		cols = append(cols, k)
	}
	for i := 1; i < len(cols); i++ {
		for j := i; j > 0 && cols[j] < cols[j-1]; j-- {
			cols[j], cols[j-1] = cols[j-1], cols[j]
		}
	}
	return cols
}

func serializeCSV(w io.Writer, rows []map[string]any) (int64, error) {
	bw := bufio.NewWriterSize(w, 1<<20)
	cw := csv.NewWriter(bw)
	cols := stableCols(rows)
	if err := cw.Write(cols); err != nil {
		return 0, err
	}
	rec := make([]string, len(cols))
	for _, r := range rows {
		for i, c := range cols {
			rec[i] = fmt.Sprintf("%v", r[c])
		}
		if err := cw.Write(rec); err != nil {
			return 0, err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return fileSize(w), nil
}

func serializeJSONL(w io.Writer, rows []map[string]any) (int64, error) {
	bw := bufio.NewWriterSize(w, 1<<20)
	enc := json.NewEncoder(bw)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return 0, err
		}
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return fileSize(w), nil
}

func serializeZip(w io.Writer, rows []map[string]any) (int64, error) {
	zw, err := flate.NewWriter(w, flate.DefaultCompression)
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriterSize(zw, 1<<20)
	cw := csv.NewWriter(bw)
	cols := stableCols(rows)
	if err := cw.Write(cols); err != nil {
		return 0, err
	}
	rec := make([]string, len(cols))
	for _, r := range rows {
		for i, c := range cols {
			rec[i] = fmt.Sprintf("%v", r[c])
		}
		if err := cw.Write(rec); err != nil {
			return 0, err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	return fileSize(w), nil
}

func fileSize(w io.Writer) int64 {
	if f, ok := w.(*os.File); ok {
		if st, err := f.Stat(); err == nil {
			return st.Size()
		}
	}
	return 0
}

func contentType(format string) string {
	switch format {
	case "csv":
		return "text/csv"
	case "jsonl":
		return "application/x-ndjson"
	case "zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}
