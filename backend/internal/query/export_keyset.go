package query

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

/*
 * ---- 键集分页导出：不截断地取全量 ------------------------------------------------
 *
 * 背景（实测，地址 0xec268cadafc68a4a659b225f3a87bcfe3b891a20 / bsc）：
 *
 *	token_transfers 实际 2,331,094 个版本键，导出只落 2,000,000 行
 *	（= 老的 maxExportRows 上限），**最老的 331,094 行被静默丢掉**，
 *	而 HTTP 响应与 exports 行都显示"完成"。用户看到的是"导出成功"，拿到的却是不全的数据。
 *
 * LIMIT/OFFSET 分页不行：OFFSET 每页重扫前 N 行，2 百万行量级是平方级成本。
 * 键集（keyset）分页则可以做到无损且线性：排序键本身是数据的全序，
 * 只需把"上一页最后一行"的排序键元组带进下一页的谓词：
 *
 *	WHERE ... AND (k1, k2, ..., kn) < (v1, v2, ..., vn)
 *	ORDER BY k1 DESC, k2 DESC, ..., kn DESC LIMIT <每页行数>
 *
 * 无损性已验证（export_cursor_probe_test.go：与一次性无界查询的**键集完全相同**）：
 *
 *	transactions     1,143,993 行 / 6 页   键集一致
 *	token_transfers  2,665,383 原始行 → 2,331,094 键 / 14 页  键集一致
 *
 * 三个必须踩准的细节（都实测过，写错了会静默丢行或重复写行）：
 *
 *  1. 游标列必须以**整数**回传。用 time.Time 回传会引入时区/精度歧义，
 *     用字符串回传会让元组比较退化成字典序（"999" > "1000"），两者都实测丢键。
 *     因此时间统一投影成 toUnixTimestamp64Milli(timestamp)（Int64），
 *     UInt32 的 log_index/call_index 统一 toInt64(...)。
 *  2. 游标必须取**原始行**（扫描到的最后一行），不能取"存活行"——游标代表数据库侧
 *     的扫描位置，若该行因去重被丢弃，下一段仍要从它之后继续。
 *  3. 去重集合必须**跨页延续**。同一版本键的两行理论上可能落在不同页
 *     （时间戳被上游修正过），每页各建一个 seen 会把它们各写一次。
 */

// CursorKind 是游标列的扫描/回传类型。
type CursorKind int

const (
	// CursorString 对应 ClickHouse String（tx_hash / 地址等）。
	CursorString CursorKind = iota
	// CursorInt64 对应投影成 Int64 的数值列（毫秒时间戳 / toInt64(log_index)）。
	CursorInt64
)

// ExportCursor 描述某数据集的全序分页游标。
//
// SelectExprs 与 KeyExprs 一一对应：前者是追加到 `SELECT *` 尾部的投影（带别名，
// 供驱动按类型扫描），后者是用于元组比较的表达式（不带别名，避免依赖别名解析）。
type ExportCursor struct {
	SelectExprs []string
	KeyExprs    []string
	OrderBy     string
	Kinds       []CursorKind
}

// ExportCursorFor 返回数据集的分页游标。
//
// 排序键的选取原则：与 ReplacingMergeTree 的版本键（exportDedupeColumns）保持
// 一致的前缀，并在其前面加 timestamp——导出契约本来就是"按 timestamp DESC 排序"，
// 而同一版本键的重复行必须相邻才能被收敛。
// 不支持的数据集（balances 快照等）返回 false，调用方退回单查询形状。
func ExportCursorFor(dataset string) (ExportCursor, bool) {
	switch dataset {
	case "transactions":
		return ExportCursor{
			SelectExprs: []string{"toUnixTimestamp64Milli(timestamp) AS __ck0", "tx_hash AS __ck1"},
			KeyExprs:    []string{"toUnixTimestamp64Milli(timestamp)", "tx_hash"},
			OrderBy:     "timestamp DESC, tx_hash DESC",
			Kinds:       []CursorKind{CursorInt64, CursorString},
		}, true
	case "token_transfers":
		return ExportCursor{
			SelectExprs: []string{
				"toUnixTimestamp64Milli(timestamp) AS __ck0", "tx_hash AS __ck1",
				"toInt64(log_index) AS __ck2", "token_address AS __ck3",
			},
			KeyExprs: []string{
				"toUnixTimestamp64Milli(timestamp)", "tx_hash",
				"toInt64(log_index)", "token_address",
			},
			OrderBy: "timestamp DESC, tx_hash DESC, log_index DESC, token_address DESC",
			Kinds:   []CursorKind{CursorInt64, CursorString, CursorInt64, CursorString},
		}, true
	case "logs":
		return ExportCursor{
			SelectExprs: []string{
				"toUnixTimestamp64Milli(timestamp) AS __ck0", "tx_hash AS __ck1",
				"toInt64(log_index) AS __ck2",
			},
			KeyExprs: []string{"toUnixTimestamp64Milli(timestamp)", "tx_hash", "toInt64(log_index)"},
			OrderBy:  "timestamp DESC, tx_hash DESC, log_index DESC",
			Kinds:    []CursorKind{CursorInt64, CursorString, CursorInt64},
		}, true
	case "nft_transfers":
		return ExportCursor{
			SelectExprs: []string{
				"toUnixTimestamp64Milli(timestamp) AS __ck0", "tx_hash AS __ck1",
				"toInt64(log_index) AS __ck2", "contract_address AS __ck3", "token_id AS __ck4",
			},
			KeyExprs: []string{
				"toUnixTimestamp64Milli(timestamp)", "tx_hash",
				"toInt64(log_index)", "contract_address", "token_id",
			},
			OrderBy: "timestamp DESC, tx_hash DESC, log_index DESC, contract_address DESC, token_id DESC",
			Kinds:   []CursorKind{CursorInt64, CursorString, CursorInt64, CursorString, CursorString},
		}, true
	case "internal_transactions":
		return ExportCursor{
			SelectExprs: []string{
				"toUnixTimestamp64Milli(timestamp) AS __ck0", "tx_hash AS __ck1",
				"toInt64(call_index) AS __ck2",
			},
			KeyExprs: []string{"toUnixTimestamp64Milli(timestamp)", "tx_hash", "toInt64(call_index)"},
			OrderBy:  "timestamp DESC, tx_hash DESC, call_index DESC",
			Kinds:    []CursorKind{CursorInt64, CursorString, CursorInt64},
		}, true
	}
	return ExportCursor{}, false
}

// ExportKeysetQuery 是分页取数的 SQL 组成。把 SELECT / FROM / WHERE 拆开传，
// 是为了在投影尾部追加游标列而不做字符串改写（改写 `SELECT *` 很容易在
// 将来某个数据集的 SQL 变形时静默错位）。
type ExportKeysetQuery struct {
	Dataset string
	// Table 是 `blockchain.` 之后的部分，例如 token_transfers。
	Table string
	// Where 不含 WHERE 关键字，也不含游标条件；时间范围由调用方自行拼进 Where。
	Where string
	Args  []any
}

// defaultExportPageRows 是每页的取数行数。
//
// 它决定的是"每页一条 SQL"的粒度，与写出的块大小（exportChunkRows）无关：
// 键集分页的每页都要在**剩余**分区上重新按 from_address/to_address 的
// bloom_filter 逐个 granule 判定，因此总成本随页数线性上涨。实测
// （0xec26…1a20 / bsc / token_transfers，2,331,094 行，同一台机器）：
//
//	每页 20 万行（12 页）  全量 34.9 s   前 100 万行 16.4 s
//	每页 100 万行（3 页）  全量 17.8 s   前 100 万行  9.6 s   ← 本默认值
//	单查询无 LIMIT         全量  8.8 s   ← 更快，但见下
//
// 单查询形状（不带 LIMIT）确实最快，但不采用：它要求 ClickHouse 把**该地址的全部
// 行**物化后排序，内存随行数线性增长（实测 266 万原始行 = 1.86 GiB，
// 约 0.75 KiB/行；而 1200 万行的地址就会逼近 14 GiB 的导出内存上限并 Code 241 失败）。
// 每页 100 万行把单次查询的内存压在 1 GiB 量级，同时页数少到几乎不损失速度。
// 页大小不影响结果集——那正是键集分页要保证的事（见同级测试）。
const defaultExportPageRows = 1_000_000

// exportPageRows 是实际使用的每页行数。变量而非常量，是为了让测试把它调小，
// 在同一个数据集上强制跨多页（页边界正是键集分页唯一可能出错的地方）。
// 生产代码不修改它。
var exportPageRows = defaultExportPageRows

// SetExportPageRowsForTest 把每页行数改成 rows，返回恢复函数。
//
// 只给测试用：页大小不影响结果集（那正是键集分页要证明的事），
// 但页太小会让每页的固定扫描成本乘以页数，因此生产保持默认值。
func SetExportPageRowsForTest(rows int) func() {
	prev := exportPageRows
	if rows > 0 {
		exportPageRows = rows
	}
	return func() { exportPageRows = prev }
}

// cursorTracker 记录"最近一条原始行的游标值"。
type cursorTracker struct {
	kinds   []CursorKind
	holders []any
	vals    []any
	seen    bool
}

func newCursorTracker(kinds []CursorKind) *cursorTracker {
	t := &cursorTracker{kinds: kinds, holders: make([]any, len(kinds)), vals: make([]any, len(kinds))}
	for i, k := range kinds {
		switch k {
		case CursorInt64:
			t.holders[i] = new(int64)
		default:
			t.holders[i] = new(string)
		}
	}
	return t
}

// observe 在每次 Scan 之后调用，把当前行的游标值复制出来。
func (t *cursorTracker) observe() {
	for i, k := range t.kinds {
		t.vals[i] = derefCursor(k, t.holders[i])
	}
	t.seen = true
}

// cursor 返回当前游标值的副本（可直接拼进下一页参数，不会被后续 Scan 覆盖）。
func (t *cursorTracker) cursor() []any {
	if !t.seen {
		return nil
	}
	out := make([]any, len(t.vals))
	copy(out, t.vals)
	return out
}

func derefCursor(kind CursorKind, holder any) any {
	switch kind {
	case CursorInt64:
		switch v := holder.(type) {
		case *int64:
			return *v
		case *uint64:
			return int64(*v)
		case *uint32:
			return int64(*v)
		case *int32:
			return int64(*v)
		}
	default:
		switch v := holder.(type) {
		case *string:
			return *v
		case *uint32:
			return strconv.FormatUint(uint64(*v), 10)
		case *int64:
			return strconv.FormatInt(*v, 10)
		}
	}
	return nil
}

// placeholderList 生成 `?,?,?`。
func placeholderList(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// exportPageSQL 渲染一页的 SQL。
func exportPageSQL(q ExportKeysetQuery, cur ExportCursor, cursor []any, pageRows int) (string, []any) {
	var sb strings.Builder
	sb.WriteString("SELECT *")
	for _, expr := range cur.SelectExprs {
		sb.WriteString(", ")
		sb.WriteString(expr)
	}
	sb.WriteString(" FROM blockchain.")
	sb.WriteString(q.Table)
	sb.WriteString(" WHERE ")
	sb.WriteString(q.Where)
	args := append([]any(nil), q.Args...)
	if len(cursor) > 0 {
		sb.WriteString(" AND (")
		sb.WriteString(strings.Join(cur.KeyExprs, ", "))
		sb.WriteString(") < (")
		sb.WriteString(placeholderList(len(cursor)))
		sb.WriteString(")")
		args = append(args, cursor...)
	}
	sb.WriteString(" ORDER BY ")
	sb.WriteString(cur.OrderBy)
	sb.WriteString(" LIMIT ?")
	args = append(args, pageRows)
	return sb.String(), args
}

// acceptExportRow 判定一条**原始**行是否存活（ReplacingMergeTree 版本收敛）。
//
// 单查询路径与键集分页路径共用这一份实现：版本收敛的判定必须处处相同，
// 否则同一份数据在"一次查完"与"分页查"下会选出不同的版本集合。
// seen 由调用方持有，键集路径下必须跨页延续。
func acceptExportRow(dataset string, names []string, holders []any, keys []string, seen map[exportKey]struct{}) bool {
	if len(keys) == 0 {
		return true
	}
	key := exportRowKey(keys, holders, names)
	if _, dup := seen[key]; dup {
		return false
	}
	seen[key] = struct{}{}
	return true
}

// forEachExportPage 是键集分页的公共取数循环：逐页查询、逐行调用 visit。
//
// visit 拿到的是**原始行**（已 Scan 的持有变量与导出列名，游标列不在 names 里），
// 由它自己决定去重与块切分；返回 stop=true 表示"够了，停止取数"。
// names 每页都一样（同一数据集），但只有首次调用才知道，因此随行传入。
//
// 返回 ok=false 表示驱动不支持按列消费（调用方必须回退 map 路径）。
func (s *Service) forEachExportPage(ctx context.Context, q ExportKeysetQuery, cur ExportCursor, limit int,
	visit func(holders []any, names []string) (bool, error)) (int, bool, error) {
	// 全量导出必须显式关掉服务端 max_result_rows：单查询路径靠显式 LIMIT 规避，
	// 这里虽然每页也有 LIMIT，但语义是"取到取空"，不能叠加服务端默认值。
	// 扫描槽在整个分页期间持有：分页等于一次导出，不应与其他导出交错占用内存预算。
	release, slotErr := acquireExportScanSlotFor(ctx)
	if slotErr != nil {
		return 0, true, slotErr
	}
	defer release()
	ctx = clickhouse.ExportScanContext(ctx)

	nCursor := len(cur.SelectExprs)
	total := 0
	var names []string
	var cursor []any
	for page := 0; ; page++ {
		tracker := newCursorTracker(cur.Kinds)
		sql, args := exportPageSQL(q, cur, cursor, exportPageRows)
		rows, err := s.CH.DB().Query(ctx, sql, args...)
		if err != nil {
			return total, true, clickhouse.DescribeScanError(err)
		}
		scanned, canScan := rows.(interface{ Scan(dest ...any) error })
		if !canScan {
			rows.Close()
			return total, false, nil
		}
		allNames, holders := fastColumnHolders(rows.ColumnTypes())
		if len(allNames) <= nCursor {
			rows.Close()
			return total, true, fmt.Errorf("keyset export: result set has %d columns, cursor needs %d",
				len(allNames), nCursor)
		}
		if names == nil {
			names = allNames[:len(allNames)-nCursor]
		}
		// 游标列的持有变量换成跟踪器自己的：类型由 CursorKind 决定，
		// 而 newColumnHolder 是按列类型分配的，两者必须一致（否则驱动拒绝扫描）。
		for i := 0; i < nCursor; i++ {
			holders[len(holders)-nCursor+i] = tracker.holders[i]
		}

		pageRows := 0
		stop := false
		for rows.Next() {
			if scanErr := scanned.Scan(holders...); scanErr != nil {
				rows.Close()
				return total, true, fmt.Errorf("clickhouse [ScanExportRowKeyset]: %w", scanErr)
			}
			pageRows++
			total++
			tracker.observe()
			halt, visitErr := visit(holders, names)
			if visitErr != nil {
				rows.Close()
				return total, true, visitErr
			}
			if halt {
				stop = true
				break
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return total, true, err
		}
		rows.Close()
		if stop || pageRows < exportPageRows {
			return total, true, nil
		}
		next := tracker.cursor()
		if next == nil {
			return total, true, nil
		}
		if page > 0 && sameCursor(cursor, next) {
			return total, true, fmt.Errorf("keyset export: cursor did not advance on page %d", page)
		}
		cursor = next
	}
}

// sameCursor 判定两个游标是否相同（用于检测"游标不前进"的死循环）。
func sameCursor(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if fmt.Sprint(a[i]) != fmt.Sprint(b[i]) {
			return false
		}
	}
	return true
}

// ForEachExportColumnChunkKeyset 用键集游标分页遍历**全量**导出结果，
// 按列把单元格文本交给回调（不建行 map，与 ForEachExportColumnChunk 同形）。
//
// 与单查询路径的差别只有"取数形状"：不再需要调用方给出 wantLimit
// （那正是截断的来源），而是一直取到某一页不满为止。
// 去重键、去重集合（跨页延续）、单元格格式化、块切分都与单查询路径一致。
//
// limit>0 时保留一个显式上限（仅用于需要"至多 N 行"的内部调用），limit<=0 表示全量。
// 返回 ok=false 表示驱动不支持按列消费，调用方必须回退 map 路径。
func (s *Service) ForEachExportColumnChunkKeyset(ctx context.Context, q ExportKeysetQuery, limit, chunk int,
	fn func(c *ColumnChunk) error) (int, bool, error) {
	cur, supported := ExportCursorFor(q.Dataset)
	if !supported {
		return 0, false, nil
	}
	if chunk <= 0 {
		chunk = exportChunkRows
	}
	seen := make(map[exportKey]struct{}, 4096)
	keys := exportDedupeColumns(q.Dataset)

	emitted := 0
	cellBuf := make([]string, 0, chunk*16)
	rowsInChunk := 0
	var names []string
	var nameIndex map[string]int

	flush := func() error {
		if rowsInChunk == 0 {
			return nil
		}
		err := fn(&ColumnChunk{Names: names, NameIndex: nameIndex, Cells: cellBuf, Rows: rowsInChunk})
		cellBuf = cellBuf[:0]
		rowsInChunk = 0
		return err
	}

	_, ok, err := s.forEachExportPage(ctx, q, cur, limit,
		func(holders []any, pageNames []string) (bool, error) {
			if names == nil {
				names = pageNames
				nameIndex = make(map[string]int, len(names))
				for i, n := range names {
					nameIndex[n] = i
				}
			}
			if !acceptExportRow(q.Dataset, names, holders, keys, seen) {
				return false, nil
			}
			for i := range names {
				cellBuf = append(cellBuf, formatCellText(exportValue(holders[i])))
			}
			rowsInChunk++
			emitted++
			if rowsInChunk >= chunk {
				if err := flush(); err != nil {
					return true, err
				}
			}
			return limit > 0 && emitted >= limit, nil
		})
	if err != nil {
		return emitted, ok, err
	}
	if !ok {
		return 0, false, nil
	}
	if err := flush(); err != nil {
		return emitted, true, err
	}
	return emitted, true, nil
}

// ForEachExportChunkKeyset 是键集分页的 map 版本：按块给出 []map[string]any，
// 供 jsonl/csv 等"按行编码"的写出器使用（列式路径只支持 csv）。
//
// 与 ForEachExportChunk 相同的块语义与去重语义，只是取数改成键集分页、不带行数上限。
func (s *Service) ForEachExportChunkKeyset(ctx context.Context, q ExportKeysetQuery, limit, chunk int,
	fn func(rows []map[string]any) error) (int, bool, error) {
	cur, supported := ExportCursorFor(q.Dataset)
	if !supported {
		return 0, false, nil
	}
	plan, planErr := rowPlan(q.Dataset)
	if planErr != nil {
		return 0, false, nil
	}
	if chunk <= 0 {
		chunk = exportChunkRows
	}
	seen := make(map[exportKey]struct{}, 4096)
	keys := exportDedupeColumns(q.Dataset)

	emitted := 0
	buf := make([]map[string]any, 0, chunk)
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		if err := fn(buf); err != nil {
			return err
		}
		buf = make([]map[string]any, 0, chunk)
		return nil
	}

	_, ok, err := s.forEachExportPage(ctx, q, cur, limit,
		func(holders []any, names []string) (bool, error) {
			if !acceptExportRow(q.Dataset, names, holders, keys, seen) {
				return false, nil
			}
			item, mapErr := scanRowMap(plan, names, holders)
			if mapErr != nil {
				return true, mapErr
			}
			normalizeStoredAmount(q.Dataset, item)
			buf = append(buf, item)
			emitted++
			if len(buf) >= chunk {
				if err := flush(); err != nil {
					return true, err
				}
			}
			return limit > 0 && emitted >= limit, nil
		})
	if err != nil {
		return emitted, ok, err
	}
	if !ok {
		return 0, false, nil
	}
	if err := flush(); err != nil {
		return emitted, true, err
	}
	return emitted, true, nil
}
