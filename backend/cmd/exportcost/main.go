// exportcost：量 Go 侧导出流水线每一段的真实成本，作为优化基线与回归依据。
//
// 与 cmd/exportbench 的分工：exportbench 量「ClickHouse 查询形状」的差别；
// exportcost 量「拿到结果之后」——驱动消费、行物化、去重、序列化——各自的耗时，
// 并可按段 CPU profile，避免凭猜优化。
//
// 两种行物化变体（-variant）：
//
//	json  现网 queryRows/scanOne 的形状：ScanStruct → json.Marshal → json.Decode(UseNumber)
//	map   直接按结果集列构造 map（跳过 JSON 往返），值与 json 路径逐列等价
//
// 用法：
//
//	go run ./cmd/exportcost -subject 0x... -limit 200000 -variant json -cpuprofile /tmp/cpu.prof
package main

import (
	"bufio"
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
	"runtime/pprof"
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

type stageTiming struct {
	name string
	dur  time.Duration
	note string
}

type stages struct{ items []stageTiming }

func (s *stages) mark(name string, start time.Time, note string) {
	s.items = append(s.items, stageTiming{name, time.Since(start), note})
}

// scanner 是本探针需要的驱动能力子集。
type scanner interface{ Scan(dest ...any) error }

func main() {
	subject := flag.String("subject", "", "address")
	chain := flag.String("chain", "bsc", "chain")
	limit := flag.Int("limit", 200000, "row limit")
	overFetchPercent := flag.Int("overfetch", 145, "over-fetch percent (145 = 1.45x)")
	bucket := flag.String("bucket", "", "upload target bucket (empty = skip storage stages)")
	variant := flag.String("variant", "json", "json|map")
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

	subj := addressnorm.NormalizeKey(*chain, *subject)
	overFetch := *limit * *overFetchPercent / 100
	sql := "SELECT * FROM blockchain.token_transfers WHERE chain = ? AND (from_address = ? OR to_address = ?)" +
		" ORDER BY timestamp DESC LIMIT ?"
	args := []any{*chain, subj, subj, overFetch}
	scanCtx := clickhouse.ExportScanContext(ctx)

	var st stages

	// A+B 在同一次结果集上分段计时：驱动持有变量与结果集实例必须配套，
	// 跨查询复用会读到不更新的值（曾导致所有行看起来相同、去重后只剩 1 行）。
	rows, err := ch.DB().Query(scanCtx, sql, args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		os.Exit(1)
	}
	cols := rows.ColumnTypes()
	names := make([]string, len(cols))
	holders := make([]any, len(cols))
	for i, c := range cols {
		names[i] = c.Name()
		holders[i] = newHolder(c.DatabaseTypeName())
	}
	sc := rows.(scanner)

	var apiRows []map[string]any
	if *variant == "map" {
		apiRows = make([]map[string]any, 0, overFetch)
	} else {
		apiRows = make([]map[string]any, 0, 1024)
	}
	raw := make(map[string]any, len(names))
	var buf strings.Builder

	t := time.Now()
	consumeDur := time.Duration(0)
	materializeDur := time.Duration(0)
	count := 0
	for rows.Next() {
		tScan := time.Now()
		if err := sc.Scan(holders...); err != nil {
			fmt.Fprintf(os.Stderr, "scan: %v\n", err)
			os.Exit(1)
		}
		consumeDur += time.Since(tScan)

		tMat := time.Now()
		if *variant == "map" {
			item := make(map[string]any, len(names))
			for i, name := range names {
				item[name] = holderValue(holders[i])
			}
			apiRows = append(apiRows, item)
		} else {
			for i, name := range names {
				raw[name] = holderValue(holders[i])
			}
			buf.Reset()
			if err := json.NewEncoder(&buf).Encode(raw); err != nil {
				fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
				os.Exit(1)
			}
			dec := json.NewDecoder(strings.NewReader(buf.String()))
			dec.UseNumber()
			item := make(map[string]any, len(names))
			if err := dec.Decode(&item); err != nil {
				fmt.Fprintf(os.Stderr, "decode: %v\n", err)
				os.Exit(1)
			}
			apiRows = append(apiRows, item)
		}
		materializeDur += time.Since(tMat)
		count++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "rows: %v\n", err)
		os.Exit(1)
	}
	_ = t
	st.mark("A 驱动消费(Scan)", time.Now().Add(-consumeDur), fmt.Sprintf("%d 行 × %d 列", count, len(cols)))
	st.mark("B 行物化("+*variant+")", time.Now().Add(-materializeDur), fmt.Sprintf("%d 行（A+B 合计 %s）",
		len(apiRows), (consumeDur+materializeDur).Round(time.Millisecond)))

	// C. 去重
	t = time.Now()
	distinct := dedupe(apiRows, *limit)
	st.mark("C 去重", t, fmt.Sprintf("%d -> %d", len(apiRows), len(distinct)))

	// D. CSV 序列化
	tmp, err := os.CreateTemp("", "exportcost-*.csv")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.Remove(tmp.Name())
	t = time.Now()
	if err := writeCSV(tmp, distinct); err != nil {
		fmt.Fprintf(os.Stderr, "csv: %v\n", err)
		os.Exit(1)
	}
	stat, _ := tmp.Stat()
	st.mark("D CSV 序列化", t, fmt.Sprintf("%.1f MiB", float64(stat.Size())/1048576))

	// E/F. 上传与 SHA 回读
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
		key := fmt.Sprintf("bench/cost-%d.csv", time.Now().UnixNano())
		t = time.Now()
		if _, err := client.PutObject(ctx, *bucket, key, tmp, stat.Size(), minio.PutObjectOptions{ContentType: "text/csv"}); err != nil {
			fmt.Fprintf(os.Stderr, "put: %v\n", err)
			os.Exit(1)
		}
		st.mark("E 上传", t, key)

		t = time.Now()
		obj, err := client.GetObject(ctx, *bucket, key, minio.GetObjectOptions{})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		h := sha256.New()
		io.Copy(h, obj)
		obj.Close()
		st.mark("F SHA 回读", t, hex.EncodeToString(h.Sum(nil))[:16])
		client.RemoveObject(ctx, *bucket, key, minio.RemoveObjectOptions{})
	}

	var total time.Duration
	fmt.Printf("\n%-24s %10s  %s\n", "阶段", "耗时", "备注")
	for _, item := range st.items {
		total += item.dur
		fmt.Printf("%-24s %10s  %s\n", item.name, item.dur.Round(time.Millisecond), item.note)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("%-24s %10s  heap=%.0f MiB sys=%.0f MiB\n", "合计", total.Round(time.Millisecond),
		float64(m.HeapAlloc)/1048576, float64(m.Sys)/1048576)
}

func dedupe(rows []map[string]any, limit int) []map[string]any {
	seen := make(map[string]struct{}, len(rows))
	out := rows[:0]
	for _, r := range rows {
		key := fmt.Sprintf("%v|%v|%v", r["tx_hash"], r["log_index"], r["token_address"])
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	if len(out) > limit {
		return out[:limit]
	}
	return out
}

func writeCSV(w io.Writer, rows []map[string]any) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	cw := csv.NewWriter(bw)
	cols := stableCols(rows)
	if err := cw.Write(cols); err != nil {
		return err
	}
	rec := make([]string, len(cols))
	for _, r := range rows {
		for i, c := range cols {
			rec[i] = fmt.Sprintf("%v", r[c])
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return bw.Flush()
}

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
	sort.Strings(cols)
	return cols
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

var _ driver.Rows
