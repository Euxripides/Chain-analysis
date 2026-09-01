package method

import (
	"context"
	"log/slog"
	"time"
)

// ---- Existing Data Backfill（§34/§35）----
//
// 硬约束：历史交易只做本地重解析，禁止为了补 Method 重新向 Ankr 下载（§62-12）。
// 任务优先级 background，分批 + checkpoint，可暂停/恢复/取消，绝不整表同步 UPDATE（§33）。

// TxRecord ClickHouse transactions 的完整一行（重写时必须原样保留 input，§62-2）。
type TxRecord struct {
	Chain            string
	BlockNumber      uint64
	BlockHash        string
	TxHash           string
	Timestamp        time.Time
	FromAddress      string
	ToAddress        string
	ValueRaw         string
	Nonce            uint64
	Gas              uint64
	GasUsed          uint64
	GasPrice         string
	TxType           string
	Status           uint8
	Input            string
	MethodID         string
	SourceProvider   string
	MethodName       string
	MethodSignature  string
	MethodSource     string
	MethodVerified   uint8
	MethodConfidence string
	MethodType       string
	MethodResolvedAt *time.Time
}

// BackfillDataStore 数据面扫描与重写。
type BackfillDataStore interface {
	CountTransactions(ctx context.Context, chain string) (int64, error)
	ScanTransactions(ctx context.Context, chain, afterTxHash string, limit int) ([]TxRecord, error)
	RewriteTransactions(ctx context.Context, rows []TxRecord) error
}

// BackfillRunner 后台回填执行器。
type BackfillRunner struct {
	Store     Store
	Data      BackfillDataStore
	Resolver  *FastResolver
	Log       *slog.Logger
	Interval  time.Duration
	BatchIdle time.Duration
}

// Run 常驻循环：每 Interval 检查是否有可执行的回填任务。
func (b *BackfillRunner) Run(ctx context.Context) {
	interval := b.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			job, err := b.Store.NextRunnableBackfillJob(ctx)
			if err != nil {
				b.Log.Warn("backfill lookup failed", "err", err)
				continue
			}
			if job == nil {
				continue
			}
			if err := b.RunJob(ctx, job.ID); err != nil {
				b.Log.Error("backfill job failed", "job_id", job.ID, "err", err)
			}
		}
	}
}

// RunJob 执行一个回填任务直到完成 / 暂停 / 取消。
func (b *BackfillRunner) RunJob(ctx context.Context, jobID string) error {
	job, err := b.Store.GetBackfillJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}
	if err := b.Store.SetBackfillStatus(ctx, job.ID, BackfillRunning, ""); err != nil {
		return err
	}
	if job.TotalRows == 0 {
		if total, err := b.Data.CountTransactions(ctx, job.Chain); err == nil {
			_ = b.Store.SetBackfillTotal(ctx, job.ID, total)
			job.TotalRows = total
		}
	}
	batchSize := job.BatchSize
	if batchSize <= 0 {
		batchSize = 20000
	}
	cursor := job.CheckpointTxHash
	scanned, rewritten, resolved, unknown := job.ScannedRows, job.RewrittenRows, job.ResolvedRows, job.UnknownRows

	for {
		select {
		case <-ctx.Done():
			// 进程退出：保留 checkpoint，下次从同一位置继续（§35）
			return b.Store.SetBackfillStatus(ctx, job.ID, BackfillPaused, "worker stopped")
		default:
		}

		current, err := b.Store.GetBackfillJob(ctx, job.ID)
		if err != nil {
			return err
		}
		if current == nil {
			return nil
		}
		switch current.Status {
		case BackfillPauseReq:
			return b.Store.SetBackfillStatus(ctx, job.ID, BackfillPaused, "")
		case BackfillCancelReq, BackfillCancelled:
			return b.Store.SetBackfillStatus(ctx, job.ID, BackfillCancelled, "")
		}

		rows, err := b.Data.ScanTransactions(ctx, job.Chain, cursor, batchSize)
		if err != nil {
			_ = b.Store.SetBackfillStatus(ctx, job.ID, BackfillFailed, err.Error())
			return err
		}
		if len(rows) == 0 {
			return b.Store.SetBackfillStatus(ctx, job.ID, BackfillCompleted, "")
		}

		updates := make([]TxRecord, 0, len(rows))
		var lastTimestamp time.Time
		for i := range rows {
			row := rows[i]
			cursor = row.TxHash
			lastTimestamp = row.Timestamp
			scanned++
			resolution, _ := b.Resolver.Resolve(ctx, MethodResolveRequest{
				Chain: row.Chain, ContractAddress: row.ToAddress, Input: row.Input,
				TxHash: row.TxHash, Value: row.ValueRaw, ToIsNull: row.ToAddress == "",
			})
			if resolution.Resolved() {
				resolved++
			} else {
				unknown++
			}
			if !needsRewrite(row, resolution) {
				continue
			}
			updates = append(updates, applyResolution(row, resolution))
		}
		if len(updates) > 0 {
			if err := b.Data.RewriteTransactions(ctx, updates); err != nil {
				_ = b.Store.SetBackfillStatus(ctx, job.ID, BackfillFailed, err.Error())
				return err
			}
			rewritten += int64(len(updates))
			BackfillRowsTotal.Add(float64(len(updates)))
		}
		checkpoint := lastTimestamp
		if err := b.Store.CheckpointBackfillJob(ctx, job.ID, &checkpoint, cursor, scanned, rewritten, resolved, unknown); err != nil {
			b.Log.Warn("backfill checkpoint failed", "job_id", job.ID, "err", err)
		}
		if len(rows) < batchSize {
			return b.Store.SetBackfillStatus(ctx, job.ID, BackfillCompleted, "")
		}
		// background 优先级：批次之间让路给用户实时下载（§35）
		idle := b.BatchIdle
		if idle <= 0 {
			idle = 200 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return b.Store.SetBackfillStatus(ctx, job.ID, BackfillPaused, "worker stopped")
		case <-time.After(idle):
		}
	}
}

// needsRewrite 只有解析结果确实更好或字段缺失时才重写（避免无意义写放大）。
func needsRewrite(row TxRecord, resolution MethodResolution) bool {
	if row.MethodSource == "" || row.MethodSource == SourceUnknown {
		// Unknown 也是一个可持久化的稳定结果。只有字段实际变化时重写，
		// 否则每次 backfill 都会重复写入全部未知 selector。
		return resolution.MethodID != row.MethodID || resolution.Name != row.MethodName ||
			resolution.Signature != row.MethodSignature || resolution.Source != row.MethodSource ||
			resolution.Confidence != row.MethodConfidence || resolution.MethodType != row.MethodType ||
			resolution.Verified != (row.MethodVerified == 1)
	}
	if !resolution.Resolved() {
		return false
	}
	if Better(resolution.Source, row.MethodSource) {
		return true
	}
	return resolution.Source == row.MethodSource && (resolution.MethodID != row.MethodID || resolution.Name != row.MethodName ||
		resolution.Signature != row.MethodSignature || resolution.Confidence != row.MethodConfidence ||
		resolution.Verified != (row.MethodVerified == 1) || resolution.MethodType != row.MethodType)
}

// applyResolution 把解析结果写回行；input 与其它原始字段原样保留。
func applyResolution(row TxRecord, resolution MethodResolution) TxRecord {
	row.MethodID = resolution.MethodID
	row.MethodName = resolution.Name
	row.MethodSignature = resolution.Signature
	row.MethodSource = resolution.Source
	row.MethodConfidence = resolution.Confidence
	row.MethodType = resolution.MethodType
	row.MethodVerified = 0
	if resolution.Verified {
		row.MethodVerified = 1
	}
	if resolution.Resolved() {
		resolvedAt := resolution.ResolvedAt
		if resolvedAt.IsZero() {
			resolvedAt = time.Now().UTC()
		}
		row.MethodResolvedAt = &resolvedAt
	} else {
		row.MethodResolvedAt = nil
	}
	return row
}
