package query

import (
	"context"
	"os"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

/*
 * ---- 导出取数的两阶段形状：先定时间下界，再带界取数 -------------------------------
 *
 * 背景（2026-09-25 在真实实例上实测，canonical 地址 0x…0001，bsc，全量历史）：
 *
 *	SELECT * ... WHERE chain AND (from OR to) ORDER BY timestamp DESC LIMIT 200000
 *	  → 3.68 s；而只读 timestamp 一列的聚合（GROUP BY toDate(timestamp)）只要 146 ms、
 *	    130 MiB。诊断出真正的成本不是扫描也不是排序：
 *	      · 只扫不排序（LIMIT，无 ORDER BY）        0.17 s
 *	      · 只排序一列（timestamp）取前 20 万        0.83 s
 *	   都很快；慢的是**把 22 列宽行物化/搬运到 LIMIT 指定的行数**。
 *	   一旦先用时间下界把候选行数压到 24.5 万，同样的 SELECT * + LIMIT 只要 0.59 s。
 *
 * 所以两阶段形状是：
 *	1) 边界查询：只读 timestamp 列，按天聚合行数，从最新一天往回累加到目标总量，
 *	   取那一天的 00:00 作为下界。实测 146 ms（读 1260 万行、内存 130 MiB）——
 *	   同一批行如果读 22 列要 3.68 s。
 *	2) 带界取数：WHERE ... AND timestamp >= ? ORDER BY timestamp DESC LIMIT ?，
 *	   取回的行数被下界限制在目标量级，物化成本随之下降约 6 倍。
 *
 * 正确性：下界只用于**裁剪候选行**，最终行集仍由 LIMIT + Go 侧去重决定。
 * 因此"边界取宽了"只损失速度，"取窄了"才会少给行——所以按天聚合的累加刻意多取
 * 一整天（conservative），并由调用方在不足时用更早的边界重试，最后兜底回退到
 * 原有的一条无界查询。
 *
 * 为什么不用分区统计（system.parts 的 min/max）：分区是 (chain, toYYYYMM(timestamp))，
 * 粒度到月，而一个月的行数可达数百万，无法定位到"第 N 行"；按天聚合才能把下界压到
 * 目标量级，且它的成本只与"只读一列"有关。
 */

// exportBoundedEnabled 控制两阶段形状是否启用。
//
// 默认启用；EXPORT_BOUNDED=0 可一键回退到无界形状（两阶段改变了"扫描哪些行"，
// 虽然 TestBoundedFetchMatchesUnbounded 已锁定集合等价，线上仍需一个不改代码的
// 回滚开关）。
func exportBoundedEnabled() bool {
	return os.Getenv("EXPORT_BOUNDED") != "0"
}

// minExportBoundaryLimit 是启用两阶段形状的最小 limit：低于它时多一次边界查询
// （约 150 ms）抵不过省下的物化开销。
const minExportBoundaryLimit = 5000

// exportBoundaryProbeLimit 是**定界查询**最多按多少行去找边界。
//
// 它不代表"超过就不定界"：导出调用方的上限是 maxExportRows=200 万，而单个地址的
// 不同转账数往往只有 170 万。若超过它就直接放弃定界，回退的无界查询会按 1.45 倍
// 超采去物化 290 万行 × 30 列，实测 RowsForExportRange 从 25.9 s 恶化到 97.5 s
// （比不定界还慢 3.8 倍）。正确做法是按上限夹取探测目标——边界更保守、可用行数更多，
// 正确性不变，而带界查询仍能靠 LIMIT 早停。
const exportBoundaryProbeLimit = 2_000_000

// exportWhere 返回导出形状的过滤条件（与 RowsForExportRange / 流式路径同一口径）。
func exportWhere(dataset, chain, subject string) (string, []any) {
	switch dataset {
	case "logs", "balances":
		return "chain=? AND address=?", []any{chain, subject}
	case "transactions", "token_transfers", "nft_transfers", "internal_transactions":
		return "chain=? AND (from_address=? OR to_address=?)", []any{chain, subject, subject}
	default:
		return "chain=?", []any{chain}
	}
}

// 边界累加的初始倍数与重试增长。
//
// 必须按**去重率**放大而不是只放大一点点：定界用的是原始行数，而目标是"不同转账键"
// 的条数，原表同一键可能有多版本行（bsc 实测重复率 31.4%，tron 9.6%）。实测踩过的坑：
// 目标 100 万行时若只按 1.0 倍定界，边界下只有 1,351,877 原始行 → 去重后 999,849 个键，
// 差 151 个；而调用方的 LIMIT 是 1,450,000（1.45 倍超采），**超过可用行数**，
// ClickHouse 于是退化成无界扫描（实测 27 s，比不使用本形状还慢一倍）。
const (
	exportBoundaryGrowthInit = 1.6
	exportBoundaryGrowthStep = 0.35
)

// exportBoundaryMaxRatio 防止重试把边界放大到无意义（超过它说明该地址总行数不足）。
const exportBoundaryMaxRatio = 3.0

// dayBucket 是一天的行数。
type dayBucket struct {
	day   time.Time
	count uint64
}

// exportTimeBoundary 用单列聚合求出"覆盖目标行数"的时间下界。
//
// 返回 ok=false 表示该数据集/请求不适合两阶段形状（调用方走原有形状）。
func (s *Service) exportTimeBoundary(ctx context.Context, dataset, chain, subject string,
	fromBlock, toBlock *int64, fromTime, toTime *time.Time, limit, offset int, ratio float64) (time.Time, uint64, bool) {
	if !exportBoundedEnabled() || offset != 0 || limit < minExportBoundaryLimit {
		return time.Time{}, 0, false
	}
	// 探测目标夹到上限之内：limit 超过上限时仍可以定界，只是最多按探测上限的
	// 行数去找边界（拿到的边界更保守、可用行数更多，不影响正确性）。
	if limit > exportBoundaryProbeLimit {
		limit = exportBoundaryProbeLimit
	}
	// 带范围条件时下界会和调用方条件相互影响，交给原有形状，避免语义漂移。
	if fromBlock != nil || toBlock != nil || fromTime != nil || toTime != nil {
		return time.Time{}, 0, false
	}
	where, args := exportWhere(dataset, chain, subject)
	if _, ok := exportSelectNoFinal(dataset, where); !ok {
		return time.Time{}, 0, false
	}

	// 只读 timestamp 列：这是整个形状能快起来的关键。
	q := "SELECT toDate(timestamp) AS day, count() AS n FROM blockchain." + tableName(dataset) +
		" WHERE " + where + " GROUP BY day ORDER BY day DESC"
	rows, err := s.CH.DB().Query(clickhouse.ExportScanContext(ctx), q, args...)
	if err != nil {
		return time.Time{}, 0, false
	}
	defer rows.Close()

	target := uint64(float64(limit) * ratio * dedupeExportRetryFactor)
	var cumulative uint64
	for rows.Next() {
		var day time.Time
		var n uint64
		if err := rows.Scan(&day, &n); err != nil {
			return time.Time{}, 0, false
		}
		cumulative += n
		if cumulative >= target {
			return day.UTC(), cumulative, true
		}
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, 0, false
	}
	// 累加到最后一桶仍不够：地址总量不足目标，交给原有形状（它会返回全部行）。
	return time.Time{}, 0, false
}

// exportBoundedWhere 在原有过滤条件上追加时间下界，返回 (where, args)。
func exportBoundedWhere(dataset, chain, subject string, bound time.Time) (string, []any) {
	where, args := exportWhere(dataset, chain, subject)
	return where + " AND timestamp >= ?", append(args, bound)
}

// BoundedExportSelect 供流式导出使用：返回已带时间下界的查询与参数。
//
// 流式路径（边取边写 MinIO）无法像批量路径那样"取不满再重试"，因此这里只做
// 一次定界并给出一个足够宽的下界；取不满由调用方按同一去重键收敛后的结果体现
// （导出契约是"该地址在此范围内的全部行"，与历史行为一致）。
//
// 返回的 wantLimit 是流式收敛目标（不同键的条数），fetch 是要扫描的原始行数。
// ok=false 表示该请求不适合两阶段，调用方应使用无界形状。
func (s *Service) BoundedExportSelect(ctx context.Context, dataset, chain, subject string, limit int) (sel string, args []any, fetch int, ok bool) {
	bound, available, found := s.exportTimeBoundary(ctx, dataset, chain, subject,
		nil, nil, nil, nil, limit, 0, exportBoundaryGrowthInit)
	if !found {
		return "", nil, 0, false
	}
	where, whereArgs := exportBoundedWhere(dataset, chain, subject, bound)
	sel, ok = exportSelectNoFinal(dataset, where)
	if !ok {
		return "", nil, 0, false
	}
	fetch = int(float64(limit) * dedupeExportRetryFactor)
	if uint64(fetch) > available {
		fetch = int(available)
	}
	// LIMIT 必须小于边界下的可用行数，否则 ClickHouse 会退化成无界扫描。
	if fetch < limit {
		return "", nil, 0, false
	}
	// 占位符顺序：(where 参数) → timestamp 下界 → LIMIT → OFFSET
	return sel, append(append([]any{}, whereArgs...), fetch, 0), fetch, true
}

// rowsForExportBounded 是两阶段形状的主入口：先按天聚合定出时间下界，
// 再带界取全列，按去重键收敛并截断到 limit。返回 ok=false 表示不适用，
// 调用方必须执行原有形状。
func (s *Service) rowsForExportBounded(ctx context.Context, dataset, chain, subject string,
	limit, offset int) ([]map[string]any, bool, error) {
	// 本形状只服务单页导出（offset==0）；分页交给原有形状，避免下界与游标各算一套。
	if offset != 0 {
		return nil, false, nil
	}

	ratio := exportBoundaryGrowthInit
	for attempt := 0; attempt < dedupeExportMaxAttempts; attempt++ {
		bound, available, ok := s.exportTimeBoundary(ctx, dataset, chain, subject,
			nil, nil, nil, nil, limit, offset, ratio)
		if !ok {
			return nil, false, nil
		}
		where, args := exportBoundedWhere(dataset, chain, subject, bound)
		sel, ok := exportSelectNoFinal(dataset, where)
		if !ok {
			return nil, false, nil
		}

		// fetch（要扫描并物化的原始行数）必须**严格小于**边界下的可用行数，
		// 否则 LIMIT 失去早停作用、查询退化成全量物化。
		//
		// 实测踩过的坑：目标 200 万、地址只有 173 万个不同转账时，可用行数被
		// 超采推到 465 万，fetch 也顶到 290 万——于是每一行都被物化成 map，
		// 97 s（比不定界的 25.9 s 还慢 3.8 倍）。带上限余量后，超采到
		// available 的 90% 就够，Fetch 停在 2.9M 以内且必然早停。
		// 余量充足时（可用行数已远超目标）不必超采：超采只是为了让"去重后仍够 limit"，
		// 而这里原始行数已经是目标的 2 倍以上，超采只会把 fetch 顶到 available 附近、
		// 让 LIMIT 失去早停作用。实测 173 万不同转账、目标 200 万时，超采把
		// fetch 推到 290 万，全部物化 -> 97 s；不超采后早停生效。
		fetch := int(float64(limit) * dedupeExportRetryFactor)
		if float64(available) > float64(limit)*dedupeExportRetryFactor {
			fetch = limit
		}
		maxUsable := int(float64(available) * 0.9)
		if fetch > maxUsable {
			fetch = maxUsable
		}
		if fetch < limit {
			// 没有余量：加大边界倍数重来，而不是发一条"要的比有的多"的查询。
			ratio += exportBoundaryGrowthStep
			if ratio > exportBoundaryMaxRatio {
				return nil, false, nil
			}
			continue
		}

		// 占位符顺序：(where 参数) → timestamp 下界 → LIMIT → OFFSET
		queryArgs := append(append([]any{}, args...), fetch, offset)
		raw, err := s.queryRowsExport(ctx, dataset, sel, limit, queryArgs...)
		if err != nil {
			return nil, false, err
		}
		distinct := dedupeExportRows(dataset, raw, limit)
		if s.Log != nil {
			s.Log.Info("带界导出取数结果",
				"dataset", dataset, "limit", limit, "bound", bound,
				"available", available, "fetch", fetch,
				"raw", len(raw), "distinct", len(distinct), "ratio", ratio)
		}
		if len(distinct) >= limit {
			return distinct, true, nil
		}
		if s.Log != nil {
			s.Log.Info("带界导出取数不足，加大边界倍数重试",
				"dataset", dataset, "chain", chain, "subject", subject,
				"want", limit, "got", len(distinct), "bound", bound,
				"available", available, "ratio", ratio, "attempt", attempt+1)
		}
		ratio += exportBoundaryGrowthStep
		if ratio > exportBoundaryMaxRatio {
			break
		}
	}
	// 兜底：回退到原有形状（无界查询）。
	return nil, false, nil
}
