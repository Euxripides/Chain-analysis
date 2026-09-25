package query

import (
	"fmt"
	"log/slog"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

/*
 * ---- 导出扫描去掉 FINAL（保留 ReplacingMergeTree 语义的等价改写） -----------------
 *
 * 背景（2026-09-25 在真实实例上逐段实测，探针见 cmd/exportbench）：
 *
 *	bsc 上最重的地址 0x…0001，目标 20 万行，全量历史：
 *	  SELECT * ... FINAL ... ORDER BY timestamp DESC LIMIT ?
 *	    → 服务端 19.6-23.8 s，峰值 5.86 GiB；
 *	  同样条件去掉 FINAL
 *	    → 服务端 5.7 s，峰值 1.12 GiB；
 *	  两者按 (tx_hash, log_index, token_address) 去重后，20 万行 × 22 列**逐列相等**、
 *	  键集完全相同（唯一差异是 15 行的 source_ingested_at——ReplacingMergeTree 的版本列，
 *	  同一排序键保留哪个版本 FINAL 与"取回后去重"都没有规范保证，导出内容不依赖它）。
 *
 * 为什么 FINAL 这么贵：token_transfers 的排序键是 (chain, tx_hash, log_index, token_address)，
 * 而导出按地址过滤（from_address/to_address 上只有 bloom_filter 跳数索引）。
 * FINAL 会**先按排序键合并整条链**，跳数索引因此完全用不上；去掉 FINAL 后
 * 扫描量从 59.3M 行降到 25.6M 行，内存从 5.86 GiB 降到 1.12 GiB。
 *
 * 为什么去掉 FINAL 仍然等价：导出落盘前本来就有 Go 侧去重（internal/api 的
 * dedupeExportRows），它的键与 ReplacingMergeTree 的排序键是同一组列，
 * 因此最终写进文件的行集不变。
 *
 * 曾经考虑并**实测否决**的更激进形状（不要再试，数据在下面）：
 *
 *  1. 用 address_activity 第 N 行的时间戳作下界裁剪分区（服务端 3.7 s，最快）：
 *     该表对同一地址存在重复键（实测同一范围内 1,000,000 行只有 820,494 个不同键，
 *     原表同范围有 834,721 个），当边界会**系统性少给行**——200k 目标少 27 行，
 *     1M 目标只能取到 834,721 个且放宽重试无效。
 *
 *  2. 用 address_activity 的键集约束原表（IN 子查询）：同样受上述重复与缺键影响，
 *     1M 目标只能取到 805,702 个不同键。
 *
 *  3. 给 IN 子查询加 ORDER BY timestamp DESC LIMIT N：子查询要排序 20.4M 行，
 *     服务端反而涨到 3.8-5.5 s、2.75 GiB，并且会改变"取哪些行"的语义。
 *
 *  4. LIMIT 1 BY 下推去重：ClickHouse 会物化全部 22 列，服务端 17-26 s、11.19 GiB。
 *
 *  5. 保留现网那层"键 IN 子查询"（exportIndexedFinalWhere 去掉 FINAL）：该子查询
 *     没有 LIMIT，要先物化该地址的全部 20,407,856 个键，实测 200k 目标反而涨到
 *     23.85 s（比裸 FINAL 还慢）。所以无 FINAL 形状**不使用**这层子查询，
 *     只保留 where 直接过滤——bloom_filter 跳数索引本来就能把扫描裁到 25.6M 行。
 */

// dedupeExportRetryFactor / dedupeExportMaxAttempts / dedupeExportExtraRows 控制
// "去重后不足目标行数"时的超采重试：bsc 链上重复行占到 31.4%，按 1:1 取数会系统性
// 少给三成行。
const (
	dedupeExportRetryFactor = 1.45
	dedupeExportMaxAttempts = 3
	dedupeExportExtraRows   = 200_000
)

// NewWithLog 返回带日志的查询服务；不传日志时退化为无日志。
func NewWithLog(ch *clickhouse.Conn, log *slog.Logger) *Service {
	return &Service{CH: ch, Log: log}
}

// exportDedupeColumns 返回导出结果在 Go 侧去重时使用的键列，与 ClickHouse 侧
// ReplacingMergeTree 的排序键一致。返回 nil 表示该数据集不需要去重。
func exportDedupeColumns(dataset string) []string {
	switch dataset {
	case "transactions", "activity":
		return []string{"tx_hash"}
	case "token_transfers", "internal_transactions":
		return []string{"tx_hash", "log_index", "token_address"}
	case "nft_transfers":
		return []string{"tx_hash", "log_index", "contract_address", "token_id"}
	case "logs":
		return []string{"tx_hash", "log_index"}
	case "balances":
		return []string{"address", "token_address", "token_symbol"}
	default:
		return nil
	}
}

// dedupeExportRows 按 ReplacingMergeTree 排序键收敛重复行，保留首见行后截断到 limit。
//
// 输入已按 timestamp DESC 排序，因此保留的是这组行里最新的那个版本；
// 与 FINAL 的语义差异仅限 source_ingested_at 这类版本列（见文件头实测说明）。
//
// 键用 128 位 FNV：2,000,000 行量级下每行拼一次字符串键会额外产生数 GiB 临时对象
// （实测 100 万行 526 ms 且 GC 压力显著），哈希键只在栈上做算术。
func dedupeExportRows(dataset string, rows []map[string]any, limit int) []map[string]any {
	cols := exportDedupeColumns(dataset)
	if len(cols) == 0 || len(rows) == 0 {
		if limit > 0 && len(rows) > limit {
			return rows[:limit]
		}
		return rows
	}
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	type hash128 struct{ hi, lo uint64 }
	seen := make(map[hash128]struct{}, len(rows))
	out := rows[:0]
	for _, row := range rows {
		h1, h2 := uint64(offset64), uint64(offset64)^0x9e3779b97f4a7c15
		for _, col := range cols {
			value := fmt.Sprint(row[col])
			for i := 0; i < len(value); i++ {
				h1 = (h1 ^ uint64(value[i])) * prime64
				h2 = (h2 ^ uint64(value[i])) * (prime64 ^ 0x2545f4914f6cdd1d)
			}
			// 列分隔符：让 ("ab","c") 与 ("a","bc") 不落到同一个键。
			h1 = (h1 ^ 0x1f) * prime64
			h2 = (h2 ^ 0x1f) * (prime64 ^ 0x2545f4914f6cdd1d)
		}
		key := hash128{hi: h1, lo: h2}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
	}
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

// exportScanDatasets 是导出形状支持的数据集。
func exportScanDatasets(dataset string) bool {
	switch dataset {
	case "transactions", "token_transfers", "nft_transfers", "logs", "balances", "internal_transactions":
		return true
	default:
		return false
	}
}

// exportSelectNoFinal 生成不带 FINAL 的导出查询：列集、过滤条件、排序与现网完全一致，
// 只把"选版本"交给调用方的 Go 侧去重（见文件头实测）。
//
// 只对确有去重键的数据集生效；其余数据集无法在 Go 侧等价收敛时返回 ok=false，
// 调用方必须保留原形状。
func exportSelectNoFinal(dataset, where string) (string, bool) {
	if !exportScanDatasets(dataset) || len(exportDedupeColumns(dataset)) == 0 {
		return "", false
	}
	return "SELECT * FROM blockchain." + tableName(dataset) + " WHERE " + where +
		" ORDER BY timestamp DESC LIMIT ? OFFSET ?", true
}

// exportIndexKey 返回该数据集在"先按地址收敛候选键"子查询（现网 FINAL 形状使用）
// 里使用的键表达式。
func exportIndexKey(dataset string) (string, bool) {
	switch dataset {
	case "transactions":
		return "tx_hash", true
	case "token_transfers":
		return "(tx_hash, log_index, token_address)", true
	default:
		return "", false
	}
}
