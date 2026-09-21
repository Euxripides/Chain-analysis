package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// ---- U-DL-01 / S5-BE-02（§18.10 / §21.5）----

type downloadV2Subject struct {
	Type  string `json:"type"`
	Chain string `json:"chain"`
	Value string `json:"value"`
}

type downloadV2Request struct {
	CaseID          string              `json:"case_id"`
	Chain           string              `json:"chain"`
	Addresses       []string            `json:"addresses"`
	Subjects        []downloadV2Subject `json:"subjects"`
	Datasets        []string            `json:"datasets"`
	Range           dataset.RangeSpec   `json:"range"`
	ReusePolicy     string              `json:"reuse_policy"`
	PerformanceMode string              `json:"performance_mode"`
	Priority        int                 `json:"priority"`

	// §18.10 / §22/§23：导入文件的案件/标签语义。客户端只能提交名称，
	// 租户作用域一律由服务端从会话主体注入（§6.4）。
	CaseName          string                           `json:"case_name"`
	Tags              []string                         `json:"tags"`
	CreateMissingMeta bool                             `json:"create_missing_meta"`
	SubjectMeta       map[string]downloadV2SubjectMeta `json:"subject_meta"`
}

type downloadV2Plan struct {
	Request      map[string]any
	EnsureReq    dataset.EnsureRequest
	Preview      *dataset.CoveragePreviewResponse
	Candidate    *model.DownloadJob
	CoverageHit  bool
	Refresh      bool
	MissingBlock int64
	Estimate     map[string]any
	Risks        []string
}

func (s *Server) parseDownloadV2(w http.ResponseWriter, r *http.Request) (downloadV2Request, bool) {
	var req downloadV2Request
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return req, false
	}
	req.Chain = strings.ToLower(strings.TrimSpace(req.Chain))
	req.ReusePolicy = strings.ToLower(strings.TrimSpace(req.ReusePolicy))
	if req.ReusePolicy == "" {
		req.ReusePolicy = "prefer_local"
	}
	switch req.ReusePolicy {
	case "prefer_local", "refresh":
	default:
		validationError(w, r, "reuse_policy 只接受 prefer_local 或 refresh", []fieldError{{Field: "reuse_policy", Code: "invalid", Message: req.ReusePolicy}})
		return req, false
	}
	if req.Chain == "" {
		validationError(w, r, "chain 必填", []fieldError{{Field: "chain", Code: "required", Message: "请选择链"}})
		return req, false
	}
	for _, sub := range req.Subjects {
		if !strings.EqualFold(strings.TrimSpace(sub.Type), "ADDRESS") {
			validationError(w, r, "v2 下载目前只接受 ADDRESS 类型对象", nil)
			return req, false
		}
		if c := strings.ToLower(strings.TrimSpace(sub.Chain)); c != "" && c != req.Chain {
			validationError(w, r, "一个下载请求只能包含同一链的对象", nil)
			return req, false
		}
		req.Addresses = append(req.Addresses, sub.Value)
	}
	if len(req.Addresses) == 0 {
		validationError(w, r, "至少需要一个地址", []fieldError{{Field: "addresses", Code: "required", Message: "请粘贴地址"}})
		return req, false
	}
	if len(req.Addresses) > 100 {
		validationError(w, r, "单次最多 100 个地址", nil)
		return req, false
	}
	if req.Priority <= 0 {
		req.Priority = 100
	}
	req.CaseName = strings.TrimSpace(req.CaseName)
	// 标签超额一律显式拒绝，绝不静默截断后照常创建。
	req.Tags = trimDedupeStrings(req.Tags)
	if len(req.Tags) > downloadImportMaxTags {
		validationError(w, r, "tags 数量超出上限", []fieldError{{Field: "tags", Code: "too_many", Message: "单次最多 20 个标签"}})
		return req, false
	}
	if len(req.SubjectMeta) > downloadImportMaxAddresses {
		validationError(w, r, "subject_meta 条目超过单次地址上限", []fieldError{{Field: "subject_meta", Code: "too_many", Message: "单次最多 100 个地址"}})
		return req, false
	}
	normalizedMeta := make(map[string]downloadV2SubjectMeta, len(req.SubjectMeta))
	for key, meta := range req.SubjectMeta {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			validationError(w, r, "subject_meta 含空地址键", []fieldError{{Field: "subject_meta", Code: "invalid", Message: "地址键不能为空"}})
			return req, false
		}
		meta.CaseName = strings.TrimSpace(meta.CaseName)
		meta.Tags = trimDedupeStrings(meta.Tags)
		if len(meta.Tags) > downloadImportMaxTags {
			validationError(w, r, "subject_meta tags 数量超出上限", []fieldError{{Field: "subject_meta", Code: "too_many", Message: trimmedKey}})
			return req, false
		}
		normalizedMeta[trimmedKey] = meta
	}
	req.SubjectMeta = normalizedMeta
	// §21.2 最小权限：只有 download:create 的调用方不得借 create_missing_meta 造案件/标签。
	if req.CreateMissingMeta {
		caseRequested := req.CaseName != ""
		tagRequested := len(req.Tags) > 0
		for _, meta := range req.SubjectMeta {
			if meta.CaseName != "" {
				caseRequested = true
			}
			if len(meta.Tags) > 0 {
				tagRequested = true
			}
		}
		if !s.authorizeMetaCreate(w, r, caseRequested, tagRequested) {
			return req, false
		}
	}
	return req, true
}

// prepareDownloadPlan 只读本地索引：验证作用域、计算 Coverage、复用候选与估算。
// 该函数不创建任务、不调用 Provider。
func (s *Server) prepareDownloadPlan(w http.ResponseWriter, r *http.Request, req downloadV2Request) (*downloadV2Plan, bool) {
	p := PrincipalFrom(r.Context())
	if s.deps.Store == nil || s.svc == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "下载服务未就绪", nil)
		return nil, false
	}
	addresses, err := dataset.NormalizeAndDedupeAddressesForChain(req.Chain, req.Addresses)
	if err != nil {
		validationError(w, r, "地址不合法或去重后为空", []fieldError{{Field: "addresses", Code: "invalid", Message: err.Error()}})
		return nil, false
	}
	datasets, err := normalizeDatasetsForDownload(req.Datasets)
	if err != nil {
		validationError(w, r, "Dataset 不合法", []fieldError{{Field: "datasets", Code: "invalid", Message: err.Error()}})
		return nil, false
	}
	// Solana 的 balances 是"取数时刻的快照"（ADR-024 D2）：区间对它没有意义，
	// 而 full 会去解析 chain head（Solana 没有可用的 head 读路径，solanaRangeGuard 会拒绝）。
	// 这里把它规范化成显式的 [0,0] block 区间让 guard 放行，Provider 的快照语义不受影响。
	if req.Chain == "solana" && isBalancesOnly(datasets) && (req.Range.Type == "" || req.Range.Type == "full") {
		zero := int64(0)
		req.Range = dataset.RangeSpec{Type: "block", FromBlock: &zero, ToBlock: &zero}
	}
	if req.CaseID != "" {
		if _, err := uuid.Parse(req.CaseID); err != nil {
			validationError(w, r, "case_id 必须是 UUID", nil)
			return nil, false
		}
		var exists bool
		if err := s.deps.Store.Pool.QueryRow(r.Context(), `
			SELECT EXISTS(SELECT 1 FROM cases WHERE id=$1 AND organization_id=$2 AND workspace_id=$3)`,
			req.CaseID, p.OrganizationID, p.WorkspaceID).Scan(&exists); err != nil || !exists {
			respondError(w, r, http.StatusNotFound, "RESOURCE_NOT_FOUND", "案件不存在或无权访问", nil)
			return nil, false
		}
	}
	// §18.10：前端只用服务端 accepted 的 normalized 地址；subject_meta 的键必须
	// 能在本次请求的地址集合中归一化命中，否则拒绝（不允许跨请求/跨工作区注入元数据）。
	subjectMeta := map[string]dataset.SubjectMeta{}
	if len(req.SubjectMeta) > 0 {
		allowed := make(map[string]bool, len(addresses))
		for _, a := range addresses {
			allowed[a] = true
		}
		origin := make(map[string]string, len(req.SubjectMeta))
		for key, meta := range req.SubjectMeta {
			// 不能在这里统一 ToLower：EVM 由 NormalizeAndDedupeAddressesForChain 折叠，
			// 而 Solana 的 base58 地址大小写敏感，先折叠会得到另一个无效地址。
			list, err := dataset.NormalizeAndDedupeAddressesForChain(req.Chain, []string{strings.TrimSpace(key)})
			if err != nil || len(list) == 0 || !allowed[list[0]] {
				validationError(w, r, "subject_meta 含未通过服务端校验的地址", []fieldError{{Field: "subject_meta", Code: "invalid", Message: key}})
				return nil, false
			}
			normalizedKey := list[0]
			// 大小写/链归一化后命中同一地址的两个不同原始键 → 语义不确定，拒绝而不是后者覆盖前者。
			if prev, ok := origin[normalizedKey]; ok && prev != key {
				validationError(w, r, "subject_meta 含归一化冲突：多个键映射到同一规范化地址",
					[]fieldError{{Field: "subject_meta", Code: "conflict", Message: prev + " / " + key}})
				return nil, false
			}
			origin[normalizedKey] = key
			subjectMeta[normalizedKey] = dataset.SubjectMeta{CaseName: meta.CaseName, Tags: meta.Tags}
		}
	}
	ensureReq := dataset.EnsureRequest{
		OrganizationID: p.OrganizationID, WorkspaceID: p.WorkspaceID, CreatedByIdentity: p.IdentityID,
		Source: "user_download", Priority: req.Priority,
		PerformanceMode: req.PerformanceMode, Subjects: addresses, Chain: req.Chain,
		Datasets: datasets, Range: req.Range, ReusePolicy: req.ReusePolicy,
		CaseName: req.CaseName, Tags: req.Tags,
		CreateMissingMeta: req.CreateMissingMeta, SubjectMeta: subjectMeta,
	}
	preview, err := s.svc.PreviewLocal(r.Context(), ensureReq)
	if err != nil {
		validationError(w, r, "预览失败："+err.Error(), nil)
		return nil, false
	}
	candidate, err := s.svc.ReuseCandidateLocal(r.Context(), ensureReq)
	if err != nil {
		validationError(w, r, "复用检查失败："+err.Error(), nil)
		return nil, false
	}
	refresh := req.ReusePolicy == "refresh"
	if refresh {
		candidate = nil // 显式 refresh 绝不复用，即使存在同指纹任务。
	}
	coverageHit := preview.OverallRatio >= 1 && !refresh
	missingBlocks := missingBlocksFromPreview(preview)
	estimate := s.downloadEstimate(r.Context(), req.Chain, addresses, datasets, missingBlocks)
	risks := []string{}
	if refresh {
		risks = append(risks, "显式 refresh 会新增 Provider 取数，不复用本地已完成数据。")
	}
	if candidate != nil {
		risks = append(risks, fmt.Sprintf("存在可复用任务 %s（状态 %s），默认不会新增 Provider 工作。", candidate.JobNo, candidate.Status))
	}
	if preview.OverallRatio > 0 && preview.OverallRatio < 1 {
		risks = append(risks, fmt.Sprintf("本地 Coverage %.1f%%，任务只会补齐缺口。", preview.OverallRatio*100))
	}
	return &downloadV2Plan{
		Request: map[string]any{
			"case_id": req.CaseID, "chain": req.Chain, "addresses": addresses,
			"datasets": datasets, "range": req.Range, "reuse_policy": req.ReusePolicy,
			"performance_mode": ensureReq.PerformanceMode,
		},
		EnsureReq: ensureReq, Preview: preview, Candidate: candidate, CoverageHit: coverageHit,
		Refresh: refresh, MissingBlock: missingBlocks, Estimate: estimate, Risks: risks,
	}, true
}

// isBalancesOnly 判断本次请求是否只是余额快照（快照不需要区间语义）。
func isBalancesOnly(datasets []string) bool {
	return len(datasets) == 1 && datasets[0] == "balances"
}

func normalizeDatasetsForDownload(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{"transactions", "token_transfers"}, nil
	}
	if len(in) > 6 {
		return nil, errors.New("单次最多选择 6 个 Dataset")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		d := strings.ToLower(strings.TrimSpace(raw))
		if !model.ValidDataset(d) {
			return nil, fmt.Errorf("未知 Dataset %q", d)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out, nil
}

func missingBlocksFromPreview(p *dataset.CoveragePreviewResponse) int64 {
	var total int64
	for _, item := range p.Items {
		for _, gap := range item.Gaps {
			if gap.ToBlock >= gap.FromBlock {
				total += gap.ToBlock - gap.FromBlock + 1
			}
		}
	}
	return total
}

// downloadEstimate 基于本地密度与历史 Provider 单位成本估算，不探测 Provider。
func (s *Server) downloadEstimate(ctx context.Context, chainSlug string, addresses, datasets []string, missingBlocks int64) map[string]any {
	out := map[string]any{
		"estimated_missing_blocks": missingBlocks,
		"estimated_rows":           nil,
		"estimated_cost_usd":       nil,
		"estimated_time_seconds":   nil,
		"basis":                    "local_index_density + historical_provider_usage",
	}
	if s.deps.CH == nil || missingBlocks <= 0 {
		out["basis"] = "no_missing_blocks"
		return out
	}
	// P4-CH-01: the ClickHouse FINAL scan below is external work that a
	// PostgreSQL rollback cannot undo. Revalidate the caller scope first, so a
	// request the commit gate is about to refuse does not charge a 20+ second
	// scan to a scope the platform has already invalidated. The gate's release
	// re-read stays the only enforcement point; this only decides whether the
	// external call is worth starting.
	if recheckErr := s.scopeCommitGateRecheck(ctx, PrincipalFrom(ctx)); recheckErr != nil {
		out["basis"] = "scope_not_revalidated"
		out["scope_recheck"] = recheckErr.Error()
		return out
	}
	var localRows int64
	if len(datasets) == 1 && datasets[0] == "token_transfers" {
		_ = s.deps.CH.DB().QueryRow(ctx, `SELECT count() FROM blockchain.token_transfers FINAL WHERE chain=? AND (from_address IN (?) OR to_address IN (?))`, chainSlug, addresses, addresses).Scan(&localRows)
	} else {
		_ = s.deps.CH.DB().QueryRow(ctx, `SELECT count() FROM blockchain.transactions FINAL WHERE chain=? AND (from_address IN (?) OR to_address IN (?))`, chainSlug, addresses, addresses).Scan(&localRows)
	}
	if localRows <= 0 {
		return out
	}
	coveredBlocks := int64(1)
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT GREATEST(COALESCE(SUM(to_block-from_block+1),0),1) FROM dataset_coverage
		WHERE chain=$1 AND subject = ANY($2) AND dataset = ANY($3)`,
		chainSlug, addresses, datasets).Scan(&coveredBlocks)
	density := float64(localRows) / float64(coveredBlocks)
	estRows := int64(density * float64(missingBlocks))
	if estRows < 0 {
		estRows = 0
	}
	out["estimated_rows"] = estRows
	var totalCost, totalRows float64
	_ = s.deps.Store.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(estimated_cost_usd),0), COALESCE(SUM(rows),0)
		FROM provider_usage WHERE dataset = ANY($1) AND rows > 0`, datasets).Scan(&totalCost, &totalRows)
	if totalRows > 0 {
		out["estimated_cost_usd"] = fmt.Sprintf("%.4f", float64(estRows)*(totalCost/totalRows))
	}
	return out
}

func (s *Server) handleDownloadPreview(w http.ResponseWriter, r *http.Request) {
	req, ok := s.parseDownloadV2(w, r)
	if !ok {
		return
	}
	plan, ok := s.prepareDownloadPlan(w, r, req)
	if !ok {
		return
	}
	providerWork := !plan.CoverageHit && plan.Candidate == nil
	reuseInfo := map[string]any{
		"policy":       plan.EnsureReq.ReusePolicy,
		"will_reuse":   plan.Candidate != nil,
		"candidate":    nil,
		"coverage_hit": plan.CoverageHit,
		"refresh":      plan.Refresh,
	}
	if plan.Candidate != nil {
		reuseInfo["candidate"] = map[string]any{
			"job_id": plan.Candidate.ID, "job_no": plan.Candidate.JobNo,
			"status": plan.Candidate.Status, "coverage": plan.Preview.OverallRatio,
			"updated_at": plan.Candidate.UpdatedAt,
		}
	}
	providerPlan, planCU, planCost := providerPlanForDataset(plan.EnsureReq.Chain, plan.EnsureReq.Datasets, plan.Estimate["estimated_rows"])
	estimate := map[string]any{}
	for k, v := range plan.Estimate {
		estimate[k] = v
	}
	// §15.2：估算值只从本地 Coverage 与历史用量推导，推不出来就留 null，
	// 绝不为了"看起来精确"编一个数。
	estimate["estimated_cu"] = planCU
	estimate["estimated_cost_usd"] = planCost
	respondOK(w, r, map[string]any{
		"request":                plan.Request,
		"coverage":               plan.Preview,
		"missing_blocks":         plan.MissingBlock,
		"provider_work_required": providerWork,
		"reuse":                  reuseInfo,
		"estimate":               estimate,
		"provider_plan":          providerPlan,
		"risks":                  plan.Risks,
		"source": map[string]any{
			"coverage": "postgres:dataset_coverage", "provider": "local_index",
			"as_of": time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// drpcDataAPICUSPerRequest / drpcRPCCUPerCall 与开发文档 §15.1 的官方 CU 表一致：
// 交易历史每次调用 1837 CU，Solana RPC 方法通常 20 CU。价格 1,000,000 CU = 0.30 USD。
const (
	drpcDataAPICUSPerRequest = 1837.0
	drpcRPCCUPerCall         = 20.0
	drpcUSDPerCU             = 0.30 / 1_000_000
	drpcHistoryPageSize      = 100
)

// providerPlanForDataset 按链能力把 Dataset 归并成 Provider/通道计划（开发文档 §15.2）。
//
// 无法从本地密度估算行数时（estimatedRows 非数字或 <= 0）只返回通道与单价口径，
// 页数/CU/成本留 null —— 前端据此显示"范围"而不是一个假精确值。
func providerPlanForDataset(chain string, datasets []string, estimatedRows any) ([]map[string]any, any, any) {
	wanted := map[string]bool{}
	for _, ds := range datasets {
		wanted[ds] = true
	}
	type group struct {
		provider, channel string
		datasets          []string
		dataAPI           bool
	}
	order := []string{}
	groups := map[string]*group{}
	for _, cap := range model.ChainDatasetCapabilities(chain) {
		if cap.Status != model.DatasetAvailable || !wanted[cap.ID] {
			continue
		}
		key := cap.Provider + "|" + cap.APIChannel
		g := groups[key]
		if g == nil {
			g = &group{provider: cap.Provider, channel: cap.APIChannel, dataAPI: cap.APIChannel == "data_api"}
			groups[key] = g
			order = append(order, key)
		}
		g.datasets = append(g.datasets, cap.ID)
	}
	rows, rowsKnown := 0.0, false
	switch v := estimatedRows.(type) {
	case int64:
		rows, rowsKnown = float64(v), v > 0
	case int:
		rows, rowsKnown = float64(v), v > 0
	case float64:
		rows, rowsKnown = v, v > 0
	}
	items := make([]map[string]any, 0, len(order))
	var totalCU float64
	cuKnown := true
	for _, key := range order {
		g := groups[key]
		item := map[string]any{
			"provider": g.provider, "api_channel": g.channel, "datasets": g.datasets,
			"estimated_pages": nil, "estimated_rpc_calls": nil,
			"estimated_cu": nil, "estimated_cost_usd": nil,
		}
		if rowsKnown {
			if g.dataAPI {
				pages := math.Ceil(rows / drpcHistoryPageSize)
				item["estimated_pages"] = int64(pages)
				cu := pages * drpcDataAPICUSPerRequest
				item["estimated_cu"] = int64(cu)
				item["estimated_cost_usd"] = strconv.FormatFloat(cu*drpcUSDPerCU, 'f', 18, 64)
				totalCU += cu
			} else {
				// RPC 通道按"每笔交易一次调用"的量级估算（详情/日志/余额快照），
				// 明确标注它是量级而不是精确值。
				calls := math.Ceil(rows)
				item["estimated_rpc_calls"] = int64(calls)
				cu := calls * drpcRPCCUPerCall
				item["estimated_cu"] = int64(cu)
				item["estimated_cost_usd"] = strconv.FormatFloat(cu*drpcUSDPerCU, 'f', 18, 64)
				totalCU += cu
			}
			item["basis"] = "local_density_estimate"
		} else {
			cuKnown = false
			item["basis"] = "range_unknown"
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return items, nil, nil
	}
	if !cuKnown {
		return items, nil, nil
	}
	return items, int64(totalCU), strconv.FormatFloat(totalCU*drpcUSDPerCU, 'f', 18, 64)
}

func (s *Server) handleDownloadCreate(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		validationError(w, r, "创建下载必须携带 Idempotency-Key", []fieldError{{Field: "Idempotency-Key", Code: "required", Message: "缺少幂等键"}})
		return
	}
	req, ok := s.parseDownloadV2(w, r)
	if !ok {
		return
	}
	p := PrincipalFrom(r.Context())
	if req.ReusePolicy == "refresh" && !p.Has("download:refresh") {
		respondError(w, r, http.StatusForbidden, "AUTHZ_FORBIDDEN", "显式 refresh 需要 download:refresh 权限", map[string]any{"permission": "download:refresh"})
		return
	}
	plan, ok := s.prepareDownloadPlan(w, r, req)
	if !ok {
		return
	}
	if plan.CoverageHit {
		s.writeAuditNow(r, "download.create.coverage_hit", "download", "download_request", req.CaseID,
			map[string]any{"chain": req.Chain, "datasets": plan.EnsureReq.Datasets, "coverage": plan.Preview.OverallRatio})
		respondOK(w, r, map[string]any{
			"status": "ready", "created": false, "reused": false,
			"provider_work_required": false, "coverage": plan.Preview.OverallRatio,
			"job_id": nil, "message": "本地索引已完整覆盖请求范围，未创建任务、未调用 Provider。",
		})
		return
	}
	if plan.Candidate != nil {
		s.writeAuditNow(r, "download.reuse", "download", "download_job", plan.Candidate.ID,
			map[string]any{"chain": req.Chain, "datasets": plan.EnsureReq.Datasets, "policy": req.ReusePolicy})
		resp, err := s.svc.Ensure(r.Context(), plan.EnsureReq)
		if err != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_REUSE_RACE", "复用任务状态已变化，请重新预览", nil)
			return
		}
		jobID := ""
		if resp.JobID != nil {
			jobID = *resp.JobID
		}
		respondOK(w, r, map[string]any{
			"status": "reused", "created": false, "reused": true,
			"provider_work_required": false, "coverage": resp.Coverage,
			"job_id": jobID, "message": "已复用现有任务，未新增 Provider 工作。",
		})
		return
	}
	resp, err := s.svc.Ensure(r.Context(), plan.EnsureReq)
	if err != nil {
		respondError(w, r, http.StatusConflict, "CONFLICT_DOWNLOAD", "创建下载失败："+err.Error(), nil)
		return
	}
	if resp.JobID != nil {
		action := "download.create"
		if plan.Refresh {
			action = "download.refresh"
		}
		s.writeAuditNow(r, action, "download", "download_job", *resp.JobID,
			map[string]any{"chain": req.Chain, "datasets": plan.EnsureReq.Datasets,
				"coverage": resp.Coverage, "refresh": plan.Refresh})
	}
	status := http.StatusOK
	if resp.Created {
		status = http.StatusCreated
	}
	body := map[string]any{
		"status": resp.Status, "created": resp.Created, "reused": resp.Reused,
		"provider_work_required": resp.ProviderWorkRequired, "coverage": resp.Coverage,
		"job_id": resp.JobID, "dataset_ref": resp.DatasetRef,
	}
	if resp.JobID != nil {
		if job, jerr := s.deps.Store.GetJob(r.Context(), *resp.JobID); jerr == nil && job != nil {
			body["job_no"] = job.JobNo
		}
	}
	writeEnvelope(w, status, envelope{Success: true, Data: body, Meta: metaFor(r.Context())})
}
