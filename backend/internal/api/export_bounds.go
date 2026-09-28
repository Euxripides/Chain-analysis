package api

import (
	"context"
	"errors"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
)

/*
 * ---- 导出的时间范围 ---------------------------------------------------------------
 *
 * 用户要的是"全部，或者我指定的那段时间"。因此导出请求接受一组可选的时间范围参数
 * （闭区间，与 range_type=time 的下载任务同语义：timestamp>=from 且 timestamp<=to），
 * 不传就是全量——**全量由键集分页取完，不再有行数上限**（见 query/export_keyset.go）。
 *
 * 这样"按时间范围导出"在两个入口上是同一件事：
 *   - 直接导出：POST /exports {addresses, datasets, start_time, end_time}
 *   - 预生成：  POST /exports/prefetch {addresses, datasets, start_time, end_time}
 * 预生成的规格哈希包含该范围，因此"全量"与"某段时间"是两份不同的缓存产物，
 * 不会互相命中（否则用户会拿到范围外的数据）。
 */

// exportBounds 是一次导出请求的时间范围（闭区间，nil 表示不限）。
type exportBounds struct {
	From *time.Time
	To   *time.Time
}

func (b exportBounds) isZero() bool { return b.From == nil && b.To == nil }

// intersect 返回两个范围的交集（nil 视为无穷）。
//
// 请求范围与作业范围都要生效时必须取交集：作业是 range_type=time 时它自己带时间窗，
// 用户在导出时又给了一个窗口，两个窗口之外的行都不该出现。
// 交集为空（from > to）时不报错——那正是"该范围内没有数据"的语义。
func (b exportBounds) intersect(other exportBounds) exportBounds {
	out := b
	if other.From != nil && (out.From == nil || other.From.After(*out.From)) {
		out.From = other.From
	}
	if other.To != nil && (out.To == nil || other.To.Before(*out.To)) {
		out.To = other.To
	}
	return out
}

// boundsFromJob 把作业的范围折算成时间范围：只有 range_type=time 的作业能给出
// 时间范围，区块范围在取数时另行处理（exportJobBounds）。
func boundsFromJob(dataset string, job *model.DownloadJob) exportBounds {
	if job == nil {
		return exportBounds{}
	}
	_, _, fromTime, toTime := exportJobBounds(dataset, job)
	return exportBounds{From: fromTime, To: toTime}
}

// appendExportTimeBounds 把时间范围拼进 WHERE（闭区间）。参数顺序与 where 的
// 追加顺序一致，调用方必须把返回的 args 原样用于同一条查询。
func appendExportTimeBounds(where string, args []any, b exportBounds) (string, []any) {
	if b.From != nil {
		where += " AND timestamp>=?"
		args = append(args, *b.From)
	}
	if b.To != nil {
		where += " AND timestamp<=?"
		args = append(args, *b.To)
	}
	return where, args
}

// newExportBounds 校验并构造请求范围。区间必须是闭区间且 from<=to；
// 只给一端是合法的（"从某时刻起"或"截至某时刻"）。
func newExportBounds(from, to *time.Time) (exportBounds, error) {
	if from != nil && to != nil && from.After(*to) {
		return exportBounds{}, errExportBoundsOrder
	}
	return exportBounds{From: from, To: to}, nil
}

var errExportBoundsOrder = errors.New("start_time 不能晚于 end_time")

// maxMaterializedExportRows 是"必须把全部行同时放进内存"的写出器的内存上限：
// xlsx / parquet / 多条目 ZIP 的 map 路径，以及任何需要整批操作（整批哈希查询、
// 整批 GPT 过滤）的路径。
//
// 它与老的 maxExportRows 有本质区别：老上限是**静默截断**——取满 200 万行就停，
// 文件照样标 COMPLETED，用户拿到的是缺行却不自知的产物（实测 token_transfers
// 少了最老的 331,094 行）。这里超过上限是**明确失败**，并在错误里告诉用户
// 改用 csv（流式、无上限）或指定时间范围。
//
// 200 万行 × 约 1 KB/行 ≈ 2 GiB 常驻堆，正是实测把 GC 打满、单条目 16.6 s 的量级；
// 放宽它只会把"缺行"换成"OOM"。
const maxMaterializedExportRows = 2_000_000

// errExportTooLargeToMaterialize 是超过内存上限时的用户可见错误。
var errExportTooLargeToMaterialize = errors.New(
	"该导出需要物化的行数超过 200 万，xlsx/parquet/多条目 ZIP 无法在服务端在线生成：" +
		"请改用 csv（流式导出，无行数上限），或在导出时指定时间范围（start_time/end_time）后分次导出")

// appendExportBlockBounds 把区块范围拼进 WHERE（闭区间，与 range_type=block 同语义）。
func appendExportBlockBounds(where string, args []any, fromBlock, toBlock *int64) (string, []any) {
	if fromBlock != nil {
		where += " AND block_number>=?"
		args = append(args, *fromBlock)
	}
	if toBlock != nil {
		where += " AND block_number<=?"
		args = append(args, *toBlock)
	}
	return where, args
}

// collectExportRows 用键集分页把全量行物化到内存，供批量写出器使用。
//
// 与 RowsForExportRange 的差别：不再有"取满 N 行就停"的截断，而是取到取空；
// 唯一的限制是内存上限，超过即返回 errExportTooLargeToMaterialize。
func (s *Server) collectExportRows(ctx context.Context, dataset, chain, subject string, bounds exportBounds,
	fromBlock, toBlock *int64) ([]map[string]any, error) {
	table, where, args := exportStreamQueryParts(dataset, chain, subject)
	where, args = appendExportTimeBounds(where, args, bounds)
	where, args = appendExportBlockBounds(where, args, fromBlock, toBlock)

	rows := make([]map[string]any, 0, 4096)
	collect := func(chunk []map[string]any) error {
		rows = append(rows, chunk...)
		if len(rows) > maxMaterializedExportRows {
			return errExportTooLargeToMaterialize
		}
		return nil
	}
	if _, ok := query.ExportCursorFor(dataset); ok {
		_, _, err := s.query.ForEachExportChunkKeyset(ctx,
			query.ExportKeysetQuery{Dataset: dataset, Table: table, Where: where, Args: args},
			maxMaterializedExportRows+1, 0, collect)
		if err != nil {
			return nil, err
		}
		return rows, nil
	}
	// 无游标的数据集（balances 快照等）：单查询形状，仍带上限保护内存。
	_, err := s.query.ForEachExportChunk(ctx, dataset, exportStreamSQLUnbounded(table, where, 0),
		maxMaterializedExportRows+1, 0, collect, args...)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// exportBoundsDetail 是写进审计/导出详情的时间范围（RFC3339，nil→null）。
func (b exportBounds) auditFields() map[string]any {
	out := map[string]any{}
	if b.From != nil {
		out["from_time"] = b.From.UTC().Format(time.RFC3339)
	}
	if b.To != nil {
		out["to_time"] = b.To.UTC().Format(time.RFC3339)
	}
	return out
}
