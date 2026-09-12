package casework

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Notification struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	TitleKey   string         `json:"title_key"`
	Title      string         `json:"title"`
	Body       string         `json:"body"`
	ObjectType string         `json:"object_type"`
	ObjectID   string         `json:"object_id"`
	Payload    map[string]any `json:"payload"`
	ReadAt     *time.Time     `json:"read_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Href       string         `json:"href"`
}

type WorkspaceMember struct {
	IdentityID  string   `json:"identity_id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	Roles       []string `json:"roles"`
}

func (s *Service) ListNotifications(ctx context.Context, actor Actor, unreadOnly bool, noticeType, cursor string, limit int) ([]Notification, *string, int, error) {
	if err := s.ready(); err != nil {
		return nil, nil, 0, err
	}
	p := actor.Principal
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{p.IdentityID}
	where := []string{"n.user_id=$1::uuid"}
	if unreadOnly {
		where = append(where, "n.read_at IS NULL")
	}
	if noticeType != "" {
		args = append(args, noticeType)
		where = append(where, fmt.Sprintf("n.type=$%d", len(args)))
	}
	if cursor != "" {
		t, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, nil, 0, validation("分页游标无效", nil)
		}
		args = append(args, t, id)
		where = append(where, fmt.Sprintf("(n.created_at,n.id) < ($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
        SELECT n.id::text,n.type,n.title_key,n.title,n.body,n.object_type,n.object_id,n.payload,n.read_at,n.created_at
        FROM notifications n WHERE %s ORDER BY n.created_at DESC,n.id DESC LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, nil, 0, err
	}
	defer rows.Close()
	items := []Notification{}
	for rows.Next() {
		var n Notification
		var payload []byte
		if err := rows.Scan(&n.ID, &n.Type, &n.TitleKey, &n.Title, &n.Body, &n.ObjectType, &n.ObjectID, &payload, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, nil, 0, err
		}
		_ = jsonUnmarshal(payload, &n.Payload)
		if n.Payload == nil {
			n.Payload = map[string]any{}
		}
		n.Href = hrefForObject(n.ObjectType, n.ObjectID)
		items = append(items, n)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := encodeCursor(items[len(items)-1].CreatedAt, items[len(items)-1].ID)
		next = &c
	}
	var total int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1::uuid`, p.IdentityID).Scan(&total)
	return items, next, total, rows.Err()
}

func (s *Service) MarkNotificationRead(ctx context.Context, actor Actor, notificationID string) (Notification, error) {
	if err := s.ready(); err != nil {
		return Notification{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Notification{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var n Notification
	var payload []byte
	err = tx.QueryRow(ctx, `
        UPDATE notifications SET read_at=coalesce(read_at,now()) WHERE id=$1::uuid AND user_id=$2::uuid
        RETURNING id::text,type,title_key,title,body,object_type,object_id,payload,read_at,created_at`,
		notificationID, actor.Principal.IdentityID).
		Scan(&n.ID, &n.Type, &n.TitleKey, &n.Title, &n.Body, &n.ObjectType, &n.ObjectID, &payload, &n.ReadAt, &n.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Notification{}, notFound()
	}
	if err != nil {
		return Notification{}, err
	}
	if err := s.auditTx(ctx, tx, actor, "notification:update", "notification", "notification", notificationID, "success", map[string]any{}); err != nil {
		return Notification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Notification{}, err
	}
	_ = jsonUnmarshal(payload, &n.Payload)
	n.Href = hrefForObject(n.ObjectType, n.ObjectID)
	return n, nil
}

func (s *Service) MarkAllNotificationsRead(ctx context.Context, actor Actor) (int, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE user_id=$1::uuid AND read_at IS NULL`, actor.Principal.IdentityID)
	if err != nil {
		return 0, err
	}
	_ = s.writeAuditAsync(ctx, actor, "notification:update", "notification", "notification", "read_all", "low", map[string]any{"affected": tag.RowsAffected()})
	return int(tag.RowsAffected()), nil
}

// ResolveNotificationTarget 在跳转前做对象级权限校验；无权时返回 404（安全失败）。
func (s *Service) ResolveNotificationTarget(ctx context.Context, actor Actor, notificationID string) (Notification, error) {
	n, err := s.markReadAndGet(ctx, actor, notificationID)
	return n, err
}

func (s *Service) markReadAndGet(ctx context.Context, actor Actor, notificationID string) (Notification, error) {
	return s.MarkNotificationRead(ctx, actor, notificationID)
}

func hrefForObject(objectType, objectID string) string {
	if objectID == "" {
		return ""
	}
	switch objectType {
	case "case":
		return "/cases/" + objectID + "/overview"
	case "investigation":
		return "/investigations/" + objectID + "/workbench"
	case "finding":
		return "/findings/" + objectID
	case "report":
		return "/reports/" + objectID
	case "evidence":
		return "/evidence/" + objectID
	default:
		return ""
	}
}

// ListWorkspaceMembers 为案件成员选择器提供最小用户目录（限当前组织/工作区）。
func (s *Service) ListWorkspaceMembers(ctx context.Context, actor Actor, search string, limit int) ([]WorkspaceMember, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	p := actor.Principal
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{p.OrganizationID, p.WorkspaceID}
	where := []string{"identities.organization_id=$1::uuid", "identities.status='active'",
		"EXISTS (SELECT 1 FROM workspace_memberships wm WHERE wm.identity_id=identities.id AND wm.workspace_id=$2::uuid AND wm.status='active')"}
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		where = append(where, fmt.Sprintf("(lower(identities.email) LIKE $%d OR lower(identities.display_name) LIKE $%d)", len(args), len(args)))
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
        SELECT identities.id::text,identities.email,identities.display_name,identities.status
        FROM identities WHERE %s ORDER BY identities.display_name LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkspaceMember{}
	for rows.Next() {
		var m WorkspaceMember
		if err := rows.Scan(&m.IdentityID, &m.Email, &m.DisplayName, &m.Status); err != nil {
			return nil, err
		}
		m.Roles = []string{}
		out = append(out, m)
	}
	return out, rows.Err()
}

var _ = http.StatusOK
