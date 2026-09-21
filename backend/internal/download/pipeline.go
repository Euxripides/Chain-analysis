// 异步 Ingest 流水线（文档 §50/§55-§58）：
// Fetch 只负责 Durable Spool + Checkpoint；Parse → Cross-Shard Batch Aggregator → ClickHouse
// 全部移出 Fetch Critical Path（§4：Fetch 不等待 ClickHouse / Metadata / Activity / 对象存储）。
package download

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/spool"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// pageRef 一页待 Ingest 工作单元（§80 下载页）。
type pageRef struct {
	pageID        string
	jobID         string
	shardID       string
	dataset       string
	pageNo        int
	chain         string
	address       string
	reqHash       string
	rawPath       string
	metaPath      string
	meta          spool.PageMeta
	minB          int64
	maxB          int64
	snapshotBlock uint64
	uploadTries   int
}

// chunk 聚合器缓冲块：一个 page 可拆成多个 kind（如 transactions 页 → tx + activity）。
type chunk struct {
	rows    any
	credits []pageCredit
	bytes   int64
}

type pageCredit struct {
	pageID   string
	inserted int
	fact     bool // 只把事实表行计入 rows_inserted/unique_rows（activity 是派生表，§60）
}

type pagePending struct {
	parsed    int
	inserted  int
	remaining int
}

type uploadInfo struct {
	key       string
	sha256    string
	bytesGz   int64
	rows      int
	pageNo    int
	tokenHash string
}

type ingestPipeline struct {
	cfg   *config.Config
	store *store.Store
	spool *spool.Store
	raw   *rawstore.Store
	log   *slog.Logger
	sc    *Scheduler

	queue   chan pageRef
	uploadQ chan pageRef
	agg     *batchAggregator

	mu      sync.Mutex
	pending map[string]*pagePending

	ingested  sync.Map // pageID -> bool
	scheduled sync.Map // pageID -> bool; prevents recovery/finalizer duplicate replay
	uploads   sync.Map // pageID -> uploadInfo
	ctx       context.Context
}

func newIngestPipeline(cfg *config.Config, st *store.Store, sp *spool.Store, raw *rawstore.Store, log *slog.Logger) *ingestPipeline {
	return &ingestPipeline{
		cfg: cfg, store: st, spool: sp, raw: raw, log: log,
		queue:   make(chan pageRef, 2048),
		uploadQ: make(chan pageRef, 2048),
		pending: map[string]*pagePending{},
	}
}

func (p *ingestPipeline) start(ctx context.Context) {
	p.ctx = ctx
	p.agg = newBatchAggregator(p.cfg, p.sc.writer, p.log)
	p.agg.pipeline = p
	go p.agg.run(ctx)
	for i := 0; i < p.cfg.Ankr.ParserSlots; i++ {
		go p.parserWorker(ctx)
	}
	for i := 0; i < 2; i++ {
		go p.uploadWorker(ctx)
	}
}

// submit 页面 DURABLE 后投递（队列满时阻塞 = Ingest Backpressure，§71）。
func (p *ingestPipeline) submit(ctx context.Context, pr pageRef) bool {
	// Recovery and the finalizer may observe the same DURABLE page concurrently.
	// Keep exactly one copy in the parse/aggregate pipeline until it either
	// becomes INGESTED or fails and is eligible for a later retry.
	if _, loaded := p.scheduled.LoadOrStore(pr.pageID, true); loaded {
		return true
	}
	var pctx <-chan struct{}
	if p.ctx != nil {
		pctx = p.ctx.Done()
	}
	select {
	case <-ctx.Done():
		p.scheduled.Delete(pr.pageID)
		return false
	case <-pctx:
		p.scheduled.Delete(pr.pageID)
		return false
	case p.queue <- pr:
		observability.IngestQueueDepth.Set(float64(len(p.queue)))
		return true
	}
}

func (p *ingestPipeline) parserWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case pr := <-p.queue:
			observability.IngestQueueDepth.Set(float64(len(p.queue)))
			p.processPage(ctx, pr)
		}
	}
}

func (p *ingestPipeline) processPage(ctx context.Context, pr pageRef) {
	start := time.Now()
	rows, err := p.spool.LoadRows(spool.PageFile{PageNo: pr.pageNo, RawPath: pr.rawPath, MetaPath: pr.metaPath, Meta: pr.meta})
	if err != nil {
		// 原始 spool 对象已不存在 => 永久失败。若仍按可重试处理，finalizer
		// 每 30 秒重放一次同样的错误，Shard 永远停在 FETCHED，任务既不能
		// 完成也不能暂停收敛（现场 DL-20260909-0001，2026-09-08 起）。
		if errors.Is(err, fs.ErrNotExist) {
			// 但"文件不存在"也可能是良性竞态：finalizer 每 5 秒的补页查询取到
			// DURABLE 快照后，页面自己完成了入库并清掉 spool 文件，重放才轮到
			// 执行。此时行已经在 ClickHouse 里，把它标成 FAILED_FINAL 会让整个
			// Shard/Dataset/Job 变成 partial_failed（现场 DL-20260921-0041：
			// 815/816 分片，唯一失败的就是这种误判）。只有确认页面未曾入库才
			// 按不可恢复处理。
			if status, serr := p.store.PageStatus(ctx, pr.pageID); serr == nil &&
				(status == model.PageIngested || status == model.PageValidated) {
				p.log.Info("replayed page already ingested, spool copy is gone",
					"shard_id", pr.shardID, "page_no", pr.pageNo, "status", status)
				p.ingested.Store(pr.pageID, true)
				p.scheduled.Delete(pr.pageID)
				return
			}
			p.failPagePermanent(ctx, pr, "spool load: "+err.Error())
			return
		}
		p.failPage(ctx, pr, "spool load: "+err.Error())
		return
	}
	batch, err := p.sc.buildRows(ctx, pr.dataset, rows, pr.chain, pr.address, pr.snapshotBlock)
	if err != nil {
		p.failPage(ctx, pr, "normalize: "+err.Error())
		return
	}
	// rows_parsed is the count of successfully normalized fact rows. Malformed
	// provider records stay auditable in the durable raw page and logs, but do
	// not make the rest of a valid TRON page fail row-consistency validation.
	parsed := normalizedFactRows(pr.dataset, batch)

	type kindDef struct {
		kind  string
		rows  any
		bytes int64
		fact  bool
	}
	var kinds []kindDef
	switch pr.dataset {
	case "transactions":
		if len(batch.txs) > 0 {
			kinds = append(kinds, kindDef{"tx", batch.txs, pr.meta.DecodedBytes, true})
		}
		if len(batch.acts) > 0 {
			kinds = append(kinds, kindDef{"act", batch.acts, pr.meta.DecodedBytes / 2, false})
		}
	case "token_transfers":
		if len(batch.transfers) > 0 {
			kinds = append(kinds, kindDef{"transfer", batch.transfers, pr.meta.DecodedBytes, true})
		}
		if len(batch.acts) > 0 {
			kinds = append(kinds, kindDef{"act", batch.acts, pr.meta.DecodedBytes / 2, false})
		}
	case "nft_transfers":
		kinds = append(kinds, kindDef{"nft", batch.nfts, pr.meta.DecodedBytes, true})
	case "logs":
		kinds = append(kinds, kindDef{"log", batch.logRows, pr.meta.DecodedBytes, true})
		// Solana 程序日志的原生扩展表：只做增强，不计入 fact 行数，
		// 否则 finalize 的 rows_parsed/rows_inserted 口径会被同一批日志算两遍。
		if len(batch.solanaLogs) > 0 {
			kinds = append(kinds, kindDef{"solanalog", batch.solanaLogs, 0, false})
		}
	case "balances":
		kinds = append(kinds, kindDef{"bal", batch.bals, pr.meta.DecodedBytes, true})
	case "internal_transactions":
		if len(batch.internals) > 0 {
			kinds = append(kinds, kindDef{"internal", batch.internals, pr.meta.DecodedBytes, true})
		}
		if len(batch.acts) > 0 {
			kinds = append(kinds, kindDef{"act", batch.acts, pr.meta.DecodedBytes / 2, false})
		}
	}

	if len(kinds) == 0 {
		// 空页（0 行）：直接 INGESTED，避免 finalizer 永远等待（§56 边界）。
		ctx2, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = p.store.UpdatePageIngested(ctx2, pr.pageID, 0, 0, 0)
		cancel()
		p.ingested.Store(pr.pageID, true)
		p.scheduled.Delete(pr.pageID)
		if _, ok := p.uploads.Load(pr.pageID); ok {
			p.spool.RemovePage(pr.rawPath, pr.metaPath)
		}
		return
	}
	p.mu.Lock()
	p.pending[pr.pageID] = &pagePending{parsed: parsed, remaining: len(kinds)}
	p.mu.Unlock()
	for _, k := range kinds {
		p.agg.add(k.kind, chunk{rows: k.rows, bytes: k.bytes, credits: []pageCredit{{pageID: pr.pageID, inserted: rowLen(k.rows), fact: k.fact}}})
	}
	observability.IngestLatencySeconds.Observe(time.Since(start).Seconds())

	// 异步 Raw Upload（§44：不阻塞 Fetch/Ingest）
	select {
	case p.uploadQ <- pr:
	default:
		p.log.Warn("raw upload queue full, page upload deferred", "shard_id", pr.shardID, "page_no", pr.pageNo)
		go func() { // 兜底：不丢上传任务（pipeline 停止时退出）
			select {
			case <-p.ctx.Done():
			case p.uploadQ <- pr:
			}
		}()
	}
}

func normalizedFactRows(dataset string, batch rowBatch) int {
	switch dataset {
	case "transactions":
		return len(batch.txs)
	case "token_transfers":
		return len(batch.transfers)
	case "nft_transfers":
		return len(batch.nfts)
	case "logs":
		return len(batch.logRows)
	case "balances":
		return len(batch.bals)
	case "internal_transactions":
		return len(batch.internals)
	default:
		return 0
	}
}

func rowLen(rows any) int {
	switch r := rows.(type) {
	case []ingest.TxRow:
		return len(r)
	case []ingest.TokenTransferRow:
		return len(r)
	case []ingest.NftTransferRow:
		return len(r)
	case []ingest.LogRow:
		return len(r)
	case []ingest.BalanceRow:
		return len(r)
	case []ingest.ActivityRow:
		return len(r)
	case []ingest.InternalTransferRow:
		return len(r)
	}
	return 0
}

func (p *ingestPipeline) failPage(ctx context.Context, pr pageRef, msg string) {
	if err := p.store.FailPage(ctx, pr.pageID, msg); err != nil {
		p.log.Error("fail page failed", "err", err)
	}
	p.log.Error("page ingest failed", "shard_id", pr.shardID, "page_no", pr.pageNo, "err", msg)
	p.scheduled.Delete(pr.pageID)
}

// failPagePermanent 标记不可恢复的页（原始 spool 对象丢失）：不再重放。
func (p *ingestPipeline) failPagePermanent(ctx context.Context, pr pageRef, msg string) {
	if err := p.store.FailPageFinal(ctx, pr.pageID, msg); err != nil {
		p.log.Error("fail page final failed", "err", err)
	}
	p.log.Error("page ingest failed permanently", "shard_id", pr.shardID, "page_no", pr.pageNo, "err", msg)
	p.scheduled.Delete(pr.pageID)
}

// onChunksFlushed 聚合器 flush 成功后回调（每 chunk 一次）。
func (p *ingestPipeline) onChunksFlushed(credits []pageCredit) {
	for _, c := range credits {
		p.mu.Lock()
		pend := p.pending[c.pageID]
		if pend != nil {
			if c.fact {
				pend.inserted += c.inserted
			}
			pend.remaining--
			if pend.remaining > 0 {
				p.mu.Unlock()
				continue
			}
			delete(p.pending, c.pageID)
		}
		p.mu.Unlock()
		if pend == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := p.store.UpdatePageIngested(ctx, c.pageID, pend.parsed, pend.inserted, pend.inserted); err != nil {
			p.log.Error("mark page ingested failed", "page_id", c.pageID, "err", err)
		} else {
			p.ingested.Store(c.pageID, true)
			observability.IngestRowsTotal.Add(float64(pend.inserted))
			// 若已上传 → 立即释放 spool 页面文件（§72 水位）
			if _, ok := p.uploads.Load(c.pageID); ok {
				if pr, ok2 := p.pageByID(c.pageID); ok2 {
					p.spool.RemovePage(pr.rawPath, pr.metaPath)
				}
			}
		}
		// A transient metadata write failure must be retryable. ClickHouse writes
		// are idempotent on the stable row key, so replay is safe.
		p.scheduled.Delete(c.pageID)
		cancel()
	}
}

// onChunksFailed flush 失败回灌：pending 状态保持不变（同一 credit 会在重试成功后
// 再次走 onChunksFlushed 扣减），页面保持 DURABLE 等待重放（§59 幂等）。
func (p *ingestPipeline) onChunksFailed(credits []pageCredit) {
	// no-op：remaining 只由成功 flush 扣减，失败重试不改变计数。
	_ = credits
}

// pageByID 供清理用：上传成功时记录 raw/meta 路径。
var pageFiles sync.Map // pageID -> pageRef

func (p *ingestPipeline) rememberPage(pr pageRef) {
	pageFiles.Store(pr.pageID, pr)
}

func (p *ingestPipeline) pageByID(pageID string) (pageRef, bool) {
	v, ok := pageFiles.Load(pageID)
	if !ok {
		return pageRef{}, false
	}
	return v.(pageRef), true
}

// uploadWorker 异步上传 Raw Part 到对象存储（§44）。
func (p *ingestPipeline) uploadWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case pr := <-p.uploadQ:
			if err := p.uploadPage(ctx, pr); err != nil {
				pr.uploadTries++
				if pr.uploadTries < 10 {
					backoff := time.Duration(1<<uint(minInt(pr.uploadTries, 6))) * time.Second
					select {
					case <-ctx.Done():
						return
					case <-time.After(backoff):
						p.uploadQ <- pr
					}
				} else {
					p.log.Error("raw upload permanently failed, keeping local spool copy",
						"shard_id", pr.shardID, "page_no", pr.pageNo, "err", err)
				}
			}
		}
	}
}

func (p *ingestPipeline) uploadPage(ctx context.Context, pr pageRef) error {
	data, err := os.ReadFile(pr.rawPath)
	if err != nil {
		return fmt.Errorf("read raw: %w", err)
	}
	key := rawPartKey(p.sc.sourceProviderForDataset(pr.chain, pr.dataset), pr.chain, pr.dataset, pr.reqHash, pr.pageNo, pr.meta.ReceivedAt)
	n, err := p.raw.PutGzip(ctx, key, data)
	if err != nil {
		return err
	}
	info := uploadInfo{key: key, sha256: pr.meta.SHA256, bytesGz: n, rows: pr.meta.Rows, pageNo: pr.pageNo, tokenHash: pr.meta.NextPageTokenHash}
	p.uploads.Store(pr.pageID, info)
	if _, ok := p.ingested.Load(pr.pageID); ok {
		p.spool.RemovePage(pr.rawPath, pr.metaPath)
	}
	return nil
}

func rawPartKey(providerName, chain, dataset, reqHash string, pageNo int, received time.Time) string {
	return fmt.Sprintf("raw/%s/%s/%s/%s/%s/%s/part-%06d.json.gz",
		providerName, chain, dataset, received.Format("2006"), received.Format("01"), reqHash, pageNo)
}

func rawManifestKey(providerName, chain, dataset, reqHash string, started time.Time) string {
	return fmt.Sprintf("raw/%s/%s/%s/%s/%s/%s/manifest.json.gz",
		providerName, chain, dataset, started.Format("2006"), started.Format("01"), reqHash)
}

// ---- Cross-Shard Batch Aggregator（§55-§57）----

type batchAggregator struct {
	cfg    *config.Config
	writer *ingest.Writer
	log    *slog.Logger

	mu        sync.Mutex
	cond      *sync.Cond
	chunks    map[string][]chunk
	bytes     map[string]int64
	rowCount  map[string]int64
	lastFlush map[string]time.Time
	closed    bool

	healthy    bool
	lastFailAt time.Time

	pipeline *ingestPipeline

	muLat sync.Mutex
	lat   []time.Duration
}

func newBatchAggregator(cfg *config.Config, w *ingest.Writer, log *slog.Logger) *batchAggregator {
	a := &batchAggregator{
		cfg: cfg, writer: w, log: log,
		chunks: map[string][]chunk{}, bytes: map[string]int64{}, rowCount: map[string]int64{},
		lastFlush: map[string]time.Time{}, healthy: true,
	}
	a.cond = sync.NewCond(&a.mu)
	return a
}

func (a *batchAggregator) add(kind string, c chunk) {
	a.mu.Lock()
	a.chunks[kind] = append(a.chunks[kind], c)
	a.bytes[kind] += c.bytes
	a.rowCount[kind] += int64(rowLen(c.rows))
	a.cond.Broadcast()
	a.mu.Unlock()
	a.updatePendingMetric()
}

func (a *batchAggregator) updatePendingMetric() {
	a.mu.Lock()
	var rows int64
	for _, n := range a.rowCount {
		rows += n
	}
	a.mu.Unlock()
	observability.CHPendingRows.Set(float64(rows))
}

// run 定时器触发 flush（§56：wait >= 250ms）+ 启动 CH Writer（§58：2~4 个）。
func (a *batchAggregator) run(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Ankr.CHBatchFlush)
	defer ticker.Stop()
	for i := 0; i < a.cfg.Ankr.CHWriterSlots; i++ {
		go a.writerLoop(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			a.mu.Lock()
			a.closed = true
			a.cond.Broadcast()
			a.mu.Unlock()
			return
		case <-ticker.C:
			a.mu.Lock()
			a.cond.Broadcast()
			a.mu.Unlock()
		}
	}
}

// pickKind 选择要 flush 的 kind：超阈值优先，其次最久未 flush 的最大缓冲。
func (a *batchAggregator) pickKindLocked() string {
	now := time.Now()
	best := ""
	bestScore := int64(-1)
	for kind, b := range a.bytes {
		n := a.rowCount[kind]
		overThreshold := b >= a.cfg.Ankr.CHBatchMaxBytes || n >= int64(a.cfg.Ankr.CHBatchMaxRows)
		idle := now.Sub(a.lastFlush[kind])
		if len(a.chunks[kind]) == 0 {
			continue
		}
		score := int64(0)
		if overThreshold {
			score = 1 << 60
		} else if idle >= a.cfg.Ankr.CHBatchFlush {
			score = 0
		} else {
			continue
		}
		score += b
		if score > bestScore {
			bestScore = score
			best = kind
		}
	}
	return best
}

func (a *batchAggregator) writerLoop(ctx context.Context) {
	failCount := 0
	for {
		a.mu.Lock()
		for !a.closed && a.pickKindLocked() == "" {
			a.cond.Wait()
		}
		if a.closed {
			a.mu.Unlock()
			return
		}
		kind := a.pickKindLocked()
		chunks := a.chunks[kind]
		a.chunks[kind] = nil
		a.bytes[kind] = 0
		a.rowCount[kind] = 0
		a.lastFlush[kind] = time.Now()
		a.mu.Unlock()
		a.updatePendingMetric()

		start := time.Now()
		err := a.flush(ctx, kind, chunks)
		if err != nil {
			failCount++
			a.mu.Lock()
			a.healthy = false
			a.lastFailAt = time.Now()
			a.chunks[kind] = append(chunks, a.chunks[kind]...)
			var bytes int64
			for _, c := range chunks {
				bytes += c.bytes
			}
			a.bytes[kind] += bytes
			a.rowCount[kind] += totalChunkRows(chunks)
			a.mu.Unlock()
			a.updatePendingMetric()
			a.log.Error("clickhouse flush failed, requeued", "kind", kind, "err", err, "fail_count", failCount)
			if p := a.scPipeline(); p != nil {
				p.onChunksFailed(flattenCredits(chunks))
			}
			backoff := time.Duration(1<<uint(minInt(failCount, 5))) * time.Second
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}
		failCount = 0
		a.mu.Lock()
		a.healthy = true
		a.mu.Unlock()
		a.recordLatency(time.Since(start))
		observability.CHBatchRows.Observe(float64(totalChunkRows(chunks)))
	}
}

func flattenCredits(chunks []chunk) []pageCredit {
	var out []pageCredit
	for _, c := range chunks {
		out = append(out, c.credits...)
	}
	return out
}

func totalChunkRows(chunks []chunk) int64 {
	var n int64
	for _, c := range chunks {
		n += int64(rowLen(c.rows))
	}
	return n
}

func (a *batchAggregator) recordLatency(d time.Duration) {
	a.muLat.Lock()
	a.lat = append(a.lat, d)
	if len(a.lat) > 64 {
		a.lat = a.lat[1:]
	}
	a.muLat.Unlock()
}

// InsertP95 最近插入延迟 P95（Backpressure 信号，§73）。
func (a *batchAggregator) InsertP95() time.Duration {
	a.muLat.Lock()
	defer a.muLat.Unlock()
	if len(a.lat) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), a.lat...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(len(sorted)*95)/100]
}

func (a *batchAggregator) Healthy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.healthy
}

func (a *batchAggregator) PendingBytes() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	var n int64
	for _, b := range a.bytes {
		n += b
	}
	return n
}

// flush 合并跨 Shard 批次写 ClickHouse（§57：按 Dataset 分 Buffer，禁止混写）。
func (a *batchAggregator) flush(ctx context.Context, kind string, chunks []chunk) error {
	switch kind {
	case "tx":
		rows := make([]ingest.TxRow, 0)
		for _, c := range chunks {
			rows = append(rows, c.rows.([]ingest.TxRow)...)
		}
		if err := a.writer.WriteTransactions(ctx, rows); err != nil {
			return err
		}
	case "transfer":
		rows := make([]ingest.TokenTransferRow, 0)
		for _, c := range chunks {
			rows = append(rows, c.rows.([]ingest.TokenTransferRow)...)
		}
		if err := a.writer.WriteTokenTransfers(ctx, rows); err != nil {
			return err
		}
	case "nft":
		rows := make([]ingest.NftTransferRow, 0)
		for _, c := range chunks {
			rows = append(rows, c.rows.([]ingest.NftTransferRow)...)
		}
		if err := a.writer.WriteNftTransfers(ctx, rows); err != nil {
			return err
		}
	case "log":
		rows := make([]ingest.LogRow, 0)
		for _, c := range chunks {
			rows = append(rows, c.rows.([]ingest.LogRow)...)
		}
		if err := a.writer.WriteLogs(ctx, rows); err != nil {
			return err
		}
	case "solanalog":
		rows := make([]ingest.SolanaProgramLogRow, 0)
		for _, c := range chunks {
			rows = append(rows, c.rows.([]ingest.SolanaProgramLogRow)...)
		}
		if len(rows) > 0 {
			if err := a.writer.WriteSolanaProgramLogs(ctx, rows); err != nil {
				return err
			}
		}
	case "bal":
		rows := make([]ingest.BalanceRow, 0)
		for _, c := range chunks {
			rows = append(rows, c.rows.([]ingest.BalanceRow)...)
		}
		if err := a.writer.WriteBalances(ctx, rows); err != nil {
			return err
		}
	case "act":
		rows := make([]ingest.ActivityRow, 0)
		for _, c := range chunks {
			rows = append(rows, c.rows.([]ingest.ActivityRow)...)
		}
		if err := a.writer.WriteActivity(ctx, rows); err != nil {
			return err
		}
	case "internal":
		rows := make([]ingest.InternalTransferRow, 0)
		for _, c := range chunks {
			rows = append(rows, c.rows.([]ingest.InternalTransferRow)...)
		}
		if err := a.writer.WriteInternalTransfers(ctx, rows); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown chunk kind %s", kind)
	}
	if p := a.scPipeline(); p != nil {
		p.onChunksFlushed(flattenCredits(chunks))
	}
	return nil
}

// scPipeline 从聚合器回调到 pipeline（在 start() 装配）。
func (a *batchAggregator) scPipeline() *ingestPipeline { return a.pipeline }

// scPipeline 由 Scheduler 调用装配聚合器回调（启动时）。
func (a *batchAggregator) setPipeline(p *ingestPipeline) { a.pipeline = p }
