package api

import (
	"bytes"
	"context"
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
