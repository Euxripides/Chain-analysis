package api

import (
	"bufio"
	"compress/flate"
	"context"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/query"
)

/*
 * 流式导出：边从 ClickHouse 取、边富化、边写进 io.Pipe → MinIO。
 *
 * 动机（实测，见 cmd/exportcost 与 internal/api/export_flowcost_test.go）：
 * 非流式路径要把 20 万行 × 27 列的 map 全部攒在内存里（峰值约 1.6 GiB），
 * 再走一遍序列化。流式把内存压到单块级别，并且让"取数 / 富化 / 序列化 / 上传"
 * 四段重叠起来。
 *
 * 与批量路径的**语义等价**由三条保证：
 *  1. 去重键与顺序沿用查询层：ForEachExportChunk 用同一 seen 集合跨块收敛，
 *     结果集本身按 timestamp DESC 排序，因此每块的存活行与批量路径逐行一致；
 *  2. 富化按块做，但每块的派生值只取决于该行的合约与金额，与"和谁一批"无关——
 *     唯一例外是 annotateRows 的 matched_address 依赖 subject 列，这里显式传入；
 *  3. 序列化复用同一套 appendCSVValue / 引号规则。
 */

// exportStreamEnabled 控制单地址导出是否走流式路径。
//
// 默认开启；EXPORT_STREAM=0 可强制回退批量路径，用于 A/B 对照与线上回滚
// （两条路径的输出必须逐字节一致，见 export_stream_equivalence_test.go）。
func exportStreamEnabled() bool {
	return os.Getenv("EXPORT_STREAM") != "0"
}

// exportStreamRawLimit 把"目标不同行数"换算成"要扫描的原始行数"。
//
// 非流式路径（rangeExportNoFinal）用同一系数超采，取不满还会重试；流式路径在
// io.Pipe 上无法回退重试，因此必须在取数前就把系数算进去，否则遇到 31.4% 重复率的
// 链（bsc）会少给约三成行。上限与批量路径的重试上限保持一致。
func exportStreamRawLimit(want int) int {
	raw := int(float64(want) * query.DedupeExportRetryFactor)
	if raw > query.DedupeExportMaxFetch {
		raw = query.DedupeExportMaxFetch
	}
	return raw
}

// streamEnrichment 是流式导出过程中跨块复用的状态。
type streamEnrichment struct {
	srv   *Server
	chain string
	wsID  string

	tokenPlans  map[string]tokenMetaPlan
	amountCache map[amountKey]string
	known       map[string]struct{} // 已查过元数据的合约（含查不到的）
	unknown     []string            // 待批量查询的新合约

	caseCache    map[string][]string
	tagCache     map[string][]string
	rowContracts []string // 复用缓冲：本块每行的归一化合约

	includeMeta bool
}

func newStreamEnrichment(srv *Server, chain, wsID string, includeMeta bool) *streamEnrichment {
	return &streamEnrichment{
		srv: srv, chain: chain, wsID: wsID, includeMeta: includeMeta,
		tokenPlans:  map[string]tokenMetaPlan{},
		amountCache: map[amountKey]string{},
		known:       map[string]struct{}{},
		caseCache:   map[string][]string{},
		tagCache:    map[string][]string{},
	}
}

// resolveTokens 按块补齐新出现的合约元数据，并把每行的合约名缓存到 rowContracts
// （避免 enrich 再逐行归一化一次——批量路径此前正是在这里重复计算了两次）。
//
// TokenMeta.GetMany 会对传入的每个地址做 ANY(...) 查询，没有缓存；逐行调用会让它
// 变成每行一次 SQL。这里按"本块新出现的合约"批量取，并记录 known，后续块不再查询。
//
// **每个新合约都会在 tokenPlans 里留下一条记录**（元数据缺失时是零值 plan）。
// 这一点是必须的：applyTokenMeta 用"是否在 plans 里"判断该行要不要写 token_contract
// /raw_value/formatted_amount 三列。若只给查到元数据的合约建条目，缺元数据的行就会
// 少这三列，与批量路径产出**列集不同**的文件（CSV 列集取自第一块的行）。
func (e *streamEnrichment) resolveTokens(ctx context.Context, rows []map[string]any) {
	if len(e.rowContracts) < len(rows) {
		e.rowContracts = make([]string, len(rows))
	}
	e.rowContracts = e.rowContracts[:len(rows)]
	if e.srv.deps.TokenMeta == nil {
		for i, row := range rows {
			e.rowContracts[i] = streamRowContract(e.chain, row)
		}
		return
	}

	e.unknown = e.unknown[:0]
	for i, row := range rows {
		contract := streamRowContract(e.chain, row)
		e.rowContracts[i] = contract
		if contract == "" {
			continue
		}
		if _, ok := e.known[contract]; ok {
			continue
		}
		e.known[contract] = struct{}{}
		e.unknown = append(e.unknown, contract)
		// 先落零值 plan：即使下面查询失败或该合约没有元数据，这一行也会按
		// "合约已知但元数据未知"的形态写全三列。
		e.tokenPlans[contract] = tokenMetaPlan{}
	}
	if len(e.unknown) == 0 {
		return
	}
	metas, err := e.srv.deps.TokenMeta.GetMany(ctx, e.chain, e.unknown)
	if err != nil {
		// 与批量路径一致：查不到就跳过元数据填充，不放大成导出失败。
		return
	}
	for contract, m := range metas {
		plan := tokenMetaPlan{
			name: m.Name, symbol: m.Symbol, tokenType: m.TokenType,
			status: m.MetadataStatus, source: m.MetadataSource, confidence: m.Confidence,
		}
		if m.Decimals != nil {
			plan.decimals = *m.Decimals
			plan.decimalsKnown = true
		}
		e.tokenPlans[contract] = plan
	}
}

// enrich 对单块施加与批量路径相同的派生列。
func (e *streamEnrichment) enrich(ctx context.Context, subject string, rows []map[string]any) {
	if len(rows) == 0 {
		return
	}
	e.resolveTokens(ctx, rows)
	if len(e.tokenPlans) > 0 {
		for i, row := range rows {
			contract := e.rowContracts[i]
			if contract == "" {
				continue
			}
			applyTokenMeta(row, contract, e.tokenPlans, e.amountCache, amountCacheLimit)
		}
	}
	if e.includeMeta {
		e.annotate(ctx, subject, rows)
	}
	// decoded_input 面向 transactions 且默认关闭，流式路径保持与批量一致：
	// 需要该列时由调用方回退到非流式路径（见 writeSingleExport）。
}

// annotate 是 annotateRows 的流式版本：按块解析案件/标签名，缓存跨块复用。
func (e *streamEnrichment) annotate(ctx context.Context, subject string, rows []map[string]any) {
	if e.wsID == "" {
		// 拿不到任务自己的作用域就不附加案件/标签名，绝不回退到默认工作区
		// （否则会把另一个租户的案件名写进导出文件）。
		return
	}
	for _, r := range rows {
		matched := matchedExportAddresses(e.chain, subject, r)
		var cases, tags []string
		for _, a := range matched {
			if v, ok := e.caseCache[a]; ok {
				cases = append(cases, v...)
			} else {
				names, _ := e.srv.deps.Store.AddressCaseNames(ctx, e.wsID, e.chain, a)
				e.caseCache[a] = names
				cases = append(cases, names...)
			}
			if v, ok := e.tagCache[a]; ok {
				tags = append(tags, v...)
			} else {
				names, _ := e.srv.deps.Store.AddressTagNames(ctx, e.wsID, e.chain, a)
				e.tagCache[a] = names
				tags = append(tags, names...)
			}
		}
		r["matched_address"] = strings.Join(matched, "|")
		r["case_names"] = strings.Join(dedupeStrings(cases), "|")
		r["tag_names"] = strings.Join(dedupeStrings(tags), "|")
	}
}

// streamRowContract 与 attachTokenMetadata 的归一化规则保持一致。
func streamRowContract(chain string, row map[string]any) string {
	norm := func(value any) string {
		text := asString(value)
		if chain == "tron" {
			return strings.TrimSpace(text)
		}
		return strings.ToLower(strings.TrimSpace(text))
	}
	contract := norm(row["token_address"])
	if contract == "" {
		contract = norm(row["contract_address"])
	}
	return contract
}

// streamCSVWriter 是增量 CSV 写入器：与 streamCSV 共用取值与引号规则，
// 但列集在收到第一块时确定，之后逐块追加。
type streamCSVWriter struct {
	bw   *bufio.Writer
	cols []string
	n    int64
	none bool // 是否还没收到任何块（用于写表头）
}

func newStreamCSVWriter(w io.Writer) *streamCSVWriter {
	return &streamCSVWriter{bw: bufio.NewWriterSize(w, 1<<20), none: true}
}

func (w *streamCSVWriter) write(rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	if w.none {
		w.cols = mapKeys(rows[0])
		for i, c := range w.cols {
			if i > 0 {
				w.bw.WriteByte(',')
			}
			writeCSVField(w.bw, c)
		}
		w.bw.WriteByte('\n')
		w.none = false
	}
	buf := make([]byte, 0, 4096)
	for _, r := range rows {
		buf = buf[:0]
		for i, c := range w.cols {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendCSVValue(buf, r[c])
		}
		buf = append(buf, '\n')
		if _, err := w.bw.Write(buf); err != nil {
			return err
		}
		w.n += int64(len(rows))
	}
	return nil
}

func (w *streamCSVWriter) close() error { return w.bw.Flush() }

// streamJSONLWriter 是流式 NDJSON 写入器。
type streamJSONLWriter struct {
	bw  *bufio.Writer
	enc interface{ Encode(v any) error }
	n   int64
}

func newStreamJSONLWriter(w io.Writer) *streamJSONLWriter {
	bw := bufio.NewWriterSize(w, 1<<20)
	return &streamJSONLWriter{bw: bw, enc: json.NewEncoder(bw)}
}

func (w *streamJSONLWriter) write(rows []map[string]any) error {
	for _, r := range rows {
		if err := w.enc.Encode(r); err != nil {
			return err
		}
		w.n++
	}
	return nil
}

func (w *streamJSONLWriter) close() error { return w.bw.Flush() }

// streamZipCSVWriter 是流式 ZIP 条目写入器：对每一块增量 deflate，
// 并累计 CRC32 与未压缩字节数（ZIP 头需要）。
type streamZipCSVWriter struct {
	fw      *flate.Writer
	hash    hash32
	n       int64
	cols    []string
	none    bool
	inner   *streamCSVWriter
	rawSink *countingWriter
}

type hash32 interface {
	io.Writer
	Sum32() uint32
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// newStreamZipCSVWriter 返回一个把 CSV 内容 deflate 后写入 w 的写入器。
// 返回的 crc/size 供 ZIP 头使用。
func newStreamZipCSVWriter(w io.Writer, level int) (*streamZipCSVWriter, error) {
	fw, err := flate.NewWriter(w, level)
	if err != nil {
		return nil, err
	}
	hash := crc32.NewIEEE()
	raw := &countingWriter{w: io.MultiWriter(fw, hash)}
	inner := newStreamCSVWriter(raw)
	return &streamZipCSVWriter{fw: fw, hash: hash, inner: inner, rawSink: raw, none: true}, nil
}

func (w *streamZipCSVWriter) write(rows []map[string]any) error {
	return w.inner.write(rows)
}

func (w *streamZipCSVWriter) close() (uint32, int64, error) {
	if err := w.inner.close(); err != nil {
		_ = w.fw.Close()
		return 0, 0, err
	}
	if err := w.fw.Close(); err != nil {
		return 0, 0, err
	}
	return w.hash.Sum32(), w.rawSink.n, nil
}
