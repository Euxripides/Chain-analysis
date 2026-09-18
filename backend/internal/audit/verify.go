package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/jackc/pgx/v5"
)

// VerifyChainResult 是 §27 AUDIT-004 的校验输出。
//
// Sprint 6 审计修复新增完整性边界字段：一条链即使存在已封存的不可恢复缺口，
// 其边界之后的区间仍然必须可逐行验证。valid 只描述「实际被校验到的区间」是否自洽，
// 是否完整则由 integrity_status / prefix_status 明确表达，二者不可互相替代。
type VerifyChainResult struct {
	Chain       string   `json:"chain"`
	FromSeq     int64    `json:"from_seq"`
	ToSeq       int64    `json:"to_seq"`
	Checked     int64    `json:"checked"`
	Valid       bool     `json:"valid"`
	BrokenAtSeq *int64   `json:"broken_at_seq"`
	Reason      string   `json:"reason"`
	HeadHash    string   `json:"head_hash"`
	Errors      []string `json:"errors"`

	// 完整性边界（Sprint 6 审计修复）
	IntegrityStatus   string             `json:"integrity_status"`
	PrefixStatus      string             `json:"prefix_status"`
	VerifiedFromSeq   int64              `json:"verified_from_seq"`
	SealedBoundary    *SealedBoundaryRef `json:"sealed_boundary,omitempty"`
	IntegrityWarnings []string           `json:"integrity_warnings"`

	// 稀疏声明（ADR-019）：逐条列出被显式声明的缺失行与内容不可复算行。
	// 这些计数与明细必须与 DB 中的声明逐条一致——「已声明」不等于「完整」。
	DeclaredEvents       []DeclaredEvent `json:"declared_events"`
	DeclaredLostCount    int64           `json:"declared_lost_count"`
	DeclaredAnomalyCount int64           `json:"declared_anomaly_count"`
}

type scanner interface {
	Scan(dest ...any) error
}

func jsonbAny(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

// chainReader 描述一条链如何从数据库读取并"由权威列重建哈希载荷"。
//
// 关键点：重建结果必须与写入时 audit.Writer.chainPayload / iam.RecordLoginEvent
// 生成的载荷逐字节一致（键排序后）。只有这样，哈希链才能同时检测
// "链被改写" 与 "行内字段被改写" 两种篡改（§27 AUDIT-004）。
type chainReader struct {
	table string
	query string
	build func(sc scanner) (int64, string, string, map[string]any, error)
}

var chainReaders = map[string]chainReader{
	ChainAudit: {
		table: "audit_events_v2",
		query: `
            SELECT seq, prev_hash, event_hash,
                   occurred_at, environment, actor_type, COALESCE(actor_id::text,''), actor_email,
                   COALESCE(organization_id::text,''), COALESCE(workspace_id::text,''),
                   COALESCE(session_id::text,''), COALESCE(device_id::text,''),
                   action, category, resource_type, resource_id, http_method, route_template,
                   request_id, trace_id, result, http_status, error_code, COALESCE(permission_key,''),
                   policy_decision, deny_reason, sensitive, risk_level,
                   COALESCE(before::text,'null'), COALESCE(after::text,'null'), COALESCE(changes::text,'null'),
                   detail::text, COALESCE(host(client_ip),''), user_agent
            FROM audit_events_v2 WHERE chain = $1 AND seq >= $2`,
		build: func(sc scanner) (int64, string, string, map[string]any, error) {
			var seq int64
			var prevHash, hash string
			var occurredAt time.Time
			var environment, actorType, actorID, actorEmail, orgID, wsID, sessionID, deviceID string
			var action, category, resourceType, resourceID, httpMethod, route, requestID, traceID string
			var result, errorCode, permissionKey, policyDecision, denyReason, riskLevel string
			var beforeRaw, afterRaw, changesRaw, detailRaw, clientIP, userAgent string
			var httpStatus int
			var sensitive bool
			if err := sc.Scan(&seq, &prevHash, &hash,
				&occurredAt, &environment, &actorType, &actorID, &actorEmail,
				&orgID, &wsID, &sessionID, &deviceID,
				&action, &category, &resourceType, &resourceID, &httpMethod, &route,
				&requestID, &traceID, &result, &httpStatus, &errorCode, &permissionKey,
				&policyDecision, &denyReason, &sensitive, &riskLevel,
				&beforeRaw, &afterRaw, &changesRaw, &detailRaw, &clientIP, &userAgent); err != nil {
				return 0, "", "", nil, err
			}
			return seq, prevHash, hash, map[string]any{
				"occurred_at_unix_micro": occurredAt.UTC().UnixMicro(),
				"environment":            environment,
				"actor_type":             actorType,
				"actor_id":               actorID,
				"actor_email":            actorEmail,
				"organization_id":        orgID,
				"workspace_id":           wsID,
				"session_id":             sessionID,
				"device_id":              deviceID,
				"action":                 action,
				"category":               category,
				"resource_type":          resourceType,
				"resource_id":            resourceID,
				"http_method":            httpMethod,
				"route_template":         route,
				"request_id":             requestID,
				"trace_id":               traceID,
				"result":                 result,
				"http_status":            httpStatus,
				"error_code":             errorCode,
				"permission_key":         permissionKey,
				"policy_decision":        policyDecision,
				"deny_reason":            denyReason,
				"sensitive":              sensitive,
				"risk_level":             riskLevel,
				"before":                 jsonbAny(beforeRaw),
				"after":                  jsonbAny(afterRaw),
				"changes":                jsonbAny(changesRaw),
				"detail":                 jsonbAny(detailRaw),
				"client_ip":              clientIP,
				"user_agent":             userAgent,
			}, nil
		},
	},
	ChainLogin: {
		table: "login_events",
		query: `
            SELECT seq, prev_hash, event_hash,
                   occurred_at, event_type, result, reason_code, COALESCE(identity_id::text,''),
                   subject_digest, COALESCE(organization_id::text,''), audience, auth_method,
                   COALESCE(session_id::text,''), COALESCE(device_id::text,''),
                   COALESCE(host(client_ip),''), COALESCE(host(peer_ip),''), user_agent, detail::text
            FROM login_events WHERE chain = $1 AND seq >= $2`,
		build: func(sc scanner) (int64, string, string, map[string]any, error) {
			var seq int64
			var prevHash, hash string
			var occurredAt time.Time
			var eventType, result, reasonCode, identityID, subjectDigest, orgID string
			var audience, authMethod, sessionID, deviceID, clientIP, peerIP, userAgent, detailRaw string
			if err := sc.Scan(&seq, &prevHash, &hash,
				&occurredAt, &eventType, &result, &reasonCode, &identityID,
				&subjectDigest, &orgID, &audience, &authMethod,
				&sessionID, &deviceID, &clientIP, &peerIP, &userAgent, &detailRaw); err != nil {
				return 0, "", "", nil, err
			}
			return seq, prevHash, hash, map[string]any{
				"occurred_at_unix_micro": occurredAt.UTC().UnixMicro(),
				"event_type":             eventType,
				"result":                 result,
				"reason_code":            reasonCode,
				"identity_id":            identityID,
				"subject_digest":         subjectDigest,
				"organization_id":        orgID,
				"audience":               audience,
				"auth_method":            authMethod,
				"session_id":             sessionID,
				"device_id":              deviceID,
				"client_ip":              clientIP,
				"peer_ip":                peerIP,
				"user_agent":             userAgent,
				"detail":                 jsonbAny(detailRaw),
			}, nil
		},
	},
}

// ChainTables 返回参与哈希链的表名，供数据字典与测试使用。
func ChainTables() []string {
	return []string{chainReaders[ChainAudit].table, chainReaders[ChainLogin].table}
}

// VerifyChain 重算指定区间的哈希链，并识别/尊重完整性事件边界。
//
// 边界语义（Sprint 6 审计修复）：
//   - 无缺口：integrity_status=intact、prefix_status=intact，行为与 S3 版完全一致。
//   - 有缺口且已封存：integrity_status=sealed_incident、prefix_status=sealed_unrecoverable，
//     实际校验自 trusted_from_seq 起，并以该事件记录的 sealed_tail_hash 作为首行 prev_hash，
//     因此边界之后的链与「被销毁的最后一条事件的哈希」仍然密码学衔接。
//   - 有缺口但未封存：integrity_status=undeclared_gap、valid=false（不合法状态，必须封存）。
//   - 声明边界与实际数据矛盾（缺口区间出现行、封存尾哈希不匹配、可信起点缺失）：
//     integrity_status=boundary_conflict、valid=false。这是反回填控制：任何向已封存缺口
//     写入行的尝试都会被判定为冲突，而不是被静默接受。
//
// 注意：valid 只描述「实际被校验到的区间」是否自洽；链路是否完整由 integrity_status /
// prefix_status 明确表达。封存之后本函数永不返回 intact。
func VerifyChain(ctx context.Context, pool *dbtx.Pool, chain string, fromSeq, toSeq int64) (VerifyChainResult, error) {
	reader, ok := chainReaders[chain]
	if !ok {
		return VerifyChainResult{}, fmt.Errorf("unknown chain %q", chain)
	}
	res := VerifyChainResult{
		Chain: chain, Valid: true, Errors: []string{}, IntegrityWarnings: []string{},
		IntegrityStatus: IntegrityIntact, PrefixStatus: PrefixIntact,
		DeclaredEvents: []DeclaredEvent{},
	}
	if fromSeq <= 0 {
		fromSeq = 1
	}
	res.FromSeq = fromSeq
	res.ToSeq = toSeq
	res.VerifiedFromSeq = fromSeq

	// 边界语义只属于 kind='unrecoverable_gap'；稀疏声明走 declared 集合单独处理。
	incident, err := LoadGapIncident(ctx, pool, chain)
	if err != nil {
		return res, err
	}
	declared, err := LoadDeclaredEvents(ctx, pool, chain)
	if err != nil {
		return res, err
	}

	startSeq := fromSeq
	prevHash := ""
	if incident != nil {
		res.SealedBoundary = incident.BoundaryRef()
		res.IntegrityStatus = IntegritySealedIncident
		res.PrefixStatus = PrefixSealedUnrecoverable

		// 反回填：已封存缺口区间内不允许存在任何行。
		var rowsInside int64
		if err := pool.QueryRow(ctx, fmt.Sprintf(
			`SELECT count(*) FROM %s WHERE chain = $1 AND seq BETWEEN $2 AND $3`, reader.table),
			chain, incident.GapFromSeq, incident.GapToSeq).Scan(&rowsInside); err != nil {
			return res, err
		}
		if rowsInside > 0 {
			res.IntegrityStatus = IntegrityBoundaryConflict
			return res.broken(incident.TrustedFromSeq, "sealed_gap_contains_rows",
				fmt.Sprintf("已封存缺口 seq %d..%d 内出现 %d 行，边界声明与实际数据矛盾（禁止回填历史审计行）",
					incident.GapFromSeq, incident.GapToSeq, rowsInside)), nil
		}

		// 新链可信起点必须存在，且其 prev_hash 必须等于封存尾哈希。
		var actualTail string
		tailErr := pool.QueryRow(ctx, fmt.Sprintf(
			`SELECT prev_hash FROM %s WHERE chain = $1 AND seq = $2`, reader.table),
			chain, incident.TrustedFromSeq).Scan(&actualTail)
		if errors.Is(tailErr, pgx.ErrNoRows) {
			res.IntegrityStatus = IntegrityBoundaryConflict
			return res.broken(incident.TrustedFromSeq, "trusted_start_row_missing",
				fmt.Sprintf("新链可信起点 seq %d 不存在，边界声明失效", incident.TrustedFromSeq)), nil
		}
		if tailErr != nil {
			return res, tailErr
		}
		if actualTail != incident.SealedTailHash {
			res.IntegrityStatus = IntegrityBoundaryConflict
			return res.broken(incident.TrustedFromSeq, "sealed_tail_hash_mismatch",
				fmt.Sprintf("seq %d 的 prev_hash 与封存尾哈希不一致，幸存区间不是被销毁链尾的延续",
					incident.TrustedFromSeq)), nil
		}
		if startSeq < incident.TrustedFromSeq {
			startSeq = incident.TrustedFromSeq
			res.IntegrityWarnings = append(res.IntegrityWarnings, fmt.Sprintf(
				"请求区间自 seq %d 起，但 seq %d..%d 已封存为不可恢复缺口，实际校验自 seq %d 起",
				fromSeq, incident.GapFromSeq, incident.GapToSeq, incident.TrustedFromSeq))
		}
	}
	res.VerifiedFromSeq = startSeq

	query := reader.query
	args := []any{chain, startSeq}
	if toSeq > 0 {
		query += " AND seq <= $3"
		args = append(args, toSeq)
	}
	query += " ORDER BY seq"

	// The predecessor hash MUST be read before the range result set is opened.
	// pgx allows exactly one in-flight statement per connection: issuing a second
	// statement while a Query result set is still open fails with "conn busy",
	// and the failing Close marks the still-open statement as invalidated in the
	// statement cache. On a *dbtx.Pool bound to the request transaction (which the
	// commit gate puts in the context) both statements run on the SAME connection,
	// so the gate's release re-read then fails with "failed to deallocate cached
	// statement(s): conn busy" and refuses every legitimate request 409
	// AUTHZ_WORKSPACE_SCOPE_STALE (P4-ANCHOR-01). One result set per connection.
	if startSeq > 1 {
		if incident != nil && startSeq == incident.TrustedFromSeq {
			// 用保留的封存尾哈希作为起点：这是新旧链唯一可用的密码学衔接证据。
			prevHash = incident.SealedTailHash
		} else if d, ok := declared[startSeq-1]; ok && d.Disposition == DispositionLost {
			// 起点前一行是**已声明**的缺失行：其 event_hash 由本行的 prev_hash 保留，
			// 这就是该缺失行的链上哈希证据（ADR-019）。
			prevHash = d.ObservedEventHash
		} else if err := pool.QueryRow(ctx, fmt.Sprintf(
			`SELECT event_hash FROM %s WHERE chain = $1 AND seq = $2`, reader.table),
			chain, startSeq-1).Scan(&prevHash); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				res.IntegrityStatus = IntegrityUndeclaredGap
				res.PrefixStatus = PrefixUnsealedGap
				return res.broken(startSeq, "sequence_gap",
					fmt.Sprintf("seq %d 之前的事件行缺失，且未封存完整性边界", startSeq)), nil
			}
			return res, fmt.Errorf("read predecessor hash: %w", err)
		}
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	expectedSeq := startSeq
	for rows.Next() {
		seq, rowPrev, rowHash, payload, err := reader.build(rows)
		if err != nil {
			return res, err
		}
		if seq != expectedSeq {
			// 缺口只允许在**逐条已声明**的情况下继续；任何未声明的缺失都是 undeclared_gap。
			declaredLost := make([]DeclaredEvent, 0, seq-expectedSeq)
			for missing := expectedSeq; missing < seq; missing++ {
				d, ok := declared[missing]
				if !ok || d.Disposition != DispositionLost {
					res.IntegrityStatus = IntegrityUndeclaredGap
					res.PrefixStatus = PrefixUnsealedGap
					return res.broken(missing, "sequence_gap",
						fmt.Sprintf("seq %d 缺失且未被声明为完整性事件（expected %d, found %d）",
							missing, expectedSeq, seq)), nil
				}
				declaredLost = append(declaredLost, d)
			}
			// 链接证据：本行 prev_hash 必须等于最后一条被声明缺失行的哈希。
			if want := declaredLost[len(declaredLost)-1].ObservedEventHash; rowPrev != want {
				res.IntegrityStatus = IntegrityBoundaryConflict
				return res.broken(seq, "declared_lost_hash_mismatch",
					fmt.Sprintf("seq %d 的 prev_hash 与声明证据不一致（声明 %s / 实际 %s）", seq, want, rowPrev)), nil
			}
			res.DeclaredEvents = append(res.DeclaredEvents, declaredLost...)
			res.DeclaredLostCount += int64(len(declaredLost))
			prevHash = rowPrev
			expectedSeq = seq
		}
		if rowPrev != prevHash {
			return res.broken(seq, "prev_hash_mismatch",
				fmt.Sprintf("seq %d: prev_hash 与上一条 event_hash 不一致", seq)), nil
		}
		if d, ok := declared[seq]; ok {
			switch d.Disposition {
			case DispositionContentAnomaly:
				// 行存在、链接完好，但内容哈希无法由行内容复算（写入侧缺陷的既成事实）。
				// 声明里记录的必须是**该行存储的**哈希；不一致说明行被改写 -> 冲突。
				if d.ObservedEventHash != rowHash {
					res.IntegrityStatus = IntegrityBoundaryConflict
					return res.broken(seq, "declared_anomaly_hash_mismatch",
						fmt.Sprintf("seq %d 的存储哈希与声明不一致（声明 %s / 实际 %s）",
							seq, d.ObservedEventHash, rowHash)), nil
				}
				res.DeclaredEvents = append(res.DeclaredEvents, d)
				res.DeclaredAnomalyCount++
				prevHash = rowHash
				res.HeadHash = rowHash
				res.Checked++
				expectedSeq++
				continue
			case DispositionLost:
				// 已声明为缺失却存在行 = 回填/伪造，必须冲突而不是静默接受。
				res.IntegrityStatus = IntegrityBoundaryConflict
				return res.broken(seq, "declared_lost_row_present",
					fmt.Sprintf("seq %d 已被声明为缺失，但链上存在该行（禁止回填历史审计行）", seq)), nil
			}
		}
		canonical, err := Canonicalize(payload)
		if err != nil {
			return res, err
		}
		if want := HashEvent(chain, seq, prevHash, canonical); want != rowHash {
			return res.broken(seq, "event_hash_mismatch",
				fmt.Sprintf("seq %d: event_hash 不匹配（记录内容被改写）", seq)), nil
		}
		prevHash = rowHash
		res.HeadHash = rowHash
		res.Checked++
		expectedSeq++
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	// 链头可能领先于最后一行（链头已推进而写行失败）。尾部缺失同样必须被声明，
	// 否则"链头说 5921、链上只到 5920"会完全静默。
	if toSeq == 0 {
		var headSeq int64
		if err := pool.QueryRow(ctx,
			"SELECT last_seq FROM audit_chain_heads WHERE chain = $1", chain).Scan(&headSeq); err == nil && headSeq >= expectedSeq {
			// 读一致性：上面那批行是**较早的快照**（游标已关闭但快照更旧），而链头是刚读的。
			// 并发写入提交在两者之间时，"链头比最后一行大"并不代表真的缺行。
			// 因此这里在读到链头**之后**重读一次最大 seq，再用它作为判定基准：
			// 原子写入（链头与行同事务提交）保证了 head 可见 ⇒ 对应行也已可见。
			var maxSeqNow int64
			if err := pool.QueryRow(ctx, fmt.Sprintf(
				"SELECT COALESCE(max(seq),0) FROM %s WHERE chain = $1", reader.table), chain).Scan(&maxSeqNow); err != nil {
				return res, err
			}
			if maxSeqNow > expectedSeq {
				expectedSeq = maxSeqNow
			}
		}
		if headSeq >= expectedSeq {
			trailing := make([]DeclaredEvent, 0, headSeq-expectedSeq+1)
			for missing := expectedSeq; missing <= headSeq; missing++ {
				d, ok := declared[missing]
				if !ok || d.Disposition != DispositionLost {
					res.IntegrityStatus = IntegrityUndeclaredGap
					res.PrefixStatus = PrefixUnsealedGap
					return res.broken(missing, "sequence_gap",
						fmt.Sprintf("链头 seq %d 之前缺少 seq %d 且未被声明为完整性事件", headSeq, missing)), nil
				}
				trailing = append(trailing, d)
			}
			res.DeclaredEvents = append(res.DeclaredEvents, trailing...)
			res.DeclaredLostCount += int64(len(trailing))
			expectedSeq = headSeq + 1
		}
	}
	res.ToSeq = expectedSeq - 1
	if res.Checked == 0 {
		res.Reason = "empty_range"
	}
	// 没有缺口边界但存在已声明事实时，用 declared_events 明确表达——
	// 绝不退回 intact，也绝不因此宣称"链完整"。
	if res.IntegrityStatus == IntegrityIntact && (res.DeclaredLostCount > 0 || res.DeclaredAnomalyCount > 0) {
		res.IntegrityStatus = IntegrityDeclaredEvents
	}
	if res.DeclaredLostCount > 0 || res.DeclaredAnomalyCount > 0 {
		res.IntegrityWarnings = append(res.IntegrityWarnings, fmt.Sprintf(
			"链 %s 存在 %d 条已声明的缺失行与 %d 条已声明的内容不可复算行；"+
				"声明本身是不可变事实记录，不代表链完整，也不改变其余区间的逐行复算结果",
			chain, res.DeclaredLostCount, res.DeclaredAnomalyCount))
	}
	return res, nil
}

func (r VerifyChainResult) broken(seq int64, reason, message string) VerifyChainResult {
	r.Valid = false
	r.Reason = reason
	at := seq
	r.BrokenAtSeq = &at
	r.Errors = append(r.Errors, message)
	return r
}

// BoundaryRef 生成校验结果中使用的边界精简引用。
func (i Incident) BoundaryRef() *SealedBoundaryRef {
	return &SealedBoundaryRef{
		IncidentRef:    i.IncidentRef,
		Chain:          i.Chain,
		GapFromSeq:     i.GapFromSeq,
		GapToSeq:       i.GapToSeq,
		GapEventCount:  i.GapEventCount,
		TrustedFromSeq: i.TrustedFromSeq,
		SealedTailHash: i.SealedTailHash,
		EvidenceHash:   i.EvidenceHash,
		SealedAt:       i.SealedAt,
		Statement: fmt.Sprintf(
			"链 %s 的 seq %d..%d 已封存为不可恢复缺口（%d 条事件）；旧链在该处不连续，新链可信起点为 seq %d，其 sealed_tail_hash 为被销毁的最后一条事件的 event_hash。",
			i.Chain, i.GapFromSeq, i.GapToSeq, i.GapEventCount, i.TrustedFromSeq),
	}
}

// ---- 每日锚点逐项验证（§27.6 AUDIT-004 的锚点一致性） ----

const anchorSelect = `SELECT chain, anchor_date::text, from_seq, to_seq, head_hash, event_count,
       object_ref, content_hash FROM audit_chain_anchors WHERE chain = $1 ORDER BY anchor_date`

// VerifyAnchors 对一条链的全部每日锚点逐项给出结论。
//
// 三种处置：
//   - verified：锚点区间完全位于可信起点之后，整段复算通过且末行 event_hash == 锚点 head_hash；
//   - sealed_unrecoverable：锚点区间完全落在已封存缺口内，事件行不可恢复，
//     此时仍核对锚点行与其 MinIO 归档对象哈希（证明保留证据未被替换）；
//   - spans_boundary / broken：跨越边界或复算失败，一律如实标注，不降级为通过。
func VerifyAnchors(ctx context.Context, pool *dbtx.Pool, getter ObjectGetter, chain string) ([]AnchorVerification, error) {
	if pool == nil {
		return nil, errors.New("audit store unavailable")
	}
	// 边界语义只属于不可恢复缺口事件：同一条链上可能同时存在稀疏声明，
	// 用"最新一条"会把声明区间当成缺口（锚点归因、反回填计数都会跟着错）。
	incident, err := LoadGapIncident(ctx, pool, chain)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, anchorSelect, chain)
	if err != nil {
		return nil, err
	}
	type rawAnchor struct {
		date                         string
		from, to, count              int64
		head, objectRef, contentHash string
	}
	pending := []rawAnchor{}
	for rows.Next() {
		var it rawAnchor
		var chainCol string
		if err := rows.Scan(&chainCol, &it.date, &it.from, &it.to, &it.head, &it.count,
			&it.objectRef, &it.contentHash); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, it)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := make([]AnchorVerification, 0, len(pending))
	for _, it := range pending {
		v := AnchorVerification{
			Chain: chain, AnchorDate: it.date, FromSeq: it.from, ToSeq: it.to,
			HeadHash: it.head, EventCount: it.count, ObjectRef: it.objectRef, ContentHash: it.contentHash,
		}
		v.ArchiveObjectHash, v.ArchiveIntact = ReadArchiveHash(ctx, getter, it.objectRef, it.contentHash)
		switch {
		case incident != nil && it.to <= incident.GapToSeq:
			v.Disposition = AnchorSealedUnrecoverable
			v.IncidentRef = incident.IncidentRef
			v.Reason = fmt.Sprintf(
				"锚点区间 [%d,%d] 完全落在已封存缺口 [%d,%d] 内：事件行不可恢复，head_hash 无法再用事件行复算；锚点行与 MinIO 归档对象原样保留（归档一致性=%t）",
				it.from, it.to, incident.GapFromSeq, incident.GapToSeq, v.ArchiveIntact)
		case incident != nil && it.from < incident.TrustedFromSeq && it.to >= incident.TrustedFromSeq:
			v.Disposition = AnchorSpansBoundary
			v.IncidentRef = incident.IncidentRef
			v.Reason = fmt.Sprintf("锚点区间 [%d,%d] 跨越完整性边界 seq %d，无法整段复算",
				it.from, it.to, incident.TrustedFromSeq)
		default:
			res, err := VerifyChain(ctx, pool, chain, it.from, it.to)
			if err != nil {
				return nil, err
			}
			v.Checked = res.Checked
			v.RecomputedHead = res.HeadHash
			derivation, err := AnchorHeadMatches(ctx, pool, chain, it.from, it.to, it.head)
			if err != nil {
				return nil, err
			}
			v.HeadHashDerivation = derivation
			switch {
			case !res.Valid:
				v.Disposition = AnchorBroken
				v.Reason = fmt.Sprintf("锚点区间复算失败：%s（broken_at_seq=%v）", res.Reason, res.BrokenAtSeq)
			case derivation == "":
				v.Disposition = AnchorBroken
				v.Reason = "锚点 head_hash 与区间事件既不符合链头哈希、也不符合历史 max(event_hash) 派生"
			case res.Checked != it.count:
				v.Disposition = AnchorBroken
				v.Reason = fmt.Sprintf("锚点 event_count=%d 与复算条数 %d 不一致", it.count, res.Checked)
			case derivation == DerivationChainHead:
				v.Verified = true
				v.Disposition = AnchorVerified
			default:
				v.Verified = true
				v.Disposition = AnchorVerifiedLegacy
				v.Reason = "锚点 head_hash 与区间事件一致，但采用 0055 之前的 max(event_hash) 派生方式；锚点行不可改写，已按该派生方式复核通过"
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// ---- 完整性报告 ----

// BuildIntegrityReport 汇总一条链的观测缺口、封存事件、链校验与锚点逐项结论。
func BuildIntegrityReport(ctx context.Context, pool *dbtx.Pool, getter ObjectGetter, chain string) (IntegrityReport, error) {
	observed, err := DetectGap(ctx, pool, chain)
	if err != nil {
		return IntegrityReport{}, err
	}
	incident, err := LoadGapIncident(ctx, pool, chain)
	if err != nil {
		return IntegrityReport{}, err
	}
	verification, err := VerifyChain(ctx, pool, chain, 0, 0)
	if err != nil {
		return IntegrityReport{}, err
	}
	anchors, err := VerifyAnchors(ctx, pool, getter, chain)
	if err != nil {
		return IntegrityReport{}, err
	}
	rep := IntegrityReport{
		Chain: chain, Observed: observed, Incident: incident,
		IntegrityStatus: verification.IntegrityStatus, PrefixStatus: verification.PrefixStatus,
		ChainVerification: verification, Anchors: anchors, AsOf: time.Now().UTC(),
	}
	for _, a := range anchors {
		switch a.Disposition {
		case AnchorVerified:
			rep.AnchorsVerified++
		case AnchorVerifiedLegacy:
			rep.AnchorsLegacy++
		case AnchorSealedUnrecoverable:
			rep.AnchorsSealed++
		default:
			rep.AnchorsUnexplained++
		}
	}
	rep.AllAnchorsAccounted = rep.AnchorsUnexplained == 0
	rep.TruthStatement = integrityStatement(rep)
	return rep, nil
}

func integrityStatement(r IntegrityReport) string {
	verdict := map[bool]string{true: "通过", false: "失败"}[r.ChainVerification.Valid]
	if r.IntegrityStatus == IntegrityIntact {
		return fmt.Sprintf("链 %s 无缺口：seq %d..%d 全部逐行复算%s（checked=%d），锚点 %d 项全部验证通过。",
			r.Chain, r.ChainVerification.VerifiedFromSeq, r.ChainVerification.ToSeq, verdict,
			r.ChainVerification.Checked, r.AnchorsVerified)
	}
	if r.Incident == nil {
		return fmt.Sprintf("链 %s 存在未封存缺口（观测最小 seq=%d）：integrity_status=%s，必须封存完整性边界后才能给出可信结论。",
			r.Chain, r.Observed.FirstSurvivingSeq, r.IntegrityStatus)
	}
	inc := r.Incident
	return fmt.Sprintf("链 %s 存在不可恢复缺口 seq %d..%d（%d 条事件，封存事件 %s）：旧链在该处不连续，且不允许伪造或回填。可验证区间为 seq %d..%d，逐行复算%s（checked=%d）；锚点 %d 项已验证、%d 项因落在封存缺口内标记为不可恢复（锚点与归档对象原样保留）。边界之后的链与每日锚点仍逐项可信。",
		r.Chain, inc.GapFromSeq, inc.GapToSeq, inc.GapEventCount, inc.IncidentRef,
		r.ChainVerification.VerifiedFromSeq, r.ChainVerification.ToSeq, verdict,
		r.ChainVerification.Checked, r.AnchorsVerified, r.AnchorsSealed)
}
