package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
)

/*
 * ---- 列式导出：不建 []map[string]any 的直写路径 -----------------------------------
 *
 * 动机（实测，canonical 地址 bsc 全量历史，生产 GC 配置）：
 *
 *	20 万行   无 map 直写下限 1.11-1.18 s   生产完整链路 5.50-5.90 s（中位）
 *	100 万行  无 map 直写下限 4.28 s        生产完整链路 23.9-26.9 s
 *
 * 现状是下限的 4.3-6.3 倍，差额来自 map[string]any 行表示：每行 30 个键、30 个装箱值，
 * 20 万行实测分配 1524 MiB（GC 标记占主要 CPU）；而且它强制富化必须"整批地图化"。
 *
 * 本文件把导出路径改成：从 ClickHouse 列持有变量直接写 CSV 单元格，富化列在写出的
 * 同时按 (合约, 金额) 记忆化算出——全程不构造行 map。
 *
 * **输出必须逐字节等于原有 map 路径**，靠三件事保证：
 *  1. 列集与列序：结果集列名 ∪ 富化列名，用与 mapKeys 完全相同的比较器排序
 *     （导出列序是契约的一部分，见 docs/implementation-notes.md 的"列序不可复现"条目）；
 *  2. 单元格取值：整数/时间/Decimal 的写法与 map 路径一致（时间 RFC3339Nano、
 *     Decimal String()，见 exportValue 的注释）；
 *  3. 富化列取值：与 applyTokenMeta 同一套规则（缺元数据时 token_contract/raw_value
 *     仍写、formatted_amount 为 nil）。
 *
 * 任何一步对不上就整体回退 map 路径（列名集合异常、数据集不支持），不做部分降级。
 */

// exportFieldRank 是 exportFieldOrder 的列名→名次索引（与 mapKeys 用同一份顺序）。
var exportFieldRank = func() map[string]int {
	m := make(map[string]int, len(exportFieldOrder))
	for i, name := range exportFieldOrder {
		m[name] = i
	}
	return m
}()

// enrichmentColumns 是富化会给一行补上的全部列名（applyTokenMeta 的写集）。
// token_decimals 不在此列：它是结果集既有列，富化会**覆盖**它的值。
var enrichmentColumns = []string{
	"token_contract", "raw_value", "formatted_amount", "token_name",
	"token_type", "token_metadata_status", "token_metadata_source", "token_metadata_confidence",
}

// columnarPlan 是列式导出的列映射：每一列要么取自结果集的某列，要么由富化现算。
// overwrittenByEnrichment 是"结果集里有同名列、但必须以富化值为准"的列。
// token_contract 就是这种：迁移里它存在于 token_transfers，但导出契约要求它等于
// 归一化后的合约地址（map 路径由 applyTokenMeta 无条件覆盖），实测两者会不一致
// （结果集里可能为空），所以列式路径也必须覆盖。
var overwrittenByEnrichment = map[string]string{
	"token_contract": "token_contract",
}

type columnarPlan struct {
	names []string
	// source[i] >= 0 表示第 i 列取自结果集 source[i]；-1 表示富化列。
	source []int
	// enrich[i] 是富化列的名字（source[i] == -1 时有效）。
	enrich []string
	// override[i] 表示该输出列取自结果集、但当富化命中时要用富化值替换
	// （目前只有 token_decimals：它在表里存在，而 applyTokenMeta 会用元数据覆盖它）。
	override []string

	contractCol int // token_address / contract_address 在结果集里的下标，-1 表示没有
	amountCol   int // amount_raw 的下标，-1 表示没有
}

// buildColumnarPlan 用结果集列名推出列式写出的列集与列序。
//
// 关键点：必须与 map 路径的 mapKeys(rows[0]) 同序。map 路径的键集是
// 「结果集列名 ∪ 实际写过的富化列」——token_name 只在元数据有名字时才出现，
// 但导出契约要求列集稳定，因此这里一律并入全部富化列（实测缺元数据的行
// 也会写 token_contract/raw_value/formatted_amount，其余列在整批里必然出现）。
func buildColumnarPlan(resultColumns []string) (*columnarPlan, error) {
	p := &columnarPlan{contractCol: -1, amountCol: -1}
	for i, n := range resultColumns {
		switch n {
		case "token_address", "contract_address":
			if p.contractCol < 0 {
				p.contractCol = i
			}
		case "amount_raw":
			p.amountCol = i
		}
	}

	all := make([]string, 0, len(resultColumns)+len(enrichmentColumns))
	seen := make(map[string]struct{}, len(resultColumns)+len(enrichmentColumns))
	for _, n := range resultColumns {
		if _, dup := seen[n]; dup {
			return nil, fmt.Errorf("columnar export: duplicate column %q in result set", n)
		}
		seen[n] = struct{}{}
		all = append(all, n)
	}
	for _, n := range enrichmentColumns {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		all = append(all, n)
	}
	// 与 mapKeys 相同的排序（exportFieldOrder 优先，未登记的按字典序兜底）。
	sort.SliceStable(all, func(i, j int) bool { return exportColumnLess(all[i], all[j]) })

	sourceOf := make(map[string]int, len(resultColumns))
	for i, n := range resultColumns {
		sourceOf[n] = i
	}
	p.names = all
	p.source = make([]int, len(all))
	p.enrich = make([]string, len(all))
	p.override = make([]string, len(all))
	for i, n := range all {
		if enrichName, replaced := overwrittenByEnrichment[n]; replaced {
			p.source[i] = -1
			p.enrich[i] = enrichName
			continue
		}
		if idx, ok := sourceOf[n]; ok {
			p.source[i] = idx
			p.enrich[i] = ""
			// token_decimals 在结果集里有值，但富化命中时必须用元数据的精度覆盖它
			// （与 applyTokenMeta 一致）。判据必须落在**输出列**上，不能拿结果集
			// 下标去比输出下标——两个坐标空间不同，实测因此写错了列。
			if n == "token_decimals" {
				p.override[i] = "decimals"
			}
			continue
		}
		p.source[i] = -1
		p.enrich[i] = n
	}
	return p, nil
}

// exportColumnLess 复用 mapKeys 的排序规则，保证列式路径与 map 路径同序。
func exportColumnLess(a, b string) bool {
	ra, oka := exportFieldRank[a]
	rb, okb := exportFieldRank[b]
	if oka != okb {
		return oka
	}
	if oka && okb && ra != rb {
		return ra < rb
	}
	return a < b
}

// streamRowContractValue 与 streamRowContract 同规则，但直接吃值（供列式路径用）。
func streamRowContractValue(chain string, value any) string {
	text, ok := value.(string)
	if !ok {
		if value == nil {
			return ""
		}
		text = fmt.Sprint(value)
	}
	if chain == "tron" {
		return strings.TrimSpace(text)
	}
	return strings.ToLower(strings.TrimSpace(text))
}

// exportColumnarEnabled 控制单地址导出是否走列式直写路径。
//
// 默认开启；EXPORT_COLUMNAR=0 可一键回退 map 路径（两条路径的输出必须逐字节一致，
// 见 export_columnar_equivalence_test.go）。
func exportColumnarEnabled() bool {
	return os.Getenv("EXPORT_COLUMNAR") != "0"
}

// exportColumnarStream 是列式导出的执行体：按块取数、按块富化、逐行直写 CSV，
// 全程不建行 map。
//
// 返回 (写出行数, 是否走通)。第二个值为 false 表示列式路径不适用（驱动不支持按列
// 消费 / 结果集列名异常），调用方必须回退 map 路径；此时已写出的内容由调用方丢弃。
func (s *Server) exportColumnarStream(ctx context.Context, dataset, chain, subject string,
	w io.Writer, limit int) (int64, bool, error) {
	const chunk = 32 * 1024

	sql := exportStreamSQL(dataset, chain, subject)
	sqlArgs := exportStreamArgs(chain, subject)
	wantLimit := limit
	if sel, selArgs, _, ok := s.query.BoundedExportSelect(ctx, dataset, chain, subject, limit); ok {
		sql, sqlArgs = sel, selArgs
	} else {
		sqlArgs = append(append([]any{}, sqlArgs...), exportStreamRawLimit(limit), 0)
	}

	bw := bufio.NewWriterSize(w, 1<<20)
	amounts := make(map[amountKey]string, 4096)
	plans := make(map[string]tokenMetaPlan, 256)
	known := make(map[string]struct{}, 256)
	var unknown []string
	var plan *columnarPlan
	headerWritten := false
	written := int64(0)
	buf := make([]byte, 0, 4096)
	rowContracts := make([]string, 0, chunk)

	// 每块分两遍：先收集本块出现的全部合约并批量补齐元数据，再逐行写出。
	// 必须整块收集——只按块内最后一行的合约去补元数据会漏掉其它合约
	// （实测：LYX 这类行整块写不出 token_name）。
	onChunk := func(c *query.ColumnChunk) error {
		if plan == nil {
			built, buildErr := buildColumnarPlan(c.Names)
			if buildErr != nil {
				return buildErr
			}
			plan = built
		}
		if !headerWritten {
			for i, n := range plan.names {
				if i > 0 {
					bw.WriteByte(',')
				}
				writeCSVField(bw, n)
			}
			bw.WriteByte('\n')
			headerWritten = true
		}

		// 第一遍：收集合约。
		if cap(rowContracts) < c.Rows {
			rowContracts = make([]string, c.Rows)
		}
		rowContracts = rowContracts[:c.Rows]
		unknown = unknown[:0]
		for j := 0; j < c.Rows; j++ {
			contract := ""
			if plan.contractCol >= 0 {
				contract = streamRowContractValue(chain, c.Cells[j*len(c.Names)+plan.contractCol])
			}
			rowContracts[j] = contract
			if contract == "" {
				continue
			}
			if _, seen := known[contract]; seen {
				continue
			}
			known[contract] = struct{}{}
			unknown = append(unknown, contract)
			plans[contract] = tokenMetaPlan{}
		}
		if len(unknown) > 0 && s.deps.TokenMeta != nil {
			if metas, metaErr := s.deps.TokenMeta.GetMany(ctx, chain, unknown); metaErr == nil {
				for c2, m := range metas {
					mp := tokenMetaPlan{
						name: m.Name, symbol: m.Symbol, tokenType: m.TokenType,
						status: m.MetadataStatus, source: m.MetadataSource, confidence: m.Confidence,
					}
					if m.Decimals != nil {
						mp.decimals = *m.Decimals
						mp.decimalsKnown = true
					}
					plans[c2] = mp
				}
			}
		}

		// 第二遍：写出。
		for j := 0; j < c.Rows; j++ {
			buf = buf[:0]
			buf = writeColumnarChunkRow(buf, plan, c, j, rowContracts[j], plans, amounts)
			buf = append(buf, '\n')
			if _, werr := bw.Write(buf); werr != nil {
				return werr
			}
			written++
		}
		return nil
	}

	_, ok, err := s.query.ForEachExportColumnChunk(ctx, dataset, sql, wantLimit, chunk, onChunk, sqlArgs...)
	if err != nil {
		return written, false, err
	}
	if !ok {
		return 0, false, nil
	}
	if err := bw.Flush(); err != nil {
		return written, true, err
	}
	return written, true, nil
}

// writeColumnarChunkRow 写出一行的所有列（不含行尾换行）。
func writeColumnarChunkRow(buf []byte, p *columnarPlan, c *query.ColumnChunk, rowIdx int,
	contract string, plans map[string]tokenMetaPlan, amounts map[amountKey]string) []byte {
	base := rowIdx * len(c.Names)
	amountRaw := ""
	if p.amountCol >= 0 {
		amountRaw = c.Cells[base+p.amountCol]
	}
	for i := range p.names {
		if i > 0 {
			buf = append(buf, ',')
		}
		if p.source[i] >= 0 {
			if p.override[i] == "decimals" && contract != "" {
				if mp, ok := plans[contract]; ok && mp.decimalsKnown {
					buf = appendCSVValue(buf, mp.decimals)
					continue
				}
			}
			buf = appendCSVQuoted(buf, c.Cells[base+p.source[i]])
			continue
		}
		buf = appendChunkEnrichment(buf, p.enrich[i], contract, amountRaw, plans, amounts)
	}
	return buf
}

// appendChunkEnrichment 按 applyTokenMeta 的规则算出一个富化单元格。
func appendChunkEnrichment(buf []byte, enrichCol, contract, amountRaw string,
	plans map[string]tokenMetaPlan, amounts map[amountKey]string) []byte {
	if contract == "" {
		// 没有合约地址的行：applyTokenMeta 直接跳过，这些列在该行不存在，
		// 在 CSV 里呈现为空字段。
		return buf
	}
	switch enrichCol {
	case "token_contract":
		return appendCSVQuoted(buf, contract)
	case "raw_value":
		return appendCSVQuoted(buf, amountRaw)
	}

	plan, ok := plans[contract]
	if !ok {
		return buf
	}
	switch enrichCol {
	case "token_name":
		return appendCSVQuoted(buf, plan.name)
	case "token_type":
		return appendCSVQuoted(buf, plan.tokenType)
	case "token_metadata_status":
		return appendCSVQuoted(buf, plan.status)
	case "token_metadata_source":
		return appendCSVQuoted(buf, plan.source)
	case "token_metadata_confidence":
		return appendCSVQuoted(buf, plan.confidence)
	case "formatted_amount":
		cacheKey := amountKey{contract: contract, raw: amountRaw}
		if cached, hit := amounts[cacheKey]; hit {
			return appendCSVQuoted(buf, cached)
		}
		value, isStr := formatTokenAmount(amountRaw, plan).(string)
		if !isStr {
			return buf
		}
		if len(amounts) < amountCacheLimit {
			amounts[cacheKey] = value
		}
		return appendCSVQuoted(buf, value)
	}
	return buf
}

// errColumnarUnsupported 表示该请求不满足列式直写的前提，调用方应回退 map 路径。
var errColumnarUnsupported = errors.New("columnar export unsupported for this request")

// canUseColumnarExport 判断该请求能否走列式直写。
//
// 刻意保守——语义相同才允许走：
//   - 只支持 csv（xlsx/parquet/jsonl 的写出器要么需要随机访问，要么逐 map 编码）；
//   - 不接受 GPT Actions 的 job（那条路径会按 token 过滤整批行）；
//   - 不开 include_decoded_input（要先按整批 tx_hash 查解码表）；
//   - 不开 include_meta（要按整个 subject 解析案件/标签名，列值取决于"整批"而不仅是本行）。
func canUseColumnarExport(job *model.DownloadJob, format, dataset string, includeMeta, includeDecodedInput bool) bool {
	if job != nil || includeMeta || includeDecodedInput {
		return false
	}
	if format != "csv" {
		return false
	}
	switch dataset {
	case "transactions", "token_transfers", "internal_transactions", "nft_transfers":
		return true
	default:
		return false
	}
}

// writeSingleExportColumnar 把单地址 CSV 导出写成对象：列式直写 → io.Pipe → MinIO。
func (s *Server) writeSingleExportColumnar(ctx context.Context, exportID, key, chain, addr, dataset string) (int64, string, exportLocation, error) {
	pr, pw := io.Pipe()
	type result struct {
		rows int64
		ok   bool
		err  error
	}
	done := make(chan result, 1)
	go func() {
		rows, ok, err := s.exportColumnarStream(ctx, dataset, chain, addr, pw, maxExportRows)
		if err == nil && !ok {
			// 列式不适用：让调用方回退，且不把已写内容当成成品。
			err = errColumnarUnsupported
		}
		_ = pw.CloseWithError(err)
		done <- result{rows: rows, ok: ok, err: err}
	}()

	_, loc, err := s.putExportStream(ctx, exportID, key, pr, -1, contentType("csv"))
	_ = pr.CloseWithError(err)
	res := <-done
	if errors.Is(res.err, errColumnarUnsupported) {
		return 0, "", loc, errColumnarUnsupported
	}
	if err != nil {
		return 0, "", loc, err
	}
	if res.err != nil {
		return 0, "", loc, res.err
	}
	sha, err := s.objectSHA256(ctx, key)
	return res.rows, sha, loc, err
}
