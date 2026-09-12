package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
)

// Sprint 6 后台控制面的公共原语（§19 / §21.6 / §28.8）。
//
// 统一约束：
//   - 每个端点必须由 s.require(permission, ...) 声明权限；未声明的端点不得挂载。
//   - 高风险命令必须 step-up（当前会话主体重新提交密码），秘密字段永不回显、永不写日志。
//   - 双人审批写入 approval_requests，requester 与 approver 由数据库约束拦截。
//   - 每个写操作写入 audit_events_v2（before/after），并带 request_id / trace_id。
//   - 并发修改统一使用 If-Match，缺失 428，过期 412（ADMIN-009）。

const s6StepUpTTL = 5 * time.Minute

var (
	errS6StepUpRequired = errors.New("step_up_required")
	errS6StepUpInvalid  = errors.New("step_up_invalid")
	errS6StepUpDisabled = errors.New("step_up_unavailable")
	errS6TwoPerson      = errors.New("two_person_violation")
)

// s6IfMatch 解析 If-Match：缺失返回 (0,false)。
func s6IfMatch(r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, false
	}
	raw = strings.TrimPrefix(raw, "W/")
	raw = strings.Trim(raw, "\"")
	raw = strings.TrimSpace(raw)
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// s6RequireRevision 校验 If-Match；返回 false 时响应已经写出。
func s6RequireRevision(w http.ResponseWriter, r *http.Request, current int64, resource string) (int64, bool) {
	rev, ok := s6IfMatch(r)
	if !ok {
		respondError(w, r, http.StatusPreconditionRequired, "CONFLICT_REVISION_REQUIRED",
			"修改"+resource+"必须携带 If-Match 版本", map[string]any{"current_revision": current})
		return 0, false
	}
	if rev != current {
		respondError(w, r, http.StatusPreconditionFailed, "CONFLICT_REVISION",
			resource+"已在其他会话被修改，请刷新后重试",
			map[string]any{"current_revision": current, "your_revision": rev})
		return 0, false
	}
	return rev, true
}

// s6StepUp 校验高风险命令的二次身份确认。
//
// 计划 §19.9 要求 step-up MFA；本平台 MFA/TOTP 仍为 NOT_IMPLEMENTED（AUTH-004），
// 因此这里实现的是「重新提交当前账号密码」的等价控制，并在审计中记录
// step_up_method=password。任何情况下都不记录密码本身，只记录校验结果与时间。
func (s *Server) s6StepUp(r *http.Request, password string) error {
	p := PrincipalFrom(r.Context())
	if !p.Authenticated || p.IdentityID == "" {
		return errS6StepUpRequired
	}
	if s.iam == nil {
		return errS6StepUpDisabled
	}
	if strings.TrimSpace(password) == "" {
		return errS6StepUpRequired
	}
	hash, err := s.iam.Repo().PasswordHash(r.Context(), p.IdentityID)
	if err != nil {
		return errS6StepUpDisabled
	}
	ok, _, err := iam.VerifyPassword(hash, password)
	if err != nil {
		return errS6StepUpDisabled
	}
	if !ok {
		return errS6StepUpInvalid
	}
	return nil
}

// s6StepUpOrFail 校验 step-up 失败时写出稳定错误码并返回 false。
func (s *Server) s6StepUpOrFail(w http.ResponseWriter, r *http.Request, password, action string) bool {
	err := s.s6StepUp(r, password)
	p := PrincipalFrom(r.Context())
	switch {
	case err == nil:
		s.s6Audit(r, action+".step_up", "admin.control", "identity", p.IdentityID, map[string]any{
			"method": "password", "result": "success",
		})
		return true
	case errors.Is(err, errS6StepUpRequired):
		respondError(w, r, http.StatusForbidden, "STEP_UP_REQUIRED",
			"该操作需要二次身份确认，请重新输入当前账号密码", map[string]any{"step_up_method": "password"})
	case errors.Is(err, errS6StepUpInvalid):
		s.s6Audit(r, action+".step_up", "admin.control", "identity", p.IdentityID, map[string]any{
			"method": "password", "result": "failure",
		})
		respondError(w, r, http.StatusForbidden, "STEP_UP_INVALID", "二次身份确认失败，请重新输入", nil)
	default:
		respondError(w, r, http.StatusServiceUnavailable, "STEP_UP_UNAVAILABLE",
			"二次身份确认当前不可用，请重新登录后重试", nil)
	}
	return false
}

// s6Audit 写入一条管理审计事件（同步；失败只记日志，不阻断已完成的写操作）。
func (s *Server) s6Audit(r *http.Request, action, category, resourceType, resourceID string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	ev := auditEventFor(r, PrincipalFrom(r.Context()), metaFrom(r.Context()), action, category, resourceType, resourceID, "success", detail)
	if err := s.audit.Write(r.Context(), ev); err != nil {
		s.deps.Log.Error("audit write failed", "action", action, "err", err)
	}
}

// s6AuditChange 写入带 before/after 的管理审计事件。
func (s *Server) s6AuditChange(r *http.Request, action, category, resourceType, resourceID string, before, after any, detail map[string]any) {
	if s.audit == nil {
		return
	}
	ev := auditEventFor(r, PrincipalFrom(r.Context()), metaFrom(r.Context()), action, category, resourceType, resourceID, "success", detail)
	ev.Before = before
	ev.After = after
	ev.Changes = map[string]any{"before": before, "after": after}
	if err := s.audit.Write(r.Context(), ev); err != nil {
		s.deps.Log.Error("audit write failed", "action", action, "err", err)
	}
}

// s6AuditWithApproval 写入带审批引用的审计事件（双人流程用）。
func (s *Server) s6AuditWithApproval(r *http.Request, action, category, resourceType, resourceID, approvalID string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	ev := auditEventFor(r, PrincipalFrom(r.Context()), metaFrom(r.Context()), action, category, resourceType, resourceID, "success", detail)
	ev.ApprovalID = approvalID
	ev.SecondConfirm = true
	ev.RequiresReview = true
	if err := s.audit.Write(r.Context(), ev); err != nil {
		s.deps.Log.Error("audit write failed", "action", action, "err", err)
	}
}

// ---- 双人审批编排（approval_requests：0042 定义 + 0051 扩展） ----

type s6Approval struct {
	ID                  string         `json:"id"`
	Operation           string         `json:"operation"`
	TargetType          string         `json:"target_type"`
	TargetID            string         `json:"target_id"`
	Status              string         `json:"status"`
	Payload             map[string]any `json:"payload"`
	RequestedBy         string         `json:"requested_by"`
	RequestedByEmail    string         `json:"requested_by_email"`
	RequestedAt         time.Time      `json:"requested_at"`
	DecidedBy           string         `json:"decided_by,omitempty"`
	DecidedByEmail      string         `json:"decided_by_email,omitempty"`
	DecidedAt           *time.Time     `json:"decided_at,omitempty"`
	DecisionNote        string         `json:"decision_note"`
	ExecutedAt          *time.Time     `json:"executed_at,omitempty"`
	ExpiresAt           *time.Time     `json:"expires_at,omitempty"`
	Result              map[string]any `json:"result,omitempty"`
	RequesterCanApprove bool           `json:"requester_can_approve"`
}

const s6ApprovalSelect = `SELECT a.id::text, a.operation, a.target_type, a.target_id, a.status,
       coalesce(a.payload, '{}'::jsonb),
       a.requested_by::text, coalesce(ri.email, ''), a.requested_at,
       coalesce(a.decided_by::text, ''), coalesce(di.email, ''),
       a.decided_at, a.decision_note, a.executed_at, a.expires_at, a.result
  FROM approval_requests a
  LEFT JOIN identities ri ON ri.id = a.requested_by
  LEFT JOIN identities di ON di.id = a.decided_by`

func scanS6Approval(row pgx.Row) (s6Approval, error) {
	var a s6Approval
	var payload []byte
	var result []byte
	err := row.Scan(&a.ID, &a.Operation, &a.TargetType, &a.TargetID, &a.Status, &payload,
		&a.RequestedBy, &a.RequestedByEmail, &a.RequestedAt,
		&a.DecidedBy, &a.DecidedByEmail, &a.DecidedAt, &a.DecisionNote, &a.ExecutedAt, &a.ExpiresAt, &result)
	if err != nil {
		return s6Approval{}, err
	}
	a.RequesterCanApprove = false
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &a.Payload)
	}
	if len(result) > 0 {
		_ = json.Unmarshal(result, &a.Result)
	}
	return a, nil
}

// s6LoadApproval 读取单条审批请求。
func (s *Server) s6LoadApproval(ctx context.Context, id string) (s6Approval, error) {
	if _, err := parseS6UUID(id); err != nil {
		return s6Approval{}, pgx.ErrNoRows
	}
	return scanS6Approval(s.deps.Store.Pool.QueryRow(ctx, s6ApprovalSelect+" WHERE a.id = $1", id))
}

// s6ListApprovals 列出审批请求（operation 为空表示全部）。
func (s *Server) s6ListApprovals(ctx context.Context, operation string, limit int) ([]s6Approval, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.deps.Store.Pool.Query(ctx, s6ApprovalSelect+
		" WHERE ($1 = '' OR a.operation = $1) ORDER BY a.requested_at DESC LIMIT $2", operation, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]s6Approval, 0, limit)
	for rows.Next() {
		item, err := scanS6Approval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// s6CreateApproval 创建待审批请求。双人约束由 0051 的 CHECK 兜底。
func (s *Server) s6CreateApproval(ctx context.Context, operation, targetType, targetID string, payload map[string]any,
	requesterID, requesterEmail string, ttl time.Duration) (s6Approval, error) {
	p := PrincipalFrom(ctx)
	orgID := any(nil)
	if p.OrganizationID != "" {
		orgID = p.OrganizationID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return s6Approval{}, err
	}
	var id string
	err = s.deps.Store.Pool.QueryRow(ctx, `
		INSERT INTO approval_requests (organization_id, operation, target_type, target_id, payload, requested_by, expires_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, now() + ($7 || ' seconds')::interval)
		RETURNING id::text`, orgID, operation, targetType, targetID, string(body), requesterID,
		strconv.Itoa(int(ttl.Seconds()))).Scan(&id)
	if err != nil {
		return s6Approval{}, err
	}
	_ = s.s6ApprovalEvent(ctx, id, "requested", requesterID, requesterEmail, "", map[string]any{
		"operation": operation, "target_type": targetType, "target_id": targetID,
	})
	return s.s6LoadApproval(ctx, id)
}

// s6ApprovalEvent 记录审批流程事件（requested / approved / rejected / executed / restored / failed）。
func (s *Server) s6ApprovalEvent(ctx context.Context, approvalID, event, actorID, actorEmail, note string, detail map[string]any) error {
	body, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	actor := any(nil)
	if actorID != "" {
		actor = actorID
	}
	_, err = s.deps.Store.Pool.Exec(ctx, `
		INSERT INTO approval_request_events (approval_request_id, event, actor_id, actor_email, note, detail)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)`, approvalID, event, actor, actorEmail, note, string(body))
	return err
}

// s6DecideApproval 记录审批决定；同一请求的请求人不能审批自己的请求（§19.9）。
func (s *Server) s6DecideApproval(ctx context.Context, id, decision, note, actorID, actorEmail string) (s6Approval, error) {
	current, err := s.s6LoadApproval(ctx, id)
	if err != nil {
		return s6Approval{}, err
	}
	if current.Status != "pending" {
		return current, fmt.Errorf("审批请求当前状态为 %s，不能重复决定", current.Status)
	}
	if current.ExpiresAt != nil && current.ExpiresAt.Before(time.Now()) {
		_, _ = s.deps.Store.Pool.Exec(ctx, "UPDATE approval_requests SET status='expired' WHERE id=$1 AND status='pending'", id)
		return current, errors.New("审批请求已过期")
	}
	if actorID == current.RequestedBy {
		return current, errS6TwoPerson
	}
	tag, err := s.deps.Store.Pool.Exec(ctx, `
		UPDATE approval_requests SET status=$2, decided_by=$3, decided_at=now(), decision_note=$4
		WHERE id=$1 AND status='pending' AND requested_by <> $3`, id, decision, actorID, note)
	if err != nil {
		return s6Approval{}, err
	}
	if tag.RowsAffected() == 0 {
		return current, errS6TwoPerson
	}
	_ = s.s6ApprovalEvent(ctx, id, decision, actorID, actorEmail, note, nil)
	return s.s6LoadApproval(ctx, id)
}

// s6FinishApproval 结束审批流程（executed / failed / cancelled / restored）。
func (s *Server) s6FinishApproval(ctx context.Context, id, status string, result map[string]any) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.deps.Store.Pool.Exec(ctx, `
		UPDATE approval_requests SET status=$2, result=$3::jsonb,
		       executed_at = CASE WHEN $2 IN ('executed','failed','restored') THEN now() ELSE executed_at END
		WHERE id=$1`, id, status, string(body))
	return err
}

// s6RequireApproved 校验审批已通过、操作匹配、未过期。
func (s *Server) s6RequireApproved(ctx context.Context, id, operation string) (s6Approval, error) {
	approval, err := s.s6LoadApproval(ctx, id)
	if err != nil {
		return s6Approval{}, err
	}
	if approval.Operation != operation {
		return approval, fmt.Errorf("审批请求操作不匹配：期望 %s，实际 %s", operation, approval.Operation)
	}
	if approval.Status != "approved" {
		return approval, fmt.Errorf("审批请求状态为 %s，需要已通过", approval.Status)
	}
	if approval.ExpiresAt != nil && approval.ExpiresAt.Before(time.Now()) {
		return approval, errors.New("审批请求已过期")
	}
	return approval, nil
}

// ---- 配置项与配置版本（configuration_settings / configuration_revisions，0051） ----

type s6Setting struct {
	Key         string    `json:"key"`
	Category    string    `json:"category"`
	ValueType   string    `json:"value_type"`
	Value       any       `json:"value"`
	SecretRef   string    `json:"secret_ref"`
	IsSecret    bool      `json:"is_secret"`
	Description string    `json:"description"`
	Revision    int64     `json:"revision"`
	UpdatedAt   time.Time `json:"updated_at"`
	Source      string    `json:"source"`
}

// s6LoadSetting 读取配置项；secret-ref 只暴露摘要引用，不返回值本体。
func (s *Server) s6LoadSetting(ctx context.Context, key string) (s6Setting, error) {
	var out s6Setting
	var raw []byte
	err := s.deps.Store.Pool.QueryRow(ctx, `
		SELECT key, category, value_type, value, secret_ref, is_secret, description, revision, updated_at
		FROM configuration_settings WHERE key=$1`, key).
		Scan(&out.Key, &out.Category, &out.ValueType, &raw, &out.SecretRef, &out.IsSecret, &out.Description, &out.Revision, &out.UpdatedAt)
	if err != nil {
		return s6Setting{}, err
	}
	_ = json.Unmarshal(raw, &out.Value)
	if out.IsSecret || out.ValueType == "secret-ref" {
		out.Value = nil
		out.SecretRef = s6MaskRef(out.SecretRef)
	}
	out.Source = "configuration_settings"
	return out, nil
}

// s6MaskRef 只保留引用名的可读前缀，避免完整环境变量名泄露部署结构。
func s6MaskRef(ref string) string {
	if ref == "" {
		return ""
	}
	if len(ref) <= 8 {
		return ref
	}
	return ref[:8] + "…"
}

// s6HashValue 计算配置值的稳定摘要（用于 before_hash / after_hash）。
func s6HashValue(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// s6WriteConfigRevision 记录配置变更历史（append-only）。
func (s *Server) s6WriteConfigRevision(ctx context.Context, key string, revision int64, value any, secretRef string, isSecret bool,
	beforeHash, afterHash, note, actorID, actorEmail, requestID, traceID, approvalID string) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	actor := any(nil)
	if actorID != "" {
		actor = actorID
	}
	approval := any(nil)
	if approvalID != "" {
		approval = approvalID
	}
	_, err = s.deps.Store.Pool.Exec(ctx, `
		INSERT INTO configuration_revisions (key, revision, value, secret_ref, is_secret, before_hash, after_hash,
		       change_note, actor_id, actor_email, request_id, trace_id, approval_request_id)
		VALUES ($1,$2,$3::jsonb,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (key, revision) DO NOTHING`,
		key, revision, string(body), secretRef, isSecret, beforeHash, afterHash, note, actor, actorEmail, requestID, traceID, approval)
	return err
}

// ---- 通用分页与解析辅助 ----

// s6ParseCursor 解析 "unix_micros:uuid" 形式的 cursor。
func s6ParseCursor(raw string) (time.Time, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, "", false
	}
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return time.Time{}, "", false
	}
	micros, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", false
	}
	return time.UnixMicro(micros).UTC(), parts[1], true
}

func s6MakeCursor(t time.Time, id string) string {
	return strconv.FormatInt(t.UTC().UnixMicro(), 10) + ":" + id
}

// s6Limit 归一化 limit 参数。
func s6Limit(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func s6NullUUID(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// parseS6UUID 校验 UUID 形式，避免把非法输入直接送到数据库。
func parseS6UUID(v string) (string, error) {
	if len(v) != 36 {
		return "", errors.New("invalid uuid")
	}
	for i, c := range v {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return "", errors.New("invalid uuid")
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return "", errors.New("invalid uuid")
			}
		}
	}
	return strings.ToLower(v), nil
}

// s6BoolSetting 读取布尔型配置项，缺失或类型不符时返回默认值。
func (s *Server) s6BoolSetting(ctx context.Context, key string, def bool) bool {
	var raw []byte
	if err := s.deps.Store.Pool.QueryRow(ctx, "SELECT value FROM configuration_settings WHERE key=$1", key).Scan(&raw); err != nil {
		return def
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return def
	}
	return v
}

// s6Now 统一 UTC 时间基准。
func s6Now() time.Time { return time.Now().UTC() }
