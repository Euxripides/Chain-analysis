package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

// ---- V1.3 Method / ABI API（§52）----

// handleTransactionDetail 交易详情 + 按需深度解码（§25）。
//
//	GET /api/v1/transactions/{chain}/{tx_hash}?decode=local_only|auto|force_refresh
//
// 默认 auto：本地已有解码结果直接返回，绝不重复请求 Provider（§23/§62-9）。
func (s *Server) handleTransactionDetail(w http.ResponseWriter, r *http.Request) {
	if s.detail == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "method resolver is disabled"})
		return
	}
	chain := strings.ToLower(chi.URLParam(r, "chain"))
	txHash := strings.ToLower(chi.URLParam(r, "tx_hash"))
	mode, err := method.ParseDecodeMode(r.URL.Query().Get("decode"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// force_refresh 会产生真实 Provider 成本，限制为管理员/分析员操作（§50）
	if mode == method.DecodeForceRefresh && !canForceDecode(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "force_refresh requires an elevated role"})
		return
	}
	detail, err := s.detail.Detail(r.Context(), chain, txHash, mode)
	if errors.Is(err, method.ErrTransactionNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "transaction not found in local dataset"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleMethodLookup Selector 查询（§52/§53）。
// 必须返回全部候选：Selector 不是唯一标识（§10）。
func (s *Server) handleMethodLookup(w http.ResponseWriter, r *http.Request) {
	selector := method.NormalizeSelector(chi.URLParam(r, "selector"))
	if selector == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "selector must be 4-byte hex, e.g. 0xa9059cbb"})
		return
	}
	records, err := s.deps.Store.CandidatesBySelector(r.Context(), selector)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	candidates := make([]method.MethodCandidate, 0, len(records))
	for _, rec := range records {
		candidates = append(candidates, method.MethodCandidate{
			Name: rec.Name, Signature: rec.Signature, Source: rec.Source,
			Verified: method.CandidateVerified(rec.Source, rec.Verified),
		})
	}
	resolution := method.ChooseCandidate(selector, -1, candidates)
	writeJSON(w, http.StatusOK, map[string]any{
		"selector":   selector,
		"candidates": candidates,
		"resolved":   resolution.Resolved(),
		"resolution": method.MethodInfoFrom(resolution),
		"collision":  len(candidates) > 1,
	})
}

// handleMethodSearch Method Name / Signature / Selector 搜索（§28）。
func (s *Server) handleMethodSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	records, err := s.deps.Store.SearchMethodSignatures(r.Context(), query, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if records == nil {
		records = []method.SignatureRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": query, "results": records, "count": len(records)})
}

// handleMethodEnrichment Method 解析覆盖率（§37）。
// 与下载 Coverage 完全解耦：Unknown 不影响 Download 100%。
func (s *Server) handleMethodEnrichment(w http.ResponseWriter, r *http.Request) {
	if s.deps.MethodData == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "method resolver is disabled"})
		return
	}
	chain := strings.ToLower(r.URL.Query().Get("chain"))
	stats, err := s.deps.MethodData.Enrichment(r.Context(), chain)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	depth, _ := s.deps.Store.MethodResolveQueueDepth(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"enrichment": stats, "resolve_queue_depth": depth,
		"note": "method enrichment is independent of download coverage",
	})
}

// ---- 管理端（§52：必须权限控制）----

func canForceDecode(r *http.Request) bool {
	switch auth.FromContext(r.Context()).Role {
	case "owner", "admin", "analyst", "investigator":
		return true
	default:
		return false
	}
}

func isAdmin(r *http.Request) bool {
	switch auth.FromContext(r.Context()).Role {
	case "owner", "admin":
		return true
	default:
		return false
	}
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if isAdmin(r) {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin role required"})
	return false
}

// handleCreateBackfill 历史数据回填（§34/§35）。只做本地重解析，绝不重新下载（§62-12）。
func (s *Server) handleCreateBackfill(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Chain     string `json:"chain"`
		BatchSize int    `json:"batch_size"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Chain) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "chain required"})
		return
	}
	job, err := s.deps.Store.CreateBackfillJob(r.Context(), req.Chain, req.BatchSize)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.deps.Store.InsertAudit(r.Context(), nil, "method.backfill.create", "method_backfill_job", job.ID,
		map[string]any{"chain": job.Chain, "batch_size": job.BatchSize})
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleListBackfill(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := s.deps.Store.ListBackfillJobs(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleGetBackfill(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	job, err := s.deps.Store.GetBackfillJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "backfill job not found"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleControlBackfill pause / resume / cancel（§35）。
func (s *Server) handleControlBackfill(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	action := chi.URLParam(r, "action")
	var target string
	switch action {
	case "pause":
		target = method.BackfillPauseReq
	case "resume":
		target = method.BackfillPending
	case "cancel":
		target = method.BackfillCancelReq
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "action must be pause, resume or cancel"})
		return
	}
	job, err := s.deps.Store.GetBackfillJob(r.Context(), id)
	if err != nil || job == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "backfill job not found"})
		return
	}
	switch job.Status {
	case method.BackfillCompleted, method.BackfillCancelled:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "backfill job is " + job.Status})
		return
	}
	if err := s.deps.Store.SetBackfillStatus(r.Context(), id, target, ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.deps.Store.InsertAudit(r.Context(), nil, "method.backfill."+action, "method_backfill_job", id,
		map[string]any{"status": target})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": target})
}

// handleCreateSignature 人工覆盖签名（§30 manual_override / §62-17：不修改 Raw Input）。
func (s *Server) handleCreateSignature(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Signature string `json:"signature"`
		Source    string `json:"source"`
		Chain     string `json:"chain"`
		Contract  string `json:"contract"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	signature := strings.TrimSpace(req.Signature)
	if signature == "" || !strings.Contains(signature, "(") || !strings.HasSuffix(signature, ")") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "signature must look like transfer(address,uint256)"})
		return
	}
	if _, err := method.SignatureParamTypes(signature); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unparsable signature: " + err.Error()})
		return
	}
	source := req.Source
	if source == "" {
		source = method.SourceManualOverride
	}
	record := method.SignatureRecord{
		Selector: method.SelectorOf(signature), Name: method.NameOf(signature),
		Signature: signature, Source: source, Verified: source == method.SourceManualOverride,
	}
	before, err := s.deps.Store.CandidatesBySelector(r.Context(), record.Selector)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if _, err := s.deps.Store.UpsertMethodSignatures(r.Context(), []method.SignatureRecord{record}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if s.deps.Registry != nil {
		if err := s.deps.Registry.Refresh(r.Context(), s.deps.Store); err != nil {
			s.deps.Log.Warn("method registry refresh after override failed", "err", err)
		}
	}
	actor := auth.FromContext(r.Context())
	s.deps.Store.InsertAudit(r.Context(), nil, "method.signature.upsert", "method_signature", record.Selector,
		map[string]any{
			"actor": map[string]any{"id": actor.ID, "email": actor.Email, "role": actor.Role},
			"chain": strings.ToLower(strings.TrimSpace(req.Chain)), "contract": strings.ToLower(strings.TrimSpace(req.Contract)),
			"selector": record.Selector, "before": before, "after": record,
		})
	writeJSON(w, http.StatusCreated, record)
}

// handleUploadABI 上传合约 ABI（§13）。ABI 解析优先级高于任何签名库（§62-5）。
func (s *Server) handleUploadABI(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	chain := strings.ToLower(chi.URLParam(r, "chain"))
	address := strings.ToLower(chi.URLParam(r, "address"))
	var req struct {
		ABI                   json.RawMessage `json:"abi"`
		Source                string          `json:"source"`
		Verified              bool            `json:"verified"`
		ImplementationAddress string          `json:"implementation_address"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if len(req.ABI) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "abi required"})
		return
	}
	parsed, err := method.ParseABI(req.ABI)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid abi: " + err.Error()})
		return
	}
	source := req.Source
	if source == "" {
		source = "user_upload"
	}
	record := method.ABIRecord{
		Chain: chain, ContractAddress: address, ABIJSON: req.ABI, Source: source,
		Verified: req.Verified, ImplementationAddress: req.ImplementationAddress,
	}
	if err := s.deps.Store.UpsertContractABI(r.Context(), record); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if s.deps.Registry != nil {
		if err := s.deps.Registry.Refresh(r.Context(), s.deps.Store); err != nil {
			s.deps.Log.Warn("method registry refresh after abi upload failed", "err", err)
		}
	}
	eventCount := len(parsed.Events) + len(parsed.AnonymousEvents)
	s.deps.Store.InsertAudit(r.Context(), nil, "contract.abi.upload", "contract_abi", chain+":"+address,
		map[string]any{"source": source, "methods": len(parsed.Methods), "events": eventCount})
	writeJSON(w, http.StatusCreated, map[string]any{
		"chain": chain, "contract_address": address, "source": source,
		"methods": len(parsed.Methods), "events": eventCount, "verified": req.Verified,
	})
}
