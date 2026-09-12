// Package casework 实现 Sprint 4「用户案件与调查闭环」的领域服务：
// 案件/成员/对象/动态/评论、Finding/Evidence 复核、报告审批与下载票据、
// 通知与复核 Inbox，以及 Investigation v2 compatibility（复用既有 runtime/artifacts）。
//
// 设计约束：
//   - 所有查询显式带 organization_id / workspace_id 条件（§22.6）；
//   - 写操作在事务内同时写业务数据、业务时间线与 audit_events_v2（§6.5 / AD-008/AD-014）；
//   - 状态转换使用显式 command，客户端不能通用 PATCH 任意写 status（§20.3）；
//   - revision / status_revision 冲突返回 412，不静默覆盖（§20.4 / USER-007 同类规则）。
package casework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/investigation"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/report"
)

// Actor 是服务层记录审计所需的请求主体快照。所有字段都来自服务端解析，
// 绝不接受来自请求体的 actor/role/organization（AD-002 / 禁止事项）。
type Actor struct {
	Principal policy.Principal
	RequestID string
	TraceID   string
	Route     string
	Method    string
	ClientIP  string
	PeerIP    string
	UserAgent string
}

// APIError 把领域错误映射为稳定的 HTTP 语义。
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *APIError) Error() string { return e.Message }

func apiErr(status int, code, message string, details map[string]any) *APIError {
	return &APIError{Status: status, Code: code, Message: message, Details: details}
}

// AsAPIError 将任意 error 转为 APIError；未知错误按 500 处理，不回显内部细节。
func AsAPIError(err error) *APIError {
	if err == nil {
		return nil
	}
	var api *APIError
	if errors.As(err, &api) {
		return api
	}
	return apiErr(http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", nil)
}

var (
	ErrNotFound = apiErr(http.StatusNotFound, "RESOURCE_NOT_FOUND", "对象不存在或无权访问", nil)
	ErrConflict = apiErr(http.StatusConflict, "CONFLICT_STATE", "当前状态不允许该操作", nil)
	ErrRevision = apiErr(http.StatusPreconditionFailed, "CONFLICT_REVISION", "数据版本已变化，请刷新后重试", nil)
	ErrGone     = apiErr(http.StatusGone, "RESOURCE_GONE", "该资源已失效", nil)
)

func notFound() *APIError { return ErrNotFound }
func conflict(msg string, details map[string]any) *APIError {
	return apiErr(http.StatusConflict, "CONFLICT_STATE", msg, details)
}
func revisionConflict() *APIError { return ErrRevision }
func validation(msg string, details map[string]any) *APIError {
	return apiErr(http.StatusBadRequest, "VALIDATION_INVALID_ARGUMENT", msg, details)
}
func forbidden(msg string, details map[string]any) *APIError {
	return apiErr(http.StatusForbidden, "AUTHZ_PERMISSION_DENIED", msg, details)
}

// Service 是 Sprint 4 领域服务的聚合入口。
type Service struct {
	pool    *pgxpool.Pool
	audit   *audit.Writer
	objects *rawstore.Store
	reports *report.Service
	invSvc  *investigation.Service
	runtime *investigation.Runtime
	preview func(context.Context, dataset.EnsureRequest) (*dataset.CoveragePreviewResponse, error)
}

func New(pool *pgxpool.Pool, auditWriter *audit.Writer, objects *rawstore.Store, reports *report.Service) *Service {
	return &Service{pool: pool, audit: auditWriter, objects: objects, reports: reports}
}

func (s *Service) WithInvestigation(svc *investigation.Service, runtime *investigation.Runtime) *Service {
	s.invSvc = svc
	s.runtime = runtime
	return s
}

func (s *Service) WithCoverage(fn func(context.Context, dataset.EnsureRequest) (*dataset.CoveragePreviewResponse, error)) *Service {
	s.preview = fn
	return s
}

func (s *Service) ready() error {
	if s == nil || s.pool == nil {
		return apiErr(http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "控制面存储不可用", nil)
	}
	return nil
}

// auditTx 在业务事务内追加审计 v2。审计失败返回错误以回滚业务（fail closed）。
func (s *Service) auditTx(ctx context.Context, tx pgx.Tx, actor Actor, action, category, resourceType, resourceID, result string, detail map[string]any) error {
	if s.audit == nil || !s.audit.Enabled() {
		return nil
	}
	event := audit.Event{
		ActorType: "user", ActorID: actor.Principal.IdentityID,
		ActorEmail: actor.Principal.Email, ActorName: actor.Principal.DisplayName,
		ActorRoles:     actor.Principal.Roles,
		OrganizationID: actor.Principal.OrganizationID, WorkspaceID: actor.Principal.WorkspaceID,
		SessionID: actor.Principal.SessionID, DeviceID: actor.Principal.DeviceID,
		Action: action, Category: category,
		ResourceType: resourceType, ResourceID: resourceID,
		HTTPMethod: actor.Method, Route: actor.Route,
		RequestID: actor.RequestID, TraceID: actor.TraceID,
		Result: result, PermissionKey: action, PolicyDecision: "allow",
		RiskLevel: policy.RiskOf(action), Sensitive: policy.Sensitive(action),
		ClientIP: actor.ClientIP, PeerIP: actor.PeerIP, UserAgent: actor.UserAgent,
		Detail: detail,
	}
	return s.audit.WriteTxOrLog(ctx, tx, event)
}

// appendActivityTx 写用户可理解的案件业务时间线（不替代审计中心）。
func (s *Service) appendActivityTx(ctx context.Context, tx pgx.Tx, actor Actor, caseID, eventType, summary, objectType, objectID string, detail map[string]any) error {
	_, err := tx.Exec(ctx, `
        INSERT INTO case_activity (case_id, event_type, actor_identity_id, actor_display, summary, object_type, object_id, detail, trace_id)
        VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9)`,
		caseID, eventType, actor.Principal.IdentityID, actor.Principal.DisplayName, summary, objectType, objectID, mustJSON(detail), actor.TraceID)
	return err
}

// notifyTx 写入站内通知，并记录 inbox 送达尝试。
func (s *Service) notifyTx(ctx context.Context, tx pgx.Tx, recipientID, orgID, wsID, noticeType, titleKey, title, body, objectType, objectID string, payload map[string]any) error {
	if recipientID == "" {
		return nil
	}
	var id string
	if err := tx.QueryRow(ctx, `
        INSERT INTO notifications (user_id, organization_id, workspace_id, type, title_key, title, body, object_type, object_id, payload)
        VALUES ($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`,
		recipientID, orgID, wsID, noticeType, titleKey, title, body, objectType, objectID, mustJSON(payload)).Scan(&id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO notification_delivery_attempts (notification_id, channel, status) VALUES ($1,'inbox','delivered')`, id)
	return err
}

// reviewersWithPermissionTx 找出本组织内拥有指定权限的身份，用于复核通知。
func (s *Service) reviewersWithPermissionTx(ctx context.Context, tx pgx.Tx, organizationID, permission string) ([]string, error) {
	rows, err := tx.Query(ctx, `
        SELECT DISTINCT b.identity_id::text
        FROM member_role_bindings b
        JOIN roles r ON r.id = b.role_id
        JOIN role_permissions rp ON rp.role_id = r.id
        JOIN organization_memberships m ON m.identity_id = b.identity_id AND m.organization_id = $1::uuid AND m.status = 'active'
        WHERE rp.permission_key = $2
          AND b.revoked_at IS NULL
          AND (b.expires_at IS NULL OR b.expires_at > now())
          AND (b.scope_type = 'self' OR (b.scope_type = 'organization' AND b.scope_id = $1::uuid)
               OR (b.scope_type = 'workspace' AND b.scope_id IN (SELECT id FROM workspaces WHERE organization_id = $1::uuid)))`,
		organizationID, permission)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Service) resolveWorkspace(ctx context.Context, q queryRower, organizationID, workspaceID string) (string, error) {
	if workspaceID != "" {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces WHERE id=$1::uuid AND organization_id=$2::uuid)`, workspaceID, organizationID).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return "", forbidden("当前工作区不属于会话组织", nil)
		}
		return workspaceID, nil
	}
	var id string
	err := q.QueryRow(ctx, `
        SELECT id::text FROM workspaces WHERE organization_id=$1::uuid
        ORDER BY (slug='default') DESC, slug LIMIT 1`, organizationID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", notFound()
	}
	return id, err
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func mustJSON(v any) []byte {
	if v == nil {
		return []byte("{}")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func trimMax(v string, max int) string {
	v = strings.TrimSpace(v)
	if len([]rune(v)) > max {
		r := []rune(v)
		return string(r[:max])
	}
	return v
}

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func fmtInt(n int) string { return fmt.Sprintf("%d", n) }
