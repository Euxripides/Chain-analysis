package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

// TestCursorPaginationProbe 验证"按时间戳游标分块取完整个地址"既不丢行也不重复。
//
// 这是要把导出从"一次 LIMIT 2,000,000（超出静默截断）"改成"游标取全量"的前置证据：
// 必须证明分块边界处没有遗漏或重复，否则改动会把静默截断换成静默丢行——更糟。
func TestCursorPaginationProbe(t *testing.T) {
	subject := os.Getenv("EXPORT_CURSOR_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_CURSOR_SUBJECT not set")
	}
	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})
	conn := srv.query.CH.DB()

	const chunk = 500_000
	keys := map[string]struct{}{}
	chunks := 0
	var cursor time.Time

	// 只投影去重键 + 时间戳：原型关心的是"行集是否完整"，不是列内容。
	projection := "SELECT tx_hash, log_index, token_address, timestamp FROM blockchain.token_transfers"
	start := time.Now()
	for {
		where := "chain=? AND (from_address=? OR to_address=?)"
		args := []any{"bsc", subject, subject}
		if !cursor.IsZero() {
			where += " AND timestamp < ?"
			args = append(args, cursor)
		}
		args = append(args, chunk)
		q := projection + " WHERE " + where + " ORDER BY timestamp DESC LIMIT ?"
		rows, qerr := conn.Query(ctx, q, args...)
		if qerr != nil {
			t.Fatalf("chunk %d query: %v", chunks, qerr)
		}
		n := 0
		var lastTS time.Time
		first := true
		for rows.Next() {
			var txHash, tokenAddr string
			var logIndex uint32
			var ts time.Time
			if serr := rows.Scan(&txHash, &logIndex, &tokenAddr, &ts); serr != nil {
				t.Fatalf("chunk %d scan: %v", chunks, serr)
			}
			keys[fmt.Sprintf("%s|%d|%s", txHash, logIndex, tokenAddr)] = struct{}{}
			lastTS = ts
			if first {
				first = false
			}
			n++
		}
		rows.Close()
		chunks++
		if n < chunk {
			break
		}
		cursor = lastTS
		if chunks > 20 {
			t.Fatalf("too many chunks (%d); cursor likely not advancing", chunks)
		}
	}
	t.Logf("游标分页：%d 块 / %d 个不同键 / 总耗时 %s", chunks, len(keys), time.Since(start).Round(time.Millisecond))
}

// TestCursorPaginationIsLosslessAgainstClickHouse 用**库侧计数**核对游标分页是否无损。
//
// 上一条用例只报"探针数到多少个键"，无法区分"探针丢了行"和"探针数错"。
// 这里让 ClickHouse 直接算两个数：
//
//	A) 全量 distinct 键数
//	B) 按游标切分后每段 distinct 键数之和
//
// 若 B == A，则"timestamp < cursor"这套切分在真实数据上无损——包括并列时间戳的情况。
func TestCursorPaginationIsLosslessAgainstClickHouse(t *testing.T) {
	subject := os.Getenv("EXPORT_CURSOR_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_CURSOR_SUBJECT not set")
	}
	ctx := context.Background()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})
	conn := srv.query.CH.DB()

	const chunk = 500_000
	base := "FROM blockchain.token_transfers WHERE chain='bsc' AND (from_address=? OR to_address=?)"

	var total uint64
	totalSQL := "SELECT uniqExact(tuple(tx_hash,log_index,token_address)) " + base
	t.Logf("totalSQL=%s subject=%q", totalSQL, subject)
	if err := conn.QueryRow(ctx, totalSQL, "bsc", subject, subject).Scan(&total); err != nil {
		t.Fatalf("total: %v", err)
	}
	// 交叉核对：同一个地址、同样的谓词，用 count() 与 uniqExact 各算一次。
	var rawCount uint64
	if err := conn.QueryRow(ctx, "SELECT count() "+base, "bsc", subject, subject).Scan(&rawCount); err != nil {
		t.Fatalf("raw count: %v", err)
	}
	t.Logf("交叉核对：raw count()=%d, uniqExact=%d", rawCount, total)

	sum := uint64(0)
	chunks := 0
	var cursor time.Time
	for {
		where := base
		args := []any{"bsc", subject, subject}
		if !cursor.IsZero() {
			where += " AND timestamp < ?"
			args = append(args, cursor)
		}
		// 每段取 chunk 行，并在库侧数这一段的 distinct 键
		sub := "SELECT tx_hash, log_index, token_address, timestamp " + where + " ORDER BY timestamp DESC LIMIT " + fmt.Sprint(chunk)
		var segKeys uint64
		q := "SELECT uniqExact(tuple(tx_hash,log_index,token_address)), max(timestamp), count() FROM (" + sub + ")"
		var maxTS time.Time
		var segRows uint64
		if err := conn.QueryRow(ctx, q, args...).Scan(&segKeys, &maxTS, &segRows); err != nil {
			t.Fatalf("chunk %d: %v", chunks, err)
		}
		sum += segKeys
		chunks++
		if segRows < chunk {
			break
		}
		cursor = maxTS
		if chunks > 20 {
			t.Fatal("cursor not advancing")
		}
	}
	t.Logf("全量 distinct 键=%d；分 %d 段求和=%d；差=%d", total, chunks, sum, total-sum)
	if sum != total {
		t.Fatalf("cursor pagination lost %d keys", total-sum)
	}
}

// TestCursorVsSingleQuerySameDriver 用**同一个驱动**量两个数：
//
//	A) 一次无界查询扫出的 distinct 键
//	B) 游标分块扫出的 distinct 键
//
// A==B 才说明分块无损。上一条用例试图用"库侧单看总数"的做法，但同一条聚合 SQL
// 在驱动里与 clickhouse-client 里给出了不同的数（97971 vs 2665383）——那是探针自身
// 的写法问题，不能用来判定分页；同驱动对比才是有意义的判据。
func TestCursorVsSingleQuerySameDriver(t *testing.T) {
	subject := os.Getenv("EXPORT_CURSOR_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_CURSOR_SUBJECT not set")
	}
	ctx := context.Background()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})
	conn := srv.query.CH.DB()

	collect := func(where string, extra ...any) int {
		args := append([]any{"bsc", subject, subject}, extra...)
		q := "SELECT tx_hash, log_index, token_address, timestamp FROM blockchain.token_transfers WHERE " +
			"chain=? AND (from_address=? OR to_address=?)" + where + " ORDER BY timestamp DESC"
		rows, qerr := conn.Query(ctx, q, args...)
		if qerr != nil {
			t.Fatalf("query: %v", qerr)
		}
		defer rows.Close()
		keys := map[string]struct{}{}
		for rows.Next() {
			var txHash, tokenAddr string
			var logIndex uint32
			var ts time.Time
			if serr := rows.Scan(&txHash, &logIndex, &tokenAddr, &ts); serr != nil {
				t.Fatalf("scan: %v", serr)
			}
			keys[fmt.Sprintf("%s|%d|%s", txHash, logIndex, tokenAddr)] = struct{}{}
		}
		return len(keys)
	}

	single := collect("")
	t.Logf("一次无界查询：%d 个不同键", single)

	const chunk = 500_000
	keys := map[string]struct{}{}
	var cursor time.Time
	chunks := 0
	for {
		where := ""
		var extra []any
		if !cursor.IsZero() {
			where = " AND timestamp < ?"
			extra = append(extra, cursor)
		}
		args := append([]any{"bsc", subject, subject}, extra...)
		args = append(args, chunk)
		q := "SELECT tx_hash, log_index, token_address, timestamp FROM blockchain.token_transfers WHERE " +
			"chain=? AND (from_address=? OR to_address=?)" + where + " ORDER BY timestamp DESC LIMIT ?"
		rows, qerr := conn.Query(ctx, q, args...)
		if qerr != nil {
			t.Fatalf("chunk %d: %v", chunks, qerr)
		}
		n := 0
		var lastTS time.Time
		for rows.Next() {
			var txHash, tokenAddr string
			var logIndex uint32
			var ts time.Time
			if serr := rows.Scan(&txHash, &logIndex, &tokenAddr, &ts); serr != nil {
				t.Fatalf("chunk %d scan: %v", chunks, serr)
			}
			keys[fmt.Sprintf("%s|%d|%s", txHash, logIndex, tokenAddr)] = struct{}{}
			lastTS = ts
			n++
		}
		rows.Close()
		chunks++
		if n < chunk {
			break
		}
		cursor = lastTS
		if chunks > 20 {
			t.Fatal("cursor not advancing")
		}
	}
	t.Logf("游标分块：%d 块 / %d 个不同键", chunks, len(keys))
	if len(keys) != single {
		t.Fatalf("cursor pagination lost %d keys (%d vs %d)", single-len(keys), single, len(keys))
	}
}

// TestCursorPaginationIntegerCursorIsLossless 用**整数毫秒游标**替代 time.Time 游标。
//
// 前一条用例发现 time.Time 游标会丢 64 个键。怀疑点：把 time.Time 当参数回传时，
// 驱动/服务端的时区与精度转换可能与列里的值不一致，导致 `timestamp < ?` 少算一段。
// 这里改用 toUnixTimestamp64Milli(timestamp) 做游标（整数比较，无时区、无精度歧义），
// 并用同一个驱动做一次无界查询作基准，两者必须完全一致。
func TestCursorPaginationIntegerCursorIsLossless(t *testing.T) {
	subject := os.Getenv("EXPORT_CURSOR_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_CURSOR_SUBJECT not set")
	}
	ctx := context.Background()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})
	conn := srv.query.CH.DB()

	base := "chain=? AND (from_address=? OR to_address=?)"
	proj := "SELECT tx_hash, log_index, token_address, toUnixTimestamp64Milli(timestamp) AS ms FROM blockchain.token_transfers WHERE "

	collect := func(rows interface {
		Next() bool
		Scan(...any) error
		Close() error
	}, keys map[string]struct{}) (int, int64) {
		n := 0
		var lastMS int64
		for rows.Next() {
			var txHash, tokenAddr string
			var logIndex uint32
			var ms int64
			if serr := rows.Scan(&txHash, &logIndex, &tokenAddr, &ms); serr != nil {
				t.Fatalf("scan: %v", serr)
			}
			keys[fmt.Sprintf("%s|%d|%s", txHash, logIndex, tokenAddr)] = struct{}{}
			lastMS = ms
			n++
		}
		return n, lastMS
	}

	// 基准：一次无界查询
	rows, qerr := conn.Query(ctx, proj+base+" ORDER BY ms DESC", "bsc", subject, subject)
	if qerr != nil {
		t.Fatalf("baseline: %v", qerr)
	}
	baselineKeys := map[string]struct{}{}
	baselineN, _ := collect(rows, baselineKeys)
	rows.Close()
	t.Logf("基准（一次查询）：%d 行 / %d 个不同键", baselineN, len(baselineKeys))

	// 游标分块：整数毫秒游标
	const chunk = 500_000
	keys := map[string]struct{}{}
	chunks := 0
	var cursor int64
	for {
		where := base
		args := []any{"bsc", subject, subject}
		if chunks > 0 {
			where += " AND ms < ?"
			args = append(args, cursor)
		}
		args = append(args, chunk)
		r, qe := conn.Query(ctx, proj+where+" ORDER BY ms DESC LIMIT ?", args...)
		if qe != nil {
			t.Fatalf("chunk %d: %v", chunks, qe)
		}
		n, lastMS := collect(r, keys)
		r.Close()
		chunks++
		if n < chunk {
			break
		}
		if lastMS == cursor {
			t.Fatalf("cursor did not advance at chunk %d (ms=%d)", chunks, cursor)
		}
		cursor = lastMS
		if chunks > 20 {
			t.Fatal("too many chunks")
		}
	}
	t.Logf("整数游标分块：%d 块 / %d 个不同键", chunks, len(keys))
	if len(keys) != len(baselineKeys) {
		t.Fatalf("integer cursor lost %d keys (%d vs %d)", len(baselineKeys)-len(keys), len(baselineKeys), len(keys))
	}
}

// TestCursorKeysetByFullSortKeyIsLossless 用**完整排序键**做 keyset 分页。
//
// 前两条用例（time.Time 游标、整数毫秒游标）都丢了 64 个键，而并列行最多只有 153 行，
// 排除"切点落在并列组中间"。根因是 `ORDER BY timestamp DESC` 对同一时间戳的行**次序
// 不确定**：同一批数据两次查询可能给出不同的行序，于是"按上一块最后一行的时间戳切分"
// 会漏掉夹在中间的行。
//
// 正解是按排序键的全部列做 keyset 分页：
//
//	ORDER BY timestamp DESC, tx_hash DESC, log_index DESC, token_address DESC
//	AND (timestamp, tx_hash, log_index, token_address) < (cursor...)
//
// 这样每一块的边界是确定的最后一行，块与块之间无缝且不重。
func TestCursorKeysetByFullSortKeyIsLossless(t *testing.T) {
	subject := os.Getenv("EXPORT_CURSOR_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_CURSOR_SUBJECT not set")
	}
	ctx := context.Background()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})
	conn := srv.query.CH.DB()

	const orderBy = " ORDER BY timestamp DESC, tx_hash DESC, log_index DESC, token_address DESC"
	proj := "SELECT tx_hash, log_index, token_address, timestamp FROM blockchain.token_transfers WHERE " +
		"chain=? AND (from_address=? OR to_address=?)"

	type cursor struct {
		ts            time.Time
		txHash, token string
		logIndex      uint32
	}

	scan := func(rows interface {
		Next() bool
		Scan(...any) error
		Close() error
	}, keys map[string]struct{}) (int, cursor) {
		n := 0
		var last cursor
		for rows.Next() {
			var txHash, tokenAddr string
			var logIndex uint32
			var ts time.Time
			if serr := rows.Scan(&txHash, &logIndex, &tokenAddr, &ts); serr != nil {
				t.Fatalf("scan: %v", serr)
			}
			keys[fmt.Sprintf("%s|%d|%s", txHash, logIndex, tokenAddr)] = struct{}{}
			last = cursor{ts: ts, txHash: txHash, logIndex: logIndex, token: tokenAddr}
			n++
		}
		return n, last
	}

	// 基准
	rows, qerr := conn.Query(ctx, proj+orderBy, "bsc", subject, subject)
	if qerr != nil {
		t.Fatalf("baseline: %v", qerr)
	}
	baseline := map[string]struct{}{}
	baselineN, _ := scan(rows, baseline)
	rows.Close()
	t.Logf("基准：%d 行 / %d 键", baselineN, len(baseline))

	const chunk = 500_000
	keys := map[string]struct{}{}
	chunks := 0
	var cur cursor
	for {
		where := ""
		args := []any{"bsc", subject, subject}
		if chunks > 0 {
			where = " AND (timestamp, tx_hash, log_index, token_address) < (?, ?, ?, ?)"
			args = append(args, cur.ts, cur.txHash, cur.logIndex, cur.token)
		}
		args = append(args, chunk)
		r, qe := conn.Query(ctx, proj+where+orderBy+" LIMIT ?", args...)
		if qe != nil {
			t.Fatalf("chunk %d: %v", chunks, qe)
		}
		n, last := scan(r, keys)
		r.Close()
		chunks++
		if n < chunk {
			break
		}
		cur = last
		if chunks > 20 {
			t.Fatal("too many chunks")
		}
	}
	t.Logf("keyset 分页：%d 块 / %d 键", chunks, len(keys))
	if len(keys) != len(baseline) {
		t.Fatalf("keyset lost %d keys (%d vs %d)", len(baseline)-len(keys), len(baseline), len(keys))
	}
}

// TestCursorKeysetLosslessAcrossDatasets 把 keyset 分页无损性扩到全部导出数据集。
//
// 每个数据集的 keyset 不同（由表的排序键决定），取值列也不同：
//
//	transactions      (timestamp, tx_hash)
//	token_transfers   (timestamp, tx_hash, log_index, token_address)
//	logs              (timestamp, tx_hash, log_index)
//
// address_balances 是当前快照（无"最新 N 行"语义），不参与分页。
// 这条用例是"把导出从一次 LIMIT 截断改成游标取全量"的前置证据：每个数据集都必须
// 做到分块与一次查询得到**完全相同的键集**。
func TestCursorKeysetLosslessAcrossDatasets(t *testing.T) {
	subject := os.Getenv("EXPORT_CURSOR_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_CURSOR_SUBJECT not set")
	}
	ctx := context.Background()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})
	conn := srv.query.CH.DB()

	cases := []struct {
		dataset  string
		table    string
		addrCols string
		keyCols  string
		orderBy  string
		keyFn    func(row []string) string
		project  string
	}{
		{
			dataset: "transactions", table: "transactions",
			addrCols: "from_address=? OR to_address=?",
			keyCols:  "timestamp, tx_hash",
			orderBy:  "timestamp DESC, tx_hash DESC",
			project:  "chain, toUnixTimestamp64Milli(timestamp), tx_hash",
			keyFn:    func(r []string) string { return r[0] + "|" + r[1] },
		},
		{
			dataset: "token_transfers", table: "token_transfers",
			addrCols: "from_address=? OR to_address=?",
			keyCols:  "timestamp, tx_hash, toInt64(log_index), token_address",
			orderBy:  "timestamp DESC, tx_hash DESC, log_index DESC, token_address DESC",
			project:  "chain, toUnixTimestamp64Milli(timestamp), tx_hash, toInt64(log_index), token_address",
			keyFn:    func(r []string) string { return r[0] + "|" + r[1] + "|" + r[2] + "|" + r[3] },
		},
		{
			dataset: "logs", table: "logs",
			addrCols: "address=?",
			keyCols:  "timestamp, tx_hash, toInt64(log_index)",
			orderBy:  "timestamp DESC, tx_hash DESC, log_index DESC",
			project:  "chain, toUnixTimestamp64Milli(timestamp), tx_hash, toInt64(log_index)",
			keyFn:    func(r []string) string { return r[0] + "|" + r[1] + "|" + r[2] },
		},
	}

	const chunk = 200_000
	for _, c := range cases {
		t.Run(c.dataset, func(t *testing.T) {
			addrArgs := []any{"bsc", subject}
			if strings.Contains(c.addrCols, "to_address") {
				addrArgs = append(addrArgs, subject)
			}
			base := "SELECT " + c.project + " FROM blockchain." + c.table +
				" WHERE chain=? AND (" + c.addrCols + ")"
			// 游标用整数毫秒时间戳：time.Time 回传会引入时区/精度歧义（实测丢 64 个键）。
			keyCols := strings.Replace(c.keyCols, "timestamp", "toUnixTimestamp64Milli(timestamp)", 1)
			single := map[string]struct{}{}
			rows, qerr := conn.Query(ctx, base+" ORDER BY "+c.orderBy, addrArgs...)
			if qerr != nil {
				t.Fatalf("baseline: %v", qerr)
			}
			cols := strings.Split(c.project, ",")
			nCols := len(cols)
			// 数值列（毫秒时间戳 / log_index）必须以 Int64 回传：游标字符串化会在
			// tuple 比较里退化成字典序（"999" > "1000"），也会被驱动拒绝扫描。
			numCol := make([]bool, nCols)
			for i, col := range cols {
				col = strings.TrimSpace(col)
				numCol[i] = strings.HasPrefix(col, "toUnixTimestamp64Milli(") || strings.HasPrefix(col, "toInt64(")
			}
			scanInto := func(rows interface {
				Next() bool
				Scan(...any) error
				Close() error
			}, keys map[string]struct{}) (int, []any) {
				n := 0
				var last []any
				for rows.Next() {
					holders := make([]any, nCols)
					vals := make([]string, nCols)
					for i := range holders {
						if numCol[i] {
							holders[i] = new(int64)
						} else {
							holders[i] = &vals[i]
						}
					}
					if serr := rows.Scan(holders...); serr != nil {
						t.Fatalf("scan: %v", serr)
					}
					row := make([]any, 0, nCols-1)
					for i := 1; i < nCols; i++ {
						if numCol[i] {
							v := *holders[i].(*int64)
							vals[i] = strconv.FormatInt(v, 10)
							row = append(row, v)
							continue
						}
						row = append(row, vals[i])
					}
					keys[c.keyFn(vals[1:])] = struct{}{}
					last = row
					n++
				}
				return n, last
			}
			singleN, _ := scanInto(rows, single)
			rows.Close()
			if singleN == 0 {
				t.Skipf("no %s rows for this subject", c.dataset)
			}

			keys := map[string]struct{}{}
			chunks := 0
			var cur []any
			for {
				where := ""
				args := append([]any{}, addrArgs...)
				if chunks > 0 {
					placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cur)), ",")
					where = " AND (" + keyCols + ") < (" + placeholders + ")"
					for _, v := range cur {
						args = append(args, v)
					}
				}
				args = append(args, chunk)
				r, qe := conn.Query(ctx, base+where+" ORDER BY "+c.orderBy+" LIMIT ?", args...)
				if qe != nil {
					t.Fatalf("chunk %d: %v", chunks, qe)
				}
				n, last := scanInto(r, keys)
				r.Close()
				chunks++
				if n < chunk {
					break
				}
				cur = last
				if chunks > 40 {
					t.Fatal("cursor not advancing")
				}
			}
			if len(keys) != len(single) {
				t.Fatalf("%s: keyset lost %d keys (%d vs %d)", c.dataset, len(single)-len(keys), len(single), len(keys))
			}
			t.Logf("%s: %d 行基准 / %d 块 keyset / 键集完全一致（%d 键）", c.dataset, singleN, chunks, len(keys))
		})
	}
}
