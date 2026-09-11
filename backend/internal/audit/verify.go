package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// VerifyChainResult 是 §27 AUDIT-004 的校验输出。
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

// VerifyChain 从头重算指定区间的哈希链。校验同时覆盖两种情况：
//  1. 事件载荷（由权威列重建）被改写；
//  2. 链被插入、删除或乱序。
func VerifyChain(ctx context.Context, pool *pgxpool.Pool, chain string, fromSeq, toSeq int64) (VerifyChainResult, error) {
	reader, ok := chainReaders[chain]
	if !ok {
		return VerifyChainResult{}, fmt.Errorf("unknown chain %q", chain)
	}
	res := VerifyChainResult{Chain: chain, Valid: true, Errors: []string{}}
	if fromSeq <= 0 {
		fromSeq = 1
	}
	res.FromSeq = fromSeq
	res.ToSeq = toSeq

	query := reader.query
	args := []any{chain, fromSeq}
	if toSeq > 0 {
		query += " AND seq <= $3"
		args = append(args, toSeq)
	}
	query += " ORDER BY seq"

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	prevHash := ""
	if fromSeq > 1 {
		if err := pool.QueryRow(ctx,
			fmt.Sprintf(`SELECT event_hash FROM %s WHERE chain = $1 AND seq = $2`, reader.table),
			chain, fromSeq-1).Scan(&prevHash); err != nil {
			return res, fmt.Errorf("read predecessor hash: %w", err)
		}
	}

	expectedSeq := fromSeq
	for rows.Next() {
		seq, rowPrev, rowHash, payload, err := reader.build(rows)
		if err != nil {
			return res, err
		}
		if seq != expectedSeq {
			return res.broken(seq, "sequence_gap",
				fmt.Sprintf("expected seq %d, found %d", expectedSeq, seq)), nil
		}
		if rowPrev != prevHash {
			return res.broken(seq, "prev_hash_mismatch",
				fmt.Sprintf("seq %d: prev_hash 与上一条 event_hash 不一致", seq)), nil
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
	res.ToSeq = expectedSeq - 1
	if res.Checked == 0 {
		res.Reason = "empty_range"
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
