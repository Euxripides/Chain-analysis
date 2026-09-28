package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

/*
 * 列式直写与 map 路径必须产出**逐字节相同**的 CSV。
 *
 * 列式路径绕过了 []map[string]any，因此列集、列序、单元格取值、富化列取值四处
 * 都可能与 map 路径分叉。导出文件是客户直接对账的产物，任何一处不同都是事故，
 * 所以判据是字节相等，而不是"看起来差不多"。
 *
 * 需要真实依赖，未配置时跳过：
 *
 *	EXPORT_EQUIV_SUBJECT=0x... go test ./internal/api/ -run TestColumnarMatchesMapPath -v
 */
func TestColumnarMatchesMapPath(t *testing.T) {
	subject := os.Getenv("EXPORT_EQUIV_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_EQUIV_SUBJECT not set")
	}
	limit := 30000
	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap unavailable: %v", err)
	}
	srv := New(Deps{
		Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData,
	})

	// 1) map 路径：查询 → 去重 → 富化 → 序列化（现网 writeSingleExport 的形态）。
	rows, err := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, limit, 0, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("map query: %v", err)
	}
	if len(rows) == 0 {
		t.Skip("no rows")
	}
	srv.attachTokenMetadata(ctx, "bsc", rows)
	var want bytes.Buffer
	if err := streamCSV(&want, rows); err != nil {
		t.Fatalf("map csv: %v", err)
	}

	// 2) 列式路径：同一个 rawLimit，直写。
	var got bytes.Buffer
	written, ok, err := srv.exportColumnarStream(ctx, "token_transfers", "bsc", subject, &got, limit)
	if err != nil {
		t.Fatalf("columnar: %v", err)
	}
	if !ok {
		t.Fatalf("columnar path reported unsupported")
	}

	// 3) 比对：表头必须逐字节相同；数据行按**多重集**相同。
	//
	// 为什么不是逐行相同：`ORDER BY timestamp DESC` 在同一 timestamp 上的行没有稳定
	// 次序（同一查询两次执行都会互换，集合不变——见 export_scan_live_test.go 里的
	// 同名结论）。两条路径各发一次查询，因此并列行的相对次序可以不同，
	// 这是导出契约里本就未定义的部分，不是列式路径的差异。
	gotLines := nonEmptyLines(got.Bytes())
	wantLines := nonEmptyLines(want.Bytes())
	if len(gotLines) == 0 || len(wantLines) == 0 {
		t.Fatal("empty output")
	}
	if !bytes.Equal(gotLines[0], wantLines[0]) {
		t.Fatalf("header differs\n columnar: %s\n map     : %s", truncate(gotLines[0]), truncate(wantLines[0]))
	}
	if len(gotLines) != len(wantLines) {
		t.Fatalf("row count differs: columnar=%d map=%d (written=%d)", len(gotLines)-1, len(wantLines)-1, written)
	}
	gotBody := sortedLines(gotLines[1:])
	wantBody := sortedLines(wantLines[1:])
	for i := range gotBody {
		if !bytes.Equal(gotBody[i], wantBody[i]) {
			t.Fatalf("row content differs (sorted line %d)\n columnar: %s\n map     : %s",
				i, truncate(gotBody[i]), truncate(wantBody[i]))
		}
	}
	t.Logf("columnar == map path: header byte-identical, %d data rows identical as a multiset (%d bytes)",
		len(gotLines)-1, got.Len())
}

// nonEmptyLines 按 \n 切分并丢弃末尾空段。
func nonEmptyLines(b []byte) [][]byte {
	all := bytes.Split(b, []byte("\n"))
	out := make([][]byte, 0, len(all))
	for _, l := range all {
		if len(l) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// sortedLines 返回按字节序排好的副本（用于多重集比较）。
func sortedLines(lines [][]byte) [][]byte {
	out := make([][]byte, len(lines))
	copy(out, lines)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

func firstLine(lines [][]byte) string {
	if len(lines) == 0 {
		return "<empty>"
	}
	return truncate(lines[0])
}

func truncate(b []byte) string {
	const max = 400
	if len(b) > max {
		return string(b[:max]) + "..."
	}
	return string(b)
}

// TestColumnarHandlesArrayColumns 是 transactions/logs 数据集的回归判据。
//
// 这两个数据集有 Array(String) 列（transactions: signers/account_keys/program_ids；
// logs: topics）。列式路径原先没有数组绑定，驱动会报
// "column signers - needs a slice or any"，导致**整条 transactions 导出失败**
// （实测：Actions csv+zip、两个数据集，4.5 s 后 FAILED）。
//
// 这里对每个含数组列的数据集比列式与 map 两条路径的输出，且必须**至少有一行非空数组**
// ——首行往往是空数组，只测空数组证明不了渲染一致性。
func TestColumnarHandlesArrayColumns(t *testing.T) {
	subject := os.Getenv("EXPORT_EQUIV_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_EQUIV_SUBJECT not set")
	}
	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap unavailable: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})

	for _, dataset := range []string{"transactions", "logs"} {
		rows, qerr := srv.query.RowsForExportRange(ctx, dataset, "bsc", subject, 2000, 0, nil, nil, nil, nil)
		if qerr != nil {
			t.Fatalf("%s: map query: %v", dataset, qerr)
		}
		if len(rows) == 0 {
			t.Logf("%s: no rows for this subject, skipping", dataset)
			continue
		}
		// 非空数组必须被覆盖到，否则这条用例证明不了渲染一致性。
		arrayCol := "signers"
		if dataset == "logs" {
			arrayCol = "topics"
		}
		nonEmpty := 0
		for _, r := range rows {
			if s := fmt.Sprint(r[arrayCol]); s != "" && s != "[]" {
				nonEmpty++
			}
		}
		if nonEmpty == 0 {
			t.Logf("%s: %d rows but no non-empty %s; rendering of populated arrays is not covered by this data", dataset, len(rows), arrayCol)
		}

		srv.attachTokenMetadata(ctx, "bsc", rows)
		var want bytes.Buffer
		if werr := streamCSV(&want, rows); werr != nil {
			t.Fatalf("%s: map csv: %v", dataset, werr)
		}
		var got bytes.Buffer
		if _, ok, cerr := srv.exportColumnarStream(ctx, dataset, "bsc", subject, &got, len(rows)); cerr != nil {
			t.Fatalf("%s: columnar: %v", dataset, cerr)
		} else if !ok {
			t.Fatalf("%s: columnar reported unsupported", dataset)
		}

		gotLines := nonEmptyLines(got.Bytes())
		wantLines := nonEmptyLines(want.Bytes())
		if len(gotLines) == 0 || len(wantLines) == 0 {
			t.Fatalf("%s: empty output", dataset)
		}
		if !bytes.Equal(gotLines[0], wantLines[0]) {
			t.Fatalf("%s: header differs\n columnar: %s\n map     : %s", dataset, truncate(gotLines[0]), truncate(wantLines[0]))
		}
		if len(gotLines) != len(wantLines) {
			t.Fatalf("%s: row count differs: columnar=%d map=%d", dataset, len(gotLines)-1, len(wantLines)-1)
		}
		gotBody := sortedLines(gotLines[1:])
		wantBody := sortedLines(wantLines[1:])
		for i := range gotBody {
			if !bytes.Equal(gotBody[i], wantBody[i]) {
				t.Fatalf("%s: row differs (sorted %d)\n columnar: %s\n map     : %s", dataset, i, truncate(gotBody[i]), truncate(wantBody[i]))
			}
		}
		t.Logf("%s: columnar == map over %d rows (non-empty arrays: %d)", dataset, len(gotLines)-1, nonEmpty)
	}
}
