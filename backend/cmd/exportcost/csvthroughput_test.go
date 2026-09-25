package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"math/big"
	"os"
	"strconv"
	"testing"
	"time"
)

// 对比两种 CSV 序列化写法的吞吐：
//
//	A: 现网 streamCSV —— 每个单元格一次 fmt.Sprintf("%v") 生成临时字符串，
//	   再交给 encoding/csv（每字段还要再扫一遍找引号）。
//	B: 直写 —— 值直接 append 进可复用缓冲区，引号判断与写出一次完成。
//
// 输入用 200k 行 × 27 列的合成数据（与真实导出的宽行同数量级）。

func synthRows(n, cols int) []map[string]any {
	rows := make([]map[string]any, n)
	for i := range rows {
		row := make(map[string]any, cols)
		for c := 0; c < cols; c++ {
			switch c % 5 {
			case 0:
				row[fmt.Sprintf("c%d", c)] = fmt.Sprintf("0x%064x", i*31+c)
			case 1:
				row[fmt.Sprintf("c%d", c)] = uint64(i * 1000)
			case 2:
				row[fmt.Sprintf("c%d", c)] = time.Unix(1700000000+int64(i), 0).UTC()
			case 3:
				row[fmt.Sprintf("c%d", c)] = "123456789012345678901234567890"
			default:
				row[fmt.Sprintf("c%d", c)] = nil
			}
		}
		rows[i] = row
	}
	return rows
}

func legacyCSV(w io.Writer, rows []map[string]any) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	cw := csv.NewWriter(bw)
	cols := make([]string, 0, 32)
	for k := range rows[0] {
		cols = append(cols, k)
	}
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

func directCSV(w io.Writer, rows []map[string]any) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	cols := make([]string, 0, 32)
	for k := range rows[0] {
		cols = append(cols, k)
	}
	for i, c := range cols {
		if i > 0 {
			bw.WriteByte(',')
		}
		writeField(bw, c)
	}
	bw.WriteByte('\n')
	buf := make([]byte, 0, 4096)
	for _, r := range rows {
		buf = buf[:0]
		for i, c := range cols {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendValue(buf, r[c])
		}
		buf = append(buf, '\n')
		if _, err := bw.Write(buf); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func appendValue(dst []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return dst
	case string:
		return appendQuoted(dst, x)
	case uint64:
		return strconv.AppendUint(dst, x, 10)
	case uint32:
		return strconv.AppendUint(dst, uint64(x), 10)
	case int64:
		return strconv.AppendInt(dst, x, 10)
	case int:
		return strconv.AppendInt(dst, int64(x), 10)
	case time.Time:
		return append(dst, x.UTC().Format(time.RFC3339Nano)...)
	case *big.Int:
		return append(dst, x.String()...)
	default:
		return appendQuoted(dst, fmt.Sprint(x))
	}
}

func appendQuoted(dst []byte, s string) []byte {
	needQuote := false
	for i := 0; i < len(s); i++ {
		if s[i] == ',' || s[i] == '"' || s[i] == '\n' || s[i] == '\r' {
			needQuote = true
			break
		}
	}
	if !needQuote {
		return append(dst, s...)
	}
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			dst = append(dst, '"')
		}
		dst = append(dst, s[i])
	}
	return append(dst, '"')
}

func writeField(bw *bufio.Writer, s string) {
	needQuote := false
	for i := 0; i < len(s); i++ {
		if s[i] == ',' || s[i] == '"' || s[i] == '\n' || s[i] == '\r' {
			needQuote = true
			break
		}
	}
	if !needQuote {
		bw.WriteString(s)
		return
	}
	bw.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			bw.WriteByte('"')
		}
		bw.WriteByte(s[i])
	}
	bw.WriteByte('"')
}

func TestCSVThroughput(t *testing.T) {
	if os.Getenv("CSVTHROUGHPUT") != "1" {
		t.Skip("set CSVTHROUGHPUT=1 to run")
	}
	rows := synthRows(200000, 27)
	for _, tc := range []struct {
		name string
		fn   func(io.Writer, []map[string]any) error
	}{{"legacy-fmt", legacyCSV}, {"direct-append", directCSV}} {
		start := time.Now()
		if err := tc.fn(io.Discard, rows); err != nil {
			t.Fatal(err)
		}
		t.Logf("%-14s %s", tc.name, time.Since(start).Round(time.Millisecond))
	}
}
