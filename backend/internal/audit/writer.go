package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
)

// Event 是审计 v2 的写入模型。字段与 0042_audit_v2.sql 一一对应。
//
// 禁止放入明文秘密：密码、验证码、Access/Refresh Token、Cookie、私钥、
// API Key、完整 Authorization 头。需要关联时只写不可逆摘要。
type Event struct {
	Environment string
	ActorType   string // user | system | api_client | anonymous
	ActorID     string
	ActorEmail  string
	ActorName   string
	ActorRoles  []string

	OrganizationID string
	WorkspaceID    string
	SessionID      string
	DeviceID       string

	Action          string
	Category        string
	ResourceType    string
	ResourceID      string
	CaseID          string
	InvestigationID string
	JobID           string
	ReportID        string

	HTTPMethod         string
	Route              string
	RequestID          string
	TraceID            string
	IdempotencyKeyHash string
	ClientVersion      string

	Result       string // allow | deny | success | failure | error
	HTTPStatus   int
	ErrorCode    string
	DurationMS   int
	ResultCount  int64
	AffectedRows int

	PermissionKey  string
	PolicyDecision string
	DenyReason     string
	Sensitive      bool
	RiskLevel      string
	ApprovalID     string
	SecondConfirm  bool
	RequiresReview bool

	Before  any
	After   any
	Changes any
	Detail  map[string]any

	PeerIP      string
	ClientIP    string
	ProxyChain  []string
	UserAgent   string
	DataSource  string
	ContentHash string
	ObjectRef   string

	OccurredAt time.Time
}

// Writer 负责把审计事件写入 audit_events_v2 并推进哈希链。
// Pool 为 nil 时退化为 no-op，让无数据库的单元测试可以复用同一调用点。
type Writer struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	env  string
}

func NewWriter(pool *pgxpool.Pool, env string, log *slog.Logger) *Writer {
	if log == nil {
		log = slog.Default()
	}
	return &Writer{pool: pool, log: log, env: env}
}

// Enabled 表示审计是否真正落库（fail-closed 判断用）。
func (w *Writer) Enabled() bool { return w != nil && w.pool != nil }

// Write 写入一条审计事件。
//
// 默认在独立事务中写入：审计行不加入业务事务，业务回滚不会带走自己的拒绝行
// （api.emitAudit 通过 dbtx.WithoutTx 保证这一点）。
//
// 唯一的例外是 **commit gate 请求内**（dbtx.IsGateRequest）：那种请求的整个 handler
// 都跑在一个事务里，而该事务里的同事务审计写入（auditTx -> WriteTx -> ChainAppend）
// 已经持有 audit_chain_heads 的行锁。此时若再另开一条连接去写审计，就会与自己的请求
// 事务互等——请求要等这条审计写完才能返回，请求事务却要等 handler 返回才能提交，
// 直到 HTTP 超时（60s）才解开，而这条审计被静默丢弃。实测见
// acceptance-evidence/dual-system-v1/ROUND-20260916-defect-closure.md（D-15）。
// 因此 gate 请求内的审计写入加入请求事务：同一个事务、同一把锁，不再等待。
func (w *Writer) Write(ctx context.Context, e Event) error {
	if !w.Enabled() {
		return nil
	}
	if tx := dbtx.FromContext(ctx); tx != nil && dbtx.IsGateRequest(ctx) {
		return w.WriteTx(ctx, tx, e)
	}
	started := time.Now()
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		recordWrite(ChainAudit, err, time.Since(started))
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := w.WriteTx(ctx, tx, e); err != nil {
		recordWrite(ChainAudit, err, time.Since(started))
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		recordWrite(ChainAudit, err, time.Since(started))
		return err
	}
	recordWrite(ChainAudit, nil, time.Since(started))
	return nil
}

// WriteTx 在调用方事务内写入，保证业务变更与审计同时提交或同时回滚。
func (w *Writer) WriteTx(ctx context.Context, tx DB, e Event) error {
	if !w.Enabled() {
		return nil
	}
	// PostgreSQL 的 timestamptz 只有微秒精度，会做四舍五入。
	// 哈希载荷必须使用与落库后完全一致的微秒值，否则校验永远不通过。
	e.OccurredAt = e.OccurredAt.UTC().Truncate(time.Microsecond)
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	if e.Environment == "" {
		e.Environment = w.env
	}
	if e.ActorType == "" {
		e.ActorType = "user"
	}
	if e.Result == "" {
		e.Result = "success"
	}
	if e.RiskLevel == "" {
		e.RiskLevel = "low"
	}
	if e.ActorRoles == nil {
		e.ActorRoles = []string{}
	}
	if e.ProxyChain == nil {
		e.ProxyChain = []string{}
	}
	if e.Detail == nil {
		e.Detail = map[string]any{}
	}

	payload, err := Canonicalize(w.chainPayload(e))
	if err != nil {
		return err
	}
	seq, prevHash, eventHash, err := ChainAppend(ctx, tx, ChainAudit, payload)
	if err != nil {
		return err
	}

	beforeJSON, err := jsonOrNil(e.Before)
	if err != nil {
		return err
	}
	afterJSON, err := jsonOrNil(e.After)
	if err != nil {
		return err
	}
	changesJSON, err := jsonOrNil(e.Changes)
	if err != nil {
		return err
	}
	detailJSON, err := marshalJSON(e.Detail)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
        INSERT INTO audit_events_v2 (
            id, chain, seq, occurred_at, event_version, environment,
            actor_type, actor_id, actor_email, actor_display_name, actor_roles,
            organization_id, workspace_id, session_id, device_id,
            action, category, resource_type, resource_id, case_id, investigation_id, job_id, report_id,
            http_method, route_template, request_id, trace_id, idempotency_key_hash, client_version,
            result, http_status, error_code, duration_ms, result_count, affected_rows,
            permission_key, policy_decision, deny_reason, sensitive, risk_level,
            approval_id, second_confirm, requires_review,
            before, after, changes, detail,
            peer_ip, client_ip, proxy_chain, user_agent, data_source, content_hash, object_ref,
            prev_hash, event_hash
        ) VALUES (
            $1,$2,$3,$4,$5,$6,
            $7,$8,$9,$10,$11,
            $12,$13,$14,$15,
            $16,$17,$18,$19,$20,$21,$22,$23,
            $24,$25,$26,$27,$28,$29,
            $30,$31,$32,$33,$34,$35,
            $36,$37,$38,$39,$40,
            $41,$42,$43,
            $44,$45,$46,$47,
            $48,$49,$50,$51,$52,$53,$54,
            $55,$56
        )`,
		uuid.NewString(), ChainAudit, seq, e.OccurredAt, 1, e.Environment,
		e.ActorType, nullUUID(e.ActorID), e.ActorEmail, e.ActorName, e.ActorRoles,
		nullUUID(e.OrganizationID), nullUUID(e.WorkspaceID), nullUUID(e.SessionID), nullUUID(e.DeviceID),
		e.Action, e.Category, e.ResourceType, e.ResourceID, nullUUID(e.CaseID), nullUUID(e.InvestigationID), nullUUID(e.JobID), nullUUID(e.ReportID),
		e.HTTPMethod, e.Route, e.RequestID, e.TraceID, e.IdempotencyKeyHash, e.ClientVersion,
		e.Result, e.HTTPStatus, e.ErrorCode, e.DurationMS, e.ResultCount, e.AffectedRows,
		nullText(e.PermissionKey), e.PolicyDecision, e.DenyReason, e.Sensitive, e.RiskLevel,
		nullUUID(e.ApprovalID), e.SecondConfirm, e.RequiresReview,
		beforeJSON, afterJSON, changesJSON, detailJSON,
		nullText(e.PeerIP), nullText(e.ClientIP), e.ProxyChain, e.UserAgent, e.DataSource, e.ContentHash, e.ObjectRef,
		prevHash, eventHash,
	)
	recordWrite(ChainAudit, err, 0)
	return err
}

// WriteOrLog 与 Write 相同，但写入失败时记录日志后再返回错误。
//
// 读路径的审计（evidence:sensitive:read、report:download 等）只能"业务成功 + 明确审计告警"，
// 不能因为调用方丢弃返回值而完全静默：2026-09-16 的 D-15 事故里，被 60s 超时打断的
// evidence:sensitive:read 就是这样整条消失的。
func (w *Writer) WriteOrLog(ctx context.Context, e Event) error {
	if err := w.Write(ctx, e); err != nil {
		w.log.Error("audit write failed", "action", e.Action, "category", e.Category, "err", err)
		return err
	}
	return nil
}

// WriteTxOrLog 用于"审计失败不得静默"的场景：返回错误让业务事务回滚。
// 对于允许降级的低风险读取审计，调用方可用 WriteAsync 明确降级并计数。
func (w *Writer) WriteTxOrLog(ctx context.Context, tx DB, e Event) error {
	if err := w.WriteTx(ctx, tx, e); err != nil {
		w.log.Error("audit write failed", "action", e.Action, "category", e.Category, "err", err)
		return err
	}
	return nil
}

// chainPayload 是参与哈希链计算的规范化载荷。
//
// 关键约束：这份载荷必须能由数据库中的同一行"重新推导"出来，否则校验时
// 无法复算（见 VerifyChain）。因此：
//   - 时间统一用 UnixMicro 整数，避免 RFC3339Nano 尾零裁剪造成两端不一致；
//   - 所有可空字段统一用空字符串而不是 NULL；
//   - before/after/changes/detail 存 jsonb，读回后重新规范化即可得到同样字节。
func (w *Writer) chainPayload(e Event) map[string]any {
	return ChainPayloadOf(map[string]any{
		"occurred_at_unix_micro": e.OccurredAt.UTC().UnixMicro(),
		"environment":            e.Environment,
		"actor_type":             e.ActorType,
		"actor_id":               UUIDPayloadValue(e.ActorID),
		"actor_email":            e.ActorEmail,
		"organization_id":        UUIDPayloadValue(e.OrganizationID),
		"workspace_id":           UUIDPayloadValue(e.WorkspaceID),
		"session_id":             UUIDPayloadValue(e.SessionID),
		"device_id":              UUIDPayloadValue(e.DeviceID),
		"action":                 e.Action,
		"category":               e.Category,
		"resource_type":          e.ResourceType,
		"resource_id":            e.ResourceID,
		"http_method":            e.HTTPMethod,
		"route_template":         e.Route,
		"request_id":             e.RequestID,
		"trace_id":               e.TraceID,
		"result":                 e.Result,
		"http_status":            e.HTTPStatus,
		"error_code":             e.ErrorCode,
		"permission_key":         e.PermissionKey,
		"policy_decision":        e.PolicyDecision,
		"deny_reason":            e.DenyReason,
		"sensitive":              e.Sensitive,
		"risk_level":             e.RiskLevel,
		"before":                 e.Before,
		"after":                  e.After,
		"changes":                e.Changes,
		"detail":                 e.Detail,
		"client_ip":              e.ClientIP,
		"user_agent":             e.UserAgent,
	})
}

// ChainPayloadOf 把 nil 归一化为稳定的可序列化值。
func ChainPayloadOf(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if v == nil {
			out[k] = nil
			continue
		}
		out[k] = v
	}
	return out
}

func marshalJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func jsonOrNil(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	if _, err := uuid.Parse(s); err != nil {
		return nil
	}
	return s
}

// UUIDPayloadValue 返回与「落库后再读回」逐字一致的字符串。
//
// 为什么必须有它：UUID 列落库走 nullUUID（非 UUID 文本 -> NULL），校验器读回时是
// COALESCE(col::text,”) -> ""。若哈希载荷使用未归一化的原始文本，就会出现
// 「写入时哈希了一个永远无法落库的值、校验时永远复算不出来」的永久链损坏。
// 2026-09-13 真实发生过：请求带了非 UUID 的工作区 id，两条 authorization.workspace_switched
// 事件的 workspace_id 落库为 NULL 而哈希载荷是 "not-a-uuid"（见 ADR-019）。
//
// 对合法 UUID 与空串，本函数是恒等映射——只在"无法落库"的输入上收敛到 ""。
func UUIDPayloadValue(s string) string {
	if s == "" {
		return ""
	}
	if _, err := uuid.Parse(s); err != nil {
		return ""
	}
	return s
}
