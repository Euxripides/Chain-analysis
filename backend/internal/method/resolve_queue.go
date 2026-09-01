package method

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ResolveRequestSubject 未知 Selector 异步补全主题（§17/§18）。
const ResolveRequestSubject = "method.resolve.request"

// Publisher 事件总线发布函数（§18：只传元数据，绝不把完整 input 塞进队列）。
type Publisher func(subject string, payload any)

// Enqueuer 未知 Selector 去重入队（§19）。
//
// 百万笔 0xa9059cbb 交易不能产生百万条 Resolve Job：
//   - 进程内去重键 chain + contract_address + method_id（singleflight 语义）；
//   - 数据库唯一约束兜底（多 Worker / 重启后仍然只有一条）。
type Enqueuer struct {
	store   Store
	publish Publisher
	log     *slog.Logger

	jobs chan ResolveJob
	mu   sync.Mutex
	seen map[string]time.Time
}

const (
	enqueueBufferSize = 4096
	enqueueBatchSize  = 500
	enqueueSeenLimit  = 200000
)

func NewEnqueuer(store Store, publish Publisher, log *slog.Logger) *Enqueuer {
	return &Enqueuer{
		store: store, publish: publish, log: log,
		jobs: make(chan ResolveJob, enqueueBufferSize),
		seen: map[string]time.Time{},
	}
}

func jobKey(job ResolveJob) string {
	return strings.ToLower(job.Chain) + "|" + strings.ToLower(job.ContractAddress) + "|" + strings.ToLower(job.MethodID)
}

// Enqueue 非阻塞投递。热路径调用，绝不能阻塞下载（§62-11）。
func (e *Enqueuer) Enqueue(job ResolveJob) {
	if e == nil || job.MethodID == "" {
		return
	}
	key := jobKey(job)
	e.mu.Lock()
	if len(e.seen) >= enqueueSeenLimit {
		e.seen = map[string]time.Time{}
	}
	if _, exists := e.seen[key]; exists {
		e.mu.Unlock()
		return
	}
	e.seen[key] = time.Now()
	e.mu.Unlock()

	select {
	case e.jobs <- job:
	default:
		// 队列满：撤销去重标记，等下一次同 selector 出现时重试
		e.mu.Lock()
		delete(e.seen, key)
		e.mu.Unlock()
	}
}

// Run 批量落库（默认每 2 秒或满 500 条一批）。
func (e *Enqueuer) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	batch := make([]ResolveJob, 0, enqueueBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := e.store.EnqueueMethodResolve(ctx, batch); err != nil {
			e.log.Warn("enqueue method resolve failed", "count", len(batch), "err", err)
		} else if e.publish != nil {
			for _, job := range batch {
				e.publish(ResolveRequestSubject, map[string]any{
					"chain": job.Chain, "contract_address": job.ContractAddress,
					"method_id": job.MethodID, "sample_tx_hash": job.SampleTxHash,
					"input_length": job.InputLength,
				})
			}
		}
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if len(batch) > 0 {
				_ = e.store.EnqueueMethodResolve(flushCtx, batch)
			}
			cancel()
			return
		case job := <-e.jobs:
			batch = append(batch, job)
			if len(batch) >= enqueueBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// ---- Async Method Resolver Worker（§18）----

// ResolverWorker 后台补全未知 Selector。
// 只有后台才允许访问公共签名库（§40/§62-8）。
type ResolverWorker struct {
	Store    Store
	Registry *Registry
	Public   *PublicSignatureResolver
	Log      *slog.Logger
	Interval time.Duration
	Batch    int
}

func (w *ResolverWorker) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := w.ProcessOnce(ctx); err != nil {
				w.Log.Warn("method resolver worker failed", "err", err)
			} else if n > 0 {
				w.Log.Info("method resolver processed", "jobs", n)
			}
			if depth, err := w.Store.MethodResolveQueueDepth(ctx); err == nil {
				ResolveQueueDepth.Set(float64(depth))
			}
		}
	}
}

// ProcessOnce 处理一批补全任务。
func (w *ResolverWorker) ProcessOnce(ctx context.Context) (int, error) {
	batch := w.Batch
	if batch <= 0 {
		batch = 50
	}
	jobs, err := w.Store.LeaseMethodResolveJobs(ctx, batch)
	if err != nil {
		return 0, err
	}
	for _, job := range jobs {
		status, message := w.resolveJob(ctx, job)
		if err := w.Store.FinishMethodResolveJob(ctx, job.ID, status, message); err != nil {
			w.Log.Warn("finish method resolve job failed", "id", job.ID, "err", err)
		}
	}
	return len(jobs), nil
}

func (w *ResolverWorker) resolveJob(ctx context.Context, job ResolveJob) (string, string) {
	// 1) ABI 或本地签名可能在入队之后才补齐
	if res := ChooseCandidate(job.MethodID, -1, w.Registry.Signatures(job.MethodID)); res.Resolved() {
		return "RESOLVED", ""
	}
	// 2) 公共签名库仅作候选补全，verified 恒为 false（§31）
	if w.Public == nil || !w.Public.Enabled() {
		return "UNRESOLVED", "no local candidate; public signature lookup disabled"
	}
	records, err := w.Public.Lookup(ctx, job.MethodID)
	if err != nil {
		return "UNRESOLVED", err.Error()
	}
	if len(records) == 0 {
		return "UNRESOLVED", "no public signature found"
	}
	if _, err := w.Store.UpsertMethodSignatures(ctx, records); err != nil {
		return "UNRESOLVED", err.Error()
	}
	w.Registry.mergeSignatures(records)
	return "RESOLVED", ""
}

// mergeSignatures 就地合入新签名，避免等待下一次全量刷新。
func (r *Registry) mergeSignatures(records []SignatureRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range records {
		selector := strings.ToLower(strings.TrimSpace(rec.Selector))
		if selector == "" || rec.Signature == "" {
			continue
		}
		exists := false
		for _, c := range r.signatures[selector] {
			if c.Signature == rec.Signature {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		name := rec.Name
		if name == "" {
			name = NameOf(rec.Signature)
		}
		r.signatures[selector] = append(r.signatures[selector], MethodCandidate{
			Name: name, Signature: rec.Signature, Source: rec.Source, Verified: rec.Verified,
		})
		sortCandidates(r.signatures[selector])
	}
	RegistryEntries.WithLabelValues("selectors").Set(float64(len(r.signatures)))
}
