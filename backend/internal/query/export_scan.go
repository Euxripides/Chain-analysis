package query

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/shopspring/decimal"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

/*
 * ---- 导出取数的快路径：跳过 JSON 往返 ---------------------------------------------
 *
 * 现网 scanOne 的每一行都要走 ScanStruct → json.Marshal → json.Decode(UseNumber)，
 * 目的是把强类型结构体变成 map[string]any。实测（cmd/exportcost，29 万行 × 22 列）：
 *
 *	A 驱动消费（只 Scan）            0.54 s
 *	B 行物化 map（直接建 map）        1.70 s   ← 本文件的做法
 *	B 行物化 json（现网往返）        13.04 s   ← 慢 7.7 倍，且 GC 占 60% CPU
 *	（CPU profile：markBits.setMarked 47.7%、heapSetTypeSmallHeader 9.0%）
 *
 * 之所以能直接建 map：各数据集的 row 结构体字段与 ClickHouse 结果集列**一一对应**
 * （ch tag 就是列名，json tag 就是 map 键），且全部 row 结构体都不带 omitempty，
 * 字段集固定。因此 map[列名] = 值 与 JSON 往返的结果在导出用到的语义上一致：
 *   - 整数：JSON 路径是 json.Number（十进制字符串），这里是整数类型，fmt.Sprint 相同；
 *   - 时间：JSON 路径是 RFC3339 字符串，**必须显式 Format**——time.Time 的
 *     fmt.Sprint 是 "2026-09-12 01:06:01 +0000 UTC"，与 RFC3339 不同，直接塞进 map
 *     会让导出文件的时间列整体变格式（实测 50000/50000 行不同才发现）；
 *   - Decimal：JSON 路径是字符串，这里是 decimal.Decimal，**必须显式 String()**，
 *     否则 fmt.Sprint 走 Stringer 得到同样的文本，但类型不再是字符串，
 *     下游做字符串比较时会不一致。
 *
 * 安全网：scanRowMap 在运行时校验"结果集列名集合 == row 结构体的 json 键集合"，
 * 任何一边多出/缺少列（SELECT * 遇到新增列、结构体漏字段）都返回 errFastScanMismatch，
 * 由调用方回退到 scanOne，绝不静默产出缺列的行。
 */

// errFastScanMismatch 表示结果集与 row 结构体不再一一对应，必须回退慢路径。
type fastScanMismatch struct {
	dataset string
	detail  string
}

func (e *fastScanMismatch) Error() string {
	return "fast scan unavailable for dataset " + e.dataset + ": " + e.detail
}

var fastScanPlans sync.Map // map[string]*fastScanPlan

// fastScanPlan 记录一次数据集的行物化方案：结果集列名 → 该列在当前结果集里的下标。
// 只依赖结构体（不依赖具体结果集），因此可以按 dataset 缓存。
type fastScanPlan struct {
	dataset string
	keys    []string // 与结果集列顺序无关，供回退校验使用
	keySet  map[string]struct{}
}

func buildFastScanPlan(dataset string) (*fastScanPlan, error) {
	row, err := newRowStruct(dataset)
	if err != nil {
		return nil, err
	}
	typ := reflect.TypeOf(row).Elem()
	plan := &fastScanPlan{dataset: dataset, keySet: make(map[string]struct{}, typ.NumField())}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" { // 未导出字段不参与
			continue
		}
		name := field.Tag.Get("json")
		if name == "" || name == "-" {
			return nil, &fastScanMismatch{dataset: dataset, detail: "field " + field.Name + " has no usable json tag"}
		}
		if idx := strings.IndexByte(name, ','); idx >= 0 {
			name = name[:idx]
		}
		plan.keys = append(plan.keys, name)
		plan.keySet[name] = struct{}{}
	}
	return plan, nil
}

// rowPlan 返回（并缓存）该数据集的行物化方案。
func rowPlan(dataset string) (*fastScanPlan, error) {
	if cached, ok := fastScanPlans.Load(dataset); ok {
		return cached.(*fastScanPlan), nil
	}
	plan, err := buildFastScanPlan(dataset)
	if err != nil {
		return nil, err
	}
	fastScanPlans.Store(dataset, plan)
	return plan, nil
}

// checkColumns 校验结果集列与结构体字段一一对应。
func (p *fastScanPlan) checkColumns(columns []string) error {
	if len(columns) != len(p.keys) {
		return &fastScanMismatch{dataset: p.dataset, detail: "column count mismatch"}
	}
	for _, name := range columns {
		if _, ok := p.keySet[name]; !ok {
			return &fastScanMismatch{dataset: p.dataset, detail: "unexpected column " + name}
		}
	}
	return nil
}

// scanRowMap 把当前行直接写成 map[string]any（键 = 结果集列名）。
// columns 与 holders 必须来自同一次查询的结果集。
//
// 必须与 scanOne 的后处理保持一致，否则两条路径产出的行不同：
// internal_transactions 在 scanOne 里会补 rejected（由 success/error 推导）
// 与 asset_type（TRON 恒为 TRX）两个**派生列**，这里照做。
func scanRowMap(plan *fastScanPlan, columns []string, holders []any) (map[string]any, error) {
	if err := plan.checkColumns(columns); err != nil {
		return nil, err
	}
	item := make(map[string]any, len(columns))
	for i, name := range columns {
		item[name] = exportValue(holders[i])
	}
	if plan.dataset == "internal_transactions" {
		success := fmt.Sprint(item["success"])
		item["rejected"] = success == "0" || strings.EqualFold(fmt.Sprint(item["error"]), "rejected")
		if strings.EqualFold(fmt.Sprint(item["chain"]), "tron") {
			item["asset_type"] = "TRX"
		}
	}
	return item, nil
}

// ExportValue 是 exportValue 的导出包装：API 层的列式导出路径需要按列取值，
// 但不能反向 import 本包内部符号。
func ExportValue(holder any) any { return exportValue(holder) }

// exportValue 把列持有变量解引用成值。
//
// time.Time 与 decimal.Decimal 直接返回值本身：导出侧只在格式化时读取它们，
// fmt.Sprint 的结果与 JSON 往返后的字符串一致（见文件头）。
func exportValue(holder any) any {
	switch v := holder.(type) {
	case *string:
		return *v
	case *time.Time:
		// 与 scanOne 的 JSON 往返保持一致：编码成 RFC3339 字符串，不是 time.Time。
		return v.UTC().Format(time.RFC3339Nano)
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
		// 同上：JSON 往返产出的是字符串。
		return v.String()
	default:
		return nil
	}
}

// fastColumnHolders 按结果集列类型分配扫描目标。驱动不支持 *any，
// 必须逐列给出具体类型（与 cmd/exportcost 的探针口径一致）。
func fastColumnHolders(cols []driver.ColumnType) ([]string, []any) {
	names := make([]string, len(cols))
	holders := make([]any, len(cols))
	for i, c := range cols {
		names[i] = c.Name()
		holders[i] = newColumnHolder(c.DatabaseTypeName())
	}
	return names, holders
}

// queryRowsExport 是导出路径专用的取数：走 scanRowMap 快路径，
// 结果集与行结构体不再一一对应时（新增列、结构体漏字段）回退 scanOne，
// 保证导出永不因为快路径而缺列。
//
// wantLimit>0 时启用**边扫边去重**：结果集已按 timestamp DESC 排序，同一去重键的
// 首个出现就是最新版本，因此只在"这一行会存活"时才物化 map，攒够 wantLimit 行
// 立即停止扫描。实测（29 万行原始、目标 20 万行）能省掉三成 map 分配与后续 GC——
// 尤其是重复率高的链（bsc 31.4%）与超采 1.45 倍时的 45 万行白建开销。
// wantLimit<=0 表示调用方自己会去重，这里只做物化。
func (s *Service) queryRowsExport(ctx context.Context, dataset, q string, wantLimit int, args ...any) ([]map[string]any, error) {
	rows, err := s.CH.DB().Query(ctx, q, args...)
	if err != nil {
		return nil, clickhouse.DescribeScanError(err)
	}
	defer rows.Close()

	names, holders := fastColumnHolders(rows.ColumnTypes())
	plan, planErr := rowPlan(dataset)
	scanner, canScan := rows.(interface{ Scan(dest ...any) error })

	if planErr != nil || !canScan {
		return s.queryRows(ctx, dataset, q, args...)
	}

	var seen map[exportKey]struct{}
	if wantLimit > 0 {
		seen = make(map[exportKey]struct{}, wantLimit)
	}
	keys := exportDedupeColumns(dataset)
	capacity := 1024
	if wantLimit > 0 {
		capacity = wantLimit
	}
	out := make([]map[string]any, 0, capacity)
	for rows.Next() {
		if scanErr := scanner.Scan(holders...); scanErr != nil {
			return nil, fmt.Errorf("clickhouse [ScanExportRow]: %w", scanErr)
		}
		if seen != nil {
			key := exportRowKey(keys, holders, names)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		item, mapErr := scanRowMap(plan, names, holders)
		if mapErr != nil {
			var mismatch *fastScanMismatch
			if errors.As(mapErr, &mismatch) {
				// 结果集与结构体不再对应：整条查询回退慢路径，避免半快半慢的行集。
				return s.queryRows(ctx, dataset, q, args...)
			}
			return nil, mapErr
		}
		normalizeStoredAmount(dataset, item)
		out = append(out, item)
		if wantLimit > 0 && len(out) >= wantLimit {
			return out, nil
		}
	}
	return out, rows.Err()
}

// ForEachExportChunk 按块遍历导出结果：每块是已按去重键收敛、按 timestamp DESC
// 排序的一批行，块大小由 chunk 控制（<=0 时用 exportChunkRows）。
//
// 用途是"边取边写"：调用方（API 层）可以把每块直接序列化进 io.Pipe → MinIO，
// 而不必等全部行物化完。相比 queryRowsExport 一次性返回，这里只多一个块切分点，
// 去重与排序语义完全相同（同一 seen 集合跨块延续，因此跨块重复键同样被收敛）。
//
// 返回已产出的行数；fn 返回错误时立即停止并透传。
func (s *Service) ForEachExportChunk(ctx context.Context, dataset, q string, wantLimit int,
	chunk int, fn func(rows []map[string]any) error, args ...any) (int, error) {
	rows, err := s.CH.DB().Query(ctx, q, args...)
	if err != nil {
		return 0, clickhouse.DescribeScanError(err)
	}
	defer rows.Close()

	names, holders := fastColumnHolders(rows.ColumnTypes())
	plan, planErr := rowPlan(dataset)
	scanner, canScan := rows.(interface{ Scan(dest ...any) error })
	if planErr != nil || !canScan {
		// 无法走快路径：退回一次性取数再按块回调，调用方语义不变。
		all, qErr := s.queryRows(ctx, dataset, q, args...)
		if qErr != nil {
			return 0, qErr
		}
		if chunk <= 0 {
			chunk = exportChunkRows
		}
		for start := 0; start < len(all); start += chunk {
			end := min(start+chunk, len(all))
			if err := fn(all[start:end]); err != nil {
				return start, err
			}
		}
		return len(all), nil
	}

	if chunk <= 0 {
		chunk = exportChunkRows
	}
	seen := make(map[exportKey]struct{}, 1024)
	keys := exportDedupeColumns(dataset)
	buf := make([]map[string]any, 0, chunk)
	emitted := 0
	for rows.Next() {
		if scanErr := scanner.Scan(holders...); scanErr != nil {
			return emitted, fmt.Errorf("clickhouse [ScanExportRow]: %w", scanErr)
		}
		if len(keys) > 0 {
			key := exportRowKey(keys, holders, names)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		item, mapErr := scanRowMap(plan, names, holders)
		if mapErr != nil {
			var mismatch *fastScanMismatch
			if errors.As(mapErr, &mismatch) {
				return emitted, &fastScanMismatch{dataset: dataset, detail: "column set changed mid-stream"}
			}
			return emitted, mapErr
		}
		normalizeStoredAmount(dataset, item)
		buf = append(buf, item)
		if len(buf) >= chunk {
			if err := fn(buf); err != nil {
				return emitted, err
			}
			emitted += len(buf)
			if wantLimit > 0 && emitted >= wantLimit {
				return emitted, nil
			}
			buf = make([]map[string]any, 0, chunk)
		}
	}
	if err := rows.Err(); err != nil {
		return emitted, err
	}
	if len(buf) > 0 {
		if err := fn(buf); err != nil {
			return emitted, err
		}
		emitted += len(buf)
	}
	return emitted, nil
}

// ColumnChunk 是一块列式导出的原始单元格文本。
//
// 为什么在这里就把值格式化成字符串：列持有变量是**按列**的、每次 Scan 覆盖，
// 而富化必须"先收集整块的合约、批量查元数据、再写出"。两者不能同时成立，
// 除非把块内数据复制出来。复制的代价很小（字符串 + 切片），而且避免了整行 map
// （每行 30 个键 / 30 个装箱值，实测 20 万行分配 1524 MiB）。
type ColumnChunk struct {
	Names []string
	// NameIndex 是列名→下标，供按列名取值（注释列需要 from_address/to_address/address）。
	NameIndex map[string]int
	// Cells[ i*Rows + j ] 是第 j 行第 i 列的可直接写出的文本（未加引号）。
	// 空字符串既表示空值也表示空文本——导出里两者在 CSV 中是同一形态。
	Cells []string
	Rows  int
}

// ForEachExportColumnChunk 按块遍历导出结果并把单元格文本交给回调，不建行 map。
//
// 去重沿用同一套 seen 集合与键算法；cells 只装**存活行**，因此与 map 路径选出
// 完全相同的行集。回调拿到的是已复制的文本，可以在块内自由分两遍处理
// （先收集合约再写出）。
//
// 返回 ok=false 表示驱动不支持按列消费，调用方必须回退 map 路径。
func (s *Service) ForEachExportColumnChunk(ctx context.Context, dataset, q string, wantLimit, chunk int,
	fn func(c *ColumnChunk) error, args ...any) (int, bool, error) {
	rows, err := s.CH.DB().Query(ctx, q, args...)
	if err != nil {
		return 0, false, clickhouse.DescribeScanError(err)
	}
	defer rows.Close()

	names, holders := fastColumnHolders(rows.ColumnTypes())
	scanner, canScan := rows.(interface{ Scan(dest ...any) error })
	if !canScan {
		return 0, false, nil
	}
	if chunk <= 0 {
		chunk = exportChunkRows
	}
	nameIndex := make(map[string]int, len(names))
	for i, n := range names {
		nameIndex[n] = i
	}
	seen := make(map[exportKey]struct{}, 1024)
	keys := exportDedupeColumns(dataset)
	emitted := 0
	cellBuf := make([]string, 0, chunk*len(names))
	rowsInChunk := 0
	flush := func() error {
		if rowsInChunk == 0 {
			return nil
		}
		err := fn(&ColumnChunk{Names: names, NameIndex: nameIndex, Cells: cellBuf, Rows: rowsInChunk})
		cellBuf = cellBuf[:0]
		rowsInChunk = 0
		return err
	}
	for rows.Next() {
		if scanErr := scanner.Scan(holders...); scanErr != nil {
			return emitted, true, fmt.Errorf("clickhouse [ScanExportRow]: %w", scanErr)
		}
		if len(keys) > 0 {
			key := exportRowKey(keys, holders, names)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		for i := range names {
			cellBuf = append(cellBuf, formatCellText(exportValue(holders[i])))
		}
		rowsInChunk++
		emitted++
		if rowsInChunk >= chunk {
			if err := flush(); err != nil {
				return emitted, true, err
			}
		}
		if wantLimit > 0 && emitted >= wantLimit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return emitted, true, err
	}
	if err := flush(); err != nil {
		return emitted, true, err
	}
	return emitted, true, nil
}

// formatCellText 把单元格值写成与 map 路径一致的文本。
//
// 必须与 api 包 appendCSVValue 的取值语义相同（时间 RFC3339Nano、Decimal String()、
// nil→空）；引号由写出方统一处理，因此这里不带引号。
func formatCellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []string:
		// 数组列（transactions.signers 等）：map 路径的值是 []string，
		// 导出时经 fmt.Sprint 渲染成 "[a b c]"。这里必须给出同样的文本。
		return fmt.Sprint(x)
	case []any:
		return fmt.Sprint(x)
	case []uint64:
		return fmt.Sprint(x)
	case []uint32:
		return fmt.Sprint(x)
	case []int64:
		return fmt.Sprint(x)
	case []int32:
		return fmt.Sprint(x)
	case []float64:
		return fmt.Sprint(x)
	case uint64:
		return strconv.FormatUint(x, 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

// exportChunkRows 是流式导出的默认块大小：过大则内存峰值回升，过小则元数据
// 富化的批量查询次数变多。实测 3.2 万行 / 块时 GetMany 每块约 1 ms。
const exportChunkRows = 32 * 1024

// exportRowKey 计算流式去重键。值与 scanRowMap 写进 map 的完全一致（同样的
// 整数/字符串/时间格式化），并与 dedupeExportRows 用同一套 128 位 FNV 与列分隔符，
// 因此"流式判定存活"与"物化后再去重"的存活集合相同。
func exportRowKey(cols []string, holders []any, names []string) exportKey {
	var key exportKey
	for _, want := range cols {
		for i, name := range names {
			if name != want {
				continue
			}
			switch v := holders[i].(type) {
			case *string:
				key.add(*v)
			case *uint8:
				key.addUint(uint64(*v))
			case *uint16:
				key.addUint(uint64(*v))
			case *uint32:
				key.addUint(uint64(*v))
			case *uint64:
				key.addUint(*v)
			case *int8:
				key.addInt(int64(*v))
			case *int16:
				key.addInt(int64(*v))
			case *int32:
				key.addInt(int64(*v))
			case *int64:
				key.addInt(*v)
			default:
				key.add(fmt.Sprint(exportValue(holders[i])))
			}
			break
		}
	}
	return key
}

// exportKey 是 128 位 FNV 去重键。
type exportKey struct{ hi, lo uint64 }

const (
	exportKeyOffset = 14695981039346656037
	exportKeyPrime  = 1099511628211
	exportKeyMix    = exportKeyPrime ^ 0x2545f4914f6cdd1d
)

func (k *exportKey) add(s string) {
	if k.hi == 0 && k.lo == 0 {
		k.hi = exportKeyOffset
		k.lo = exportKeyOffset ^ 0x9e3779b97f4a7c15
	}
	for i := 0; i < len(s); i++ {
		k.hi = (k.hi ^ uint64(s[i])) * exportKeyPrime
		k.lo = (k.lo ^ uint64(s[i])) * exportKeyMix
	}
	k.hi = (k.hi ^ 0x1f) * exportKeyPrime
	k.lo = (k.lo ^ 0x1f) * exportKeyMix
}

func (k *exportKey) addUint(v uint64) { k.add(strconv.FormatUint(v, 10)) }
func (k *exportKey) addInt(v int64)   { k.add(strconv.FormatInt(v, 10)) }

// newArrayHolder 按数组元素类型分配扫描目标。
//
// 驱动要求数组列用"元素类型的切片指针"接收（实测 Array(String) → *[]string，
// ColumnType.ScanType() 也返回 []string）。未知元素类型退回 *[]any——
// 驱动支持用 *[]any 接任意数组，代价是每个元素装箱；正确性优先于这点开销。
func newArrayHolder(arrayType string) any {
	inner := strings.TrimSuffix(strings.TrimPrefix(arrayType, "array("), ")")
	inner = strings.TrimSpace(inner)
	// 去掉 Nullable(...) 包裹，取底层类型。
	inner = strings.TrimPrefix(inner, "nullable(")
	inner = strings.TrimSuffix(inner, ")")
	switch {
	case strings.HasPrefix(inner, "string"), strings.HasPrefix(inner, "fixedstring"),
		strings.HasPrefix(inner, "uuid"), strings.HasPrefix(inner, "enum"):
		return new([]string)
	case strings.HasPrefix(inner, "uint64"):
		return new([]uint64)
	case strings.HasPrefix(inner, "uint32"):
		return new([]uint32)
	case strings.HasPrefix(inner, "int64"):
		return new([]int64)
	case strings.HasPrefix(inner, "int32"):
		return new([]int32)
	case strings.HasPrefix(inner, "float64"):
		return new([]float64)
	default:
		return new([]any)
	}
}

func newColumnHolder(typ string) any {
	t := strings.ToLower(typ)
	// 数组列必须先处理：transactions 的 signers/account_keys/program_ids 与 logs 的
	// topics 都是 Array(String)。此前没有这一支，它们会落到 default 的 *string，
	// 驱动报 "(signers string) column signers - needs a slice or any"——**transactions
	// 数据集整条导出直接失败**（实测：Actions csv+zip 两个数据集 4.5 s 后 FAILED）。
	switch {
	case strings.HasPrefix(t, "array("):
		return newArrayHolder(t)
	}
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
	case strings.HasPrefix(t, "float32"):
		return new(float32)
	case strings.HasPrefix(t, "float64"):
		return new(float64)
	default:
		// LowCardinality(String)/String/Enum/UUID 等一律按字符串接收。
		return new(string)
	}
}
