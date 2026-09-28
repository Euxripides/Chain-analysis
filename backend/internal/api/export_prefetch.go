package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

/*
 * 导出预生成（prefetch）：让"点击导出到可下载"接近零等待。
 *
 * 用户在导出表单上通常要花 30-60 秒，而一份 173 万行的 csv+zip 产物实测 17 秒就能
 * 生成完。因此表单草稿一变化就（防抖后）触发服务端预生成；用户真正点"导出"时，
 * 若命中同一份规格的已完成产物，直接把它提升成正式导出并返回，无需再算一遍。
 *
 * 语义边界：
 *   - 预生成产物**不出现在导出列表**里，也不写导出审计——它是投机产物，可能永远
 *     用不上（见 store/export_prefetch.go 的说明）。命中时它才被"提升"成一条真正的
 *     exports 行，此后的下载、审计、过期语义与普通导出完全一致。
 *   - 规格哈希用的是用户点导出时**同一份快照 JSON**（createExportV2 里已经在算的那份），
 *     否则字段顺序或默认值一变就永远命不中。
 *   - 命中只在同组织 + 同工作区内发生，绝不跨租户。
 *   - 预生成失败不影响任何东西：用户点导出时照常走正常路径。
 */

// prefetchTTL 是预生成产物的存活时间。超过它由维护任务连同对象一起删除。
const prefetchTTL = time.Hour

// prefetchRequest 是预生成请求体（与导出请求同形，便于前端复用同一份草稿）。
type prefetchRequest struct {
	Chain               string   `json:"chain"`
	Addresses           []string `json:"addresses"`
	Datasets            []string `json:"datasets"`
	Format              string   `json:"format"`
	ForceZip            bool     `json:"force_zip"`
	IncludeMeta         bool     `json:"include_meta"`
	IncludeDecodedInput bool     `json:"include_decoded_input"`
	JobID               string   `json:"job_id"`
}

// exportZipDecision 是"本次导出是否打包成 ZIP"的**唯一**判定点。
//
// createExportV2 与预生成必须得到同一个结论，否则规格哈希里 zip 字段不同、命中率
// 归零（而且不报错，只是每次都白算）。原先预生成只看 force_zip，漏了
// "地址数>3 或数据集数>1 也强制 ZIP" 这条——用户选两个数据集时预生成算 false、
// 真正导出算 true，永不命中。
func exportZipDecision(forceZip bool, addressCount, datasetCount int) bool {
	return forceZip || addressCount > 3 || datasetCount > 1
}

// buildExportSnapshot 产出写进 exports.query_snapshot 的规范化规格。
//
// **预生成的规格哈希与真实导出的命中判定都用它**：任何字段或顺序差异都会让
// 预生成永远命不中（且不会报错，只是白算）。因此这里是唯一构造点。
func buildExportSnapshot(chain string, addresses, datasets []string, format string,
	includeMeta, zip, includeDecodedInput bool, jobID string, partial bool, jobStatus string) []byte {
	snapshot, _ := json.Marshal(map[string]any{
		"chain": chain, "addresses": addresses, "datasets": datasets,
		"format": format, "include_meta": includeMeta, "zip": zip,
		"include_decoded_input": includeDecodedInput, "job_id": jobID,
		"partial": partial, "job_status": jobStatus,
	})
	return snapshot
}

// jobStatusOf 返回 job 的状态（job 为 nil 时为空串），与快照里的 job_status 字段一致。
func jobStatusOf(job *model.DownloadJob) string {
	if job == nil {
		return ""
	}
	return job.Status
}

// handleExportPrefetch 接收一份导出草稿并在后台开始生成产物。
//
// 返回 202 + prefetch_id（客户端不需要它，只用于观测）。幂等：同一份规格重复触发
// 只会命中已有行，不会重复生成。
func (s *Server) handleExportPrefetch(w http.ResponseWriter, r *http.Request) {
	var req prefetchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	scope, ok := s.tenantScopeOr401(w, r)
	if !ok {
		return
	}
	chain := strings.ToLower(strings.TrimSpace(req.Chain))
	if chain == "" {
		chain = "bsc"
	}
	datasets := req.Datasets
	if len(datasets) == 0 {
		datasets = []string{"transactions", "token_transfers"}
	}
	format := req.Format
	if format == "" {
		format = "csv"
	}
	// 地址归一化必须与 createExportV2 **完全一致**：规格哈希是在归一化之后算的，
	// 两边规则只要有一点不同（大小写、空白、空串处理）就会静默失配——用户点导出时
	// 永远命中不了，而且不会报错，只是每次都白算一遍。
	// 因此这里调用同一个 addressnorm.NormalizeKey，而不是自己写 trim/lower。
	addresses := make([]string, 0, len(req.Addresses))
	for _, a := range req.Addresses {
		if norm := addressnorm.NormalizeKey(chain, a); norm != "" {
			addresses = append(addresses, norm)
		}
	}
	if len(addresses) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "addresses required"})
		return
	}
	// 预生成只做"单地址 + 单数据集"这一种最常见形态：它是用户端表单的默认形状，
	// 也是唯一能用一次列式直写覆盖的形态。多地址/多数据集走 zip 组装，
	// 预生成的收益不足以支撑额外的复杂度与内存占用。
	if len(addresses) != 1 || len(datasets) != 1 {
		writeJSON(w, http.StatusOK, map[string]any{"prefetched": false, "reason": "prefetch supports a single address and dataset"})
		return
	}

	zipIt := exportZipDecision(req.ForceZip, len(addresses), len(datasets))
	snapshot := buildExportSnapshot(chain, addresses, datasets, format, req.IncludeMeta, zipIt, req.IncludeDecodedInput, req.JobID, false, "")
	specHash := store.PrefetchSpecHash(snapshot)

	id, created, err := s.deps.Store.CreatePrefetch(r.Context(), specHash, scope.OrganizationID, scope.WorkspaceID,
		nil, strings.Join(datasets, "+"), format, zipIt, prefetchTTL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if created {
		go s.runExportPrefetch(backgroundAuditCtx(r), id, specHash, chain, addresses[0], datasets[0], format, zipIt,
			req.IncludeMeta, req.IncludeDecodedInput, scope.OrganizationID, scope.WorkspaceID)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"prefetched": true, "prefetch_id": id, "started": created})
}

// runExportPrefetch 在后台生成一份预生成产物。
//
// 复用正式导出的流水线：先建一条**临时 exports 行**（putExportStream 的 intent gate
// 要求存在已提交的导出意图），跑完把对象名与行数抄进 export_prefetch，随后删掉那条
// 临时行——用户的导出列表因此看不到它。
func (s *Server) runExportPrefetch(ctx context.Context, prefetchID, specHash, chain, addr, dataset, format string,
	zipIt, includeMeta, includeDecodedInput bool, orgID, wsID string) {
	fail := func(err error) {
		_ = s.deps.Store.UpdatePrefetch(ctx, prefetchID, "FAILED", "", "", "", 0, err.Error())
		s.writeAuditCtx(ctx, "export.prefetch_failed", "export", "export_prefetch", prefetchID,
			map[string]any{"error": err.Error(), "chain": chain, "dataset": dataset})
	}

	// 与用户点导出时的快照逐字一致（无 job ⇒ partial=false、job_status=""）。
	snapshot := buildExportSnapshot(chain, []string{addr}, []string{dataset}, format, includeMeta, zipIt, includeDecodedInput, "", false, "")
	exp, err := s.deps.Store.CreateExportV2(ctx, wsID, orgID, nil, nil, dataset, format, zipIt, snapshot)
	if err != nil {
		fail(fmt.Errorf("create intent: %w", err))
		return
	}
	// 无论成功失败都要把临时意图行删掉，避免它在用户导出列表里露出来。
	defer func() {
		if _, derr := s.deps.Store.Pool.Exec(context.Background(), "DELETE FROM exports WHERE id=$1", exp.ID); derr != nil {
			s.deps.Log.Warn("prefetch intent cleanup failed", "export_id", exp.ID, "err", derr.Error())
		}
	}()

	_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "RUNNING", "", "", 0, "")
	key := fmt.Sprintf("exports/%s.%s", exp.ID, extFor(format))
	if zipIt {
		key = fmt.Sprintf("exports/%s.zip", exp.ID)
	}

	var rowsTotal int64
	var objectSHA string
	var loc exportLocation
	if zipIt {
		rowsTotal, objectSHA, loc, err = s.writeZipExport(ctx, exp, nil, key, chain, wsID,
			[]string{addr}, []string{dataset}, format, includeMeta, includeDecodedInput)
	} else {
		rowsTotal, objectSHA, loc, err = s.writeSingleExport(ctx, exp.ID, nil, key, chain, wsID, addr, dataset,
			format, includeMeta, includeDecodedInput)
	}
	if err != nil {
		_ = s.deps.Store.UpdateExportV2(ctx, exp.ID, "FAILED", "", "", 0, err.Error())
		fail(err)
		return
	}
	if err := s.deps.Store.UpdatePrefetch(ctx, prefetchID, "COMPLETED", key, loc.Name, objectSHA, rowsTotal, ""); err != nil {
		fail(fmt.Errorf("record prefetch result: %w", err))
		return
	}
	s.writeAuditCtx(ctx, "export.prefetched", "export", "export_prefetch", prefetchID,
		map[string]any{"rows": rowsTotal, "bytes_sha256": objectSHA, "object_bucket": loc.Name, "object_ref": key,
			"chain": chain, "dataset": dataset, "format": format, "zip": zipIt})
}

// tryPromotePrefetch 尝试把一份已完成的预生成产物提升成正式导出。
//
// 命中条件（缺一不可）：同组织 + 同工作区 + 同规格快照 + 状态 COMPLETED + 对象名非空。
// 返回 (export, true) 表示已提升，调用方应直接返回它、不再跑导出流水线。
func (s *Server) tryPromotePrefetch(ctx context.Context, specHash, orgID, wsID, createdBy string,
	dataset, format string, zipIt bool, snapshot []byte) (*model.Export, bool) {
	if s.deps.Store == nil {
		return nil, false
	}
	pre, err := s.deps.Store.FindCompletedPrefetch(ctx, specHash, orgID, wsID)
	if err != nil || pre == nil {
		return nil, false
	}
	var createdByPtr *string
	if createdBy != "" {
		createdByPtr = &createdBy
	}
	exp, err := s.deps.Store.CreateExportV2(ctx, wsID, orgID, createdByPtr, nil, dataset, format, zipIt, snapshot)
	if err != nil {
		return nil, false
	}
	// 直接写终态：对象已经在 MinIO 里，无需再跑流水线。
	if err := s.deps.Store.UpdateExportV2(ctx, exp.ID, "COMPLETED", pre.ObjectRef, pre.SHA256, pre.Rows, ""); err != nil {
		return nil, false
	}
	// 提升过的预生成行不再需要：删掉它，避免同一份草稿被反复提升出多条导出。
	// 对象**不能删**——它现在归这条正式导出所有。
	if _, err := s.deps.Store.Pool.Exec(ctx, "DELETE FROM export_prefetch WHERE id=$1", pre.ID); err != nil {
		s.deps.Log.Warn("prefetch row cleanup failed", "prefetch_id", pre.ID, "err", err.Error())
	}
	s.writeAuditCtx(ctx, "export.prefetch_promoted", "export", "export", exp.ID,
		map[string]any{"prefetch_id": pre.ID, "rows": pre.Rows, "object_ref": pre.ObjectRef,
			"object_bucket": pre.ObjectBucket, "spec_hash": specHash})
	exp.Status = "COMPLETED"
	exp.ObjectRef = pre.ObjectRef
	exp.SHA256 = pre.SHA256
	exp.Rows = pre.Rows
	return exp, true
}

// handleExportPrefetchStatus 供前端轮询（可选）：预生成是否已就绪。
//
// 前端不需要等它就绪才允许点导出——点导出时服务端自己会命中；这个接口只用于
// 在界面上显示"已预生成，点击即下载"这类提示。
func (s *Server) handleExportPrefetchStatus(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.tenantScopeOr401(w, r)
	if !ok {
		return
	}
	specHash := strings.TrimSpace(r.URL.Query().Get("spec_hash"))
	if specHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "spec_hash required"})
		return
	}
	pre, err := s.deps.Store.FindCompletedPrefetch(r.Context(), specHash, scope.OrganizationID, scope.WorkspaceID)
	if err != nil || pre == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ready": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": true, "rows": pre.Rows, "format": pre.Format, "zip": pre.Zip})
}

// sha256Hex 供审计字段使用。
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var _ = errors.Is
