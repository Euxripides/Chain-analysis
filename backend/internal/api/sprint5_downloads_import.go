package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
)

// ---- U-DL-01 地址导入：服务端再次验证（docs/dual-system-development-plan.md 1042-1043）----
//
// 契约原文：
//   - 地址支持粘贴和 CSV/XLSX 导入；导入先在浏览器显示解析摘要，服务端再次验证。
//   - 每个地址可单独选择链；重复判断键为 chain + normalized address。
//
// 本端点是**只读校验**：不写业务数据、不创建下载任务、不调用 Provider。
// 租户作用域（organization_id / workspace_id）一律取自已认证会话主体（§6.4），
// 绝不接受客户端提交的租户 ID；案件/标签解析严格限制在调用方工作区内（§1771）。
//
// 失败语义（fail-closed）：
//   - 服务未就绪 / 存储层查询失败 → 503，绝不降级成"不存在"；
//   - 不存在且调用方没有创建权限 → denied，绝不写成 will_create。
const (
	downloadImportMaxAddresses = 100
	downloadImportMaxTags      = 20
	downloadImportMaxItems     = 5000
)

type downloadImportItem struct {
	Row     int      `json:"row"`
	Address string   `json:"address"`
	Chain   string   `json:"chain"`
	Case    string   `json:"case"`
	Tags    []string `json:"tags"`
}

type downloadValidateRequest struct {
	Chain             string               `json:"chain"`
	Items             []downloadImportItem `json:"items"`
	CaseName          string               `json:"case_name"`
	Tags              []string             `json:"tags"`
	CreateMissingMeta bool                 `json:"create_missing_meta"`
}

// downloadV2SubjectMeta 是 §23 的 per-address override（案件/标签按地址覆盖）。
type downloadV2SubjectMeta struct {
	CaseName string   `json:"case_name"`
	Tags     []string `json:"tags"`
}

// metaWriteGrants 描述"按名创建案件/标签"所需的现存权限键。
//
// 依据现有权限目录（backend/internal/policy/catalog.go）与既有写入口：
//   - 案件创建：POST /api/v1/cases        → case:create
//   - 标签创建：POST /api/v1/tags         → case:update
//
// 下载侧只有 download:create 时**不得**越权创建元数据。
type metaWriteGrants struct {
	CanCreateCase bool
	CanUpdateCase bool
	WantCreate    bool
}

func (g metaWriteGrants) canCreateCase() bool { return g.WantCreate && g.CanCreateCase }
func (g metaWriteGrants) canCreateTag() bool  { return g.WantCreate && g.CanUpdateCase }

// trimDedupeStrings 只做去空白与去重，**不做静默截断**。
// 超出 max 时由调用方显式拒绝并记录，避免"摘要看起来正常但实际被截断"。
func trimDedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// chainKnownEnabled 查询链注册表（与 /api/v2/meta/chains 同源），
// 保证"服务端再次验证"用的是服务端能力清单而不是前端写死的正则。
func (s *Server) chainKnownEnabled(slug string) (known bool, enabled bool) {
	if s.deps.ChainRead == nil || s.deps.ChainRead.Chains() == nil {
		return false, false
	}
	for _, cfg := range s.deps.ChainRead.Chains().List() {
		if strings.EqualFold(cfg.Slug, slug) {
			return true, cfg.Enabled
		}
	}
	return false, false
}

// resolveMetaName 解析单个案件/标签名在调用方工作区内是否存在。
//
// 返回 status ∈ {resolved, missing, will_create, denied}。
// 存储层错误原样返回，调用方必须转 503 —— 数据库故障不得被包装成"不存在"。
func (s *Server) resolveMetaName(ctx context.Context, wsID, name, kind string, grants metaWriteGrants) (map[string]any, error) {
	entry := map[string]any{"name": name, "kind": kind, "status": "missing", kind + "_id": nil}
	switch kind {
	case "case":
		if grants.canCreateCase() {
			entry["status"] = "will_create"
		} else if grants.WantCreate {
			entry["status"] = "denied"
			entry["required_permission"] = "case:create"
		}
		c, err := s.deps.Store.GetCaseByName(ctx, wsID, name)
		if err != nil {
			return nil, err
		}
		if c != nil {
			entry["status"] = "resolved"
			entry["case_id"] = c.ID
		}
	case "tag":
		if grants.canCreateTag() {
			entry["status"] = "will_create"
		} else if grants.WantCreate {
			entry["status"] = "denied"
			entry["required_permission"] = "case:update"
		}
		t, err := s.deps.Store.GetTagByName(ctx, wsID, name)
		if err != nil {
			return nil, err
		}
		if t != nil {
			entry["status"] = "resolved"
			entry["tag_id"] = t.ID
		}
	default:
		return nil, context.Canceled
	}
	return entry, nil
}

// authorizeMetaCreate 是"按名创建案件/标签"的唯一授权闸门（§21.2 最小权限）。
//
// 下载侧权限 download:create **不**蕴含元数据写权限：既有写入口是
// POST /api/v1/cases → case:create，POST /api/v1/tags → case:update。
// 任一不满足时直接写 403 并返回 false，调用方必须立即返回。
func (s *Server) authorizeMetaCreate(w http.ResponseWriter, r *http.Request, caseRequested, tagRequested bool) bool {
	if !caseRequested && !tagRequested {
		return true
	}
	p := PrincipalFrom(r.Context())
	if caseRequested && !p.Has("case:create") {
		respondError(w, r, http.StatusForbidden, "AUTHZ_FORBIDDEN",
			"create_missing_meta 需要 case:create 权限（按名创建案件）",
			map[string]any{"permission": "case:create"})
		return false
	}
	if tagRequested && !p.Has("case:update") {
		respondError(w, r, http.StatusForbidden, "AUTHZ_FORBIDDEN",
			"create_missing_meta 需要 case:update 权限（按名创建标签）",
			map[string]any{"permission": "case:update"})
		return false
	}
	// 真实创建会在 dataset.Ensure → addressmeta.EnsureLinked 内发生，这里留下授权审计。
	s.writeAuditNow(r, "download.meta.create_authorized", "download", "download_request", "", map[string]any{
		"case_create_authorized": caseRequested, "tag_create_authorized": tagRequested,
		"grant_case_create": p.Has("case:create"), "grant_case_update": p.Has("case:update"),
		"organization_id": p.OrganizationID, "workspace_id": p.WorkspaceID,
	})
	return true
}

// handleDownloadValidateAddresses 是导入流程的服务端复核入口。
//
// 返回逐条 verdict（accepted / rejected + code）、逐字段拒绝（field_rejections）、
// 以及案件/标签在本工作区内的解析结果。前端只用 accepted 的 normalized 地址发起
// preview/create；被拒条目必须在摘要 Dialog 中原样呈现，不允许静默丢弃。
func (s *Server) handleDownloadValidateAddresses(w http.ResponseWriter, r *http.Request) {
	var req downloadValidateRequest
	if err := decodeJSON(r, &req); err != nil {
		validationError(w, r, "请求体不是合法 JSON", nil)
		return
	}
	p := PrincipalFrom(r.Context())
	if strings.TrimSpace(p.OrganizationID) == "" || strings.TrimSpace(p.WorkspaceID) == "" {
		respondError(w, r, http.StatusForbidden, "AUTHZ_SCOPE_REQUIRED",
			"当前会话没有绑定组织/工作区，无法校验导入地址", nil)
		return
	}
	if s.deps.Store == nil || s.deps.Store.Pool == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY",
			"元数据存储未就绪，无法完成服务端复核", nil)
		return
	}
	if len(req.Items) == 0 {
		validationError(w, r, "没有需要校验的条目", nil)
		return
	}
	if len(req.Items) > downloadImportMaxItems {
		validationError(w, r, "单次校验条目过多", nil)
		return
	}
	grants := metaWriteGrants{
		CanCreateCase: p.Has("case:create"),
		CanUpdateCase: p.Has("case:update"),
		WantCreate:    req.CreateMissingMeta,
	}
	defaultChain := strings.ToLower(strings.TrimSpace(req.Chain))

	accepted := make([]map[string]any, 0, len(req.Items))
	rejected := make([]map[string]any, 0)
	fieldRejections := make([]map[string]any, 0)
	seen := map[string]bool{}
	chainCounts := map[string]int{}
	caseNames := []string{}
	tagNames := []string{}
	var blank, invalid, duplicate, capRejected, tagsOverLimit int

	for index, item := range req.Items {
		row := item.Row
		if row <= 0 {
			row = index + 1
		}
		rawInput := strings.TrimSpace(item.Address)
		itemChain := strings.ToLower(strings.TrimSpace(item.Chain))
		itemCase := strings.TrimSpace(item.Case)
		rawTags := trimDedupeStrings(item.Tags)
		if itemCase == "" {
			itemCase = strings.TrimSpace(req.CaseName)
		}
		if len(rawTags) == 0 {
			rawTags = trimDedupeStrings(req.Tags)
		}
		// 超额标签**不静默截断**：整条 tags 字段被拒绝并单独记录，地址本身仍可接受。
		appliedTags := rawTags
		if len(rawTags) > downloadImportMaxTags {
			tagsOverLimit++
			fieldRejections = append(fieldRejections, map[string]any{
				"row": row, "index": index, "field": "tags", "code": "TOO_MANY_TAGS",
				"count": len(rawTags), "limit": downloadImportMaxTags,
				"message": "标签数量超出上限，整条 tags 字段未应用（未截断绑定）",
			})
			appliedTags = []string{}
		}

		reject := func(code, message string) {
			rejected = append(rejected, map[string]any{
				"index": index, "row": row, "input": rawInput, "chain": itemChain,
				"code": code, "message": message,
			})
		}

		if rawInput == "" {
			blank++
			reject("BLANK_ADDRESS", "空地址行已跳过")
			continue
		}
		if len(accepted) >= downloadImportMaxAddresses {
			capRejected++
			reject("CAP_EXCEEDED", "超出单次 100 个地址上限，未纳入本次导入")
			continue
		}
		effChain := itemChain
		if effChain == "" {
			effChain = defaultChain
		}
		if effChain == "" {
			reject("CHAIN_REQUIRED", "该条没有链信息，且请求未提供默认链")
			continue
		}
		known, enabled := s.chainKnownEnabled(effChain)
		if !known {
			reject("CHAIN_UNKNOWN", "链不在服务端链注册表中："+effChain)
			continue
		}
		if !enabled {
			reject("CHAIN_DISABLED", "链当前不可用："+effChain)
			continue
		}
		normalizedList, err := dataset.NormalizeAndDedupeAddressesForChain(effChain, []string{rawInput})
		if err != nil || len(normalizedList) == 0 {
			invalid++
			reject("INVALID_ADDRESS", "地址格式不符合该链的规范："+effChain)
			continue
		}
		normalized := normalizedList[0]
		key := effChain + "|" + normalized
		if seen[key] {
			duplicate++
			reject("DUPLICATE_ADDRESS", "与前面的地址重复（去重键 chain + normalized address）")
			continue
		}
		seen[key] = true
		chainCounts[effChain]++
		if itemCase != "" {
			caseNames = append(caseNames, itemCase)
		}
		tagNames = append(tagNames, appliedTags...)
		accepted = append(accepted, map[string]any{
			"index": index, "row": row, "input": rawInput, "chain": effChain,
			"normalized": normalized, "case_name": itemCase, "tags": appliedTags,
		})
	}

	// ---- 案件/标签解析（严格工作区作用域；存储错误 fail-closed 503）----
	caseVerdicts := []map[string]any{}
	for _, name := range trimDedupeStrings(caseNames) {
		entry, err := s.resolveMetaName(r.Context(), p.WorkspaceID, name, "case", grants)
		if err != nil {
			s.deps.Log.Error("download import validate: case lookup failed", "err", err, "workspace_id", p.WorkspaceID)
			respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_META_LOOKUP_FAILED",
				"案件元数据查询失败，未做任何判定（不视为不存在）", nil)
			return
		}
		caseVerdicts = append(caseVerdicts, entry)
	}
	tagVerdicts := []map[string]any{}
	for _, name := range trimDedupeStrings(tagNames) {
		entry, err := s.resolveMetaName(r.Context(), p.WorkspaceID, name, "tag", grants)
		if err != nil {
			s.deps.Log.Error("download import validate: tag lookup failed", "err", err, "workspace_id", p.WorkspaceID)
			respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_META_LOOKUP_FAILED",
				"标签元数据查询失败，未做任何判定（不视为不存在）", nil)
			return
		}
		tagVerdicts = append(tagVerdicts, entry)
	}
	denied := 0
	for _, entry := range append(append([]map[string]any{}, caseVerdicts...), tagVerdicts...) {
		if entry["status"] == "denied" {
			denied++
		}
	}

	s.writeAuditNow(r, "download.import.validate", "download", "download_request", "", map[string]any{
		"chain": defaultChain, "submitted": len(req.Items), "accepted": len(accepted),
		"rejected": len(rejected), "field_rejections": len(fieldRejections),
		"create_missing_meta": req.CreateMissingMeta, "meta_denied": denied,
		"organization_id": p.OrganizationID, "workspace_id": p.WorkspaceID,
	})

	respondOK(w, r, map[string]any{
		"chain":            defaultChain,
		"accepted":         accepted,
		"rejected":         rejected,
		"field_rejections": fieldRejections,
		"scope": map[string]any{
			"organization_id": p.OrganizationID,
			"workspace_id":    p.WorkspaceID,
		},
		"grants": map[string]any{
			"download:create":               p.Has("download:create"),
			"case:create":                   grants.CanCreateCase,
			"case:update":                   grants.CanUpdateCase,
			"create_missing_meta_requested": grants.WantCreate,
		},
		"summary": map[string]any{
			"submitted":        len(req.Items),
			"accepted":         len(accepted),
			"rejected":         len(rejected),
			"blank":            blank,
			"invalid":          invalid,
			"duplicate":        duplicate,
			"cap_rejected":     capRejected,
			"tags_over_limit":  tagsOverLimit,
			"field_rejections": len(fieldRejections),
			"meta_denied":      denied,
			"max_addresses":    downloadImportMaxAddresses,
			"max_tags":         downloadImportMaxTags,
			"chains":           chainCounts,
			"distinct_chains":  len(chainCounts),
		},
		"meta": map[string]any{
			"cases": caseVerdicts,
			"tags":  tagVerdicts,
		},
		"source": "server:dataset.NormalizeAndDedupeAddressesForChain + chain_registry + workspace-scoped meta lookup",
	})
}
