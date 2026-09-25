// exportbench：导出链路分段计时探针（点击导出 → 对象可下载）。
//
// 目的：把「查询 / 去重 / 序列化 / 上传 / SHA 回读」五段的真实耗时与内存分开量，
// 用来判断 5 秒端到端预算能否达成、以及该优化哪一段。
//
// 用法：
//
//	go run ./cmd/exportbench -subject 0x... -window 3 -limit 200000
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
	"strings"

	"time"

	"github.com/shopspring/decimal"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

type stage struct {
	name string
	dur  time.Duration
	mem  uint64
	note string
}

var stages []stage

func mark(name string, start time.Time, note string) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	stages = append(stages, stage{name: name, dur: time.Since(start), mem: m.TotalAlloc, note: note})
}

func main() {
	dataset := flag.String("dataset", "token_transfers", "dataset")
	chain := flag.String("chain", "bsc", "chain")
	subject := flag.String("subject", "", "address")
	windowDays := flag.Int("window", 0, "timestamp window in days (0 = unbounded)")
	limit := flag.Int("limit", 200000, "row limit")
	format := flag.String("format", "csv", "csv|jsonl|zip")
	bucket := flag.String("bucket", "", "if set, upload to this MinIO bucket")
	shape := flag.String("shape", "new", "new|old")
	countOnly := flag.Bool("count-only", false, "只数行，不物化 map（量 ClickHouse+驱动基线）")
	skipSerialize := flag.Bool("skip-serialize", false, "跳过序列化（量查询段单独耗时）")
	dedupe := flag.Bool("dedupe", false, "按现网 dedupeExportRows 的口径在 Go 侧去重")
	overFetch := flag.Float64("overfetch", 1.5, "twophase 形状的取数余量倍数")
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

	subjectNorm := addressnorm.NormalizeKey(*chain, *subject)

	if *countOnly {
		t := time.Now()
		n, err := countRows(ctx, ch, *shape, *dataset, *chain, subjectNorm, *windowDays, *limit, *overFetch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "count: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("count-only shape=%s rows=%d elapsed=%s\n", *shape, n, time.Since(t).Round(time.Millisecond))
		return
	}

	// ---- 分段 1：查询 + 去重 + Go 侧映射 ----
	t0 := time.Now()
	rows, err := fetchRows(ctx, ch, *shape, *dataset, *chain, subjectNorm, *windowDays, *limit, *overFetch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		os.Exit(1)
	}
	if *dedupe {
		before := len(rows)
		rows = dedupeRows(rows)
		mark("1a.Go 侧去重", t0, fmt.Sprintf("%d -> %d 行", before, len(rows)))
	}
	mark("1.查询+去重+映射", t0, fmt.Sprintf("%d 行", len(rows)))
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "no rows")
		os.Exit(1)
	}

	if *skipSerialize {
		for _, st := range stages {
			fmt.Printf("%-22s %10s  %s\n", st.name, st.dur.Round(time.Millisecond), st.note)
		}
		return
	}

	// ---- 分段 2：序列化到临时文件 ----
	tmp, err := os.CreateTemp("", "exportbench-*."+*format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tempfile: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmp.Name())
	t1 := time.Now()
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
	mark("2.序列化", t1, fmt.Sprintf("%.1f MiB", float64(written)/1048576))

	// ---- 分段 3：上传（如指定桶）----
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
		t2 := time.Now()
		key := fmt.Sprintf("bench/%d.%s", time.Now().UnixNano(), *format)
		if _, err := client.PutObject(ctx, *bucket, key, tmp, written, minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
			fmt.Fprintf(os.Stderr, "put: %v\n", err)
			os.Exit(1)
		}
		mark("3.上传 MinIO", t2, key)

		// ---- 分段 4：SHA-256 回读（现有实现要求）----
		t3 := time.Now()
		obj, err := client.GetObject(ctx, *bucket, key, minio.GetObjectOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "get: %v\n", err)
			os.Exit(1)
		}
		h := sha256.New()
		n, err := io.Copy(h, obj)
		obj.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sha: %v\n", err)
			os.Exit(1)
		}
		mark("4.SHA256 回读", t3, fmt.Sprintf("%s %d bytes", hex.EncodeToString(h.Sum(nil))[:16], n))
		_ = client.RemoveObject(ctx, *bucket, key, minio.RemoveObjectOptions{})
	}

	// ---- 汇总 ----
	var total time.Duration
	fmt.Printf("\n%-22s %10s %12s  %s\n", "阶段", "耗时", "文件/规模", "备注")
	for _, s := range stages {
		total += s.dur
		fmt.Printf("%-22s %10s %12s  %s\n", s.name, s.dur.Round(time.Millisecond), "", s.note)
	}
	fmt.Printf("%-22s %10s\n", "合计", total.Round(time.Millisecond))
}

// fetchRows 执行导出查询。
//
//	old      = 现网 FINAL 形状
//	dedup    = 显式流式去重（LIMIT 1 BY）
//	activity = address_activity 前缀读（排序键含 timestamp，零排序）
//	block    = token_transfers 按 block_number 倒序（免排序）
func fetchRows(ctx context.Context, ch *clickhouse.Conn, shape, dataset, chain, subject string, windowDays, limit int, overFetch float64) ([]map[string]any, error) {
	sql, args := buildSQL(shape, dataset, chain, subject, windowDays, limit, overFetch)
	args = append(args, limit)
	rows, err := ch.DB().Query(clickhouse.ExportScanContext(ctx), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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

// buildSQL 生成各形状的导出 SQL（fetchRows 与 countRows 共用，保证两者除消费方式外完全一致）。
func buildSQL(shape, dataset, chain, subject string, windowDays, limit int, overFetch float64) (string, []any) {
	if shape == "twophase" {
		return twoPhaseSQL(chain, subject, windowDays, limit, overFetch)
	}
	table := dataset
	if dataset == "internal_transactions" {
		table = "internal_calls"
	}
	cond := "chain = ? AND (from_address = ? OR to_address = ?)"
	args := []any{chain, subject, subject}
	if shape == "activity" {
		table = "address_activity"
		cond = "chain = ? AND address = ?"
		args = []any{chain, subject}
	}
	where := cond
	if windowDays > 0 {
		// 数据是离线摄取的，窗口相对数据最新时间而不是 now()，否则会落空。
		where += fmt.Sprintf(" AND timestamp >= (SELECT max(timestamp) FROM blockchain.%s WHERE chain = '%s') - INTERVAL %d DAY",
			table, strings.ReplaceAll(chain, "'", ""), windowDays)
	}
	key := "tx_hash, log_index, token_address"
	var sql string
	switch shape {
	case "old":
		sql = "SELECT * FROM blockchain." + table + " FINAL WHERE " + where +
			" ORDER BY timestamp DESC LIMIT ?"
	case "activity", "block":
		order := "timestamp DESC"
		if shape == "block" {
			order = "block_number DESC"
		}
		sql = "SELECT * FROM blockchain." + table + " WHERE " + where +
			" ORDER BY " + order + " LIMIT ?"
	default:
		sql = "SELECT * FROM (SELECT * FROM blockchain." + table + " WHERE " + where +
			" ORDER BY source_ingested_at DESC LIMIT 1 BY " + key + ") ORDER BY timestamp DESC LIMIT ?"
	}
	return sql, append(args, limit)
}

// twoPhaseSQL 是"先定位后回表"两段式：第一段只从 address_activity 取
// (tx_hash, log_index, token_address) 键（列少、排序键已含 timestamp，可流式 LIMIT），
// 第二段按键回 token_transfers 取全列。导出列集与现网完全一致。
func twoPhaseSQL(chain, subject string, windowDays, limit int, overFetch float64) (string, []any) {
	keyLimit := int(float64(limit) * overFetch)
	keySQL := "SELECT tx_hash, log_index, token_address FROM blockchain.address_activity " +
		"WHERE chain = ? AND address = ?"
	keyArgs := []any{chain, subject}
	if windowDays > 0 {
		keySQL += fmt.Sprintf(" AND timestamp >= (SELECT max(timestamp) FROM blockchain.address_activity WHERE chain = '%s') - INTERVAL %d DAY",
			strings.ReplaceAll(chain, "'", ""), windowDays)
	}
	keySQL += " ORDER BY timestamp DESC LIMIT ?"
	keyArgs = append(keyArgs, keyLimit)

	full := "SELECT * FROM blockchain.token_transfers WHERE chain = ? AND (tx_hash, log_index, token_address) IN (" +
		keySQL + ") ORDER BY timestamp DESC LIMIT ?"
	args := append([]any{chain}, keyArgs...)
	args = append(args, limit)
	return full, args
}

// dedupeRows 复刻 internal/api/export.go 的 dedupeExportRows（token_transfers 口径：
// tx_hash|log_index|token_address，保留首见行），用来量它自身的代价。
func dedupeRows(rows []map[string]any) []map[string]any {
	seen := make(map[string]struct{}, len(rows))
	out := rows[:0]
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

// countRows 复用与 fetchRows 完全相同的 SQL，但只做行数消费，用来把
// ClickHouse 执行时间与 Go 侧 map 物化/序列化分开。
func countRows(ctx context.Context, ch *clickhouse.Conn, shape, dataset, chain, subject string, windowDays, limit int, overFetch float64) (int, error) {
	sql, args := buildSQL(shape, dataset, chain, subject, windowDays, limit, overFetch)
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

// newHolder 按 ClickHouse 列类型分配扫描目标；驱动不支持 *any。
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

// stableCols 固定列序：现网实现依赖 map 迭代顺序，这里按字典序固定，只用于对比成本。
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
	return countOf(w), nil
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
	return countOf(w), nil
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
	return countOf(w), nil
}

func countOf(w io.Writer) int64 {
	if f, ok := w.(*os.File); ok {
		if st, err := f.Stat(); err == nil {
			return st.Size()
		}
	}
	return 0
}
