// Package exportmigrate：F-R10-BUCKET 的对象桶迁移引擎（ADR-019 §2.3）。
//
// 硬约束（directive-bucket-inventory-migration-safety-1904.txt + 根 2026-09-13 追加口径）：
//  1. 默认 dry-run，只有显式 Execute 才复制；
//  2. 不以 PG sha 为唯一依据：有 PG sha 的必须先证明源 GET SHA == PG SHA 再复制；
//     缺 PG sha 的必须先算出并保留 source SHA-256，复制后与 destination SHA-256 逐对象一致；
//  3. 目标已存在同 key：只有全字节相同才记 ALREADY_VERIFIED，否则 CONFLICT + fail closed，**绝不覆盖**；
//  4. 逐对象复制后验证；ETag/size **不是**字节证明，必须用内容 SHA-256；
//  5. 非 404 错误（权限/网络/5xx）不得降级为"对象缺失"，一律 fail closed；
//  6. 不删除 RAW 对象、不移动对象、不回填/改写 exports 历史行（含 66 条缺 sha 的行）。
package exportmigrate

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
)

// 账本状态机。
const (
	StatusPending         = "PENDING"
	StatusCopied          = "COPIED"
	StatusVerified        = "VERIFIED"
	StatusAlreadyVerified = "ALREADY_VERIFIED"
	StatusConflict        = "CONFLICT"
	StatusFailed          = "FAILED"
)

// IsVerified 判断该状态是否代表"目标字节已被证明与源一致"。
func IsVerified(status string) bool {
	return status == StatusVerified || status == StatusAlreadyVerified
}

// Item 是一个待迁移对象（来自 exports 表；PGSHA256 可能为空，270 条中 66 条为空）。
// JSON tag 必须显式给出：隔离校准与计划文件用 snake_case 字段名，缺 tag 会导致
// 反序列化静默得到空对象（本轮实测到的失败：items[0] must have export_id and object_ref）。
type Item struct {
	ExportID  string `json:"export_id"`
	ObjectRef string `json:"object_ref"`
	PGSHA256  string `json:"pg_sha256"`
}

// Row 是账本行；source/destination/pg 三个 SHA 同时保留，便于离线复核。
type Row struct {
	ExportID          string `json:"export_id"`
	ObjectRef         string `json:"object_ref"`
	FromBucket        string `json:"from_bucket"`
	ToBucket          string `json:"to_bucket"`
	Bytes             int64  `json:"bytes"`
	SourceSHA256      string `json:"source_sha256"`
	DestinationSHA256 string `json:"destination_sha256"`
	PGSHA256          string `json:"pg_sha256"`
	Status            string `json:"status"`
	Attempts          int    `json:"attempts"`
	LastError         string `json:"last_error"`
}

// ObjectStore 是迁移所需的**最小**对象存储能力。实现必须是"对象级 404 才表示不存在"。
type ObjectStore interface {
	BucketExists(ctx context.Context, bucket string) (bool, error)
	// Stat 返回 (size, exists, err)。exists=false 只允许在对象级 404 时返回；
	// 桶不存在/权限/网络/5xx 必须返回 err，由调用方 fail closed。
	Stat(ctx context.Context, bucket, key string) (int64, bool, error)
	// SHA256 流式读取对象并复算内容 SHA-256，返回 (sha, size, err)。
	SHA256(ctx context.Context, bucket, key string) (string, int64, error)
	// Copy 从源桶流式复制到目标桶（不做删除/移动）。
	Copy(ctx context.Context, fromBucket, fromKey, toBucket, toKey, contentType string) (int64, error)
}

// Ledger 是迁移账本（幂等 + 可续跑 + 可审计）。
type Ledger interface {
	Get(ctx context.Context, exportID, toBucket string) (*Row, error)
	Upsert(ctx context.Context, row Row) error
	List(ctx context.Context, toBucket string) ([]Row, error)
}

// Engine 执行迁移；Store 与 Ledger 均为接口，便于隔离负向校准与单测。
type Engine struct {
	Store      ObjectStore
	Ledger     Ledger
	FromBucket string
	ToBucket   string
	Log        *slog.Logger
}

// Options 控制执行强度。
type Options struct {
	// Execute=false 为默认 dry-run：**不复制、不写账本**，只产出计划。
	Execute bool
	// Limit>0 时最多处理 Limit 个对象（用于分段/试点）。
	Limit int
	// StopOnError=true 时首个失败立即返回（默认 false：继续并完整报告失败清单）。
	StopOnError bool
}

// Outcome 是单个对象的处理结果（逐对象状态与原因）。
type Outcome struct {
	ExportID          string `json:"export_id"`
	ObjectRef         string `json:"object_ref"`
	Action            string `json:"action"`
	Status            string `json:"status"`
	Bytes             int64  `json:"bytes"`
	SourceSHA256      string `json:"source_sha256,omitempty"`
	DestinationSHA256 string `json:"destination_sha256,omitempty"`
	PGSHA256          string `json:"pg_sha256,omitempty"`
	Note              string `json:"note,omitempty"`
}

// Report 是整轮对账结果：总数 / 总字节 / 每对象 SHA / 状态分布。
type Report struct {
	FromBucket string         `json:"from_bucket"`
	ToBucket   string         `json:"to_bucket"`
	Execute    bool           `json:"execute"`
	Total      int            `json:"total"`
	TotalBytes int64          `json:"total_bytes"`
	ByStatus   map[string]int `json:"by_status"`
	ByAction   map[string]int `json:"by_action"`
	Outcomes   []Outcome      `json:"outcomes"`
	Failures   []string       `json:"failures"`
}

// AllVerified 表示本轮涉及的每个对象都已证明目标与源逐字节一致。
func (r *Report) AllVerified() bool {
	return len(r.Failures) == 0 && r.ByStatus[StatusVerified]+r.ByStatus[StatusAlreadyVerified] == r.Total
}

func (r *Report) add(o Outcome) {
	r.Outcomes = append(r.Outcomes, o)
	if o.Status != "" {
		r.ByStatus[o.Status]++
	}
	r.ByAction[o.Action]++
	if IsVerified(o.Status) {
		r.TotalBytes += o.Bytes
	}
}

func (e *Engine) logger() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

// Preflight：两个桶都必须存在。桶不存在是部署/配置故障，必须 fail closed，
// 绝不能把"桶不存在"当成"对象缺失"（HEAD 语义下二者都表现为 404）。
func (e *Engine) Preflight(ctx context.Context) error {
	if e.FromBucket == e.ToBucket {
		return fmt.Errorf("source and destination bucket must differ (both %q)", e.FromBucket)
	}
	for _, bucket := range []string{e.FromBucket, e.ToBucket} {
		exists, err := e.Store.BucketExists(ctx, bucket)
		if err != nil {
			return fmt.Errorf("verify bucket %s: %w", bucket, err)
		}
		if !exists {
			return fmt.Errorf("bucket %s does not exist (refusing to continue)", bucket)
		}
	}
	return nil
}

// Run 按 items 顺序处理；账本即 checkpoint，已 VERIFIED 的对象在重复运行时被跳过（幂等）。
func (e *Engine) Run(ctx context.Context, items []Item, opts Options) (*Report, error) {
	if err := e.Preflight(ctx); err != nil {
		return nil, err
	}
	rep := &Report{
		FromBucket: e.FromBucket, ToBucket: e.ToBucket, Execute: opts.Execute,
		ByStatus: map[string]int{}, ByAction: map[string]int{}, Outcomes: []Outcome{},
	}
	processed := 0
	for _, item := range items {
		if opts.Limit > 0 && processed >= opts.Limit {
			break
		}
		processed++
		rep.Total++
		out, err := e.process(ctx, item, opts)
		rep.add(out)
		if err != nil {
			rep.Failures = append(rep.Failures, fmt.Sprintf("%s: %v", item.ObjectRef, err))
			if opts.StopOnError {
				return rep, err
			}
		}
	}
	return rep, nil
}

func (e *Engine) process(ctx context.Context, item Item, opts Options) (Outcome, error) {
	row := Row{
		ExportID: item.ExportID, ObjectRef: item.ObjectRef,
		FromBucket: e.FromBucket, ToBucket: e.ToBucket, PGSHA256: item.PGSHA256,
		Status: StatusPending,
	}
	out := Outcome{ExportID: item.ExportID, ObjectRef: item.ObjectRef, PGSHA256: item.PGSHA256}

	// 0) 续跑：账本已 VERIFIED 且 key 未变 → 跳过（不重复复制）。
	prev, err := e.Ledger.Get(ctx, item.ExportID, e.ToBucket)
	if err != nil {
		out.Action, out.Status = "ledger_error", StatusFailed
		return out, fmt.Errorf("read ledger: %w", err)
	}
	if prev != nil && prev.ObjectRef == item.ObjectRef && IsVerified(prev.Status) {
		out.Action, out.Status = "skipped_verified", prev.Status
		out.Bytes = prev.Bytes
		out.SourceSHA256, out.DestinationSHA256 = prev.SourceSHA256, prev.DestinationSHA256
		out.Note = "ledger already verified (idempotent re-run)"
		return out, nil
	}
	if prev != nil {
		row.Attempts = prev.Attempts
	}

	// 1) 目标已存在时先做内容比对（绝不覆盖）。
	dstSize, dstExists, err := e.Store.Stat(ctx, e.ToBucket, item.ObjectRef)
	if err != nil {
		out.Action, out.Status = "stat_destination_failed", StatusFailed
		row.LastError = err.Error()
		return out, e.commit(ctx, row, &out, opts.Execute, err)
	}
	if dstExists {
		srcSHA, srcSize, err := e.Store.SHA256(ctx, e.FromBucket, item.ObjectRef)
		if err != nil {
			out.Action, out.Status = "read_source_failed", StatusFailed
			row.LastError = err.Error()
			return out, e.commit(ctx, row, &out, opts.Execute, err)
		}
		dstSHA, dstSize2, err := e.Store.SHA256(ctx, e.ToBucket, item.ObjectRef)
		if err != nil {
			out.Action, out.Status = "read_destination_failed", StatusFailed
			row.LastError = err.Error()
			return out, e.commit(ctx, row, &out, opts.Execute, err)
		}
		row.Bytes, row.SourceSHA256, row.DestinationSHA256 = srcSize, srcSHA, dstSHA
		out.Bytes, out.SourceSHA256, out.DestinationSHA256 = srcSize, srcSHA, dstSHA
		if srcSHA != dstSHA || srcSize != dstSize2 {
			row.Status = StatusConflict
			row.LastError = fmt.Sprintf("destination exists with different bytes (src=%s dst=%s, reported_size=%d)", srcSHA, dstSHA, dstSize)
			out.Action, out.Status = "conflict", StatusConflict
			return out, e.commit(ctx, row, &out, opts.Execute, fmt.Errorf("destination conflict for %s: refusing to overwrite", item.ObjectRef))
		}
		if item.PGSHA256 != "" && !strings.EqualFold(srcSHA, item.PGSHA256) {
			row.Status = StatusFailed
			row.LastError = fmt.Sprintf("source sha256 %s != exports.sha256 %s", srcSHA, item.PGSHA256)
			out.Action, out.Status = "source_pg_sha_mismatch", StatusFailed
			return out, e.commit(ctx, row, &out, opts.Execute, fmt.Errorf("%s", row.LastError))
		}
		row.Status = StatusAlreadyVerified
		out.Action, out.Status, out.Note = "already_verified", StatusAlreadyVerified, "identical bytes already present; not overwritten"
		return out, e.commit(ctx, row, &out, opts.Execute, nil)
	}

	// 2) 源对象 GET SHA（唯一字节依据；PG sha 只作为交叉校验）。
	srcSHA, srcSize, err := e.Store.SHA256(ctx, e.FromBucket, item.ObjectRef)
	if err != nil {
		out.Action, out.Status = "read_source_failed", StatusFailed
		row.LastError = err.Error()
		return out, e.commit(ctx, row, &out, opts.Execute, err)
	}
	row.Bytes, row.SourceSHA256 = srcSize, srcSHA
	out.Bytes, out.SourceSHA256 = srcSize, srcSHA
	if item.PGSHA256 != "" && !strings.EqualFold(srcSHA, item.PGSHA256) {
		row.Status = StatusFailed
		row.LastError = fmt.Sprintf("source sha256 %s != exports.sha256 %s (refusing to copy)", srcSHA, item.PGSHA256)
		out.Action, out.Status = "source_pg_sha_mismatch", StatusFailed
		return out, e.commit(ctx, row, &out, opts.Execute, fmt.Errorf("%s", row.LastError))
	}

	if !opts.Execute {
		out.Action, out.Status = "planned_copy", StatusPending
		out.Note = "dry-run: no copy performed, ledger untouched"
		return out, nil
	}

	// 3) 复制（只有显式 Execute 才执行）。
	n, err := e.Store.Copy(ctx, e.FromBucket, item.ObjectRef, e.ToBucket, item.ObjectRef, ContentTypeFor(item.ObjectRef))
	if err != nil {
		row.Status = StatusCopied
		row.LastError = err.Error()
		out.Action, out.Status = "copy_failed", StatusFailed
		return out, e.commit(ctx, row, &out, opts.Execute, err)
	}
	row.Bytes = n
	row.Status = StatusCopied
	// 复制完成即落账（COPIED）：中断后重跑会重新校验，而不是假设成功。
	if err := e.commit(ctx, row, &out, opts.Execute, nil); err != nil {
		return out, err
	}

	// 4) 目标 GET SHA 复算并逐字节比对。
	dstSHA, dstSize3, err := e.Store.SHA256(ctx, e.ToBucket, item.ObjectRef)
	if err != nil {
		row.Status = StatusFailed
		row.LastError = fmt.Sprintf("read back destination: %v", err)
		out.Action, out.Status = "read_destination_failed", StatusFailed
		return out, e.commit(ctx, row, &out, opts.Execute, err)
	}
	row.DestinationSHA256 = dstSHA
	out.DestinationSHA256 = dstSHA
	if dstSHA != srcSHA || dstSize3 != srcSize {
		row.Status = StatusFailed
		row.LastError = fmt.Sprintf("destination sha256 %s / size %d != source %s / %d", dstSHA, dstSize3, srcSHA, srcSize)
		out.Action, out.Status = "destination_mismatch", StatusFailed
		return out, e.commit(ctx, row, &out, opts.Execute, fmt.Errorf("destination mismatch for %s", item.ObjectRef))
	}
	row.Status = StatusVerified
	row.LastError = ""
	out.Action, out.Status = "copied", StatusVerified
	return out, e.commit(ctx, row, &out, opts.Execute, nil)
}

// commit 写账本。
//
// 两条硬规则：
//  1. **dry-run（execute=false）绝不写账本**——这是"默认 dry-run 零副作用"的唯一实现点。
//     本轮真实负向校准发现过反例：CONFLICT / ALREADY_VERIFIED 分支绕过了 PENDING 判断，
//     在 dry-run 下也落了账本行。dry-run 的计划只进报告文件，不进 checkpoint。
//  2. execute 模式下，带错误返回却没给出终态的路径一律落 FAILED：否则会出现
//     "报告说失败、账本却是 PENDING"的不一致，续跑时无法区分"没做过"和"失败了"。
func (e *Engine) commit(ctx context.Context, row Row, out *Outcome, execute bool, cause error) error {
	if !execute {
		return cause
	}
	if cause != nil && row.Status == StatusPending {
		row.Status = StatusFailed
	}
	if cause == nil && row.Status == StatusPending {
		return nil
	}
	if err := e.Ledger.Upsert(ctx, row); err != nil {
		return fmt.Errorf("ledger upsert %s: %w", row.ObjectRef, err)
	}
	return cause
}

// ContentTypeFor 按扩展名给出 Content-Type（与 API 写入时保持一致）。
func ContentTypeFor(key string) string {
	switch strings.ToLower(path.Ext(key)) {
	case ".csv":
		return "text/csv"
	case ".jsonl", ".json", ".ndjson":
		return "application/x-ndjson"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".parquet":
		return "application/vnd.apache.parquet"
	case ".zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}
