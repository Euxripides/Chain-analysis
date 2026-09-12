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
	"github.com/blockchain-platform/blockchain-platform/internal/investigation"
)

// Finding 是复核视图；算法/证据字段只读，复核只改变状态与意见。
type Finding struct {
	ID               string         `json:"id"`
	InvestigationID  string         `json:"investigation_id"`
	FindingType      string         `json:"finding_type"`
	Title            string         `json:"title"`
	Summary          string         `json:"summary"`
	Confidence       float64        `json:"confidence"`
	Status           string         `json:"status"`
	Nature           string         `json:"nature"`
	Properties       map[string]any `json:"properties"`
	Algorithm        string         `json:"algorithm"`
	AlgorithmVersion string         `json:"algorithm_version"`
	Version          int            `json:"version"`
	Revision         int64          `json:"revision"`
	ReviewNote       string         `json:"review_note"`
	ReviewedBy       *string        `json:"reviewed_by,omitempty"`
	ReviewedAt       *time.Time     `json:"reviewed_at,omitempty"`
	SupersedesID     *string        `json:"supersedes_id,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	Evidence         []EvidenceRef  `json:"evidence"`
}

type EvidenceRef struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	ContentHash  string `json:"content_hash"`
	EvidenceType string `json:"evidence_type"`
}

type Evidence struct {
	ID              string         `json:"id"`
	InvestigationID string         `json:"investigation_id"`
	EvidenceType    string         `json:"evidence_type"`
	Title           string         `json:"title"`
	Status          string         `json:"status"`
	Nature          string         `json:"nature"`
	SourceRefs      []string       `json:"source_refs"`
	ContentHash     string         `json:"content_hash"`
	ObjectRef       string         `json:"object_ref,omitempty"`
	Provenance      map[string]any `json:"provenance"`
	Version         int            `json:"version"`
	Revision        int64          `json:"revision"`
	SupersedesID    *string        `json:"supersedes_id,omitempty"`
	SupersededBy    *string        `json:"superseded_by,omitempty"`
	VerifiedBy      *string        `json:"verified_by,omitempty"`
	VerifiedAt      *time.Time     `json:"verified_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type ReviewCommandRequest struct {
	Note            string `json:"note"`
	Reason          string `json:"reason"`
	ExceptionReason string `json:"exception_reason"`
	Message         string `json:"message"`
}

type EvidenceCommandRequest struct {
	Note        string         `json:"note"`
	Reason      string         `json:"reason"`
	Title       string         `json:"title"`
	ContentHash string         `json:"content_hash"`
	SourceRefs  []string       `json:"source_refs"`
	Provenance  map[string]any `json:"provenance"`
}

type ReviewInboxItem struct {
	ID              string    `json:"id"`
	ResourceType    string    `json:"resource_type"`
	ResourceID      string    `json:"resource_id"`
	CaseID          *string   `json:"case_id,omitempty"`
	InvestigationID *string   `json:"investigation_id,omitempty"`
	RequestedBy     *string   `json:"requested_by,omitempty"`
	Requester       string    `json:"requester"`
	ReviewerID      *string   `json:"reviewer_id,omitempty"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason"`
	Revision        int64     `json:"revision"`
	CreatedAt       time.Time `json:"created_at"`
	Title           string    `json:"title"`
}

func (s *Service) ListFindings(ctx context.Context, actor Actor, investigationID, status, findingType, nature, cursor string, limit int) ([]Finding, *string, error) {
	if err := s.ready(); err != nil {
		return nil, nil, err
	}
	p := actor.Principal
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{investigationID, p.OrganizationID}
	where := []string{"f.investigation_id=$1::uuid", "i.organization_id=$2::uuid"}
	if status != "" && status != "all" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("f.status=$%d", len(args)))
	}
	if findingType != "" {
		args = append(args, findingType)
		where = append(where, fmt.Sprintf("f.finding_type=$%d", len(args)))
	}
	if nature != "" {
		args = append(args, nature)
		where = append(where, fmt.Sprintf("f.nature=$%d", len(args)))
	}
	if cursor != "" {
		t, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, nil, validation("分页游标无效", nil)
		}
		args = append(args, t, id)
		where = append(where, fmt.Sprintf("(f.created_at,f.id) < ($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
        SELECT f.id::text,f.investigation_id::text,f.finding_type,f.title,f.summary,f.confidence,f.status,f.nature,
               f.properties,f.algorithm,f.algorithm_version,f.version,f.revision,f.review_note,f.reviewed_by::text,
               f.reviewed_at,f.supersedes_id::text,f.created_at,f.updated_at
        FROM investigation_findings f JOIN investigations i ON i.id=f.investigation_id
        WHERE %s ORDER BY f.created_at DESC,f.id DESC LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []Finding{}
	for rows.Next() {
		var f Finding
		var props []byte
		if err := rows.Scan(&f.ID, &f.InvestigationID, &f.FindingType, &f.Title, &f.Summary, &f.Confidence, &f.Status, &f.Nature,
			&props, &f.Algorithm, &f.AlgorithmVersion, &f.Version, &f.Revision, &f.ReviewNote, &f.ReviewedBy,
			&f.ReviewedAt, &f.SupersedesID, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, nil, err
		}
		_ = jsonUnmarshal(props, &f.Properties)
		if f.Properties == nil {
			f.Properties = map[string]any{}
		}
		refs, err := s.findingEvidence(ctx, f.ID)
		if err != nil {
			return nil, nil, err
		}
		f.Evidence = refs
		items = append(items, f)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := encodeCursor(items[len(items)-1].CreatedAt, items[len(items)-1].ID)
		next = &c
	}
	return items, next, rows.Err()
}

func (s *Service) findingEvidence(ctx context.Context, findingID string) ([]EvidenceRef, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT e.id::text,e.title,e.status,e.content_hash,e.evidence_type
        FROM finding_evidence_links l JOIN investigation_evidence e ON e.id=l.evidence_id
        WHERE l.finding_id=$1::uuid ORDER BY e.created_at`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvidenceRef{}
	for rows.Next() {
		var e EvidenceRef
		if err := rows.Scan(&e.ID, &e.Title, &e.Status, &e.ContentHash, &e.EvidenceType); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) GetFinding(ctx context.Context, actor Actor, findingID string) (Finding, error) {
	if err := s.ready(); err != nil {
		return Finding{}, err
	}
	p := actor.Principal
	var f Finding
	var props []byte
	err := s.pool.QueryRow(ctx, `
        SELECT f.id::text,f.investigation_id::text,f.finding_type,f.title,f.summary,f.confidence,f.status,f.nature,
               f.properties,f.algorithm,f.algorithm_version,f.version,f.revision,f.review_note,f.reviewed_by::text,
               f.reviewed_at,f.supersedes_id::text,f.created_at,f.updated_at
        FROM investigation_findings f JOIN investigations i ON i.id=f.investigation_id
        WHERE f.id=$1::uuid AND i.organization_id=$2::uuid`, findingID, p.OrganizationID).
		Scan(&f.ID, &f.InvestigationID, &f.FindingType, &f.Title, &f.Summary, &f.Confidence, &f.Status, &f.Nature,
			&props, &f.Algorithm, &f.AlgorithmVersion, &f.Version, &f.Revision, &f.ReviewNote, &f.ReviewedBy,
			&f.ReviewedAt, &f.SupersedesID, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, notFound()
	}
	if err != nil {
		return Finding{}, err
	}
	_ = jsonUnmarshal(props, &f.Properties)
	if f.Properties == nil {
		f.Properties = map[string]any{}
	}
	f.Evidence, err = s.findingEvidence(ctx, f.ID)
	if err != nil {
		return Finding{}, err
	}
	return f, nil
}

func (s *Service) PatchFinding(ctx context.Context, actor Actor, findingID string, expectedRevision int64, summary, reviewNote *string) (Finding, error) {
	if err := s.ready(); err != nil {
		return Finding{}, err
	}
	if expectedRevision <= 0 {
		return Finding{}, apiErr(http.StatusPreconditionRequired, "CONFLICT_REVISION_REQUIRED", "修改 Finding 必须携带 If-Match 版本", nil)
	}
	p := actor.Principal
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Finding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var invID, status string
	var revision int64
	if err := tx.QueryRow(ctx, `
        SELECT f.investigation_id::text,f.status,f.revision FROM investigation_findings f
        JOIN investigations i ON i.id=f.investigation_id
        WHERE f.id=$1::uuid AND i.organization_id=$2::uuid FOR UPDATE`, findingID, p.OrganizationID).Scan(&invID, &status, &revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Finding{}, notFound()
		}
		return Finding{}, err
	}
	if revision != expectedRevision {
		return Finding{}, revisionConflict()
	}
	if oneOf(status, "confirmed", "rejected", "superseded") && summary != nil {
		return Finding{}, conflict("已完成的 Finding 不能修改摘要，只能追加复核意见", nil)
	}
	sets := []string{"revision=revision+1", "updated_at=now()"}
	args := []any{findingID}
	if summary != nil {
		args = append(args, strings.TrimSpace(*summary))
		sets = append(sets, fmt.Sprintf("summary=$%d", len(args)))
	}
	if reviewNote != nil {
		args = append(args, strings.TrimSpace(*reviewNote))
		sets = append(sets, fmt.Sprintf("review_note=$%d", len(args)))
	}
	if len(sets) <= 2 {
		return Finding{}, validation("没有可更新的字段", nil)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE investigation_findings SET %s WHERE id=$1::uuid`, strings.Join(sets, ",")), args...); err != nil {
		return Finding{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "finding:update", "finding", "finding", findingID, "success", map[string]any{"expected_revision": expectedRevision}); err != nil {
		return Finding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Finding{}, err
	}
	return s.GetFinding(ctx, actor, findingID)
}

func (s *Service) CommandFinding(ctx context.Context, actor Actor, findingID, command string, expectedRevision int64, req ReviewCommandRequest) (Finding, error) {
	if err := s.ready(); err != nil {
		return Finding{}, err
	}
	if expectedRevision <= 0 {
		return Finding{}, apiErr(http.StatusPreconditionRequired, "CONFLICT_REVISION_REQUIRED", "Finding 复核命令必须携带 If-Match 版本", nil)
	}
	p := actor.Principal
	if command == "confirm" && !p.Has("finding:review") {
		return Finding{}, forbidden("需要 finding:review 权限才能确认 Finding", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Finding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var invID, caseID, status, nature string
	var revision int64
	err = tx.QueryRow(ctx, `
        SELECT f.investigation_id::text, coalesce(i.case_id::text,''), f.status, f.nature, f.revision
        FROM investigation_findings f JOIN investigations i ON i.id=f.investigation_id
        WHERE f.id=$1::uuid AND i.organization_id=$2::uuid FOR UPDATE`, findingID, p.OrganizationID).Scan(&invID, &caseID, &status, &nature, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, notFound()
	}
	if err != nil {
		return Finding{}, err
	}
	if revision != expectedRevision {
		return Finding{}, revisionConflict()
	}
	if status == "superseded" {
		return Finding{}, conflict("Finding 已被替代，不能再复核", nil)
	}
	note := strings.TrimSpace(firstNonEmpty(req.Note, req.Message, req.Reason))
	var target string
	switch command {
	case "confirm":
		if !oneOf(status, "candidate", "in_review") {
			return Finding{}, conflict("只有 candidate/in_review 状态的 Finding 可以确认", map[string]any{"status": status})
		}
		verified, err := s.verifiedEvidenceCount(ctx, tx, findingID)
		if err != nil {
			return Finding{}, err
		}
		if verified == 0 && strings.TrimSpace(req.ExceptionReason) == "" {
			return Finding{}, conflict("确认 Finding 至少需要一条 verified Evidence，或填写经审批的例外原因", map[string]any{"rule": "finding_requires_verified_evidence"})
		}
		target, nature = "confirmed", "CONFIRMED"
	case "reject":
		if !oneOf(status, "candidate", "in_review") {
			return Finding{}, conflict("只有 candidate/in_review 状态的 Finding 可以驳回", map[string]any{"status": status})
		}
		if note == "" {
			return Finding{}, validation("驳回必须填写原因", map[string]any{"field": "note"})
		}
		target = "rejected"
	case "supersede":
		if !oneOf(status, "candidate", "in_review", "confirmed") {
			return Finding{}, conflict("当前状态不允许替代", map[string]any{"status": status})
		}
		target = "superseded"
	default:
		return Finding{}, notFound()
	}
	_, err = tx.Exec(ctx, `
        UPDATE investigation_findings SET status=$2,nature=$3,review_note=CASE WHEN $4='' THEN review_note ELSE $4 END,
               reviewed_by=NULLIF($5,'')::uuid,reviewed_at=now(),revision=revision+1,updated_at=now()
        WHERE id=$1::uuid`, findingID, target, nature, note, p.IdentityID)
	if err != nil {
		return Finding{}, err
	}
	if caseID != "" {
		if err := s.appendActivityTx(ctx, tx, actor, caseID, "finding."+target, "Finding 状态："+status+" → "+target, "finding", findingID, map[string]any{"command": command}); err != nil {
			return Finding{}, err
		}
	}
	if err := s.closeReviewRequestsTx(ctx, tx, "finding", findingID, map[string]any{"decision": target, "note": note}); err != nil {
		return Finding{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "finding:review", "finding", "finding", findingID, "success", map[string]any{
		"command": command, "from": status, "to": target, "note": note,
	}); err != nil {
		return Finding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Finding{}, err
	}
	return s.GetFinding(ctx, actor, findingID)
}

func (s *Service) verifiedEvidenceCount(ctx context.Context, q pgx.Tx, findingID string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
        SELECT count(*) FROM finding_evidence_links l JOIN investigation_evidence e ON e.id=l.evidence_id
        WHERE l.finding_id=$1::uuid AND e.status='verified'`, findingID).Scan(&n)
	return n, err
}

// ---- Evidence -------------------------------------------------------------

func (s *Service) ListEvidence(ctx context.Context, actor Actor, investigationID, status string, limit int) ([]Evidence, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	p := actor.Principal
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	args := []any{investigationID, p.OrganizationID}
	where := []string{"e.investigation_id=$1::uuid", "i.organization_id=$2::uuid"}
	if status != "" && status != "all" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("e.status=$%d", len(args)))
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
        SELECT e.id::text,e.investigation_id::text,e.evidence_type,e.title,e.status,e.nature,e.source_refs,e.content_hash,
               e.object_ref,e.provenance,e.version,e.revision,e.supersedes_id::text,e.superseded_by::text,
               e.verified_by::text,e.verified_at,e.created_at
        FROM investigation_evidence e JOIN investigations i ON i.id=e.investigation_id
        WHERE %s ORDER BY e.created_at DESC LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Evidence{}
	for rows.Next() {
		var e Evidence
		var refs, prov []byte
		if err := rows.Scan(&e.ID, &e.InvestigationID, &e.EvidenceType, &e.Title, &e.Status, &e.Nature, &refs, &e.ContentHash,
			&e.ObjectRef, &prov, &e.Version, &e.Revision, &e.SupersedesID, &e.SupersededBy, &e.VerifiedBy, &e.VerifiedAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = jsonUnmarshal(refs, &e.SourceRefs)
		_ = jsonUnmarshal(prov, &e.Provenance)
		if e.SourceRefs == nil {
			e.SourceRefs = []string{}
		}
		if e.Provenance == nil {
			e.Provenance = map[string]any{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) GetEvidence(ctx context.Context, actor Actor, evidenceID string, includeSensitive bool) (Evidence, error) {
	if err := s.ready(); err != nil {
		return Evidence{}, err
	}
	p := actor.Principal
	var e Evidence
	var refs, prov []byte
	err := s.pool.QueryRow(ctx, `
        SELECT e.id::text,e.investigation_id::text,e.evidence_type,e.title,e.status,e.nature,e.source_refs,e.content_hash,
               e.object_ref,e.provenance,e.version,e.revision,e.supersedes_id::text,e.superseded_by::text,
               e.verified_by::text,e.verified_at,e.created_at
        FROM investigation_evidence e JOIN investigations i ON i.id=e.investigation_id
        WHERE e.id=$1::uuid AND i.organization_id=$2::uuid`, evidenceID, p.OrganizationID).
		Scan(&e.ID, &e.InvestigationID, &e.EvidenceType, &e.Title, &e.Status, &e.Nature, &refs, &e.ContentHash,
			&e.ObjectRef, &prov, &e.Version, &e.Revision, &e.SupersedesID, &e.SupersededBy, &e.VerifiedBy, &e.VerifiedAt, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Evidence{}, notFound()
	}
	if err != nil {
		return Evidence{}, err
	}
	_ = jsonUnmarshal(refs, &e.SourceRefs)
	_ = jsonUnmarshal(prov, &e.Provenance)
	if e.SourceRefs == nil {
		e.SourceRefs = []string{}
	}
	if e.Provenance == nil {
		e.Provenance = map[string]any{}
	}
	if !includeSensitive {
		// 无 evidence:sensitive:read 时只返回元数据，不返回对象引用正文。
		e.ObjectRef = ""
	}
	action, risk := "evidence:read", "low"
	if includeSensitive {
		action = "evidence:sensitive:read"
		risk = "critical"
	}
	_ = s.writeAuditAsync(ctx, actor, action, "evidence", "evidence", evidenceID, risk, map[string]any{"include_sensitive": includeSensitive})
	return e, nil
}

func (s *Service) CommandEvidence(ctx context.Context, actor Actor, evidenceID, command string, req EvidenceCommandRequest) (Evidence, error) {
	if err := s.ready(); err != nil {
		return Evidence{}, err
	}
	p := actor.Principal
	if !p.Has("evidence:review") {
		return Evidence{}, forbidden("需要 evidence:review 权限", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Evidence{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var invID, caseID, status, nature string
	var version int
	var title, contentHash string
	var refsRaw, prov []byte
	err = tx.QueryRow(ctx, `
        SELECT e.investigation_id::text, coalesce(i.case_id::text,''), e.status, e.nature, e.version, e.title, e.content_hash, e.source_refs, e.provenance
        FROM investigation_evidence e JOIN investigations i ON i.id=e.investigation_id
        WHERE e.id=$1::uuid AND i.organization_id=$2::uuid FOR UPDATE`, evidenceID, p.OrganizationID).
		Scan(&invID, &caseID, &status, &nature, &version, &title, &contentHash, &refsRaw, &prov)
	if errors.Is(err, pgx.ErrNoRows) {
		return Evidence{}, notFound()
	}
	if err != nil {
		return Evidence{}, err
	}
	if status == "superseded" && command != "supersede" {
		return Evidence{}, conflict("Evidence 已被替代，不能再改状态", nil)
	}
	note := strings.TrimSpace(firstNonEmpty(req.Note, req.Reason))
	switch command {
	case "verify":
		if status == "verified" {
			return Evidence{}, conflict("Evidence 已经是 verified 状态", nil)
		}
		if !oneOf(status, "collected", "disputed") {
			return Evidence{}, conflict("当前状态不允许 verify", map[string]any{"status": status})
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation_evidence SET status='verified',verified_by=NULLIF($2,'')::uuid,verified_at=now(),revision=revision+1 WHERE id=$1::uuid`, evidenceID, p.IdentityID); err != nil {
			return Evidence{}, err
		}
	case "dispute":
		if !oneOf(status, "collected", "verified") {
			return Evidence{}, conflict("当前状态不允许 dispute", map[string]any{"status": status})
		}
		if note == "" {
			return Evidence{}, validation("质疑 Evidence 必须填写原因", map[string]any{"field": "note"})
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation_evidence SET status='disputed',revision=revision+1 WHERE id=$1::uuid`, evidenceID); err != nil {
			return Evidence{}, err
		}
	case "supersede":
		if status == "superseded" {
			return Evidence{}, conflict("Evidence 已被替代", nil)
		}
		newTitle := strings.TrimSpace(req.Title)
		if newTitle == "" {
			newTitle = title
		}
		newHash := strings.TrimSpace(req.ContentHash)
		if newHash == "" {
			newHash = contentHash
		}
		newRefs := req.SourceRefs
		if newRefs == nil {
			var parsed []string
			_ = jsonUnmarshal(refsRaw, &parsed)
			newRefs = parsed
		}
		newProv := req.Provenance
		if newProv == nil {
			var parsed map[string]any
			_ = jsonUnmarshal(prov, &parsed)
			newProv = parsed
		}
		var newID string
		if err := tx.QueryRow(ctx, `
            INSERT INTO investigation_evidence (investigation_id,evidence_type,title,source_refs,content_hash,object_ref,provenance,
                status,nature,version,supersedes_id,created_by,collected_at)
            VALUES ($1::uuid,$2,$3,$4,$5,'',$6,'collected','DETERMINISTIC',$7,$8::uuid,NULLIF($9,'')::uuid,now())
            RETURNING id::text`,
			invID, "evidence_revision", newTitle, mustJSON(newRefs), newHash, mustJSON(newProv), version+1, evidenceID, p.IdentityID).Scan(&newID); err != nil {
			return Evidence{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation_evidence SET status='superseded',superseded_by=$2::uuid,revision=revision+1 WHERE id=$1::uuid`, evidenceID, newID); err != nil {
			return Evidence{}, err
		}
		if err := s.auditTx(ctx, tx, actor, "evidence:review", "evidence", "evidence", newID, "success", map[string]any{
			"command": "supersede", "supersedes_id": evidenceID, "version": version + 1,
		}); err != nil {
			return Evidence{}, err
		}
		if caseID != "" {
			if err := s.appendActivityTx(ctx, tx, actor, caseID, "evidence.superseded", "Evidence 生成新版本", "evidence", newID, map[string]any{"supersedes_id": evidenceID}); err != nil {
				return Evidence{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return Evidence{}, err
		}
		return s.GetEvidence(ctx, actor, newID, true)
	default:
		return Evidence{}, notFound()
	}
	if err := s.auditTx(ctx, tx, actor, "evidence:review", "evidence", "evidence", evidenceID, "success", map[string]any{"command": command, "note": note}); err != nil {
		return Evidence{}, err
	}
	if caseID != "" {
		if err := s.appendActivityTx(ctx, tx, actor, caseID, "evidence."+command, "Evidence 状态："+command, "evidence", evidenceID, map[string]any{"note": note}); err != nil {
			return Evidence{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Evidence{}, err
	}
	return s.GetEvidence(ctx, actor, evidenceID, true)
}

// ---- 复核 Inbox -----------------------------------------------------------

func (s *Service) ListInbox(ctx context.Context, actor Actor, status string, limit int) ([]ReviewInboxItem, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	p := actor.Principal
	if status == "" {
		status = "pending"
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
        SELECT r.id::text,r.resource_type,r.resource_id::text,r.case_id::text,r.investigation_id::text,
               r.requested_by::text,coalesce(i.display_name,''),r.reviewer_id::text,r.status,r.reason,r.revision,r.created_at,
               CASE r.resource_type
                 WHEN 'investigation' THEN coalesce(inv.goal,'')
                 WHEN 'finding' THEN coalesce(f.title,'')
                 WHEN 'evidence' THEN coalesce(e.title,'')
                 WHEN 'report' THEN coalesce(rep.object_key,'')
                 ELSE ''
               END AS title
        FROM review_requests r
        LEFT JOIN identities i ON i.id=r.requested_by
        LEFT JOIN investigations inv ON inv.id=r.investigation_id
        LEFT JOIN investigation_findings f ON f.id=r.resource_id AND r.resource_type='finding'
        LEFT JOIN investigation_evidence e ON e.id=r.resource_id AND r.resource_type='evidence'
        LEFT JOIN investigation_reports rep ON rep.id=r.resource_id AND r.resource_type='report'
        WHERE r.organization_id=$1::uuid AND r.workspace_id=$2::uuid AND r.status=$3
          AND (r.reviewer_id=$4::uuid OR r.reviewer_id IS NULL)
        ORDER BY r.created_at DESC LIMIT $5`, p.OrganizationID, p.WorkspaceID, status, p.IdentityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewInboxItem{}
	for rows.Next() {
		var item ReviewInboxItem
		if err := rows.Scan(&item.ID, &item.ResourceType, &item.ResourceID, &item.CaseID, &item.InvestigationID,
			&item.RequestedBy, &item.Requester, &item.ReviewerID, &item.Status, &item.Reason, &item.Revision, &item.CreatedAt, &item.Title); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) CreateReviewRequest(ctx context.Context, actor Actor, resourceType, resourceID, reviewerID, reason string, caseID, investigationID string) (ReviewInboxItem, error) {
	if err := s.ready(); err != nil {
		return ReviewInboxItem{}, err
	}
	p := actor.Principal
	if !oneOf(resourceType, "investigation", "finding", "evidence", "report") {
		return ReviewInboxItem{}, validation("复核对象类型不合法", map[string]any{"field": "resource_type"})
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReviewInboxItem{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
        INSERT INTO review_requests (organization_id,workspace_id,case_id,investigation_id,resource_type,resource_id,requested_by,reviewer_id,status,reason)
        VALUES ($1::uuid,$2::uuid,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6::uuid,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,'pending',$9)
        RETURNING id::text,created_at`,
		p.OrganizationID, p.WorkspaceID, caseID, investigationID, resourceType, resourceID, p.IdentityID, reviewerID, reason).Scan(&id, &createdAt); err != nil {
		return ReviewInboxItem{}, err
	}
	if reviewerID != "" {
		title := "有一条新的复核请求"
		if resourceType == "finding" {
			title = "有一条 Finding 待你复核"
		} else if resourceType == "report" {
			title = "有一份报告待你审批"
		}
		if err := s.notifyTx(ctx, tx, reviewerID, p.OrganizationID, p.WorkspaceID, "review.requested",
			"notification.review.requested", title, reason, resourceType, resourceID, map[string]any{"review_id": id}); err != nil {
			return ReviewInboxItem{}, err
		}
	}
	if err := s.auditTx(ctx, tx, actor, "review:request", "review", resourceType, resourceID, "success", map[string]any{"reviewer_id": reviewerID, "reason": reason}); err != nil {
		return ReviewInboxItem{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReviewInboxItem{}, err
	}
	return ReviewInboxItem{ID: id, ResourceType: resourceType, ResourceID: resourceID, RequestedBy: &p.IdentityID,
		Requester: p.DisplayName, Status: "pending", Reason: reason, Revision: 1, CreatedAt: createdAt}, nil
}

func (s *Service) closeReviewRequestsTx(ctx context.Context, tx pgx.Tx, resourceType, resourceID string, decision map[string]any) error {
	target := "approved"
	switch fmt.Sprint(decision["decision"]) {
	case "rejected", "reject":
		target = "rejected"
	case "cancelled", "cancel":
		target = "cancelled"
	}
	_, err := tx.Exec(ctx, `
        UPDATE review_requests SET status=$3,decision_note=coalesce($4,''),revision=revision+1,updated_at=now(),decided_at=now()
        WHERE resource_type=$1 AND resource_id=$2::uuid AND status='pending'`,
		resourceType, resourceID, target, fmt.Sprint(decision["note"]))
	return err
}

// CancelPendingReviewRequests 在对象被取消/归档时关闭未决复核请求。
func (s *Service) CancelPendingReviewRequests(ctx context.Context, actor Actor, resourceType, resourceID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
        UPDATE review_requests SET status='cancelled',revision=revision+1,updated_at=now(),decided_at=now()
        WHERE resource_type=$1 AND resource_id=$2::uuid AND status='pending'`, resourceType, resourceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) writeAuditAsync(ctx context.Context, actor Actor, action, category, resourceType, resourceID, risk string, detail map[string]any) error {
	if s.audit == nil || !s.audit.Enabled() {
		return nil
	}
	event := auditEvent(actor, action, category, resourceType, resourceID, "success", risk, detail)
	return s.audit.Write(ctx, event)
}

// ---- Investigation v2 compatibility ---------------------------------------

type CreateInvestigationRequest struct {
	CaseID             string            `json:"case_id"`
	Goal               string            `json:"goal"`
	Mode               string            `json:"mode"`
	Subjects           []SubjectInput    `json:"subjects"`
	ExpectedOutputs    []string          `json:"expected_outputs"`
	DataRequirement    DataRequirement   `json:"data_requirement"`
	Budget             map[string]any    `json:"budget"`
	CapabilityVersions map[string]string `json:"capability_versions"`
}

type SubjectInput struct {
	Type  string `json:"type"`
	Chain string `json:"chain"`
	Value string `json:"value"`
}

type DataRequirement struct {
	Datasets    []string       `json:"datasets"`
	Range       map[string]any `json:"range"`
	ReusePolicy string         `json:"reuse_policy"`
}

func (s *Service) CreateInvestigation(ctx context.Context, actor Actor, req CreateInvestigationRequest, idempotencyKey string) (investigation.Investigation, bool, error) {
	if err := s.ready(); err != nil {
		return investigation.Investigation{}, false, err
	}
	if s.invSvc == nil {
		return investigation.Investigation{}, false, apiErr(http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "调查服务不可用", nil)
	}
	p := actor.Principal
	if len(req.Subjects) == 0 {
		return investigation.Investigation{}, false, validation("至少需要一个调查对象", map[string]any{"field": "subjects"})
	}
	primary := req.Subjects[0]
	chain := strings.ToLower(strings.TrimSpace(primary.Chain))
	value := strings.TrimSpace(primary.Value)
	if chain == "" || value == "" {
		return investigation.Investigation{}, false, validation("调查对象必须包含 chain 与 value", map[string]any{"field": "subjects"})
	}
	if primary.Type != "" && !strings.EqualFold(primary.Type, "ADDRESS") {
		return investigation.Investigation{}, false, validation("Sprint 4 只支持 ADDRESS 调查对象", map[string]any{"field": "subjects[0].type"})
	}
	values, err := dataset.NormalizeAndDedupeAddressesForChain(chain, []string{value})
	if err != nil || len(values) == 0 {
		return investigation.Investigation{}, false, validation("调查地址格式不合法", map[string]any{"field": "subjects[0].value", "chain": chain})
	}
	value = values[0]
	caseID := strings.TrimSpace(req.CaseID)
	if caseID == "" {
		return investigation.Investigation{}, false, validation("调查必须绑定案件", map[string]any{"field": "case_id"})
	}
	var caseWS string
	var caseStatus string
	if err := s.pool.QueryRow(ctx, `
        SELECT workspace_id::text, status FROM cases WHERE id=$1::uuid AND organization_id=$2::uuid`, caseID, p.OrganizationID).Scan(&caseWS, &caseStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return investigation.Investigation{}, false, notFound()
		}
		return investigation.Investigation{}, false, err
	}
	if caseStatus == "archived" {
		return investigation.Investigation{}, false, conflict("归档案件不能再发起调查", nil)
	}
	workspaceID := caseWS
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "assisted"
	}
	dataReq := map[string]any{
		"datasets": req.DataRequirement.Datasets, "range": req.DataRequirement.Range,
		"reuse_policy":     firstNonEmpty(req.DataRequirement.ReusePolicy, "prefer_local"),
		"expected_outputs": req.ExpectedOutputs, "capability_versions": req.CapabilityVersions,
	}
	insert := investigation.CreateRequest{
		Organization: p.OrganizationID, WorkspaceID: &workspaceID, CaseID: &caseID,
		Subject: contractSubject(chain, value), Goal: strings.TrimSpace(req.Goal), Mode: mode,
		Budget: req.Budget, DataRequirement: dataReq, IdempotencyKey: idempotencyKey,
	}
	inv, replayed, err := s.invSvc.Create(ctx, insert)
	if err != nil {
		return investigation.Investigation{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return investigation.Investigation{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.appendActivityTx(ctx, tx, actor, caseID, "investigation.created", "发起调查："+inv.Goal, "investigation", inv.ID, map[string]any{"status": inv.Status}); err != nil {
		return investigation.Investigation{}, false, err
	}
	if err := s.auditTx(ctx, tx, actor, "investigation:create", "investigation", "investigation", inv.ID, "success", map[string]any{
		"case_id": caseID, "goal": inv.Goal, "idempotent_replay": replayed,
	}); err != nil {
		return investigation.Investigation{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return investigation.Investigation{}, false, err
	}
	return inv, replayed, nil
}

func (s *Service) PreviewInvestigation(ctx context.Context, actor Actor, caseID string, subjects []SubjectInput, dataReq DataRequirement) (*dataset.CoveragePreviewResponse, map[string]any, error) {
	if err := s.ready(); err != nil {
		return nil, nil, err
	}
	if s.preview == nil {
		return nil, nil, apiErr(http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "Coverage 预览不可用", nil)
	}
	if len(subjects) == 0 {
		return nil, nil, validation("至少需要一个调查对象", map[string]any{"field": "subjects"})
	}
	chain := strings.ToLower(strings.TrimSpace(subjects[0].Chain))
	values := make([]string, 0, len(subjects))
	for _, sub := range subjects {
		values = append(values, sub.Value)
	}
	datasets := dataReq.Datasets
	if len(datasets) == 0 {
		datasets = []string{"token_transfers"}
	}
	ensureReq := dataset.EnsureRequest{
		Chain: chain, Subjects: values, Datasets: datasets,
		Range:       dataset.RangeSpecFromMap(dataReq.Range),
		ReusePolicy: firstNonEmpty(dataReq.ReusePolicy, "prefer_local"),
	}
	if err := s.previewRequestValid(ctx, actor, caseID, ensureReq); err != nil {
		return nil, nil, err
	}
	preview, err := s.preview(ctx, ensureReq)
	if err != nil {
		return nil, nil, validation("Coverage 预览失败："+err.Error(), nil)
	}
	summary := map[string]any{
		"overall_ratio": preview.OverallRatio,
		"coverage_hit":  preview.OverallRatio >= 1,
		"datasets":      preview.Datasets,
		"scope":         map[string]any{"organization_id": actor.Principal.OrganizationID, "workspace_id": actor.Principal.WorkspaceID},
		"reuse_policy":  ensureReq.ReusePolicy,
	}
	_ = s.writeAuditAsync(ctx, actor, "investigation:create", "investigation", "investigation_preview", caseID, "low",
		map[string]any{"chain": chain, "coverage": preview.OverallRatio})
	return preview, summary, nil
}

func (s *Service) previewRequestValid(ctx context.Context, actor Actor, caseID string, req dataset.EnsureRequest) error {
	if strings.TrimSpace(caseID) != "" {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cases WHERE id=$1::uuid AND organization_id=$2::uuid AND status <> 'archived')`,
			caseID, actor.Principal.OrganizationID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return notFound()
		}
	}
	if err := s.validateChainEnabled(req.Chain); err != nil {
		return validation(err.Error(), nil)
	}
	return nil
}

func (s *Service) validateChainEnabled(chain string) error {
	if s.invSvc == nil {
		return nil
	}
	if chain == "" {
		return errors.New("chain 不能为空")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
