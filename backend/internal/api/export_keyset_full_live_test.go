package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
)

/*
 * ---- "导出完整吗"的端到端判据 -------------------------------------------------------
 *
 * 这次事故的形态是"状态 COMPLETED、文件能下载、内容却少了 33 万行"。因此判据不能是
 * "链路没报错"，必须是**文件里的行集合与库里的版本键集合逐一对应**：
 *
 *	1. 行数 == distinct 版本键数（不多不少：多了说明跨页重复写，少了说明丢行）
 *	2. 版本键集合 == 库侧 uniqExact（完整性）
 *	3. 页大小被强行调小后结论不变（页边界是键集分页唯一可能出错的地方）
 *
 * 需要真实依赖与真实数据，未配置时跳过：
 *
 *	EXPORT_KEYSET_SUBJECT=0x...            go test ./internal/api/ -run TestKeysetExportIsComplete -v
 *	EXPORT_KEYSET_LARGE=1 EXPORT_CURSOR_SUBJECT=0x...  go test ./internal/api/ -run TestKeysetFullExportAtScale -v
 */
func keysetLiveServer(t *testing.T) (*Server, context.Context) {
	t.Helper()
	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap unavailable: %v", err)
	}
	return New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData}), ctx
}

// subjectKeyCount 返回库侧的原始行数与 distinct 版本键数（导出完整性的唯一真值来源）。
func subjectKeyCount(t *testing.T, srv *Server, ctx context.Context, chain, subject string) (int64, int64) {
	t.Helper()
	var raw, keys uint64
	q := "SELECT count(), uniqExact(tuple(tx_hash, log_index, token_address)) " +
		"FROM blockchain.token_transfers WHERE chain=? AND (from_address=? OR to_address=?)"
	if err := srv.query.CH.DB().QueryRow(ctx, q, chain, subject, subject).Scan(&raw, &keys); err != nil {
		t.Fatalf("库侧计数: %v", err)
	}
	return int64(raw), int64(keys)
}

// csvKeyStats 解析导出的 CSV，返回数据行数与 distinct 版本键数。
func csvKeyStats(t *testing.T, body []byte) (int, int) {
	t.Helper()
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		t.Fatalf("读取表头: %v", err)
	}
	idx := map[string]int{}
	for i, name := range header {
		idx[name] = i
	}
	for _, col := range []string{"tx_hash", "log_index", "token_address"} {
		if _, ok := idx[col]; !ok {
			t.Fatalf("导出缺少列 %q：表头=%v", col, header)
		}
	}
	keys := map[string]struct{}{}
	rows := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("第 %d 行: %v", rows+2, err)
		}
		rows++
		keys[rec[idx["tx_hash"]]+"|"+rec[idx["log_index"]]+"|"+rec[idx["token_address"]]] = struct{}{}
	}
	return rows, len(keys)
}

// lineCounter 只数行数与字节数，不保存内容（用于 GB 级全量导出的完整性判据）。
// 与 export_stream.go 的 countingWriter 区分开：那个只数字节（CRC/大小用）。
type lineCounter struct {
	n     int64
	lines int64
}

func (w *lineCounter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	w.lines += int64(bytes.Count(p, []byte{'\n'}))
	return len(p), nil
}

func TestKeysetExportIsComplete(t *testing.T) {
	subject := os.Getenv("EXPORT_KEYSET_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_KEYSET_SUBJECT not set")
	}
	srv, ctx := keysetLiveServer(t)
	raw, wantKeys := subjectKeyCount(t, srv, ctx, "bsc", subject)
	t.Logf("库侧：原始行=%d，distinct 版本键=%d", raw, wantKeys)
	if wantKeys == 0 {
		t.Skip("该地址没有 token_transfers 数据")
	}

	// 1) 生产入口全量导出，页大小保持默认。
	var full bytes.Buffer
	start := time.Now()
	written, ok, err := srv.exportColumnarStreamOpts(ctx, "token_transfers", "bsc", subject, &full,
		columnarStreamOpts{Limit: 0})
	if err != nil {
		t.Fatalf("全量导出: %v", err)
	}
	if !ok {
		t.Fatal("列式路径报告不支持")
	}
	elapsed := time.Since(start)
	rows, keys := csvKeyStats(t, full.Bytes())
	t.Logf("全量导出：%d 行 / %d 键 / %d 字节 / %v", rows, keys, full.Len(), elapsed.Round(time.Millisecond))
	if int64(rows) != written {
		t.Fatalf("写出计数 %d 与文件行数 %d 不一致", written, rows)
	}
	if int64(keys) != wantKeys {
		t.Fatalf("导出键数 %d != 库侧 distinct 键 %d（丢行或重复写）", keys, wantKeys)
	}
	if rows != keys {
		t.Fatalf("文件里有 %d 行但只有 %d 个版本键：跨页去重失效", rows, keys)
	}

	// 2) 页大小强行调小：同一条路径、同一份数据，结论必须不变。
	//    页边界正是"游标取原始行 + seen 跨页延续"唯一可能出错的地方。
	restore := query.SetExportPageRowsForTest(2_000)
	var paged bytes.Buffer
	pagedStart := time.Now()
	pagedRows, ok, err := srv.exportColumnarStreamOpts(ctx, "token_transfers", "bsc", subject, &paged,
		columnarStreamOpts{Limit: 0})
	restore()
	if err != nil {
		t.Fatalf("小页导出: %v", err)
	}
	if !ok {
		t.Fatal("小页导出：列式路径报告不支持")
	}
	pagedN, pagedKeys := csvKeyStats(t, paged.Bytes())
	t.Logf("小页导出（%d 行/页）：%d 行 / %d 键 / %v", 2000, pagedN, pagedKeys, time.Since(pagedStart).Round(time.Millisecond))
	if pagedRows != written || pagedN != rows || pagedKeys != keys {
		t.Fatalf("页大小改变后结果不一致：默认(%d 行/%d 键) vs 小页(%d 行/%d 键)",
			rows, keys, pagedN, pagedKeys)
	}

	// 3) 时间范围导出：行集合必须等于"该窗口内的 distinct 版本键"，而不是全量的一部分。
	//    取全量里最早与最晚时间之间的一段，确保窗口内确实有数据。
	var minTS, maxTS time.Time
	if err := srv.query.CH.DB().QueryRow(ctx,
		"SELECT min(timestamp), max(timestamp) FROM blockchain.token_transfers WHERE chain=? AND (from_address=? OR to_address=?)",
		"bsc", subject, subject).Scan(&minTS, &maxTS); err != nil {
		t.Fatalf("时间范围: %v", err)
	}
	if maxTS.Sub(minTS) < 48*time.Hour {
		t.Skipf("时间跨度不足，跳过范围用例：%v", maxTS.Sub(minTS))
	}
	// 数据可能在时间上不连续（这张表按"该地址实际有转账的时段"分布），
	// 因此候选窗口要在整个跨度上取几段试，取到第一段有数据的。
	span := maxTS.Sub(minTS)
	var from, to time.Time
	var windowKeys uint64
	for _, frac := range []float64{0.25, 0.5, 0.75, 0.05, 0.95} {
		start := minTS.Add(time.Duration(float64(span) * frac))
		candidateTo := start.Add(12 * time.Hour)
		if candidateTo.After(maxTS) {
			candidateTo = maxTS
		}
		var n uint64
		if err := srv.query.CH.DB().QueryRow(ctx,
			"SELECT uniqExact(tuple(tx_hash, log_index, token_address)) FROM blockchain.token_transfers "+
				"WHERE chain=? AND (from_address=? OR to_address=?) AND timestamp>=? AND timestamp<=?",
			"bsc", subject, subject, start, candidateTo).Scan(&n); err != nil {
			t.Fatalf("窗口计数: %v", err)
		}
		if n > 0 {
			from, to, windowKeys = start, candidateTo, n
			break
		}
	}
	if windowKeys == 0 {
		t.Skip("候选窗口内都没有数据，跳过范围用例")
	}
	var ranged bytes.Buffer
	rangedRows, ok, err := srv.exportColumnarStreamOpts(ctx, "token_transfers", "bsc", subject, &ranged,
		columnarStreamOpts{Limit: 0, Bounds: exportBounds{From: &from, To: &to}})
	if err != nil {
		t.Fatalf("范围导出: %v", err)
	}
	if !ok {
		t.Fatal("范围导出：列式路径报告不支持")
	}
	rangeRows, rangeKeyN := csvKeyStats(t, ranged.Bytes())
	t.Logf("范围导出 [%s, %s]：%d 行 / %d 键（库侧 %d 键）",
		from.Format(time.RFC3339), to.Format(time.RFC3339), rangeRows, rangeKeyN, windowKeys)
	if int64(rangeRows) != rangedRows {
		t.Fatalf("范围导出写出计数 %d 与文件行数 %d 不一致", rangedRows, rangeRows)
	}
	if uint64(rangeKeyN) != windowKeys {
		t.Fatalf("范围导出键数 %d != 库侧窗口内键数 %d", rangeKeyN, windowKeys)
	}
	if rangeRows >= rows {
		t.Fatalf("范围导出行数 %d 不应达到全量 %d（时间范围没生效）", rangeRows, rows)
	}
}

// TestKeysetFullExportAtScale 用百万行级地址验证"全量导出不再被截断"。
//
// 判据只有一条：导出的数据行数 == 库侧 distinct 版本键数。老的 2 百万行上限
// 正是在这个地址上把最老的 331,094 行悄悄丢掉（导出 2,000,000 / 实际 2,331,094）。
func TestKeysetFullExportAtScale(t *testing.T) {
	if os.Getenv("EXPORT_KEYSET_LARGE") != "1" {
		t.Skip("EXPORT_KEYSET_LARGE != 1")
	}
	subject := os.Getenv("EXPORT_CURSOR_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_CURSOR_SUBJECT not set")
	}
	srv, ctx := keysetLiveServer(t)
	raw, wantKeys := subjectKeyCount(t, srv, ctx, "bsc", subject)
	t.Logf("库侧：原始行=%d，distinct 版本键=%d", raw, wantKeys)
	if wantKeys <= 2_000_000 {
		t.Fatalf("该地址只有 %d 个版本键，无法验证「超过 200 万仍不截断」", wantKeys)
	}

	w := &lineCounter{}
	start := time.Now()
	written, ok, err := srv.exportColumnarStreamOpts(ctx, "token_transfers", "bsc", subject, w,
		columnarStreamOpts{Limit: 0})
	if err != nil {
		t.Fatalf("全量导出: %v", err)
	}
	if !ok {
		t.Fatal("列式路径报告不支持")
	}
	elapsed := time.Since(start)
	dataLines := w.lines - 1 // 去掉表头
	t.Logf("全量导出：%d 行 / %d 字节 / %v", dataLines, w.n, elapsed.Round(time.Millisecond))
	if written != dataLines {
		t.Fatalf("写出计数 %d != 文件数据行数 %d", written, dataLines)
	}
	if dataLines != wantKeys {
		t.Fatalf("导出行数 %d != 库侧 distinct 版本键 %d（差 %d）", dataLines, wantKeys, wantKeys-dataLines)
	}
	if strings.Contains(fmt.Sprint(dataLines), "2000000") {
		t.Fatal("行数恰好落在老上限上，说明截断仍在生效")
	}
}
