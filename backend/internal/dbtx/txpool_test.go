package dbtx

// P3 seam tests. The seam is the whole reason a gated request is all-or-nothing
// and the whole reason a scope mutation no longer queues behind a handler, so
// every routing decision it makes is asserted here rather than assumed:
//
//   * a request transaction in the context takes Exec / Query / QueryRow /
//     SendBatch / CopyFrom / Begin;
//   * Begin inside a request transaction is a SAVEPOINT on the same connection,
//     never a second pooled connection;
//   * BeginTx with non-default options inside a request transaction fails closed,
//     because transaction options cannot be changed inside a running transaction
//     and silently downgrading them would change the isolation the caller asked
//     for;
//   * Acquire fails closed inside a request transaction, because a second
//     connection would commit outside the gate transaction;
//   * WithoutTx really removes the transaction (an audit write must survive the
//     rollback of the business write it records) while preserving every other
//     context value;
//   * a pool with no underlying pool fails closed with ErrNoPool instead of
//     panicking, and New(nil) stays nil so "no database configured" keeps its
//     existing meaning.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeTx struct {
	calls   []string
	lastSQL string
	child   pgx.Tx
}

func (f *fakeTx) note(what, sql string) {
	f.calls = append(f.calls, what)
	f.lastSQL = sql
}

func (f *fakeTx) Begin(ctx context.Context) (pgx.Tx, error) {
	f.note("Begin", "")
	if f.child != nil {
		return f.child, nil
	}
	return &fakeTx{}, nil
}

func (f *fakeTx) Commit(ctx context.Context) error   { f.note("Commit", ""); return nil }
func (f *fakeTx) Rollback(ctx context.Context) error { f.note("Rollback", ""); return nil }

func (f *fakeTx) CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error) {
	f.note("CopyFrom", table.Sanitize())
	return 7, nil
}

func (f *fakeTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	f.note("SendBatch", "")
	return fakeBatch{}
}

func (f *fakeTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }

func (f *fakeTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	f.note("Prepare", sql)
	return &pgconn.StatementDescription{Name: name, SQL: sql}, nil
}

func (f *fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.note("Exec", sql)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (f *fakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.note("Query", sql)
	return nil, errFakeRows
}

func (f *fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	f.note("QueryRow", sql)
	return fakeRow{}
}

func (f *fakeTx) Conn() *pgx.Conn { return nil }

var errFakeRows = errors.New("fake rows")

type fakeRow struct{}

func (fakeRow) Scan(dest ...any) error { return nil }

type fakeBatch struct{}

func (fakeBatch) Exec() (pgconn.CommandTag, error) { return pgconn.NewCommandTag("SELECT 1"), nil }
func (fakeBatch) Query() (pgx.Rows, error)         { return nil, errFakeRows }
func (fakeBatch) QueryRow() pgx.Row                { return fakeRow{} }
func (fakeBatch) Close() error                     { return nil }

func TestContextRoundTrip(t *testing.T) {
	if got := FromContext(nil); got != nil {
		t.Fatalf("FromContext(nil) = %v, want nil", got)
	}
	if got := FromContext(context.Background()); got != nil {
		t.Fatalf("FromContext(background) = %v, want nil", got)
	}
	if got := WithTx(context.Background(), nil); FromContext(got) != nil {
		t.Fatalf("WithTx(ctx, nil) installed a transaction")
	}
	tx := &fakeTx{}
	ctx := WithTx(context.Background(), tx)
	if got := FromContext(ctx); got != tx {
		t.Fatalf("FromContext(WithTx(ctx, tx)) = %v, want the transaction", got)
	}
}

func TestWithoutTxRemovesOnlyTheTransaction(t *testing.T) {
	type otherKey struct{}
	tx := &fakeTx{}
	ctx := WithTx(context.WithValue(context.Background(), otherKey{}, "kept"), tx)
	stripped := WithoutTx(ctx)
	if FromContext(stripped) != nil {
		t.Fatalf("WithoutTx kept the transaction")
	}
	if v, _ := stripped.Value(otherKey{}).(string); v != "kept" {
		t.Fatalf("WithoutTx dropped an unrelated context value (%q)", v)
	}
	if FromContext(ctx) != tx {
		t.Fatalf("WithoutTx mutated the original context")
	}
	if got := WithoutTx(context.Background()); got == nil {
		t.Fatalf("WithoutTx(background) = nil")
	}
}

// TestGateMarkerIsSetAndStrippedWithTheTransaction 断言 gate 标记的生命周期。
//
// 这个标记决定 audit.Writer 是否加入请求事务，加入与否是"审计行随业务回滚消失"和
// "请求与自己的审计写入互等 60 秒"之间的取舍，所以它不能靠约定维护：
//
//   - 只有 WithGate 过的上下文才算 gate 请求；
//   - WithoutTx 必须同时清掉标记，否则 emitAudit 剥离事务之后还会被判定为
//     gate 请求，从而把一个本该独立落库的拒绝行塞进要被回滚的事务里。
func TestGateMarkerIsSetAndStrippedWithTheTransaction(t *testing.T) {
	if IsGateRequest(context.Background()) {
		t.Fatalf("a plain context is not a gate request")
	}
	if IsGateRequest(nil) {
		t.Fatalf("IsGateRequest(nil) must be false")
	}
	if got := WithGate(nil, "req-1"); got != nil {
		t.Fatalf("WithGate(nil, ...) = %v, want nil", got)
	}
	// 请求 id 缺失也必须保持 gate 语义：标记一旦静默失效，D-15 的自死锁会原样回来。
	if !IsGateRequest(WithGate(context.Background(), "")) {
		t.Fatalf("a gate request without a request id must still join the request transaction")
	}

	type otherKey struct{}
	tx := &fakeTx{}
	base := context.WithValue(context.Background(), otherKey{}, "kept")

	gated := WithGate(WithTx(base, tx), "req-1")
	if !IsGateRequest(gated) {
		t.Fatalf("WithGate did not mark the context")
	}
	if FromContext(gated) != tx {
		t.Fatalf("WithGate dropped the request transaction")
	}
	if IsGateRequest(base) {
		t.Fatalf("WithGate mutated the original context")
	}

	stripped := WithoutTx(gated)
	if FromContext(stripped) != nil {
		t.Fatalf("WithoutTx kept the transaction")
	}
	if IsGateRequest(stripped) {
		t.Fatalf("WithoutTx kept the gate marker: a detached audit write would still join the request transaction")
	}
	if v, _ := stripped.Value(otherKey{}).(string); v != "kept" {
		t.Fatalf("WithoutTx dropped an unrelated context value (%q)", v)
	}
	if !IsGateRequest(gated) {
		t.Fatalf("WithoutTx mutated the original context")
	}
}

func TestExecQueryQueryRowRouteToTransaction(t *testing.T) {
	tx := &fakeTx{}
	ctx := WithTx(context.Background(), tx)
	p := &Pool{}
	if _, err := p.Exec(ctx, "UPDATE cases SET name=$1", "x"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if _, err := p.Query(ctx, "SELECT 1"); !errors.Is(err, errFakeRows) {
		t.Fatalf("Query did not reach the transaction: %v", err)
	}
	if err := p.QueryRow(ctx, "SELECT 2").Scan(); err != nil {
		t.Fatalf("QueryRow did not reach the transaction: %v", err)
	}
	p.SendBatch(ctx, &pgx.Batch{})
	if _, err := p.CopyFrom(ctx, pgx.Identifier{"cases"}, []string{"id"}, nil); err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	want := []string{"Exec", "Query", "QueryRow", "SendBatch", "CopyFrom"}
	if len(tx.calls) != len(want) {
		t.Fatalf("transaction calls = %v, want %v", tx.calls, want)
	}
	for i := range want {
		if tx.calls[i] != want[i] {
			t.Fatalf("transaction calls = %v, want %v", tx.calls, want)
		}
	}
	// pgx.Identifier.Sanitize quotes the identifier, so the transaction sees
	// "cases" and not cases. Asserting the sanitized form keeps this test honest
	// about what a real transaction would receive.
	wantTable := pgx.Identifier{"cases"}.Sanitize()
	if tx.lastSQL != wantTable {
		t.Fatalf("CopyFrom table = %q, want %q", tx.lastSQL, wantTable)
	}
}

func TestBeginInsideTransactionIsASavepoint(t *testing.T) {
	child := &fakeTx{}
	tx := &fakeTx{child: child}
	ctx := WithTx(context.Background(), tx)
	p := &Pool{}
	got, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got != child {
		t.Fatalf("Begin returned %v, want the savepoint of the request transaction", got)
	}
	if len(tx.calls) != 1 || tx.calls[0] != "Begin" {
		t.Fatalf("Begin calls = %v, want one Begin on the request transaction", tx.calls)
	}
}

func TestBeginTxInsideTransactionFailsClosedOnNonDefaultOptions(t *testing.T) {
	tx := &fakeTx{}
	ctx := WithTx(context.Background(), tx)
	p := &Pool{}
	if _, err := p.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}); !errors.Is(err, ErrConnectionNotAllowedInsideTx) {
		t.Fatalf("BeginTx(serializable) = %v, want ErrConnectionNotAllowedInsideTx", err)
	}
	if len(tx.calls) != 0 {
		t.Fatalf("BeginTx downgraded the options and opened a savepoint anyway: %v", tx.calls)
	}
	child := &fakeTx{}
	tx.child = child
	got, err := p.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx(default): %v", err)
	}
	if got != child {
		t.Fatalf("BeginTx(default) = %v, want the savepoint", got)
	}
}

func TestAcquireFailsClosedInsideTransaction(t *testing.T) {
	tx := &fakeTx{}
	ctx := WithTx(context.Background(), tx)
	p := &Pool{}
	if _, err := p.Acquire(ctx); !errors.Is(err, ErrConnectionNotAllowedInsideTx) {
		t.Fatalf("Acquire inside a request transaction = %v, want ErrConnectionNotAllowedInsideTx", err)
	}
}

func TestMissingPoolFailsClosed(t *testing.T) {
	var nilPool *Pool
	ctx := context.Background()
	if _, err := nilPool.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrNoPool) {
		t.Fatalf("nil Exec = %v, want ErrNoPool", err)
	}
	if _, err := nilPool.Query(ctx, "SELECT 1"); !errors.Is(err, ErrNoPool) {
		t.Fatalf("nil Query = %v, want ErrNoPool", err)
	}
	if err := nilPool.QueryRow(ctx, "SELECT 1").Scan(); !errors.Is(err, ErrNoPool) {
		t.Fatalf("nil QueryRow = %v, want ErrNoPool", err)
	}
	if _, err := nilPool.Begin(ctx); !errors.Is(err, ErrNoPool) {
		t.Fatalf("nil Begin = %v, want ErrNoPool", err)
	}
	if _, err := nilPool.BeginTx(ctx, pgx.TxOptions{}); !errors.Is(err, ErrNoPool) {
		t.Fatalf("nil BeginTx = %v, want ErrNoPool", err)
	}
	if _, err := nilPool.Acquire(ctx); !errors.Is(err, ErrNoPool) {
		t.Fatalf("nil Acquire = %v, want ErrNoPool", err)
	}
	if _, err := nilPool.CopyFrom(ctx, pgx.Identifier{"cases"}, nil, nil); !errors.Is(err, ErrNoPool) {
		t.Fatalf("nil CopyFrom = %v, want ErrNoPool", err)
	}
	if b := nilPool.SendBatch(ctx, &pgx.Batch{}); b == nil {
		t.Fatalf("nil SendBatch returned nil instead of a failing result")
	} else if _, err := b.Exec(); !errors.Is(err, ErrNoPool) {
		t.Fatalf("nil SendBatch Exec = %v, want ErrNoPool", err)
	}
	if nilPool.Raw() != nil {
		t.Fatalf("nil Raw() returned a pool")
	}
	if New(nil) != nil {
		t.Fatalf("New(nil) must stay nil so a store without a database keeps its meaning")
	}
}

func TestContextTransactionWinsOverThePool(t *testing.T) {
	// The request transaction must be used even when the wrapper DOES have a
	// pool: that is the entire point of the seam. A wrapper with no pool but a
	// context transaction must still work, which is exactly what this asserts.
	tx := &fakeTx{}
	ctx := WithTx(context.Background(), tx)
	p := &Pool{}
	if _, err := p.Exec(ctx, "UPDATE sessions SET revoked_at=now()"); err != nil {
		t.Fatalf("Exec with a context transaction and no pool: %v", err)
	}
	if len(tx.calls) != 1 || tx.calls[0] != "Exec" {
		t.Fatalf("calls = %v, want one Exec on the request transaction", tx.calls)
	}
}
