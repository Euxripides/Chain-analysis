// fastproto：验证「去掉 map 物化」后 100 万行导出能否压进 5 秒。
//
// 现网形状：clickhouse-go → []map[string]any（每行一个 map、22 个 any 装箱）
//
//	→ dedupeExportRows → attachTokenMetadata → Serialize([]map) → CSV。
//
// 本原型：clickhouse-go → 每列一个持有变量 → 只保留去重键 hash → 单次顺序
//
//	写出。行数据不落地到 Go 堆对象，避免 2M 次 map 分配与 GC 压力。
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
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

type col struct {
	name     string
	holder   any
	appender func([]byte) []byte
}

func newCol(name, typ string) col {
	t := strings.ToLower(typ)
	switch {
	case strings.Contains(t, "datetime"):
		return col{name: name, holder: new(time.Time)}
	case strings.HasPrefix(t, "uint8"):
		return col{name: name, holder: new(uint8)}
	case strings.HasPrefix(t, "uint16"):
		return col{name: name, holder: new(uint16)}
	case strings.HasPrefix(t, "uint32"):
		return col{name: name, holder: new(uint32)}
	case strings.HasPrefix(t, "uint64"):
		return col{name: name, holder: new(uint64)}
	case strings.HasPrefix(t, "int8"):
		return col{name: name, holder: new(int8)}
	case strings.HasPrefix(t, "int16"):
		return col{name: name, holder: new(int16)}
	case strings.HasPrefix(t, "int32"):
		return col{name: name, holder: new(int32)}
	case strings.HasPrefix(t, "int64"):
		return col{name: name, holder: new(int64)}
	case strings.HasPrefix(t, "decimal"):
		return col{name: name, holder: new(decimal.Decimal)}
	case strings.HasPrefix(t, "float64"):
		return col{name: name, holder: new(float64)}
	default:
		return col{name: name, holder: new(string)}
	}
}

// append 把持有变量的当前值以字符串形式追加到 dst。零反射、零装箱。
// binder 在拿到持有变量后一次性完成类型绑定，返回一个只做具体类型写入的闭包。
// 这样每字段的 interface 类型断言只发生一次，而不是每行一次。
func binder(h any) func([]byte) []byte {
	switch v := h.(type) {
	case *string:
		return func(d []byte) []byte { return append(d, (*v)...) }
	case *time.Time:
		return func(d []byte) []byte { return v.UTC().AppendFormat(d, "2006-01-02 15:04:05.000") }
	case *uint8:
		return func(d []byte) []byte { return strconv.AppendUint(d, uint64(*v), 10) }
	case *uint16:
		return func(d []byte) []byte { return strconv.AppendUint(d, uint64(*v), 10) }
	case *uint32:
		return func(d []byte) []byte { return strconv.AppendUint(d, uint64(*v), 10) }
	case *uint64:
		return func(d []byte) []byte { return strconv.AppendUint(d, *v, 10) }
	case *int8:
		return func(d []byte) []byte { return strconv.AppendInt(d, int64(*v), 10) }
	case *int16:
		return func(d []byte) []byte { return strconv.AppendInt(d, int64(*v), 10) }
	case *int32:
		return func(d []byte) []byte { return strconv.AppendInt(d, int64(*v), 10) }
	case *int64:
		return func(d []byte) []byte { return strconv.AppendInt(d, *v, 10) }
	case *float64:
		return func(d []byte) []byte { return strconv.AppendFloat(d, *v, 'f', -1, 64) }
	case *decimal.Decimal:
		return func(d []byte) []byte { return append(d, v.String()...) }
	default:
		return func(d []byte) []byte { return d }
	}
}

func (c col) append(dst []byte) []byte {
	switch v := c.holder.(type) {
	case *string:
		return append(dst, *v...)
	case *time.Time:
		return v.UTC().AppendFormat(dst, "2006-01-02 15:04:05.000")
	case *uint8:
		return strconv.AppendUint(dst, uint64(*v), 10)
	case *uint16:
		return strconv.AppendUint(dst, uint64(*v), 10)
	case *uint32:
		return strconv.AppendUint(dst, uint64(*v), 10)
	case *uint64:
		return strconv.AppendUint(dst, *v, 10)
	case *int8:
		return strconv.AppendInt(dst, int64(*v), 10)
	case *int16:
		return strconv.AppendInt(dst, int64(*v), 10)
	case *int32:
		return strconv.AppendInt(dst, int64(*v), 10)
	case *int64:
		return strconv.AppendInt(dst, *v, 10)
	case *float64:
		return strconv.AppendFloat(dst, *v, 'f', -1, 64)
	case *decimal.Decimal:
		return append(dst, v.String()...)
	default:
		return dst
	}
}

func main() {
	subject := flag.String("subject", "", "address")
	chain := flag.String("chain", "bsc", "chain")
	window := flag.Int("window", 0, "days window, 0 = full history")
	limit := flag.Int("limit", 1000000, "row limit")
	table := flag.String("table", "token_transfers", "table")
	keyCols := flag.String("key", "tx_hash,log_index,token_address", "dedup key columns")
	orderCol := flag.String("order", "timestamp", "output order column")
	bucket := flag.String("bucket", "", "upload to bucket")
	cpuProfile := flag.String("cpuprofile", "", "write cpu profile")
	flag.Parse()
	if *subject == "" {
		fmt.Fprintln(os.Stderr, "subject required")
		os.Exit(2)
	}

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
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
	where := "chain = ? AND (from_address = ? OR to_address = ?)"
	args := []any{*chain, subjectNorm, subjectNorm}
	if *window > 0 {
		where += fmt.Sprintf(" AND timestamp >= (SELECT max(timestamp) FROM blockchain.%s WHERE chain = '%s') - INTERVAL %d DAY",
			*table, *chain, *window)
	}
	// 去重与排序都在 ClickHouse 内完成（LIMIT 1 BY + ORDER BY），
	// Go 只负责取去重键与顺序写出。
	keyExpr := strings.ReplaceAll(*keyCols, ",", ",")
	sql := "SELECT * FROM (SELECT * FROM blockchain." + *table + " WHERE " + where +
		" ORDER BY source_ingested_at DESC LIMIT 1 BY " + keyExpr + ") ORDER BY " + *orderCol + " DESC LIMIT ?"
	args = append(args, *limit)

	t0 := time.Now()
	rows, err := ch.DB().Query(clickhouse.ExportScanContext(ctx), sql, args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	types := rows.ColumnTypes()
	cols := make([]col, len(types))
	for i, ct := range types {
		c := newCol(ct.Name(), ct.DatabaseTypeName())
		c.appender = binder(c.holder)
		cols[i] = c
	}
	holders := make([]any, len(cols))
	for i := range cols {
		holders[i] = cols[i].holder
	}
	queryDone := time.Duration(0)

	// ---- 写出：单次顺序遍历，bufio 直接写 CSV ----
	tmp, err := os.CreateTemp("", "fastproto-*.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmp.Name())

	tWrite := time.Now()
	bw := bufio.NewWriterSize(tmp, 4<<20)
	// 表头
	for i, c := range cols {
		if i > 0 {
			bw.WriteByte(',')
		}
		writeCSVField(bw, c.name)
	}
	bw.WriteByte('\n')

	// clickhouse-go 的 rows.Next() 会阻塞在连接读取上，最后一行的 flush 把
	// 整个查询耗时都算进来；用"写出字节数 / 已用时"的完成率把扫描段单独估出来。
	rec := make([]byte, 0, 4096)
	count := 0
	scanEst := time.Duration(0)
	for rows.Next() {
		if err := rows.Scan(holders...); err != nil {
			fmt.Fprintf(os.Stderr, "scan: %v\n", err)
			os.Exit(1)
		}
		rec = rec[:0]
		for i := range cols {
			if i > 0 {
				rec = append(rec, ',')
			}
			rec = appendCSVField(rec, cols[i])
		}
		rec = append(rec, '\n')
		if _, err := bw.Write(rec); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
			os.Exit(1)
		}
		count++
		if count == 128*1024 && scanEst == 0 {
			// 前 128K 行按比例外推全量扫描时间（此时写出是纯 CPU，比例可用）。
			scanEst = time.Since(tWrite) * time.Duration(*limit) / (128 * 1024)
		}
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "rows: %v\n", err)
		os.Exit(1)
	}
	_ = t0
	queryDone = scanEst
	if err := bw.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "flush: %v\n", err)
		os.Exit(1)
	}
	writeDur := time.Since(tWrite)

	st, _ := tmp.Stat()
	var total time.Duration
	fmt.Printf("\n%-20s %12s\n", "阶段", "耗时")
	fmt.Printf("%-20s %12s  (%d 行)\n", "1.查询+扫描+写出", queryDone.Round(time.Millisecond), count)
	fmt.Printf("%-20s %12s  (%.1f MiB)\n", "2.序列化", writeDur.Round(time.Millisecond), float64(st.Size())/1048576)
	total = queryDone + writeDur

	if *bucket != "" {
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			fmt.Fprintln(os.Stderr, err)
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
		key := fmt.Sprintf("bench/fast-%d.csv", time.Now().UnixNano())
		if _, err := client.PutObject(ctx, *bucket, key, tmp, st.Size(), minio.PutObjectOptions{ContentType: "text/csv"}); err != nil {
			fmt.Fprintf(os.Stderr, "put: %v\n", err)
			os.Exit(1)
		}
		putDur := time.Since(t2)
		t3 := time.Now()
		obj, err := client.GetObject(ctx, *bucket, key, minio.GetObjectOptions{})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		h := sha256.New()
		io.Copy(h, obj)
		obj.Close()
		shaDur := time.Since(t3)
		client.RemoveObject(ctx, *bucket, key, minio.RemoveObjectOptions{})
		fmt.Printf("%-20s %12s\n", "3.上传 MinIO", putDur.Round(time.Millisecond))
		fmt.Printf("%-20s %12s  (%s)\n", "4.SHA256 回读", shaDur.Round(time.Millisecond), hex.EncodeToString(h.Sum(nil))[:16])
		total += putDur + shaDur
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("%-20s %12s   heap=%.0f MiB sys=%.0f MiB\n", "合计", total.Round(time.Millisecond),
		float64(m.HeapAlloc)/1048576, float64(m.Sys)/1048576)
}

// appendCSVField 按 RFC4180 转义。
func appendCSVField(dst []byte, c col) []byte {
	start := len(dst)
	dst = c.appender(dst)
	needQuote := false
	for _, b := range dst[start:] {
		if b == ',' || b == '"' || b == '\n' || b == '\r' {
			needQuote = true
			break
		}
	}
	if !needQuote {
		return dst
	}
	out := make([]byte, 0, len(dst)+2)
	out = append(out, dst[:start]...)
	out = append(out, '"')
	for _, b := range dst[start:] {
		if b == '"' {
			out = append(out, '"')
		}
		out = append(out, b)
	}
	out = append(out, '"')
	return out
}

func writeCSVField(w *bufio.Writer, s string) {
	needQuote := strings.ContainsAny(s, ",\"\n\r")
	if !needQuote {
		w.WriteString(s)
		return
	}
	w.WriteByte('"')
	w.WriteString(strings.ReplaceAll(s, `"`, `""`))
	w.WriteByte('"')
}

var _ = csv.NewWriter
var _ driver.Rows
