package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// Sprint 5（§28.7）用户层 Explorer/下载/任务/导出共享工具。
//
// 约束：
//   - cursor 是 opaque 的 base64url JSON，客户端不得解析；
//   - 所有列表先按服务端注入的 organization/workspace 作用域过滤；
//   - 任何跨作用域对象统一返回 404（不泄露存在性）。

var errBadCursor = errors.New("invalid cursor")

func encodeCursor(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(raw string, out any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return errBadCursor
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return errBadCursor
	}
	return nil
}

type timeIDCursor struct {
	TS string `json:"ts"`
	ID string `json:"id"`
}

type offsetCursor struct {
	Offset int `json:"o"`
}

type apiLimit struct {
	Limit int
}

func parseLimit(r *http.Request, def, max int) int {
	limit := def
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if max <= 0 {
		max = 200
	}
	if limit > max {
		limit = max
	}
	return limit
}

func parseOptionalLimit(r *http.Request, def, max int) (int, error) {
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > max {
			return 0, errBadCursor
		}
		return n, nil
	}
	return def, nil
}

func scopeMatches(orgID, wsID *string, p policy.Principal) bool {
	if p.OrganizationID == "" || p.WorkspaceID == "" {
		return false
	}
	if orgID == nil || wsID == nil {
		return false
	}
	return *orgID == p.OrganizationID && *wsID == p.WorkspaceID
}

// requireJobScope 读取 job 并做对象级作用域校验；不可见统一 404（§4.2）。
func (s *Server) requireJobScope(r *http.Request, id string) (*model.DownloadJob, bool) {
	if s.deps.Store == nil {
		return nil, false
	}
	job, err := s.deps.Store.GetJob(r.Context(), id)
	if err != nil || job == nil {
		return nil, false
	}
	p := PrincipalFrom(r.Context())
	if !scopeMatches(job.OrganizationID, job.WorkspaceID, p) {
		return nil, false
	}
	// 用户 scope：默认只能访问自己创建的任务；拥有 job:list:workspace 的角色可看工作区任务。
	if p.Has("job:list:workspace") {
		return job, true
	}
	if job.CreatedByIdentity != nil && *job.CreatedByIdentity == p.IdentityID {
		return job, true
	}
	if job.CreatedBy != nil {
		var legacy *string
		if err := s.deps.Store.Pool.QueryRow(r.Context(),
			`SELECT legacy_user_id::text FROM identities WHERE id=$1`, p.IdentityID).Scan(&legacy); err == nil &&
			legacy != nil && *legacy == *job.CreatedBy {
			return job, true
		}
	}
	return nil, false
}

// requireExportScope 校验导出对象的作用域。
func (s *Server) requireExportScope(r *http.Request, id string) (map[string]any, bool) {
	if s.deps.Store == nil {
		return nil, false
	}
	p := PrincipalFrom(r.Context())
	var (
		exportID, status, format, objectRef, sha string
		rows                                     int64
		zip                                      bool
		orgID, wsID, jobID                       *string
		createdAt, updatedAt                     time.Time
		expiresAt                                *time.Time
		errorMsg                                 string
	)
	// R10 缺陷修复（2026-09-13）：原先没有 SELECT/Scan job_id，导致复用路径（export.reuse）的响应
	// 里没有 job_id，前端「导出任务已创建」弹窗显示 job_id —、地址数 0、Dataset —。
	// job_id 是 exports 表的真实列，且本查询已做 org/ws 作用域校验，属 scope-safe 返回。
	err := s.deps.Store.Pool.QueryRow(r.Context(), `
		SELECT id::text, status, format, zip, object_ref, rows, sha256, error,
		       organization_id::text, workspace_id::text, job_id::text, created_at, updated_at, expires_at
		FROM exports WHERE id=$1`, id).Scan(&exportID, &status, &format, &zip, &objectRef, &rows, &sha, &errorMsg, &orgID, &wsID, &jobID, &createdAt, &updatedAt, &expiresAt)
	if err != nil {
		return nil, false
	}
	if !scopeMatches(orgID, wsID, p) {
		return nil, false
	}
	downloadAvailable := status == "COMPLETED" && (expiresAt == nil || expiresAt.After(time.Now()))
	item := map[string]any{
		"id": exportID, "status": status, "format": format, "zip": zip,
		"object_ref": objectRef, "rows": rows, "sha256": sha, "error": errorMsg,
		"organization_id": orgID, "workspace_id": wsID, "job_id": jobID,
		"created_at": createdAt, "updated_at": updatedAt,
		"download_available": downloadAvailable,
	}
	if expiresAt != nil {
		item["expires_at"] = expiresAt
	}
	// DATA-008：对象缺失必须显式暴露，不能只凭 DB status=COMPLETED 宣称可下载。
	if downloadAvailable && objectRef != "" && s.deps.Raw != nil {
		// F-R10-BUCKET：导出对象可能在新桶或旧桶，存在性检查必须双读；
		// 非 404（权限/网络）同样按不可下载处理，绝不把故障当作“对象存在”。
		if _, loc, statErr := s.statExportObject(context.Background(), objectRef); statErr != nil {
			item["download_available"] = false
			item["object_missing"] = true
			item["action_required"] = "restore_minio_object_or_regenerate_export"
		} else {
			// D-R10V9-01：object_bucket 必须是**真实 MinIO 桶名**（与读/写 helper 的审计口径一致），
			// 逻辑角色另记 object_bucket_role。旧实现把逻辑角色写进 object_bucket，导致 API 响应
			// 与审计链出现过"export"这种无法与物理桶对齐的取值。
			item["object_bucket"] = loc.Name
			item["object_bucket_role"] = string(loc.Bucket)
		}
	}
	return item, true
}

func objectString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case *string:
		if t == nil {
			return ""
		}
		return *t
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// jobFailureKind 把后端状态收敛为用户可理解分类（原始 Provider 错误只进诊断详情）。
func jobFailureKind(err string) string {
	e := strings.ToLower(err)
	switch {
	case e == "":
		return ""
	case strings.Contains(e, "timeout") || strings.Contains(e, "deadline") || strings.Contains(e, "i/o"):
		return "timeout"
	case strings.Contains(e, "provider") || strings.Contains(e, "rpc") || strings.Contains(e, "ankr"):
		return "provider"
	case strings.Contains(e, "coverage") || strings.Contains(e, "range"):
		return "data_gap"
	case strings.Contains(e, "memory") || strings.Contains(e, "oom"):
		return "resource"
	default:
		return "internal"
	}
}

// pgxNotFound 便于把 pgx.ErrNoRows 与普通错误区分。
func pgxNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
