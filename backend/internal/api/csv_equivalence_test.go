package api

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// legacyStreamCSV 是改动前的 streamCSV 实现（fmt.Sprintf + encoding/csv）。
// 保留它作为逐字节参照：导出文件是客户直接对账的产物，序列化换成手写 append 之后
// 必须证明输出完全一致，否则引号规则/换行符的任何差异都会污染所有导出文件。
func legacyStreamCSV(w *bytes.Buffer, rows []map[string]any) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	cw := csv.NewWriter(bw)
	if len(rows) > 0 {
		cols := mapKeys(rows[0])
		if err := cw.Write(cols); err != nil {
			return err
		}
		for _, r := range rows {
			rec := make([]string, len(cols))
			for i, c := range cols {
				rec[i] = fmt.Sprintf("%v", r[c])
			}
			if err := cw.Write(rec); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return bw.Flush()
}

// TestStreamCSVMatchesLegacyByteForByte 是 CSV 序列化改写的判据。
func TestStreamCSVMatchesLegacyByteForByte(t *testing.T) {
	rows := []map[string]any{
		// 覆盖导出里真实出现的取值形态与所有需要转义的字符。
		{
			"chain": "bsc", "tx_hash": "0xabc", "block_number": uint64(121939936),
			"timestamp":  time.Date(2026, 9, 15, 1, 15, 14, 370000000, time.UTC),
			"amount":     decimal.RequireFromString("1.764025991317028396"),
			"amount_raw": "1764025991317028396",
			"empty":      "", "status": uint8(1),
			"with_comma": "a,b", "with_quote": `he said "hi"`,
			"with_newline": "line1\nline2", "with_cr": "line1\r\nline2",
			"json_number": json.Number("12345678901234567890"),
			"big":         big.NewInt(42), "flag": true, "ratio": 0.5,
		},
		{
			"chain": "tron", "tx_hash": "0xdef", "block_number": uint64(7),
			"timestamp": time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC),
			"amount":    decimal.Zero, "amount_raw": "0",
			"empty": "", "status": uint8(0),
			"with_comma": "", "with_quote": `"`,
			"with_newline": "\n", "with_cr": "\r",
			"json_number": json.Number("1"), "big": big.NewInt(-1), "flag": false, "ratio": -1.25,
		},
	}

	var legacy bytes.Buffer
	if err := legacyStreamCSV(&legacy, rows); err != nil {
		t.Fatalf("legacy: %v", err)
	}
	var current bytes.Buffer
	if err := streamCSV(&current, rows); err != nil {
		t.Fatalf("current: %v", err)
	}
	if legacy.String() != current.String() {
		t.Fatalf("CSV output differs\n--- legacy ---\n%q\n--- current ---\n%q", legacy.String(), current.String())
	}
}

// TestStreamCSVWritesNilAsEmptyField 锁定与旧实现的**唯一**行为差异。
//
// 旧实现走 fmt.Sprintf("%v", nil) 会把空值写成字面量 <nil>；新实现写成空字段。
// 后者才是导出该有的样子，但这是行为变更，所以显式断言 + 记录理由：
// 真实数据实测（20 万行 token_transfers 全量、含 token 元数据富化）nil 单元格数为 0，
// 即这条差异当前不可达；它保护的是将来某个 LEFT JOIN 式富化引入空值时，
// 导出文件里出现的会是空字段而不是 <nil>。
func TestStreamCSVWritesNilAsEmptyField(t *testing.T) {
	rows := []map[string]any{{"a": "x", "b": nil}}
	var out bytes.Buffer
	if err := streamCSV(&out, rows); err != nil {
		t.Fatalf("streamCSV: %v", err)
	}
	if got, want := out.String(), "a,b\nx,\n"; got != want {
		t.Fatalf("nil must render as an empty field: got %q, want %q", got, want)
	}
	if bytes.Contains(out.Bytes(), []byte("<nil>")) {
		t.Fatal("output must not contain the literal <nil>")
	}
}

// TestStreamCSVEmptyRows 空行集也必须与旧实现一致（只输出空内容）。
func TestStreamCSVEmptyRows(t *testing.T) {
	var legacy, current bytes.Buffer
	if err := legacyStreamCSV(&legacy, nil); err != nil {
		t.Fatalf("legacy: %v", err)
	}
	if err := streamCSV(&current, nil); err != nil {
		t.Fatalf("current: %v", err)
	}
	if legacy.String() != current.String() {
		t.Fatalf("empty CSV differs: %q vs %q", legacy.String(), current.String())
	}
}
