package api

// Sprint 6 审计修复（阻断项）：完整性事件边界的后台 API。
//
// 端点语义（全部挂在 /api/admin/v1 下，逐端点声明权限）：
//   GET  /audit/integrity                          观测缺口 + 封存事件 + 链校验 + 锚点逐项结论
//   POST /audit/integrity/preview                  只读缺口检测（不写任何行）
//   GET  /audit/integrity/incidents                已封存事件列表
//   POST /audit/integrity/incidents/requests       发起封存（step-up + 原因必填 → 双人审批）
//   POST /audit/integrity/incidents/requests/{id}/approve|reject
//   POST /audit/integrity/incidents/requests/{id}/execute
//   GET  /audit/anchors                            每日锚点逐项验证
//   POST /audit/anchors                            生成/确认指定日期锚点（边界感知，幂等）
//   GET  /audit/anchors/{chain}/{date}/object      回读 MinIO 归档对象并给出 SHA-256
//
// 不可协商的约束：
//   - 封存只 INSERT，永不 UPDATE/DELETE 既有事件、锚点或链头；
//   - 不提供任何回填历史审计行的入口；
//   - 封存后 integrity_status 恒为 sealed_incident/boundary_conflict，绝不返回 intact。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
)

// s6IntegritySealOperation 是封存边界的审批操作名。
const s6IntegritySealOperation = "audit.integrity.seal"

func (s *Server) registerAdminAuditIntegrityRoutes(r chi.Router) {
	r.Get("/audit/integrity", s.require("admin:audit:verify", s.handleAdminAuditIntegrity))
	r.Post("/audit/integrity/preview", s.require("admin:audit:verify", s.handleAdminAuditIntegrityPreview))
	r.Get("/audit/integrity/incidents", s.require("admin:audit:verify", s.handleAdminAuditIntegrityIncidents))
	r.Post("/audit/integrity/incidents/requests", s.require("admin:audit:seal", s.handleAdminAuditIntegrityRequest))
	r.Post("/audit/integrity/incidents/requests/{id}/approve", s.require("admin:approval:decide", s.handleAdminAuditIntegrityApprove))
	r.Post("/audit/integrity/incidents/requests/{id}/reject", s.require("admin:approval:decide", s.handleAdminAuditIntegrityReject))
	r.Post("/audit/integrity/incidents/requests/{id}/execute", s.require("admin:audit:seal", s.handleAdminAuditIntegrityExecute))
	r.Get("/audit/anchors", s.require("admin:audit:verify", s.handleAdminAuditAnchorList))
	r.Post("/audit/anchors", s.require("admin:audit:seal", s.handleAdminAuditAnchorCreate))
	r.Get("/audit/anchors/{chain}/{date}/object", s.require("admin:audit:verify", s.handleAdminAuditAnchorObject))
}

func s6ValidChain(chain string) bool {
	return chain == audit.ChainAudit || chain == audit.ChainLogin
}

func (s *Server) s6ChainParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	chain := strings.TrimSpace(r.URL.Query().Get("chain"))
	if chain == "" {
		chain = audit.ChainAudit
	}
	if !s6ValidChain(chain) {
		validationError(w, r, "chain 取值非法", []fieldError{{Field: "chain", Code: "invalid_value", Message: "audit 或 login"}})
		return "", false
	}
	return chain, true
}

func (s *Server) s6ObjectGetter() audit.ObjectGetter {
	if s.deps.Raw == nil {
		return nil
	}
	return s.deps.Raw
}

// s6IntegrityEvent 在边界事件链上追加一条不可变记录（requested / approved / rejected）。
func (s *Server) s6IntegrityEvent(ctx context.Context, ref, chain, event, actorID, actorEmail, note string, detail map[string]any) error {
	tx, err := s.deps.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := audit.AppendIncidentEvent(ctx, tx, ref, chain, event, actorID, actorEmail, note, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- 完整性报告 ----

func (s *Server) handleAdminAuditIntegrity(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	chain, ok := s.s6ChainParam(w, r)
	if !ok {
		return
	}
	report, err := audit.BuildIntegrityReport(r.Context(), s.deps.Store.Pool, s.s6ObjectGetter(), chain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "完整性报告生成失败", nil)
		return
	}
	s.s6Audit(r, "admin.audit.integrity.read", "admin.security", "audit_chain", chain, map[string]any{
		"integrity_status":    report.IntegrityStatus,
		"prefix_status":       report.PrefixStatus,
		"verified_from_seq":   report.ChainVerification.VerifiedFromSeq,
		"valid":               report.ChainVerification.Valid,
		"anchors_verified":    report.AnchorsVerified,
		"anchors_sealed":      report.AnchorsSealed,
		"anchors_unexplained": report.AnchorsUnexplained,
	})
	respondOK(w, r, report)
}

func (s *Server) handleAdminAuditIntegrityPreview(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		Chain string `json:"chain"`
	}
	_ = decodeJSON(r, &in)
	if in.Chain == "" {
		in.Chain = audit.ChainAudit
	}
	if !s6ValidChain(in.Chain) {
		validationError(w, r, "chain 取值非法", []fieldError{{Field: "chain", Code: "invalid_value", Message: "audit 或 login"}})
		return
	}
	ctx := r.Context()
	observed, err := audit.DetectGap(ctx, s.deps.Store.Pool, in.Chain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "缺口检测失败", nil)
		return
	}
	incident, err := audit.LoadIncident(ctx, s.deps.Store.Pool, in.Chain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "封存事件读取失败", nil)
		return
	}
	payload := map[string]any{
		"observed":        observed,
		"incident":        incident,
		"already_sealed":  incident != nil,
		"sealable":        observed.HasPrefixGap && observed.InternalGapCount == 0,
		"requires_action": observed.HasPrefixGap && incident == nil,
	}
	if observed.HasPrefixGap {
		payload["incident_ref"] = audit.IncidentRefFor(in.Chain, observed.GapFromSeq, observed.GapToSeq)
	}
	if observed.InternalGapCount > 0 {
		payload["blocking_reason"] = "幸存区间内部存在断层，属于真实篡改；必须先在链上定位与处置，不得用边界掩盖"
	}
	s.s6Audit(r, "admin.audit.integrity.preview", "admin.security", "audit_chain", in.Chain, map[string]any{
		"has_prefix_gap": observed.HasPrefixGap,
		"gap_from_seq":   observed.GapFromSeq,
		"gap_to_seq":     observed.GapToSeq,
		"sealed":         incident != nil,
	})
	respondOK(w, r, payload)
}

func (s *Server) handleAdminAuditIntegrityIncidents(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	items := make([]audit.Incident, 0, 2)
	for _, chain := range []string{audit.ChainAudit, audit.ChainLogin} {
		inc, err := audit.LoadIncident(ctx, s.deps.Store.Pool, chain)
		if err != nil {
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "封存事件读取失败", nil)
			return
		}
		if inc != nil {
			items = append(items, *inc)
		}
	}
	respondList(w, r, map[string]any{"items": items}, len(items), nil)
}

// ---- 封存：请求 / 审批 / 执行 ----

func (s *Server) handleAdminAuditIntegrityRequest(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		Chain          string `json:"chain"`
		Reason         string `json:"reason"`
		StepUpPassword string `json:"step_up_password"`
		// Kind 为空时按缺口封存处理（向后兼容）；declared_integrity_events
		// 走稀疏声明路径（ADR-019），可声明缺失行与内容不可复算行。
		Kind string `json:"kind"`
	}
	_ = decodeJSON(r, &in)
	if in.Chain == "" {
		in.Chain = audit.ChainAudit
	}
	if !s6ValidChain(in.Chain) {
		validationError(w, r, "chain 取值非法", []fieldError{{Field: "chain", Code: "invalid_value", Message: "audit 或 login"}})
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写封存原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	if in.Kind == audit.IncidentKindDeclared {
		s.handleAdminAuditIntegrityDeclareRequest(w, r, in.Chain, in.Reason, in.StepUpPassword)
		return
	}
	ctx := r.Context()
	observed, err := audit.DetectGap(ctx, s.deps.Store.Pool, in.Chain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "缺口检测失败", nil)
		return
	}
	if observed.InternalGapCount > 0 {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE",
			"幸存区间内部存在断层，必须先定位真实篡改，不能用边界掩盖", nil)
		return
	}
	if !observed.HasPrefixGap {
		respondError(w, r, http.StatusConflict, "NO_GAP_DETECTED",
			"当前链没有可封存的前缀缺口", map[string]any{"min_seq": observed.FirstSurvivingSeq})
		return
	}
	if existing, err := audit.LoadIncident(ctx, s.deps.Store.Pool, in.Chain); err == nil && existing != nil {
		respondOK(w, r, map[string]any{
			"incident":       existing,
			"already_sealed": true,
			"approval":       nil,
		})
		return
	}
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.audit.integrity.request") {
		return
	}
	p := PrincipalFrom(ctx)
	ref := audit.IncidentRefFor(in.Chain, observed.GapFromSeq, observed.GapToSeq)
	payload := map[string]any{
		"chain":             in.Chain,
		"incident_ref":      ref,
		"reason":            in.Reason,
		"gap_from_seq":      observed.GapFromSeq,
		"gap_to_seq":        observed.GapToSeq,
		"gap_event_count":   observed.GapEventCount,
		"trusted_from_seq":  observed.FirstSurvivingSeq,
		"sealed_tail_hash":  observed.SealedTailHash,
		"head_seq":          observed.HeadSeq,
		"detection_method":  "verifier.sequence_gap + prev_hash_of_first_surviving_row",
		"forbidden_actions": []string{"回填历史审计行", "改写既有锚点", "把 NOT_VERIFIED 记为 PASS"},
	}
	approval, err := s.s6CreateApproval(ctx, s6IntegritySealOperation, "audit_chain", in.Chain, payload,
		p.IdentityID, p.Email, 24*time.Hour)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审批请求创建失败", nil)
		return
	}
	if err := s.s6IntegrityEvent(ctx, ref, in.Chain, "requested", p.IdentityID, p.Email, in.Reason, map[string]any{
		"approval_request_id": approval.ID,
		"gap_from_seq":        observed.GapFromSeq,
		"gap_to_seq":          observed.GapToSeq,
		"trusted_from_seq":    observed.FirstSurvivingSeq,
		"sealed_tail_hash":    observed.SealedTailHash,
	}); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "边界事件记录失败", nil)
		return
	}
	s.s6AuditWithApproval(r, "admin.audit.integrity.request", "admin.security", "audit_chain", in.Chain, approval.ID,
		map[string]any{
			"reason": in.Reason, "incident_ref": ref, "gap_from_seq": observed.GapFromSeq,
			"gap_to_seq": observed.GapToSeq, "sealed_tail_hash": observed.SealedTailHash,
			"step_up_method": "password",
		})
	respondCreated(w, r, map[string]any{
		"approval":       approval,
		"observed":       observed,
		"incident_ref":   ref,
		"already_sealed": false,
	})
}

func (s *Server) handleAdminAuditIntegrityApprove(w http.ResponseWriter, r *http.Request) {
	s.s6IntegrityDecision(w, r, "approved")
}

func (s *Server) handleAdminAuditIntegrityReject(w http.ResponseWriter, r *http.Request) {
	s.s6IntegrityDecision(w, r, "rejected")
}

func (s *Server) s6IntegrityDecision(w http.ResponseWriter, r *http.Request, decision string) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		Note string `json:"note"`
	}
	_ = decodeJSON(r, &in)
	if strings.TrimSpace(in.Note) == "" {
		validationError(w, r, "必须填写审批意见", []fieldError{{Field: "note", Code: "required", Message: "审批意见必填"}})
		return
	}
	ctx := r.Context()
	current, err := s.s6LoadApproval(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "封存审批请求不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审批请求读取失败", nil)
		return
	}
	if current.Operation != s6IntegritySealOperation {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "该审批请求不是审计边界封存请求", nil)
		return
	}
	p := PrincipalFrom(ctx)
	updated, err := s.s6DecideApproval(ctx, id, decision, in.Note, p.IdentityID, p.Email)
	switch {
	case errors.Is(err, errS6TwoPerson):
		respondError(w, r, http.StatusForbidden, "TWO_PERSON_REQUIRED",
			"封存审计边界不能由发起人自行审批，需要第二名具备审批权限的人员", nil)
		return
	case err != nil:
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", err.Error(), nil)
		return
	}
	ref, _ := current.Payload["incident_ref"].(string)
	if ref == "" {
		ref = "INC-" + strings.ToUpper(current.TargetID)
	}
	_ = s.s6IntegrityEvent(ctx, ref, current.TargetID, decision, p.IdentityID, p.Email, in.Note, map[string]any{
		"approval_request_id": id, "decision": decision,
	})
	s.s6AuditWithApproval(r, "admin.audit.integrity."+decision, "admin.security", "audit_chain", current.TargetID, id,
		map[string]any{"note": in.Note, "requester": current.RequestedByEmail})
	respondOK(w, r, updated)
}

func (s *Server) handleAdminAuditIntegrityExecute(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		StepUpPassword string `json:"step_up_password"`
		Reason         string `json:"reason"`
	}
	_ = decodeJSON(r, &in)
	ctx := r.Context()
	approval, err := s.s6LoadApproval(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "封存审批请求不存在", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审批请求读取失败", nil)
		return
	}
	if approval.Operation != s6IntegritySealOperation {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "该审批请求不是审计边界封存请求", nil)
		return
	}
	if _, err := s.s6RequireApproved(ctx, id, s6IntegritySealOperation); err != nil {
		respondError(w, r, http.StatusPreconditionFailed, "APPROVAL_REQUIRED", err.Error(), nil)
		return
	}
	if !s.s6StepUpOrFail(w, r, in.StepUpPassword, "admin.audit.integrity.execute") {
		return
	}
	chain := approval.TargetID
	if !s6ValidChain(chain) {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "审批请求的目标链非法", nil)
		return
	}
	if kind, _ := approval.Payload["kind"].(string); kind == audit.IncidentKindDeclared {
		p := PrincipalFrom(ctx)
		reason := strings.TrimSpace(in.Reason)
		if reason == "" {
			reason, _ = approval.Payload["reason"].(string)
		}
		if reason == "" {
			reason = "双人审批通过后声明稀疏完整性事实（ADR-019）"
		}
		incident, refs, err := s.s6ExecuteDeclaredEvents(ctx, approval, chain, reason, p.IdentityID, p.Email)
		if err != nil {
			respondError(w, r, http.StatusConflict, "CONFLICT_STATE", err.Error(), nil)
			return
		}
		_ = s.s6FinishApproval(ctx, id, "executed", map[string]any{"declaration_refs": refs})
		s.s6AuditWithApproval(r, "admin.audit.integrity.declare.execute", "admin.security", "audit_chain", chain, id,
			map[string]any{"declaration_refs": refs, "incident_ref": incident.IncidentRef})
		respondOK(w, r, map[string]any{"declaration_refs": refs, "incident_ref": incident.IncidentRef,
			"declared_lost_count": incident.GapEventCount})
		return
	}
	observed, err := audit.DetectGap(ctx, s.deps.Store.Pool, chain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "缺口检测失败", nil)
		return
	}
	wantFrom, _ := s6PayloadInt(approval.Payload, "gap_from_seq")
	wantTo, _ := s6PayloadInt(approval.Payload, "gap_to_seq")
	wantTail, _ := approval.Payload["sealed_tail_hash"].(string)
	if !observed.HasPrefixGap || observed.GapFromSeq != wantFrom || observed.GapToSeq != wantTo ||
		observed.SealedTailHash != wantTail {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE",
			"审批时的缺口与实际观测不一致，封存已中止（不会写入任何行）",
			map[string]any{
				"approved_gap_from_seq": wantFrom, "approved_gap_to_seq": wantTo,
				"observed_gap_from_seq": observed.GapFromSeq, "observed_gap_to_seq": observed.GapToSeq,
			})
		return
	}
	p := PrincipalFrom(ctx)
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason, _ = approval.Payload["reason"].(string)
	}
	if reason == "" {
		reason = "双人审批通过后封存不可恢复审计缺口边界"
	}
	incident, err := audit.SealIncident(ctx, s.deps.Store.Pool, s.s6ObjectGetter(), audit.SealParams{
		Chain:             chain,
		Reason:            reason,
		SealedBy:          p.IdentityID,
		SealedByEmail:     p.Email,
		ApprovalRequestID: id,
		RecoveryAttempts:  s6RecoveryAttempts(),
	})
	switch {
	case errors.Is(err, audit.ErrNoGapDetected):
		respondError(w, r, http.StatusConflict, "NO_GAP_DETECTED", "当前链没有可封存的前缀缺口", nil)
		return
	case errors.Is(err, audit.ErrInternalGap):
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "幸存区间内部存在断层，禁止用边界掩盖", nil)
		return
	case err != nil:
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "边界封存失败："+err.Error(), nil)
		return
	}
	_ = s.s6FinishApproval(ctx, id, "executed", map[string]any{
		"incident_ref": incident.IncidentRef, "evidence_hash": incident.EvidenceHash,
	})
	s.s6AuditWithApproval(r, "admin.audit.integrity.execute", "admin.security", "audit_chain", chain, id,
		map[string]any{
			"incident_ref": incident.IncidentRef, "gap_from_seq": incident.GapFromSeq,
			"gap_to_seq": incident.GapToSeq, "trusted_from_seq": incident.TrustedFromSeq,
			"sealed_tail_hash": incident.SealedTailHash, "evidence_hash": incident.EvidenceHash,
			"already_sealed": incident.AlreadySealed,
		})
	respondOK(w, r, map[string]any{"incident": incident, "approval_id": id})
}

// s6RecoveryAttempts 记录「是否可从真实备份恢复」的排查结论，作为封存证据的一部分。
// 这些结论来自本环境的实际核查（见 docs/implementation/sprint6-report.md §6），
// 不是假设：缺口的不可恢复性必须可追溯。
func s6RecoveryAttempts() []map[string]any {
	return []map[string]any{
		{"source": "postgres_base_backup", "checked": true, "available": false,
			"detail": "环境内不存在该库的 pg_dump / 物理基备文件"},
		{"source": "wal_archiving", "checked": true, "available": false,
			"detail": "archive_mode=off、archive_command=(disabled)，无 WAL 归档；且无基备时 WAL 无法单独重建"},
		{"source": "filesystem_or_vm_snapshot", "checked": true, "available": false,
			"detail": "LVM 无快照卷（lvs 仅 ubuntu-lv/data_lv），无块设备或虚拟机快照"},
		{"source": "minio_audit_archive", "checked": true, "available": false,
			"detail": "blockchain-audit 桶只保存每日 anchor.json（不含事件本体），缺口区间事件不可重建"},
		{"source": "clickhouse_audit_mirror", "checked": true, "available": false,
			"detail": "ClickHouse blockchain 库只有链上数据表，没有审计事件镜像表"},
		{"source": "audit_exports", "checked": true, "available": false,
			"detail": "audit_exports 全部记录的 from_seq 均晚于缺口区间，未覆盖被销毁的事件"},
	}
}

func s6PayloadInt(payload map[string]any, key string) (int64, bool) {
	if payload == nil {
		return 0, false
	}
	switch v := payload[key].(type) {
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case int64:
		return v, true
	case int:
		return int64(v), true
	}
	return 0, false
}

// ---- 锚点逐项验证与归档对象回读 ----

func (s *Server) handleAdminAuditAnchorList(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	chain, ok := s.s6ChainParam(w, r)
	if !ok {
		return
	}
	items, err := audit.VerifyAnchors(r.Context(), s.deps.Store.Pool, s.s6ObjectGetter(), chain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "锚点验证失败", nil)
		return
	}
	verified, legacy, sealed, unexplained := 0, 0, 0, 0
	for _, it := range items {
		switch it.Disposition {
		case audit.AnchorVerified:
			verified++
		case audit.AnchorVerifiedLegacy:
			legacy++
		case audit.AnchorSealedUnrecoverable:
			sealed++
		default:
			unexplained++
		}
	}
	s.s6Audit(r, "admin.audit.anchors.verify", "admin.security", "audit_chain_anchors", chain, map[string]any{
		"count": len(items), "verified": verified, "legacy": legacy, "sealed": sealed, "unexplained": unexplained,
	})
	respondList(w, r, map[string]any{
		"items": items, "verified": verified, "legacy_derivation": legacy,
		"sealed_unrecoverable": sealed, "unexplained": unexplained,
		"all_accounted": unexplained == 0,
	}, len(items), nil)
}

func (s *Server) handleAdminAuditAnchorCreate(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	var in struct {
		Chain  string `json:"chain"`
		Date   string `json:"date"`
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &in)
	if in.Chain == "" {
		in.Chain = audit.ChainAudit
	}
	if !s6ValidChain(in.Chain) {
		validationError(w, r, "chain 取值非法", []fieldError{{Field: "chain", Code: "invalid_value", Message: "audit 或 login"}})
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		validationError(w, r, "必须填写生成锚点的原因", []fieldError{{Field: "reason", Code: "required", Message: "原因必填"}})
		return
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	if strings.TrimSpace(in.Date) != "" {
		parsed, err := time.Parse("2006-01-02", strings.TrimSpace(in.Date))
		if err != nil {
			validationError(w, r, "date 必须是 YYYY-MM-DD", []fieldError{{Field: "date", Code: "invalid_format", Message: "YYYY-MM-DD"}})
			return
		}
		day = parsed.UTC()
	}
	ctx := r.Context()
	getter := s.s6ObjectGetter()
	archiver := audit.NewArchiver(s.deps.Store.Pool, s.deps.Raw, s.deps.Cfg.Env, s.deps.Log)
	if getter != nil {
		archiver = archiver.WithObjectGetter(getter)
	}
	anchors, err := archiver.ArchiveOnceForDate(ctx, day)
	if err != nil {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE", "锚点生成失败："+err.Error(), nil)
		return
	}
	created := false
	var createdAnchor *audit.Anchor
	for i := range anchors {
		if anchors[i].Chain == in.Chain {
			created = true
			createdAnchor = &anchors[i]
		}
	}
	items, err := audit.VerifyAnchors(ctx, s.deps.Store.Pool, getter, in.Chain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "锚点验证失败", nil)
		return
	}
	s.s6Audit(r, "admin.audit.anchors.create", "admin.security", "audit_chain_anchors", in.Chain, map[string]any{
		"reason": in.Reason, "date": day.Format("2006-01-02"), "created": created,
	})
	respondOK(w, r, map[string]any{
		"chain": in.Chain, "date": day.Format("2006-01-02"), "created": created,
		"anchor": createdAnchor, "anchors": items,
		"note": "既有锚点不可改写：若该日期已有锚点，本次只做逐项复核，不重写也不重传归档对象",
	})
}

func (s *Server) handleAdminAuditAnchorObject(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	chain := chi.URLParam(r, "chain")
	date := chi.URLParam(r, "date")
	if !s6ValidChain(chain) {
		validationError(w, r, "chain 取值非法", []fieldError{{Field: "chain", Code: "invalid_value", Message: "audit 或 login"}})
		return
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		validationError(w, r, "date 必须是 YYYY-MM-DD", []fieldError{{Field: "date", Code: "invalid_format", Message: "YYYY-MM-DD"}})
		return
	}
	ctx := r.Context()
	var objectRef, contentHash, headHash string
	var fromSeq, toSeq, eventCount int64
	err := s.deps.Store.Pool.QueryRow(ctx, `SELECT object_ref, content_hash, head_hash, from_seq, to_seq, event_count
	  FROM audit_chain_anchors WHERE chain = $1 AND anchor_date = $2`, chain, date).
		Scan(&objectRef, &contentHash, &headHash, &fromSeq, &toSeq, &eventCount)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "该日期没有锚点", nil)
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "锚点读取失败", nil)
		return
	}
	getter := s.s6ObjectGetter()
	if getter == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "对象存储不可用，无法回读归档对象", nil)
		return
	}
	rc, size, err := getter.OpenNamed(ctx, audit.ArchiveBucket, objectRef)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "OBJECT_NOT_FOUND", "归档对象不存在："+objectRef, nil)
		return
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "归档对象读取失败", nil)
		return
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	s.s6Audit(r, "admin.audit.anchors.object", "admin.security", "audit_chain_anchor", chain+"/"+date, map[string]any{
		"object_ref": objectRef, "object_hash": got, "content_hash": contentHash, "intact": got == contentHash,
	})
	respondOK(w, r, map[string]any{
		"chain": chain, "anchor_date": date, "object_ref": objectRef, "size_bytes": size,
		"object_sha256": got, "recorded_content_hash": contentHash, "intact": got == contentHash,
		"head_hash": headHash, "from_seq": fromSeq, "to_seq": toSeq, "event_count": eventCount,
		"manifest": json.RawMessage(body),
	})
}

// ---- 稀疏声明（ADR-019 / 迁移 0063） ----

// s6DeclaredRefFromSummary 把扫描结果里的两个集合整理成可比较形态。
func s6DeclaredEventsOf(items []audit.DeclaredEvent) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{"seq": it.Seq, "observed_event_hash": it.ObservedEventHash})
	}
	return out
}

// s6PayloadDeclaredEvents 从审批载荷读回声明集合（seq -> observed_event_hash）。
func s6PayloadDeclaredEvents(payload map[string]any, key string) map[int64]string {
	out := map[int64]string{}
	raw, ok := payload[key].([]any)
	if !ok {
		return out
	}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		seq, ok := s6PayloadInt(m, "seq")
		if !ok {
			continue
		}
		hash, _ := m["observed_event_hash"].(string)
		out[seq] = hash
	}
	return out
}

// handleAdminAuditIntegrityDeclareRequest 发起一次稀疏声明（step-up + 双人审批）。
//
// 与缺口封存的区别：这里声明的是「逐条事实」而不是「连续缺口」——缺失行可以稀疏地
// 夹在真实数据之间，内容不可复算行则本来就存在。两者都是 append-only 链上不可修复、
// 也不允许被忽略的事实（ADR-019）。
func (s *Server) handleAdminAuditIntegrityDeclareRequest(w http.ResponseWriter, r *http.Request, chain, reason, stepUp string) {
	ctx := r.Context()
	scan, err := audit.ScanChain(ctx, s.deps.Store.Pool, chain)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "全链扫描失败", nil)
		return
	}
	if scan.Blocking() {
		respondError(w, r, http.StatusConflict, "CONFLICT_STATE",
			"存在 prev_hash 链接断裂（真实篡改），必须先定位处置，不能用声明掩盖",
			map[string]any{"link_breaks": s6DeclaredEventsOf(scan.LinkBreaks)})
		return
	}
	// 已经声明过的事实不再重复声明（声明表是 append-only 的，重复登记只会制造噪声）。
	// 校验器按 seq 读声明，过滤掉已声明项后仍为空的扫描结果意味着"无可声明事实"。
	if declared, derr := audit.LoadDeclaredEvents(ctx, s.deps.Store.Pool, chain); derr == nil {
		scan.MissingSeqs = s6FilterUndeclared(scan.MissingSeqs, declared)
		scan.ContentAnomalies = s6FilterUndeclared(scan.ContentAnomalies, declared)
	}
	if len(scan.MissingSeqs) == 0 && len(scan.ContentAnomalies) == 0 {
		respondOK(w, r, map[string]any{"declaration_needed": false, "scan": scan})
		return
	}
	if !s.s6StepUpOrFail(w, r, stepUp, "admin.audit.integrity.declare.request") {
		return
	}
	p := PrincipalFrom(ctx)
	ref := audit.DeclaredRefFor(chain, "all", s6SeqList(scan.MissingSeqs, scan.ContentAnomalies))
	payload := map[string]any{
		"kind":              audit.IncidentKindDeclared,
		"chain":             chain,
		"declaration_ref":   ref,
		"reason":            reason,
		"lost":              s6DeclaredEventsOf(scan.MissingSeqs),
		"content_anomalies": s6DeclaredEventsOf(scan.ContentAnomalies),
		"detection_method":  "audit.ScanChain（逐行复算整条链并收敛全部问题）",
		"head_seq":          scan.HeadSeq,
		"checked":           scan.Checked,
		"forbidden_actions": []string{"回填缺失的审计行", "重写既有行的 event_hash", "把已声明事实记为完整"},
	}
	approval, err := s.s6CreateApproval(ctx, s6IntegritySealOperation, "audit_chain", chain, payload,
		p.IdentityID, p.Email, 24*time.Hour)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "审批请求创建失败", nil)
		return
	}
	if err := s.s6IntegrityEvent(ctx, ref, chain, "requested", p.IdentityID, p.Email, reason, map[string]any{
		"approval_request_id": approval.ID,
		"lost_count":          len(scan.MissingSeqs),
		"anomaly_count":       len(scan.ContentAnomalies),
	}); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "声明事件记录失败", nil)
		return
	}
	s.s6AuditWithApproval(r, "admin.audit.integrity.declare.request", "admin.security", "audit_chain", chain, approval.ID,
		map[string]any{
			"reason": reason, "declaration_ref": ref,
			"lost_count": len(scan.MissingSeqs), "anomaly_count": len(scan.ContentAnomalies),
			"step_up_method": "password",
		})
	respondCreated(w, r, map[string]any{
		"approval":        approval,
		"scan":            scan,
		"declaration_ref": ref,
	})
}

// s6SeqList 汇总两段 seq，用于生成确定性引用名。
func s6SeqList(groups ...[]audit.DeclaredEvent) []int64 {
	out := []int64{}
	for _, g := range groups {
		for _, e := range g {
			out = append(out, e.Seq)
		}
	}
	return out
}

// s6ExecuteDeclaredEvents 执行一次稀疏声明：重新扫描，逐条比对审批时的集合与哈希，
// 一致才写入（任何漂移都中止，不写入任何行）。
func (s *Server) s6ExecuteDeclaredEvents(ctx context.Context, approval s6Approval, chain, reason, actorID, actorEmail string) (*audit.Incident, []string, error) {
	scan, err := audit.ScanChain(ctx, s.deps.Store.Pool, chain)
	if err != nil {
		return nil, nil, err
	}
	if scan.Blocking() {
		return nil, nil, errors.New("扫描发现 prev_hash 链接断裂，已中止声明")
	}
	wantLost := s6PayloadDeclaredEvents(approval.Payload, "lost")
	wantAnomalies := s6PayloadDeclaredEvents(approval.Payload, "content_anomalies")
	nowLost := map[int64]string{}
	for _, e := range scan.MissingSeqs {
		nowLost[e.Seq] = e.ObservedEventHash
	}
	nowAnomalies := map[int64]string{}
	for _, e := range scan.ContentAnomalies {
		nowAnomalies[e.Seq] = e.ObservedEventHash
	}
	if !s6SameDeclaredSet(wantLost, nowLost) || !s6SameDeclaredSet(wantAnomalies, nowAnomalies) {
		return nil, nil, errors.New("审批时的声明集合与当前扫描结果不一致，已中止（不会写入任何行）")
	}
	refs := []string{}
	var first *audit.Incident
	groups := []struct {
		disposition string
		events      []audit.DeclaredEvent
	}{
		{audit.DispositionLost, scan.MissingSeqs},
		{audit.DispositionContentAnomaly, scan.ContentAnomalies},
	}
	for _, g := range groups {
		if len(g.events) == 0 {
			continue
		}
		inc, err := audit.SealDeclaredEvents(ctx, s.deps.Store.Pool, audit.SealDeclaredParams{
			Chain:             chain,
			Disposition:       g.disposition,
			Events:            g.events,
			Reason:            reason,
			SealedBy:          actorID,
			SealedByEmail:     actorEmail,
			ApprovalRequestID: approval.ID,
			DetectionMethod:   "audit.ScanChain（逐行复算整条链并收敛全部问题）",
			Evidence: map[string]any{
				"chain": chain, "head_seq": scan.HeadSeq, "checked": scan.Checked,
				"row_count": scan.RowCount, "scanned_at": scan.ScannedAt,
			},
		})
		if err != nil {
			return nil, refs, err
		}
		refs = append(refs, inc.IncidentRef)
		if first == nil {
			first = inc
		}
	}
	return first, refs, nil
}

// s6SameDeclaredSet 比较 seq -> hash 集合（顺序无关，缺失/多出/哈希不同都算不一致）。
func s6SameDeclaredSet(a, b map[int64]string) bool {
	if len(a) != len(b) {
		return false
	}
	for seq, hash := range a {
		if b[seq] != hash {
			return false
		}
	}
	return true
}

// s6FilterUndeclared 去掉已经声明过的 seq（同 seq 同哈希才算已声明）。
func s6FilterUndeclared(items []audit.DeclaredEvent, declared map[int64]audit.DeclaredEvent) []audit.DeclaredEvent {
	out := make([]audit.DeclaredEvent, 0, len(items))
	for _, it := range items {
		if d, ok := declared[it.Seq]; ok && d.ObservedEventHash == it.ObservedEventHash {
			continue
		}
		out = append(out, it)
	}
	return out
}
