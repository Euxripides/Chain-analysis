package casework

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type ReportView struct {
	ID              string           `json:"id"`
	InvestigationID string           `json:"investigation_id"`
	CaseID          *string          `json:"case_id,omitempty"`
	CaseNo          string           `json:"case_no,omitempty"`
	Revision        int64            `json:"revision"`
	Format          string           `json:"format"`
	ContentHash     string           `json:"content_hash"`
	SizeBytes       int64            `json:"size_bytes"`
	Status          string           `json:"status"`
	StatusRevision  int64            `json:"status_revision"`
	Metadata        map[string]any   `json:"metadata"`
	GeneratedAt     time.Time        `json:"generated_at"`
	SubmittedAt     *time.Time       `json:"submitted_at,omitempty"`
	SubmittedBy     *string          `json:"submitted_by,omitempty"`
	ApprovedAt      *time.Time       `json:"approved_at,omitempty"`
	ApprovedBy      *string          `json:"approved_by,omitempty"`
	RejectedAt      *time.Time       `json:"rejected_at,omitempty"`
	RejectReason    string           `json:"reject_reason"`
	SignedAt        *time.Time       `json:"signed_at,omitempty"`
	SignatureHash   string           `json:"signature_hash,omitempty"`
	ArchivedAt      *time.Time       `json:"archived_at,omitempty"`
	ArtifactSHA256  string           `json:"artifact_sha256"`
	Downloadable    bool             `json:"downloadable"`
	Approvals       []ReportApproval `json:"approvals"`
}

type ReportApproval struct {
	ID          string    `json:"id"`
	Revision    int64     `json:"report_revision"`
	ReviewerID  *string   `json:"reviewer_id,omitempty"`
	Decision    string    `json:"decision"`
	ContentHash string    `json:"content_hash"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
}

type ReportCommandRequest struct {
	Reason     string `json:"reason"`
	Note       string `json:"note"`
	ReviewerID string `json:"reviewer_id"`
}

type ReportTicket struct {
	TicketID         string `json:"ticket_id"`
	URL              string `json:"url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
	ContentHash      string `json:"content_hash"`
	SizeBytes        int64  `json:"size_bytes"`
	ETag             string `json:"etag"`
}

func (s *Service) ListReports(ctx context.Context, actor Actor, caseID, investigationID, status string, limit int) ([]ReportView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	p := actor.Principal
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	args := []any{p.OrganizationID, p.WorkspaceID}
	where := []string{"r.organization_id=$1::uuid", "r.workspace_id=$2::uuid"}
	if caseID != "" {
		args = append(args, caseID)
		where = append(where, fmt.Sprintf("r.case_id=$%d::uuid", len(args)))
	}
	if investigationID != "" {
		args = append(args, investigationID)
		where = append(where, fmt.Sprintf("r.investigation_id=$%d::uuid", len(args)))
	}
	if status != "" && status != "all" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("r.status=$%d", len(args)))
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
        SELECT r.id::text,r.investigation_id::text,r.case_id::text,coalesce(c.case_no,''),r.revision,r.format,r.content_hash,r.size_bytes,
               r.status,r.status_revision,r.metadata,r.generated_at,r.submitted_at,r.submitted_by::text,r.approved_at,r.approved_by::text,
               r.rejected_at,r.reject_reason,r.signed_at,r.signature_hash,r.archived_at
        FROM investigation_reports r LEFT JOIN cases c ON c.id=r.case_id
        WHERE %s ORDER BY r.generated_at DESC,r.id DESC LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportView{}
	for rows.Next() {
		v, err := scanReportRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) GetReport(ctx context.Context, actor Actor, reportID string) (ReportView, error) {
	if err := s.ready(); err != nil {
		return ReportView{}, err
	}
	p := actor.Principal
	row := s.pool.QueryRow(ctx, `
        SELECT r.id::text,r.investigation_id::text,r.case_id::text,coalesce(c.case_no,''),r.revision,r.format,r.content_hash,r.size_bytes,
               r.status,r.status_revision,r.metadata,r.generated_at,r.submitted_at,r.submitted_by::text,r.approved_at,r.approved_by::text,
               r.rejected_at,r.reject_reason,r.signed_at,r.signature_hash,r.archived_at
        FROM investigation_reports r LEFT JOIN cases c ON c.id=r.case_id
        WHERE r.id=$1::uuid AND r.organization_id=$2::uuid AND r.workspace_id=$3::uuid`, reportID, p.OrganizationID, p.WorkspaceID)
	v, err := scanReportRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReportView{}, notFound()
	}
	if err != nil {
		return ReportView{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,report_revision,reviewer_id::text,decision,content_hash,note,created_at
        FROM report_approvals WHERE report_id=$1::uuid ORDER BY created_at`, reportID)
	if err != nil {
		return ReportView{}, err
	}
	defer rows.Close()
	v.Approvals = []ReportApproval{}
	for rows.Next() {
		var a ReportApproval
		if err := rows.Scan(&a.ID, &a.Revision, &a.ReviewerID, &a.Decision, &a.ContentHash, &a.Note, &a.CreatedAt); err != nil {
			return ReportView{}, err
		}
		v.Approvals = append(v.Approvals, a)
	}
	return v, rows.Err()
}

type reportScanner interface{ Scan(...any) error }

func scanReportRow(row reportScanner) (ReportView, error) {
	var v ReportView
	var meta []byte
	if err := row.Scan(&v.ID, &v.InvestigationID, &v.CaseID, &v.CaseNo, &v.Revision, &v.Format, &v.ContentHash, &v.SizeBytes,
		&v.Status, &v.StatusRevision, &meta, &v.GeneratedAt, &v.SubmittedAt, &v.SubmittedBy, &v.ApprovedAt, &v.ApprovedBy,
		&v.RejectedAt, &v.RejectReason, &v.SignedAt, &v.SignatureHash, &v.ArchivedAt); err != nil {
		return ReportView{}, err
	}
	_ = jsonUnmarshal(meta, &v.Metadata)
	if v.Metadata == nil {
		v.Metadata = map[string]any{}
	}
	v.ArtifactSHA256 = v.ContentHash
	v.Downloadable = oneOf(v.Status, "approved", "signed", "archived")
	return v, nil
}

// GenerateReport 为案件下某个 investigation 生成新的不可变 revision。
func (s *Service) GenerateReport(ctx context.Context, actor Actor, caseID, investigationID string) (ReportView, error) {
	if err := s.ready(); err != nil {
		return ReportView{}, err
	}
	p := actor.Principal
	if s.reports == nil {
		return ReportView{}, apiErr(http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "报告服务不可用", nil)
	}
	var gotCase string
	var status string
	if err := s.pool.QueryRow(ctx, `
        SELECT coalesce(case_id::text,''),status FROM investigations
        WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid`, investigationID, p.OrganizationID, p.WorkspaceID).Scan(&gotCase, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReportView{}, notFound()
		}
		return ReportView{}, err
	}
	if caseID != "" && gotCase != caseID {
		return ReportView{}, validation("调查不属于该案件", map[string]any{"field": "case_id"})
	}
	var findingCount int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM investigation_findings WHERE investigation_id=$1::uuid AND status IN ('confirmed','candidate')`, investigationID).Scan(&findingCount)
	if findingCount == 0 {
		return ReportView{}, conflict("没有已确认或候选 Finding，不能生成报告", map[string]any{"rule": "report_requires_findings"})
	}
	report, err := s.reports.GenerateFor(ctx, investigationID, p.IdentityID)
	if err != nil {
		return ReportView{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReportView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if gotCase != "" {
		if err := s.appendActivityTx(ctx, tx, actor, gotCase, "report.generated", fmt.Sprintf("报告 revision %d 已生成", report.Revision), "report", report.ID, map[string]any{"content_hash": report.ContentHash}); err != nil {
			return ReportView{}, err
		}
	}
	if err := s.auditTx(ctx, tx, actor, "report:create", "report", "report", report.ID, "success", map[string]any{
		"investigation_id": investigationID, "revision": report.Revision, "content_hash": report.ContentHash, "size_bytes": report.SizeBytes,
	}); err != nil {
		return ReportView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReportView{}, err
	}
	return s.GetReport(ctx, actor, report.ID)
}

func (s *Service) CommandReport(ctx context.Context, actor Actor, reportID, command string, expectedStatusRevision int64, req ReportCommandRequest) (ReportView, error) {
	if err := s.ready(); err != nil {
		return ReportView{}, err
	}
	if expectedStatusRevision <= 0 {
		return ReportView{}, apiErr(http.StatusPreconditionRequired, "CONFLICT_REVISION_REQUIRED", "报告命令必须携带 If-Match 版本", nil)
	}
	p := actor.Principal
	if command == "approve" || command == "reject" {
		if !p.Has("report:review") && !p.Has("report:approve") {
			return ReportView{}, forbidden("需要 report:review 或 report:approve 权限", nil)
		}
	}
	if command == "sign" && !p.Has("report:sign") {
		return ReportView{}, forbidden("需要 report:sign 权限", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReportView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, contentHash, submittedBy, caseID, investigationID string
	var revision, statusRevision int64
	err = tx.QueryRow(ctx, `
        SELECT status,revision,status_revision,content_hash,coalesce(submitted_by::text,''),coalesce(case_id::text,''),investigation_id::text
        FROM investigation_reports WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		reportID, p.OrganizationID, p.WorkspaceID).Scan(&status, &revision, &statusRevision, &contentHash, &submittedBy, &caseID, &investigationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReportView{}, notFound()
	}
	if err != nil {
		return ReportView{}, err
	}
	if statusRevision != expectedStatusRevision {
		return ReportView{}, revisionConflict()
	}
	note := strings.TrimSpace(firstNonEmpty(req.Note, req.Reason))
	switch command {
	case "submit":
		if !oneOf(status, "generated", "rejected") {
			return ReportView{}, conflict("只有 generated/rejected 状态的报告可以提交复核", map[string]any{"status": status})
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation_reports SET status='in_review',submitted_at=now(),submitted_by=NULLIF($2,'')::uuid,
            status_revision=status_revision+1 WHERE id=$1::uuid`, reportID, p.IdentityID); err != nil {
			return ReportView{}, err
		}
		reviewer := strings.TrimSpace(req.ReviewerID)
		reviewID, createdAt, err := s.insertReportReviewTx(ctx, tx, actor, reviewIDParams{
			caseID: caseID, investigationID: investigationID, reportID: reportID, reviewerID: reviewer, note: note,
		})
		if err != nil {
			return ReportView{}, err
		}
		_ = createdAt
		if reviewer != "" {
			if err := s.notifyTx(ctx, tx, reviewer, p.OrganizationID, p.WorkspaceID, "review.requested",
				"notification.report.review", "有一份报告待你审批", note, "report", reportID, map[string]any{"review_id": reviewID}); err != nil {
				return ReportView{}, err
			}
		} else {
			recipients, err := s.reviewersWithPermissionTx(ctx, tx, p.OrganizationID, "report:review")
			if err != nil {
				return ReportView{}, err
			}
			for _, recipient := range recipients {
				if err := s.notifyTx(ctx, tx, recipient, p.OrganizationID, p.WorkspaceID, "review.requested",
					"notification.report.review", "有一份报告待你审批", note, "report", reportID, map[string]any{"review_id": reviewID}); err != nil {
					return ReportView{}, err
				}
			}
		}
	case "approve":
		if status != "in_review" {
			return ReportView{}, conflict("只有 in_review 状态的报告可以审批", map[string]any{"status": status})
		}
		if submittedBy != "" && submittedBy == p.IdentityID {
			return ReportView{}, forbidden("报告提交人不能审批自己的报告（职责分离）", map[string]any{"rule": "separation_of_duty"})
		}
		if s.reports == nil {
			return ReportView{}, apiErr(http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "报告服务不可用", nil)
		}
		verified, err := s.reports.Verify(ctx, reportID)
		if err != nil {
			return ReportView{}, conflict("报告 artifact 校验失败，不能审批", map[string]any{"rule": "artifact_hash_mismatch"})
		}
		if verified.ContentHash != contentHash {
			return ReportView{}, conflict("报告 artifact 与数据库哈希不一致", map[string]any{"rule": "artifact_hash_mismatch"})
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation_reports SET status='approved',approved_at=now(),approved_by=NULLIF($2,'')::uuid,
            status_revision=status_revision+1,reject_reason='' WHERE id=$1::uuid`, reportID, p.IdentityID); err != nil {
			return ReportView{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO report_approvals (report_id,report_revision,reviewer_id,decision,content_hash,note)
            VALUES ($1::uuid,$2,$3::uuid,'approve',$4,$5)`, reportID, revision, p.IdentityID, contentHash, note); err != nil {
			return ReportView{}, err
		}
		if err := s.closeReviewRequestsTx(ctx, tx, "report", reportID, map[string]any{"decision": "approved", "note": note}); err != nil {
			return ReportView{}, err
		}
		if submittedBy != "" {
			_ = s.notifyTx(ctx, tx, submittedBy, p.OrganizationID, p.WorkspaceID, "report.approved",
				"notification.report.approved", "报告已审批通过", note, "report", reportID, map[string]any{})
		}
	case "reject":
		if status != "in_review" {
			return ReportView{}, conflict("只有 in_review 状态的报告可以驳回", map[string]any{"status": status})
		}
		if note == "" {
			return ReportView{}, validation("驳回必须填写原因", map[string]any{"field": "note"})
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation_reports SET status='rejected',rejected_at=now(),rejected_by=NULLIF($2,'')::uuid,
            reject_reason=$3,status_revision=status_revision+1 WHERE id=$1::uuid`, reportID, p.IdentityID, note); err != nil {
			return ReportView{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO report_approvals (report_id,report_revision,reviewer_id,decision,content_hash,note)
            VALUES ($1::uuid,$2,$3::uuid,'reject',$4,$5)`, reportID, revision, p.IdentityID, contentHash, note); err != nil {
			return ReportView{}, err
		}
		if err := s.closeReviewRequestsTx(ctx, tx, "report", reportID, map[string]any{"decision": "rejected", "note": note}); err != nil {
			return ReportView{}, err
		}
		if submittedBy != "" {
			_ = s.notifyTx(ctx, tx, submittedBy, p.OrganizationID, p.WorkspaceID, "report.rejected",
				"notification.report.rejected", "报告被驳回", note, "report", reportID, map[string]any{})
		}
	case "sign":
		if status != "approved" {
			return ReportView{}, conflict("只有 approved 状态的报告可以签署", map[string]any{"status": status})
		}
		sum := sha256.Sum256([]byte("v1|" + contentHash + "|" + p.IdentityID + "|" + nowUTC().Format(time.RFC3339)))
		signature := hex.EncodeToString(sum[:])
		if _, err := tx.Exec(ctx, `UPDATE investigation_reports SET status='signed',signed_at=now(),signed_by=NULLIF($2,'')::uuid,
            signature_hash=$3,status_revision=status_revision+1 WHERE id=$1::uuid`, reportID, p.IdentityID, signature); err != nil {
			return ReportView{}, err
		}
	case "archive":
		if !oneOf(status, "approved", "signed") {
			return ReportView{}, conflict("只有 approved/signed 状态的报告可以归档", map[string]any{"status": status})
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation_reports SET status='archived',archived_at=now(),status_revision=status_revision+1 WHERE id=$1::uuid`, reportID); err != nil {
			return ReportView{}, err
		}
	default:
		return ReportView{}, notFound()
	}
	if caseID != "" {
		if err := s.appendActivityTx(ctx, tx, actor, caseID, "report."+command, "报告操作："+command, "report", reportID, map[string]any{"note": note}); err != nil {
			return ReportView{}, err
		}
	}
	if err := s.auditTx(ctx, tx, actor, "report:"+command, "report", "report", reportID, "success", map[string]any{
		"from": status, "command": command, "note": note, "report_revision": revision,
	}); err != nil {
		return ReportView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReportView{}, err
	}
	return s.GetReport(ctx, actor, reportID)
}

type reviewIDParams struct {
	caseID, investigationID, reportID, reviewerID, note string
}

func (s *Service) insertReportReviewTx(ctx context.Context, tx pgx.Tx, actor Actor, p reviewIDParams) (string, time.Time, error) {
	var id string
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
        INSERT INTO review_requests (organization_id,workspace_id,case_id,investigation_id,resource_type,resource_id,requested_by,reviewer_id,status,reason)
        VALUES ($1::uuid,$2::uuid,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,'report',$5::uuid,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,'pending',$8)
        RETURNING id::text,created_at`,
		actor.Principal.OrganizationID, actor.Principal.WorkspaceID, p.caseID, p.investigationID, p.reportID,
		actor.Principal.IdentityID, p.reviewerID, p.note).Scan(&id, &createdAt)
	return id, createdAt, err
}

// ---- 下载票据 -------------------------------------------------------------

func (s *Service) CreateReportTicket(ctx context.Context, actor Actor, reportID string) (ReportTicket, error) {
	if err := s.ready(); err != nil {
		return ReportTicket{}, err
	}
	if s.reports == nil {
		return ReportTicket{}, apiErr(http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "报告服务不可用", nil)
	}
	p := actor.Principal
	report, err := s.GetReport(ctx, actor, reportID)
	if err != nil {
		return ReportTicket{}, err
	}
	if !oneOf(report.Status, "approved", "signed", "archived") {
		return ReportTicket{}, conflict("只有 approved/signed/archived 报告可以下载", map[string]any{"status": report.Status})
	}
	verified, err := s.reports.Verify(ctx, reportID)
	if err != nil {
		return ReportTicket{}, conflict("报告 artifact 校验失败", map[string]any{"rule": "artifact_hash_mismatch"})
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return ReportTicket{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReportTicket{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
        INSERT INTO report_download_tickets (report_id,token_hash,user_id,organization_id,workspace_id,expires_at)
        VALUES ($1::uuid,$2,NULLIF($3,'')::uuid,$4::uuid,$5::uuid,now()+interval '60 seconds')`,
		reportID, hash, p.IdentityID, p.OrganizationID, p.WorkspaceID); err != nil {
		return ReportTicket{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "report:download", "report", "report", reportID, "success", map[string]any{
		"ticket_hash_prefix": hash[:12], "expires_in_seconds": 60, "content_hash": verified.ContentHash,
	}); err != nil {
		return ReportTicket{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReportTicket{}, err
	}
	return ReportTicket{
		TicketID: token, URL: "/api/v2/reports/download/" + token, ExpiresInSeconds: 60,
		ContentHash: verified.ContentHash, SizeBytes: verified.SizeBytes, ETag: `"` + verified.ContentHash + `"`,
	}, nil
}

// ConsumeReportTicket 一次性消费票据；过期/重复使用/越权都写审计。
func (s *Service) ConsumeReportTicket(ctx context.Context, actor Actor, token string) ([]byte, ReportView, error) {
	if err := s.ready(); err != nil {
		return nil, ReportView{}, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, ReportView{}, notFound()
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	p := actor.Principal
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, ReportView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var reportID, userID string
	err = tx.QueryRow(ctx, `
        UPDATE report_download_tickets SET used_at=now()
        WHERE token_hash=$1 AND used_at IS NULL AND expires_at > now()
        RETURNING report_id::text,coalesce(user_id::text,'')`, hash).Scan(&reportID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingStatus string
		_ = tx.QueryRow(ctx, `SELECT CASE WHEN used_at IS NOT NULL THEN 'used' ELSE 'expired' END FROM report_download_tickets WHERE token_hash=$1`, hash).Scan(&existingStatus)
		_ = tx.Rollback(ctx)
		_ = s.writeAuditAsync(ctx, actor, "report:download", "report", "report_ticket", hash[:12], "high", map[string]any{"result": "failure", "reason": existingStatus})
		return nil, ReportView{}, apiErr(http.StatusGone, "RESOURCE_GONE", "下载票据已过期或已使用", map[string]any{"reason": existingStatus})
	}
	if err != nil {
		return nil, ReportView{}, err
	}
	if userID != "" && userID != p.IdentityID {
		_ = tx.Rollback(ctx)
		_ = s.writeAuditAsync(ctx, actor, "report:download", "report", "report_ticket", hash[:12], "high", map[string]any{"result": "deny", "reason": "ticket_owner_mismatch"})
		return nil, ReportView{}, forbidden("下载票据不属于当前身份", nil)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, ReportView{}, err
	}
	view, err := s.GetReport(ctx, actor, reportID)
	if err != nil {
		_ = s.writeAuditAsync(ctx, actor, "report:download", "report", "report", reportID, "high", map[string]any{"result": "failure", "reason": "report_unavailable"})
		return nil, ReportView{}, err
	}
	if !view.Downloadable {
		_ = s.writeAuditAsync(ctx, actor, "report:download", "report", "report", reportID, "high", map[string]any{"result": "deny", "reason": "status_" + view.Status})
		return nil, ReportView{}, conflict("报告当前状态不可下载", map[string]any{"status": view.Status})
	}
	body, report, err := s.reports.ReadAll(ctx, reportID)
	if err != nil {
		_ = s.writeAuditAsync(ctx, actor, "report:download", "report", "report", reportID, "high", map[string]any{"result": "failure", "reason": "artifact_unavailable"})
		return nil, ReportView{}, err
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != report.ContentHash {
		_ = s.writeAuditAsync(ctx, actor, "report:download", "report", "report", reportID, "high", map[string]any{"result": "failure", "reason": "hash_mismatch"})
		return nil, ReportView{}, conflict("下载内容哈希校验失败", map[string]any{"rule": "artifact_hash_mismatch"})
	}
	_ = s.writeAuditAsync(ctx, actor, "report:download", "report", "report", reportID, "high", map[string]any{
		"result": "success", "ticket_hash_prefix": hash[:12], "content_hash": report.ContentHash, "size_bytes": len(body),
	})
	return body, view, nil
}
