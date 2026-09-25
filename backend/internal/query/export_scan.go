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

func newColumnHolder(typ string) any {
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
	case strings.HasPrefix(t, "float32"):
		return new(float32)
	case strings.HasPrefix(t, "float64"):
		return new(float64)
	default:
		// LowCardinality(String)/String/Enum/UUID 等一律按字符串接收。
		return new(string)
	}
}
