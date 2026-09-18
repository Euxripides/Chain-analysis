package audit

// 审计完整性「稀疏声明」（ADR-019）：
// 把 append-only 链上不可修复、也不允许被忽略的两类事实，逐条登记为不可变记录。
//
//   1) disposition=lost            行的 seq 缺失（链头推进与写行不同事务造成），
//      其 event_hash 由**后继行的 prev_hash** 保留；
//   2) disposition=content_anomaly 行存在、链接完好，但其 event_hash 由不可落库的
//      字段值派生，永远无法由行内容复算。
//
// 声明不是"修复"，也不是"放行"：校验器仍然逐行复算其余行、仍然校验 prev_hash 链接，
// 并把声明明细与计数原样回报；「已声明」绝不等于「完整」。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/jackc/pgx/v5"
)

// 声明的处置类型。
const (
	DispositionLost           = "lost"
	DispositionContentAnomaly = "content_anomaly"
)

// IncidentKindDeclared 是 0063 引入的第二个完整性事件 kind。
const IncidentKindDeclared = "declared_integrity_events"

// DeclaredEvent 是一条被显式声明的完整性事实。
type DeclaredEvent struct {
	Chain             string         `json:"chain"`
	Seq               int64          `json:"seq"`
	Disposition       string         `json:"disposition"`
	ObservedEventHash string         `json:"observed_event_hash"`
	Evidence          map[string]any `json:"evidence"`
	IncidentRef       string         `json:"incident_ref,omitempty"`
}

// ChainScan 是「扫描整条链并收敛全部问题」的结果，只读、不写任何行。
//
// 与 VerifyChain 的区别：VerifyChain 在第一个问题处停止（在线校验/页面展示），
// ChainScan 走完整条链并把**所有**问题收敛出来（声明前的取证与人工核对）。
type ChainScan struct {
	Chain            string          `json:"chain"`
	Table            string          `json:"table"`
	RowCount         int64           `json:"row_count"`
	FirstSeq         int64           `json:"first_seq"`
	LastSeq          int64           `json:"last_seq"`
	HeadSeq          int64           `json:"head_seq"`
	Checked          int64           `json:"checked"`
	MissingSeqs      []DeclaredEvent `json:"missing_seqs"`
	ContentAnomalies []DeclaredEvent `json:"content_anomalies"`
	LinkBreaks       []DeclaredEvent `json:"link_breaks"`
	Errors           []string        `json:"errors"`
	ScannedAt        time.Time       `json:"scanned_at"`
}

// Blocking 表示存在**不可声明**的问题（prev_hash 链接断裂 = 真实篡改）。
func (s ChainScan) Blocking() bool { return len(s.LinkBreaks) > 0 }

const declaredSelect = `SELECT d.chain, d.seq, d.disposition, d.observed_event_hash, d.evidence::text,
       COALESCE(i.incident_ref,'')
  FROM audit_integrity_declared_events d
  LEFT JOIN audit_integrity_incidents i ON i.id = d.incident_id
 WHERE d.chain = $1 ORDER BY d.seq`

// LoadDeclaredEvents 读取一条链已声明的完整性事实（按 seq 索引）。
func LoadDeclaredEvents(ctx context.Context, pool *dbtx.Pool, chain string) (map[int64]DeclaredEvent, error) {
	out := map[int64]DeclaredEvent{}
	if pool == nil {
		return out, nil
	}
	rows, err := pool.Query(ctx, declaredSelect, chain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DeclaredEvent
		var evidenceRaw string
		if err := rows.Scan(&d.Chain, &d.Seq, &d.Disposition, &d.ObservedEventHash, &evidenceRaw, &d.IncidentRef); err != nil {
			return nil, err
		}
		d.Evidence = map[string]any{}
		if strings.TrimSpace(evidenceRaw) != "" {
			_ = json.Unmarshal([]byte(evidenceRaw), &d.Evidence)
		}
		out[d.Seq] = d
	}
	return out, rows.Err()
}

// DeclaredRefFor 生成确定性的声明引用名（同一组事实恒定同名，保证幂等）。
func DeclaredRefFor(chain, disposition string, seqs []int64) string {
	sorted := append([]int64(nil), seqs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	minSeq, maxSeq := int64(0), int64(0)
	if len(sorted) > 0 {
		minSeq, maxSeq = sorted[0], sorted[len(sorted)-1]
	}
	return fmt.Sprintf("INC-%s-DECLARED-%s-%d-%d-%d",
		strings.ToUpper(chain), strings.ToUpper(disposition), len(sorted), minSeq, maxSeq)
}

// SealDeclaredParams 是登记一次稀疏声明所需的全部输入（走 0055 的封存审批链）。
type SealDeclaredParams struct {
	Chain             string
	Disposition       string
	Events            []DeclaredEvent
	Reason            string
	SealedBy          string
	SealedByEmail     string
	ApprovalRequestID string
	DetectionMethod   string
	Evidence          map[string]any
	Environment       map[string]any
}

// SealDeclaredEvents 写入一条不可变的稀疏声明（事件 + 逐条明细）。
//
// 只 INSERT：不触碰任何审计行、锚点或链头。调用方必须已取得双人审批与 step-up
// （见 api/sprint6_audit_integrity.go），本函数不做授权判断。
func SealDeclaredEvents(ctx context.Context, pool *dbtx.Pool, p SealDeclaredParams) (*Incident, error) {
	if pool == nil {
		return nil, errors.New("audit store unavailable")
	}
	if len(p.Events) == 0 {
		return nil, errors.New("declaration requires at least one event")
	}
	if p.Disposition != DispositionLost && p.Disposition != DispositionContentAnomaly {
		return nil, fmt.Errorf("unsupported disposition %q", p.Disposition)
	}
	seqs := make([]int64, 0, len(p.Events))
	tailHash := ""
	for _, e := range p.Events {
		if e.Chain != p.Chain {
			return nil, fmt.Errorf("declared event chain %q does not match %q", e.Chain, p.Chain)
		}
		if len(e.ObservedEventHash) != 64 {
			return nil, fmt.Errorf("declared event seq %d has invalid observed hash", e.Seq)
		}
		if e.Disposition != p.Disposition {
			return nil, fmt.Errorf("declared event seq %d disposition mismatch", e.Seq)
		}
		seqs = append(seqs, e.Seq)
		if e.Seq >= 0 && e.ObservedEventHash > tailHash {
			tailHash = e.ObservedEventHash
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	lastSeq := seqs[len(seqs)-1]
	for _, e := range p.Events {
		if e.Seq == lastSeq {
			tailHash = e.ObservedEventHash
		}
	}
	ref := DeclaredRefFor(p.Chain, p.Disposition, seqs)

	evidence := map[string]any{}
	for k, v := range p.Evidence {
		evidence[k] = v
	}
	evidence["disposition"] = p.Disposition
	evidence["declared_seqs"] = seqs
	evidence["declared_count"] = len(seqs)
	evidence["forbidden_actions"] = []string{
		"回填缺失的审计行", "重写既有行的 event_hash", "把已声明事实记为完整",
	}
	evidenceHash, err := EvidenceHash(evidence)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "audit-integrity:"+ref); err != nil {
		return nil, err
	}
	var existingID string
	err = tx.QueryRow(ctx, "SELECT id::text FROM audit_integrity_incidents WHERE incident_ref = $1", ref).Scan(&existingID)
	if err == nil {
		// 幂等：同一组事实重复登记返回既有记录，不重复写入。
		_ = tx.Rollback(ctx)
		inc, lerr := loadIncidentByRef(ctx, pool, ref)
		return inc, lerr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	var incidentID string
	insertIncident := "INSERT INTO audit_integrity_incidents (" +
		"incident_ref, chain, kind, gap_from_seq, gap_to_seq, gap_event_count, " +
		"trusted_from_seq, sealed_tail_hash, sealed_tail_source, detection_method, " +
		"detected_by_email, sealed_by, sealed_by_email, reason, approval_request_id, " +
		"evidence, evidence_hash" +
		") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,'')::uuid,$13,$14,NULLIF($15,'')::uuid,$16::jsonb,$17) " +
		"RETURNING id::text"
	if err := tx.QueryRow(ctx, insertIncident,
		ref, p.Chain, IncidentKindDeclared, seqs[0], lastSeq, int64(len(seqs)),
		lastSeq+1, tailHash,
		"audit_integrity_declared_events.observed_event_hash（链上唯一留存的哈希证据）",
		p.DetectionMethod, p.SealedByEmail, p.SealedBy, p.SealedByEmail, p.Reason, p.ApprovalRequestID,
		mustJSONString(evidence), evidenceHash).Scan(&incidentID); err != nil {
		return nil, err
	}
	for _, e := range p.Events {
		if _, err := tx.Exec(ctx, "INSERT INTO audit_integrity_declared_events ("+
			"incident_id, chain, seq, disposition, observed_event_hash, evidence"+
			") VALUES ($1::uuid,$2,$3,$4,$5,$6::jsonb) ON CONFLICT (incident_id, chain, seq) DO NOTHING",
			incidentID, e.Chain, e.Seq, e.Disposition, e.ObservedEventHash, mustJSONString(e.Evidence)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	inc, err := loadIncidentByRef(ctx, pool, ref)
	return inc, err
}

func loadIncidentByRef(ctx context.Context, pool *dbtx.Pool, ref string) (*Incident, error) {
	inc, err := scanIncident(pool.QueryRow(ctx, incidentSelect+" WHERE incident_ref = $1", ref))
	if err != nil {
		return nil, err
	}
	events, err := loadDeclaredEventsForIncident(ctx, pool, inc.ID)
	if err != nil {
		return nil, err
	}
	inc.DeclaredEvents = events
	return &inc, nil
}

func loadDeclaredEventsForIncident(ctx context.Context, pool *dbtx.Pool, incidentID string) ([]DeclaredEvent, error) {
	rows, err := pool.Query(ctx, "SELECT chain, seq, disposition, observed_event_hash, evidence::text "+
		"FROM audit_integrity_declared_events WHERE incident_id = $1::uuid ORDER BY seq", incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeclaredEvent{}
	for rows.Next() {
		var d DeclaredEvent
		var evidenceRaw string
		if err := rows.Scan(&d.Chain, &d.Seq, &d.Disposition, &d.ObservedEventHash, &evidenceRaw); err != nil {
			return nil, err
		}
		d.Evidence = map[string]any{}
		if strings.TrimSpace(evidenceRaw) != "" {
			_ = json.Unmarshal([]byte(evidenceRaw), &d.Evidence)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// EvidenceHash 计算证据对象的 SHA-256（与 SealIncident 的派生方式一致：plain json.Marshal）。
func EvidenceHash(m map[string]any) (string, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// mustJSONString 序列化证据对象；失败时返回 "{}"（证据体必须总是可落库）。
func mustJSONString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// ---- 全链扫描（声明前的取证，只读） ----

// ScanChain 走完整条链，收敛**全部**问题：缺失的 seq、内容哈希不可复算的行、
// 以及 prev_hash 链接断裂。前两类可以走声明流程登记为事实，第三类不是事实而是篡改，
// 必须先在链上定位处置（Blocking()==true 时不允许声明）。
func ScanChain(ctx context.Context, pool *dbtx.Pool, chain string) (ChainScan, error) {
	reader, ok := chainReaders[chain]
	if !ok {
		return ChainScan{}, fmt.Errorf("unknown chain %q", chain)
	}
	out := ChainScan{Chain: chain, Table: reader.table, Errors: []string{},
		MissingSeqs: []DeclaredEvent{}, ContentAnomalies: []DeclaredEvent{}, LinkBreaks: []DeclaredEvent{},
		ScannedAt: time.Now().UTC()}
	if err := pool.QueryRow(ctx, "SELECT count(*), COALESCE(min(seq),0), COALESCE(max(seq),0) FROM "+reader.table+" WHERE chain = $1",
		chain).Scan(&out.RowCount, &out.FirstSeq, &out.LastSeq); err != nil {
		return out, err
	}
	if out.RowCount == 0 {
		return out, nil
	}
	// 链头：seq 缺失可能落在最后一行之后，必须与链头比对才能发现。
	if err := pool.QueryRow(ctx, "SELECT last_seq FROM audit_chain_heads WHERE chain = $1", chain).Scan(&out.HeadSeq); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		out.HeadSeq = out.LastSeq
	}

	// 已封存的不可恢复缺口：其区间内的行本来就不存在，不作为"缺失"重复报告。
	// 注意必须按 kind 精确取缺口事件——同一条链上可能同时存在缺口与稀疏声明，
	// LoadIncident（取最新）会把声明当成边界。
	sealedFrom, sealedTo := int64(0), int64(0)
	_ = pool.QueryRow(ctx, "SELECT gap_from_seq, gap_to_seq FROM audit_integrity_incidents "+
		"WHERE chain = $1 AND kind = 'unrecoverable_gap' ORDER BY sealed_at DESC LIMIT 1",
		chain).Scan(&sealedFrom, &sealedTo)

	query := reader.query
	args := []any{chain, out.FirstSeq}
	query += " ORDER BY seq"
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	expected := out.FirstSeq
	prevHash := ""
	first := true
	for rows.Next() {
		seq, rowPrev, rowHash, payload, err := reader.build(rows)
		if err != nil {
			return out, err
		}
		if seq != expected {
			for missing := expected; missing < seq; missing++ {
				if missing >= sealedFrom && missing <= sealedTo {
					continue
				}
				// 连续缺失段里，链上只保留了**段尾**那一行的哈希（即后继行的 prev_hash）；
				// 段内其余行的哈希在 append-only 链上不可能恢复。'因此整段共用同一个可验证
				// 哈希证据，并用 hash_role 明确标注它对本行到底是什么——不制造"每行都有哈希"的假象。
				role := "run_tail"
				if missing < seq-1 {
					role = "run_interior"
				}
				out.MissingSeqs = append(out.MissingSeqs, DeclaredEvent{
					Chain: chain, Seq: missing, Disposition: DispositionLost, ObservedEventHash: rowPrev,
					Evidence: map[string]any{
						"source":                      "successor_prev_hash",
						"hash_role":                   role,
						"successor_seq":               seq,
						"successor_hash":              rowHash,
						"observed_event_hash_meaning": "本行 event_hash 不可恢复；该值是该缺失段段尾行的哈希（后继行 prev_hash）",
					},
				})
			}
			expected = seq
			// 缺口行的 event_hash 由**本行的 prev_hash** 保留，因此链接证据在这里接续。
			// 少了这一步，每个缺口后面的第一行都会被误报成"链接断裂"——
			// 那是把"已经能解释的缺失"错认成"真实篡改"。
			prevHash = rowPrev
		}
		if !first && rowPrev != prevHash {
			out.LinkBreaks = append(out.LinkBreaks, DeclaredEvent{
				Chain: chain, Seq: seq, Disposition: "link_break", ObservedEventHash: rowHash,
				Evidence: map[string]any{"prev_hash_in_row": rowPrev, "expected_prev_hash": prevHash},
			})
			out.Errors = append(out.Errors, fmt.Sprintf("seq %d: prev_hash 与上一行的 event_hash 不一致", seq))
		}
		canonical, err := Canonicalize(payload)
		if err != nil {
			return out, err
		}
		if want := HashEvent(chain, seq, rowPrev, canonical); want != rowHash {
			out.ContentAnomalies = append(out.ContentAnomalies, DeclaredEvent{
				Chain: chain, Seq: seq, Disposition: DispositionContentAnomaly, ObservedEventHash: rowHash,
				Evidence: map[string]any{"recomputed_event_hash": want, "stored_event_hash": rowHash},
			})
		}
		prevHash = rowHash
		expected = seq + 1
		first = false
		out.Checked++
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	// 链头之后的缺失（尾部落空）。
	for missing := expected; missing <= out.HeadSeq; missing++ {
		if missing >= sealedFrom && missing <= sealedTo {
			continue
		}
		out.MissingSeqs = append(out.MissingSeqs, DeclaredEvent{
			Chain: chain, Seq: missing, Disposition: DispositionLost, ObservedEventHash: "",
			Evidence: map[string]any{"source": "chain_head_above_last_row", "chain_head_seq": out.HeadSeq},
		})
	}
	return out, nil
}

// LoadGapIncident 按 kind 精确读取「不可恢复缺口」边界事件；不存在返回 (nil, nil)。
//
// 为什么不能直接用 LoadIncident：同一条链上可以同时存在缺口事件与稀疏声明，
// 而 LoadIncident 取的是最新一条。边界语义（可信起点、封存尾哈希、反回填检查）
// 只属于 kind='unrecoverable_gap'，取错会让校验器把声明区间当成缺口。
func LoadGapIncident(ctx context.Context, pool *dbtx.Pool, chain string) (*Incident, error) {
	if pool == nil {
		return nil, nil
	}
	inc, err := scanIncident(pool.QueryRow(ctx,
		incidentSelect+" WHERE chain = $1 AND kind = 'unrecoverable_gap' ORDER BY sealed_at DESC LIMIT 1", chain))
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
	return &inc, nil
}
