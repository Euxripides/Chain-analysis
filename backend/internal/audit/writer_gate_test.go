package audit

// D-15 回归：commit gate 请求内的独立审计写入必须加入请求事务。
//
// 事实（2026-09-16 实测，POST /api/v2/evidence/{id}/commands/verify）：
// 同一请求里，业务侧的 auditTx 已经把审计行写进 gate 事务并因此持有
// audit_chain_heads 的行锁；随后 GetEvidence 走 audit.Writer.Write（独立连接）
// 再取同一把锁——请求要等这条审计写完才返回，gate 事务却要等 handler 返回才提交。
// 结果：请求卡 60.0s（HTTP 超时把它解开），evidence:sensitive:read 整条丢失。
//
// 本文件用两个断言把修复钉住：
//  1. gate 请求内：Write 用上下文里的事务，绝不开第二条连接；
//  2. 非 gate 请求（含 WithoutTx 之后的上下文）：Write 仍然走独立事务，拒绝行
//     不会随业务回滚消失。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
)

// unreachablePool 指向一个必然连不上的地址：任何真正使用它的调用都会失败。
// 因此"Write 返回 nil 且事务收到语句"就等价于"没有开第二条连接"。
func unreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(),
		"postgres://127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("build unreachable pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// stubTx 记录落到这条事务上的语句，ChainAppend 的链头读取返回固定的 (7, prev)。
type stubTx struct {
	execs []string
}

func (s *stubTx) Begin(context.Context) (pgx.Tx, error) { return s, nil }
func (s *stubTx) Commit(context.Context) error          { return nil }
func (s *stubTx) Rollback(context.Context) error        { return nil }
func (s *stubTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("stubTx: CopyFrom is not expected")
}
func (s *stubTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (s *stubTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (s *stubTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("stubTx: Prepare is not expected")
}
func (s *stubTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	s.execs = append(s.execs, sql)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (s *stubTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("stubTx: Query is not expected")
}
func (s *stubTx) QueryRow(context.Context, string, ...any) pgx.Row { return stubRow{} }
func (s *stubTx) Conn() *pgx.Conn                                  { return nil }

func (s *stubTx) insertedAuditRow() bool {
	for _, sql := range s.execs {
		if strings.Contains(sql, "INSERT INTO audit_events_v2") {
			return true
		}
	}
	return false
}

type stubRow struct{}

func (stubRow) Scan(dest ...any) error {
	for _, d := range dest {
		switch v := d.(type) {
		case *int64:
			*v = 7
		case *string:
			*v = "0000000000000000000000000000000000000000000000000000000000000000"
		default:
			return errors.New("stubRow: unexpected scan destination")
		}
	}
	return nil
}

func testEvent() Event {
	return Event{
		Environment: "test", ActorType: "user", ActorID: "11111111-1111-1111-1111-111111111111",
		ActorEmail: "reviewer@example.test", Action: "evidence:sensitive:read",
		Category: "evidence", ResourceType: "evidence", ResourceID: "22222222-2222-2222-2222-222222222222",
		Result: "success", RiskLevel: "critical", OccurredAt: time.Unix(0, 0).UTC(),
	}
}

func TestWriteJoinsGateRequestTransaction(t *testing.T) {
	w := NewWriter(unreachablePool(t), "test", nil)
	tx := &stubTx{}
	ctx := dbtx.WithGate(dbtx.WithTx(context.Background(), tx), "req-d15")

	if err := w.Write(ctx, testEvent()); err != nil {
		t.Fatalf("Write inside a gate request must join the request transaction, got: %v", err)
	}
	if !tx.insertedAuditRow() {
		t.Fatalf("the audit row was not written on the request transaction")
	}
}

func TestWriteStaysIndependentOutsideGateRequests(t *testing.T) {
	cases := map[string]context.Context{}
	tx := &stubTx{}
	cases["no request transaction"] = context.Background()
	cases["transaction without the gate marker"] = dbtx.WithTx(context.Background(), tx)
	cases["detached (WithoutTx)"] = dbtx.WithoutTx(
		dbtx.WithGate(dbtx.WithTx(context.Background(), tx), "req-d15"))

	for name, ctx := range cases {
		t.Run(name, func(t *testing.T) {
			w := NewWriter(unreachablePool(t), "test", nil)
			tx := &stubTx{}
			if name == "transaction without the gate marker" {
				ctx = dbtx.WithTx(context.Background(), tx)
			}
			if err := w.Write(ctx, testEvent()); err == nil {
				t.Fatalf("Write must use its own transaction here; it silently used the context instead")
			}
			if tx.insertedAuditRow() {
				t.Fatalf("a deny/read audit row joined a transaction it must survive")
			}
		})
	}
}
