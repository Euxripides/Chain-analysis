package casework

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
)

// Case 是用户层案件视图。
type Case struct {
	ID                 string     `json:"id"`
	OrganizationID     string     `json:"organization_id"`
	WorkspaceID        string     `json:"workspace_id"`
	CaseNo             string     `json:"case_no"`
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	Status             string     `json:"status"`
	Color              string     `json:"color"`
	Classification     string     `json:"classification"`
	Priority           string     `json:"priority"`
	Revision           int64      `json:"revision"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ArchivedAt         *time.Time `json:"archived_at,omitempty"`
	ClosedAt           *time.Time `json:"closed_at,omitempty"`
	LeadCount          int        `json:"lead_count,omitempty"`
	MemberCount        int        `json:"member_count,omitempty"`
	SubjectCount       int        `json:"subject_count,omitempty"`
	OpenFindings       int        `json:"open_findings,omitempty"`
	OpenReports        int        `json:"open_reports,omitempty"`
	OpenInvestigations int        `json:"open_investigations,omitempty"`
}

type CreateCaseRequest struct {
	CaseNo         string   `json:"case_no"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Classification string   `json:"classification"`
	Priority       string   `json:"priority"`
	Color          string   `json:"color"`
	Status         string   `json:"status"`
	MemberIDs      []string `json:"member_ids"`
	TagIDs         []string `json:"tag_ids"`
}

type UpdateCaseRequest struct {
	Name           *string `json:"name"`
	Description    *string `json:"description"`
	Classification *string `json:"classification"`
	Priority       *string `json:"priority"`
	Color          *string `json:"color"`
}

type CaseCommandRequest struct {
	Reason          string `json:"reason"`
	ExceptionReason string `json:"exception_reason"`
}

type CaseMember struct {
	ID          string    `json:"id"`
	IdentityID  string    `json:"identity_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	AddedBy     *string   `json:"added_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Status      string    `json:"status"`
}

type CaseSubject struct {
	ID              string         `json:"id"`
	CaseID          string         `json:"case_id"`
	SubjectType     string         `json:"subject_type"`
	Chain           *string        `json:"chain,omitempty"`
	Value           string         `json:"value"`
	NormalizedValue string         `json:"normalized_value"`
	Label           string         `json:"label"`
	Properties      map[string]any `json:"properties"`
	Revision        int64          `json:"revision"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type ActivityItem struct {
	ID           string         `json:"id"`
	EventType    string         `json:"event_type"`
	ActorDisplay string         `json:"actor_display"`
	Summary      string         `json:"summary"`
	ObjectType   string         `json:"object_type"`
	ObjectID     string         `json:"object_id"`
	Detail       map[string]any `json:"detail"`
	TraceID      string         `json:"trace_id,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type Comment struct {
	ID        string    `json:"id"`
	CaseID    string    `json:"case_id"`
	ThreadID  *string   `json:"thread_id,omitempty"`
	ParentID  *string   `json:"parent_id,omitempty"`
	Body      string    `json:"body_markdown"`
	BodyHash  string    `json:"body_hash"`
	CreatedBy string    `json:"created_by"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---- cursor 分页 ----------------------------------------------------------

func encodeCursor(t time.Time, id string) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", errors.New("bad cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return t, parts[1], nil
}

func pageLimit(r *http.Request) int {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	return limit
}

func parseInt(v string) (int, error) {
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid integer")
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 0, errors.New("invalid integer")
		}
	}
	if v == "" {
		return 0, errors.New("invalid integer")
	}
	return n, nil
}

// ---- 案件 CRUD ------------------------------------------------------------

func (s *Service) ListCases(ctx context.Context, actor Actor, status, search, cursor string, limit int) ([]Case, *string, int, error) {
	if err := s.ready(); err != nil {
		return nil, nil, 0, err
	}
	p := actor.Principal
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{p.OrganizationID, p.WorkspaceID}
	where := []string{"organization_id = $1", "workspace_id = $2"}
	if status != "" && status != "all" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		where = append(where, fmt.Sprintf("(lower(name) LIKE $%d OR lower(coalesce(case_no,'')) LIKE $%d)", len(args), len(args)))
	}
	if cursor != "" {
		t, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, nil, 0, validation("分页游标无效", nil)
		}
		args = append(args, t, id)
		where = append(where, fmt.Sprintf("(updated_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	query := fmt.Sprintf(`
        SELECT id::text, organization_id::text, workspace_id::text, coalesce(case_no,''), name, coalesce(description,''),
               status, coalesce(color,''), classification, priority, revision, created_at, updated_at, archived_at, closed_at
        FROM cases WHERE %s ORDER BY updated_at DESC, id DESC LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, 0, err
	}
	defer rows.Close()
	items := []Case{}
	for rows.Next() {
		var c Case
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.WorkspaceID, &c.CaseNo, &c.Name, &c.Description,
			&c.Status, &c.Color, &c.Classification, &c.Priority, &c.Revision, &c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt, &c.ClosedAt); err != nil {
			return nil, nil, 0, err
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, err
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := encodeCursor(items[len(items)-1].UpdatedAt, items[len(items)-1].ID)
		next = &c
	}
	var total int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM cases WHERE organization_id=$1 AND workspace_id=$2`, p.OrganizationID, p.WorkspaceID).Scan(&total)
	return items, next, total, nil
}

func (s *Service) CreateCase(ctx context.Context, actor Actor, req CreateCaseRequest) (Case, error) {
	if err := s.ready(); err != nil {
		return Case{}, err
	}
	p := actor.Principal
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 200 {
		return Case{}, validation("案件名称长度必须在 1-200 个字符之间", map[string]any{"field": "name"})
	}
	classification := strings.TrimSpace(req.Classification)
	if classification == "" {
		classification = "internal"
	}
	if !oneOf(classification, "public", "internal", "confidential", "restricted") {
		return Case{}, validation("密级不合法", map[string]any{"field": "classification"})
	}
	priority := strings.TrimSpace(req.Priority)
	if priority == "" {
		priority = "medium"
	}
	if !oneOf(priority, "low", "medium", "high", "critical") {
		return Case{}, validation("优先级不合法", map[string]any{"field": "priority"})
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "draft"
	}
	if !oneOf(status, "draft", "active") {
		return Case{}, validation("创建时状态只能是 draft 或 active", map[string]any{"field": "status"})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Case{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	workspaceID, err := s.resolveWorkspace(ctx, tx, p)
	if err != nil {
		return Case{}, err
	}
	caseNo := strings.TrimSpace(req.CaseNo)
	if caseNo == "" {
		var seq int
		_ = tx.QueryRow(ctx, `SELECT count(*)+1 FROM cases WHERE workspace_id=$1::uuid AND created_at >= date_trunc('year', now())`, workspaceID).Scan(&seq)
		caseNo = fmt.Sprintf("CASE-%d-%04d", time.Now().Year(), seq)
	}
	var id string
	var createdAt, updatedAt time.Time
	err = tx.QueryRow(ctx, `
        INSERT INTO cases (organization_id, workspace_id, case_no, name, description, status, color, classification, priority, created_by, revision)
        VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')::uuid,1)
        RETURNING id::text, created_at, updated_at`,
		p.OrganizationID, workspaceID, caseNo, name, strings.TrimSpace(req.Description), status,
		strings.TrimSpace(req.Color), classification, priority, p.IdentityID).Scan(&id, &createdAt, &updatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "cases_workspace_id_name_key") {
			return Case{}, conflict("同一工作区内已存在同名案件", map[string]any{"field": "name"})
		}
		return Case{}, err
	}
	// 创建人自动成为 lead；其余成员按请求加入，忽略当前组织之外的 ID。
	if _, err := tx.Exec(ctx, `
        INSERT INTO case_members (case_id, identity_id, role, added_by)
        VALUES ($1::uuid,NULLIF($2,'')::uuid,'lead',NULLIF($2,'')::uuid)`, id, p.IdentityID); err != nil {
		return Case{}, err
	}
	for _, memberID := range dedupeStrings(req.MemberIDs) {
		if memberID == p.IdentityID {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `
            SELECT EXISTS(SELECT 1 FROM identities WHERE id=$1::uuid AND organization_id=$2::uuid AND status='active')`,
			memberID, p.OrganizationID).Scan(&exists); err != nil {
			return Case{}, err
		}
		if !exists {
			return Case{}, validation("成员不存在或不属于当前组织", map[string]any{"field": "member_ids", "value": memberID})
		}
		if _, err := tx.Exec(ctx, `
            INSERT INTO case_members (case_id, identity_id, role, added_by)
            VALUES ($1::uuid,$2::uuid,'investigator',NULLIF($3,'')::uuid)
            ON CONFLICT (case_id, identity_id) DO NOTHING`, id, memberID, p.IdentityID); err != nil {
			return Case{}, err
		}
		if err := s.notifyTx(ctx, tx, memberID, p.OrganizationID, workspaceID, "case.member.added",
			"notification.case.member.added", "你被加入案件", name, "case", id, map[string]any{"case_id": id}); err != nil {
			return Case{}, err
		}
	}
	if err := s.appendActivityTx(ctx, tx, actor, id, "case.created", "案件已创建："+name, "case", id, map[string]any{"status": status}); err != nil {
		return Case{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "case.create", "case", "case", id, "success", map[string]any{
		"name": name, "case_no": caseNo, "classification": classification, "priority": priority, "member_count": len(req.MemberIDs),
	}); err != nil {
		return Case{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Case{}, err
	}
	return Case{
		ID: id, OrganizationID: p.OrganizationID, WorkspaceID: workspaceID, CaseNo: caseNo, Name: name,
		Description: strings.TrimSpace(req.Description), Status: status, Color: strings.TrimSpace(req.Color),
		Classification: classification, Priority: priority, Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

func (s *Service) GetCase(ctx context.Context, actor Actor, caseID string) (Case, error) {
	if err := s.ready(); err != nil {
		return Case{}, err
	}
	p := actor.Principal
	var c Case
	err := s.pool.QueryRow(ctx, `
        SELECT id::text, organization_id::text, workspace_id::text, coalesce(case_no,''), name, coalesce(description,''),
               status, coalesce(color,''), classification, priority, revision, created_at, updated_at, archived_at, closed_at
        FROM cases WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid`,
		caseID, p.OrganizationID, p.WorkspaceID).Scan(&c.ID, &c.OrganizationID, &c.WorkspaceID, &c.CaseNo, &c.Name, &c.Description,
		&c.Status, &c.Color, &c.Classification, &c.Priority, &c.Revision, &c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt, &c.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, notFound()
	}
	if err != nil {
		return Case{}, err
	}
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM case_members WHERE case_id=$1::uuid AND role='lead'`, caseID).Scan(&c.LeadCount)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM case_members WHERE case_id=$1::uuid`, caseID).Scan(&c.MemberCount)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM case_subjects WHERE case_id=$1::uuid`, caseID).Scan(&c.SubjectCount)
	_ = s.pool.QueryRow(ctx, `
        SELECT count(*) FROM investigation_findings f JOIN investigations i ON i.id=f.investigation_id
        WHERE i.case_id=$1::uuid AND f.status IN ('candidate','in_review')`, caseID).Scan(&c.OpenFindings)
	_ = s.pool.QueryRow(ctx, `
        SELECT count(*) FROM investigation_reports r JOIN investigations i ON i.id=r.investigation_id
        WHERE i.case_id=$1::uuid AND r.status IN ('generated','in_review')`, caseID).Scan(&c.OpenReports)
	_ = s.pool.QueryRow(ctx, `
        SELECT count(*) FROM investigations WHERE case_id=$1::uuid AND status NOT IN ('completed','cancelled','failed')`, caseID).Scan(&c.OpenInvestigations)
	return c, nil
}

func (s *Service) UpdateCase(ctx context.Context, actor Actor, caseID string, expectedRevision int64, req UpdateCaseRequest) (Case, error) {
	if err := s.ready(); err != nil {
		return Case{}, err
	}
	p := actor.Principal
	if expectedRevision <= 0 {
		return Case{}, apiErr(http.StatusPreconditionRequired, "CONFLICT_REVISION_REQUIRED", "修改案件必须携带 If-Match 版本", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Case{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM cases WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		caseID, p.OrganizationID, p.WorkspaceID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Case{}, notFound()
		}
		return Case{}, err
	}
	if status == "archived" {
		return Case{}, conflict("归档案件为只读状态", nil)
	}
	sets := []string{"revision = revision + 1", "updated_at = now()"}
	args := []any{caseID, p.OrganizationID, p.WorkspaceID}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 200 {
			return Case{}, validation("案件名称长度必须在 1-200 个字符之间", map[string]any{"field": "name"})
		}
		add("name", name)
	}
	if req.Description != nil {
		add("description", strings.TrimSpace(*req.Description))
	}
	if req.Color != nil {
		add("color", strings.TrimSpace(*req.Color))
	}
	if req.Classification != nil {
		classification := strings.TrimSpace(*req.Classification)
		if !oneOf(classification, "public", "internal", "confidential", "restricted") {
			return Case{}, validation("密级不合法", map[string]any{"field": "classification"})
		}
		add("classification", classification)
	}
	if req.Priority != nil {
		priority := strings.TrimSpace(*req.Priority)
		if !oneOf(priority, "low", "medium", "high", "critical") {
			return Case{}, validation("优先级不合法", map[string]any{"field": "priority"})
		}
		add("priority", priority)
	}
	if len(sets) <= 2 {
		return Case{}, validation("没有可更新的字段", nil)
	}
	var c Case
	err = tx.QueryRow(ctx, fmt.Sprintf(`
        UPDATE cases SET %s WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid AND revision=$%d
        RETURNING id::text, organization_id::text, workspace_id::text, coalesce(case_no,''), name, coalesce(description,''),
                  status, coalesce(color,''), classification, priority, revision, created_at, updated_at, archived_at, closed_at`,
		strings.Join(sets, ", "), len(args)+1), append(args, expectedRevision)...,
	).Scan(&c.ID, &c.OrganizationID, &c.WorkspaceID, &c.CaseNo, &c.Name, &c.Description, &c.Status, &c.Color,
		&c.Classification, &c.Priority, &c.Revision, &c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt, &c.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, revisionConflict()
	}
	if err != nil {
		return Case{}, err
	}
	if err := s.appendActivityTx(ctx, tx, actor, caseID, "case.updated", "案件信息已更新", "case", caseID, nil); err != nil {
		return Case{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "case.update", "case", "case", caseID, "success", map[string]any{"expected_revision": expectedRevision, "new_revision": c.Revision}); err != nil {
		return Case{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Case{}, err
	}
	return c, nil
}

func (s *Service) CommandCase(ctx context.Context, actor Actor, caseID, command string, expectedRevision int64, req CaseCommandRequest) (Case, error) {
	if err := s.ready(); err != nil {
		return Case{}, err
	}
	p := actor.Principal
	if expectedRevision <= 0 {
		return Case{}, apiErr(http.StatusPreconditionRequired, "CONFLICT_REVISION_REQUIRED", "案件状态命令必须携带 If-Match 版本", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Case{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT status, revision FROM cases WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		caseID, p.OrganizationID, p.WorkspaceID).Scan(&current, &revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Case{}, notFound()
		}
		return Case{}, err
	}
	if revision != expectedRevision {
		return Case{}, revisionConflict()
	}
	var target string
	allowed := false
	switch command {
	case "activate":
		target, allowed = "active", oneOf(current, "draft", "active", "under_review")
	case "close":
		target, allowed = "closed", oneOf(current, "draft", "active", "under_review")
	case "archive":
		target, allowed = "archived", current == "closed"
	case "reopen":
		target, allowed = "active", oneOf(current, "closed", "archived")
	default:
		return Case{}, notFound()
	}
	if !allowed {
		return Case{}, conflict(fmt.Sprintf("状态 %s 不允许执行 %s", current, command), map[string]any{"current": current, "command": command})
	}
	if command == "close" {
		var pendingFindings, pendingReports int
		_ = tx.QueryRow(ctx, `
            SELECT count(*) FROM investigation_findings f JOIN investigations i ON i.id=f.investigation_id
            WHERE i.case_id=$1::uuid AND f.status IN ('candidate','in_review')`, caseID).Scan(&pendingFindings)
		_ = tx.QueryRow(ctx, `
            SELECT count(*) FROM investigation_reports r JOIN investigations i ON i.id=r.investigation_id
            WHERE i.case_id=$1::uuid AND r.status IN ('generated','in_review')`, caseID).Scan(&pendingReports)
		if (pendingFindings > 0 || pendingReports > 0) && strings.TrimSpace(req.ExceptionReason) == "" {
			return Case{}, conflict("仍有待复核 Finding 或未完成报告审批；如确需关闭请填写例外原因", map[string]any{
				"pending_findings": pendingFindings, "pending_reports": pendingReports,
			})
		}
	}
	var c Case
	err = tx.QueryRow(ctx, `
        UPDATE cases SET status=$4::text, revision=revision+1, updated_at=now(),
               archived_at=CASE WHEN $4='archived' THEN now() WHEN $4='active' THEN NULL ELSE archived_at END,
               closed_at=CASE WHEN $4='closed' THEN now() WHEN $4='active' THEN NULL ELSE closed_at END
        WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid AND revision=$5
        RETURNING id::text, organization_id::text, workspace_id::text, coalesce(case_no,''), name, coalesce(description,''),
                  status, coalesce(color,''), classification, priority, revision, created_at, updated_at, archived_at, closed_at`,
		caseID, p.OrganizationID, p.WorkspaceID, target, expectedRevision,
	).Scan(&c.ID, &c.OrganizationID, &c.WorkspaceID, &c.CaseNo, &c.Name, &c.Description, &c.Status, &c.Color,
		&c.Classification, &c.Priority, &c.Revision, &c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt, &c.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, revisionConflict()
	}
	if err != nil {
		return Case{}, err
	}
	detail := map[string]any{"from": current, "to": target, "reason": req.Reason}
	if req.ExceptionReason != "" {
		detail["exception_reason"] = req.ExceptionReason
	}
	if err := s.appendActivityTx(ctx, tx, actor, caseID, "case.state."+target, fmt.Sprintf("案件状态：%s → %s", current, target), "case", caseID, detail); err != nil {
		return Case{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "case.state:update", "case", "case", caseID, "success", detail); err != nil {
		return Case{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Case{}, err
	}
	return c, nil
}

// ---- 成员 -----------------------------------------------------------------

func (s *Service) ListMembers(ctx context.Context, actor Actor, caseID string) ([]CaseMember, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	p := actor.Principal
	rows, err := s.pool.Query(ctx, `
        SELECT m.id::text, m.identity_id::text, i.email, i.display_name, m.role, m.added_by::text, m.created_at, i.status
        FROM case_members m JOIN identities i ON i.id=m.identity_id
        JOIN cases c ON c.id=m.case_id
        WHERE m.case_id=$1::uuid AND c.organization_id=$2::uuid AND c.workspace_id=$3::uuid
        ORDER BY (m.role='lead') DESC, i.display_name`, caseID, p.OrganizationID, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CaseMember{}
	for rows.Next() {
		var m CaseMember
		if err := rows.Scan(&m.ID, &m.IdentityID, &m.Email, &m.DisplayName, &m.Role, &m.AddedBy, &m.CreatedAt, &m.Status); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) AddMember(ctx context.Context, actor Actor, caseID, identityID, role string) (CaseMember, error) {
	if err := s.ready(); err != nil {
		return CaseMember{}, err
	}
	if role == "" {
		role = "investigator"
	}
	if !oneOf(role, "lead", "investigator", "reviewer", "viewer") {
		return CaseMember{}, validation("成员角色不合法", map[string]any{"field": "role"})
	}
	p := actor.Principal
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CaseMember{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM cases WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		caseID, p.OrganizationID, p.WorkspaceID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CaseMember{}, notFound()
		}
		return CaseMember{}, err
	}
	if status == "archived" {
		return CaseMember{}, conflict("归档案件为只读状态", nil)
	}
	var email, display, identityStatus string
	if err := tx.QueryRow(ctx, `SELECT email, display_name, status FROM identities WHERE id=$1::uuid AND organization_id=$2::uuid`,
		identityID, p.OrganizationID).Scan(&email, &display, &identityStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CaseMember{}, validation("成员不存在或不属于当前组织", map[string]any{"field": "identity_id"})
		}
		return CaseMember{}, err
	}
	if identityStatus != "active" {
		return CaseMember{}, validation("只能添加启用状态的成员", map[string]any{"field": "identity_id"})
	}
	var m CaseMember
	err = tx.QueryRow(ctx, `
        INSERT INTO case_members (case_id, identity_id, role, added_by)
        VALUES ($1::uuid,$2::uuid,$3,NULLIF($4,'')::uuid)
        ON CONFLICT (case_id, identity_id) DO UPDATE SET role=EXCLUDED.role, updated_at=now()
        RETURNING id::text, identity_id::text, role, added_by::text, created_at`,
		caseID, identityID, role, p.IdentityID).Scan(&m.ID, &m.IdentityID, &m.Role, &m.AddedBy, &m.CreatedAt)
	if err != nil {
		return CaseMember{}, err
	}
	m.Email, m.DisplayName, m.Status = email, display, identityStatus
	if err := s.appendActivityTx(ctx, tx, actor, caseID, "case.member.added", "成员已加入："+display, "case_member", m.ID, map[string]any{"role": role}); err != nil {
		return CaseMember{}, err
	}
	if err := s.notifyTx(ctx, tx, identityID, p.OrganizationID, p.WorkspaceID, "case.member.added",
		"notification.case.member.added", "你被加入案件", "", "case", caseID, map[string]any{"role": role}); err != nil {
		return CaseMember{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "case.member:manage", "case", "case_member", m.ID, "success", map[string]any{"case_id": caseID, "identity_id": identityID, "role": role}); err != nil {
		return CaseMember{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CaseMember{}, err
	}
	return m, nil
}

func (s *Service) RemoveMember(ctx context.Context, actor Actor, caseID, identityID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	p := actor.Principal
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM cases WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		caseID, p.OrganizationID, p.WorkspaceID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		return err
	}
	if status == "archived" {
		return conflict("归档案件为只读状态", nil)
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM case_members WHERE case_id=$1::uuid AND identity_id=$2::uuid`, caseID, identityID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		return err
	}
	if role == "lead" {
		var leads int
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM case_members WHERE case_id=$1::uuid AND role='lead'`, caseID).Scan(&leads)
		if leads <= 1 {
			return conflict("不能移除案件最后一个 lead", map[string]any{"rule": "last_case_lead"})
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM case_members WHERE case_id=$1::uuid AND identity_id=$2::uuid`, caseID, identityID); err != nil {
		return err
	}
	if err := s.appendActivityTx(ctx, tx, actor, caseID, "case.member.removed", "成员已移除", "case_member", identityID, map[string]any{"role": role}); err != nil {
		return err
	}
	if err := s.auditTx(ctx, tx, actor, "case.member:manage", "case", "case_member", identityID, "success", map[string]any{"case_id": caseID, "action": "remove", "role": role}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- 调查对象 -------------------------------------------------------------

func (s *Service) ListSubjects(ctx context.Context, actor Actor, caseID string) ([]CaseSubject, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	p := actor.Principal
	rows, err := s.pool.Query(ctx, `
        SELECT s.id::text, s.case_id::text, s.subject_type, s.chain, s.value, s.normalized_value, s.label, s.properties, s.revision, s.created_at, s.updated_at
        FROM case_subjects s JOIN cases c ON c.id=s.case_id
        WHERE s.case_id=$1::uuid AND c.organization_id=$2::uuid AND c.workspace_id=$3::uuid
        ORDER BY s.created_at DESC`, caseID, p.OrganizationID, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CaseSubject{}
	for rows.Next() {
		var item CaseSubject
		var props []byte
		if err := rows.Scan(&item.ID, &item.CaseID, &item.SubjectType, &item.Chain, &item.Value, &item.NormalizedValue,
			&item.Label, &props, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(props, &item.Properties)
		if item.Properties == nil {
			item.Properties = map[string]any{}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) AddSubject(ctx context.Context, actor Actor, caseID string, item CaseSubject) (CaseSubject, error) {
	if err := s.ready(); err != nil {
		return CaseSubject{}, err
	}
	p := actor.Principal
	item.SubjectType = strings.ToUpper(strings.TrimSpace(item.SubjectType))
	if !oneOf(item.SubjectType, "ADDRESS", "TRANSACTION", "DOMAIN", "IP", "APK", "ENTITY", "OTHER") {
		return CaseSubject{}, validation("对象类型不合法", map[string]any{"field": "subject_type"})
	}
	item.Value = strings.TrimSpace(item.Value)
	if item.Value == "" || len(item.Value) > 512 {
		return CaseSubject{}, validation("对象值长度必须在 1-512 个字符之间", map[string]any{"field": "value"})
	}
	chain := strings.ToLower(strings.TrimSpace(deref(item.Chain)))
	normalized := item.Value
	if item.SubjectType == "ADDRESS" {
		if chain == "" {
			return CaseSubject{}, validation("地址对象必须指定 chain", map[string]any{"field": "chain"})
		}
		values, err := dataset.NormalizeAndDedupeAddressesForChain(chain, []string{item.Value})
		if err != nil || len(values) == 0 {
			return CaseSubject{}, validation("地址格式不合法", map[string]any{"field": "value", "chain": chain})
		}
		normalized = values[0]
	} else {
		normalized = strings.ToLower(item.Value)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CaseSubject{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM cases WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		caseID, p.OrganizationID, p.WorkspaceID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CaseSubject{}, notFound()
		}
		return CaseSubject{}, err
	}
	if status == "archived" {
		return CaseSubject{}, conflict("归档案件为只读状态", nil)
	}
	var out CaseSubject
	props := mustJSON(item.Properties)
	err = tx.QueryRow(ctx, `
        INSERT INTO case_subjects (case_id, subject_type, chain, value, normalized_value, label, properties, created_by)
        VALUES ($1::uuid,$2,NULLIF($3,''),$4,$5,$6,$7,NULLIF($8,'')::uuid)
        ON CONFLICT (case_id, subject_type, chain, normalized_value) DO UPDATE
            SET label=EXCLUDED.label, properties=EXCLUDED.properties, revision=case_subjects.revision+1, updated_at=now()
        RETURNING id::text, case_id::text, subject_type, chain, value, normalized_value, label, properties, revision, created_at, updated_at`,
		caseID, item.SubjectType, chain, item.Value, normalized, item.Label, props, p.IdentityID,
	).Scan(&out.ID, &out.CaseID, &out.SubjectType, &out.Chain, &out.Value, &out.NormalizedValue, &out.Label, &props, &out.Revision, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return CaseSubject{}, err
	}
	_ = json.Unmarshal(props, &out.Properties)
	if out.Properties == nil {
		out.Properties = map[string]any{}
	}
	if err := s.appendActivityTx(ctx, tx, actor, caseID, "case.subject.added", "新增调查对象："+out.Value, "case_subject", out.ID, map[string]any{"subject_type": out.SubjectType}); err != nil {
		return CaseSubject{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "subject:create", "case", "case_subject", out.ID, "success", map[string]any{"case_id": caseID, "subject_type": out.SubjectType, "value": out.Value}); err != nil {
		return CaseSubject{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CaseSubject{}, err
	}
	return out, nil
}

func (s *Service) RemoveSubject(ctx context.Context, actor Actor, caseID, subjectID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	p := actor.Principal
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `
        SELECT c.status FROM case_subjects s JOIN cases c ON c.id=s.case_id
        WHERE s.id=$1::uuid AND s.case_id=$2::uuid AND c.organization_id=$3::uuid AND c.workspace_id=$4::uuid FOR UPDATE`,
		subjectID, caseID, p.OrganizationID, p.WorkspaceID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		return err
	}
	if status == "archived" {
		return conflict("归档案件为只读状态", nil)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM case_subjects WHERE id=$1::uuid AND case_id=$2::uuid`, subjectID, caseID); err != nil {
		return err
	}
	if err := s.appendActivityTx(ctx, tx, actor, caseID, "case.subject.removed", "调查对象已移除", "case_subject", subjectID, nil); err != nil {
		return err
	}
	if err := s.auditTx(ctx, tx, actor, "subject:remove", "case", "case_subject", subjectID, "success", map[string]any{"case_id": caseID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- 动态与评论 -----------------------------------------------------------

func (s *Service) ListActivity(ctx context.Context, actor Actor, caseID, cursor string, limit int) ([]ActivityItem, *string, error) {
	if err := s.ready(); err != nil {
		return nil, nil, err
	}
	p := actor.Principal
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{caseID, p.OrganizationID, p.WorkspaceID}
	where := []string{"a.case_id=$1::uuid", "c.organization_id=$2::uuid", "c.workspace_id=$3::uuid"}
	if cursor != "" {
		t, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, nil, validation("分页游标无效", nil)
		}
		args = append(args, t, id)
		where = append(where, fmt.Sprintf("(a.created_at, a.id) < ($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
        SELECT a.id::text, a.event_type, a.actor_display, a.summary, a.object_type, a.object_id, a.detail, a.trace_id, a.created_at
        FROM case_activity a JOIN cases c ON c.id=a.case_id
        WHERE %s ORDER BY a.created_at DESC, a.id DESC LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []ActivityItem{}
	for rows.Next() {
		var item ActivityItem
		var detail []byte
		if err := rows.Scan(&item.ID, &item.EventType, &item.ActorDisplay, &item.Summary, &item.ObjectType, &item.ObjectID, &detail, &item.TraceID, &item.CreatedAt); err != nil {
			return nil, nil, err
		}
		_ = json.Unmarshal(detail, &item.Detail)
		if item.Detail == nil {
			item.Detail = map[string]any{}
		}
		items = append(items, item)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := encodeCursor(items[len(items)-1].CreatedAt, items[len(items)-1].ID)
		next = &c
	}
	return items, next, rows.Err()
}

func (s *Service) ListComments(ctx context.Context, actor Actor, caseID, cursor string, limit int) ([]Comment, *string, error) {
	if err := s.ready(); err != nil {
		return nil, nil, err
	}
	p := actor.Principal
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{caseID, p.OrganizationID, p.WorkspaceID}
	where := []string{"m.case_id=$1::uuid", "c.organization_id=$2::uuid", "c.workspace_id=$3::uuid"}
	if cursor != "" {
		t, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, nil, validation("分页游标无效", nil)
		}
		args = append(args, t, id)
		where = append(where, fmt.Sprintf("(m.created_at, m.id) < ($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
        SELECT m.id::text, m.case_id::text, m.thread_id::text, m.parent_id::text, m.body_markdown, m.body_hash,
               coalesce(m.created_by::text,''), coalesce(i.display_name,''), m.created_at, m.updated_at
        FROM case_comments m JOIN cases c ON c.id=m.case_id LEFT JOIN identities i ON i.id=m.created_by
        WHERE %s ORDER BY m.created_at DESC, m.id DESC LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []Comment{}
	for rows.Next() {
		var item Comment
		if err := rows.Scan(&item.ID, &item.CaseID, &item.ThreadID, &item.ParentID, &item.Body, &item.BodyHash, &item.CreatedBy, &item.Author, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, nil, err
		}
		items = append(items, item)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := encodeCursor(items[len(items)-1].CreatedAt, items[len(items)-1].ID)
		next = &c
	}
	return items, next, rows.Err()
}

func (s *Service) CreateComment(ctx context.Context, actor Actor, caseID, body, threadID, parentID string) (Comment, error) {
	if err := s.ready(); err != nil {
		return Comment{}, err
	}
	body = sanitizeComment(body)
	if body == "" || len([]rune(body)) > 8000 {
		return Comment{}, validation("评论内容长度必须在 1-8000 个字符之间", map[string]any{"field": "body"})
	}
	p := actor.Principal
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Comment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM cases WHERE id=$1::uuid AND organization_id=$2::uuid AND workspace_id=$3::uuid FOR UPDATE`,
		caseID, p.OrganizationID, p.WorkspaceID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Comment{}, notFound()
		}
		return Comment{}, err
	}
	if status == "archived" {
		return Comment{}, conflict("归档案件为只读状态", nil)
	}
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	var item Comment
	err = tx.QueryRow(ctx, `
        INSERT INTO case_comments (case_id, thread_id, parent_id, body_markdown, body_hash, created_by)
        VALUES ($1::uuid, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4, $5, NULLIF($6,'')::uuid)
        RETURNING id::text, case_id::text, thread_id::text, parent_id::text, body_markdown, body_hash, coalesce(created_by::text,''), created_at, updated_at`,
		caseID, threadID, parentID, body, hash, p.IdentityID,
	).Scan(&item.ID, &item.CaseID, &item.ThreadID, &item.ParentID, &item.Body, &item.BodyHash, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Comment{}, err
	}
	item.Author = p.DisplayName
	if err := s.appendActivityTx(ctx, tx, actor, caseID, "case.comment.created", "新增评论", "case_comment", item.ID, map[string]any{"body_hash": hash}); err != nil {
		return Comment{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "comment:create", "case", "case_comment", item.ID, "success", map[string]any{"case_id": caseID, "body_hash": hash}); err != nil {
		return Comment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Comment{}, err
	}
	return item, nil
}

// ---- helpers --------------------------------------------------------------

func oneOf(v string, options ...string) bool {
	for _, o := range options {
		if v == o {
			return true
		}
	}
	return false
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// sanitizeComment 只保留纯文本：移除 HTML 标签与控制字符，避免任何标签在后端/前端成为可执行内容。
func sanitizeComment(v string) string {
	var b strings.Builder
	inTag := false
	for _, r := range v {
		switch {
		case r == '<':
			inTag = true
			continue
		case r == '>':
			inTag = false
			continue
		case inTag:
			continue
		case r == 0:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
