package casework

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
)

// InvestigationView 是 v2 列表/详情视图，包含案件、对象、revision 与复核状态。
type InvestigationView struct {
	ID              string         `json:"id"`
	OrganizationID  string         `json:"organization_id"`
	WorkspaceID     string         `json:"workspace_id"`
	CaseID          *string        `json:"case_id,omitempty"`
	CaseNo          string         `json:"case_no,omitempty"`
	Goal            string         `json:"goal"`
	Mode            string         `json:"mode"`
	Status          string         `json:"status"`
	Subject         map[string]any `json:"subject"`
	Revision        int64          `json:"revision"`
	ActiveRunID     *string        `json:"active_run_id,omitempty"`
	FindingCount    int            `json:"finding_count"`
	EvidenceCount   int            `json:"evidence_count"`
	PendingFindings int            `json:"pending_findings"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func (s *Service) ListInvestigationsV2(ctx context.Context, actor Actor, status, caseID, cursor string, limit int) ([]InvestigationView, *string, error) {
	if err := s.ready(); err != nil {
		return nil, nil, err
	}
	p := actor.Principal
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{p.OrganizationID, p.WorkspaceID}
	where := []string{"i.organization_id=$1::uuid", "i.workspace_id=$2::uuid"}
	if status != "" && status != "all" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("i.status=$%d", len(args)))
	}
	if caseID != "" {
		args = append(args, caseID)
		where = append(where, fmt.Sprintf("i.case_id=$%d::uuid", len(args)))
	}
	if cursor != "" {
		t, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, nil, validation("分页游标无效", nil)
		}
		args = append(args, t, id)
		where = append(where, fmt.Sprintf("(i.updated_at,i.id) < ($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
        SELECT i.id::text,i.organization_id::text,i.workspace_id::text,i.case_id::text,coalesce(c.case_no,''),i.goal,i.mode,i.status,
               i.subject,i.revision,i.active_run_id::text,i.created_at,i.updated_at,
               (SELECT count(*) FROM investigation_findings f WHERE f.investigation_id=i.id),
               (SELECT count(*) FROM investigation_evidence e WHERE e.investigation_id=i.id),
               (SELECT count(*) FROM investigation_findings f WHERE f.investigation_id=i.id AND f.status IN ('candidate','in_review'))
        FROM investigations i LEFT JOIN cases c ON c.id=i.case_id
        WHERE %s ORDER BY i.updated_at DESC,i.id DESC LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []InvestigationView{}
	for rows.Next() {
		var v InvestigationView
		var subject []byte
		if err := rows.Scan(&v.ID, &v.OrganizationID, &v.WorkspaceID, &v.CaseID, &v.CaseNo, &v.Goal, &v.Mode, &v.Status,
			&subject, &v.Revision, &v.ActiveRunID, &v.CreatedAt, &v.UpdatedAt,
			&v.FindingCount, &v.EvidenceCount, &v.PendingFindings); err != nil {
			return nil, nil, err
		}
		_ = jsonUnmarshal(subject, &v.Subject)
		if v.Subject == nil {
			v.Subject = map[string]any{}
		}
		items = append(items, v)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := encodeCursor(items[len(items)-1].UpdatedAt, items[len(items)-1].ID)
		next = &c
	}
	return items, next, rows.Err()
}

func (s *Service) GetInvestigationV2(ctx context.Context, actor Actor, investigationID string) (InvestigationView, error) {
	if err := s.ready(); err != nil {
		return InvestigationView{}, err
	}
	p := actor.Principal
	var v InvestigationView
	var subject []byte
	err := s.pool.QueryRow(ctx, `
        SELECT i.id::text,i.organization_id::text,i.workspace_id::text,i.case_id::text,coalesce(c.case_no,''),i.goal,i.mode,i.status,
               i.subject,i.revision,i.active_run_id::text,i.created_at,i.updated_at,
               (SELECT count(*) FROM investigation_findings f WHERE f.investigation_id=i.id),
               (SELECT count(*) FROM investigation_evidence e WHERE e.investigation_id=i.id),
               (SELECT count(*) FROM investigation_findings f WHERE f.investigation_id=i.id AND f.status IN ('candidate','in_review'))
        FROM investigations i LEFT JOIN cases c ON c.id=i.case_id
        WHERE i.id=$1::uuid AND i.organization_id=$2::uuid`, investigationID, p.OrganizationID).
		Scan(&v.ID, &v.OrganizationID, &v.WorkspaceID, &v.CaseID, &v.CaseNo, &v.Goal, &v.Mode, &v.Status,
			&subject, &v.Revision, &v.ActiveRunID, &v.CreatedAt, &v.UpdatedAt,
			&v.FindingCount, &v.EvidenceCount, &v.PendingFindings)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvestigationView{}, notFound()
	}
	if err != nil {
		return InvestigationView{}, err
	}
	_ = jsonUnmarshal(subject, &v.Subject)
	if v.Subject == nil {
		v.Subject = map[string]any{}
	}
	return v, nil
}

func (s *Service) InvestigationRuntime(ctx context.Context, actor Actor, investigationID string) (map[string]any, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.invSvc == nil {
		return nil, apiErr(http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "调查服务不可用", nil)
	}
	if _, err := s.GetInvestigationV2(ctx, actor, investigationID); err != nil {
		return nil, err
	}
	return s.invSvc.RuntimeView(ctx, investigationID)
}

func (s *Service) InvestigationArtifacts(ctx context.Context, actor Actor, investigationID string) (map[string]any, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.invSvc == nil {
		return nil, apiErr(http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "调查服务不可用", nil)
	}
	if _, err := s.GetInvestigationV2(ctx, actor, investigationID); err != nil {
		return nil, err
	}
	return s.invSvc.Artifacts(ctx, investigationID)
}

// StartInvestigation 显式启动调查。Sprint 4 的 compatibility service：
//   - Coverage 命中：复用本地索引执行分析（不产生 Provider 费用）；
//   - 存在缺口：交给既有 runtime.Start 创建 Ensure 任务（S5 才会做完整下载调度）；
//   - 所有路径都异步执行，先落 running 状态，客户端可立即 pause（USER-004）。
func (s *Service) StartInvestigation(ctx context.Context, actor Actor, investigationID string, expectedRevision int64) (InvestigationView, error) {
	if err := s.ready(); err != nil {
		return InvestigationView{}, err
	}
	if expectedRevision <= 0 {
		return InvestigationView{}, apiErr(http.StatusPreconditionRequired, "CONFLICT_REVISION_REQUIRED", "启动调查必须携带 If-Match 版本", nil)
	}
	p := actor.Principal
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InvestigationView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var revision int64
	var caseID string
	var reqRaw []byte
	var subjectChain, subjectValue string
	err = tx.QueryRow(ctx, `
        SELECT status,revision,coalesce(case_id::text,''),data_requirement,subject->>'chain',subject->>'value'
        FROM investigations WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		investigationID, p.OrganizationID, p.WorkspaceID).Scan(&status, &revision, &caseID, &reqRaw, &subjectChain, &subjectValue)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvestigationView{}, notFound()
	}
	if err != nil {
		return InvestigationView{}, err
	}
	if revision != expectedRevision {
		return InvestigationView{}, revisionConflict()
	}
	if !oneOf(status, "created", "failed") {
		return InvestigationView{}, conflict("只有 created/failed 状态的调查可以启动", map[string]any{"status": status})
	}
	if _, err := tx.Exec(ctx, `UPDATE investigations SET status='running',revision=revision+1,updated_at=now() WHERE id=$1::uuid`, investigationID); err != nil {
		return InvestigationView{}, err
	}
	if caseID != "" {
		if err := s.appendActivityTx(ctx, tx, actor, caseID, "investigation.started", "调查已启动", "investigation", investigationID, nil); err != nil {
			return InvestigationView{}, err
		}
	}
	if err := s.auditTx(ctx, tx, actor, "investigation:start", "investigation", "investigation", investigationID, "success", map[string]any{"expected_revision": expectedRevision}); err != nil {
		return InvestigationView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InvestigationView{}, err
	}
	s.scheduleAnalysis(investigationID, subjectChain, subjectValue, reqRaw, actor.Principal.IdentityID)
	return s.GetInvestigationV2(ctx, actor, investigationID)
}

// scheduleAnalysis 把兼容分析放入后台队列。1.5s 的 admission delay 是队列语义：
// 给客户端一个确定的 pause 窗口，而不是让 HTTP 请求同步等待分析完成。
func (s *Service) scheduleAnalysis(investigationID, chain, value string, reqRaw []byte, actorIdentity string) {
	if s.runtime == nil {
		return
	}
	go func() {
		time.Sleep(1500 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var status string
		if err := s.pool.QueryRow(ctx, `SELECT status FROM investigations WHERE id=$1::uuid`, investigationID).Scan(&status); err != nil || status != "running" {
			return
		}
		// Coverage 命中的本地索引复用：AnalyzeExisting 不触达 Provider（S4 compatibility）。
		if s.preview != nil {
			req := dataset.EnsureRequest{Chain: chain, Subjects: []string{value}, Datasets: []string{"token_transfers"}, ReusePolicy: "prefer_local"}
			var dataReq map[string]any
			if len(reqRaw) > 2 {
				_ = jsonUnmarshal(reqRaw, &dataReq)
				if ds, ok := dataReq["datasets"].([]any); ok && len(ds) > 0 {
					values := make([]string, 0, len(ds))
					for _, v := range ds {
						values = append(values, fmt.Sprint(v))
					}
					req.Datasets = values
				}
				if rng, ok := dataReq["range"].(map[string]any); ok {
					req.Range = dataset.RangeSpecFromMap(rng)
				}
			}
			if preview, err := s.preview(ctx, req); err == nil && preview.OverallRatio >= 1 {
				_, err = s.runtime.AnalyzeExisting(ctx, investigationID)
				if err != nil {
					s.markRuntimeFailed(investigationID, err.Error())
				}
				return
			}
		}
		if _, err := s.runtime.Start(ctx, investigationID, actorIdentity); err != nil {
			s.markRuntimeFailed(investigationID, err.Error())
		}
	}()
}

func (s *Service) markRuntimeFailed(investigationID, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.pool.Exec(ctx, `UPDATE investigations SET status='failed',state=jsonb_set(state,'{runtime_error}',to_jsonb($2::text),true),revision=revision+1,updated_at=now()
        WHERE id=$1::uuid AND status IN ('running','waiting_data')`, investigationID, message)
}

// PauseInvestigation 冻结调查状态；后台 runner 会因 status != running 而退出。
func (s *Service) PauseInvestigation(ctx context.Context, actor Actor, investigationID string, expectedRevision int64, note string) (InvestigationView, error) {
	return s.simpleInvestigationCommand(ctx, actor, investigationID, expectedRevision, "pause", note)
}

// ResumeInvestigation 恢复被暂停的调查；若发现结果已落库则直接进入待复核，避免重复分析。
func (s *Service) ResumeInvestigation(ctx context.Context, actor Actor, investigationID string, expectedRevision int64, note string) (InvestigationView, error) {
	v, err := s.simpleInvestigationCommand(ctx, actor, investigationID, expectedRevision, "resume", note)
	if err != nil {
		return InvestigationView{}, err
	}
	// 已经在暂停前完成分析的情况：不重复运行。
	if v.FindingCount > 0 {
		ctx2, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(ctx2, `UPDATE investigations SET status='waiting_review',revision=revision+1,updated_at=now()
            WHERE id=$1::uuid AND status='running'`, investigationID)
		return s.GetInvestigationV2(ctx, actor, investigationID)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InvestigationView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var chain, value string
	var reqRaw []byte
	if err := tx.QueryRow(ctx, `SELECT subject->>'chain',subject->>'value',data_requirement FROM investigations WHERE id=$1::uuid`, investigationID).Scan(&chain, &value, &reqRaw); err != nil {
		return InvestigationView{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "investigation:resume", "investigation", "investigation", investigationID, "success", map[string]any{"note": note}); err != nil {
		return InvestigationView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InvestigationView{}, err
	}
	s.scheduleAnalysis(investigationID, chain, value, reqRaw, actor.Principal.IdentityID)
	return s.GetInvestigationV2(ctx, actor, investigationID)
}

func (s *Service) simpleInvestigationCommand(ctx context.Context, actor Actor, investigationID string, expectedRevision int64, command, note string) (InvestigationView, error) {
	if err := s.ready(); err != nil {
		return InvestigationView{}, err
	}
	if expectedRevision <= 0 {
		return InvestigationView{}, apiErr(http.StatusPreconditionRequired, "CONFLICT_REVISION_REQUIRED", "调查命令必须携带 If-Match 版本", nil)
	}
	p := actor.Principal
	var to string
	var allowed []string
	switch command {
	case "pause":
		to, allowed = "paused", []string{"running", "waiting_data"}
	case "resume":
		to, allowed = "running", []string{"paused"}
	case "cancel":
		to, allowed = "cancelled", []string{"created", "running", "waiting_data", "waiting_review", "paused", "failed"}
	case "retry":
		to, allowed = "running", []string{"failed"}
	default:
		return InvestigationView{}, notFound()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InvestigationView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current string
	var revision int64
	var caseID string
	err = tx.QueryRow(ctx, `SELECT status,revision,coalesce(case_id::text,'') FROM investigations WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		investigationID, p.OrganizationID, p.WorkspaceID).Scan(&current, &revision, &caseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvestigationView{}, notFound()
	}
	if err != nil {
		return InvestigationView{}, err
	}
	if revision != expectedRevision {
		return InvestigationView{}, revisionConflict()
	}
	if !oneOf(current, allowed...) {
		return InvestigationView{}, conflict("当前状态不允许 "+command, map[string]any{"status": current, "command": command})
	}
	if _, err := tx.Exec(ctx, `UPDATE investigations SET status=$2,revision=revision+1,updated_at=now() WHERE id=$1::uuid`, investigationID, to); err != nil {
		return InvestigationView{}, err
	}
	if command == "pause" || command == "cancel" {
		if _, err := tx.Exec(ctx, `UPDATE investigation_runs SET status='paused',updated_at=now() WHERE investigation_id=$1::uuid AND status IN ('pending','running','waiting')`, investigationID); err != nil {
			return InvestigationView{}, err
		}
	}
	if caseID != "" {
		if err := s.appendActivityTx(ctx, tx, actor, caseID, "investigation."+to, "调查状态："+current+" → "+to, "investigation", investigationID, map[string]any{"note": note}); err != nil {
			return InvestigationView{}, err
		}
	}
	action := "investigation:" + command
	if err := s.auditTx(ctx, tx, actor, action, "investigation", "investigation", investigationID, "success", map[string]any{
		"from": current, "to": to, "note": note,
	}); err != nil {
		return InvestigationView{}, err
	}
	if to == "cancelled" || to == "paused" {
		if err := s.closeReviewRequestsTx(ctx, tx, "investigation", investigationID, map[string]any{"decision": "cancelled"}); err != nil {
			return InvestigationView{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return InvestigationView{}, err
	}
	return s.GetInvestigationV2(ctx, actor, investigationID)
}

func (s *Service) CancelInvestigation(ctx context.Context, actor Actor, investigationID string, expectedRevision int64, reason string) (InvestigationView, error) {
	return s.simpleInvestigationCommand(ctx, actor, investigationID, expectedRevision, "cancel", reason)
}

func (s *Service) RetryInvestigation(ctx context.Context, actor Actor, investigationID string, expectedRevision int64, reason string) (InvestigationView, error) {
	v, err := s.simpleInvestigationCommand(ctx, actor, investigationID, expectedRevision, "retry", reason)
	if err != nil {
		return InvestigationView{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InvestigationView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var chain, value string
	var reqRaw []byte
	if err := tx.QueryRow(ctx, `SELECT subject->>'chain',subject->>'value',data_requirement FROM investigations WHERE id=$1::uuid`, investigationID).Scan(&chain, &value, &reqRaw); err != nil {
		return InvestigationView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InvestigationView{}, err
	}
	s.scheduleAnalysis(investigationID, chain, value, reqRaw, actor.Principal.IdentityID)
	return v, nil
}

// SubmitInvestigationReview 把调查提交给复核人，并创建可追踪的 review_request + 通知。
func (s *Service) SubmitInvestigationReview(ctx context.Context, actor Actor, investigationID string, expectedRevision int64, reviewerID, note string) (InvestigationView, error) {
	if err := s.ready(); err != nil {
		return InvestigationView{}, err
	}
	if expectedRevision <= 0 {
		return InvestigationView{}, apiErr(http.StatusPreconditionRequired, "CONFLICT_REVISION_REQUIRED", "提交复核必须携带 If-Match 版本", nil)
	}
	p := actor.Principal
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InvestigationView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var revision int64
	var caseID string
	err = tx.QueryRow(ctx, `SELECT status,revision,coalesce(case_id::text,'') FROM investigations WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		investigationID, p.OrganizationID, p.WorkspaceID).Scan(&status, &revision, &caseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvestigationView{}, notFound()
	}
	if err != nil {
		return InvestigationView{}, err
	}
	if revision != expectedRevision {
		return InvestigationView{}, revisionConflict()
	}
	if !oneOf(status, "running", "waiting_data", "waiting_review", "paused") {
		return InvestigationView{}, conflict("当前状态不允许提交复核", map[string]any{"status": status})
	}
	if _, err := tx.Exec(ctx, `UPDATE investigations SET status='waiting_review',revision=revision+1,updated_at=now() WHERE id=$1::uuid`, investigationID); err != nil {
		return InvestigationView{}, err
	}
	reviewer := strings.TrimSpace(reviewerID)
	title := "调查待复核"
	if note != "" {
		title = note
	}
	if reviewer != "" {
		var org string
		if err := tx.QueryRow(ctx, `SELECT organization_id::text FROM identities WHERE id=$1::uuid`, reviewer).Scan(&org); err != nil || org != p.OrganizationID {
			return InvestigationView{}, validation("复核人不存在或不属于当前组织", map[string]any{"field": "reviewer_id"})
		}
	}
	var reviewID string
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
        INSERT INTO review_requests (organization_id,workspace_id,case_id,investigation_id,resource_type,resource_id,requested_by,reviewer_id,status,reason)
        VALUES ($1::uuid,$2::uuid,NULLIF($3,'')::uuid,$4::uuid,'investigation',$4::uuid,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,'pending',$7)
        RETURNING id::text,created_at`, p.OrganizationID, p.WorkspaceID, caseID, investigationID, p.IdentityID, reviewer, note).Scan(&reviewID, &createdAt); err != nil {
		return InvestigationView{}, err
	}
	if reviewer != "" {
		if err := s.notifyTx(ctx, tx, reviewer, p.OrganizationID, p.WorkspaceID, "review.requested",
			"notification.investigation.review", title, note, "investigation", investigationID, map[string]any{"review_id": reviewID}); err != nil {
			return InvestigationView{}, err
		}
	} else {
		recipients, err := s.reviewersWithPermissionTx(ctx, tx, p.OrganizationID, "finding:review")
		if err != nil {
			return InvestigationView{}, err
		}
		for _, recipient := range recipients {
			if err := s.notifyTx(ctx, tx, recipient, p.OrganizationID, p.WorkspaceID, "review.requested",
				"notification.investigation.review", title, note, "investigation", investigationID, map[string]any{"review_id": reviewID}); err != nil {
				return InvestigationView{}, err
			}
		}
	}
	if caseID != "" {
		if err := s.appendActivityTx(ctx, tx, actor, caseID, "investigation.submitted_review", "调查已提交复核", "investigation", investigationID, map[string]any{"reviewer_id": reviewer}); err != nil {
			return InvestigationView{}, err
		}
	}
	if err := s.auditTx(ctx, tx, actor, "investigation:submit_review", "investigation", "investigation", investigationID, "success", map[string]any{"reviewer_id": reviewer, "note": note}); err != nil {
		return InvestigationView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InvestigationView{}, err
	}
	return s.GetInvestigationV2(ctx, actor, investigationID)
}
