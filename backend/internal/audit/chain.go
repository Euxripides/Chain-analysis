// Package audit 实现 §6.5 / §6.6 的不可抵赖审计底座：
// 顺序哈希链、事务内写入和 Outbox 去重键。
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB 同时被 *pgxpool.Pool 和 pgx.Tx 满足，便于把审计写入同一业务事务。
type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ChainAudit / ChainLogin 是 audit_chain_heads 中预置的两条链。
const (
	ChainAudit = "audit"
	ChainLogin = "login"
)

// Canonicalize 生成稳定 JSON：对象键排序、去除无意义空白，保证同一逻辑载荷
// 在任何进程/版本下产生相同字节，否则哈希链无法校验。
func Canonicalize(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	var b strings.Builder
	if err := writeCanonical(&b, generic); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func writeCanonical(b *strings.Builder, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		b.WriteString(strconv.FormatFloat(t, 'f', -1, 64))
	case string:
		enc, err := json.Marshal(t)
		if err != nil {
			return err
		}
		b.Write(enc)
	case []any:
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			enc, err := json.Marshal(k)
			if err != nil {
				return err
			}
			b.Write(enc)
			b.WriteByte(':')
			if err := writeCanonical(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		enc, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b.Write(enc)
	}
	return nil
}

// HashEvent 计算链上事件哈希：sha256(chain|seq|prevHash|sha256(payload))。
// 把 seq 与 prevHash 一并纳入，任何插入、删除、乱序都会被检测到。
func HashEvent(chain string, seq int64, prevHash string, payload []byte) string {
	sum := sha256.Sum256(payload)
	h := sha256.New()
	h.Write([]byte(chain))
	h.Write([]byte("|"))
	h.Write([]byte(strconv.FormatInt(seq, 10)))
	h.Write([]byte("|"))
	h.Write([]byte(prevHash))
	h.Write([]byte("|"))
	h.Write([]byte(hex.EncodeToString(sum[:])))
	return hex.EncodeToString(h.Sum(nil))
}

// ChainAppend 在事务内追加一条链记录，返回 (seq, prevHash, eventHash)。
// 调用方必须把返回的哈希写进同一条被审计记录，并保证同一事务提交。
// 行锁（FOR UPDATE）保证并发下序号单调且不重不漏。
func ChainAppend(ctx context.Context, db DB, chain string, payload []byte) (int64, string, string, error) {
	var seq int64
	var prevHash string
	err := db.QueryRow(ctx,
		`SELECT last_seq, last_hash FROM audit_chain_heads WHERE chain = $1 FOR UPDATE`,
		chain).Scan(&seq, &prevHash)
	if err != nil {
		return 0, "", "", fmt.Errorf("lock audit chain %q: %w", chain, err)
	}
	seq++
	eventHash := HashEvent(chain, seq, prevHash, payload)
	if _, err := db.Exec(ctx,
		`UPDATE audit_chain_heads SET last_seq = $2, last_hash = $3, updated_at = now() WHERE chain = $1`,
		chain, seq, eventHash); err != nil {
		return 0, "", "", fmt.Errorf("advance audit chain %q: %w", chain, err)
	}
	return seq, prevHash, eventHash, nil
}

// ChainStatus 返回链头，用于后台审计校验页。
type ChainStatus struct {
	Chain    string `json:"chain"`
	LastSeq  int64  `json:"last_seq"`
	LastHash string `json:"last_hash"`
}

// ChainState 读取链头。
func ChainState(ctx context.Context, db DB, chain string) (ChainStatus, error) {
	st := ChainStatus{Chain: chain}
	err := db.QueryRow(ctx, `SELECT last_seq, last_hash FROM audit_chain_heads WHERE chain = $1`, chain).
		Scan(&st.LastSeq, &st.LastHash)
	return st, err
}
