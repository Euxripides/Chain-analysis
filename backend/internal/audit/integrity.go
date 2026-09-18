package audit

// Sprint 6 审计修复（阻断项）：不可恢复审计缺口的「不可变完整性事件边界」。
//
// 背景：验收环境上 AUDIT-003 的负向用例真实执行了 TRUNCATE audit_events_v2，
// 清空 chain='audit' 的 seq 1..2343，且不存在任何可用恢复源（无基备、无 WAL 归档、
// 无文件系统快照、MinIO 只保存每日 anchor.json、ClickHouse 无审计镜像、
// 所有 audit_exports 的 from_seq 均 >= 2344）。
//
// 本文件实现的方向**不是**"修复历史"，而是如实封存事实：
//   1. 缺口检测（DetectGap）：明确观测到的最小幸存 seq、缺口区间，以及幸存首行的
//      prev_hash —— 那正是被销毁的最后一条事件的 event_hash，是旧链尾唯一保留的
//      密码学证据，也是新旧链的衔接点；
//   2. 封存（SealIncident）：写入不可变事件记录，引用保留的旧锚点（原样不动）；
//   3. 边界感知校验（VerifyChain）：从 trusted_from_seq 起、以 sealed_tail_hash 为
//      起点逐行复算，使边界之后的链逐行可验证；
//   4. 锚点逐项验证（VerifyAnchors）：边界之前的锚点标注为封存不可恢复并核对其
//      MinIO 归档对象哈希，边界之后的锚点整段复算并比对 head_hash。
//
// 明确的禁止项（本包不提供任何入口）：
//   - 不写入、不回填、不伪造缺口区间内的 audit_events_v2 行；
//   - 不修改、不删除 audit_chain_anchors 既有行；
//   - 不把 NOT_VERIFIED 表述为 PASS：封存后 integrity_status 永远不是 "intact"。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/jackc/pgx/v5"
)

// 完整性状态取值。任何一条链在任何时刻只能是其中之一。
const (
	IntegrityIntact           = "intact"            // 无缺口，全链可验证
	IntegritySealedIncident   = "sealed_incident"   // 缺口已封存，边界之后可验证
	IntegrityUndeclaredGap    = "undeclared_gap"    // 观测到缺口但尚未封存（不合法状态）
	IntegrityDeclaredEvents   = "declared_events"   // 无缺口，但存在逐条声明的完整性事实（ADR-019）
	IntegrityBoundaryConflict = "boundary_conflict" // 声明边界与实际数据矛盾（疑似回填）
)

// 缺口前缀状态。
const (
	PrefixIntact              = "intact"
	PrefixSealedUnrecoverable = "sealed_unrecoverable"
	PrefixUnsealedGap         = "unsealed_gap"
)

// 锚点处置结论。
const (
	AnchorVerified            = "verified"
	AnchorSealedUnrecoverable = "sealed_unrecoverable"
	AnchorSpansBoundary       = "spans_boundary"
	AnchorBroken              = "broken"
	// AnchorVerifiedLegacy 表示锚点用 0055 之前的派生方式（区间内 max(event_hash)）记录
	// head_hash；内容仍可复算且一致，但其承诺强度弱于规范的链头哈希，因此单独标注。
	AnchorVerifiedLegacy = "verified_legacy_derivation"
)

// 锚点 head_hash 派生方式。
const (
	DerivationChainHead     = "chain_head"
	DerivationLegacyMaxHash = "legacy_max_hash"
)

var (
	// ErrNoGapDetected 表示当前链没有可封存的前缀缺口。
	ErrNoGapDetected = errors.New("no_unrecoverable_gap_detected")
	// ErrInternalGap 表示幸存区间内部存在断层，属于真正的篡改，不允许用边界掩盖。
	ErrInternalGap = errors.New("internal_gap_in_surviving_segment")
)

// ObjectGetter 是完整性边界所需的最小对象存储读取接口（rawstore.Store 满足）。
// 定义成接口是为了让 audit 包不反向依赖 rawstore。
type ObjectGetter interface {
	OpenNamed(ctx context.Context, bucket, key string) (io.ReadCloser, int64, error)
}

// ObservedGap 是对一条链「当前真实数据形态」的直接观测结果。
type ObservedGap struct {
	Chain             string    `json:"chain"`
	Table             string    `json:"table"`
	HasPrefixGap      bool      `json:"has_prefix_gap"`
	GapFromSeq        int64     `json:"gap_from_seq"`
	GapToSeq          int64     `json:"gap_to_seq"`
	GapEventCount     int64     `json:"gap_event_count"`
	FirstSurvivingSeq int64     `json:"first_surviving_seq"`
	SealedTailHash    string    `json:"sealed_tail_hash"`
	SealedTailSource  string    `json:"sealed_tail_source"`
	HeadSeq           int64     `json:"head_seq"`
	HeadHash          string    `json:"head_hash"`
	RowCount          int64     `json:"row_count"`
	InternalGapCount  int64     `json:"internal_gap_count"`
	RowsInsideGap     int64     `json:"rows_inside_sealed_gap"`
	Sealed            bool      `json:"sealed"`
	IncidentRef       string    `json:"incident_ref,omitempty"`
	DetectedAt        time.Time `json:"detected_at"`
}

// IncidentAnchor 是边界引用的保留锚点（只读引用，不复制、不改写源行）。
type IncidentAnchor struct {
	Chain         string    `json:"chain"`
	AnchorDate    string    `json:"anchor_date"`
	FromSeq       int64     `json:"from_seq"`
	ToSeq         int64     `json:"to_seq"`
	HeadHash      string    `json:"head_hash"`
	EventCount    int64     `json:"event_count"`
	ObjectRef     string    `json:"object_ref"`
	ContentHash   string    `json:"content_hash"`
	ArchiveHash   string    `json:"archive_hash"`
	ArchiveIntact bool      `json:"archive_intact"`
	Disposition   string    `json:"disposition"`
	CreatedAt     time.Time `json:"created_at"`
}

// Incident 是已封存的完整性事件（不可变）。
type Incident struct {
	ID                string           `json:"id"`
	IncidentRef       string           `json:"incident_ref"`
	Chain             string           `json:"chain"`
	Kind              string           `json:"kind"`
	GapFromSeq        int64            `json:"gap_from_seq"`
	GapToSeq          int64            `json:"gap_to_seq"`
	GapEventCount     int64            `json:"gap_event_count"`
	TrustedFromSeq    int64            `json:"trusted_from_seq"`
	SealedTailHash    string           `json:"sealed_tail_hash"`
	SealedTailSource  string           `json:"sealed_tail_source"`
	DetectionMethod   string           `json:"detection_method"`
	DetectedAt        time.Time        `json:"detected_at"`
	DetectedByEmail   string           `json:"detected_by_email"`
	SealedAt          time.Time        `json:"sealed_at"`
	SealedBy          string           `json:"sealed_by,omitempty"`
	SealedByEmail     string           `json:"sealed_by_email"`
	Reason            string           `json:"reason"`
	ApprovalRequestID string           `json:"approval_request_id,omitempty"`
	Evidence          map[string]any   `json:"evidence"`
	EvidenceHash      string           `json:"evidence_hash"`
	RetainedAnchors   []IncidentAnchor `json:"retained_anchors"`
	// DeclaredEvents 仅对 kind='declared_integrity_events' 非空：逐条声明的
	// 稀疏事实（缺失行 / 内容哈希不可复算行）与其链上哈希证据。
	DeclaredEvents []DeclaredEvent `json:"declared_events"`
	AlreadySealed  bool            `json:"already_sealed"`
}

// SealedBoundaryRef 是校验结果里对边界的精简引用（不含大字段）。
type SealedBoundaryRef struct {
	IncidentRef    string    `json:"incident_ref"`
	Chain          string    `json:"chain"`
	GapFromSeq     int64     `json:"gap_from_seq"`
	GapToSeq       int64     `json:"gap_to_seq"`
	GapEventCount  int64     `json:"gap_event_count"`
	TrustedFromSeq int64     `json:"trusted_from_seq"`
	SealedTailHash string    `json:"sealed_tail_hash"`
	EvidenceHash   string    `json:"evidence_hash"`
	SealedAt       time.Time `json:"sealed_at"`
	Statement      string    `json:"statement"`
}

// AnchorVerification 是单个每日锚点的逐项验证结论。
type AnchorVerification struct {
	Chain              string `json:"chain"`
	AnchorDate         string `json:"anchor_date"`
	FromSeq            int64  `json:"from_seq"`
	ToSeq              int64  `json:"to_seq"`
	HeadHash           string `json:"head_hash"`
	EventCount         int64  `json:"event_count"`
	ObjectRef          string `json:"object_ref"`
	ContentHash        string `json:"content_hash"`
	Disposition        string `json:"disposition"`
	Verified           bool   `json:"verified"`
	RecomputedHead     string `json:"recomputed_head_hash"`
	Checked            int64  `json:"checked"`
	Reason             string `json:"reason"`
	ArchiveObjectHash  string `json:"archive_object_hash"`
	ArchiveIntact      bool   `json:"archive_intact"`
	HeadHashDerivation string `json:"head_hash_derivation"`
	IncidentRef        string `json:"incident_ref,omitempty"`
}

// IntegrityReport 是 /audit/integrity 的完整输出。
type IntegrityReport struct {
	Chain               string               `json:"chain"`
	Observed            ObservedGap          `json:"observed"`
	Incident            *Incident            `json:"incident"`
	IntegrityStatus     string               `json:"integrity_status"`
	PrefixStatus        string               `json:"prefix_status"`
	ChainVerification   VerifyChainResult    `json:"chain_verification"`
	Anchors             []AnchorVerification `json:"anchors"`
	AnchorsVerified     int                  `json:"anchors_verified_count"`
	AnchorsLegacy       int                  `json:"anchors_legacy_derivation_count"`
	AnchorsSealed       int                  `json:"anchors_sealed_count"`
	AnchorsUnexplained  int                  `json:"anchors_unexplained_count"`
	AllAnchorsAccounted bool                 `json:"all_anchors_accounted"`
	TruthStatement      string               `json:"truth_statement"`
	AsOf                time.Time            `json:"as_of"`
}

// ---- 缺口检测 ----

// DetectGap 直接观测一条链的当前数据形态，不依赖任何声明。
func DetectGap(ctx context.Context, pool *dbtx.Pool, chain string) (ObservedGap, error) {
	reader, ok := chainReaders[chain]
	if !ok {
		return ObservedGap{}, fmt.Errorf("unknown chain %q", chain)
	}
	out := ObservedGap{Chain: chain, Table: reader.table, DetectedAt: time.Now().UTC()}
	err := pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT count(*), COALESCE(min(seq),0), COALESCE(max(seq),0) FROM %s WHERE chain = $1`,
		reader.table), chain).Scan(&out.RowCount, &out.FirstSurvivingSeq, &out.HeadSeq)
	if err != nil {
		return out, err
	}
	if out.RowCount == 0 {
		return out, nil
	}
	if out.FirstSurvivingSeq > 1 {
		out.HasPrefixGap = true
		out.GapFromSeq = 1
		out.GapToSeq = out.FirstSurvivingSeq - 1
		out.GapEventCount = out.GapToSeq // 链不变量：seq 从 1 起连续递增
		if err := pool.QueryRow(ctx, fmt.Sprintf(
			`SELECT prev_hash FROM %s WHERE chain = $1 AND seq = $2`, reader.table),
			chain, out.FirstSurvivingSeq).Scan(&out.SealedTailHash); err != nil {
			return out, err
		}
		out.SealedTailSource = fmt.Sprintf("%s.seq=%d.prev_hash（被销毁的最后一条事件的 event_hash）",
			reader.table, out.FirstSurvivingSeq)
	}
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT event_hash FROM %s WHERE chain = $1 AND seq = $2`, reader.table),
		chain, out.HeadSeq).Scan(&out.HeadHash); err != nil {
		return out, err
	}
	// 幸存区间内部断层 = 真正的篡改，不允许被边界掩盖。
	if span := out.HeadSeq - out.FirstSurvivingSeq + 1; span != out.RowCount {
		out.InternalGapCount = span - out.RowCount
	}
	// 只看缺口事件：稀疏声明不属于"边界"，不能拿它的区间做反回填计数。
	if inc, err := LoadGapIncident(ctx, pool, chain); err == nil && inc != nil {
		out.Sealed = true
		out.IncidentRef = inc.IncidentRef
		if err := pool.QueryRow(ctx, fmt.Sprintf(
			`SELECT count(*) FROM %s WHERE chain = $1 AND seq BETWEEN $2 AND $3`, reader.table),
			chain, inc.GapFromSeq, inc.GapToSeq).Scan(&out.RowsInsideGap); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ---- 事件读取 ----

const incidentSelect = `SELECT id::text, incident_ref, chain, kind, gap_from_seq, gap_to_seq,
       gap_event_count, trusted_from_seq, sealed_tail_hash, sealed_tail_source, detection_method,
       detected_at, detected_by_email, sealed_at, COALESCE(sealed_by::text,''), sealed_by_email,
       reason, COALESCE(approval_request_id::text,''), evidence::text, evidence_hash
  FROM audit_integrity_incidents`

func scanIncident(row pgx.Row) (Incident, error) {
	var inc Incident
	var evidenceRaw string
	err := row.Scan(&inc.ID, &inc.IncidentRef, &inc.Chain, &inc.Kind, &inc.GapFromSeq, &inc.GapToSeq,
		&inc.GapEventCount, &inc.TrustedFromSeq, &inc.SealedTailHash, &inc.SealedTailSource,
		&inc.DetectionMethod, &inc.DetectedAt, &inc.DetectedByEmail, &inc.SealedAt, &inc.SealedBy,
		&inc.SealedByEmail, &inc.Reason, &inc.ApprovalRequestID, &evidenceRaw, &inc.EvidenceHash)
	if err != nil {
		return Incident{}, err
	}
	inc.Evidence = map[string]any{}
	if strings.TrimSpace(evidenceRaw) != "" {
		_ = json.Unmarshal([]byte(evidenceRaw), &inc.Evidence)
	}
	return inc, nil
}

// LoadIncident 读取某条链已封存的完整性事件；不存在返回 (nil, nil)。
func LoadIncident(ctx context.Context, pool *dbtx.Pool, chain string) (*Incident, error) {
	if pool == nil {
		return nil, nil
	}
	inc, err := scanIncident(pool.QueryRow(ctx, incidentSelect+" WHERE chain = $1 ORDER BY sealed_at DESC LIMIT 1", chain))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	anchors, err := loadIncidentAnchors(ctx, pool, inc.ID)
	if err != nil {
		return nil, err
	}
	inc.RetainedAnchors = anchors
	declared, err := loadDeclaredEventsForIncident(ctx, pool, inc.ID)
	if err != nil {
		return nil, err
	}
	inc.DeclaredEvents = declared
	return &inc, nil
}

func loadIncidentAnchors(ctx context.Context, pool *dbtx.Pool, incidentID string) ([]IncidentAnchor, error) {
	rows, err := pool.Query(ctx, `SELECT chain, anchor_date::text, from_seq, to_seq, head_hash, event_count,
	       object_ref, content_hash, archive_hash, archive_intact, disposition, created_at
	  FROM audit_integrity_incident_anchors WHERE incident_id = $1 ORDER BY anchor_date`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IncidentAnchor{}
	for rows.Next() {
		var a IncidentAnchor
		if err := rows.Scan(&a.Chain, &a.AnchorDate, &a.FromSeq, &a.ToSeq, &a.HeadHash, &a.EventCount,
			&a.ObjectRef, &a.ContentHash, &a.ArchiveHash, &a.ArchiveIntact, &a.Disposition, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- 边界创建（封存） ----

// SealParams 是封存一次完整性缺口所需的全部输入。
type SealParams struct {
	Chain             string
	Reason            string
	SealedBy          string
	SealedByEmail     string
	ApprovalRequestID string
	DetectionMethod   string
	RecoveryAttempts  []map[string]any
	Environment       map[string]any
}

// IncidentRefFor 生成确定性的边界引用名（同缺口恒定同名，保证幂等）。
func IncidentRefFor(chain string, gapFrom, gapTo int64) string {
	return fmt.Sprintf("INC-%s-GAP-%d-%d", strings.ToUpper(chain), gapFrom, gapTo)
}

// HashIntegrityEvent 计算边界事件链的哈希：与事件链同构，
// 域分隔为 "integrity:<incident_ref>"，避免与 audit/login 链混淆。
func HashIntegrityEvent(ref string, seq int64, prevHash string, payload []byte) string {
	return HashEvent("integrity:"+ref, seq, prevHash, payload)
}

// IntegrityEventPayload 重建边界事件链一行的规范化载荷（与写入端逐字节一致）。
func IntegrityEventPayload(ref, chain, event, actorEmail, note string, detail any) ([]byte, error) {
	return Canonicalize(map[string]any{
		"incident_ref": ref, "chain": chain, "event": event,
		"actor_email": actorEmail, "note": note, "detail": detail,
	})
}

// AppendIncidentEvent 在边界事件链上追加一条记录（requested / approved / sealed ...）。
// 用事务级 advisory lock 串行化同一 ref 的追加，保证 seq 连续且 prev_hash 链接正确。
func AppendIncidentEvent(ctx context.Context, tx pgx.Tx, ref, chain, event, actorID, actorEmail, note string, detail map[string]any) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "audit-integrity:"+ref); err != nil {
		return err
	}
	var seq int64
	var prev string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(seq),0),
	       COALESCE((SELECT event_hash FROM audit_integrity_incident_events WHERE incident_ref=$1 ORDER BY seq DESC LIMIT 1),'')
	  FROM audit_integrity_incident_events WHERE incident_ref=$1`, ref).Scan(&seq, &prev); err != nil {
		return err
	}
	seq++
	if detail == nil {
		detail = map[string]any{}
	}
	payload, err := IntegrityEventPayload(ref, chain, event, actorEmail, note, detail)
	if err != nil {
		return err
	}
	hash := HashIntegrityEvent(ref, seq, prev, payload)
	body, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	actor := any(nil)
	if actorID != "" {
		actor = actorID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_integrity_incident_events
		    (incident_ref, chain, seq, event, actor_id, actor_email, note, detail, prev_hash, event_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10)`,
		ref, chain, seq, event, actor, actorEmail, note, string(body), prev, hash)
	return err
}

// SealIncident 封存当前观测到的不可恢复缺口。
//
// 幂等：同一 (chain, gap_from, gap_to) 已封存时直接返回既有事件（AlreadySealed=true），
// 不重复写入、不修改任何既有行。
func SealIncident(ctx context.Context, pool *dbtx.Pool, getter ObjectGetter, p SealParams) (Incident, error) {
	if pool == nil {
		return Incident{}, errors.New("audit store unavailable")
	}
	observed, err := DetectGap(ctx, pool, p.Chain)
	if err != nil {
		return Incident{}, err
	}
	if observed.InternalGapCount > 0 {
		return Incident{}, fmt.Errorf("%w: chain=%s internal_gap=%d", ErrInternalGap, p.Chain, observed.InternalGapCount)
	}
	if !observed.HasPrefixGap {
		return Incident{}, fmt.Errorf("%w: chain=%s min_seq=%d", ErrNoGapDetected, p.Chain, observed.FirstSurvivingSeq)
	}
	if existing, err := LoadGapIncident(ctx, pool, p.Chain); err != nil {
		return Incident{}, err
	} else if existing != nil && existing.GapFromSeq == observed.GapFromSeq && existing.GapToSeq == observed.GapToSeq {
		existing.AlreadySealed = true
		return *existing, nil
	}

	ref := IncidentRefFor(p.Chain, observed.GapFromSeq, observed.GapToSeq)
	retained, err := collectRetainedAnchors(ctx, pool, getter, p.Chain, observed.GapToSeq)
	if err != nil {
		return Incident{}, err
	}
	method := p.DetectionMethod
	if method == "" {
		method = "verifier.sequence_gap + prev_hash_of_first_surviving_row"
	}
	recovery := p.RecoveryAttempts
	if recovery == nil {
		recovery = []map[string]any{}
	}
	env := p.Environment
	if env == nil {
		env = map[string]any{}
	}
	evidence := map[string]any{
		"chain":                    p.Chain,
		"table":                    observed.Table,
		"gap_from_seq":             observed.GapFromSeq,
		"gap_to_seq":               observed.GapToSeq,
		"gap_event_count":          observed.GapEventCount,
		"trusted_from_seq":         observed.FirstSurvivingSeq,
		"head_seq":                 observed.HeadSeq,
		"head_hash":                observed.HeadHash,
		"row_count_after_boundary": observed.RowCount,
		"sealed_tail_hash":         observed.SealedTailHash,
		"sealed_tail_source":       observed.SealedTailSource,
		"detection_method":         method,
		"recovery_attempts":        recovery,
		"retained_anchor_count":    len(retained),
		"environment":              env,
		"forbidden_actions": []string{
			"不得回填或伪造缺口区间内的 audit_events_v2 行",
			"不得修改或删除 audit_chain_anchors 既有行",
			"不得把封存后的 integrity_status 表述为 intact",
		},
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return Incident{}, err
	}
	sum := sha256.Sum256(evidenceJSON)
	evidenceHash := hex.EncodeToString(sum[:])

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Incident{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sealedBy := any(nil)
	if p.SealedBy != "" {
		sealedBy = p.SealedBy
	}
	approvalID := any(nil)
	if p.ApprovalRequestID != "" {
		approvalID = p.ApprovalRequestID
	}
	var incidentID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO audit_integrity_incidents
		    (incident_ref, chain, kind, gap_from_seq, gap_to_seq, gap_event_count, trusted_from_seq,
		     sealed_tail_hash, sealed_tail_source, detection_method, detected_at, detected_by_email,
		     sealed_by, sealed_by_email, reason, approval_request_id, evidence, evidence_hash)
		VALUES ($1,$2,'unrecoverable_gap',$3,$4,$5,$6,$7,$8,$9,now(),$10,$11,$12,$13,$14,$15::jsonb,$16)
		RETURNING id::text`,
		ref, p.Chain, observed.GapFromSeq, observed.GapToSeq, observed.GapEventCount,
		observed.FirstSurvivingSeq, observed.SealedTailHash, observed.SealedTailSource, method,
		p.SealedByEmail, sealedBy, p.SealedByEmail, p.Reason, approvalID,
		string(evidenceJSON), evidenceHash).Scan(&incidentID); err != nil {
		return Incident{}, err
	}
	for _, a := range retained {
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_integrity_incident_anchors
			    (incident_id, chain, anchor_date, from_seq, to_seq, head_hash, event_count,
			     object_ref, content_hash, archive_hash, archive_intact, disposition)
			VALUES ($1,$2,$3::date,$4,$5,$6,$7,$8,$9,$10,$11,'sealed_unrecoverable')`,
			incidentID, a.Chain, a.AnchorDate, a.FromSeq, a.ToSeq, a.HeadHash, a.EventCount,
			a.ObjectRef, a.ContentHash, a.ArchiveHash, a.ArchiveIntact); err != nil {
			return Incident{}, err
		}
	}
	detail := map[string]any{
		"gap_from_seq": observed.GapFromSeq, "gap_to_seq": observed.GapToSeq,
		"trusted_from_seq": observed.FirstSurvivingSeq, "sealed_tail_hash": observed.SealedTailHash,
		"evidence_hash": evidenceHash, "retained_anchor_count": len(retained),
	}
	if err := AppendIncidentEvent(ctx, tx, ref, p.Chain, "detected", p.SealedBy, p.SealedByEmail, method, detail); err != nil {
		return Incident{}, err
	}
	if err := AppendIncidentEvent(ctx, tx, ref, p.Chain, "sealed", p.SealedBy, p.SealedByEmail, p.Reason, detail); err != nil {
		return Incident{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Incident{}, err
	}
	return LoadIncidentByRef(ctx, pool, ref)
}

// LoadIncidentByRef 按引用名读取事件。
func LoadIncidentByRef(ctx context.Context, pool *dbtx.Pool, ref string) (Incident, error) {
	inc, err := scanIncident(pool.QueryRow(ctx, incidentSelect+" WHERE incident_ref = $1", ref))
	if err != nil {
		return Incident{}, err
	}
	anchors, err := loadIncidentAnchors(ctx, pool, inc.ID)
	if err != nil {
		return Incident{}, err
	}
	inc.RetainedAnchors = anchors
	return inc, nil
}

// collectRetainedAnchors 收集完全落在封存缺口内的既有锚点，并核对 MinIO 归档对象哈希。
// 只读：不修改 audit_chain_anchors 任何行。
func collectRetainedAnchors(ctx context.Context, pool *dbtx.Pool, getter ObjectGetter, chain string, gapTo int64) ([]IncidentAnchor, error) {
	rows, err := pool.Query(ctx, `SELECT chain, anchor_date::text, from_seq, to_seq, head_hash, event_count,
	       object_ref, content_hash FROM audit_chain_anchors
	  WHERE chain = $1 AND to_seq <= $2 ORDER BY anchor_date`, chain, gapTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IncidentAnchor{}
	for rows.Next() {
		var a IncidentAnchor
		if err := rows.Scan(&a.Chain, &a.AnchorDate, &a.FromSeq, &a.ToSeq, &a.HeadHash, &a.EventCount,
			&a.ObjectRef, &a.ContentHash); err != nil {
			return nil, err
		}
		a.Disposition = AnchorSealedUnrecoverable
		a.ArchiveHash, a.ArchiveIntact = ReadArchiveHash(ctx, getter, a.ObjectRef, a.ContentHash)
		out = append(out, a)
	}
	return out, rows.Err()
}

// AnchorHeadMatches 判断锚点记录的 head_hash 是否与其声明区间的事件一致。
//
// 必须同时支持两种派生方式，否则会把历史锚点误判为损坏：
//   - chain_head（0055 起的规范语义）：head_hash == 区间末行 event_hash；
//   - legacy_max_hash（0055 之前的归档器语义）：head_hash == 区间内 max(event_hash)。
//
// 返回空字符串表示两种派生都对不上，锚点内容与区间事件不一致。
func AnchorHeadMatches(ctx context.Context, pool *dbtx.Pool, chain string, fromSeq, toSeq int64, headHash string) (string, error) {
	reader, ok := chainReaders[chain]
	if !ok {
		return "", fmt.Errorf("unknown chain %q", chain)
	}
	var canonical string
	err := pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT event_hash FROM %s WHERE chain = $1 AND seq = $2`, reader.table),
		chain, toSeq).Scan(&canonical)
	if err == nil && canonical != "" && canonical == headHash {
		return DerivationChainHead, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var legacy string
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT COALESCE(max(event_hash),'') FROM %s WHERE chain = $1 AND seq BETWEEN $2 AND $3`,
		reader.table), chain, fromSeq, toSeq).Scan(&legacy); err != nil {
		return "", err
	}
	if legacy != "" && legacy == headHash {
		return DerivationLegacyMaxHash, nil
	}
	return "", nil
}

// ReadArchiveHash 重新读取归档对象并返回其 SHA-256，与记录的 content_hash 比对。
func ReadArchiveHash(ctx context.Context, getter ObjectGetter, objectRef, want string) (string, bool) {
	if getter == nil || strings.TrimSpace(objectRef) == "" {
		return "", false
	}
	rc, _, err := getter.OpenNamed(ctx, ArchiveBucket, objectRef)
	if err != nil {
		return "", false
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	return got, want != "" && got == want
}
